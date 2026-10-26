package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

const (
	followupBrowserEnv       = "JELLY_FOLLOWUP_BROWSER"
	followupBrowserInfoPath  = "/tmp/jelly29-fixture.json"
	followupBrowserTokenPath = "/tmp/jelly29-fixture-token"
	followupBrowserStopPath  = "/tmp/jelly29-browser-stop"
)

type followupBrowserFixtureInfo struct {
	URL          string   `json:"url"`
	ControlPath  string   `json:"control_path"`
	StopNonce    string   `json:"stop_nonce"`
	PostPaths    []string `json:"post_paths"`
	StaticPath   string   `json:"static_path"`
	SearchQuery  string   `json:"search_query"`
	FixtureLabel string   `json:"fixture_label"`
}

func TestFollowupBrowserFixture(t *testing.T) {
	if os.Getenv(followupBrowserEnv) != "1" {
		t.Skip("set JELLY_FOLLOWUP_BROWSER=1 to run the local Chromium acceptance fixture")
	}
	for _, path := range []string{followupBrowserInfoPath, followupBrowserTokenPath} {
		if _, err := os.Lstat(path); err == nil {
			t.Fatalf("refusing to replace existing fixture file %s", path)
		} else if !os.IsNotExist(err) {
			t.Fatalf("inspect fixture file %s: %v", path, err)
		}
	}

	h := archivesHarness(t)
	workDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("read isolated fixture work directory: %v", err)
	}
	themeFontDir := filepath.Join(workDir, "web", "themes", "default", "assets", "fonts")
	if _, statErr := os.Stat(themeFontDir); statErr == nil {
		t.Fatalf("theme font copy still exists in fixture workspace: %s", themeFontDir)
	} else if !os.IsNotExist(statErr) {
		t.Fatalf("inspect theme font copy in isolated fixture: %v", statErr)
	}
	author := h.user("author", "theme-followup-browser")
	pubThemeSetOption(t, h, "site_description", "A short fixture description for contrast testing.")
	categories := []core.Category{
		pubThemeCategory(t, h, "Fixture Notes", "fixture-notes", nil, 1, false),
		pubThemeCategory(t, h, "Fixture Guides", "fixture-guides", nil, 2, false),
	}
	postPaths := make([]string, 0, 7)
	base := time.Date(2025, time.June, 1, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= 7; i++ {
		content := fmt.Sprintf(`<p>Fixture article %02d has searchable phrase jelly fixture.</p><h2>Section %02d</h2><p>Reading content for section %02d.</p><h3>Details %02d</h3><p>More fixture detail.</p><h2>More reading %02d</h2><p>Additional fixture content.</p><h3>Further details %02d</h3><p>Final fixture detail.</p>`, i, i, i, i, i, i)
		spec := pubThemePost{title: fmt.Sprintf("Fixture Article %02d", i), slug: fmt.Sprintf("fixture-article-%02d", i), content: content, publishedAt: base.Add(time.Duration(i) * 24 * time.Hour), categories: []core.Category{categories[(i-1)%len(categories)]}}
		if i == 1 {
			spec.cover = "/admin/assets/jelly-mark.svg"
		}
		if i == 2 {
			spec.content += `<p><img src="/admin/assets/jelly-mark.svg" alt="Fixture inline image"></p>`
		}
		pubThemeSeedPost(t, h, author, spec)
		postPaths = append(postPaths, "/post/"+spec.slug)
	}
	static := pubThemeSeedPost(t, h, author, pubThemePost{
		title: "Fixture Static Page", slug: "fixture-static", kind: "page", content: "<p>Static page fixture body.</p>", publishedAt: base.Add(10 * 24 * time.Hour),
	})
	if static.Slug == "" {
		t.Fatal("static page fixture has no slug")
	}

	token, err := followupBrowserSecret()
	if err != nil {
		t.Fatalf("generate fixture control token: %v", err)
	}
	nonce, err := followupBrowserSecret()
	if err != nil {
		t.Fatalf("generate fixture stop nonce: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/__fixture/maintenance" {
			h.echo.ServeHTTP(w, r)
			return
		}
		remote, _, splitErr := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(remote)
		if splitErr != nil || ip == nil || !ip.IsLoopback() {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if len(provided) != len(token) || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var input struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&input); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		value := "false"
		if input.Enabled {
			value = "true"
		}
		if err := h.db.Save(&core.Option{Key: maintenanceEnabledKey, Value: value}).Error; err != nil {
			http.Error(w, "fixture update failed", http.StatusInternalServerError)
			return
		}
		if input.Enabled {
			if err := h.db.Save(&core.Option{Key: maintenanceStartedKey, Value: time.Date(2025, time.June, 2, 16, 0, 0, 0, time.UTC).Format(time.RFC3339)}).Error; err != nil {
				http.Error(w, "fixture update failed", http.StatusInternalServerError)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	info := followupBrowserFixtureInfo{URL: server.URL, ControlPath: "/__fixture/maintenance", StopNonce: nonce,
		PostPaths: postPaths, StaticPath: "/" + static.Slug, SearchQuery: "jelly fixture", FixtureLabel: "7 posts, 2 categories, cover, inline image, and static page"}
	if err := followupBrowserWritePrivate(followupBrowserTokenPath, []byte(token)); err != nil {
		t.Fatalf("write fixture control token: %v", err)
	}
	encoded, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		t.Fatalf("encode fixture metadata: %v", err)
	}
	if err := followupBrowserWritePrivate(followupBrowserInfoPath, encoded); err != nil {
		t.Fatalf("write fixture metadata: %v", err)
	}
	t.Cleanup(func() {
		for path, expected := range map[string][]byte{followupBrowserInfoPath: encoded, followupBrowserTokenPath: []byte(token)} {
			if current, readErr := os.ReadFile(path); readErr == nil && subtle.ConstantTimeCompare(current, expected) == 1 {
				_ = os.Remove(path)
			}
		}
		if marker, readErr := os.ReadFile(followupBrowserStopPath); readErr == nil && subtle.ConstantTimeCompare(marker, []byte(nonce)) == 1 {
			_ = os.Remove(followupBrowserStopPath)
		}
	})

	t.Logf("browser fixture ready at %s; stop by writing the supplied nonce to %s", server.URL, followupBrowserStopPath)
	deadline := time.Now().Add(20 * time.Minute)
	for time.Now().Before(deadline) {
		if marker, readErr := os.ReadFile(followupBrowserStopPath); readErr == nil && subtle.ConstantTimeCompare(marker, []byte(nonce)) == 1 {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("browser fixture stop signal not received within 20 minutes")
}

func followupBrowserSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func followupBrowserWritePrivate(path string, body []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(body); err != nil {
		_ = os.Remove(path)
		return err
	}
	return f.Sync()
}
