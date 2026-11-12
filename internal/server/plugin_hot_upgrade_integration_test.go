package server

// Acceptance tests for ticket 36 R3 (ticket 36):
// upgrading a loaded plugin in place through the real upload endpoints. A
// running plugin is not refused, the new version boots with its new permission
// approval already recorded, a failing new version keeps both the old files and
// the old row, and a disabled plugin keeps the pre-existing upload behavior.
//
// This file is the ticket-36 contract and is written before the implementation.
// Implementers must not edit this file; make store_api.go and handlers_admin.go
// satisfy it instead.
//
// Every test needs JELLY_TEST_POSTGRES=1 and the dedicated PostgreSQL container.
// All waits poll with a bounded loop; a fixed sleep is never followed directly
// by an assertion.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
)

// hotUpgradeArchive builds an in-memory plugin archive from a manifest map and
// the main.js entry script.
func hotUpgradeArchive(t *testing.T, manifest map[string]any, mainJS string) []byte {
	t.Helper()

	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal plugin manifest: %v", err)
	}
	return secZip(t, secZipEntry{name: "plugin.json", body: raw}, secZipEntry{name: "main.js", body: []byte(mainJS)})
}

// hotUpgradeManifest returns a manifest with the fields these fixtures need.
func hotUpgradeManifest(id, version string, permissions, actions []string, settings []map[string]any) map[string]any {
	manifest := map[string]any{"id": id, "name": id, "version": version, "entrypoint": "main.js"}
	if len(permissions) > 0 {
		items := make([]map[string]string, 0, len(permissions))
		for _, name := range permissions {
			items = append(items, map[string]string{"name": name})
		}
		manifest["permissions"] = items
	}
	if len(actions) > 0 {
		items := make([]map[string]string, 0, len(actions))
		for _, action := range actions {
			items = append(items, map[string]string{"id": action})
		}
		manifest["actions"] = items
	}
	if len(settings) > 0 {
		manifest["settings"] = settings
	}
	return manifest
}

type hotUpgradePreview struct {
	ID              string   `json:"id"`
	Version         string   `json:"version"`
	Permissions     []string `json:"permissions"`
	PermissionsHash string   `json:"permissions_hash"`
	SHA256          string   `json:"sha256"`
}

// hotUpgradePreviewProject runs the dry upload the admin page uses before it
// accepts permissions.
func hotUpgradePreviewProject(t *testing.T, h *accessHarness, token string, archive []byte) hotUpgradePreview {
	t.Helper()

	r := pprUpload(h, token, "file", "/api/admin/plugins/upload?dry=1", "plugin.zip", archive, nil)
	if r.code != http.StatusOK {
		t.Fatalf("upload preview = %d, want 200; body=%q", r.code, r.body)
	}
	var preview hotUpgradePreview
	if err := json.Unmarshal([]byte(r.body), &preview); err != nil {
		t.Fatalf("upload preview is not JSON: %v; body=%q", err, r.body)
	}
	return preview
}

// hotUpgradeInstallPreview previews the exact archive and installs it with the
// approval fields the admin page sends.
func hotUpgradeInstallPreview(t *testing.T, h *accessHarness, token string, archive []byte) accessResult {
	t.Helper()

	preview := hotUpgradePreviewProject(t, h, token, archive)
	return pprUpload(h, token, "file", "/api/admin/plugins/upload", "plugin.zip", archive, map[string]string{
		"accept_permissions": preview.PermissionsHash,
		"sha256":             preview.SHA256,
	})
}

// hotUpgradeRow reads the columns a hot upgrade must update.
func hotUpgradeRow(t *testing.T, h *accessHarness, id string) (enabled bool, version, permissionsHash string) {
	t.Helper()

	var rows []struct {
		Enabled         bool   `gorm:"column:enabled"`
		Version         string `gorm:"column:version"`
		PermissionsHash string `gorm:"column:permissions_granted_hash"`
	}
	if err := h.db.Raw(`SELECT enabled, version, permissions_granted_hash FROM plugins WHERE id = ?`, id).Scan(&rows).Error; err != nil {
		t.Fatalf("read plugin row %s: %v", id, err)
	}
	if len(rows) != 1 {
		t.Fatalf("plugin rows for %s = %d, want 1", id, len(rows))
	}
	return rows[0].Enabled, rows[0].Version, rows[0].PermissionsHash
}

// hotUpgradeTreeDigest hashes the installed files of one plugin.
func hotUpgradeTreeDigest(t *testing.T, id string) string {
	t.Helper()

	digest, err := pluginTreeDigest(filepath.Join(pluginDirRoot, id))
	if err != nil {
		t.Fatalf("hash plugin tree %s: %v", id, err)
	}
	return digest
}

// hotUpgradeInstall enables an installed plugin and waits for its first boot.
func hotUpgradeEnable(t *testing.T, h *reloadHarness, token, id, bootMarker string) {
	t.Helper()

	if r := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+id+"/enable", "", token); r.code != http.StatusOK {
		t.Fatalf("enable %s = %d, want 200; body=%q", id, r.code, r.body)
	}
	supervisionWaitState(t, h, id, plugin.StateRunning, supervisionStateBudget)
	h.logs.waitFor(t, bootMarker)
}

func TestPluginHotUpgradeRunningPluginKeepsRunning(t *testing.T) {
	h := newPluginReloadHarness(t)
	admin := h.user("admin", "hup-running-admin")
	token := h.token(admin)
	const id = "hup-running"
	run := nextPluginReloadRun()

	v1 := hotUpgradeArchive(t, hotUpgradeManifest(id, "1.0.0", nil, []string{"ping"}, nil), fmt.Sprintf(`
JellyCMS.registerHook("OnBoot", function () {
  JellyCMS.log.info("HUP-%s-boot-v1");
});
JellyCMS.registerHook("OnShutdown", function () {
  JellyCMS.log.info("HUP-%s-shutdown-v1");
});
JellyCMS.registerHook("action:ping", function () {
  JellyCMS.log.info("HUP-%s-ping-v1");
});
`, run, run, run))
	if r := hotUpgradeInstallPreview(t, h.accessHarness, token, v1); r.code != http.StatusOK {
		t.Fatalf("install v1 = %d, want 200; body=%q", r.code, r.body)
	}
	hotUpgradeEnable(t, h, token, id, "HUP-"+run+"-boot-v1")

	if r := supervisionAction(h.accessHarness, token, id, "ping"); r.code != http.StatusOK {
		t.Fatalf("v1 ping = %d, want 200; body=%q", r.code, r.body)
	}
	h.logs.waitFor(t, "HUP-"+run+"-ping-v1")

	v2 := hotUpgradeArchive(t, hotUpgradeManifest(id, "2.0.0", nil, []string{"ping"}, nil), fmt.Sprintf(`
JellyCMS.registerHook("OnBoot", function () {
  JellyCMS.log.info("HUP-%s-boot-v2");
});
JellyCMS.registerHook("OnShutdown", function () {
  JellyCMS.log.info("HUP-%s-shutdown-v2");
});
JellyCMS.registerHook("action:ping", function () {
  JellyCMS.log.info("HUP-%s-ping-v2");
});
`, run, run, run))
	preview := hotUpgradePreviewProject(t, h.accessHarness, token, v2)
	if preview.ID != id || preview.Version != "2.0.0" {
		t.Fatalf("v2 preview = id:%q version:%q, want id:%q version:2.0.0", preview.ID, preview.Version, id)
	}
	if r := hotUpgradeInstallPreview(t, h.accessHarness, token, v2); r.code != http.StatusOK {
		t.Fatalf("hot upgrade while running = %d, want 200; body=%q (the old behavior refused running plugins)", r.code, r.body)
	}

	if !h.runtime.IsLoaded(id) {
		t.Errorf("plugin %s is not loaded after a hot upgrade, want it to stay loaded", id)
	}
	status, ok := h.runtime.Status(id)
	if !ok || status.State != plugin.StateRunning {
		t.Errorf("Status(%q) after a hot upgrade = %+v (ok=%t), want running", id, status, ok)
	}
	if enabled, version, _ := hotUpgradeRow(t, h.accessHarness, id); !enabled || version != "2.0.0" {
		t.Errorf("plugin row after a hot upgrade = enabled:%t version:%q, want enabled:true version:2.0.0", enabled, version)
	}

	h.logs.waitFor(t, "HUP-"+run+"-shutdown-v1")
	h.logs.waitFor(t, "HUP-"+run+"-boot-v2")
	if r := supervisionAction(h.accessHarness, token, id, "ping"); r.code != http.StatusOK {
		t.Fatalf("v2 ping = %d, want 200; body=%q", r.code, r.body)
	}
	h.logs.waitFor(t, "HUP-"+run+"-ping-v2")
}

func TestPluginHotUpgradeApprovesNewPermissionsBeforeBoot(t *testing.T) {
	h := newPluginReloadHarness(t)
	admin := h.user("admin", "hup-permissions-admin")
	token := h.token(admin)
	const id = "hup-permissions"
	run := nextPluginReloadRun()

	settings := []map[string]any{{"key": "limit", "type": "int", "default": 3, "min": 1, "max": 10}}
	v1Script := fmt.Sprintf(`
JellyCMS.registerHook("OnBoot", function () {
  JellyCMS.log.info("HUP-%s-boot-v1:" + JellyCMS.config.get("limit"));
});
`, run)
	v1 := hotUpgradeArchive(t, hotUpgradeManifest(id, "1.0.0", []string{"settings"}, nil, settings), v1Script)
	if r := hotUpgradeInstallPreview(t, h.accessHarness, token, v1); r.code != http.StatusOK {
		t.Fatalf("install v1 = %d, want 200; body=%q", r.code, r.body)
	}
	setGrantedPermissions(t, h.accessHarness, id)
	hotUpgradeEnable(t, h, token, id, "HUP-"+run+"-boot-v1:3")

	if r := updCall(h.accessHarness, http.MethodPut, "/api/admin/plugins/"+id+"/settings", `{"values":{"limit":7}}`, token); r.code != http.StatusOK {
		t.Fatalf("save settings before upgrade = %d, want 200; body=%q", r.code, r.body)
	}

	v2Script := fmt.Sprintf(`
JellyCMS.registerHook("OnBoot", function () {
  JellyCMS.log.info("HUP-%s-boot-v2:" + JellyCMS.config.get("limit"));
});
`, run)
	v2 := hotUpgradeArchive(t, hotUpgradeManifest(id, "2.0.0", []string{"http.fetch", "settings"}, nil, settings), v2Script)
	if r := hotUpgradeInstallPreview(t, h.accessHarness, token, v2); r.code != http.StatusOK {
		t.Fatalf("hot upgrade with a new permission = %d, want 200; body=%q", r.code, r.body)
	}

	enabled, version, granted := hotUpgradeRow(t, h.accessHarness, id)
	if !enabled || version != "2.0.0" {
		t.Errorf("plugin row after the permission upgrade = enabled:%t version:%q, want enabled:true version:2.0.0", enabled, version)
	}
	wantHash := pprPermissionHash("http.fetch", "settings")
	if granted != wantHash {
		t.Errorf("permissions_granted_hash after the permission upgrade = %q, want %q (the new permission was approved before OnBoot)", granted, wantHash)
	}
	h.logs.waitFor(t, "HUP-"+run+"-boot-v2:7")
	status, ok := h.runtime.Status(id)
	if !ok || status.State != plugin.StateRunning {
		t.Errorf("Status(%q) after the permission upgrade = %+v (ok=%t), want running", id, status, ok)
	}
}

func TestPluginHotUpgradeFailureKeepsOldVersionAndFiles(t *testing.T) {
	h := newPluginReloadHarness(t)
	admin := h.user("admin", "hup-failure-admin")
	token := h.token(admin)
	const id = "hup-failure"
	run := nextPluginReloadRun()

	v1 := hotUpgradeArchive(t, hotUpgradeManifest(id, "1.0.0", nil, []string{"ping"}, nil), fmt.Sprintf(`
JellyCMS.registerHook("OnBoot", function () {
  JellyCMS.log.info("HUP-%s-boot-v1");
});
JellyCMS.registerHook("action:ping", function () {
  JellyCMS.log.info("HUP-%s-ping-v1");
});
`, run, run))
	if r := hotUpgradeInstallPreview(t, h.accessHarness, token, v1); r.code != http.StatusOK {
		t.Fatalf("install v1 = %d, want 200; body=%q", r.code, r.body)
	}
	hotUpgradeEnable(t, h, token, id, "HUP-"+run+"-boot-v1")

	beforeTree := hotUpgradeTreeDigest(t, id)
	enabledBefore, versionBefore, _ := hotUpgradeRow(t, h.accessHarness, id)

	v2 := hotUpgradeArchive(t, hotUpgradeManifest(id, "2.0.0", nil, []string{"ping"}, nil), fmt.Sprintf(`
JellyCMS.log.info("HUP-%s-entry-v2");
JellyCMS.registerHook("OnBoot", function () {
  throw new Error("HUP-%s-boot-failure");
});
JellyCMS.registerHook("action:ping", function () {
  JellyCMS.log.info("HUP-%s-ping-v2");
});
`, run, run, run))
	failed := hotUpgradeInstallPreview(t, h.accessHarness, token, v2)
	if failed.code != http.StatusConflict {
		t.Fatalf("hot upgrade with a failing OnBoot = %d, want 409; body=%q", failed.code, failed.body)
	}
	if strings.Contains(failed.body, "disable it before uploading a new version") {
		t.Errorf("hot upgrade with a failing OnBoot was refused as a running plugin instead of attempted: body=%q", failed.body)
	}
	if !strings.Contains(failed.body, "HUP-"+run+"-boot-failure") {
		t.Errorf("hot upgrade failure body = %q, want the last plugin log error containing %q", failed.body, "HUP-"+run+"-boot-failure")
	}
	if afterTree := hotUpgradeTreeDigest(t, id); afterTree != beforeTree {
		t.Errorf("plugin tree digest after a failed hot upgrade = %q, want the pre-upgrade digest %q", afterTree, beforeTree)
	}
	enabledAfter, versionAfter, _ := hotUpgradeRow(t, h.accessHarness, id)
	if enabledAfter != enabledBefore || versionAfter != versionBefore {
		t.Errorf("plugin row after a failed hot upgrade = enabled:%t version:%q, want enabled:%t version:%q", enabledAfter, versionAfter, enabledBefore, versionBefore)
	}
	if !h.runtime.IsLoaded(id) {
		t.Errorf("plugin %s is not loaded after a failed hot upgrade, want the old version to keep running", id)
	}
	status, ok := h.runtime.Status(id)
	if !ok || status.State != plugin.StateRunning {
		t.Errorf("Status(%q) after a failed hot upgrade = %+v (ok=%t), want running", id, status, ok)
	}
	if r := supervisionAction(h.accessHarness, token, id, "ping"); r.code != http.StatusOK {
		t.Fatalf("v1 ping after a failed hot upgrade = %d, want 200; body=%q", r.code, r.body)
	}
	h.logs.waitFor(t, "HUP-"+run+"-ping-v1")
}

func TestPluginHotUpgradeDisabledPluginKeepsExistingBehavior(t *testing.T) {
	h := newPluginReloadHarness(t)
	admin := h.user("admin", "hup-disabled-admin")
	token := h.token(admin)
	const id = "hup-disabled"

	v1 := hotUpgradeArchive(t, hotUpgradeManifest(id, "1.0.0", nil, nil, nil), `JellyCMS.registerHook("OnBoot", function () {});`)
	if r := hotUpgradeInstallPreview(t, h.accessHarness, token, v1); r.code != http.StatusOK {
		t.Fatalf("install v1 = %d, want 200; body=%q", r.code, r.body)
	}
	if enabled, version, _ := hotUpgradeRow(t, h.accessHarness, id); enabled || version != "1.0.0" {
		t.Fatalf("newly installed plugin row = enabled:%t version:%q, want enabled:false version:1.0.0 (installs start disabled)", enabled, version)
	}
	if h.runtime.IsLoaded(id) {
		t.Fatalf("newly installed plugin %s is loaded, want it disabled", id)
	}

	v2 := hotUpgradeArchive(t, hotUpgradeManifest(id, "2.0.0", nil, nil, nil), `JellyCMS.registerHook("OnBoot", function () {});`)
	if r := hotUpgradeInstallPreview(t, h.accessHarness, token, v2); r.code != http.StatusOK {
		t.Fatalf("upload a new version of a disabled plugin = %d, want 200; body=%q", r.code, r.body)
	}
	if enabled, version, _ := hotUpgradeRow(t, h.accessHarness, id); enabled || version != "2.0.0" {
		t.Errorf("disabled plugin row after upload = enabled:%t version:%q, want enabled:false version:2.0.0", enabled, version)
	}
	if h.runtime.IsLoaded(id) {
		t.Errorf("disabled plugin %s was loaded by an upload, want it to stay disabled", id)
	}
}
