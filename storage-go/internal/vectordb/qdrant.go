// Package vectordb is a small Qdrant REST client covering the operations the
// storage service needs: collection bootstrap, point upsert, and dense-vector
// search. It mirrors the Python db.DB (qdrant-client) behaviour.
package vectordb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/mruruk/doc-storage/storage-go/internal/chunk"
)

const (
	denseVector  = "dense-vector"
	sparseVector = "sparse-vector"
	vectorSize   = 1024
)

var oldCollections = []string{
	"frida_html_collection",
	"frida_html_agentic_collection",
	"qwen3_html_collection",
}

type Client struct {
	baseURL    string
	collection string
	http       *http.Client
}

// Hit is a single search result.
type Hit struct {
	ID           string
	Score        float32
	DocumentName string
	HTMLContent  string
	Header       string
}

func New(httpClient *http.Client, host, port, collection string) *Client {
	return &Client{
		baseURL:    fmt.Sprintf("http://%s:%s", host, port),
		collection: collection,
		http:       httpClient,
	}
}

// Connect drops superseded collections and ensures the active one exists.
func (c *Client) Connect(ctx context.Context) error {
	c.dropOldCollections(ctx)
	return c.ensureCollection(ctx)
}

func (c *Client) dropOldCollections(ctx context.Context) {
	for _, name := range oldCollections {
		if name == c.collection {
			continue
		}
		exists, err := c.collectionExists(ctx, name)
		if err != nil || !exists {
			continue
		}
		if err := c.do(ctx, http.MethodDelete, "/collections/"+name, nil, nil); err != nil {
			slog.Warn("failed to drop old collection", "name", name, "err", err)
			continue
		}
		slog.Info("dropped old collection", "name", name)
	}
}

func (c *Client) ensureCollection(ctx context.Context) error {
	exists, err := c.collectionExists(ctx, c.collection)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	slog.Info("creating collection", "name", c.collection)
	body := map[string]any{
		"vectors": map[string]any{
			denseVector: map[string]any{"size": vectorSize, "distance": "Cosine"},
		},
		"sparse_vectors": map[string]any{
			sparseVector: map[string]any{},
		},
	}
	return c.do(ctx, http.MethodPut, "/collections/"+c.collection, body, nil)
}

func (c *Client) collectionExists(ctx context.Context, name string) (bool, error) {
	var out struct {
		Result struct {
			Exists bool `json:"exists"`
		} `json:"result"`
	}
	if err := c.do(ctx, http.MethodGet, "/collections/"+name+"/exists", nil, &out); err != nil {
		return false, err
	}
	return out.Result.Exists, nil
}

// SaveEmbeddings upserts one point per chunk with the dense vector and payload.
func (c *Client) SaveEmbeddings(ctx context.Context, fileName string, chunks []chunk.Chunk, embeddings [][]float32) error {
	points := make([]map[string]any, 0, len(chunks))
	for i, ch := range chunks {
		if i >= len(embeddings) {
			break
		}
		points = append(points, map[string]any{
			"id": uuid.NewString(),
			"vector": map[string]any{
				denseVector: embeddings[i],
			},
			"payload": map[string]any{
				"document_name": fileName,
				"html_content":  ch.OriginalHTML,
				"text_to_embed": ch.TextToEmbed,
				"header":        ch.Header,
				"is_table":      strings.Contains(ch.OriginalHTML, "<table>"),
			},
		})
	}
	body := map[string]any{"points": points}
	if err := c.do(ctx, http.MethodPut, "/collections/"+c.collection+"/points?wait=true", body, nil); err != nil {
		return err
	}
	slog.Info("saved points to qdrant", "count", len(points), "document", fileName)
	return nil
}

// Search runs a dense-vector similarity query.
func (c *Client) Search(ctx context.Context, queryVector []float32, limit int) ([]Hit, error) {
	body := map[string]any{
		"query":        queryVector,
		"using":        denseVector,
		"limit":        limit,
		"with_payload": true,
	}
	var out struct {
		Result struct {
			Points []struct {
				ID      json.RawMessage `json:"id"`
				Score   float32         `json:"score"`
				Payload struct {
					DocumentName string `json:"document_name"`
					HTMLContent  string `json:"html_content"`
					Header       string `json:"header"`
				} `json:"payload"`
			} `json:"points"`
		} `json:"result"`
	}
	if err := c.do(ctx, http.MethodPost, "/collections/"+c.collection+"/points/query", body, &out); err != nil {
		return nil, err
	}

	hits := make([]Hit, 0, len(out.Result.Points))
	for _, p := range out.Result.Points {
		hits = append(hits, Hit{
			ID:           strings.Trim(string(p.ID), `"`),
			Score:        p.Score,
			DocumentName: p.Payload.DocumentName,
			HTMLContent:  p.Payload.HTMLContent,
			Header:       p.Payload.Header,
		})
	}
	return hits, nil
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		return fmt.Errorf("qdrant %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(buf.String()))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
