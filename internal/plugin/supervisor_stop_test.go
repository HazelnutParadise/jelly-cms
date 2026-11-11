package plugin

// Regression tests for the PR #53 fourth-round review: Runtime.StopRestarts.
//
// StopRestarts is the contract an admin path uses when a rollback could not
// finish: it turns a plugin that is waiting for an automatic restart into
// StateCrashed with the given reason as LastError, cancels the scheduled
// restart, and keeps it from being scheduled again, including when a hold taken
// during the wait is released afterwards. A running or unknown plugin is left
// alone. These tests are the contract; implementers must not edit this file.
// The current skeleton returns false and does nothing, so
// TestSupervisorStopRestartsCancelsScheduledRestart and
// TestSupervisorStopRestartsSurvivesHoldRelease must fail until StopRestarts is
// implemented.
//
// Every plugin runs as a real worker subprocess. All waits poll with a bounded
// loop; a fixed sleep is only used to observe that nothing happens for a window.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	// supervisorStopBackoff keeps a killed plugin in StateRestarting long enough
	// for the test to act on the waiting period before the timer fires.
	supervisorStopBackoff = 300 * time.Millisecond
	// supervisorStopObserveWindow is several times the backoff: a restart that
	// was not cancelled would have fired well inside it.
	supervisorStopObserveWindow = 5 * supervisorStopBackoff
)

// requireSupervisorStaysCrashed watches the plugin for window and fails if it
// leaves StateCrashed or boots again.
func requireSupervisorStaysCrashed(t *testing.T, runtime *Runtime, sink *supervisorLogSink, pluginID string, boots int, window time.Duration) {
	t.Helper()

	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		status, ok := runtime.Status(pluginID)
		if !ok || status.State != StateCrashed {
			t.Fatalf(
				"Status(%q) while the stopped restart was pending = %+v (ok=%t), want State=%q; a stopped plugin must stay crashed",
				pluginID, status, ok, StateCrashed,
			)
		}
		if got := sink.bootCount(pluginID); got != boots {
			t.Fatalf(
				"OnBoot log count for plugin %q after StopRestarts = %d, want %d; the cancelled restart must not boot the plugin again",
				pluginID, got, boots,
			)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestSupervisorStopRestartsCancelsScheduledRestart covers the review fix that a
// plugin waiting for an automatic restart can be stopped with a reason: the
// restart is cancelled, the plugin is crashed with that reason, and an admin
// reload brings it back.
func TestSupervisorStopRestartsCancelsScheduledRestart(t *testing.T) {
	setSupervisorPolicy(t, []time.Duration{supervisorStopBackoff}, 5, 30*time.Minute)

	sink := installSupervisorLogSink(t)
	pluginDir, meta := writeSupervisorPlugin(t, "supervisor-stop-scheduled")
	runtime := newSupervisorRuntime(t)

	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load(%q) error = %v, want nil", meta.ID, err)
	}
	waitSupervisorBootCount(t, sink, meta.ID, 1, supervisorStateBudget)

	killSupervisorWorker(t, runtime, meta.ID)
	waitSupervisorState(t, runtime, meta.ID, StateRestarting, supervisorStateBudget)
	bootsBefore := sink.bootCount(meta.ID)

	const reasonText = "rollback incomplete"
	if stopped := runtime.StopRestarts(meta.ID, errors.New(reasonText)); !stopped {
		t.Fatalf("StopRestarts(%q) while waiting for an automatic restart = %t, want true", meta.ID, stopped)
	}

	status, ok := runtime.Status(meta.ID)
	if !ok || status.State != StateCrashed || !strings.Contains(status.LastError, reasonText) {
		t.Fatalf(
			"Status(%q) after StopRestarts = State %q, LastError %q (ok=%t), want State=%q and LastError containing %q",
			meta.ID, status.State, status.LastError, ok, StateCrashed, reasonText,
		)
	}

	requireSupervisorStaysCrashed(t, runtime, sink, meta.ID, bootsBefore, supervisorStopObserveWindow)

	ctx, cancel := context.WithTimeout(context.Background(), supervisorStateBudget)
	defer cancel()

	if err := runtime.ReloadForID(ctx, meta.ID, pluginDir); err != nil {
		t.Fatalf("ReloadForID(%q) after StopRestarts error = %v, want nil", meta.ID, err)
	}
	waitSupervisorState(t, runtime, meta.ID, StateRunning, supervisorStateBudget)
}

// TestSupervisorStopRestartsSurvivesHoldRelease covers the review fix that
// releasing an admin hold taken during the waiting period does not schedule a
// restart for a plugin that StopRestarts already stopped.
func TestSupervisorStopRestartsSurvivesHoldRelease(t *testing.T) {
	setSupervisorPolicy(t, []time.Duration{supervisorStopBackoff}, 5, 30*time.Minute)

	sink := installSupervisorLogSink(t)
	pluginDir, meta := writeSupervisorPlugin(t, "supervisor-stop-hold")
	runtime := newSupervisorRuntime(t)

	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load(%q) error = %v, want nil", meta.ID, err)
	}
	waitSupervisorBootCount(t, sink, meta.ID, 1, supervisorStateBudget)

	killSupervisorWorker(t, runtime, meta.ID)
	waitSupervisorState(t, runtime, meta.ID, StateRestarting, supervisorStateBudget)
	bootsBefore := sink.bootCount(meta.ID)

	release, err := runtime.HoldRestart(context.Background(), meta.ID)
	if err != nil {
		t.Fatalf("HoldRestart(%q) error = %v, want nil", meta.ID, err)
	}
	if release == nil {
		t.Fatalf("HoldRestart(%q) release = nil, want a non-nil release", meta.ID)
	}

	if stopped := runtime.StopRestarts(meta.ID, errors.New("rollback incomplete")); !stopped {
		t.Fatalf("StopRestarts(%q) while held and restarting = %t, want true", meta.ID, stopped)
	}

	release()

	requireSupervisorStaysCrashed(t, runtime, sink, meta.ID, bootsBefore, supervisorStopObserveWindow)
}

// TestSupervisorStopRestartsLeavesRunningAndUnknownAlone covers the review fix
// that StopRestarts only touches a plugin that is waiting for a restart: a
// running plugin and an id that is not loaded are left as they are.
func TestSupervisorStopRestartsLeavesRunningAndUnknownAlone(t *testing.T) {
	setSupervisorPolicy(t, []time.Duration{supervisorStopBackoff}, 5, 30*time.Minute)

	pluginDir, meta := writeSupervisorPlugin(t, "supervisor-stop-running")
	runtime := newSupervisorRuntime(t)

	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load(%q) error = %v, want nil", meta.ID, err)
	}
	waitSupervisorState(t, runtime, meta.ID, StateRunning, supervisorStateBudget)

	if changed := runtime.StopRestarts(meta.ID, errors.New("should not apply")); changed {
		t.Fatalf("StopRestarts(%q) on a running plugin = true, want false", meta.ID)
	}

	status, ok := runtime.Status(meta.ID)
	if !ok || status.State != StateRunning {
		t.Fatalf("Status(%q) after StopRestarts on a running plugin = State %q (ok=%t), want State=%q", meta.ID, status.State, ok, StateRunning)
	}

	const unknownID = "supervisor-stop-never-loaded"
	if changed := runtime.StopRestarts(unknownID, errors.New("should not apply")); changed {
		t.Fatalf("StopRestarts(%q) on an unloaded plugin = true, want false", unknownID)
	}
}
