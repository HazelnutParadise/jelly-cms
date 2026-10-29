package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/goccy/go-json"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// Menus API (slot C2). admin/editor only.
//
//	GET    /api/admin/menus                → [{id,name,slug,location,items:[tree of MenuItem with children]}]
//	POST   /api/admin/menus                {name, location?} → 201 menu (slug from name, unique)
//	PUT    /api/admin/menus/:id            {name?, location?} → 200 menu; assigning a location unassigns any other menu holding it
//	DELETE /api/admin/menus/:id            → 200 {"message"}
//	PUT    /api/admin/menus/:id/items      body = full tree [{id?, label, type, ref_id?, url?, icon?, new_tab?, login_only?, css_class?, children:[...]}]
//	                                       replaces all items atomically (one transaction); positions follow array order; depth max 2 (children of children → 400)
//	GET    /api/admin/menus/targets?type=page|post|category|tag&q=  → [{id,label,url}] for the picker (all roles may read)
//
// Item types and resolved URLs (core.MenuNode.URL):
//   home → "/", page → "/"+slug, post → "/post/"+slug, category → "/c/"+slug, tag → "/t/"+slug, shop → "/shop", custom → url as given.
// A typed item whose target no longer exists is skipped when rendering.
// custom url is trimmed and must be non-empty and either start with "/" or "#", or parse as a URL with scheme http, https, mailto or tel (case-insensitive); otherwise PUT items → 400 and nothing is written.
//
// Theme function: {{range menu "header"}} … {{end}} returns []core.MenuNode for the menu assigned to that location
// (empty slice when none). Active is true when the request path equals the node URL (set per request through
// theme data "CurrentPath" when available). login_only items are only returned when a user is signed in.
//
// Admin page: web/admin/menus.html per design/project/Menus.dc.html (menu list + locations, add-item panel with
// tabs 頁面/分類/文章/商店/自訂, drag tree two levels, right-side item properties).

func registerMenuRoutes(manage *echo.Group, tm *theme.Manager) {
	manage.GET("/menus", listMenus)
	manage.POST("/menus", createMenu)
	manage.PUT("/menus/:id", updateMenu)
	manage.DELETE("/menus/:id", deleteMenu)
	manage.PUT("/menus/:id/items", replaceMenuItems)
	manage.GET("/menus/targets", listMenuTargets)
	if tm != nil {
		tm.RegisterFunc("menu", menuForLocation)
	}
}

const (
	menuMaxDepth    = 2
	menuNameMaxLen  = 120
	menuLocationMax = 40
	menuLabelMaxLen = 120
	menuIconMaxLen  = 40
	menuCSSMaxLen   = 120
)

var menuItemTypes = map[string]bool{
	"home": true, "page": true, "post": true, "category": true, "tag": true, "shop": true, "custom": true,
}

var menuLikeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

type menuInput struct {
	Name     *string `json:"name"`
	Location *string `json:"location"`
}

// menuItemInput is one node of the PUT /menus/:id/items body. A client-sent id is ignored
// because the whole tree is replaced.
type menuItemInput struct {
	Label     string          `json:"label"`
	Type      string          `json:"type"`
	RefID     uint            `json:"ref_id"`
	URL       string          `json:"url"`
	Icon      string          `json:"icon"`
	NewTab    bool            `json:"new_tab"`
	LoginOnly bool            `json:"login_only"`
	CSSClass  string          `json:"css_class"`
	Children  []menuItemInput `json:"children"`
}

type menuTarget struct {
	ID    uint   `json:"id"`
	Label string `json:"label"`
	URL   string `json:"url"`
}

// menuRefs holds the typed targets referenced by a menu, loaded in one query per kind.
type menuRefs struct {
	posts      map[uint]core.Post
	categories map[uint]core.Category
	tags       map[uint]core.Tag
}

// menuForLocation resolves the menu assigned to location into nodes. It must
// never panic and returns an empty slice without a database.
func menuForLocation(location string) []core.MenuNode {
	return menuForLocationForRequest(data.DB, location, false, "")
}

func menuForLocationForRequest(db *gorm.DB, location string, signedIn bool, path string) []core.MenuNode {
	if db == nil || location == "" {
		return []core.MenuNode{}
	}
	var menus []core.Menu
	if err := db.Where("location = ?", location).Order("id ASC").Limit(1).Find(&menus).Error; err != nil || len(menus) == 0 {
		return []core.MenuNode{}
	}
	var rows []core.MenuItem
	if err := db.Where("menu_id = ?", menus[0].ID).Order("position ASC, id ASC").Find(&rows).Error; err != nil {
		return []core.MenuNode{}
	}
	refs, err := loadMenuRefsWithDB(db, rows)
	if err != nil {
		return []core.MenuNode{}
	}
	return resolveMenuNodes(buildMenuTree(rows), refs, signedIn, path)
}

func listMenus(c echo.Context) error {
	menus := []core.Menu{}
	if err := data.DB.Order("id ASC").Find(&menus).Error; err != nil {
		return err
	}
	ids := make([]uint, 0, len(menus))
	for _, m := range menus {
		ids = append(ids, m.ID)
	}
	trees, err := menuItemTrees(data.DB, ids...)
	if err != nil {
		return err
	}
	for i := range menus {
		menus[i].Items = trees[menus[i].ID]
	}
	return c.JSON(http.StatusOK, menus)
}

func createMenu(c echo.Context) error {
	in, err := decodeMenuJSON[menuInput](c)
	if err != nil {
		return menuBadRequest(c, "invalid JSON body")
	}
	name := ""
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
	}
	if name == "" || utf8.RuneCountInString(name) > menuNameMaxLen {
		return menuBadRequest(c, "name must be 1 to 120 characters")
	}
	location, ok := menuLocationValue(in.Location)
	if !ok {
		return menuBadRequest(c, "location must be 40 characters or fewer")
	}

	var menu core.Menu
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		slug, err := uniqueMenuSlug(tx, core.Slugify(name, "menu"))
		if err != nil {
			return err
		}
		menu = core.Menu{Name: name, Slug: slug, Location: location}
		if err := tx.Create(&menu).Error; err != nil {
			return err
		}
		if location == "" {
			return nil
		}
		return unassignMenuLocation(tx, location, menu.ID)
	})
	if err != nil {
		return err
	}
	menu, err = menuWithItems(data.DB, menu.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, menu)
}

func updateMenu(c echo.Context) error {
	menu, found, err := findMenuByParam(c)
	if err != nil {
		return err
	}
	if !found {
		return menuNotFound(c)
	}
	in, err := decodeMenuJSON[menuInput](c)
	if err != nil {
		return menuBadRequest(c, "invalid JSON body")
	}
	updates := map[string]any{}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" || utf8.RuneCountInString(name) > menuNameMaxLen {
			return menuBadRequest(c, "name must be 1 to 120 characters")
		}
		updates["name"] = name
	}
	if in.Location != nil {
		location, ok := menuLocationValue(in.Location)
		if !ok {
			return menuBadRequest(c, "location must be 40 characters or fewer")
		}
		updates["location"] = location
	}
	if len(updates) > 0 {
		err = data.DB.Transaction(func(tx *gorm.DB) error {
			if location, ok := updates["location"].(string); ok && location != "" {
				if err := unassignMenuLocation(tx, location, menu.ID); err != nil {
					return err
				}
			}
			return tx.Model(&core.Menu{}).Where("id = ?", menu.ID).Updates(updates).Error
		})
		if err != nil {
			return err
		}
	}
	updated, err := menuWithItems(data.DB, menu.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, updated)
}

func deleteMenu(c echo.Context) error {
	menu, found, err := findMenuByParam(c)
	if err != nil {
		return err
	}
	if !found {
		return menuNotFound(c)
	}
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("menu_id = ?", menu.ID).Delete(&core.MenuItem{}).Error; err != nil {
			return err
		}
		return tx.Delete(&core.Menu{}, menu.ID).Error
	})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "Deleted"})
}

func replaceMenuItems(c echo.Context) error {
	menu, found, err := findMenuByParam(c)
	if err != nil {
		return err
	}
	if !found {
		return menuNotFound(c)
	}
	nodes, err := decodeMenuJSON[[]menuItemInput](c)
	if err != nil {
		return menuBadRequest(c, "invalid JSON body")
	}
	if err := validateMenuTree(nodes, 1); err != nil {
		return menuBadRequest(c, err.Error())
	}
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("menu_id = ?", menu.ID).Delete(&core.MenuItem{}).Error; err != nil {
			return err
		}
		return insertMenuItems(tx, menu.ID, nil, nodes)
	})
	if err != nil {
		return err
	}
	trees, err := menuItemTrees(data.DB, menu.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, trees[menu.ID])
}

func listMenuTargets(c echo.Context) error {
	kind := c.QueryParam("type")
	if kind != "page" && kind != "post" && kind != "category" && kind != "tag" {
		return menuBadRequest(c, "type must be page, post, category or tag")
	}
	pattern := "%" + menuLikeEscaper.Replace(strings.TrimSpace(c.QueryParam("q"))) + "%"
	targets := []menuTarget{}
	switch kind {
	case "page", "post":
		var posts []core.Post
		err := data.DB.Select("id", "title", "slug").
			Where("type = ? AND status = ? AND title ILIKE ?", kind, "published", pattern).
			Order("title ASC, id ASC").Find(&posts).Error
		if err != nil {
			return err
		}
		prefix := "/"
		if kind == "post" {
			prefix = "/post/"
		}
		for _, p := range posts {
			targets = append(targets, menuTarget{ID: p.ID, Label: p.Title, URL: prefix + p.Slug})
		}
	case "category":
		var categories []core.Category
		err := data.DB.Select("id", "name", "slug").
			Where("name ILIKE ?", pattern).
			Order("name ASC, id ASC").Find(&categories).Error
		if err != nil {
			return err
		}
		for _, cat := range categories {
			targets = append(targets, menuTarget{ID: cat.ID, Label: cat.Name, URL: "/c/" + cat.Slug})
		}
	case "tag":
		var tags []core.Tag
		err := data.DB.Select("id", "name", "slug").
			Where("name ILIKE ?", pattern).
			Order("name ASC, id ASC").Find(&tags).Error
		if err != nil {
			return err
		}
		for _, tag := range tags {
			targets = append(targets, menuTarget{ID: tag.ID, Label: tag.Name, URL: "/t/" + tag.Slug})
		}
	}
	return c.JSON(http.StatusOK, targets)
}

// decodeMenuJSON decodes exactly one JSON value into T and rejects trailing data,
// the same way decodePostInput does. A JSON null is an error.
func decodeMenuJSON[T any](c echo.Context) (T, error) {
	var zero T
	var out *T
	defer c.Request().Body.Close()
	decoder := json.NewDecoder(c.Request().Body)
	if err := decoder.Decode(&out); err != nil {
		return zero, err
	}
	if out == nil {
		return zero, errors.New("expected JSON value")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return zero, errors.New("unexpected trailing data")
	}
	return *out, nil
}

func menuBadRequest(c echo.Context, message string) error {
	return c.JSON(http.StatusBadRequest, map[string]string{"error": message})
}

func menuNotFound(c echo.Context) error {
	return c.JSON(http.StatusNotFound, map[string]string{"error": "Menu not found"})
}

func parseMenuID(value string) (uint, bool) {
	id, err := strconv.ParseUint(value, 10, strconv.IntSize)
	if err != nil || id == 0 {
		return 0, false
	}
	return uint(id), true
}

// findMenuByParam loads the menu named by :id. found is false for a malformed or unknown id.
func findMenuByParam(c echo.Context) (core.Menu, bool, error) {
	var menu core.Menu
	id, ok := parseMenuID(c.Param("id"))
	if !ok {
		return menu, false, nil
	}
	err := data.DB.First(&menu, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return menu, false, nil
	}
	if err != nil {
		return menu, false, err
	}
	return menu, true, nil
}

// menuLocationValue trims a location from the request. A nil pointer means the field was not sent.
func menuLocationValue(v *string) (string, bool) {
	if v == nil {
		return "", true
	}
	location := strings.TrimSpace(*v)
	return location, utf8.RuneCountInString(location) <= menuLocationMax
}

// unassignMenuLocation clears location from every menu holding it except keepID.
func unassignMenuLocation(tx *gorm.DB, location string, keepID uint) error {
	return tx.Model(&core.Menu{}).Where("location = ? AND id <> ?", location, keepID).Update("location", "").Error
}

// uniqueMenuSlug returns base, or base-2, base-3 … when that slug is taken.
func uniqueMenuSlug(db *gorm.DB, base string) (string, error) {
	slug := base
	for n := 2; ; n++ {
		var count int64
		if err := db.Model(&core.Menu{}).Where("slug = ?", slug).Count(&count).Error; err != nil {
			return "", err
		}
		if count == 0 {
			return slug, nil
		}
		slug = fmt.Sprintf("%s-%d", base, n)
	}
}

// menuWithItems loads one menu with its item tree.
func menuWithItems(db *gorm.DB, id uint) (core.Menu, error) {
	var menu core.Menu
	if err := db.First(&menu, id).Error; err != nil {
		return menu, err
	}
	trees, err := menuItemTrees(db, id)
	if err != nil {
		return menu, err
	}
	menu.Items = trees[id]
	return menu, nil
}

// menuItemTrees loads the item trees of the given menus with one query.
// Every requested menu id is present in the result, with an empty slice when it has no items.
func menuItemTrees(db *gorm.DB, menuIDs ...uint) (map[uint][]core.MenuItem, error) {
	trees := make(map[uint][]core.MenuItem, len(menuIDs))
	if len(menuIDs) == 0 {
		return trees, nil
	}
	var rows []core.MenuItem
	if err := db.Where("menu_id IN ?", menuIDs).Order("position ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	flat := make(map[uint][]core.MenuItem, len(menuIDs))
	for _, row := range rows {
		flat[row.MenuID] = append(flat[row.MenuID], row)
	}
	for _, id := range menuIDs {
		trees[id] = buildMenuTree(flat[id])
	}
	return trees, nil
}

// buildMenuTree nests flat rows under their parents, keeping the given order among siblings.
func buildMenuTree(rows []core.MenuItem) []core.MenuItem {
	byParent := make(map[uint][]core.MenuItem)
	roots := []core.MenuItem{}
	for _, row := range rows {
		if row.ParentID == nil {
			roots = append(roots, row)
			continue
		}
		byParent[*row.ParentID] = append(byParent[*row.ParentID], row)
	}
	var attach func([]core.MenuItem) []core.MenuItem
	attach = func(nodes []core.MenuItem) []core.MenuItem {
		for i := range nodes {
			nodes[i].Children = attach(byParent[nodes[i].ID])
		}
		return nodes
	}
	return attach(roots)
}

// validateMenuTree checks the whole tree before anything is written. depth is 1 for root items.
func validateMenuTree(nodes []menuItemInput, depth int) error {
	if len(nodes) > 0 && depth > menuMaxDepth {
		return errors.New("menu items may nest at most two levels")
	}
	for _, node := range nodes {
		label := strings.TrimSpace(node.Label)
		switch {
		case label == "":
			return errors.New("label is required")
		case utf8.RuneCountInString(label) > menuLabelMaxLen:
			return errors.New("label must be 120 characters or fewer")
		case !menuItemTypes[node.Type]:
			return errors.New("type must be home, page, post, category, tag, shop or custom")
		case node.Type == "custom" && !menuCustomURLAllowed(strings.TrimSpace(node.URL)):
			return errors.New("custom url must start with / or #, or use http, https, mailto or tel")
		case utf8.RuneCountInString(node.Icon) > menuIconMaxLen:
			return errors.New("icon must be 40 characters or fewer")
		case utf8.RuneCountInString(node.CSSClass) > menuCSSMaxLen:
			return errors.New("css_class must be 120 characters or fewer")
		}
		if err := validateMenuTree(node.Children, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// menuCustomURLAllowed reports whether an already-trimmed custom item url may be stored.
func menuCustomURLAllowed(trimmed string) bool {
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "#") {
		return true
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "mailto", "tel":
		return true
	}
	return false
}

// insertMenuItems writes a validated tree. Positions are the array index among siblings.
func insertMenuItems(tx *gorm.DB, menuID uint, parentID *uint, nodes []menuItemInput) error {
	for i, node := range nodes {
		storedURL := node.URL
		if node.Type == "custom" {
			storedURL = strings.TrimSpace(node.URL)
		}
		row := core.MenuItem{
			MenuID:    menuID,
			ParentID:  parentID,
			Position:  i,
			Label:     strings.TrimSpace(node.Label),
			Type:      node.Type,
			RefID:     node.RefID,
			URL:       storedURL,
			Icon:      node.Icon,
			NewTab:    node.NewTab,
			LoginOnly: node.LoginOnly,
			CSSClass:  node.CSSClass,
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		if len(node.Children) > 0 {
			if err := insertMenuItems(tx, menuID, &row.ID, node.Children); err != nil {
				return err
			}
		}
	}
	return nil
}

// loadMenuRefs loads the published pages and posts, categories and tags that the
// typed items point at. Unknown or unpublished targets are simply absent from the maps.
func loadMenuRefs(rows []core.MenuItem) (menuRefs, error) {
	return loadMenuRefsWithDB(data.DB, rows)
}

func loadMenuRefsWithDB(db *gorm.DB, rows []core.MenuItem) (menuRefs, error) {
	refs := menuRefs{
		posts:      make(map[uint]core.Post),
		categories: make(map[uint]core.Category),
		tags:       make(map[uint]core.Tag),
	}
	var postIDs, categoryIDs, tagIDs []uint
	for _, row := range rows {
		switch row.Type {
		case "page", "post":
			postIDs = append(postIDs, row.RefID)
		case "category":
			categoryIDs = append(categoryIDs, row.RefID)
		case "tag":
			tagIDs = append(tagIDs, row.RefID)
		}
	}
	if len(postIDs) > 0 {
		var posts []core.Post
		if err := publicPostVisibility(db).Select("posts.id", "posts.type", "posts.slug").
			Where("posts.id IN ?", postIDs).Find(&posts).Error; err != nil {
			return refs, err
		}
		for _, p := range posts {
			refs.posts[p.ID] = p
		}
	}
	if len(categoryIDs) > 0 {
		var categories []core.Category
		if err := db.Select("id", "slug").Where("id IN ?", categoryIDs).Find(&categories).Error; err != nil {
			return refs, err
		}
		for _, cat := range categories {
			refs.categories[cat.ID] = cat
		}
	}
	if len(tagIDs) > 0 {
		var tags []core.Tag
		if err := db.Select("id", "slug").Where("id IN ?", tagIDs).Find(&tags).Error; err != nil {
			return refs, err
		}
		for _, tag := range tags {
			refs.tags[tag.ID] = tag
		}
	}
	return refs, nil
}

// menuItemURL resolves an item to its URL. ok is false when a typed target is missing or unpublished.
func menuItemURL(item core.MenuItem, refs menuRefs) (string, bool) {
	switch item.Type {
	case "home":
		return "/", true
	case "page":
		p, ok := refs.posts[item.RefID]
		if !ok || p.Type != "page" {
			return "", false
		}
		return "/" + p.Slug, true
	case "post":
		p, ok := refs.posts[item.RefID]
		if !ok || p.Type != "post" {
			return "", false
		}
		return "/post/" + p.Slug, true
	case "category":
		cat, ok := refs.categories[item.RefID]
		if !ok {
			return "", false
		}
		return "/c/" + cat.Slug, true
	case "tag":
		tag, ok := refs.tags[item.RefID]
		if !ok {
			return "", false
		}
		return "/t/" + tag.Slug, true
	case "shop":
		return "/shop", true
	case "custom":
		return item.URL, true
	}
	return "", false
}

// menuNodesFrom converts the item tree into theme nodes. An item that is skipped takes its children with it.
// login_only items are always skipped because menuForLocation has no signed-in user.
func menuNodesFrom(items []core.MenuItem, refs menuRefs) []core.MenuNode {
	return resolveMenuNodes(items, refs, false, "")
}

func resolveMenuNodes(items []core.MenuItem, refs menuRefs, signedIn bool, path string) []core.MenuNode {
	nodes := []core.MenuNode{}
	for _, item := range items {
		if item.LoginOnly && !signedIn {
			continue
		}
		url, ok := menuItemURL(item, refs)
		if !ok {
			continue
		}
		node := core.MenuNode{
			Label:    item.Label,
			URL:      url,
			Icon:     item.Icon,
			NewTab:   item.NewTab,
			CSSClass: item.CSSClass,
			Active:   url != "" && path == url,
			Children: resolveMenuNodes(item.Children, refs, signedIn, path),
		}
		nodes = append(nodes, node)
	}
	return nodes
}
