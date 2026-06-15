"""ASGI application factory and entrypoint.

Builds the Connect-RPC StorageService app, wraps it with a lifespan handler
that opens/closes the Postgres pool and ensures the S3 bucket exists, and runs
it under uvicorn.
"""

from __future__ import annotations

import logging
from collections.abc import Awaitable, Callable
from typing import Any

from storage.v1.storage_connecpy import StorageServiceASGIApplication

from .agent import DocumentAgent
from .config import Config, load_config
from .objectstore import ObjectStore
from .repository import Repository
from .service import StorageService

logger = logging.getLogger(__name__)

Scope = dict[str, Any]
Receive = Callable[[], Awaitable[dict[str, Any]]]
Send = Callable[[dict[str, Any]], Awaitable[None]]


class Application:
    """ASGI callable that manages resource lifecycle around the Connect app."""

    def __init__(self, config: Config) -> None:
        self._config = config
        self._repo = Repository(config.database)
        self._store = ObjectStore(config.s3)
        self._agent = DocumentAgent(config.agent, self._repo, self._store)
        service = StorageService(
            self._repo, self._agent, max_upload_bytes=config.max_upload_bytes
        )
        self._connect_app = StorageServiceASGIApplication(
            service, read_max_bytes=config.max_upload_bytes
        )

    async def startup(self) -> None:
        await self._repo.open()
        await self._store.ensure_bucket()
        logger.info("storage service ready")

    async def shutdown(self) -> None:
        await self._repo.close()

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope["type"] == "lifespan":
            await self._handle_lifespan(receive, send)
            return
        await self._connect_app(scope, receive, send)

    async def _handle_lifespan(self, receive: Receive, send: Send) -> None:
        while True:
            message = await receive()
            if message["type"] == "lifespan.startup":
                try:
                    await self.startup()
                except Exception as exc:  # noqa: BLE001
                    logger.exception("startup failed")
                    await send(
                        {"type": "lifespan.startup.failed", "message": str(exc)}
                    )
                    return
                await send({"type": "lifespan.startup.complete"})
            elif message["type"] == "lifespan.shutdown":
                try:
                    await self.shutdown()
                except Exception:  # noqa: BLE001
                    logger.exception("shutdown failed")
                await send({"type": "lifespan.shutdown.complete"})
                return


def create_app(config: Config | None = None) -> Application:
    return Application(config or load_config())


def main() -> None:
    import uvicorn

    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(name)s %(message)s",
    )
    config = load_config()
    uvicorn.run(
        create_app(config),
        host=config.host,
        port=config.port,
        log_level="info",
    )


if __name__ == "__main__":
    main()
