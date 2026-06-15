# cleaner (Go)

Go port of the Python `cleaner.reduce_html_token`. Reduces the token footprint
of DOCX-converted HTML while **preserving table structure** — `rowspan` /
`colspan` are kept verbatim and never normalised (no span expansion/merging,
which would itself blow up token counts).

Single entry point:

```go
import "github.com/mruruk/doc-storage/cleaner-go"

clean := cleaner.ReduceHTMLToken(messyHTML)
clean := cleaner.ReduceHTMLToken(messyHTML, cleaner.KeepImages(true), cleaner.KeepLinks(false))
```

## What it does

- **Drops with content:** `<style>/<script>/<head>/<meta>`, namespaced Word/VML
  tags (`o:p`, `v:shape`, `w:sdt`, …), `<colgroup>/<col>`, conditional and plain
  comments.
- **Unwraps (keeps text, drops tag):** `<span>`, `<font>`, `<div>`, `<section>`
  and any tag outside the semantic whitelist.
- **Whitelists attributes:** `td`/`th` keep `rowspan`/`colspan` **only when > 1**
  (`rowspan="1"` is dropped as noise); `a` keeps `href`; `img` keeps `alt`/`src`
  (data-URI sources always dropped). Everything else — `style`, `class`, `lang`,
  `width`, `valign`, `bgcolor`, `mso-*` — is removed.
- **Text:** collapses whitespace and `&nbsp;`, strips leading list-bullet glyphs
  (including Word's fragmented bullets), removes empty `<p>/<li>/<ul>` and `<br>`
  runs.

## Options

| Option | Default | Effect |
|--------|---------|--------|
| `KeepLinks(bool)` | `true` | keep `<a href>`; `false` unwraps links to text |
| `KeepImages(bool)` | `false` | keep `<img>` (data-URI src always dropped) |

## Notes

- Dependency: `golang.org/x/net/html` only.
- The HTML5 parser inserts an implicit `<tbody>` around bare `<tr>` rows — this
  is valid HTML and kept; it's the only structural difference from the Python
  output, which uses a more literal parser.
- Idempotent: `ReduceHTMLToken(ReduceHTMLToken(x)) == ReduceHTMLToken(x)`.

## Test

```bash
go test ./...
```
