// Package docxprops extracts core properties from a .docx (an OOXML zip),
// mirroring docx_utils.extract_modified_date.
package docxprops

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"time"
)

// coreProperties matches the subset of docProps/core.xml we need.
type coreProperties struct {
	Modified string `xml:"http://purl.org/dc/terms/ modified"`
}

// ExtractModifiedDate returns the dcterms:modified timestamp from a .docx.
// Returns an error if the property is missing or unparseable; callers fall back
// to the current time, as the Python service does.
func ExtractModifiedDate(docxBytes []byte) (time.Time, error) {
	zr, err := zip.NewReader(bytes.NewReader(docxBytes), int64(len(docxBytes)))
	if err != nil {
		return time.Time{}, fmt.Errorf("open docx zip: %w", err)
	}

	for _, f := range zr.File {
		if f.Name != "docProps/core.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return time.Time{}, fmt.Errorf("open core.xml: %w", err)
		}
		defer rc.Close()

		var buf bytes.Buffer
		if _, err := buf.ReadFrom(rc); err != nil {
			return time.Time{}, fmt.Errorf("read core.xml: %w", err)
		}

		var cp coreProperties
		if err := xml.Unmarshal(buf.Bytes(), &cp); err != nil {
			return time.Time{}, fmt.Errorf("parse core.xml: %w", err)
		}
		if cp.Modified == "" {
			return time.Time{}, fmt.Errorf("no dcterms:modified in core properties")
		}
		// OOXML uses RFC3339 (e.g. 2024-03-15T10:00:00Z).
		t, err := time.Parse(time.RFC3339, cp.Modified)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse modified date %q: %w", cp.Modified, err)
		}
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("docProps/core.xml not found")
}
