package server

import (
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

func shopThemeAssetHandler(tm *theme.Manager) echo.HandlerFunc {
	return func(c echo.Context) error {
		snapshot := requestAssetSnapshot(c)
		if snapshot == nil {
			return echo.ErrNotFound
		}
		snapshot.ServeHTTP(c.Response(), c.Request())
		return nil
	}
}
