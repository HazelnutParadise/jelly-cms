package server

import (
	"log"
	"net/http"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/assets"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

func requestAssetURL(c echo.Context) func(string) string {
	snapshot, _ := c.Get("assetSnapshot").(interface{ URL(string) string })
	return func(raw string) string {
		if snapshot == nil {
			return raw
		}
		return snapshot.URL(raw)
	}
}

func requestAssetSnapshot(c echo.Context) *assets.Snapshot {
	snapshot, _ := c.Get("assetSnapshot").(*assets.Snapshot)
	return snapshot
}

func registerAssetHashRoutes(e *echo.Echo, tm *theme.Manager) {
	favicon := func(c echo.Context) error {
		snapshot := requestAssetSnapshot(c)
		if snapshot == nil {
			return echo.ErrNotFound
		}
		url := snapshot.URL("/admin/assets/jelly-mark.svg")
		if url == "/admin/assets/jelly-mark.svg" || !strings.HasPrefix(url, "/admin/assets/jelly-mark.svg?v=") {
			return echo.ErrNotFound
		}
		c.Response().Header().Set(echo.HeaderCacheControl, "no-cache")
		return c.Redirect(http.StatusTemporaryRedirect, url)
	}
	e.GET("/favicon.ico", favicon)
	e.HEAD("/favicon.ico", favicon)

	handler := func(c echo.Context) error {
		snapshot := requestAssetSnapshot(c)
		if snapshot == nil {
			if tm != nil && tm.AssetError() != nil {
				log.Printf("asset snapshot unavailable: %v", tm.AssetError())
			}
			return echo.ErrNotFound
		}
		snapshot.ServeHTTP(c.Response(), c.Request())
		return nil
	}
	for _, route := range []string{"/admin/assets/*", "/vendor/katex/*"} {
		e.GET(route, handler)
		e.HEAD(route, handler)
	}
}

func htmlRevalidation(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Before(func() {
			h := c.Response().Header()
			if strings.Contains(strings.ToLower(h.Get(echo.HeaderContentType)), "text/html") && !hasNoStore(h.Get(echo.HeaderCacheControl)) {
				h.Set(echo.HeaderCacheControl, "no-cache")
			}
		})
		return next(c)
	}
}

func hasNoStore(value string) bool {
	for _, directive := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(directive), "no-store") {
			return true
		}
	}
	return false
}
