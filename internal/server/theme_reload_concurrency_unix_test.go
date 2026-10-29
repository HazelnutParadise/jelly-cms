//go:build unix

package server

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

func TestThemeReloadSettingsCannotOverwriteConcurrentActivation(t *testing.T) {
	h := newWebMCPHarness(t)
	if err := os.MkdirAll(filepath.Join("web", "themes", webmcpAltTheme, "assets"), 0755); err != nil {
		t.Fatal(err)
	}
	cssBytes := []byte(".webmcp-alt-theme{color:#123456}")
	if err := os.WriteFile(filepath.Join("web", "themes", webmcpAltTheme, "assets", "content.css"), cssBytes, 0644); err != nil {
		t.Fatal(err)
	}
	jsonPath := filepath.Join("web", "themes", "default", "theme.json")
	original, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(jsonPath); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(jsonPath, 0600); err != nil {
		t.Fatal(err)
	}
	opened, release, stop := make(chan error, 1), make(chan struct{}), make(chan struct{})
	writerResult, writerDone, requestDone := make(chan error, 1), make(chan struct{}), make(chan struct{})
	var releaseOnce, stopOnce sync.Once
	cleanupRelease := func() { releaseOnce.Do(func() { close(release) }) }
	cleanupStop := func() { stopOnce.Do(func() { close(stop) }) }
	finished := make(chan accessResult, 1)
	t.Cleanup(func() {
		fd, rendezvousErr := syscall.Open(jsonPath, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
		if rendezvousErr == nil {
			defer syscall.Close(fd)
		}
		cleanupRelease()
		if e := serverReplaceFile(jsonPath, original); e != nil {
			t.Errorf("restore theme.json during cleanup: %v", e)
		}
		cleanupStop()
		select {
		case <-writerDone:
		case <-time.After(5 * time.Second):
			t.Errorf("FIFO writer did not stop during cleanup")
		}
		select {
		case <-requestDone:
		case <-time.After(5 * time.Second):
			t.Errorf("settings request did not stop during cleanup")
		}
	})
	go func() {
		defer close(writerDone)
		fd, e := serverOpenFIFOWriter(jsonPath, stop)
		opened <- e
		if e == nil {
			f := os.NewFile(uintptr(fd), jsonPath)
			<-release
			_, e = f.Write(original)
			ce := f.Close()
			if e == nil {
				e = ce
			}
		}
		writerResult <- e
	}()
	go func() {
		defer close(requestDone)
		finished <- h.adminJSON(http.MethodPost, "/api/admin/themes/default/settings", `{"colors":{"primary":"#123456"},"layout":{},"custom":{}}`)
	}()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case e := <-opened:
		if e != nil {
			t.Fatal(e)
		}
	case <-timer.C:
		t.Fatal("settings reload did not enter FIFO theme.json")
	}
	if err := serverReplaceFile(jsonPath, original); err != nil {
		t.Fatal(err)
	}
	activated := h.adminJSON(http.MethodPost, "/api/admin/themes/activate", `{"name":"webmcp-alt"}`)
	if activated.code != http.StatusOK {
		t.Fatalf("activate B returned %d: %s", activated.code, archivesSnippet(activated.body))
	}
	bSnapshot := h.tm.AssetSnapshot()
	bURL := h.tm.AssetURL("/theme/assets/content.css")
	cleanupRelease()
	select {
	case e := <-writerResult:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("FIFO writer did not finish")
	}
	select {
	case r := <-finished:
		if r.code != http.StatusOK {
			t.Fatalf("settings response = %d: %s", r.code, archivesSnippet(r.body))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("settings request did not finish")
	}
	if h.tm.GetActive() != webmcpAltTheme || h.tm.AssetSnapshot() != bSnapshot || h.tm.AssetURL("/theme/assets/content.css") != bURL {
		t.Fatal("stale settings reload replaced theme B runtime or snapshot")
	}
	var selected core.Option
	if err := h.db.Where("key = ?", "active_theme").First(&selected).Error; err != nil || selected.Value != webmcpAltTheme {
		t.Fatalf("saved active theme=%q err=%v", selected.Value, err)
	}
	var saved core.ThemeSettings
	if err := h.db.Where("theme_name = ?", "default").First(&saved).Error; err != nil {
		t.Fatalf("settings A colors=%s err=%v", saved.Colors, err)
	}
	var colors map[string]any
	if err := json.Unmarshal(saved.Colors, &colors); err != nil || len(colors) != 1 || colors["primary"] != "#123456" {
		t.Fatalf("settings A colors=%s decoded=%v err=%v", saved.Colors, colors, err)
	}
	css := httptest.NewRecorder()
	h.tm.AssetSnapshot().ServeHTTP(css, httptest.NewRequest(http.MethodGet, bURL, nil))
	parsed, parseErr := url.Parse(bURL)
	sum := sha256.Sum256(cssBytes)
	expectedHash := fmt.Sprintf("%x", sum)
	if css.Code != http.StatusOK || css.Body.String() != string(cssBytes) || parseErr != nil || parsed.Query().Get("v") != expectedHash {
		t.Fatalf("B CSS response status=%d body=%q URL=%q expected hash=%s", css.Code, css.Body.String(), bURL, expectedHash)
	}
	body, err := h.tm.Render("index.html", nil)
	if err != nil || !strings.Contains(string(body), webmcpAltMarker) {
		t.Fatalf("rendered active theme B = %q err=%v", body, err)
	}
}

func serverReplaceFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".theme-json-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func serverOpenFIFOWriter(path string, stop <-chan struct{}) (int, error) {
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			return fd, nil
		}
		if !errors.Is(err, syscall.ENXIO) && !errors.Is(err, syscall.ENOENT) {
			return -1, err
		}
		select {
		case <-stop:
			return -1, errors.New("FIFO writer cancelled")
		case <-deadline.C:
			return -1, errors.New("timed out waiting for FIFO reader")
		case <-tick.C:
		}
	}
}
