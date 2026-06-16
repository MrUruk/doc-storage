// Package sqldb stores document metadata and versions in PostgreSQL using
// pgx/v5, mirroring sql_db.SqlDB.
package sqldb

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS documents (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project     TEXT NOT NULL,
    doc_type    TEXT NOT NULL,
    title       TEXT,
    created_at  TIMESTAMPTZ DEFAULT now(),
    UNIQUE (project, doc_type)
);

CREATE TABLE IF NOT EXISTS document_versions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id     UUID NOT NULL REFERENCES documents(id),
    content_html    TEXT NOT NULL,
    docx_blob_ref   TEXT,
    docx_rendered   BOOLEAN DEFAULT false,
    created_at      TIMESTAMPTZ DEFAULT now()
);
`

type DB struct {
	pool *pgxpool.Pool
}

func New() *DB { return &DB{} }

func (d *DB) Connect(ctx context.Context, databaseURL string) error {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("parse database url: %w", err)
	}
	cfg.MinConns = 1
	cfg.MaxConns = 4

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return fmt.Errorf("ping: %w", err)
	}
	d.pool = pool
	return nil
}

func (d *DB) EnsureTables(ctx context.Context) error {
	_, err := d.pool.Exec(ctx, schemaSQL)
	return err
}

// UpsertDocument inserts or updates a (project, doc_type) document and returns
// its id.
func (d *DB) UpsertDocument(ctx context.Context, project, docType, title string) (string, error) {
	var id string
	err := d.pool.QueryRow(ctx,
		`INSERT INTO documents (project, doc_type, title)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (project, doc_type) DO UPDATE SET title = EXCLUDED.title
		 RETURNING id`,
		project, docType, title,
	).Scan(&id)
	return id, err
}

// InsertVersion appends a new immutable version row and returns its id.
func (d *DB) InsertVersion(ctx context.Context, documentID, contentHTML, docxBlobRef string, createdAt time.Time) (string, error) {
	var id string
	err := d.pool.QueryRow(ctx,
		`INSERT INTO document_versions (document_id, content_html, docx_blob_ref, created_at)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id`,
		documentID, contentHTML, docxBlobRef, createdAt,
	).Scan(&id)
	return id, err
}

func (d *DB) Close() {
	if d.pool != nil {
		d.pool.Close()
	}
}
