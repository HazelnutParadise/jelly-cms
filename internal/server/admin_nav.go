package server

import (
	"sort"
	"sync"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
)

// NavItem is one entry in the admin rail. Features register their own entries
// so the rail only ever lists screens that really exist.
type NavItem struct {
	// Key matches AdminPage.Active and marks the entry as current.
	Key string
	// Section is one of the keys in navSections.
	Section string
	// Label is a translation key looked up in the locale map.
	Label string
	// Icon is the sprite symbol id without the leading "#" (e.g. "i-grid").
	Icon string
	Href string
	// Roles that may see the entry. Empty means every signed-in role.
	Roles []string
	// Badge returns a count shown beside the label; nil or 0 shows nothing.
	Badge func(c echo.Context) int
	// Order sorts entries inside a section; lower first.
	Order int
}

// NavSection groups rail entries under a translated title.
type NavSection struct {
	Key   string
	Label string
	Items []NavEntry
}

// NavEntry is a NavItem resolved for one request.
type NavEntry struct {
	Key    string
	Label  string
	Icon   string
	Href   string
	Active bool
	Badge  int
}

// navSections fixes the order of rail sections. Label is a translation key.
var navSections = []struct{ Key, Label string }{
	{"content", "content"},
	{"shop", "shop"},
	{"appearance", "appearance"},
	{"system", "system"},
}

var (
	navMu    sync.RWMutex
	navItems []NavItem
)

// RegisterNav adds rail entries. Registering the same Key twice replaces the
// earlier entry so tests and features can re-register safely.
func RegisterNav(items ...NavItem) {
	navMu.Lock()
	defer navMu.Unlock()
	for _, item := range items {
		replaced := false
		for i := range navItems {
			if navItems[i].Key == item.Key {
				navItems[i] = item
				replaced = true
				break
			}
		}
		if !replaced {
			navItems = append(navItems, item)
		}
	}
}

// navFor resolves the rail for the signed-in user: entries the role may not
// see are dropped, empty sections are dropped, badges are evaluated.
func navFor(c echo.Context, user *core.User, active string, t map[string]string) []NavSection {
	navMu.RLock()
	items := make([]NavItem, len(navItems))
	copy(items, navItems)
	navMu.RUnlock()

	sort.SliceStable(items, func(i, j int) bool { return items[i].Order < items[j].Order })

	var out []NavSection
	for _, section := range navSections {
		entry := NavSection{Key: section.Key, Label: translate(t, section.Label)}
		for _, item := range items {
			if item.Section != section.Key || !roleAllowed(user, item.Roles) {
				continue
			}
			badge := 0
			if item.Badge != nil {
				badge = item.Badge(c)
			}
			entry.Items = append(entry.Items, NavEntry{
				Key:    item.Key,
				Label:  translate(t, item.Label),
				Icon:   item.Icon,
				Href:   item.Href,
				Active: item.Key == active,
				Badge:  badge,
			})
		}
		if len(entry.Items) > 0 {
			out = append(out, entry)
		}
	}
	return out
}

func roleAllowed(user *core.User, roles []string) bool {
	if len(roles) == 0 {
		return true
	}
	if user == nil {
		return false
	}
	for _, role := range roles {
		if role == user.Role {
			return true
		}
	}
	return false
}

func translate(t map[string]string, key string) string {
	if v, ok := t[key]; ok && v != "" {
		return v
	}
	return key
}

func init() {
	adminOnly := []string{"admin"}
	RegisterNav(
		NavItem{Key: "dashboard", Section: "content", Label: "dashboard", Icon: "i-grid", Href: "/admin", Order: 0},
		NavItem{Key: "posts", Section: "content", Label: "posts", Icon: "i-file-text", Href: "/admin/posts", Order: 10},
		NavItem{Key: "pages", Section: "content", Label: "pages", Icon: "i-layout", Href: "/admin/pages", Order: 20, Roles: []string{"admin", "editor"}},
		NavItem{Key: "media", Section: "content", Label: "media", Icon: "i-image", Href: "/admin/media", Order: 60},
		NavItem{Key: "products", Section: "shop", Label: "products", Icon: "i-box", Href: "/admin/products", Order: 0, Roles: adminOnly},
		NavItem{Key: "orders", Section: "shop", Label: "orders", Icon: "i-cart", Href: "/admin/orders", Order: 10, Roles: adminOnly},
		NavItem{Key: "themes", Section: "appearance", Label: "themes", Icon: "i-layers", Href: "/admin/themes", Order: 0, Roles: adminOnly},
		NavItem{Key: "plugins", Section: "appearance", Label: "plugins", Icon: "i-plug", Href: "/admin/plugins", Order: 20, Roles: adminOnly},
		NavItem{Key: "settings", Section: "system", Label: "settings", Icon: "i-gear", Href: "/admin/settings", Order: 90, Roles: adminOnly},
	)
}
