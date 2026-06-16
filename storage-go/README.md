# storage-go

Go-порт Python-сервиса `storage` — ингест документов (наряды) и семантический
поиск. Тот же конвейер, те же контракты proto, на стеке, заданном требованиями:

- **Connect-RPC** (`connectrpc.com/connect`) — сервер и клиент к Pandoc.
- **eino** (`github.com/cloudwego/eino`) — ReAct-агент для agentic-чанкинга.
- **aws-sdk-go-v2** — выгрузка `.docx` в S3-совместимое хранилище.
- **pgx/v5** (`github.com/jackc/pgx/v5 v5.9.2`) — метаданные документов в PostgreSQL.
- **Qdrant** (REST) — векторное хранилище чанков.

## Конвейер `AgenticSaveFile` (client-streaming)

```
поток SaveFileRequest (file_name, project, doc_type, part…)
  → собрать .docx
  → extract dcterms:modified (docProps/core.xml)
  → PandocService.ConvertDocxToHtml  (Connect-клиент)
  → cleaner.ReduceHTMLToken          (переиспользует ../cleaner-go)
  → eino ReAct-агент + инструмент record_chunk  → []Chunk
  → эмбеддинги (OpenAI-совместимый /v1/embeddings, qwen3-embedding)
  → Qdrant upsert (dense-vector, payload)
  → Postgres: upsert documents + insert document_versions
  → S3 (aws-sdk-go-v2) PutObject  (не фатально при ошибке)
```

`AgenticSearch` (unary): эмбеддинг запроса (с инструкцией) → dense-поиск в
Qdrant → `SearchResult{document_name, html_content, header, score}`.

## Раскладка

| Путь | Назначение |
|------|------------|
| `proto/…` | контракты `storage.v1`, `pandoc.v1` |
| `gen/…` | сгенерированные protobuf + Connect стабы (закоммичены) |
| `internal/config` | конфиг из env (`params.py`) |
| `internal/docxprops` | дата изменения из `.docx` |
| `internal/embedding` | клиент сервиса эмбеддингов |
| `internal/vectordb` | Qdrant REST-клиент |
| `internal/sqldb` | PostgreSQL (pgx/v5) |
| `internal/blob` | S3 upload (aws-sdk-go-v2) |
| `internal/chunker` | eino ReAct-агент + `record_chunk` |
| `internal/service` | Connect-хендлер `StorageService` |
| `cmd/server` | точка входа (h2c-сервер, lifecycle) |

HTML-очистка переиспользует модуль `cleaner-go` через `replace` на `../cleaner-go`.

## Запуск

`config.InitEnv()` выбирает .env-файл по `GO_ENV`: `unset → .env` (сервис в
контейнере), `GO_ENV=development → .env.development` (локальный инстанс). Единый
fail-fast — функция `must`: обязательные переменные и недоступные зависимости
приводят к панике на старте.

```bash
cp .env.example .env                 # сервис/контейнер
go run ./cmd/server

cp .env.example .env.development     # локально
GO_ENV=development go run ./cmd/server
```

## Кодоген proto

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install connectrpc.com/connect/cmd/protoc-gen-connect-go@latest
./scripts/gen.sh
```

## Тесты

```bash
go test ./...
```

Покрыты чистые части без внешних сервисов: парсинг даты `.docx`, формат
инструкции/упорядочивание эмбеддингов, и формат чанка `record_chunk`.

## Отличия от Python (намеренные)

- Чанкинг-агент — на **eino** (ReAct), а не langgraph; инструмент `record_chunk`
  и системный промпт перенесены 1:1.
- Qdrant — через REST (без gRPC-клиента); коллекция, payload и dense-поиск
  совпадают.
- HTML5-парсер очистки оставляет валидный неявный `<tbody>` (как в `cleaner-go`).
