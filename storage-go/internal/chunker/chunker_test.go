package chunker

import (
	"context"
	"strings"
	"testing"
)

func TestRecordChunkFormatting(t *testing.T) {
	col := &collector{}
	out, err := col.record(context.Background(), recordChunkArgs{
		Title:         "Версия ПО",
		Summary:       "Изменена версия и способ запуска миграции.",
		ChunkContent:  "| Поле | Значение |\n|---|---|\n| Версия | 2.4.1 |",
		ChunkIndex:    0,
		GlobalContext: "Тип документа: Наряд, Проект: АБОБ, Версия: 2.4.1",
	})
	if err != nil {
		t.Fatalf("record returned error: %v", err)
	}
	if !strings.Contains(out, "Чанк 0 'Версия ПО' записан.") {
		t.Errorf("unexpected tool result: %q", out)
	}
	if len(col.chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(col.chunks))
	}
	c := col.chunks[0]
	if c.Header != "Версия ПО" {
		t.Errorf("header = %q", c.Header)
	}
	// text_to_embed mirrors the Python template.
	for _, want := range []string{
		"Контекст: Тип документа: Наряд, Проект: АБОБ, Версия: 2.4.1",
		"# Версия ПО",
		"> **Описание:** Изменена версия",
		"## Содержимое",
		"| Версия | 2.4.1 |",
	} {
		if !strings.Contains(c.TextToEmbed, want) {
			t.Errorf("text_to_embed missing %q in:\n%s", want, c.TextToEmbed)
		}
	}
	// original_html wraps title/summary/content.
	for _, want := range []string{
		"<div class='agentic-chunk'>", "<h2>Версия ПО</h2>", "<p>Изменена версия",
	} {
		if !strings.Contains(c.OriginalHTML, want) {
			t.Errorf("original_html missing %q in:\n%s", want, c.OriginalHTML)
		}
	}
}
