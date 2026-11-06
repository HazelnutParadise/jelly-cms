package plugin

import (
	"context"
)

// Event queue, hook snapshots and the log sink (docs/tickets/27-plugin-events.md).
//
// Emit is the site's non-blocking entry point: HTTP handlers never wait for a
// plugin. Every plugin that registered the hook (and passes the event filter)
// gets the already encoded payload on its own bounded queue, drained by its
// own dispatcher goroutine in order; a slow plugin never delays another. The
// queues are in-memory and best effort: a full queue drops the newest event
// with a runtime log line, and Close drops whatever a bounded Flush could not
// deliver.

// eventQueueSize bounds the events waiting for one plugin. eventQueueBytes
// bounds the encoded payload bytes waiting across the whole runtime. Both are
// variables so a package test can shrink them to exercise the drop path.
var (
	eventQueueSize  = 256
	eventQueueBytes = 32 << 20
)

// eventFailureLimit is how many consecutive hook failures (errors or
// timeouts) suspend event dispatch for a plugin until it is reloaded.
const eventFailureLimit = 5

// EventFilter decides whether one loaded plugin receives one event. The site
// uses it to enforce the "events" permission. A nil filter allows every plugin.
type EventFilter func(meta PluginMetadata, hook Hook) bool

// SetEventFilter installs filter for later Emit calls. Boot, shutdown and
// action hooks are not events and never pass through the filter.
func (m *Runtime) SetEventFilter(filter EventFilter) {}

// Emit queues one event for every loaded plugin that registered hook. Wanted
// results:
//   - returns immediately, also while a plugin callback is running
//   - each plugin receives events in Emit order, one at a time
//   - a plugin without a callback for hook, or rejected by the event filter,
//     receives no worker request and HookDispatches does not grow
//   - a failing or timing-out plugin is logged (sink level error) and never
//     delays another plugin or its own next event
//   - after eventFailureLimit consecutive failures the plugin's event queue is
//     suspended: one warn "events suspended after 5 consecutive failures;
//     reload the plugin to resume", later events dropped silently; a reload
//     (new worker state) resumes
//   - with the plugin queue full, or eventQueueBytes exceeded, the event is
//     dropped for that plugin and a runtime log line
//     "[Plugin runtime] event <hook> dropped for <plugin>: queue full" is written
//   - after Close every Emit is dropped silently; Emit never panics
//   - payload rules of CallHook apply (JSON encodable, at most maxPayloadBytes)
func (m *Runtime) Emit(hook Hook, data map[string]interface{}) {}

// Flush blocks until every event queued before the call has been dispatched
// (or dropped), including the one a dispatcher is handing to a worker right
// now, and returns nil; it returns ctx.Err() when ctx ends first. A runtime
// that never emitted returns at once.
func (m *Runtime) Flush(ctx context.Context) error { return nil }

// HookDispatches returns how many hook requests this runtime has sent to
// workers through Emit or CallHook since it was created. A plugin without a
// callback for the hook does not add to the count.
func (m *Runtime) HookDispatches() uint64 { return 0 }

// HasHook reports whether the loaded plugin registered a callback for hook,
// according to the snapshot the worker reported with its latest response.
// Unloaded plugins and dead workers report false. A hook registered inside a
// callback shows up once the worker's next response arrives.
func (m *Runtime) HasHook(pluginID string, hook Hook) bool { return false }

// LogSink receives every plugin log line (JellyCMS.log.*, console.*) and every
// hook, extension or boot failure the runtime records for a plugin. level is
// one of info, warn, error, debug.
type LogSink func(pluginID, level, message string)

// SetLogSink installs sink for the whole process, replacing a previous one; nil
// removes it. The sink runs on the log goroutine after the stdout write, so it
// must return quickly; a panic inside it is recovered and the line is dropped.
func SetLogSink(sink LogSink) {}
