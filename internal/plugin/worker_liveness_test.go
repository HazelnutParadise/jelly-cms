package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	livenessCaseEnv            = "JELLY_PLUGIN_LIVENESS_CASE"
	livenessPIDFileEnv         = "JELLY_PLUGIN_LIVENESS_PIDFILE"
	livenessChildMarkerPrefix  = "JELLY_PLUGIN_LIVENESS_CHILD_OK:"
	livenessParentMarkerPrefix = "JELLY_PLUGIN_LIVENESS_PARENT_OK:"
	livenessStatePrefix        = "JELLY_PLUGIN_LIVENESS_STATE:"

	livenessCaseLoadLogFlood = "load-log-flood"
	livenessCaseHookLogFlood = "hook-log-flood"
	livenessCaseOrphanWorker = "orphan-worker"
	livenessCaseBusyOrphan   = "busy-orphan"

	livenessBusyHook        = "OnLivenessBusy"
	livenessBusyReadyMarker = "JELLY_PLUGIN_LIVENESS_BUSY_READY"

	// livenessParentHardLimit bounds one child test process. A blocked parent
	// reader is exercised inside that child, so killing the process group is the
	// last line of defence for go test.
	livenessParentHardLimit = 15 * time.Second

	// livenessOperationBudget bounds Load and CallHook while the parent stdout is
	// an undrained pipe. It stays below the hard limit so the child can report
	// the measured state before the parent kills it.
	livenessOperationBudget = 4 * time.Second
	livenessCloseBudget     = 3 * time.Second

	// livenessOrphanExitBudget is the time a worker gets to notice that its
	// parent process was killed.
	livenessOrphanExitBudget = 3 * time.Second

	// Every script writes this many log entries of this size. The payload is far
	// larger than a pipe buffer, so a parent that prints logs synchronously in
	// its protocol reader blocks instead of finishing the request.
	livenessLogCount = 100
	livenessLogSize  = 4096

	// livenessFailureOutputLimit keeps failure messages readable when the
	// restored stdout has just drained a log flood.
	livenessFailureOutputLimit = 16 << 10
)

// TestWorkerLiveness re-executes the test binary for every case. The child turns
// its own stdout into a pipe that nobody reads, so the parent-side reader cannot
// print the plugin logs. Load, CallHook and Close must still finish within a
// bounded time and the worker must be reclaimed. The second case kills the
// child that owns a worker and checks the worker exits on its own.
func TestWorkerLiveness(t *testing.T) {
	if !processGroupSupported() {
		t.Skip("worker liveness checks need Unix process groups and ps")
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}

	t.Run(livenessCaseLoadLogFlood, func(t *testing.T) {
		runWorkerLivenessFloodParent(t, executable, livenessCaseLoadLogFlood)
	})
	t.Run(livenessCaseHookLogFlood, func(t *testing.T) {
		runWorkerLivenessFloodParent(t, executable, livenessCaseHookLogFlood)
	})
	t.Run(livenessCaseOrphanWorker, func(t *testing.T) {
		runWorkerLivenessOrphanParent(t, executable)
	})
	t.Run(livenessCaseBusyOrphan, func(t *testing.T) {
		runWorkerLivenessBusyOrphanParent(t, executable)
	})
}

// TestWorkerLivenessHelper is the subprocess side of TestWorkerLiveness. It only
// runs when the dedicated environment variable selects a case.
func TestWorkerLivenessHelper(t *testing.T) {
	name := os.Getenv(livenessCaseEnv)
	if name == "" {
		t.Skip("subprocess helper for TestWorkerLiveness")
	}

	switch name {
	case livenessCaseLoadLogFlood:
		runWorkerLivenessLoadLogFlood(t)
	case livenessCaseHookLogFlood:
		runWorkerLivenessHookLogFlood(t)
	case livenessCaseOrphanWorker:
		runWorkerLivenessOrphanWorker(t)

		return
	case livenessCaseBusyOrphan:
		runWorkerLivenessBusyOrphanWorker(t)

		return
	default:
		t.Fatalf("unknown worker liveness case %q", name)
	}

	if t.Failed() {
		return
	}

	fmt.Fprintf(os.Stderr, "%s%s\n", livenessChildMarkerPrefix, name)
}

// runWorkerLivenessLoadLogFlood expects Load to finish even though the entry
// script fills the blocked parent stdout, and Close must still reclaim the
// worker.
func runWorkerLivenessLoadLogFlood(t *testing.T) {
	t.Helper()

	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}

	pluginDir, meta := writeLivenessPlugin(t, "liveness-load-flood", livenessLogScript(
		"load-flood", livenessLogCount, livenessLogSize,
	))

	restore := blockWorkerStdout(t)
	defer restore()

	loadDone := make(chan error, 1)
	start := time.Now()

	go func() {
		loadDone <- runtime.Load(context.Background(), pluginDir)
	}()

	select {
	case loadErr := <-loadDone:
		elapsed := time.Since(start)
		reportLivenessState(
			t, "case=load-log-flood load_finished=true load_elapsed=%s load_err=%v",
			elapsed, loadErr,
		)
		if loadErr != nil {
			restore()

			t.Fatalf(
				"Load() with %d logs of %d bytes returned error after %s, want nil: %v",
				livenessLogCount, livenessLogSize, elapsed, loadErr,
			)
		}
	case <-time.After(livenessOperationBudget):
		restore()
		reportLivenessState(
			t, "case=load-log-flood load_finished=false load_elapsed>=%s",
			livenessOperationBudget,
		)

		t.Fatalf(
			"Load() with %d logs of %d bytes did not return within %s while stdout was blocked",
			livenessLogCount, livenessLogSize, livenessOperationBudget,
		)
	}

	if _, err := runtime.GetPlugin(meta.ID); err != nil {
		restore()

		t.Fatalf("GetPlugin(%q) after Load() error = %v", meta.ID, err)
	}

	closeElapsed, closed := closeRuntimeBounded(runtime, livenessCloseBudget)
	reportLivenessState(
		t, "case=load-log-flood close_finished=%t close_elapsed=%s",
		closed, closeElapsed,
	)
	if !closed {
		restore()

		t.Fatalf("Close() did not return within %s after a blocked log flood", livenessCloseBudget)
	}

	workers, pidErr := findWorkerPIDs(os.Getpid())
	reportLivenessState(
		t, "case=load-log-flood workers_after_close=%v workers_after_close_err=%v",
		workers, pidErr,
	)
	if pidErr != nil {
		restore()

		t.Fatalf("list worker processes after Close(): %v", pidErr)
	}
	if len(workers) > 0 {
		restore()

		t.Fatalf("worker processes still alive after Close(): %v", workers)
	}

	restore()
}

// runWorkerLivenessHookLogFlood expects CallHook to finish even though one hook
// fills the blocked parent stdout, the worker must survive the call, and Close
// must still reclaim it.
func runWorkerLivenessHookLogFlood(t *testing.T) {
	t.Helper()

	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}

	entry := "JellyCMS.registerHook(\"OnLivenessFlood\", function (data) {\n" +
		livenessLogScript("hook-flood", livenessLogCount, livenessLogSize) +
		"});\n"

	pluginDir, meta := writeLivenessPlugin(t, "liveness-hook-flood", entry)

	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if _, err := runtime.GetPlugin(meta.ID); err != nil {
		t.Fatalf("GetPlugin(%q) after Load() error = %v", meta.ID, err)
	}

	restore := blockWorkerStdout(t)
	defer restore()

	hookDone := make(chan struct{})
	start := time.Now()

	go func() {
		runtime.CallHook(Hook("OnLivenessFlood"), map[string]interface{}{"case": "hook-log-flood"})
		close(hookDone)
	}()

	select {
	case <-hookDone:
		elapsed := time.Since(start)
		reportLivenessState(
			t, "case=hook-log-flood hook_finished=true hook_elapsed=%s",
			elapsed,
		)
	case <-time.After(livenessOperationBudget):
		restore()
		reportLivenessState(
			t, "case=hook-log-flood hook_finished=false hook_elapsed>=%s",
			livenessOperationBudget,
		)

		t.Fatalf(
			"CallHook() with %d logs of %d bytes did not return within %s while stdout was blocked",
			livenessLogCount, livenessLogSize, livenessOperationBudget,
		)
	}

	workers, pidErr := findWorkerPIDs(os.Getpid())
	reportLivenessState(
		t, "case=hook-log-flood workers_after_hook=%v workers_after_hook_err=%v",
		workers, pidErr,
	)
	if pidErr != nil {
		restore()

		t.Fatalf("list worker processes after CallHook(): %v", pidErr)
	}
	if len(workers) == 0 {
		restore()

		t.Fatalf(
			"worker exited during CallHook() with a blocked log flood, want a healthy worker",
		)
	}

	closeElapsed, closed := closeRuntimeBounded(runtime, livenessCloseBudget)
	reportLivenessState(
		t, "case=hook-log-flood close_finished=%t close_elapsed=%s",
		closed, closeElapsed,
	)
	if !closed {
		restore()

		t.Fatalf("Close() did not return within %s after a blocked hook log flood", livenessCloseBudget)
	}

	workers, pidErr = findWorkerPIDs(os.Getpid())
	reportLivenessState(
		t, "case=hook-log-flood workers_after_close=%v workers_after_close_err=%v",
		workers, pidErr,
	)
	if pidErr != nil {
		restore()

		t.Fatalf("list worker processes after Close(): %v", pidErr)
	}
	if len(workers) > 0 {
		restore()

		t.Fatalf("worker processes still alive after Close(): %v", workers)
	}

	restore()
}

// runWorkerLivenessOrphanWorker loads one idle plugin, writes the worker pid the
// parent asks for, and then waits to be killed. The worker must notice the
// closed stdin pipe and exit without any help from this process.
func runWorkerLivenessOrphanWorker(t *testing.T) {
	t.Helper()

	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}

	pluginDir, _ := writeLivenessPlugin(
		t, "liveness-orphan", `JellyCMS.registerHook("OnLiveness", function (data) {});`,
	)

	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	workerPID := waitForWorkerPID(t, os.Getpid(), 2*time.Second)
	publishWorkerPID(t, workerPID)

	fmt.Fprintf(os.Stderr, "%scase=orphan-worker child_pid=%d worker_pid=%d ready=true\n",
		livenessStatePrefix, os.Getpid(), workerPID)

	select {}
}

// runWorkerLivenessBusyOrphanWorker loads one plugin whose hook logs a ready
// marker and then never returns. It publishes the worker pid before entering
// the hook, so the outer test can confirm the marker and the real parent/child
// relationship before it kills this process.
func runWorkerLivenessBusyOrphanWorker(t *testing.T) {
	t.Helper()

	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}

	entry := fmt.Sprintf(`JellyCMS.registerHook(%q, function (data) {
  JellyCMS.log.info(%q);
  while (true) {}
});
`, livenessBusyHook, livenessBusyReadyMarker)

	pluginDir, _ := writeLivenessPlugin(t, "liveness-busy-orphan", entry)

	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	workerPID := waitForWorkerPID(t, os.Getpid(), 2*time.Second)
	publishWorkerPID(t, workerPID)

	fmt.Fprintf(os.Stderr, "%scase=%s child_pid=%d worker_pid=%d ready=true\n",
		livenessStatePrefix, livenessCaseBusyOrphan, os.Getpid(), workerPID)

	// The hook never returns, so this goroutine stays inside QuickJS until the
	// parent process is killed. The outer test waits for the log marker instead
	// of sleeping for a fixed time.
	go runtime.CallHook(Hook(livenessBusyHook), map[string]interface{}{"case": livenessCaseBusyOrphan})

	select {}
}

// runWorkerLivenessFloodParent runs one log flood case and enforces the hard
// deadline on the child process group.
func runWorkerLivenessFloodParent(t *testing.T, executable, name string) {
	t.Helper()

	childTemp, err := os.MkdirTemp("", "jelly-plugin-liveness-*")
	if err != nil {
		t.Fatalf("create child temp directory: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(childTemp)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), livenessParentHardLimit)
	defer cancel()

	cmd := newWorkerLivenessChild(ctx, executable, name, childTemp)

	// The worker inherits the child's process group. If the child exits without
	// reclaiming it, only that group is killed here.
	defer func() {
		_ = killProcessGroup(cmd)
	}()

	start := time.Now()
	output, runErr := cmd.CombinedOutput()
	elapsed := time.Since(start)
	text := string(output)
	failureOutput := filteredFailureOutput(text, livenessFailureOutputLimit)

	if ctx.Err() == context.DeadlineExceeded {
		_ = killProcessGroup(cmd)

		t.Fatalf(
			"case %s exceeded the %s hard deadline; child output:\n%s",
			name, livenessParentHardLimit, failureOutput,
		)
	}

	printLivenessState(name, text)

	if runErr != nil {
		t.Fatalf("case %s child failed after %s: %v; output:\n%s", name, elapsed, runErr, failureOutput)
	}
	if !strings.Contains(text, livenessChildMarkerPrefix+name) {
		t.Fatalf(
			"case %s child exited 0 without success marker %q after %s; output:\n%s",
			name, livenessChildMarkerPrefix+name, elapsed, failureOutput,
		)
	}

	fmt.Printf("%s%s elapsed=%s\n", livenessParentMarkerPrefix, name, elapsed)
}

// runWorkerLivenessOrphanParent kills the child that owns one idle worker and
// checks that exact worker pid disappears within livenessOrphanExitBudget.
func runWorkerLivenessOrphanParent(t *testing.T, executable string) {
	t.Helper()

	childTemp, err := os.MkdirTemp("", "jelly-plugin-liveness-*")
	if err != nil {
		t.Fatalf("create child temp directory: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(childTemp)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), livenessParentHardLimit)
	defer cancel()

	cmd := newWorkerLivenessChild(ctx, executable, livenessCaseOrphanWorker, childTemp)

	pidFile := filepath.Join(childTemp, "worker.pid")
	logPath := filepath.Join(childTemp, "child.log")

	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create child log: %v", err)
	}
	defer func() {
		_ = logFile.Close()
	}()

	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		t.Fatalf("start orphan case child: %v", err)
	}

	waited := false
	defer func() {
		_ = killProcessGroup(cmd)
		if !waited {
			_ = cmd.Wait()
		}
	}()

	workerPID, err := waitForWorkerPIDFile(pidFile, 5*time.Second)
	if err != nil {
		_ = cmd.Process.Kill()
		waitErr := cmd.Wait()
		waited = true

		childLog, _ := os.ReadFile(logPath)

		t.Fatalf(
			"orphan case child did not report a worker pid: %v; child wait error = %v; child output:\n%s",
			err, waitErr, childLog,
		)
	}

	childPID := cmd.Process.Pid

	fmt.Fprintf(os.Stderr, "%scase=orphan-worker child_pid=%d worker_pid=%d ready=true\n",
		livenessStatePrefix, childPID, workerPID)

	observedParent, err := processParentPID(workerPID)
	if err != nil {
		t.Fatalf("resolve parent of worker pid %d: %v", workerPID, err)
	}
	if observedParent != childPID {
		t.Fatalf(
			"worker pid %d parent = %d, want child pid %d",
			workerPID, observedParent, childPID,
		)
	}

	workers, err := findWorkerPIDs(childPID)
	if err != nil {
		t.Fatalf("list worker children of child pid %d: %v", childPID, err)
	}
	if !containsPID(workers, workerPID) {
		t.Fatalf(
			"ps did not list worker pid %d as a child of child pid %d; children = %v",
			workerPID, childPID, workers,
		)
	}

	killStart := time.Now()
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("kill child pid %d: %v", childPID, err)
	}

	waitErr := cmd.Wait()
	waited = true

	gone, goneErr := waitForProcessExit(workerPID, livenessOrphanExitBudget)
	elapsed := time.Since(killStart)

	fmt.Fprintf(os.Stderr,
		"%scase=orphan-worker child_kill_wait_err=%v worker_exited=%t worker_exit_elapsed=%s\n",
		livenessStatePrefix, waitErr, gone, elapsed,
	)

	if goneErr != nil {
		t.Fatalf("check worker pid %d after killing child %d: %v", workerPID, childPID, goneErr)
	}

	childLog, _ := os.ReadFile(logPath)
	printLivenessState(livenessCaseOrphanWorker, string(childLog))

	if !gone {
		t.Fatalf(
			"worker pid %d was still alive %s after its parent child pid %d was killed; child output:\n%s",
			workerPID, elapsed, childPID, childLog,
		)
	}

	fmt.Printf(
		"%s%s child_pid=%d worker_pid=%d worker_exit_elapsed=%s\n",
		livenessParentMarkerPrefix, livenessCaseOrphanWorker, childPID, workerPID, elapsed,
	)
}

// runWorkerLivenessBusyOrphanParent kills the child that owns a worker busy in
// a JavaScript hook and checks that exact worker pid disappears within
// livenessOrphanExitBudget. Readiness comes from the hook log marker, the
// published worker pid and the observed ppid, never from a fixed sleep.
func runWorkerLivenessBusyOrphanParent(t *testing.T, executable string) {
	t.Helper()

	childTemp, err := os.MkdirTemp("", "jelly-plugin-liveness-*")
	if err != nil {
		t.Fatalf("create child temp directory: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(childTemp)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), livenessParentHardLimit)
	defer cancel()

	cmd := newWorkerLivenessChild(ctx, executable, livenessCaseBusyOrphan, childTemp)

	pidFile := filepath.Join(childTemp, "worker.pid")
	logPath := filepath.Join(childTemp, "child.log")

	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create child log: %v", err)
	}
	defer func() {
		_ = logFile.Close()
	}()

	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		t.Fatalf("start busy orphan case child: %v", err)
	}

	workerPID := 0
	waited := false

	defer func() {
		// Only the process group created for this helper is killed, so a worker
		// that survives the parent is reclaimed without touching other tests.
		_ = killProcessGroup(cmd)

		if !waited {
			_ = cmd.Wait()
		}
		if workerPID > 0 {
			gone, checkErr := waitForProcessExit(workerPID, time.Second)
			if checkErr != nil {
				fmt.Fprintf(os.Stderr, "%scase=%s worker_cleanup_err=%v\n",
					livenessStatePrefix, livenessCaseBusyOrphan, checkErr)
			} else if !gone {
				fmt.Fprintf(os.Stderr, "%scase=%s worker_cleanup_failed=true worker_pid=%d\n",
					livenessStatePrefix, livenessCaseBusyOrphan, workerPID)
			}
		}
	}()

	workerPID, err = waitForWorkerPIDFile(pidFile, 5*time.Second)
	if err != nil {
		_ = cmd.Process.Kill()
		waitErr := cmd.Wait()
		waited = true

		childLog, _ := os.ReadFile(logPath)

		t.Fatalf(
			"busy orphan case child did not report a worker pid: %v; child wait error = %v; child output:\n%s",
			err, waitErr, childLog,
		)
	}

	childPID := cmd.Process.Pid

	observedParent, err := processParentPID(workerPID)
	if err != nil {
		t.Fatalf("resolve parent of worker pid %d: %v", workerPID, err)
	}
	if observedParent != childPID {
		t.Fatalf(
			"worker pid %d parent = %d, want child pid %d",
			workerPID, observedParent, childPID,
		)
	}

	workers, err := findWorkerPIDs(childPID)
	if err != nil {
		t.Fatalf("list worker children of child pid %d: %v", childPID, err)
	}
	if !containsPID(workers, workerPID) {
		t.Fatalf(
			"ps did not list worker pid %d as a child of child pid %d; children = %v",
			workerPID, childPID, workers,
		)
	}

	markerStart := time.Now()
	markerSeen, err := waitForLogMarker(logPath, livenessBusyReadyMarker, 5*time.Second)
	markerElapsed := time.Since(markerStart)
	if err != nil {
		t.Fatalf("wait for busy hook marker in %s: %v", logPath, err)
	}
	if !markerSeen {
		_ = cmd.Process.Kill()
		waitErr := cmd.Wait()
		waited = true

		childLog, _ := os.ReadFile(logPath)

		t.Fatalf(
			"case %s did not reach the JavaScript busy hook marker %q within 5s; child wait error = %v; child output:\n%s",
			livenessCaseBusyOrphan, livenessBusyReadyMarker, waitErr, childLog,
		)
	}

	fmt.Fprintf(os.Stderr,
		"%scase=%s child_pid=%d worker_pid=%d ready=true marker_elapsed=%s\n",
		livenessStatePrefix, livenessCaseBusyOrphan, childPID, workerPID, markerElapsed)

	alive, err := processExists(workerPID)
	if err != nil {
		t.Fatalf("check worker pid %d before killing child: %v", workerPID, err)
	}
	if !alive {
		t.Fatalf(
			"worker pid %d exited before child pid %d was killed even though the hook marker %q was logged",
			workerPID, childPID, livenessBusyReadyMarker,
		)
	}

	killStart := time.Now()
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("kill child pid %d: %v", childPID, err)
	}

	waitErr := cmd.Wait()
	waited = true

	gone, goneErr := waitForProcessExit(workerPID, livenessOrphanExitBudget)
	elapsed := time.Since(killStart)

	fmt.Fprintf(os.Stderr,
		"%scase=%s child_kill_wait_err=%v worker_exited=%t worker_exit_elapsed=%s\n",
		livenessStatePrefix, livenessCaseBusyOrphan, waitErr, gone, elapsed,
	)

	if goneErr != nil {
		t.Fatalf("check busy worker pid %d after killing child %d: %v", workerPID, childPID, goneErr)
	}

	childLog, _ := os.ReadFile(logPath)
	printLivenessState(livenessCaseBusyOrphan, string(childLog))

	if !gone {
		t.Fatalf(
			"busy worker pid %d was still alive %s after its parent child pid %d was killed while JavaScript was in its hook busy loop (marker seen after %s); child output:\n%s",
			workerPID, elapsed, childPID, markerElapsed, childLog,
		)
	}

	fmt.Printf(
		"%s%s child_pid=%d worker_pid=%d worker_exit_elapsed=%s\n",
		livenessParentMarkerPrefix, livenessCaseBusyOrphan, childPID, workerPID, elapsed,
	)
}

// newWorkerLivenessChild builds one re-executed helper. It runs in its own
// process group so a hard deadline can clean up the helper and its worker
// without touching unrelated processes.
func newWorkerLivenessChild(ctx context.Context, executable, name, tempDir string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, executable, "-test.v", "-test.run=^TestWorkerLivenessHelper$")
	cmd.Env = livenessChildEnv(map[string]string{
		livenessCaseEnv:    name,
		livenessPIDFileEnv: filepath.Join(tempDir, "worker.pid"),
		"TMPDIR":           tempDir,
		"TMP":              tempDir,
		"TEMP":             tempDir,
	})
	cmd.WaitDelay = 500 * time.Millisecond

	configureProcessGroup(cmd)

	cmd.Cancel = func() error {
		return killProcessGroup(cmd)
	}

	return cmd
}

// blockWorkerStdout replaces os.Stdout with the write end of a pipe that is
// never read. Writes block once the pipe buffer is full. The returned function
// restores os.Stdout and releases both ends; it is safe to call repeatedly.
func blockWorkerStdout(t *testing.T) func() {
	t.Helper()

	previous := os.Stdout

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create blocked stdout pipe: %v", err)
	}

	os.Stdout = writer

	var once sync.Once

	return func() {
		once.Do(func() {
			os.Stdout = previous
			_ = reader.Close()
			_ = writer.Close()
		})
	}
}

// closeRuntimeBounded runs Close and reports whether it finished within limit.
func closeRuntimeBounded(runtime *Runtime, limit time.Duration) (time.Duration, bool) {
	done := make(chan struct{})

	start := time.Now()

	go func() {
		runtime.Close()
		close(done)
	}()

	select {
	case <-done:
		return time.Since(start), true
	case <-time.After(limit):
		return time.Since(start), false
	}
}

// reportLivenessState writes one machine-readable state line to stderr, which
// stays usable while stdout is blocked.
func reportLivenessState(t *testing.T, format string, args ...interface{}) {
	t.Helper()

	fmt.Fprintf(os.Stderr, "%s%s\n", livenessStatePrefix, fmt.Sprintf(format, args...))
}

// printLivenessState forwards helper state lines from child output.
func printLivenessState(name, output string) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, livenessStatePrefix) {
			continue
		}

		fmt.Printf(
			"JELLY_PLUGIN_LIVENESS_RESULT:%s %s\n",
			name, strings.TrimPrefix(line, livenessStatePrefix),
		)
	}
}

// filteredFailureOutput removes the flood log lines and keeps the remaining
// diagnostic lines, so failure messages stay readable.
func filteredFailureOutput(output string, limit int) string {
	flood := strings.Repeat("x", 64)

	var kept strings.Builder

	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, flood) {
			continue
		}

		kept.WriteString(line)
		kept.WriteByte('\n')
	}

	return tailOutput(kept.String(), limit)
}

// tailOutput returns at most limit trailing bytes so failure messages stay
// readable after a log flood.
func tailOutput(output string, limit int) string {
	if len(output) <= limit {
		return output
	}

	return "...(earlier output truncated)...\n" + output[len(output)-limit:]
}

// livenessLogScript builds count log statements with a payload of size bytes.
func livenessLogScript(prefix string, count, size int) string {
	payload := strings.Repeat("x", size)

	var script strings.Builder

	for i := 0; i < count; i++ {
		message := fmt.Sprintf("%s-%03d-%s", prefix, i, payload)
		fmt.Fprintf(&script, "JellyCMS.log.info(%q);\n", message)
	}

	return script.String()
}

// writeLivenessPlugin writes a self-contained plugin fixture.
func writeLivenessPlugin(t *testing.T, id, entrypoint string) (string, PluginMetadata) {
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
		t.Fatalf("marshal plugin manifest for %s: %v", id, err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), manifest, 0o600); err != nil {
		t.Fatalf("write plugin manifest for %s: %v", id, err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, meta.Entrypoint), []byte(entrypoint), 0o600); err != nil {
		t.Fatalf("write plugin entrypoint for %s: %v", id, err)
	}

	return pluginDir, meta
}

// livenessChildEnv builds the child environment, replacing selected keys.
func livenessChildEnv(extras map[string]string) []string {
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

// waitForWorkerPID waits until exactly one worker child of parentPID exists.
// Readiness is the observed process, not a fixed delay.
func waitForWorkerPID(t *testing.T, parentPID int, limit time.Duration) int {
	t.Helper()

	deadline := time.Now().Add(limit)
	for {
		workers, err := findWorkerPIDs(parentPID)
		if err != nil {
			t.Fatalf("list worker processes: %v", err)
		}
		if len(workers) > 0 {
			if len(workers) != 1 {
				t.Fatalf("worker children of pid %d = %v, want exactly one", parentPID, workers)
			}

			return workers[0]
		}
		if time.Now().After(deadline) {
			t.Fatalf("no worker child of pid %d was found within %s", parentPID, limit)
		}

		time.Sleep(20 * time.Millisecond)
	}
}

// publishWorkerPID atomically writes the worker pid for the outer test.
func publishWorkerPID(t *testing.T, workerPID int) {
	t.Helper()

	pidFile := os.Getenv(livenessPIDFileEnv)
	if pidFile == "" {
		t.Fatalf("%s is not set", livenessPIDFileEnv)
	}

	tmp := pidFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(workerPID)+"\n"), 0o600); err != nil {
		t.Fatalf("write worker pid file: %v", err)
	}
	if err := os.Rename(tmp, pidFile); err != nil {
		t.Fatalf("publish worker pid file: %v", err)
	}
}

// waitForLogMarker waits until path contains marker. It polls the file, so a
// fixed delay never decides readiness.
func waitForLogMarker(path, marker string, limit time.Duration) (bool, error) {
	deadline := time.Now().Add(limit)

	for {
		data, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return false, fmt.Errorf("read child log: %w", err)
			}
		} else if strings.Contains(string(data), marker) {
			return true, nil
		}

		if time.Now().After(deadline) {
			return false, nil
		}

		time.Sleep(20 * time.Millisecond)
	}
}

// waitForWorkerPIDFile waits until the helper publishes its worker pid.
func waitForWorkerPIDFile(path string, limit time.Duration) (int, error) {
	deadline := time.Now().Add(limit)

	for {
		data, err := os.ReadFile(path)
		if err == nil {
			text := strings.TrimSpace(string(data))
			if text != "" {
				pid, convErr := strconv.Atoi(text)
				if convErr != nil {
					return 0, fmt.Errorf("parse worker pid %q: %w", text, convErr)
				}
				if pid <= 0 {
					return 0, fmt.Errorf("invalid worker pid %d", pid)
				}

				return pid, nil
			}
		} else if !os.IsNotExist(err) {
			return 0, fmt.Errorf("read worker pid file: %w", err)
		}

		if time.Now().After(deadline) {
			return 0, fmt.Errorf("worker pid file %s not written within %s", path, limit)
		}

		time.Sleep(25 * time.Millisecond)
	}
}

// waitForProcessExit polls until the pid is gone or the limit expires.
func waitForProcessExit(pid int, limit time.Duration) (bool, error) {
	deadline := time.Now().Add(limit)

	for {
		exists, err := processExists(pid)
		if err != nil {
			return false, err
		}
		if !exists {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, nil
		}

		time.Sleep(25 * time.Millisecond)
	}
}

func containsPID(pids []int, want int) bool {
	for _, pid := range pids {
		if pid == want {
			return true
		}
	}

	return false
}
