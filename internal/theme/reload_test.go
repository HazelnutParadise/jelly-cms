package theme

import (
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	neturl "net/url"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestThemeReloadActiveRefreshesCurrentSnapshot(t *testing.T) {
	root := t.TempDir()
	persistenceTheme(t, root, "a", "A")
	m := NewManager(root)
	if err := m.Activate("a"); err != nil {
		t.Fatal(err)
	}
	before := m.AssetSnapshot()
	css := filepath.Join(root, "a", "assets", "site.css")
	if err := os.WriteFile(css, []byte("changed-css"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := m.ReloadActive(); err != nil {
		t.Fatal(err)
	}
	if m.AssetSnapshot() == before {
		t.Fatal("successful reload reused the prior snapshot")
	}
	url := m.AssetURL("/theme/assets/site.css")
	if url == before.URL("/theme/assets/site.css") {
		t.Fatalf("CSS URL did not change: before=%q after=%q", before.URL("/theme/assets/site.css"), url)
	}
	parsed, err := neturl.Parse(url)
	if err != nil {
		t.Fatal(err)
	}
	hash := parsed.Query().Get("v")
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(hash) {
		t.Fatalf("asset URL version=%q, want SHA-256 hex", hash)
	}
	req := httptest.NewRequest("GET", url, nil)
	res := httptest.NewRecorder()
	m.AssetSnapshot().ServeHTTP(res, req)
	if res.Code != 200 || res.Body.String() != "changed-css" {
		t.Fatalf("read refreshed CSS = status %d body %q", res.Code, res.Body.String())
	}
	sum := sha256.Sum256(res.Body.Bytes())
	if fmt.Sprintf("%x", sum) != hash {
		t.Fatalf("asset hash=%s does not match served bytes %x", hash, sum)
	}
}

func TestThemeReloadActiveRequiresActiveTheme(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.ReloadActive(); err == nil {
		t.Fatal("reload without an active theme succeeded")
	}
}

func TestThemeReloadActiveFailureKeepsUsableSnapshot(t *testing.T) {
	root := t.TempDir()
	persistenceTheme(t, root, "a", "A")
	m := NewManager(root)
	if err := m.Activate("a"); err != nil {
		t.Fatal(err)
	}
	before, beforeURL := m.AssetSnapshot(), m.AssetURL("/theme/assets/site.css")
	if err := os.Remove(filepath.Join(root, "a", "theme.json")); err != nil {
		t.Fatal(err)
	}
	if err := m.ReloadActive(); err == nil {
		t.Fatal("reload of missing current theme succeeded")
	}
	if m.AssetSnapshot() != before || m.AssetURL("/theme/assets/site.css") != beforeURL {
		t.Fatal("failed reload changed current snapshot")
	}
	body, err := m.Render("index.html", nil)
	if err != nil || string(body) != "A" {
		t.Fatalf("render after failed reload = %q, %v", body, err)
	}
}
