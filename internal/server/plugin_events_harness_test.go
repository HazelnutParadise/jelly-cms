package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

// Shared fixture for the plugin event tests (docs/tickets/27-plugin-events.md).
//
// The harness boots the isolated PostgreSQL site from newAccessHarness, adds a
// real plugin runtime and loads one fixture plugin that registers every hook
// in plugin.KnownHooks() and logs `event:<hook>:<payload JSON>` for each
// delivery. Tests drive real HTTP requests, call flush and then read the
// captured events. Nothing here fakes the runtime or the worker process.

const pluginEventsFixtureID = "events-fixture"

type pluginEventsHarness struct {
	*accessHarness
	runtime *plugin.Runtime
	sink    *pluginEventSink
}

// pluginEventSink captures every plugin log line delivered through
// plugin.SetLogSink while also forwarding it to the production sink, so the
// admin logs tab can be asserted in the same test.
type pluginEventSink struct {
	mu    sync.Mutex
	lines []pluginLogLine
}

type pluginLogLine struct {
	pluginID string
	level    string
	message  string
}

func (s *pluginEventSink) add(pluginID, level, message string) {
	s.mu.Lock()
	s.lines = append(s.lines, pluginLogLine{pluginID: pluginID, level: level, message: message})
	s.mu.Unlock()
}

func (s *pluginEventSink) snapshot() []pluginLogLine {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]pluginLogLine(nil), s.lines...)
}

// pluginEvent is one captured delivery: the hook name and the decoded payload
// the plugin callback received.
type pluginEvent struct {
	Hook    string
	Payload map[string]any
}

func newPluginEventsHarness(t *testing.T) *pluginEventsHarness {
	t.Helper()
	base := newAccessHarness(t)
	runtime, err := plugin.NewRuntime()
	if err != nil {
		t.Fatalf("create plugin runtime: %v", err)
	}
	h := &pluginEventsHarness{accessHarness: base, runtime: runtime, sink: &pluginEventSink{}}
	h.rebuildRoutes(t, theme.NewManager("web/themes"))
	t.Cleanup(func() {
		runtime.Close()
		plugin.SetLogSink(nil)
		configurePluginEvents(nil)
	})
	h.installFixturePlugin(t, pluginEventsFixtureID, nil)
	return h
}

// rebuildRoutes registers the routes again on a fresh echo with tm (for
// example an activated theme) and the harness runtime, then re-installs the
// capturing log sink, because RegisterRoutes installs the production sink.
func (h *pluginEventsHarness) rebuildRoutes(t *testing.T, tm *theme.Manager) {
	t.Helper()
	e := echo.New()
	RegisterRoutes(e, tm, h.runtime)
	h.accessHarness.echo = e
	sink := h.sink
	plugin.SetLogSink(func(pluginID, level, message string) {
		sink.add(pluginID, level, message)
		forwardPluginLog(pluginID, level, message)
	})
}

// installFixturePlugin writes and loads a plugin that registers hooks (all
// known hooks when nil) and logs every delivery. The manifest declares the
// "events" permission and the enabled plugins row carries the matching
// permissions_granted_hash, so the event filter admits it. withEvents false
// installs a plugin without that permission (it must not receive site events).
func (h *pluginEventsHarness) installFixturePlugin(t *testing.T, id string, hooks []plugin.Hook) {
	t.Helper()
	h.installFixturePluginWithPermission(t, id, hooks, true)
}

func (h *pluginEventsHarness) installFixturePluginWithPermission(t *testing.T, id string, hooks []plugin.Hook, withEvents bool) {
	t.Helper()
	if hooks == nil {
		hooks = plugin.KnownHooks()
	}
	names := make([]string, 0, len(hooks))
	for _, hook := range hooks {
		names = append(names, string(hook))
	}
	list, err := json.Marshal(names)
	if err != nil {
		t.Fatalf("marshal hook list: %v", err)
	}
	script := fmt.Sprintf(`
var hooks = %s;
hooks.forEach(function (name) {
  JellyCMS.registerHook(name, function (data) {
    JellyCMS.log.info("event:" + name + ":" + JSON.stringify(data));
  });
});
JellyCMS.log.info("fixture-ready");
`, string(list))
	dir := filepath.Join("data", "plugins", id)
	meta := plugin.PluginMetadata{ID: id, Name: "Events fixture " + id, Version: "1.0.0", Entrypoint: "index.js"}
	if withEvents {
		meta.Permissions = []plugin.PermissionSpec{{Name: "events"}}
	}
	writeReloadPlugin(t, dir, meta, script)
	granted := plugin.PermissionNamesHash(plugin.ManifestPermissionNames(meta))
	if err := h.db.Exec(`INSERT INTO plugins (id, kind, version, source, enabled, signature, permissions_granted_hash) VALUES (?, 'plugin', '1.0.0', 'upload', TRUE, 'unsigned', ?) ON CONFLICT (id) DO UPDATE SET enabled = TRUE, permissions_granted_hash = EXCLUDED.permissions_granted_hash`, id, granted).Error; err != nil {
		t.Fatalf("insert plugins row for %s: %v", id, err)
	}
	if err := h.runtime.Load(context.Background(), dir); err != nil {
		t.Fatalf("load fixture plugin %s: %v", id, err)
	}
}

// flush waits until every event emitted so far has been dispatched.
func (h *pluginEventsHarness) flush(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.runtime.Flush(ctx); err != nil {
		t.Fatalf("flush plugin events: %v", err)
	}
}

// events returns the deliveries of hook captured so far for pluginID, in
// delivery order. Call flush first.
func (h *pluginEventsHarness) events(t *testing.T, pluginID string, hook plugin.Hook) []pluginEvent {
	t.Helper()
	prefix := "event:" + string(hook) + ":"
	var out []pluginEvent
	for _, line := range h.sink.snapshot() {
		if line.pluginID != pluginID || !strings.HasPrefix(line.message, prefix) {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line.message, prefix)), &payload); err != nil {
			t.Fatalf("decode %s payload %q: %v", hook, line.message, err)
		}
		out = append(out, pluginEvent{Hook: string(hook), Payload: payload})
	}
	return out
}

// allEvents returns every captured delivery for pluginID in order, so a test
// can assert relative ordering across hooks.
func (h *pluginEventsHarness) allEvents(t *testing.T, pluginID string) []pluginEvent {
	t.Helper()
	var out []pluginEvent
	for _, line := range h.sink.snapshot() {
		if line.pluginID != pluginID || !strings.HasPrefix(line.message, "event:") {
			continue
		}
		rest := strings.TrimPrefix(line.message, "event:")
		name, raw, ok := strings.Cut(rest, ":")
		if !ok {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatalf("decode %s payload %q: %v", name, line.message, err)
		}
		out = append(out, pluginEvent{Hook: name, Payload: payload})
	}
	return out
}

// waitEvents flushes the runtime and waits up to five seconds for at least n
// deliveries of hook, returning them. It fails the test on timeout with the
// captured log for diagnosis.
func (h *pluginEventsHarness) waitEvents(t *testing.T, hook plugin.Hook, n int) []pluginEvent {
	t.Helper()
	h.flush(t)
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := h.events(t, pluginEventsFixtureID, hook)
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected at least %d %s deliveries, got %d; captured log:\n%s", n, hook, len(got), h.sink.dump())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// requireNoEvent flushes and fails when hook was delivered at all.
func (h *pluginEventsHarness) requireNoEvent(t *testing.T, hook plugin.Hook) {
	t.Helper()
	h.flush(t)
	if got := h.events(t, pluginEventsFixtureID, hook); len(got) != 0 {
		t.Fatalf("expected no %s delivery, got %d: %+v", hook, len(got), got)
	}
}

// reset forgets the captured deliveries so the next assertion only sees new
// events.
func (h *pluginEventsHarness) reset(t *testing.T) {
	t.Helper()
	h.flush(t)
	h.sink.mu.Lock()
	h.sink.lines = nil
	h.sink.mu.Unlock()
}

func (s *pluginEventSink) dump() string {
	var b strings.Builder
	for _, line := range s.snapshot() {
		fmt.Fprintf(&b, "[%s] [%s] %s\n", line.pluginID, line.level, line.message)
	}
	return b.String()
}

// nested returns payload[key] as an object, failing when it is missing.
func nested(t *testing.T, payload map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := payload[key].(map[string]any)
	if !ok {
		t.Fatalf("payload has no object %q: %v", key, payload)
	}
	return value
}

// number returns payload[key] as float64 (JSON numbers), failing when absent.
func number(t *testing.T, payload map[string]any, key string) float64 {
	t.Helper()
	value, ok := payload[key].(float64)
	if !ok {
		t.Fatalf("payload has no number %q: %v", key, payload)
	}
	return value
}
