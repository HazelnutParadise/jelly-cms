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
	"gorm.io/gorm"
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
	return m.RenderWithDB(templateName, data, nil)
}

// RenderWithDB renders a template with theme settings injected from database
func (m *Manager) RenderWithDB(templateName string, data interface{}, db interface {
	First(dest interface{}, conds ...interface{}) *gorm.DB
}) ([]byte, error) {
	m.mu.RLock()
	active := m.activeTheme
	m.mu.RUnlock()

	if active == "" {
		return nil, fmt.Errorf("no active theme")
	}

	// Inject theme settings into data
	if db != nil {
		settings, err := m.GetSettings(active, db)
		if err == nil && settings != nil {
			// Parse settings and inject into data map
			if dataMap, ok := data.(map[string]interface{}); ok {
				var colors map[string]string
				var layout map[string]interface{}
				var custom map[string]interface{}

				if len(settings.Colors) > 0 {
					json.Unmarshal(settings.Colors, &colors)
				}
				if len(settings.Layout) > 0 {
					json.Unmarshal(settings.Layout, &layout)
				}
				if len(settings.Custom) > 0 {
					json.Unmarshal(settings.Custom, &custom)
				}

				dataMap["ThemeColors"] = colors
				dataMap["ThemeLayout"] = layout
				dataMap["ThemeCustom"] = custom
			}
		}
	}

	// Check cache
	m.mu.RLock()
	tmpl, ok := m.cache[templateName]
	m.mu.RUnlock()

	if !ok {
		// Parse template with custom functions
		themeDir := filepath.Join(m.themesDir, active)
		layoutPath := filepath.Join(themeDir, "layout.html")
		tmplPath := filepath.Join(themeDir, templateName)

		funcMap := template.FuncMap{
			"substr": func(s string, start, end int) string {
				if start < 0 {
					start = 0
				}
				if end > len(s) {
					end = len(s)
				}
				if start >= end {
					return ""
				}
				return s[start:end]
			},
			"add": func(a, b int) int {
				return a + b
			},
			"sub": func(a, b int) int {
				return a - b
			},
			"themeColor": func(colors map[string]string, key string, defaultValue string) string {
				if colors == nil {
					return defaultValue
				}
				if val, ok := colors[key]; ok && val != "" {
					return val
				}
				return defaultValue
			},
			"themeLayout": func(layout map[string]interface{}, key string, defaultValue interface{}) interface{} {
				if layout == nil {
					return defaultValue
				}
				if val, ok := layout[key]; ok {
					return val
				}
				return defaultValue
			},
		}

		var err error
		// Check if template file exists, if not, try to use layout only
		if _, err := os.Stat(tmplPath); os.IsNotExist(err) {
			// Template file doesn't exist, return error so handler can fallback
			return nil, fmt.Errorf("template %s not found", templateName)
		}

		tmpl, err = template.New("layout").Funcs(funcMap).ParseFiles(layoutPath, tmplPath)
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

// GetSettings retrieves theme settings from database
func (m *Manager) GetSettings(themeName string, db interface {
	First(dest interface{}, conds ...interface{}) *gorm.DB
}) (*core.ThemeSettings, error) {
	var settings core.ThemeSettings
	result := db.First(&settings, "theme_name = ?", themeName)
	if result.Error != nil {
		// Return default settings if not found
		return &core.ThemeSettings{ThemeName: themeName}, nil
	}
	return &settings, nil
}

// SaveSettings saves theme settings to database
func (m *Manager) SaveSettings(settings *core.ThemeSettings, db interface {
	First(dest interface{}, conds ...interface{}) *gorm.DB
	Save(value interface{}) *gorm.DB
	Create(value interface{}) *gorm.DB
}) error {
	var existing core.ThemeSettings
	result := db.First(&existing, "theme_name = ?", settings.ThemeName)
	if result.Error != nil {
		result = db.Create(settings)
		return result.Error
	}
	existing.Colors = settings.Colors
	existing.Layout = settings.Layout
	existing.Custom = settings.Custom
	result = db.Save(&existing)
	return result.Error
}
