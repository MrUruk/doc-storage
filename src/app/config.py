"""Runtime configuration, sourced from environment variables.

Everything the service touches in production — Postgres and S3 — is configured
here so the same code runs locally (docker-compose: postgres + MinIO) and in
production (managed Postgres + real S3) by changing env vars only.
"""

from __future__ import annotations

import os
from dataclasses import dataclass, field


def _env(name: str, default: str | None = None) -> str | None:
    value = os.environ.get(name)
    if value is None or value == "":
        return default
    return value


@dataclass(frozen=True)
class DatabaseConfig:
    # Standard libpq connection string / URL, e.g.
    # postgresql://user:pass@host:5432/docstorage
    url: str = field(
        default_factory=lambda: _env(
            "DATABASE_URL",
            "postgresql://postgres:postgres@localhost:5432/docstorage",
        )
        or ""
    )
    # Full-text search dictionary. "simple" avoids language-specific stemming
    # and works well for mixed Russian/English document text.
    text_search_config: str = field(
        default_factory=lambda: _env("DB_TEXT_SEARCH_CONFIG", "simple") or "simple"
    )
    min_pool_size: int = field(
        default_factory=lambda: int(_env("DB_POOL_MIN", "1") or "1")
    )
    max_pool_size: int = field(
        default_factory=lambda: int(_env("DB_POOL_MAX", "10") or "10")
    )


@dataclass(frozen=True)
class S3Config:
    bucket: str = field(default_factory=lambda: _env("S3_BUCKET", "doc-storage") or "")
    region: str | None = field(default_factory=lambda: _env("AWS_REGION", "us-east-1"))
    # endpoint_url is only set for non-AWS S3 (e.g. MinIO at http://localhost:9000).
    # Leave it unset in production to talk to real AWS S3.
    endpoint_url: str | None = field(default_factory=lambda: _env("S3_ENDPOINT_URL"))
    access_key_id: str | None = field(
        default_factory=lambda: _env("AWS_ACCESS_KEY_ID")
    )
    secret_access_key: str | None = field(
        default_factory=lambda: _env("AWS_SECRET_ACCESS_KEY")
    )
    # MinIO needs path-style addressing; AWS works with either.
    force_path_style: bool = field(
        default_factory=lambda: (_env("S3_FORCE_PATH_STYLE", "false") or "false").lower()
        == "true"
    )


@dataclass(frozen=True)
class AgentConfig:
    api_key: str | None = field(
        default_factory=lambda: _env("ANTHROPIC_API_KEY")
    )
    model: str = field(
        default_factory=lambda: _env("ANTHROPIC_MODEL", "claude-opus-4-8")
        or "claude-opus-4-8"
    )
    effort: str = field(default_factory=lambda: _env("ANTHROPIC_EFFORT", "high") or "high")
    max_tokens: int = field(
        default_factory=lambda: int(_env("ANTHROPIC_MAX_TOKENS", "16000") or "16000")
    )
    # Safety valve: cap the number of agentic tool-use turns per upload.
    max_iterations: int = field(
        default_factory=lambda: int(_env("AGENT_MAX_ITERATIONS", "12") or "12")
    )
    # Default document type used when the upload doesn't otherwise specify one.
    default_doc_type: str = field(
        default_factory=lambda: _env("DEFAULT_DOC_TYPE", "deploy_order")
        or "deploy_order"
    )


@dataclass(frozen=True)
class Config:
    database: DatabaseConfig = field(default_factory=DatabaseConfig)
    s3: S3Config = field(default_factory=S3Config)
    agent: AgentConfig = field(default_factory=AgentConfig)
    host: str = field(default_factory=lambda: _env("HOST", "0.0.0.0") or "0.0.0.0")
    port: int = field(default_factory=lambda: int(_env("PORT", "8080") or "8080"))
    # Max bytes accepted for a single streamed upload (default 64 MiB).
    max_upload_bytes: int = field(
        default_factory=lambda: int(
            _env("MAX_UPLOAD_BYTES", str(64 * 1024 * 1024)) or str(64 * 1024 * 1024)
        )
    )


def load_config() -> Config:
    return Config()
