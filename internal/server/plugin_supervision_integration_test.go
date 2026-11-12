package server

// Acceptance tests for ticket 36 R2 and R4
// (docs/tickets/36-plugin-usability.md): the server wiring around automatic
// restart, enable/reload during a restart, the system.plugin_failed event, and
// the enabled/managed/state/last_error status fields on the admin and /api/v1
// plugin lists plus the detail endpoint.
//
// This file is the ticket-36 contract and is written before the implementation.
// Implementers must not edit this file; make plugin_supervision.go,
// plugin_detail_api.go, integration_shared_handlers.go and
// integrations_discovery.go satisfy it instead.
//
// Every test needs JELLY_TEST_POSTGRES=1 and the dedicated PostgreSQL container.
// All waits poll with a bounded loop; a fixed sleep is never followed directly
// by an assertion.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/notify"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
)

const (
	// supervisionStateBudget bounds one automatic restart; a restart starts a
	// real worker subprocess, so it stays generous enough for -race runs.
	supervisionStateBudget = 20 * time.Second
	// supervisionObservation keeps a crashed plugin visible long enough to
	// prove the supervisor did not schedule another restart.
	supervisionObservation = 1500 * time.Millisecond
)

// supervisionSetPolicy replaces the restart policy for one test and restores
// the previous values after it.
func supervisionSetPolicy(t *testing.T, backoff []time.Duration, threshold int, window time.Duration) {
	t.Helper()
	restore := plugin.SetRestartPolicy(backoff, threshold, window)
	t.Cleanup(restore)
}

// supervisionWaitState polls Runtime.Status until the plugin reaches want.
func supervisionWaitState(t *testing.T, h *reloadHarness, id, want string, budget time.Duration) plugin.PluginStatus {
	t.Helper()

	deadline := time.Now().Add(budget)
	var (
		status plugin.PluginStatus
		ok     bool
	)
	for {
		status, ok = h.runtime.Status(id)
		if ok && status.State == want {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf("Status(%q) = %+v (ok=%t), want State=%q within %s; LastError=%q", id, status, ok, want, budget, status.LastError)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// supervisionWaitCondition polls until check succeeds or the budget ends.
func supervisionWaitCondition(t *testing.T, what string, budget time.Duration, check func() bool) {
	t.Helper()

	deadline := time.Now().Add(budget)
	for {
		if check() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen within %s", what, budget)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// supervisionSeed writes an installed, stopped plugin with a runaway action, a
// healthy action and an OnBoot log marker.
func supervisionSeed(t *testing.T, h *reloadHarness, id, marker string) {
	t.Helper()

	manifest := fmt.Sprintf(`{"id":%q,"name":%q,"version":"1.0.0","entrypoint":"index.js","actions":[{"id":"slow"},{"id":"ping"}]}`, id, id)
	plgSeed(t, h.accessHarness, id, manifest, false)
	hostWriteEntry(t, id, fmt.Sprintf(`
JellyCMS.registerHook("OnBoot", function () {
  JellyCMS.log.info(%q);
});
JellyCMS.registerHook("action:slow", function () {
  while (true) {}
});
JellyCMS.registerHook("action:ping", function () {
  JellyCMS.log.info(%q);
});
`, marker+"-boot", marker+"-ping"))
}

// supervisionLogCount counts marker occurrences in the captured plugin output.
func supervisionLogCount(h *reloadHarness, marker string) int {
	return strings.Count(h.logs.snapshot(), marker)
}

// supervisionAction calls one admin plugin action.
func supervisionAction(h *accessHarness, token, id, action string) accessResult {
	return updCall(h, http.MethodPost, "/api/admin/plugins/"+id+"/actions/"+action, "", token)
}

type supervisionActionReply struct {
	Dispatched bool   `json:"dispatched"`
	Reason     string `json:"reason"`
}

// supervisionDecodeAction decodes the action response.
func supervisionDecodeAction(t *testing.T, r accessResult) supervisionActionReply {
	t.Helper()

	var reply supervisionActionReply
	if err := json.Unmarshal([]byte(r.body), &reply); err != nil {
		t.Fatalf("action response is not JSON: %v; status=%d body=%q", err, r.code, r.body)
	}
	return reply
}

// supervisionPluginRow reads the state columns the status view depends on.
func supervisionPluginRow(t *testing.T, h *accessHarness, id string) (enabled bool, version string) {
	t.Helper()

	var rows []struct {
		Enabled bool   `gorm:"column:enabled"`
		Version string `gorm:"column:version"`
	}
	if err := h.db.Raw(`SELECT enabled, version FROM plugins WHERE id = ?`, id).Scan(&rows).Error; err != nil {
		t.Fatalf("read plugin row %s: %v", id, err)
	}
	if len(rows) != 1 {
		t.Fatalf("plugin rows for %s = %d, want 1", id, len(rows))
	}
	return rows[0].Enabled, rows[0].Version
}

// supervisionDetailFields fetches the admin detail view and returns its state
// fields. last_error, last_failure_at and next_restart_at are omitted when unset.
func supervisionDetailFields(t *testing.T, h *accessHarness, token, id string) map[string]any {
	t.Helper()

	r := updCall(h, http.MethodGet, "/api/admin/plugins/"+id, "", token)
	if r.code != http.StatusOK {
		t.Fatalf("GET plugin detail %s = %d, want 200; body=%q", id, r.code, r.body)
	}
	return plgDecode(t, r.body).(map[string]any)
}

func TestPluginSupervisionRestartingStateAndActionGate(t *testing.T) {
	supervisionSetPolicy(t, []time.Duration{5 * time.Second}, 5, 30*time.Minute)

	h := newPluginReloadHarness(t)
	admin := h.user("admin", "sup-restart-admin")
	token := h.token(admin)
	const id = "sup-restart"
	marker := "SUP-" + nextPluginReloadRun()
	supervisionSeed(t, h, id, marker)

	if r := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+id+"/enable", "", token); r.code != http.StatusOK {
		t.Fatalf("enable %s = %d, want 200; body=%q", id, r.code, r.body)
	}
	supervisionWaitState(t, h, id, plugin.StateRunning, supervisionStateBudget)
	h.logs.waitFor(t, marker+"-boot")

	if r := supervisionAction(h.accessHarness, token, id, "slow"); r.code < 500 || r.code > 599 {
		t.Fatalf("runaway action = %d, want a 5xx failure; body=%q", r.code, r.body)
	}

	restarting := supervisionWaitState(t, h, id, plugin.StateRestarting, 5*time.Second)
	if restarting.Faults != 1 {
		t.Errorf("Faults while restarting = %d, want 1; status=%+v", restarting.Faults, restarting)
	}
	if restarting.LastError == "" {
		t.Errorf("LastError while restarting is empty, want the timeout reason; status=%+v", restarting)
	}
	if restarting.NextRestartAt.IsZero() {
		t.Errorf("NextRestartAt while restarting is zero, want the scheduled restart time; status=%+v", restarting)
	}

	detail := supervisionDetailFields(t, h.accessHarness, token, id)
	if detail["state"] != plugin.StateRestarting {
		t.Errorf("detail state while restarting = %v, want %q; detail=%v", detail["state"], plugin.StateRestarting, detail)
	}
	if detail["loaded"] != true {
		t.Errorf("detail loaded while restarting = %v, want true; detail=%v", detail["loaded"], detail)
	}
	if faults, _ := detail["faults"].(float64); int(faults) != 1 {
		t.Errorf("detail faults while restarting = %v, want 1; detail=%v", detail["faults"], detail)
	}
	if errText, _ := detail["last_error"].(string); errText == "" {
		t.Errorf("detail last_error while restarting = %q, want the timeout reason; detail=%v", errText, detail)
	}
	if detail["last_failure_at"] == nil || detail["next_restart_at"] == nil {
		t.Errorf("detail timestamps while restarting = last_failure_at:%v next_restart_at:%v, want both present; detail=%v", detail["last_failure_at"], detail["next_restart_at"], detail)
	}

	waiting := supervisionAction(h.accessHarness, token, id, "ping")
	if waiting.code != http.StatusOK {
		t.Fatalf("action during restart wait = %d, want 200; body=%q", waiting.code, waiting.body)
	}
	gate := supervisionDecodeAction(t, waiting)
	if gate.Dispatched || gate.Reason != "plugin_restarting" {
		t.Errorf("action during restart wait = dispatched:%t reason:%q, want dispatched:false reason:plugin_restarting", gate.Dispatched, gate.Reason)
	}

	supervisionWaitState(t, h, id, plugin.StateRunning, supervisionStateBudget)
	recovered := supervisionAction(h.accessHarness, token, id, "ping")
	if recovered.code != http.StatusOK {
		t.Fatalf("action after restart = %d, want 200; body=%q", recovered.code, recovered.body)
	}
	if reply := supervisionDecodeAction(t, recovered); !reply.Dispatched {
		t.Errorf("action after restart = dispatched:%t, want true; body=%q", reply.Dispatched, recovered.body)
	}
	h.logs.waitFor(t, marker+"-ping")

	drainPluginLogPipelines(t)
	logs := updCall(h.accessHarness, http.MethodGet, "/api/admin/plugins/"+id+"/logs", "", token)
	if logs.code != http.StatusOK {
		t.Fatalf("GET plugin logs = %d, want 200; body=%q", logs.code, logs.body)
	}
	var rows []pluginLogView
	if err := json.Unmarshal([]byte(logs.body), &rows); err != nil {
		t.Fatalf("plugin logs are not JSON: %v; body=%q", err, logs.body)
	}
	found := false
	for _, row := range rows {
		if row.Level == 3 && (strings.Contains(strings.ToLower(row.Message), "restart") || strings.Contains(strings.ToLower(row.Message), "timeout")) {
			found = true

			break
		}
	}
	if !found {
		t.Errorf("plugin logs = %+v, want an error row containing the restart reason and next attempt", rows)
	}
}

func TestPluginSupervisionEnableDuringRestartReloadsOnce(t *testing.T) {
	supervisionSetPolicy(t, []time.Duration{10 * time.Second}, 5, 30*time.Minute)

	h := newPluginReloadHarness(t)
	admin := h.user("admin", "sup-enable-admin")
	token := h.token(admin)
	const id = "sup-enable"
	marker := "SUP-" + nextPluginReloadRun()
	supervisionSeed(t, h, id, marker)

	if r := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+id+"/enable", "", token); r.code != http.StatusOK {
		t.Fatalf("enable %s = %d, want 200; body=%q", id, r.code, r.body)
	}
	supervisionWaitState(t, h, id, plugin.StateRunning, supervisionStateBudget)
	h.logs.waitFor(t, marker+"-boot")
	if count := supervisionLogCount(h, marker+"-boot"); count != 1 {
		t.Fatalf("OnBoot count before failure = %d, want 1", count)
	}

	if r := supervisionAction(h.accessHarness, token, id, "slow"); r.code < 500 || r.code > 599 {
		t.Fatalf("runaway action = %d, want a 5xx failure; body=%q", r.code, r.body)
	}
	supervisionWaitState(t, h, id, plugin.StateRestarting, 5*time.Second)

	started := time.Now()
	if r := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+id+"/enable", "", token); r.code != http.StatusOK {
		t.Fatalf("enable during restart = %d, want 200; body=%q", r.code, r.body)
	}
	supervisionWaitState(t, h, id, plugin.StateRunning, 3*time.Second)
	if elapsed := time.Since(started); elapsed >= 10*time.Second {
		t.Errorf("enable during restart took %s, want an immediate reload instead of waiting for the 10s backoff", elapsed)
	}
	supervisionWaitCondition(t, "one reload OnBoot after enabling during restart", 5*time.Second, func() bool {
		return supervisionLogCount(h, marker+"-boot") == 2
	})

	observed := time.Now().Add(time.Second)
	for time.Now().Before(observed) {
		if count := supervisionLogCount(h, marker+"-boot"); count != 2 {
			t.Fatalf("OnBoot count after enabling during restart = %d, want exactly 2 (the original boot and one reload)", count)
		}
		if status, ok := h.runtime.Status(id); !ok || status.State != plugin.StateRunning {
			t.Fatalf("Status after enabling during restart = %+v (ok=%t), want running", status, ok)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestPluginSupervisionCrashNotifiesAdminsAndRetryResetsFaults(t *testing.T) {
	supervisionSetPolicy(t, []time.Duration{50 * time.Millisecond}, 3, 30*time.Minute)

	h := newPluginReloadHarness(t)
	admin := h.user("admin", "sup-crash-admin")
	token := h.token(admin)
	const id = "sup-crash"
	marker := "SUP-" + nextPluginReloadRun()
	supervisionSeed(t, h, id, marker)

	if r := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+id+"/enable", "", token); r.code != http.StatusOK {
		t.Fatalf("enable %s = %d, want 200; body=%q", id, r.code, r.body)
	}
	supervisionWaitState(t, h, id, plugin.StateRunning, supervisionStateBudget)

	for fault := 1; fault <= 3; fault++ {
		if fault > 1 {
			supervisionWaitState(t, h, id, plugin.StateRunning, supervisionStateBudget)
		}
		if r := supervisionAction(h.accessHarness, token, id, "slow"); r.code < 500 || r.code > 599 {
			t.Fatalf("runaway action fault %d = %d, want a 5xx failure; body=%q", fault, r.code, r.body)
		}
		if fault < 3 {
			status := supervisionWaitState(t, h, id, plugin.StateRestarting, 5*time.Second)
			if status.Faults != fault {
				t.Errorf("Faults after failure %d = %d, want %d; status=%+v", fault, status.Faults, fault, status)
			}
			continue
		}
		crashed := supervisionWaitState(t, h, id, plugin.StateCrashed, 5*time.Second)
		if crashed.Faults != 3 {
			t.Errorf("Faults after crash = %d, want 3; status=%+v", crashed.Faults, crashed)
		}
		if crashed.LastError == "" {
			t.Errorf("LastError after crash is empty, want the timeout reason; status=%+v", crashed)
		}
		if !crashed.NextRestartAt.IsZero() {
			t.Errorf("NextRestartAt after crash = %s, want zero (no further restart)", crashed.NextRestartAt)
		}
	}

	groupKey := notify.KindSystemPluginFailed + ":" + id
	supervisionWaitCondition(t, "one system.plugin_failed notification for the admin", 10*time.Second, func() bool {
		var count int64
		if err := h.db.Raw(`SELECT count(*) FROM notifications WHERE user_id = ? AND kind = ?`, admin.ID, notify.KindSystemPluginFailed).Scan(&count).Error; err != nil {
			t.Fatalf("count plugin failure notifications: %v", err)
		}
		return count == 1
	})

	var notification struct {
		Category string `gorm:"column:category"`
		GroupKey string `gorm:"column:group_key"`
		Params   string `gorm:"column:params"`
	}
	if err := h.db.Raw(`SELECT category, group_key, params::text AS params FROM notifications WHERE user_id = ? AND kind = ? ORDER BY id DESC LIMIT 1`, admin.ID, notify.KindSystemPluginFailed).Scan(&notification).Error; err != nil {
		t.Fatalf("read plugin failure notification: %v", err)
	}
	if notification.Category != notify.CategorySystem {
		t.Errorf("notification category = %q, want %q", notification.Category, notify.CategorySystem)
	}
	if notification.GroupKey != groupKey {
		t.Errorf("notification group_key = %q, want %q", notification.GroupKey, groupKey)
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(notification.Params), &params); err != nil {
		t.Fatalf("notification params are not JSON: %v; params=%q", err, notification.Params)
	}
	if params["plugin"] != id {
		t.Errorf("notification params.plugin = %v, want %q; params=%v", params["plugin"], id, params)
	}
	if name, _ := params["name"].(string); name == "" {
		t.Errorf("notification params.name = %q, want the plugin name; params=%v", name, params)
	}
	if errText, _ := params["error"].(string); errText == "" {
		t.Errorf("notification params.error = %q, want the failure reason; params=%v", errText, params)
	}

	observed := time.Now().Add(supervisionObservation)
	for time.Now().Before(observed) {
		status, ok := h.runtime.Status(id)
		if !ok || status.State != plugin.StateCrashed {
			t.Fatalf("Status while observing the crash = %+v (ok=%t), want crashed without another restart", status, ok)
		}
		time.Sleep(20 * time.Millisecond)
	}

	detail := supervisionDetailFields(t, h.accessHarness, token, id)
	if detail["state"] != plugin.StateCrashed {
		t.Errorf("detail state after crash = %v, want %q; detail=%v", detail["state"], plugin.StateCrashed, detail)
	}
	if detail["loaded"] != true {
		t.Errorf("detail loaded after crash = %v, want true; detail=%v", detail["loaded"], detail)
	}
	if faults, _ := detail["faults"].(float64); int(faults) != 3 {
		t.Errorf("detail faults after crash = %v, want 3; detail=%v", detail["faults"], detail)
	}
	if _, present := detail["next_restart_at"]; present {
		t.Errorf("detail next_restart_at after crash = %v, want it omitted; detail=%v", detail["next_restart_at"], detail)
	}

	list := updCall(h.accessHarness, http.MethodGet, "/api/admin/plugins", "", token)
	if list.code != http.StatusOK {
		t.Fatalf("GET /api/admin/plugins after crash = %d, want 200; body=%q", list.code, list.body)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(list.body), &items); err != nil {
		t.Fatalf("admin plugin list after crash is not JSON: %v; body=%q", err, list.body)
	}
	foundCrashed := false
	for _, item := range items {
		if item["id"] != id {
			continue
		}
		foundCrashed = true
		if item["state"] != plugin.StateCrashed {
			t.Errorf("list state after crash = %v, want %q; item=%v", item["state"], plugin.StateCrashed, item)
		}
		if item["enabled"] != true {
			t.Errorf("list enabled after crash = %v, want true; item=%v", item["enabled"], item)
		}
		if errText, _ := item["last_error"].(string); errText == "" {
			t.Errorf("list last_error after crash = %q, want the failure reason; item=%v", errText, item)
		}
	}
	if !foundCrashed {
		t.Errorf("admin plugin list has no item for crashed plugin %s; items=%v", id, items)
	}

	if r := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+id+"/enable", "", token); r.code != http.StatusOK {
		t.Fatalf("retry crashed plugin = %d, want 200; body=%q", r.code, r.body)
	}
	recovered := supervisionWaitState(t, h, id, plugin.StateRunning, supervisionStateBudget)
	if recovered.Faults != 0 {
		t.Errorf("Faults after retrying a crashed plugin = %d, want 0; status=%+v", recovered.Faults, recovered)
	}
	if enabled, _ := supervisionPluginRow(t, h.accessHarness, id); !enabled {
		t.Errorf("plugin row enabled after retry = false, want true")
	}
}

func TestPluginSupervisionListAndDetailStateFields(t *testing.T) {
	h := newPluginReloadHarness(t)
	_, token, reader := agtSetup(t, h.accessHarness, "Supervision List Reader", "plugins.read")

	const runningID = "sup-list-running"
	const disabledID = "sup-list-disabled"
	const unmanagedID = "sup-list-unmanaged"

	plgSeed(t, h.accessHarness, runningID, fmt.Sprintf(`{"id":%q,"name":"Running","version":"1.0.0","entrypoint":"index.js"}`, runningID), false)
	hostWriteEntry(t, runningID, `JellyCMS.registerHook("OnBoot", function () {});`)
	plgSeed(t, h.accessHarness, disabledID, fmt.Sprintf(`{"id":%q,"name":"Disabled","version":"1.0.0","entrypoint":"index.js"}`, disabledID), false)
	hostWriteEntry(t, disabledID, `JellyCMS.registerHook("OnBoot", function () {});`)
	writeReloadPlugin(t, "data/plugins/"+unmanagedID, reloadPluginMeta(unmanagedID, "1.0.0", "index.js"), `JellyCMS.registerHook("OnBoot", function () {});`)

	if r := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+runningID+"/enable", "", token); r.code != http.StatusOK {
		t.Fatalf("enable running list fixture = %d, want 200; body=%q", r.code, r.body)
	}
	supervisionWaitState(t, h, runningID, plugin.StateRunning, supervisionStateBudget)

	type listExpectation struct {
		enabled bool
		managed bool
		state   string
	}
	want := map[string]listExpectation{
		runningID:   {enabled: true, managed: true, state: plugin.StateRunning},
		disabledID:  {enabled: false, managed: true, state: "disabled"},
		unmanagedID: {enabled: true, managed: false, state: "stopped"},
	}

	checkItems := func(label string, items []map[string]any) {
		t.Helper()
		byID := make(map[string]map[string]any, len(items))
		for _, item := range items {
			id, _ := item["id"].(string)
			byID[id] = item
		}
		for id, expectation := range want {
			item, ok := byID[id]
			if !ok {
				t.Errorf("%s has no item for %s; items=%v", label, id, items)
				continue
			}
			enabled, _ := item["enabled"].(bool)
			managed, _ := item["managed"].(bool)
			state, _ := item["state"].(string)
			if enabled != expectation.enabled || managed != expectation.managed || state != expectation.state {
				t.Errorf("%s item %s = enabled:%t managed:%t state:%q, want enabled:%t managed:%t state:%q; item=%v", label, id, enabled, managed, state, expectation.enabled, expectation.managed, expectation.state, item)
			}
			if _, present := item["last_error"]; present {
				t.Errorf("%s item %s has last_error=%v while healthy, want the field omitted; item=%v", label, id, item["last_error"], item)
			}
		}
	}

	adminList := updCall(h.accessHarness, http.MethodGet, "/api/admin/plugins", "", token)
	if adminList.code != http.StatusOK {
		t.Fatalf("GET /api/admin/plugins = %d, want 200; body=%q", adminList.code, adminList.body)
	}
	var adminItems []map[string]any
	if err := json.Unmarshal([]byte(adminList.body), &adminItems); err != nil {
		t.Fatalf("admin plugin list is not JSON: %v; body=%q", err, adminList.body)
	}
	checkItems("GET /api/admin/plugins", adminItems)

	v1List := agtDo(h.accessHarness, http.MethodGet, "/api/v1/plugins", "", reader.APIKey, "")
	agtExpectOK(t, v1List, "GET /api/v1/plugins")
	var collection struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	agtDecode(t, v1List, &collection, "GET /api/v1/plugins")
	if collection.Total != len(want) || len(collection.Items) != len(want) {
		t.Errorf("GET /api/v1/plugins total=%d items=%d, want %d", collection.Total, len(collection.Items), len(want))
	}
	checkItems("GET /api/v1/plugins", collection.Items)

	doc := apiv1ContractDoc(t, h.accessHarness)
	schema := apiv1ContractResponseSchema(doc, apiv1ContractOp(doc, "/api/v1/plugins", "get"), "200")
	collectionProperties := apiv1ContractChild(doc, schema, "properties")
	itemsArray := apiv1ContractChild(doc, collectionProperties, "items")
	itemSchema := apiv1ContractChild(doc, itemsArray, "items")
	properties := apiv1ContractChild(doc, itemSchema, "properties")
	for _, field := range []string{"enabled", "managed", "state", "last_error"} {
		if _, ok := properties[field]; !ok {
			t.Errorf("OpenAPI /api/v1/plugins item schema has no %q field; properties=%v", field, properties)
		}
	}

	detail := supervisionDetailFields(t, h.accessHarness, token, runningID)
	if detail["state"] != plugin.StateRunning {
		t.Errorf("detail state for a running plugin = %v, want %q; detail=%v", detail["state"], plugin.StateRunning, detail)
	}
	if faults, _ := detail["faults"].(float64); faults != 0 {
		t.Errorf("detail faults for a healthy plugin = %v, want 0; detail=%v", detail["faults"], detail)
	}
	for _, field := range []string{"last_error", "last_failure_at", "next_restart_at"} {
		if _, present := detail[field]; present {
			t.Errorf("detail %s for a healthy plugin = %v, want it omitted; detail=%v", field, detail[field], detail)
		}
	}
}
