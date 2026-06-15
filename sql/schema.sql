-- Document storage schema.
--
-- A "document" is the logical group identified by (project, doc_type), e.g.
-- ("АБОБ", "deploy_order"). It owns an append-only, monotonically increasing
-- sequence of immutable versions. HTML (content_html) is the source of truth;
-- the DOCX is a derived artifact stored in S3 and referenced by key only.

CREATE TABLE IF NOT EXISTS documents (
    id          BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project     TEXT        NOT NULL,
    doc_type    TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project, doc_type)
);

CREATE TABLE IF NOT EXISTS document_versions (
    id               BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    document_id      BIGINT      NOT NULL REFERENCES documents (id) ON DELETE CASCADE,
    version_number   INTEGER     NOT NULL,
    -- Denormalised "project/doc_type" label so the search vector and result
    -- rows are self-contained without a join.
    document_label   TEXT        NOT NULL,
    header           TEXT        NOT NULL DEFAULT '',
    content_html     TEXT        NOT NULL,
    content_text     TEXT        NOT NULL DEFAULT '',
    metadata         JSONB       NOT NULL DEFAULT '{}'::jsonb,
    change_summary   TEXT        NOT NULL DEFAULT '',
    docx_object_key  TEXT,
    docx_rendered_at TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (document_id, version_number)
);

-- Generated full-text search vector. document_label is weighted highest so a
-- query that names the project/doc_type ranks the right document first.
ALTER TABLE document_versions
    ADD COLUMN IF NOT EXISTS search_vector tsvector
    GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', coalesce(document_label, '')), 'A') ||
        setweight(to_tsvector('simple', coalesce(header, '')), 'A') ||
        setweight(to_tsvector('simple', coalesce(change_summary, '')), 'B') ||
        setweight(to_tsvector('simple', coalesce(content_text, '')), 'C')
    ) STORED;

CREATE INDEX IF NOT EXISTS document_versions_search_idx
    ON document_versions USING GIN (search_vector);

CREATE INDEX IF NOT EXISTS document_versions_doc_idx
    ON document_versions (document_id, version_number DESC);
