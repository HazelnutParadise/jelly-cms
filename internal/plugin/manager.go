package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/fastschema/qjs"
)

type Runtime struct {
	runtime *qjs.Runtime
	ctx     *qjs.Context
	plugins map[string]*PluginMetadata
	hooks   map[Hook][]*qjs.Value
	mu      sync.RWMutex
}

func NewRuntime() (*Runtime, error) {
	r, err := qjs.New()
	if err != nil {
		return nil, err
	}

	ctx := r.Context()
	m := &Runtime{
		runtime: r,
		ctx:     ctx,
		plugins: make(map[string]*PluginMetadata),
		hooks:   make(map[Hook][]*qjs.Value),
	}

	m.registerAPI()
	return m, nil
}

func (m *Runtime) registerAPI() {
	// Register global JellyCMS object and APIs
	// This is a simplified version - full implementation would require
	// proper Go function binding to JavaScript

	// For now, we'll inject a simple API through eval
	apiScript := `
		globalThis.JellyCMS = {
			registerHook: function(hookName, callback) {
				// Hook registration will be handled by Go code
				if (typeof callback === 'function') {
					__jelly_register_hook(hookName, callback);
				}
			},
			log: {
				info: function(msg) { console.log('[Plugin]', msg); },
				error: function(msg) { console.error('[Plugin ERROR]', msg); }
			},
			config: {
				get: function(key) { return __jelly_config_get(key); },
				set: function(key, value) { return __jelly_config_set(key, value); }
			}
		};
	`

	_, err := m.runtime.Eval(apiScript)
	if err != nil {
		fmt.Printf("Failed to register API: %v\n", err)
	}
}

func (m *Runtime) LoadAll(ctx context.Context, pluginDir string) error {
	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			if err := m.Load(ctx, filepath.Join(pluginDir, entry.Name())); err != nil {
				fmt.Printf("Failed to load plugin %s: %v\n", entry.Name(), err)
			}
		}
	}
	return nil
}

func (m *Runtime) Load(ctx context.Context, dir string) error {
	// Read plugin.json
	configPath := filepath.Join(dir, "plugin.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	var meta PluginMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return err
	}

	// Read entrypoint
	entryPath := filepath.Join(dir, meta.Entrypoint)
	script, err := os.ReadFile(entryPath)
	if err != nil {
		return err
	}

	// Evaluate
	m.mu.Lock()
	defer m.mu.Unlock()

	// Execute plugin script
	_, err = m.runtime.Eval(string(script))
	if err != nil {
		return fmt.Errorf("failed to eval plugin %s: %v", meta.Name, err)
	}

	// Call onBoot hook if plugin registered it
	m.callHook(HookOnBoot, map[string]interface{}{
		"plugin": meta.ID,
	})

	m.plugins[meta.ID] = &meta
	fmt.Printf("Loaded plugin: %s\n", meta.Name)
	return nil
}

// callHook calls all registered callbacks for a hook
func (m *Runtime) callHook(hook Hook, data map[string]interface{}) {
	m.mu.RLock()
	callbacks := m.hooks[hook]
	m.mu.RUnlock()

	if len(callbacks) == 0 {
		return
	}

	// Convert data to JavaScript value
	_, err := qjs.ToJsValue(m.ctx, data)
	if err != nil {
		fmt.Printf("Error converting data to JS value for hook %s: %v\n", hook, err)
		return
	}

	for _, callback := range callbacks {
		if callback != nil {
			// Call the JavaScript function with the data
			// Note: This is a simplified implementation
			// In production, you'd need to properly handle the callback invocation
			fmt.Printf("Calling hook %s callback (implementation pending)\n", hook)
			// TODO: Implement proper callback invocation using qjs API
		}
	}
}

// CallHook is the public method to trigger hooks
func (m *Runtime) CallHook(hook Hook, data map[string]interface{}) {
	m.callHook(hook, data)
}

func (m *Runtime) Unload(ctx context.Context, pluginID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Remove plugin from registry
	delete(m.plugins, pluginID)

	// Note: We can't easily remove hooks without tracking which plugin registered them
	// In a production system, you'd want to track hook ownership

	return nil
}

// Reload reloads a plugin (unloads and loads again)
func (m *Runtime) Reload(ctx context.Context, pluginDir string) error {
	// Get plugin ID from directory
	configPath := filepath.Join(pluginDir, "plugin.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	var meta PluginMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return err
	}

	// Unload first
	if err := m.Unload(ctx, meta.ID); err != nil {
		// Ignore error if plugin wasn't loaded
	}

	// Load again
	return m.Load(ctx, pluginDir)
}

func (m *Runtime) GetPlugin(pluginID string) (*PluginMetadata, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if p, ok := m.plugins[pluginID]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("plugin not found")
}
