package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
	nethtml "golang.org/x/net/html"
)

// The dashboard is rendered through the authenticated admin route and then its actual inline
// script is exercised by Node. The HTTP cases use the dedicated PostgreSQL archives harness.

func dashboardRoleHTML(t *testing.T, h *accessHarness, role string, user core.User) string {
	t.Helper()
	r := h.request(http.MethodGet, "/admin", "", h.token(user), echo.MIMETextHTML, "")
	h.mustNoPanic(r, role+" dashboard")
	if r.code != http.StatusOK {
		t.Fatalf("GET /admin as %s returned %d, want 200; body=%q", role, r.code, archivesSnippet(r.body))
	}
	return r.body
}

func dashboardRoleHrefs(body string) map[string]bool {
	doc, err := nethtml.Parse(strings.NewReader(body))
	if err != nil {
		return nil
	}
	hrefs := map[string]bool{}
	var walk func(*nethtml.Node)
	walk = func(n *nethtml.Node) {
		if n.Type == nethtml.ElementNode && n.Data == "a" {
			for _, attr := range n.Attr {
				if attr.Key == "href" {
					hrefs[attr.Val] = true
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return hrefs
}

func dashboardRenderedScript(t *testing.T, body string) string {
	t.Helper()
	doc, err := nethtml.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse rendered dashboard: %v", err)
	}
	var found string
	var walk func(*nethtml.Node)
	walk = func(n *nethtml.Node) {
		if n.Type == nethtml.ElementNode && n.Data == "script" {
			var b strings.Builder
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == nethtml.TextNode {
					b.WriteString(c.Data)
				}
			}
			if strings.Contains(b.String(), "loadStats") {
				found = b.String()
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if found == "" {
		t.Fatal("rendered dashboard has no inline script containing loadStats")
	}
	return found
}

const dashboardNodeRunner = `
const vm = require('node:vm');
const fs = require('node:fs');
const script = fs.readFileSync(process.argv[2], 'utf8');
const scenario = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const ids = ['postsCount','productsCount','ordersCount','activeTheme','recentPosts'];
const tileNotes = {};
const nodes = Object.fromEntries(ids.map(id => {
  const node = { id, children: [], _text: '???', _html: '',
    appendChild(child) { this.children.push(child); }
  };
  Object.defineProperty(node, 'textContent', { get() { return this._text; }, set(value) {
    this._text = String(value); this._html = this._text;
  }});
  Object.defineProperty(node, 'innerHTML', { get() { return this._html; }, set(value) {
    this._html = String(value); this._text = this._html.replace(/<[^>]*>/g, '');
  }});
  if (id !== 'recentPosts') {
    const note = { _text: '', get textContent() { return this._text; }, set textContent(value) { this._text = String(value); }};
    const tile = { querySelector(selector) { return selector === '.tile-note' ? note : null; }};
    node.closest = selector => selector === '.tile' ? tile : null;
    tileNotes[id] = note;
  }
  if (id === 'recentPosts') node.innerHTML = '<tr><td>loading</td></tr>';
  return [id, node];
}));
global.document = {
  getElementById(id) { return nodes[id] || null; },
  createElement(tag) { return { tag, textContent: '', innerHTML: '', appendChild() {} }; }
};
global.fetch = async url => {
  scenario.urls.push(String(url));
  const item = scenario.responses[String(url)] || { status: 500, body: {} };
  return { ok: item.status >= 200 && item.status < 300, status: item.status, json: async () => item.body };
};
global.console = { error() {}, log: process.stdout.write.bind(process.stdout) };
vm.runInThisContext(script, { filename: 'rendered-dashboard.js' });
const deadline = Date.now() + 2000;
(async () => {
  // Observe the one automatic loadStats invocation; never invoke it a second time.
  while (Date.now() < deadline && scenario.urls.length < scenario.minimumCalls) {
    await new Promise(resolve => setTimeout(resolve, 1));
  }
  // Let the sequential awaits and final DOM writes settle, bounded for stuck requests.
  let stable = 0, previous = -1;
  while (Date.now() < deadline && stable < 5) {
    await new Promise(resolve => setTimeout(resolve, 5));
    if (scenario.urls.length === previous) stable++; else { previous = scenario.urls.length; stable = 0; }
  }
  console.log(JSON.stringify({
    urls: scenario.urls,
    text: Object.fromEntries(ids.map(id => [id, String(nodes[id].textContent)])),
    html: Object.fromEntries(ids.map(id => [id, String(nodes[id].innerHTML)])),
    notes: Object.fromEntries(Object.entries(tileNotes).map(([id, note]) => [id, String(note.textContent)])),
    recentHTML: nodes.recentPosts.innerHTML,
    recentRows: nodes.recentPosts.children.length,
    recentLoading: /loading/i.test(nodes.recentPosts.innerHTML)
  }));
})().catch(error => { console.error(error); process.exitCode = 1; });
`

type dashboardScriptScenario struct {
	name         string
	responses    map[string]dashboardScriptResponse
	minimumCalls int
	wantCalls    []string
	forbidCalls  []string
	wantText     map[string]string
	wantAnyText  map[string][]string
	forbidText   []string
	wantRows     int
	checkRows    bool
	wantLoading  bool
}

type dashboardScriptResponse struct {
	Status int `json:"status"`
	Body   any `json:"body"`
}

type dashboardScriptResult struct {
	URLs          []string          `json:"urls"`
	Text          map[string]string `json:"text"`
	HTML          map[string]string `json:"html"`
	Notes         map[string]string `json:"notes"`
	RecentHTML    string            `json:"recentHTML"`
	RecentRows    int               `json:"recentRows"`
	RecentLoading bool              `json:"recentLoading"`
}

func dashboardRunRenderedScript(t *testing.T, script string, scenario dashboardScriptScenario) dashboardScriptResult {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is unavailable; rendered dashboard script behavior was not executed")
	}
	work := t.TempDir()
	scriptPath := filepath.Join(work, "dashboard.js")
	runnerPath := filepath.Join(work, "runner.cjs")
	configPath := filepath.Join(work, "scenario.json")
	scenarioJSON, err := json.Marshal(struct {
		Responses    map[string]dashboardScriptResponse `json:"responses"`
		MinimumCalls int                                `json:"minimumCalls"`
		URLs         []string                           `json:"urls"`
	}{scenario.responses, scenario.minimumCalls, []string{}})
	if err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string][]byte{scriptPath: []byte(script), runnerPath: []byte(dashboardNodeRunner), configPath: scenarioJSON} {
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatalf("write Node fixture: %v", err)
		}
	}
	cmd := exec.Command(node, runnerPath, scriptPath, configPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("execute rendered dashboard script with Node: %v\n%s", err, output)
	}
	var result dashboardScriptResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode Node dashboard result: %v; output=%s", err, output)
	}
	return result
}

var dashboardNumericZero = regexp.MustCompile(`^0$`)

func dashboardAssertScenario(t *testing.T, script string, scenario dashboardScriptScenario) {
	t.Helper()
	result := dashboardRunRenderedScript(t, script, scenario)
	seen := map[string]int{}
	for _, path := range result.URLs {
		seen[path]++
	}
	for _, path := range scenario.wantCalls {
		if seen[path] != 1 {
			t.Errorf("%s requested %q %d times, want once; requests=%v", scenario.name, path, seen[path], result.URLs)
		}
	}
	for _, path := range scenario.forbidCalls {
		if seen[path] != 0 {
			t.Errorf("%s requested forbidden %q; requests=%v", scenario.name, path, result.URLs)
		}
	}
	for key, want := range scenario.wantText {
		if got := result.Text[key]; got != want {
			t.Errorf("%s rendered %s=%q, want %q", scenario.name, key, got, want)
		}
	}
	for key, wants := range scenario.wantAnyText {
		got := result.Text[key]
		matched := false
		for _, want := range wants {
			if got == want {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("%s rendered %s=%q, want one of %q", scenario.name, key, got, wants)
		}
	}
	for _, value := range append(append([]string{}, result.Text["postsCount"], result.Text["productsCount"], result.Text["ordersCount"], result.Text["activeTheme"]), result.RecentHTML) {
		if strings.Contains(value, "undefined") {
			t.Errorf("%s rendered undefined: %q", scenario.name, value)
		}
	}
	for id, value := range result.HTML {
		if strings.Contains(value, "undefined") {
			t.Errorf("%s rendered undefined in %s HTML: %q", scenario.name, id, value)
		}
	}
	for key, value := range result.Notes {
		if strings.Contains(value, "undefined") {
			t.Errorf("%s rendered undefined in %s tile note: %q", scenario.name, key, value)
		}
	}
	if scenario.checkRows && result.RecentRows != scenario.wantRows {
		t.Errorf("%s rendered %d recent rows, want %d", scenario.name, result.RecentRows, scenario.wantRows)
	}
	if result.RecentLoading != scenario.wantLoading {
		t.Errorf("%s recentPosts loading state=%v, want %v (%q)", scenario.name, result.RecentLoading, scenario.wantLoading, result.RecentHTML)
	}
	for _, forbidden := range scenario.forbidText {
		for _, value := range []string{result.Text["postsCount"], result.Text["productsCount"], result.Text["ordersCount"], result.Text["activeTheme"], result.RecentHTML} {
			if strings.Contains(value, forbidden) {
				t.Errorf("%s rendered forbidden text %q in %q", scenario.name, forbidden, value)
			}
		}
	}
	for _, key := range []string{"postsCount", "productsCount", "ordersCount"} {
		if strings.Contains(strings.ToLower(scenario.name), "failure") && dashboardNumericZero.MatchString(result.Text[key]) {
			t.Errorf("%s represented unknown %s as zero", scenario.name, key)
		}
	}
}

func dashboardFailureMessageVisible(result dashboardScriptResult) bool {
	values := []string{result.RecentHTML}
	for _, value := range result.Text {
		values = append(values, value)
	}
	for _, value := range result.HTML {
		values = append(values, value)
	}
	for _, value := range result.Notes {
		values = append(values, value)
	}
	joined := strings.Join(values, " ")
	return strings.Contains(joined, "Could not load data") || strings.Contains(joined, "資料載入失敗")
}

func dashboardAssertInvalidCounter(t *testing.T, script, key, path, name string, body map[string]any) {
	t.Helper()
	responses := map[string]dashboardScriptResponse{
		"/api/admin/posts":                 {Status: 200, Body: []any{}},
		"/api/admin/products":              {Status: 200, Body: map[string]any{"items": []any{}, "total": 0}},
		"/api/admin/orders":                {Status: 200, Body: map[string]any{"items": []any{}, "total": 0, "counts": map[string]any{"pending": 0}}},
		"/api/admin/settings":              {Status: 200, Body: []any{map[string]any{"key": "active_theme", "value": "default"}}},
		"/api/admin/themes/default/config": {Status: 200, Body: map[string]any{"name": "Dashboard Fixture Theme"}},
	}
	responses[path] = dashboardScriptResponse{Status: http.StatusOK, Body: body}
	result := dashboardRunRenderedScript(t, script, dashboardScriptScenario{name: name + " failure", responses: responses, minimumCalls: 5})
	if result.Text[key] == "0" || strings.Contains(result.Text[key], "undefined") {
		t.Errorf("%s rendered invalid count as %q", name, result.Text[key])
	}
	if !dashboardFailureMessageVisible(result) {
		t.Errorf("%s did not show the existing localized load-failure message", name)
	}
}

func TestDashboardRoleRenderedActionsAndSensitiveAPIStatus(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "dashboard-role-admin")
	editor := h.user("editor", "dashboard-role-editor")
	author := h.user("author", "dashboard-role-author")
	adminBody := dashboardRoleHTML(t, h, "admin", admin)
	editorBody := dashboardRoleHTML(t, h, "editor", editor)
	authorBody := dashboardRoleHTML(t, h, "author", author)
	adminScript := dashboardRenderedScript(t, adminBody)
	editorScript := dashboardRenderedScript(t, editorBody)
	authorScript := dashboardRenderedScript(t, authorBody)
	adminHrefs, editorHrefs, authorHrefs := dashboardRoleHrefs(adminBody), dashboardRoleHrefs(editorBody), dashboardRoleHrefs(authorBody)
	for role, script := range map[string]string{"editor": editorScript, "author": authorScript} {
		for _, api := range []string{"/api/admin/products", "/api/admin/orders", "/api/admin/settings", "/api/admin/themes"} {
			if strings.Contains(script, api) {
				t.Errorf("%s rendered dashboard script contains admin-only API %s", role, api)
			}
		}
	}
	for _, elementID := range []string{`id="productsCount"`, `id="ordersCount"`, `id="activeTheme"`} {
		if !strings.Contains(adminBody, elementID) {
			t.Errorf("admin rendered dashboard is missing admin-only statistic %s", elementID)
		}
		for role, body := range map[string]string{"editor": editorBody, "author": authorBody} {
			if strings.Contains(body, elementID) {
				t.Errorf("%s rendered dashboard contains admin-only statistic %s", role, elementID)
			}
		}
	}
	for _, api := range []string{"/api/admin/products", "/api/admin/orders", "/api/admin/settings", "/api/admin/themes"} {
		if !strings.Contains(adminScript, api) {
			t.Errorf("admin rendered dashboard script is missing existing API %s", api)
		}
	}
	for _, path := range []string{"/admin/posts", "/admin/posts/new", "/admin/media"} {
		for role, hrefs := range map[string]map[string]bool{"admin": adminHrefs, "editor": editorHrefs, "author": authorHrefs} {
			if !hrefs[path] {
				t.Errorf("%s dashboard is missing allowed action %s", role, path)
			}
		}
	}
	for _, path := range []string{"/admin/pages/new"} {
		for role, hrefs := range map[string]map[string]bool{"admin": adminHrefs, "editor": editorHrefs} {
			if !hrefs[path] {
				t.Errorf("%s dashboard is missing allowed action %s", role, path)
			}
		}
		if authorHrefs[path] {
			t.Errorf("author dashboard exposes %s", path)
		}
	}
	for _, path := range []string{"/admin/products/new", "/admin/products", "/admin/orders", "/admin/themes"} {
		if !adminHrefs[path] {
			t.Errorf("admin dashboard is missing %s", path)
		}
		for role, hrefs := range map[string]map[string]bool{"editor": editorHrefs, "author": authorHrefs} {
			if hrefs[path] {
				t.Errorf("%s dashboard exposes admin-only action %s", role, path)
			}
		}
	}

	// The admin-only API contract remains 403 for both editor and author, regardless of the
	// rendered dashboard's links. Page-shell GETs are intentionally not used as authorization tests.
	for _, path := range []string{"/api/admin/products", "/api/admin/orders", "/api/admin/settings", "/api/admin/themes"} {
		adminResponse := h.request(http.MethodGet, path, "", h.token(admin), echo.MIMEApplicationJSON, "")
		if adminResponse.code != http.StatusOK {
			t.Errorf("GET %s as admin returned %d, want 200; body=%q", path, adminResponse.code, archivesSnippet(adminResponse.body))
		}
		for role, user := range map[string]core.User{"editor": editor, "author": author} {
			r := h.request(http.MethodGet, path, "", h.token(user), echo.MIMEApplicationJSON, "")
			h.mustNoPanic(r, role+" "+path)
			if r.code != http.StatusForbidden {
				t.Errorf("GET %s as %s returned %d, want 403; body=%q", path, role, r.code, archivesSnippet(r.body))
			}
		}
	}
}

func TestDashboardRenderedAdminScriptHandlesListsAndFailures(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "dashboard-script-admin")
	rendered := h.request(http.MethodGet, "/admin?lang=en", "", h.token(admin), echo.MIMETextHTML, "")
	if rendered.code != http.StatusOK {
		t.Fatalf("GET /admin?lang=en returned %d; body=%q", rendered.code, archivesSnippet(rendered.body))
	}
	body := rendered.body
	script := dashboardRenderedScript(t, body)
	paths := map[string]string{
		"postsCount": "/api/admin/posts", "productsCount": "/api/admin/products", "ordersCount": "/api/admin/orders",
		"settings": "/api/admin/settings", "themes": "/api/admin/themes",
	}
	success := map[string]dashboardScriptResponse{
		paths["postsCount"]:                {Status: 200, Body: []any{map[string]any{"id": 71, "title": "Dashboard fixture post", "status": "published", "created_at": "2026-10-29T12:00:00Z"}}},
		paths["productsCount"]:             {Status: 200, Body: map[string]any{"items": []any{map[string]any{}}, "total": 25}},
		paths["ordersCount"]:               {Status: 200, Body: map[string]any{"items": []any{map[string]any{}}, "total": 4, "counts": map[string]any{"pending": 1}}},
		paths["settings"]:                  {Status: 200, Body: []any{map[string]any{"key": "active_theme", "value": "default"}}},
		"/api/admin/themes/default/config": {Status: 200, Body: map[string]any{"name": "Dashboard Fixture Theme"}},
	}
	dashboardAssertScenario(t, script, dashboardScriptScenario{name: "success", responses: success, minimumCalls: 5,
		wantCalls: []string{paths["postsCount"], paths["productsCount"], paths["ordersCount"], paths["settings"], "/api/admin/themes/default/config"},
		wantText:  map[string]string{"postsCount": "1", "productsCount": "25", "ordersCount": "1", "activeTheme": "Dashboard Fixture Theme"}, checkRows: true, wantRows: 1})

	empty := map[string]dashboardScriptResponse{}
	for _, path := range []string{paths["postsCount"], paths["settings"], paths["themes"]} {
		empty[path] = dashboardScriptResponse{Status: 200, Body: []any{}}
	}
	empty[paths["productsCount"]] = dashboardScriptResponse{Status: 200, Body: map[string]any{"items": []any{}, "total": 0}}
	empty[paths["ordersCount"]] = dashboardScriptResponse{Status: 200, Body: map[string]any{"items": []any{}, "total": 0, "counts": map[string]any{"pending": 0}}}
	dashboardAssertScenario(t, script, dashboardScriptScenario{name: "empty arrays", responses: empty, minimumCalls: 5,
		wantText:   map[string]string{"postsCount": "0", "productsCount": "0", "ordersCount": "0"},
		forbidText: []string{"undefined"}, checkRows: true, wantRows: 0})
	fallbackTheme := map[string]dashboardScriptResponse{
		paths["postsCount"]: {Status: 200, Body: []any{}}, paths["productsCount"]: {Status: 200, Body: map[string]any{"items": []any{}, "total": 0}}, paths["ordersCount"]: {Status: 200, Body: map[string]any{"items": []any{}, "total": 0, "counts": map[string]any{"pending": 0}}},
		paths["settings"]: {Status: 200, Body: []any{}}, paths["themes"]: {Status: 200, Body: []any{"default"}},
		"/api/admin/themes/default/config": {Status: 200, Body: map[string]any{"name": "Fallback Fixture Theme"}},
	}
	dashboardAssertScenario(t, script, dashboardScriptScenario{name: "theme fallback success", responses: fallbackTheme, minimumCalls: 5,
		wantCalls: []string{paths["themes"], "/api/admin/themes/default/config"}, wantText: map[string]string{"activeTheme": "Fallback Fixture Theme"}})

	// Each result endpoint gets independent non-success, server-error, and malformed-success cases.
	// Products and orders are objects under their existing API contracts; posts/settings/themes are arrays.
	// This catches unchecked response status and unchecked .json() shape assumptions.
	for key, path := range paths {
		if key == "themes" {
			continue
		}
		for _, response := range []dashboardScriptResponse{{Status: 403, Body: map[string]any{"error": "forbidden"}}, {Status: 500, Body: map[string]any{"error": "failure"}}, {Status: 200, Body: map[string]any{"unexpected": true}}} {
			responses := map[string]dashboardScriptResponse{
				paths["postsCount"]: {Status: 200, Body: []any{}}, paths["productsCount"]: {Status: 200, Body: map[string]any{"items": []any{}, "total": 0}}, paths["ordersCount"]: {Status: 200, Body: map[string]any{"items": []any{}, "total": 0, "counts": map[string]any{"pending": 0}}},
				paths["settings"]:                  {Status: 200, Body: []any{map[string]any{"key": "active_theme", "value": "default"}}},
				"/api/admin/themes/default/config": {Status: 200, Body: map[string]any{"name": "Dashboard Fixture Theme"}},
			}
			responses[path] = response
			name := fmt.Sprintf("%s failure status %d", key, response.Status)
			result := dashboardRunRenderedScript(t, script, dashboardScriptScenario{name: name, responses: responses, minimumCalls: 1})
			if !dashboardFailureMessageVisible(result) {
				t.Errorf("%s did not show the existing localized load-failure message", name)
			}
			for _, value := range []string{result.Text["postsCount"], result.Text["productsCount"], result.Text["ordersCount"], result.Text["activeTheme"], result.RecentHTML} {
				if strings.Contains(value, "undefined") {
					t.Errorf("%s rendered undefined: %q", name, value)
				}
			}
			if key == "postsCount" && result.RecentLoading {
				t.Errorf("%s left recentPosts in loading state", name)
			}
			if key == "postsCount" && result.Text["postsCount"] == "0" {
				t.Errorf("%s represented unknown post count as zero", name)
			}
			if key == "productsCount" && result.Text["productsCount"] == "0" {
				t.Errorf("%s represented unknown product count as zero", name)
			}
			if key == "ordersCount" && result.Text["ordersCount"] == "0" {
				t.Errorf("%s represented unknown order count as zero", name)
			}
			if key == "settings" && result.Text["activeTheme"] == "Jelly" {
				t.Errorf("%s assumed a theme after failure", name)
			}
			if key == "settings" && strings.Contains(strings.Join(result.URLs, " "), paths["themes"]) {
				t.Errorf("%s fell back to another restricted themes API: %v", name, result.URLs)
			}
		}
	}
	// The themes API is reached only after settings succeeds with an empty array.
	for _, response := range []dashboardScriptResponse{{Status: 403, Body: map[string]any{"error": "forbidden"}}, {Status: 500, Body: map[string]any{"error": "failure"}}, {Status: 200, Body: map[string]any{"unexpected": true}}} {
		responses := map[string]dashboardScriptResponse{
			paths["postsCount"]: {Status: 200, Body: []any{}}, paths["productsCount"]: {Status: 200, Body: map[string]any{"items": []any{}, "total": 0}}, paths["ordersCount"]: {Status: 200, Body: map[string]any{"items": []any{}, "total": 0, "counts": map[string]any{"pending": 0}}},
			paths["settings"]: {Status: 200, Body: []any{}}, paths["themes"]: response,
		}
		name := fmt.Sprintf("themes failure status %d", response.Status)
		result := dashboardRunRenderedScript(t, script, dashboardScriptScenario{name: name, responses: responses, minimumCalls: 5})
		if !dashboardFailureMessageVisible(result) {
			t.Errorf("%s did not show the existing localized load-failure message", name)
		}
		if result.Text["activeTheme"] == "Jelly" || result.Text["activeTheme"] == "" {
			t.Errorf("%s showed an assumed or empty theme value", name)
		}
	}
	for _, invalidTotal := range []struct {
		name  string
		value any
	}{{"negative product total", -1}, {"fractional product total", 1.5}, {"unsafe product total", int64(9007199254740992)}} {
		dashboardAssertInvalidCounter(t, script, "productsCount", paths["productsCount"], invalidTotal.name,
			map[string]any{"items": []any{}, "total": invalidTotal.value})
	}
	dashboardAssertInvalidCounter(t, script, "productsCount", paths["productsCount"], "product response missing items", map[string]any{"total": 3})
	dashboardAssertInvalidCounter(t, script, "productsCount", paths["productsCount"], "product response items is not an array", map[string]any{"items": map[string]any{}, "total": 3})
	for _, invalidPending := range []struct {
		name   string
		counts any
	}{{"missing pending order count", map[string]any{}}, {"negative pending order count", map[string]any{"pending": -1}}, {"fractional pending order count", map[string]any{"pending": 1.5}}, {"unsafe pending order count", map[string]any{"pending": int64(9007199254740992)}}, {"missing order counts", nil}} {
		body := map[string]any{"items": []any{}, "total": 4}
		if invalidPending.counts != nil {
			body["counts"] = invalidPending.counts
		}
		dashboardAssertInvalidCounter(t, script, "ordersCount", paths["ordersCount"], invalidPending.name, body)
	}

}
