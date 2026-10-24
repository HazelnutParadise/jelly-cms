package server

// Offline-asset acceptance tests for ticket 05 slot B4 (public theme). A page must never load scripts,
// styles, fonts or icons from an external URL, and the fonts must be vendored with their LICENSE and VERSION
// files. The contract is the 契約 section of docs/tickets/05-public-theme.md and the 工作規則 in AGENTS.md
// (vendored packages live under web/ with LICENSE and VERSION; scripts/vendor/ rebuilds them). These tests
// are expected to fail now: the layout still loads Google Fonts and the woff2 files, LICENSE and VERSION
// files, scripts/vendor/fonts.sh and scripts/vendor/fonts-charset.txt do not exist yet. Do not edit them to
// match an implementation. The file checks need no database; the render check needs JELLY_TEST_POSTGRES=1.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// offlineForbiddenInPage lists the external references a public page must not contain in its head,
// masthead or footer.
var offlineForbiddenInPage = []string{
	`href="http`,
	`src="http`,
	"fonts.googleapis.com",
	"fonts.gstatic.com",
	"cdn.",
	"unpkg.com",
	"jsdelivr.net",
}

// offlineForbiddenInTemplate lists the external references a template file must not contain at all.
var offlineForbiddenInTemplate = []string{
	"fonts.googleapis.com",
	"fonts.gstatic.com",
	"cdn.",
	"unpkg.com",
	"jsdelivr.net",
	`<script src="http`,
	`<link href="http`,
}

// offlineChromeRegions returns the rendered document without the main content, which may legitimately
// contain links the author wrote. When the page has no <main> element the whole document is returned.
func offlineChromeRegions(html string) string {
	first := strings.Index(html, "<main")
	last := strings.LastIndex(html, "</main>")
	if first < 0 || last < 0 {
		return html
	}
	return html[:first] + html[last+len("</main>"):]
}

func TestPublicLayoutLoadsNoExternalAssets(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "offline-author")
	post := archivesSeedPost(t, h, author, archivesPost{title: "Offline Layout Post",
		publishedAt: archivesDay(2025, time.March, 7)})

	for _, path := range []string{"/", "/post/" + post.Slug, "/search?q=x", "/offline-no-such-page"} {
		r := archivesGet(t, h, path)
		if strings.TrimSpace(r.body) == "" {
			t.Errorf("GET %s rendered an empty body (status %d)", path, r.code)
			continue
		}
		chrome := offlineChromeRegions(r.body)
		for _, bad := range offlineForbiddenInPage {
			if strings.Contains(chrome, bad) {
				t.Errorf("GET %s loads or links an external asset in its head, masthead or footer: contains %q", path, bad)
			}
		}
	}
}

func TestAdminTemplatesLoadNoExternalAssets(t *testing.T) {
	root := repoRoot(t)
	var files []string
	for _, pattern := range []string{
		filepath.Join(root, "web", "admin", "*.html"),
		filepath.Join(root, "web", "themes", "default", "*.html"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("glob %s: %v", pattern, err)
		}
		if len(matches) == 0 {
			t.Fatalf("no templates match %s", pattern)
		}
		files = append(files, matches...)
	}
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, bad := range offlineForbiddenInTemplate {
			if strings.Contains(string(content), bad) {
				rel, relErr := filepath.Rel(root, file)
				if relErr != nil {
					rel = file
				}
				t.Errorf("%s references an external asset: contains %q", rel, bad)
			}
		}
	}
}

func TestVendoredFontsPresent(t *testing.T) {
	root := repoRoot(t)
	for _, dir := range []string{
		filepath.Join(root, "web", "themes", "default", "assets", "fonts"),
		filepath.Join(root, "web", "admin", "assets", "fonts"),
	} {
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			rel = dir
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		var woff2, license, version bool
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			switch name := entry.Name(); {
			case strings.HasSuffix(name, ".woff2"):
				woff2 = true
			case name == "LICENSE":
				license = true
			case name == "VERSION":
				version = true
			}
		}
		if !woff2 {
			t.Errorf("%s has no .woff2 font file", rel)
		}
		if !license {
			t.Errorf("%s has no LICENSE file", rel)
		}
		if !version {
			t.Errorf("%s has no VERSION file", rel)
		}
	}

	script := filepath.Join(root, "scripts", "vendor", "fonts.sh")
	info, err := os.Stat(script)
	switch {
	case err != nil:
		t.Errorf("scripts/vendor/fonts.sh: %v", err)
	case info.Mode().Perm()&0o111 == 0:
		t.Errorf("scripts/vendor/fonts.sh is not executable (mode %v)", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(root, "scripts", "vendor", "fonts-charset.txt")); err != nil {
		t.Errorf("scripts/vendor/fonts-charset.txt: %v", err)
	}
}
