package docxprops

import (
	"archive/zip"
	"bytes"
	"testing"
	"time"
)

func buildDocx(t *testing.T, coreXML string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if coreXML != "" {
		w, err := zw.Create("docProps/core.xml")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(coreXML)); err != nil {
			t.Fatal(err)
		}
	}
	// an unrelated entry so the zip isn't trivially the core file
	w, _ := zw.Create("word/document.xml")
	_, _ = w.Write([]byte("<document/>"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const coreWithModified = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties"
  xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/">
  <dc:title>Наряд</dc:title>
  <dcterms:modified xsi:type="dcterms:W3CDTF">2024-03-15T10:30:00Z</dcterms:modified>
</cp:coreProperties>`

func TestExtractModifiedDate(t *testing.T) {
	got, err := ExtractModifiedDate(buildDocx(t, coreWithModified))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2024, 3, 15, 10, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExtractModifiedDateMissing(t *testing.T) {
	if _, err := ExtractModifiedDate(buildDocx(t, "")); err == nil {
		t.Error("expected error when core.xml is absent")
	}
	noModified := `<cp:coreProperties xmlns:cp="x" xmlns:dcterms="http://purl.org/dc/terms/"></cp:coreProperties>`
	if _, err := ExtractModifiedDate(buildDocx(t, noModified)); err == nil {
		t.Error("expected error when dcterms:modified is absent")
	}
}

func TestExtractModifiedDateBadZip(t *testing.T) {
	if _, err := ExtractModifiedDate([]byte("not a zip")); err == nil {
		t.Error("expected error for non-zip input")
	}
}
