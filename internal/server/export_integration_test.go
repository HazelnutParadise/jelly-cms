package server

// Ticket 09 export contract (docs/tickets/09-editor.md, 匯出規則 and API table):
//
//	GET /api/admin/posts/:id/export?format=jmd|md|html|pdf&images=1&frontmatter=1&history=1
//
// Owner or editor only; another author's post is 404; an unknown format is 400; pdf is 501
// pdf_engine_unavailable until a PDF engine exists (the first version prints from the browser). jmd is
// the stored source verbatim, optionally with YAML front matter. md follows the flattening rules. html
// is a standalone document with content.css inlined; images=1 returns a zip with relative image paths.
// Runs through the real route table on the dedicated PostgreSQL database; skipped unless
// JELLY_TEST_POSTGRES=1. Written before the export exists; do not edit them to match an implementation.

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

func edtExport(h *accessHarness, token string, id uint, query string) accessResult {
	return h.request(http.MethodGet, "/api/admin/posts/"+edtID(id)+"/export?"+query, "", token, "", "")
}

func TestExportJmdVerbatimWithFrontMatter(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "edt-export-jmd")
	token := h.token(author)

	source := "## 原樣{reveal=fade-up}\n\n:::hero {bg=mint}\n歡迎  \n:::\n\n$$x^2$$\n"
	id, _ := edtCreate(t, h, token, map[string]any{"title": "匯出標題", "source": source})

	plain := edtExport(h, token, id, "format=jmd")
	h.mustNoPanic(plain, "export jmd")
	if plain.code != http.StatusOK {
		t.Fatalf("export jmd returned %d body=%q", plain.code, plain.body)
	}
	if plain.body != source {
		t.Errorf("export jmd must return the source verbatim\n got: %q\nwant: %q", plain.body, source)
	}

	withFront := edtExport(h, token, id, "format=jmd&frontmatter=1")
	h.mustNoPanic(withFront, "export jmd with front matter")
	if withFront.code != http.StatusOK {
		t.Fatalf("export jmd with front matter returned %d body=%q", withFront.code, withFront.body)
	}
	if !strings.HasPrefix(withFront.body, "---\n") {
		t.Errorf("front matter must open with ---: %q", withFront.body)
	}
	if !strings.Contains(withFront.body, "匯出標題") {
		t.Errorf("front matter must carry the title: %q", withFront.body)
	}
	if !strings.HasSuffix(withFront.body, source) {
		t.Errorf("front matter export must end with the verbatim source")
	}
}

func TestExportMdFlattens(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "edt-export-md")
	token := h.token(author)

	source := "## 標題{reveal=fade-up}\n\n> [!NOTE]\n> 注意\n\n:::columns\n::: col\n左\n:::\n::: col\n右\n:::\n:::\n"
	id, _ := edtCreate(t, h, token, map[string]any{"title": "攤平", "source": source})

	r := edtExport(h, token, id, "format=md")
	h.mustNoPanic(r, "export md")
	if r.code != http.StatusOK {
		t.Fatalf("export md returned %d body=%q", r.code, r.body)
	}
	if !strings.Contains(r.body, "## 標題") || strings.Contains(r.body, "{reveal") {
		t.Errorf("md export must keep the heading and drop its attributes: %q", r.body)
	}
	if !strings.Contains(r.body, "> **Note**") {
		t.Errorf("md export must turn the callout into a Note quote: %q", r.body)
	}
	if strings.Contains(r.body, ":::") {
		t.Errorf("md export must not contain container syntax: %q", r.body)
	}
	if !strings.Contains(r.body, "左") || !strings.Contains(r.body, "右") {
		t.Errorf("md export must keep the column text: %q", r.body)
	}
}

func TestExportHtmlStandaloneAndZipWithImages(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "edt-export-html")
	token := h.token(author)

	imageBytes := []byte("\x89PNG\r\n\x1a\nedt-test-image")
	if err := os.MkdirAll(filepath.Join("data", "uploads"), 0o755); err != nil {
		t.Fatalf("create uploads directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join("data", "uploads", "a.png"), imageBytes, 0o644); err != nil {
		t.Fatalf("write fixture image: %v", err)
	}

	source := "# 圖片文章\n\n![說明](/uploads/a.png)\n\n內文\n"
	id, _ := edtCreate(t, h, token, map[string]any{"title": "圖片文章", "source": source})

	page := edtExport(h, token, id, "format=html")
	h.mustNoPanic(page, "export html")
	if page.code != http.StatusOK {
		t.Fatalf("export html returned %d body=%q", page.code, page.body)
	}
	lower := strings.ToLower(page.body)
	if !strings.Contains(lower, "<!doctype html") {
		t.Errorf("html export must be a standalone document: %.200q", page.body)
	}
	if !strings.Contains(page.body, "<style") {
		t.Errorf("html export must inline content.css in a <style> element")
	}
	if strings.Contains(page.body, `<link rel="stylesheet"`) {
		t.Errorf("html export must not link an external stylesheet")
	}

	archive := edtExport(h, token, id, "format=html&images=1")
	h.mustNoPanic(archive, "export html zip")
	if archive.code != http.StatusOK {
		t.Fatalf("export html with images returned %d body=%q", archive.code, archive.body)
	}
	if ct := archive.header.Get(echo.HeaderContentType); !strings.HasPrefix(ct, "application/zip") {
		t.Fatalf("images=1 content type = %q, want application/zip", ct)
	}

	reader, err := zip.NewReader(bytes.NewReader([]byte(archive.body)), int64(len(archive.body)))
	if err != nil {
		t.Fatalf("images=1 response is not a zip: %v", err)
	}
	var htmlEntry, imageEntry *zip.File
	for _, f := range reader.File {
		if strings.HasPrefix(f.Name, "/") || strings.Contains(f.Name, "..") {
			t.Errorf("zip entry %q must be a relative path", f.Name)
		}
		switch {
		case strings.HasSuffix(f.Name, ".html"):
			htmlEntry = f
		case strings.HasSuffix(f.Name, "a.png"):
			imageEntry = f
		}
	}
	if htmlEntry == nil {
		t.Fatalf("zip has no html document")
	}
	if imageEntry == nil {
		t.Fatalf("zip has no a.png entry")
	}

	rc, err := htmlEntry.Open()
	if err != nil {
		t.Fatalf("open html entry: %v", err)
	}
	htmlBody, _ := io.ReadAll(rc)
	rc.Close()
	if strings.Contains(string(htmlBody), `src="/uploads/`) {
		t.Errorf("zip html must rewrite /uploads paths to relative paths")
	}
	if !strings.Contains(string(htmlBody), "a.png") {
		t.Errorf("zip html must still reference the image")
	}

	rc, err = imageEntry.Open()
	if err != nil {
		t.Fatalf("open image entry: %v", err)
	}
	gotImage, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(gotImage, imageBytes) {
		t.Errorf("zip image bytes differ from data/uploads/a.png")
	}
}

func TestExportPdfUnavailable501(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "edt-export-pdf")
	token := h.token(author)

	id, _ := edtCreate(t, h, token, map[string]any{"title": "列印", "source": "內文"})
	r := edtExport(h, token, id, "format=pdf")
	h.mustNoPanic(r, "export pdf")
	if r.code != http.StatusNotImplemented {
		t.Errorf("export pdf = %d, want 501 (body %q)", r.code, r.body)
	}
	if !strings.Contains(r.body, "pdf_engine_unavailable") {
		t.Errorf("export pdf body must name pdf_engine_unavailable: %q", r.body)
	}
}

func TestExportOtherAuthors404AndBadFormat400(t *testing.T) {
	h := newAccessHarness(t)
	owner := h.user("author", "edt-export-owner")
	other := h.user("author", "edt-export-stranger")
	editor := h.user("editor", "edt-export-editor")
	ownerToken := h.token(owner)

	id, _ := edtCreate(t, h, ownerToken, map[string]any{"title": "匯出權限", "source": "內文"})

	if r := edtExport(h, h.token(other), id, "format=md"); r.code != http.StatusNotFound {
		t.Errorf("another author's export = %d, want 404 (body %q)", r.code, r.body)
	}
	if r := edtExport(h, h.token(editor), id, "format=md"); r.code != http.StatusOK {
		t.Errorf("editor export = %d, want 200 (body %q)", r.code, r.body)
	}
	if r := edtExport(h, ownerToken, id, "format=docx"); r.code != http.StatusBadRequest {
		t.Errorf("unknown format = %d, want 400 (body %q)", r.code, r.body)
	}
}
