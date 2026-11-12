package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// Event queue, hook snapshots and the log sink (ticket 28).
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
// eventQueueSize is read when a plugin's queue is created.
var (
	eventQueueSize  = 256
	eventQueueBytes = 32 << 20
)

// eventFailureLimit is how many consecutive hook failures (errors or
// timeouts) suspend event dispatch for a plugin until it is reloaded.
const eventFailureLimit = 5

// maxPluginHooks and maxHookNameLength bound the hook snapshot a worker may
// report. Exceeding either fails the load or the boot, like extension points.
const (
	maxPluginHooks    = 64
	maxHookNameLength = 64
)

// eventCloseBudget bounds how long Close waits for queued events to drain.
const eventCloseBudget = 2 * time.Second

// flushPollInterval is how often Flush re-checks the pending counter.
const flushPollInterval = 10 * time.Millisecond

// EventFilter decides whether one loaded plugin receives one event. The site
// uses it to enforce the "events" permission. A nil filter allows every plugin.
type EventFilter func(meta PluginMetadata, hook Hook) bool

// eventItem is one encoded event waiting in a plugin's queue. size is the
// number of payload bytes reserved against eventQueueBytes.
type eventItem struct {
	hook    Hook
	payload []byte
	size    int64
}

// SetEventFilter installs filter for later Emit calls. Boot, shutdown and
// action hooks are not events and never pass through the filter.
func (m *Runtime) SetEventFilter(filter EventFilter) {
	m.mu.Lock()
	m.eventFilter = filter
	m.mu.Unlock()
}

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
func (m *Runtime) Emit(hook Hook, data map[string]interface{}) {
	m.mu.RLock()
	closed := m.closed
	filter := m.eventFilter
	m.mu.RUnlock()
	if closed {
		return
	}

	payload, err := json.Marshal(data)
	if err != nil {
		enqueueRuntimeLog(fmt.Sprintf("[Plugin runtime] cannot encode %s payload: %v\n", hook, err))

		return
	}
	if len(payload) > maxPayloadBytes {
		enqueueRuntimeLog(fmt.Sprintf(
			"[Plugin runtime] %s payload of %d bytes exceeds limit %d\n",
			hook, len(payload), maxPayloadBytes,
		))

		return
	}

	for _, state := range m.states() {
		if !state.hasHook(hook) {
			continue
		}
		if filter != nil && !filter(clonePluginMetadata(state.meta), hook) {
			continue
		}
		m.enqueueEvent(state, hook, payload)
	}
}

// enqueueEvent places one encoded event on the plugin's queue without waiting.
// The queue and its dispatcher are created on the first event.
func (m *Runtime) enqueueEvent(s *pluginState, hook Hook, payload []byte) {
	if s.suspended.Load() {
		return
	}

	size := int64(len(payload))
	// full is true when the event was refused for capacity. A stopped queue
	// belongs to a plugin that is gone, so its events are dropped silently.
	full := false

	s.eventsMu.Lock()
	if !s.eventsStopped {
		if s.events == nil {
			s.events = make(chan eventItem, eventQueueSize)
			s.eventsDone = make(chan struct{})
			go s.runEvents(m, s.events, s.eventsDone)
		}
		// The pending count goes up before the send so a dispatcher that takes
		// the item at once can never observe a negative count.
		full = true
		if m.reserveEventBytes(size) {
			m.pending.Add(1)
			select {
			case s.events <- eventItem{hook: hook, payload: payload, size: size}:
				full = false
			default:
				m.pending.Add(-1)
				m.queuedBytes.Add(-size)
			}
		}
	}
	s.eventsMu.Unlock()

	if full {
		enqueueRuntimeLog(fmt.Sprintf("[Plugin runtime] event %s dropped for %s: queue full\n", hook, s.id))
	}
}

// reserveEventBytes reserves size payload bytes against eventQueueBytes. A
// single event may exceed the bound when nothing else is queued, so an
// oversized event is still delivered rather than blocked forever.
func (m *Runtime) reserveEventBytes(size int64) bool {
	limit := int64(eventQueueBytes)
	for {
		queued := m.queuedBytes.Load()
		if queued > 0 && queued+size > limit {
			return false
		}
		if m.queuedBytes.CompareAndSwap(queued, queued+size) {
			return true
		}
	}
}

// finishEvent releases one event after it was delivered or discarded.
func (m *Runtime) finishEvent(size int64) {
	m.queuedBytes.Add(-size)
	m.pending.Add(-1)
}

// runEvents delivers the queue of one plugin in order until done closes. The
// events channel is never closed, so an Emit racing with the stop cannot send
// on a closed channel.
func (s *pluginState) runEvents(m *Runtime, events <-chan eventItem, done <-chan struct{}) {
	for {
		select {
		case <-done:
			m.discardEvents(events)

			return
		default:
		}

		select {
		case item := <-events:
			s.deliverEvent(m, item)
		case <-done:
			m.discardEvents(events)

			return
		}
	}
}

// discardEvents drops whatever is still queued after the plugin stopped.
func (m *Runtime) discardEvents(events <-chan eventItem) {
	for {
		select {
		case item := <-events:
			m.finishEvent(item.size)
		default:
			return
		}
	}
}

// deliverEvent sends one event to the worker and applies the failure rules:
// a failure is logged and counted, a success resets the count, and the
// eventFailureLimit-th consecutive failure suspends the plugin's events.
func (s *pluginState) deliverEvent(m *Runtime, item eventItem) {
	defer m.finishEvent(item.size)

	if s.suspended.Load() {
		return
	}

	err := s.dispatchEvent(item.hook, item.payload)
	if err == nil {
		s.failures = 0

		return
	}

	enqueuePluginLog(s.id, "error", err.Error())
	s.failures++
	if s.failures >= eventFailureLimit && s.suspended.CompareAndSwap(false, true) {
		enqueuePluginLog(s.id, "warn", fmt.Sprintf(
			"events suspended after %d consecutive failures; reload the plugin to resume",
			eventFailureLimit,
		))
	}
}

// dispatchEvent is the event path of sendHook. A closed plugin drops the event
// silently. A dead worker counts as a failure, so a plugin whose worker died
// is suspended like one that keeps throwing.
func (s *pluginState) dispatchEvent(hook Hook, payload []byte) error {
	s.execMu.Lock()

	if s.closed {
		s.execMu.Unlock()

		return nil
	}
	if s.invalid {
		// The supervisor already reported this worker; events queued for the
		// waiting period are dropped silently instead of logged one by one.
		s.execMu.Unlock()

		return nil
	}

	err := s.sendHook(context.Background(), hook, payload)
	report, reason := s.claimWorkerFailureLocked()
	s.execMu.Unlock()

	if report {
		s.reportFailure(reason)
	}

	return err
}

// stopEvents stops the plugin's dispatcher. Events still queued are discarded
// by the dispatcher. It is idempotent and must not wait for execMu.
func (s *pluginState) stopEvents() {
	s.eventsMu.Lock()
	defer s.eventsMu.Unlock()

	if s.eventsStopped {
		return
	}
	s.eventsStopped = true
	if s.eventsDone != nil {
		close(s.eventsDone)
	}
}

// Flush blocks until no event is queued or in flight on any plugin queue,
// including events emitted after the call, and returns nil; it returns
// ctx.Err() when ctx ends first. Under continuous traffic it therefore waits
// until ctx ends. A runtime that never emitted returns at once.
func (m *Runtime) Flush(ctx context.Context) error {
	m.mu.RLock()
	closed := m.closed
	m.mu.RUnlock()
	if closed || m.pending.Load() == 0 {
		return nil
	}

	ticker := time.NewTicker(flushPollInterval)
	defer ticker.Stop()

	for m.pending.Load() > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}

	return nil
}

// HookDispatches returns how many hook requests this runtime has sent to
// workers through Emit or CallHook since it was created. A plugin without a
// callback for the hook does not add to the count.
func (m *Runtime) HookDispatches() uint64 {
	return m.dispatches.Load()
}

// HasHook reports whether the loaded plugin registered a callback for hook,
// according to the snapshot the worker reported with its latest response.
// Unloaded plugins and dead workers report false. A hook registered inside a
// callback shows up once the worker's next response arrives.
func (m *Runtime) HasHook(pluginID string, hook Hook) bool {
	m.mu.RLock()
	state := m.plugins[pluginID]
	m.mu.RUnlock()

	return state != nil && state.worker.alive() && state.hasHook(hook)
}

// hasHook reports whether the last snapshot of the plugin lists hook.
func (s *pluginState) hasHook(hook Hook) bool {
	s.hooksMu.RLock()
	defer s.hooksMu.RUnlock()

	_, ok := s.hooks[hook]

	return ok
}

// setHookSnapshot replaces the hook snapshot with names. A nil list means the
// response carried no snapshot and the previous one stays.
func (s *pluginState) setHookSnapshot(names []string) {
	if names == nil {
		return
	}

	registered := make(map[Hook]struct{}, len(names))
	for _, name := range names {
		registered[Hook(name)] = struct{}{}
	}

	s.hooksMu.Lock()
	s.hooks = registered
	s.hooksMu.Unlock()
}

// validateHookSnapshot rejects a hook list that exceeds the snapshot limits.
func validateHookSnapshot(names []string) error {
	if len(names) > maxPluginHooks {
		return fmt.Errorf("plugin worker reported more than %d hooks", maxPluginHooks)
	}
	for _, name := range names {
		if utf8.RuneCountInString(name) > maxHookNameLength {
			return fmt.Errorf("plugin worker reported a hook name longer than %d characters", maxHookNameLength)
		}
	}

	return nil
}

// validateWorkerSnapshots checks the extension and hook snapshots of a
// response before any of them is applied.
func validateWorkerSnapshots(meta PluginMetadata, frame workerFrame) error {
	if err := validateExtensionSnapshot(meta, frame.Extensions); err != nil {
		return err
	}

	return validateHookSnapshot(frame.Hooks)
}

// LogSink receives every plugin log line (JellyCMS.log.*, console.*) and every
// hook, extension or boot failure the runtime records for a plugin. level is
// one of info, warn, error, debug.
type LogSink func(pluginID, level, message string)

// logSinkHolder lets atomic.Value store a nil sink, which it cannot do directly.
type logSinkHolder struct {
	sink LogSink
}

var logSink atomic.Value // holds logSinkHolder

// SetLogSink installs sink for the whole process, replacing a previous one; nil
// removes it. The sink runs on the log goroutine after the stdout write, so it
// must return quickly; a panic inside it is recovered and the line is dropped.
func SetLogSink(sink LogSink) {
	logSink.Store(logSinkHolder{sink: sink})
}

// currentLogSink returns the installed sink, or nil.
func currentLogSink() LogSink {
	holder, _ := logSink.Load().(logSinkHolder)

	return holder.sink
}
