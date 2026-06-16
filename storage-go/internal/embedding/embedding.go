// Package embedding calls the OpenAI-compatible embedding service, mirroring
// embedding.encode_texts.
package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
)

const searchTask = "Given a web search query, retrieve relevant passages that answer the query"

func queryInstruction(query string) string {
	return fmt.Sprintf("Instruct: %s\nQuery:%s", searchTask, query)
}

type request struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type response struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// EncodeTexts returns embeddings for texts. When isQuery is true, each input is
// wrapped with the retrieval instruction. maxLength is accepted for parity with
// the Python signature; the embedding request body does not include it.
func EncodeTexts(ctx context.Context, client *http.Client, url string, texts []string, _ int, isQuery bool) ([][]float32, error) {
	in := texts
	if isQuery {
		in = make([]string, len(texts))
		for i, t := range texts {
			in[i] = queryInstruction(t)
		}
	}

	body, err := json.Marshal(request{Model: "qwen3-embedding", Input: in})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("embedding service returned %s", resp.Status)
	}

	var parsed response
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	// Preserve input order, the embedding service may return out of order.
	sort.Slice(parsed.Data, func(i, j int) bool { return parsed.Data[i].Index < parsed.Data[j].Index })
	out := make([][]float32, len(parsed.Data))
	for i := range parsed.Data {
		out[i] = parsed.Data[i].Embedding
	}
	return out, nil
}
