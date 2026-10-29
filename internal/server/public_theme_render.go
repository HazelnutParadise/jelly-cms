package server

import (
	"html/template"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

func publicMenuFuncs(c echo.Context, db *gorm.DB) (template.FuncMap, bool) {
	user, err := auth.GetCurrentUser(c)
	signedIn := err == nil && user != nil
	path := c.Request().URL.Path
	return template.FuncMap{"menu": func(location string) interface{} {
		return menuForLocationForRequest(db, location, signedIn, path)
	}}, signedIn
}

func renderPublicTheme(c echo.Context, tm *theme.Manager, db *gorm.DB, name string, values interface{}) ([]byte, error) {
	return renderPublicThemeWithLayout(c, tm, db, name, values, true)
}

func renderPublicThemePage(c echo.Context, tm *theme.Manager, db *gorm.DB, name string, values interface{}) ([]byte, error) {
	return renderPublicThemeWithLayout(c, tm, db, name, values, false)
}

func renderPublicThemeWithLayout(c echo.Context, tm *theme.Manager, db *gorm.DB, name string, values interface{}, layout bool) ([]byte, error) {
	funcs, signedIn := publicMenuFuncs(c, db)
	var renderDB interface {
		First(dest interface{}, conds ...interface{}) *gorm.DB
	}
	if db != nil {
		renderDB = db
	}
	var body []byte
	var err error
	if layout {
		body, err = tm.RenderWithDBAndFuncs(name, values, renderDB, funcs)
	} else {
		body, err = tm.RenderPageWithDBAndFuncs(name, values, renderDB, funcs)
	}
	if err == nil && signedIn {
		cacheControl := c.Response().Header().Get("Cache-Control")
		if !hasNoStore(cacheControl) {
			c.Response().Header().Set("Cache-Control", "private, no-store")
		}
	}
	return body, err
}
