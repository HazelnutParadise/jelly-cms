package assetcheck

// Vendored-package acceptance tests for ticket 12 slots Z1 and Z2 (docs/tickets/12-zero-cdn-jelly-ui.md).
// Every third-party front-end package under web/ ships built files with LICENSE and VERSION, and a script in
// scripts/vendor/ rebuilds it (AGENTS.md 工作規則). Most of these tests are expected to fail now: Jelly UI,
// marked's LICENSE and VERSION, the vendor scripts, the release checklist entries and the system
// requirements page do not exist yet. Do not edit them to match an implementation. No database is needed.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func mustRead(t *testing.T, root string, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{root}, parts...)...)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("%s: %v", filepath.ToSlash(filepath.Join(parts...)), err)
		return ""
	}
	return string(content)
}

func requireVersionFile(t *testing.T, name, content string) (version, commit string) {
	t.Helper()
	patterns := []struct {
		label string
		re    *regexp.Regexp
	}{
		{"a `Version: <version>` line", regexp.MustCompile(`(?m)^Version:\s*(\S+)\s*$`)},
		{"a `Commit: <40 hex>` line (the source commit)", regexp.MustCompile(`(?m)^Commit:\s*([0-9a-f]{40})\s*$`)},
		{"a `Build date (UTC): YYYY-MM-DD` line", regexp.MustCompile(`(?m)^Build date \(UTC\):\s*(\d{4}-\d{2}-\d{2})\s*$`)},
	}
	var got [3]string
	for i, p := range patterns {
		m := p.re.FindStringSubmatch(content)
		if m == nil {
			t.Errorf("%s needs %s", name, p.label)
			continue
		}
		got[i] = m[1]
	}
	return got[0], got[1]
}

func TestJellyUIVendoredBuildIsPresent(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join("web", "admin", "assets", "jelly-ui")

	js, err := os.ReadFile(filepath.Join(root, dir, "jelly.js"))
	if err != nil {
		t.Errorf("%s/jelly.js: %v", filepath.ToSlash(dir), err)
	} else {
		if len(js) < 100_000 {
			t.Errorf("jelly.js is %d bytes; the upstream build is about 318 KB, so this is not the built bundle", len(js))
		}
		for _, name := range []string{"jelly-switch", "jelly-segmented", "jelly-segment", "jelly-dialog", "jelly-toaster", "jellyToast"} {
			if !strings.Contains(string(js), name) {
				t.Errorf("jelly.js does not define %q", name)
			}
		}
	}

	if license := mustRead(t, root, dir, "LICENSE"); license != "" {
		if !strings.Contains(license, "MIT License") {
			t.Errorf("%s/LICENSE is not the MIT license text", filepath.ToSlash(dir))
		}
		// The bundle carries Fluent System Icons glyphs; their MIT notice has to travel with it.
		if !strings.Contains(license, "Fluent System Icons") || !strings.Contains(license, "Microsoft Corporation") {
			t.Errorf("%s/LICENSE does not carry the Fluent System Icons (Microsoft Corporation) MIT notice", filepath.ToSlash(dir))
		}
	}

	version := mustRead(t, root, dir, "VERSION")
	if version == "" {
		return
	}
	v, commit := requireVersionFile(t, filepath.ToSlash(dir)+"/VERSION", version)
	if v != "" && !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(v) {
		t.Errorf("VERSION Version = %q, want the upstream package.json version such as 1.1.0", v)
	}

	// The script pins the commit; VERSION must say the same thing, so a rerun reproduces the same build.
	script := mustRead(t, root, "scripts", "vendor", "jelly-ui.sh")
	m := regexp.MustCompile(`(?m)^jelly_ui_commit=([0-9a-f]{40})\s*$`).FindStringSubmatch(script)
	switch {
	case script == "":
	case m == nil:
		t.Error("scripts/vendor/jelly-ui.sh must pin the source with a line `jelly_ui_commit=<40 hex>`")
	case commit != "" && m[1] != commit:
		t.Errorf("scripts/vendor/jelly-ui.sh pins %s but VERSION records %s", m[1], commit)
	}
}

func TestVendorScriptsExistAndRebuild(t *testing.T) {
	root := repoRoot(t)
	for _, name := range []string{"jelly-ui.sh", "marked.sh", "fonts.sh"} {
		path := filepath.Join(root, "scripts", "vendor", name)
		info, err := os.Stat(path)
		switch {
		case err != nil:
			t.Errorf("scripts/vendor/%s: %v", name, err)
		case info.Mode().Perm()&0o111 == 0:
			t.Errorf("scripts/vendor/%s is not executable (mode %v)", name, info.Mode().Perm())
		}
	}
	jelly := mustRead(t, root, "scripts", "vendor", "jelly-ui.sh")
	for _, want := range []string{"npm ci", "npm run build", "web/admin/assets/jelly-ui", "LICENSE", "VERSION", "dist/jelly.js"} {
		if jelly != "" && !strings.Contains(jelly, want) {
			t.Errorf("scripts/vendor/jelly-ui.sh does not mention %q; it must clone, build and copy dist/jelly.js, LICENSE and VERSION", want)
		}
	}
	marked := mustRead(t, root, "scripts", "vendor", "marked.sh")
	if marked != "" && !regexp.MustCompile(`(?m)^marked_version=\d+\.\d+\.\d+\s*$`).MatchString(marked) {
		t.Error("scripts/vendor/marked.sh must pin the release with a line `marked_version=<x.y.z>`")
	}
}

// markedPresent reports whether this branch still carries marked. Ticket 09 (PR #26) removes it; when that
// merges first, the marked checks have nothing to check.
func markedPresent(root string) bool {
	for _, p := range []string{
		filepath.Join(root, "web", "admin", "assets", "marked.min.js"),
		filepath.Join(root, "web", "admin", "assets", "marked"),
	} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

func TestMarkedIsVendoredAsAPackageDirectory(t *testing.T) {
	root := repoRoot(t)
	if !markedPresent(root) {
		t.Skip("marked is not shipped on this branch (removed by ticket 09)")
	}
	dir := filepath.Join("web", "admin", "assets", "marked")
	if _, err := os.Stat(filepath.Join(root, "web", "admin", "assets", "marked.min.js")); err == nil {
		t.Error("web/admin/assets/marked.min.js sits loose next to other packages; move it to web/admin/assets/marked/marked.min.js")
	}
	if _, err := os.Stat(filepath.Join(root, dir, "marked.min.js")); err != nil {
		t.Errorf("%s/marked.min.js: %v", filepath.ToSlash(dir), err)
	}
	if license := mustRead(t, root, dir, "LICENSE"); license != "" && !strings.Contains(license, "MIT License") {
		t.Errorf("%s/LICENSE is not the MIT license text", filepath.ToSlash(dir))
	}
	version := mustRead(t, root, dir, "VERSION")
	if version == "" {
		return
	}
	v, _ := requireVersionFile(t, filepath.ToSlash(dir)+"/VERSION", version)
	if script := mustRead(t, root, "scripts", "vendor", "marked.sh"); v != "" && script != "" &&
		!strings.Contains(script, "marked_version="+v) {
		t.Errorf("scripts/vendor/marked.sh pins a different release than VERSION (%s)", v)
	}
	if components := mustRead(t, root, "web", "admin", "components.html"); components != "" &&
		strings.Contains(components, "/admin/assets/marked.min.js") {
		t.Error("web/admin/components.html still loads /admin/assets/marked.min.js; use /admin/assets/marked/marked.min.js")
	}
}

// firstPartyAssets are files this repository authors itself; every other loose file next to the
// package directories is treated as an unlabelled third-party file. sortable.js, jelly-motion.js and
// jelly-components.js arrive with ticket 09 (docs/tickets/09-editor.md on its branch) and are first-party.
var firstPartyAssets = map[string]bool{
	"jelly-mark.svg": true, "sortable.js": true, "jelly-motion.js": true, "jelly-components.js": true,
}

func TestEveryThirdPartyPackageHasLicenseAndVersion(t *testing.T) {
	root := repoRoot(t)
	parents := []string{filepath.Join(root, "web", "admin", "assets"), filepath.Join(root, "web", "vendor")}
	themes, _ := filepath.Glob(filepath.Join(root, "web", "themes", "*", "assets"))
	parents = append(parents, themes...)

	checked := 0
	for _, parent := range parents {
		entries, err := os.ReadDir(parent)
		if err != nil {
			continue // web/vendor may not exist
		}
		for _, entry := range entries {
			rel, _ := filepath.Rel(root, filepath.Join(parent, entry.Name()))
			rel = filepath.ToSlash(rel)
			if !entry.IsDir() {
				if !firstPartyAssets[entry.Name()] {
					t.Errorf("%s is a loose file; a vendored package is a directory with its own LICENSE and VERSION", rel)
				}
				continue
			}
			checked++
			for _, name := range []string{"LICENSE", "VERSION"} {
				if info, err := os.Stat(filepath.Join(parent, entry.Name(), name)); err != nil || info.Size() == 0 {
					t.Errorf("%s has no non-empty %s", rel, name)
				}
			}
		}
	}
	if checked < 2 {
		t.Errorf("found %d package directories, want at least the two font directories", checked)
	}
}

func TestReleaseChecklistListsVendoredPackages(t *testing.T) {
	root := repoRoot(t)
	checklist := mustRead(t, root, "docs", "release-checklist.md")
	if checklist == "" {
		return
	}
	for _, want := range []string{
		"scripts/vendor/fonts.sh", "scripts/vendor/jelly-ui.sh", "scripts/vendor/marked.sh",
		"web/admin/assets/jelly-ui", "Jelly UI", "marked", "字型",
	} {
		if !strings.Contains(checklist, want) {
			t.Errorf("docs/release-checklist.md does not list %q in its vendored package inventory", want)
		}
	}
}

func TestSystemRequirementsNameTheBrowserFeaturesJellyUINeeds(t *testing.T) {
	root := repoRoot(t)
	doc := mustRead(t, root, "docs", "system-requirements.md")
	if doc == "" {
		return
	}
	for _, want := range []string{"Custom Elements", "Shadow DOM", "ElementInternals", "ResizeObserver", "Canvas", "color-mix()", "JavaScript"} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/system-requirements.md does not name %q", want)
		}
	}
}

func TestDesignBoardsUseSelfHostedFonts(t *testing.T) {
	root := repoRoot(t)
	css := mustRead(t, root, "design", "project", "fonts.css")
	if css != "" {
		if got := strings.Count(css, "@font-face"); got != 2 {
			t.Errorf("design/project/fonts.css has %d @font-face rules, want 2 (Bricolage Grotesque, Noto Sans TC)", got)
		}
		for _, want := range []string{`url("fonts/bricolage-grotesque.woff2")`, `url("fonts/noto-sans-tc.woff2")`, "font-display: swap"} {
			if !strings.Contains(css, want) {
				t.Errorf("design/project/fonts.css does not contain %q", want)
			}
		}
	}

	boards, err := filepath.Glob(filepath.Join(root, "design", "project", "*.dc.html"))
	if err != nil || len(boards) < 60 {
		t.Fatalf("found %d design boards (err %v), want at least 60", len(boards), err)
	}
	for _, board := range boards {
		content, err := os.ReadFile(board)
		if err != nil {
			t.Fatalf("read %s: %v", board, err)
		}
		name := filepath.Base(board)
		if !strings.Contains(string(content), `<link rel="stylesheet" href="./fonts.css">`) {
			t.Errorf("%s does not load ./fonts.css", name)
		}
		for _, bad := range []string{"fonts.googleapis.com", "fonts.gstatic.com"} {
			if strings.Contains(string(content), bad) {
				t.Errorf("%s still references %s", name, bad)
			}
		}
	}

	if gitignore := mustRead(t, root, ".gitignore"); gitignore != "" {
		found := false
		for _, line := range strings.Split(gitignore, "\n") {
			if strings.TrimSpace(line) == "design/project/fonts/" {
				found = true
			}
		}
		if !found {
			t.Error(".gitignore does not list design/project/fonts/")
		}
	}
	if script := mustRead(t, root, "scripts", "vendor", "fonts.sh"); script != "" && !strings.Contains(script, `design/project/fonts`) {
		t.Error("scripts/vendor/fonts.sh does not copy the subset into design/project/fonts")
	}
	if script := mustRead(t, root, "design", "gen", "gen.py"); script != "" && !strings.Contains(script, `href="./fonts.css"`) {
		t.Error("design/gen/gen.py FONTS does not link ./fonts.css, so regenerated boards would lose the fonts")
	}
}
