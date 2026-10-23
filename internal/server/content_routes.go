package server

import (
	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

// Content organisation (docs/tickets/04-content-organization.md).
//
// Roles: admin and editor manage categories, tags, menus, comments and the
// recycle bin. Authors read taxonomy (to assign terms to their own posts),
// may create tags, and see revisions and trash of their own posts only.
//
// Every handler below lives in its own file so slots can be filled in
// parallel:
//
//	handlers_taxonomy.go   categories + tags API
//	handlers_menus.go      menus API + theme "menu" function
//	handlers_comments.go   public comment API, moderation API, theme data
//	handlers_trash.go      recycle bin API + purge
//	handlers_revisions.go  revision history API
//	handlers_archives.go   public /c /t /author /archive pages
func registerContentRoutes(e *echo.Echo, tm *theme.Manager, renderAdmin func(c echo.Context, file, active string) error) {
	manage := e.Group("/api/admin", auth.RequireRole("admin", "editor"))
	content := e.Group("/api/admin", auth.RequireRole("admin", "editor", "author"))

	registerTaxonomyRoutes(manage, content)
	registerMenuRoutes(manage, tm)
	registerCommentRoutes(e, manage, tm)
	registerTrashRoutes(manage, content)
	registerRevisionRoutes(content)
	registerArchiveRoutes(e, tm)

	pages := e.Group("/admin", auth.RequireAuth)
	pages.GET("/taxonomy", func(c echo.Context) error { return renderAdmin(c, "taxonomy.html", "taxonomy") })
	pages.GET("/menus", func(c echo.Context) error { return renderAdmin(c, "menus.html", "menus") })
	pages.GET("/comments", func(c echo.Context) error { return renderAdmin(c, "comments.html", "comments") })
	pages.GET("/trash", func(c echo.Context) error { return renderAdmin(c, "trash.html", "trash") })
	pages.GET("/posts/:id/revisions", func(c echo.Context) error { return renderAdmin(c, "revisions.html", "posts") })
	pages.GET("/pages/:id/revisions", func(c echo.Context) error { return renderAdmin(c, "revisions.html", "pages") })
}

func init() {
	editors := []string{"admin", "editor"}
	RegisterNav(
		NavItem{Key: "taxonomy", Section: "content", Label: "taxonomy", Icon: "i-tag", Href: "/admin/taxonomy", Order: 30, Roles: editors},
		NavItem{Key: "comments", Section: "content", Label: "comments", Icon: "i-comment", Href: "/admin/comments", Order: 40, Roles: editors, Badge: pendingCommentCount},
		NavItem{Key: "trash", Section: "content", Label: "trash", Icon: "i-trash", Href: "/admin/trash", Order: 70},
		NavItem{Key: "menus", Section: "appearance", Label: "menus", Icon: "i-menu", Href: "/admin/menus", Order: 10, Roles: editors},
	)
	RegisterPaletteCommands(
		PaletteCommand{Key: "approve-comments", Label: "approvePendingComments", Keywords: []string{"comments", "留言", "審核"}, Icon: "i-check", Href: "/admin/comments?status=pending", Roles: editors},
		PaletteCommand{Key: "new-category", Label: "createCategory", Keywords: []string{"category", "分類"}, Icon: "i-tag", Href: "/admin/taxonomy#new", Roles: editors},
	)
}
