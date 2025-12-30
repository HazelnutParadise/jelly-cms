package server

import (
	"net/http"
	"os"
	"text/template"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/install"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
	"github.com/nicksnyder/go-i18n/v2/i18n"
)

// RegisterRoutes sets up the server routes.
func RegisterRoutes(e *echo.Echo, tm *theme.Manager, pluginRuntime *plugin.Runtime) {
	// Installation Middleware
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Allow static files and install routes
			if c.Path() == "/install" || c.Path() == "/api/install" || c.Path() == "/api/install/test-db" {
				return next(c)
			}

			if !install.IsInstalled() {
				return c.Redirect(http.StatusFound, "/install")
			}
			return next(c)
		}
	})

	e.Use(I18nMiddleware)
	e.Static("/uploads", "data/uploads")

	// Install Routes
	e.GET("/install", func(c echo.Context) error {
		if install.IsInstalled() {
			return c.Redirect(http.StatusFound, "/")
		}

		// Auto-fill from Env
		data := map[string]string{
			"DBHost":     os.Getenv("DB_HOST"),
			"DBPort":     os.Getenv("DB_PORT"),
			"DBUser":     os.Getenv("DB_USER"),
			"DBPass":     os.Getenv("DB_PASSWORD"),
			"DBName":     os.Getenv("DB_NAME"),
			"DBTimezone": os.Getenv("DB_TIMEZONE"),
		}
		if data["DBTimezone"] == "" {
			data["DBTimezone"] = "Asia/Taipei" // Default suggestion
		}

		// Render install.html with data
		tmpl, err := template.ParseFiles("web/admin/install.html")
		if err != nil {
			return err
		}

		return tmpl.Execute(c.Response().Writer, data)
	})

	e.POST("/api/install/test-db", func(c echo.Context) error {
		var req install.InstallRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		}

		if err := install.TestConnection(req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "Connection successful"})
	})

	e.POST("/api/install", func(c echo.Context) error {
		var req install.InstallRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		}

		if err := install.PerformInstallation(req); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "Installation successful"})
	})

	// Auth Routes
	auth.RegisterRoutes(e)

	// Payment Routes
	RegisterPaymentRoutes(e)

	// Admin Routes
	RegisterAdminRoutes(e, tm)
	RegisterEcommerceRoutes(e)

	// Helper to render admin templates
	renderAdmin := func(c echo.Context, file string, active string) error {
		// Get localizer from context
		localizer, _ := c.Get("localizer").(*i18n.Localizer)
		lang := c.Get("lang").(string)
		
		// Load translations for this language
		translations := make(map[string]string)
		translationKeys := []string{
			"dashboard", "posts", "pages", "products", "orders", "media", "themes", "plugins", "settings",
			"logout", "search", "searchPlaceholder", "viewSite", "welcomeBack",
			"totalPosts", "totalProducts", "totalOrders", "activeTheme",
			"totalPostsDesc", "totalProductsDesc", "totalOrdersDesc", "activeThemeDesc",
			"quickActions", "createPost", "createPage", "createProduct", "mediaLibrary",
			"recentPosts", "viewAll", "title", "status", "date", "actions", "edit", "delete",
			"loading", "noPosts", "published", "draft", "pending",
			"noPlugins", "noPluginsDesc", "uploadPlugin", "uploadPluginTitle",
			"pluginZipFile", "pluginZipDesc", "upload", "cancel", "uploading",
			"uploadSuccess", "uploadError", "reload", "reloadConfirm",
			"pluginReloaded", "reloadFailed",
			"content", "shop", "appearance", "system", "administrator",
		}
		
		for _, key := range translationKeys {
			if localizer != nil {
				result, err := localizer.Localize(&i18n.LocalizeConfig{
					MessageID: key,
				})
				if err == nil {
					translations[key] = result
				} else {
					translations[key] = key
				}
			} else {
				translations[key] = key
			}
		}

		tmpl, err := template.New("base").Funcs(template.FuncMap{
			"add": func(a, b int) int { return a + b },
			"sub": func(a, b int) int { return a - b },
			"dict": func(values ...interface{}) map[string]interface{} {
				dict := make(map[string]interface{})
				for i := 0; i < len(values); i += 2 {
					if i+1 < len(values) {
						key := values[i].(string)
						dict[key] = values[i+1]
					}
				}
				return dict
			},
			"list": func(items ...interface{}) []interface{} {
				return items
			},
		}).ParseFiles("web/admin/components.html", "web/admin/layout.html", "web/admin/"+file)
		if err != nil {
			return err
		}

		data := map[string]interface{}{
			"Active": active,
			"Lang":   lang,
			"T":      translations,
		}
		
		return tmpl.ExecuteTemplate(c.Response().Writer, "base", data)
	}

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
