package server

// Ticket 09 asset contract (docs/tickets/09-editor.md, 前端與資產; AGENTS.md on external assets). These are
// file checks with no database. The editor and the theme ship their own CodeMirror, KaTeX, and motion and
// component scripts, the templates load nothing from an external URL, and marked.min.js is gone. Written
// before the assets exist and expected to fail until they are vendored. Do not edit them to match an
// implementation.

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	edaExternalScript = regexp.MustCompile(`(?is)<script\b[^>]*\ssrc\s*=\s*["']?\s*(?:https?:)?//`)
	edaExternalLink   = regexp.MustCompile(`(?is)<link\b[^>]*\shref\s*=\s*["']?\s*(?:https?:)?//`)
)

// edaNonEmpty fails unless path exists and has content.
func edaNonEmpty(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Errorf("missing %s: %v", path, err)
		return
	}
	if info.Size() == 0 {
		t.Errorf("%s is empty", path)
	}
}

// edaHasFileWithSuffix reports whether dir, or any directory under it, holds a file with suffix.
func edaHasFileWithSuffix(t *testing.T, dir, suffix string) bool {
	t.Helper()
	found := false
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() && strings.HasSuffix(path, suffix) {
			found = true
		}
		return nil
	})
	if err != nil {
		t.Errorf("walk %s: %v", dir, err)
	}
	return found
}

func TestEditorVendoredAssetsPresent(t *testing.T) {
	root := repoRoot(t)

	codemirror := filepath.Join(root, "web", "admin", "assets", "codemirror")
	if !edaHasFileWithSuffix(t, codemirror, ".js") {
		t.Errorf("no .js file under %s", codemirror)
	}
	edaNonEmpty(t, filepath.Join(codemirror, "LICENSE"))
	edaNonEmpty(t, filepath.Join(codemirror, "VERSION"))

	katex := filepath.Join(root, "web", "vendor", "katex")
	edaNonEmpty(t, filepath.Join(katex, "katex.min.css"))
	if !edaHasFileWithSuffix(t, katex, ".woff2") {
		t.Errorf("no .woff2 font under %s", katex)
	}
	edaNonEmpty(t, filepath.Join(katex, "LICENSE"))
	edaNonEmpty(t, filepath.Join(katex, "VERSION"))

	for _, script := range []string{"codemirror.sh", "katex.sh"} {
		info, err := os.Stat(filepath.Join(root, "scripts", "vendor", script))
		if err != nil {
			t.Errorf("missing scripts/vendor/%s: %v", script, err)
			continue
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Errorf("scripts/vendor/%s is not executable (mode %v)", script, info.Mode().Perm())
		}
	}
}

func TestEditorTemplatesLoadNoExternalAssets(t *testing.T) {
	root := repoRoot(t)
	adminRoot := filepath.Join(root, "web", "admin")

	for _, name := range []string{"post_editor.html", "page_editor.html", "editor.html"} {
		path := filepath.Join(adminRoot, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		if m := edaExternalScript.FindString(string(raw)); m != "" {
			t.Errorf("%s loads a script from an external URL: %q", name, m)
		}
		if m := edaExternalLink.FindString(string(raw)); m != "" {
			t.Errorf("%s loads a stylesheet or icon from an external URL: %q", name, m)
		}
	}

	err := filepath.WalkDir(adminRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(raw), "marked.min.js") {
			t.Errorf("%s still references marked.min.js", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", adminRoot, err)
	}

	if _, err := os.Stat(filepath.Join(adminRoot, "assets", "marked.min.js")); err == nil {
		t.Errorf("web/admin/assets/marked.min.js must be removed; the server is the only renderer")
	}
}

func TestThemeAssetsPresent(t *testing.T) {
	root := repoRoot(t)
	assets := filepath.Join(root, "web", "themes", "default", "assets")
	for _, name := range []string{"content.css", "jelly-motion.js", "jelly-components.js"} {
		edaNonEmpty(t, filepath.Join(assets, name))
	}
}
