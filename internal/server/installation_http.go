package server

import (
	"context"
	"errors"
	"html/template"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/install"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

const installationUnavailableMessage = "Service temporarily unavailable"

// installGate serializes installation writes against every other request. It is
// owned by a single RegisterRoutes call so separate servers never share a lock,
// and it only guards publishing the installation globals over HTTP. The install
// service keeps its own locking for the installation itself.
type installGate struct {
	mu sync.RWMutex
}

func newInstallGate() *installGate {
	return &installGate{}
}

// middleware takes the write lock for installation requests so the whole
// handler and the following middleware run while no other request can observe
// a half-published installation. An already-installed site is rejected before
// the lock is requested, so a repeated install cannot queue behind slow readers
// and stall later requests. Everything else takes the read lock.
func (g *installGate) middleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if isInstallWrite(c.Request().Method, c.Request().URL.Path) {
			// A repeated installation must be rejected before asking for the
			// gate write lock. A slow non-install request can hold the read
			// lock for a long time, and the pending writer would then make
			// every later reader queue behind it. IsInstalled is protected by
			// the installation mutex and never reads the HTTP globals, so this
			// check is safe without the gate and does not touch other
			// middleware.
			if install.IsInstalled() {
				return c.JSON(http.StatusConflict, map[string]string{"error": install.ErrAlreadyInstalled.Error()})
			}

			g.mu.Lock()
			defer g.mu.Unlock()
			return next(c)
		}

		g.mu.RLock()
		defer g.mu.RUnlock()
		return next(c)
	}
}

func isInstallEndpoint(path string) bool {
	return path == "/install" || path == "/api/install" || path == "/api/install/test-db"
}

func isInstallWrite(method, path string) bool {
	if method != http.MethodPost {
		return false
	}
	return path == "/api/install" || path == "/api/install/test-db"
}

// installStateMiddleware keeps the installer closed once the site is installed
// and answers plainly while the installed site has no live database handle.
// Reading data.DB here is safe because installation publishes it under the
// gate's write lock.
func installStateMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if isInstallEndpoint(c.Request().URL.Path) {
			return next(c)
		}

		if !install.IsInstalled() {
			return c.Redirect(http.StatusFound, "/install")
		}

		if data.DB == nil {
			return c.String(http.StatusServiceUnavailable, installationUnavailableMessage)
		}

		return next(c)
	}
}

func registerInstallRoutes(e *echo.Echo, tm *theme.Manager, pluginRuntime *plugin.Runtime) {
	e.GET("/install", func(c echo.Context) error {
		if install.IsInstalled() {
			return c.Redirect(http.StatusFound, "/")
		}

		data := map[string]string{
			"DBHost":     os.Getenv("DB_HOST"),
			"DBPort":     os.Getenv("DB_PORT"),
			"DBUser":     os.Getenv("DB_USER"),
			"DBPass":     os.Getenv("DB_PASSWORD"),
			"DBName":     os.Getenv("DB_NAME"),
			"DBTimezone": os.Getenv("DB_TIMEZONE"),
		}
		if data["DBTimezone"] == "" {
			data["DBTimezone"] = "Asia/Taipei"
		}

		tmpl, err := template.ParseFiles("web/admin/install.html")
		if err != nil {
			return err
		}

		return tmpl.Execute(c.Response().Writer, data)
	})

	e.POST("/api/install/test-db", auth.ProtectBrowserWrites(func(c echo.Context) error {
		if install.IsInstalled() {
			return c.JSON(http.StatusConflict, map[string]string{"error": install.ErrAlreadyInstalled.Error()})
		}

		var req install.InstallRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		}

		if err := install.TestConnection(req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Database connection test failed"})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "Connection successful"})
	}))

	e.POST("/api/install", auth.ProtectBrowserWrites(func(c echo.Context) error {
		if install.IsInstalled() {
			return c.JSON(http.StatusConflict, map[string]string{"error": install.ErrAlreadyInstalled.Error()})
		}

		var req install.InstallRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		}

		if err := install.PerformInstallation(req); err != nil {
			var validationErr *install.ValidationError
			switch {
			case errors.As(err, &validationErr):
				return c.JSON(http.StatusBadRequest, map[string]string{"error": validationErr.Error()})
			case errors.Is(err, install.ErrAlreadyInstalled):
				return c.JSON(http.StatusConflict, map[string]string{"error": install.ErrAlreadyInstalled.Error()})
			default:
				log.Printf("installation failed: %v", err)
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Installation failed"})
			}
		}

		initializeAfterInstallation(pluginRuntime)
		return c.JSON(http.StatusOK, map[string]string{"message": "Installation successful"})
	}))
}

// initializeAfterInstallation publishes the services that depend on the new
// database. The database installation already committed at this point, so a
// failure here is logged as a service initialization problem instead of being
// reported as a rolled back installation.
func initializeAfterInstallation(pluginRuntime *plugin.Runtime) {
	db := data.DB
	if db == nil {
		log.Printf("installation completed without a live database handle; skipping service initialization")
		return
	}

	InitPaymentService(db)
	auth.ReloadOAuthProviders()

	if pluginRuntime == nil {
		return
	}

	if err := os.MkdirAll("data/plugins", 0755); err != nil {
		log.Printf("plugin directory setup failed after installation: %v", err)
	}
	if err := pluginRuntime.LoadAll(context.Background(), "data/plugins"); err != nil {
		log.Printf("plugin loading failed after installation: %v", err)
	}
}
