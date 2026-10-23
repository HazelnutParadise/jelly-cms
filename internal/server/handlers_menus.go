package server

import (
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
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

// menuForLocation resolves the menu assigned to location into nodes. It must
// never panic and returns an empty slice without a database.
func menuForLocation(location string) []core.MenuNode { return nil }

func listMenus(c echo.Context) error        { return notImplemented(c) }
func createMenu(c echo.Context) error       { return notImplemented(c) }
func updateMenu(c echo.Context) error       { return notImplemented(c) }
func deleteMenu(c echo.Context) error       { return notImplemented(c) }
func replaceMenuItems(c echo.Context) error { return notImplemented(c) }
func listMenuTargets(c echo.Context) error  { return notImplemented(c) }
