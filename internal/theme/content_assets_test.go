package theme

// Content-asset tests for ticket 11 slot M1 (Mermaid diagrams rendered in the browser by the vendored Pele
// build). The contract is the 契約 section of docs/tickets/11-mermaid.md: a theme calls {{contentAssets .}}
// in its layout <head>; when the page content holds a mermaid code block the function emits the Pele and
// jelly-mermaid.js <script> tags plus the DOMContentLoaded call, otherwise nothing. These tests are expected
// to fail now: ContentAssets and the "contentAssets" template function return "" until slot M1 is
// implemented. Do not edit them to match an implementation. They need no database.

import (
	"html/template"
	"regexp"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

const (
	mermaidTestPeleTag        = `<script src="/admin/assets/pele/pele.global.js" defer></script>`
	mermaidTestIntegrationTag = `<script src="/admin/assets/jelly-mermaid.js" defer></script>`
	mermaidTestRenderCall     = `JellyMermaid.render(document)`
	mermaidTestBlock          = "<pre><code class=\"language-mermaid\">flowchart TD\n  A[開始] --&gt; B[結束]\n</code></pre>\n"
)

// mermaidTestWantAssets requires the full Mermaid asset set, once, in order: Pele, then the integration
// script, then the inline DOMContentLoaded call.
func mermaidTestWantAssets(t *testing.T, label, out string) {
	t.Helper()
	pele := strings.Index(out, mermaidTestPeleTag)
	integration := strings.Index(out, mermaidTestIntegrationTag)
	if pele < 0 {
		t.Errorf("%s: output does not contain %s\noutput: %q", label, mermaidTestPeleTag, out)
	}
	if integration < 0 {
		t.Errorf("%s: output does not contain %s\noutput: %q", label, mermaidTestIntegrationTag, out)
	}
	if pele >= 0 && integration >= 0 && pele > integration {
		t.Errorf("%s: the Pele tag must come before jelly-mermaid.js\noutput: %q", label, out)
	}
	if n := strings.Count(out, "pele.global.js"); n > 1 {
		t.Errorf("%s: pele.global.js is referenced %d times, want once\noutput: %q", label, n, out)
	}
	if n := strings.Count(out, "jelly-mermaid.js"); n > 1 {
		t.Errorf("%s: jelly-mermaid.js is referenced %d times, want once\noutput: %q", label, n, out)
	}
	call := strings.Index(out, mermaidTestRenderCall)
	if call < 0 || !strings.Contains(out, "DOMContentLoaded") {
		t.Errorf("%s: output must call %s on DOMContentLoaded\noutput: %q", label, mermaidTestRenderCall, out)
	} else if integration >= 0 && call < integration {
		t.Errorf("%s: the DOMContentLoaded call must come after the jelly-mermaid.js tag\noutput: %q", label, out)
	}
}

func mermaidTestWantNothing(t *testing.T, label, out string) {
	t.Helper()
	if strings.TrimSpace(out) != "" {
		t.Errorf("%s: output = %q, want empty (no mermaid block in the content)", label, out)
	}
}

func TestContentAssetsEmptyWithoutMermaidBlock(t *testing.T) {
	cases := map[string][]string{
		"no contents":          nil,
		"empty content":        {""},
		"plain prose":          {"<p>沒有圖表的文章。</p>"},
		"other code languages": {"<pre><code class=\"language-go\">package main</code></pre><pre><code>plain</code></pre>"},
		"class name as text":   {"<p>在程式碼區塊標上 language-mermaid 就會畫成圖。</p>"},
		"class name in inline code": {
			"<p>用 <code>```mermaid</code> 開頭，輸出是 <code>class=\"language-mermaid\"</code>。</p>",
		},
		"similar class":       {"<pre><code class=\"language-mermaidx\">x</code></pre><pre><code class=\"xlanguage-mermaid\">y</code></pre>"},
		"class on a non-code": {"<div class=\"language-mermaid\">flowchart TD</div>"},
	}
	for name, contents := range cases {
		mermaidTestWantNothing(t, name, string(ContentAssets(contents...)))
	}
}

func TestContentAssetsEmitsScriptsForMermaidBlock(t *testing.T) {
	cases := map[string][]string{
		"single block":          {mermaidTestBlock},
		"several blocks":        {mermaidTestBlock + "<p>中間</p>" + mermaidTestBlock + mermaidTestBlock},
		"class among others":    {"<pre><code class=\"hljs language-mermaid\">sequenceDiagram\n  甲->>乙: 你好\n</code></pre>"},
		"second content":        {"<p>沒有圖</p>", mermaidTestBlock},
		"two contents with one": {mermaidTestBlock, mermaidTestBlock},
	}
	for name, contents := range cases {
		mermaidTestWantAssets(t, name, string(ContentAssets(contents...)))
	}
}

var mermaidTestURLAttr = regexp.MustCompile(`(?i)\b(src|href)\s*=\s*["']([^"']*)["']`)

func TestContentAssetsUsesOnlySameSitePaths(t *testing.T) {
	out := string(ContentAssets(mermaidTestBlock))
	urls := mermaidTestURLAttr.FindAllStringSubmatch(out, -1)
	if len(urls) < 2 {
		t.Fatalf("output has %d src/href attributes, want at least the two script sources\noutput: %q", len(urls), out)
	}
	for _, m := range urls {
		value := m[2]
		if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
			t.Errorf("%s=%q is not a same-site absolute path", m[1], value)
		}
		if strings.Contains(strings.ToLower(value), "http") {
			t.Errorf("%s=%q must not name a scheme or host", m[1], value)
		}
		if !strings.HasPrefix(value, "/admin/assets/") {
			t.Errorf("%s=%q must be served from the vendored /admin/assets/ directory", m[1], value)
		}
	}
	for _, bad := range []string{"cdn.", "jsdelivr", "unpkg", "pele.run"} {
		if strings.Contains(out, bad) {
			t.Errorf("output references an external host: contains %q", bad)
		}
	}
}

// mermaidTestTheme builds a demo theme whose layout calls {{contentAssets .}} in its head.
func mermaidTestTheme(t *testing.T) *Manager {
	t.Helper()
	return partialsTheme(t, map[string]string{
		"layout.html": `{{define "layout"}}<html><head>{{contentAssets .}}</head><body>{{template "content" .}}</body></html>{{end}}`,
		"page.html":   `{{define "content"}}<main>body</main>{{end}}`,
	})
}

func mermaidTestRender(t *testing.T, m *Manager, data any) string {
	t.Helper()
	out, err := m.Render("page.html", data)
	if err != nil {
		t.Fatalf("Render with {{contentAssets .}} in the layout: %v", err)
	}
	return string(out)
}

func mermaidTestHead(t *testing.T, html string) string {
	t.Helper()
	start, end := strings.Index(html, "<head>"), strings.Index(html, "</head>")
	if start < 0 || end < start {
		t.Fatalf("rendered page has no <head>: %q", html)
	}
	return html[start+len("<head>") : end]
}

func TestContentAssetsTemplateFuncReadsPageContent(t *testing.T) {
	m := mermaidTestTheme(t)
	post := core.Post{Title: "圖表", Content: mermaidTestBlock}
	cases := map[string]map[string]interface{}{
		"Post value":            {"Post": post},
		"Post pointer":          {"Post": &post},
		"Page value":            {"Page": post, "IsPage": true},
		"Content template.HTML": {"Page": core.Post{Title: "頁面"}, "Content": template.HTML(mermaidTestBlock)},
		"Content string":        {"Content": mermaidTestBlock},
	}
	for name, data := range cases {
		mermaidTestWantAssets(t, name, mermaidTestHead(t, mermaidTestRender(t, m, data)))
	}
}

func TestContentAssetsTemplateFuncIgnoresListingsAndOtherData(t *testing.T) {
	m := mermaidTestTheme(t)
	withDiagram := core.Post{Title: "圖表", Content: mermaidTestBlock}
	cases := map[string]any{
		"listing of posts":    map[string]interface{}{"Posts": []core.Post{withDiagram}, "Featured": &withDiagram},
		"post without chart":  map[string]interface{}{"Post": core.Post{Content: "<p>沒有圖表</p>"}},
		"nil Post pointer":    map[string]interface{}{"Post": (*core.Post)(nil)},
		"unexpected Post":     map[string]interface{}{"Post": "not a post"},
		"Page as page number": map[string]interface{}{"Page": 2, "Posts": []core.Post{withDiagram}},
		"empty map":           map[string]interface{}{},
		"nil data":            nil,
		"non-map data":        struct{ Content string }{Content: mermaidTestBlock},
	}
	for name, data := range cases {
		mermaidTestWantNothing(t, name, mermaidTestHead(t, mermaidTestRender(t, m, data)))
	}
}
