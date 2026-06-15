"""Extract uploaded files into HTML the agent can reason about and edit.

An upload may be a DOCX (a prior order or a template), raw HTML, or plain
text/markdown describing the change. We normalise everything to HTML because
content_html is the source of truth the agent works against.
"""

from __future__ import annotations

import html
import io
from dataclasses import dataclass

from docx import Document
from docx.document import Document as DocxDocument
from docx.oxml.ns import qn
from docx.table import Table
from docx.text.paragraph import Paragraph


@dataclass
class ExtractedFile:
    file_name: str
    # "html" when we have structured HTML, "text" when it's free-form text.
    kind: str
    content: str


def extract(file_name: str, data: bytes) -> ExtractedFile:
    lower = file_name.lower()
    if lower.endswith(".docx"):
        return ExtractedFile(file_name, "html", _docx_to_html(data))
    if lower.endswith((".html", ".htm", ".xhtml")):
        return ExtractedFile(file_name, "html", _decode(data))
    # txt, md, json, or unknown -> treat as plain text.
    return ExtractedFile(file_name, "text", _decode(data))


def _decode(data: bytes) -> str:
    for encoding in ("utf-8", "utf-16", "cp1251", "latin-1"):
        try:
            return data.decode(encoding)
        except UnicodeDecodeError:
            continue
    return data.decode("utf-8", errors="replace")


def _docx_to_html(data: bytes) -> str:
    document = Document(io.BytesIO(data))
    parts: list[str] = []
    for block in _iter_block_items(document):
        if isinstance(block, Paragraph):
            text = block.text.strip()
            if not text:
                continue
            style = (block.style.name or "").lower() if block.style else ""
            if style.startswith("heading"):
                level = "".join(ch for ch in style if ch.isdigit()) or "2"
                parts.append(f"<h{level}>{html.escape(text)}</h{level}>")
            else:
                parts.append(f"<p>{html.escape(text)}</p>")
        elif isinstance(block, Table):
            parts.append(_table_to_html(block))
    return "\n".join(parts)


def _iter_block_items(parent: DocxDocument):
    """Yield paragraphs and tables of a document in document order."""
    body = parent.element.body
    for child in body.iterchildren():
        if child.tag == qn("w:p"):
            yield Paragraph(child, parent)
        elif child.tag == qn("w:tbl"):
            yield Table(child, parent)


def _table_to_html(table: Table) -> str:
    rows_html: list[str] = []
    for row in table.rows:
        cells_html = [
            f"<td>{html.escape(cell.text.strip())}</td>" for cell in row.cells
        ]
        rows_html.append("<tr>" + "".join(cells_html) + "</tr>")
    return "<table>" + "".join(rows_html) + "</table>"
