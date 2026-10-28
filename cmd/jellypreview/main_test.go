package main

import (
	"crypto/sha256"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	previewManagedRef  = regexp.MustCompile(`/(?:admin/assets|vendor/katex|_jelly|theme/assets)/[^"'<>\s)]+`)
	previewHTMLAttr    = regexp.MustCompile(`(?is)\b(?:src|href|srcset|data-[a-z0-9_-]+|style)\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	previewStyleBlock  = regexp.MustCompile(`(?is)<style\b[^>]*>(.*?)</style\s*>`)
	previewCSSRef      = regexp.MustCompile(`(?i)(?:url\(\s*['"]?([^)'"\s]+)['"]?\s*\)|@import\s+['"]([^'"]+)['"])`)
	previewHashValue   = regexp.MustCompile(`^[a-f0-9]{64}$`)
	previewManagedPath = regexp.MustCompile(`^/(admin/assets|vendor/katex|_jelly|theme/assets)/`)
)

func previewNormalizeRef(ref, from string) string {
	ref = strings.TrimSpace(strings.Trim(ref, `"'();`))
	parsed, err := url.Parse(ref)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || strings.HasPrefix(ref, "//") || strings.HasPrefix(ref, "data:") || strings.HasPrefix(ref, "#") {
		return ""
	}
	if !strings.HasPrefix(parsed.Path, "/") {
		base, _ := url.Parse(from)
		parsed = base.ResolveReference(parsed)
		parsed.Path = path.Clean(parsed.Path)
	}
	return parsed.String()
}

func TestPreviewEmitsAllAdminPagesAndEditorDependencies(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	web := filepath.Join(repo, "web")
	want := []string{"index", "posts", "pages", "taxonomy", "menus", "products", "orders", "media", "themes", "plugins", "settings", "post_editor", "page_editor", "product_editor", "comments", "trash", "revisions", "error-404", "error-500", "error-403", "error"}
	for _, lang := range []string{"zh-TW", "en"} {
		t.Run(lang, func(t *testing.T) {
			out := t.TempDir()
			cmd := exec.Command("go", "run", ".", "-web", web, "-out", out, "-lang", lang)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("jellypreview failed: %v\n%s", err, output)
			}
			for _, name := range want {
				path := filepath.Join(out, "admin-"+name+".html")
				body, e := os.ReadFile(path)
				if e != nil {
					t.Errorf("missing output %s: %v", name, e)
					continue
				}
				if len(body) == 0 {
					t.Errorf("empty output %s", name)
				}
			}
			for _, name := range []string{"post_editor", "page_editor"} {
				body, e := os.ReadFile(filepath.Join(out, "admin-"+name+".html"))
				if e != nil {
					continue
				}
				for _, needle := range []string{`id="jellyEditor"`, `/admin/assets/codemirror/editor.js?`, `/admin/assets/editor.js?`} {
					if !strings.Contains(string(body), needle) {
						t.Errorf("%s missing %s", name, needle)
					}
				}
			}
			seen := map[string]bool{}
			var checkRef func(string, string)
			checkRef = func(from, raw string) {
				ref := previewNormalizeRef(raw, from)
				if ref == "" {
					return
				}
				parsed, _ := url.Parse(ref)
				if !previewManagedPath.MatchString(parsed.Path) {
					return
				}
				if strings.HasPrefix(parsed.Path, "/vendor/katex/fonts/") && (strings.HasSuffix(parsed.Path, ".woff") || strings.HasSuffix(parsed.Path, ".ttf")) {
					source := filepath.Join(web, filepath.FromSlash(strings.TrimPrefix(parsed.Path, "/")))
					if _, sourceErr := os.Stat(source); os.IsNotExist(sourceErr) {
						return
					}
				}
				key := ref
				if seen[key] {
					return
				}
				seen[key] = true
				query, queryErr := url.ParseQuery(parsed.RawQuery)
				values := query["v"]
				if queryErr != nil || len(values) != 1 || !previewHashValue.MatchString(values[0]) {
					t.Errorf("%s managed resource %s has no valid 64-character v hash", from, ref)
					return
				}
				local := filepath.Join(out, filepath.FromSlash(strings.TrimPrefix(parsed.Path, "/")))
				copied, e := os.ReadFile(local)
				if e != nil {
					t.Errorf("%s local asset %s not copied: %v", from, ref, e)
					return
				}
				sum := fmt.Sprintf("%x", sha256.Sum256(copied))
				if sum != values[0] {
					t.Errorf("%s asset %s hash=%s want %s", from, ref, values[0], sum)
				}
				if strings.HasSuffix(parsed.Path, ".css") {
					for _, css := range previewCSSRef.FindAllSubmatch(copied, -1) {
						candidate := string(css[1])
						if candidate == "" {
							candidate = string(css[2])
						}
						checkRef(ref, candidate)
					}
				}
			}
			checkBody := func(from string, body []byte) {
				checkAttrs := func(text []byte) {
					for _, raw := range previewManagedRef.FindAll(text, -1) {
						checkRef(from, string(raw))
					}
					for _, css := range previewCSSRef.FindAllSubmatch(text, -1) {
						candidate := string(css[1])
						if candidate == "" {
							candidate = string(css[2])
						}
						checkRef(from, candidate)
					}
				}
				for _, attr := range previewHTMLAttr.FindAllSubmatch(body, -1) {
					value := attr[1]
					if len(value) == 0 {
						value = attr[2]
					}
					checkAttrs(value)
				}
				for _, block := range previewStyleBlock.FindAllSubmatch(body, -1) {
					for _, css := range previewCSSRef.FindAllSubmatch(block[1], -1) {
						candidate := string(css[1])
						if candidate == "" {
							candidate = string(css[2])
						}
						checkRef(from, candidate)
					}
				}
			}
			for _, html := range want {
				body, e := os.ReadFile(filepath.Join(out, "admin-"+html+".html"))
				if e == nil {
					checkBody("/admin-"+html+".html", body)
				}
			}
			fonts, e := filepath.Glob(filepath.Join(web, "vendor", "katex", "fonts", "*.woff2"))
			if e != nil {
				t.Fatal(e)
			}
			if len(fonts) != 20 {
				t.Fatalf("source KaTeX woff2 count=%d, want 20", len(fonts))
			}
			for _, font := range fonts {
				rel, _ := filepath.Rel(web, font)
				copied, e := os.ReadFile(filepath.Join(out, filepath.FromSlash(rel)))
				if e != nil {
					t.Errorf("woff2 dependency %s missing from output: %v", filepath.Base(font), e)
					continue
				}
				sum := fmt.Sprintf("%x", sha256.Sum256(copied))
				referenced := false
				for ref := range seen {
					parsed, _ := url.Parse(ref)
					if strings.HasSuffix(parsed.Path, "/fonts/"+filepath.Base(font)) {
						referenced = true
						query, queryErr := url.ParseQuery(parsed.RawQuery)
						values := query["v"]
						if queryErr != nil || len(values) != 1 || values[0] != sum || !previewHashValue.MatchString(values[0]) {
							t.Errorf("woff2 dependency %s hash missing or mismatched", filepath.Base(font))
						}
					}
				}
				if !referenced {
					t.Errorf("woff2 dependency %s is not referenced by hashed CSS", filepath.Base(font))
				}
			}
		})
	}
}
