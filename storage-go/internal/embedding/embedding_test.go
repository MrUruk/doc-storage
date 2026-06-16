package embedding

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQueryInstruction(t *testing.T) {
	got := queryInstruction("как откатить миграцию")
	want := "Instruct: Given a web search query, retrieve relevant passages that answer the query\nQuery:как откатить миграцию"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEncodeTextsOrdersByIndexAndWrapsQueries(t *testing.T) {
	var gotInput []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		_ = json.Unmarshal(body, &req)
		gotInput = req.Input
		// Return out of order to verify sorting by index.
		_, _ = io.WriteString(w, `{"data":[
			{"index":1,"embedding":[0.4,0.5]},
			{"index":0,"embedding":[0.1,0.2]}
		]}`)
	}))
	defer srv.Close()

	out, err := EncodeTexts(context.Background(), srv.Client(), srv.URL, []string{"a", "b"}, 1000, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 2 || out[0][0] != 0.1 || out[1][0] != 0.4 {
		t.Errorf("embeddings not ordered by index: %v", out)
	}
	// is_query=true wraps each input with the instruction prefix.
	for _, in := range gotInput {
		if !strings.HasPrefix(in, "Instruct: ") {
			t.Errorf("query not wrapped: %q", in)
		}
	}
}
