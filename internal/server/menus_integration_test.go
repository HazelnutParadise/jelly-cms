package server

// Menus integration tests for ticket 04 slot C2 (contract in the header of
// handlers_menus.go). They are written against the contract before the
// implementation exists and are expected to fail until the menus API and
// menuForLocation are implemented. Do not edit them to match an implementation.
//
// Menu item positions are checked for ordering only (they must follow array
// order), because the contract does not fix the base index.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

type menuTestItem struct {
	ID        uint           `json:"id"`
	Label     string         `json:"label"`
	Type      string         `json:"type"`
	RefID     uint           `json:"ref_id"`
	URL       string         `json:"url"`
	NewTab    bool           `json:"new_tab"`
	LoginOnly bool           `json:"login_only"`
	Position  int            `json:"position"`
	Children  []menuTestItem `json:"children"`
}

type menuTestMenu struct {
	ID       uint           `json:"id"`
	Name     string         `json:"name"`
	Slug     string         `json:"slug"`
	Location string         `json:"location"`
	Items    []menuTestItem `json:"items"`
}

type menuTestTarget struct {
	ID    uint   `json:"id"`
	Label string `json:"label"`
	URL   string `json:"url"`
}

func menuTestCheck(t *testing.T, r accessResult, where string, want int) {
	t.Helper()
	if r.panicked != nil {
		t.Fatalf("%s panicked: %v", where, r.panicked)
	}
	if r.code != want {
		t.Fatalf("%s returned %d, want %d; body=%q", where, r.code, want, r.body)
	}
}

func menuTestDecode(t *testing.T, r accessResult, where string, out any) {
	t.Helper()
	if err := json.Unmarshal([]byte(r.body), out); err != nil {
		t.Fatalf("%s: decode response: %v; body=%q", where, err, r.body)
	}
}

func menuTestCount(t *testing.T, h *accessHarness, model any, query string, args ...any) int64 {
	t.Helper()
	var n int64
	q := h.db.Model(model)
	if query != "" {
		q = q.Where(query, args...)
	}
	if err := q.Count(&n).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

func menuTestList(t *testing.T, h *accessHarness, token string) []menuTestMenu {
	t.Helper()
	r := h.request(http.MethodGet, "/api/admin/menus", "", token, echo.MIMEApplicationJSON, "")
	menuTestCheck(t, r, "GET /api/admin/menus", http.StatusOK)
	var menus []menuTestMenu
	menuTestDecode(t, r, "GET /api/admin/menus", &menus)
	return menus
}

func menuTestMenuByID(t *testing.T, menus []menuTestMenu, id uint) menuTestMenu {
	t.Helper()
	for _, m := range menus {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("menu %d missing from GET /api/admin/menus", id)
	return menuTestMenu{}
}

func menuTestCreate(t *testing.T, h *accessHarness, token, body string) menuTestMenu {
	t.Helper()
	r := h.request(http.MethodPost, "/api/admin/menus", body, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	menuTestCheck(t, r, "POST /api/admin/menus "+body, http.StatusCreated)
	var m menuTestMenu
	menuTestDecode(t, r, "POST /api/admin/menus", &m)
	return m
}

func menuTestItemsOf(t *testing.T, h *accessHarness, token string, menuID uint) []menuTestItem {
	t.Helper()
	return menuTestMenuByID(t, menuTestList(t, h, token), menuID).Items
}

func menuTestLabelsJoined(items []menuTestItem) string {
	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, item.Label)
	}
	return strings.Join(labels, "|")
}

func menuTestPositionsAscending(items []menuTestItem) bool {
	for i := 1; i < len(items); i++ {
		if items[i].Position <= items[i-1].Position {
			return false
		}
	}
	return true
}

func menuTestTargets(t *testing.T, h *accessHarness, token, typ, q string) []menuTestTarget {
	t.Helper()
	values := url.Values{"type": {typ}, "q": {q}}
	path := "/api/admin/menus/targets?" + values.Encode()
	r := h.request(http.MethodGet, path, "", token, echo.MIMEApplicationJSON, "")
	menuTestCheck(t, r, "GET "+path, http.StatusOK)
	var out []menuTestTarget
	menuTestDecode(t, r, "GET "+path, &out)
	return out
}

// TestMenusRequireAdminOrEditorAndReturnJSON401 covers the role table: anonymous
// callers get a JSON 401, authors get 403 on every /api/admin/menus route, and
// editors may use the menu API.
func TestMenusRequireAdminOrEditorAndReturnJSON401(t *testing.T) {
	h := newAccessHarness(t)
	editor := h.user("editor", "menus-editor")
	author := h.user("author", "menus-author")
	existing := core.Menu{Name: "既有選單", Slug: "existing-menu", Location: "footer"}
	if err := h.db.Create(&existing).Error; err != nil {
		t.Fatalf("seed menu: %v", err)
	}
	itemPath := fmt.Sprintf("/api/admin/menus/%d", existing.ID)
	routes := []struct{ method, path, body string }{
		{http.MethodGet, "/api/admin/menus", ""},
		{http.MethodPost, "/api/admin/menus", `{"name":"權限測試","location":"header"}`},
		{http.MethodPut, itemPath, `{"location":"header"}`},
		{http.MethodDelete, itemPath, ""},
		{http.MethodPut, itemPath + "/items", `[{"label":"首頁","type":"home"}]`},
		{http.MethodGet, "/api/admin/menus/targets?type=page", ""},
	}
	state := func() string {
		return fmt.Sprintf("menus=%d items=%d",
			menuTestCount(t, h, &core.Menu{}, ""), menuTestCount(t, h, &core.MenuItem{}, ""))
	}

	for _, route := range routes {
		for _, accept := range []string{echo.MIMEApplicationJSON, "text/html"} {
			before := state()
			r := h.request(route.method, route.path, route.body, "", accept, echo.MIMEApplicationJSON)
			h.mustNoPanic(r, "anonymous "+route.method+" "+route.path)
			if r.code != http.StatusUnauthorized {
				t.Errorf("anonymous %s %s Accept=%q returned %d, want 401; body=%q", route.method, route.path, accept, r.code, r.body)
			}
			if !strings.Contains(strings.ToLower(r.header.Get(echo.HeaderContentType)), "application/json") {
				t.Errorf("anonymous %s %s content type = %q, want JSON", route.method, route.path, r.header.Get(echo.HeaderContentType))
			}
			if after := state(); after != before {
				t.Errorf("anonymous %s %s changed the database: %s -> %s", route.method, route.path, before, after)
			}
		}
	}

	authorToken := h.token(author)
	for _, route := range routes {
		before := state()
		r := h.request(route.method, route.path, route.body, authorToken, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
		h.mustNoPanic(r, "author "+route.method+" "+route.path)
		// Note: the handler header says targets are readable by all roles, but the
		// spec for this slot says author gets 403 on any /api/admin/menus method.
		// The targets route is in the same admin/editor group, so it is included here.
		if r.code != http.StatusForbidden {
			t.Errorf("author %s %s returned %d, want 403; body=%q", route.method, route.path, r.code, r.body)
		}
		if after := state(); after != before {
			t.Errorf("author %s %s changed the database: %s -> %s", route.method, route.path, before, after)
		}
	}

	editorToken := h.token(editor)
	listed := h.request(http.MethodGet, "/api/admin/menus", "", editorToken, echo.MIMEApplicationJSON, "")
	menuTestCheck(t, listed, "editor GET /api/admin/menus", http.StatusOK)
	created := h.request(http.MethodPost, "/api/admin/menus", `{"name":"編輯者建立"}`, editorToken, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	menuTestCheck(t, created, "editor POST /api/admin/menus", http.StatusCreated)
	targets := h.request(http.MethodGet, "/api/admin/menus/targets?type=page", "", editorToken, echo.MIMEApplicationJSON, "")
	menuTestCheck(t, targets, "editor GET /api/admin/menus/targets", http.StatusOK)
}

// TestMenusCreateAssignsUniqueSlugFromName checks that the slug keeps CJK
// characters and that a duplicate name gets a numeric suffix.
func TestMenusCreateAssignsUniqueSlugFromName(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("editor", "menus-slug-editor"))

	first := menuTestCreate(t, h, token, `{"name":"主選單","location":"header"}`)
	if first.Slug != "主選單" {
		t.Errorf("first slug = %q, want %q", first.Slug, "主選單")
	}
	second := menuTestCreate(t, h, token, `{"name":"主選單","location":"header"}`)
	if second.Slug != "主選單-2" {
		t.Errorf("duplicate-name slug = %q, want %q", second.Slug, "主選單-2")
	}
	if second.ID == first.ID {
		t.Errorf("duplicate-name menu reused id %d", first.ID)
	}
}

// TestMenusLocationBelongsToOneMenu checks that assigning a location to one menu
// unassigns it from the menu that held it before.
func TestMenusLocationBelongsToOneMenu(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("editor", "menus-location-editor"))
	first := menuTestCreate(t, h, token, `{"name":"頁首甲","location":"header"}`)
	second := menuTestCreate(t, h, token, `{"name":"頁首乙"}`)

	assign := h.request(http.MethodPut, fmt.Sprintf("/api/admin/menus/%d", second.ID), `{"location":"header"}`, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	menuTestCheck(t, assign, "PUT assign header location", http.StatusOK)

	menus := menuTestList(t, h, token)
	if got := menuTestMenuByID(t, menus, first.ID).Location; got != "" {
		t.Errorf("previous header menu location = %q, want empty", got)
	}
	if got := menuTestMenuByID(t, menus, second.ID).Location; got != "header" {
		t.Errorf("new header menu location = %q, want header", got)
	}
	if n := menuTestCount(t, h, &core.Menu{}, "location = ?", "header"); n != 1 {
		t.Errorf("%d menus hold location header, want 1", n)
	}
}

// TestMenusItemsTreeNestingAndReplacement covers the two-level item tree, the
// rejection of a three-level tree, and full replacement by a shorter tree.
func TestMenusItemsTreeNestingAndReplacement(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("editor", "menus-items-editor"))
	menu := menuTestCreate(t, h, token, `{"name":"項目樹","location":"header"}`)
	itemsPath := fmt.Sprintf("/api/admin/menus/%d/items", menu.ID)
	put := func(body string) accessResult {
		return h.request(http.MethodPut, itemsPath, body, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	}

	twoLevel := `[
		{"label":"首頁","type":"home"},
		{"label":"關於","type":"custom","url":"/about","children":[
			{"label":"團隊","type":"custom","url":"/team"},
			{"label":"歷史","type":"custom","url":"/history"}
		]},
		{"label":"聯絡","type":"custom","url":"/contact"}
	]`
	menuTestCheck(t, put(twoLevel), "PUT two-level items", http.StatusOK)

	items := menuTestItemsOf(t, h, token, menu.ID)
	if len(items) != 3 {
		t.Fatalf("root item count = %d, want 3; items=%+v", len(items), items)
	}
	if got := menuTestLabelsJoined(items); got != "首頁|關於|聯絡" {
		t.Errorf("root labels = %q, want %q", got, "首頁|關於|聯絡")
	}
	if !menuTestPositionsAscending(items) {
		t.Errorf("root positions do not follow array order: %+v", items)
	}
	if len(items[1].Children) != 2 {
		t.Fatalf("children of 關於 = %d, want 2; items=%+v", len(items[1].Children), items[1])
	}
	if got := menuTestLabelsJoined(items[1].Children); got != "團隊|歷史" {
		t.Errorf("children of 關於 = %q, want %q", got, "團隊|歷史")
	}
	if !menuTestPositionsAscending(items[1].Children) {
		t.Errorf("child positions do not follow array order: %+v", items[1].Children)
	}
	if len(items[0].Children) != 0 || len(items[2].Children) != 0 {
		t.Errorf("leaf roots gained children: 首頁=%d 聯絡=%d", len(items[0].Children), len(items[2].Children))
	}
	if n := menuTestCount(t, h, &core.MenuItem{}, "menu_id = ?", menu.ID); n != 5 {
		t.Errorf("stored %d item rows, want 5 (3 roots + 2 children)", n)
	}

	threeLevel := `[{"label":"甲","type":"custom","url":"/a","children":[
		{"label":"乙","type":"custom","url":"/b","children":[
			{"label":"丙","type":"custom","url":"/c"}
		]}
	]}]`
	menuTestCheck(t, put(threeLevel), "PUT three-level items", http.StatusBadRequest)
	items = menuTestItemsOf(t, h, token, menu.ID)
	if got := menuTestLabelsJoined(items); got != "首頁|關於|聯絡" {
		t.Errorf("rejected three-level PUT changed roots to %q, want %q", got, "首頁|關於|聯絡")
	}
	if n := menuTestCount(t, h, &core.MenuItem{}, "menu_id = ?", menu.ID); n != 5 {
		t.Errorf("rejected three-level PUT left %d item rows, want 5", n)
	}

	shorter := `[{"label":"只剩一個","type":"custom","url":"/only"}]`
	menuTestCheck(t, put(shorter), "PUT shorter items", http.StatusOK)
	items = menuTestItemsOf(t, h, token, menu.ID)
	if len(items) != 1 || items[0].Label != "只剩一個" {
		t.Errorf("after replacement roots = %+v, want exactly [只剩一個]", items)
	}
	if len(items) == 1 && len(items[0].Children) != 0 {
		t.Errorf("replacement left children behind: %+v", items[0].Children)
	}
	if n := menuTestCount(t, h, &core.MenuItem{}, "menu_id = ?", menu.ID); n != 1 {
		t.Errorf("after replacement stored %d item rows, want 1; old items were not removed", n)
	}
}

// TestMenusTargetsFilterByTypeAndQuery covers the picker: type filter, title
// substring filter, the resolved URL per type, and the 400 for unknown types.
func TestMenusTargetsFilterByTypeAndQuery(t *testing.T) {
	h := newAccessHarness(t)
	editor := h.user("editor", "menus-targets-editor")
	about := core.Post{Title: "關於我們", Slug: "about-us", Content: "about", Type: "page", Status: "published", AuthorID: editor.ID}
	privacy := core.Post{Title: "隱私權政策", Slug: "privacy", Content: "privacy", Type: "page", Status: "published", AuthorID: editor.ID}
	blog := core.Post{Title: "關於部落格", Slug: "blog-about", Content: "post", Type: "post", Status: "published", AuthorID: editor.ID}
	for _, p := range []*core.Post{&about, &privacy, &blog} {
		if err := h.db.Create(p).Error; err != nil {
			t.Fatalf("seed post %q: %v", p.Title, err)
		}
	}
	keyCat := core.Category{Name: "關鍵分類", Slug: "key-topics"}
	news := core.Category{Name: "新聞", Slug: "news"}
	for _, c := range []*core.Category{&keyCat, &news} {
		if err := h.db.Create(c).Error; err != nil {
			t.Fatalf("seed category %q: %v", c.Name, err)
		}
	}
	token := h.token(editor)

	pages := menuTestTargets(t, h, token, "page", "關")
	if len(pages) != 1 {
		t.Fatalf("type=page q=關 returned %d targets, want 1; got=%+v", len(pages), pages)
	}
	if pages[0].ID != about.ID || pages[0].Label != "關於我們" || pages[0].URL != "/about-us" {
		t.Errorf("type=page q=關 target = %+v, want id=%d label=關於我們 url=/about-us", pages[0], about.ID)
	}

	categories := menuTestTargets(t, h, token, "category", "關")
	if len(categories) != 1 {
		t.Fatalf("type=category q=關 returned %d targets, want 1; got=%+v", len(categories), categories)
	}
	if categories[0].ID != keyCat.ID || categories[0].Label != "關鍵分類" || categories[0].URL != "/c/key-topics" {
		t.Errorf("type=category q=關 target = %+v, want id=%d label=關鍵分類 url=/c/key-topics", categories[0], keyCat.ID)
	}

	unknown := h.request(http.MethodGet, "/api/admin/menus/targets?type=video&q=關", "", token, echo.MIMEApplicationJSON, "")
	menuTestCheck(t, unknown, "GET targets type=video", http.StatusBadRequest)
}

// TestMenusMenuForLocationResolvesTreeAndSkipsDeadTargets checks the theme
// function's output: URLs resolved per item type, dead targets skipped, and
// login_only items left out.
func TestMenusMenuForLocationResolvesTreeAndSkipsDeadTargets(t *testing.T) {
	h := newAccessHarness(t)
	editor := h.user("editor", "menus-resolve-editor")
	about := core.Post{Title: "關於我們", Slug: "about-us", Content: "about", Type: "page", Status: "published", AuthorID: editor.ID}
	if err := h.db.Create(&about).Error; err != nil {
		t.Fatalf("seed page: %v", err)
	}
	news := core.Category{Name: "新聞", Slug: "news"}
	if err := h.db.Create(&news).Error; err != nil {
		t.Fatalf("seed category: %v", err)
	}
	menu := core.Menu{Name: "頁首", Slug: "resolve-header", Location: "header"}
	if err := h.db.Create(&menu).Error; err != nil {
		t.Fatalf("seed menu: %v", err)
	}
	rows := []core.MenuItem{
		{MenuID: menu.ID, Position: 0, Label: "首頁", Type: "home"},
		{MenuID: menu.ID, Position: 1, Label: "關於", Type: "page", RefID: about.ID},
		{MenuID: menu.ID, Position: 2, Label: "新聞", Type: "category", RefID: news.ID},
		{MenuID: menu.ID, Position: 3, Label: "外部", Type: "custom", URL: "https://example.com", NewTab: true},
		// A page target that does not exist; it must be skipped when rendering.
		{MenuID: menu.ID, Position: 4, Label: "已刪除", Type: "page", RefID: 987654321},
		// Contract: login_only items are returned only for a signed-in user.
		// menuForLocation takes only a location, so it treats every caller as
		// anonymous and must leave this item out.
		{MenuID: menu.ID, Position: 5, Label: "會員專區", Type: "custom", URL: "/member", LoginOnly: true},
	}
	if err := h.db.Create(&rows).Error; err != nil {
		t.Fatalf("seed menu items: %v", err)
	}

	nodes := menuForLocation("header")
	wantLabels := []string{"首頁", "關於", "新聞", "外部"}
	wantURLs := []string{"/", "/about-us", "/c/news", "https://example.com"}
	if len(nodes) != len(wantURLs) {
		t.Fatalf("menuForLocation(header) returned %d nodes, want %d; nodes=%+v", len(nodes), len(wantURLs), nodes)
	}
	for i := range wantURLs {
		if nodes[i].Label != wantLabels[i] {
			t.Errorf("node %d label = %q, want %q", i, nodes[i].Label, wantLabels[i])
		}
		if nodes[i].URL != wantURLs[i] {
			t.Errorf("node %d URL = %q, want %q", i, nodes[i].URL, wantURLs[i])
		}
		if wantNewTab := i == 3; nodes[i].NewTab != wantNewTab {
			t.Errorf("node %d NewTab = %v, want %v", i, nodes[i].NewTab, wantNewTab)
		}
	}

	if unassigned := menuForLocation("footer"); len(unassigned) != 0 {
		t.Errorf("menuForLocation(footer) with no assigned menu returned %d nodes, want 0", len(unassigned))
	}
}

// TestMenusMenuForLocationWithoutDatabaseReturnsEmpty covers the header rule
// that menuForLocation never panics and returns an empty slice without a database.
func TestMenusMenuForLocationWithoutDatabaseReturnsEmpty(t *testing.T) {
	previous := data.DB
	data.DB = nil
	t.Cleanup(func() { data.DB = previous })

	var nodes []core.MenuNode
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("menuForLocation panicked without a database: %v", r)
			}
		}()
		nodes = menuForLocation("header")
	}()
	if len(nodes) != 0 {
		t.Errorf("menuForLocation without a database returned %d nodes, want 0", len(nodes))
	}
}

// TestMenusDeleteRemovesMenuAndItems checks that a deleted menu disappears from
// the list and that its items are removed from the database, while another
// menu's items are kept.
func TestMenusDeleteRemovesMenuAndItems(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "menus-delete-admin"))
	doomed := menuTestCreate(t, h, token, `{"name":"將刪除","location":"footer"}`)
	kept := menuTestCreate(t, h, token, `{"name":"保留","location":"mobile"}`)

	setItems := func(id uint, body string) {
		t.Helper()
		r := h.request(http.MethodPut, fmt.Sprintf("/api/admin/menus/%d/items", id), body, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
		menuTestCheck(t, r, fmt.Sprintf("PUT /api/admin/menus/%d/items", id), http.StatusOK)
	}
	setItems(doomed.ID, `[{"label":"首頁","type":"home"},{"label":"外部","type":"custom","url":"https://example.com"}]`)
	setItems(kept.ID, `[{"label":"保留項目","type":"custom","url":"/keep"}]`)
	if n := menuTestCount(t, h, &core.MenuItem{}, "menu_id = ?", doomed.ID); n != 2 {
		t.Fatalf("precondition: doomed menu has %d items, want 2", n)
	}

	del := h.request(http.MethodDelete, fmt.Sprintf("/api/admin/menus/%d", doomed.ID), "", token, echo.MIMEApplicationJSON, "")
	menuTestCheck(t, del, "DELETE /api/admin/menus/:id", http.StatusOK)
	var msg map[string]any
	menuTestDecode(t, del, "DELETE /api/admin/menus/:id", &msg)
	if _, ok := msg["message"]; !ok {
		t.Errorf("DELETE response has no message field: %q", del.body)
	}

	menus := menuTestList(t, h, token)
	for _, m := range menus {
		if m.ID == doomed.ID {
			t.Errorf("deleted menu %d still listed by GET /api/admin/menus", doomed.ID)
		}
	}
	menuTestMenuByID(t, menus, kept.ID)

	if n := menuTestCount(t, h, &core.MenuItem{}, "menu_id = ?", doomed.ID); n != 0 {
		t.Errorf("deleted menu left %d item rows in the database, want 0", n)
	}
	if n := menuTestCount(t, h, &core.Menu{}, "id = ?", doomed.ID); n != 0 {
		t.Errorf("deleted menu row still exists in the database")
	}
	if n := menuTestCount(t, h, &core.MenuItem{}, "menu_id = ?", kept.ID); n != 1 {
		t.Errorf("kept menu has %d item rows, want 1", n)
	}
}

// TestMenusThemeFunctionRendersResolvedLinks renders a temporary theme that uses
// {{range menu "header"}} and checks the links in the output.
func TestMenusThemeFunctionRendersResolvedLinks(t *testing.T) {
	h := newAccessHarness(t)
	menu := core.Menu{Name: "頁首", Slug: "theme-header", Location: "header"}
	if err := h.db.Create(&menu).Error; err != nil {
		t.Fatalf("seed menu: %v", err)
	}
	rows := []core.MenuItem{
		{MenuID: menu.ID, Position: 0, Label: "首頁", Type: "home"},
		{MenuID: menu.ID, Position: 1, Label: "關於", Type: "custom", URL: "/about"},
	}
	if err := h.db.Create(&rows).Error; err != nil {
		t.Fatalf("seed menu items: %v", err)
	}

	themesRoot := t.TempDir()
	themeDir := filepath.Join(themesRoot, "menu-test")
	if err := os.MkdirAll(themeDir, 0755); err != nil {
		t.Fatalf("create temp theme: %v", err)
	}
	files := map[string]string{
		"theme.json":  `{"name":"menu-test","version":"1.0.0"}`,
		"layout.html": `{{define "layout"}}{{range menu "header"}}<a href="{{.URL}}">{{.Label}}</a>{{end}}{{end}}`,
		"index.html":  `{{define "content"}}content{{end}}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(themeDir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write theme file %s: %v", name, err)
		}
	}

	tm := theme.NewManager(themesRoot)
	if err := tm.Activate("menu-test"); err != nil {
		t.Fatalf("activate temp theme: %v", err)
	}
	tm.RegisterFunc("menu", menuForLocation)
	out, err := tm.Render("index.html", map[string]any{})
	if err != nil {
		t.Fatalf("render theme template with menu function: %v", err)
	}
	want := `<a href="/">首頁</a><a href="/about">關於</a>`
	if got := string(out); got != want {
		t.Errorf("rendered header menu = %q, want %q", got, want)
	}
}

// menuCustomURLBad lists custom item URLs that PUT /menus/:id/items must reject:
// a script or data scheme (any letter case, with or without leading whitespace),
// an empty URL, and a bare relative path that starts with neither "/" nor "#".
var menuCustomURLBad = []string{
	"javascript:alert(1)",
	"JavaScript:alert(1)",
	"data:text/html,x",
	"vbscript:x",
	"  javascript:alert(1)",
	"",
	"evil.example",
}

// menuCustomURLGood lists custom item URLs that PUT /menus/:id/items must accept.
var menuCustomURLGood = []string{
	"/about",
	"#top",
	"https://example.com/x?y=1",
	"mailto:hi@example.com",
	"tel:+886912345678",
	"HTTP://EXAMPLE.COM",
}

func menuTestMarshal(t *testing.T, v any) string {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode request body: %v", err)
	}
	return string(body)
}

// TestMenusCustomURLValidation checks the custom item URL rule: a relative path
// starting with "/" or "#", or an absolute URL whose scheme is http, https, mailto
// or tel (case-insensitive). Other URLs get a 400 with an error body.
func TestMenusCustomURLValidation(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("editor", "menus-url-editor"))
	itemsPathOf := func(id uint) string { return fmt.Sprintf("/api/admin/menus/%d/items", id) }

	for _, url := range menuCustomURLGood {
		t.Run("accept "+url, func(t *testing.T) {
			menu := menuTestCreate(t, h, token, `{"name":"網址接受"}`)
			body := menuTestMarshal(t, []map[string]any{{"label": "連結", "type": "custom", "url": url}})
			r := h.request(http.MethodPut, itemsPathOf(menu.ID), body, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
			menuTestCheck(t, r, "PUT custom url "+url, http.StatusOK)
			items := menuTestItemsOf(t, h, token, menu.ID)
			if len(items) != 1 || items[0].URL != url {
				t.Errorf("stored items = %+v, want one custom item with url %q", items, url)
			}
		})
	}

	for _, url := range menuCustomURLBad {
		t.Run(fmt.Sprintf("reject %q", url), func(t *testing.T) {
			menu := menuTestCreate(t, h, token, `{"name":"網址拒絕"}`)
			body := menuTestMarshal(t, []map[string]any{{"label": "連結", "type": "custom", "url": url}})
			r := h.request(http.MethodPut, itemsPathOf(menu.ID), body, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
			menuTestCheck(t, r, "PUT custom url "+fmt.Sprintf("%q", url), http.StatusBadRequest)
			var e map[string]any
			menuTestDecode(t, r, "PUT rejected custom url", &e)
			if msg, _ := e["error"].(string); msg == "" {
				t.Errorf("rejected url %q: response has no error message; body=%q", url, r.body)
			}
			if n := menuTestCount(t, h, &core.MenuItem{}, "menu_id = ?", menu.ID); n != 0 {
				t.Errorf("rejected url %q stored %d item rows, want 0", url, n)
			}
		})
	}
}

// TestMenusCustomURLRejectLeavesTreeUnchanged checks that a PUT with a bad custom
// URL, at the root or nested under a valid parent, is refused as a whole: the
// previous tree is still returned by GET and no rows change.
func TestMenusCustomURLRejectLeavesTreeUnchanged(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("editor", "menus-url-unchanged-editor"))
	menu := menuTestCreate(t, h, token, `{"name":"網址不變"}`)
	itemsPath := fmt.Sprintf("/api/admin/menus/%d/items", menu.ID)
	put := func(body string) accessResult {
		return h.request(http.MethodPut, itemsPath, body, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	}

	baseline := `[
		{"label":"首頁","type":"home"},
		{"label":"關於","type":"custom","url":"/about","children":[
			{"label":"團隊","type":"custom","url":"/team"}
		]}
	]`
	menuTestCheck(t, put(baseline), "PUT baseline items", http.StatusOK)

	checkUnchanged := func(where string) {
		t.Helper()
		items := menuTestItemsOf(t, h, token, menu.ID)
		if got := menuTestLabelsJoined(items); got != "首頁|關於" {
			t.Errorf("%s: root labels = %q, want %q; items=%+v", where, got, "首頁|關於", items)
			return
		}
		if items[1].URL != "/about" || len(items[1].Children) != 1 || items[1].Children[0].URL != "/team" {
			t.Errorf("%s: tree changed: %+v", where, items)
		}
		if n := menuTestCount(t, h, &core.MenuItem{}, "menu_id = ?", menu.ID); n != 3 {
			t.Errorf("%s: stored %d item rows, want 3", where, n)
		}
	}
	checkUnchanged("after baseline")

	for _, url := range menuCustomURLBad {
		rootBad := menuTestMarshal(t, []map[string]any{
			{"label": "新首頁", "type": "home"},
			{"label": "壞", "type": "custom", "url": url},
		})
		childBad := menuTestMarshal(t, []map[string]any{
			{"label": "新首頁", "type": "home"},
			{"label": "關於", "type": "custom", "url": "/about", "children": []map[string]any{
				{"label": "壞", "type": "custom", "url": url},
			}},
		})
		for placement, body := range map[string]string{"root": rootBad, "child": childBad} {
			t.Run(fmt.Sprintf("%s %q", placement, url), func(t *testing.T) {
				r := put(body)
				menuTestCheck(t, r, "PUT bad custom url at "+placement, http.StatusBadRequest)
				checkUnchanged("after rejected " + placement + " url " + fmt.Sprintf("%q", url))
			})
		}
	}

	menus := menuTestList(t, h, token)
	if got := menuTestItemsOf(t, h, token, menu.ID); len(got) != 2 {
		t.Errorf("final root count = %d, want 2; menus=%+v", len(got), menus)
	}
}

// menuThemeTree is the header/footer fixture: labels that cannot appear in the default theme by
// accident, one nested child, and one custom item whose href is distinctive.
const menuThemeTree = `[
	{"label":"Zebra Guides","type":"custom","url":"/zebra-guides","children":[
		{"label":"Quokka Notes","type":"custom","url":"/quokka-notes"}
	]},
	{"label":"Contact Z","type":"custom","url":"/contact-z"}
]`

// menuThemeSeedPost creates one published post and returns its slug.
func menuThemeSeedPost(t *testing.T, h *accessHarness) string {
	t.Helper()
	author := h.user("author", "menutheme-author")
	post := archivesSeedPost(t, h, author, archivesPost{
		title:       "Menu Theme Post",
		publishedAt: archivesDay(2025, time.May, 3),
	})
	if post.Slug == "" {
		t.Fatalf("seeded post has no slug")
	}
	return post.Slug
}

// menuThemeAssertPresent checks that every menu label and the custom href appear in body.
func menuThemeAssertPresent(t *testing.T, where, body string, want []string) {
	t.Helper()
	for _, s := range want {
		if !strings.Contains(body, s) {
			t.Errorf("%s: rendered HTML lacks %q", where, s)
		}
	}
}

// TestMenusHeaderMenuRendersInTheme checks that the default theme's header renders the menu assigned
// to location "header" on the home page and on a post page, and that it falls back to the hard-coded
// 首頁 link while no header menu exists.
func TestMenusHeaderMenuRendersInTheme(t *testing.T) {
	h := archivesHarness(t)
	token := h.token(h.user("admin", "menutheme-header-admin"))
	slug := menuThemeSeedPost(t, h)
	pages := []string{"/", "/post/" + slug}

	for _, path := range pages {
		r := archivesGet(t, h, path)
		archivesWantStatus(t, path, r, http.StatusOK)
		if !strings.Contains(r.body, "首頁") {
			t.Errorf("GET %s without a header menu lacks fallback link 首頁", path)
		}
	}

	menu := menuTestCreate(t, h, token, `{"name":"Theme Header","location":"header"}`)
	itemsPath := fmt.Sprintf("/api/admin/menus/%d/items", menu.ID)
	menuTestCheck(t, h.request(http.MethodPut, itemsPath, menuThemeTree, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON),
		"PUT header items", http.StatusOK)

	want := []string{"Zebra Guides", "Quokka Notes", "Contact Z", "/contact-z"}
	for _, path := range pages {
		r := archivesGet(t, h, path)
		archivesWantStatus(t, path, r, http.StatusOK)
		menuThemeAssertPresent(t, "GET "+path+" with header menu", r.body, want)
	}
}

// TestMenusFooterMenuRendersInTheme checks that the default theme's footer renders the menu assigned
// to location "footer" inside the page footer, on the home page and on a post page.
func TestMenusFooterMenuRendersInTheme(t *testing.T) {
	h := archivesHarness(t)
	token := h.token(h.user("admin", "menutheme-footer-admin"))
	slug := menuThemeSeedPost(t, h)
	pages := []string{"/", "/post/" + slug}

	for _, path := range pages {
		r := archivesGet(t, h, path)
		archivesWantStatus(t, path, r, http.StatusOK)
	}

	menu := menuTestCreate(t, h, token, `{"name":"Theme Footer","location":"footer"}`)
	itemsPath := fmt.Sprintf("/api/admin/menus/%d/items", menu.ID)
	menuTestCheck(t, h.request(http.MethodPut, itemsPath, menuThemeTree, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON),
		"PUT footer items", http.StatusOK)

	want := []string{"Zebra Guides", "Quokka Notes", "Contact Z", "/contact-z"}
	for _, path := range pages {
		r := archivesGet(t, h, path)
		archivesWantStatus(t, path, r, http.StatusOK)
		idx := strings.LastIndex(r.body, "<footer")
		if idx < 0 {
			t.Fatalf("GET %s: rendered HTML has no <footer> element", path)
		}
		menuThemeAssertPresent(t, "GET "+path+" footer area with footer menu", r.body[idx:], want)
	}
}
