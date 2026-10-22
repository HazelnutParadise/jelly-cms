package plugin

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	// sandboxCleanupDirPrefix is the exact prefix the worker uses for its
	// isolated work directory. Only own directories are inspected.
	sandboxCleanupDirPrefix = "jelly-plugin-"

	// sandboxCleanupLogSettle gives the detached plugin log writer a moment to
	// flush a marker that the worker emitted while a request was running.
	sandboxCleanupLogSettle = 150 * time.Millisecond
)

// TestPluginSandboxCleanupAfterLoadClose verifies that the sandbox directory a
// worker creates in the inherited TMPDIR is reclaimed when a plugin is loaded
// and then closed through the public runtime API.
func TestPluginSandboxCleanupAfterLoadClose(t *testing.T) {
	tempRoot := isolateSandboxTempDir(t)

	pluginDir, meta := writeTimeoutPlugin(t, "cleanup-normal", `
JellyCMS.registerHook("OnBoot", function (data) {
  JellyCMS.log.info("sandbox-cleanup-boot:" + data.plugin);
});
`)

	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	ctx, cancel := timeoutOperationContext()
	defer cancel()

	loadOutput, _, loadErr := runSandboxStep(t, "Load()", timeoutParentHardLimit, sandboxCleanupLogSettle, func() error {
		return runtime.Load(ctx, pluginDir)
	})
	if loadErr != nil {
		t.Fatalf("Load() error = %v; output = %q", loadErr, loadOutput)
	}
	if _, err := runtime.GetPlugin(meta.ID); err != nil {
		t.Fatalf("GetPlugin(%q) after Load() error = %v", meta.ID, err)
	}
	if !strings.Contains(loadOutput, "sandbox-cleanup-boot:"+meta.ID) {
		t.Fatalf("Load() did not run the plugin OnBoot callback; output = %q", loadOutput)
	}

	loaded := sandboxWorkDirs(t, tempRoot)
	if len(loaded) != 1 {
		t.Fatalf(
			"after Load() found %d %q directories in TMPDIR %s, want exactly one worker directory: %v",
			len(loaded), sandboxCleanupDirPrefix, tempRoot, loaded,
		)
	}

	_, closeElapsed, closeErr := runSandboxStep(t, "Close()", timeoutParentHardLimit, 0, func() error {
		runtime.Close()

		return nil
	})
	if closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}
	if closeElapsed > timeoutCloseBudget {
		t.Errorf("Close() took %s, want at most %s", closeElapsed, timeoutCloseBudget)
	}

	assertSandboxWorkDirsGone(t, tempRoot, timeoutCloseBudget)
}

// TestPluginSandboxCleanupAfterHookTimeoutClose verifies that a worker whose
// hook never returns is still reclaimed by Close, with no sandbox directory
// left behind in the inherited TMPDIR.
func TestPluginSandboxCleanupAfterHookTimeoutClose(t *testing.T) {
	tempRoot := isolateSandboxTempDir(t)

	pluginDir, meta := writeTimeoutPlugin(t, "cleanup-hook-timeout", `
JellyCMS.registerHook("OnCleanupProbe", function (data) {
  JellyCMS.log.info("sandbox-cleanup-hook-started");
  while (true) {}
});
`)

	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	ctx, cancel := timeoutOperationContext()
	defer cancel()

	loadOutput, _, loadErr := runSandboxStep(t, "Load()", timeoutParentHardLimit, 0, func() error {
		return runtime.Load(ctx, pluginDir)
	})
	if loadErr != nil {
		t.Fatalf("Load() error = %v; output = %q", loadErr, loadOutput)
	}
	if _, err := runtime.GetPlugin(meta.ID); err != nil {
		t.Fatalf("GetPlugin(%q) after Load() error = %v", meta.ID, err)
	}

	loaded := sandboxWorkDirs(t, tempRoot)
	if len(loaded) != 1 {
		t.Fatalf(
			"after Load() found %d %q directories in TMPDIR %s, want exactly one worker directory: %v",
			len(loaded), sandboxCleanupDirPrefix, tempRoot, loaded,
		)
	}

	hookOutput, _, hookErr := runSandboxStep(t, "CallHook()", timeoutParentHardLimit, sandboxCleanupLogSettle, func() error {
		runtime.CallHook(Hook("OnCleanupProbe"), map[string]interface{}{"plugin": meta.ID})

		return nil
	})
	if hookErr != nil {
		t.Fatalf("CallHook() error = %v; output = %q", hookErr, hookOutput)
	}
	if !strings.Contains(hookOutput, "sandbox-cleanup-hook-started") {
		t.Fatalf("CallHook() did not execute the infinite plugin callback; output = %q", hookOutput)
	}

	_, closeElapsed, closeErr := runSandboxStep(t, "Close()", timeoutParentHardLimit, 0, func() error {
		runtime.Close()

		return nil
	})
	if closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}
	if closeElapsed > timeoutCloseBudget {
		t.Errorf("Close() after a timed out hook took %s, want at most %s", closeElapsed, timeoutCloseBudget)
	}

	assertSandboxWorkDirsGone(t, tempRoot, timeoutCloseBudget)
}

// isolateSandboxTempDir points every temp variable a worker may inherit at one
// test-owned root, so the assertion never inspects the real system temp.
func isolateSandboxTempDir(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	t.Setenv("TMP", root)
	t.Setenv("TEMP", root)

	return root
}

// sandboxWorkDirs returns only the worker sandbox directory names created
// directly below root. Plugin fixtures and test temp directories use other
// names and are never treated as worker output.
func sandboxWorkDirs(t *testing.T, root string) []string {
	t.Helper()

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read TMPDIR %s: %v", root, err)
	}

	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), sandboxCleanupDirPrefix) {
			dirs = append(dirs, entry.Name())
		}
	}

	return dirs
}

// assertSandboxWorkDirsGone waits a bounded time for the worker's own sandbox
// directories to disappear, then reports any that still remain.
func assertSandboxWorkDirsGone(t *testing.T, root string, limit time.Duration) {
	t.Helper()

	deadline := time.Now().Add(limit)
	var remaining []string
	for {
		remaining = sandboxWorkDirs(t, root)
		if len(remaining) == 0 {
			return
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	t.Errorf(
		"%d %q sandbox directories remain in TMPDIR %s after Close(): %v",
		len(remaining), sandboxCleanupDirPrefix, root, remaining,
	)
}

// runSandboxStep runs one public API call under an outer deadline, so a stuck
// worker cannot hang the whole test run. The step owns stdout while it runs so
// worker log markers can be checked afterwards.
func runSandboxStep(
	t *testing.T,
	name string,
	limit time.Duration,
	settle time.Duration,
	run func() error,
) (string, time.Duration, error) {
	t.Helper()

	type stepResult struct {
		output string
		err    error
	}

	done := make(chan stepResult, 1)
	start := time.Now()

	go func() {
		output, err := captureSandboxOutput(run, settle)
		done <- stepResult{output: output, err: err}
	}()

	timer := time.NewTimer(limit)
	defer timer.Stop()

	select {
	case result := <-done:
		return result.output, time.Since(start), result.err
	case <-timer.C:
		t.Fatalf("%s did not finish within the %s outer deadline", name, limit)

		return "", 0, nil
	}
}

// captureSandboxOutput swaps stdout for a pipe while run executes and returns
// everything the worker logged during the request, including detached lines.
func captureSandboxOutput(run func() error, settle time.Duration) (string, error) {
	previous := os.Stdout

	reader, writer, err := os.Pipe()
	if err != nil {
		return "", fmt.Errorf("create stdout pipe: %w", err)
	}

	os.Stdout = writer
	runErr := run()
	if settle > 0 {
		time.Sleep(settle)
	}
	os.Stdout = previous

	closeErr := writer.Close()
	output, readErr := io.ReadAll(reader)
	_ = reader.Close()

	if readErr != nil {
		return string(output), fmt.Errorf("read captured stdout: %w", readErr)
	}
	if closeErr != nil {
		return string(output), fmt.Errorf("close captured stdout: %w", closeErr)
	}

	return string(output), runErr
}
