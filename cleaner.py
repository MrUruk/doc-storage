"""Reduce the token footprint of DOCX-converted HTML.

Word / LibreOffice "Save as HTML" output is full of noise that wastes model
context and breaks naive chunking: inline ``style`` soup, ``class="MsoNormal"``,
``mso-*`` properties, ``<span>``/``<font>``/``<div>`` wrappers, ``<o:p>`` markers,
conditional comments, namespaced VML tags, ``&nbsp;`` runs and empty paragraphs.

``reduce_html_token`` strips all of that while **preserving table structure** —
including complex ``rowspan`` / ``colspan`` — without normalising (expanding /
merging) the tables, which would itself blow up the token count.

Public API:
    >>> from cleaner import reduce_html_token
    >>> clean = reduce_html_token(messy_html)

Only dependency: beautifulsoup4 (lxml parser preferred, falls back to the
stdlib parser).
"""

from __future__ import annotations

import re

from bs4 import BeautifulSoup, Comment, NavigableString

__all__ = ["reduce_html_token"]

# Tags removed together with their content — pure noise from Word exports.
_DROP_TAGS = {
    "style", "script", "head", "meta", "link", "title", "base",
    "xml", "o:p", "colgroup", "col", "button", "input", "select",
    "textarea", "iframe", "noscript",
}

# Structural / semantic tags we keep. Anything not here (span, font, div,
# section, unknown Word junk, ...) is unwrapped: its text is kept, the tag
# dropped. Table tags are all here so the grid survives untouched.
_KEEP_TAGS = {
    "table", "thead", "tbody", "tfoot", "tr", "td", "th", "caption",
    "p", "br", "hr",
    "h1", "h2", "h3", "h4", "h5", "h6",
    "ul", "ol", "li",
    "strong", "b", "em", "i", "u", "sup", "sub",
    "blockquote", "pre", "code", "a", "img",
}

# Per-tag attribute whitelist. Everything else (style, class, lang, width,
# valign, bgcolor, mso-*, ...) is dropped.
_KEEP_ATTRS = {
    "td": {"rowspan", "colspan"},
    "th": {"rowspan", "colspan"},
    "a": {"href"},
    "img": {"alt", "src"},
}

# Tags that are legitimately empty (they don't need text to be meaningful).
_VOID_OK = {"br", "hr", "img", "td", "th"}

# Tags that are not whitespace-collapsed (content is significant verbatim).
_PREFORMATTED = {"pre", "code"}

# Leading bullet glyphs Word leaves behind in list items once spans are gone.
_BULLET_RE = re.compile("^\\s*(?:[\u2022\u00b7\u25aa\u25e6\u2023\u2219]+|o(?=\\s))\\s*")


def reduce_html_token(
    html: str,
    *,
    keep_links: bool = True,
    keep_images: bool = False,
) -> str:
    """Return a de-noised copy of ``html``.

    Parameters
    ----------
    html:
        DOCX-converted HTML (a fragment or a full document).
    keep_links:
        Keep ``<a href>`` anchors. When False, links are unwrapped to their
        text. Default True.
    keep_images:
        Keep ``<img>`` (data-URI sources are always dropped, since base64
        images are huge). When False, images are removed entirely. Default
        False — most DOCX images are decorative and bloat the context.
    """
    if not html or not html.strip():
        return ""

    soup = _parse(html)

    _strip_comments(soup)
    _drop_noise_tags(soup, keep_images=keep_images)
    _clean_attributes(soup, keep_links=keep_links, keep_images=keep_images)
    _unwrap_non_semantic(soup, keep_links=keep_links)
    _collapse_whitespace(soup)
    _strip_list_bullets(soup)
    _collapse_breaks(soup)
    _remove_empty(soup)

    return _serialize(soup)


# --------------------------------------------------------------------------- #
# Parsing / serialization
# --------------------------------------------------------------------------- #

def _parse(html: str) -> BeautifulSoup:
    try:
        return BeautifulSoup(html, "lxml")
    except Exception:
        return BeautifulSoup(html, "html.parser")


def _serialize(soup: BeautifulSoup) -> str:
    # Emit only the body's children so we never leak <html>/<head>/DOCTYPE.
    root = soup.body or soup
    out = "".join(str(child) for child in root.children)
    # Tidy whitespace between block tags without touching inline runs.
    out = re.sub(r">[ \t]+<", "><", out)
    out = re.sub(r"\n[ \t]*\n+", "\n", out)
    out = re.sub(r"[ \t]{2,}", " ", out)
    return out.strip()


# --------------------------------------------------------------------------- #
# Cleaning passes
# --------------------------------------------------------------------------- #

def _strip_comments(soup: BeautifulSoup) -> None:
    # Removes plain comments and Word's conditional comments (<!--[if ...]>).
    for comment in soup.find_all(string=lambda s: isinstance(s, Comment)):
        comment.extract()


def _drop_noise_tags(soup: BeautifulSoup, *, keep_images: bool) -> None:
    for tag in list(soup.find_all(True)):
        if tag.parent is None:  # already removed with an ancestor
            continue
        name = tag.name.lower()
        # Namespaced Word/VML tags (o:p, v:shape, w:sdt, m:oMath, ...).
        if ":" in name or name in _DROP_TAGS:
            tag.decompose()
        elif name == "img" and not keep_images:
            tag.decompose()


def _clean_attributes(
    soup: BeautifulSoup, *, keep_links: bool, keep_images: bool
) -> None:
    for tag in soup.find_all(True):
        name = tag.name.lower()
        allowed = _KEEP_ATTRS.get(name, set())
        kept: dict[str, object] = {}
        for attr, value in tag.attrs.items():
            if attr not in allowed:
                continue
            if attr in ("rowspan", "colspan"):
                span = _as_int(value)
                # Keep spans only when they actually span (>1); a stray
                # rowspan="1" is noise.
                if span > 1:
                    kept[attr] = str(span)
            elif attr == "src":
                v = str(value).strip()
                if keep_images and v and not v.lower().startswith("data:"):
                    kept[attr] = v
            elif attr == "href":
                v = str(value).strip()
                if keep_links and v and not v.lower().startswith("javascript:"):
                    kept[attr] = v
            elif attr == "alt":
                v = str(value).strip()
                if v:
                    kept[attr] = v
        tag.attrs = kept


def _unwrap_non_semantic(soup: BeautifulSoup, *, keep_links: bool) -> None:
    # Document order => parents precede children, so unwrapping a wrapper still
    # lets us reach (and unwrap) nested wrappers later in the same pass.
    for tag in list(soup.find_all(True)):
        if tag.parent is None:
            continue
        name = tag.name.lower()
        if name not in _KEEP_TAGS:
            tag.unwrap()
        elif name == "a" and (not keep_links or "href" not in tag.attrs):
            # A link with no usable href is just an anchor name — drop the tag.
            tag.unwrap()


def _collapse_whitespace(soup: BeautifulSoup) -> None:
    for text in list(soup.find_all(string=True)):
        if _within(text, _PREFORMATTED):
            continue
        collapsed = re.sub(r"[\s ]+", " ", str(text))
        if collapsed != str(text):
            text.replace_with(NavigableString(collapsed))


def _strip_list_bullets(soup: BeautifulSoup) -> None:
    # Word fragments the bullet across text nodes ("\u00b7", nbsp, "text").
    # Consume bullet-only and whitespace-only leading nodes, then left-strip
    # the first real text node.
    for li in soup.find_all("li"):
        for node in list(li.find_all(string=True)):
            if _within(node, _PREFORMATTED):
                break
            s = str(node)
            if not s.strip():
                node.replace_with(NavigableString(""))
                continue
            new = _BULLET_RE.sub("", s)
            if not new.strip():
                node.replace_with(NavigableString(""))
                continue
            new = new.lstrip()
            if new != s:
                node.replace_with(NavigableString(new))
            break

def _collapse_breaks(soup: BeautifulSoup) -> None:
    # Drop <br> runs and leading/trailing <br> inside a block.
    for br in soup.find_all("br"):
        prev = br.previous_sibling
        while isinstance(prev, NavigableString) and not prev.strip():
            prev = prev.previous_sibling
        nxt = br.next_sibling
        while isinstance(nxt, NavigableString) and not nxt.strip():
            nxt = nxt.next_sibling
        if (prev is None or getattr(prev, "name", None) == "br") or nxt is None:
            br.decompose()


def _remove_empty(soup: BeautifulSoup) -> None:
    # Iterate to a fixed point: emptying a child can empty its parent.
    while True:
        removed = False
        for tag in list(soup.find_all(True)):
            if tag.parent is None:
                continue
            if _is_empty(tag):
                tag.decompose()
                removed = True
        if not removed:
            break


# --------------------------------------------------------------------------- #
# Helpers
# --------------------------------------------------------------------------- #

def _is_empty(tag) -> bool:
    name = tag.name.lower()
    if name in _VOID_OK:
        # Table cells stay even when empty — they hold a position in the grid.
        return False
    if tag.get_text(strip=True):
        return False
    # Keep wrappers that still carry structural / media descendants.
    if tag.find(["img", "br", "td", "th", "table", "hr"]):
        return False
    return True


def _within(node, names: set[str]) -> bool:
    parent = node.parent
    while parent is not None:
        if getattr(parent, "name", None) in names:
            return True
        parent = parent.parent
    return False


def _as_int(value: object) -> int:
    try:
        return int(str(value).strip())
    except (TypeError, ValueError):
        return 1
