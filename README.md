# doc-storage

Connect-RPC service that stores company documents — deploy orders ("наряды") —
as an **append-only sequence of immutable versions** and lets clients search
them. An Anthropic (Claude) agent does the actual version authoring: given an
uploaded file it reconstructs the latest version of the relevant document,
applies *only* the requested changes, and saves a new version.

The design follows the agent specification: `content_html` is the source of
truth, the DOCX is a derived artifact stored in S3 (referenced by key), and
structured fields are mirrored into a JSON `metadata` column for search and
comparison. History is never rewritten.

## Architecture

```
                      Connect-RPC (connecpy ASGI, uvicorn)
                                   │
                 ┌─────────────────┴──────────────────┐
        AgenticSaveFile (client-stream)          AgenticSearch (unary)
                 │                                      │
                 ▼                                      ▼
        DocumentAgent (Claude, tool-use loop)    Postgres full-text search
                 │   tools                          (ts_rank over versions)
   ┌─────────────┼───────────────┐
   ▼             ▼               ▼
 find_latest  create_version   render_docx
   │             │               │
   ▼             ▼               ▼
 Postgres     Postgres        Postgres + S3
 (history)   (immutable        (HTML→DOCX,
              versions)         object store)
```

- **`AgenticSaveFile(stream SaveFileRequest) → SaveFileResponse`** — the client
  streams a file in parts. The server assembles it, extracts it to HTML
  (DOCX/HTML/text supported), and runs the agent. The agent's tools
  (`find_latest_document`, `list_document_history`, `get_document_version`,
  `create_document_version`, `render_docx`) execute server-side against Postgres
  and S3. If the agent cannot unambiguously determine the target document
  `(project, doc_type)`, it does **not** guess — the RPC returns
  `FAILED_PRECONDITION` with its explanation.
- **`AgenticSearch(SearchRequest) → SearchResponse`** — ranked Postgres
  full-text search (`websearch_to_tsquery` + `ts_rank`) over every stored
  version. Each result carries `document_name`, `html_content`, `header`, and a
  `score`. (No vector embeddings — full-text search over the HTML/metadata is
  sufficient here.)

### Layout

| Path | What |
|------|------|
| `proto/storage/v1/storage.proto` | Service contract |
| `src/storage/v1/` | **Generated** connecpy + protobuf stubs (committed) |
| `src/app/config.py` | Env-driven config (Postgres, S3, Anthropic) |
| `src/app/repository.py` | Postgres persistence + full-text search (psycopg 3) |
| `src/app/objectstore.py` | S3 / MinIO object storage (boto3) |
| `src/app/agent.py` | Claude agent + tool implementations |
| `src/app/file_extract.py` | Upload → HTML (DOCX/HTML/text) |
| `src/app/docx_render.py` | HTML → DOCX (incl. rowspan/colspan tables) |
| `src/app/service.py` | Connect-RPC `StorageService` implementation |
| `src/app/app.py` | ASGI app + lifecycle + uvicorn entrypoint |
| `sql/schema.sql` | Tables, generated `tsvector`, indexes |

## Running locally

```bash
# 1. dependencies (Postgres + MinIO)
docker compose up -d

# 2. Python env
python -m venv .venv && . .venv/bin/activate
pip install -e ".[dev]"

# 3. config
cp .env.example .env        # set ANTHROPIC_API_KEY
set -a; . ./.env; set +a

# 4. run (creates schema + bucket on startup)
python -m app.app
# serves Connect-RPC on http://localhost:8080
```

### Example calls

Search (Connect's JSON-over-HTTP, so plain `curl` works):

```bash
curl -X POST http://localhost:8080/storage.v1.StorageService/AgenticSearch \
  -H "Content-Type: application/json" \
  -d '{"query": "АБОБ kubectl миграция", "limit": 5}'
```

Saving a file is client-streaming — use the generated client
(`storage.v1.storage_connecpy.StorageServiceClient`) or any Connect client and
stream `SaveFileRequest{file_name, part}` messages.

## Production

The same code targets managed Postgres and real AWS S3 — set `DATABASE_URL`,
leave `S3_ENDPOINT_URL` unset (so boto3 uses AWS), and provide AWS credentials
via the standard chain (env/role/instance profile) and `ANTHROPIC_API_KEY`. The
service ensures the schema and bucket exist on startup.

## Regenerating stubs

```bash
./scripts/gen_proto.sh        # after editing proto/storage/v1/storage.proto
```

## Tests

```bash
pytest      # pure-logic + tool-dispatch tests (no Postgres/S3/Anthropic needed)
```
