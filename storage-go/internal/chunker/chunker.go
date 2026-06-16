// Package chunker performs agentic semantic chunking of a cleaned HTML document
// using the eino framework's ReAct agent. The agent reads the whole document
// and, for every logical block, GENERATES a self-contained, semantically dense
// chunk optimized for vector search (it does not copy the source markup), then
// records it via the record_chunk tool.
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

const systemInstruction = `Ты — экспертный агент семантического чанкинга для системы векторного поиска (RAG). На вход — очищенный HTML документа (наряды на обновление ПО и т.п.), часто со сложными таблицами (rowspan/colspan). Твоя задача — разбить документ на самодостаточные смысловые блоки и для КАЖДОГО сгенерировать максимально насыщенный по смыслу текст, который легко находится векторным поиском, и сохранить его через инструмент record_chunk.

# Главные принципы
1. НЕ копируй исходный текст и НЕ воспроизводи разметку/таблицы дословно. Для каждого чанка СГЕНЕРИРУЙ новый связный текст на естественном языке, полностью понятный без остального документа.
2. Точные значения сохраняй дословно: версии (2.4.1), команды (kubectl apply -f migrate-job.yaml), идентификаторы, коды, имена файлов — их нельзя искажать, перефразировать или терять. Вплетай их в связные формулировки.
3. Делай чанк плотным по смыслу: свяжи каждое значение с его смыслом (что это за поле/колонка), укажи раздел/этап, добавь естественные формулировки и синонимы, под которые пользователь сформулирует запрос, раскрой аббревиатуры и коды.

# Глобальный контекст
- Один раз определи тип документа, проект и версию. Сформируй global_context вида "Тип документа: [Тип], Проект: [Название], Версия: [Версия]" и передавай его ОДИНАКОВЫМ во все вызовы record_chunk.
- Эти же сведения (проект, версия, раздел) встрой и в сам текст чанка, чтобы он был самодостаточен.

# Таблицы (включая rowspan/colspan)
- Разворачивай объединённые ячейки: значение ячейки с rowspan/colspan впиши в КАЖДУЮ строку/колонку, которую она покрывает. Ни одна строка не должна ссылаться на «заголовок выше/слева».
- НЕ создавай отдельный чанк на каждую ячейку и не дроби механически по одной строке — для большой таблицы это даст слишком много чанков, и модель не успеет их все сгенерировать. ГРУППИРУЙ логически связанные строки/секции в один насыщенный чанк: общий заголовок/секцию назови один раз и компактно перечисли отличающиеся значения строк, связав каждое со смыслом.
- Один чанк = одна логическая тема/секция таблицы (а не вся таблица целиком и не половина ячейки).

# Объём
- Цель ~350–400 токенов на чанк. Если секция большая — уплотняй формулировки, но не теряй ни одного значимого факта или значения.

# Заголовок
- title — содержательное название чанка (например, «Этап 3 — миграция базы данных»); оно тоже участвует в поиске.

# Процесс
1. Определи global_context.
2. Пройди документ, выделяя логические смысловые блоки (разделы, секции таблиц).
3. Для каждого вызови record_chunk(title, content, chunk_index, global_context), где content — сгенерированный насыщенный самодостаточный текст по принципам выше.

# Пример (как должен выглядеть content)
Исходная строка таблицы: Этап «3», секция «Миграция БД», «Способ запуска» = kubectl apply -f migrate-job.yaml, «Версия ПО» = 2.4.1.
content: «Проект АБОБ, наряд на обновление ПО, версия 2.4.1. Этап 3 — миграция базы данных. Способ запуска миграции (как запускается миграция БД на этом этапе): выполнить команду kubectl apply -f migrate-job.yaml. Версия ПО, к которой относится этот этап миграции, — 2.4.1.»

Отвечай по-русски, кратко и по делу; вся фактура — внутри вызовов record_chunk.`

const humanTemplate = "Разбей следующий HTML-документ на смысловые чанки и запиши каждый через инструмент record_chunk:\n\n%s"

// recordChunkArgs is the record_chunk tool's argument schema. Field
// descriptions are inferred from the jsonschema tags.
type recordChunkArgs struct {
	Title         string `json:"title" jsonschema:"required,description=Содержательное название чанка, например 'Этап 3 — миграция базы данных'."`
	Content       string `json:"content" jsonschema:"required,description=Сгенерированный максимально насыщенный по смыслу самодостаточный текст чанка для векторного поиска. НЕ копия исходника и не markdown-таблица. Точные значения (версии, команды, идентификаторы, имена файлов) — дословно."`
	ChunkIndex    int    `json:"chunk_index" jsonschema:"required,description=Порядковый номер чанка, начиная с 0."`
	GlobalContext string `json:"global_context" jsonschema:"required,description=Одинаковые для всех чанков метаданные документа: 'Тип документа: [Тип], Проект: [Название], Версия: [Версия]'."`
}

// collector accumulates chunks recorded by the tool during a single agent run.
type collector struct {
	mu     sync.Mutex
	chunks []chunk.Chunk
}

func (c *collector) record(_ context.Context, a recordChunkArgs) (string, error) {
	// Embed the global context together with the generated content; store the
	// generated content itself (not the source document).
	textToEmbed := fmt.Sprintf("%s\n\n%s", a.GlobalContext, a.Content)
	c.mu.Lock()
	c.chunks = append(c.chunks, chunk.Chunk{
		TextToEmbed: textToEmbed,
		Content:     a.Content,
		Header:      a.Title,
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
		"Сохраняет один сгенерированный смысловой чанк. Вызывай для каждого выделенного логического блока документа.",
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
