package plugin

// Tests for ticket 36 R1 (ticket 36): automatic
// worker restart, supervision status and the failure handler.
//
// This file is the R1 contract and is written before the implementation. Every
// test must fail on the empty supervisor skeleton. Implementers must not edit
// this file; make supervisor.go and manager.go satisfy it instead.
//
// Every plugin runs as a real worker subprocess. Tests replace the package-level
// restart policy at the start and restore it with t.Cleanup. All waits poll with
// a bounded loop; a fixed sleep is never followed directly by an assertion.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const (
	// supervisorStateBudget bounds one state transition. A worker restart has to
	// start a new subprocess, so this stays generous enough for -race runs.
	supervisorStateBudget = 15 * time.Second
	// supervisorHandlerBudget bounds a wait for failure-handler callbacks.
	supervisorHandlerBudget = 10 * time.Second
	// supervisorSlowBackoff keeps a plugin visibly in StateRestarting long
	// enough for the test to observe and act on the waiting period.
	supervisorSlowBackoff = 2 * time.Second
	// supervisorFastBackoff cycles through restarts quickly.
	supervisorFastBackoff = 250 * time.Millisecond
	// supervisorCrashWindow is a deliberately tiny fault window.
	supervisorCrashWindow = 300 * time.Millisecond

	supervisorPluginScript = `
JellyCMS.registerHook("OnBoot", function (data) {
  JellyCMS.log.info("supervisor-boot:" + data.plugin);
});
JellyCMS.registerHook("action:slow", function (data) {
  while (true) {}
});
JellyCMS.registerHook("action:ping", function (data) {
  JellyCMS.log.info("supervisor-ping");
});
`
)

var (
	supervisorSlowHook = Hook("action:slow")
	supervisorPingHook = Hook("action:ping")
)

// setSupervisorPolicy replaces the restart policy for one test and restores the
// previous values after it.
func setSupervisorPolicy(t *testing.T, backoff []time.Duration, threshold int, window time.Duration) {
	t.Helper()

	previousBackoff, previousThreshold, previousWindow := restartBackoff, crashThreshold, crashWindow
	restartBackoff, crashThreshold, crashWindow = backoff, threshold, window

	t.Cleanup(func() {
		restartBackoff = previousBackoff
		crashThreshold = previousThreshold
		crashWindow = previousWindow
	})
}

// newSupervisorRuntime creates a runtime that is closed when the test ends.
func newSupervisorRuntime(t *testing.T) *Runtime {
	t.Helper()

	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v, want nil", err)
	}

	t.Cleanup(runtime.Close)

	return runtime
}

// writeSupervisorPlugin writes a plugin with a boot log, a runaway action and a
// healthy action, all running in a real worker.
func writeSupervisorPlugin(t *testing.T, id string) (string, PluginMetadata) {
	t.Helper()

	pluginDir := t.TempDir()
	meta := PluginMetadata{
		ID:         id,
		Name:       id,
		Version:    "1.0.0",
		Entrypoint: "index.js",
		Actions:    []ActionSpec{{ID: "slow"}, {ID: "ping"}},
	}

	manifest, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal manifest for %q error = %v, want nil", id, err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), manifest, 0o600); err != nil {
		t.Fatalf("write manifest for %q error = %v, want nil", id, err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, meta.Entrypoint), []byte(supervisorPluginScript), 0o600); err != nil {
		t.Fatalf("write entrypoint for %q error = %v, want nil", id, err)
	}

	return pluginDir, meta
}

type supervisorLogLine struct {
	plugin  string
	level   string
	message string
}

// supervisorLogSink records every line the runtime hands to the log sink.
type supervisorLogSink struct {
	mu    sync.Mutex
	lines []supervisorLogLine
}

// installSupervisorLogSink routes the process log sink into a recorder.
func installSupervisorLogSink(t *testing.T) *supervisorLogSink {
	t.Helper()

	sink := &supervisorLogSink{}
	SetLogSink(func(pluginID, level, message string) {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		sink.lines = append(sink.lines, supervisorLogLine{plugin: pluginID, level: level, message: message})
	})
	t.Cleanup(func() { SetLogSink(nil) })

	return sink
}

// bootCount reports how many times the plugin's OnBoot log line was seen.
func (s *supervisorLogSink) bootCount(pluginID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	want := "supervisor-boot:" + pluginID
	count := 0
	for _, line := range s.lines {
		if line.plugin == pluginID && line.message == want {
			count++
		}
	}

	return count
}

// waitSupervisorBootCount polls until the plugin booted want times.
func waitSupervisorBootCount(t *testing.T, sink *supervisorLogSink, pluginID string, want int, budget time.Duration) {
	t.Helper()

	deadline := time.Now().Add(budget)
	for {
		got := sink.bootCount(pluginID)
		if got == want {
			return
		}
		if got > want {
			t.Fatalf("OnBoot log count for plugin %q = %d, want %d", pluginID, got, want)
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"OnBoot log count for plugin %q = %d, want %d within %s",
				pluginID, got, want, budget,
			)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitSupervisorState polls Status until the plugin reaches want.
func waitSupervisorState(t *testing.T, runtime *Runtime, pluginID, want string, budget time.Duration) PluginStatus {
	t.Helper()

	deadline := time.Now().Add(budget)
	var (
		last   PluginStatus
		lastOK bool
	)
	for {
		status, ok := runtime.Status(pluginID)
		last, lastOK = status, ok
		if ok && status.State == want {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"Status(%q) = %+v (ok=%t), want State=%q within %s; last status = %+v (ok=%t)",
				pluginID, status, ok, want, budget, last, lastOK,
			)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// supervisorWorkerPID returns the pid of the worker currently registered for
// the plugin. Same-package tests may read the unexported worker field.
func supervisorWorkerPID(t *testing.T, runtime *Runtime, pluginID string) int {
	t.Helper()

	runtime.mu.RLock()
	state := runtime.plugins[pluginID]
	pid := 0
	if state != nil && state.worker != nil && state.worker.cmd != nil && state.worker.cmd.Process != nil {
		pid = state.worker.cmd.Process.Pid
	}
	runtime.mu.RUnlock()

	if pid <= 0 {
		t.Fatalf("worker pid for plugin %q = %d, want a live worker process", pluginID, pid)
	}

	return pid
}

// killSupervisorWorker kills the current worker from outside the runtime, which
// is the idle-crash path the supervisor has to notice on its own.
func killSupervisorWorker(t *testing.T, runtime *Runtime, pluginID string) int {
	t.Helper()

	pid := supervisorWorkerPID(t, runtime, pluginID)
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("os.FindProcess(%d) for plugin %q error = %v, want nil", pid, pluginID, err)
	}
	if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("kill worker pid %d for plugin %q error = %v, want nil", pid, pluginID, err)
	}

	return pid
}

type supervisorHandlerRecord struct {
	pluginID    string
	status      PluginStatus
	statusOK    bool
	statusState string
}

// supervisorHandlerRecorder captures failure-handler calls. The handler itself
// calls Status, so a handler invoked under a runtime lock shows up as a missing
// call instead of an untested deadlock guard.
type supervisorHandlerRecorder struct {
	mu    sync.Mutex
	calls []supervisorHandlerRecord
}

func (r *supervisorHandlerRecorder) add(record supervisorHandlerRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, record)
}

func (r *supervisorHandlerRecorder) snapshot() []supervisorHandlerRecord {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]supervisorHandlerRecord(nil), r.calls...)
}

// waitSupervisorHandlerCalls polls until at least want handler calls arrived.
func waitSupervisorHandlerCalls(t *testing.T, recorder *supervisorHandlerRecorder, want int, budget time.Duration) []supervisorHandlerRecord {
	t.Helper()

	deadline := time.Now().Add(budget)
	for {
		calls := recorder.snapshot()
		if len(calls) >= want {
			return calls
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"failure handler call count = %d, want at least %d within %s; calls = %+v",
				len(calls), want, budget, calls,
			)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestSupervisorTimeoutRestartsAndRecovers covers R1: a hook that runs past the
// one second step limit fails the call, the plugin enters restarting, then the
// supervisor reloads it and a normal action works again.
func TestSupervisorTimeoutRestartsAndRecovers(t *testing.T) {
	setSupervisorPolicy(t, []time.Duration{supervisorSlowBackoff}, 5, 30*time.Minute)

	sink := installSupervisorLogSink(t)
	pluginDir, meta := writeSupervisorPlugin(t, "supervisor-timeout")
	runtime := newSupervisorRuntime(t)

	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load(%q) error = %v, want nil", meta.ID, err)
	}
	running, ok := runtime.Status(meta.ID)
	if !ok || running.State != StateRunning {
		t.Fatalf("Status(%q) after Load = %+v (ok=%t), want State=%q (ok=true)", meta.ID, running, ok, StateRunning)
	}
	if running.Faults != 0 {
		t.Fatalf("Status(%q).Faults after Load = %d, want 0", meta.ID, running.Faults)
	}
	waitSupervisorBootCount(t, sink, meta.ID, 1, supervisorStateBudget)

	if err := runtime.Invoke(context.Background(), meta.ID, supervisorSlowHook, map[string]interface{}{"case": "timeout"}); err == nil {
		t.Fatalf("Invoke(%q, %q) on a runaway hook error = nil, want a timeout error", meta.ID, supervisorSlowHook)
	}

	restarting := waitSupervisorState(t, runtime, meta.ID, StateRestarting, supervisorStateBudget)
	if restarting.Faults != 1 {
		t.Fatalf("Status(%q).Faults while restarting = %d, want 1", meta.ID, restarting.Faults)
	}
	if restarting.LastError == "" {
		t.Fatalf("Status(%q).LastError while restarting = %q, want a non-empty failure reason", meta.ID, restarting.LastError)
	}
	if !restarting.NextRestartAt.After(time.Now()) {
		t.Fatalf(
			"Status(%q).NextRestartAt while restarting = %s, want a time after now (%s)",
			meta.ID, restarting.NextRestartAt, time.Now(),
		)
	}

	waitSupervisorState(t, runtime, meta.ID, StateRunning, supervisorStateBudget)
	if err := runtime.Invoke(context.Background(), meta.ID, supervisorPingHook, map[string]interface{}{"case": "after-restart"}); err != nil {
		t.Fatalf("Invoke(%q, %q) after automatic restart error = %v, want nil", meta.ID, supervisorPingHook, err)
	}
	waitSupervisorBootCount(t, sink, meta.ID, 2, supervisorStateBudget)
}

// TestSupervisorWaitingInvokeReturnsNotLoaded covers R1: action calls during the
// waiting period answer ErrPluginNotLoaded immediately instead of queueing for
// the restart.
func TestSupervisorWaitingInvokeReturnsNotLoaded(t *testing.T) {
	setSupervisorPolicy(t, []time.Duration{supervisorSlowBackoff}, 5, 30*time.Minute)

	pluginDir, meta := writeSupervisorPlugin(t, "supervisor-waiting-invoke")
	runtime := newSupervisorRuntime(t)

	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load(%q) error = %v, want nil", meta.ID, err)
	}
	waitSupervisorState(t, runtime, meta.ID, StateRunning, supervisorStateBudget)

	killSupervisorWorker(t, runtime, meta.ID)
	waitSupervisorState(t, runtime, meta.ID, StateRestarting, supervisorStateBudget)

	start := time.Now()
	err := runtime.Invoke(context.Background(), meta.ID, supervisorPingHook, map[string]interface{}{"case": "waiting"})
	elapsed := time.Since(start)

	if !errors.Is(err, ErrPluginNotLoaded) {
		t.Fatalf("Invoke(%q) while restarting error = %v, want ErrPluginNotLoaded", meta.ID, err)
	}
	if elapsed >= 100*time.Millisecond {
		t.Fatalf(
			"Invoke(%q) while restarting took %s, want under 100ms because it must not queue for the restart",
			meta.ID, elapsed,
		)
	}
}

// TestSupervisorCrashThresholdStopsRestartsAndReloadResets covers R1: after
// crashThreshold faults the plugin is crashed and stays crashed, then an
// explicit ReloadForID brings it back with Faults reset to 0.
func TestSupervisorCrashThresholdStopsRestartsAndReloadResets(t *testing.T) {
	setSupervisorPolicy(t, []time.Duration{supervisorFastBackoff}, 3, 30*time.Minute)

	sink := installSupervisorLogSink(t)
	pluginDir, meta := writeSupervisorPlugin(t, "supervisor-crash")
	runtime := newSupervisorRuntime(t)

	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load(%q) error = %v, want nil", meta.ID, err)
	}
	waitSupervisorBootCount(t, sink, meta.ID, 1, supervisorStateBudget)

	for failure := 1; failure <= 3; failure++ {
		waitSupervisorState(t, runtime, meta.ID, StateRunning, supervisorStateBudget)
		killSupervisorWorker(t, runtime, meta.ID)
		if failure < 3 {
			waitSupervisorState(t, runtime, meta.ID, StateRestarting, supervisorStateBudget)
			waitSupervisorState(t, runtime, meta.ID, StateRunning, supervisorStateBudget)
			waitSupervisorBootCount(t, sink, meta.ID, failure+1, supervisorStateBudget)

			continue
		}
	}

	crashed := waitSupervisorState(t, runtime, meta.ID, StateCrashed, supervisorStateBudget)
	if crashed.Faults != 3 {
		t.Fatalf("Status(%q).Faults after %d failures = %d, want 3", meta.ID, 3, crashed.Faults)
	}
	if crashed.LastError == "" {
		t.Fatalf("Status(%q).LastError after crashing = %q, want a non-empty failure reason", meta.ID, crashed.LastError)
	}

	bootsAtCrash := sink.bootCount(meta.ID)
	holdUntil := time.Now().Add(4 * supervisorFastBackoff)
	for time.Now().Before(holdUntil) {
		status, ok := runtime.Status(meta.ID)
		if !ok || status.State != StateCrashed {
			t.Fatalf(
				"Status(%q) = %+v (ok=%t) after crashing, want State=%q (ok=true) while holding %s (4x backoff)",
				meta.ID, status, ok, StateCrashed, 4*supervisorFastBackoff,
			)
		}
		if got := sink.bootCount(meta.ID); got != bootsAtCrash {
			t.Fatalf(
				"OnBoot log count while crashed = %d, want %d; a crashed plugin must not be reloaded",
				got, bootsAtCrash,
			)
		}
		time.Sleep(5 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), supervisorStateBudget)
	defer cancel()

	if err := runtime.ReloadForID(ctx, meta.ID, pluginDir); err != nil {
		t.Fatalf("ReloadForID(%q) after crashing error = %v, want nil", meta.ID, err)
	}

	recovered := waitSupervisorState(t, runtime, meta.ID, StateRunning, supervisorStateBudget)
	if recovered.Faults != 0 {
		t.Fatalf("Status(%q).Faults after ReloadForID = %d, want 0", meta.ID, recovered.Faults)
	}
}

// TestSupervisorFailureHandlerReportsRestartingAndCrashed covers R1:
// SetFailureHandler runs once per failure with State restarting and once more
// when the plugin enters crashed, outside the runtime locks.
func TestSupervisorFailureHandlerReportsRestartingAndCrashed(t *testing.T) {
	setSupervisorPolicy(t, []time.Duration{supervisorFastBackoff}, 3, 30*time.Minute)

	pluginDir, meta := writeSupervisorPlugin(t, "supervisor-handler")
	runtime := newSupervisorRuntime(t)

	recorder := &supervisorHandlerRecorder{}
	runtime.SetFailureHandler(func(pluginID string, status PluginStatus) {
		inner, ok := runtime.Status(pluginID)
		recorder.add(supervisorHandlerRecord{
			pluginID:    pluginID,
			status:      status,
			statusOK:    ok,
			statusState: inner.State,
		})
	})

	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load(%q) error = %v, want nil", meta.ID, err)
	}
	waitSupervisorState(t, runtime, meta.ID, StateRunning, supervisorStateBudget)

	for failure := 1; failure <= 2; failure++ {
		killSupervisorWorker(t, runtime, meta.ID)
		calls := waitSupervisorHandlerCalls(t, recorder, failure, supervisorHandlerBudget)
		record := calls[failure-1]

		if record.pluginID != meta.ID {
			t.Fatalf("failure handler call %d pluginID = %q, want %q", failure, record.pluginID, meta.ID)
		}
		if record.status.State != StateRestarting {
			t.Fatalf("failure handler call %d State = %q, want %q", failure, record.status.State, StateRestarting)
		}
		if !record.statusOK {
			t.Fatalf(
				"Status(%q) called inside failure handler call %d returned ok=false, want ok=true",
				meta.ID, failure,
			)
		}

		waitSupervisorState(t, runtime, meta.ID, StateRunning, supervisorStateBudget)
	}

	killSupervisorWorker(t, runtime, meta.ID)
	calls := waitSupervisorHandlerCalls(t, recorder, 4, supervisorHandlerBudget)

	restartingCalls, crashedCalls := 0, 0
	for _, record := range calls {
		if record.pluginID != meta.ID {
			t.Fatalf("failure handler pluginID = %q, want %q; all calls = %+v", record.pluginID, meta.ID, calls)
		}
		if !record.statusOK {
			t.Fatalf(
				"Status(%q) called inside failure handler for State=%q returned ok=false, want ok=true",
				meta.ID, record.status.State,
			)
		}

		switch record.status.State {
		case StateRestarting:
			restartingCalls++
		case StateCrashed:
			crashedCalls++
		default:
			t.Fatalf(
				"failure handler State = %q, want %q or %q; all calls = %+v",
				record.status.State, StateRestarting, StateCrashed, calls,
			)
		}
	}

	if restartingCalls != 3 {
		t.Fatalf("failure handler restarting calls = %d, want 3 (one per failure); all calls = %+v", restartingCalls, calls)
	}
	if crashedCalls != 1 {
		t.Fatalf("failure handler crashed calls = %d, want 1 (one for entering crashed); all calls = %+v", crashedCalls, calls)
	}

	waitSupervisorState(t, runtime, meta.ID, StateCrashed, supervisorStateBudget)
}

// TestSupervisorUnloadDuringWaitingCancelsRestart covers R1: unloading while the
// plugin waits for a restart cancels the restart, so it is never loaded again.
func TestSupervisorUnloadDuringWaitingCancelsRestart(t *testing.T) {
	setSupervisorPolicy(t, []time.Duration{supervisorSlowBackoff}, 5, 30*time.Minute)

	sink := installSupervisorLogSink(t)
	pluginDir, meta := writeSupervisorPlugin(t, "supervisor-unload-waiting")
	runtime := newSupervisorRuntime(t)

	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load(%q) error = %v, want nil", meta.ID, err)
	}
	waitSupervisorBootCount(t, sink, meta.ID, 1, supervisorStateBudget)

	killSupervisorWorker(t, runtime, meta.ID)
	restarting := waitSupervisorState(t, runtime, meta.ID, StateRestarting, supervisorStateBudget)

	scheduled := restarting.NextRestartAt
	if scheduled.IsZero() {
		scheduled = time.Now().Add(supervisorSlowBackoff)
	}

	ctx, cancel := context.WithTimeout(context.Background(), supervisorStateBudget)
	defer cancel()

	if err := runtime.Unload(ctx, meta.ID); err != nil {
		t.Fatalf("Unload(%q) while restarting error = %v, want nil", meta.ID, err)
	}
	if runtime.IsLoaded(meta.ID) {
		t.Fatalf("IsLoaded(%q) after Unload while restarting = true, want false", meta.ID)
	}
	if status, ok := runtime.Status(meta.ID); ok {
		t.Fatalf("Status(%q) after Unload while restarting = %+v (ok=true), want ok=false", meta.ID, status)
	}

	holdUntil := scheduled.Add(400 * time.Millisecond)
	if floor := time.Now().Add(250 * time.Millisecond); holdUntil.Before(floor) {
		holdUntil = floor
	}
	deadline := time.Now().Add(supervisorStateBudget)
	for time.Now().Before(holdUntil) {
		if runtime.IsLoaded(meta.ID) {
			t.Fatalf(
				"IsLoaded(%q) = true while holding past the cancelled restart at %s, want false",
				meta.ID, scheduled,
			)
		}
		if time.Now().After(deadline) {
			t.Fatalf("holding past cancelled restart at %s exceeded %s", scheduled, supervisorStateBudget)
		}
		time.Sleep(5 * time.Millisecond)
	}

	if got := sink.bootCount(meta.ID); got != 1 {
		t.Fatalf("OnBoot log count after cancelled restart = %d, want 1; the unloaded plugin must not load again", got)
	}
}

// TestSupervisorReloadDuringWaitingLoadsOnce covers R1: ReloadForID while the
// plugin waits loads it exactly once and cancels the scheduled restart.
func TestSupervisorReloadDuringWaitingLoadsOnce(t *testing.T) {
	setSupervisorPolicy(t, []time.Duration{supervisorSlowBackoff}, 5, 30*time.Minute)

	sink := installSupervisorLogSink(t)
	pluginDir, meta := writeSupervisorPlugin(t, "supervisor-reload-waiting")
	runtime := newSupervisorRuntime(t)

	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load(%q) error = %v, want nil", meta.ID, err)
	}
	waitSupervisorBootCount(t, sink, meta.ID, 1, supervisorStateBudget)

	killSupervisorWorker(t, runtime, meta.ID)
	restarting := waitSupervisorState(t, runtime, meta.ID, StateRestarting, supervisorStateBudget)

	scheduled := restarting.NextRestartAt
	if scheduled.IsZero() {
		scheduled = time.Now().Add(supervisorSlowBackoff)
	}

	ctx, cancel := context.WithTimeout(context.Background(), supervisorStateBudget)
	defer cancel()

	if err := runtime.ReloadForID(ctx, meta.ID, pluginDir); err != nil {
		t.Fatalf("ReloadForID(%q) while restarting error = %v, want nil", meta.ID, err)
	}

	waitSupervisorState(t, runtime, meta.ID, StateRunning, supervisorStateBudget)
	waitSupervisorBootCount(t, sink, meta.ID, 2, supervisorStateBudget)

	holdUntil := scheduled.Add(500 * time.Millisecond)
	if floor := time.Now().Add(300 * time.Millisecond); holdUntil.Before(floor) {
		holdUntil = floor
	}
	deadline := time.Now().Add(supervisorStateBudget)
	for time.Now().Before(holdUntil) {
		if !runtime.IsLoaded(meta.ID) {
			t.Fatalf("IsLoaded(%q) = false after ReloadForID while restarting, want true", meta.ID)
		}
		status, ok := runtime.Status(meta.ID)
		if !ok || status.State != StateRunning {
			t.Fatalf(
				"Status(%q) = %+v (ok=%t) after ReloadForID while holding past the scheduled restart at %s, want State=%q (ok=true)",
				meta.ID, status, ok, scheduled, StateRunning,
			)
		}
		if got := sink.bootCount(meta.ID); got != 2 {
			t.Fatalf(
				"OnBoot log count after ReloadForID while holding past the scheduled restart = %d, want 2; the scheduled restart must be cancelled",
				got,
			)
		}
		if time.Now().After(deadline) {
			t.Fatalf("holding past scheduled restart at %s exceeded %s", scheduled, supervisorStateBudget)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestSupervisorDetectsIdleWorkerExit covers R1: the supervisor notices a worker
// that dies while idle, without waiting for the next call, and restarts it.
func TestSupervisorDetectsIdleWorkerExit(t *testing.T) {
	setSupervisorPolicy(t, []time.Duration{supervisorFastBackoff}, 5, 30*time.Minute)

	sink := installSupervisorLogSink(t)
	pluginDir, meta := writeSupervisorPlugin(t, "supervisor-idle-exit")
	runtime := newSupervisorRuntime(t)

	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load(%q) error = %v, want nil", meta.ID, err)
	}
	waitSupervisorBootCount(t, sink, meta.ID, 1, supervisorStateBudget)

	killSupervisorWorker(t, runtime, meta.ID)

	restarting := waitSupervisorState(t, runtime, meta.ID, StateRestarting, supervisorStateBudget)
	if restarting.Faults != 1 {
		t.Fatalf("Status(%q).Faults after an idle worker exit = %d, want 1", meta.ID, restarting.Faults)
	}
	if restarting.LastError == "" {
		t.Fatalf("Status(%q).LastError after an idle worker exit = %q, want a non-empty failure reason", meta.ID, restarting.LastError)
	}

	waitSupervisorState(t, runtime, meta.ID, StateRunning, supervisorStateBudget)
	waitSupervisorBootCount(t, sink, meta.ID, 2, supervisorStateBudget)
}

// TestSupervisorFaultsOnlyCountWithinCrashWindow covers R1: a fault outside the
// crash window does not count, so the second failure reports Faults=1.
func TestSupervisorFaultsOnlyCountWithinCrashWindow(t *testing.T) {
	setSupervisorPolicy(t, []time.Duration{supervisorFastBackoff}, 5, supervisorCrashWindow)

	pluginDir, meta := writeSupervisorPlugin(t, "supervisor-fault-window")
	runtime := newSupervisorRuntime(t)

	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load(%q) error = %v, want nil", meta.ID, err)
	}
	waitSupervisorState(t, runtime, meta.ID, StateRunning, supervisorStateBudget)

	killSupervisorWorker(t, runtime, meta.ID)
	first := waitSupervisorState(t, runtime, meta.ID, StateRestarting, supervisorStateBudget)
	if first.Faults != 1 {
		t.Fatalf("Status(%q).Faults after the first failure = %d, want 1", meta.ID, first.Faults)
	}

	failureAt := first.LastFailureAt
	if failureAt.IsZero() {
		failureAt = time.Now()
	}

	waitSupervisorState(t, runtime, meta.ID, StateRunning, supervisorStateBudget)

	holdUntil := failureAt.Add(crashWindow + 250*time.Millisecond)
	deadline := time.Now().Add(supervisorStateBudget)
	for time.Now().Before(holdUntil) {
		if time.Now().After(deadline) {
			t.Fatalf("waiting past the %s crash window from %s exceeded %s", crashWindow, failureAt, supervisorStateBudget)
		}
		time.Sleep(10 * time.Millisecond)
	}

	killSupervisorWorker(t, runtime, meta.ID)
	second := waitSupervisorState(t, runtime, meta.ID, StateRestarting, supervisorStateBudget)
	if second.Faults != 1 {
		t.Fatalf(
			"Status(%q).Faults after a second failure %s after the first = %d, want 1 because the crash window is %s",
			meta.ID, time.Since(failureAt), second.Faults, crashWindow,
		)
	}
}

// TestSupervisorNormalUnloadAndCloseDoNotReportFailures covers R1: a normal
// Unload and Close are not worker failures and must not call the handler.
func TestSupervisorNormalUnloadAndCloseDoNotReportFailures(t *testing.T) {
	setSupervisorPolicy(t, []time.Duration{supervisorFastBackoff}, 5, 30*time.Minute)

	pluginDir, meta := writeSupervisorPlugin(t, "supervisor-normal-lifecycle")
	runtime := newSupervisorRuntime(t)

	var (
		mu        sync.Mutex
		handlerID []string
	)
	runtime.SetFailureHandler(func(pluginID string, status PluginStatus) {
		mu.Lock()
		defer mu.Unlock()
		handlerID = append(handlerID, pluginID)
	})

	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load(%q) error = %v, want nil", meta.ID, err)
	}
	status, ok := runtime.Status(meta.ID)
	if !ok || status.State != StateRunning {
		t.Fatalf("Status(%q) after Load = %+v (ok=%t), want State=%q (ok=true)", meta.ID, status, ok, StateRunning)
	}

	if err := runtime.Unload(context.Background(), meta.ID); err != nil {
		t.Fatalf("Unload(%q) error = %v, want nil", meta.ID, err)
	}
	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load(%q) after Unload error = %v, want nil", meta.ID, err)
	}
	runtime.Close()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		calls := append([]string(nil), handlerID...)
		mu.Unlock()
		if len(calls) != 0 {
			t.Fatalf("failure handler calls after normal Unload/Close = %v, want none", calls)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestSupervisorStatusReportsLoadedAndUnknownPlugins covers R1: Status reports
// ok=true only while the plugin is in the registry.
func TestSupervisorStatusReportsLoadedAndUnknownPlugins(t *testing.T) {
	setSupervisorPolicy(t, []time.Duration{supervisorFastBackoff}, 5, 30*time.Minute)

	pluginDir, meta := writeSupervisorPlugin(t, "supervisor-status")
	runtime := newSupervisorRuntime(t)

	if status, ok := runtime.Status("supervisor-never-loaded"); ok {
		t.Fatalf("Status(%q) before load = %+v (ok=true), want ok=false", "supervisor-never-loaded", status)
	}

	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load(%q) error = %v, want nil", meta.ID, err)
	}
	status, ok := runtime.Status(meta.ID)
	if !ok {
		t.Fatalf("Status(%q) after Load = %+v (ok=false), want ok=true with State=%q", meta.ID, status, StateRunning)
	}
	if status.State != StateRunning {
		t.Fatalf("Status(%q).State after Load = %q, want %q", meta.ID, status.State, StateRunning)
	}
	if status.Faults != 0 {
		t.Fatalf("Status(%q).Faults after Load = %d, want 0", meta.ID, status.Faults)
	}

	if err := runtime.Unload(context.Background(), meta.ID); err != nil {
		t.Fatalf("Unload(%q) error = %v, want nil", meta.ID, err)
	}
	if status, ok := runtime.Status(meta.ID); ok {
		t.Fatalf("Status(%q) after Unload = %+v (ok=true), want ok=false", meta.ID, status)
	}
}
