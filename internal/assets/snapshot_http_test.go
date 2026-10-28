package assets

import (
	"net/http"
	"strings"
	"testing"
)

func TestSnapshotHeadIgnoresRange(t *testing.T) {
	dir := t.TempDir()
	writeAsset(t, dir, "app.js", "0123456789")
	snap, err := Build(map[string]string{"/assets/": dir})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	assetURL := snap.URL("/assets/app.js")

	for _, rangeHeader := range []string{"bytes=1-3", "bytes=100-"} {
		t.Run(rangeHeader, func(t *testing.T) {
			w := serveAsset(t, snap, http.MethodHead, assetURL, map[string]string{"Range": rangeHeader})
			if w.Code != http.StatusOK {
				t.Errorf("HEAD status = %d, want %d", w.Code, http.StatusOK)
			}
			if got := w.Result().ContentLength; got != 10 {
				t.Errorf("HEAD Content-Length = %d, want 10", got)
			}
			if got := w.Header().Get("Content-Range"); got != "" {
				t.Errorf("HEAD Content-Range = %q, want empty", got)
			}
			if got := w.Body.String(); got != "" {
				t.Errorf("HEAD body = %q, want empty", got)
			}
		})
	}

	get := serveAsset(t, snap, http.MethodGet, assetURL, map[string]string{"Range": "bytes=1-3"})
	if get.Code != http.StatusPartialContent {
		t.Errorf("GET range status = %d, want %d", get.Code, http.StatusPartialContent)
	}
	if got := get.Body.String(); got != "123" {
		t.Errorf("GET range body = %q, want %q", got, "123")
	}
	if got := get.Header().Get("Content-Range"); got != "bytes 1-3/10" {
		t.Errorf("GET Content-Range = %q, want %q", got, "bytes 1-3/10")
	}

	etag := get.Header().Get("ETag")
	if etag == "" {
		t.Fatal("GET response has no ETag")
	}
	conditional := serveAsset(t, snap, http.MethodHead, assetURL, map[string]string{
		"Range":         "bytes=1-3",
		"If-None-Match": etag,
	})
	if conditional.Code != http.StatusNotModified {
		t.Errorf("conditional HEAD status = %d, want %d", conditional.Code, http.StatusNotModified)
	}
	if body := conditional.Body.String(); body != "" {
		t.Errorf("conditional HEAD body = %q, want empty", strings.TrimSpace(body))
	}
}
