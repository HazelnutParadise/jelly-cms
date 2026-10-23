package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExcerpt(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		limit int
		want  string
	}{
		{"strips tags and entities", "<p>Hello&nbsp;<b>world</b></p>", 50, "Hello world"},
		{"collapses whitespace", "<p>a</p>\n\n<p>b   c</p>", 50, "a b c"},
		{"truncates by runes not bytes", "一二三四五六七八九十", 4, "一二三四…"},
		{"does not add ellipsis when short", "短文", 10, "短文"},
		{"trims trailing space before ellipsis", "one two three", 4, "one…"},
		{"limit zero returns full text", "<em>x</em> y", 0, "x y"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Excerpt(tc.in, tc.limit); got != tc.want {
				t.Fatalf("Excerpt(%q, %d) = %q, want %q", tc.in, tc.limit, got, tc.want)
			}
		})
	}
}

func TestInitial(t *testing.T) {
	cases := map[string]string{"jelly": "J", "  榛果繽紛樂": "榛", "«Quoted»": "Q", "": "J", "!!!": "J", "42 things": "4"}
	for in, want := range cases {
		if got := Initial(in); got != want {
			t.Fatalf("Initial(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFirstImage(t *testing.T) {
	cases := map[string]string{
		`<p>text</p><img src="/uploads/a.jpg" alt="a"><img src="/b.png">`: "/uploads/a.jpg",
		`<IMG alt="x" SRC='/c.webp'>`:                                     "/c.webp",
		`<p>no image</p>`:                                                 "",
		"":                                                                "",
	}
	for in, want := range cases {
		if got := FirstImage(in); got != want {
			t.Fatalf("FirstImage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderTemplateFuncs(t *testing.T) {
	dir := t.TempDir()
	themeDir := filepath.Join(dir, "demo")
	if err := os.MkdirAll(themeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"theme.json":  `{"name":"demo"}`,
		"layout.html": `{{define "layout"}}<html>{{template "content" .}}</html>{{end}}`,
		"index.html":  `{{define "content"}}<i>{{initial .SiteTitle}}</i><p>{{excerpt .Body 5}}</p><div>{{safeHTML .Body}}</div><s>{{.Body}}</s>{{end}}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(themeDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	m := NewManager(dir)
	if err := m.Activate("demo"); err != nil {
		t.Fatal(err)
	}
	out, err := m.Render("index.html", map[string]interface{}{
		"SiteTitle": "jelly cms",
		"Body":      "<strong>Bold</strong> text that is long",
	})
	if err != nil {
		t.Fatal(err)
	}
	html := string(out)
	for _, want := range []string{
		"<i>J</i>",
		"<p>Bold…</p>",
		"<div><strong>Bold</strong> text that is long</div>",
		"<s>&lt;strong&gt;Bold&lt;/strong&gt; text that is long</s>",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("rendered output missing %q:\n%s", want, html)
		}
	}
}
