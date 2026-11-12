package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Tests for the site event path (ticket 28): Emit,
// Flush, the event filter, failure isolation, queue limits, hook snapshots,
// registerHook warnings, the log sink and the hook catalogue. Every plugin runs
// in a real worker subprocess. Log lines reach the tests through SetLogSink,
// because plugin log output is written asynchronously after a hook returns.

const (
	evtWaitBudget    = 5 * time.Second
	evtSuspendText   = "events suspended after 5 consecutive failures"
	evtNotDispatched = "is not dispatched by this site"
)

type evtLine struct {
	plugin  string
	level   string
	message string
}

// evtSink records every line the runtime hands to the log sink.
type evtSink struct {
	mu    sync.Mutex
	lines []evtLine
}

// evtInstallSink routes the process log sink into a recorder for the test and
// removes it again at cleanup.
func evtInstallSink(t *testing.T) *evtSink {
	t.Helper()

	sink := &evtSink{}
	SetLogSink(func(pluginID, level, message string) {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		sink.lines = append(sink.lines, evtLine{plugin: pluginID, level: level, message: message})
	})
	t.Cleanup(func() { SetLogSink(nil) })

	return sink
}

// count returns how many recorded lines match the plugin, level and substring.
// An empty plugin, level or substring matches anything.
func (s *evtSink) count(pluginID, level, substr string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := 0
	for _, line := range s.lines {
		if pluginID != "" && line.plugin != pluginID {
			continue
		}
		if level != "" && line.level != level {
			continue
		}
		if !strings.Contains(line.message, substr) {
			continue
		}
		n++
	}

	return n
}

// messages returns the messages of one plugin at one level that start with
// prefix, in the order they arrived.
func (s *evtSink) messages(pluginID, level, prefix string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []string
	for _, line := range s.lines {
		if line.plugin == pluginID && line.level == level && strings.HasPrefix(line.message, prefix) {
			out = append(out, line.message)
		}
	}

	return out
}

// evtWaitFor polls cond until it holds or the wait budget ends.
func evtWaitFor(cond func() bool) bool {
	deadline := time.Now().Add(evtWaitBudget)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}

	return cond()
}

func evtSameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}

	return true
}

func evtNewRuntime(t *testing.T) *Runtime {
	t.Helper()

	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	return runtime
}

// evtPlugin writes a plugin with the given manifest and script into a fresh
// directory. Unlike writeTestPlugin the id, actions and permissions are chosen
// by the test.
func evtPlugin(t *testing.T, meta PluginMetadata, script string) string {
	t.Helper()

	if meta.Name == "" {
		meta.Name = meta.ID
	}
	meta.Version = "1.0.0"
	meta.Entrypoint = "index.js"

	dir := t.TempDir()
	manifest, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal manifest for %s: %v", meta.ID, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), manifest, 0o600); err != nil {
		t.Fatalf("write manifest for %s: %v", meta.ID, err)
	}
	evtWriteScript(t, dir, script)

	return dir
}

func evtWriteScript(t *testing.T, dir, script string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(script), 0o600); err != nil {
		t.Fatalf("write index.js: %v", err)
	}
}

func evtLoad(t *testing.T, runtime *Runtime, dir string) {
	t.Helper()

	var err error
	captureStdout(t, func() {
		err = runtime.Load(context.Background(), dir)
	})
	if err != nil {
		t.Fatalf("Load(%s) error = %v", dir, err)
	}
}

// evtFlush calls Flush with its own bounded context.
func evtFlush(runtime *Runtime, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return runtime.Flush(ctx)
}

func evtNumbered(prefix string, from, to int) []string {
	out := make([]string, 0, to-from+1)
	for n := from; n <= to; n++ {
		out = append(out, prefix+strconv.Itoa(n))
	}

	return out
}

// Wanted result: Emit returns quickly, each plugin gets its events in order,
// and Flush returns nil once they are delivered.
func TestPluginEventsEmitDeliversInOrderWithoutBlocking(t *testing.T) {
	runtime := evtNewRuntime(t)
	sink := evtInstallSink(t)

	const id = "ordered-plugin"
	dir := evtPlugin(t, PluginMetadata{ID: id}, `
JellyCMS.registerHook("OnPostSave", function (data) {
  JellyCMS.log.info("ev:" + data.n);
});
`)
	evtLoad(t, runtime, dir)

	start := time.Now()
	for n := 1; n <= 20; n++ {
		runtime.Emit(HookOnPostSave, map[string]interface{}{"n": n})
	}
	if elapsed := time.Since(start); elapsed >= 500*time.Millisecond {
		t.Errorf("20 Emit calls took %s, want under 500ms", elapsed)
	}

	if err := evtFlush(runtime, 5*time.Second); err != nil {
		t.Fatalf("Flush(5s) error = %v, want nil", err)
	}
	evtWaitFor(func() bool { return sink.count(id, "info", "ev:") >= 20 })

	want := evtNumbered("ev:", 1, 20)
	got := sink.messages(id, "info", "ev:")
	if !evtSameStrings(got, want) {
		t.Fatalf("plugin received events %v, want %v", got, want)
	}
}

// Wanted result: a plugin without the hook is never called and HasHook tells
// the two plugins apart. HookDispatches counts only the Emit request.
func TestPluginEventsSkipPluginsWithoutTheHook(t *testing.T) {
	runtime := evtNewRuntime(t)
	sink := evtInstallSink(t)

	const hooked, bootOnly = "hooked-plugin", "boot-only-plugin"
	hookedDir := evtPlugin(t, PluginMetadata{ID: hooked}, `
JellyCMS.registerHook("OnPostSave", function (data) {
  JellyCMS.log.info("hooked-ev:" + data.n);
});
`)
	bootDir := evtPlugin(t, PluginMetadata{ID: bootOnly}, `
JellyCMS.registerHook("OnBoot", function (data) {
  JellyCMS.log.info("boot-only-booted");
});
`)
	evtLoad(t, runtime, hookedDir)
	evtLoad(t, runtime, bootDir)

	if !runtime.HasHook(hooked, HookOnPostSave) {
		t.Errorf("HasHook(%q, OnPostSave) = false, want true", hooked)
	}
	if runtime.HasHook(bootOnly, HookOnPostSave) {
		t.Errorf("HasHook(%q, OnPostSave) = true, want false", bootOnly)
	}
	if runtime.HasHook("missing", HookOnPostSave) {
		t.Error("HasHook(missing, OnPostSave) = true, want false")
	}

	base := runtime.HookDispatches()
	runtime.Emit(HookOnPostSave, map[string]interface{}{"n": 1})
	if err := evtFlush(runtime, 5*time.Second); err != nil {
		t.Fatalf("Flush(5s) error = %v, want nil", err)
	}
	evtWaitFor(func() bool { return sink.count(hooked, "info", "hooked-ev:1") == 1 })

	if got := runtime.HookDispatches() - base; got != 1 {
		t.Errorf("HookDispatches() grew by %d for one event, want 1", got)
	}
	if n := sink.count(bootOnly, "", "hooked-ev"); n != 0 {
		t.Errorf("plugin without the hook received the event; %d matching lines", n)
	}

	if err := runtime.Unload(context.Background(), hooked); err != nil {
		t.Fatalf("Unload(%q) error = %v", hooked, err)
	}
	if runtime.HasHook(hooked, HookOnPostSave) {
		t.Errorf("HasHook(%q, OnPostSave) after Unload = true, want false", hooked)
	}
}

// Wanted result: an event filter rejecting a plugin means that plugin gets no
// worker request and no output, and HookDispatches counts only the allowed one.
func TestPluginEventsFilterKeepsRejectedPluginSilent(t *testing.T) {
	runtime := evtNewRuntime(t)
	sink := evtInstallSink(t)

	const allowed, rejected = "allowed-plugin", "rejected-plugin"
	allowedDir := evtPlugin(t, PluginMetadata{ID: allowed}, `
JellyCMS.registerHook("OnPostSave", function (data) {
  JellyCMS.log.info("allowed-ev:" + data.n);
});
`)
	rejectedDir := evtPlugin(t, PluginMetadata{ID: rejected}, `
JellyCMS.registerHook("OnPostSave", function (data) {
  JellyCMS.log.info("rejected-ev:" + data.n);
});
`)

	runtime.SetEventFilter(func(meta PluginMetadata, hook Hook) bool {
		return meta.ID == allowed
	})
	evtLoad(t, runtime, allowedDir)
	evtLoad(t, runtime, rejectedDir)

	base := runtime.HookDispatches()
	runtime.Emit(HookOnPostSave, map[string]interface{}{"n": 1})
	runtime.Emit(HookOnPostSave, map[string]interface{}{"n": 2})
	if err := evtFlush(runtime, 5*time.Second); err != nil {
		t.Fatalf("Flush(5s) error = %v, want nil", err)
	}
	evtWaitFor(func() bool { return sink.count(allowed, "info", "allowed-ev:2") == 1 })

	if n := sink.count(rejected, "", ""); n != 0 {
		t.Errorf("rejected plugin produced %d lines; want no output at all", n)
	}
	if got := runtime.HookDispatches() - base; got != 2 {
		t.Errorf("HookDispatches() grew by %d for two events to one allowed plugin, want 2", got)
	}
}

// Wanted result: a plugin whose callback always throws is logged at error level
// and does not stop the other plugin, which keeps getting every event.
func TestPluginEventsFailingPluginDoesNotStopOthers(t *testing.T) {
	runtime := evtNewRuntime(t)
	sink := evtInstallSink(t)

	const broken, healthy = "broken-plugin", "healthy-plugin"
	brokenDir := evtPlugin(t, PluginMetadata{ID: broken}, `
JellyCMS.registerHook("OnPostSave", function (data) {
  throw new Error("broken-on-save");
});
`)
	healthyDir := evtPlugin(t, PluginMetadata{ID: healthy}, `
JellyCMS.registerHook("OnPostSave", function (data) {
  JellyCMS.log.info("healthy-ev:" + data.n);
});
`)
	evtLoad(t, runtime, brokenDir)
	evtLoad(t, runtime, healthyDir)

	for n := 1; n <= 3; n++ {
		runtime.Emit(HookOnPostSave, map[string]interface{}{"n": n})
	}
	if err := evtFlush(runtime, 5*time.Second); err != nil {
		t.Fatalf("Flush(5s) error = %v, want nil", err)
	}

	if !evtWaitFor(func() bool { return sink.count(healthy, "info", "healthy-ev:") == 3 }) {
		t.Errorf("healthy plugin received %v, want 3 events", sink.messages(healthy, "info", "healthy-ev:"))
	}
	if !evtWaitFor(func() bool { return sink.count(broken, "error", "OnPostSave") >= 1 }) {
		t.Errorf("failing callback was not logged at error level with OnPostSave; lines = %+v", sink.lines)
	}

	runtime.Emit(HookOnPostSave, map[string]interface{}{"n": 4})
	if err := evtFlush(runtime, 5*time.Second); err != nil {
		t.Fatalf("second Flush(5s) error = %v, want nil", err)
	}
	if !evtWaitFor(func() bool { return sink.count(healthy, "info", "healthy-ev:4") == 1 }) {
		t.Errorf("healthy plugin missed the next event after a failure; messages = %v",
			sink.messages(healthy, "info", "healthy-ev:"))
	}
}

// Wanted result: after five consecutive failures the plugin's events are
// suspended with one warn, later events are dropped silently, and a reload
// resumes delivery.
func TestPluginEventsSuspendAfterConsecutiveFailuresUntilReload(t *testing.T) {
	runtime := evtNewRuntime(t)
	sink := evtInstallSink(t)

	const id = "failing-plugin"
	dir := evtPlugin(t, PluginMetadata{ID: id}, `
JellyCMS.registerHook("OnPostSave", function (data) {
  throw new Error("always-broken");
});
`)
	evtLoad(t, runtime, dir)
	base := runtime.HookDispatches()

	for n := 1; n <= 7; n++ {
		runtime.Emit(HookOnPostSave, map[string]interface{}{"n": n})
	}
	if err := evtFlush(runtime, 5*time.Second); err != nil {
		t.Fatalf("Flush(5s) error = %v, want nil", err)
	}

	evtWaitFor(func() bool {
		return sink.count(id, "error", "") == 5 && sink.count(id, "warn", evtSuspendText) == 1
	})
	time.Sleep(200 * time.Millisecond) // a late sixth error or second warn would appear here

	if got := sink.count(id, "error", ""); got != 5 {
		t.Errorf("error lines = %d, want exactly 5", got)
	}
	if got := sink.count(id, "warn", evtSuspendText); got != 1 {
		t.Errorf("suspension warn lines = %d, want exactly 1", got)
	}
	if got := runtime.HookDispatches() - base; got != 5 {
		t.Errorf("HookDispatches() grew by %d for 7 events after suspension, want 5", got)
	}

	evtWriteScript(t, dir, `
JellyCMS.registerHook("OnPostSave", function (data) {
  JellyCMS.log.info("recovered-ev:" + data.n);
});
`)
	var reloadErr error
	captureStdout(t, func() {
		reloadErr = runtime.Reload(context.Background(), dir)
	})
	if reloadErr != nil {
		t.Fatalf("Reload() error = %v", reloadErr)
	}

	runtime.Emit(HookOnPostSave, map[string]interface{}{"n": 8})
	if err := evtFlush(runtime, 5*time.Second); err != nil {
		t.Fatalf("Flush(5s) after reload error = %v, want nil", err)
	}
	if !evtWaitFor(func() bool { return sink.count(id, "info", "recovered-ev:8") == 1 }) {
		t.Errorf("reloaded plugin did not receive the event; messages = %v",
			sink.messages(id, "info", "recovered-ev:"))
	}
}

// Wanted result: with a queue of 2 and a slow plugin, Emit never waits, excess
// events are dropped for that plugin and the runtime log says so.
func TestPluginEventsFullQueueDropsAndLogs(t *testing.T) {
	originalSize := eventQueueSize
	t.Cleanup(func() { eventQueueSize = originalSize })
	eventQueueSize = 2

	runtime := evtNewRuntime(t)
	sink := evtInstallSink(t)

	const id = "slow-queue-plugin"
	dir := evtPlugin(t, PluginMetadata{ID: id}, `
JellyCMS.registerHook("OnPostSave", function (data) {
  var start = Date.now();
  while (Date.now() - start < 100) {}
  JellyCMS.log.info("slow6:" + data.n);
});
`)
	evtLoad(t, runtime, dir)

	var emitElapsed time.Duration
	var flushErr error
	runtimeLog := captureStdout(t, func() {
		start := time.Now()
		for n := 1; n <= 10; n++ {
			runtime.Emit(HookOnPostSave, map[string]interface{}{"n": n})
		}
		emitElapsed = time.Since(start)
		flushErr = evtFlush(runtime, 10*time.Second)
	})

	if emitElapsed >= 200*time.Millisecond {
		t.Errorf("10 Emit calls took %s with a slow plugin, want under 200ms", emitElapsed)
	}
	if flushErr != nil {
		t.Errorf("Flush(10s) error = %v, want nil", flushErr)
	}
	if !strings.Contains(runtimeLog, "dropped for "+id+": queue full") {
		t.Errorf("runtime log does not record the drop; output = %q", runtimeLog)
	}

	evtWaitFor(func() bool { return sink.count(id, "info", "slow6:") >= 1 })
	time.Sleep(100 * time.Millisecond)
	if got := sink.count(id, "info", "slow6:"); got >= 10 {
		t.Errorf("slow plugin delivered %d of 10 events, want fewer than 10", got)
	}
}

// Wanted result: the byte bound drops the second large event while the first,
// which must still reach the plugin, is in flight.
func TestPluginEventsByteLimitDropsSecondEvent(t *testing.T) {
	originalBytes := eventQueueBytes
	t.Cleanup(func() { eventQueueBytes = originalBytes })
	eventQueueBytes = 1024

	runtime := evtNewRuntime(t)
	sink := evtInstallSink(t)

	const id = "large-payload-plugin"
	dir := evtPlugin(t, PluginMetadata{ID: id}, `
JellyCMS.registerHook("OnPostSave", function (data) {
  var start = Date.now();
  while (Date.now() - start < 300) {}
  JellyCMS.log.info("big7:" + data.n);
});
`)
	evtLoad(t, runtime, dir)

	blob := strings.Repeat("x", 2048)
	runtimeLog := captureStdout(t, func() {
		runtime.Emit(HookOnPostSave, map[string]interface{}{"n": 1, "blob": blob})
		runtime.Emit(HookOnPostSave, map[string]interface{}{"n": 2, "blob": blob})
		if err := evtFlush(runtime, 5*time.Second); err != nil {
			t.Errorf("Flush(5s) error = %v, want nil", err)
		}
	})

	if !evtWaitFor(func() bool { return sink.count(id, "info", "big7:1") == 1 }) {
		t.Errorf("first ~2KiB event did not reach the plugin; messages = %v",
			sink.messages(id, "info", "big7:"))
	}
	time.Sleep(100 * time.Millisecond)
	if n := sink.count(id, "info", "big7:2"); n != 0 {
		t.Errorf("second event was delivered %d times, want dropped by the byte limit", n)
	}
	if !strings.Contains(runtimeLog, "dropped for "+id) {
		t.Errorf("runtime log does not record the byte-limit drop; output = %q", runtimeLog)
	}
}

// Wanted result: Flush honours its context and returns ctx.Err() while a slow
// plugin is still busy, and a later Flush delivers everything.
func TestPluginEventsFlushHonoursContext(t *testing.T) {
	runtime := evtNewRuntime(t)
	sink := evtInstallSink(t)

	const id = "slow-flush-plugin"
	dir := evtPlugin(t, PluginMetadata{ID: id}, `
JellyCMS.registerHook("OnPostSave", function (data) {
  var start = Date.now();
  while (Date.now() - start < 300) {}
  JellyCMS.log.info("slow8:" + data.n);
});
`)
	evtLoad(t, runtime, dir)

	for n := 1; n <= 3; n++ {
		runtime.Emit(HookOnPostSave, map[string]interface{}{"n": n})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	err := runtime.Flush(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Flush(50ms) error = %v, want context.DeadlineExceeded", err)
	}

	if err := evtFlush(runtime, 5*time.Second); err != nil {
		t.Fatalf("Flush(5s) error = %v, want nil", err)
	}
	want := evtNumbered("slow8:", 1, 3)
	if !evtWaitFor(func() bool { return sink.count(id, "info", "slow8:") == 3 }) {
		t.Fatalf("slow plugin delivered %v, want %v", sink.messages(id, "info", "slow8:"), want)
	}
	if got := sink.messages(id, "info", "slow8:"); !evtSameStrings(got, want) {
		t.Errorf("slow plugin delivered %v, want %v", got, want)
	}
}

// Wanted result: Close delivers what was queued, after Close Emit is silent and
// does not panic, and Flush returns nil at once.
func TestPluginEventsCloseDeliversQueuedEvents(t *testing.T) {
	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(runtime.Close)
	sink := evtInstallSink(t)

	const id = "close-plugin"
	dir := evtPlugin(t, PluginMetadata{ID: id}, `
JellyCMS.registerHook("OnPostSave", function (data) {
  JellyCMS.log.info("close9:" + data.n);
});
`)
	evtLoad(t, runtime, dir)

	for n := 1; n <= 3; n++ {
		runtime.Emit(HookOnPostSave, map[string]interface{}{"n": n})
	}
	runtime.Close()

	if !evtWaitFor(func() bool { return sink.count(id, "info", "close9:") == 3 }) {
		t.Errorf("Close() delivered %v, want 3 queued events", sink.messages(id, "info", "close9:"))
	}

	panicked := func() (recovered any) {
		defer func() { recovered = recover() }()
		runtime.Emit(HookOnPostSave, map[string]interface{}{"n": 4})

		return nil
	}()
	if panicked != nil {
		t.Fatalf("Emit after Close panicked: %v", panicked)
	}
	time.Sleep(200 * time.Millisecond)
	if n := sink.count(id, "info", "close9:4"); n != 0 {
		t.Errorf("event emitted after Close was delivered %d times, want 0", n)
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Flush(ctx); err != nil {
		t.Errorf("Flush() after Close error = %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed >= 500*time.Millisecond {
		t.Errorf("Flush() after Close took %s, want immediate", elapsed)
	}
}

// Wanted result: registerHook warns once per unknown name, accepts declared
// action ids, and the plugin still loads.
func TestPluginEventsRegisterHookWarnsOnUnknownNames(t *testing.T) {
	runtime := evtNewRuntime(t)
	sink := evtInstallSink(t)

	const id = "hook-name-plugin"
	meta := PluginMetadata{
		ID:      id,
		Actions: []ActionSpec{{ID: "sync"}},
	}
	dir := evtPlugin(t, meta, `
JellyCMS.registerHook("OnWhatever", function (data) {});
JellyCMS.registerHook("OnWhatever", function (data) {});
JellyCMS.registerHook("OnPostSave", function (data) {});
JellyCMS.registerHook("action:sync", function (data) {});
JellyCMS.registerHook("action:nope", function (data) {});
`)
	evtLoad(t, runtime, dir)

	if !evtWaitFor(func() bool {
		return sink.count(id, "warn", `hook "OnWhatever" `+evtNotDispatched) >= 1 &&
			sink.count(id, "warn", `hook "action:nope" `+evtNotDispatched) >= 1
	}) {
		t.Errorf("registration warnings missing; lines = %+v", sink.lines)
	}
	time.Sleep(100 * time.Millisecond)

	if got := sink.count(id, "warn", `hook "OnWhatever" `+evtNotDispatched); got != 1 {
		t.Errorf("OnWhatever warned %d times, want exactly 1", got)
	}
	if got := sink.count(id, "warn", "action:nope"); got != 1 {
		t.Errorf("action:nope warned %d times, want exactly 1", got)
	}
	if got := sink.count(id, "warn", "OnPostSave"); got != 0 {
		t.Errorf("known hook OnPostSave produced %d warnings, want 0", got)
	}
	if got := sink.count(id, "warn", "action:sync"); got != 0 {
		t.Errorf("declared action hook action:sync produced %d warnings, want 0", got)
	}
}

// Wanted result: the sink receives plugin log lines and console errors, stops
// after SetLogSink(nil), and a panicking sink fails neither Load nor CallHook.
func TestPluginEventsLogSinkReceivesLinesAndStops(t *testing.T) {
	runtime := evtNewRuntime(t)
	sink := evtInstallSink(t)

	const id = "sink-plugin"
	dir := evtPlugin(t, PluginMetadata{ID: id}, `
JellyCMS.log.warn("hello");
console.error("boom");
JellyCMS.registerHook("OnPostSave", function (data) {
  JellyCMS.log.info("callback-after-nil:" + data.n);
});
`)
	evtLoad(t, runtime, dir)

	if !evtWaitFor(func() bool {
		return sink.count(id, "warn", "hello") == 1 && sink.count(id, "error", "boom") == 1
	}) {
		t.Errorf("sink lines = %+v, want one warn hello and one error boom", sink.lines)
	}
	if got := sink.messages(id, "warn", ""); !evtSameStrings(got, []string{"hello"}) {
		t.Errorf("warn lines = %v, want [hello]", got)
	}
	if got := sink.messages(id, "error", ""); !evtSameStrings(got, []string{"boom"}) {
		t.Errorf("error lines = %v, want [boom]", got)
	}

	SetLogSink(nil)
	before := sink.count(id, "", "")

	output := captureStdout(t, func() {
		runtime.CallHook(HookOnPostSave, map[string]interface{}{"n": 1})
	})
	assertContains(t, output, "callback-after-nil:1")
	time.Sleep(100 * time.Millisecond)

	if after := sink.count(id, "", ""); after != before {
		t.Errorf("sink received %d lines after SetLogSink(nil), want none", after-before)
	}
}

// Wanted result: a panic inside the sink is recovered; Load and CallHook keep
// working.
func TestPluginEventsPanickingLogSinkDoesNotFailLoadOrCallHook(t *testing.T) {
	runtime := evtNewRuntime(t)

	// The sink must really run, otherwise the test would pass without the
	// sink being wired up at all.
	var sinkCalled atomic.Bool
	SetLogSink(func(pluginID, level, message string) {
		sinkCalled.Store(true)
		panic("sink exploded")
	})
	t.Cleanup(func() { SetLogSink(nil) })

	const id = "panic-sink-plugin"
	dir := evtPlugin(t, PluginMetadata{ID: id}, `
JellyCMS.log.info("sink-panic-load");
JellyCMS.registerHook("OnPostSave", function (data) {
  JellyCMS.log.info("sink-panic-hook:" + data.n);
});
`)

	var loadErr error
	captureStdout(t, func() {
		loadErr = runtime.Load(context.Background(), dir)
	})
	if loadErr != nil {
		t.Fatalf("Load() with a panicking sink error = %v, want nil", loadErr)
	}

	output := captureStdout(t, func() {
		runtime.CallHook(HookOnPostSave, map[string]interface{}{"n": 1})
	})
	assertContains(t, output, "sink-panic-hook:1")
	if !evtWaitFor(sinkCalled.Load) {
		t.Error("log sink was never called, so the panic path was not exercised")
	}
}

// Wanted result: KnownHooks is the 28-entry catalogue without the removed
// request hooks, returns a copy, and IsKnownHook accepts exactly the catalogue
// and declared actions.
func TestPluginEventsKnownHooksCatalogue(t *testing.T) {
	hooks := KnownHooks()
	if len(hooks) != 28 {
		t.Fatalf("len(KnownHooks()) = %d, want 28", len(hooks))
	}

	contains := func(list []Hook, name Hook) bool {
		for _, hook := range list {
			if hook == name {
				return true
			}
		}

		return false
	}
	if !contains(hooks, HookOnPostSave) || !contains(hooks, HookOnSettingsChange) {
		t.Errorf("KnownHooks() = %v, want OnPostSave and OnSettingsChange", hooks)
	}
	if contains(hooks, Hook("OnRequest")) || contains(hooks, Hook("OnResponse")) {
		t.Errorf("KnownHooks() still lists OnRequest or OnResponse: %v", hooks)
	}

	hooks[0] = Hook("MUTATED")
	again := KnownHooks()
	if again[0] == Hook("MUTATED") {
		t.Error("KnownHooks() returned the shared slice; mutating the result changed the catalogue")
	}

	meta := PluginMetadata{ID: "catalogue-plugin", Actions: []ActionSpec{{ID: "sync"}}}
	checks := []struct {
		name string
		want bool
	}{
		{"OnPostSave", true},
		{"OnWhatever", false},
		{"action:sync", true},
		{"action:nope", false},
		{"action:", false},
	}
	for _, check := range checks {
		if got := IsKnownHook(meta, check.name); got != check.want {
			t.Errorf("IsKnownHook(meta, %q) = %v, want %v", check.name, got, check.want)
		}
	}
}
