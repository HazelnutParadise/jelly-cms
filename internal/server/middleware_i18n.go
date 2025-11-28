package server

import (
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/i18n"
	"github.com/labstack/echo/v4"
)

func I18nMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		// 1. Check query param ?lang=
		lang := c.QueryParam("lang")

		// 2. Check cookie
		if lang == "" {
			cookie, err := c.Cookie("lang")
			if err == nil {
				lang = cookie.Value
			}
		}

		// 3. Check DB setting
		if lang == "" {
			var opt core.Option
			if err := data.DB.Where("key = ?", "site_language").First(&opt).Error; err == nil {
				lang = opt.Value
			}
		}

		// 4. Default
		if lang == "" {
			lang = "en"
		}

		// Set localizer in context using the internal manager
		localizer := i18n.GetLocalizer(lang)
		c.Set("localizer", localizer)
		c.Set("lang", lang)

		return next(c)
	}
}
