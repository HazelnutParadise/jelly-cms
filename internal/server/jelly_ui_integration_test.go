package server

// Jelly UI adoption acceptance tests for ticket 12 slots Z3 to Z6 (docs/tickets/12-zero-cdn-jelly-ui.md):
// the admin layout loads the vendored Jelly UI module, the settings booleans and the posts status filter
// render as Jelly UI elements, and no other page loads Jelly UI. They run through the real route table
// against the dedicated PostgreSQL database and are skipped unless JELLY_TEST_POSTGRES=1. Most of them are
// expected to fail now: no template loads Jelly UI yet (TestSettingsPayloadsStayTheSame pins behavior that
// must not change and passes now). Do not edit them to match an implementation.

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
	nethtml "golang.org/x/net/html"
)

const jellyUIModulePath = "/admin/assets/jelly-ui/jelly.js"

var jellyUIHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// jellyUIAdminPages are the admin pages a signed-in administrator can open; every one shares layout.html.
var jellyUIAdminPages = []string{
	"/admin", "/admin/posts", "/admin/posts/new", "/admin/pages", "/admin/pages/new", "/admin/media",
	"/admin/products", "/admin/orders", "/admin/themes", "/admin/plugins", "/admin/settings/site",
	"/admin/taxonomy", "/admin/menus", "/admin/comments", "/admin/trash",
}

func jellyUIAdminGet(t *testing.T, h *accessHarness, token, path string) accessResult {
	t.Helper()
	r := h.request(http.MethodGet, path, "", token, echo.MIMETextHTML, "")
	h.mustNoPanic(r, "GET "+path)
	return r
}

// jellyUITags returns, for every start tag called name, its attributes plus its first text child.
func jellyUITags(body, name string) []map[string]string {
	var out []map[string]string
	z := nethtml.NewTokenizer(strings.NewReader(body))
	for tt := z.Next(); tt != nethtml.ErrorToken; tt = z.Next() {
		if tt != nethtml.StartTagToken && tt != nethtml.SelfClosingTagToken {
			continue
		}
		tag, hasAttr := z.TagName()
		if string(tag) != name {
			continue
		}
		attrs := map[string]string{}
		for hasAttr {
			var k, v []byte
			k, v, hasAttr = z.TagAttr()
			attrs[string(k)] = string(v)
		}
		if tt == nethtml.StartTagToken {
			if z.Next() == nethtml.TextToken {
				attrs["#text"] = strings.TrimSpace(string(z.Text()))
			}
		}
		out = append(out, attrs)
	}
	return out
}

func jellyUIModuleReferenceCounts(body string) (pathCount, validHashCount int) {
	for _, attrs := range jellyUITags(body, "script") {
		u, err := url.Parse(attrs["src"])
		if err != nil || u.Path != jellyUIModulePath {
			continue
		}
		pathCount++
		values := u.Query()["v"]
		if attrs["type"] == "module" && len(values) == 1 && jellyUIHashPattern.MatchString(values[0]) {
			validHashCount++
		}
	}
	return pathCount, validHashCount
}

func TestAdminPagesLoadTheJellyUIModuleOnce(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "jelly-ui-admin")
	token := h.token(admin)

	root := jellyUIAdminGet(t, h, token, "/admin/settings")
	if root.code != http.StatusFound || root.header.Get(echo.HeaderLocation) != "/admin/settings/site" {
		t.Errorf("GET /admin/settings = %d Location %q, want 302 to /admin/settings/site", root.code, root.header.Get(echo.HeaderLocation))
	}

	for _, path := range jellyUIAdminPages {
		r := jellyUIAdminGet(t, h, token, path)
		if r.code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", path, r.code)
			continue
		}
		paths, valid := jellyUIModuleReferenceCounts(r.body)
		if paths != 1 || valid != 1 {
			t.Errorf("GET %s contains %d Jelly UI module references, %d with a full SHA-256, want 1 of each", path, paths, valid)
		}
		if !strings.Contains(r.body, `<jelly-toaster position="bottom"`) {
			t.Errorf("GET %s has no <jelly-toaster position=\"bottom\"> host", path)
		}
	}

	notFound := jellyUIAdminGet(t, h, token, "/admin/does-not-exist")
	paths, valid := jellyUIModuleReferenceCounts(notFound.body)
	if notFound.code != http.StatusNotFound || paths != 1 || valid != 1 {
		t.Errorf("the admin 404 page (status %d) must be rendered in the admin layout with the Jelly UI module", notFound.code)
	}
}

func TestVendoredJellyUIIsServedAsJavaScript(t *testing.T) {
	h := archivesHarness(t)
	r := archivesGet(t, h, "/admin/assets/jelly-ui/jelly.js")
	archivesWantStatus(t, "/admin/assets/jelly-ui/jelly.js", r, http.StatusOK)
	if ct := r.header.Get(echo.HeaderContentType); !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type = %q, want a JavaScript type (module scripts are blocked otherwise)", ct)
	}
	if !strings.Contains(r.body, "jelly-switch") {
		t.Error("the served file does not define jelly-switch")
	}
	for _, name := range []string{"LICENSE", "VERSION"} {
		path := "/admin/assets/jelly-ui/" + name
		archivesWantStatus(t, path, archivesGet(t, h, path), http.StatusOK)
	}
}

func TestLoginInstallAccountAndPublicPagesDoNotLoadJellyUI(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "pub-author")
	post := archivesSeedPost(t, h, author, archivesPost{title: "Plain Public Post"})
	admin := h.user("admin", "acct-admin")

	pages := []struct{ path, token string }{
		{"/login", ""}, {"/install", ""}, {"/", ""}, {"/posts", ""}, {"/post/" + post.Slug, ""},
		{"/search?q=jelly", ""}, {"/no-such-page-for-jelly-ui", ""}, {"/account", h.token(admin)},
	}
	for _, p := range pages {
		r := h.request(http.MethodGet, p.path, "", p.token, echo.MIMETextHTML, "")
		h.mustNoPanic(r, "GET "+p.path)
		for _, bad := range []string{"jelly-ui", "jelly.js", "<jelly-"} {
			if strings.Contains(r.body, bad) {
				t.Errorf("GET %s (status %d) contains %q; only admin pages load Jelly UI", p.path, r.code, bad)
			}
		}
	}
}

func TestSettingsPageRendersJellySwitchesWithTheOriginalNames(t *testing.T) {
	h := archivesHarness(t)
	token := h.token(h.user("admin", "jelly-ui-settings"))
	// The system settings page redirects its root URL to /site. The legacy OAuth
	// and payment settings still render from these explicit tabs, in the selected locale.
	r := jellyUIAdminGet(t, h, token, "/admin/settings/payment?lang=zh-TW")
	archivesWantStatus(t, "/admin/settings/payment?lang=zh-TW", r, http.StatusOK)
	if !strings.Contains(r.body, `<html lang="zh-TW"`) {
		t.Error("the payment settings response ignored the explicit zh-TW locale")
	}
	want := map[string]string{
		"googleEnabled": "google_enabled", "githubEnabled": "github_enabled",
		"ecpayEnabled": "ecpay_enabled", "newebpayEnabled": "newebpay_enabled",
	}
	got := map[string]map[string]string{}
	for _, attrs := range jellyUITags(r.body, "jelly-switch") {
		got[attrs["id"]] = attrs
	}
	if len(got) < len(want) {
		t.Errorf("the settings page renders %d <jelly-switch>, want at least %d (the two OAuth and the two payment enable settings)", len(got), len(want))
	}
	for id, name := range want {
		attrs, ok := got[id]
		if !ok {
			t.Errorf("no <jelly-switch id=%q>", id)
			continue
		}
		if attrs["name"] != name {
			t.Errorf("<jelly-switch id=%q> name = %q, want %q", id, attrs["name"], name)
		}
		if strings.TrimSpace(attrs["label"]) == "" {
			t.Errorf("<jelly-switch id=%q> has no rendered label", id)
		}
		if _, checked := attrs["checked"]; checked {
			t.Errorf("<jelly-switch id=%q> is server-rendered as checked; the page script sets it after loading the saved value", id)
		}
	}
	for _, id := range []string{"transferOn", "sandboxOn"} {
		switches := jellyUITags(r.body, "jelly-switch")
		found := false
		for _, attrs := range switches {
			if attrs["id"] == id {
				found = true
			}
		}
		if !found {
			t.Errorf("the settings page has no <jelly-switch id=%q> for a main payment boolean", id)
		}
	}
	for _, id := range []string{"googleEnabled", "githubEnabled", "ecpayEnabled", "newebpayEnabled", "transferOn", "sandboxOn"} {
		for _, attrs := range jellyUITags(r.body, "input") {
			if attrs["id"] == id {
				t.Errorf("the settings response renders main boolean %q as a native input", id)
			}
		}
	}

	// The two test-mode fields follow the design board: an 環境 segmented control, 測試 or 正式.
	envs := map[string]string{"ecpayTestMode": "ecpay_test_mode", "newebpayTestMode": "newebpay_test_mode"}
	controls := map[string]map[string]string{}
	for _, attrs := range jellyUITags(r.body, "jelly-segmented") {
		controls[attrs["id"]] = attrs
	}
	for id, name := range envs {
		attrs, ok := controls[id]
		if !ok {
			t.Errorf("no <jelly-segmented id=%q> (the 環境 control)", id)
			continue
		}
		if attrs["name"] != name || attrs["label"] != "環境" || attrs["value"] != "test" {
			t.Errorf("<jelly-segmented id=%q> name=%q label=%q value=%q, want name %q, label \"環境\", value \"test\"", id, attrs["name"], attrs["label"], attrs["value"], name)
		}
	}
	var segments []string
	for _, seg := range jellyUITags(r.body, "jelly-segment") {
		segments = append(segments, seg["value"]+"="+seg["#text"])
	}
	if got, want := strings.Join(segments, ","), "test=測試,live=正式,test=測試,live=正式"; got != want {
		t.Errorf("settings page <jelly-segment> = %q, want %q (no English hard-coded labels)", got, want)
	}
	if strings.Contains(r.body, "Test Mode") {
		t.Error("the settings page still shows the English \"Test Mode\" label")
	}
}

func TestPostsPageRendersTheStatusSegmentedControl(t *testing.T) {
	h := archivesHarness(t)
	token := h.token(h.user("admin", "jelly-ui-posts"))
	r := jellyUIAdminGet(t, h, token, "/admin/posts")
	archivesWantStatus(t, "/admin/posts", r, http.StatusOK)

	controls := jellyUITags(r.body, "jelly-segmented")
	if len(controls) != 1 {
		t.Fatalf("the posts page renders %d <jelly-segmented>, want 1", len(controls))
	}
	if controls[0]["name"] != "status" || strings.TrimSpace(controls[0]["label"]) == "" {
		t.Errorf("<jelly-segmented> name = %q label = %q, want name \"status\" and a rendered label", controls[0]["name"], controls[0]["label"])
	}
	if controls[0]["value"] != "all" {
		t.Errorf("<jelly-segmented> value = %q, want \"all\"", controls[0]["value"])
	}
	var values []string
	for _, seg := range jellyUITags(r.body, "jelly-segment") {
		values = append(values, seg["value"])
		if seg["#text"] == "" {
			t.Errorf("<jelly-segment value=%q> has no rendered text", seg["value"])
		}
	}
	if strings.Join(values, ",") != "all,published,draft,pending" {
		t.Errorf("<jelly-segment> values = %v, want all,published,draft,pending", values)
	}
}

// The page script posts these exact payloads; Jelly UI only changes how the booleans are collected, never
// what the server stores. This test pins the stored result and passes before and after the change.
func TestSettingsPayloadsStayTheSame(t *testing.T) {
	h := archivesHarness(t)
	token := h.token(h.user("admin", "jelly-ui-payloads"))

	post := func(path, body string) {
		t.Helper()
		r := h.request(http.MethodPost, path, body, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
		h.mustNoPanic(r, "POST "+path)
		if r.code != http.StatusOK {
			t.Fatalf("POST %s = %d, want 200; body=%q", path, r.code, r.body)
		}
	}
	// Read the stored options directly: GET /api/admin/payment/gateways reuses one Option value across
	// its queries and returns an empty config, a defect outside this ticket.
	option := func(key string) string {
		t.Helper()
		var o core.Option
		if err := h.db.Where("key = ?", key).First(&o).Error; err != nil {
			t.Fatalf("read option %s: %v", key, err)
		}
		return o.Value
	}

	post("/api/admin/payment/gateways/ecpay", `{"enabled":true,"merchant_id":"2000132","hash_key":"k","hash_iv":"v","test_mode":true}`)
	post("/api/admin/payment/gateways/newebpay", `{"enabled":true,"merchant_id":"MS1","hash_key":"k","hash_iv":"v","test_mode":false}`)
	for key, want := range map[string]string{
		"payment_ecpay_enabled": "true", "payment_ecpay_test_mode": "true", "payment_ecpay_merchant_id": "2000132",
		"payment_newebpay_enabled": "true", "payment_newebpay_test_mode": "false",
	} {
		if got := option(key); got != want {
			t.Errorf("option %s = %q, want %q", key, got, want)
		}
	}

	post("/api/admin/payment/gateways/ecpay", `{"enabled":true,"merchant_id":"2000132","hash_key":"k","hash_iv":"v","test_mode":false}`)
	if got := option("payment_ecpay_test_mode"); got != "false" {
		t.Errorf("option payment_ecpay_test_mode after turning test_mode off = %q, want \"false\"", got)
	}

	post("/api/admin/settings", `{"site_title":"Jelly UI Title","site_description":"d","site_language":"en"}`)
	r := h.request(http.MethodGet, "/api/admin/settings", "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(r, "GET settings")
	var options []struct{ Key, Value string }
	if err := json.Unmarshal([]byte(r.body), &options); err != nil {
		t.Fatalf("settings body is not JSON: %v; body=%q", err, r.body)
	}
	stored := map[string]string{}
	for _, o := range options {
		stored[o.Key] = o.Value
	}
	if stored["site_title"] != "Jelly UI Title" || stored["site_language"] != "en" {
		t.Errorf("stored settings = %v, want site_title and site_language as posted", stored)
	}
}
