package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/i18n"
	"github.com/labstack/echo/v4"
	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
)

// repoRoot returns the absolute project root, so locale files are loaded from
// the same place the server loads them from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve the test file location")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// setupI18n loads the locale files into the shared bundle and restores the
// previous bundle when the test finishes.
func setupI18n(t *testing.T) {
	t.Helper()
	t.Chdir(repoRoot(t))

	previous := i18n.Bundle
	t.Cleanup(func() { i18n.Bundle = previous })

	i18n.Init()
	if i18n.Bundle == nil {
		t.Fatal("i18n.Init() left the bundle nil")
	}
	for _, lang := range []string{"en", "zh-TW"} {
		if _, err := i18n.GetLocalizer(lang).Localize(&goi18n.LocalizeConfig{MessageID: "welcome"}); err != nil {
			t.Fatalf("locale %s was not loaded: %v", lang, err)
		}
	}
}

// isolateDB detaches the global database handle for the duration of the test
// and restores whatever was there before.
func isolateDB(t *testing.T) {
	t.Helper()
	previous := data.DB
	data.DB = nil
	t.Cleanup(func() { data.DB = previous })
}

// localeMessages reads the expected translations straight from the locale file
// so the assertions do not depend on the current wording.
func localeMessages(t *testing.T, file string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "web", "locales", file))
	if err != nil {
		t.Fatalf("read locale %s: %v", file, err)
	}
	messages := map[string]string{}
	if err := json.Unmarshal(raw, &messages); err != nil {
		t.Fatalf("parse locale %s: %v", file, err)
	}
	return messages
}

// i18nCall records what the next handler observed through the context.
type i18nCall struct {
	called    bool
	lang      string
	localizer *goi18n.Localizer
}

// runI18nMiddleware sends a single request through I18nMiddleware. A panic
// inside the middleware is captured so one broken case does not abort the
// whole test binary.
func runI18nMiddleware(t *testing.T, target, query, cookieLang string) (call i18nCall, panicked any) {
	t.Helper()

	e := echo.New()
	e.Use(I18nMiddleware)
	e.GET(target, func(c echo.Context) error {
		call.called = true
		call.lang, _ = c.Get("lang").(string)
		call.localizer, _ = c.Get("localizer").(*goi18n.Localizer)
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, target, nil)
	if query != "" {
		req.URL.RawQuery = query
	}
	if cookieLang != "" {
		req.AddCookie(&http.Cookie{Name: "lang", Value: cookieLang})
	}
	rec := httptest.NewRecorder()

	defer func() { panicked = recover() }()
	e.ServeHTTP(rec, req)
	return call, nil
}

// TestI18nMiddlewareNilDB covers the not-installed state: the database handle
// is nil, the next handler still has to run, and the context has to carry the
// zh-TW default.
func TestI18nMiddlewareNilDB(t *testing.T) {
	setupI18n(t)
	isolateDB(t)

	call, panicked := runI18nMiddleware(t, "/install", "", "")
	if panicked != nil {
		t.Fatalf("I18nMiddleware panicked with data.DB = nil: %v", panicked)
	}
	if !call.called {
		t.Fatal("the next handler was not executed with data.DB = nil")
	}
	if call.lang != "zh-TW" {
		t.Fatalf("expected the default context language zh-TW, got %q", call.lang)
	}
	if call.localizer == nil {
		t.Fatal("the localizer was not set in the context")
	}

	expected := localeMessages(t, "zh-TW.json")["welcome"]
	message, err := call.localizer.Localize(&goi18n.LocalizeConfig{MessageID: "welcome"})
	if err != nil {
		t.Fatalf("localize welcome with the default language: %v", err)
	}
	if message != expected {
		t.Fatalf("expected the zh-TW default text %q, got %q", expected, message)
	}
}

// TestI18nMiddlewareSupportedLanguages checks that the supported languages are
// selectable and that each one renders its own translation.
func TestI18nMiddlewareSupportedLanguages(t *testing.T) {
	setupI18n(t)
	isolateDB(t)

	english := localeMessages(t, "en.json")
	traditional := localeMessages(t, "zh-TW.json")

	cases := []struct {
		lang     string
		expected string
	}{
		{lang: "en", expected: english["welcome"]},
		{lang: "zh-TW", expected: traditional["welcome"]},
	}

	for _, tc := range cases {
		t.Run(tc.lang, func(t *testing.T) {
			call, panicked := runI18nMiddleware(t, "/install", "lang="+url.QueryEscape(tc.lang), "")
			if panicked != nil {
				t.Fatalf("I18nMiddleware panicked for lang=%s: %v", tc.lang, panicked)
			}
			if !call.called {
				t.Fatalf("the next handler was not executed for lang=%s", tc.lang)
			}
			if call.lang != tc.lang {
				t.Fatalf("expected the context language %q, got %q", tc.lang, call.lang)
			}
			if call.localizer == nil {
				t.Fatalf("the localizer was not set in the context for lang=%s", tc.lang)
			}

			message, err := call.localizer.Localize(&goi18n.LocalizeConfig{MessageID: "welcome"})
			if err != nil {
				t.Fatalf("localize welcome for lang=%s: %v", tc.lang, err)
			}
			if message != tc.expected {
				t.Fatalf("expected %q for lang=%s, got %q", tc.expected, tc.lang, message)
			}
		})
	}
}

// TestI18nMiddlewareUnknownLanguage checks that an unsupported language neither
// crashes nor leaves the caller without a usable translation.
func TestI18nMiddlewareUnknownLanguage(t *testing.T) {
	setupI18n(t)
	isolateDB(t)

	english := localeMessages(t, "en.json")["welcome"]
	traditional := localeMessages(t, "zh-TW.json")["welcome"]

	for _, unknown := range []string{"xx-YY", "not-a-language"} {
		t.Run(unknown, func(t *testing.T) {
			call, panicked := runI18nMiddleware(t, "/install", "lang="+url.QueryEscape(unknown), "")
			if panicked != nil {
				t.Fatalf("I18nMiddleware panicked for the unknown lang=%s: %v", unknown, panicked)
			}
			if !call.called {
				t.Fatalf("the next handler was not executed for the unknown lang=%s", unknown)
			}
			if call.localizer == nil {
				t.Fatalf("the localizer was not set in the context for the unknown lang=%s", unknown)
			}

			message, err := call.localizer.Localize(&goi18n.LocalizeConfig{MessageID: "welcome"})
			if err != nil {
				t.Fatalf("localize welcome for the unknown lang=%s: %v", unknown, err)
			}
			if message != english && message != traditional {
				t.Fatalf("expected a fallback to a supported locale for lang=%s, got %q", unknown, message)
			}
		})
	}
}

// TestI18nMiddlewareAdminCookie checks the admin cookie preference, the only
// language source that does not need the database.
func TestI18nMiddlewareAdminCookie(t *testing.T) {
	setupI18n(t)
	isolateDB(t)

	english := localeMessages(t, "en.json")["welcome"]

	call, panicked := runI18nMiddleware(t, "/admin", "", "en")
	if panicked != nil {
		t.Fatalf("I18nMiddleware panicked with the admin lang cookie: %v", panicked)
	}
	if !call.called {
		t.Fatal("the next handler was not executed with the admin lang cookie")
	}
	if call.lang != "en" {
		t.Fatalf("expected the context language %q from the cookie, got %q", "en", call.lang)
	}
	if call.localizer == nil {
		t.Fatal("the localizer was not set in the context")
	}

	message, err := call.localizer.Localize(&goi18n.LocalizeConfig{MessageID: "welcome"})
	if err != nil {
		t.Fatalf("localize welcome for the admin cookie: %v", err)
	}
	if message != english {
		t.Fatalf("expected %q for the admin cookie, got %q", english, message)
	}
}
