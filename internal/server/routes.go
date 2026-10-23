package server

import (
	"net/http"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

// RegisterRoutes sets up the server routes.
func RegisterRoutes(e *echo.Echo, tm *theme.Manager, pluginRuntime *plugin.Runtime) {
	gate := newInstallGate()
	e.Use(gate.middleware)
	e.Use(installStateMiddleware)
	e.Use(I18nMiddleware)
	e.Static("/uploads", "data/uploads")
	e.Static("/admin/assets", "web/admin/assets")

	// Install Routes
	registerInstallRoutes(e, tm, pluginRuntime)

	// Auth Routes
	auth.RegisterRoutes(e)

	// Payment Routes
	RegisterPaymentRoutes(e)

	// Admin Routes
	RegisterAdminRoutes(e, tm)
	RegisterEcommerceRoutes(e)

	// Helper to render admin templates through the shared layout.
	renderAdmin := func(c echo.Context, file string, active string) error {
		return renderAdminPage(c, AdminPage{File: file, Active: active})
	}

	// Error pages (JSON for APIs, admin screen under /admin, theme elsewhere).
	e.HTTPErrorHandler = newHTTPErrorHandler(tm)

	// ⌘K palette
	registerPaletteRoutes(e)

	// Admin page routes with authentication
	adminPages := e.Group("/admin", auth.RequireAuth)
	adminPages.GET("", func(c echo.Context) error {
		return renderAdmin(c, "index.html", "dashboard")
	})

	adminPages.GET("/posts", func(c echo.Context) error {
		return renderAdmin(c, "posts.html", "posts")
	})

	adminPages.GET("/posts/new", func(c echo.Context) error {
		return renderAdmin(c, "post_editor.html", "posts")
	})

	adminPages.GET("/posts/:id", func(c echo.Context) error {
		return renderAdmin(c, "post_editor.html", "posts")
	})

	adminPages.GET("/pages", func(c echo.Context) error {
		return renderAdmin(c, "pages.html", "pages")
	})

	adminPages.GET("/pages/new", func(c echo.Context) error {
		return renderAdmin(c, "page_editor.html", "pages")
	})

	adminPages.GET("/pages/:id", func(c echo.Context) error {
		return renderAdmin(c, "page_editor.html", "pages")
	})

	adminPages.GET("/products", func(c echo.Context) error {
		return renderAdmin(c, "products.html", "products")
	})

	adminPages.GET("/products/new", func(c echo.Context) error {
		return renderAdmin(c, "product_editor.html", "products")
	})

	adminPages.GET("/products/:id", func(c echo.Context) error {
		return renderAdmin(c, "product_editor.html", "products")
	})

	adminPages.GET("/orders", func(c echo.Context) error {
		return renderAdmin(c, "orders.html", "orders")
	})

	adminPages.GET("/media", func(c echo.Context) error {
		return renderAdmin(c, "media.html", "media")
	})

	adminPages.GET("/themes", func(c echo.Context) error {
		return renderAdmin(c, "themes.html", "themes")
	})

	adminPages.GET("/plugins", func(c echo.Context) error {
		return renderAdmin(c, "plugins.html", "plugins")
	})

	adminPages.GET("/settings", func(c echo.Context) error {
		return renderAdmin(c, "settings.html", "settings")
	})

	e.GET("/login", func(c echo.Context) error {
		return c.File("web/admin/login.html")
	})

	// Public OAuth status endpoint (for login page)
	e.GET("/api/oauth/status", func(c echo.Context) error {
		result := make(map[string]bool)

		if data.DB != nil {
			var opt core.Option
			if err := data.DB.Where("key = ?", "oauth_google_enabled").First(&opt).Error; err == nil {
				result["google"] = opt.Value == "true"
			} else {
				result["google"] = false
			}
			if err := data.DB.Where("key = ?", "oauth_github_enabled").First(&opt).Error; err == nil {
				result["github"] = opt.Value == "true"
			} else {
				result["github"] = false
			}
		} else {
			result["google"] = false
			result["github"] = false
		}

		return c.JSON(http.StatusOK, result)
	})

	// Public Routes (Catch-all should be last)
	RegisterPublicRoutes(e, tm)
}
