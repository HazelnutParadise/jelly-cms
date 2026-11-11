package plugin

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Plugin worker states reported by Runtime.Status (ticket 36 R1).
const (
	StateRunning    = "running"
	StateRestarting = "restarting"
	StateCrashed    = "crashed"
)

// restartTimeout bounds one automatic restart. The worker start, entry script,
// OnBoot and host calls all run inside it; it never follows a request context.
const restartTimeout = 15 * time.Second

// PluginStatus is the supervision state of one loaded plugin.
type PluginStatus struct {
	State         string
	Faults        int // failures within crashWindow
	LastError     string
	LastFailureAt time.Time
	NextRestartAt time.Time
}

// Restart policy. Tests shorten these.
var (
	restartBackoff = []time.Duration{time.Second, 5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute}
	crashThreshold = 5
	crashWindow    = 30 * time.Minute
)

// restartPolicyMu guards reads and writes of the policy variables above. The
// package tests replace the variables directly before a runtime exists, so the
// lock is only needed for SetRestartPolicy and the runtime's own reads.
var restartPolicyMu sync.RWMutex

// errRestartTargetChanged reports that the plugin a restart was bound to is no
// longer the registered instance. It cancels the restart without counting a
// failure (unload, reload, load or runtime close).
var errRestartTargetChanged = errors.New("plugin changed while a restart was scheduled")

// SetRestartPolicy replaces the restart policy and returns a function that
// restores the previous one. Other packages' tests use it to shorten waits.
func SetRestartPolicy(backoff []time.Duration, threshold int, window time.Duration) (restore func()) {
	restartPolicyMu.Lock()
	previousBackoff, previousThreshold, previousWindow := restartBackoff, crashThreshold, crashWindow
	restartBackoff = append([]time.Duration(nil), backoff...)
	crashThreshold = threshold
	crashWindow = window
	restartPolicyMu.Unlock()

	var once sync.Once

	return func() {
		once.Do(func() {
			restartPolicyMu.Lock()
			restartBackoff = previousBackoff
			crashThreshold = previousThreshold
			crashWindow = previousWindow
			restartPolicyMu.Unlock()
		})
	}
}

// currentRestartPolicy copies the policy so a caller never reads the package
// variables outside the lock.
func currentRestartPolicy() (backoff []time.Duration, threshold int, window time.Duration) {
	restartPolicyMu.RLock()
	defer restartPolicyMu.RUnlock()

	return append([]time.Duration(nil), restartBackoff...), crashThreshold, crashWindow
}

// Status reports the supervision state of a loaded plugin. ok is false when
// the plugin is not in the registry.
func (m *Runtime) Status(id string) (PluginStatus, bool) {
	m.mu.RLock()
	state := m.plugins[id]
	m.mu.RUnlock()

	if state == nil {
		return PluginStatus{}, false
	}

	return state.status(), true
}

// SetFailureHandler registers fn, called outside the runtime locks after every
// worker failure (State StateRestarting) and once more when a plugin enters
// StateCrashed. A successful automatic restart is not reported here; it goes to
// SetRecoveryHandler.
func (m *Runtime) SetFailureHandler(fn func(id string, status PluginStatus)) {
	m.mu.Lock()
	m.failureHandler = fn
	m.mu.Unlock()
}

// SetRecoveryHandler registers fn, called outside the runtime locks after an
// automatic restart replaced a failed worker and the plugin runs again. It is
// not called for admin installs or for failures.
func (m *Runtime) SetRecoveryHandler(fn func(id string)) {
	m.mu.Lock()
	m.recoveryHandler = fn
	m.mu.Unlock()
}

// status returns the current supervision state of one plugin.
func (s *pluginState) status() PluginStatus {
	_, _, window := currentRestartPolicy()
	now := time.Now()

	s.supMu.Lock()
	defer s.supMu.Unlock()

	s.pruneFaultsLocked(now, window)

	return s.statusLocked()
}

// statusLocked builds the status. The caller holds supMu.
func (s *pluginState) statusLocked() PluginStatus {
	state := s.supState
	if state == "" {
		state = StateRunning
	}

	return PluginStatus{
		State:         state,
		Faults:        len(s.faultTimes),
		LastError:     s.lastError,
		LastFailureAt: s.lastFailureAt,
		NextRestartAt: s.nextRestartAt,
	}
}

// pruneFaultsLocked drops failures outside the crash window. The caller holds
// supMu.
func (s *pluginState) pruneFaultsLocked(now time.Time, window time.Duration) {
	if window <= 0 {
		s.faultTimes = nil

		return
	}

	cutoff := now.Add(-window)
	first := 0
	for first < len(s.faultTimes) && !s.faultTimes[first].After(cutoff) {
		first++
	}
	if first > 0 {
		s.faultTimes = append([]time.Time(nil), s.faultTimes[first:]...)
	}
}

// reportFailure records one worker failure, schedules the next restart (or
// marks the plugin crashed) and calls the failure handler outside the locks.
func (s *pluginState) reportFailure(cause error) {
	if cause == nil {
		cause = errors.New("plugin worker failed")
	}

	m := s.runtime
	now := time.Now()
	backoff, threshold, window := currentRestartPolicy()

	m.mu.Lock()
	if m.closed || m.plugins[s.id] != s || s.stopped.Load() {
		m.mu.Unlock()

		return
	}
	handler := m.failureHandler

	s.supMu.Lock()
	if s.supState == StateCrashed {
		s.supMu.Unlock()
		m.mu.Unlock()

		return
	}

	s.pruneFaultsLocked(now, window)
	s.faultTimes = append(s.faultTimes, now)
	faults := len(s.faultTimes)
	s.lastError = cause.Error()
	s.lastFailureAt = now

	// While an admin install holds the instance the restart is not scheduled
	// here; releaseAdminHold schedules it when the install ends.
	held := s.adminOps > 0
	crashed := threshold <= 0 || faults >= threshold || len(backoff) == 0
	var (
		delay time.Duration
		next  time.Time
	)
	if crashed {
		s.supState = StateCrashed
		s.nextRestartAt = time.Time{}
		s.cancelRestartLocked()
	} else {
		s.supState = StateRestarting
		delay = restartDelay(backoff, faults)
		if held {
			s.nextRestartAt = time.Time{}
			s.cancelRestartLocked()
		} else {
			s.armRestartLocked(delay)
			next = s.nextRestartAt
		}
	}

	status := s.statusLocked()
	perFailure := status
	perFailure.State = StateRestarting
	s.supMu.Unlock()
	m.mu.Unlock()

	// Waiting-period events are dropped, never logged one by one.
	s.stopEvents()

	switch {
	case crashed:
		enqueuePluginLog(s.id, "error", fmt.Sprintf(
			"%v; automatic restart stopped after %d failure(s) within %s",
			cause, faults, window,
		))
	case held:
		enqueuePluginLog(s.id, "error", fmt.Sprintf(
			"%v; automatic restart waits until the admin operation on this plugin ends",
			cause,
		))
	default:
		enqueuePluginLog(s.id, "error", fmt.Sprintf(
			"%v; restarting in %s (next attempt at %s)",
			cause, delay, next.Format(time.RFC3339),
		))
	}

	if handler == nil {
		return
	}

	handler(s.id, perFailure)
	if crashed {
		handler(s.id, status)
	}
}

// restartWorker runs one scheduled automatic restart. It is bound to the state
// instance that failed and cancels silently when that instance was replaced or
// the runtime closed.
func (m *Runtime) restartWorker(state *pluginState, generation uint64) {
	ctx, cancel, ok := m.beginRestart(state, generation)
	if !ok {
		return
	}
	defer m.endRestart()
	defer cancel()

	err := m.install(ctx, state.dir, true, state.id, state)
	if err == nil {
		m.mu.RLock()
		handler := m.recoveryHandler
		m.mu.RUnlock()

		if handler != nil {
			handler(state.id)
		}

		return
	}
	if errors.Is(err, errRestartTargetChanged) || errors.Is(err, errRuntimeClosed) {
		return
	}

	state.reportFailure(fmt.Errorf("automatic restart failed: %w", err))
}

// beginRestart marks one restart as in progress after checking, under the
// registry lock, that the failed state is still the registered instance, still
// waiting at this generation and not held by an admin install. The restart
// context is created here so that Unload and Close can cancel it.
func (m *Runtime) beginRestart(state *pluginState, generation uint64) (context.Context, context.CancelFunc, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed || m.plugins[state.id] != state {
		return nil, nil, false
	}

	state.supMu.Lock()
	defer state.supMu.Unlock()

	current := state.restartGen == generation && state.supState == StateRestarting && state.adminOps == 0
	if !current {
		return nil, nil, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), restartTimeout)
	state.restartCancel = cancel
	m.restartActive++

	return ctx, cancel, true
}

// restartDelay returns the wait before the restart that follows the given
// number of failures inside the crash window.
func restartDelay(backoff []time.Duration, faults int) time.Duration {
	if len(backoff) == 0 {
		return 0
	}

	index := faults - 1
	if index < 0 {
		index = 0
	}
	if index >= len(backoff) {
		index = len(backoff) - 1
	}

	if backoff[index] < 0 {
		return 0
	}

	return backoff[index]
}

// armRestartLocked schedules the automatic restart of this instance after
// delay. The caller holds m.mu and supMu.
func (s *pluginState) armRestartLocked(delay time.Duration) {
	m := s.runtime

	s.nextRestartAt = time.Now().Add(delay)
	s.cancelRestartLocked()
	s.restartGen++
	gen := s.restartGen
	done := make(chan struct{})
	s.restartDone = done
	s.restartTimer = time.AfterFunc(delay, func() {
		defer close(done)

		m.restartWorker(s, gen)
	})
}

// stopRestart drops the scheduled restart of this instance. With cancelInFlight
// it also cancels an automatic restart that is installing a candidate right
// now. The returned channel closes when a restart whose timer already fired has
// ended; it is nil when there is nothing to wait for. The caller holds m.mu.
func (s *pluginState) stopRestart(cancelInFlight bool) <-chan struct{} {
	s.supMu.Lock()
	defer s.supMu.Unlock()

	if cancelInFlight && s.restartCancel != nil {
		s.restartCancel()
	}

	return s.cancelRestartLocked()
}

// holdForAdmin gives the registered instance of id to an admin install. It
// cancels that instance's scheduled restart, waits for an automatic restart
// that is already installing, and refuses new restarts until releaseAdminHold.
// It returns nil when id is not registered.
func (m *Runtime) holdForAdmin(ctx context.Context, id string) (*pluginState, error) {
	var held *pluginState
	for {
		m.mu.Lock()
		state := m.plugins[id]
		var done <-chan struct{}
		if state != nil && state != held {
			state.supMu.Lock()
			state.adminOps++
			done = state.cancelRestartLocked()
			state.supMu.Unlock()
		}
		m.mu.Unlock()

		// The previous instance was replaced or unloaded while we waited.
		if held != nil && held != state {
			m.releaseAdminHold(held)
		}
		if state == nil {
			return nil, nil
		}

		held = state
		if done == nil {
			return held, nil
		}

		select {
		case <-done:
		case <-ctx.Done():
			m.releaseAdminHold(held)

			return nil, ctx.Err()
		}
	}
}

// releaseAdminHold ends one admin install's hold on an instance. If the
// instance is still registered and restarting, the automatic restart that was
// deferred during the hold is scheduled now, so the plugin never waits without
// a timer.
func (m *Runtime) releaseAdminHold(state *pluginState) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state.supMu.Lock()
	defer state.supMu.Unlock()

	state.adminOps--
	if state.adminOps > 0 || m.closed || m.plugins[state.id] != state {
		return
	}
	if state.supState != StateRestarting || state.restartTimer != nil {
		return
	}

	backoff, _, _ := currentRestartPolicy()
	state.armRestartLocked(restartDelay(backoff, len(state.faultTimes)))
}

// endRestart reports that one in-progress restart finished.
func (m *Runtime) endRestart() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.restartActive--
	if m.restartActive == 0 && m.restartIdle != nil {
		close(m.restartIdle)
		m.restartIdle = nil
	}
}

// cancelRestart drops a scheduled restart. It is idempotent and also
// invalidates a timer callback that already fired. The returned channel is nil
// when the callback will not run, otherwise it closes when the callback is
// done, which lets Close wait for timers that already fired.
func (s *pluginState) cancelRestart() <-chan struct{} {
	s.supMu.Lock()
	defer s.supMu.Unlock()

	return s.cancelRestartLocked()
}

// cancelRestartLocked is cancelRestart with supMu already held.
func (s *pluginState) cancelRestartLocked() <-chan struct{} {
	timer := s.restartTimer
	done := s.restartDone
	s.restartTimer = nil
	s.restartDone = nil
	s.restartGen++
	if timer == nil {
		return nil
	}
	if timer.Stop() {
		return nil
	}

	return done
}

// carryFaultsFrom copies the fault history of the failed instance into a state
// that replaces it after an automatic restart. Faults only reset when an admin
// load, reload or unload replaces the instance.
func (s *pluginState) carryFaultsFrom(previous *pluginState) {
	if previous == nil {
		return
	}

	previous.supMu.Lock()
	faults := append([]time.Time(nil), previous.faultTimes...)
	lastError := previous.lastError
	lastFailureAt := previous.lastFailureAt
	previous.supMu.Unlock()

	s.supMu.Lock()
	s.faultTimes = faults
	s.lastError = lastError
	s.lastFailureAt = lastFailureAt
	s.supMu.Unlock()
}

// watchWorker notices a worker that stopped reading, including an idle worker
// killed from outside. A normal shutdown, abort or replacement already marked
// the state closed, so it is not reported as a failure.
func (s *pluginState) watchWorker() {
	defer s.runtime.monitorWG.Done()

	<-s.worker.readDone

	s.execMu.Lock()
	closed := s.closed
	var reason error
	if !closed {
		s.invalid = true
		reason = s.failureReason
	}
	s.execMu.Unlock()

	if closed {
		return
	}
	if reason == nil {
		reason = errors.New("plugin worker exited unexpectedly")
	}

	s.worker.terminate()
	s.reportWorkerFailure(reason)
}

// reportWorkerFailure reports a worker death unless a caller already claimed
// it inside execMu.
func (s *pluginState) reportWorkerFailure(reason error) {
	if !s.workerFaulted.CompareAndSwap(false, true) {
		return
	}

	s.reportFailure(reason)
}
