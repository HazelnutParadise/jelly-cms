package server

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
)

// snapshotNavItems copies the nav registry and restores it when the test ends,
// so entries registered here never leak into other tests.
func snapshotNavItems(t *testing.T) {
	t.Helper()
	navMu.RLock()
	saved := make([]NavItem, len(navItems))
	copy(saved, navItems)
	navMu.RUnlock()
	t.Cleanup(func() {
		navMu.Lock()
		navItems = saved
		navMu.Unlock()
	})
}

func navTestContext() echo.Context {
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	return echo.New().NewContext(req, httptest.NewRecorder())
}

// navSectionsAndItems returns the section keys in render order and the entry
// keys inside each section.
func navSectionsAndItems(sections []NavSection) ([]string, map[string][]string) {
	var order []string
	items := map[string][]string{}
	for _, s := range sections {
		order = append(order, s.Key)
		for _, e := range s.Items {
			items[s.Key] = append(items[s.Key], e.Key)
		}
	}
	return order, items
}

func TestAdminNavRegisterNavReplacesSameKey(t *testing.T) {
	snapshotNavItems(t)
	RegisterNav(NavItem{Key: "nav-test-dup", Section: "content", Label: "first", Icon: "i-one", Href: "/admin/first", Order: 1})
	RegisterNav(NavItem{Key: "nav-test-dup", Section: "content", Label: "second", Icon: "i-two", Href: "/admin/second", Order: 2})

	navMu.RLock()
	var matches []NavItem
	for _, item := range navItems {
		if item.Key == "nav-test-dup" {
			matches = append(matches, item)
		}
	}
	navMu.RUnlock()
	if len(matches) != 1 {
		t.Fatalf("registry holds %d entries for key nav-test-dup, want 1", len(matches))
	}
	if matches[0].Href != "/admin/second" || matches[0].Order != 2 {
		t.Fatalf("registry kept %+v, want the second registration", matches[0])
	}

	admin := &core.User{Username: "admin-user", Role: "admin"}
	var rendered []NavEntry
	for _, section := range navFor(navTestContext(), admin, "", nil) {
		for _, entry := range section.Items {
			if entry.Key == "nav-test-dup" {
				rendered = append(rendered, entry)
			}
		}
	}
	if len(rendered) != 1 {
		t.Fatalf("navFor rendered %d entries for nav-test-dup, want 1", len(rendered))
	}
	if rendered[0].Href != "/admin/second" {
		t.Fatalf("navFor rendered href %q, want /admin/second", rendered[0].Href)
	}
}

func TestAdminNavRoleVisibility(t *testing.T) {
	cases := []struct {
		role     string
		sections []string
		items    map[string][]string
	}{
		{
			role:     "admin",
			sections: []string{"content", "shop", "appearance", "system"},
			items: map[string][]string{
				"content":    {"dashboard", "posts", "pages", "media"},
				"shop":       {"products", "orders"},
				"appearance": {"themes", "plugins"},
				"system":     {"settings"},
			},
		},
		{
			role:     "editor",
			sections: []string{"content"},
			items: map[string][]string{
				"content": {"dashboard", "posts", "pages", "media"},
			},
		},
		{
			role:     "author",
			sections: []string{"content"},
			items: map[string][]string{
				"content": {"dashboard", "posts", "media"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.role, func(t *testing.T) {
			user := &core.User{Username: tc.role + "-user", Role: tc.role}
			sections, items := navSectionsAndItems(navFor(navTestContext(), user, "", nil))
			if !reflect.DeepEqual(sections, tc.sections) {
				t.Errorf("sections for %s = %v, want %v", tc.role, sections, tc.sections)
			}
			if !reflect.DeepEqual(items, tc.items) {
				t.Errorf("items for %s = %v, want %v", tc.role, items, tc.items)
			}
		})
	}
}

func TestAdminNavActiveAndBadge(t *testing.T) {
	snapshotNavItems(t)
	RegisterNav(NavItem{
		Key:     "nav-test-badge",
		Section: "content",
		Label:   "badgeTest",
		Icon:    "i-x",
		Href:    "/admin/badge",
		Order:   50,
		Badge:   func(echo.Context) int { return 7 },
	})

	admin := &core.User{Username: "admin-user", Role: "admin"}
	seen := map[string]NavEntry{}
	for _, section := range navFor(navTestContext(), admin, "posts", nil) {
		for _, entry := range section.Items {
			seen[entry.Key] = entry
		}
	}

	posts, ok := seen["posts"]
	if !ok {
		t.Fatal("navFor did not include the posts entry for admin")
	}
	if !posts.Active {
		t.Error("posts entry Active = false, want true when active is posts")
	}
	for key, entry := range seen {
		if key != "posts" && entry.Active {
			t.Errorf("entry %q Active = true, want false when active is posts", key)
		}
	}

	badge, ok := seen["nav-test-badge"]
	if !ok {
		t.Fatal("navFor did not include the nav-test-badge entry")
	}
	if badge.Badge != 7 {
		t.Errorf("badge entry Badge = %d, want 7", badge.Badge)
	}
	if dashboard := seen["dashboard"]; dashboard.Badge != 0 {
		t.Errorf("entry without Badge func has Badge = %d, want 0", dashboard.Badge)
	}
}

func TestAdminNavNilUserSeesOnlyUnrestrictedEntries(t *testing.T) {
	want := map[string]bool{}
	navMu.RLock()
	for _, item := range navItems {
		if len(item.Roles) != 0 {
			continue
		}
		for _, section := range navSections {
			if item.Section == section.Key {
				want[item.Key] = true
			}
		}
	}
	navMu.RUnlock()
	if len(want) == 0 {
		t.Fatal("no unrestricted nav entries registered; test cannot judge nil-user filtering")
	}

	got := map[string]bool{}
	for _, section := range navFor(navTestContext(), nil, "", nil) {
		for _, entry := range section.Items {
			got[entry.Key] = true
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("nil user saw %v, want exactly the unrestricted entries %v", got, want)
	}
	for _, restricted := range []string{"pages", "products", "orders", "themes", "plugins", "settings"} {
		if got[restricted] {
			t.Errorf("nil user saw role-restricted entry %q", restricted)
		}
	}
}

func TestTranslateReturnsValueOrKey(t *testing.T) {
	values := map[string]string{"posts": "文章", "blank": ""}
	if got := translate(values, "posts"); got != "文章" {
		t.Errorf("translate(posts) = %q, want 文章", got)
	}
	if got := translate(values, "missingKey"); got != "missingKey" {
		t.Errorf("translate(missingKey) = %q, want the key itself", got)
	}
	if got := translate(values, "blank"); got != "blank" {
		t.Errorf("translate(blank) = %q, want the key because empty values count as missing", got)
	}
	if got := translate(nil, "posts"); got != "posts" {
		t.Errorf("translate with nil map = %q, want the key itself", got)
	}
}
