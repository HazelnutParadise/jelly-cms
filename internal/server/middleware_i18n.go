package server

import (
	"net/http"
	"os"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/i18n"
	"github.com/labstack/echo/v4"
)

func I18nMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		lang := ""
		path := c.Path()
		usesLanguagePreference := isLanguagePreferencePath(path)
		accountLanguagePage := path == "/account" || path == "/account/recover"

		// Read the shared preference only on pages that expose or use it.
		if usesLanguagePreference {
			cookie, err := c.Cookie("lang")
			if err == nil && supportedLanguage(cookie.Value) {
				lang = cookie.Value
			}
		}

		// Check DB setting (site default language)
		if lang == "" && data.DB != nil {
			var opt core.Option
			if err := data.DB.Where("key = ?", "site_language").First(&opt).Error; err == nil {
				lang = opt.Value
			}
		}

		// On preference-aware pages, only an unambiguous supported language may
		// override the cookie. Explicit choices on the account pages are saved.
		if usesLanguagePreference {
			queryLanguages := c.QueryParams()["lang"]
			if len(queryLanguages) == 1 && supportedLanguage(queryLanguages[0]) {
				lang = queryLanguages[0]
				if accountLanguagePage && c.Request().Method == http.MethodGet {
					c.SetCookie(&http.Cookie{
						Name:     "lang",
						Value:    lang,
						Path:     "/",
						Secure:   os.Getenv("APP_ENV") == "production",
						SameSite: http.SameSiteLaxMode,
					})
				}
			}
		} else if queryLang := c.QueryParam("lang"); queryLang != "" {
			// Preserve the existing query override for other pages without reading
			// or writing the account language preference cookie there.
			lang = queryLang
		}

		// Default
		if lang == "" {
			lang = "zh-TW"
		}
		if usesLanguagePreference && !supportedLanguage(lang) {
			if strings.HasPrefix(strings.ToLower(lang), "zh") {
				lang = "zh-TW"
			} else {
				lang = "en"
			}
		}

		// Set localizer in context using the internal manager
		localizer := i18n.GetLocalizer(lang)
		c.Set("localizer", localizer)
		c.Set("lang", lang)

		return next(c)
	}
}

func isLanguagePreferencePath(path string) bool {
	return path == "/account" || path == "/account/recover" || path == "/login" ||
		path == "/admin" || strings.HasPrefix(path, "/admin/")
}

func supportedLanguage(lang string) bool {
	return lang == "en" || lang == "zh-TW"
}
