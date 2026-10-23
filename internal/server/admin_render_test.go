package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
)

// useRenderDirs points localesDir and adminDir at fresh temp directories and
// restores the originals and the translation cache when the test ends.
func useRenderDirs(t *testing.T) (localesPath, adminPath string) {
	t.Helper()
	root := t.TempDir()
	localesPath = filepath.Join(root, "locales")
	adminPath = filepath.Join(root, "admin")
	for _, dir := range []string{localesPath, adminPath} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	prevLocales, prevAdmin := localesDir, adminDir
	localesDir, adminDir = localesPath, adminPath
	resetTranslationCache()
	t.Cleanup(func() {
		localesDir, adminDir = prevLocales, prevAdmin
		resetTranslationCache()
	})
	return localesPath, adminPath
}

func writeRenderFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// writeAdminLayout writes the minimal components and layout templates that
// renderAdminPage parses before the page file.
func writeAdminLayout(t *testing.T, adminPath string) {
	t.Helper()
	writeRenderFile(t, filepath.Join(adminPath, "components.html"), `{{define "components"}}{{end}}`)
	writeRenderFile(t, filepath.Join(adminPath, "layout.html"),
		`{{define "base"}}{{range .Nav}}{{.Key}}:{{range .Items}}{{.Key}}{{if .Active}}*{{end}},{{end}};{{end}}|{{.Viewer.Initial}}|{{.Title}}|{{index .T "posts"}}|{{template "content" .}}{{end}}`)
}

func adminRenderContext(t *testing.T) (echo.Context, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/posts", nil)
	c := echo.New().NewContext(req, rec)
	c.Set("lang", "zh-TW")
	c.Set("user", &core.User{Username: "hazel", Role: "admin"})
	return c, rec
}

func TestLoadTranslationsReadsWholeFileWithFallback(t *testing.T) {
	locales, _ := useRenderDirs(t)
	writeRenderFile(t, filepath.Join(locales, "en.json"), `{"posts":"Posts","pages":"Pages","dashboard":"Dashboard"}`)
	writeRenderFile(t, filepath.Join(locales, "zh-TW.json"), `{"posts":"暫存文章"}`)

	t.Run("reads every key of the requested language", func(t *testing.T) {
		got := loadTranslations("en")
		want := map[string]string{"posts": "Posts", "pages": "Pages", "dashboard": "Dashboard"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("loadTranslations(en) = %v, want %v", got, want)
		}
	})

	t.Run("unknown language falls back to zh-TW", func(t *testing.T) {
		got := loadTranslations("fr")
		want := map[string]string{"posts": "暫存文章"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("loadTranslations(fr) = %v, want zh-TW fallback %v", got, want)
		}
	})
}

func TestLoadTranslationsEmptyMapWhenNoLocaleExists(t *testing.T) {
	useRenderDirs(t)
	for _, lang := range []string{"fr", "zh-TW"} {
		got := loadTranslations(lang)
		if got == nil {
			t.Fatalf("loadTranslations(%s) = nil, want an empty map", lang)
		}
		if len(got) != 0 {
			t.Fatalf("loadTranslations(%s) = %v, want an empty map", lang, got)
		}
	}
}

func TestLoadTranslationsRejectsPathLikeLanguages(t *testing.T) {
	locales, _ := useRenderDirs(t)
	// A file the traversal would reach if the language were used as a path.
	writeRenderFile(t, filepath.Join(filepath.Dir(locales), "evil.json"), `{"posts":"EVIL"}`)
	writeRenderFile(t, filepath.Join(locales, "zh-TW.json"), `{"posts":"暫存文章"}`)

	for _, lang := range []string{"../evil", `..\evil`, "zh-TW/../../evil"} {
		resetTranslationCache()
		got := loadTranslations(lang)
		if got["posts"] == "EVIL" {
			t.Errorf("loadTranslations(%q) read a file outside the locales directory", lang)
		}
		if got["posts"] != "暫存文章" {
			t.Errorf("loadTranslations(%q) = %v, want zh-TW fallback", lang, got)
		}
	}
}

func TestAdminRenderPageRendersLayoutWithDataAndTranslations(t *testing.T) {
	locales, admin := useRenderDirs(t)
	writeRenderFile(t, filepath.Join(locales, "zh-TW.json"), `{"posts":"暫存文章"}`)
	writeAdminLayout(t, admin)
	writeRenderFile(t, filepath.Join(admin, "page.html"), `{{define "content"}}X={{.Extra}}{{end}}`)

	c, rec := adminRenderContext(t)
	err := renderAdminPage(c, AdminPage{
		File:   "page.html",
		Active: "posts",
		Title:  "文章管理",
		Data:   map[string]interface{}{"Extra": "hi"},
	})
	if err != nil {
		t.Fatalf("renderAdminPage returned error: %v", err)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"content:dashboard,posts*,",
		"shop:products,orders,",
		"|H|文章管理|暫存文章|X=hi",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered body missing %q; body=%q", want, body)
		}
	}
}

func TestAdminRenderPageMissingTemplateReturnsError(t *testing.T) {
	_, admin := useRenderDirs(t)
	writeAdminLayout(t, admin)

	c, rec := adminRenderContext(t)
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("renderAdminPage panicked on a missing template: %v", r)
			}
		}()
		err = renderAdminPage(c, AdminPage{File: "does-not-exist.html", Active: "posts"})
	}()
	if err == nil {
		t.Fatal("renderAdminPage returned nil error for a missing page template")
	}
	if rec.Body.Len() != 0 {
		t.Errorf("renderAdminPage wrote %d bytes before failing to parse", rec.Body.Len())
	}
}
