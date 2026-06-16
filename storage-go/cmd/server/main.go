// Command server runs the storage.v1.StorageService over Connect-RPC.
package main

import (
	"context"
	"errors"
	"fmt"
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
	config.InitEnv()
	initLogging()
	serve()
}

// serve wires up every collaborator and blocks until shutdown. Startup is
// fail-fast through must: a missing required env var or an unavailable
// dependency panics, since the process cannot do its job without it. Panicking
// (rather than os.Exit) unwinds the stack so the deferred resource closers
// below still run, and emits a stack trace pointing at the failed step.
func serve() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	httpClient := &http.Client{Timeout: 5 * time.Minute}

	// PostgreSQL.
	databaseURL, err := requireEnv("DATABASE_URL")
	must("env DATABASE_URL", err)
	sql := sqldb.New()
	must("connect postgres", sql.Connect(ctx, databaseURL))
	defer sql.Close()
	must("ensure tables", sql.EnsureTables(ctx))
	slog.Info("postgres ready", "url", databaseURL)

	// Qdrant.
	qdrantHost, err := requireEnv("QDRANT_HOST")
	must("env QDRANT_HOST", err)
	qdrantCollection, err := requireEnv("QDRANT_COLLECTION")
	must("env QDRANT_COLLECTION", err)
	vdb := vectordb.New(httpClient, qdrantHost, getEnvInt("QDRANT_PORT", 6333), qdrantCollection)
	must("connect qdrant", vdb.Connect(ctx))
	slog.Info("qdrant ready", "collection", qdrantCollection)

	// S3 uploader (client is lazy; no connection established here).
	s3URL, err := requireEnv("S3_URL")
	must("env S3_URL", err)
	s3Bucket, err := requireEnv("S3_BUCKET")
	must("env S3_BUCKET", err)
	s3Access, err := requireEnv("RUSTFS_ACCESS_KEY")
	must("env RUSTFS_ACCESS_KEY", err)
	s3Secret, err := requireEnv("RUSTFS_SECRET_KEY")
	must("env RUSTFS_SECRET_KEY", err)
	uploader := blob.NewUploader(s3URL, s3Bucket, s3Access, s3Secret)

	// Pandoc Connect client (lazy; reached on first request).
	pandocURL, err := requireEnv("PANDOC_SERVICE_URL")
	must("env PANDOC_SERVICE_URL", err)
	pandoc := pandocv1connect.NewPandocServiceClient(httpClient, pandocURL)
	slog.Info("pandoc client ready", "url", pandocURL)

	// eino chat model (OpenAI-compatible endpoint).
	modelName, err := requireEnv("MODEL_NAME")
	must("env MODEL_NAME", err)
	modelURL, err := requireEnv("MODEL_URL")
	must("env MODEL_URL", err)
	temperature := float32(0.1)
	chatModel, err := einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
		BaseURL:     modelURL + "/v1",
		APIKey:      "not-needed",
		Model:       modelName,
		Temperature: &temperature,
	})
	must("init chat model", err)
	slog.Info("eino chat model ready", "model", modelName, "url", modelURL)

	embeddingURL, err := requireEnv("EMBEDDING_SERVICE_URL")
	must("env EMBEDDING_SERVICE_URL", err)

	svc := service.New(service.Deps{
		Pandoc:       pandoc,
		VectorDB:     vdb,
		SQLDB:        sql,
		Uploader:     uploader,
		ChatModel:    chatModel,
		HTTPClient:   httpClient,
		EmbeddingURL: embeddingURL,
		MaxLength:    getEnvInt("MAX_LENGTH", 1000),
	})

	mux := http.NewServeMux()
	path, handler := storagev1connect.NewStorageServiceHandler(
		svc,
		connect.WithReadMaxBytes(256<<20), // allow large streamed uploads
	)
	mux.Handle(path, handler)

	addr := net.JoinHostPort(getEnv("HOST", "localhost"), strconv.Itoa(getEnvInt("PORT", 8080)))
	srv := &http.Server{
		Addr:    addr,
		Handler: h2c.NewHandler(mux, &http2.Server{}), // HTTP/2 cleartext for gRPC/Connect streaming
	}

	// Surface a fatal listen error from the goroutine to the main goroutine, so
	// the panic unwinds through serve's defers instead of bypassing them.
	listenErr := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		slog.Info("shutting down")
	case err := <-listenErr:
		must("listen", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "err", err)
	}
}

// must aborts startup if err is non-nil. It is the single fail-fast mechanism.
func must(step string, err error) {
	if err != nil {
		panic(fmt.Errorf("startup: %s: %w", step, err))
	}
}

// requireEnv returns the value for key, or an error when it is unset/empty, so
// the caller can route it through must.
func requireEnv(key string) (string, error) {
	if v := os.Getenv(key); v != "" {
		return v, nil
	}
	return "", errors.New("not provided")
}

// getEnv returns the value for key, or fallback when unset/empty.
func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// getEnvInt returns the integer value for key, or fallback when unset/invalid.
func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func initLogging() {
	lvl := slog.LevelInfo
	switch getEnv("LOG_LEVEL", "info") {
	case "debug":
		lvl = slog.LevelDebug
	case "error", "critical":
		lvl = slog.LevelError
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})))
}
