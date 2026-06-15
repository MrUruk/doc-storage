"""Tests for tool dispatch and search wiring using in-memory fakes.

These exercise the real agent tool-execution and the real service code paths
without Postgres, S3, or the Anthropic API.
"""

from __future__ import annotations

import json
from dataclasses import dataclass

import pytest

from app.agent import DocumentAgent
from app.config import AgentConfig
from app.repository import DocumentVersion, SearchHit
from app.service import StorageService


class FakeRepo:
    def __init__(self):
        self.versions: dict[int, DocumentVersion] = {}
        self.docx_refs: dict[int, str] = {}
        self._next_id = 1

    async def create_document_version(
        self, project, doc_type, content_html, metadata, change_summary, header=None
    ):
        version = DocumentVersion(
            id=self._next_id,
            project=project,
            doc_type=doc_type,
            version_number=len(self.versions) + 1,
            document_label=f"{project}/{doc_type}",
            header=header or "Наряд",
            content_html=content_html,
            metadata=metadata or {},
            change_summary=change_summary,
            docx_object_key=None,
            docx_rendered_at=None,
            created_at=None,
        )
        self.versions[version.id] = version
        self._next_id += 1
        return version

    async def get_version_by_id(self, version_id):
        return self.versions.get(version_id)

    async def set_docx_reference(self, version_id, object_key):
        self.docx_refs[version_id] = object_key

    async def find_latest_document(self, project, doc_type):
        matches = [
            v
            for v in self.versions.values()
            if v.project == project and v.doc_type == doc_type
        ]
        return max(matches, key=lambda v: v.version_number, default=None)

    async def search(self, query, limit):
        return [
            SearchHit(
                document_name="АБОБ/deploy_order v8",
                html_content="<h1>Наряд</h1>",
                header="Наряд",
                score=0.42,
            )
        ]


class FakeStore:
    def __init__(self):
        self.objects: dict[str, bytes] = {}

    async def put(self, key, body, content_type="application/octet-stream"):
        self.objects[key] = body
        return key

    async def presigned_url(self, key, expires_in=3600):
        return f"https://example/{key}"


def _agent(repo, store):
    cfg = AgentConfig(api_key="test-key")
    return DocumentAgent(cfg, repo, store)


async def test_create_then_render_tool_flow():
    repo, store = FakeRepo(), FakeStore()
    agent = _agent(repo, store)
    created: list[dict] = []

    payload, is_error = await agent._execute_tool(
        "create_document_version",
        {
            "project": "АБОБ",
            "doc_type": "deploy_order",
            "content_html": "<h1>Наряд</h1><p>ПО 2.4.1</p>",
            "metadata": {"software_version": "2.4.1"},
            "change_summary": "Версия ПО 2.3.0 → 2.4.1",
        },
        created,
    )
    assert not is_error
    result = json.loads(payload)
    assert result["version_number"] == 1
    assert created and created[0]["version_id"] == result["version_id"]

    version_id = result["version_id"]
    payload, is_error = await agent._execute_tool(
        "render_docx", {"version_id": version_id}, created
    )
    assert not is_error
    render = json.loads(payload)
    assert render["docx_object_key"].endswith("v1.docx")
    # DOCX persisted to the store and referenced in the repo.
    assert render["docx_object_key"] in store.objects
    assert repo.docx_refs[version_id] == render["docx_object_key"]
    assert created[0]["docx_url"].startswith("https://example/")


async def test_render_failure_preserves_version(monkeypatch):
    repo, store = FakeRepo(), FakeStore()
    agent = _agent(repo, store)
    created: list[dict] = []
    await agent._execute_tool(
        "create_document_version",
        {
            "project": "АБОБ",
            "doc_type": "deploy_order",
            "content_html": "<h1>Наряд</h1>",
            "change_summary": "init",
        },
        created,
    )

    async def boom(_html):
        raise RuntimeError("render exploded")

    monkeypatch.setattr("app.agent.render_docx", boom)
    payload, is_error = await agent._execute_tool(
        "render_docx", {"version_id": 1}, created
    )
    assert is_error
    assert "version preserved" in json.loads(payload)["error"]
    # The version itself still exists.
    assert await repo.get_version_by_id(1) is not None


async def test_unknown_tool_reports_error():
    agent = _agent(FakeRepo(), FakeStore())
    payload, is_error = await agent._execute_tool("nope", {}, [])
    assert is_error
    assert "unknown tool" in json.loads(payload)["error"]


@dataclass
class _Ctx:
    pass


class _Req:
    def __init__(self, query, limit):
        self.query = query
        self.limit = limit


async def test_service_search_maps_hits():
    repo = FakeRepo()
    service = StorageService(repo, agent=None, max_upload_bytes=1024)
    response = await service.agentic_search(_Req("АБОБ kubectl", 5), _Ctx())
    assert len(response.results) == 1
    hit = response.results[0]
    assert hit.document_name == "АБОБ/deploy_order v8"
    assert hit.header == "Наряд"
    assert abs(hit.score - 0.42) < 1e-6


async def test_save_file_requires_filename():
    from connecpy.exceptions import ConnecpyException

    service = StorageService(FakeRepo(), agent=None, max_upload_bytes=1024)

    async def stream():
        class _M:
            file_name = ""
            part = b"data"

        yield _M()

    with pytest.raises(ConnecpyException):
        await service.agentic_save_file(stream(), _Ctx())
