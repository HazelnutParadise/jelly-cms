package theme

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/assets"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"gorm.io/gorm"
)

type Manager struct {
	themesDir     string
	activeTheme   string
	generation    uint64
	mu            sync.RWMutex
	cache         map[string]*template.Template
	extraFuncs    template.FuncMap
	assetSnapshot *assets.Snapshot
	assetErr      error
}

func NewManager(themesDir string) *Manager {
	m := &Manager{
		themesDir: themesDir,
		cache:     make(map[string]*template.Template),
	}
	if snapshot, err := m.buildAssetSnapshot(""); err == nil {
		m.assetSnapshot = snapshot
	} else {
		m.assetErr = err
	}
	return m
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
	if !filepath.IsLocal(name) || filepath.Base(name) != name || name == "." || name == ".." {
		return fmt.Errorf("invalid theme name")
	}
	if _, err := m.Load(name); err != nil {
		return err
	}
	snapshot, err := m.buildAssetSnapshot(name)
	if err != nil {
		return fmt.Errorf("build theme asset snapshot: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeTheme = name
	m.assetSnapshot = snapshot
	m.assetErr = nil
	m.generation++
	m.cache = make(map[string]*template.Template) // Clear cache
	return nil
}

// AssetSnapshot returns the immutable resources captured for the current theme generation.
func (m *Manager) AssetSnapshot() *assets.Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.assetSnapshot
}

// AssetError reports a builtin snapshot initialization failure for startup diagnostics.
func (m *Manager) AssetError() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.assetErr
}

// AssetURL resolves a local URL against the current immutable asset snapshot.
func (m *Manager) AssetURL(raw string) string {
	snapshot := m.AssetSnapshot()
	if snapshot == nil {
		return raw
	}
	return snapshot.URL(raw)
}

func (m *Manager) buildAssetSnapshot(themeName string) (*assets.Snapshot, error) {
	webRoot := filepath.Dir(filepath.Clean(m.themesDir))
	mounts := map[string]string{}
	for prefix, root := range map[string]string{
		"/admin/assets/": filepath.Join(webRoot, "admin", "assets"),
		"/vendor/katex/": filepath.Join(webRoot, "vendor", "katex"),
		"/_jelly/":       filepath.Join(webRoot, "jelly"),
	} {
		if _, err := os.Stat(root); err == nil {
			mounts[prefix] = root
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("stat asset root %s: %w", root, err)
		}
	}
	if themeName != "" {
		themeRoot := filepath.Join(m.themesDir, themeName)
		resolvedThemes, err := filepath.EvalSymlinks(m.themesDir)
		if err != nil {
			return nil, fmt.Errorf("resolve themes root: %w", err)
		}
		resolvedTheme, err := filepath.EvalSymlinks(themeRoot)
		if err != nil {
			return nil, fmt.Errorf("resolve theme root: %w", err)
		}
		if !withinDirectory(resolvedThemes, resolvedTheme) {
			return nil, fmt.Errorf("theme escapes themes root")
		}
		assetRoot := filepath.Join(resolvedTheme, "assets")
		if _, err := os.Stat(assetRoot); err == nil {
			resolvedAssets, err := filepath.EvalSymlinks(assetRoot)
			if err != nil {
				return nil, fmt.Errorf("resolve theme assets: %w", err)
			}
			if !withinDirectory(resolvedTheme, resolvedAssets) {
				return nil, fmt.Errorf("theme assets escape theme directory")
			}
			mounts["/theme/assets/"] = resolvedAssets
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("stat theme assets: %w", err)
		}
	}
	return assets.Build(mounts)
}

func (m *Manager) GetActive() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeTheme
}

// ActiveAssetsDir returns the assets directory of the active theme. It resolves
// symlinks and rejects theme or assets directories that escape the configured
// themes root, so callers can safely serve files from the returned directory.
func (m *Manager) ActiveAssetsDir() (string, error) {
	m.mu.RLock()
	active := m.activeTheme
	themesDir := m.themesDir
	m.mu.RUnlock()
	if active == "" || !filepath.IsLocal(active) || filepath.Base(active) != active {
		return "", fmt.Errorf("no valid active theme")
	}

	root, err := filepath.Abs(themesDir)
	if err != nil {
		return "", fmt.Errorf("resolve themes root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve themes root: %w", err)
	}
	themeDir, err := filepath.EvalSymlinks(filepath.Join(root, active))
	if err != nil {
		return "", fmt.Errorf("resolve active theme: %w", err)
	}
	if !withinDirectory(root, themeDir) {
		return "", fmt.Errorf("active theme escapes themes root")
	}
	assetsDir, err := filepath.EvalSymlinks(filepath.Join(themeDir, "assets"))
	if err != nil {
		return "", fmt.Errorf("resolve active theme assets: %w", err)
	}
	if !withinDirectory(themeDir, assetsDir) {
		return "", fmt.Errorf("active theme assets escape theme directory")
	}
	info, err := os.Stat(assetsDir)
	if err != nil {
		return "", fmt.Errorf("stat active theme assets: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("active theme assets are not a directory")
	}
	return assetsDir, nil
}

func withinDirectory(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

// RegisterFunc adds a template function available to every theme template,
// for example "menu" or "comments" provided by the server package. It clears
// the template cache so the next render picks it up.
func (m *Manager) RegisterFunc(name string, fn interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.extraFuncs == nil {
		m.extraFuncs = template.FuncMap{}
	}
	m.extraFuncs[name] = fn
	m.generation++
	m.cache = map[string]*template.Template{}
}

func (m *Manager) Render(templateName string, data interface{}) ([]byte, error) {
	return m.RenderWithDB(templateName, data, nil)
}

// RenderWithDB renders a template with theme settings injected from database
func (m *Manager) RenderWithDB(templateName string, data interface{}, db interface {
	First(dest interface{}, conds ...interface{}) *gorm.DB
}) ([]byte, error) {
	return m.renderTemplate(templateName, data, db, true)
}

// RenderPage renders one standalone theme template and its partials without layout.html.
func (m *Manager) RenderPage(templateName string, data interface{}) ([]byte, error) {
	return m.renderTemplate(templateName, data, nil, false)
}

// RenderPageWithDB renders a standalone theme template with theme settings loaded from the database.
func (m *Manager) RenderPageWithDB(templateName string, data interface{}, db interface {
	First(dest interface{}, conds ...interface{}) *gorm.DB
}) ([]byte, error) {
	return m.renderTemplate(templateName, data, db, false)
}

func (m *Manager) renderTemplate(templateName string, data interface{}, db interface {
	First(dest interface{}, conds ...interface{}) *gorm.DB
}, withLayout bool) ([]byte, error) {
	m.mu.RLock()
	active, generation, snapshot := m.activeTheme, m.generation, m.assetSnapshot
	m.mu.RUnlock()
	if active == "" {
		return nil, fmt.Errorf("no active theme")
	}

	if db != nil {
		settings, err := m.GetSettings(active, db)
		if err != nil {
			return nil, fmt.Errorf("load theme settings: %w", err)
		}
		if dataMap, ok := data.(map[string]interface{}); ok {
			var colors map[string]string
			var layout map[string]interface{}
			var custom map[string]interface{}
			if len(settings.Colors) > 0 {
				if err := json.Unmarshal(settings.Colors, &colors); err != nil {
					return nil, fmt.Errorf("decode theme colors: %w", err)
				}
			}
			if len(settings.Layout) > 0 {
				if err := json.Unmarshal(settings.Layout, &layout); err != nil {
					return nil, fmt.Errorf("decode theme layout: %w", err)
				}
			}
			if len(settings.Custom) > 0 {
				if err := json.Unmarshal(settings.Custom, &custom); err != nil {
					return nil, fmt.Errorf("decode theme custom settings: %w", err)
				}
			}
			dataMap["ThemeColors"] = colors
			dataMap["ThemeLayout"] = layout
			dataMap["ThemeCustom"] = custom
		}
	}

	cacheKey := fmt.Sprintf("%s\x00%d\x00%t\x00%s", active, generation, withLayout, templateName)
	m.mu.RLock()
	tmpl := m.cache[cacheKey]
	m.mu.RUnlock()
	if tmpl == nil {
		themeDir := filepath.Join(m.themesDir, active)
		tmplPath := filepath.Join(themeDir, templateName)
		funcMap := m.templateFuncsWithSnapshot(snapshot)
		var err error
		if withLayout {
			tmpl, err = parseRenderSet(funcMap, themeDir, filepath.Join(themeDir, "layout.html"), tmplPath)
		} else {
			tmpl, err = parseStandaloneRenderSet(funcMap, themeDir, tmplPath)
		}
		if err != nil {
			return nil, err
		}
		m.mu.Lock()
		if m.generation == generation && m.activeTheme == active {
			m.cache[cacheKey] = tmpl
		}
		m.mu.Unlock()
	}

	var buf bytes.Buffer
	executeName := filepath.Base(templateName)
	if withLayout {
		executeName = "layout"
	}
	if err := tmpl.ExecuteTemplate(&buf, executeName, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (m *Manager) templateFuncs() template.FuncMap {
	return m.templateFuncsWithSnapshot(m.AssetSnapshot())
}

func (m *Manager) templateFuncsWithSnapshot(snapshot *assets.Snapshot) template.FuncMap {
	assetURL := func(raw string) string {
		if snapshot == nil {
			return raw
		}
		return snapshot.URL(raw)
	}
	funcMap := template.FuncMap{
		"menu": func(string) interface{} { return nil },
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
		"add":      func(a, b int) int { return a + b },
		"sub":      func(a, b int) int { return a - b },
		"safeHTML": func(s string) template.HTML { return template.HTML(s) },
		"excerpt":  Excerpt, "initial": Initial, "firstImage": FirstImage,
		"readingTime": ReadingTime, "highlight": Highlight, "snippet": Snippet,
		"headingIDs": headingIDsForTemplate, "flavor": Flavor,
		"assetURL":      assetURL,
		"contentAssets": func(data any) template.HTML { return contentAssetsForTemplateWithURL(data, assetURL) },
		"themeColor": func(colors map[string]string, key, defaultValue string) string {
			if val, ok := colors[key]; ok && val != "" {
				return val
			}
			return defaultValue
		},
		"themeLayout": func(layout map[string]interface{}, key string, defaultValue interface{}) interface{} {
			if val, ok := layout[key]; ok {
				return val
			}
			return defaultValue
		},
	}
	m.mu.RLock()
	for name, fn := range m.extraFuncs {
		funcMap[name] = fn
	}
	m.mu.RUnlock()
	return funcMap
}

// parseRenderSet parses the layout, the page template and every partial in
// themeDir. Partials are files whose base name starts with "_" and ends with
// ".html"; they hold {{define}} blocks that any page may call. Each partial is
// parsed on its own so a failure names the file. The result is cached with the
// page, so partials are parsed once per cached page, like layout.html.
func parseRenderSet(funcMap template.FuncMap, themeDir, layoutPath, tmplPath string) (*template.Template, error) {
	partials, err := partialPaths(themeDir)
	if err != nil {
		return nil, err
	}
	tmpl, err := template.New("layout").Funcs(funcMap).ParseFiles(layoutPath, tmplPath)
	if err != nil {
		return nil, err
	}
	pageName := filepath.Base(tmplPath)
	for _, path := range partials {
		name := filepath.Base(path)
		if name == pageName {
			continue // already parsed as the page itself
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read partial %s: %w", name, err)
		}
		if _, err := tmpl.New(name).Parse(string(content)); err != nil {
			return nil, fmt.Errorf("parse partial %s: %w", name, err)
		}
	}
	return tmpl, nil
}

func parseStandaloneRenderSet(funcMap template.FuncMap, themeDir, tmplPath string) (*template.Template, error) {
	partials, err := partialPaths(themeDir)
	if err != nil {
		return nil, err
	}
	pageName := filepath.Base(tmplPath)
	tmpl, err := template.New(pageName).Funcs(funcMap).ParseFiles(tmplPath)
	if err != nil {
		return nil, err
	}
	for _, path := range partials {
		name := filepath.Base(path)
		if name == pageName {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read partial %s: %w", name, err)
		}
		if _, err := tmpl.New(name).Parse(string(content)); err != nil {
			return nil, fmt.Errorf("parse partial %s: %w", name, err)
		}
	}
	return tmpl, nil
}

// partialPaths returns the partial files in themeDir, sorted by name.
func partialPaths(themeDir string) ([]string, error) {
	entries, err := os.ReadDir(themeDir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "_") || !strings.HasSuffix(name, ".html") {
			continue
		}
		paths = append(paths, filepath.Join(themeDir, name))
	}
	sort.Strings(paths)
	return paths, nil
}

// GetSettings retrieves theme settings from database
func (m *Manager) GetSettings(themeName string, db interface {
	First(dest interface{}, conds ...interface{}) *gorm.DB
}) (*core.ThemeSettings, error) {
	var settings core.ThemeSettings
	result := db.First(&settings, "theme_name = ?", themeName)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		// Return default settings if not found
		return &core.ThemeSettings{ThemeName: themeName}, nil
	}
	if result.Error != nil {
		return nil, result.Error
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

var (
	htmlTagPattern    = regexp.MustCompile(`<[^>]*>`)
	whitespacePattern = regexp.MustCompile(`\s+`)
	imageSrcPattern   = regexp.MustCompile(`(?i)<img[^>]*\ssrc\s*=\s*["']([^"']+)["']`)
)

// FirstImage returns the src of the first <img> tag in s, or "" when there is
// none. Themes use it to pick a cover image for post cards.
func FirstImage(s string) string {
	if m := imageSrcPattern.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// Excerpt strips HTML tags from s, collapses whitespace and truncates the
// result to at most limit characters (runes, not bytes). A truncated excerpt
// ends with an ellipsis.
func Excerpt(s string, limit int) string {
	text := htmlTagPattern.ReplaceAllString(s, " ")
	text = strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'").Replace(text)
	text = strings.TrimSpace(whitespacePattern.ReplaceAllString(text, " "))
	if limit <= 0 || utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return strings.TrimRightFunc(string(runes[:limit]), unicode.IsSpace) + "…"
}

// Initial returns the first letter or CJK character of s in upper case,
// skipping leading whitespace and punctuation. It returns "J" when s has no
// such character.
func Initial(s string) string {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return string(unicode.ToUpper(r))
		}
	}
	return "J"
}
