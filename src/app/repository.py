"""Postgres persistence for documents and their immutable versions.

This module is the single source of truth for the data model described in the
agent specification: documents keyed by (project, doc_type), each owning an
append-only sequence of versions. content_html is authoritative; the DOCX is a
derived artifact referenced by S3 object key.

Uses psycopg 3 with an async connection pool so it composes with the async
Connect-RPC handlers.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Any

import psycopg
from psycopg.rows import dict_row
from psycopg.types.json import Jsonb
from psycopg_pool import AsyncConnectionPool

from .config import DatabaseConfig
from .html_text import html_to_text, html_first_heading

_SCHEMA_PATH = Path(__file__).resolve().parents[2] / "sql" / "schema.sql"


@dataclass
class DocumentVersion:
    id: int
    project: str
    doc_type: str
    version_number: int
    document_label: str
    header: str
    content_html: str
    metadata: dict[str, Any]
    change_summary: str
    docx_object_key: str | None
    docx_rendered_at: datetime | None
    created_at: datetime | None

    @property
    def document_name(self) -> str:
        return f"{self.document_label} v{self.version_number}"


@dataclass
class SearchHit:
    document_name: str
    html_content: str
    header: str
    score: float


def _label(project: str, doc_type: str) -> str:
    return f"{project}/{doc_type}"


class Repository:
    def __init__(self, config: DatabaseConfig) -> None:
        self._config = config
        # open=False: the pool is opened explicitly during app startup.
        self._pool = AsyncConnectionPool(
            conninfo=config.url,
            min_size=config.min_pool_size,
            max_size=config.max_pool_size,
            kwargs={"row_factory": dict_row},
            open=False,
        )

    async def open(self) -> None:
        await self._pool.open()
        await self.ensure_schema()

    async def close(self) -> None:
        await self._pool.close()

    async def ensure_schema(self) -> None:
        ddl = _SCHEMA_PATH.read_text(encoding="utf-8")
        async with self._pool.connection() as conn:
            await conn.execute(ddl)
            await conn.commit()

    # -- reads ---------------------------------------------------------------

    async def find_latest_document(
        self, project: str, doc_type: str
    ) -> DocumentVersion | None:
        """Return the most recent version of (project, doc_type), or None."""
        async with self._pool.connection() as conn:
            row = await (
                await conn.execute(
                    """
                    SELECT v.*, d.project, d.doc_type
                    FROM document_versions v
                    JOIN documents d ON d.id = v.document_id
                    WHERE d.project = %s AND d.doc_type = %s
                    ORDER BY v.version_number DESC
                    LIMIT 1
                    """,
                    (project, doc_type),
                )
            ).fetchone()
        return _to_version(row) if row else None

    async def list_document_history(
        self, project: str, doc_type: str, limit: int = 20
    ) -> list[dict[str, Any]]:
        """Version metadata (no full HTML) for agent context."""
        limit = max(1, min(limit or 20, 100))
        async with self._pool.connection() as conn:
            rows = await (
                await conn.execute(
                    """
                    SELECT v.version_number, v.header, v.change_summary,
                           v.metadata, v.docx_object_key, v.created_at
                    FROM document_versions v
                    JOIN documents d ON d.id = v.document_id
                    WHERE d.project = %s AND d.doc_type = %s
                    ORDER BY v.version_number DESC
                    LIMIT %s
                    """,
                    (project, doc_type, limit),
                )
            ).fetchall()
        return [
            {
                "version_number": r["version_number"],
                "header": r["header"],
                "change_summary": r["change_summary"],
                "metadata": r["metadata"],
                "docx_object_key": r["docx_object_key"],
                "created_at": r["created_at"].isoformat() if r["created_at"] else None,
            }
            for r in rows
        ]

    async def get_document_version(
        self, project: str, doc_type: str, version_number: int
    ) -> DocumentVersion | None:
        async with self._pool.connection() as conn:
            row = await (
                await conn.execute(
                    """
                    SELECT v.*, d.project, d.doc_type
                    FROM document_versions v
                    JOIN documents d ON d.id = v.document_id
                    WHERE d.project = %s AND d.doc_type = %s
                      AND v.version_number = %s
                    """,
                    (project, doc_type, version_number),
                )
            ).fetchone()
        return _to_version(row) if row else None

    async def get_version_by_id(self, version_id: int) -> DocumentVersion | None:
        async with self._pool.connection() as conn:
            row = await (
                await conn.execute(
                    """
                    SELECT v.*, d.project, d.doc_type
                    FROM document_versions v
                    JOIN documents d ON d.id = v.document_id
                    WHERE v.id = %s
                    """,
                    (version_id,),
                )
            ).fetchone()
        return _to_version(row) if row else None

    # -- writes --------------------------------------------------------------

    async def create_document_version(
        self,
        project: str,
        doc_type: str,
        content_html: str,
        metadata: dict[str, Any] | None,
        change_summary: str,
        header: str | None = None,
    ) -> DocumentVersion:
        """Append a new version with version_number = previous + 1.

        History is immutable: this only ever inserts. The document row is
        created on first use. Runs in a single transaction with row locking so
        concurrent uploads cannot mint the same version number.
        """
        metadata = metadata or {}
        label = _label(project, doc_type)
        content_text = html_to_text(content_html)
        header = (header or html_first_heading(content_html) or label).strip()

        async with self._pool.connection() as conn:
            async with conn.transaction():
                doc = await (
                    await conn.execute(
                        """
                        INSERT INTO documents (project, doc_type)
                        VALUES (%s, %s)
                        ON CONFLICT (project, doc_type) DO UPDATE
                            SET project = EXCLUDED.project
                        RETURNING id
                        """,
                        (project, doc_type),
                    )
                ).fetchone()
                document_id = doc["id"]

                # The upsert above takes a row lock on the documents row, so
                # concurrent uploads of the same (project, doc_type) serialize
                # here and this MAX read is stable. The UNIQUE(document_id,
                # version_number) constraint is the final backstop.
                next_row = await (
                    await conn.execute(
                        """
                        SELECT COALESCE(MAX(version_number), 0) + 1 AS next
                        FROM document_versions
                        WHERE document_id = %s
                        """,
                        (document_id,),
                    )
                ).fetchone()
                next_version = next_row["next"]

                row = await (
                    await conn.execute(
                        """
                        INSERT INTO document_versions (
                            document_id, version_number, document_label, header,
                            content_html, content_text, metadata, change_summary
                        )
                        VALUES (%s, %s, %s, %s, %s, %s, %s, %s)
                        RETURNING *,
                            (SELECT project FROM documents WHERE id = %s) AS project,
                            (SELECT doc_type FROM documents WHERE id = %s) AS doc_type
                        """,
                        (
                            document_id,
                            next_version,
                            label,
                            header,
                            content_html,
                            content_text,
                            Jsonb(metadata),
                            change_summary,
                            document_id,
                            document_id,
                        ),
                    )
                ).fetchone()
        return _to_version(row)

    async def set_docx_reference(self, version_id: int, object_key: str) -> None:
        """Record the rendered DOCX artifact without mutating the HTML source."""
        async with self._pool.connection() as conn:
            await conn.execute(
                """
                UPDATE document_versions
                SET docx_object_key = %s, docx_rendered_at = now()
                WHERE id = %s
                """,
                (object_key, version_id),
            )
            await conn.commit()

    # -- search --------------------------------------------------------------

    async def search(self, query: str, limit: int = 10) -> list[SearchHit]:
        """Ranked full-text search across all stored versions.

        Uses websearch_to_tsquery so callers can pass natural queries
        ("АБОБ kubectl migration"). score is the ts_rank of the match.
        """
        limit = max(1, min(limit or 10, 100))
        if not query or not query.strip():
            return []
        cfg = self._config.text_search_config
        async with self._pool.connection() as conn:
            rows = await (
                await conn.execute(
                    f"""
                    SELECT
                        v.document_label,
                        v.version_number,
                        v.header,
                        v.content_html,
                        ts_rank(v.search_vector,
                                websearch_to_tsquery('{cfg}', %s)) AS score
                    FROM document_versions v
                    WHERE v.search_vector @@ websearch_to_tsquery('{cfg}', %s)
                    ORDER BY score DESC, v.created_at DESC
                    LIMIT %s
                    """,
                    (query, query, limit),
                )
            ).fetchall()
        return [
            SearchHit(
                document_name=f"{r['document_label']} v{r['version_number']}",
                html_content=r["content_html"],
                header=r["header"],
                score=float(r["score"]),
            )
            for r in rows
        ]


def _to_version(row: dict[str, Any]) -> DocumentVersion:
    return DocumentVersion(
        id=row["id"],
        project=row["project"],
        doc_type=row["doc_type"],
        version_number=row["version_number"],
        document_label=row["document_label"],
        header=row["header"],
        content_html=row["content_html"],
        metadata=row["metadata"] or {},
        change_summary=row["change_summary"],
        docx_object_key=row.get("docx_object_key"),
        docx_rendered_at=row.get("docx_rendered_at"),
        created_at=row.get("created_at"),
    )
