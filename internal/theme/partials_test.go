package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// partialsTheme builds a temporary theme "demo" holding the given files next
// to a layout that renders the page's "content" template, then activates it.
func partialsTheme(t *testing.T, files map[string]string) *Manager {
	t.Helper()
	dir := t.TempDir()
	themeDir := filepath.Join(dir, "demo")
	if err := os.MkdirAll(themeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	all := map[string]string{
		"theme.json":  `{"name":"demo"}`,
		"layout.html": `{{define "layout"}}<html>{{template "content" .}}</html>{{end}}`,
	}
	for name, content := range files {
		all[name] = content
	}
	for name, content := range all {
		if err := os.WriteFile(filepath.Join(themeDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	m := NewManager(dir)
	if err := m.Activate("demo"); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPartialDefinedTemplateIsCalledByPage(t *testing.T) {
	m := partialsTheme(t, map[string]string{
		"_comments.html": `{{define "comments"}}<section data-partial="comments">{{.SiteTitle}}</section>{{end}}`,
		"index.html":     `{{define "content"}}<main>{{template "comments" .}}</main>{{end}}`,
	})
	out, err := m.Render("index.html", map[string]interface{}{"SiteTitle": "jelly cms"})
	if err != nil {
		t.Fatalf("Render with a _comments.html partial: %v", err)
	}
	want := `<section data-partial="comments">jelly cms</section>`
	if !strings.Contains(string(out), want) {
		t.Fatalf("rendered output missing %q:\n%s", want, out)
	}
}

func TestPartialEmptyBlockUsesPartialDefinition(t *testing.T) {
	m := partialsTheme(t, map[string]string{
		"_sidebar.html": `{{define "sidebar"}}<aside>partial sidebar</aside>{{end}}`,
		"index.html":    `{{define "content"}}<main>{{block "sidebar" .}}{{end}}</main>{{end}}`,
	})
	out, err := m.Render("index.html", map[string]interface{}{})
	if err != nil {
		t.Fatalf("Render with an empty block: %v", err)
	}
	want := `<main><aside>partial sidebar</aside></main>`
	if !strings.Contains(string(out), want) {
		t.Fatalf("rendered output missing %q:\n%s", want, out)
	}
}

func TestPartialFilesResolveRegardlessOfOrder(t *testing.T) {
	card := `{{define "card"}}<div class="card">{{template "badge" .}}</div>{{end}}`
	badge := `{{define "badge"}}<b>new</b>{{end}}`
	arrangements := map[string]map[string]string{
		"dependent file sorts first": {
			"_a_card.html":  card,
			"_z_badge.html": badge,
		},
		"dependency file sorts first": {
			"_a_badge.html": badge,
			"_z_card.html":  card,
		},
	}
	for name, files := range arrangements {
		t.Run(name, func(t *testing.T) {
			files["index.html"] = `{{define "content"}}{{template "card" .}}{{end}}`
			m := partialsTheme(t, files)
			out, err := m.Render("index.html", map[string]interface{}{})
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			want := `<div class="card"><b>new</b></div>`
			if !strings.Contains(string(out), want) {
				t.Fatalf("rendered output missing %q:\n%s", want, out)
			}
		})
	}
}

func TestPartialAvailableToEveryPageRender(t *testing.T) {
	m := partialsTheme(t, map[string]string{
		"_comments.html": `{{define "comments"}}<ul class="comments"></ul>{{end}}`,
		"index.html":     `{{define "content"}}<main>index {{template "comments" .}}</main>{{end}}`,
		"post.html":      `{{define "content"}}<main>post {{template "comments" .}}</main>{{end}}`,
	})
	for _, page := range []struct{ file, prefix string }{
		{"index.html", "<main>index "},
		{"post.html", "<main>post "},
		{"index.html", "<main>index "},
	} {
		out, err := m.Render(page.file, map[string]interface{}{})
		if err != nil {
			t.Fatalf("Render %s: %v", page.file, err)
		}
		want := page.prefix + `<ul class="comments"></ul></main>`
		if !strings.Contains(string(out), want) {
			t.Fatalf("Render %s output missing %q:\n%s", page.file, want, out)
		}
	}
}

func TestPartialParseErrorNamesFile(t *testing.T) {
	broken := map[string]string{
		"unterminated define": `{{define "broken"}}{{if .Title}}open`,
		"unknown function":    `{{define "broken"}}{{nosuch .Title}}{{end}}`,
	}
	for name, content := range broken {
		t.Run(name, func(t *testing.T) {
			m := partialsTheme(t, map[string]string{
				"_broken.html": content,
				"index.html":   `{{define "content"}}<main>plain page</main>{{end}}`,
			})
			_, err := m.Render("index.html", map[string]interface{}{"Title": "x"})
			if err == nil {
				t.Fatal("Render with a broken _broken.html partial returned no error")
			}
			if !strings.Contains(err.Error(), "_broken.html") {
				t.Fatalf("error %q does not name _broken.html", err)
			}
		})
	}
}
