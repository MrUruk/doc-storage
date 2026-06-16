// Package service implements the storage.v1.StorageService Connect handler,
// orchestrating the same pipeline as the Python Service: stream -> pandoc ->
// clean -> agentic chunk (eino) -> embed -> Qdrant + Postgres + S3.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/cloudwego/eino/components/model"

	cleaner "github.com/mruruk/doc-storage/cleaner-go"
	pandocv1 "github.com/mruruk/doc-storage/storage-go/gen/pandoc/v1"
	"github.com/mruruk/doc-storage/storage-go/gen/pandoc/v1/pandocv1connect"
	storagev1 "github.com/mruruk/doc-storage/storage-go/gen/storage/v1"
	"github.com/mruruk/doc-storage/storage-go/internal/blob"
	"github.com/mruruk/doc-storage/storage-go/internal/chunk"
	"github.com/mruruk/doc-storage/storage-go/internal/chunker"
	"github.com/mruruk/doc-storage/storage-go/internal/docxprops"
	"github.com/mruruk/doc-storage/storage-go/internal/embedding"
	"github.com/mruruk/doc-storage/storage-go/internal/sqldb"
	"github.com/mruruk/doc-storage/storage-go/internal/vectordb"
)

const outputChunksDir = "output_chunks"

// Deps are the collaborators the service needs.
type Deps struct {
	Pandoc       pandocv1connect.PandocServiceClient
	VectorDB     *vectordb.Client
	SQLDB        *sqldb.DB
	Uploader     *blob.Uploader
	ChatModel    model.ToolCallingChatModel
	HTTPClient   *http.Client
	EmbeddingURL string
	MaxLength    int
}

type Service struct {
	d Deps
}

func New(d Deps) *Service { return &Service{d: d} }

func (s *Service) AgenticSaveFile(
	ctx context.Context,
	stream *connect.ClientStream[storagev1.SaveFileRequest],
) (*connect.Response[storagev1.SaveFileResponse], error) {
	var (
		fileName, project, docType string
		fileBytes                  []byte
	)
	for stream.Receive() {
		part := stream.Msg()
		if fileName == "" {
			fileName = part.GetFileName()
			project = part.GetProject()
			docType = part.GetDocType()
		}
		fileBytes = append(fileBytes, part.GetPart()...)
	}
	if err := stream.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("receive stream: %w", err))
	}

	if len(fileBytes) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("received empty file stream"))
	}
	if project == "" || docType == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("project and doc_type are required"))
	}
	if !strings.HasSuffix(strings.ToLower(fileName), ".docx") {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unsupported file type. Expected .docx"))
	}

	historicalDate := time.Now().UTC()
	if d, err := docxprops.ExtractModifiedDate(fileBytes); err != nil {
		slog.Warn("failed to extract modified date from docx, using current time", "err", err)
	} else {
		historicalDate = d
	}

	// 1. Convert DOCX -> HTML via the Pandoc service.
	slog.Info("converting via pandoc", "file", fileName)
	pandocResp, err := s.d.Pandoc.ConvertDocxToHtml(ctx, connect.NewRequest(&pandocv1.ConvertDocxToHtmlRequest{
		DocxBytes:    fileBytes,
		ExtractMedia: false,
	}))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("pandoc conversion failed: %w", err))
	}
	htmlContent := pandocResp.Msg.GetHtmlText()

	// 2. Clean HTML to reduce token footprint.
	compressedHTML := cleaner.ReduceHTMLToken(htmlContent)
	slog.Info("html cleaned", "before", len(htmlContent), "after", len(compressedHTML))
	writeDebugHTML(fileName, compressedHTML)

	// 3. Agentic chunking via the eino ReAct agent.
	slog.Info("calling chunking agent")
	chunks, err := chunker.Chunk(ctx, s.d.ChatModel, compressedHTML)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("agentic chunking failed: %w", err))
	}
	if len(chunks) == 0 {
		slog.Warn("agentic chunking produced no chunks")
		return connect.NewResponse(&storagev1.SaveFileResponse{}), nil
	}
	slog.Info("got chunks", "count", len(chunks))
	saveChunksToDisk(fileName, chunks)

	// 4. Embed chunk texts.
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.TextToEmbed
	}
	embeddings, err := embedding.EncodeTexts(ctx, s.d.HTTPClient, s.d.EmbeddingURL, texts, s.d.MaxLength, false)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("embedding generation failed: %w", err))
	}

	// 5. Persist vectors to Qdrant.
	if s.d.VectorDB != nil {
		if err := s.d.VectorDB.SaveEmbeddings(ctx, fileName, chunks, embeddings); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database saving failed: %w", err))
		}
	}

	// 6. Persist document metadata to Postgres (+ best-effort S3 upload).
	if s.d.SQLDB != nil {
		title := fileName
		if i := strings.LastIndex(fileName, "."); i > 0 {
			title = fileName[:i]
		}
		documentID, err := s.d.SQLDB.UpsertDocument(ctx, project, docType, title)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("document metadata save failed: %w", err))
		}

		docxBlobRef := ""
		if s.d.Uploader != nil {
			if ref, err := s.d.Uploader.UploadDocx(ctx, fileBytes, project, docType); err != nil {
				slog.Warn("s3 upload failed (non-fatal)", "err", err)
			} else {
				docxBlobRef = ref
			}
		}

		if _, err := s.d.SQLDB.InsertVersion(ctx, documentID, compressedHTML, docxBlobRef, historicalDate); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("document metadata save failed: %w", err))
		}
		slog.Info("document saved", "project", project, "doc_type", docType, "doc_id", documentID, "date", historicalDate)
	}

	return connect.NewResponse(&storagev1.SaveFileResponse{}), nil
}

func (s *Service) AgenticSearch(
	ctx context.Context,
	req *connect.Request[storagev1.SearchRequest],
) (*connect.Response[storagev1.SearchResponse], error) {
	query := strings.TrimSpace(req.Msg.GetQuery())
	if query == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("search query is empty"))
	}
	if s.d.EmbeddingURL == "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("embedding service is not initialized"))
	}
	if s.d.VectorDB == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("database is not initialized"))
	}

	limit := 10
	if l := int(req.Msg.GetLimit()); l > 0 {
		limit = min(l, 100)
	}

	vectors, err := embedding.EncodeTexts(ctx, s.d.HTTPClient, s.d.EmbeddingURL, []string{query}, s.d.MaxLength, true)
	if err != nil || len(vectors) == 0 {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to encode query: %w", err))
	}

	hits, err := s.d.VectorDB.Search(ctx, vectors[0], limit)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database search failed: %w", err))
	}

	results := make([]*storagev1.SearchResult, 0, len(hits))
	for _, h := range hits {
		results = append(results, &storagev1.SearchResult{
			DocumentName: h.DocumentName,
			HtmlContent:  h.HTMLContent,
			Header:       h.Header,
			Score:        h.Score,
		})
	}
	return connect.NewResponse(&storagev1.SearchResponse{Results: results}), nil
}

// --- debug artifacts (mirror the Python on-disk output) ---

func writeDebugHTML(fileName, html string) {
	if err := os.MkdirAll(outputChunksDir, 0o755); err != nil {
		return
	}
	path := filepath.Join(outputChunksDir, "_debug_"+fileName+".html")
	if err := os.WriteFile(path, []byte(html), 0o644); err != nil {
		slog.Warn("failed to write debug html", "err", err)
	}
}

func saveChunksToDisk(fileName string, chunks []chunk.Chunk) {
	ts := time.Now().UTC().Format("20060102_150405")
	dir := filepath.Join(outputChunksDir, fmt.Sprintf("%s_%s", fileName, ts))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Warn("failed to create chunk dir", "err", err)
		return
	}
	for i, c := range chunks {
		path := filepath.Join(dir, fmt.Sprintf("chunk_%03d.md", i))
		_ = os.WriteFile(path, []byte(c.TextToEmbed), 0o644)
	}
	var idx strings.Builder
	fmt.Fprintf(&idx, "Document: %s\nChunks: %d\nTimestamp: %s\n\n", fileName, len(chunks), ts)
	for i, c := range chunks {
		fmt.Fprintf(&idx, "[%03d] header=%s\n", i, c.Header)
	}
	_ = os.WriteFile(filepath.Join(dir, "_index.txt"), []byte(idx.String()), 0o644)
	slog.Info("saved chunks to disk", "count", len(chunks), "dir", dir)
}
