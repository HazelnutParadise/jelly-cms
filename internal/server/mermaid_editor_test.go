package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestMermaidEditorPreviewSaveReopenAndExportsKeepSource(t *testing.T) {
	h := newAccessHarness(t)
	user := h.user("admin", "mermaid-editor-flow")
	token := h.token(user)
	source := "# Mermaid source\n\n```mermaid\nflowchart TD\n  A[\"& <node>\"] --> B\n```\n"
	preview := h.request(http.MethodPost, edtRenderPath, edtRenderBody(t, source), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(preview, "Mermaid editor preview")
	if preview.code != http.StatusOK {
		t.Fatalf("render returned %d: %s", preview.code, preview.body)
	}
	var result struct {
		HTML     string   `json:"html"`
		Features []string `json:"features"`
	}
	if err := json.Unmarshal([]byte(preview.body), &result); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if !strings.Contains(result.HTML, `<code class="language-mermaid">`) || !strings.Contains(result.HTML, `&amp; &lt;node&gt;`) {
		t.Errorf("preview did not preserve escaped Mermaid source: %s", result.HTML)
	}
	for _, generated := range []string{"<svg", "<script"} {
		if strings.Contains(result.HTML, generated) {
			t.Errorf("preview contains generated %s; source should remain server-rendered code: %s", generated, result.HTML)
		}
	}
	if !mermaidEditorHas(result.Features, "mermaid") {
		t.Errorf("preview features %v do not include mermaid", result.Features)
	}

	id, _ := edtCreate(t, h, token, map[string]any{"title": "Mermaid editor flow", "source": source, "format": "jmd", "meta": map[string]any{"markdown": source, "kept": "yes"}})
	reopened := h.request(http.MethodGet, "/api/admin/posts/"+edtID(id), "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(reopened, "reopen Mermaid post")
	if reopened.code != http.StatusOK {
		t.Fatalf("reopen returned %d: %s", reopened.code, reopened.body)
	}
	var saved struct {
		Source string `json:"source"`
		Meta   struct {
			Markdown string `json:"markdown"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(reopened.body), &saved); err != nil {
		t.Fatalf("decode reopened post: %v", err)
	}
	if saved.Source != source || saved.Meta.Markdown != source {
		t.Errorf("reopened source/meta differ: source=%q meta.markdown=%q", saved.Source, saved.Meta.Markdown)
	}

	jmd := edtExport(h, token, id, "format=jmd")
	if jmd.code != http.StatusOK || jmd.body != source {
		t.Errorf("Jelly Markdown export status=%d source=%q", jmd.code, jmd.body)
	}
	md := edtExport(h, token, id, "format=md")
	if md.code != http.StatusOK || !strings.Contains(md.body, "```mermaid") || !strings.Contains(md.body, "A[\"& <node>\"] --> B") {
		t.Errorf("Markdown export lost Mermaid source: status=%d body=%q", md.code, md.body)
	}

	doc := exportOfflineDocument(t, h, token, id)
	if !strings.Contains(exportOfflineText(doc), "& <node>") {
		t.Errorf("standalone HTML export lost Mermaid source")
	}
	var exportedScripts strings.Builder
	for _, script := range exportOfflineElements(doc, "script") {
		if exportOfflineAttr(script, "src") != "" {
			t.Errorf("standalone export has external script %q", exportOfflineAttr(script, "src"))
		}
		exportedScripts.WriteString(exportOfflineText(script))
	}
	if !strings.Contains(exportedScripts.String(), "Pele") && !strings.Contains(exportedScripts.String(), "pele.render") {
		t.Errorf("standalone export does not contain its inline Mermaid renderer")
	}
	if !strings.Contains(exportedScripts.String(), "JellyMermaid") {
		t.Errorf("standalone export omits the Mermaid integration call")
	}
	plainID, _ := edtCreate(t, h, token, map[string]any{"title": "Plain export", "source": "No diagram here."})
	plain := edtExport(h, token, plainID, "format=html")
	if plain.code != http.StatusOK || strings.Contains(plain.body, "JellyMermaid") || strings.Contains(plain.body, "pele.global.js") {
		t.Errorf("plain standalone export unexpectedly includes Mermaid runtime: status=%d", plain.code)
	}
}

func mermaidEditorHas(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
