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
	// ctx     *qjs.Context // Maybe Runtime is the context?
	plugins map[string]*PluginMetadata
	hooks   map[Hook][]qjs.Value
	mu      sync.RWMutex
}

func NewRuntime() *Runtime {
	// Guessing API: New() returns *Runtime
	r, _ := qjs.New()

	m := &Runtime{
		runtime: r,
		plugins: make(map[string]*PluginMetadata),
		hooks:   make(map[Hook][]qjs.Value),
	}

	m.registerAPI()
	return m
}

func (m *Runtime) registerAPI() {
	// Commented out until we know the API
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

	// Guessing Eval API
	_, err = m.runtime.Eval(string(script))
	if err != nil {
		return fmt.Errorf("failed to eval plugin %s: %v", meta.Name, err)
	}

	m.plugins[meta.ID] = &meta
	fmt.Printf("Loaded plugin: %s\n", meta.Name)
	return nil
}

func (m *Runtime) Unload(ctx context.Context, pluginID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.plugins, pluginID)
	return nil
}

func (m *Runtime) GetPlugin(pluginID string) (*PluginMetadata, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if p, ok := m.plugins[pluginID]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("plugin not found")
}
