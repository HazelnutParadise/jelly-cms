package server

import (
	"net/http"
	"os"
	"text/template"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/install"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

// RegisterRoutes sets up the server routes.
func RegisterRoutes(e *echo.Echo, tm *theme.Manager) {
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

	// Admin Routes
	RegisterAdminRoutes(e, tm)
	RegisterEcommerceRoutes(e)

	e.GET("/admin", func(c echo.Context) error {
		// TODO: Check authentication
		return c.File("web/admin/index.html")
	})

	e.GET("/admin/posts", func(c echo.Context) error {
		return c.File("web/admin/posts.html")
	})

	e.GET("/admin/posts/new", func(c echo.Context) error {
		return c.File("web/admin/post_editor.html")
	})

	e.GET("/admin/posts/:id", func(c echo.Context) error {
		return c.File("web/admin/post_editor.html")
	})

	e.GET("/admin/pages", func(c echo.Context) error {
		return c.File("web/admin/pages.html")
	})

	e.GET("/admin/pages/new", func(c echo.Context) error {
		return c.File("web/admin/page_editor.html")
	})

	e.GET("/admin/pages/:id", func(c echo.Context) error {
		return c.File("web/admin/page_editor.html")
	})

	e.GET("/admin/products", func(c echo.Context) error {
		return c.File("web/admin/products.html")
	})

	e.GET("/admin/products/new", func(c echo.Context) error {
		return c.File("web/admin/product_editor.html")
	})

	e.GET("/admin/products/:id", func(c echo.Context) error {
		return c.File("web/admin/product_editor.html")
	})

	e.GET("/admin/orders", func(c echo.Context) error {
		return c.File("web/admin/orders.html")
	})

	e.GET("/admin/media", func(c echo.Context) error {
		return c.File("web/admin/media.html")
	})

	e.GET("/admin/themes", func(c echo.Context) error {
		return c.File("web/admin/themes.html")
	})

	e.GET("/admin/plugins", func(c echo.Context) error {
		return c.File("web/admin/plugins.html")
	})

	e.GET("/admin/settings", func(c echo.Context) error {
		return c.File("web/admin/settings.html")
	})

	e.GET("/login", func(c echo.Context) error {
		return c.File("web/admin/login.html")
	})

	// Public Routes (Catch-all should be last)
	RegisterPublicRoutes(e, tm)
}
