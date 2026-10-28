package server

import (
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	appI18n "github.com/HazelnutParadise/jelly-cms/internal/i18n"
	"github.com/labstack/echo/v4"
	"github.com/nicksnyder/go-i18n/v2/i18n"
)

var accountPageMessageIDs = []string{
	"accountTitle", "accountDescription", "loginMethods", "localLogin", "available", "unavailable",
	"enabled", "disabled", "linked", "notLinked", "linkProvider", "unlinkProvider", "unlinkPrompt",
	"confirmUnlink", "cancel", "loading", "loadError", "retry", "noProvidersLinked", "lastLoginMethod",
	"reauthenticate", "offline", "requestTimeout", "requestFailed", "linking", "removing", "recoverTitle",
	"recoverDescription", "recoveryGrant", "continue", "recoveryError", "recovering", "backToAdmin",
	"backToLogin", "switchToEnglish", "switchToChinese",
}

func registerAccountPages(e *echo.Echo) {
	e.GET("/account", noStoreAccountPage(auth.RequireAuth(func(c echo.Context) error {
		user, err := auth.GetCurrentUser(c)
		if err != nil || user == nil {
			return echo.NewHTTPError(http.StatusUnauthorized)
		}
		lang := ""
		if values := c.QueryParams()["lang"]; len(values) == 1 && supportedLanguage(values[0]) {
			lang = values[0]
		}
		switch user.Role {
		case "admin", "editor", "author":
			target := "/admin/account?tab=identities"
			if lang != "" {
				target += "&lang=" + url.QueryEscape(lang)
			}
			return c.Redirect(http.StatusFound, target)
		default:
			target := "/account/orders"
			if lang != "" {
				target += "?lang=" + url.QueryEscape(lang)
			}
			return c.Redirect(http.StatusFound, target)
		}
	})))
	e.GET("/account/recover", noStoreAccountPage(func(c echo.Context) error {
		return renderAccountPage(c, "account_recover.html")
	}))
}

func staffPageGate(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		user, ok := auth.GetUserFromContext(c)
		if !ok || user == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
		}
		switch user.Role {
		case "admin", "editor", "author":
			return next(c)
		default:
			return c.Redirect(http.StatusFound, "/")
		}
	}
}

func enforceStaffAdminPages(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		path := c.Request().URL.Path
		if path != "/admin" && !strings.HasPrefix(path, "/admin/") || strings.HasPrefix(path, "/admin/assets/") {
			return next(c)
		}
		user, err := auth.GetCurrentUser(c)
		if err != nil {
			returnURL := c.Request().URL.RequestURI()
			return c.Redirect(http.StatusFound, "/login?redirect="+url.QueryEscape(returnURL))
		}
		switch user.Role {
		case "admin", "editor", "author":
			c.Set("user", user)
			return next(c)
		default:
			return c.Redirect(http.StatusFound, "/")
		}
	}
}

func noStoreAccountPage(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return next(c)
	}
}

func renderAccountPage(c echo.Context, page string) error {
	lang := "en"
	if requested, ok := c.Get("lang").(string); ok && strings.HasPrefix(strings.ToLower(requested), "zh") {
		lang = "zh-TW"
	}
	localizer := appI18n.GetLocalizer(lang)
	translations := make(map[string]string, len(accountPageMessageIDs))
	for _, key := range accountPageMessageIDs {
		message, err := localizer.Localize(&i18n.LocalizeConfig{MessageID: key})
		if err != nil {
			translations[key] = key
			continue
		}
		translations[key] = message
	}

	switchURL, switchLabel := "/account?lang=en", translations["switchToEnglish"]
	if lang == "en" {
		switchURL, switchLabel = "/account?lang=zh-TW", translations["switchToChinese"]
	}
	backURL, backLabel := "/admin?lang="+lang, translations["backToAdmin"]
	if page == "account_recover.html" {
		backURL, backLabel = "/login?lang="+lang, translations["backToLogin"]
		switchURL = "/account/recover?lang=en"
		if lang == "en" {
			switchURL = "/account/recover?lang=zh-TW"
		}
	}

	tmpl, err := template.New("account_layout.html").Funcs(adminFuncsFor(c)).ParseFiles("web/admin/account_layout.html", "web/admin/"+page)
	if err != nil {
		return err
	}
	data := map[string]interface{}{
		"Lang":        lang,
		"T":           translations,
		"PageTitle":   translations["accountTitle"],
		"SwitchURL":   switchURL,
		"SwitchLabel": switchLabel,
		"BackURL":     backURL,
		"BackLabel":   backLabel,
	}
	if page == "account_recover.html" {
		data["PageTitle"] = translations["recoverTitle"]
	}
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
	return tmpl.ExecuteTemplate(c.Response().Writer, "account_layout", data)
}
