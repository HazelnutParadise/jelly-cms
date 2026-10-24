package server

import (
	"html/template"
	"net/http"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	appI18n "github.com/HazelnutParadise/jelly-cms/internal/i18n"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
	"github.com/nicksnyder/go-i18n/v2/i18n"
)

// RegisterRoutes sets up the server routes.
func RegisterRoutes(e *echo.Echo, tm *theme.Manager, pluginRuntime *plugin.Runtime) {
	gate := newInstallGate()
	e.Use(gate.middleware)
	e.Use(installStateMiddleware)
	e.Use(I18nMiddleware)
	e.Use(maintenanceMiddleware(tm))
	e.Static("/uploads", "data/uploads")
	e.Static("/admin/assets", "web/admin/assets")

	// Install Routes
	registerInstallRoutes(e, tm, pluginRuntime)

	// Auth Routes
	auth.RegisterRoutes(e)

	// Payment Routes
	RegisterPaymentRoutes(e)

	// Admin Routes
	registerAdminRoutes(e, tm, pluginRuntime)
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
		tmpl, err := template.ParseFiles("web/admin/login.html")
		if err != nil {
			return err
		}
		c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
		lang := "en"
		if requested, ok := c.Get("lang").(string); ok && strings.HasPrefix(strings.ToLower(requested), "zh") {
			lang = "zh-TW"
		}
		recoverTitle, err := appI18n.GetLocalizer(lang).Localize(&i18n.LocalizeConfig{MessageID: "recoverTitle"})
		if err != nil {
			recoverTitle = "Recover OAuth account"
			if lang == "zh-TW" {
				recoverTitle = "復原第三方帳號"
			}
		}
		return tmpl.Execute(c.Response().Writer, map[string]string{
			"RedirectURL":  auth.SafeRedirectURL(c.QueryParam("redirect")),
			"Lang":         lang,
			"RecoverTitle": recoverTitle,
		})
	})

	// Public OAuth status endpoint (for login page)
	e.GET("/api/oauth/status", func(c echo.Context) error {
		result := map[string]bool{"google": false, "github": false}
		if data.DB != nil {
			keys := []string{"oauth_google_enabled", "oauth_github_enabled"}
			var options []core.Option
			if err := data.DB.WithContext(c.Request().Context()).Where("key IN ?", keys).Find(&options).Error; err == nil {
				for _, option := range options {
					switch option.Key {
					case "oauth_google_enabled":
						result["google"] = option.Value == "true"
					case "oauth_github_enabled":
						result["github"] = option.Value == "true"
					}
				}
			}
		}

		return c.JSON(http.StatusOK, result)
	})

	// Content organisation: taxonomy, menus, comments, trash, revisions, archives
	registerContentRoutes(e, tm, renderAdmin)

	// WebMCP script, tool list and the public JSON API behind the tools (ticket 13).
	registerWebMCPRoutes(e, tm, pluginRuntime)
	registerPublicAPIRoutes(e)

	// Public Routes (Catch-all should be last)
	registerAccountPages(e)
	RegisterPublicRoutes(e, tm)
}
