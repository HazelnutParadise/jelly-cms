package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

// settingsAdminOnlyTabs are registered with the default role (admin only).
var settingsAdminOnlyTabs = []string{"site", "routes", "crawlers", "auth", "mail", "infra", "update"}

func TestSettingsTabsRegistryOrderAndRoles(t *testing.T) {
	admin := &core.User{Username: "tabs-admin", Role: "admin"}
	tabs := settingsTabsFor(admin)

	want := []string{"site", "routes", "crawlers", "auth", "comments", "mail", "infra", "update"}
	position := map[string]int{}
	for i, tab := range tabs {
		position[tab.Key] = i
	}
	last := -1
	for _, key := range want {
		i, ok := position[key]
		if !ok {
			t.Errorf("settingsTabsFor(admin) has no %q tab; tabs=%v", key, settingsTabKeys(tabs))
			continue
		}
		if i <= last {
			t.Errorf("settingsTabsFor(admin) lists %q out of Order; tabs=%v", key, settingsTabKeys(tabs))
		}
		last = i
	}
	for i := 1; i < len(tabs); i++ {
		if tabs[i].Order < tabs[i-1].Order {
			t.Errorf("settingsTabsFor returned tabs out of Order at %q (%d) after %q (%d)", tabs[i].Key, tabs[i].Order, tabs[i-1].Key, tabs[i-1].Order)
		}
	}

	editor := &core.User{Username: "tabs-editor", Role: "editor"}
	for _, tab := range settingsTabsFor(editor) {
		if !settingsContainsString(tab.Roles, "editor") {
			t.Errorf("editor was shown tab %q whose Roles %v do not include editor", tab.Key, tab.Roles)
		}
	}
	editorKeys := map[string]bool{}
	for _, tab := range settingsTabsFor(editor) {
		editorKeys[tab.Key] = true
	}
	for _, key := range settingsAdminOnlyTabs {
		if editorKeys[key] {
			t.Errorf("editor can see admin-only settings tab %q", key)
		}
	}
}

func TestSettingsPageRedirectsAndRendersTab(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "settings-page-admin")
	editor := h.user("editor", "settings-page-editor")
	adminToken, editorToken := h.token(admin), h.token(editor)
	if err := h.db.Create(&core.Option{Key: "site_language", Value: "zh-TW"}).Error; err != nil {
		t.Fatalf("seed site language: %v", err)
	}

	root := h.request(http.MethodGet, "/admin/settings", "", adminToken, "text/html", "")
	h.mustNoPanic(root, "GET /admin/settings")
	if root.code != http.StatusFound {
		t.Errorf("GET /admin/settings returned %d, want 302", root.code)
	} else if got := root.header.Get("Location"); got != "/admin/settings/site" {
		t.Errorf("GET /admin/settings redirects to %q, want /admin/settings/site", got)
	}

	routes := h.request(http.MethodGet, "/admin/settings/routes", "", adminToken, "text/html", "")
	h.mustNoPanic(routes, "GET /admin/settings/routes")
	if routes.code != http.StatusOK {
		t.Fatalf("GET /admin/settings/routes returned %d, want 200", routes.code)
	}
	if !strings.Contains(routes.body, "閱讀與網址") {
		t.Errorf("GET /admin/settings/routes does not render the tab bar label 閱讀與網址")
	}

	unknown := h.request(http.MethodGet, "/admin/settings/no-such-tab", "", adminToken, "text/html", "")
	h.mustNoPanic(unknown, "GET /admin/settings/no-such-tab")
	if unknown.code != http.StatusNotFound {
		t.Errorf("GET /admin/settings/no-such-tab returned %d, want 404", unknown.code)
	}

	denied := h.request(http.MethodGet, "/admin/settings/site", "", editorToken, "text/html", "")
	h.mustNoPanic(denied, "GET /admin/settings/site as editor")
	if denied.code == http.StatusOK {
		t.Errorf("editor received the site settings tab")
	}
}

func TestSettingsRegistryTypedValidation(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "settings-typed-admin")
	token := h.token(admin)

	cases := []struct {
		name, body string
		want       int
	}{
		{"int key with non-number", `{"per_page_home":"abc"}`, http.StatusBadRequest},
		{"bool key with maybe", `{"seo_index_site":"maybe"}`, http.StatusBadRequest},
		{"valid int and bool", `{"per_page_home":"20","seo_index_site":"false"}`, http.StatusOK},
		{"value over 64 KiB", `{"site_description":"` + strings.Repeat("x", 64<<10+1) + `"}`, http.StatusRequestEntityTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := h.request(http.MethodPost, "/api/admin/settings", tc.body, token, "application/json", "application/json")
			h.mustNoPanic(result, "POST /api/admin/settings "+tc.name)
			if result.code != tc.want {
				t.Errorf("POST /api/admin/settings (%s) returned %d, want %d; body=%q", tc.name, result.code, tc.want, settingsTruncate(result.body))
			}
		})
	}
}

func TestSettingsSecretStoredEncrypted(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "settings-secret-admin")
	token := h.token(admin)
	const plaintext = "plain-secret-SETTINGS-PROBE"

	result := h.request(http.MethodPost, "/api/admin/settings", `{"mail_password_enc":"`+plaintext+`"}`, token, "application/json", "application/json")
	h.mustNoPanic(result, "POST mail_password_enc")
	if result.code != http.StatusOK {
		t.Fatalf("POST mail_password_enc returned %d, want 200; body=%q", result.code, result.body)
	}

	var stored core.Option
	if err := h.db.Where("key = ?", "mail_password_enc").First(&stored).Error; err != nil {
		t.Fatalf("read mail_password_enc row: %v", err)
	}
	if strings.Contains(stored.Value, plaintext) || stored.Value == plaintext {
		t.Errorf("mail_password_enc is stored in plaintext in options.value")
	}

	get := h.request(http.MethodGet, "/api/admin/settings", "", token, "application/json", "")
	h.mustNoPanic(get, "GET /api/admin/settings after secret write")
	if strings.Contains(get.body, plaintext) {
		t.Errorf("GET /api/admin/settings returned the plaintext of mail_password_enc")
	}
}

func settingsTabKeys(tabs []SettingsTab) []string {
	keys := make([]string, 0, len(tabs))
	for _, tab := range tabs {
		keys = append(keys, tab.Key)
	}
	return keys
}

func settingsContainsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// settingsTruncate keeps failure messages readable when a body is large.
func settingsTruncate(s string) string {
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}
