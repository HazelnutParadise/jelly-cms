package server

// WebMCP integration tests for ticket 13 (docs/tickets/13-webmcp.md): slots W2 (public JSON API behind the
// tools), W3 (script, tool list and page injection), W4 (plugin and theme declarations) and W5 (the
// webmcp_enabled switch and its settings UI). They are written before the implementation and are expected to
// fail now: the routes answer 501, the theme pipeline does not inject the script, Runtime.LoadedPlugins
// returns nil and the settings page has no switch. Do not edit them to match an implementation. Run with
// JELLY_TEST_POSTGRES=1; the harness skips otherwise.
//
// Fixtures live in testdata/webmcp: plugins/webmcp-fixture (a manifest with valid and invalid webmcp_tools
// entries) and themes/webmcp-alt (a second theme whose layout writes <HEAD> in upper case and which declares
// its own webmcp_tools). The harness copies them into its work directory as data/plugins/webmcp-fixture and
// web/themes/webmcp-alt; it never touches web/themes/default.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

const (
	webmcpTag           = `<script type="module" src="/_jelly/webmcp.js"></script>`
	webmcpFixturePlugin = "webmcp-fixture"
	webmcpAltTheme      = "webmcp-alt"
	webmcpAltMarker     = `class="webmcp-alt-theme"`
)

var (
	webmcpCorrelationPattern = regexp.MustCompile(`^req_[0-9a-z]+$`)
	webmcpHeadEndPattern     = regexp.MustCompile(`(?i)</head>`)
	webmcpBuiltinNames       = []string{"search_content", "list_posts", "get_post", "get_site_info"}
)

type webmcpHarness struct {
	*accessHarness
	tm      *theme.Manager
	runtime *plugin.Runtime
	admin   core.User
	token   string
}

// newWebMCPHarness rebuilds the routes on a theme manager with "default" active and a real plugin runtime,
// and copies the plugin and theme fixtures into the isolated work directory.
func newWebMCPHarness(t *testing.T) *webmcpHarness {
	t.Helper()
	h := newAccessHarness(t)
	fixtures := filepath.Join(repoRoot(t), "internal", "server", "testdata", "webmcp")
	if err := copyAccessTree(filepath.Join(fixtures, "themes", webmcpAltTheme), filepath.Join("web", "themes", webmcpAltTheme)); err != nil {
		t.Fatalf("copy theme fixture: %v", err)
	}
	if err := copyAccessTree(filepath.Join(fixtures, "plugins", webmcpFixturePlugin), filepath.Join("data", "plugins", webmcpFixturePlugin)); err != nil {
		t.Fatalf("copy plugin fixture: %v", err)
	}
	runtime, err := plugin.NewRuntime()
	if err != nil {
		t.Fatalf("create plugin runtime: %v", err)
	}
	t.Cleanup(runtime.Close)
	tm := theme.NewManager("web/themes")
	if err := tm.Activate("default"); err != nil {
		t.Fatalf("activate default theme: %v", err)
	}
	e := echo.New()
	RegisterRoutes(e, tm, runtime)
	h.echo = e
	admin := h.user("admin", "webmcp-admin")
	return &webmcpHarness{accessHarness: h, tm: tm, runtime: runtime, admin: admin, token: h.token(admin)}
}

func (h *webmcpHarness) page(path string) accessResult {
	h.t.Helper()
	r := h.request(http.MethodGet, path, "", "", "text/html", "")
	h.mustNoPanic(r, "GET "+path)
	return r
}

func (h *webmcpHarness) getJSON(path string) accessResult {
	h.t.Helper()
	r := h.request(http.MethodGet, path, "", "", echo.MIMEApplicationJSON, "")
	h.mustNoPanic(r, "GET "+path)
	return r
}

func (h *webmcpHarness) adminJSON(method, path, body string) accessResult {
	h.t.Helper()
	r := h.request(method, path, body, h.token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(r, method+" "+path)
	return r
}

func (h *webmcpHarness) setEnabled(t *testing.T, value string) {
	t.Helper()
	r := h.adminJSON(http.MethodPost, "/api/admin/settings", fmt.Sprintf(`{"webmcp_enabled":%q}`, value))
	if r.code != http.StatusOK {
		t.Fatalf("POST /api/admin/settings webmcp_enabled=%s status = %d, want 200; body=%q", value, r.code, archivesSnippet(r.body))
	}
}

func (h *webmcpHarness) activateTheme(t *testing.T, name string) {
	t.Helper()
	r := h.adminJSON(http.MethodPost, "/api/admin/themes/activate", fmt.Sprintf(`{"name":%q}`, name))
	if r.code != http.StatusOK {
		t.Fatalf("activate theme %s status = %d, want 200; body=%q", name, r.code, archivesSnippet(r.body))
	}
}

func (h *webmcpHarness) loadPlugin(t *testing.T, id string) {
	t.Helper()
	if err := h.runtime.Load(context.Background(), filepath.Join("data", "plugins", id)); err != nil {
		t.Fatalf("load plugin %s: %v", id, err)
	}
}

func (h *webmcpHarness) unloadPlugin(t *testing.T, id string) {
	t.Helper()
	if err := h.runtime.Unload(context.Background(), id); err != nil {
		t.Fatalf("unload plugin %s: %v", id, err)
	}
}

// webmcpSeed creates a published, public post (or page) by the harness admin at noon UTC on the given day.
func (h *webmcpHarness) seed(t *testing.T, spec pubThemePost) core.Post {
	t.Helper()
	if spec.publishedAt.IsZero() {
		spec.publishedAt = archivesDay(2024, time.March, 1)
	}
	return pubThemeSeedPost(t, h.accessHarness, h.admin, spec)
}

// webmcpWantInjectedOnce requires the WebMCP tag exactly once, before the last </head> of the page.
func webmcpWantInjectedOnce(t *testing.T, path string, r accessResult, wantCode int) {
	t.Helper()
	if r.code != wantCode {
		t.Errorf("GET %s status = %d, want %d; body=%q", path, r.code, wantCode, archivesSnippet(r.body))
		return
	}
	if n := strings.Count(r.body, webmcpTag); n != 1 {
		t.Errorf("GET %s: WebMCP script tag appears %d times, want exactly 1", path, n)
		return
	}
	ends := webmcpHeadEndPattern.FindAllStringIndex(r.body, -1)
	if len(ends) == 0 {
		t.Errorf("GET %s: page has no </head>", path)
		return
	}
	lastHeadEnd := ends[len(ends)-1][0]
	if at := strings.Index(r.body, webmcpTag); at+len(webmcpTag) != lastHeadEnd {
		t.Errorf("GET %s: WebMCP tag is not placed right before the last </head> (tag at %d, </head> at %d)", path, at, lastHeadEnd)
	}
}

func webmcpWantNotInjected(t *testing.T, path string, r accessResult) {
	t.Helper()
	if strings.Contains(r.body, "/_jelly/webmcp.js") {
		t.Errorf("GET %s (status %d) references /_jelly/webmcp.js, want no WebMCP script", path, r.code)
	}
}

// webmcpHasToolsPolicy reports whether a Permissions-Policy header disables the tools feature.
func webmcpHasToolsPolicy(header http.Header) bool {
	for _, value := range header.Values("Permissions-Policy") {
		for _, directive := range strings.Split(value, ",") {
			if strings.ReplaceAll(strings.TrimSpace(directive), " ", "") == "tools=()" {
				return true
			}
		}
	}
	return false
}

type webmcpToolEntry struct {
	Name        string                     `json:"name"`
	Title       string                     `json:"title"`
	Description string                     `json:"description"`
	InputSchema map[string]json.RawMessage `json:"input_schema"`
	Endpoint    string                     `json:"endpoint"`
	Annotations map[string]json.RawMessage `json:"annotations"`
	Source      string                     `json:"source"`
}

// webmcpTools fetches and decodes GET /_jelly/webmcp-tools.json; it fails the test unless the answer is 200 JSON.
func (h *webmcpHarness) tools(t *testing.T) []webmcpToolEntry {
	t.Helper()
	r := h.getJSON("/_jelly/webmcp-tools.json")
	if r.code != http.StatusOK {
		t.Fatalf("GET /_jelly/webmcp-tools.json status = %d, want 200; body=%q", r.code, archivesSnippet(r.body))
	}
	if ct := r.header.Get(echo.HeaderContentType); !strings.HasPrefix(ct, echo.MIMEApplicationJSON) {
		t.Errorf("GET /_jelly/webmcp-tools.json Content-Type = %q, want application/json", ct)
	}
	var list struct {
		Tools []webmcpToolEntry `json:"tools"`
	}
	if err := json.Unmarshal([]byte(r.body), &list); err != nil {
		t.Fatalf("decode tool list: %v; body=%q", err, archivesSnippet(r.body))
	}
	return list.Tools
}

func webmcpToolNames(tools []webmcpToolEntry) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

func webmcpFind(tools []webmcpToolEntry, name string) (webmcpToolEntry, int) {
	var found webmcpToolEntry
	count := 0
	for _, tool := range tools {
		if tool.Name == name {
			if count == 0 {
				found = tool
			}
			count++
		}
	}
	return found, count
}

func webmcpBoolAnnotation(tool webmcpToolEntry, key string) (value, present bool) {
	raw, ok := tool.Annotations[key]
	if !ok {
		return false, false
	}
	var v bool
	if err := json.Unmarshal(raw, &v); err != nil {
		return false, true
	}
	return v, true
}

func webmcpWantSchemaObject(t *testing.T, tool webmcpToolEntry) {
	t.Helper()
	var kind string
	if raw, ok := tool.InputSchema["type"]; !ok || json.Unmarshal(raw, &kind) != nil || kind != "object" {
		t.Errorf("tool %s input_schema = %v, want a JSON Schema object with type \"object\"", tool.Name, tool.InputSchema)
	}
}

func webmcpSchemaProperties(tool webmcpToolEntry) map[string]json.RawMessage {
	var props map[string]json.RawMessage
	if raw, ok := tool.InputSchema["properties"]; ok {
		_ = json.Unmarshal(raw, &props)
	}
	return props
}

func webmcpSchemaRequired(tool webmcpToolEntry) []string {
	var required []string
	if raw, ok := tool.InputSchema["required"]; ok {
		_ = json.Unmarshal(raw, &required)
	}
	return required
}

// webmcpLogCapture collects the standard logger output while a test runs.
type webmcpLogCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *webmcpLogCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *webmcpLogCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func webmcpCaptureLog(t *testing.T) *webmcpLogCapture {
	t.Helper()
	capture := &webmcpLogCapture{}
	previous := log.Writer()
	log.SetOutput(capture)
	t.Cleanup(func() { log.SetOutput(previous) })
	return capture
}

// webmcpLogLine returns the first log line that mentions WebMCP, the source and the tool name.
func webmcpLogLine(logs, source, name string) (string, bool) {
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(strings.ToLower(line), "webmcp") && strings.Contains(line, source) && strings.Contains(line, name) {
			return line, true
		}
	}
	return "", false
}

func webmcpJSONKeys(t *testing.T, where string, raw []byte) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("%s: decode JSON object: %v; body=%q", where, err, archivesSnippet(string(raw)))
	}
	return object
}

func webmcpWantKeys(t *testing.T, where string, object map[string]json.RawMessage, want ...string) {
	t.Helper()
	got := make([]string, 0, len(object))
	for key := range object {
		got = append(got, key)
	}
	sort.Strings(got)
	sorted := append([]string(nil), want...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(got, sorted) {
		t.Errorf("%s keys = %v, want exactly %v", where, got, sorted)
	}
}

func webmcpString(t *testing.T, where string, raw json.RawMessage) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Errorf("%s = %s, want a JSON string", where, raw)
	}
	return s
}

func webmcpInt(t *testing.T, where string, raw json.RawMessage) int {
	t.Helper()
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		t.Errorf("%s = %s, want a JSON integer", where, raw)
	}
	return n
}

// ---------------------------------------------------------------------------------------------------------
// W3: script tag injection on public theme pages
// ---------------------------------------------------------------------------------------------------------

// TestWebMCPInjectsScriptOnceOnDefaultThemePages: every public page rendered by the default theme, including
// the theme 404 page, carries the WebMCP module script exactly once, right before </head>.
func TestWebMCPInjectsScriptOnceOnDefaultThemePages(t *testing.T) {
	h := newWebMCPHarness(t)
	category := pubThemeCategory(t, h.accessHarness, "WebMCP 分類", "webmcp-cat", nil, 0, false)
	h.seed(t, pubThemePost{title: "WebMCP 注入測試文章", slug: "webmcp-inject-post", content: "<p>webmcpneedle 內文</p>", categories: []core.Category{category}})
	h.seed(t, pubThemePost{title: "WebMCP 關於頁", slug: "webmcp-about", kind: "page"})

	for _, tc := range []struct {
		path string
		code int
	}{
		{"/", http.StatusOK},
		{"/posts", http.StatusOK},
		{"/post/webmcp-inject-post", http.StatusOK},
		{"/search?q=webmcpneedle", http.StatusOK},
		{"/webmcp-about", http.StatusOK},
		{"/c/webmcp-cat", http.StatusOK},
		{"/archive", http.StatusOK},
		{"/webmcp-no-such-page", http.StatusNotFound},
	} {
		webmcpWantInjectedOnce(t, tc.path, h.page(tc.path), tc.code)
	}
}

// TestWebMCPInjectsScriptOnceOnSecondTheme: injection does not depend on the theme layout; the fixture theme
// writes <HEAD> in upper case and has no script tag of its own.
func TestWebMCPInjectsScriptOnceOnSecondTheme(t *testing.T) {
	h := newWebMCPHarness(t)
	h.seed(t, pubThemePost{title: "第二主題文章", slug: "webmcp-alt-post", content: "<p>altneedle 內文</p>"})
	h.activateTheme(t, webmcpAltTheme)

	for _, tc := range []struct {
		path string
		code int
	}{
		{"/", http.StatusOK},
		{"/posts", http.StatusOK},
		{"/post/webmcp-alt-post", http.StatusOK},
		{"/search?q=altneedle", http.StatusOK},
		{"/webmcp-alt-no-such-page", http.StatusNotFound},
	} {
		r := h.page(tc.path)
		if !strings.Contains(r.body, webmcpAltMarker) {
			t.Errorf("GET %s was not rendered by the %s fixture theme (status %d); body=%q", tc.path, webmcpAltTheme, r.code, archivesSnippet(r.body))
			continue
		}
		webmcpWantInjectedOnce(t, tc.path, r, tc.code)
	}
}

// TestWebMCPDoesNotInjectOutsideThemePages: admin screens, the login page, account pages and JSON answers
// never reference the WebMCP script.
func TestWebMCPDoesNotInjectOutsideThemePages(t *testing.T) {
	h := newWebMCPHarness(t)
	h.seed(t, pubThemePost{title: "JSON 搜尋目標", slug: "webmcp-json-target", content: "<p>jsonneedle</p>"})

	for _, path := range []string{"/admin", "/admin/settings", "/admin/posts"} {
		r := h.request(http.MethodGet, path, "", h.token, "text/html", "")
		h.mustNoPanic(r, "GET "+path)
		if r.code != http.StatusOK {
			t.Errorf("GET %s as admin status = %d, want 200", path, r.code)
		}
		webmcpWantNotInjected(t, path, r)
	}
	login := h.page("/login")
	if login.code != http.StatusOK {
		t.Errorf("GET /login status = %d, want 200", login.code)
	}
	webmcpWantNotInjected(t, "/login", login)
	account := h.request(http.MethodGet, "/account", "", h.token, "text/html", "")
	webmcpWantNotInjected(t, "/account", account)
	adminMissing := h.request(http.MethodGet, "/admin/webmcp-no-such-screen", "", h.token, "text/html", "")
	webmcpWantNotInjected(t, "/admin/webmcp-no-such-screen", adminMissing)

	for _, path := range []string{"/api/search?q=jsonneedle", "/api/public/site", "/api/public/posts", "/api/oauth/status", "/api/webmcp-no-such-endpoint"} {
		webmcpWantNotInjected(t, path, h.getJSON(path))
	}
}

// TestWebMCPMaintenancePageHasNoScript: while maintenance mode answers 503, the API behind the tools is
// unavailable, so the standalone maintenance page does not load the script.
func TestWebMCPMaintenancePageHasNoScript(t *testing.T) {
	h := newWebMCPHarness(t)
	maintenanceEnable(t, h.accessHarness, 1)
	r := h.page("/")
	if r.code != http.StatusServiceUnavailable {
		t.Fatalf("GET / during maintenance status = %d, want 503", r.code)
	}
	webmcpWantNotInjected(t, "/ (maintenance)", r)
}

// TestWebMCPReservedSlug: "_jelly" is a reserved first path segment, so a page cannot take it over.
func TestWebMCPReservedSlug(t *testing.T) {
	h := newWebMCPHarness(t)
	h.seed(t, pubThemePost{title: "保留路徑頁", slug: "_jelly", kind: "page"})
	if r := h.page("/_jelly"); r.code != http.StatusNotFound {
		t.Errorf("GET /_jelly status = %d, want 404 because _jelly is reserved", r.code)
	}
}

// TestWebMCPScriptIsSmallSameOriginJavaScript: the script is served as JavaScript, stays under 8 KiB and
// names no other origin.
func TestWebMCPScriptIsSmallSameOriginJavaScript(t *testing.T) {
	h := newWebMCPHarness(t)
	r := h.request(http.MethodGet, "/_jelly/webmcp.js", "", "", "*/*", "")
	h.mustNoPanic(r, "GET /_jelly/webmcp.js")
	if r.code != http.StatusOK {
		t.Fatalf("GET /_jelly/webmcp.js status = %d, want 200", r.code)
	}
	if ct := r.header.Get(echo.HeaderContentType); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("GET /_jelly/webmcp.js Content-Type = %q, want text/javascript", ct)
	}
	if n := len(r.body); n == 0 || n > 8192 {
		t.Errorf("GET /_jelly/webmcp.js body is %d bytes, want 1..8192", n)
	}
	for _, unwanted := range []string{"http://", "https://"} {
		if strings.Contains(r.body, unwanted) {
			t.Errorf("GET /_jelly/webmcp.js contains %q; the script must only talk to its own origin", unwanted)
		}
	}
	file, err := os.ReadFile(webmcpScriptFile)
	if err != nil {
		t.Fatalf("read %s: %v", webmcpScriptFile, err)
	}
	if string(file) != r.body {
		t.Errorf("GET /_jelly/webmcp.js does not serve %s byte for byte", webmcpScriptFile)
	}
}

// ---------------------------------------------------------------------------------------------------------
// W3: built-in tool list
// ---------------------------------------------------------------------------------------------------------

// TestWebMCPToolListHasFourReadOnlyBuiltins: without plugins and with the default theme the list holds the
// four core tools in a fixed order, all read-only; search_content, list_posts and get_post mark untrusted content.
func TestWebMCPToolListHasFourReadOnlyBuiltins(t *testing.T) {
	h := newWebMCPHarness(t)
	tools := h.tools(t)
	if got := webmcpToolNames(tools); !reflect.DeepEqual(got, webmcpBuiltinNames) {
		t.Fatalf("tool names = %v, want %v", got, webmcpBuiltinNames)
	}
	endpoints := map[string]string{
		"search_content": "/api/search",
		"list_posts":     "/api/public/posts",
		"get_post":       "/api/public/content/{slug}",
		"get_site_info":  "/api/public/site",
	}
	untrusted := map[string]bool{"search_content": true, "list_posts": true, "get_post": true}
	for _, tool := range tools {
		if tool.Source != "core" {
			t.Errorf("tool %s source = %q, want core", tool.Name, tool.Source)
		}
		if tool.Endpoint != endpoints[tool.Name] {
			t.Errorf("tool %s endpoint = %q, want %q", tool.Name, tool.Endpoint, endpoints[tool.Name])
		}
		if strings.TrimSpace(tool.Title) == "" || strings.TrimSpace(tool.Description) == "" {
			t.Errorf("tool %s has an empty title or description", tool.Name)
		}
		webmcpWantSchemaObject(t, tool)
		if v, ok := webmcpBoolAnnotation(tool, "readOnlyHint"); !ok || !v {
			t.Errorf("tool %s annotations.readOnlyHint = %v (present %v), want true", tool.Name, v, ok)
		}
		if untrusted[tool.Name] {
			if v, ok := webmcpBoolAnnotation(tool, "untrustedContentHint"); !ok || !v {
				t.Errorf("tool %s annotations.untrustedContentHint = %v (present %v), want true", tool.Name, v, ok)
			}
		}
		if _, ok := tool.Annotations["consequentialHint"]; ok {
			t.Errorf("tool %s publishes consequentialHint, want it omitted", tool.Name)
		}
	}
	search, _ := webmcpFind(tools, "search_content")
	if _, ok := webmcpSchemaProperties(search)["q"]; !ok || !reflect.DeepEqual(webmcpSchemaRequired(search), []string{"q"}) {
		t.Errorf("search_content input_schema = %v, want property q required", search.InputSchema)
	}
	getPost, _ := webmcpFind(tools, "get_post")
	if _, ok := webmcpSchemaProperties(getPost)["slug"]; !ok || !reflect.DeepEqual(webmcpSchemaRequired(getPost), []string{"slug"}) {
		t.Errorf("get_post input_schema = %v, want property slug required", getPost.InputSchema)
	}
	listPosts, _ := webmcpFind(tools, "list_posts")
	props := webmcpSchemaProperties(listPosts)
	for _, key := range []string{"page", "per_page"} {
		if _, ok := props[key]; !ok {
			t.Errorf("list_posts input_schema lacks property %s: %v", key, listPosts.InputSchema)
		}
	}
	if required := webmcpSchemaRequired(listPosts); len(required) != 0 {
		t.Errorf("list_posts input_schema requires %v, want no required input", required)
	}
}

// ---------------------------------------------------------------------------------------------------------
// W4: plugin and theme declarations
// ---------------------------------------------------------------------------------------------------------

// TestWebMCPPluginToolsFollowLoadState: a loaded plugin adds its valid declarations after the core tools,
// with readOnlyHint forced to true and consequentialHint dropped; invalid ones are skipped with a warning;
// unloading the plugin removes its tools.
func TestWebMCPPluginToolsFollowLoadState(t *testing.T) {
	h := newWebMCPHarness(t)
	logs := webmcpCaptureLog(t)
	h.loadPlugin(t, webmcpFixturePlugin)
	tools := h.tools(t)

	source := "plugin:" + webmcpFixturePlugin
	valid64 := "a" + strings.Repeat("b", 63)
	wantNames := append(append([]string(nil), webmcpBuiltinNames...), "fixture_events", "fixture_item", "fixture_ping", valid64)
	if got := webmcpToolNames(tools); !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("tool names with %s loaded = %v, want %v", webmcpFixturePlugin, got, wantNames)
	}
	for _, name := range wantNames[len(webmcpBuiltinNames):] {
		tool, _ := webmcpFind(tools, name)
		if tool.Source != source {
			t.Errorf("tool %s source = %q, want %q", name, tool.Source, source)
		}
		if v, ok := webmcpBoolAnnotation(tool, "readOnlyHint"); !ok || !v {
			t.Errorf("plugin tool %s readOnlyHint = %v (present %v), want true", name, v, ok)
		}
		if _, ok := tool.Annotations["consequentialHint"]; ok {
			t.Errorf("plugin tool %s publishes consequentialHint, want it omitted", name)
		}
		webmcpWantSchemaObject(t, tool)
	}
	events, _ := webmcpFind(tools, "fixture_events")
	if events.Endpoint != "/api/public/posts" || events.Title != "Fixture events" || events.Description != "List fixture events." {
		t.Errorf("fixture_events = %+v, want the declared endpoint, title and description", events)
	}
	if v, ok := webmcpBoolAnnotation(events, "untrustedContentHint"); !ok || !v {
		t.Errorf("fixture_events untrustedContentHint = %v (present %v), want the declared true", v, ok)
	}
	if _, ok := webmcpSchemaProperties(events)["page"]; !ok {
		t.Errorf("fixture_events input_schema = %v, want the declared schema", events.InputSchema)
	}
	item, _ := webmcpFind(tools, "fixture_item")
	if item.Endpoint != "/api/public/content/{slug}" {
		t.Errorf("fixture_item endpoint = %q, want the declared /api/public/content/{slug}", item.Endpoint)
	}
	if search, n := webmcpFind(tools, "search_content"); n != 1 || search.Source != "core" {
		t.Errorf("search_content appears %d times with source %q, want once from core", n, search.Source)
	}

	skipped := []string{
		"external_https", "protocol_relative", "backslash_host", "relative_path", "javascript_scheme",
		"query_in_endpoint", "admin_endpoint", "auth_endpoint", "jelly_endpoint", "Bad-Name",
		"9starts_with_digit", "a" + strings.Repeat("b", 64), "missing_description", "bad_schema", "bad_annotations",
	}
	for _, name := range skipped {
		if _, n := webmcpFind(tools, name); n != 0 {
			t.Errorf("invalid plugin tool %q is listed, want it skipped", name)
		}
	}
	output := logs.String()
	for _, name := range []string{"external_https", "protocol_relative", "backslash_host", "admin_endpoint", "Bad-Name", "search_content", "bad_schema"} {
		if _, ok := webmcpLogLine(output, source, name); !ok {
			t.Errorf("no warning that mentions webmcp, %s and the skipped tool %q; log=%q", source, name, archivesSnippet(output))
		}
	}

	h.unloadPlugin(t, webmcpFixturePlugin)
	if got := webmcpToolNames(h.tools(t)); !reflect.DeepEqual(got, webmcpBuiltinNames) {
		t.Errorf("tool names after unloading %s = %v, want only %v", webmcpFixturePlugin, got, webmcpBuiltinNames)
	}
}

// TestWebMCPSecondPluginDuplicateNameIsSkipped: when two plugins declare the same name, the one loaded first
// keeps it; the later plugin's other tools still appear.
func TestWebMCPSecondPluginDuplicateNameIsSkipped(t *testing.T) {
	h := newWebMCPHarness(t)
	logs := webmcpCaptureLog(t)
	secondID := "webmcp-fixture-two"
	dir := filepath.Join("data", "plugins", secondID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create second plugin: %v", err)
	}
	manifest := `{"id":"webmcp-fixture-two","name":"WebMCP Fixture Two","version":"1.0.0","entrypoint":"index.js",
"webmcp_tools":[{"name":"fixture_events","description":"Same name as the first plugin.","endpoint":"/api/public/site"},
{"name":"fixture_two_only","description":"Only the second plugin has this.","endpoint":"/api/public/site"}]}`
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write second manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("// no hooks\n"), 0o600); err != nil {
		t.Fatalf("write second entry: %v", err)
	}
	h.loadPlugin(t, webmcpFixturePlugin)
	h.loadPlugin(t, secondID)

	tools := h.tools(t)
	events, n := webmcpFind(tools, "fixture_events")
	if n != 1 || events.Source != "plugin:"+webmcpFixturePlugin || events.Endpoint != "/api/public/posts" {
		t.Errorf("fixture_events appears %d times as %+v, want once from plugin:%s", n, events, webmcpFixturePlugin)
	}
	if only, n := webmcpFind(tools, "fixture_two_only"); n != 1 || only.Source != "plugin:"+secondID {
		t.Errorf("fixture_two_only appears %d times as %+v, want once from plugin:%s", n, only, secondID)
	}
	if _, ok := webmcpLogLine(logs.String(), "plugin:"+secondID, "fixture_events"); !ok {
		t.Errorf("no warning for the duplicate fixture_events of plugin:%s; log=%q", secondID, archivesSnippet(logs.String()))
	}
}

// TestWebMCPThemeToolsOnlyForActiveTheme: theme declarations appear only while that theme is active, after
// core and plugin tools; a theme cannot take over a core or plugin name.
func TestWebMCPThemeToolsOnlyForActiveTheme(t *testing.T) {
	h := newWebMCPHarness(t)
	themeSource := "theme:" + webmcpAltTheme
	if _, n := webmcpFind(h.tools(t), "theme_lookup"); n != 0 {
		t.Errorf("theme_lookup is listed while the default theme is active")
	}

	h.activateTheme(t, webmcpAltTheme)
	tools := h.tools(t)
	wantNames := append(append([]string(nil), webmcpBuiltinNames...), "theme_lookup", "fixture_ping")
	if got := webmcpToolNames(tools); !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("tool names with %s active = %v, want %v", webmcpAltTheme, got, wantNames)
	}
	if lookup, _ := webmcpFind(tools, "theme_lookup"); lookup.Source != themeSource {
		t.Errorf("theme_lookup source = %q, want %q", lookup.Source, themeSource)
	}
	if listPosts, n := webmcpFind(tools, "list_posts"); n != 1 || listPosts.Source != "core" || listPosts.Endpoint != "/api/public/posts" {
		t.Errorf("list_posts appears %d times as %+v, want once from core", n, listPosts)
	}
	if ping, _ := webmcpFind(tools, "fixture_ping"); ping.Source != themeSource {
		t.Errorf("fixture_ping source = %q without the plugin, want %q", ping.Source, themeSource)
	}

	h.loadPlugin(t, webmcpFixturePlugin)
	if ping, n := webmcpFind(h.tools(t), "fixture_ping"); n != 1 || ping.Source != "plugin:"+webmcpFixturePlugin {
		t.Errorf("fixture_ping appears %d times as %+v with the plugin loaded, want once from the plugin", n, ping)
	}

	h.activateTheme(t, "default")
	if _, n := webmcpFind(h.tools(t), "theme_lookup"); n != 0 {
		t.Errorf("theme_lookup is still listed after switching back to the default theme")
	}
}

// ---------------------------------------------------------------------------------------------------------
// W5: the webmcp_enabled switch
// ---------------------------------------------------------------------------------------------------------

// TestWebMCPDisabledSwitchesOffScriptAndSendsPolicy: with webmcp_enabled=false no page loads the script,
// both /_jelly paths answer 404 and theme pages send Permissions-Policy: tools=(); the public API keeps
// working; switching back on restores everything.
func TestWebMCPDisabledSwitchesOffScriptAndSendsPolicy(t *testing.T) {
	h := newWebMCPHarness(t)
	h.seed(t, pubThemePost{title: "開關測試文章", slug: "webmcp-switch-post"})
	pages := []struct {
		path string
		code int
	}{
		{"/", http.StatusOK},
		{"/posts", http.StatusOK},
		{"/post/webmcp-switch-post", http.StatusOK},
		{"/webmcp-switch-no-such-page", http.StatusNotFound},
	}

	for _, p := range pages {
		r := h.page(p.path)
		if webmcpHasToolsPolicy(r.header) {
			t.Errorf("GET %s sends Permissions-Policy tools=() while WebMCP is enabled by default", p.path)
		}
		webmcpWantInjectedOnce(t, p.path+" (default on)", r, p.code)
	}

	h.setEnabled(t, "false")
	for _, p := range pages {
		r := h.page(p.path)
		if r.code != p.code {
			t.Errorf("GET %s with WebMCP off status = %d, want %d", p.path, r.code, p.code)
		}
		webmcpWantNotInjected(t, p.path+" (off)", r)
		if !webmcpHasToolsPolicy(r.header) {
			t.Errorf("GET %s with WebMCP off Permissions-Policy = %q, want tools=()", p.path, r.header.Values("Permissions-Policy"))
		}
	}
	for _, path := range []string{"/_jelly/webmcp.js", "/_jelly/webmcp-tools.json"} {
		if r := h.getJSON(path); r.code != http.StatusNotFound {
			t.Errorf("GET %s with WebMCP off status = %d, want 404", path, r.code)
		}
	}
	for _, path := range []string{"/api/public/site", "/api/public/posts", "/api/public/content/webmcp-switch-post"} {
		if r := h.getJSON(path); r.code != http.StatusOK {
			t.Errorf("GET %s with WebMCP off status = %d, want 200 (the public API does not follow the switch)", path, r.code)
		}
	}

	h.setEnabled(t, "true")
	for _, p := range pages {
		r := h.page(p.path)
		if webmcpHasToolsPolicy(r.header) {
			t.Errorf("GET %s still sends tools=() after WebMCP was switched back on", p.path)
		}
		webmcpWantInjectedOnce(t, p.path+" (back on)", r, p.code)
	}
	if r := h.request(http.MethodGet, "/_jelly/webmcp.js", "", "", "*/*", ""); r.code != http.StatusOK {
		t.Errorf("GET /_jelly/webmcp.js after switching back on status = %d, want 200", r.code)
	}
	if got := webmcpToolNames(h.tools(t)); !reflect.DeepEqual(got, webmcpBuiltinNames) {
		t.Errorf("tool names after switching back on = %v, want %v", got, webmcpBuiltinNames)
	}
}

// TestWebMCPSettingAcceptsOnlyBooleans: POST /api/admin/settings stores webmcp_enabled only as "true" or
// "false"; any other value is rejected with 400 and leaves the stored value alone.
func TestWebMCPSettingAcceptsOnlyBooleans(t *testing.T) {
	h := newWebMCPHarness(t)
	h.setEnabled(t, "false")
	if value, found := maintenanceOption(t, h.accessHarness, webmcpEnabledKey); !found || value != "false" {
		t.Fatalf("option %s = %q (found %v), want \"false\"", webmcpEnabledKey, value, found)
	}
	for _, bad := range []string{"yes", "0", "", "FALSE "} {
		r := h.adminJSON(http.MethodPost, "/api/admin/settings", fmt.Sprintf(`{"webmcp_enabled":%q}`, bad))
		if r.code != http.StatusBadRequest {
			t.Errorf("POST webmcp_enabled=%q status = %d, want 400", bad, r.code)
		}
	}
	if value, _ := maintenanceOption(t, h.accessHarness, webmcpEnabledKey); value != "false" {
		t.Errorf("option %s = %q after rejected writes, want it to stay \"false\"", webmcpEnabledKey, value)
	}
	h.setEnabled(t, "true")
	if value, _ := maintenanceOption(t, h.accessHarness, webmcpEnabledKey); value != "true" {
		t.Errorf("option %s = %q, want \"true\"", webmcpEnabledKey, value)
	}
}

// TestWebMCPAdminSettingsPageHasSwitch: the admin settings screen has the WebMCP switch bound to the option.
func TestWebMCPAdminSettingsPageHasSwitch(t *testing.T) {
	h := newWebMCPHarness(t)
	r := h.request(http.MethodGet, "/admin/settings", "", h.token, "text/html", "")
	h.mustNoPanic(r, "GET /admin/settings")
	if r.code != http.StatusOK {
		t.Fatalf("GET /admin/settings status = %d, want 200", r.code)
	}
	for _, want := range []string{`id="webmcpEnabled"`, `type="checkbox"`, webmcpEnabledKey} {
		if !strings.Contains(r.body, want) {
			t.Errorf("GET /admin/settings lacks %q", want)
		}
	}
	input := regexp.MustCompile(`<input[^>]*id="webmcpEnabled"[^>]*>`).FindString(r.body)
	if !strings.Contains(input, `type="checkbox"`) {
		t.Errorf("the webmcpEnabled control %q is not a checkbox", input)
	}
}

// ---------------------------------------------------------------------------------------------------------
// W2: public JSON API behind the tools
// ---------------------------------------------------------------------------------------------------------

// TestWebMCPPublicPostsShape: GET /api/public/posts lists only published, public, already published posts
// (no pages, drafts, private, scheduled or trashed rows), newest first, with exactly four item fields.
func TestWebMCPPublicPostsShape(t *testing.T) {
	h := newWebMCPHarness(t)
	h.seed(t, pubThemePost{title: "公開 一", slug: "webmcp-list-1", excerpt: "第一篇摘要", publishedAt: archivesDay(2024, time.January, 10)})
	h.seed(t, pubThemePost{title: "公開 二", slug: "webmcp-list-2", content: "<p>第二篇 <b>內文</b></p>", publishedAt: archivesDay(2024, time.February, 10)})
	h.seed(t, pubThemePost{title: "公開 三", slug: "webmcp-list-3", publishedAt: archivesDay(2024, time.March, 10)})
	h.seed(t, pubThemePost{title: "草稿", slug: "webmcp-list-draft", status: "draft", publishedAt: archivesDay(2024, time.April, 1)})
	h.seed(t, pubThemePost{title: "私密", slug: "webmcp-list-private", visibility: "private", publishedAt: archivesDay(2024, time.April, 2)})
	h.seed(t, pubThemePost{title: "排程", slug: "webmcp-list-future", publishedAt: time.Now().Add(72 * time.Hour)})
	h.seed(t, pubThemePost{title: "回收", slug: "webmcp-list-trashed", trashed: true, publishedAt: archivesDay(2024, time.April, 3)})
	h.seed(t, pubThemePost{title: "頁面", slug: "webmcp-list-page", kind: "page", publishedAt: archivesDay(2024, time.April, 4)})

	r := h.getJSON("/api/public/posts")
	if r.code != http.StatusOK {
		t.Fatalf("GET /api/public/posts status = %d, want 200; body=%q", r.code, archivesSnippet(r.body))
	}
	if ct := r.header.Get(echo.HeaderContentType); !strings.HasPrefix(ct, echo.MIMEApplicationJSON) {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	top := webmcpJSONKeys(t, "/api/public/posts", []byte(r.body))
	webmcpWantKeys(t, "/api/public/posts", top, "items", "page", "per_page", "total")
	if page := webmcpInt(t, "page", top["page"]); page != 1 {
		t.Errorf("page = %d, want 1", page)
	}
	if perPage := webmcpInt(t, "per_page", top["per_page"]); perPage != 10 {
		t.Errorf("per_page = %d, want the default 10", perPage)
	}
	if total := webmcpInt(t, "total", top["total"]); total != 3 {
		t.Errorf("total = %d, want 3 public posts", total)
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(top["items"], &items); err != nil {
		t.Fatalf("decode items: %v", err)
	}
	wantSlugs := []string{"webmcp-list-3", "webmcp-list-2", "webmcp-list-1"}
	if len(items) != len(wantSlugs) {
		t.Fatalf("items = %d, want %d", len(items), len(wantSlugs))
	}
	wantDates := []time.Time{archivesDay(2024, time.March, 10), archivesDay(2024, time.February, 10), archivesDay(2024, time.January, 10)}
	for i, item := range items {
		where := fmt.Sprintf("items[%d]", i)
		webmcpWantKeys(t, where, item, "title", "excerpt", "url", "published_at")
		if url := webmcpString(t, where+".url", item["url"]); url != "/post/"+wantSlugs[i] {
			t.Errorf("%s.url = %q, want /post/%s", where, url, wantSlugs[i])
		}
		var published time.Time
		if err := json.Unmarshal(item["published_at"], &published); err != nil || !published.Equal(wantDates[i]) {
			t.Errorf("%s.published_at = %s, want %s", where, item["published_at"], wantDates[i].Format(time.RFC3339))
		}
		if excerpt := webmcpString(t, where+".excerpt", item["excerpt"]); strings.Contains(excerpt, "<") {
			t.Errorf("%s.excerpt = %q contains markup", where, excerpt)
		}
	}
	if title := webmcpString(t, "items[2].title", items[2]["title"]); title != "公開 一" {
		t.Errorf("items[2].title = %q, want 公開 一", title)
	}
	if excerpt := webmcpString(t, "items[2].excerpt", items[2]["excerpt"]); excerpt != "第一篇摘要" {
		t.Errorf("items[2].excerpt = %q, want the hand-written excerpt", excerpt)
	}
	if excerpt := webmcpString(t, "items[1].excerpt", items[1]["excerpt"]); !strings.Contains(excerpt, "第二篇 內文") {
		t.Errorf("items[1].excerpt = %q, want text taken from the body", excerpt)
	}
}

// TestWebMCPPublicPostsPaging: per_page defaults to 10 and is capped at 50; bad numbers fall back to the
// defaults; a page past the end is an empty list, not an error.
func TestWebMCPPublicPostsPaging(t *testing.T) {
	h := newWebMCPHarness(t)
	posts := make([]core.Post, 0, 52)
	for i := 1; i <= 52; i++ {
		day := archivesDay(2023, time.January, 1).Add(time.Duration(i) * 24 * time.Hour)
		posts = append(posts, core.Post{
			Title: fmt.Sprintf("分頁 %02d", i), Slug: fmt.Sprintf("webmcp-page-%02d", i), Content: "<p>x</p>",
			Type: "post", Status: "published", Visibility: "public", AuthorID: h.admin.ID,
			PublishedAt: &day, CreatedAt: day, UpdatedAt: day,
		})
	}
	if err := h.db.Create(&posts).Error; err != nil {
		t.Fatalf("seed posts: %v", err)
	}

	for _, tc := range []struct {
		query                string
		page, perPage, items int
	}{
		{"", 1, 10, 10},
		{"?per_page=500", 1, 50, 50},
		{"?per_page=50", 1, 50, 50},
		{"?per_page=50&page=2", 2, 50, 2},
		{"?per_page=0", 1, 10, 10},
		{"?per_page=-3", 1, 10, 10},
		{"?per_page=abc", 1, 10, 10},
		{"?page=0", 1, 10, 10},
		{"?page=abc", 1, 10, 10},
		{"?page=6", 6, 10, 2},
		{"?page=99", 99, 10, 0},
	} {
		path := "/api/public/posts" + tc.query
		r := h.getJSON(path)
		if r.code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200; body=%q", path, r.code, archivesSnippet(r.body))
			continue
		}
		top := webmcpJSONKeys(t, path, []byte(r.body))
		if got := webmcpInt(t, path+" page", top["page"]); got != tc.page {
			t.Errorf("GET %s page = %d, want %d", path, got, tc.page)
		}
		if got := webmcpInt(t, path+" per_page", top["per_page"]); got != tc.perPage {
			t.Errorf("GET %s per_page = %d, want %d", path, got, tc.perPage)
		}
		if got := webmcpInt(t, path+" total", top["total"]); got != 52 {
			t.Errorf("GET %s total = %d, want 52", path, got)
		}
		if raw := strings.TrimSpace(string(top["items"])); raw == "null" {
			t.Errorf("GET %s items = null, want a JSON array", path)
		}
		var items []json.RawMessage
		_ = json.Unmarshal(top["items"], &items)
		if len(items) != tc.items {
			t.Errorf("GET %s returned %d items, want %d", path, len(items), tc.items)
		}
	}
}

// TestWebMCPPublicContentPostAndPage: GET /api/public/content/{slug} answers a public post or page as six
// fields, with text derived from the HTML: paragraphs kept, no markup, no script, matching the page.
func TestWebMCPPublicContentPostAndPage(t *testing.T) {
	h := newWebMCPHarness(t)
	body := `<h2>開場</h2><p>第一段 <strong>重點</strong> 文字</p><script>alert("agent")</script>` +
		`<p>第二段 &amp; 結尾</p>`
	published := archivesDay(2024, time.May, 5)
	h.seed(t, pubThemePost{title: "內文工具文章", slug: "webmcp-content-post", content: body, excerpt: "手寫的摘要", publishedAt: published})
	h.seed(t, pubThemePost{title: "內文工具頁面", slug: "webmcp-content-page", kind: "page", content: "<p>頁面內文</p>", publishedAt: published})

	r := h.getJSON("/api/public/content/webmcp-content-post")
	if r.code != http.StatusOK {
		t.Fatalf("GET /api/public/content/webmcp-content-post status = %d, want 200; body=%q", r.code, archivesSnippet(r.body))
	}
	object := webmcpJSONKeys(t, "post content", []byte(r.body))
	webmcpWantKeys(t, "post content", object, "type", "title", "url", "published_at", "excerpt", "text")
	if got := webmcpString(t, "type", object["type"]); got != "post" {
		t.Errorf("type = %q, want post", got)
	}
	if got := webmcpString(t, "title", object["title"]); got != "內文工具文章" {
		t.Errorf("title = %q, want 內文工具文章", got)
	}
	if got := webmcpString(t, "url", object["url"]); got != "/post/webmcp-content-post" {
		t.Errorf("url = %q, want /post/webmcp-content-post", got)
	}
	var at time.Time
	if err := json.Unmarshal(object["published_at"], &at); err != nil || !at.Equal(published) {
		t.Errorf("published_at = %s, want %s", object["published_at"], published.Format(time.RFC3339))
	}
	if got := webmcpString(t, "excerpt", object["excerpt"]); got != "手寫的摘要" {
		t.Errorf("excerpt = %q, want the hand-written excerpt", got)
	}
	text := webmcpString(t, "text", object["text"])
	if strings.Contains(text, "<") || strings.Contains(text, ">") {
		t.Errorf("text contains markup: %q", text)
	}
	if strings.Contains(text, "alert") {
		t.Errorf("text contains script source: %q", text)
	}
	for _, want := range []string{"開場", "第一段 重點 文字", "第二段 & 結尾"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q: %q", want, text)
		}
	}
	first, second := strings.Index(text, "第一段"), strings.Index(text, "第二段")
	if first < 0 || second < first || !strings.Contains(text[first:second], "\n") {
		t.Errorf("text does not keep the paragraph break between the two paragraphs: %q", text)
	}
	page := h.page("/post/webmcp-content-post")
	for _, visible := range []string{"開場", "重點", "第二段 &amp; 結尾"} {
		if !strings.Contains(page.body, visible) {
			t.Errorf("GET /post/webmcp-content-post does not show %q, so the tool text and the page disagree", visible)
		}
	}

	r = h.getJSON("/api/public/content/webmcp-content-page")
	if r.code != http.StatusOK {
		t.Fatalf("GET /api/public/content/webmcp-content-page status = %d, want 200; body=%q", r.code, archivesSnippet(r.body))
	}
	object = webmcpJSONKeys(t, "page content", []byte(r.body))
	webmcpWantKeys(t, "page content", object, "type", "title", "url", "published_at", "excerpt", "text")
	if got := webmcpString(t, "type", object["type"]); got != "page" {
		t.Errorf("page type = %q, want page", got)
	}
	if got := webmcpString(t, "url", object["url"]); got != "/webmcp-content-page" {
		t.Errorf("page url = %q, want /webmcp-content-page", got)
	}
	if got := webmcpString(t, "text", object["text"]); got != "頁面內文" {
		t.Errorf("page text = %q, want 頁面內文", got)
	}
	if got := webmcpString(t, "excerpt", object["excerpt"]); got == "" || strings.Contains(got, "<") || utf8.RuneCountInString(got) > 201 {
		t.Errorf("page excerpt = %q, want a non-empty plain-text excerpt of at most 200 characters plus an ellipsis", got)
	}
}

// TestWebMCPPublicContentHidesNonPublicLikeUnknown: drafts, private, scheduled and trashed posts and pages
// answer exactly like a slug that does not exist: same status, headers and body apart from the request id.
func TestWebMCPPublicContentHidesNonPublicLikeUnknown(t *testing.T) {
	h := newWebMCPHarness(t)
	h.seed(t, pubThemePost{title: "隱藏草稿", slug: "webmcp-hidden-draft", status: "draft"})
	h.seed(t, pubThemePost{title: "隱藏私密", slug: "webmcp-hidden-private", visibility: "private"})
	h.seed(t, pubThemePost{title: "隱藏排程", slug: "webmcp-hidden-future", publishedAt: time.Now().Add(48 * time.Hour)})
	h.seed(t, pubThemePost{title: "隱藏回收", slug: "webmcp-hidden-trashed", trashed: true})
	h.seed(t, pubThemePost{title: "隱藏頁面草稿", slug: "webmcp-hidden-page-draft", kind: "page", status: "draft"})
	h.seed(t, pubThemePost{title: "隱藏私密頁面", slug: "webmcp-hidden-page-private", kind: "page", visibility: "private"})

	type normalized struct {
		code    int
		headers []string
		body    map[string]json.RawMessage
	}
	normalize := func(path string) (normalized, string) {
		r := h.getJSON(path)
		object := map[string]json.RawMessage{}
		if err := json.Unmarshal([]byte(r.body), &object); err != nil {
			t.Errorf("GET %s body is not a JSON object: %q", path, archivesSnippet(r.body))
		}
		correlation := ""
		if raw, ok := object["correlation_id"]; ok {
			_ = json.Unmarshal(raw, &correlation)
			object["correlation_id"] = json.RawMessage(`"<id>"`)
		}
		if id := r.header.Get("X-Request-ID"); id != correlation {
			t.Errorf("GET %s X-Request-ID = %q, correlation_id = %q, want them equal", path, id, correlation)
		}
		var headers []string
		for key, values := range r.header {
			if http.CanonicalHeaderKey(key) == "X-Request-Id" {
				continue
			}
			headers = append(headers, key+": "+strings.Join(values, ","))
		}
		sort.Strings(headers)
		return normalized{code: r.code, headers: headers, body: object}, correlation
	}

	unknown, unknownID := normalize("/api/public/content/webmcp-no-such-slug")
	if unknown.code != http.StatusNotFound {
		t.Fatalf("GET unknown slug status = %d, want 404", unknown.code)
	}
	webmcpWantKeys(t, "unknown slug error", unknown.body, "code", "message", "correlation_id")
	if code := webmcpString(t, "code", unknown.body["code"]); code != "not_found" {
		t.Errorf("unknown slug code = %q, want not_found", code)
	}
	if message := webmcpString(t, "message", unknown.body["message"]); strings.TrimSpace(message) == "" {
		t.Errorf("unknown slug message is empty")
	}
	if !webmcpCorrelationPattern.MatchString(unknownID) {
		t.Errorf("unknown slug correlation_id = %q, want req_ followed by lower-case letters or digits", unknownID)
	}
	_, secondID := normalize("/api/public/content/webmcp-no-such-slug")
	if secondID == unknownID {
		t.Errorf("two requests share correlation_id %q, want one per request", secondID)
	}

	for _, slug := range []string{
		"webmcp-hidden-draft", "webmcp-hidden-private", "webmcp-hidden-future", "webmcp-hidden-trashed",
		"webmcp-hidden-page-draft", "webmcp-hidden-page-private",
	} {
		got, _ := normalize("/api/public/content/" + slug)
		if !reflect.DeepEqual(got, unknown) {
			t.Errorf("GET /api/public/content/%s = %+v, want exactly the unknown-slug answer %+v", slug, got, unknown)
		}
	}
}

// TestWebMCPPublicSiteShape: GET /api/public/site answers name, description, language and the site URL.
func TestWebMCPPublicSiteShape(t *testing.T) {
	h := newWebMCPHarness(t)
	site := func() map[string]json.RawMessage {
		req := httptest.NewRequest(http.MethodGet, "/api/public/site", nil)
		req.Host = "webmcp.example"
		req.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		h.echo.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/public/site status = %d, want 200; body=%q", rec.Code, archivesSnippet(rec.Body.String()))
		}
		object := webmcpJSONKeys(t, "/api/public/site", rec.Body.Bytes())
		webmcpWantKeys(t, "/api/public/site", object, "name", "description", "language", "url")
		return object
	}

	defaults := site()
	for key, want := range map[string]string{"name": "Jelly CMS", "description": "", "language": "zh-TW", "url": "http://webmcp.example/"} {
		if got := webmcpString(t, key, defaults[key]); got != want {
			t.Errorf("default site %s = %q, want %q", key, got, want)
		}
	}

	pubThemeSetOption(t, h.accessHarness, "site_title", "WebMCP 測試站")
	pubThemeSetOption(t, h.accessHarness, "site_description", "給代理讀的網站")
	pubThemeSetOption(t, h.accessHarness, "site_language", "en")
	configured := site()
	for key, want := range map[string]string{"name": "WebMCP 測試站", "description": "給代理讀的網站", "language": "en", "url": "http://webmcp.example/"} {
		if got := webmcpString(t, key, configured[key]); got != want {
			t.Errorf("configured site %s = %q, want %q", key, got, want)
		}
	}
}
