"""Connect-RPC StorageService implementation.

Wires the generated service protocol to the agent (AgenticSaveFile) and the
Postgres full-text search (AgenticSearch).
"""

from __future__ import annotations

import logging
from collections.abc import AsyncIterator

from connecpy.code import Code
from connecpy.exceptions import ConnecpyException
from connecpy.request import RequestContext

import storage.v1.storage_pb2 as pb
from storage.v1.storage_connecpy import StorageService as StorageServiceProtocol

from .agent import DocumentAgent
from .repository import Repository

logger = logging.getLogger(__name__)


class StorageService(StorageServiceProtocol):
    def __init__(
        self,
        repository: Repository,
        agent: DocumentAgent,
        *,
        max_upload_bytes: int,
    ) -> None:
        self._repo = repository
        self._agent = agent
        self._max_upload_bytes = max_upload_bytes

    async def agentic_save_file(
        self,
        request: AsyncIterator[pb.SaveFileRequest],
        ctx: RequestContext,
    ) -> pb.SaveFileResponse:
        file_name = ""
        chunks: list[bytes] = []
        total = 0
        async for message in request:
            if message.file_name and not file_name:
                file_name = message.file_name
            if message.part:
                total += len(message.part)
                if total > self._max_upload_bytes:
                    raise ConnecpyException(
                        Code.RESOURCE_EXHAUSTED,
                        f"upload exceeds {self._max_upload_bytes} bytes",
                    )
                chunks.append(message.part)

        if not file_name:
            raise ConnecpyException(
                Code.INVALID_ARGUMENT, "file_name is required in the stream"
            )
        data = b"".join(chunks)
        if not data:
            raise ConnecpyException(Code.INVALID_ARGUMENT, "no file content received")

        result = await self._agent.process_upload(file_name, data)
        if result.blocked:
            # The agent could not unambiguously create a version (e.g. project /
            # doc_type undetermined). Surface its explanation to the caller.
            raise ConnecpyException(
                Code.FAILED_PRECONDITION,
                result.final_text or "could not determine the target document",
            )
        logger.info(
            "saved %d version(s) from %s: %s",
            len(result.created_versions),
            file_name,
            ", ".join(v["document_name"] for v in result.created_versions),
        )
        return pb.SaveFileResponse()

    async def agentic_search(
        self,
        request: pb.SearchRequest,
        ctx: RequestContext,
    ) -> pb.SearchResponse:
        hits = await self._repo.search(request.query, request.limit)
        return pb.SearchResponse(
            results=[
                pb.SearchResult(
                    document_name=hit.document_name,
                    html_content=hit.html_content,
                    header=hit.header,
                    score=hit.score,
                )
                for hit in hits
            ]
        )
