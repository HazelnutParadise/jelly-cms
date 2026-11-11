package plugin

// Regression tests for the PR #53 review fixes around Runtime.HoldRestart.
//
// HoldRestart is the contract a caller uses before replacing a plugin's files:
// it cancels a scheduled automatic restart, waits for a restart that is already
// installing a candidate worker, and returns a release that resumes the deferred
// restart. These tests are the contract; implementers must not edit this file.
// The current skeleton returns a no-op release, so
// TestSupervisorHoldRestartDefersScheduledRestart and
// TestSupervisorHoldRestartWaitsForInFlightRestart must fail until HoldRestart
// is implemented.
//
// Every plugin runs as a real worker subprocess. Tests replace the package-level
// restart policy at the start and restore it with t.Cleanup. All waits poll with
// a bounded loop; a fixed sleep is never followed directly by an assertion.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	// supervisorHoldBackoff keeps a failed plugin in StateRestarting long
	// enough for the test to hold the restart before its timer fires.
	supervisorHoldBackoff = 200 * time.Millisecond
	// supervisorHoldFastBackoff lets a restart start almost immediately so the
	// test can catch it while the new worker is still booting.
	supervisorHoldFastBackoff = 10 * time.Millisecond
	// supervisorHoldBusyBoot is the OnBoot busy loop of the in-flight restart
	// fixture. It stays well under the one second worker step limit.
	supervisorHoldBusyBootMS = 300
)

// writeSupervisorHoldPlugin writes a plugin whose OnBoot logs a start marker,
// busy loops for supervisorHoldBusyBootMS and then logs an end marker. The two
// markers let a test see a restart while it is still installing.
func writeSupervisorHoldPlugin(t *testing.T, id string) (string, PluginMetadata) {
	t.Helper()

	pluginDir := t.TempDir()
	meta := PluginMetadata{
		ID:         id,
		Name:       id,
		Version:    "1.0.0",
		Entrypoint: "index.js",
	}

	manifest, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal manifest for %q error = %v, want nil", id, err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), manifest, 0o600); err != nil {
		t.Fatalf("write manifest for %q error = %v, want nil", id, err)
	}

	script := fmt.Sprintf(`
JellyCMS.registerHook("OnBoot", function (data) {
  JellyCMS.log.info("supervisor-hold-boot-start:%s");
  var until = Date.now() + %d;
  while (Date.now() < until) {}
  JellyCMS.log.info("supervisor-hold-boot-end:%s");
});
`, id, supervisorHoldBusyBootMS, id)
	if err := os.WriteFile(filepath.Join(pluginDir, meta.Entrypoint), []byte(script), 0o600); err != nil {
		t.Fatalf("write entrypoint for %q error = %v, want nil", id, err)
	}

	return pluginDir, meta
}

// supervisorSinkMessageCount counts exact log messages for one plugin.
func supervisorSinkMessageCount(sink *supervisorLogSink, pluginID, message string) int {
	sink.mu.Lock()
	defer sink.mu.Unlock()

	count := 0
	for _, line := range sink.lines {
		if line.plugin == pluginID && line.message == message {
			count++
		}
	}

	return count
}

// waitSupervisorMessageCount polls until the plugin logged message want times.
func waitSupervisorMessageCount(t *testing.T, sink *supervisorLogSink, pluginID, message string, want int, budget time.Duration) {
	t.Helper()

	deadline := time.Now().Add(budget)
	for {
		got := supervisorSinkMessageCount(sink, pluginID, message)
		if got == want {
			return
		}
		if got > want {
			t.Fatalf("log message %q for plugin %q count = %d, want %d", message, pluginID, got, want)
		}
		if time.Now().After(deadline) {
			t.Fatalf("log message %q for plugin %q count = %d, want %d within %s", message, pluginID, got, want, budget)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestSupervisorHoldRestartDefersScheduledRestart covers the first review fix: a
// hold cancels the scheduled restart, keeps the plugin restarting while held,
// and release lets the restart run exactly once.
func TestSupervisorHoldRestartDefersScheduledRestart(t *testing.T) {
	setSupervisorPolicy(t, []time.Duration{supervisorHoldBackoff}, 5, 30*time.Minute)

	sink := installSupervisorLogSink(t)
	pluginDir, meta := writeSupervisorPlugin(t, "supervisor-hold-defer")
	runtime := newSupervisorRuntime(t)

	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load(%q) error = %v, want nil", meta.ID, err)
	}
	waitSupervisorBootCount(t, sink, meta.ID, 1, supervisorStateBudget)

	if err := runtime.Invoke(context.Background(), meta.ID, supervisorSlowHook, map[string]interface{}{"case": "hold-timeout"}); err == nil {
		t.Fatalf("Invoke(%q, %q) on a runaway hook error = nil, want a timeout error", meta.ID, supervisorSlowHook)
	}
	waitSupervisorState(t, runtime, meta.ID, StateRestarting, supervisorStateBudget)

	release, err := runtime.HoldRestart(context.Background(), meta.ID)
	if err != nil {
		t.Fatalf("HoldRestart(%q) error = %v, want nil", meta.ID, err)
	}
	if release == nil {
		t.Fatalf("HoldRestart(%q) release = nil, want a non-nil release", meta.ID)
	}

	holdDeadline := time.Now().Add(time.Second)
	for time.Now().Before(holdDeadline) {
		status, ok := runtime.Status(meta.ID)
		if !ok || status.State != StateRestarting {
			t.Fatalf(
				"Status(%q) while held = %+v (ok=%t), want State=%q; a held restart must not run",
				meta.ID, status, ok, StateRestarting,
			)
		}
		if got := sink.bootCount(meta.ID); got != 1 {
			t.Fatalf(
				"OnBoot log count for plugin %q while held = %d, want 1; a held restart must not load the plugin",
				meta.ID, got,
			)
		}
		time.Sleep(10 * time.Millisecond)
	}

	release()

	waitSupervisorState(t, runtime, meta.ID, StateRunning, supervisorStateBudget)
	waitSupervisorBootCount(t, sink, meta.ID, 2, supervisorStateBudget)
}

// TestSupervisorHoldRestartWaitsForInFlightRestart covers the second review fix:
// a hold taken while a restart is installing its candidate waits for that
// restart to finish, so the caller never swaps files under a half-started
// worker.
func TestSupervisorHoldRestartWaitsForInFlightRestart(t *testing.T) {
	setSupervisorPolicy(t, []time.Duration{supervisorHoldFastBackoff}, 5, 30*time.Minute)

	sink := installSupervisorLogSink(t)
	pluginDir, meta := writeSupervisorHoldPlugin(t, "supervisor-hold-in-flight")
	runtime := newSupervisorRuntime(t)

	startMessage := "supervisor-hold-boot-start:" + meta.ID
	endMessage := "supervisor-hold-boot-end:" + meta.ID

	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load(%q) error = %v, want nil", meta.ID, err)
	}
	waitSupervisorMessageCount(t, sink, meta.ID, endMessage, 1, supervisorStateBudget)

	killSupervisorWorker(t, runtime, meta.ID)
	waitSupervisorState(t, runtime, meta.ID, StateRestarting, supervisorStateBudget)
	waitSupervisorMessageCount(t, sink, meta.ID, startMessage, 2, supervisorStateBudget)

	release, err := runtime.HoldRestart(context.Background(), meta.ID)
	if err != nil {
		t.Fatalf("HoldRestart(%q) error = %v, want nil", meta.ID, err)
	}
	if release == nil {
		t.Fatalf("HoldRestart(%q) release = nil, want a non-nil release", meta.ID)
	}

	if got := supervisorSinkMessageCount(sink, meta.ID, endMessage); got != 2 {
		t.Fatalf(
			"OnBoot completions for plugin %q when HoldRestart returned = %d, want 2; HoldRestart must wait for the in-flight restart to finish",
			meta.ID, got,
		)
	}

	boots := supervisorSinkMessageCount(sink, meta.ID, endMessage)
	holdDeadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(holdDeadline) {
		if status, ok := runtime.Status(meta.ID); !ok || status.State != StateRunning {
			t.Fatalf(
				"Status(%q) while held after the in-flight restart = %+v (ok=%t), want State=%q",
				meta.ID, status, ok, StateRunning,
			)
		}
		if got := supervisorSinkMessageCount(sink, meta.ID, endMessage); got != boots {
			t.Fatalf(
				"OnBoot completions for plugin %q while held = %d, want %d; a held plugin must not restart",
				meta.ID, got, boots,
			)
		}
		time.Sleep(10 * time.Millisecond)
	}

	release()
}

// TestSupervisorHoldRestartUnknownPluginIsNoop covers the review fix for callers
// that hold an id which is not loaded: the hold is a no-op, not an error.
func TestSupervisorHoldRestartUnknownPluginIsNoop(t *testing.T) {
	runtime := newSupervisorRuntime(t)

	release, err := runtime.HoldRestart(context.Background(), "supervisor-hold-never-loaded")
	if err != nil {
		t.Fatalf("HoldRestart(%q) error = %v, want nil", "supervisor-hold-never-loaded", err)
	}
	if release == nil {
		t.Fatalf("HoldRestart(%q) release = nil, want a non-nil no-op release", "supervisor-hold-never-loaded")
	}

	release()
}

// TestSupervisorHoldRestartNestsWithReload covers the review fix that a hold
// does not block an admin reload and that releasing afterwards does not schedule
// another load.
func TestSupervisorHoldRestartNestsWithReload(t *testing.T) {
	backoff := 2 * time.Second
	setSupervisorPolicy(t, []time.Duration{backoff}, 5, 30*time.Minute)

	sink := installSupervisorLogSink(t)
	pluginDir, meta := writeSupervisorPlugin(t, "supervisor-hold-reload")
	runtime := newSupervisorRuntime(t)

	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load(%q) error = %v, want nil", meta.ID, err)
	}
	waitSupervisorBootCount(t, sink, meta.ID, 1, supervisorStateBudget)

	killSupervisorWorker(t, runtime, meta.ID)
	restarting := waitSupervisorState(t, runtime, meta.ID, StateRestarting, supervisorStateBudget)

	scheduled := restarting.NextRestartAt
	if scheduled.IsZero() {
		scheduled = time.Now().Add(backoff)
	}

	release, err := runtime.HoldRestart(context.Background(), meta.ID)
	if err != nil {
		t.Fatalf("HoldRestart(%q) error = %v, want nil", meta.ID, err)
	}
	if release == nil {
		t.Fatalf("HoldRestart(%q) release = nil, want a non-nil release", meta.ID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), supervisorStateBudget)
	defer cancel()

	if err := runtime.ReloadForID(ctx, meta.ID, pluginDir); err != nil {
		t.Fatalf("ReloadForID(%q) while held error = %v, want nil", meta.ID, err)
	}
	waitSupervisorState(t, runtime, meta.ID, StateRunning, supervisorStateBudget)
	waitSupervisorBootCount(t, sink, meta.ID, 2, supervisorStateBudget)

	release()

	holdUntil := scheduled.Add(500 * time.Millisecond)
	if floor := time.Now().Add(500 * time.Millisecond); holdUntil.Before(floor) {
		holdUntil = floor
	}
	deadline := time.Now().Add(supervisorStateBudget)
	for time.Now().Before(holdUntil) {
		if status, ok := runtime.Status(meta.ID); !ok || status.State != StateRunning {
			t.Fatalf(
				"Status(%q) after releasing a hold that nested with a reload = %+v (ok=%t), want State=%q",
				meta.ID, status, ok, StateRunning,
			)
		}
		if got := sink.bootCount(meta.ID); got != 2 {
			t.Fatalf(
				"OnBoot log count for plugin %q after releasing a hold that nested with a reload = %d, want 2; only the reload may load the plugin",
				meta.ID, got,
			)
		}
		if time.Now().After(deadline) {
			t.Fatalf("observing plugin %q past the cancelled restart at %s exceeded %s", meta.ID, scheduled, supervisorStateBudget)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
