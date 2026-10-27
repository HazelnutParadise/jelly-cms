package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
)

// General settings writes must not provide a second route around the dedicated
// interactive, password-confirmed injection endpoint.
func TestSystemIntegrityGeneralSettingsCannotBypassInjectionWriteGate(t *testing.T) {
	h := newAccessHarness(t)
	admin := s08bPasswordUser(t, h, "integrity-injection-admin", "admin")
	jwt := h.token(admin)
	for _, key := range []string{"inject_head_all", "inject_head_content", "inject_footer", "inject_on_commerce"} {
		for _, method := range []string{http.MethodPost, http.MethodPut} {
			t.Run(method+"/"+key, func(t *testing.T) {
				if err := h.db.Exec(`DELETE FROM options WHERE key = ?`, key).Error; err != nil {
					t.Fatal(err)
				}
				value := "integrity-bypass-probe"
				if key == "inject_on_commerce" {
					value = "true"
				}
				body := s08bJSON(t, map[string]string{key: value})
				for _, authMode := range []string{"cookie", "bearer"} {
					if err := h.db.Exec(`DELETE FROM options WHERE key = ?`, key).Error; err != nil {
						t.Fatal(err)
					}
					req := httptest.NewRequest(method, "/api/admin/settings", strings.NewReader(body))
					req.Header.Set("X-Jelly-CSRF", "1")
					req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
					req.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
					if authMode == "cookie" {
						req.AddCookie(&http.Cookie{Name: "jwt_token", Value: jwt})
					} else {
						req.Header.Set(echo.HeaderAuthorization, "Bearer "+jwt)
					}
					rec := httptest.NewRecorder()
					h.echo.ServeHTTP(rec, req)
					r := accessResult{code: rec.Code, body: rec.Body.String()}
					if r.code >= 200 && r.code < 300 {
						t.Errorf("%s general settings %s via %s returned %d; injection must require dedicated interactive password confirmation", method, key, authMode, r.code)
					}
					if got := s08bOptionValue(t, h, key); got != "" {
						t.Errorf("rejected general settings write persisted %s=%q via %s", key, got, authMode)
					}
					if err := h.db.Exec(`DELETE FROM options WHERE key = ?`, key).Error; err != nil {
						t.Fatal(err)
					}
				}
				if got := s08bOptionValue(t, h, key); got != "" {
					t.Errorf("rejected general settings writes persisted %s=%q", key, got)
				}
			})
		}
	}
	for key, configuredValue := range map[string]string{"cache_driver": "redis", "queue_driver": "redis"} {
		if err := h.db.Exec(`DELETE FROM options WHERE key = ?`, key).Error; err != nil {
			t.Fatal(err)
		}
		r := h.request(http.MethodPost, "/api/admin/settings", s08bJSON(t, map[string]string{key: configuredValue}), jwt, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
		if r.code >= 200 && r.code < 300 {
			t.Errorf("general settings accepted %s=%s with status %d; infrastructure drivers must change through their connection-tested API", key, configuredValue, r.code)
		}
		if got := s08bOptionValue(t, h, key); got != "" {
			t.Errorf("general settings changed infrastructure driver %s to %q", key, got)
		}
	}
}

func TestSystemIntegrityFailedPluginDatabaseWritePreservesDiskAndRow(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "integrity-plugin-admin")
	token := h.token(admin)
	const existingID = "integrity-plugin-existing"
	oldArchive := secZip(t, secZipEntry{name: "plugin.json", body: secManifest(t, existingID)}, secZipEntry{name: "main.js", body: []byte("old implementation")})
	if r := secUploadPlugin(t, h, token, "old.zip", oldArchive); r.code != http.StatusCreated && r.code != http.StatusOK {
		t.Fatalf("initial plugin install = %d: %s", r.code, r.body)
	}
	if err := h.db.Exec(`CREATE FUNCTION jelly_test_reject_plugin_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected plugin row failure'; END; $$`).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.db.Exec(`CREATE TRIGGER jelly_test_reject_plugin_write BEFORE INSERT OR UPDATE ON plugins FOR EACH ROW EXECUTE FUNCTION jelly_test_reject_plugin_write()`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = h.db.Exec(`DROP TRIGGER IF EXISTS jelly_test_reject_plugin_write ON plugins`)
		_ = h.db.Exec(`DROP FUNCTION IF EXISTS jelly_test_reject_plugin_write()`)
	})
	newManifest := strings.Replace(string(secManifest(t, existingID)), `"1.0.0"`, `"2.0.0"`, 1)
	newArchive := secZip(t, secZipEntry{name: "plugin.json", body: []byte(newManifest)}, secZipEntry{name: "main.js", body: []byte("replacement implementation")})
	if r := secUploadPlugin(t, h, token, "new.zip", newArchive); r.code < 400 {
		t.Fatalf("replacement with failing DB write = %d, want error", r.code)
	}
	manifest, err := os.ReadFile(filepath.Join(pluginDirRoot, existingID, "plugin.json"))
	if err != nil || strings.Contains(string(manifest), `"2.0.0"`) {
		t.Errorf("old plugin files not preserved after DB failure: manifest=%s err=%v", manifest, err)
	}
	mainJS, err := os.ReadFile(filepath.Join(pluginDirRoot, existingID, "main.js"))
	if err != nil || string(mainJS) != "old implementation" {
		t.Errorf("old plugin entrypoint after DB failure = %q, err %v", mainJS, err)
	}
	var version string
	if err := h.db.Raw(`SELECT version FROM plugins WHERE id = ?`, existingID).Scan(&version).Error; err != nil {
		t.Fatal(err)
	}
	if version != "1.0.0" {
		t.Errorf("plugin DB version after rejected update = %q, want 1.0.0", version)
	}
	const newID = "integrity-plugin-new-failure"
	newOnly := secZip(t, secZipEntry{name: "plugin.json", body: secManifest(t, newID)}, secZipEntry{name: "main.js", body: []byte("new plugin")})
	if r := secUploadPlugin(t, h, token, "new-only.zip", newOnly); r.code < 400 {
		t.Fatalf("new plugin with failing DB insert = %d, want error", r.code)
	}
	if _, err := os.Stat(filepath.Join(pluginDirRoot, newID)); !os.IsNotExist(err) {
		t.Errorf("failed new plugin archive remains on disk after simulated restart: stat error %v", err)
	}
	var rows int64
	if err := h.db.Raw(`SELECT count(*) FROM plugins WHERE id = ?`, newID).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("failed new plugin has %d database rows", rows)
	}
}

func TestSystemIntegrityBackupRestoreRoundTripsImmutableLedgersAndAuditSnapshots(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "integrity-backup-admin")
	widget := core.Widget{Name: "integrity-backup-widget", Title: "Backup widget", Version: 1, Status: "published"}
	if err := h.db.Create(&widget).Error; err != nil {
		t.Fatal(err)
	}
	version := core.WidgetVersion{WidgetID: widget.ID, Version: 1, Source: "<p>published snapshot</p>", Params: "[]", Published: true, CreatedAt: time.Now().UTC()}
	if err := h.db.Create(&version).Error; err != nil {
		t.Fatal(err)
	}
	product := core.Product{Title: "Integrity backup product", Slug: "integrity-backup-product", Price: 123, Stock: 7, Status: "active"}
	if err := h.db.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	variant := core.ProductVariant{ProductID: product.ID, Name: "", SKU: "integrity-backup-sku", Price: 123, Stock: 7, Status: "active"}
	if err := h.db.Create(&variant).Error; err != nil {
		t.Fatal(err)
	}
	movement := core.StockMovement{VariantID: variant.ID, Delta: 2, Reason: "adjust", CreatedAt: time.Now().UTC()}
	if err := h.db.Create(&movement).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.db.First(&movement, movement.ID).Error; err != nil {
		t.Fatalf("read persisted stock movement fixture: %v", err)
	}
	identity := core.OAuthIdentity{UserID: admin.ID, Provider: "github", Subject: "integrity-backup-subject"}
	if err := h.db.Create(&identity).Error; err != nil {
		t.Fatal(err)
	}

	var before struct {
		WidgetVersions, WidgetAudit, WidgetVersionAudit, StockMovements, OAuthIdentities, OAuthAudit int64
		WidgetAuditRows, WidgetVersionAuditRows, OAuthAuditRows                                      string
	}
	if err := h.db.Raw(`SELECT (SELECT count(*) FROM widget_versions), (SELECT count(*) FROM widget_audit_log), (SELECT count(*) FROM widget_version_audit_log), (SELECT count(*) FROM stock_movements), (SELECT count(*) FROM oauth_identities), (SELECT count(*) FROM oauth_identity_audit_log)`).Row().Scan(&before.WidgetVersions, &before.WidgetAudit, &before.WidgetVersionAudit, &before.StockMovements, &before.OAuthIdentities, &before.OAuthAudit); err != nil {
		t.Fatal(err)
	}
	if before.WidgetVersionAudit == 0 || before.OAuthAudit == 0 {
		t.Fatalf("fixture did not create trigger audit snapshots: %+v", before)
	}
	if err := h.db.Raw(`SELECT COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY id)::text FROM widget_audit_log t),'[]'), COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY id)::text FROM widget_version_audit_log t),'[]'), COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY id)::text FROM oauth_identity_audit_log t),'[]')`).Row().Scan(&before.WidgetAuditRows, &before.WidgetVersionAuditRows, &before.OAuthAuditRows); err != nil {
		t.Fatal(err)
	}

	token := h.token(admin)
	created := bkpCall(h, http.MethodPost, "/api/admin/backups", `{"content":["db"]}`, token, false)
	if created.code != http.StatusAccepted {
		t.Fatalf("create database backup = %d: %s", created.code, created.body)
	}
	backupID := bkpLatest(t, h).ID
	path := fmt.Sprintf("/api/admin/backups/%d/restore", backupID)
	res := bkpCall(h, http.MethodPost, path, `{"confirm":"RESTORE"}`, token, false)
	if res.code != http.StatusOK && res.code != http.StatusAccepted {
		t.Fatalf("restore complete database snapshot = %d: %s", res.code, res.body)
	}
	var after struct {
		WidgetVersions, WidgetAudit, WidgetVersionAudit, StockMovements, OAuthIdentities, OAuthAudit int64
		WidgetAuditRows, WidgetVersionAuditRows, OAuthAuditRows                                      string
	}
	if err := h.db.Raw(`SELECT (SELECT count(*) FROM widget_versions), (SELECT count(*) FROM widget_audit_log), (SELECT count(*) FROM widget_version_audit_log), (SELECT count(*) FROM stock_movements), (SELECT count(*) FROM oauth_identities), (SELECT count(*) FROM oauth_identity_audit_log)`).Row().Scan(&after.WidgetVersions, &after.WidgetAudit, &after.WidgetVersionAudit, &after.StockMovements, &after.OAuthIdentities, &after.OAuthAudit); err != nil {
		t.Fatal(err)
	}
	if after.WidgetVersions != before.WidgetVersions || after.WidgetAudit != before.WidgetAudit || after.WidgetVersionAudit != before.WidgetVersionAudit || after.StockMovements != before.StockMovements || after.OAuthIdentities != before.OAuthIdentities || after.OAuthAudit != before.OAuthAudit {
		t.Errorf("restored dataset counts = %+v, want exact pre-backup counts %+v", after, before)
	}
	if err := h.db.Raw(`SELECT COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY id)::text FROM widget_audit_log t),'[]'), COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY id)::text FROM widget_version_audit_log t),'[]'), COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY id)::text FROM oauth_identity_audit_log t),'[]')`).Row().Scan(&after.WidgetAuditRows, &after.WidgetVersionAuditRows, &after.OAuthAuditRows); err != nil {
		t.Fatal(err)
	}
	if after.WidgetAuditRows != before.WidgetAuditRows || after.WidgetVersionAuditRows != before.WidgetVersionAuditRows || after.OAuthAuditRows != before.OAuthAuditRows {
		t.Errorf("audit snapshots changed across restore:\nwidget=%s\nwant=%s\nwidget_version=%s\nwant=%s\noauth=%s\nwant=%s", after.WidgetAuditRows, before.WidgetAuditRows, after.WidgetVersionAuditRows, before.WidgetVersionAuditRows, after.OAuthAuditRows, before.OAuthAuditRows)
	}
	var gotVersion core.WidgetVersion
	if err := h.db.Where("widget_id = ? AND version = ?", widget.ID, 1).Take(&gotVersion).Error; err != nil {
		t.Fatalf("published widget version missing after restore: %v", err)
	}
	if !gotVersion.Published || gotVersion.Source != version.Source || gotVersion.Params != version.Params {
		t.Errorf("published version changed across backup restore: %+v", gotVersion)
	}
	if err := h.db.Exec(`UPDATE widget_versions SET source = 'tampered' WHERE id = ?`, gotVersion.ID).Error; err == nil {
		t.Error("published widget version UPDATE succeeded after restore; immutable trigger was not restored")
	}
	if err := h.db.Exec(`DELETE FROM widget_versions WHERE id = ?`, gotVersion.ID).Error; err == nil {
		t.Error("published widget version DELETE succeeded after restore; immutable trigger was not restored")
	}
	var gotMovement core.StockMovement
	if err := h.db.Where("id = ?", movement.ID).Take(&gotMovement).Error; err != nil || gotMovement != movement {
		t.Errorf("stock movement after restore = %+v, err %v; want %+v", gotMovement, err, movement)
	}
	if err := h.db.Exec(`UPDATE stock_movements SET delta = 999 WHERE id = ?`, movement.ID).Error; err == nil {
		t.Error("stock movement UPDATE succeeded after restore; append-only trigger was not restored")
	}
	if err := h.db.Exec(`DELETE FROM stock_movements WHERE id = ?`, movement.ID).Error; err == nil {
		t.Error("stock movement DELETE succeeded after restore; append-only trigger was not restored")
	}
	var gotIdentity core.OAuthIdentity
	if err := h.db.Where("id = ?", identity.ID).Take(&gotIdentity).Error; err != nil || gotIdentity.Subject != identity.Subject {
		t.Errorf("OAuth identity after restore = %+v, err %v", gotIdentity, err)
	}
}
