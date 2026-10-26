package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

// plgSeed writes data/plugins/<id>/plugin.json and the matching plugins row.
// Installation itself is covered by the store tests; these tests start from
// an installed plugin.
func plgSeed(t *testing.T, h *accessHarness, id, manifest string, enabled bool) {
	t.Helper()
	dir := filepath.Join("data", "plugins", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.db.Exec(`INSERT INTO plugins (id, kind, version, source, enabled, signature) VALUES (?, 'plugin', '1.0.0', 'upload', ?, 'unsigned')`, id, enabled).Error; err != nil {
		t.Fatalf("insert plugin row %s: %v", id, err)
	}
}

// plgCollect gathers every value stored under key anywhere in a decoded JSON tree.
func plgCollect(v any, key string, out *[]any) {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if k == key {
				*out = append(*out, child)
			}
			plgCollect(child, key, out)
		}
	case []any:
		for _, child := range x {
			plgCollect(child, key, out)
		}
	}
}

func plgDecode(t *testing.T, body string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("response is not JSON: %v: %s", err, body)
	}
	return v
}

func TestPluginEnableDisablePersistsAndUnloads(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "plg-life-admin")
	token := h.token(admin)
	plgSeed(t, h, "plg-life", `{"id":"plg-life","name":"Life","version":"1.0.0"}`, false)

	if r := updCall(h, http.MethodPost, "/api/admin/plugins/plg-life/enable", "", token); r.code < 200 || r.code > 299 {
		t.Fatalf("enable = %d, want 2xx: %s", r.code, r.body)
	}
	if n := impCount(t, h, "SELECT COUNT(*) FROM plugins WHERE id = 'plg-life' AND enabled"); n != 1 {
		t.Fatalf("enable did not persist: enabled rows = %d", n)
	}
	if r := updCall(h, http.MethodPost, "/api/admin/plugins/plg-life/disable", "", token); r.code < 200 || r.code > 299 {
		t.Fatalf("disable = %d, want 2xx: %s", r.code, r.body)
	}
	if n := impCount(t, h, "SELECT COUNT(*) FROM plugins WHERE id = 'plg-life' AND NOT enabled"); n != 1 {
		t.Fatalf("disable did not persist: disabled rows = %d", n)
	}

	// Restart: a fresh router reads the persisted state and must still show
	// the plugin disabled.
	restarted := echo.New()
	RegisterRoutes(restarted, theme.NewManager("web/themes"), nil)
	rec := updCallOn(restarted, h, http.MethodGet, "/api/admin/plugins/plg-life", token)
	if rec.code != http.StatusOK {
		t.Fatalf("GET plugin after restart = %d: %s", rec.code, rec.body)
	}
	var enabled []any
	plgCollect(plgDecode(t, rec.body), "enabled", &enabled)
	if len(enabled) == 0 || enabled[0] != false {
		t.Errorf("enabled after restart = %v, want false", enabled)
	}
	// Route and hook inactivity after a runtime restart is asserted once the
	// host runtime can load a fixture plugin without a worker binary.
}

// updCallOn is updCall against an arbitrary router.
func updCallOn(e *echo.Echo, h *accessHarness, method, path, token string) accessResult {
	saved := h.echo
	h.echo = e
	defer func() { h.echo = saved }()
	return updCall(h, method, path, "", token)
}

func TestPluginSettingsValidateMaskAndSnapshot(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "plg-cfg-admin")
	token := h.token(admin)
	plgSeed(t, h, "plg-cfg", `{"id":"plg-cfg","name":"Config","version":"1.0.0","settings":[`+
		`{"key":"api_key","type":"secret","label":"API key"},`+
		`{"key":"limit","type":"int","min":1,"max":10,"default":3}]}`, true)
	const secret = "s3cret-value-123"
	settingsPath := "/api/admin/plugins/plg-cfg/settings"

	if r := updCall(h, http.MethodPut, settingsPath, `{"values":{"limit":99}}`, token); r.code != http.StatusBadRequest {
		t.Fatalf("out-of-range limit = %d, want 400: %s", r.code, r.body)
	}
	if r := updCall(h, http.MethodPut, settingsPath, fmt.Sprintf(`{"values":{"api_key":%q,"limit":5}}`, secret), token); r.code < 200 || r.code > 299 {
		t.Fatalf("valid settings = %d, want 2xx: %s", r.code, r.body)
	}
	for v := 2; v <= 5; v++ {
		if r := updCall(h, http.MethodPut, settingsPath, fmt.Sprintf(`{"values":{"limit":%d}}`, v), token); r.code < 200 || r.code > 299 {
			t.Fatalf("save limit=%d = %d: %s", v, r.code, r.body)
		}
	}

	get := updCall(h, http.MethodGet, settingsPath, "", token)
	if get.code != http.StatusOK {
		t.Fatalf("GET settings = %d: %s", get.code, get.body)
	}
	if strings.Contains(get.body, secret) {
		t.Error("GET settings returned the secret in clear text")
	}
	var apiKey []any
	plgCollect(plgDecode(t, get.body), "api_key", &apiKey)
	if len(apiKey) == 0 {
		t.Fatalf("settings response has no api_key entry: %s", get.body)
	}
	masked, ok := apiKey[0].(map[string]any)
	if !ok || masked["set"] != true || masked["source"] == nil {
		t.Errorf("api_key = %v, want {set:true, source:...}", apiKey[0])
	}

	if n := impCount(t, h, "SELECT COUNT(*) FROM plugin_setting_snapshots WHERE plugin_id = 'plg-cfg'"); n != 3 {
		t.Errorf("snapshots kept = %d, want 3", n)
	}
	if n := impCount(t, h, "SELECT COUNT(*) FROM plugin_settings WHERE plugin_id = 'plg-cfg' AND secrets_enc IS NOT NULL"); n != 1 {
		t.Errorf("encrypted secrets row count = %d, want 1", n)
	}
	var stored string
	h.db.Raw("SELECT COALESCE(encode(secrets_enc, 'escape'), '') FROM plugin_settings WHERE plugin_id = 'plg-cfg'").Scan(&stored)
	if strings.Contains(stored, secret) {
		t.Error("secret stored in plain text in plugin_settings.secrets_enc")
	}
}

func TestPluginDetailLogsAndActions(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "plg-detail-admin")
	token := h.token(admin)
	plgSeed(t, h, "plg-det", `{"id":"plg-det","name":"Detail","version":"1.0.0",`+
		`"permissions":[{"name":"http.fetch"},{"name":"fs.write"}],`+
		`"actions":[{"id":"refresh","label":"Refresh"}]}`, false)

	detail := updCall(h, http.MethodGet, "/api/admin/plugins/plg-det", "", token)
	if detail.code != http.StatusOK {
		t.Fatalf("GET plugin detail = %d: %s", detail.code, detail.body)
	}
	var enforced []any
	plgCollect(plgDecode(t, detail.body), "enforced", &enforced)
	var sawTrue, sawFalse bool
	for _, v := range enforced {
		if v == true {
			sawTrue = true
		}
		if v == false {
			sawFalse = true
		}
	}
	if !sawTrue || !sawFalse {
		t.Errorf("permissions do not separate enforced (http.fetch) from declared-only (fs.write): %v", enforced)
	}

	logs := updCall(h, http.MethodGet, "/api/admin/plugins/plg-det/logs", "", token)
	if logs.code != http.StatusOK || !strings.HasPrefix(strings.TrimSpace(logs.body), "[") {
		t.Errorf("GET logs = %d %s, want 200 and a JSON array", logs.code, logs.body)
	}
	if r := updCall(h, http.MethodPost, "/api/admin/plugins/plg-det/test", "", token); r.code < 200 || r.code > 299 {
		t.Errorf("POST test = %d, want 2xx: %s", r.code, r.body)
	}
	if r := updCall(h, http.MethodPost, "/api/admin/plugins/plg-det/actions/refresh", "", token); r.code < 200 || r.code > 299 {
		t.Errorf("POST actions/refresh = %d, want 2xx: %s", r.code, r.body)
	}
	if r := updCall(h, http.MethodPost, "/api/admin/plugins/plg-det/actions/missing", "", token); r.code != http.StatusNotFound {
		t.Errorf("POST undeclared action = %d, want 404", r.code)
	}
}

func TestExtensionBindingFallsBackToBuiltin(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "plg-ext-admin")
	token := h.token(admin)

	list := updCall(h, http.MethodGet, "/api/admin/extensions", "", token)
	if list.code != http.StatusOK || !strings.Contains(list.body, "search.engine") {
		t.Fatalf("GET extensions = %d, want 200 listing search.engine: %s", list.code, list.body)
	}
	if r := updCall(h, http.MethodPut, "/api/admin/extensions/search.engine", `{"active":["missing-id"]}`, token); r.code != http.StatusBadRequest {
		t.Errorf("bind unknown provider = %d, want 400: %s", r.code, r.body)
	}
	if r := updCall(h, http.MethodPut, "/api/admin/extensions/search.engine", `{"active":[]}`, token); r.code < 200 || r.code > 299 {
		t.Fatalf("clear bindings = %d, want 2xx: %s", r.code, r.body)
	}
	after := updCall(h, http.MethodGet, "/api/admin/extensions", "", token)
	if !strings.Contains(after.body, "builtin") {
		t.Errorf("search.engine with no active providers does not resolve to builtin: %s", after.body)
	}
	if strings.Contains(after.body, "missing-id") {
		t.Error("rejected provider id was stored")
	}
}

func TestPluginUninstallKeepOrDeleteSettings(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "plg-rm-admin")
	token := h.token(admin)

	seedSettings := func(id string) {
		t.Helper()
		plgSeed(t, h, id, fmt.Sprintf(`{"id":%q,"name":"Remove","version":"1.0.0"}`, id), false)
		if err := h.db.Exec(`INSERT INTO plugin_settings (plugin_id, "values", version) VALUES (?, '{"limit":3}'::jsonb, 1)`, id).Error; err != nil {
			t.Fatalf("insert settings for %s: %v", id, err)
		}
		if err := h.db.Exec(`INSERT INTO plugin_data (plugin_id, collection, key, value) VALUES (?, 'items', 'a', '{"x":1}'::jsonb)`, id).Error; err != nil {
			t.Fatalf("insert plugin data for %s: %v", id, err)
		}
	}

	seedSettings("plg-rm-keep")
	if r := updCall(h, http.MethodDelete, "/api/admin/plugins/plg-rm-keep?keep_settings=true", "", token); r.code != http.StatusOK && r.code != http.StatusNoContent {
		t.Fatalf("uninstall keeping settings = %d: %s", r.code, r.body)
	}
	if n := impCount(t, h, "SELECT COUNT(*) FROM plugins WHERE id = 'plg-rm-keep'"); n != 0 {
		t.Errorf("plugin row still present after uninstall: %d", n)
	}
	if n := impCount(t, h, "SELECT COUNT(*) FROM plugin_settings WHERE plugin_id = 'plg-rm-keep'"); n != 1 {
		t.Errorf("settings kept = %d rows, want 1", n)
	}

	seedSettings("plg-rm-drop")
	if r := updCall(h, http.MethodDelete, "/api/admin/plugins/plg-rm-drop?keep_settings=false", "", token); r.code != http.StatusOK && r.code != http.StatusNoContent {
		t.Fatalf("uninstall deleting settings = %d: %s", r.code, r.body)
	}
	if n := impCount(t, h, "SELECT COUNT(*) FROM plugin_settings WHERE plugin_id = 'plg-rm-drop'"); n != 0 {
		t.Errorf("settings still present after delete: %d rows", n)
	}

	// Uninstall is interactive-only.
	seedSettings("plg-rm-bearer")
	req := updCallBearer(h, http.MethodDelete, "/api/admin/plugins/plg-rm-bearer?keep_settings=true", token)
	if req != http.StatusUnauthorized && req != http.StatusForbidden {
		t.Errorf("Bearer uninstall = %d, want 401 or 403", req)
	}
}

// updCallBearer sends a Bearer-authenticated request and returns the status.
func updCallBearer(h *accessHarness, method, path, token string) int {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	return rec.Code
}
