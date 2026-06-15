"""Tests for the pure-logic helpers: no Postgres, S3, or Anthropic needed."""

from __future__ import annotations

import io
import zipfile

from app.docx_render import _render_sync
from app.file_extract import extract
from app.html_text import html_first_heading, html_to_text

SAMPLE_HTML = """
<h1>Наряд на обновление ПО</h1>
<p>Проект: АБОБ</p>
<table>
  <tr><th rowspan="2">Этап</th><th colspan="2">Параметры</th></tr>
  <tr><td>Версия ПО</td><td>2.4.1</td></tr>
  <tr><td>Миграция</td><td colspan="2">kubectl apply -f migrate-job.yaml</td></tr>
</table>
"""


def test_html_to_text_strips_tags():
    text = html_to_text(SAMPLE_HTML)
    assert "Наряд на обновление ПО" in text
    assert "<h1>" not in text
    assert "2.4.1" in text


def test_html_first_heading():
    assert html_first_heading(SAMPLE_HTML) == "Наряд на обновление ПО"
    assert html_first_heading("<p>Just a paragraph</p>") == "Just a paragraph"
    assert html_first_heading("") is None


def test_render_docx_produces_valid_document_with_spans():
    data = _render_sync(SAMPLE_HTML)
    # A .docx is a zip; document.xml must be present and contain the content.
    with zipfile.ZipFile(io.BytesIO(data)) as zf:
        names = zf.namelist()
        assert "word/document.xml" in names
        xml = zf.read("word/document.xml").decode("utf-8")
    assert "Наряд на обновление ПО" in xml
    assert "kubectl apply" in xml


def test_extract_html_and_text():
    html = extract("order.html", SAMPLE_HTML.encode("utf-8"))
    assert html.kind == "html"
    assert "Наряд" in html.content

    txt = extract("note.txt", "поменялась версия ПО на 2.4.1".encode("utf-8"))
    assert txt.kind == "text"
    assert "2.4.1" in txt.content


def test_extract_decodes_cp1251():
    payload = "Проект АБОБ".encode("cp1251")
    result = extract("note.txt", payload)
    assert "Проект АБОБ" in result.content
