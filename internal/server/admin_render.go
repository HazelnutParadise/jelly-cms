package server

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
)

// Crumb is one segment of the topbar breadcrumb. An empty Href renders as
// plain text.
type Crumb struct {
	Label string
	Href  string
}

// AdminPage describes one admin screen to render.
type AdminPage struct {
	// File is the template under web/admin, e.g. "posts.html".
	File string
	// Active is the NavItem.Key this screen belongs to.
	Active string
	// Title overrides the template's "title" block in the browser tab when set.
	Title string
	// Crumbs render after the section name. Empty falls back to the template's
	// "title" block, which keeps existing pages working unchanged.
	Crumbs []Crumb
	// Data is merged into the template data under its own keys.
	Data map[string]interface{}
}

// AdminViewer is what the layout knows about the signed-in user.
type AdminViewer struct {
	ID       uint
	Name     string
	Initial  string
	Role     string
	Email    string
	Avatar   string
	IsAdmin  bool
	Unread   int
	Settings string
}

// UnreadNotifications is set by the notifications feature; nil means the bell
// shows no count.
var UnreadNotifications func(c echo.Context, userID uint) int

var (
	localeMu    sync.RWMutex
	localeCache = map[string]map[string]string{}
	// localesDir can be pointed at a temp directory by tests.
	localesDir = "web/locales"
	// adminDir can be pointed at a temp directory by tests.
	adminDir = "web/admin"
)

// loadTranslations returns the whole locale file as a flat map so templates
// never have to list the keys they use. Unknown languages fall back to zh-TW,
// then to an empty map; templates print the key itself for missing entries.
func loadTranslations(lang string) map[string]string {
	localeMu.RLock()
	if m, ok := localeCache[lang]; ok {
		localeMu.RUnlock()
		return m
	}
	localeMu.RUnlock()

	m := readLocale(lang)
	if m == nil && lang != "zh-TW" {
		m = readLocale("zh-TW")
	}
	if m == nil {
		m = map[string]string{}
	}
	localeMu.Lock()
	localeCache[lang] = m
	localeMu.Unlock()
	return m
}

func readLocale(lang string) map[string]string {
	if strings.ContainsAny(lang, `/\.`) {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(localesDir, lang+".json"))
	if err != nil {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

// resetTranslationCache is used by tests after swapping localesDir.
func resetTranslationCache() {
	localeMu.Lock()
	localeCache = map[string]map[string]string{}
	localeMu.Unlock()
}

// adminFuncs are the template helpers every admin template may use.
func adminFuncs() template.FuncMap {
	return template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		"dict": func(values ...interface{}) map[string]interface{} {
			dict := make(map[string]interface{})
			for i := 0; i+1 < len(values); i += 2 {
				key, _ := values[i].(string)
				dict[key] = values[i+1]
			}
			return dict
		},
		"list": func(items ...interface{}) []interface{} { return items },
		"json": func(v interface{}) template.JS {
			b, err := json.Marshal(v)
			if err != nil {
				return template.JS("null")
			}
			return template.JS(b)
		},
		"safeHTML": func(s string) template.HTML { return template.HTML(s) },
		"assetURL": func(raw string) string { return raw },
	}
}

func adminFuncsFor(c echo.Context) template.FuncMap {
	funcs := adminFuncs()
	funcs["assetURL"] = requestAssetURL(c)
	return funcs
}

// viewerFor builds the layout's user block from the request. It never fails:
// an unresolved user renders as a generic administrator so the layout is still
// usable on pages that only require RequireAuth.
func viewerFor(c echo.Context) (*core.User, AdminViewer) {
	user, _ := auth.GetUserFromContext(c)
	if user == nil {
		if u, err := currentUser(c); err == nil {
			user = u
		}
	}
	v := AdminViewer{Name: "Administrator", Initial: "A", Role: "admin", IsAdmin: true}
	if user != nil {
		name := user.Username
		if name == "" {
			name = user.Email
		}
		v = AdminViewer{ID: user.ID, Name: name, Initial: initialOf(name), Role: user.Role, Email: user.Email, Avatar: user.AvatarURL, IsAdmin: user.Role == "admin"}
		if UnreadNotifications != nil {
			v.Unread = UnreadNotifications(c, user.ID)
		}
	}
	return user, v
}

func initialOf(s string) string {
	for _, r := range strings.TrimSpace(s) {
		return strings.ToUpper(string(r))
	}
	return "J"
}

// renderAdminPage renders an admin screen inside the shared layout. The layout
// receives: Active, Lang, T (full locale map), Nav (sections for this role),
// Viewer, Crumbs, Title, SiteTitle, plus every key of p.Data.
func renderAdminPage(c echo.Context, p AdminPage) error {
	user, viewer := viewerFor(c)
	lang, _ := c.Get("lang").(string)
	if lang == "" {
		lang = "zh-TW"
	}
	if c.QueryParam("lang") == "" {
		cookie, err := c.Cookie("lang")
		if err != nil || !supportedLanguage(cookie.Value) {
			if user != nil && supportedLanguage(user.Locale) {
				lang = user.Locale
			}
		}
	}

	t := loadTranslations(lang)
	theme := "system"
	if user != nil && (user.UITheme == "light" || user.UITheme == "dark") {
		theme = user.UITheme
	}

	files := []string{filepath.Join(adminDir, "components.html"), filepath.Join(adminDir, "layout.html"), filepath.Join(adminDir, p.File)}
	if p.File == "post_editor.html" || p.File == "page_editor.html" {
		editor := filepath.Join(adminDir, "editor.html")
		if _, statErr := os.Stat(editor); statErr == nil {
			files = append(files, editor)
		}
	}
	tmpl, err := template.New("base").Funcs(adminFuncsFor(c)).ParseFiles(files...)
	if err != nil {
		return fmt.Errorf("parse admin template %s: %w", p.File, err)
	}

	data := map[string]interface{}{
		"Active":    p.Active,
		"Lang":      lang,
		"UITheme":   theme,
		"T":         t,
		"Nav":       navFor(c, user, p.Active, t),
		"Viewer":    viewer,
		"Crumbs":    p.Crumbs,
		"Title":     p.Title,
		"SiteTitle": siteTitleOrDefault(),
	}
	for k, v := range p.Data {
		data[k] = v
	}
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
	if !hasNoStore(c.Response().Header().Get(echo.HeaderCacheControl)) {
		c.Response().Header().Set(echo.HeaderCacheControl, "no-cache")
	}
	return tmpl.ExecuteTemplate(c.Response().Writer, "base", data)
}

func enforceStaffAdminAPIs(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		path := c.Request().URL.Path
		if path != "/api/admin" && !strings.HasPrefix(path, "/api/admin/") {
			return next(c)
		}
		principal, err := auth.GetCurrentPrincipal(c)
		if err != nil || principal == nil || principal.User == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
		}
		switch principal.User.Role {
		case "admin", "editor", "author":
			return next(c)
		default:
			return c.JSON(http.StatusForbidden, map[string]string{"error": "Forbidden"})
		}
	}
}

// siteTitleOrDefault tolerates a missing database so the layout can render on
// error pages before installation finishes.
func siteTitleOrDefault() string {
	defer func() { _ = recover() }()
	return getSiteTitle()
}
