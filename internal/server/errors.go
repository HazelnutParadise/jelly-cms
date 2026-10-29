package server

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

// ErrorPage is what error templates receive.
type ErrorPage struct {
	Code    int
	Title   string
	Message string
	// RequestID helps a reader quote the failure in a bug report.
	RequestID string
	Path      string
}

// errorTitles are translation keys per status code; the fallback is the HTTP
// status text.
var errorTitles = map[int]string{
	http.StatusNotFound:            "errorNotFound",
	http.StatusForbidden:           "errorForbidden",
	http.StatusUnauthorized:        "errorUnauthorized",
	http.StatusInternalServerError: "errorInternal",
	http.StatusServiceUnavailable:  "errorMaintenance",
}

// newHTTPErrorHandler renders JSON for API callers, the admin error screen for
// /admin paths and the active theme's error.html elsewhere. It never leaks
// internal error text for 5xx responses.
func newHTTPErrorHandler(tm *theme.Manager) echo.HTTPErrorHandler {
	return func(err error, c echo.Context) {
		if c.Response().Committed {
			return
		}
		code := http.StatusInternalServerError
		message := ""
		var he *echo.HTTPError
		if errors.As(err, &he) {
			code = he.Code
			if m, ok := he.Message.(string); ok {
				message = m
			}
		}
		if code >= 500 {
			log.Printf("http %d %s %s: %v", code, c.Request().Method, c.Request().URL.Path, err)
			message = ""
		}
		if message == "" {
			message = http.StatusText(code)
		}

		path := c.Request().URL.Path
		if pathOrSubtree(path, "/api/agent/v1") {
			codeName := "internal_error"
			switch code {
			case http.StatusNotFound:
				codeName = "not_found"
			case http.StatusMethodNotAllowed:
				codeName = "method_not_allowed"
			case http.StatusRequestEntityTooLarge:
				codeName = "payload_too_large"
			case http.StatusServiceUnavailable:
				codeName = "service_unavailable"
			case http.StatusBadRequest:
				codeName = "bad_request"
			case http.StatusForbidden:
				codeName = "forbidden"
			case http.StatusUnauthorized:
				codeName = "key_invalid"
			case http.StatusConflict:
				codeName = "conflict"
			}
			_ = writeAPIError(c, code, codeName, message, nil)
			return
		}
		if wantsJSON(c) {
			_ = c.JSON(code, map[string]interface{}{"error": message, "code": code})
			return
		}
		requestID := c.Response().Header().Get(echo.HeaderXRequestID)
		page := ErrorPage{Code: code, Title: message, Message: message, RequestID: requestID, Path: path}

		if strings.HasPrefix(path, "/admin") {
			c.Response().WriteHeader(code)
			if rerr := renderAdminPage(c, AdminPage{File: "error.html", Active: "", Data: map[string]interface{}{"Error": page}}); rerr != nil {
				log.Printf("render admin error page: %v", rerr)
				_ = c.String(code, message)
			}
			return
		}

		if tm != nil {
			lang, _ := c.Get("lang").(string)
			t := loadTranslations(lang)
			if key, ok := errorTitles[code]; ok {
				page.Title = translate(t, key)
			}
			if code == http.StatusNotFound {
				page.Message = translate(t, "errorNotFoundBody")
			}
			values := map[string]interface{}{
				"SiteTitle":   siteTitleOrDefault(),
				"Title":       page.Title,
				"CurrentPath": path,
				"Error":       page,
			}
			html, rerr := renderPublicTheme(c, tm, data.DB, "error.html", values)
			if rerr == nil {
				_ = renderedPublicHTML(c, code, html)
				return
			}
			log.Printf("render theme error page: %v", rerr)
		}
		_ = c.String(code, message)
	}
}

// wantsJSON is true for API paths and for clients that ask for JSON first.
func wantsJSON(c echo.Context) bool {
	path := c.Request().URL.Path
	if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/auth/") {
		return true
	}
	accept := c.Request().Header.Get(echo.HeaderAccept)
	return strings.Contains(accept, echo.MIMEApplicationJSON) && !strings.Contains(accept, echo.MIMETextHTML)
}
