"""The document-versioning agent.

On every uploaded file the agent reconstructs the latest version of the
relevant document, applies only the requested changes, and appends a new
immutable version — exactly the workflow in the specification. It runs a manual
Anthropic tool-use loop so each tool call executes against Postgres / S3
server-side.
"""

from __future__ import annotations

import json
import logging
from dataclasses import dataclass, field
from typing import Any

from anthropic import AsyncAnthropic

from .config import AgentConfig
from .docx_render import render_docx
from .file_extract import extract
from .objectstore import ObjectStore
from .repository import Repository

logger = logging.getLogger(__name__)

SYSTEM_PROMPT = """\
Ты управляешь документами компании (наряды на обновление ПО и др.). По загруженному \
файлу ты создаёшь новую версию документа на основе истории предыдущих версий, сохраняешь \
её и при необходимости рендеришь DOCX.

# Модель данных
Документ — логическая группа, идентифицируемая парой (project, doc_type). У документа \
есть упорядоченные версии с монотонно растущим version_number. Источник истины — HTML \
(content_html). DOCX — производный артефакт, хранится в объектном хранилище; в БД лежит \
только ссылка. Версия считается сохранённой сразу после записи HTML; сбой render_docx не \
отменяет создание версии. Структурированные поля (версия ПО, способ запуска миграции и \
т.п.) дублируй в metadata (JSON), чтобы их можно было искать и сравнивать без парсинга HTML.

# Инструменты
- find_latest_document(project, doc_type) — последняя версия или null, если документа нет.
- list_document_history(project, doc_type, limit) — список версий без полного HTML.
- get_document_version(project, doc_type, version_number) — конкретная версия целиком.
- create_document_version(project, doc_type, content_html, metadata, change_summary) — \
создаёт новую версию с version_number = предыдущий + 1.
- render_docx(version_id) — рендерит DOCX из content_html, кладёт в хранилище, проставляет \
ссылку. Может завершиться ошибкой — это не удаляет версию.

# Алгоритм
1. Определи project и doc_type из имени файла и содержимого. Если определить однозначно \
невозможно — НЕ создавай версию и НЕ угадывай: заверши ответ текстом, явно перечислив, \
чего не хватает (этот сервис работает без интерактивного уточнения).
2. Вызови find_latest_document. Если документа нет — создаёшь первую версию (version_number \
станет 1) и сообщаешь, что истории не было.
3. Если версия найдена — возьми её content_html и metadata за основу. Внеси ТОЛЬКО \
изменения из загруженного файла; всё остальное (структуру таблиц, стили, неизменённые \
поля) сохрани без изменений. Сложные таблицы (rowspan/colspan) сохраняй структурно как есть.
4. Обнови соответствующие поля в metadata.
5. Сформируй короткий человекочитаемый change_summary — что изменилось относительно \
предыдущей версии.
6. Вызови create_document_version с новым HTML и metadata.
7. Вызови render_docx для созданной версии (используй её version_id из результата).
8. Заверши ответ: номер новой версии, краткое описание изменений и ссылка на DOCX (или \
пометка, что DOCX требует повторного рендеринга, если render_docx упал).

# Правила
- Никогда не перезаписывай существующую версию — история неизменяема.
- Не меняй части документа, которых изменение не касалось.
- Перед сохранением убедись, что внесённые изменения соответствуют запросу, и перечисли \
их в change_summary.
"""


def _tool_schema() -> list[dict[str, Any]]:
    return [
        {
            "name": "find_latest_document",
            "description": "Вернуть последнюю версию документа (project, doc_type) или null, если документа ещё нет.",
            "input_schema": {
                "type": "object",
                "properties": {
                    "project": {"type": "string"},
                    "doc_type": {"type": "string"},
                },
                "required": ["project", "doc_type"],
            },
        },
        {
            "name": "list_document_history",
            "description": "Список предыдущих версий документа (метаданные, без полного HTML) для контекста.",
            "input_schema": {
                "type": "object",
                "properties": {
                    "project": {"type": "string"},
                    "doc_type": {"type": "string"},
                    "limit": {"type": "integer"},
                },
                "required": ["project", "doc_type"],
            },
        },
        {
            "name": "get_document_version",
            "description": "Вернуть конкретную версию документа целиком, включая content_html и metadata.",
            "input_schema": {
                "type": "object",
                "properties": {
                    "project": {"type": "string"},
                    "doc_type": {"type": "string"},
                    "version_number": {"type": "integer"},
                },
                "required": ["project", "doc_type", "version_number"],
            },
        },
        {
            "name": "create_document_version",
            "description": "Создать новую версию (version_number = предыдущий + 1). Источник истины — content_html.",
            "input_schema": {
                "type": "object",
                "properties": {
                    "project": {"type": "string"},
                    "doc_type": {"type": "string"},
                    "content_html": {
                        "type": "string",
                        "description": "Полный HTML новой версии.",
                    },
                    "metadata": {
                        "type": "object",
                        "description": "Структурированные поля документа (JSON).",
                        "additionalProperties": True,
                    },
                    "change_summary": {
                        "type": "string",
                        "description": "Краткое описание изменений относительно предыдущей версии.",
                    },
                },
                "required": ["project", "doc_type", "content_html", "change_summary"],
            },
        },
        {
            "name": "render_docx",
            "description": "Отрендерить DOCX из content_html версии и сохранить в хранилище. Сбой не удаляет версию.",
            "input_schema": {
                "type": "object",
                "properties": {"version_id": {"type": "integer"}},
                "required": ["version_id"],
            },
        },
    ]


@dataclass
class SaveResult:
    blocked: bool
    final_text: str
    created_versions: list[dict[str, Any]] = field(default_factory=list)


class DocumentAgent:
    def __init__(
        self,
        config: AgentConfig,
        repository: Repository,
        object_store: ObjectStore,
    ) -> None:
        self._config = config
        self._repo = repository
        self._store = object_store
        # AsyncAnthropic resolves ANTHROPIC_API_KEY from the env by default.
        self._client = AsyncAnthropic(api_key=config.api_key) if config.api_key else AsyncAnthropic()

    async def process_upload(self, file_name: str, data: bytes) -> SaveResult:
        extracted = extract(file_name, data)
        user_text = (
            f"Загружен файл: {file_name}\n"
            f"Тип содержимого: {extracted.kind}\n"
            f"Тип документа по умолчанию (если не указан иной): "
            f"{self._config.default_doc_type}\n\n"
            f"Содержимое файла:\n{extracted.content}"
        )
        messages: list[dict[str, Any]] = [{"role": "user", "content": user_text}]
        tools = _tool_schema()
        created: list[dict[str, Any]] = []
        final_text = ""

        for _ in range(self._config.max_iterations):
            response = await self._client.messages.create(
                model=self._config.model,
                max_tokens=self._config.max_tokens,
                system=SYSTEM_PROMPT,
                thinking={"type": "adaptive"},
                output_config={"effort": self._config.effort},
                tools=tools,
                messages=messages,
            )

            if response.stop_reason == "pause_turn":
                messages.append({"role": "assistant", "content": response.content})
                continue

            text_blocks = [b.text for b in response.content if b.type == "text"]
            if text_blocks:
                final_text = "\n".join(text_blocks)

            if response.stop_reason == "end_turn":
                break

            tool_uses = [b for b in response.content if b.type == "tool_use"]
            if not tool_uses:
                break

            messages.append({"role": "assistant", "content": response.content})
            results = []
            for block in tool_uses:
                payload, is_error = await self._execute_tool(
                    block.name, block.input, created
                )
                results.append(
                    {
                        "type": "tool_result",
                        "tool_use_id": block.id,
                        "content": payload,
                        "is_error": is_error,
                    }
                )
            messages.append({"role": "user", "content": results})

        blocked = len(created) == 0
        return SaveResult(blocked=blocked, final_text=final_text, created_versions=created)

    async def _execute_tool(
        self, name: str, args: dict[str, Any], created: list[dict[str, Any]]
    ) -> tuple[str, bool]:
        try:
            if name == "find_latest_document":
                version = await self._repo.find_latest_document(
                    args["project"], args["doc_type"]
                )
                return json.dumps(_version_payload(version), ensure_ascii=False), False

            if name == "list_document_history":
                history = await self._repo.list_document_history(
                    args["project"], args["doc_type"], int(args.get("limit", 20))
                )
                return json.dumps(history, ensure_ascii=False), False

            if name == "get_document_version":
                version = await self._repo.get_document_version(
                    args["project"], args["doc_type"], int(args["version_number"])
                )
                return json.dumps(_version_payload(version), ensure_ascii=False), False

            if name == "create_document_version":
                version = await self._repo.create_document_version(
                    project=args["project"],
                    doc_type=args["doc_type"],
                    content_html=args["content_html"],
                    metadata=args.get("metadata") or {},
                    change_summary=args.get("change_summary", ""),
                )
                created.append(
                    {
                        "version_id": version.id,
                        "document_name": version.document_name,
                        "version_number": version.version_number,
                        "change_summary": version.change_summary,
                    }
                )
                return (
                    json.dumps(
                        {
                            "version_id": version.id,
                            "version_number": version.version_number,
                            "document_name": version.document_name,
                        },
                        ensure_ascii=False,
                    ),
                    False,
                )

            if name == "render_docx":
                return await self._render_docx_tool(int(args["version_id"]), created)

            return json.dumps({"error": f"unknown tool {name}"}), True
        except KeyError as exc:
            return json.dumps({"error": f"missing argument: {exc}"}), True
        except Exception as exc:  # noqa: BLE001 - report tool failures to the model
            logger.exception("tool %s failed", name)
            return json.dumps({"error": str(exc)}), True

    async def _render_docx_tool(
        self, version_id: int, created: list[dict[str, Any]]
    ) -> tuple[str, bool]:
        version = await self._repo.get_version_by_id(version_id)
        if version is None:
            return json.dumps({"error": "version not found"}), True
        # Rendering must not undo the saved version: on failure, report an error
        # result the agent can relay, but leave the version intact.
        try:
            docx_bytes = await render_docx(version.content_html)
            key = f"{version.project}/{version.doc_type}/v{version.version_number}.docx"
            await self._store.put(
                key,
                docx_bytes,
                content_type=(
                    "application/vnd.openxmlformats-officedocument."
                    "wordprocessingml.document"
                ),
            )
            await self._repo.set_docx_reference(version.id, key)
            url = await self._store.presigned_url(key)
            for entry in created:
                if entry.get("version_id") == version.id:
                    entry["docx_object_key"] = key
                    entry["docx_url"] = url
            return json.dumps({"docx_object_key": key, "docx_url": url}, ensure_ascii=False), False
        except Exception as exc:  # noqa: BLE001
            logger.exception("render_docx failed for version %s", version_id)
            return (
                json.dumps(
                    {"error": f"render failed, version preserved: {exc}"},
                    ensure_ascii=False,
                ),
                True,
            )


def _version_payload(version: Any) -> Any:
    if version is None:
        return None
    return {
        "version_id": version.id,
        "project": version.project,
        "doc_type": version.doc_type,
        "version_number": version.version_number,
        "header": version.header,
        "content_html": version.content_html,
        "metadata": version.metadata,
        "change_summary": version.change_summary,
        "docx_object_key": version.docx_object_key,
    }
