package server

// Regression test for the PR #53 fifth-round review: the plugin test endpoint
// reports the supervisor state.
//
// POST /api/admin/plugins/:id/test answers a runtime check. The check must use
// the supervisor state (Runtime.Status), not only whether the plugin is in the
// registry. A plugin that waits for an automatic restart is loaded but not
// healthy, so while it is restarting the check must answer ok false with detail
// restarting. These tests are the contract; implementers must not edit this file.
//
// Every test needs JELLY_TEST_POSTGRES=1 and the dedicated PostgreSQL container.
// All waits poll with a bounded loop; a fixed sleep is never followed directly
// by an assertion.

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
)

// reviewRound5RestartBackoff keeps the plugin in StateRestarting long enough for
// the test to call the endpoint while the restart is still pending.
const reviewRound5RestartBackoff = 10 * time.Second

type reviewRound5Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

type reviewRound5TestReply struct {
	OK     bool                `json:"ok"`
	Checks []reviewRound5Check `json:"checks"`
}

// reviewRound5RuntimeCheck calls the test endpoint and returns its runtime check.
func reviewRound5RuntimeCheck(t *testing.T, h *reloadHarness, token, id string) reviewRound5Check {
	t.Helper()

	r := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+id+"/test", "", token)
	if r.code != http.StatusOK {
		t.Fatalf("POST /api/admin/plugins/%s/test = %d, want 200; body=%q", id, r.code, r.body)
	}

	var reply reviewRound5TestReply
	if err := json.Unmarshal([]byte(r.body), &reply); err != nil {
		t.Fatalf("plugin test response is not JSON: %v; body=%q", err, r.body)
	}
	for _, check := range reply.Checks {
		if check.Name == "runtime" {
			return check
		}
	}
	t.Fatalf("plugin test response has no runtime check; body=%q", r.body)

	return reviewRound5Check{}
}

// TestPluginTestEndpointReportsSupervisorState covers the review fix that the
// runtime check of the test endpoint reports the supervisor state: running for a
// healthy plugin, and not ok with detail restarting while it waits for a restart.
func TestPluginTestEndpointReportsSupervisorState(t *testing.T) {
	supervisionSetPolicy(t, []time.Duration{reviewRound5RestartBackoff}, 5, 30*time.Minute)

	h := newPluginReloadHarness(t)
	admin := h.user("admin", "rev5-test-admin")
	token := h.token(admin)
	const id = "rev5-test-probe"
	marker := "REV5-" + nextPluginReloadRun()
	supervisionSeed(t, h, id, marker)

	if r := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+id+"/enable", "", token); r.code != http.StatusOK {
		t.Fatalf("enable %s = %d, want 200; body=%q", id, r.code, r.body)
	}
	supervisionWaitState(t, h, id, plugin.StateRunning, supervisionStateBudget)

	running := reviewRound5RuntimeCheck(t, h, token, id)
	if !running.OK || running.Detail != plugin.StateRunning {
		t.Fatalf(
			"runtime check of a running plugin = ok:%t detail:%q, want ok:true detail:%q",
			running.OK, running.Detail, plugin.StateRunning,
		)
	}

	if r := supervisionAction(h.accessHarness, token, id, "slow"); r.code < 500 || r.code > 599 {
		t.Fatalf("runaway action = %d, want a 5xx failure; body=%q", r.code, r.body)
	}
	supervisionWaitState(t, h, id, plugin.StateRestarting, 5*time.Second)

	restarting := reviewRound5RuntimeCheck(t, h, token, id)
	if restarting.OK || restarting.Detail != plugin.StateRestarting {
		t.Fatalf(
			"runtime check while restarting = ok:%t detail:%q, want ok:false detail:%q; a plugin waiting for its automatic restart is not healthy",
			restarting.OK, restarting.Detail, plugin.StateRestarting,
		)
	}
}
