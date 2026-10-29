package theme

import (
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newRenderFuncsManager(t *testing.T) *Manager {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "request-funcs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"theme.json":       `{"name":"request-funcs"}`,
		"layout.html":      `{{define "layout"}}<layout>{{template "content" .}}</layout>{{end}}`,
		"index.html":       `{{define "content"}}<index>{{initial .SiteTitle}}|{{assetURL .Asset}}</index>{{end}}`,
		"maintenance.html": `{{define "maintenance.html"}}<maintenance>{{initial .SiteTitle}}</maintenance>{{end}}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	m := NewManager(root)
	if err := m.Activate("request-funcs"); err != nil {
		t.Fatal(err)
	}
	return m
}

func requestFuncMap(value, path string) template.FuncMap {
	return template.FuncMap{
		"initial":  func(string) string { return value },
		"assetURL": func(string) string { return path },
	}
}

func TestRequestThemeFuncsKeepCachedBaseUnexecutedAndIsolated(t *testing.T) {
	m := newRenderFuncsManager(t)
	data := map[string]any{"SiteTitle": "Old", "Asset": "/base"}

	// Populate the regular render cache first. A request-specific render must
	// still be able to clone the cached parsed template after this execution.
	if _, err := m.Render("index.html", data); err != nil {
		t.Fatalf("warm cached base through Render: %v", err)
	}
	withDB, err := m.RenderWithDB("index.html", data, nil)
	if err != nil {
		t.Fatalf("interleaved RenderWithDB: %v", err)
	}
	if string(withDB) != "<layout><index>O|/base</index></layout>" {
		t.Errorf("RenderWithDB output = %q", withDB)
	}
	standaloneData := map[string]any{"SiteTitle": "Old"}
	page, err := m.RenderPage("maintenance.html", standaloneData)
	if err != nil {
		t.Fatalf("interleaved RenderPage: %v", err)
	}
	if string(page) != "<maintenance>O</maintenance>" {
		t.Errorf("RenderPage output = %q", page)
	}
	pageWithDB, err := m.RenderPageWithDB("maintenance.html", standaloneData, nil)
	if err != nil {
		t.Fatalf("interleaved RenderPageWithDB: %v", err)
	}
	if string(pageWithDB) != "<maintenance>O</maintenance>" {
		t.Errorf("RenderPageWithDB output = %q", pageWithDB)
	}
	for _, tc := range []struct{ value, path string }{
		{"first", "/first"},
		{"second", "/second"},
	} {
		got, err := m.RenderWithDBAndFuncs("index.html", data, nil, requestFuncMap(tc.value, tc.path))
		if err != nil {
			t.Fatalf("RenderWithDBAndFuncs(%q): %v", tc.value, err)
		}
		want := "<layout><index>" + tc.value + "|" + tc.path + "</index></layout>"
		if string(got) != want {
			t.Errorf("request render = %q, want %q", got, want)
		}
	}

	// The request's functions must not leak into later old-entry calls.
	old, err := m.Render("index.html", data)
	if err != nil {
		t.Fatalf("old Render after request-specific render: %v", err)
	}
	if string(old) != "<layout><index>O|/base</index></layout>" {
		t.Errorf("old Render inherited request functions: %q", old)
	}
}

func TestRequestThemeFuncsAreIsolatedAcrossConcurrentValuesAndPaths(t *testing.T) {
	m := newRenderFuncsManager(t)
	const count = 16
	var wg sync.WaitGroup
	errCh := make(chan error, count)
	for i := 0; i < count; i++ {
		value, path := fmt.Sprintf("visitor-%02d", i), fmt.Sprintf("/path/%02d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := m.RenderWithDBAndFuncs("index.html", map[string]any{"SiteTitle": "Concurrent", "Asset": "/base"}, nil, requestFuncMap(value, path))
			if err != nil {
				errCh <- err
				return
			}
			want := "<layout><index>" + value + "|" + path + "</index></layout>"
			if string(got) != want {
				errCh <- fmt.Errorf("render = %q, want %q", got, want)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

func TestRequestThemeFuncsStandaloneAndErrors(t *testing.T) {
	m := newRenderFuncsManager(t)
	got, err := m.RenderPageWithDBAndFuncs("maintenance.html", map[string]any{"SiteTitle": "Offline"}, nil, requestFuncMap("offline", ""))
	if err != nil {
		t.Fatalf("RenderPageWithDBAndFuncs: %v", err)
	}
	if string(got) != "<maintenance>offline</maintenance>" {
		t.Errorf("standalone output = %q, want no layout wrapper", got)
	}

	badFuncs := template.FuncMap{"initial": func(string) (string, error) { return "", fmt.Errorf("request function failed") }}
	if _, err := m.RenderWithDBAndFuncs("index.html", map[string]any{"SiteTitle": "x", "Asset": "/"}, nil, badFuncs); err == nil || !strings.Contains(err.Error(), "request function failed") {
		t.Errorf("execution error = %v, want request function error", err)
	}
	if _, err := m.RenderWithDBAndFuncs("missing.html", nil, nil, requestFuncMap("x", "/")); err == nil {
		t.Error("missing template render returned no error")
	}
}
