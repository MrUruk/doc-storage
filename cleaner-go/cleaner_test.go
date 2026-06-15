package cleaner

import (
	"strings"
	"testing"
)

const messy = `<html xmlns:o="urn:schemas-microsoft-com:office:office"
xmlns:w="urn:schemas-microsoft-com:office:word">
<head><meta charset="utf-8"><style>p.MsoNormal{margin:0}</style>
<!--[if gte mso 9]><xml><w:WordDocument/></xml><![endif]--></head>
<body lang=RU style='tab-interval:35.4pt'>
<div class=WordSection1>
<p class=MsoNormal style='margin:0cm'><span style='font-size:12.0pt;mso-fareast-language:RU'>
<b>Наряд&nbsp;&nbsp; на&nbsp;обновление</b></span><o:p></o:p></p>
<p class=MsoNormal><span lang=EN-US>Версия ПО:&nbsp;2.4.1</span></p>
<p class=MsoNormal>&nbsp;</p>
<table class=MsoTableGrid border=1 cellspacing=0 cellpadding=0 style='border-collapse:collapse'>
 <colgroup><col width=100><col width=200></colgroup>
 <tr style='height:20pt'>
  <td rowspan=2 valign=top style='border:solid'><p class=MsoNormal><span>Этап</span></p></td>
  <td colspan=2 width=300 style='background:yellow'><b>Параметры</b></td>
 </tr>
 <tr>
  <td rowspan="1" style='width:50pt'><span style='mso-list:Ignore'>Версия</span></td>
  <td>2.4.1</td>
 </tr>
 <tr>
  <td>Миграция</td>
  <td colspan=2><font face=Calibri>kubectl apply -f migrate-job.yaml</font></td>
 </tr>
</table>
<ul style='margin:0'>
 <li class=MsoListParagraph><span style='mso-list:Ignore'>·<span>&nbsp;</span></span>проверить логи</li>
 <li>откатить при ошибке</li>
</ul>
<p>&nbsp;</p>
<p><o:p></o:p></p>
</div>
</body></html>`

func TestReduceHTMLToken(t *testing.T) {
	clean := ReduceHTMLToken(messy)
	t.Logf("cleaned:\n%s", clean)
	t.Logf("before=%d after=%d reduction=%.0f%%",
		len(messy), len(clean), 100*(1-float64(len(clean))/float64(len(messy))))

	mustContain := []string{
		`rowspan="2"`, `colspan="2"`,
		"kubectl apply -f migrate-job.yaml",
		"<li>проверить логи</li>", "<li>откатить при ошибке</li>",
	}
	for _, s := range mustContain {
		if !strings.Contains(clean, s) {
			t.Errorf("expected output to contain %q", s)
		}
	}

	mustNotContain := []string{
		`rowspan="1"`, "style", "class", "mso", "MsoNormal",
		"<span", "<font", "o:p", "<div", "<colgroup", "<style",
		"WordDocument", "&nbsp;", "·", // bullet glyph
	}
	lc := strings.ToLower(clean)
	for _, s := range mustNotContain {
		if strings.Contains(lc, strings.ToLower(s)) {
			t.Errorf("expected output to NOT contain %q", s)
		}
	}

	if got := strings.Count(clean, "<table"); got != 1 {
		t.Errorf("table count = %d, want 1", got)
	}
	if got := strings.Count(clean, "<tr"); got != 3 {
		t.Errorf("tr count = %d, want 3", got)
	}
	if got := strings.Count(clean, "<td"); got != 6 {
		t.Errorf("td count = %d, want 6", got)
	}
}

func TestIdempotent(t *testing.T) {
	once := ReduceHTMLToken(messy)
	twice := ReduceHTMLToken(once)
	if once != twice {
		t.Errorf("not idempotent:\n once=%q\ntwice=%q", once, twice)
	}
}

func TestEdgeCases(t *testing.T) {
	if got := ReduceHTMLToken(""); got != "" {
		t.Errorf("empty input => %q, want \"\"", got)
	}
	if got := ReduceHTMLToken("   \n  "); got != "" {
		t.Errorf("whitespace input => %q, want \"\"", got)
	}
	if got := ReduceHTMLToken("<p>plain</p>"); got != "<p>plain</p>" {
		t.Errorf("plain => %q, want <p>plain</p>", got)
	}
}

func TestComplexSpansPreserved(t *testing.T) {
	// Irregular grid: a cell spanning 2 rows beside cells spanning 3 cols.
	in := `<table><tbody>
	  <tr><td rowspan="3" style="x">A</td><td colspan="3" class="y">B</td></tr>
	  <tr><td>c1</td><td colspan="2">c23</td></tr>
	  <tr><td>d1</td><td>d2</td><td>d3</td></tr>
	</tbody></table>`
	clean := ReduceHTMLToken(in)
	for _, s := range []string{`rowspan="3"`, `colspan="3"`, `colspan="2"`} {
		if !strings.Contains(clean, s) {
			t.Errorf("lost span %q in %q", s, clean)
		}
	}
	if strings.Contains(clean, "style") || strings.Contains(clean, "class") {
		t.Errorf("attrs not stripped: %q", clean)
	}
	// No normalisation: cell counts are untouched (1+1, 1+1, 3).
	if got := strings.Count(clean, "<td"); got != 7 {
		t.Errorf("td count = %d, want 7 (no normalisation)", got)
	}
}

func TestLinkAndImageOptions(t *testing.T) {
	in := `<p><a href="https://x" style="c">link</a> <a name="anchor">nm</a>
	       <img src="data:image/png;base64,AAAA" alt="pic"></p>`

	def := ReduceHTMLToken(in)
	if !strings.Contains(def, `<a href="https://x">link</a>`) {
		t.Errorf("href link not kept: %q", def)
	}
	if strings.Contains(def, "anchor") || strings.Contains(def, "<a name") {
		t.Errorf("anchor-only link should be unwrapped: %q", def)
	}
	if strings.Contains(def, "<img") {
		t.Errorf("image should be dropped by default: %q", def)
	}

	noLinks := ReduceHTMLToken(in, KeepLinks(false))
	if strings.Contains(noLinks, "<a") {
		t.Errorf("links should be unwrapped with KeepLinks(false): %q", noLinks)
	}
	if !strings.Contains(noLinks, "link") {
		t.Errorf("link text should remain: %q", noLinks)
	}

	keepImg := ReduceHTMLToken(in, KeepImages(true))
	if !strings.Contains(keepImg, "<img") || !strings.Contains(keepImg, `alt="pic"`) {
		t.Errorf("image with alt should be kept: %q", keepImg)
	}
	if strings.Contains(keepImg, "data:") {
		t.Errorf("data-URI src must be dropped even when keeping images: %q", keepImg)
	}
}
