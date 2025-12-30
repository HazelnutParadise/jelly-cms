package server

import (
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/i18n"
	"github.com/labstack/echo/v4"
)

func I18nMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		lang := ""
		
		// For admin routes, check cookie first (user preference)
		if c.Path() != "" && len(c.Path()) >= 6 && c.Path()[:6] == "/admin" {
			cookie, err := c.Cookie("lang")
			if err == nil {
				lang = cookie.Value
			}
		}

		// Check DB setting (site default language)
		if lang == "" {
			var opt core.Option
			if err := data.DB.Where("key = ?", "site_language").First(&opt).Error; err == nil {
				lang = opt.Value
			}
		}

		// Check query param ?lang= (override)
		if queryLang := c.QueryParam("lang"); queryLang != "" {
			lang = queryLang
		}

		// Default
		if lang == "" {
			lang = "zh-TW"
		}

		// Set localizer in context using the internal manager
		localizer := i18n.GetLocalizer(lang)
		c.Set("localizer", localizer)
		c.Set("lang", lang)

		return next(c)
	}
}
