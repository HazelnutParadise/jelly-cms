package server

// Regression tests for the PR #53 review fixes.
//
// TestPluginHotUpgradeKeepsOldWorkerPermissionsUntilSwap is the contract that a
// running plugin's old worker keeps its approved permissions until the new
// worker is swapped in. The current code records the new permission approval in
// the plugins row before the reload finishes, so actions served by the old
// worker answer "plugin permission approval is stale" during the swap window.
//
// TestPluginEnableLoadsStoppedPlugin documents the enable path the plugins page
// must use for a row that says enabled but has no runtime entry: reload answers
// 404 for a stopped plugin, enable loads it.
//
// These tests are the contract; implementers must not edit this file. Every test
// needs JELLY_TEST_POSTGRES=1 and the dedicated PostgreSQL container. All waits
// poll with a bounded loop; a fixed sleep is never followed directly by an
// assertion.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
)

// hotUpgradeAsyncUpload builds the multipart request on the test goroutine and
// sends it from a new goroutine, so the test can act on the old worker while the
// upload is in flight. The request helper never calls t.Fatalf, so it is safe to
// run off the test goroutine.
func hotUpgradeAsyncUpload(t *testing.T, h *accessHarness, token string, archive []byte, values map[string]string) <-chan accessResult {
	t.Helper()

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", "plugin.zip")
	if err != nil {
		t.Fatalf("create plugin upload field: %v", err)
	}
	if _, err := part.Write(archive); err != nil {
		t.Fatalf("write plugin archive: %v", err)
	}
	for key, value := range values {
		if err := w.WriteField(key, value); err != nil {
			t.Fatalf("write plugin form field %s: %v", key, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart plugin upload: %v", err)
	}
	contentType := w.FormDataContentType()

	done := make(chan accessResult, 1)
	go func() {
		done <- h.requestReader(http.MethodPost, "/api/admin/plugins/upload", &body, token, "application/json", contentType)
	}()

	return done
}

// hotUpgradeWaitRowVersion polls the plugins row until it reports want. During a
// hot upgrade the row is written before the new worker is swapped in, so this
// marks the start of the window the test must exercise.
func hotUpgradeWaitRowVersion(t *testing.T, h *accessHarness, id, want string, budget time.Duration) {
	t.Helper()

	deadline := time.Now().Add(budget)
	var version string
	for {
		_, version, _ = hotUpgradeRow(t, h, id)
		if version == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("plugin row version for %q = %q, want %q within %s", id, version, want, budget)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestPluginHotUpgradeKeepsOldWorkerPermissionsUntilSwap covers the review fix
// that the old worker keeps working while a version with extra permissions is
// being swapped in.
func TestPluginHotUpgradeKeepsOldWorkerPermissionsUntilSwap(t *testing.T) {
	h := newPluginReloadHarness(t)
	admin := h.user("admin", "huf-permissions-admin")
	token := h.token(admin)
	const id = "huf-old-worker-permissions"
	run := nextPluginReloadRun()
	greeting := "hello-" + run
	readMarker := "HUF-" + run + "-read-v1:" + greeting

	settings := []map[string]any{{"key": "greeting", "type": "string", "default": greeting}}
	v1 := hotUpgradeArchive(t, hotUpgradeManifest(id, "1.0.0", []string{"settings"}, []string{"read"}, settings), fmt.Sprintf(`
JellyCMS.registerHook("OnBoot", function () {
  JellyCMS.log.info("HUF-%s-boot-v1");
});
JellyCMS.registerHook("action:read", function () {
  try {
    var value = JellyCMS.settings.get("greeting");
    JellyCMS.log.info("HUF-%s-read-v1:" + value);
  } catch (error) {
    JellyCMS.log.error("HUF-%s-read-v1-error:" + error);
  }
});
`, run, run, run))
	if r := hotUpgradeInstallPreview(t, h.accessHarness, token, v1); r.code != http.StatusOK {
		t.Fatalf("install v1 = %d, want 200; body=%q", r.code, r.body)
	}
	hotUpgradeEnable(t, h, token, id, "HUF-"+run+"-boot-v1")

	if r := supervisionAction(h.accessHarness, token, id, "read"); r.code != http.StatusOK {
		t.Fatalf("v1 read action = %d, want 200; body=%q", r.code, r.body)
	}
	h.logs.waitFor(t, readMarker)
	readsBeforeUpgrade := strings.Count(h.logs.snapshot(), readMarker)

	v2 := hotUpgradeArchive(t, hotUpgradeManifest(id, "2.0.0", []string{"settings", "store"}, []string{"read"}, settings), fmt.Sprintf(`
var until = Date.now() + 700;
while (Date.now() < until) {}
JellyCMS.registerHook("OnBoot", function () {
  JellyCMS.log.info("HUF-%s-boot-v2");
});
JellyCMS.registerHook("action:read", function () {
  var value = JellyCMS.settings.get("greeting");
  JellyCMS.log.info("HUF-%s-read-v2:" + value);
});
`, run, run))
	preview := hotUpgradePreviewProject(t, h.accessHarness, token, v2)
	uploadDone := hotUpgradeAsyncUpload(t, h.accessHarness, token, v2, map[string]string{
		"accept_permissions": preview.PermissionsHash,
		"sha256":             preview.SHA256,
	})

	time.Sleep(200 * time.Millisecond)
	hotUpgradeWaitRowVersion(t, h.accessHarness, id, "2.0.0", 3*time.Second)

	var (
		upload              accessResult
		uploadFinished      bool
		deliveredBeforeSwap bool
	)
	uploadDeadline := time.Now().Add(10 * time.Second)
	for !uploadFinished {
		if r := supervisionAction(h.accessHarness, token, id, "read"); r.code != http.StatusOK {
			// A failure here is the bug on the current code; the log assertions
			// below report the stale approval that caused it.
		}
		reads := strings.Count(h.logs.snapshot(), readMarker)
		select {
		case upload = <-uploadDone:
			uploadFinished = true
		default:
		}
		if !uploadFinished && reads > readsBeforeUpgrade {
			deliveredBeforeSwap = true
		}
		if time.Now().After(uploadDeadline) {
			t.Fatalf("hot upgrade upload did not return within %s; plugin logs:\n%s", 10*time.Second, h.logs.snapshot())
		}
		if !uploadFinished {
			time.Sleep(20 * time.Millisecond)
		}
	}

	if upload.panicked != nil {
		t.Fatalf("hot upgrade upload panicked: %v", upload.panicked)
	}
	if upload.code != http.StatusOK {
		t.Fatalf("hot upgrade upload = %d, want 200; body=%q", upload.code, upload.body)
	}
	if logs := h.logs.snapshot(); strings.Contains(logs, "approval is stale") {
		t.Fatalf("plugin logs contain %q after the hot upgrade, want none; logs:\n%s", "approval is stale", logs)
	}
	if !deliveredBeforeSwap {
		t.Fatalf(
			"old worker actions logged the setting value %q %d times before the upload returned, want at least %d; logs:\n%s",
			readMarker, readsBeforeUpgrade, readsBeforeUpgrade+1, h.logs.snapshot(),
		)
	}

	h.logs.waitFor(t, "HUF-"+run+"-boot-v2")
	supervisionWaitState(t, h, id, plugin.StateRunning, supervisionStateBudget)
	if logs := h.logs.snapshot(); strings.Contains(logs, "approval is stale") {
		t.Fatalf("plugin logs contain %q after the new worker booted, want none; logs:\n%s", "approval is stale", logs)
	}
}

// TestPluginEnableLoadsStoppedPlugin covers the review fix that the plugins page
// must enable a row that says enabled but has no runtime entry instead of
// reloading it.
func TestPluginEnableLoadsStoppedPlugin(t *testing.T) {
	h := newPluginReloadHarness(t)
	admin := h.user("admin", "plg-enabled-stopped-admin")
	token := h.token(admin)
	const id = "plg-enabled-stopped"
	marker := "PLG-" + nextPluginReloadRun()

	plgSeed(t, h.accessHarness, id, fmt.Sprintf(`{"id":%q,"name":%q,"version":"1.0.0","entrypoint":"index.js"}`, id, id), true)
	hostWriteEntry(t, id, fmt.Sprintf(`JellyCMS.registerHook("OnBoot", function () { JellyCMS.log.info(%q); });`, marker))

	listState := func() map[string]any {
		t.Helper()
		list := updCall(h.accessHarness, http.MethodGet, "/api/admin/plugins", "", token)
		if list.code != http.StatusOK {
			t.Fatalf("GET /api/admin/plugins = %d, want 200; body=%q", list.code, list.body)
		}
		var items []map[string]any
		if err := json.Unmarshal([]byte(list.body), &items); err != nil {
			t.Fatalf("admin plugin list is not JSON: %v; body=%q", err, list.body)
		}
		for _, item := range items {
			if item["id"] == id {
				return item
			}
		}
		t.Fatalf("admin plugin list has no item for %q; items=%v", id, items)
		return nil
	}

	stopped := listState()
	if state, _ := stopped["state"].(string); state != "stopped" {
		t.Fatalf("list state for an enabled but unloaded plugin = %q, want %q; item=%v", state, "stopped", stopped)
	}
	if enabled, _ := stopped["enabled"].(bool); !enabled {
		t.Fatalf("list enabled for an enabled but unloaded plugin = %t, want true; item=%v", enabled, stopped)
	}

	if r := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+id+"/reload", "", token); r.code != http.StatusNotFound {
		t.Fatalf("reload a stopped plugin = %d, want 404; body=%q", r.code, r.body)
	}

	if r := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+id+"/enable", "", token); r.code != http.StatusOK {
		t.Fatalf("enable a stopped plugin = %d, want 200; body=%q", r.code, r.body)
	}
	supervisionWaitState(t, h, id, plugin.StateRunning, supervisionStateBudget)
	h.logs.waitFor(t, marker)

	running := listState()
	if state, _ := running["state"].(string); state != plugin.StateRunning {
		t.Fatalf("list state after enabling a stopped plugin = %q, want %q; item=%v", state, plugin.StateRunning, running)
	}
}
