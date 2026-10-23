package server

import (
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

// Public archives (slot C5). Each page lists published, public posts, pinned first then newest,
// 12 per page (?page=), 404 for unknown terms or pages beyond the end.
//
//	GET /c/:slug            category.html   data: Term (core.Category), Children ([]core.Category), Posts, Page, TotalPages, Total, Breadcrumbs ([]core.Category from root)
//	                        includes posts of descendant categories unless a descendant has NoCascade; HideFromList categories are still reachable here
//	GET /t/:slug            tag.html        data: Term (core.Tag), Related ([]core.Tag that co-occur most), Posts, Page, TotalPages, Total
//	GET /author/:username   author.html     data: Author (core.User without secrets), Posts, Page, TotalPages, Total, TopCategories ([]core.Category with PostCount)
//	GET /archive            archive.html    data: Categories (tree, with PostCount, hidden ones excluded), Tags (by PostCount desc), Months ([{Year, Month, Count}]), Authors ([]core.User with PostCount), Total
//	GET /archive/:year/:month  archive_month.html  data: Year, Month, Posts, Page, TotalPages, Total
//
// Every data map also carries SiteTitle, Title and CurrentPath. Posts are preloaded with Author, Categories, Tags.
// Templates live in web/themes/default and follow design/project/Archive.dc.html (category: blue head),
// Tag.dc.html (tag: ink head; month: yellow head), Author.dc.html (mint head), ArchiveIndex.dc.html.
// posts.html / index.html / post.html show each post's categories as chips and link to /c/<slug>;
// post.html shows tags and links the author name to /author/<username>.
//
// The /:slug catch-all in handlers_public.go must keep working for pages: register these routes before it
// (RegisterRoutes already calls registerContentRoutes ahead of RegisterPublicRoutes).

func registerArchiveRoutes(e *echo.Echo, tm *theme.Manager) {
	e.GET("/c/:slug", archiveCategory(tm))
	e.GET("/t/:slug", archiveTag(tm))
	e.GET("/author/:username", archiveAuthor(tm))
	e.GET("/archive", archiveIndex(tm))
	e.GET("/archive/:year/:month", archiveMonth(tm))
}

func archiveCategory(tm *theme.Manager) echo.HandlerFunc { return notImplemented }
func archiveTag(tm *theme.Manager) echo.HandlerFunc      { return notImplemented }
func archiveAuthor(tm *theme.Manager) echo.HandlerFunc   { return notImplemented }
func archiveIndex(tm *theme.Manager) echo.HandlerFunc    { return notImplemented }
func archiveMonth(tm *theme.Manager) echo.HandlerFunc    { return notImplemented }
