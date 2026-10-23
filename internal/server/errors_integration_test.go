package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

func TestErrorHandlerAPINotFoundReturnsJSON(t *testing.T) {
	h := newAccessHarness(t)
	result := h.request(http.MethodGet, "/api/does-not-exist", "", "", "", "")
	h.mustNoPanic(result, "GET /api/does-not-exist")
	if result.code != http.StatusNotFound {
		t.Fatalf("GET /api/does-not-exist returned %d, want 404; body=%q", result.code, result.body)
	}
	if ct := result.header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(result.body), &body); err != nil {
		t.Fatalf("404 body is not JSON: %v; body=%q", err, result.body)
	}
	if code, _ := body["code"].(float64); code != http.StatusNotFound {
		t.Errorf("404 body code = %v, want 404; body=%q", body["code"], result.body)
	}
}

func TestErrorHandlerAdminNotFoundRendersAdminErrorPage(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "err-admin")
	result := h.request(http.MethodGet, "/admin/does-not-exist", "", h.token(admin), "text/html", "")
	h.mustNoPanic(result, "GET /admin/does-not-exist")
	if result.code != http.StatusNotFound {
		t.Fatalf("GET /admin/does-not-exist returned %d, want 404; body=%q", result.code, result.body)
	}
	if !strings.Contains(result.body, `data-code="404"`) {
		t.Errorf("admin 404 body lacks data-code=\"404\" marker; body=%q", result.body)
	}
	if !strings.Contains(result.body, "<html") {
		t.Errorf("admin 404 body is not wrapped in the admin layout; body=%q", result.body)
	}
}

func TestErrorHandlerAnonymousFrontNotFoundUsesThemePage(t *testing.T) {
	h := newAccessHarness(t)
	// The harness builds its theme manager without activating a theme, but
	// cmd/server/main.go activates "default" at startup. Mirror that so the
	// theme's error.html is the page under test.
	tm := theme.NewManager("web/themes")
	if err := tm.Activate("default"); err != nil {
		t.Fatalf("activate default theme: %v", err)
	}
	h.echo.HTTPErrorHandler = newHTTPErrorHandler(tm)
	result := h.request(http.MethodGet, "/no-such-page-xyz", "", "", "", "")
	h.mustNoPanic(result, "GET /no-such-page-xyz")
	if result.code != http.StatusNotFound {
		t.Fatalf("GET /no-such-page-xyz returned %d, want 404; body=%q", result.code, result.body)
	}
	if ct := result.header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(result.body, `data-code="404"`) {
		t.Errorf("front-end 404 body lacks theme error marker data-code=\"404\"; body=%q", result.body)
	}
}

func TestErrorHandlerHidesInternalDetailOnServerErrors(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "err-admin-500")
	const leak = "secret-detail"
	h.echo.GET("/api/errors-test/boom", func(c echo.Context) error {
		return echo.NewHTTPError(http.StatusInternalServerError, leak)
	})
	h.echo.GET("/admin/errors-test/boom", func(c echo.Context) error {
		return echo.NewHTTPError(http.StatusInternalServerError, leak)
	}, auth.RequireAuth)

	t.Run("api path returns generic JSON", func(t *testing.T) {
		result := h.request(http.MethodGet, "/api/errors-test/boom", "", "", "", "")
		h.mustNoPanic(result, "GET /api/errors-test/boom")
		if result.code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", result.code)
		}
		if strings.Contains(result.body, leak) {
			t.Errorf("API 500 body leaks internal detail %q: %q", leak, result.body)
		}
		var body map[string]interface{}
		if err := json.Unmarshal([]byte(result.body), &body); err != nil {
			t.Fatalf("API 500 body is not JSON: %v; body=%q", err, result.body)
		}
		if body["error"] != http.StatusText(http.StatusInternalServerError) {
			t.Errorf("API 500 error = %v, want generic status text", body["error"])
		}
	})

	t.Run("admin path renders generic error page", func(t *testing.T) {
		result := h.request(http.MethodGet, "/admin/errors-test/boom", "", h.token(admin), "text/html", "")
		h.mustNoPanic(result, "GET /admin/errors-test/boom")
		if result.code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500; body=%q", result.code, result.body)
		}
		if strings.Contains(result.body, leak) {
			t.Errorf("admin 500 body leaks internal detail %q", leak)
		}
		if !strings.Contains(result.body, `data-code="500"`) {
			t.Errorf("admin 500 body is not the admin error page; body=%q", result.body)
		}
	})
}
