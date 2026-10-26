package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/ext"
)

// ErrExtensionUnavailable reports that a plugin has no live callback for a
// shipped extension point.
var ErrExtensionUnavailable = errors.New("plugin extension is unavailable")

// CallExtension invokes one callback registered for a shipped extension point.
// The one-second deadline starts before waiting for the plugin's worker lock.
func (m *Runtime) CallExtension(ctx context.Context, pluginID, point string, input json.RawMessage) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, workerStepTimeout)
	defer cancel()
	deadline, _ := ctx.Deadline()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !extensionPointShipped(point) {
		return nil, fmt.Errorf("extension point %q is not shipped", point)
	}
	if len(input) == 0 || len(input) > maxExtensionBytes || !json.Valid(input) || jsonDepth(input) > 64 {
		return nil, errors.New("extension input is invalid or too large")
	}
	input = append(json.RawMessage(nil), input...)

	m.mu.RLock()
	state := m.plugins[pluginID]
	m.mu.RUnlock()
	if state == nil {
		return nil, ErrPluginNotLoaded
	}
	if !extensionDeclared(state.meta, point) {
		return nil, fmt.Errorf("plugin %s does not declare extension point %q", pluginID, point)
	}
	if !state.hasExtension(point) || !state.worker.alive() {
		return nil, ErrExtensionUnavailable
	}
	if err := lockExtensionUntil(ctx, &state.execMu); err != nil {
		return nil, err
	}
	defer state.execMu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if state.closed || state.invalid {
		return nil, ErrPluginNotLoaded
	}
	if _, ok := state.extensionSnapshot()[point]; !ok || !state.worker.alive() {
		return nil, ErrExtensionUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return nil, context.DeadlineExceeded
	}
	frame, err := m.requestWorker(ctx, state.worker, state.meta, workerRequest{
		Op:        opExtension,
		Extension: point,
		Payload:   input,
	}, remaining)
	if err != nil {
		if !state.worker.alive() {
			state.invalid = true
		}
		return nil, fmt.Errorf("call extension %s for plugin %s: %w", point, pluginID, err)
	}
	if err := state.setExtensionSnapshot(state.meta, frame.Extensions); err != nil {
		state.invalid = true
		state.worker.terminate()
		return nil, err
	}
	if len(frame.Result) == 0 || len(frame.Result) > maxExtensionBytes || !json.Valid(frame.Result) || jsonDepth(frame.Result) > 64 {
		state.invalid = true
		state.worker.terminate()
		return nil, errors.New("plugin worker returned an invalid or oversized extension result")
	}
	return append(json.RawMessage(nil), frame.Result...), nil
}

// HasExtension reports live callback availability, not only manifest
// declaration. It is safe to use as an adapter gate before CallExtension.
func (m *Runtime) HasExtension(pluginID, point string) bool {
	if !extensionPointShipped(point) {
		return false
	}
	m.mu.RLock()
	state := m.plugins[pluginID]
	m.mu.RUnlock()
	if state == nil || !extensionDeclared(state.meta, point) || !state.worker.alive() {
		return false
	}
	return state.hasExtension(point)
}

func extensionPointShipped(point string) bool {
	return ext.Shipped(point) && (point == ext.PointSearchEngine || point == ext.PointMediaStorage)
}

func extensionDeclared(meta PluginMetadata, point string) bool {
	for _, extension := range meta.Extensions {
		if extension.Point == point {
			return true
		}
	}
	return false
}

func validateExtensionSnapshot(meta PluginMetadata, points []string) error {
	if len(points) > 2 {
		return errors.New("plugin worker reported more than two extension callbacks")
	}
	seen := make(map[string]struct{}, len(points))
	for _, point := range points {
		if !extensionPointShipped(point) || !extensionDeclared(meta, point) {
			return fmt.Errorf("plugin worker reported undeclared or unsupported extension point %q", point)
		}
		if _, ok := seen[point]; ok {
			return fmt.Errorf("plugin worker reported extension point %q twice", point)
		}
		seen[point] = struct{}{}
	}
	return nil
}

func (s *pluginState) setExtensionSnapshot(meta PluginMetadata, points []string) error {
	if err := validateExtensionSnapshot(meta, points); err != nil {
		return err
	}
	registered := make(map[string]struct{}, len(points))
	for _, point := range points {
		registered[point] = struct{}{}
	}
	s.extensionsMu.Lock()
	s.extensions = registered
	s.extensionsMu.Unlock()
	return nil
}

func (s *pluginState) hasExtension(point string) bool {
	s.extensionsMu.RLock()
	defer s.extensionsMu.RUnlock()
	_, ok := s.extensions[point]
	return ok
}

func (s *pluginState) extensionSnapshot() map[string]struct{} {
	s.extensionsMu.RLock()
	defer s.extensionsMu.RUnlock()
	copy := make(map[string]struct{}, len(s.extensions))
	for point := range s.extensions {
		copy[point] = struct{}{}
	}
	return copy
}

func lockExtensionUntil(ctx context.Context, mutex *sync.Mutex) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if mutex.TryLock() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
