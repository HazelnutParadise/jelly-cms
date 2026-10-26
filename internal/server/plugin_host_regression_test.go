package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"gorm.io/gorm"
)

func TestPluginHostSettingsPermissionPersistsTypedConfig(t *testing.T) {
	h := newPluginReloadHarness(t)
	const id = "host-settings"
	plgSeed(t, h.accessHarness, id, `{"id":"host-settings","name":"Host settings","version":"1.0.0","entrypoint":"index.js","permissions":[{"name":"settings"}],"settings":[{"key":"limit","type":"int","min":1,"max":10}]}`, false)
	setGrantedPermissions(t, h.accessHarness, id)
	entry := filepath.Join("data", "plugins", id, "index.js")
	script := `JellyCMS.registerHook("OnConfigProbe", function () {
  const result = JellyCMS.config.set("limit", 7);
  JellyCMS.log.info("host-settings-result:" + JSON.stringify(result));
});
JellyCMS.registerHook("OnConfigReadProbe", function () {
  JellyCMS.log.info("host-settings-read:" + JSON.stringify(JellyCMS.config.get("limit")));
});`
	if err := os.WriteFile(entry, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	token := h.token(h.user("admin", "host-settings-admin"))
	if r := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+id+"/enable", "", token); r.code != http.StatusOK {
		t.Fatalf("enable settings plugin = %d: %s", r.code, r.body)
	}
	h.runtime.CallHook(plugin.Hook("OnConfigProbe"), map[string]any{})
	h.logs.waitFor(t, "host-settings-result:")
	output := h.logs.snapshot()
	marker := "host-settings-result:"
	index := strings.Index(output, marker)
	if index < 0 {
		t.Fatalf("plugin produced no config.set result: %s", output)
	}
	result := output[index+len(marker):]
	if end := strings.IndexByte(result, '\n'); end >= 0 {
		result = result[:end]
	}
	if !strings.Contains(result, `"persisted":true`) || !strings.Contains(result, `"stored":true`) {
		t.Fatalf("settings-authorized config.set = %s, want stored and persisted true", result)
	}
	var values string
	if err := h.db.Raw(`SELECT "values"::text FROM plugin_settings WHERE plugin_id = ?`, id).Scan(&values).Error; err != nil {
		t.Fatalf("read persisted plugin settings: %v", err)
	}
	if !strings.Contains(values, `"limit": 7`) && !strings.Contains(values, `"limit":7`) {
		t.Errorf("persisted settings = %s, want typed integer limit 7", values)
	}

	if r := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+id+"/disable", "", token); r.code != http.StatusOK {
		t.Fatalf("disable settings plugin = %d: %s", r.code, r.body)
	}
	if r := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+id+"/enable", "", token); r.code != http.StatusOK {
		t.Fatalf("reenable settings plugin = %d: %s", r.code, r.body)
	}
	h.runtime.CallHook(plugin.Hook("OnConfigReadProbe"), map[string]any{})
	h.logs.waitFor(t, "host-settings-read:7")
	if !strings.Contains(h.logs.snapshot(), "host-settings-read:7") {
		t.Errorf("config.get after worker restart = %s, want 7", h.logs.snapshot())
	}
}

func TestPluginHostLegacyConfigRemainsSessionOnlyWithoutSettingsPermission(t *testing.T) {
	h := newPluginReloadHarness(t)
	const id = "host-legacy-config"
	plgSeed(t, h.accessHarness, id, `{"id":"host-legacy-config","name":"Legacy config","version":"1.0.0","entrypoint":"index.js"}`, false)
	script := `JellyCMS.registerHook("LegacyConfigProbe", function () {
  const saved = JellyCMS.config.set("undeclared_legacy_key", {nested: 3});
  const read = JellyCMS.config.get("undeclared_legacy_key");
  JellyCMS.log.info("host-legacy-config:" + JSON.stringify({saved: saved, read: read}));
});`
	hostWriteEntry(t, id, script)
	if r := hostEnable(t, h, id); r.code != http.StatusOK {
		t.Fatalf("enable legacy config plugin = %d: %s", r.code, r.body)
	}
	h.runtime.CallHook(plugin.Hook("LegacyConfigProbe"), map[string]any{})
	h.logs.waitFor(t, "host-legacy-config:")
	line := hostLogLine(h.logs.snapshot(), "host-legacy-config:")
	if !strings.Contains(line, `"persisted":false`) || !strings.Contains(line, `"nested":3`) {
		t.Fatalf("legacy no-permission config result = %s; want memory readback and persisted=false", line)
	}
	if n := impCount(t, h.accessHarness, `SELECT COUNT(*) FROM plugin_settings WHERE plugin_id = ?`, id); n != 0 {
		t.Errorf("legacy config without settings permission wrote %d plugin_settings rows", n)
	}
}

func TestPluginHostSettingsWriteFailureDoesNotReportPersistedOrReplaceValue(t *testing.T) {
	h := newPluginReloadHarness(t)
	const id = "host-settings-failure"
	plgSeed(t, h.accessHarness, id, `{"id":"host-settings-failure","name":"Settings failure","version":"1.0.0","entrypoint":"index.js","permissions":[{"name":"settings"}],"settings":[{"key":"limit","type":"int","min":1,"max":10}]}`, false)
	setGrantedPermissions(t, h.accessHarness, id)
	hostWriteEntry(t, id, `JellyCMS.registerHook("SettingsFailureProbe", function () {
  let saved;
  try { saved = JellyCMS.config.set("limit", 8); }
  catch (error) { saved = {stored: false, persisted: false, error: String(error)}; }
  const read = JellyCMS.config.get("limit");
  JellyCMS.log.info("host-settings-failure:" + JSON.stringify({saved: saved, read: read}));
});`)
	token := h.token(h.user("admin", "host-settings-failure-admin"))
	if r := updCall(h.accessHarness, http.MethodPut, "/api/admin/plugins/"+id+"/settings", `{"values":{"limit":4}}`, token); r.code != http.StatusOK {
		t.Fatalf("seed old plugin setting = %d: %s", r.code, r.body)
	}
	if err := h.db.Exec(`CREATE FUNCTION jelly_test_reject_host_settings() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected plugin setting failure'; END; $$`).Error; err != nil {
		t.Fatalf("create plugin setting failure function: %v", err)
	}
	if err := h.db.Exec(`CREATE TRIGGER jelly_test_reject_host_settings BEFORE UPDATE ON plugin_settings FOR EACH ROW EXECUTE FUNCTION jelly_test_reject_host_settings()`).Error; err != nil {
		t.Fatalf("create plugin setting failure trigger: %v", err)
	}
	t.Cleanup(func() {
		_ = h.db.Exec(`DROP TRIGGER IF EXISTS jelly_test_reject_host_settings ON plugin_settings`).Error
		_ = h.db.Exec(`DROP FUNCTION IF EXISTS jelly_test_reject_host_settings()`).Error
	})
	if r := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+id+"/enable", "", token); r.code != http.StatusOK {
		t.Fatalf("enable settings plugin = %d: %s", r.code, r.body)
	}
	h.runtime.CallHook(plugin.Hook("SettingsFailureProbe"), map[string]any{})
	h.logs.waitFor(t, "host-settings-failure:")
	line := hostLogLine(h.logs.snapshot(), "host-settings-failure:")
	if strings.Contains(line, `"persisted":true`) {
		t.Errorf("failed DB write reported persisted=true: %s", line)
	}
	if !strings.Contains(line, `"read":4`) {
		t.Errorf("config.get after failed write = %s, want previous value 4", line)
	}
	var values string
	if err := h.db.Raw(`SELECT "values"::text FROM plugin_settings WHERE plugin_id = ?`, id).Scan(&values).Error; err != nil {
		t.Fatalf("read settings after injected failure: %v", err)
	}
	if !strings.Contains(values, `"limit": 4`) && !strings.Contains(values, `"limit":4`) {
		t.Errorf("failed write changed durable settings: %s", values)
	}
}

func TestPluginHostStoreNamespaceNullAndPermissionDenial(t *testing.T) {
	h := newPluginReloadHarness(t)
	const allowedID = "host-store-allowed"
	plgSeed(t, h.accessHarness, allowedID, `{"id":"host-store-allowed","name":"Store allowed","version":"1.0.0","entrypoint":"index.js","permissions":[{"name":"store"}]}`, false)
	hostWriteEntry(t, allowedID, `JellyCMS.registerHook("StoreProbe", function () {
  if (!JellyCMS.store || typeof JellyCMS.store.set !== "function" || typeof JellyCMS.store.get !== "function") {
    JellyCMS.log.info("host-store-api-missing"); return;
  }
  try {
    JellyCMS.store.set("profile", "explicit-null", null);
    JellyCMS.store.set("other", "same-key", "allowed-plugin");
    JellyCMS.log.info("host-store-result:" + JSON.stringify({value: JellyCMS.store.get("profile", "explicit-null"), missing: JellyCMS.store.get("profile", "missing")}));
  } catch (error) { JellyCMS.log.info("host-store-error:" + String(error)); }
});`)
	token := h.token(h.user("admin", "host-store-admin"))
	setGrantedPermissions(t, h.accessHarness, allowedID)
	if r := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+allowedID+"/enable", "", token); r.code != http.StatusOK {
		t.Fatalf("enable store plugin = %d: %s", r.code, r.body)
	}
	h.runtime.CallHook(plugin.Hook("StoreProbe"), map[string]any{})
	h.logs.waitFor(t, "] host-store-result:")
	output := h.logs.snapshot()
	storeMissing := strings.Contains(output, "host-store-api-missing")
	storeError := strings.Contains(output, "host-store-error:")
	if storeMissing || storeError {
		if n := impCount(t, h.accessHarness, `SELECT COUNT(*) FROM plugin_data WHERE plugin_id = ?`, allowedID); n != 0 {
			t.Fatalf("failed/missing store API left %d plugin_data rows", n)
		}
		if storeError {
			t.Error("store-enabled plugin could not complete valid get/set operations")
		}
	} else {
		line := hostLogLine(output, "host-store-result:")
		if !strings.Contains(line, `"value":null`) || strings.Contains(line, `"missing":`) {
			t.Errorf("store missing/null distinction = %s", line)
		}
		if n := impCount(t, h.accessHarness, `SELECT COUNT(*) FROM plugin_data WHERE plugin_id = ? AND collection IN ('profile','other')`, allowedID); n != 2 {
			t.Errorf("store namespace durable rows = %d, want 2", n)
		}
	}

	const deniedID = "host-store-denied"
	plgSeed(t, h.accessHarness, deniedID, `{"id":"host-store-denied","name":"Store denied","version":"1.0.0","entrypoint":"index.js"}`, false)
	hostWriteEntry(t, deniedID, `JellyCMS.registerHook("StoreDeniedProbe", function () {
  if (!JellyCMS.store || typeof JellyCMS.store.set !== "function") {
    JellyCMS.log.info("host-store-denied-api-missing"); return;
  }
  try { JellyCMS.store.set("profile", "blocked", "denied"); }
  catch (error) { JellyCMS.log.info("host-store-denied:" + String(error)); return; }
  JellyCMS.log.info("host-store-denied-no-error");
});`)
	if r := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+deniedID+"/enable", "", token); r.code != http.StatusOK {
		t.Fatalf("enable no-store-permission plugin = %d: %s", r.code, r.body)
	}
	h.runtime.CallHook(plugin.Hook("StoreDeniedProbe"), map[string]any{})
	h.logs.waitFor(t, "] host-store-denied:")
	output = h.logs.snapshot()
	denialMissing := strings.Contains(output, "host-store-denied-api-missing")
	if strings.Contains(output, "host-store-denied-no-error") {
		t.Error("plugin without store permission wrote through JellyCMS.store")
	}
	if !strings.Contains(output, "host-store-denied:") {
		t.Errorf("plugin without store permission produced no denial error: %s", output)
	}
	if n := impCount(t, h.accessHarness, `SELECT COUNT(*) FROM plugin_data WHERE plugin_id = ?`, deniedID); n != 0 {
		t.Errorf("plugin without store permission wrote %d plugin_data rows", n)
	}
	if storeMissing || denialMissing {
		t.Errorf("store API is unavailable in an authorized or denied worker; allowed_missing=%t denied_missing=%t", storeMissing, denialMissing)
	}
}

func TestPluginHostHTTPFetchExactHostAndPrivateRedirectDenied(t *testing.T) {
	h := newPluginReloadHarness(t)
	t.Setenv("JELLY_TEST_ALLOW_LOOPBACK", "1")
	var originHits, redirectHits atomic.Int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originHits.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("allowed-origin-body"))
	}))
	defer origin.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectHits.Add(1)
		http.Redirect(w, r, "http://10.0.0.1/private", http.StatusFound)
	}))
	defer redirector.Close()
	originHost := strings.Split(strings.TrimPrefix(origin.URL, "http://"), ":")[0]
	redirectURL, err := url.Parse(redirector.URL)
	if err != nil {
		t.Fatalf("parse test redirect URL: %v", err)
	}
	const id = "host-http-fetch"
	manifest := fmt.Sprintf(`{"id":%q,"name":"HTTP fetch","version":"1.0.0","entrypoint":"index.js","permissions":[{"name":"http.fetch","hosts":[%q]}]}`, id, originHost)
	plgSeed(t, h.accessHarness, id, manifest, false)
	wrongHostURL := "http://localhost:" + redirectURL.Port() + "/"
	hostWriteEntry(t, id, fmt.Sprintf(`JellyCMS.registerHook("HTTPProbe", function () {
  if (!JellyCMS.http || typeof JellyCMS.http.fetch !== "function") {
    JellyCMS.log.info("host-http-api-missing");
    JellyCMS.log.info("host-http-probe-complete"); return;
  }
  try {
    const ok = JellyCMS.http.fetch(%q);
    JellyCMS.log.info("host-http-allowed:" + JSON.stringify(ok));
  } catch (error) { JellyCMS.log.info("host-http-allowed-error:" + String(error)); }
  try {
    JellyCMS.http.fetch(%q);
    JellyCMS.log.info("host-http-wrong-host-allowed");
  } catch (error) { JellyCMS.log.info("host-http-wrong-host-denied:" + String(error)); }
  try {
    JellyCMS.http.fetch(%q);
    JellyCMS.log.info("host-http-private-redirect-allowed");
  } catch (error) { JellyCMS.log.info("host-http-private-redirect-denied:" + String(error)); }
  JellyCMS.log.info("host-http-probe-complete");
});`, origin.URL, wrongHostURL, redirector.URL))
	setGrantedPermissions(t, h.accessHarness, id)
	token := h.token(h.user("admin", "host-http-admin"))
	if r := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+id+"/enable", "", token); r.code != http.StatusOK {
		t.Fatalf("enable HTTP plugin = %d: %s", r.code, r.body)
	}
	h.runtime.CallHook(plugin.Hook("HTTPProbe"), map[string]any{})
	h.logs.waitFor(t, "host-http-probe-complete")
	output := h.logs.snapshot()
	if !strings.Contains(output, "host-http-wrong-host-denied:") {
		t.Errorf("wrong-host request has no explicit denial result: %s", output)
	}
	if !strings.Contains(output, "host-http-private-redirect-denied:") {
		t.Errorf("private-redirect request has no explicit denial result: %s", output)
	}
	if strings.Contains(output, "host-http-api-missing") {
		if originHits.Load() != 0 || redirectHits.Load() != 0 {
			t.Errorf("missing HTTP API still caused network side effects: origin=%d redirect=%d", originHits.Load(), redirectHits.Load())
		}
		t.Fatal("worker reported missing JellyCMS.http.fetch")
	}
	if originHits.Load() != 1 {
		t.Errorf("exact-host local endpoint hits = %d, want 1", originHits.Load())
	}
	if !strings.Contains(output, "allowed-origin-body") {
		t.Errorf("allowed HTTP fetch did not return its bounded body: %s", output)
	}
	if strings.Contains(output, "host-http-wrong-host-allowed") {
		t.Error("http.fetch allowed a hostname outside the manifest's exact host list")
	}
	if redirectHits.Load() != 1 || strings.Contains(output, "host-http-private-redirect-allowed") {
		t.Errorf("private redirect was not rejected after one allowed origin request; hits=%d logs=%s", redirectHits.Load(), output)
	}
}

func hostWriteEntry(t *testing.T, id, source string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join("data", "plugins", id, "index.js"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}

func hostEnable(t *testing.T, h *reloadHarness, id string) accessResult {
	t.Helper()
	token := h.token(h.user("admin", "host-admin-"+id))
	return updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+id+"/enable", "", token)
}

func hostLogLine(output, marker string) string {
	idx := strings.LastIndex(output, marker)
	if idx < 0 {
		return ""
	}
	line := output[idx+len(marker):]
	if end := strings.IndexByte(line, '\n'); end >= 0 {
		line = line[:end]
	}
	return line
}

func setGrantedPermissions(t *testing.T, h *accessHarness, id string) {
	t.Helper()
	meta, err := plugin.ReadManifest(filepath.Join("data", "plugins", id))
	if err != nil {
		t.Fatalf("read permissions for %s: %v", id, err)
	}
	hash := plugin.PermissionNamesHash(plugin.ManifestPermissionNames(meta))
	if err := h.db.Exec(`UPDATE plugins SET permissions_granted_hash = ? WHERE id = ?`, hash, id).Error; err != nil {
		t.Fatalf("record granted permission hash for %s: %v", id, err)
	}
}

func TestPluginHostOnBootCanUseGrantedSettingsWhileEnabling(t *testing.T) {
	h := newPluginReloadHarness(t)
	const id = "host-boot-settings"
	plgSeed(t, h.accessHarness, id, `{"id":"host-boot-settings","name":"Boot settings","version":"1.0.0","entrypoint":"index.js","permissions":[{"name":"settings"}],"settings":[{"key":"limit","type":"int","default":3,"min":1,"max":10}]}`, false)
	setGrantedPermissions(t, h.accessHarness, id)
	hostWriteEntry(t, id, `JellyCMS.registerHook("OnBoot", function () {
  const before = JellyCMS.config.get("limit");
  const next = before === 3 ? 7 : before;
  const saved = JellyCMS.config.set("limit", next);
  JellyCMS.log.info("host-boot-settings:" + JSON.stringify({before: before, saved: saved}));
});`)
	token := h.token(h.user("admin", "host-boot-settings-admin"))
	if result := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+id+"/enable", "", token); result.code != http.StatusOK {
		t.Fatalf("enable granted plugin with OnBoot settings access = %d: %s", result.code, result.body)
	}
	h.logs.waitFor(t, `"before":3`)
	firstBoot := hostLogLine(h.logs.snapshot(), "host-boot-settings:")
	if !strings.Contains(firstBoot, `"before":3`) || !strings.Contains(firstBoot, `"persisted":true`) {
		t.Fatalf("OnBoot did not read the declared default and persist its update: %s", firstBoot)
	}
	var value int
	if err := h.db.Raw(`SELECT ("values"->>'limit')::int FROM plugin_settings WHERE plugin_id = ?`, id).Scan(&value).Error; err != nil || value != 7 {
		t.Fatalf("setting after initial OnBoot = %d, err %v; want 7", value, err)
	}
	if result := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+id+"/disable", "", token); result.code != http.StatusOK {
		t.Fatalf("disable boot settings plugin = %d: %s", result.code, result.body)
	}
	if result := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+id+"/enable", "", token); result.code != http.StatusOK {
		t.Fatalf("re-enable boot settings plugin = %d: %s", result.code, result.body)
	}
	h.logs.waitFor(t, `"before":7`)
	if secondBoot := hostLogLine(h.logs.snapshot(), `"before":7`); secondBoot == "" {
		t.Fatalf("OnBoot did not read the persisted value after restart: %s", h.logs.snapshot())
	}
	if err := h.db.Raw(`SELECT ("values"->>'limit')::int FROM plugin_settings WHERE plugin_id = ?`, id).Scan(&value).Error; err != nil || value != 7 {
		t.Errorf("setting after re-enable OnBoot = %d, err %v; want retained value 7", value, err)
	}
}

func TestPluginHostDatabaseReadIsCanceledWithinWorkerDeadline(t *testing.T) {
	h := newPluginReloadHarness(t)
	const id = "host-db-deadline"
	plgSeed(t, h.accessHarness, id, `{"id":"host-db-deadline","name":"DB deadline","version":"1.0.0","entrypoint":"index.js","permissions":[{"name":"settings"}],"settings":[{"key":"limit","type":"int","default":3,"min":1,"max":10}]}`, false)
	setGrantedPermissions(t, h.accessHarness, id)
	hostWriteEntry(t, id, `JellyCMS.registerHook("DatabaseDeadlineProbe", function () {
  try {
    JellyCMS.config.get("limit");
    JellyCMS.log.info("host-db-deadline:unexpected-read-success");
  } catch (error) {
    JellyCMS.log.info("host-db-deadline:read-canceled:" + String(error));
  }
});`)
	token := h.token(h.user("admin", "host-db-deadline-admin"))
	if result := updCall(h.accessHarness, http.MethodPut, "/api/admin/plugins/"+id+"/settings", `{"values":{"limit":5}}`, token); result.code != http.StatusOK {
		t.Fatalf("seed declared host setting = %d: %s", result.code, result.body)
	}
	if result := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+id+"/enable", "", token); result.code != http.StatusOK {
		t.Fatalf("enable database deadline plugin = %d: %s", result.code, result.body)
	}
	sqlDB, err := h.db.DB()
	if err != nil {
		t.Fatalf("get PostgreSQL pool: %v", err)
	}
	lockTx, err := sqlDB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin plugin settings lock transaction: %v", err)
	}
	if _, err := lockTx.ExecContext(context.Background(), `LOCK TABLE plugin_settings IN ACCESS EXCLUSIVE MODE`); err != nil {
		_ = lockTx.Rollback()
		t.Fatalf("lock plugin_settings table: %v", err)
	}
	lockHeld := true
	defer func() {
		if lockHeld {
			_ = lockTx.Rollback()
		}
	}()

	hookDone := make(chan struct{})
	started := time.Now()
	go func() {
		h.runtime.CallHook(plugin.Hook("DatabaseDeadlineProbe"), map[string]any{})
		close(hookDone)
	}()
	if !waitForPluginHostSettingsLock(t, h.db, 3*time.Second, true) {
		_ = lockTx.Rollback()
		lockHeld = false
		select {
		case <-hookDone:
		case <-time.After(3 * time.Second):
			t.Fatal("host hook remained blocked after the database lock was released")
		}
		t.Fatal("host config.get never entered a PostgreSQL Lock wait on plugin_settings")
	}
	deadline := started.Add(1250 * time.Millisecond)
	returnedWithinBudget := false
	select {
	case <-hookDone:
		returnedWithinBudget = time.Since(started) <= 1250*time.Millisecond
	case <-time.After(time.Until(deadline)):
	}
	if returnedWithinBudget && !waitForPluginHostSettingsLock(t, h.db, 150*time.Millisecond, false) {
		t.Error("config.get hook returned but its host query remained blocked on plugin_settings")
	}
	if err := lockTx.Rollback(); err != nil {
		t.Errorf("release plugin settings table lock: %v", err)
	}
	lockHeld = false
	select {
	case <-hookDone:
	case <-time.After(3 * time.Second):
		t.Fatal("host hook did not terminate after plugin settings lock release")
	}
	if !returnedWithinBudget {
		t.Fatal("host config.get did not honor the worker's one-second deadline while its PostgreSQL query was lock-blocked")
	}
	if !strings.Contains(h.logs.snapshot(), "host-db-deadline:read-canceled:") {
		t.Errorf("host config.get did not report the canceled read: %s", h.logs.snapshot())
	}
}

func waitForPluginHostSettingsLock(t *testing.T, db *gorm.DB, timeout time.Duration, wantBlocked bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var count int64
		err := db.Raw(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid() AND state = 'active' AND wait_event_type = 'Lock' AND query ILIKE '%plugin_settings%'`).Scan(&count).Error
		if err != nil {
			t.Fatalf("inspect host plugin_settings query activity: %v", err)
		}
		blocked := count > 0
		if blocked == wantBlocked {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestPluginHostLegacyConfigClonesNonJSONValuesWithoutSettingsPermission(t *testing.T) {
	h := newPluginReloadHarness(t)
	const id = "host-legacy-config-clone"
	plgSeed(t, h.accessHarness, id, `{"id":"host-legacy-config-clone","name":"Legacy config clone","version":"1.0.0","entrypoint":"index.js"}`, false)
	hostWriteEntry(t, id, `JellyCMS.registerHook("LegacyCloneProbe", function () {
  const fn = function () { return "function-value"; };
  let functionWriteRejected = false;
  try { JellyCMS.config.set("function", fn); } catch (error) { functionWriteRejected = true; }
  let functionRead;
  let functionReadErrored = false;
  try { functionRead = JellyCMS.config.get("function"); } catch (error) { functionReadErrored = true; }

  const circular = {};
  circular.self = circular;
  let circularWriteRejected = false;
  try { JellyCMS.config.set("circular", circular); } catch (error) { circularWriteRejected = true; }
  let circularRead;
  let circularReadErrored = false;
  try { circularRead = JellyCMS.config.get("circular"); } catch (error) { circularReadErrored = true; }

  let undefinedWriteRejected = false;
  try { JellyCMS.config.set("undefined", undefined); } catch (error) { undefinedWriteRejected = true; }
  const undefinedRead = JellyCMS.config.get("undefined");
  JellyCMS.log.info("host-legacy-config-clone:" + JSON.stringify({
    functionPreserved: typeof functionRead === "function" && functionRead() === "function-value",
    functionReadErrored: functionReadErrored,
    functionWriteRejected: functionWriteRejected,
    circularPreserved: circularRead !== undefined && circularRead === circularRead.self,
    circularReadErrored: circularReadErrored,
    circularWriteRejected: circularWriteRejected,
    undefinedPreserved: undefinedRead === undefined,
    undefinedWriteRejected: undefinedWriteRejected
  }));
});`)
	if result := hostEnable(t, h, id); result.code != http.StatusOK {
		t.Fatalf("enable legacy no-settings plugin = %d: %s", result.code, result.body)
	}
	h.runtime.CallHook(plugin.Hook("LegacyCloneProbe"), map[string]any{})
	h.logs.waitFor(t, "host-legacy-config-clone:")
	line := hostLogLine(h.logs.snapshot(), "host-legacy-config-clone:")
	for _, expected := range []string{
		`"functionPreserved":true`, `"functionReadErrored":false`, `"functionWriteRejected":false`,
		`"circularPreserved":true`, `"circularReadErrored":false`, `"circularWriteRejected":false`,
		`"undefinedPreserved":true`, `"undefinedWriteRejected":false`,
	} {
		if !strings.Contains(line, expected) {
			t.Errorf("legacy config clone result missing %s: %s", expected, line)
		}
	}
	if n := impCount(t, h.accessHarness, `SELECT COUNT(*) FROM plugin_settings WHERE plugin_id = ?`, id); n != 0 {
		t.Errorf("legacy arbitrary config wrote %d persistent settings rows", n)
	}
}
