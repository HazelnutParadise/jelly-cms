package server

// Site extension tests (docs/tickets/29-plugin-read-api-and-extensions.md, PR B): theme.inject, content.render,
// comments.component and the admin extension settings API.
//
// Control case: TestPluginExtensionsSiteAdminSettingsAPI. It covers GET /api/admin/extensions and
// PUT /api/admin/extensions/:point, which the skeleton already implements (ext.go, plugin_detail_api.go), so it is
// expected to PASS on the empty skeleton. Every other test here is expected to FAIL on the skeleton, because the
// wanted-results cells in plugin_extensions_site.go and the notify cells are still stubs that return nothing.
//
// Each test builds its own site with newPluginEventsHarness (skipped unless JELLY_TEST_POSTGRES=1), activates the
// default theme with rebuildRoutes and installs fixture plugins that declare the extension point they serve. Public
// pages are read through the real HTTP routes. Plugin error logs are read from system_logs after
// drainPluginLogPipelines, which flushes plugin.FlushLogs and flushPluginLogForwarder.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/ext"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
)

// pluginExtLevelError is the system_logs level of a plugin error line (syslog LevelError).
const pluginExtLevelError = 3

// pluginExtUseTheme activates the default theme and rebuilds the routes on the harness runtime so public pages
// render while the fixture plugins stay live.
func pluginExtUseTheme(t *testing.T, h *pluginEventsHarness) {
	t.Helper()
	tm := theme.NewManager("web/themes")
	if err := tm.Activate("default"); err != nil {
		t.Fatalf("activate default theme: %v", err)
	}
	h.rebuildRoutes(t, tm)
}

// pluginExtInstall writes a fixture plugin that declares points, inserts its enabled plugins row with the granted
// permission hash, loads it into the harness runtime and bumps the plugin generation. The bump makes sure no
// provider list cached by an earlier test or request answers for this plugin.
func pluginExtInstall(t *testing.T, h *pluginEventsHarness, id string, points []string, script string) {
	t.Helper()
	meta := plugin.PluginMetadata{ID: id, Name: "Extension fixture " + id, Version: "1.0.0", Entrypoint: "index.js"}
	for _, point := range points {
		meta.Extensions = append(meta.Extensions, plugin.ExtensionSpec{Point: point})
	}
	dir := filepath.Join("data", "plugins", id)
	writeReloadPlugin(t, dir, meta, script)
	granted := plugin.PermissionNamesHash(plugin.ManifestPermissionNames(meta))
	if err := h.db.Exec(`INSERT INTO plugins (id, kind, version, source, enabled, signature, permissions_granted_hash) VALUES (?, 'plugin', '1.0.0', 'upload', TRUE, 'unsigned', ?) ON CONFLICT (id) DO UPDATE SET enabled = TRUE, permissions_granted_hash = EXCLUDED.permissions_granted_hash`, id, granted).Error; err != nil {
		t.Fatalf("insert plugins row for %s: %v", id, err)
	}
	if err := h.runtime.Load(context.Background(), dir); err != nil {
		t.Fatalf("load fixture plugin %s: %v", id, err)
	}
	bumpPluginExtensionGeneration()
}

// pluginExtJS returns v as a JavaScript literal (JSON is valid JavaScript for strings and numbers).
func pluginExtJS(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode JavaScript literal: %v", err)
	}
	return string(raw)
}

// pluginExtInjectHead and pluginExtInjectFooter are the markers an inject fixture contributes.
func pluginExtInjectHead(id string) string   { return `<meta name="` + id + `">` }
func pluginExtInjectFooter(id string) string { return "<!--" + id + "-footer-->" }

// pluginExtInjectScript registers a theme.inject callback that returns the given head and footer.
func pluginExtInjectScript(t *testing.T, head, footer string) string {
	t.Helper()
	return fmt.Sprintf(`JellyCMS.registerExtension("theme.inject", function (input) {
  return {head: %s, footer: %s};
});`, pluginExtJS(t, head), pluginExtJS(t, footer))
}

// pluginExtInstallInject installs an inject fixture whose markers are derived from its id.
func pluginExtInstallInject(t *testing.T, h *pluginEventsHarness, id string) {
	t.Helper()
	pluginExtInstall(t, h, id, []string{ext.PointThemeInject},
		pluginExtInjectScript(t, pluginExtInjectHead(id), pluginExtInjectFooter(id)))
}

// pluginExtRenderGuard checks the content.render input contract: the post summary fields and the html string.
const pluginExtRenderGuard = `function guard(input) {
  var p = input.post;
  if (!p || typeof p.id !== "number" || p.id <= 0) throw new Error("post.id missing");
  if (typeof p.slug !== "string" || p.slug === "") throw new Error("post.slug missing");
  if (typeof p.title !== "string" || p.title === "") throw new Error("post.title missing");
  if (typeof p.author_id !== "number" || p.author_id <= 0) throw new Error("post.author_id missing");
  if (typeof input.html !== "string") throw new Error("html missing");
}`

// pluginExtInstallRenderPair installs render-a (turns every <p> into <p class="x">) and render-b (checks it received
// render-a's output and appends a marker paragraph).
func pluginExtInstallRenderPair(t *testing.T, h *pluginEventsHarness) {
	t.Helper()
	renderA := pluginExtRenderGuard + `
JellyCMS.registerExtension("content.render", function (input) {
  guard(input);
  return {html: input.html.split("<p>").join('<p class="x">')};
});`
	renderB := pluginExtRenderGuard + `
JellyCMS.registerExtension("content.render", function (input) {
  guard(input);
  if (input.html.indexOf('class="x"') < 0) throw new Error("render-b did not receive render-a output");
  return {html: input.html + '<p data-render="b"></p>'};
});`
	pluginExtInstall(t, h, "render-a", []string{ext.PointContentRender}, renderA)
	pluginExtInstall(t, h, "render-b", []string{ext.PointContentRender}, renderB)
}

// pluginExtPage fetches a public page through the real routes; archivesPage requires 200.
func pluginExtPage(t *testing.T, h *pluginEventsHarness, path string) string {
	t.Helper()
	return archivesPage(t, h.accessHarness, path)
}

// pluginExtArticle returns the article element of a post page (empty when the page has none).
func pluginExtArticle(body string) string {
	start := strings.Index(body, `<article class="prose">`)
	if start < 0 {
		return ""
	}
	end := strings.Index(body[start:], "</article>")
	if end < 0 {
		return body[start:]
	}
	return body[start : start+end+len("</article>")]
}

// pluginExtClip shortens s for a failure message.
func pluginExtClip(s string) string {
	const max = 300
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

// pluginExtWantContains fails unless body contains want.
func pluginExtWantContains(t *testing.T, label, body, want string) {
	t.Helper()
	if !strings.Contains(body, want) {
		t.Errorf("%s: page does not contain %q (%d bytes searched)", label, want, len(body))
	}
}

// pluginExtWantAbsent fails when body contains unwanted. Callers pair it with a positive check on the same page.
func pluginExtWantAbsent(t *testing.T, label, body, unwanted string) {
	t.Helper()
	if strings.Contains(body, unwanted) {
		t.Errorf("%s: page must not contain %q", label, unwanted)
	}
}

// pluginExtWantBefore fails unless both texts occur in body and first occurs before second.
func pluginExtWantBefore(t *testing.T, label, body, first, second string) {
	t.Helper()
	i, j := strings.Index(body, first), strings.Index(body, second)
	if i < 0 || j < 0 {
		t.Errorf("%s: want %q before %q; positions %d and %d", label, first, second, i, j)
		return
	}
	if i > j {
		t.Errorf("%s: %q (at %d) must come before %q (at %d)", label, first, i, second, j)
	}
}

// pluginExtLogCount counts system_logs rows written for pluginID at level. Call drainPluginLogPipelines first.
func pluginExtLogCount(t *testing.T, h *pluginEventsHarness, pluginID string, level int) int64 {
	t.Helper()
	var n int64
	if err := h.db.Raw(`SELECT COUNT(*) FROM system_logs WHERE source = 'plugin' AND plugin_id = ? AND level = ?`, pluginID, level).Scan(&n).Error; err != nil {
		t.Fatalf("count plugin %s level %d logs: %v", pluginID, level, err)
	}
	return n
}

// pluginExtPutExtension sends PUT /api/admin/extensions/:point and requires 2xx.
func pluginExtPutExtension(t *testing.T, h *pluginEventsHarness, token, point, body string) accessResult {
	t.Helper()
	r := updCall(h.accessHarness, http.MethodPut, "/api/admin/extensions/"+point, body, token)
	if r.code < 200 || r.code > 299 {
		t.Fatalf("PUT extensions/%s %s = %d: %s", point, body, r.code, r.body)
	}
	return r
}

// pluginExtWantInjected checks that each inject fixture's head marker is in <head> and its footer marker in <body>.
func pluginExtWantInjected(t *testing.T, label, body string, ids ...string) {
	t.Helper()
	for _, id := range ids {
		head, footer := pluginExtInjectHead(id), pluginExtInjectFooter(id)
		pluginExtWantContains(t, label+" head of "+id, body, head)
		pluginExtWantContains(t, label+" footer of "+id, body, footer)
		pluginExtWantBefore(t, label+" head of "+id+" before </head>", body, head, "</head>")
		pluginExtWantBefore(t, label+" footer of "+id+" before </body>", body, footer, "</body>")
	}
}

// TestPluginExtensionsSiteThemeInjectOrderAndReorder covers the default placement of two theme.inject providers on the
// home page and a post page, and the order change after the active list is stored.
func TestPluginExtensionsSiteThemeInjectOrderAndReorder(t *testing.T) {
	h := newPluginEventsHarness(t)
	pluginExtUseTheme(t, h)
	pluginExtInstallInject(t, h, "inject-a")
	pluginExtInstallInject(t, h, "inject-b")
	author := h.user("author", "inject-author")
	pubThemeSeedPost(t, h.accessHarness, author, pubThemePost{title: "Inject Probe", slug: "inject-probe"})
	token := h.token(h.user("admin", "inject-admin"))

	for _, path := range []string{"/", "/post/inject-probe"} {
		body := pluginExtPage(t, h, path)
		pluginExtWantInjected(t, "GET "+path, body, "inject-a", "inject-b")
		pluginExtWantBefore(t, "GET "+path+" head order", body, pluginExtInjectHead("inject-a"), pluginExtInjectHead("inject-b"))
		pluginExtWantBefore(t, "GET "+path+" footer order", body, pluginExtInjectFooter("inject-a"), pluginExtInjectFooter("inject-b"))
	}

	pluginExtPutExtension(t, h, token, ext.PointThemeInject, `{"active":["inject-b","inject-a"]}`)
	for _, path := range []string{"/", "/post/inject-probe"} {
		body := pluginExtPage(t, h, path)
		pluginExtWantInjected(t, "after reorder GET "+path, body, "inject-a", "inject-b")
		pluginExtWantBefore(t, "after reorder GET "+path+" head order", body, pluginExtInjectHead("inject-b"), pluginExtInjectHead("inject-a"))
		pluginExtWantBefore(t, "after reorder GET "+path+" footer order", body, pluginExtInjectFooter("inject-b"), pluginExtInjectFooter("inject-a"))
	}
}

// TestPluginExtensionsSiteThemeInjectCommerceRule checks that the commerce rule of applyPublicSEO applies to
// theme.inject: /checkout carries the markers only when inject_on_commerce is true. The public home page is the
// positive control for the same providers. /checkout is reached through a real cart (shop harness).
func TestPluginExtensionsSiteThemeInjectCommerceRule(t *testing.T) {
	h := pluginEventsShopHarness(t)
	pluginExtUseTheme(t, h)
	pluginExtInstallInject(t, h, "inject-a")
	pluginExtInstallInject(t, h, "inject-b")

	product := shopSeedProduct(t, h.accessHarness, "inject-tea", 1200, 5)
	variant := shopVariantRows(t, h.accessHarness, product.ID)[0]
	b := newShopBrowser(h.accessHarness)
	shopWantOK(t, b.send(http.MethodPost, "/api/cart/items", map[string]any{"variant_id": variant.ID, "qty": 1}), "POST /api/cart/items")

	home := pluginExtPage(t, h, "/")
	pluginExtWantInjected(t, "GET / (control)", home, "inject-a", "inject-b")

	checkout := b.get("/checkout")
	shopWantCode(t, checkout, "GET /checkout (default)", http.StatusOK)
	pluginExtWantAbsent(t, "GET /checkout default", checkout.body, pluginExtInjectHead("inject-a"))
	pluginExtWantAbsent(t, "GET /checkout default", checkout.body, pluginExtInjectHead("inject-b"))
	pluginExtWantAbsent(t, "GET /checkout default", checkout.body, pluginExtInjectFooter("inject-a"))
	pluginExtWantAbsent(t, "GET /checkout default", checkout.body, pluginExtInjectFooter("inject-b"))

	pubThemeSetOption(t, h.accessHarness, "inject_on_commerce", "true")
	checkout = b.get("/checkout")
	shopWantCode(t, checkout, "GET /checkout (inject_on_commerce=true)", http.StatusOK)
	pluginExtWantInjected(t, "GET /checkout inject_on_commerce=true", checkout.body, "inject-a", "inject-b")
}

// TestPluginExtensionsSiteThemeInjectDisableDropsOnlyThatPlugin disables inject-b and checks that only its markers
// leave the page. The inject-a markers stay (positive control for the negative checks).
func TestPluginExtensionsSiteThemeInjectDisableDropsOnlyThatPlugin(t *testing.T) {
	h := newPluginEventsHarness(t)
	pluginExtUseTheme(t, h)
	pluginExtInstallInject(t, h, "inject-a")
	pluginExtInstallInject(t, h, "inject-b")
	token := h.token(h.user("admin", "inject-disable-admin"))
	author := h.user("author", "inject-disable-author")
	pubThemeSeedPost(t, h.accessHarness, author, pubThemePost{title: "Inject Disable Probe", slug: "inject-disable-probe"})

	before := pluginExtPage(t, h, "/")
	pluginExtWantInjected(t, "GET / before disable", before, "inject-a", "inject-b")

	r := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/inject-b/disable", "", token)
	if r.code < 200 || r.code > 299 {
		t.Fatalf("disable inject-b = %d: %s", r.code, r.body)
	}
	after := pluginExtPage(t, h, "/")
	pluginExtWantInjected(t, "GET / after disable of inject-b", after, "inject-a")
	pluginExtWantAbsent(t, "GET / after disable of inject-b", after, pluginExtInjectHead("inject-b"))
	pluginExtWantAbsent(t, "GET / after disable of inject-b", after, pluginExtInjectFooter("inject-b"))

	postPage := pluginExtPage(t, h, "/post/inject-disable-probe")
	pluginExtWantInjected(t, "GET /post/inject-disable-probe after disable of inject-b", postPage, "inject-a")
	pluginExtWantAbsent(t, "GET /post/inject-disable-probe after disable of inject-b", postPage, pluginExtInjectHead("inject-b"))
	pluginExtWantAbsent(t, "GET /post/inject-disable-probe after disable of inject-b", postPage, pluginExtInjectFooter("inject-b"))
}

// TestPluginExtensionsSiteThemeInjectOversizedHeadIsSkippedAndLogged installs a provider whose head is 70,000
// characters (above the 64 KiB limit). Its footer is normal. The page must keep the footer, drop the head and log a
// plugin error for the provider.
func TestPluginExtensionsSiteThemeInjectOversizedHeadIsSkippedAndLogged(t *testing.T) {
	h := newPluginEventsHarness(t)
	pluginExtUseTheme(t, h)
	pluginExtInstallInject(t, h, "inject-a")

	const bigID = "inject-big"
	bigHead := `<meta name="inject-big">` + strings.Repeat("x", 70000)
	pluginExtInstall(t, h, bigID, []string{ext.PointThemeInject},
		pluginExtInjectScript(t, bigHead, pluginExtInjectFooter(bigID)))

	body := pluginExtPage(t, h, "/")
	pluginExtWantContains(t, "GET / oversized head provider", body, pluginExtInjectHead("inject-a"))
	pluginExtWantContains(t, "GET / oversized head provider", body, pluginExtInjectFooter(bigID))
	pluginExtWantAbsent(t, "GET / oversized head provider", body, `<meta name="inject-big">`)

	drainPluginLogPipelines(t)
	if n := pluginExtLogCount(t, h, bigID, pluginExtLevelError); n < 1 {
		t.Errorf("system_logs rows with plugin_id=%q level=%d = %d, want at least 1", bigID, pluginExtLevelError, n)
	}
}

// TestPluginExtensionsSiteContentRenderChainsProviders checks that render-a's output reaches render-b and that both
// changes are on the post page. The callbacks check the post summary and the html input.
func TestPluginExtensionsSiteContentRenderChainsProviders(t *testing.T) {
	h := newPluginEventsHarness(t)
	pluginExtUseTheme(t, h)
	pluginExtInstallRenderPair(t, h)
	author := h.user("author", "render-author")
	pubThemeSeedPost(t, h.accessHarness, author, pubThemePost{title: "Render Probe", slug: "render-probe", content: "<p>hello render</p>"})

	body := pluginExtPage(t, h, "/post/render-probe")
	pluginExtWantContains(t, "GET /post/render-probe", body, `<p class="x">hello render</p>`)
	pluginExtWantContains(t, "GET /post/render-probe", body, `data-render="b"`)
	pluginExtWantBefore(t, "GET /post/render-probe chain order", body, `<p class="x">hello render</p>`, `data-render="b"`)
}

// TestPluginExtensionsSiteContentRenderCachesUntilContentChanges checks the render cache. The second request for the
// same post makes no provider call and returns the same article. Changing the post content makes the next request
// call the providers again (positive control).
func TestPluginExtensionsSiteContentRenderCachesUntilContentChanges(t *testing.T) {
	h := newPluginEventsHarness(t)
	pluginExtUseTheme(t, h)
	pluginExtInstallRenderPair(t, h)
	author := h.user("author", "render-cache-author")
	post := pubThemeSeedPost(t, h.accessHarness, author, pubThemePost{title: "Render Cache", slug: "render-cache", content: "<p>hello render</p>"})
	const path = "/post/render-cache"

	beforeFirst := h.runtime.ExtensionCalls()
	first := pluginExtPage(t, h, path)
	afterFirst := h.runtime.ExtensionCalls()
	if afterFirst <= beforeFirst {
		t.Errorf("first GET %s made %d provider calls (counter %d -> %d), want the content.render providers to run", path, afterFirst-beforeFirst, beforeFirst, afterFirst)
	}
	firstArticle := pluginExtArticle(first)
	pluginExtWantContains(t, "first GET "+path, firstArticle, `<p class="x">hello render</p>`)

	second := pluginExtPage(t, h, path)
	afterSecond := h.runtime.ExtensionCalls()
	if afterSecond != afterFirst {
		t.Errorf("second GET %s changed the provider call counter %d -> %d, want a cache hit with no plugin call", path, afterFirst, afterSecond)
	}
	if secondArticle := pluginExtArticle(second); secondArticle != firstArticle {
		t.Errorf("second GET %s article differs from the first:\nfirst:  %s\nsecond: %s", path, pluginExtClip(firstArticle), pluginExtClip(secondArticle))
	}

	if err := h.db.Exec(`UPDATE posts SET content = ? WHERE id = ?`, "<p>hello changed</p>", post.ID).Error; err != nil {
		t.Fatalf("update post content: %v", err)
	}
	third := pluginExtPage(t, h, path)
	afterThird := h.runtime.ExtensionCalls()
	if afterThird <= afterSecond {
		t.Errorf("GET %s after a content change made no provider call (counter %d -> %d), want a new render", path, afterSecond, afterThird)
	}
	pluginExtWantContains(t, "GET "+path+" after content change", pluginExtArticle(third), `<p class="x">hello changed</p>`)
}

// TestPluginExtensionsSiteContentRenderFailureKeepsOriginal installs a provider that always throws. The post page
// must still be 200 and show the original HTML, and the failure must be logged for the plugin.
func TestPluginExtensionsSiteContentRenderFailureKeepsOriginal(t *testing.T) {
	h := newPluginEventsHarness(t)
	pluginExtUseTheme(t, h)
	pluginExtInstall(t, h, "render-throw", []string{ext.PointContentRender},
		`JellyCMS.registerExtension("content.render", function (input) { throw new Error("render-throw always fails"); });`)
	author := h.user("author", "render-throw-author")
	pubThemeSeedPost(t, h.accessHarness, author, pubThemePost{title: "Render Throw", slug: "render-throw-probe", content: "<p>hello render</p>"})

	body := pluginExtPage(t, h, "/post/render-throw-probe")
	pluginExtWantContains(t, "GET /post/render-throw-probe", body, `<p>hello render</p>`)
	pluginExtWantAbsent(t, "GET /post/render-throw-probe", body, `<p class="x">hello render</p>`)

	drainPluginLogPipelines(t)
	if n := pluginExtLogCount(t, h, "render-throw", pluginExtLevelError); n < 1 {
		t.Errorf("system_logs rows with plugin_id=%q level=%d = %d, want at least 1", "render-throw", pluginExtLevelError, n)
	}
}

// TestPluginExtensionsSiteCommentsComponentReplacesBuiltin checks that a comments.component provider replaces the
// theme's comments block (control: the builtin block shows when the provider is disabled).
func TestPluginExtensionsSiteCommentsComponentReplacesBuiltin(t *testing.T) {
	h := newPluginEventsHarness(t)
	pluginExtUseTheme(t, h)
	author := h.user("author", "comments-ext-author")
	post := commentsPublishedPost(t, h.accessHarness, author, "ext-comments-post", nil)
	commentsSeed(t, h.accessHarness, commentsRow(post.ID, "ZebraVisitor", "approved", 1))
	pluginExtInstall(t, h, "comments-ext", []string{ext.PointCommentsComponent},
		`JellyCMS.registerExtension("comments.component", function (input) {
  return {html: "<div id=\"ext-comments\">ext " + input.post.slug + "</div>"};
});`)
	token := h.token(h.user("admin", "comments-ext-admin"))
	const path = "/post/ext-comments-post"
	// comments.component is a single-provider point: the plugin serves only
	// once it is the first entry of the active list (no list means builtin).
	pluginExtPutExtension(t, h, token, ext.PointCommentsComponent, `{"active":["comments-ext"]}`)

	body := pluginExtPage(t, h, path)
	pluginExtWantContains(t, "GET "+path, body, `<section class="comments" id="comments">`)
	pluginExtWantContains(t, "GET "+path, body, `<div id="ext-comments">ext ext-comments-post</div>`)
	pluginExtWantBefore(t, "GET "+path+" section wraps provider", body, `<section class="comments" id="comments">`, `<div id="ext-comments">`)
	// The page has earlier sections (post-author), so look for the closing tag after the provider fragment.
	if i := strings.Index(body, `<div id="ext-comments">`); i >= 0 && !strings.Contains(body[i:], `</section>`) {
		t.Errorf("GET %s provider inside section: no </section> after the provider fragment", path)
	}
	pluginExtWantContains(t, "GET "+path+" reply entry", body, `href="#comments"`)
	pluginExtWantAbsent(t, "GET "+path+" with provider", body, "comments-head")
	pluginExtWantAbsent(t, "GET "+path+" with provider", body, "ZebraVisitor body")

	r := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/comments-ext/disable", "", token)
	if r.code < 200 || r.code > 299 {
		t.Fatalf("disable comments-ext = %d: %s", r.code, r.body)
	}
	body = pluginExtPage(t, h, path)
	pluginExtWantContains(t, "GET "+path+" after disable", body, "comments-head")
	pluginExtWantContains(t, "GET "+path+" after disable", body, "ZebraVisitor body")
	pluginExtWantAbsent(t, "GET "+path+" after disable", body, `<div id="ext-comments">`)
}

// pluginExtAdminView is one entry of GET /api/admin/extensions.
type pluginExtAdminView map[string]any

// pluginExtAdminList reads GET /api/admin/extensions into a map keyed by point.
func pluginExtAdminList(t *testing.T, h *pluginEventsHarness, token string) map[string]pluginExtAdminView {
	t.Helper()
	r := updCall(h.accessHarness, http.MethodGet, "/api/admin/extensions", "", token)
	if r.code != http.StatusOK {
		t.Fatalf("GET /api/admin/extensions = %d: %s", r.code, r.body)
	}
	var list []pluginExtAdminView
	if err := json.Unmarshal([]byte(r.body), &list); err != nil {
		t.Fatalf("decode extension list: %v; body=%q", err, r.body)
	}
	views := make(map[string]pluginExtAdminView, len(list))
	for _, view := range list {
		point, _ := view["point"].(string)
		views[point] = view
	}
	return views
}

// pluginExtProviders returns the providers field of a view as strings.
func pluginExtProviders(t *testing.T, point string, view pluginExtAdminView) []string {
	t.Helper()
	raw, ok := view["providers"].([]any)
	if !ok {
		t.Fatalf("%s: providers = %v, want an array", point, view["providers"])
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, _ := item.(string)
		out = append(out, s)
	}
	return out
}

// pluginExtWantProvider fails unless providers contains want (want true) or does not contain it (want false).
func pluginExtWantProvider(t *testing.T, point string, providers []string, id string, want bool) {
	t.Helper()
	got := false
	for _, p := range providers {
		if p == id {
			got = true
		}
	}
	if got != want {
		t.Errorf("%s providers = %v; contains %q = %v, want %v", point, providers, id, got, want)
	}
}

// TestPluginExtensionsSiteAdminSettingsAPI is the control case. It checks the admin extension settings API for the
// new points: the multiple flag, builtin providers and the resolved value with no active list, and a PUT that makes a
// comments.component plugin the resolved provider. It is expected to pass on the empty skeleton.
func TestPluginExtensionsSiteAdminSettingsAPI(t *testing.T) {
	h := newPluginEventsHarness(t)
	token := h.token(h.user("admin", "ext-settings-admin"))

	views := pluginExtAdminList(t, h, token)
	for _, point := range ext.Points() {
		view, ok := views[point]
		if !ok {
			t.Errorf("GET /api/admin/extensions has no entry for %s", point)
			continue
		}
		if _, ok := view["multiple"].(bool); !ok {
			t.Errorf("%s: multiple = %v, want a boolean", point, view["multiple"])
		}
	}

	wantMultiple := map[string]bool{
		ext.PointMediaStorage:      true,
		ext.PointThemeInject:       true,
		ext.PointContentRender:     true,
		ext.PointNotifyChannel:     true,
		ext.PointSearchEngine:      false,
		ext.PointCommentsComponent: false,
	}
	for point, want := range wantMultiple {
		if view, ok := views[point]; ok && view["multiple"] != want {
			t.Errorf("%s: multiple = %v, want %v", point, view["multiple"], want)
		}
	}

	for _, point := range []string{ext.PointThemeInject, ext.PointContentRender, ext.PointNotifyChannel} {
		view, ok := views[point]
		if !ok {
			continue
		}
		pluginExtWantProvider(t, point, pluginExtProviders(t, point, view), "builtin", false)
		if view["resolved"] != nil {
			t.Errorf("%s: resolved = %v with no active providers, want null", point, view["resolved"])
		}
	}

	for _, point := range []string{ext.PointCommentsComponent, ext.PointSearchEngine} {
		view, ok := views[point]
		if !ok {
			continue
		}
		pluginExtWantProvider(t, point, pluginExtProviders(t, point, view), "builtin", true)
		if view["resolved"] != "builtin" {
			t.Errorf("%s: resolved = %v with no active providers, want \"builtin\"", point, view["resolved"])
		}
	}

	pluginExtInstall(t, h, "comments-ext", []string{ext.PointCommentsComponent},
		`JellyCMS.registerExtension("comments.component", function (input) { return {html: "<div id=\"ext-comments\"></div>"}; });`)
	r := pluginExtPutExtension(t, h, token, ext.PointCommentsComponent, `{"active":["comments-ext"]}`)
	var put pluginExtAdminView
	if err := json.Unmarshal([]byte(r.body), &put); err != nil {
		t.Fatalf("decode PUT comments.component response: %v; body=%q", err, r.body)
	}
	if put["resolved"] != "comments-ext" {
		t.Errorf("PUT comments.component resolved = %v, want \"comments-ext\"; body=%s", put["resolved"], r.body)
	}
	after := pluginExtAdminList(t, h, token)
	if view, ok := after[ext.PointCommentsComponent]; ok && view["resolved"] != "comments-ext" {
		t.Errorf("GET comments.component after PUT resolved = %v, want \"comments-ext\"", view["resolved"])
	}
}
