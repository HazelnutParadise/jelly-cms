package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/markup"
	"github.com/labstack/echo/v4"
	xhtml "golang.org/x/net/html"
	"gorm.io/gorm"
)

func TestWidgetPublishedVersionsRemainImmutableAndAddressable(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "editor-regression-widget-versions"))
	params := []map[string]any{{"name": "to", "type": "date", "required": true}}
	sourceV1 := `<p>version-one</p><script>window.value=props.to;</script>`
	sourceV2 := `<p>version-two</p><script>window.value=props.to;</script>`
	id, _ := wgtCreate(t, h, token, "version-regression", params, sourceV1)
	wgtPublishOK(t, h, token, id)
	wgtUpdateOK(t, h, token, id, "version-regression", sourceV2, params)
	wgtPublishOK(t, h, token, id)

	for _, tc := range []struct {
		version int
		want    string
		absent  string
	}{{1, "version-one", "version-two"}, {2, "version-two", "version-one"}} {
		r := wgtFrame(h, "version-regression", tc.version, "?to=2027-01-01")
		h.mustNoPanic(r, fmt.Sprintf("version %d frame", tc.version))
		if r.code != http.StatusOK {
			t.Fatalf("version %d frame returned %d, want 200: %s", tc.version, r.code, r.body)
		}
		if !strings.Contains(r.body, tc.want) || strings.Contains(r.body, tc.absent) {
			t.Errorf("version %d frame content does not match immutable source: %s", tc.version, r.body)
		}
	}
}

func TestKaTeXVendorAssetsServedThroughPublicHTTP(t *testing.T) {
	h := newAccessHarness(t)
	root := filepath.Join(repoRoot(t), "web", "vendor", "katex")
	cssPath := filepath.Join(root, "katex.min.css")
	expectedCSS, err := os.ReadFile(cssPath)
	if err != nil {
		t.Fatalf("read vendored CSS %s: %v", cssPath, err)
	}
	fontDir := filepath.Join(root, "fonts")
	fontEntries, err := os.ReadDir(fontDir)
	if err != nil {
		t.Fatalf("read KaTeX font directory %s: %v", fontDir, err)
	}
	fontFiles := 0
	for _, entry := range fontEntries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".woff2") {
			continue
		}
		fontFiles++
		fontName := entry.Name()
		fontPath := filepath.Join(fontDir, fontName)
		fontBytes, readErr := os.ReadFile(fontPath)
		if readErr != nil {
			t.Fatalf("read vendored font %s: %v", fontPath, readErr)
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(fontBytes))
		sourceRef := []byte("fonts/" + fontName)
		if count := bytes.Count(expectedCSS, sourceRef); count == 0 {
			t.Errorf("vendored CSS does not reference existing font %s", fontName)
		} else {
			replacement := []byte("/vendor/katex/fonts/" + fontName + "?v=" + hash)
			expectedCSS = bytes.ReplaceAll(expectedCSS, sourceRef, replacement)
		}
		fontURL := "/vendor/katex/fonts/" + fontName + "?v=" + hash
		response := h.request(http.MethodGet, fontURL, "", "", "", "")
		if response.code != http.StatusOK {
			t.Errorf("anonymous GET %s returned %d, want 200", fontURL, response.code)
			continue
		}
		if !bytes.Equal([]byte(response.body), fontBytes) {
			t.Errorf("anonymous GET %s did not return the corresponding vendored file bytes", fontURL)
		}
	}
	if fontFiles == 0 {
		t.Fatal("no vendored KaTeX .woff2 files found")
	}
	for _, asset := range []struct {
		path string
		file string
		want []byte
	}{
		{path: "/vendor/katex/katex.min.css", file: cssPath, want: expectedCSS},
		{path: "/vendor/katex/katex.min.js", file: filepath.Join(root, "katex.min.js")},
		{path: "/vendor/katex/fonts/KaTeX_Main-Regular.woff2", file: filepath.Join(fontDir, "KaTeX_Main-Regular.woff2")},
	} {
		fileBytes, readErr := os.ReadFile(asset.file)
		if readErr != nil {
			t.Fatalf("read vendored asset %s: %v", asset.file, readErr)
		}
		want := asset.want
		if want == nil {
			want = fileBytes
		}
		response := h.request(http.MethodGet, asset.path, "", "", "", "")
		if response.code != http.StatusOK {
			t.Errorf("anonymous GET %s returned %d, want 200", asset.path, response.code)
			continue
		}
		if !bytes.Equal([]byte(response.body), want) {
			t.Errorf("anonymous GET %s did not return the expected vendored bytes and exact font URL substitutions", asset.path)
		}
	}

	projectFile, err := os.ReadFile(filepath.Join(repoRoot(t), "go.mod"))
	if err != nil {
		t.Fatalf("read project-file traversal sentinel: %v", err)
	}
	for _, path := range []string{
		"/vendor/katex/../../../go.mod",
		"/vendor/katex/%2e%2e/%2e%2e/%2e%2e/go.mod",
		"/vendor/katex/../../../config/config.go",
	} {
		response := h.request(http.MethodGet, path, "", "", "", "")
		if response.code >= http.StatusOK && response.code < http.StatusMultipleChoices {
			t.Errorf("path traversal GET %s returned success status %d", path, response.code)
		}
		if bytes.Contains([]byte(response.body), projectFile) || strings.Contains(response.body, "module github.com/HazelnutParadise/jelly-cms") {
			t.Errorf("path traversal GET %s leaked project-file content", path)
		}
	}
}

func TestCountdownFrameProvidesNoScriptDeadlineAndKeepsSandbox(t *testing.T) {
	h := newAccessHarness(t)
	deadline := "2031-07-08"
	response := wgtFrame(h, "countdown", 1, "?to="+deadline)
	h.mustNoPanic(response, "built-in countdown frame")
	if response.code != http.StatusOK {
		t.Fatalf("built-in countdown frame returned %d: %s", response.code, response.body)
	}
	csp := response.header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "sandbox allow-scripts") || strings.Contains(csp, "allow-same-origin") {
		t.Errorf("countdown CSP must remain sandboxed without same-origin access: %q", csp)
	}
	doc, err := xhtml.Parse(strings.NewReader(response.body))
	if err != nil {
		t.Fatalf("parse countdown frame DOM: %v", err)
	}
	var visible strings.Builder
	var collectVisible func(*xhtml.Node)
	collectVisible = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && (node.Data == "script" || node.Data == "style") {
			return
		}
		if node.Type == xhtml.TextNode {
			visible.WriteString(node.Data)
			visible.WriteByte(' ')
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			collectVisible(child)
		}
	}
	collectVisible(doc)
	if !strings.Contains(visible.String(), deadline) {
		t.Errorf("countdown frame has no visible no-script deadline %q after script/style removal: %q", deadline, visible.String())
	}
	for _, query := range []string{"?to=notadate", "?to=2031-07-08%3Cscript%3E"} {
		bad := wgtFrame(h, "countdown", 1, query)
		if bad.code != http.StatusBadRequest {
			t.Errorf("countdown frame with invalid or malicious date %q returned %d, want 400", query, bad.code)
		}
	}
}

func TestDraftWidgetVersionDoesNotOverridePublishedRenderVersion(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "editor-regression-widget-draft-version"))
	params := []map[string]any{{"name": "to", "type": "date", "required": true}}
	id, _ := wgtCreate(t, h, token, "draft-version-regression", params, `<p>version-one</p>`)
	wgtPublishOK(t, h, token, id)
	wgtUpdateOK(t, h, token, id, "draft-version-regression", `<p>version-two-draft</p>`, params)

	assertRenderedVersion := func(wantVersion int, wantText string) {
		t.Helper()
		widgets, err := publishedWidgetSpecs(h.db)
		if err != nil {
			t.Fatalf("publishedWidgetSpecs: %v", err)
		}
		result, err := markup.Render(t.Context(), `::widget{name=draft-version-regression to=2027-01-01}`, markup.Options{Trust: markup.TrustRich, Format: "jmd", Widgets: widgets})
		if err != nil {
			t.Fatalf("render widget directive: %v", err)
		}
		if !strings.Contains(result.HTML, `data-v="`+fmt.Sprint(wantVersion)+`"`) || !strings.Contains(result.HTML, "/widgets/draft-version-regression/"+fmt.Sprint(wantVersion)+"/frame") {
			t.Errorf("render did not select published v%d: %s", wantVersion, result.HTML)
		}
		frame := wgtFrame(h, "draft-version-regression", wantVersion, "?to=2027-01-01")
		if frame.code != http.StatusOK || !strings.Contains(frame.body, wantText) {
			t.Errorf("v%d frame does not serve expected published source: status=%d body=%s", wantVersion, frame.code, frame.body)
		}
	}
	assertRenderedVersion(1, "version-one")
	wgtPublishOK(t, h, token, id)
	assertRenderedVersion(2, "version-two-draft")
}

func TestPostContentOnlyWritesPreserveNonObjectJSONMetadata(t *testing.T) {
	h := newAccessHarness(t)
	adminToken := h.token(h.user("admin", "editor-regression-meta-values"))
	for _, tc := range []struct {
		name  string
		value string
	}{
		{name: "array", value: `[1,"two",{"three":3}]`},
		{name: "string", value: `"metadata"`},
		{name: "number", value: `17`},
		{name: "boolean", value: `true`},
		{name: "null", value: `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			postResponse := h.request(http.MethodPost, "/api/admin/posts", `{"title":"metadata `+tc.name+`","content":"initial body","meta":`+tc.value+`}`, adminToken, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
			if postResponse.code != http.StatusOK {
				t.Fatalf("POST returned %d: %s", postResponse.code, postResponse.body)
			}
			var created core.Post
			if err := json.Unmarshal([]byte(postResponse.body), &created); err != nil {
				t.Fatalf("decode POST response: %v", err)
			}
			if !jsonEqual(created.Meta, tc.value) {
				t.Errorf("POST response metadata=%s, want %s", created.Meta, tc.value)
			}
			var stored core.Post
			if err := h.db.First(&stored, created.ID).Error; err != nil {
				t.Fatalf("reload POST result: %v", err)
			}
			if !jsonEqual(stored.Meta, tc.value) {
				t.Errorf("POST database metadata=%s, want %s", stored.Meta, tc.value)
			}

			path := fmt.Sprintf("/api/admin/posts/%d", created.ID)
			putResponse := h.request(http.MethodPut, path, `{"content":"updated body","meta":`+tc.value+`}`, adminToken, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
			if putResponse.code != http.StatusOK {
				t.Fatalf("content-only PUT returned %d: %s", putResponse.code, putResponse.body)
			}
			var updated core.Post
			if err := json.Unmarshal([]byte(putResponse.body), &updated); err != nil {
				t.Fatalf("decode content-only PUT response: %v", err)
			}
			if !jsonEqual(updated.Meta, tc.value) {
				t.Errorf("content-only PUT response metadata=%s, want %s", updated.Meta, tc.value)
			}
			stored = core.Post{}
			if err := h.db.First(&stored, created.ID).Error; err != nil {
				t.Fatalf("reload content-only PUT result: %v", err)
			}
			if !jsonEqual(stored.Meta, tc.value) {
				t.Errorf("content-only PUT database metadata=%s, want %s", stored.Meta, tc.value)
			}

			titleResponse := h.request(http.MethodPut, path, `{"title":"title-only `+tc.name+`"}`, adminToken, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
			if titleResponse.code != http.StatusOK {
				t.Fatalf("title-only PUT returned %d: %s", titleResponse.code, titleResponse.body)
			}
			if err := json.Unmarshal([]byte(titleResponse.body), &updated); err != nil {
				t.Fatalf("decode title-only PUT response: %v", err)
			}
			if !jsonEqual(updated.Meta, tc.value) {
				t.Errorf("title-only PUT response metadata=%s, want %s", updated.Meta, tc.value)
			}
			stored = core.Post{}
			if err := h.db.First(&stored, created.ID).Error; err != nil {
				t.Fatalf("reload title-only PUT result: %v", err)
			}
			if !jsonEqual(stored.Meta, tc.value) {
				t.Errorf("title-only PUT database metadata=%s, want %s", stored.Meta, tc.value)
			}
		})
	}
}

func TestLegacyMarkdownExportReturnsStoredSourceVerbatim(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "editor-regression-legacy-md-export")
	token := h.token(author)
	source := "::counter{to=7}\n\n" +
		":::columns\n::: col\nleft\n:::\n::: col\nright\n:::\n:::\n\n" +
		"[Go](/go){.btn reveal=fade-up}\n\n" +
		"```html\n<strong>literal</strong>\n```\n"
	id, _ := edtCreate(t, h, token, map[string]any{"title": "Legacy Markdown export", "source": source})
	if err := h.db.Model(&core.Post{}).Where("id = ?", id).Update("format", "md").Error; err != nil {
		t.Fatalf("mark fixture as legacy Markdown: %v", err)
	}

	legacy := edtExport(h, token, id, "format=md")
	h.mustNoPanic(legacy, "export legacy Markdown")
	if legacy.code != http.StatusOK {
		t.Fatalf("export legacy Markdown returned %d body=%q", legacy.code, legacy.body)
	}
	if legacy.body != source {
		t.Errorf("legacy Markdown export must return stored source byte-for-byte\n got: %q\nwant: %q", legacy.body, source)
	}
	jelly := edtExport(h, token, id, "format=jmd")
	h.mustNoPanic(jelly, "export legacy source as Jelly Markdown")
	if jelly.code != http.StatusOK || jelly.body != source {
		t.Errorf("Jelly Markdown export must remain source-verbatim: status=%d body=%q", jelly.code, jelly.body)
	}

	jmdID, _ := edtCreate(t, h, token, map[string]any{"title": "Jelly Markdown export", "source": "::counter{to=7}\n"})
	flattened := edtExport(h, token, jmdID, "format=md")
	h.mustNoPanic(flattened, "flatten Jelly Markdown")
	if flattened.code != http.StatusOK || strings.Contains(flattened.body, "::counter") || !strings.Contains(flattened.body, "7") {
		t.Errorf("Jelly Markdown export as md must continue flattening active directives: status=%d body=%q", flattened.code, flattened.body)
	}
}

func TestSoftDeletedWidgetKeepsPublishedFrameAddressable(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "editor-regression-widget-soft-delete"))
	id, _ := wgtCreate(t, h, token, "soft-delete-frame", wgtParams(), `<p>published frame survives</p>`)
	wgtPublishOK(t, h, token, id)
	var widget core.Widget
	if err := h.db.First(&widget, id).Error; err != nil {
		t.Fatalf("load widget before soft delete: %v", err)
	}
	before := wgtFrame(h, "soft-delete-frame", 1, "?to=2027-01-01")
	h.mustNoPanic(before, "published widget frame before soft delete")
	if before.code != http.StatusOK || !strings.Contains(before.body, "published frame survives") {
		t.Fatalf("published frame was invalid before soft delete: status=%d body=%s", before.code, before.body)
	}
	if err := h.db.Delete(&widget).Error; err != nil {
		t.Fatalf("soft delete widget: %v", err)
	}
	frame := wgtFrame(h, "soft-delete-frame", 1, "?to=2027-01-01")
	h.mustNoPanic(frame, "published widget frame after soft delete")
	if frame.code != http.StatusOK || !strings.Contains(frame.body, "published frame survives") {
		t.Fatalf("published frame stopped working after soft delete: status=%d body=%s", frame.code, frame.body)
	}
}

func TestRenderEndpointRejectsDepthNineErrorDiagnostic(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("author", "editor-regression-render-depth"))
	var source strings.Builder
	for range 9 {
		source.WriteString(":::section\n")
	}
	source.WriteString("內容\n")
	for range 9 {
		source.WriteString(":::\n")
	}
	r := h.request(http.MethodPost, edtRenderPath, edtRenderBody(t, source.String()), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(r, "depth-nine render endpoint")
	if r.code != http.StatusBadRequest {
		t.Fatalf("depth-nine render endpoint returned %d, want 400: %s", r.code, r.body)
	}
	var body struct {
		Diagnostics []struct {
			Severity string `json:"Severity"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(r.body), &body); err != nil {
		t.Fatalf("decode depth-nine render diagnostics: %v body=%q", err, r.body)
	}
	for _, d := range body.Diagnostics {
		if d.Severity == "error" {
			return
		}
	}
	t.Fatalf("400 response lacks an error-severity diagnostic: %s", r.body)
}

func TestPostFormatSourceRevisionAndRestoreContracts(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "editor-regression-source-format")
	token := h.token(author)
	id, _ := edtCreate(t, h, token, map[string]any{"title": "empty source snapshot", "source": "", "format": "jmd"})
	emptySnapshot := revTestNewest(t, h, token, id)
	if r := edtUpdate(h, token, id, map[string]any{"source": "baseline source", "format": "jmd"}); r.code != http.StatusOK {
		t.Fatalf("save non-empty source: status=%d body=%s", r.code, r.body)
	}
	restore := h.request(http.MethodPost, fmt.Sprintf("/api/admin/posts/%d/revisions/%d/restore", id, emptySnapshot), "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(restore, "restore empty-source revision")
	if restore.code != http.StatusOK {
		t.Fatalf("restore empty-source revision returned %d: %s", restore.code, restore.body)
	}
	var row core.Post
	if err := h.db.First(&row, id).Error; err != nil {
		t.Fatalf("reload empty-source restored post: %v", err)
	}
	if row.Source != "" || row.Format != "jmd" || row.Content != "" {
		t.Fatalf("restored empty-source snapshot = source %q format %q content %q", row.Source, row.Format, row.Content)
	}

	for _, source := range []string{"**same HTML**", "__same HTML__"} {
		updated := edtUpdate(h, token, id, map[string]any{"source": source, "format": "jmd"})
		h.mustNoPanic(updated, "save equivalent jmd source")
		if updated.code != http.StatusOK {
			t.Fatalf("save source %q returned %d: %s", source, updated.code, updated.body)
		}
	}
	if err := h.db.First(&row, id).Error; err != nil {
		t.Fatalf("reload equivalent jmd post: %v", err)
	}
	if row.Content != "<p><strong>same HTML</strong></p>" {
		t.Fatalf("equivalent source rendered unexpected HTML: %q", row.Content)
	}
	revs := revTestList(t, h, token, id)
	if len(revs) < 3 {
		t.Fatalf("equivalent source change did not create a revision: got %d revisions", len(revs))
	}
	fromID := revs[1].ID
	diff := h.request(http.MethodGet, fmt.Sprintf("/api/admin/posts/%d/revisions/diff?from=%d&to=current", id, fromID), "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(diff, "same-format source diff")
	if diff.code != http.StatusOK || edtDecode(t, diff.body)["basis"] != "source" {
		t.Fatalf("same-format diff did not use source basis: status=%d body=%s", diff.code, diff.body)
	}
	jmdSnapshot := revTestNewest(t, h, token, id)

	contentUpdate := edtUpdate(h, token, id, map[string]any{"content": "<p>legacy HTML update</p>", "meta": map[string]any{"markdown": "legacy client markdown", "other": "preserved metadata"}})
	h.mustNoPanic(contentUpdate, "content-only update")
	if contentUpdate.code != http.StatusOK {
		t.Fatalf("content-only update returned %d: %s", contentUpdate.code, contentUpdate.body)
	}
	if err := h.db.First(&row, id).Error; err != nil {
		t.Fatalf("reload content-only update: %v", err)
	}
	if row.Source != "" || row.Format != "html" || row.Content != "<p>legacy HTML update</p>" {
		t.Fatalf("content-only update retained source-format state: source=%q format=%q content=%q", row.Source, row.Format, row.Content)
	}
	var savedMeta map[string]any
	if err := json.Unmarshal(row.Meta, &savedMeta); err != nil {
		t.Fatalf("decode content-only compatibility metadata: %v", err)
	}
	if savedMeta["markdown"] != "legacy client markdown" || savedMeta["other"] != "preserved metadata" {
		t.Fatalf("content-only update lost supplied legacy metadata: %#v", savedMeta)
	}
	htmlSnapshot := revTestNewest(t, h, token, id)
	mixedDiff := h.request(http.MethodGet, fmt.Sprintf("/api/admin/posts/%d/revisions/diff?from=%d&to=current", id, jmdSnapshot), "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(mixedDiff, "mixed-format content diff")
	if mixedDiff.code != http.StatusOK || edtDecode(t, mixedDiff.body)["basis"] != "content" || !strings.Contains(mixedDiff.body, "same HTML") || !strings.Contains(mixedDiff.body, "legacy HTML update") {
		t.Fatalf("mixed-format diff did not compare rendered content on both sides: status=%d body=%s", mixedDiff.code, mixedDiff.body)
	}
	beforeRejected := row
	badFormat := edtUpdate(h, token, id, map[string]any{"format": "md"})
	h.mustNoPanic(badFormat, "format-only change")
	if badFormat.code != http.StatusBadRequest {
		t.Fatalf("format-only change returned %d, want 400: %s", badFormat.code, badFormat.body)
	}
	noopFormat := edtUpdate(h, token, id, map[string]any{"format": "html"})
	h.mustNoPanic(noopFormat, "same-format update")
	if noopFormat.code != http.StatusOK {
		t.Fatalf("same-format update returned %d, want 200: %s", noopFormat.code, noopFormat.body)
	}
	if err := h.db.First(&row, id).Error; err != nil {
		t.Fatalf("reload after format-only requests: %v", err)
	}
	if row.Source != beforeRejected.Source || row.Format != beforeRejected.Format || row.Content != beforeRejected.Content {
		t.Fatalf("format-only requests changed post state: before=%+v after=%+v", beforeRejected, row)
	}
	restoreJMD := h.request(http.MethodPost, fmt.Sprintf("/api/admin/posts/%d/revisions/%d/restore", id, jmdSnapshot), "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(restoreJMD, "restore jmd snapshot after legacy content update")
	if restoreJMD.code != http.StatusOK {
		t.Fatalf("restore jmd snapshot returned %d: %s", restoreJMD.code, restoreJMD.body)
	}
	if err := h.db.First(&row, id).Error; err != nil {
		t.Fatalf("reload restored jmd snapshot: %v", err)
	}
	if row.Source != "__same HTML__" || row.Format != "jmd" {
		t.Fatalf("restored jmd snapshot mismatch: source=%q format=%q", row.Source, row.Format)
	}
	restoreHTML := h.request(http.MethodPost, fmt.Sprintf("/api/admin/posts/%d/revisions/%d/restore", id, htmlSnapshot), "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(restoreHTML, "restore html snapshot")
	if restoreHTML.code != http.StatusOK {
		t.Fatalf("restore html snapshot returned %d: %s", restoreHTML.code, restoreHTML.body)
	}
	if err := h.db.First(&row, id).Error; err != nil {
		t.Fatalf("reload restored html snapshot: %v", err)
	}
	if row.Source != "" || row.Format != "html" || row.Content != "<p>legacy HTML update</p>" {
		t.Fatalf("restored html snapshot mismatch: source=%q format=%q content=%q", row.Source, row.Format, row.Content)
	}
	var restoredMeta map[string]any
	if err := json.Unmarshal(row.Meta, &restoredMeta); err != nil {
		t.Fatalf("decode restored html metadata: %v", err)
	}
	if restoredMeta["markdown"] != "legacy client markdown" || restoredMeta["other"] != "preserved metadata" {
		t.Errorf("HTML snapshot restore lost compatibility metadata: %#v", restoredMeta)
	}
}

func TestLegacyMarkdownRenderAutosaveAndRestoreKeepMarkdownSemantics(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "editor-regression-legacy-md")
	token := h.token(author)
	source := "Legacy **bold** prose\n\n```html\n<strong>safe</strong>\n```\n\n" +
		":::columns\n::: col\nleft column\n:::\n::: col\nright column\n:::\n:::\n"
	id, _ := edtCreate(t, h, token, map[string]any{
		"title":  "legacy Markdown",
		"type":   "post",
		"status": "draft",
		"source": source,
		"format": "md",
	})
	row := edtLoadRow(t, h, id)
	if row.Format != "md" {
		t.Fatalf("created legacy post format = %q, want md", row.Format)
	}
	assertLegacyMDHTML := func(stage, got string) {
		t.Helper()
		if !strings.Contains(got, `class="language-html"`) || !strings.Contains(got, `&lt;strong&gt;safe&lt;/strong&gt;`) {
			t.Errorf("%s did not retain the HTML fence as escaped code: %s", stage, got)
		}
		for _, active := range []string{`<strong>safe</strong>`, `class="jc-html"`, `class="jc-columns"`, `class="jc-col"`} {
			if strings.Contains(got, active) {
				t.Errorf("%s interpreted legacy Markdown literal %q: %s", stage, active, got)
			}
		}
		if !strings.Contains(got, ":::columns") || !strings.Contains(got, "left column") || !strings.Contains(got, "right column") {
			t.Errorf("%s lost literal container source: %s", stage, got)
		}
	}
	assertLegacyMDHTML("stored md content", row.Content)

	renderReq, err := json.Marshal(map[string]any{"post_id": id, "source": source})
	if err != nil {
		t.Fatalf("encode post-aware render request: %v", err)
	}
	render := h.request(http.MethodPost, edtRenderPath, string(renderReq), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(render, "legacy md editor preview")
	if render.code != http.StatusOK {
		t.Fatalf("POST /api/admin/render for legacy md post returned %d: %s", render.code, render.body)
	}
	preview := edtDecode(t, render.body)["html"]
	previewHTML, ok := preview.(string)
	if !ok {
		t.Fatalf("legacy md preview response has no html string: %s", render.body)
	}
	assertLegacyMDHTML("post-aware preview", previewHTML)
	if previewHTML != row.Content {
		t.Errorf("legacy md preview differs from stored published content:\npreview: %s\nstored:  %s", previewHTML, row.Content)
	}

	// A new document still defaults to JMD, and a render without post_id follows
	// the same JMD default without adding a client-supplied format field.
	newID, _ := edtCreate(t, h, token, map[string]any{"title": "new JMD document", "source": source})
	if newPost := edtLoadRow(t, h, newID); newPost.Format != "jmd" {
		t.Errorf("new post without an explicit format = %q, want jmd", newPost.Format)
	}
	newRender := h.request(http.MethodPost, edtRenderPath, edtRenderBody(t, source), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(newRender, "new-document default render")
	if newRender.code != http.StatusOK {
		t.Fatalf("POST /api/admin/render without post_id returned %d: %s", newRender.code, newRender.body)
	}
	newHTML, _ := edtDecode(t, newRender.body)["html"].(string)
	if !strings.Contains(newHTML, `class="jc-columns"`) {
		t.Errorf("render without post_id did not retain the JMD default: %s", newHTML)
	}

	autosave := edtAutosave(h, token, id, "legacy Markdown", source)
	h.mustNoPanic(autosave, "legacy md autosave")
	if autosave.code != http.StatusOK {
		t.Fatalf("legacy md autosave returned %d: %s", autosave.code, autosave.body)
	}
	var saved struct {
		ID     uint
		Source string
		Format string
	}
	if err := h.db.Raw("SELECT id, source, format FROM revisions WHERE post_id = ? AND kind = 'autosave' ORDER BY id DESC LIMIT 1", id).Scan(&saved).Error; err != nil {
		t.Fatalf("load legacy md autosave revision: %v", err)
	}
	if saved.ID == 0 || saved.Source != source {
		t.Fatalf("legacy md autosave revision = id %d source %q", saved.ID, saved.Source)
	}
	if saved.Format != "md" {
		t.Errorf("legacy md autosave revision format = %q, want md", saved.Format)
	}
	changed := edtUpdate(h, token, id, map[string]any{"source": "changed JMD source", "format": "jmd"})
	h.mustNoPanic(changed, "change source before autosave restore")
	if changed.code != http.StatusOK {
		t.Fatalf("change source before restore returned %d: %s", changed.code, changed.body)
	}
	restore := h.request(http.MethodPost, fmt.Sprintf("/api/admin/posts/%d/revisions/%d/restore", id, saved.ID), "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(restore, "restore legacy md autosave revision")
	if restore.code != http.StatusOK {
		t.Fatalf("restore legacy md autosave revision returned %d: %s", restore.code, restore.body)
	}
	row = edtLoadRow(t, h, id)
	if row.Format != "md" || row.Source != source {
		t.Errorf("restored autosave state = format %q source %q", row.Format, row.Source)
	}
	assertLegacyMDHTML("restored legacy md content", row.Content)
}

func TestSourceWorkflowPreservesNonObjectMetadata(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("author", "editor-regression-source-meta-values"))
	tests := []struct {
		name string
		meta string
	}{
		{name: "array", meta: `[1,"two",{"three":3}]`},
		{name: "string", meta: `"metadata"`},
		{name: "number", meta: `17`},
		{name: "boolean", meta: `false`},
		{name: "null", meta: `null`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertState := func(stage string, response accessResult, postID uint, wantSource, wantFormat, wantMeta string) {
				t.Helper()
				h.mustNoPanic(response, stage)
				if response.code != http.StatusOK {
					t.Errorf("%s returned %d: %s", stage, response.code, response.body)
					return
				}
				var apiPost core.Post
				if err := json.Unmarshal([]byte(response.body), &apiPost); err != nil {
					t.Errorf("decode %s response: %v", stage, err)
					return
				}
				if apiPost.Source != wantSource || apiPost.Format != wantFormat || !jsonEqual(apiPost.Meta, wantMeta) {
					t.Errorf("%s response state = source %q format %q meta %s; want source %q format %q meta %s", stage, apiPost.Source, apiPost.Format, apiPost.Meta, wantSource, wantFormat, wantMeta)
				}
				var stored core.Post
				if err := h.db.First(&stored, postID).Error; err != nil {
					t.Errorf("reload after %s: %v", stage, err)
					return
				}
				if stored.Source != wantSource || stored.Format != wantFormat || !jsonEqual(stored.Meta, wantMeta) {
					t.Errorf("%s database state = source %q format %q meta %s; want source %q format %q meta %s", stage, stored.Source, stored.Format, stored.Meta, wantSource, wantFormat, wantMeta)
				}
			}

			create := h.request(http.MethodPost, "/api/admin/posts", edtBody(t, map[string]any{
				"title": "source meta " + tc.name, "source": "## initial " + tc.name, "format": "jmd", "meta": json.RawMessage(tc.meta),
			}), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
			h.mustNoPanic(create, "create source metadata post")
			if create.code != http.StatusOK {
				t.Fatalf("create source metadata post returned %d: %s", create.code, create.body)
			}
			var created core.Post
			if err := json.Unmarshal([]byte(create.body), &created); err != nil || created.ID == 0 {
				t.Fatalf("decode created source metadata post: id=%d err=%v body=%s", created.ID, err, create.body)
			}
			assertState("POST source plus metadata", create, created.ID, "## initial "+tc.name, "jmd", tc.meta)

			newSource := "## changed " + tc.name
			assertState("PUT source without metadata", edtUpdate(h, token, created.ID, map[string]any{"source": newSource}), created.ID, newSource, "jmd", tc.meta)
			assertState("PUT empty source without metadata", edtUpdate(h, token, created.ID, map[string]any{"source": ""}), created.ID, "", "jmd", tc.meta)

			// RecordRevision snapshots the post after the empty-source update. Select that exact
			// snapshot by its stored source/format so restore cannot accidentally target the
			// preceding non-empty revision.
			var emptySourceSnapshot core.Revision
			if err := h.db.Where("post_id = ? AND source = ? AND format = ?", created.ID, "", "jmd").Order("id DESC").First(&emptySourceSnapshot).Error; err != nil {
				t.Fatalf("load empty-source revision snapshot: %v", err)
			}
			if emptySourceSnapshot.Source != "" || emptySourceSnapshot.Format != "jmd" || !jsonEqual(emptySourceSnapshot.Meta, tc.meta) {
				t.Errorf("empty-source revision snapshot = source %q format %q meta %s; want empty/jmd/%s", emptySourceSnapshot.Source, emptySourceSnapshot.Format, emptySourceSnapshot.Meta, tc.meta)
			}

			changedMeta := json.RawMessage(`{"other":"changed"}`)
			changedSource := "## alternate " + tc.name
			changedExpectedMeta, err := json.Marshal(map[string]string{"other": "changed", "markdown": changedSource})
			if err != nil {
				t.Fatalf("encode expected changed metadata: %v", err)
			}
			change := edtUpdate(h, token, created.ID, map[string]any{"source": changedSource, "meta": changedMeta})
			assertState("change source and object metadata before restore", change, created.ID, changedSource, "jmd", string(changedExpectedMeta))

			restore := h.request(http.MethodPost, fmt.Sprintf("/api/admin/posts/%d/revisions/%d/restore", created.ID, emptySourceSnapshot.ID), "", token, echo.MIMEApplicationJSON, "")
			assertState("restore empty-source metadata snapshot", restore, created.ID, "", "jmd", tc.meta)
		})
	}
}

func TestWidgetUsageCountCountsOnlyActiveJMDReferences(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "editor-regression-widget-usage-precision"))
	wgtCreate(t, h, token, "usage-w", wgtParams(), wgtSafeSource)
	wgtCreate(t, h, token, "usage-w-more", wgtParams(), wgtSafeSource)

	pageID, _ := edtCreate(t, h, token, map[string]any{
		"title": "quoted duplicate page", "slug": "widget-usage-quoted-page", "type": "page", "status": "published",
		"source": "::widget{name=\"usage-w\" to=\"2027-01-01\"}\n\n::widget{name=\"usage-w\" to=\"2027-02-01\"}\n",
	})
	_, _ = edtCreate(t, h, token, map[string]any{
		"title": "private widget post", "slug": "widget-usage-private", "status": "private", "visibility": "private",
		"source": "::widget{name=usage-w to=2027-03-01}\n",
	})
	_, _ = edtCreate(t, h, token, map[string]any{
		"title": "draft widget post", "slug": "widget-usage-draft", "status": "draft",
		"source": "::widget{name=usage-w to=2027-04-01}\n",
	})
	for slug, source := range map[string]string{
		"widget-usage-inline-code": "`::widget{name=usage-w to=2027-01-01}`\n",
		"widget-usage-fenced-code": "```text\n::widget{name=usage-w to=2027-01-01}\n```\n",
		"widget-usage-plain-text":  "name=usage-w\n",
	} {
		_, _ = edtCreate(t, h, token, map[string]any{
			"title": slug, "slug": slug, "status": "published", "source": source,
		})
	}
	_, _ = edtCreate(t, h, token, map[string]any{
		"title": "longer widget name", "slug": "widget-usage-prefix", "status": "published",
		"source": "::widget{name=usage-w-more to=2027-01-01}\n",
	})
	_, _ = edtCreate(t, h, token, map[string]any{
		"title": "legacy Markdown text", "slug": "widget-usage-legacy-md", "status": "published", "format": "md",
		"source": "::widget{name=usage-w to=2027-01-01}\n",
	})
	deletedID, _ := edtCreate(t, h, token, map[string]any{
		"title": "deleted widget post", "slug": "widget-usage-deleted", "status": "published",
		"source": "::widget{name=usage-w to=2027-01-01}\n",
	})
	trashTestDeletePost(t, h, token, deletedID)
	if pageID == 0 {
		t.Fatal("page with quoted widget references was not created")
	}

	list := h.request(http.MethodGet, "/api/admin/widgets", "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(list, "admin widget usage precision list")
	if list.code != http.StatusOK {
		t.Fatalf("GET /api/admin/widgets returned %d, want 200; body=%q", list.code, list.body)
	}
	items := wgtListItems(t, list.body)
	for name, want := range map[string]int{"usage-w": 3, "usage-w-more": 1} {
		item := wgtFindByName(items, name)
		if item == nil {
			t.Errorf("widget %s is missing from the list: %s", name, list.body)
			continue
		}
		if got, _ := item["uses"].(float64); int(got) != want {
			t.Errorf("widget %s uses = %v, want %d active JMD source documents", name, item["uses"], want)
		}
	}
}

func TestInvalidMarkupSaveDoesNotCreateOrOverwritePost(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "editor-regression-invalid-markup-save")
	token := h.token(author)
	var tooDeep strings.Builder
	for range 9 {
		tooDeep.WriteString(":::section\n")
	}
	tooDeep.WriteString("depth nine\n")
	for range 9 {
		tooDeep.WriteString(":::\n")
	}
	var beforeCount int64
	if err := h.db.Model(&core.Post{}).Count(&beforeCount).Error; err != nil {
		t.Fatalf("count posts before invalid create: %v", err)
	}
	created := h.request(http.MethodPost, "/api/admin/posts", edtBody(t, map[string]any{"title": "invalid depth create", "slug": "invalid-depth-create", "type": "post", "status": "draft", "source": tooDeep.String(), "format": "jmd"}), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(created, "depth-nine post create")
	if created.code != http.StatusBadRequest || !strings.Contains(created.body, "container_depth") {
		t.Fatalf("depth-nine create returned %d or omitted diagnostic: %s", created.code, created.body)
	}
	var afterCount int64
	if err := h.db.Model(&core.Post{}).Count(&afterCount).Error; err != nil {
		t.Fatalf("count posts after invalid create: %v", err)
	}
	if afterCount != beforeCount {
		t.Fatalf("depth-nine create wrote a post: count %d -> %d", beforeCount, afterCount)
	}

	id, _ := edtCreate(t, h, token, map[string]any{"title": "safe source", "type": "post", "status": "draft", "source": "SAFE_SOURCE_SENTINEL", "format": "jmd"})
	var before core.Post
	if err := h.db.First(&before, id).Error; err != nil {
		t.Fatalf("read post before rejected update: %v", err)
	}
	updated := edtUpdate(h, token, id, map[string]any{"source": tooDeep.String(), "format": "jmd"})
	h.mustNoPanic(updated, "depth-nine post update")
	if updated.code != http.StatusBadRequest || !strings.Contains(updated.body, "container_depth") {
		t.Fatalf("depth-nine update returned %d or omitted diagnostic: %s", updated.code, updated.body)
	}
	var after core.Post
	if err := h.db.First(&after, id).Error; err != nil {
		t.Fatalf("read post after rejected update: %v", err)
	}
	if after.Source != before.Source || after.Content != before.Content {
		t.Fatalf("depth-nine update overwrote safe content: before=(%q,%q) after=(%q,%q)", before.Source, before.Content, after.Source, after.Content)
	}
	badFormat := edtUpdate(h, token, id, map[string]any{"format": "not-a-format"})
	h.mustNoPanic(badFormat, "unsupported format update")
	if badFormat.code != http.StatusBadRequest {
		t.Fatalf("unsupported format update returned %d, want 400: %s", badFormat.code, badFormat.body)
	}
}

func TestPublicPageSanitizesLegacyContentAndRerendersNullSource(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "editor-regression-legacy-page")
	legacy := core.Post{Title: "Legacy HTML safety", Slug: "editor-regression-legacy-page", Content: `<script>window.legacyUnsafeMarker=true</script><p>LEGACY_SAFE_TEXT</p>`, Type: "page", Status: "published", AuthorID: author.ID, Visibility: "public", Format: "html", RenderVersion: 0}
	if err := h.db.Create(&legacy).Error; err != nil {
		t.Fatalf("seed legacy page: %v", err)
	}
	if err := h.db.Exec("UPDATE posts SET source = NULL, render_version = 0, format = 'html' WHERE id = ?", legacy.ID).Error; err != nil {
		t.Fatalf("seed nullable legacy source: %v", err)
	}
	var baseline core.Post
	if err := h.db.First(&baseline, legacy.ID).Error; err != nil {
		t.Fatalf("read legacy page baseline from database: %v", err)
	}
	public := h.request(http.MethodGet, "/editor-regression-legacy-page", "", "", "", "")
	h.mustNoPanic(public, "legacy public page")
	if public.code != http.StatusOK || !strings.Contains(public.body, "LEGACY_SAFE_TEXT") || strings.Contains(public.body, "window.legacyUnsafeMarker") {
		t.Fatalf("legacy public page did not sanitize unsafe HTML: status=%d body=%s", public.code, public.body)
	}
	RerenderStalePosts(context.Background())
	var after core.Post
	if err := h.db.First(&after, legacy.ID).Error; err != nil {
		t.Fatalf("reload rerendered legacy page: %v", err)
	}
	if after.RenderVersion != 1 || strings.Contains(after.Content, "<script") || strings.Contains(after.Content, "window.legacyUnsafeMarker") || !strings.Contains(after.Content, "LEGACY_SAFE_TEXT") {
		t.Errorf("legacy page did not upgrade to sanitized content: version=%d content=%s", after.RenderVersion, after.Content)
	}
	if !after.UpdatedAt.Equal(baseline.UpdatedAt) {
		t.Errorf("background rerender changed updated_at: before=%s after=%s", baseline.UpdatedAt, after.UpdatedAt)
	}
}

func TestPublicPageExpandsIndependentProductAndPostListsSafely(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "editor-regression-dynamic-page")
	postCategory := archivesSeedCategory(t, h, "Dynamic posts", "editor-regression-posts", nil, false, false)
	hiddenPostCategory := archivesSeedCategory(t, h, "Hidden dynamic posts", "editor-regression-hidden-posts", nil, true, false)
	productCategory := shopCatalogSeedCategory(t, h, "Dynamic products", "editor-regression-products", nil, false)
	hiddenProductCategory := shopCatalogSeedCategory(t, h, "Hidden dynamic products", "editor-regression-hidden-products", nil, true)
	now := time.Now().UTC().Truncate(time.Second)
	archivesSeedPost(t, h, author, archivesPost{title: "VISIBLE_POST_ENTRY", publishedAt: now.Add(-time.Hour), categories: []core.Category{postCategory}})
	archivesSeedPost(t, h, author, archivesPost{title: "DRAFT_POST_SECRET", status: "draft", publishedAt: now.Add(-time.Hour), categories: []core.Category{postCategory}})
	archivesSeedPost(t, h, author, archivesPost{title: "PRIVATE_POST_SECRET", visibility: "private", publishedAt: now.Add(-time.Hour), categories: []core.Category{postCategory}})
	archivesSeedPost(t, h, author, archivesPost{title: "FUTURE_POST_SECRET", publishedAt: now.Add(48 * time.Hour), categories: []core.Category{postCategory}})
	archivesSeedPost(t, h, author, archivesPost{title: "HIDDEN_POST_SECRET", publishedAt: now.Add(-time.Hour), categories: []core.Category{hiddenPostCategory}})
	shopCatalogSeedProduct(t, h, shopCatalogProduct{title: "VISIBLE_PRODUCT_ENTRY", slug: "editor-regression-visible-product", status: "active", price: 100, stock: 5, categories: []core.ProductCategory{productCategory}})
	shopCatalogSeedProduct(t, h, shopCatalogProduct{title: "HIDDEN_PRODUCT_SECRET", slug: "editor-regression-hidden-product", status: "active", price: 100, stock: 5, categories: []core.ProductCategory{hiddenProductCategory}})

	source := ":::products {category=editor-regression-products limit=1}\n:::\n\n:::posts {category=editor-regression-posts limit=2}\n:::\n"
	rendered, err := markup.Render(context.Background(), source, markup.Options{Trust: markup.TrustRich, Format: "jmd"})
	if err != nil {
		t.Fatalf("render dynamic page source: %v", err)
	}
	page := core.Post{Title: "Dynamic expansion regression", Slug: "editor-regression-dynamic-page", Content: rendered.HTML, Source: source, Type: "page", Status: "published", AuthorID: author.ID, Visibility: "public", Format: "jmd", RenderVersion: 1}
	if err := h.db.Create(&page).Error; err != nil {
		t.Fatalf("seed dynamic page: %v", err)
	}
	got := h.request(http.MethodGet, "/editor-regression-dynamic-page", "", "", "", "")
	h.mustNoPanic(got, "public page with independent dynamic lists")
	if got.code != http.StatusOK {
		t.Fatalf("dynamic public page returned %d: %s", got.code, got.body)
	}
	for _, want := range []string{"VISIBLE_PRODUCT_ENTRY", "VISIBLE_POST_ENTRY"} {
		if !strings.Contains(got.body, want) {
			t.Errorf("dynamic page omitted %s: %s", want, got.body)
		}
	}
	for _, forbidden := range []string{"DRAFT_POST_SECRET", "PRIVATE_POST_SECRET", "FUTURE_POST_SECRET", "HIDDEN_POST_SECRET", "HIDDEN_PRODUCT_SECRET"} {
		if strings.Contains(got.body, forbidden) {
			t.Errorf("dynamic page exposed %s: %s", forbidden, got.body)
		}
	}
}

func TestWidgetFrameInjectsPropsOnceForTypedMultipleScripts(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "editor-regression-widget-scripts"))
	params := []map[string]any{
		{"name": "mode", "type": "enum", "required": true, "values": []string{"compact", "wide"}},
		{"name": "count", "type": "number", "required": true},
		{"name": "enabled", "type": "boolean", "required": true},
	}
	source := `<script type="text/javascript">window.mode=props.mode;</script>` +
		`<script type="module">window.count=props.count;window.enabled=props.enabled;</script>`
	id, _ := wgtCreate(t, h, token, "multi-script-widget", params, source)
	wgtPublishOK(t, h, token, id)

	for _, query := range []string{"?mode=unknown&count=2&enabled=true", "?mode=wide&count=nope&enabled=true", "?mode=wide&count=2&enabled=yes", "?mode=wide&count=NaN&enabled=true", "?mode=wide&count=%2BInf&enabled=true", "?mode=wide&count=-Inf&enabled=true"} {
		r := wgtFrame(h, "multi-script-widget", 1, query)
		h.mustNoPanic(r, "invalid declared parameter "+query)
		if r.code != http.StatusBadRequest {
			t.Errorf("invalid parameter query %s returned %d, want 400", query, r.code)
		}
	}
	r := wgtFrame(h, "multi-script-widget", 1, "?mode=wide&count=2.5&enabled=false&evil=poison")
	h.mustNoPanic(r, "valid declared parameters and extra key")
	if r.code != http.StatusOK {
		t.Fatalf("valid parameter query returned %d: %s", r.code, r.body)
	}
	if strings.Count(r.body, "const props=") != 1 {
		t.Errorf("frame injected props %d times, want exactly once: %s", strings.Count(r.body, "const props="), r.body)
	}
	for _, want := range []string{`type="text/javascript"`, `type="module"`, `window.mode=props.mode`, `window.count=props.count`, `window.enabled=props.enabled`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("frame lost a typed script or props access %q: %s", want, r.body)
		}
	}
	propsRE := regexp.MustCompile(`const props=(\{[^;]*\});`)
	m := propsRE.FindStringSubmatch(r.body)
	if m == nil {
		t.Fatalf("frame props object not found: %s", r.body)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(m[1]), &got); err != nil {
		t.Fatalf("decode frame props: %v (%s)", err, m[1])
	}
	if got["mode"] != "wide" || got["count"] != 2.5 || got["enabled"] != false {
		t.Errorf("validated props = %#v", got)
	}
	if _, exists := got["evil"]; exists {
		t.Errorf("undeclared query key polluted props: %#v", got)
	}
}

func TestWidgetVersionGuardsAndActorAuditSnapshots(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "editor-regression-widget-audit")
	token := h.token(admin)
	params := []map[string]any{{"name": "label", "type": "string", "required": true}}
	source := `<p>immutable published source</p><script>window.label=props.label;</script>`
	id, _ := wgtCreate(t, h, token, "audit-immutable-widget", params, source)
	var baseline core.WidgetVersion
	if err := h.db.Where("widget_id = ? AND version = ?", id, 1).First(&baseline).Error; err != nil {
		t.Fatalf("load draft version baseline: %v", err)
	}
	wgtPublishOK(t, h, token, id)
	var published core.WidgetVersion
	if err := h.db.Where("widget_id = ? AND version = ?", id, 1).First(&published).Error; err != nil {
		t.Fatalf("load published version: %v", err)
	}
	if !published.Published || !published.UpdatedAt.After(baseline.UpdatedAt) {
		t.Fatalf("publishing did not update version state/timestamp: baseline=%+v published=%+v", baseline, published)
	}
	var audit struct {
		Action     string
		ActorID    *uint
		ActorEmail string
		OldState   string
		NewState   string
	}
	if err := h.db.Raw(`SELECT action, actor_id, actor_email, old_state::text AS old_state, new_state::text AS new_state FROM widget_version_audit_log WHERE widget_id = ? AND version = 1 ORDER BY id DESC LIMIT 1`, id).Scan(&audit).Error; err != nil {
		t.Fatalf("read publish audit snapshot: %v", err)
	}
	if audit.Action != "UPDATE" || audit.ActorID == nil || *audit.ActorID != admin.ID || audit.ActorEmail != admin.Email || !strings.Contains(audit.OldState, `"published": false`) || !strings.Contains(audit.NewState, `"published": true`) {
		t.Errorf("publish audit lacks actor/state snapshot: %+v", audit)
	}
	if err := h.db.Exec("UPDATE widget_versions SET source = ? WHERE widget_id = ? AND version = 1", "tampered source", id).Error; err == nil {
		t.Error("SQL update changed published widget source")
	}
	if err := h.db.Exec("UPDATE widget_versions SET params = '[]'::jsonb WHERE widget_id = ? AND version = 1", id).Error; err == nil {
		t.Error("SQL update changed published widget parameters")
	}
	if err := h.db.Exec("DELETE FROM widget_versions WHERE widget_id = ? AND version = 1", id).Error; err == nil {
		t.Error("SQL DELETE removed a published widget version")
	}
	var afterGuard core.WidgetVersion
	if err := h.db.Where("widget_id = ? AND version = 1", id).First(&afterGuard).Error; err != nil {
		t.Fatalf("load version after rejected SQL writes: %v", err)
	}
	if afterGuard.Source != source || afterGuard.Params != published.Params || !afterGuard.Published {
		t.Errorf("published version changed after rejected SQL writes: %+v", afterGuard)
	}
	blockedDelete := h.request(http.MethodDelete, "/api/admin/widgets/"+edtID(id), "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(blockedDelete, "delete published widget")
	if blockedDelete.code != http.StatusConflict {
		t.Errorf("delete published widget returned %d, want 409: %s", blockedDelete.code, blockedDelete.body)
	}

	draftID, _ := wgtCreate(t, h, token, "audit-draft-delete", params, `<p>draft source</p>`)
	deleted := h.request(http.MethodDelete, "/api/admin/widgets/"+edtID(draftID), "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(deleted, "delete draft widget")
	if deleted.code != http.StatusNoContent {
		t.Fatalf("delete draft widget returned %d, want 204: %s", deleted.code, deleted.body)
	}
	var deletionAudit struct {
		Action     string
		ActorID    *uint
		ActorEmail string
		NewState   string
	}
	if err := h.db.Raw(`SELECT action, actor_id, actor_email, new_state::text AS new_state FROM widget_audit_log WHERE widget_id = ? ORDER BY id DESC LIMIT 1`, draftID).Scan(&deletionAudit).Error; err != nil {
		t.Fatalf("read draft deletion audit: %v", err)
	}
	if deletionAudit.Action != "UPDATE" || deletionAudit.ActorID == nil || *deletionAudit.ActorID != admin.ID || deletionAudit.ActorEmail != admin.Email || !strings.Contains(deletionAudit.NewState, "deleted_at") {
		t.Errorf("draft deletion audit lost actor or soft-delete snapshot: %+v", deletionAudit)
	}
}

func TestWidgetForeignKeyColumnsHaveLeadingIndexes(t *testing.T) {
	h := newAccessHarness(t)
	var missing []struct {
		TableName      string
		ConstraintName string
		ColumnName     string
	}
	query := `
WITH foreign_key_columns AS (
  SELECT c.conrelid, c.conname, a.attnum, a.attname
  FROM pg_constraint c
  CROSS JOIN LATERAL unnest(c.conkey) AS fk(attnum)
  JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = fk.attnum
  WHERE c.contype = 'f'
    AND c.conrelid = ANY (ARRAY[
      'public.widgets'::regclass,
      'public.widget_versions'::regclass,
      'public.widget_audit_log'::regclass,
      'public.widget_version_audit_log'::regclass
    ])
)
SELECT fk.conrelid::regclass::text AS table_name,
       fk.conname AS constraint_name,
       fk.attname AS column_name
FROM foreign_key_columns fk
WHERE NOT EXISTS (
  SELECT 1
  FROM pg_index i
  CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS indexed(attnum, position)
  WHERE i.indrelid = fk.conrelid
    AND i.indisready AND i.indisvalid AND i.indpred IS NULL
    AND indexed.position = 1 AND indexed.attnum = fk.attnum
)
ORDER BY table_name, constraint_name, column_name`
	if err := h.db.Raw(query).Scan(&missing).Error; err != nil {
		t.Fatalf("inspect widget foreign-key indexes: %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("widget foreign-key columns lack a leading index column: %+v", missing)
	}
}

func TestInvalidWidgetDeleteDoesNotDeleteAnyWidgets(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "editor-regression-widget-delete"))
	wgtCreate(t, h, token, "delete-keep-one", wgtParams(), wgtSafeSource)
	wgtCreate(t, h, token, "delete-keep-two", wgtParams(), wgtSafeSource)
	var before int64
	if err := h.db.Model(&core.Widget{}).Count(&before).Error; err != nil {
		t.Fatalf("count widgets before invalid delete: %v", err)
	}
	var widgetSQL atomic.Int64
	callbackName := fmt.Sprintf("editor_regression_invalid_widget_delete_%d", accessTestDBCounter.Add(1))
	countWidgetStatement := func(tx *gorm.DB) {
		if tx.Statement.Table == "widgets" || tx.Statement.Table == "widget_versions" {
			widgetSQL.Add(1)
		}
	}
	if err := h.db.Callback().Query().Before("gorm:query").Register(callbackName+"_query", countWidgetStatement); err != nil {
		t.Fatalf("register widget query counter: %v", err)
	}
	if err := h.db.Callback().Delete().Before("gorm:delete").Register(callbackName+"_delete", countWidgetStatement); err != nil {
		t.Fatalf("register widget delete counter: %v", err)
	}
	t.Cleanup(func() {
		_ = h.db.Callback().Query().Remove(callbackName + "_query")
		_ = h.db.Callback().Delete().Remove(callbackName + "_delete")
	})
	r := h.request(http.MethodDelete, "/api/admin/widgets/not-a-number", "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(r, "invalid widget delete ID")
	if r.code != http.StatusNotFound {
		t.Errorf("invalid widget delete returned %d, want 404: %s", r.code, r.body)
	}
	if count := widgetSQL.Load(); count != 0 {
		t.Errorf("invalid widget delete executed %d widget SQL operations, want none", count)
	}
	var after int64
	if err := h.db.Model(&core.Widget{}).Count(&after).Error; err != nil {
		t.Fatalf("count widgets after invalid delete: %v", err)
	}
	if after != before {
		t.Errorf("invalid delete changed widget count from %d to %d", before, after)
	}
	for _, name := range []string{"delete-keep-one", "delete-keep-two"} {
		var count int64
		if err := h.db.Model(&core.Widget{}).Where("name = ?", name).Count(&count).Error; err != nil {
			t.Fatalf("check widget %s after invalid delete: %v", name, err)
		}
		if count != 1 {
			t.Errorf("widget %s missing after invalid delete", name)
		}
	}
}

func TestConcurrentPostSavesWithSameBaseAllowOnlyOne(t *testing.T) {
	h := newAccessHarness(t)
	user := h.user("author", "editor-regression-concurrent-save")
	token := h.token(user)
	id, _ := edtCreate(t, h, token, map[string]any{"title": "same-base", "source": "first"})
	read := h.request(http.MethodGet, "/api/admin/posts/"+edtID(id), "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(read, "read initial post for concurrent save")
	base := edtDecode(t, read.body)["updated_at"]
	if base == nil {
		t.Fatalf("initial post has no updated_at: %s", read.body)
	}

	start := make(chan struct{})
	results := make(chan accessResult, 2)
	var wg sync.WaitGroup
	for _, title := range []string{"save-A", "save-B"} {
		wg.Add(1)
		go func(title string) {
			defer wg.Done()
			<-start
			results <- edtUpdate(h, token, id, map[string]any{"title": title, "base_updated_at": base})
		}(title)
	}
	close(start)
	wg.Wait()
	close(results)
	ok, conflicts := 0, 0
	for r := range results {
		h.mustNoPanic(r, "same-base concurrent save")
		switch r.code {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflicts++
		default:
			t.Errorf("concurrent save returned %d, want 200 or 409: %s", r.code, r.body)
		}
	}
	if ok != 1 || conflicts != 1 {
		t.Errorf("same-base saves yielded %d successes and %d conflicts, want one each", ok, conflicts)
	}
}

func TestEmptySourceIgnoresClientHTMLContent(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("author", "editor-regression-empty-source"))
	id, created := edtCreate(t, h, token, map[string]any{
		"title": "empty source", "source": "", "content": `<p>client content</p><script>alert(1)</script>`,
	})
	row := edtLoadRow(t, h, id)
	if row.Source != "" || row.Format != "jmd" {
		t.Errorf("empty source stored source=%q format=%q, want empty and jmd", row.Source, row.Format)
	}
	if strings.Contains(row.Content, "client content") || strings.Contains(strings.ToLower(row.Content), "<script") || strings.Contains(row.Content, "alert(1)") {
		t.Errorf("empty source allowed client HTML to become content: %q", row.Content)
	}
	if got, _ := created["content"].(string); got != row.Content {
		t.Errorf("response content %q differs from stored rendered empty source %q", got, row.Content)
	}
}

func TestRestoringLegacyUnsafeHTMLRevisionDoesNotResurrectScript(t *testing.T) {
	h := newAccessHarness(t)
	user := h.user("author", "editor-regression-restore-unsafe")
	token := h.token(user)
	id, _ := edtCreate(t, h, token, map[string]any{"title": "safe current", "content": "<p>safe current</p>"})
	legacy := core.Revision{
		PostID: id, UserID: user.ID, Kind: "manual", Title: "legacy unsafe",
		Content: `<p>legacy</p><script>alert(1)</script>`, Format: "html",
	}
	if err := h.db.Create(&legacy).Error; err != nil {
		t.Fatalf("insert pre-hardening legacy revision: %v", err)
	}
	r := h.request(http.MethodPost, fmt.Sprintf("/api/admin/posts/%d/revisions/%d/restore", id, legacy.ID), "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(r, "restore legacy unsafe HTML revision")
	if r.code != http.StatusOK {
		t.Fatalf("restore legacy revision returned %d: %s", r.code, r.body)
	}
	row := edtLoadRow(t, h, id)
	if strings.Contains(strings.ToLower(row.Content), "<script") || strings.Contains(row.Content, "alert(1)") {
		t.Errorf("restoring legacy HTML revived unsafe script: %q", row.Content)
	}
	if !strings.Contains(row.Content, "legacy") {
		t.Errorf("restore lost safe legacy prose: %q", row.Content)
	}
}
