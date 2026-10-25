package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// pluginIDPattern limits plugin ids to characters that are safe to use as a
// registry key and to print into the log without escaping the line.
var pluginIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Runtime owns the plugin registry and dispatches hooks to the loaded plugins.
//
// Every plugin runs inside its own worker process, so global state, hooks and
// failures stay isolated between plugins. The registry lock only guards the
// registry itself; JavaScript never runs while the lock is held.
type Runtime struct {
	mu          sync.RWMutex
	plugins     map[string]*pluginState
	order       []string
	hostHandler HostHandler
	closed      bool
}

// pluginState is the parent-side handle of one loaded plugin. execMu serialises
// every request to the worker, and the registry lock never waits for it.
type pluginState struct {
	id      string
	meta    PluginMetadata
	runtime *Runtime

	worker *workerProcess

	execMu       sync.Mutex
	extensionsMu sync.RWMutex
	extensions   map[string]struct{}
	closed       bool
	invalid      bool
}

// errRuntimeClosed reports a load or reload attempted on a closed runtime.
var errRuntimeClosed = errors.New("plugin runtime is closed")

// ErrPluginNotLoaded reports a request for a plugin that is not running.
var ErrPluginNotLoaded = errors.New("plugin is not loaded")

// NewRuntime creates an empty plugin runtime. RunWorkerIfRequested must have
// been called first, otherwise a child process could recurse into the normal
// program start-up instead of serving plugin requests.
func NewRuntime() (*Runtime, error) {
	if !dispatcherRegistered.Load() {
		return nil, errors.New("plugin runtime requires RunWorkerIfRequested before NewRuntime")
	}

	return &Runtime{plugins: make(map[string]*pluginState)}, nil
}

// Load loads the plugin stored in dir and makes it available for hook dispatch.
// When a plugin with the same id is already loaded it is replaced, but only
// after the new version loaded and booted successfully.
func (m *Runtime) Load(ctx context.Context, dir string) error {
	return m.install(ctx, dir, false, "")
}

// Reload hot reloads the plugin stored in dir. The running version keeps
// serving hooks when the new version fails to load or to boot.
func (m *Runtime) Reload(ctx context.Context, dir string) error {
	return m.install(ctx, dir, true, "")
}

// ReloadForID hot reloads an already loaded plugin from its own directory.
// The manifest read by this call has to declare pluginID before a candidate
// worker starts, and the plugin has to stay loaded until the swap. This keeps
// a request from reloading another plugin or resurrecting an unloaded one.
func (m *Runtime) ReloadForID(ctx context.Context, pluginID, dir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if !pluginIDPattern.MatchString(pluginID) {
		return fmt.Errorf("plugin id %q is missing or invalid", pluginID)
	}

	return m.install(ctx, dir, true, pluginID)
}

// install builds a candidate worker and swaps it in once it is ready. A
// non-empty expectedID binds the candidate to that already loaded plugin.
func (m *Runtime) install(ctx context.Context, dir string, reload bool, expectedID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// Reject work on a closed runtime or, in bound mode, on a plugin that is
	// no longer loaded before any worker starts.
	m.mu.RLock()
	closed := m.closed
	loaded := expectedID == "" || m.plugins[expectedID] != nil
	m.mu.RUnlock()

	if closed {
		return errRuntimeClosed
	}

	if !loaded {
		return fmt.Errorf("plugin %s is not loaded", expectedID)
	}

	meta, entryPath, err := readPluginManifest(dir)
	if err != nil {
		return err
	}

	if expectedID != "" && meta.ID != expectedID {
		return fmt.Errorf("plugin manifest declares %q instead of %q", meta.ID, expectedID)
	}

	script, err := readFileLimited(entryPath, maxEntryBytes)
	if err != nil {
		return fmt.Errorf("read entry script of plugin %s: %w", meta.ID, err)
	}

	worker, err := startWorker(ctx, meta.ID)
	if err != nil {
		return err
	}

	state := &pluginState{
		id: meta.ID, meta: clonePluginMetadata(meta), worker: worker, runtime: m,
		extensions: make(map[string]struct{}),
	}

	loadFrame, err := m.requestWorker(ctx, worker, meta, workerRequest{
		Op:       opLoad,
		Entry:    meta.Entrypoint,
		Code:     string(script),
		Metadata: &meta,
	}, workerStepTimeout)
	if err != nil {
		worker.terminate()

		return fmt.Errorf("load plugin %s: %w", meta.ID, err)
	}
	if err := validateExtensionSnapshot(meta, loadFrame.Extensions); err != nil {
		worker.terminate()
		return fmt.Errorf("load plugin %s: %w", meta.ID, err)
	}

	bootPayload, err := json.Marshal(map[string]interface{}{"plugin": meta.ID})
	if err != nil {
		worker.terminate()

		return fmt.Errorf("build boot payload for plugin %s: %w", meta.ID, err)
	}

	bootFrame, err := m.requestWorker(ctx, worker, meta, workerRequest{
		Op:      opBoot,
		Payload: bootPayload,
	}, workerStepTimeout)
	if err != nil {
		worker.terminate()

		return fmt.Errorf("boot plugin %s: %w", meta.ID, err)
	}
	if err := state.setExtensionSnapshot(meta, bootFrame.Extensions); err != nil {
		worker.terminate()
		return fmt.Errorf("boot plugin %s: %w", meta.ID, err)
	}

	if err := ctx.Err(); err != nil {
		worker.terminate()

		return err
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		worker.terminate()

		return errRuntimeClosed
	}
	if expectedID != "" {
		if _, ok := m.plugins[expectedID]; !ok {
			m.mu.Unlock()
			worker.terminate()

			return fmt.Errorf("plugin %s is not loaded", expectedID)
		}
	}
	previous := m.plugins[meta.ID]
	if previous == nil {
		m.order = append(m.order, meta.ID)
	}
	m.plugins[meta.ID] = state
	m.mu.Unlock()

	if previous != nil {
		if payload, err := shutdownPayload(meta.ID, "replaced"); err == nil {
			_ = previous.shutdown(context.Background(), payload)
		} else {
			previous.abort()
		}
	}

	if reload {
		enqueuePluginLog(meta.ID, "info", fmt.Sprintf("reloaded version %s", meta.Version))
	} else {
		enqueuePluginLog(meta.ID, "info", fmt.Sprintf("loaded version %s", meta.Version))
	}

	return nil
}

// LoadAll loads every plugin directory inside pluginDir. Failures are
// collected and reported together, a broken plugin never hides the others.
func (m *Runtime) LoadAll(ctx context.Context, pluginDir string) error {
	return m.LoadWhere(ctx, pluginDir, nil)
}

// LoadWhere is LoadAll restricted to the plugins include accepts. A nil
// include loads everything. A plugin whose manifest cannot be read is passed to
// Load so the failure is reported with the other load failures.
func (m *Runtime) LoadWhere(ctx context.Context, pluginDir string, include func(pluginID string) bool) error {
	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		return fmt.Errorf("read plugin directory %s: %w", pluginDir, err)
	}

	rootResolved, err := filepath.EvalSymlinks(pluginDir)
	if err != nil {
		return fmt.Errorf("resolve plugin directory %s: %w", pluginDir, err)
	}

	var failures []error

	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(pluginDir, name)

		switch {
		case entry.Type()&os.ModeSymlink != 0:
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				failures = append(failures, fmt.Errorf("plugin %s: resolve symlink: %w", name, err))

				continue
			}

			info, err := os.Stat(resolved)
			if err != nil {
				failures = append(failures, fmt.Errorf("plugin %s: stat %s: %w", name, resolved, err))

				continue
			}

			if !info.IsDir() {
				continue
			}

			if !isInside(rootResolved, resolved) {
				failures = append(failures, fmt.Errorf(
					"plugin %s: directory %s escapes %s", name, resolved, pluginDir,
				))

				continue
			}

			path = resolved
		case !entry.IsDir():
			continue
		}

		if include != nil {
			if meta, _, err := readPluginManifest(path); err == nil && !include(meta.ID) {
				continue
			}
		}

		if err := m.Load(ctx, path); err != nil {
			failures = append(failures, fmt.Errorf("plugin %s: %w", name, err))
		}
	}

	return errors.Join(failures...)
}

// Unload removes the plugin, notifies its OnShutdown hook and reclaims the
// worker process it owns.
func (m *Runtime) Unload(ctx context.Context, pluginID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	m.mu.Lock()
	state, ok := m.plugins[pluginID]
	if ok {
		delete(m.plugins, pluginID)
		m.removeOrderLocked(pluginID)
	}
	m.mu.Unlock()

	if !ok {
		return fmt.Errorf("plugin %s is not loaded", pluginID)
	}

	payload, err := shutdownPayload(pluginID, "unloaded")
	if err != nil {
		state.abort()

		return err
	}

	shutdownErr := state.shutdown(ctx, payload)

	enqueuePluginLog(pluginID, "info", "unloaded")

	if shutdownErr != nil {
		enqueuePluginLog(pluginID, "error", fmt.Sprintf("shutdown failed: %v", shutdownErr))

		return shutdownErr
	}

	return nil
}

// GetPlugin returns a copy of the metadata of a loaded plugin. The caller can
// modify the returned value without touching the registry.
func (m *Runtime) GetPlugin(pluginID string) (*PluginMetadata, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state, ok := m.plugins[pluginID]
	if !ok {
		return nil, fmt.Errorf("plugin not found: %s", pluginID)
	}

	meta := clonePluginMetadata(state.meta)

	return &meta, nil
}

// IsLoaded reports whether the plugin currently runs in this runtime.
func (m *Runtime) IsLoaded(pluginID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, ok := m.plugins[pluginID]

	return ok
}

// Invoke runs one hook for a single plugin and reports its failure to the
// caller. It is for requests the admin starts, such as a plugin action. Unlike
// CallHook, a failure is returned instead of only logged.
func (m *Runtime) Invoke(ctx context.Context, pluginID string, hook Hook, data map[string]interface{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	m.mu.RLock()
	state, ok := m.plugins[pluginID]
	m.mu.RUnlock()

	if !ok {
		return ErrPluginNotLoaded
	}

	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode payload for hook %s: %w", hook, err)
	}

	if len(payload) > maxPayloadBytes {
		return fmt.Errorf("payload for hook %s exceeds %d bytes", hook, maxPayloadBytes)
	}

	return state.call(ctx, hook, payload)
}

// CallHook hands the payload to every callback registered for the hook. A
// failing plugin is reported and never stops the other plugins.
func (m *Runtime) CallHook(hook Hook, data map[string]interface{}) {
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
		if err := state.dispatch(hook, payload); err != nil {
			enqueuePluginLog(state.id, "error", err.Error())
		}
	}
}

// Close unloads every plugin and releases the workers. The registry is detached
// atomically under the lock, so a concurrent Load cannot slip a new plugin in
// after the snapshot. Shutdown hooks run without the lock. Close is idempotent.
func (m *Runtime) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()

		return
	}
	m.closed = true

	states := make([]*pluginState, 0, len(m.order))
	for _, id := range m.order {
		if state, ok := m.plugins[id]; ok {
			states = append(states, state)
		}
	}
	m.plugins = make(map[string]*pluginState)
	m.order = nil
	m.mu.Unlock()

	for _, state := range states {
		payload, err := shutdownPayload(state.id, "runtime closed")
		if err != nil {
			state.abort()

			continue
		}

		_ = state.shutdown(context.Background(), payload)
	}
}

// states returns the loaded plugins in load order.
func (m *Runtime) states() []*pluginState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	states := make([]*pluginState, 0, len(m.order))
	for _, id := range m.order {
		if state, ok := m.plugins[id]; ok {
			states = append(states, state)
		}
	}

	return states
}

// removeOrderLocked drops a plugin id from the load order. The registry lock
// has to be held by the caller.
func (m *Runtime) removeOrderLocked(pluginID string) {
	for i, id := range m.order {
		if id == pluginID {
			m.order = append(m.order[:i], m.order[i+1:]...)

			return
		}
	}
}

// dispatch hands an already encoded payload to the callbacks of one hook. A
// worker that died is marked invalid so later hooks skip it instead of waiting
// for a dead process.
func (s *pluginState) dispatch(hook Hook, payload []byte) error {
	s.execMu.Lock()
	defer s.execMu.Unlock()

	if s.closed || s.invalid {
		return nil
	}

	frame, err := s.runtime.requestWorker(context.Background(), s.worker, s.meta, workerRequest{
		Op:      opHook,
		Hook:    string(hook),
		Payload: payload,
	}, workerStepTimeout)
	if err != nil {
		if !s.worker.alive() {
			s.invalid = true
		}

		return fmt.Errorf("hook %s: %w", hook, err)
	}
	if err := s.setExtensionSnapshot(s.meta, frame.Extensions); err != nil {
		s.invalid = true
		s.worker.terminate()
		return err
	}

	return nil
}

// call runs one hook and returns its error. A stopped or dead plugin answers
// ErrPluginNotLoaded instead of waiting for a worker that is gone.
func (s *pluginState) call(ctx context.Context, hook Hook, payload []byte) error {
	s.execMu.Lock()
	defer s.execMu.Unlock()

	if s.closed || s.invalid {
		return ErrPluginNotLoaded
	}

	frame, err := s.runtime.requestWorker(ctx, s.worker, s.meta, workerRequest{
		Op:      opHook,
		Hook:    string(hook),
		Payload: payload,
	}, workerStepTimeout)
	if err != nil {
		if !s.worker.alive() {
			s.invalid = true
		}

		return fmt.Errorf("hook %s: %w", hook, err)
	}
	if err := s.setExtensionSnapshot(s.meta, frame.Extensions); err != nil {
		s.invalid = true
		s.worker.terminate()
		return err
	}

	return nil
}

// shutdown runs the OnShutdown hook of the plugin exactly once and reclaims its
// worker.
func (s *pluginState) shutdown(ctx context.Context, payload []byte) error {
	s.execMu.Lock()
	defer s.execMu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	if s.invalid || s.worker == nil {
		if s.worker != nil {
			s.worker.terminate()
		}

		return nil
	}

	_, err := s.runtime.requestWorker(ctx, s.worker, s.meta, workerRequest{Op: opShutdown, Payload: payload}, workerStepTimeout)
	s.worker.terminate()
	return err
}

// abort reclaims the worker without running JavaScript.
func (s *pluginState) abort() {
	s.execMu.Lock()
	defer s.execMu.Unlock()

	if s.closed {
		return
	}
	s.closed = true
	s.invalid = true

	if s.worker != nil {
		s.worker.terminate()
	}
}

// shutdownPayload builds the payload passed to an OnShutdown callback.
func shutdownPayload(pluginID, reason string) ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"plugin": pluginID,
		"reason": reason,
	})
}

// readPluginManifest reads and validates plugin.json and resolves the entry
// script inside the plugin directory.
func readPluginManifest(dir string) (PluginMetadata, string, error) {
	var meta PluginMetadata

	info, err := os.Stat(dir)
	if err != nil {
		return meta, "", fmt.Errorf("plugin directory %s: %w", dir, err)
	}

	if !info.IsDir() {
		return meta, "", fmt.Errorf("plugin path %s is not a directory", dir)
	}

	manifestPath, err := resolvePluginFile(dir, "plugin.json")
	if err != nil {
		return meta, "", fmt.Errorf("plugin manifest: %w", err)
	}

	data, err := readFileLimited(manifestPath, maxManifestBytes)
	if err != nil {
		return meta, "", fmt.Errorf("read manifest %s: %w", manifestPath, err)
	}

	meta, err = ParseManifest(data)
	if err != nil {
		return meta, "", fmt.Errorf("invalid manifest %s: %w", manifestPath, err)
	}

	if err := validatePluginMeta(meta); err != nil {
		return meta, "", fmt.Errorf("invalid manifest %s: %w", manifestPath, err)
	}

	entryPath, err := resolvePluginFile(dir, meta.Entrypoint)
	if err != nil {
		return meta, "", fmt.Errorf("plugin %s entry script: %w", meta.ID, err)
	}

	return meta, entryPath, nil
}

// readFileLimited reads at most limit bytes and rejects larger files instead of
// loading them into memory.
func readFileLimited(path string, limit int) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, err
	}

	if len(data) > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, limit)
	}

	return data, nil
}

// validatePluginMeta rejects manifests the runtime cannot work with.
func validatePluginMeta(meta PluginMetadata) error {
	if !pluginIDPattern.MatchString(meta.ID) {
		return fmt.Errorf("plugin id %q is missing or invalid", meta.ID)
	}

	if strings.TrimSpace(meta.Entrypoint) == "" {
		return fmt.Errorf("plugin %s does not declare an entry script", meta.ID)
	}

	return nil
}

// resolvePluginFile resolves rel inside root and guarantees the result stays
// inside root. Symlinks are resolved first, so a link cannot be used to reach
// a file outside of the plugin directory.
func resolvePluginFile(root, rel string) (string, error) {
	if rel == "" {
		return "", errors.New("empty file name")
	}

	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute file name %q is not allowed", rel)
	}

	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("file name %q escapes the plugin directory", rel)
	}

	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve plugin directory %s: %w", root, err)
	}

	resolved, err := filepath.EvalSymlinks(filepath.Join(rootResolved, clean))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("file %q does not exist", rel)
		}

		return "", fmt.Errorf("resolve file %q: %w", rel, err)
	}

	if !isInside(rootResolved, resolved) {
		return "", fmt.Errorf("file %q escapes the plugin directory", rel)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat file %q: %w", rel, err)
	}

	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("file %q is not a regular file", rel)
	}

	return resolved, nil
}

// isInside reports whether path is root itself or lives below root.
func isInside(root, path string) bool {
	if path == root {
		return true
	}

	return strings.HasPrefix(path, root+string(filepath.Separator))
}
