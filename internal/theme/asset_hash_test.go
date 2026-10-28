package theme

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeThemeAssetHashFixture(t *testing.T, root, name, asset string, body []byte) {
	t.Helper()
	themeDir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(themeDir, "assets"), 0755); err != nil {
		t.Fatal(err)
	}
	for file, content := range map[string][]byte{
		"theme.json":                   []byte(`{"name":"` + name + `"}`),
		"layout.html":                  []byte(`{{define "layout"}}{{template "content" .}}{{end}}`),
		"index.html":                   []byte(`{{define "content"}}ok{{end}}`),
		filepath.Join("assets", asset): body,
	} {
		if err := os.WriteFile(filepath.Join(themeDir, file), content, 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAssetHashThemeURLTracksActiveThemeContent(t *testing.T) {
	root := t.TempDir()
	writeThemeAssetHashFixture(t, root, "a", "site.css", []byte("body{color:red}"))
	writeThemeAssetHashFixture(t, root, "b", "site.css", []byte("body{color:blue}"))
	m := NewManager(root)
	if err := m.Activate("a"); err != nil {
		t.Fatalf("activate a: %v", err)
	}
	aURL := m.AssetURL("/theme/assets/site.css?mode=dark#top")
	if !strings.HasPrefix(aURL, "/theme/assets/site.css?mode=dark&v=") || !strings.HasSuffix(aURL, "#top") {
		t.Fatalf("theme A asset URL did not preserve suffix and include hash: %q", aURL)
	}
	if err := m.Activate("b"); err != nil {
		t.Fatalf("activate b: %v", err)
	}
	bURL := m.AssetURL("/theme/assets/site.css?mode=dark#top")
	if aURL == bURL {
		t.Fatalf("same theme asset path with different bytes retained URL %q", aURL)
	}
	if got := m.AssetURL("https://example.com/site.css"); got != "https://example.com/site.css" {
		t.Fatalf("external URL changed: %q", got)
	}
}

func TestAssetHashThemeFailedActivationKeepsCurrentAssetSnapshot(t *testing.T) {
	root := t.TempDir()
	writeThemeAssetHashFixture(t, root, "a", "site.css", []byte("body{color:red}"))
	m := NewManager(root)
	if err := m.Activate("a"); err != nil {
		t.Fatalf("activate a: %v", err)
	}
	before := m.AssetURL("/theme/assets/site.css")
	if err := m.Activate("missing"); err == nil {
		t.Fatal("activation of missing theme unexpectedly succeeded")
	}
	if m.GetActive() != "a" || m.AssetURL("/theme/assets/site.css") != before {
		t.Fatalf("failed activation changed current theme or asset URL: active=%q url=%q", m.GetActive(), m.AssetURL("/theme/assets/site.css"))
	}
}

func TestAssetHashThemeWithoutAssetsCanActivate(t *testing.T) {
	root := t.TempDir()
	themeDir := filepath.Join(root, "empty")
	if err := os.MkdirAll(themeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(themeDir, "theme.json"), []byte(`{"name":"empty"}`), 0644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(root)
	if err := m.Activate("empty"); err != nil {
		t.Fatalf("theme without an assets directory should remain activatable: %v", err)
	}
	if got := m.AssetURL("/theme/assets/absent.css"); got != "/theme/assets/absent.css" {
		t.Fatalf("unknown asset URL = %q", got)
	}
}

func TestAssetHashThemeSameMtimeContentChangeAndReload(t *testing.T) {
	root := t.TempDir()
	writeThemeAssetHashFixture(t, root, "demo", "site.css", []byte("body{color:red}"))
	m := NewManager(root)
	if err := m.Activate("demo"); err != nil {
		t.Fatal(err)
	}
	first := m.AssetURL("/theme/assets/site.css")
	assetPath := filepath.Join(root, "demo", "assets", "site.css")
	info, err := os.Stat(assetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(assetPath, []byte("body{color:blue}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(assetPath, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := m.Activate("demo"); err != nil {
		t.Fatalf("reload active theme: %v", err)
	}
	second := m.AssetURL("/theme/assets/site.css")
	if first == second {
		t.Fatalf("same-mtime theme asset content change retained URL %q after reload", first)
	}
	stable, err := os.Stat(assetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(assetPath, stable.ModTime().Add(time.Hour), stable.ModTime().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := m.Activate("demo"); err != nil {
		t.Fatalf("reload unchanged theme: %v", err)
	}
	if got := m.AssetURL("/theme/assets/site.css"); got != second {
		t.Fatalf("mtime-only theme update changed URL: got %q want %q", got, second)
	}
}

func TestAssetHashThemeRenderAndActivateKeepTemplateAndAssetGenerationTogether(t *testing.T) {
	root := t.TempDir()
	writeThemeAssetHashFixture(t, root, "a", "site.css", []byte("a bytes"))
	writeThemeAssetHashFixture(t, root, "b", "site.css", []byte("b bytes"))
	for name, marker := range map[string]string{"a": "THEME_A", "b": "THEME_B"} {
		source := `{{define "content"}}` + marker + `|{{assetURL "/theme/assets/site.css"}}{{end}}`
		if err := os.WriteFile(filepath.Join(root, name, "index.html"), []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	m := NewManager(root)
	if err := m.Activate("a"); err != nil {
		t.Fatal(err)
	}
	aURL := m.AssetURL("/theme/assets/site.css")
	if err := m.Activate("b"); err != nil {
		t.Fatal(err)
	}
	bURL := m.AssetURL("/theme/assets/site.css")
	if aURL == bURL {
		t.Fatal("fixture themes unexpectedly share the same asset URL")
	}
	if err := m.Activate("a"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	activateDone := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(activateDone)
		for i := 0; i < 250; i++ {
			name := "b"
			if i%2 == 0 {
				name = "a"
			}
			if err := m.Activate(name); err != nil {
				t.Errorf("Activate(%s): %v", name, err)
				return
			}
		}
	}()
	for i := 0; i < 250; i++ {
		select {
		case <-activateDone:
			i = 250
		default:
		}
		out, err := m.Render("index.html", nil)
		if err != nil {
			t.Fatalf("Render during theme activation: %v", err)
		}
		got := string(out)
		if strings.Contains(got, "THEME_A") && strings.Contains(got, aURL) {
			continue
		}
		if strings.Contains(got, "THEME_B") && strings.Contains(got, bURL) {
			continue
		}
		t.Fatalf("render mixed theme template and asset generation: %q", got)
	}
	wg.Wait()
}
