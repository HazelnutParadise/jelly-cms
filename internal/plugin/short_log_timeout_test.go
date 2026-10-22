//go:build unix

package plugin

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const (
	shortLogCaseEnv            = "JELLY_PLUGIN_SHORT_LOG_CASE"
	shortLogChildMarkerPrefix  = "JELLY_PLUGIN_SHORT_LOG_CHILD_OK:"
	shortLogParentMarkerPrefix = "JELLY_PLUGIN_SHORT_LOG_PARENT_OK:"
	shortLogStatePrefix        = "JELLY_PLUGIN_SHORT_LOG_STATE:"

	shortLogCaseHook  = "hook-infinite-log"
	shortLogCaseClose = "close-with-busy-hook"

	shortLogHook       = "OnShortLogTimeout"
	shortLogHookMarker = "JELLY_PLUGIN_SHORT_LOG_HOOK_STARTED"

	// shortLogParentHardLimit bounds one child test process. The drain happens
	// inside that child, so killing only its process group is the last line of
	// defence and never touches unrelated processes.
	shortLogParentHardLimit = 10 * time.Second

	// shortLogLocalReturnLimit lets the child report the measured state before
	// the outer hard deadline kills it.
	shortLogLocalReturnLimit = 8 * time.Second

	// shortLogMarkerBudget bounds how long the hook may take to emit its first
	// log line. Readiness is the observed marker, never a fixed sleep.
	shortLogMarkerBudget = 5 * time.Second

	// shortLogDrainLimit bounds the post-measurement read of the blocked pipe.
	shortLogDrainLimit = 3 * time.Second

	// Linux fcntl commands for pipe capacity. The numbers are Linux ABI values
	// and are only used on Linux; the test never changes a system-wide limit.
	shortLogFSetPipeSize = 0x407
	shortLogFGetPipeSize = 0x408
	shortLogPipeSize     = 64 << 10
)

// TestPluginShortLogTimeout reproduces the Linux small-log reclaim delay. The
// child replaces its own stdout with an undrained pipe. The hook logs a marker
// and then logs forever, so the parent reader spends the 2ms per-log budget and
// leaves up to one 64KiB worker stdout pipe full of log frames when the 1s
// worker step timeout kills the worker. CallHook and Close must still return
// within the existing 2s budgets.
func TestPluginShortLogTimeout(t *testing.T) {
	if !processGroupSupported() {
		t.Skip("short log timeout check needs Unix process groups")
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}

	reportShortLogState(t,
		"environment go_version=%s goos=%s goarch=%s calibrated_pipe_size=%d worker_step_timeout=%s log_write_budget=%s",
		runtime.Version(), runtime.GOOS, runtime.GOARCH, shortLogCalibratePipeSize(t),
		workerStepTimeout, workerLogWriteBudget,
	)

	for _, name := range []string{shortLogCaseHook, shortLogCaseClose} {
		name := name

		t.Run(name, func(t *testing.T) {
			childTemp, err := os.MkdirTemp("", "jelly-plugin-short-log-*")
			if err != nil {
				t.Fatalf("create child temp directory: %v", err)
			}
			defer func() {
				_ = os.RemoveAll(childTemp)
			}()

			ctx, cancel := context.WithTimeout(context.Background(), shortLogParentHardLimit)
			defer cancel()

			cmd := exec.CommandContext(ctx, executable, "-test.v", "-test.run=^TestPluginShortLogTimeoutHelper$")
			cmd.Env = shortLogChildEnv(map[string]string{
				shortLogCaseEnv: name,
				// A killed child does not clean up its temp files. Give it an
				// isolated temp root the parent removes unconditionally.
				"TMPDIR": childTemp,
				"TMP":    childTemp,
				"TEMP":   childTemp,
			})
			cmd.WaitDelay = 500 * time.Millisecond

			// Only this child and the workers it starts share the new group.
			configureProcessGroup(cmd)
			cmd.Cancel = func() error {
				return killProcessGroup(cmd)
			}

			defer func() {
				_ = killProcessGroup(cmd)
			}()

			start := time.Now()
			output, runErr := cmd.CombinedOutput()
			elapsed := time.Since(start)
			text := string(output)

			if ctx.Err() == context.DeadlineExceeded {
				_ = killProcessGroup(cmd)

				t.Fatalf(
					"case %s exceeded the %s hard deadline, so its process group was killed; output:\n%s",
					name, shortLogParentHardLimit, tailOutput(text, 16<<10),
				)
			}

			printShortLogState(name, text)

			if runErr != nil {
				t.Fatalf("case %s child failed after %s: %v; output:\n%s",
					name, elapsed, runErr, tailOutput(text, 16<<10))
			}
			if !strings.Contains(text, shortLogChildMarkerPrefix+name) {
				t.Fatalf("case %s child exited 0 without success marker %q; output:\n%s",
					name, shortLogChildMarkerPrefix+name, tailOutput(text, 16<<10))
			}

			fmt.Printf("%s%s elapsed=%s\n", shortLogParentMarkerPrefix, name, elapsed)
		})
	}
}

// TestPluginShortLogTimeoutHelper is the subprocess side of
// TestPluginShortLogTimeout. It only runs when the dedicated environment
// variable selects a case.
func TestPluginShortLogTimeoutHelper(t *testing.T) {
	name := os.Getenv(shortLogCaseEnv)
	if name == "" {
		t.Skip("subprocess helper for TestPluginShortLogTimeout")
	}

	switch name {
	case shortLogCaseHook:
		runShortLogHookCase(t)
	case shortLogCaseClose:
		runShortLogCloseCase(t)
	default:
		t.Fatalf("unknown short log case %q", name)
	}

	if t.Failed() {
		return
	}

	fmt.Fprintf(os.Stderr, "%s%s\n", shortLogChildMarkerPrefix, name)
}

// runShortLogHookCase expects CallHook to stop the forever-logging callback
// within the existing hook budget even though stdout is blocked.
func runShortLogHookCase(t *testing.T) {
	t.Helper()

	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	defer closeRuntimeWithin(t, runtime, timeoutCloseBudget)

	pluginDir, meta := writeTimeoutPlugin(t, "short-log-hook", shortLogPluginEntry())
	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	blocked := newShortLogBlockedStdout(t)
	defer blocked.restore()

	hookDone := make(chan struct{})

	start := time.Now()

	go func() {
		runtime.CallHook(Hook(shortLogHook), map[string]interface{}{"case": shortLogCaseHook})
		close(hookDone)
	}()

	prefix, seen, markerElapsed, markerErr := blocked.waitForMarker(shortLogHookMarker, shortLogMarkerBudget)
	if markerErr != nil || !seen {
		blocked.restore()
		reportShortLogState(t,
			"case=%s marker_seen=%t marker_elapsed=%s marker_err=%v",
			shortLogCaseHook, seen, markerElapsed, markerErr,
		)

		t.Fatalf(
			"the infinite logging hook did not reach marker %q within %s (elapsed %s, error %v)",
			shortLogHookMarker, shortLogMarkerBudget, markerElapsed, markerErr,
		)
	}

	reportShortLogState(t,
		"case=%s marker_seen=true marker_elapsed=%s",
		shortLogCaseHook, markerElapsed,
	)

	select {
	case <-hookDone:
	case <-time.After(shortLogLocalReturnLimit):
		blocked.restore()
		reportShortLogState(t,
			"case=%s callhook_finished=false callhook_elapsed>=%s",
			shortLogCaseHook, shortLogLocalReturnLimit,
		)

		t.Fatalf(
			"CallHook() with the infinite logging hook did not return within %s",
			shortLogLocalReturnLimit,
		)
	}

	callElapsed := time.Since(start)

	drained, drainErr := blocked.restoreAndDrain(shortLogDrainLimit)
	text := string(prefix) + string(drained)

	reportShortLogState(t,
		"case=%s callhook_finished=true callhook_elapsed=%s drained_bytes=%d drain_err=%v",
		shortLogCaseHook, callElapsed, len(text), drainErr,
	)

	if drainErr != nil {
		t.Fatalf("read the blocked stdout pipe after CallHook(): %v", drainErr)
	}
	if !strings.Contains(text, shortLogHookMarker) {
		t.Fatalf("CallHook() was bounded but the hook marker %q never reached stdout; output = %q",
			shortLogHookMarker, tailOutput(text, 16<<10))
	}
	if callElapsed > timeoutHookBudget {
		t.Fatalf(
			"CallHook() with the infinite logging hook took %s, want at most %s; "+
				"worker step timeout is %s and each drained log line costs up to %s; drained bytes = %d",
			callElapsed, timeoutHookBudget, workerStepTimeout, workerLogWriteBudget, len(text),
		)
	}
	if _, err := runtime.GetPlugin(meta.ID); err != nil {
		t.Fatalf("GetPlugin(%q) after a timed out logging hook error = %v, want the plugin still registered",
			meta.ID, err)
	}
}

// runShortLogCloseCase starts the same endless log flood and then calls Close
// while the hook is still running. Close has to wait for the in-flight hook
// dispatch, but the leftover log frames must not add seconds after the step
// timeout.
func runShortLogCloseCase(t *testing.T) {
	t.Helper()

	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	defer closeRuntimeWithin(t, runtime, timeoutCloseBudget)

	pluginDir, meta := writeTimeoutPlugin(t, "short-log-close", shortLogPluginEntry())
	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	blocked := newShortLogBlockedStdout(t)
	defer blocked.restore()

	hookDone := make(chan struct{})

	go func() {
		runtime.CallHook(Hook(shortLogHook), map[string]interface{}{"case": shortLogCaseClose})
		close(hookDone)
	}()

	prefix, seen, markerElapsed, markerErr := blocked.waitForMarker(shortLogHookMarker, shortLogMarkerBudget)
	if markerErr != nil || !seen {
		blocked.restore()
		reportShortLogState(t,
			"case=%s marker_seen=%t marker_elapsed=%s marker_err=%v",
			shortLogCaseClose, seen, markerElapsed, markerErr,
		)

		t.Fatalf(
			"the infinite logging hook did not reach marker %q within %s (elapsed %s, error %v)",
			shortLogHookMarker, shortLogMarkerBudget, markerElapsed, markerErr,
		)
	}

	reportShortLogState(t,
		"case=%s marker_seen=true marker_elapsed=%s",
		shortLogCaseClose, markerElapsed,
	)

	closeDone := make(chan struct{})
	closeStart := time.Now()

	go func() {
		runtime.Close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
	case <-time.After(shortLogLocalReturnLimit):
		blocked.restore()
		reportShortLogState(t,
			"case=%s close_finished=false close_elapsed>=%s",
			shortLogCaseClose, shortLogLocalReturnLimit,
		)

		t.Fatalf("Close() after the endless log flood did not return within %s", shortLogLocalReturnLimit)
	}

	closeElapsed := time.Since(closeStart)

	select {
	case <-hookDone:
	case <-time.After(shortLogLocalReturnLimit):
		blocked.restore()
		t.Fatalf("the in-flight CallHook() did not return after Close() within %s", shortLogLocalReturnLimit)
	}

	drained, drainErr := blocked.restoreAndDrain(shortLogDrainLimit)
	text := string(prefix) + string(drained)

	reportShortLogState(t,
		"case=%s close_finished=true close_elapsed=%s drained_bytes=%d drain_err=%v",
		shortLogCaseClose, closeElapsed, len(text), drainErr,
	)

	if drainErr != nil {
		t.Fatalf("read the blocked stdout pipe after Close(): %v", drainErr)
	}
	if !strings.Contains(text, shortLogHookMarker) {
		t.Fatalf("the hook marker %q never reached stdout before Close(); output = %q",
			shortLogHookMarker, tailOutput(text, 16<<10))
	}
	if closeElapsed > timeoutCloseBudget {
		t.Fatalf(
			"Close() after the endless log flood took %s, want at most %s; "+
				"worker step timeout is %s and each drained log line costs up to %s; drained bytes = %d",
			closeElapsed, timeoutCloseBudget, workerStepTimeout, workerLogWriteBudget, len(text),
		)
	}
	if _, err := runtime.GetPlugin(meta.ID); err == nil {
		t.Fatalf("GetPlugin(%q) after Close() error = nil, want plugin not found", meta.ID)
	}
}

// shortLogPluginEntry logs one marker and then floods stdout with one-byte log
// entries forever. The marker proves the hook body ran, not just that a request
// timed out.
func shortLogPluginEntry() string {
	return fmt.Sprintf(`JellyCMS.registerHook(%q, function (data) {
  JellyCMS.log.info(%q);
  while (true) {
    JellyCMS.log.info("x");
  }
});
`, shortLogHook, shortLogHookMarker)
}

// shortLogCalibratePipeSize confirms the Linux pipe size used by os.Pipe and
// therefore by exec.Cmd.StdoutPipe. It changes only a pipe this test owns.
func shortLogCalibratePipeSize(t *testing.T) int {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create calibration pipe: %v", err)
	}
	defer func() {
		_ = reader.Close()
		_ = writer.Close()
	}()

	if runtime.GOOS != "linux" {
		return -1
	}

	if _, err := unix.FcntlInt(writer.Fd(), shortLogFSetPipeSize, shortLogPipeSize); err != nil {
		t.Fatalf("set calibration pipe capacity to %d: %v", shortLogPipeSize, err)
	}

	size, err := unix.FcntlInt(writer.Fd(), shortLogFGetPipeSize, 0)
	if err != nil {
		t.Fatalf("read calibration pipe capacity: %v", err)
	}
	if size != shortLogPipeSize {
		t.Fatalf("calibration pipe capacity = %d, want %d", size, shortLogPipeSize)
	}

	return size
}

// shortLogBlockedStdout replaces os.Stdout with the write end of a pipe. Until
// restoreAndDrain runs, no goroutine reads that pipe, so writes block once the
// kernel buffer is full.
type shortLogBlockedStdout struct {
	previous *os.File
	reader   *os.File
	writer   *os.File

	restoreOnce sync.Once
}

func newShortLogBlockedStdout(t *testing.T) *shortLogBlockedStdout {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create blocked stdout pipe: %v", err)
	}

	previous := os.Stdout
	os.Stdout = writer

	return &shortLogBlockedStdout{previous: previous, reader: reader, writer: writer}
}

// waitForMarker reads the blocked pipe until marker appears, then stops
// reading so the remaining log flood fills the pipe. Readiness comes from the
// marker itself, never from a fixed delay.
func (b *shortLogBlockedStdout) waitForMarker(marker string, limit time.Duration) ([]byte, bool, time.Duration, error) {
	type readResult struct {
		data []byte
		err  error
	}

	start := time.Now()
	done := make(chan readResult, 1)

	go func() {
		var seen []byte

		buf := make([]byte, 4096)
		for {
			n, err := b.reader.Read(buf)
			if n > 0 {
				seen = append(seen, buf[:n]...)
				if bytes.Contains(seen, []byte(marker)) {
					done <- readResult{data: seen}

					return
				}
			}
			if err != nil {
				done <- readResult{data: seen, err: err}

				return
			}
		}
	}()

	select {
	case result := <-done:
		return result.data, bytes.Contains(result.data, []byte(marker)), time.Since(start), result.err
	case <-time.After(limit):
		return nil, false, time.Since(start), fmt.Errorf("marker %q not observed within %s", marker, limit)
	}
}

// restore puts the original os.Stdout back and releases both pipe ends. It is
// safe to call after restoreAndDrain and after repeated failure paths.
func (b *shortLogBlockedStdout) restore() {
	b.restoreOnce.Do(func() {
		os.Stdout = b.previous
		_ = b.writer.Close()
		_ = b.reader.Close()
	})
}

// restoreAndDrain restores os.Stdout, closes the pipe writer and reads every
// byte still buffered in the pipe. Closing the writer also releases a log
// writer goroutine blocked on the full pipe.
func (b *shortLogBlockedStdout) restoreAndDrain(limit time.Duration) ([]byte, error) {
	b.restoreOnce.Do(func() {
		os.Stdout = b.previous
		_ = b.writer.Close()
	})

	_ = b.reader.SetReadDeadline(time.Now().Add(limit))
	data, err := io.ReadAll(b.reader)
	_ = b.reader.Close()

	return data, err
}

// shortLogChildEnv builds the child environment, replacing selected keys.
func shortLogChildEnv(extras map[string]string) []string {
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

// reportShortLogState writes one machine-readable state line to stderr, which
// stays usable while stdout is blocked.
func reportShortLogState(t *testing.T, format string, args ...interface{}) {
	t.Helper()

	fmt.Fprintf(os.Stderr, "%s%s\n", shortLogStatePrefix, fmt.Sprintf(format, args...))
}

// printShortLogState forwards helper state lines from child output.
func printShortLogState(name, output string) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, shortLogStatePrefix) {
			continue
		}

		fmt.Printf(
			"JELLY_PLUGIN_SHORT_LOG_RESULT:%s %s\n",
			name, strings.TrimPrefix(line, shortLogStatePrefix),
		)
	}
}
