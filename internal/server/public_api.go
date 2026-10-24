package server

// Public read-only JSON API used by the WebMCP tools (ticket 13, docs/tickets/13-webmcp.md).
//
// GET /api/public/posts?page=&per_page=  list_posts
// GET /api/public/content/:slug          get_post (published, public posts and pages only)
// GET /api/public/site                   get_site_info
//
// Errors answer {"code", "message", "correlation_id"}; every non-public slug
// answers exactly like an unknown slug.

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

func registerPublicAPIRoutes(e *echo.Echo) {
	e.GET("/api/public/posts", func(c echo.Context) error {
		// TODO(ticket 13): list public posts.
		return echo.NewHTTPError(http.StatusNotImplemented)
	})
	e.GET("/api/public/content/:slug", func(c echo.Context) error {
		// TODO(ticket 13): one published, public post or page as plain text.
		return echo.NewHTTPError(http.StatusNotImplemented)
	})
	e.GET("/api/public/site", func(c echo.Context) error {
		// TODO(ticket 13): site name, description, language and URL.
		return echo.NewHTTPError(http.StatusNotImplemented)
	})
}
