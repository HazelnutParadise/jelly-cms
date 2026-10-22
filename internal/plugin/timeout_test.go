package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	timeoutCaseEnv            = "JELLY_PLUGIN_TIMEOUT_CASE"
	timeoutChildMarkerPrefix  = "JELLY_PLUGIN_TIMEOUT_CHILD_OK:"
	timeoutParentMarkerPrefix = "JELLY_PLUGIN_TIMEOUT_PARENT_OK:"

	timeoutCaseLoadEntryLoop   = "load-entry-loop"
	timeoutCaseHookLoop        = "hook-loop"
	timeoutCaseReloadEntryLoop = "reload-entry-loop"
	timeoutCaseShutdownLoop    = "shutdown-loop"
)

const (
	// timeoutParentHardLimit bounds one child test process. A stuck plugin runs
	// inside that child, so killing it is the last line of defence for go test.
	timeoutParentHardLimit = 5 * time.Second

	// timeoutCallerDeadline is passed to Load, Reload and Unload. It stays below
	// the parent hard limit so the runtime has time to stop the worker and report
	// the cancellation before the child is killed.
	timeoutCallerDeadline = 3 * time.Second

	timeoutHookBudget     = 2 * time.Second
	timeoutShutdownBudget = 2 * time.Second
	timeoutCloseBudget    = 2 * time.Second
)

// TestPluginTimeout runs every timeout case in its own re-executed test binary.
// The child is killed after a hard deadline, which keeps an infinite JavaScript
// loop from hanging the whole go test run. Until Load, Reload, CallHook and
// Unload actually bound plugin execution, every case is expected to be red.
func TestPluginTimeout(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}

	cases := []string{
		timeoutCaseLoadEntryLoop,
		timeoutCaseHookLoop,
		timeoutCaseReloadEntryLoop,
		timeoutCaseShutdownLoop,
	}

	for _, name := range cases {
		name := name

		t.Run(name, func(t *testing.T) {
			childTemp, err := os.MkdirTemp("", "jelly-plugin-timeout-*")
			if err != nil {
				t.Fatalf("create child temp directory: %v", err)
			}
			defer func() {
				_ = os.RemoveAll(childTemp)
			}()

			ctx, cancel := context.WithTimeout(context.Background(), timeoutParentHardLimit)
			defer cancel()

			cmd := exec.CommandContext(ctx, executable, "-test.v", "-test.run=^TestPluginTimeoutHelper$")
			cmd.Env = timeoutChildEnv(map[string]string{
				timeoutCaseEnv: name,
				// A killed child does not run t.TempDir cleanup. Give it an
				// isolated temp root the parent can remove unconditionally.
				"TMPDIR": childTemp,
				"TMP":    childTemp,
				"TEMP":   childTemp,
			})
			cmd.WaitDelay = 500 * time.Millisecond

			output, runErr := cmd.CombinedOutput()
			if ctx.Err() == context.DeadlineExceeded {
				t.Fatalf(
					"case %s exceeded the %s hard deadline, so the child was killed; output:\n%s",
					name, timeoutParentHardLimit, output,
				)
			}
			if runErr != nil {
				t.Fatalf("case %s child failed: %v; output:\n%s", name, runErr, output)
			}
			if !strings.Contains(string(output), timeoutChildMarkerPrefix+name) {
				t.Fatalf(
					"case %s child exited 0 without success marker %q; output:\n%s",
					name, timeoutChildMarkerPrefix+name, output,
				)
			}

			fmt.Printf("%s%s\n", timeoutParentMarkerPrefix, name)
		})
	}
}

// TestPluginTimeoutHelper is the subprocess side of TestPluginTimeout. It only
// runs when the dedicated environment variable selects a case.
func TestPluginTimeoutHelper(t *testing.T) {
	name := os.Getenv(timeoutCaseEnv)
	if name == "" {
		t.Skip("subprocess helper for TestPluginTimeout")
	}

	switch name {
	case timeoutCaseLoadEntryLoop:
		runTimeoutLoadEntryLoop(t)
	case timeoutCaseHookLoop:
		runTimeoutHookLoop(t)
	case timeoutCaseReloadEntryLoop:
		runTimeoutReloadEntryLoop(t)
	case timeoutCaseShutdownLoop:
		runTimeoutShutdownLoop(t)
	default:
		t.Fatalf("unknown timeout case %q", name)
	}

	if t.Failed() {
		return
	}

	fmt.Printf("%s%s\n", timeoutChildMarkerPrefix, name)
}

// runTimeoutLoadEntryLoop expects Load to reject a plugin whose entry script
// never returns, and the failed candidate must not enter the registry.
func runTimeoutLoadEntryLoop(t *testing.T) {
	t.Helper()

	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	defer closeRuntimeWithin(t, runtime, timeoutCloseBudget)

	pluginDir, meta := writeTimeoutPlugin(t, "timeout-load", `
JellyCMS.log.info("timeout-load-entry-started");
while (true) {}
`)

	ctx, cancel := timeoutOperationContext()
	defer cancel()

	var loadErr error
	output := captureStdout(t, func() {
		loadErr = runtime.Load(ctx, pluginDir)
	})
	if loadErr == nil {
		t.Fatalf("Load() with an infinite entry loop error = nil, want timeout error; output = %q", output)
	}
	if !strings.Contains(output, "timeout-load-entry-started") {
		t.Fatalf("Load() did not start the infinite entry script; output = %q", output)
	}
	if _, err := runtime.GetPlugin(meta.ID); err == nil {
		t.Fatalf("GetPlugin(%q) after timed out Load() error = nil, want plugin not found", meta.ID)
	}
}

// runTimeoutHookLoop expects one runaway callback to be stopped without
// blocking a healthy plugin, and the following Unload and Close must still
// finish.
func runTimeoutHookLoop(t *testing.T) {
	t.Helper()

	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	defer closeRuntimeWithin(t, runtime, timeoutCloseBudget)

	badDir, badMeta := writeTimeoutPlugin(t, "timeout-hook-bad", `
JellyCMS.registerHook("OnTimeoutProbe", function (data) {
  JellyCMS.log.info("timeout-hook-bad-started");
  while (true) {}
});
`)
	healthyDir, healthyMeta := writeTimeoutPlugin(t, "timeout-hook-healthy", `
JellyCMS.registerHook("OnTimeoutProbe", function (data) {
  JellyCMS.log.info("timeout-healthy:" + data.value);
});
`)

	var loadErr error
	captureStdout(t, func() {
		loadErr = runtime.Load(context.Background(), badDir)
	})
	if loadErr != nil {
		t.Fatalf("Load() bad plugin error = %v", loadErr)
	}

	captureStdout(t, func() {
		loadErr = runtime.Load(context.Background(), healthyDir)
	})
	if loadErr != nil {
		t.Fatalf("Load() healthy plugin error = %v", loadErr)
	}

	start := time.Now()
	callOutput := captureStdout(t, func() {
		runtime.CallHook(Hook("OnTimeoutProbe"), map[string]interface{}{"value": "after-timeout"})
	})
	if elapsed := time.Since(start); elapsed > timeoutHookBudget {
		t.Fatalf(
			"CallHook() with an infinite callback took %s, want at most %s; output = %q",
			elapsed, timeoutHookBudget, callOutput,
		)
	}
	if !strings.Contains(callOutput, "timeout-hook-bad-started") {
		t.Fatalf("CallHook() did not start the infinite callback; output = %q", callOutput)
	}
	if !strings.Contains(callOutput, "timeout-healthy:after-timeout") {
		t.Fatalf("CallHook() did not run the healthy plugin callback; output = %q", callOutput)
	}

	ctx, cancel := timeoutOperationContext()
	defer cancel()

	start = time.Now()
	var unloadErr error
	captureStdout(t, func() {
		unloadErr = runtime.Unload(ctx, badMeta.ID)
	})
	if elapsed := time.Since(start); elapsed > timeoutShutdownBudget {
		t.Fatalf(
			"Unload(%q) after a timed out hook took %s, want at most %s",
			badMeta.ID, elapsed, timeoutShutdownBudget,
		)
	}
	if unloadErr != nil {
		t.Logf("Unload(%q) returned %v after the hook timeout", badMeta.ID, unloadErr)
	}
	if _, err := runtime.GetPlugin(badMeta.ID); err == nil {
		t.Fatalf("GetPlugin(%q) after Unload() error = nil, want plugin not found", badMeta.ID)
	}
	if _, err := runtime.GetPlugin(healthyMeta.ID); err != nil {
		t.Fatalf("healthy plugin %q disappeared: %v", healthyMeta.ID, err)
	}
}

// runTimeoutReloadEntryLoop expects a failed reload to report an error and keep
// the old plugin serving hooks.
func runTimeoutReloadEntryLoop(t *testing.T) {
	t.Helper()

	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	defer closeRuntimeWithin(t, runtime, timeoutCloseBudget)

	pluginDir, meta := writeTimeoutPlugin(t, "timeout-reload", `
JellyCMS.registerHook("OnTimeoutReload", function (data) {
  JellyCMS.log.info("timeout-reload-old:" + data.value);
});
`)

	var loadErr error
	captureStdout(t, func() {
		loadErr = runtime.Load(context.Background(), pluginDir)
	})
	if loadErr != nil {
		t.Fatalf("Load() old plugin error = %v", loadErr)
	}

	if err := os.WriteFile(filepath.Join(pluginDir, meta.Entrypoint), []byte(`
JellyCMS.log.info("timeout-reload-new-entry-started");
while (true) {}
`), 0o600); err != nil {
		t.Fatalf("replace plugin entrypoint: %v", err)
	}

	ctx, cancel := timeoutOperationContext()
	defer cancel()

	var reloadErr error
	output := captureStdout(t, func() {
		reloadErr = runtime.Reload(ctx, pluginDir)
	})
	if reloadErr == nil {
		t.Fatalf("Reload() with an infinite entry loop error = nil, want timeout error; output = %q", output)
	}
	if !strings.Contains(output, "timeout-reload-new-entry-started") {
		t.Fatalf("Reload() did not start the infinite entry script; output = %q", output)
	}

	if _, err := runtime.GetPlugin(meta.ID); err != nil {
		t.Fatalf("GetPlugin(%q) after failed Reload() error = %v, want old plugin retained", meta.ID, err)
	}

	callOutput := captureStdout(t, func() {
		runtime.CallHook(Hook("OnTimeoutReload"), map[string]interface{}{"value": "old-version"})
	})
	if !strings.Contains(callOutput, "timeout-reload-old:old-version") {
		t.Fatalf("old plugin callback did not run after failed Reload(); output = %q", callOutput)
	}

	var unloadErr error
	captureStdout(t, func() {
		unloadErr = runtime.Unload(ctx, meta.ID)
	})
	if unloadErr != nil {
		t.Fatalf("Unload() after failed Reload() error = %v", unloadErr)
	}
}

// runTimeoutShutdownLoop expects Unload to stop an infinite OnShutdown callback
// without waiting forever, and the plugin must be gone from the registry.
func runTimeoutShutdownLoop(t *testing.T) {
	t.Helper()

	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	defer closeRuntimeWithin(t, runtime, timeoutCloseBudget)

	pluginDir, meta := writeTimeoutPlugin(t, "timeout-shutdown", `
JellyCMS.registerHook("OnShutdown", function (data) {
  JellyCMS.log.info("timeout-shutdown-started");
  while (true) {}
});
`)

	var loadErr error
	captureStdout(t, func() {
		loadErr = runtime.Load(context.Background(), pluginDir)
	})
	if loadErr != nil {
		t.Fatalf("Load() error = %v", loadErr)
	}

	ctx, cancel := timeoutOperationContext()
	defer cancel()

	start := time.Now()
	var unloadErr error
	unloadOutput := captureStdout(t, func() {
		unloadErr = runtime.Unload(ctx, meta.ID)
	})
	if elapsed := time.Since(start); elapsed > timeoutShutdownBudget {
		t.Fatalf(
			"Unload() with an infinite OnShutdown took %s, want at most %s",
			elapsed, timeoutShutdownBudget,
		)
	}
	if !strings.Contains(unloadOutput, "timeout-shutdown-started") {
		t.Fatalf("Unload() did not start the infinite OnShutdown callback; output = %q", unloadOutput)
	}
	if unloadErr != nil {
		t.Logf("Unload(%q) returned %v after the shutdown timeout", meta.ID, unloadErr)
	}
	if _, err := runtime.GetPlugin(meta.ID); err == nil {
		t.Fatalf("GetPlugin(%q) after Unload() error = nil, want plugin not found", meta.ID)
	}
}

// writeTimeoutPlugin uses the shared test plugin fixture and then gives the
// manifest a case-specific id so two plugins can live in one runtime.
func writeTimeoutPlugin(t *testing.T, id, entrypoint string) (string, PluginMetadata) {
	t.Helper()

	pluginDir, _ := writeTestPlugin(t, entrypoint)
	meta := PluginMetadata{
		ID:         id,
		Name:       id,
		Version:    "1.0.0",
		Entrypoint: "index.js",
	}

	manifest, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal plugin manifest for %s: %v", id, err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), manifest, 0o600); err != nil {
		t.Fatalf("write plugin manifest for %s: %v", id, err)
	}

	return pluginDir, meta
}

func timeoutOperationContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), timeoutCallerDeadline)
}

func timeoutChildEnv(extras map[string]string) []string {
	replaced := make(map[string]struct{}, len(extras))
	for key := range extras {
		replaced[key] = struct{}{}
	}

	env := make([]string, 0, len(os.Environ())+len(extras))
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, drop := replaced[key]; drop {
				continue
			}
		}
		env = append(env, entry)
	}
	for key, value := range extras {
		env = append(env, key+"="+value)
	}

	return env
}

func closeRuntimeWithin(t *testing.T, runtime *Runtime, limit time.Duration) {
	t.Helper()

	start := time.Now()
	runtime.Close()
	if elapsed := time.Since(start); elapsed > limit {
		t.Errorf("Close() took %s, want at most %s", elapsed, limit)
	}
}
