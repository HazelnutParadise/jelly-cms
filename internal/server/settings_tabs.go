package server

import (
	"net/http"
	"sort"
	"sync"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
)

// SettingsTab is one tab of the settings page at /admin/settings/:tab.
// Features register their own tabs with RegisterSettingsTab.
type SettingsTab struct {
	// Key is the URL segment, e.g. "site".
	Key string
	// Label is a translation key, looked up in the locale map.
	Label string
	// Href defaults to /admin/settings/<Key>.
	Href string
	// Order sorts tabs; lower first.
	Order int
	// Roles that may see the tab. Empty means admin only.
	Roles []string
}

// settingsTabView is a SettingsTab resolved for one request.
type settingsTabView struct {
	Key    string
	Label  string
	Href   string
	Active bool
}

var (
	settingsTabsMu sync.RWMutex
	settingsTabs   []SettingsTab
)

// RegisterSettingsTab adds tabs. A tab with the same Key replaces the earlier one.
func RegisterSettingsTab(tabs ...SettingsTab) {
	settingsTabsMu.Lock()
	defer settingsTabsMu.Unlock()
	for _, tab := range tabs {
		if tab.Href == "" {
			tab.Href = "/admin/settings/" + tab.Key
		}
		if len(tab.Roles) == 0 {
			tab.Roles = []string{"admin"}
		}
		replaced := false
		for i := range settingsTabs {
			if settingsTabs[i].Key == tab.Key {
				settingsTabs[i] = tab
				replaced = true
				break
			}
		}
		if !replaced {
			settingsTabs = append(settingsTabs, tab)
		}
	}
}

// settingsTabsFor returns the tabs the user may see, in Order.
func settingsTabsFor(user *core.User) []SettingsTab {
	settingsTabsMu.RLock()
	all := append([]SettingsTab(nil), settingsTabs...)
	settingsTabsMu.RUnlock()

	out := make([]SettingsTab, 0, len(all))
	for _, tab := range all {
		if roleAllowed(user, tab.Roles) {
			out = append(out, tab)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}

// registerSettingsRoutes mounts the settings pages. Both routes require admin.
func registerSettingsRoutes(e *echo.Echo) {
	pages := e.Group("/admin/settings", auth.RequireRole("admin"))
	pages.GET("", func(c echo.Context) error {
		return c.Redirect(http.StatusFound, "/admin/settings/site")
	})
	pages.GET("/:tab", handleSettingsTab)
}

// handleSettingsTab renders the settings page with the tab bar and the active tab.
// An unknown or unauthorised tab is a 404.
func handleSettingsTab(c echo.Context) error {
	user, _ := auth.GetUserFromContext(c)
	active := c.Param("tab")

	var views []settingsTabView
	found := false
	for _, tab := range settingsTabsFor(user) {
		isActive := tab.Key == active
		found = found || isActive
		views = append(views, settingsTabView{Key: tab.Key, Label: tab.Label, Href: tab.Href, Active: isActive})
	}
	if !found {
		return echo.ErrNotFound
	}
	return renderAdminPage(c, AdminPage{
		File:   "settings.html",
		Active: "settings",
		Data: map[string]interface{}{
			"Tabs":      views,
			"ActiveTab": active,
		},
	})
}

func init() {
	RegisterSettingsTab(
		SettingsTab{Key: "site", Label: "settingsTabSite", Order: 10},
		SettingsTab{Key: "routes", Label: "settingsTabRoutes", Order: 20},
		SettingsTab{Key: "crawlers", Label: "settingsTabCrawlers", Order: 30},
		SettingsTab{Key: "auth", Label: "settingsTabAuth", Order: 40},
		SettingsTab{Key: "comments", Label: "settingsTabComments", Order: 50},
		// shared with ticket 07: the payment tab is registered by 07; this
		// stub keeps the existing payment pane reachable until 07 lands.
		SettingsTab{Key: "payment", Label: "settingsTabPayment", Order: 55},
		SettingsTab{Key: "mail", Label: "settingsTabMail", Order: 60},
		SettingsTab{Key: "infra", Label: "settingsTabInfra", Order: 70},
		SettingsTab{Key: "update", Label: "settingsTabUpdate", Order: 80},
	)
}
