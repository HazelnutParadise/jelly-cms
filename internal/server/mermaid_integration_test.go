package server

// Mermaid acceptance tests for ticket 11 (docs/tickets/11-mermaid.md). Slot M1 makes the default theme
// call {{contentAssets .}} so a post or page whose content holds a mermaid code block loads the vendored
// Pele build and web/admin/assets/jelly-mermaid.js, and slot M2 vendors Pele with its LICENSE, VERSION,
// rebuild script and release-checklist entry. The diagram itself is drawn in the browser; the browser
// behavior is covered by scripts/acceptance/mermaid.spec.mjs. These tests are expected to fail now: the
// skeleton's contentAssets returns "", the layout does not call it, and web/admin/assets/pele/,
// jelly-mermaid.js and scripts/vendor/pele.sh do not exist yet. Do not edit them to match an
// implementation. The file checks need no database; the HTTP checks need JELLY_TEST_POSTGRES=1.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
	xhtml "golang.org/x/net/html"
)

const (
	mermaidITPeleTag        = `<script src="/admin/assets/pele/pele.global.js" defer></script>`
	mermaidITIntegrationTag = `<script src="/admin/assets/jelly-mermaid.js" defer></script>`
	mermaidITRenderCall     = `JellyMermaid.render(document)`

	// mermaidITMarkdown is what an author types; mermaidITHTML is what the editor's Markdown converter
	// stores for it in content (marked 15 output: the source is HTML-escaped inside the code element).
	mermaidITMarkdown = "## 發布流程\n\n```mermaid\nflowchart TD\n  A[開始] --> B{\"是否 & <登入>\"}\n  B -->|是| C[後台]\n```\n"
	mermaidITHTML     = "<h2>發布流程</h2>\n<pre><code class=\"language-mermaid\">flowchart TD\n  A[開始] --&gt; B{&quot;是否 &amp; &lt;登入&gt;&quot;}\n  B --&gt;|是| C[後台]\n</code></pre>\n"
	// mermaidITSource is the diagram source the browser reads back from the code element's text.
	mermaidITSource = "flowchart TD\n  A[開始] --> B{\"是否 & <登入>\"}\n  B -->|是| C[後台]\n"
)

// mermaidITSources returns the text of every <pre><code class="language-mermaid"> in a rendered page, in
// document order. Comparing decoded text lets the theme re-serialize the content (for example &quot; as
// &#34;) while still requiring the source to survive unchanged.
func mermaidITSources(t *testing.T, path, body string) []string {
	t.Helper()
	doc, err := xhtml.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("GET %s: parse HTML: %v", path, err)
	}
	var sources []string
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode && n.Data == "code" && n.Parent != nil && n.Parent.Type == xhtml.ElementNode && n.Parent.Data == "pre" {
			for _, a := range n.Attr {
				if a.Key == "class" && strings.Contains(" "+a.Val+" ", " language-mermaid ") {
					var text strings.Builder
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						if c.Type == xhtml.TextNode {
							text.WriteString(c.Data)
						}
					}
					sources = append(sources, text.String())
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return sources
}

// mermaidITWantSources requires the page's mermaid blocks to hold exactly want, still escaped in the markup.
func mermaidITWantSources(t *testing.T, path, body string, want ...string) {
	t.Helper()
	got := mermaidITSources(t, path, body)
	if len(got) != len(want) {
		t.Errorf("GET %s: found %d mermaid code blocks %q, want %d %q", path, len(got), got, len(want), want)
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("GET %s: mermaid block %d source = %q, want %q", path, i, got[i], want[i])
		}
	}
	if strings.Contains(body, "<登入>") {
		t.Errorf("GET %s: the diagram source must stay HTML-escaped in the page, found raw <登入>", path)
	}
}

// mermaidITSeed stores one published, public post or page with the given content directly in the database.
func mermaidITSeed(t *testing.T, h *accessHarness, author core.User, kind, title, slug, content string) core.Post {
	t.Helper()
	published := time.Date(2025, time.May, 4, 12, 0, 0, 0, time.UTC)
	post := core.Post{Title: title, Slug: slug, Content: content, Type: kind, Status: "published", Visibility: "public",
		AuthorID: author.ID, PublishedAt: &published, CreatedAt: published, UpdatedAt: published}
	if err := h.db.Create(&post).Error; err != nil {
		t.Fatalf("seed %s %q: %v", kind, slug, err)
	}
	return post
}

// mermaidITHead returns the <head> section of a rendered page.
func mermaidITHead(t *testing.T, path, body string) string {
	t.Helper()
	start, end := strings.Index(body, "<head>"), strings.Index(body, "</head>")
	if start < 0 || end < start {
		t.Fatalf("GET %s: the page has no <head> section; body=%q", path, archivesSnippet(body))
	}
	return body[start:end]
}

// mermaidITWantAssets requires the Mermaid script tags exactly once, inside <head>, in order.
func mermaidITWantAssets(t *testing.T, path, body string) {
	t.Helper()
	head := mermaidITHead(t, path, body)
	for _, tag := range []string{mermaidITPeleTag, mermaidITIntegrationTag} {
		if !strings.Contains(head, tag) {
			t.Errorf("GET %s: <head> does not contain %s", path, tag)
		}
	}
	if !strings.Contains(head, mermaidITRenderCall) || !strings.Contains(head, "DOMContentLoaded") {
		t.Errorf("GET %s: <head> must call %s on DOMContentLoaded", path, mermaidITRenderCall)
	}
	if i, j := strings.Index(head, mermaidITPeleTag), strings.Index(head, mermaidITIntegrationTag); i >= 0 && j >= 0 && i > j {
		t.Errorf("GET %s: pele.global.js must load before jelly-mermaid.js", path)
	}
	for _, file := range []string{"pele.global.js", "jelly-mermaid.js"} {
		if n := strings.Count(body, file); n != 1 {
			t.Errorf("GET %s: %s is referenced %d times, want exactly once", path, file, n)
		}
	}
}

func mermaidITWantNoAssets(t *testing.T, path, body string) {
	t.Helper()
	for _, file := range []string{"pele.global.js", "jelly-mermaid.js", mermaidITRenderCall} {
		if strings.Contains(body, file) {
			t.Errorf("GET %s: a page without a mermaid block must not reference %s", path, file)
		}
	}
}

func TestMermaidPostPageLoadsDiagramAssets(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "mermaid-post-author")
	mermaidITSeed(t, h, author, "post", "Mermaid Flow Post", "mermaid-flow-post",
		"<p>流程如下：</p>\n"+mermaidITHTML+"<p>第二張：</p>\n<pre><code class=\"language-mermaid\">sequenceDiagram\n  訪客-&gt;&gt;網站: 開啟文章\n</code></pre>\n")

	path := "/post/mermaid-flow-post"
	body := archivesPage(t, h, path)
	mermaidITWantAssets(t, path, body)
	// The source stays in the page, escaped, inside its code element: the browser draws from it.
	mermaidITWantSources(t, path, body, mermaidITSource, "sequenceDiagram\n  訪客->>網站: 開啟文章\n")
	archivesWantAbsent(t, path, body, "<svg class=\"pele")

	head := mermaidITHead(t, path, body)
	for _, want := range []string{".jc-mermaid", "--pele-fg", "--pele-bg", "--pele-font", ".jc-mermaid-error"} {
		if !strings.Contains(head, want) {
			t.Errorf("GET %s: the default theme styles must map Pele to the theme: <head> lacks %q", path, want)
		}
	}
}

func TestMermaidStaticPageLoadsDiagramAssets(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("editor", "mermaid-page-author")
	mermaidITSeed(t, h, author, "page", "Mermaid Org Chart", "mermaid-org-chart",
		"<p>資料模型：</p>\n<pre><code class=\"language-mermaid\">erDiagram\n  作者 ||--o{ 文章 : 撰寫\n</code></pre>\n")

	path := "/mermaid-org-chart"
	body := archivesPage(t, h, path)
	mermaidITWantAssets(t, path, body)
	mermaidITWantSources(t, path, body, "erDiagram\n  作者 ||--o{ 文章 : 撰寫\n")
}

func TestMermaidPagesWithoutDiagramsLoadNoAssets(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "mermaid-plain-author")
	mermaidITSeed(t, h, author, "post", "Plain Code Post", "mermaid-plain-code",
		"<p>在程式碼區塊標上 language-mermaid 就會畫成圖，例如 <code>```mermaid</code>。</p>\n"+
			"<pre><code class=\"language-go\">package main</code></pre>\n<div class=\"language-mermaid\">not code</div>\n")
	mermaidITSeed(t, h, author, "page", "Plain About Page", "mermaid-plain-about", "<p>關於我們</p>")
	// The newest post has a diagram, but listings show excerpts only and must not load the assets.
	newest := mermaidITSeed(t, h, author, "post", "Newest Diagram Post", "mermaid-newest-diagram", mermaidITHTML)
	later := time.Date(2025, time.June, 1, 12, 0, 0, 0, time.UTC)
	if err := h.db.Model(&newest).Updates(map[string]any{"published_at": later, "created_at": later}).Error; err != nil {
		t.Fatalf("move the diagram post to the top of the listings: %v", err)
	}

	for _, path := range []string{"/post/mermaid-plain-code", "/mermaid-plain-about", "/", "/posts", "/search?q=Diagram", "/mermaid-no-such-page"} {
		r := archivesGet(t, h, path)
		if strings.TrimSpace(r.body) == "" {
			t.Errorf("GET %s rendered an empty body (status %d)", path, r.code)
			continue
		}
		mermaidITWantNoAssets(t, path, r.body)
	}
	// Control: the diagram post itself does load them.
	mermaidITWantAssets(t, "/post/mermaid-newest-diagram", archivesPage(t, h, "/post/mermaid-newest-diagram"))
}

func TestMermaidSourceSurvivesAdminSaveAndReopen(t *testing.T) {
	h := archivesHarness(t)
	editor := h.user("editor", "mermaid-editor")
	token := h.token(editor)

	payload, err := json.Marshal(map[string]any{
		"title": "Saved Diagram Post", "slug": "mermaid-saved-diagram", "status": "published",
		"content": mermaidITHTML, "meta": map[string]any{"markdown": mermaidITMarkdown},
	})
	if err != nil {
		t.Fatal(err)
	}
	created := h.request(http.MethodPost, "/api/admin/posts", string(payload), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	if created.code != http.StatusOK {
		t.Fatalf("POST /api/admin/posts status = %d, body=%s", created.code, created.body)
	}
	var post struct {
		ID      uint            `json:"id"`
		Content string          `json:"content"`
		Meta    json.RawMessage `json:"meta"`
	}
	if err := json.Unmarshal([]byte(created.body), &post); err != nil || post.ID == 0 {
		t.Fatalf("decode created post: %v; body=%s", err, created.body)
	}

	reopened := h.request(http.MethodGet, fmt.Sprintf("/api/admin/posts/%d", post.ID), "", token, echo.MIMEApplicationJSON, "")
	if reopened.code != http.StatusOK {
		t.Fatalf("GET /api/admin/posts/%d status = %d, body=%s", post.ID, reopened.code, reopened.body)
	}
	var again struct {
		Content string `json:"content"`
		Meta    struct {
			Markdown string `json:"markdown"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(reopened.body), &again); err != nil {
		t.Fatalf("decode reopened post: %v; body=%s", err, reopened.body)
	}
	if again.Meta.Markdown != mermaidITMarkdown {
		t.Errorf("reopened meta.markdown = %q, want the original source %q", again.Meta.Markdown, mermaidITMarkdown)
	}
	if post.Content != again.Content {
		t.Errorf("reopened content = %q, want the created content %q (saving and reopening must not mutate stored HTML)", again.Content, post.Content)
	}
	if strings.Contains(strings.ToLower(again.Content), "<svg") || strings.Contains(strings.ToLower(again.Content), "<script") {
		t.Errorf("reopened content contains rendered SVG or script; Mermaid output must never be stored: %q", again.Content)
	}

	path := "/post/mermaid-saved-diagram"
	body := archivesPage(t, h, path)
	mermaidITWantAssets(t, path, body)
	mermaidITWantSources(t, path, body, mermaidITSource)

	// Editing the diagram keeps one set of assets and shows the new source.
	editedMarkdown := strings.Replace(mermaidITMarkdown, "C[後台]", "C[管理後台]", 1)
	editedHTML := strings.Replace(mermaidITHTML, "C[後台]", "C[管理後台]", 1)
	update, err := json.Marshal(map[string]any{"title": "Saved Diagram Post", "slug": "mermaid-saved-diagram", "status": "published",
		"content": editedHTML, "meta": map[string]any{"markdown": editedMarkdown}})
	if err != nil {
		t.Fatal(err)
	}
	updated := h.request(http.MethodPut, fmt.Sprintf("/api/admin/posts/%d", post.ID), string(update), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	if updated.code != http.StatusOK {
		t.Fatalf("PUT /api/admin/posts/%d status = %d, body=%s", post.ID, updated.code, updated.body)
	}
	body = archivesPage(t, h, path)
	mermaidITWantAssets(t, path, body)
	mermaidITWantSources(t, path, body, strings.Replace(mermaidITSource, "C[後台]", "C[管理後台]", 1))
}

func TestMermaidVendoredFilesAreServed(t *testing.T) {
	h := archivesHarness(t)
	for path, want := range map[string]string{
		"/admin/assets/pele/pele.global.js": "/*! Pele ",
		"/admin/assets/jelly-mermaid.js":    "JellyMermaid",
	} {
		r := archivesGet(t, h, path)
		if r.code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", path, r.code)
			continue
		}
		if !strings.Contains(r.body, want) {
			t.Errorf("GET %s: body does not contain %q", path, want)
		}
		if ct := r.header.Get(echo.HeaderContentType); !strings.Contains(ct, "javascript") {
			t.Errorf("GET %s Content-Type = %q, want a JavaScript type", path, ct)
		}
		for _, bad := range []string{"cdn.jsdelivr.net", "unpkg.com", "https://pele.run"} {
			if path == "/admin/assets/jelly-mermaid.js" && strings.Contains(r.body, bad) {
				t.Errorf("GET %s: the integration script must not reference %q", path, bad)
			}
		}
	}
}

var (
	mermaidVersionLine = regexp.MustCompile(`(?m)^Version: (\d+\.\d+\.\d+)$`)
	mermaidCommitLine  = regexp.MustCompile(`(?m)^Commit: ([0-9a-f]{40})$`)
	mermaidBuiltLine   = regexp.MustCompile(`(?m)^Build date \(UTC\): \d{4}-\d{2}-\d{2}$`)
	mermaidPeleBanner  = regexp.MustCompile(`^/\*! Pele (\d+\.\d+\.\d+) \| MIT License`)
)

// TestMermaidVendoredPelePackage needs no database: it checks the vendored files, the rebuild script and
// the release checklist agree on one pinned version and commit.
func TestMermaidVendoredPelePackage(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "web", "admin", "assets", "pele")

	build, err := os.ReadFile(filepath.Join(dir, "pele.global.js"))
	var builtVersion string
	switch {
	case err != nil:
		t.Errorf("web/admin/assets/pele/pele.global.js: %v", err)
	case len(build) < 100_000:
		t.Errorf("web/admin/assets/pele/pele.global.js is %d bytes; want the full global build", len(build))
	default:
		if m := mermaidPeleBanner.FindSubmatch(build); m == nil {
			t.Errorf("web/admin/assets/pele/pele.global.js does not start with the upstream banner /*! Pele X.Y.Z | MIT License")
		} else {
			builtVersion = string(m[1])
		}
	}

	license, err := os.ReadFile(filepath.Join(dir, "LICENSE"))
	if err != nil {
		t.Errorf("web/admin/assets/pele/LICENSE: %v", err)
	} else if !strings.Contains(string(license), "MIT License") || !strings.Contains(string(license), "kepano") {
		t.Errorf("web/admin/assets/pele/LICENSE is not Pele's MIT license")
	}

	var version, commit string
	versionFile, err := os.ReadFile(filepath.Join(dir, "VERSION"))
	if err != nil {
		t.Errorf("web/admin/assets/pele/VERSION: %v", err)
	} else {
		text := string(versionFile)
		if m := mermaidVersionLine.FindStringSubmatch(text); m == nil {
			t.Errorf("VERSION has no `Version: X.Y.Z` line:\n%s", text)
		} else {
			version = m[1]
		}
		if m := mermaidCommitLine.FindStringSubmatch(text); m == nil {
			t.Errorf("VERSION has no `Commit: <40-hex sha>` line:\n%s", text)
		} else {
			commit = m[1]
		}
		if !mermaidBuiltLine.MatchString(text) {
			t.Errorf("VERSION has no `Build date (UTC): YYYY-MM-DD` line:\n%s", text)
		}
		if !strings.Contains(text, "Source: https://github.com/obsidianmd/pele") {
			t.Errorf("VERSION does not name the source repository https://github.com/obsidianmd/pele:\n%s", text)
		}
	}
	if version != "" && builtVersion != "" && version != builtVersion {
		t.Errorf("VERSION says %s but pele.global.js is %s", version, builtVersion)
	}

	if _, err := os.Stat(filepath.Join(root, "web", "admin", "assets", "jelly-mermaid.js")); err != nil {
		t.Errorf("web/admin/assets/jelly-mermaid.js: %v", err)
	}

	scriptPath := filepath.Join(root, "scripts", "vendor", "pele.sh")
	info, err := os.Stat(scriptPath)
	switch {
	case err != nil:
		t.Errorf("scripts/vendor/pele.sh: %v", err)
	case info.Mode().Perm()&0o111 == 0:
		t.Errorf("scripts/vendor/pele.sh is not executable (mode %v)", info.Mode().Perm())
	default:
		script, err := os.ReadFile(scriptPath)
		if err != nil {
			t.Fatalf("read scripts/vendor/pele.sh: %v", err)
		}
		for _, pin := range []string{version, commit} {
			if pin != "" && !strings.Contains(string(script), pin) {
				t.Errorf("scripts/vendor/pele.sh does not pin %q from VERSION", pin)
			}
		}
		if !strings.Contains(string(script), "npm") {
			t.Errorf("scripts/vendor/pele.sh must fetch the release with npm")
		}
	}

	checklist, err := os.ReadFile(filepath.Join(root, "docs", "release-checklist.md"))
	if err != nil {
		t.Fatalf("docs/release-checklist.md: %v", err)
	}
	for _, want := range []string{"Pele", "scripts/vendor/pele.sh", "web/admin/assets/pele/"} {
		if !strings.Contains(string(checklist), want) {
			t.Errorf("docs/release-checklist.md does not list the vendored Pele package: missing %q", want)
		}
	}
}
