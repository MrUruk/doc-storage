// Command server runs the storage.v1.StorageService over Connect-RPC.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"connectrpc.com/connect"
	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/mruruk/doc-storage/storage-go/gen/pandoc/v1/pandocv1connect"
	"github.com/mruruk/doc-storage/storage-go/gen/storage/v1/storagev1connect"
	"github.com/mruruk/doc-storage/storage-go/internal/blob"
	"github.com/mruruk/doc-storage/storage-go/internal/config"
	"github.com/mruruk/doc-storage/storage-go/internal/service"
	"github.com/mruruk/doc-storage/storage-go/internal/sqldb"
	"github.com/mruruk/doc-storage/storage-go/internal/vectordb"
)

func main() {
	cfg := config.Load()
	initLogging(cfg.LogLevel)

	if err := run(cfg); err != nil {
		slog.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

func run(cfg *config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	httpClient := &http.Client{Timeout: 5 * time.Minute}

	// PostgreSQL.
	sql := sqldb.New()
	if err := sql.Connect(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	defer sql.Close()
	if err := sql.EnsureTables(ctx); err != nil {
		return err
	}
	slog.Info("postgres ready", "url", cfg.DatabaseURL)

	// Qdrant.
	vdb := vectordb.New(httpClient, cfg.QdrantHost, cfg.QdrantPort, cfg.QdrantCollection)
	if err := vdb.Connect(ctx); err != nil {
		return err
	}
	slog.Info("qdrant ready", "collection", cfg.QdrantCollection)

	// S3 uploader.
	uploader := blob.NewUploader(cfg.S3URL, cfg.S3Bucket, cfg.S3AccessKey, cfg.S3SecretKey)

	// Pandoc Connect client.
	pandoc := pandocv1connect.NewPandocServiceClient(httpClient, cfg.PandocServiceURL)
	slog.Info("pandoc client ready", "url", cfg.PandocServiceURL)

	// eino chat model (OpenAI-compatible endpoint).
	temperature := float32(0.1)
	chatModel, err := einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
		BaseURL:     cfg.ModelURL + "/v1",
		APIKey:      "not-needed",
		Model:       cfg.ModelName,
		Temperature: &temperature,
	})
	if err != nil {
		return err
	}
	slog.Info("eino chat model ready", "model", cfg.ModelName, "url", cfg.ModelURL)

	svc := service.New(service.Deps{
		Pandoc:       pandoc,
		VectorDB:     vdb,
		SQLDB:        sql,
		Uploader:     uploader,
		ChatModel:    chatModel,
		HTTPClient:   httpClient,
		EmbeddingURL: cfg.EmbeddingServiceURL,
		MaxLength:    cfg.MaxLength,
	})

	mux := http.NewServeMux()
	path, handler := storagev1connect.NewStorageServiceHandler(
		svc,
		connect.WithReadMaxBytes(256<<20), // allow large streamed uploads
	)
	mux.Handle(path, handler)

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	srv := &http.Server{
		Addr:    addr,
		Handler: h2c.NewHandler(mux, &http2.Server{}), // HTTP/2 cleartext for gRPC/Connect streaming
	}

	go func() {
		slog.Info("listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("listen error", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func initLogging(level string) {
	lvl := slog.LevelInfo
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "error":
		lvl = slog.LevelError
	case "critical":
		lvl = slog.LevelError
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})))
}
