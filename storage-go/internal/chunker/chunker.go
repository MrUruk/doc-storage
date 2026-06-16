// Package chunker performs agentic semantic chunking of a cleaned HTML document
// using the eino framework's ReAct agent. It mirrors agentic.agentic_chunk_via_agent:
// the agent reads the document and calls the record_chunk tool once per logical
// chunk; the tool collects the chunks server-side.
package chunker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"github.com/mruruk/doc-storage/storage-go/internal/chunk"
)

const systemInstruction = `Ты — экспертный агент по семантическому чанкированию (Agentic Chunking) документов. Твоя цель — прочитать исходный документ, аккуратно разделить его на самостоятельные логические чанки и сохранить их.

Твой рабочий процесс:
1. Проанализируй полученный HTML-документ. Если документ содержит HTML-таблицы (включая сложные rowspan/colspan), логически разверни объединенные ячейки, чтобы не потерять связь данных.
2. Сформулируй единый глобальный контекст для ВСЕХ чанков данного документа.
   - Определи тип документа (например, "Наряд на проведение работ по внесению изменений в ПО").
   - Найди название проекта.
   - Найди версию.
   Сформируй строку global_context вида: "Тип документа: [Тип], Проект: [Название], Версия: [Версия]".
3. Раздели документ на самостоятельные чанки под лимит 350-400 токенов.
   - Если фрагмент представляет собой часть таблицы, обязательно дублируй оригинальную строку заголовков (headers) в начало каждого табличного чанка.
   - Старайся не разделять строки, относящиеся к одной логической группе.
4. Для каждого выделенного чанка подготовь структуру:
   - title: краткое содержательное название блока.
   - summary: емкое семантическое саммари из 1-2 предложений.
   - chunk_content: форматированный текст фрагмента в Markdown.
5. Последовательно вызови инструмент record_chunk для сохранения каждого чанка, передавая извлеченный global_context ОДИНАКОВЫМ для всех вызовов.

Отвечай на русском языке кратко, профессионально и по делу.`

const humanTemplate = "Разбей следующий HTML-документ на логические чанки и запиши их через инструмент record_chunk:\n\n%s"

// recordChunkArgs is the record_chunk tool's argument schema. Field
// descriptions are inferred from the jsonschema tags.
type recordChunkArgs struct {
	Title         string `json:"title" jsonschema:"required,description=Краткое содержательное название чанка."`
	Summary       string `json:"summary" jsonschema:"required,description=1-2 предложения с семантическим описанием содержимого чанка."`
	ChunkContent  string `json:"chunk_content" jsonschema:"required,description=Фактическое содержимое чанка (строки таблицы в Markdown или логический раздел)."`
	ChunkIndex    int    `json:"chunk_index" jsonschema:"required,description=Порядковый номер чанка (начиная с 0)."`
	GlobalContext string `json:"global_context" jsonschema:"required,description=Глобальные метаданные документа в формате 'Тип документа: [Тип], Проект: [Название], Версия: [Версия]'."`
}

// collector accumulates chunks recorded by the tool during a single agent run.
type collector struct {
	mu     sync.Mutex
	chunks []chunk.Chunk
}

func (c *collector) record(_ context.Context, a recordChunkArgs) (string, error) {
	textToEmbed := fmt.Sprintf(
		"Контекст: %s\n\n# %s\n\n> **Описание:** %s\n\n## Содержимое\n%s",
		a.GlobalContext, a.Title, a.Summary, a.ChunkContent,
	)
	html := fmt.Sprintf(
		"<div class='agentic-chunk'><h2>%s</h2><p>%s</p>%s</div>",
		a.Title, a.Summary, a.ChunkContent,
	)
	c.mu.Lock()
	c.chunks = append(c.chunks, chunk.Chunk{
		TextToEmbed:  textToEmbed,
		OriginalHTML: html,
		Header:       a.Title,
	})
	c.mu.Unlock()
	slog.Info("agent recorded chunk", "index", a.ChunkIndex, "title", a.Title, "len", len(textToEmbed))
	return fmt.Sprintf("Чанк %d '%s' записан.", a.ChunkIndex, a.Title), nil
}

// Chunk drives the eino ReAct agent over htmlContent and returns the recorded
// chunks.
func Chunk(ctx context.Context, chatModel model.ToolCallingChatModel, htmlContent string) ([]chunk.Chunk, error) {
	col := &collector{}

	recordTool, err := utils.InferTool(
		"record_chunk",
		"Записывает сформированный семантический чанк. Вызывай этот инструмент для каждого выделенного логического чанка.",
		col.record,
	)
	if err != nil {
		return nil, fmt.Errorf("build record_chunk tool: %w", err)
	}

	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: []tool.BaseTool{recordTool},
		},
		MessageModifier: react.NewPersonaModifier(systemInstruction),
		MaxStep:         40,
	})
	if err != nil {
		return nil, fmt.Errorf("create react agent: %w", err)
	}

	slog.Info("invoking chunking agent", "html_chars", len(htmlContent))
	if _, err := agent.Generate(ctx, []*schema.Message{
		schema.UserMessage(fmt.Sprintf(humanTemplate, htmlContent)),
	}); err != nil {
		return nil, fmt.Errorf("agent generate: %w", err)
	}

	col.mu.Lock()
	defer col.mu.Unlock()
	slog.Info("chunking agent done", "chunks", len(col.chunks))
	return col.chunks, nil
}
