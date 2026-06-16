package chunker

import (
	"context"
	"strings"
	"testing"
)

func TestRecordChunkFormatting(t *testing.T) {
	col := &collector{}
	content := "Проект АБОБ, наряд на обновление ПО, версия 2.4.1. Этап 3 — миграция базы данных. " +
		"Способ запуска миграции: выполнить команду kubectl apply -f migrate-job.yaml. Версия ПО — 2.4.1."
	out, err := col.record(context.Background(), recordChunkArgs{
		Title:         "Этап 3 — миграция базы данных",
		Content:       content,
		ChunkIndex:    0,
		GlobalContext: "Тип документа: Наряд, Проект: АБОБ, Версия: 2.4.1",
	})
	if err != nil {
		t.Fatalf("record returned error: %v", err)
	}
	if !strings.Contains(out, "Чанк 0 'Этап 3 — миграция базы данных' записан.") {
		t.Errorf("unexpected tool result: %q", out)
	}
	if len(col.chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(col.chunks))
	}
	c := col.chunks[0]

	if c.Header != "Этап 3 — миграция базы данных" {
		t.Errorf("header = %q", c.Header)
	}
	// Content stored verbatim — the generated chunk, not the source document.
	if c.Content != content {
		t.Errorf("content = %q, want the generated chunk", c.Content)
	}
	// Exact values are preserved.
	for _, want := range []string{"kubectl apply -f migrate-job.yaml", "2.4.1"} {
		if !strings.Contains(c.Content, want) {
			t.Errorf("content missing exact value %q", want)
		}
	}
	// text_to_embed = global context + generated content.
	if !strings.HasPrefix(c.TextToEmbed, "Тип документа: Наряд, Проект: АБОБ, Версия: 2.4.1") {
		t.Errorf("text_to_embed should start with global context: %q", c.TextToEmbed)
	}
	if !strings.Contains(c.TextToEmbed, content) {
		t.Errorf("text_to_embed should contain the generated content")
	}
}
