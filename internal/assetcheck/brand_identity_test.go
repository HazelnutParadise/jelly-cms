package assetcheck

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	nethtml "golang.org/x/net/html"
)

const canonicalBrandSHA256 = "a7b81e6a34f0f354b4c3c788f59a511d43e330e9eb2a8f1bbc3a68265682b32a"

func brandHasClass(n *nethtml.Node, name string) bool {
	for _, class := range strings.Fields(brandAttr(n, "class")) {
		if class == name {
			return true
		}
	}
	return false
}

func brandAttr(n *nethtml.Node, name string) string {
	for _, attr := range n.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}
	return ""
}

func TestCanonicalBrandAssetAndPortableBoardCopy(t *testing.T) {
	root := repoRoot(t)
	canonicalPath := filepath.Join(root, "web", "admin", "assets", "jelly-mark.svg")
	portablePath := filepath.Join(root, "design", "project", "jelly-mark.svg")
	canonical, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	if got := hex.EncodeToString(digest[:]); got != canonicalBrandSHA256 {
		t.Errorf("canonical logo SHA-256=%s, want %s", got, canonicalBrandSHA256)
	}
	if !strings.Contains(string(canonical), `viewBox="0 0 85 100"`) {
		t.Errorf("canonical logo does not declare viewBox 0 0 85 100")
	}
	portable, err := os.ReadFile(portablePath)
	if err != nil {
		t.Fatalf("portable design copy: %v", err)
	}
	if string(portable) != string(canonical) {
		t.Errorf("design/project/jelly-mark.svg differs from canonical source bytes")
	}

	// Login and installer already use the original asset; protect those references without assuming
	// the installed-site /install route renders an installer.
	for _, name := range []string{"login.html", "install.html"} {
		body, err := os.ReadFile(filepath.Join(root, "web", "admin", name))
		if err != nil {
			t.Errorf("read web/admin/%s: %v", name, err)
			continue
		}
		if !strings.Contains(string(body), `src="{{assetURL "/admin/assets/jelly-mark.svg"}}"`) {
			t.Errorf("web/admin/%s no longer references canonical logo asset", name)
		}
	}
}

func TestDesignBoardBrandMarksUsePortableImage(t *testing.T) {
	root := repoRoot(t)
	projectDir := filepath.Join(root, "design", "project")
	portablePath := filepath.Join(projectDir, "jelly-mark.svg")
	seen := 0
	mermaidMarks := 0
	err := filepath.WalkDir(projectDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".html" || !strings.HasSuffix(path, ".dc.html") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		doc, err := nethtml.Parse(strings.NewReader(string(body)))
		if err != nil {
			return err
		}
		board, _ := filepath.Rel(projectDir, path)
		var walk func(*nethtml.Node)
		walk = func(n *nethtml.Node) {
			if n.Type == nethtml.ElementNode && brandHasClass(n, "mark") {
				seen++
				if strings.EqualFold(filepath.Base(path), "Mermaid.dc.html") {
					mermaidMarks++
				}
				if n.Data != "img" {
					t.Errorf("design/project/%s .mark is <%s>, want img referencing the portable SVG", board, n.Data)
				} else {
					src := brandAttr(n, "src")
					u, parseErr := url.Parse(src)
					resolved := ""
					if parseErr == nil && !u.IsAbs() && u.Host == "" {
						resolved = filepath.Clean(filepath.Join(filepath.Dir(path), filepath.FromSlash(u.Path)))
					}
					if resolved != portablePath || u.RawQuery != "" || u.Fragment != "" {
						t.Errorf("design/project/%s .mark src=%q resolves to %q, want %s", board, src, resolved, portablePath)
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(doc)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen < 2 || mermaidMarks < 2 {
		t.Errorf("found %d .mark nodes overall and %d in Mermaid.dc.html; expected existing representative header/footer identity marks", seen, mermaidMarks)
	}
}
