package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

func shopThemeAssetHandler(tm *theme.Manager) echo.HandlerFunc {
	return func(c echo.Context) error {
		assetsDir, err := tm.ActiveAssetsDir()
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound)
		}

		// Echo's wildcard value may include the leading slash. Treat the request
		// as a relative path and reject empty, absolute, and parent paths.
		requestPath := strings.TrimPrefix(c.Param("*"), "/")
		if requestPath == "" || !filepath.IsLocal(filepath.FromSlash(requestPath)) {
			return echo.NewHTTPError(http.StatusNotFound)
		}
		cleanPath := filepath.Clean(filepath.FromSlash(requestPath))
		if cleanPath == "." || cleanPath == ".." || strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) {
			return echo.NewHTTPError(http.StatusNotFound)
		}

		candidate := filepath.Join(assetsDir, cleanPath)
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil || !shopPathWithin(assetsDir, resolved) {
			return echo.NewHTTPError(http.StatusNotFound)
		}
		file, err := os.Open(resolved)
		if err != nil {
			if os.IsNotExist(err) {
				return echo.NewHTTPError(http.StatusNotFound)
			}
			return echo.NewHTTPError(http.StatusInternalServerError)
		}
		defer file.Close()

		info, err := file.Stat()
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError)
		}
		if !info.Mode().IsRegular() {
			return echo.NewHTTPError(http.StatusNotFound)
		}

		// The path points at the active theme, so revalidate on each request
		// after theme activation instead of caching a previous theme's assets.
		c.Response().Header().Set(echo.HeaderCacheControl, "no-cache")
		http.ServeContent(c.Response().Writer, c.Request(), filepath.Base(resolved), info.ModTime(), file)
		return nil
	}
}

func shopPathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
