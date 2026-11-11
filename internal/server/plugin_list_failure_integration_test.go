package server

// Contract for the PR #53 review fix. When the plugins table cannot be read,
// the plugin lists must fail instead of reporting the plugins on disk as
// enabled and unmanaged with a 200. GET /api/admin/plugins answers 500 with the
// admin JSON error object (an "error" field). GET /api/v1/plugins answers 500
// with the /api/v1 error envelope (code internal_error, message, details,
// correlation_id). Neither response may contain a plugin item or SQL detail.
//
// This file is the PR #53 review contract. Implementers must not edit this
// file; make integration_shared_handlers.go and integration_list.go satisfy it.
//
// Needs JELLY_TEST_POSTGRES=1 and the dedicated PostgreSQL container.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// pluginListFailureNoLeak fails when a failed list response exposes the plugin
// id, the renamed table or SQL error text.
func pluginListFailureNoLeak(t *testing.T, where, body, id string) {
	t.Helper()

	for _, leak := range []string{id, "plugins_broken", "SQLSTATE", "does not exist", "ERROR:"} {
		if strings.Contains(body, leak) {
			t.Errorf("%s leaks %q in the failure body; want no plugin item or SQL detail; body=%q", where, leak, body)
		}
	}
}

func TestPluginListFailureReturns500OnBothEndpoints(t *testing.T) {
	h := newPluginReloadHarness(t)
	_, token, reader := agtSetup(t, h.accessHarness, "Plugin List Failure Reader", "plugins.read")

	const id = "plg-list-fail"
	plgSeed(t, h.accessHarness, id, fmt.Sprintf(`{"id":%q,"name":"List Failure","version":"1.0.0","entrypoint":"index.js"}`, id), false)
	hostWriteEntry(t, id, `JellyCMS.registerHook("OnBoot", function () {});`)

	// Healthy baseline: the disabled plugin is listed on both endpoints.
	baseline := updCall(h.accessHarness, http.MethodGet, "/api/admin/plugins", "", token)
	if baseline.code != http.StatusOK {
		t.Fatalf("baseline GET /api/admin/plugins = %d, want 200; body=%q", baseline.code, baseline.body)
	}
	var baselineItems []map[string]any
	if err := json.Unmarshal([]byte(baseline.body), &baselineItems); err != nil {
		t.Fatalf("baseline GET /api/admin/plugins is not a JSON array: %v; body=%q", err, baseline.body)
	}
	foundBaseline := false
	for _, item := range baselineItems {
		if item["id"] != id {
			continue
		}
		foundBaseline = true
		if item["state"] != "disabled" || item["enabled"] != false {
			t.Fatalf("baseline admin item %s = state:%v enabled:%v, want state:\"disabled\" enabled:false; item=%v", id, item["state"], item["enabled"], item)
		}
	}
	if !foundBaseline {
		t.Fatalf("baseline GET /api/admin/plugins has no item %s; body=%q", id, baseline.body)
	}
	v1Baseline := agtDo(h.accessHarness, http.MethodGet, "/api/v1/plugins", "", reader.APIKey, "")
	if v1Baseline.panicked != nil || v1Baseline.code != http.StatusOK {
		t.Fatalf("baseline GET /api/v1/plugins = %d (panicked=%v), want 200; body=%q", v1Baseline.code, v1Baseline.panicked, v1Baseline.body)
	}

	// Make the plugins query fail for this test's database only. The cleanup
	// runs before the harness drops the database and restores the name.
	if err := h.db.Exec(`ALTER TABLE plugins RENAME TO plugins_broken`).Error; err != nil {
		t.Fatalf("rename plugins table to plugins_broken: %v", err)
	}
	t.Cleanup(func() {
		if err := h.db.Exec(`ALTER TABLE plugins_broken RENAME TO plugins`).Error; err != nil {
			t.Errorf("restore plugins table name: %v", err)
		}
	})

	adminFail := updCall(h.accessHarness, http.MethodGet, "/api/admin/plugins", "", token)
	if adminFail.code != http.StatusInternalServerError {
		t.Errorf("GET /api/admin/plugins with the plugins table unreadable = %d, want 500; body=%q", adminFail.code, adminFail.body)
	}
	var adminErr map[string]any
	if err := json.Unmarshal([]byte(adminFail.body), &adminErr); err != nil {
		t.Errorf("GET /api/admin/plugins failure body is not a JSON object: %v; body=%q", err, adminFail.body)
	} else if _, ok := adminErr["error"]; !ok {
		t.Errorf("GET /api/admin/plugins failure body has no \"error\" field; body=%q", adminFail.body)
	}
	pluginListFailureNoLeak(t, "GET /api/admin/plugins", adminFail.body, id)

	v1Fail := agtDo(h.accessHarness, http.MethodGet, "/api/v1/plugins", "", reader.APIKey, "")
	agtExpectError(t, v1Fail, http.StatusInternalServerError, "internal_error", "GET /api/v1/plugins with the plugins table unreadable")
	var v1Body map[string]any
	if err := json.Unmarshal([]byte(v1Fail.body), &v1Body); err == nil {
		if _, ok := v1Body["items"]; ok {
			t.Errorf("GET /api/v1/plugins failure body contains an items field; want no plugin items; body=%q", v1Fail.body)
		}
	}
	pluginListFailureNoLeak(t, "GET /api/v1/plugins", v1Fail.body, id)
}
