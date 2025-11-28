package theme

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sync"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

type Manager struct {
	themesDir   string
	activeTheme string
	mu          sync.RWMutex
	cache       map[string]*template.Template
}

func NewManager(themesDir string) *Manager {
	return &Manager{
		themesDir: themesDir,
		cache:     make(map[string]*template.Template),
	}
}

func (m *Manager) Load(name string) (*core.ThemeConfig, error) {
	configPath := filepath.Join(m.themesDir, name, "theme.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var config core.ThemeConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	return &config, nil
}

func (m *Manager) Activate(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.Load(name); err != nil {
		return err
	}
	m.activeTheme = name
	m.cache = make(map[string]*template.Template) // Clear cache
	return nil
}

func (m *Manager) GetActive() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeTheme
}

func (m *Manager) Render(templateName string, data interface{}) ([]byte, error) {
	m.mu.RLock()
	active := m.activeTheme
	m.mu.RUnlock()

	if active == "" {
		return nil, fmt.Errorf("no active theme")
	}

	// Check cache
	m.mu.RLock()
	tmpl, ok := m.cache[templateName]
	m.mu.RUnlock()

	if !ok {
		// Parse template
		themeDir := filepath.Join(m.themesDir, active)
		layoutPath := filepath.Join(themeDir, "layout.html")
		tmplPath := filepath.Join(themeDir, templateName)

		var err error
		tmpl, err = template.ParseFiles(layoutPath, tmplPath)
		if err != nil {
			return nil, err
		}

		m.mu.Lock()
		m.cache[templateName] = tmpl
		m.mu.Unlock()
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
