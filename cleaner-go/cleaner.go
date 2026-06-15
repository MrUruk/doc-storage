// Package cleaner reduces the token footprint of DOCX-converted HTML.
//
// Word / LibreOffice "Save as HTML" output is full of noise that wastes model
// context and breaks naive chunking: inline style soup, class="MsoNormal",
// mso-* properties, <span>/<font>/<div> wrappers, <o:p> markers, conditional
// comments, namespaced VML tags, &nbsp; runs and empty paragraphs.
//
// ReduceHTMLToken strips all of that while preserving table structure —
// including complex rowspan / colspan — without normalising (expanding /
// merging) the tables, which would itself blow up the token count.
//
// This is a Go port of the Python cleaner.reduce_html_token. It depends only on
// golang.org/x/net/html.
//
//	clean := cleaner.ReduceHTMLToken(messyHTML)
//	clean := cleaner.ReduceHTMLToken(messyHTML, cleaner.KeepImages(true))
package cleaner

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Option configures ReduceHTMLToken.
type Option func(*options)

type options struct {
	keepLinks  bool
	keepImages bool
}

// KeepLinks controls whether <a href> anchors are kept (true, default) or
// unwrapped to their text (false).
func KeepLinks(v bool) Option { return func(o *options) { o.keepLinks = v } }

// KeepImages controls whether <img> is kept. Data-URI sources are always
// dropped regardless (base64 images are huge). Default false — most DOCX images
// are decorative and bloat the context.
func KeepImages(v bool) Option { return func(o *options) { o.keepImages = v } }

// Tags removed together with their content — pure noise from Word exports.
var dropTags = set(
	"style", "script", "head", "meta", "link", "title", "base",
	"xml", "o:p", "colgroup", "col", "button", "input", "select",
	"textarea", "iframe", "noscript",
)

// Structural / semantic tags we keep. Anything not here (span, font, div,
// section, unknown Word junk, ...) is unwrapped: its text is kept, the tag
// dropped. Table tags are all here so the grid survives untouched.
var keepTags = set(
	"table", "thead", "tbody", "tfoot", "tr", "td", "th", "caption",
	"p", "br", "hr",
	"h1", "h2", "h3", "h4", "h5", "h6",
	"ul", "ol", "li",
	"strong", "b", "em", "i", "u", "sup", "sub",
	"blockquote", "pre", "code", "a", "img",
)

// Per-tag attribute whitelist. Everything else (style, class, lang, width,
// valign, bgcolor, mso-*, ...) is dropped.
var keepAttrs = map[string]map[string]bool{
	"td":  set("rowspan", "colspan"),
	"th":  set("rowspan", "colspan"),
	"a":   set("href"),
	"img": set("alt", "src"),
}

// Tags that are legitimately empty (they don't need text to be meaningful).
var voidOK = set("br", "hr", "img", "td", "th")

// Tags whose text content is significant verbatim (not whitespace-collapsed).
var preformatted = set("pre", "code")

// Elements that keep a wrapper alive even when it holds no text.
var structuralChild = set("img", "br", "td", "th", "table", "hr")

const spaceClass = `[\t\n\v\f\r\x{0085}\p{Zs}]`

var (
	spaceRe       = regexp.MustCompile(spaceClass + `+`)
	bulletGlyphRe = regexp.MustCompile(`^` + spaceClass + `*[\x{2022}\x{00b7}\x{25aa}\x{25e6}\x{2023}\x{2219}]+` + spaceClass + `*`)
	bulletORe     = regexp.MustCompile(`^` + spaceClass + `*o` + spaceClass + `+`)
	gtSpacesLtRe  = regexp.MustCompile(`>[ \t]+<`)
	blankLinesRe  = regexp.MustCompile(`\n[ \t]*\n+`)
	manySpacesRe  = regexp.MustCompile(`[ \t]{2,}`)
)

// ReduceHTMLToken returns a de-noised copy of the given DOCX-converted HTML
// (a fragment or a full document).
func ReduceHTMLToken(htmlStr string, opts ...Option) string {
	if strings.TrimSpace(htmlStr) == "" {
		return ""
	}
	o := options{keepLinks: true, keepImages: false}
	for _, opt := range opts {
		opt(&o)
	}

	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return ""
	}
	body := findBody(doc)
	if body == nil {
		return ""
	}

	stripComments(body)
	dropNoiseTags(body, o)
	cleanAttributes(body, o)
	unwrapNonSemantic(body, o)
	collapseWhitespace(body)
	stripListBullets(body)
	collapseBreaks(body)
	removeEmpty(body)

	return serialize(body)
}

// --------------------------------------------------------------------------- //
// Cleaning passes
// --------------------------------------------------------------------------- //

func stripComments(root *html.Node) {
	for _, n := range collect(root, func(n *html.Node) bool { return n.Type == html.CommentNode }) {
		remove(n)
	}
}

func dropNoiseTags(root *html.Node, o options) {
	for _, n := range elements(root) {
		if n.Parent == nil {
			continue
		}
		name := n.Data
		if strings.Contains(name, ":") || dropTags[name] {
			remove(n)
		} else if name == "img" && !o.keepImages {
			remove(n)
		}
	}
}

func cleanAttributes(root *html.Node, o options) {
	for _, n := range elements(root) {
		allowed := keepAttrs[n.Data]
		if len(allowed) == 0 {
			n.Attr = nil
			continue
		}
		kept := n.Attr[:0:0]
		for _, a := range n.Attr {
			if !allowed[a.Key] {
				continue
			}
			switch a.Key {
			case "rowspan", "colspan":
				// Keep spans only when they actually span (>1); a stray
				// rowspan="1" is noise.
				if span := asInt(a.Val); span > 1 {
					kept = append(kept, html.Attribute{Key: a.Key, Val: strconv.Itoa(span)})
				}
			case "src":
				v := strings.TrimSpace(a.Val)
				if o.keepImages && v != "" && !strings.HasPrefix(strings.ToLower(v), "data:") {
					kept = append(kept, html.Attribute{Key: a.Key, Val: v})
				}
			case "href":
				v := strings.TrimSpace(a.Val)
				if o.keepLinks && v != "" && !strings.HasPrefix(strings.ToLower(v), "javascript:") {
					kept = append(kept, html.Attribute{Key: a.Key, Val: v})
				}
			case "alt":
				if v := strings.TrimSpace(a.Val); v != "" {
					kept = append(kept, html.Attribute{Key: a.Key, Val: v})
				}
			}
		}
		n.Attr = kept
	}
}

func unwrapNonSemantic(root *html.Node, o options) {
	// Document order => parents precede children, so unwrapping a wrapper still
	// lets us reach (and unwrap) nested wrappers later in the same pass.
	for _, n := range elements(root) {
		if n.Parent == nil {
			continue
		}
		name := n.Data
		if !keepTags[name] {
			unwrap(n)
		} else if name == "a" && (!o.keepLinks || !hasAttr(n, "href")) {
			// A link with no usable href is just an anchor name — drop the tag.
			unwrap(n)
		}
	}
}

func collapseWhitespace(root *html.Node) {
	for _, t := range collect(root, func(n *html.Node) bool { return n.Type == html.TextNode }) {
		if within(t, preformatted) {
			continue
		}
		t.Data = spaceRe.ReplaceAllString(t.Data, " ")
	}
}

func stripListBullets(root *html.Node) {
	// Word fragments the bullet across text nodes ("·", nbsp, "text"). Consume
	// bullet-only and whitespace-only leading nodes, then left-strip the first
	// real text node.
	for _, li := range collect(root, isElement("li")) {
		for _, node := range collect(li, func(n *html.Node) bool { return n.Type == html.TextNode }) {
			if within(node, preformatted) {
				break
			}
			s := node.Data
			if strings.TrimSpace(s) == "" {
				node.Data = ""
				continue
			}
			next := bulletGlyphRe.ReplaceAllString(s, "")
			if next == s {
				next = bulletORe.ReplaceAllString(s, "")
			}
			if strings.TrimSpace(next) == "" {
				node.Data = ""
				continue
			}
			node.Data = strings.TrimLeftFunc(next, unicode.IsSpace)
			break
		}
	}
}

func collapseBreaks(root *html.Node) {
	for _, br := range collect(root, isElement("br")) {
		if br.Parent == nil {
			continue
		}
		prev := prevMeaningful(br)
		next := nextMeaningful(br)
		if prev == nil || (prev.Type == html.ElementNode && prev.Data == "br") || next == nil {
			remove(br)
		}
	}
}

func removeEmpty(root *html.Node) {
	// Iterate to a fixed point: emptying a child can empty its parent.
	for {
		removed := false
		for _, n := range elements(root) {
			if n.Parent == nil {
				continue
			}
			if isEmpty(n) {
				remove(n)
				removed = true
			}
		}
		if !removed {
			break
		}
	}
}

func isEmpty(n *html.Node) bool {
	if voidOK[n.Data] {
		// Table cells stay even when empty — they hold a position in the grid.
		return false
	}
	if strings.TrimSpace(textOf(n)) != "" {
		return false
	}
	// Keep wrappers that still carry structural / media descendants.
	if hasDescendantElement(n, structuralChild) {
		return false
	}
	return true
}

// --------------------------------------------------------------------------- //
// Serialization
// --------------------------------------------------------------------------- //

func serialize(body *html.Node) string {
	var sb strings.Builder
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		_ = html.Render(&sb, c)
	}
	out := sb.String()
	// Tidy whitespace between block tags without touching inline runs.
	out = gtSpacesLtRe.ReplaceAllString(out, "><")
	out = blankLinesRe.ReplaceAllString(out, "\n")
	out = manySpacesRe.ReplaceAllString(out, " ")
	return strings.TrimSpace(out)
}

// --------------------------------------------------------------------------- //
// Tree helpers
// --------------------------------------------------------------------------- //

func findBody(n *html.Node) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == atom.Body {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if b := findBody(c); b != nil {
			return b
		}
	}
	return nil
}

// collect returns, in document order, every descendant of root satisfying pred.
// The result is a snapshot, so callers may mutate the tree while iterating.
func collect(root *html.Node, pred func(*html.Node) bool) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if pred(c) {
				out = append(out, c)
			}
			walk(c)
		}
	}
	walk(root)
	return out
}

func elements(root *html.Node) []*html.Node {
	return collect(root, func(n *html.Node) bool { return n.Type == html.ElementNode })
}

func isElement(name string) func(*html.Node) bool {
	return func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == name }
}

func remove(n *html.Node) {
	if n.Parent != nil {
		n.Parent.RemoveChild(n)
	}
}

// unwrap replaces n with its children in place.
func unwrap(n *html.Node) {
	parent := n.Parent
	if parent == nil {
		return
	}
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		n.RemoveChild(c)
		parent.InsertBefore(c, n)
		c = next
	}
	parent.RemoveChild(n)
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}

func within(n *html.Node, names map[string]bool) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && names[p.Data] {
			return true
		}
	}
	return false
}

func textOf(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(m *html.Node) {
		if m.Type == html.TextNode {
			sb.WriteString(m.Data)
		}
		for c := m.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

func hasDescendantElement(n *html.Node, names map[string]bool) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && names[c.Data] {
			return true
		}
		if hasDescendantElement(c, names) {
			return true
		}
	}
	return false
}

func prevMeaningful(n *html.Node) *html.Node {
	p := n.PrevSibling
	for p != nil && p.Type == html.TextNode && strings.TrimSpace(p.Data) == "" {
		p = p.PrevSibling
	}
	return p
}

func nextMeaningful(n *html.Node) *html.Node {
	s := n.NextSibling
	for s != nil && s.Type == html.TextNode && strings.TrimSpace(s.Data) == "" {
		s = s.NextSibling
	}
	return s
}

func asInt(v string) int {
	i, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 1
	}
	return i
}

func set(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it] = true
	}
	return m
}
