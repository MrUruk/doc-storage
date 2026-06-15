"""HTML helpers shared by the repository (search text) and the agent.

content_html is the source of truth, but for full-text search we need a
plain-text projection, and for a result header we want the document's first
heading. Both are derived here with BeautifulSoup.
"""

from __future__ import annotations

from bs4 import BeautifulSoup


def _soup(html: str) -> BeautifulSoup:
    # lxml is fast and tolerant; falls back to the stdlib parser if absent.
    try:
        return BeautifulSoup(html or "", "lxml")
    except Exception:
        return BeautifulSoup(html or "", "html.parser")


def html_to_text(html: str) -> str:
    """Collapse HTML to searchable plain text (tags stripped, whitespace tidy)."""
    if not html:
        return ""
    text = _soup(html).get_text(separator=" ")
    return " ".join(text.split())


def html_first_heading(html: str) -> str | None:
    """Return the first heading (h1..h4) or first non-empty block, if any."""
    if not html:
        return None
    soup = _soup(html)
    for tag in ("h1", "h2", "h3", "h4", "title", "caption", "p"):
        el = soup.find(tag)
        if el and el.get_text(strip=True):
            return el.get_text(strip=True)
    return None
