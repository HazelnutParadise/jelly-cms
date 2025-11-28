package server

import (
	"html/template"
	"net/http"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

func RegisterPublicRoutes(e *echo.Echo, tm *theme.Manager) {
	e.GET("/", func(c echo.Context) error {
		// Get Home Page (or list of posts)
		// For now, just render a dummy home page
		data := map[string]interface{}{
			"SiteTitle": "Jelly CMS",
			"Title":     "Welcome Home",
			"Content":   "This is the home page content.",
		}

		html, err := tm.Render("index.html", data)
		if err != nil {
			return c.String(http.StatusInternalServerError, err.Error())
		}
		return c.HTMLBlob(http.StatusOK, html)
	})

	e.GET("/:slug", func(c echo.Context) error {
		slug := c.Param("slug")
		var post core.Post
		if err := data.DB.Where("slug = ?", slug).First(&post).Error; err != nil {
			return c.String(http.StatusNotFound, "Page not found")
		}

		data := map[string]interface{}{
			"SiteTitle": "Jelly CMS",
			"Title":     post.Title,
			"Content":   template.HTML(post.Content), // Unsafe! Needs sanitization in real app
		}

		html, err := tm.Render("index.html", data)
		if err != nil {
			return c.String(http.StatusInternalServerError, err.Error())
		}
		return c.HTMLBlob(http.StatusOK, html)
	})
}
