package plugin

// Regression tests for the PR #53 fifth-round review: Runtime.ForbidRestarts.
//
// ForbidRestarts is the contract a caller uses when a rollback could not finish:
// the running instance keeps serving actions, but its next worker failure goes
// straight to StateCrashed and is never restarted automatically. The mark belongs
// to that instance, so an admin reload replaces it and restores the normal
// automatic restart. An id that is not loaded yields false. These tests are the
// contract; implementers must not edit this file. The current skeleton returns
// false and does nothing, so TestSupervisorForbidRestartsCrashesOnNextFailure and
// TestSupervisorForbidRestartsClearedByAdminReload must fail until
// ForbidRestarts is implemented.
//
// Every plugin runs as a real worker subprocess. All waits poll with a bounded
// loop; a fixed sleep is only used to observe that nothing happens for a window.

import (
	"context"
	"errors"
	"testing"
	"time"
)

const (
	// supervisorForbidBackoff is the wait a normal automatic restart would take.
	// A restart that was not forbidden would boot the plugin again inside it.
	supervisorForbidBackoff = 300 * time.Millisecond
	// supervisorForbidObserveWindow is several times the backoff.
	supervisorForbidObserveWindow = 5 * supervisorForbidBackoff
)

// TestSupervisorForbidRestartsCrashesOnNextFailure covers the review fix that a
// plugin marked by ForbidRestarts keeps running, and that its next worker failure
// goes directly to StateCrashed without the automatic restart.
func TestSupervisorForbidRestartsCrashesOnNextFailure(t *testing.T) {
	setSupervisorPolicy(t, []time.Duration{supervisorForbidBackoff}, 5, 30*time.Minute)

	sink := installSupervisorLogSink(t)
	pluginDir, meta := writeSupervisorPlugin(t, "supervisor-forbid-next")
	runtime := newSupervisorRuntime(t)

	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load(%q) error = %v, want nil", meta.ID, err)
	}
	waitSupervisorBootCount(t, sink, meta.ID, 1, supervisorStateBudget)
	waitSupervisorState(t, runtime, meta.ID, StateRunning, supervisorStateBudget)

	const reasonText = "rollback incomplete"
	if marked := runtime.ForbidRestarts(meta.ID, errors.New(reasonText)); !marked {
		t.Fatalf("ForbidRestarts(%q) on a loaded plugin = %t, want true", meta.ID, marked)
	}

	running, ok := runtime.Status(meta.ID)
	if !ok || running.State != StateRunning {
		t.Fatalf(
			"Status(%q) after ForbidRestarts = State %q (ok=%t), want State=%q; marking must not stop a running worker",
			meta.ID, running.State, ok, StateRunning,
		)
	}
	if err := runtime.Invoke(context.Background(), meta.ID, supervisorPingHook, map[string]interface{}{"case": "forbid-running"}); err != nil {
		t.Fatalf("Invoke(%q, %q) after ForbidRestarts error = %v, want nil; the marked worker must keep serving actions", meta.ID, supervisorPingHook, err)
	}

	bootsBefore := sink.bootCount(meta.ID)
	killSupervisorWorker(t, runtime, meta.ID)

	// The failure must not pass through StateRestarting: poll tightly and fail on
	// the first observation of it, because a normal restart would wait 300ms there.
	deadline := time.Now().Add(supervisorStateBudget)
	for {
		status, ok := runtime.Status(meta.ID)
		if ok && status.State == StateRestarting {
			t.Fatalf(
				"Status(%q) after the forbidden failure = State %q, want %q directly; the plugin must not wait for an automatic restart (NextRestartAt=%s)",
				meta.ID, status.State, StateCrashed, status.NextRestartAt,
			)
		}
		if ok && status.State == StateCrashed {
			if status.LastError == "" {
				t.Fatalf("Status(%q).LastError after the forbidden failure = %q, want a non-empty failure reason", meta.ID, status.LastError)
			}

			break
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"Status(%q) = %+v (ok=%t) after the forbidden failure, want State=%q within %s",
				meta.ID, status, ok, StateCrashed, supervisorStateBudget,
			)
		}
		time.Sleep(5 * time.Millisecond)
	}

	requireSupervisorStaysCrashed(t, runtime, sink, meta.ID, bootsBefore, supervisorForbidObserveWindow)
}

// TestSupervisorForbidRestartsClearedByAdminReload covers the review fix that an
// admin reload replaces the marked instance: the new instance restarts
// automatically again after a failure.
func TestSupervisorForbidRestartsClearedByAdminReload(t *testing.T) {
	setSupervisorPolicy(t, []time.Duration{supervisorForbidBackoff}, 5, 30*time.Minute)

	sink := installSupervisorLogSink(t)
	pluginDir, meta := writeSupervisorPlugin(t, "supervisor-forbid-reload")
	runtime := newSupervisorRuntime(t)

	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load(%q) error = %v, want nil", meta.ID, err)
	}
	waitSupervisorBootCount(t, sink, meta.ID, 1, supervisorStateBudget)
	waitSupervisorState(t, runtime, meta.ID, StateRunning, supervisorStateBudget)

	if marked := runtime.ForbidRestarts(meta.ID, errors.New("rollback incomplete")); !marked {
		t.Fatalf("ForbidRestarts(%q) on a loaded plugin = %t, want true", meta.ID, marked)
	}

	ctx, cancel := context.WithTimeout(context.Background(), supervisorStateBudget)
	defer cancel()

	if err := runtime.ReloadForID(ctx, meta.ID, pluginDir); err != nil {
		t.Fatalf("ReloadForID(%q) after ForbidRestarts error = %v, want nil", meta.ID, err)
	}
	waitSupervisorState(t, runtime, meta.ID, StateRunning, supervisorStateBudget)
	waitSupervisorBootCount(t, sink, meta.ID, 2, supervisorStateBudget)

	killSupervisorWorker(t, runtime, meta.ID)
	waitSupervisorState(t, runtime, meta.ID, StateRestarting, supervisorStateBudget)
	waitSupervisorState(t, runtime, meta.ID, StateRunning, supervisorStateBudget)
	waitSupervisorBootCount(t, sink, meta.ID, 3, supervisorStateBudget)
}

// TestSupervisorForbidRestartsUnknownPlugin covers the review fix that
// ForbidRestarts reports false for an id that is not loaded.
func TestSupervisorForbidRestartsUnknownPlugin(t *testing.T) {
	runtime := newSupervisorRuntime(t)

	const unknownID = "supervisor-forbid-never-loaded"
	if marked := runtime.ForbidRestarts(unknownID, errors.New("should not apply")); marked {
		t.Fatalf("ForbidRestarts(%q) on an unloaded plugin = true, want false", unknownID)
	}
}
