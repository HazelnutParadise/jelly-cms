package assets

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAsset(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func serveAsset(t *testing.T, snap *Snapshot, method, rawURL string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, rawURL, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	snap.ServeHTTP(w, req)
	return w
}

func TestSnapshotURLAddsContentHashAndPreservesSuffix(t *testing.T) {
	dir := t.TempDir()
	body := "console.log('snapshot')"
	writeAsset(t, dir, "app.js", body)
	snap, err := Build(map[string]string{"/admin/assets/": dir})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	wantHash := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
	got := snap.URL("/admin/assets/app.js?lang=zh-TW#main")
	if got != "/admin/assets/app.js?lang=zh-TW&v="+wantHash+"#main" {
		t.Fatalf("URL() = %q, want original query and full SHA-256 %q", got, "/admin/assets/app.js?lang=zh-TW&v="+wantHash+"#main")
	}
	if len(wantHash) != 64 {
		t.Fatalf("test SHA-256 length = %d", len(wantHash))
	}
}

func TestSnapshotURLUnknownUnchanged(t *testing.T) {
	dir := t.TempDir()
	snap, err := Build(map[string]string{"/admin/assets/": dir})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, raw := range []string{"/other/foo.js?x=1#y", "https://cdn.example.com/x.js", "data:text/plain,x"} {
		if got := snap.URL(raw); got != raw {
			t.Errorf("URL(%q) = %q", raw, got)
		}
	}
}

func TestSnapshotBuildCapturesImmutableBytes(t *testing.T) {
	dir := t.TempDir()
	path := writeAsset(t, dir, "app.js", "old bytes")
	snap, err := Build(map[string]string{"/admin/assets/": dir})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	u := snap.URL("/admin/assets/app.js")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	w := serveAsset(t, snap, http.MethodGet, u, nil)
	if w.Code != http.StatusOK || w.Body.String() != "old bytes" {
		t.Fatalf("snapshot after source removal: status=%d body=%q", w.Code, w.Body.String())
	}
}

func TestSnapshotCSSRewritesAndServesFontDependency(t *testing.T) {
	dir := t.TempDir()
	writeAsset(t, dir, "font.woff2", "font bytes")
	writeAsset(t, dir, "style.css", "@font-face{src:url('font.woff2');}")
	snap, err := Build(map[string]string{"/admin/assets/": dir})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	cssURL := snap.URL("/admin/assets/style.css")
	cssResponse := serveAsset(t, snap, http.MethodGet, cssURL, nil)
	if cssResponse.Code != http.StatusOK {
		t.Fatalf("CSS response status = %d", cssResponse.Code)
	}
	css := cssResponse.Body.String()
	if !strings.Contains(css, "/admin/assets/font.woff2?v=") {
		t.Fatalf("CSS dependency was not rewritten to hashed URL: %q", css)
	}
	start := strings.Index(css, "/admin/assets/font.woff2?v=")
	end := strings.IndexAny(css[start:], "'\")")
	if end < 0 {
		t.Fatalf("cannot find rewritten font URL terminator in %q", css)
	}
	fontURL := css[start : start+end]
	fontResponse := serveAsset(t, snap, http.MethodGet, fontURL, nil)
	if fontResponse.Code != http.StatusOK || fontResponse.Body.String() != "font bytes" {
		t.Fatalf("font response: status=%d body=%q", fontResponse.Code, fontResponse.Body.String())
	}
}

func TestSnapshotCSSDependencyChangeChangesParentHash(t *testing.T) {
	dir := t.TempDir()
	fontPath := writeAsset(t, dir, "font.woff2", "font A")
	writeAsset(t, dir, "style.css", "body{src:url(font.woff2);}")
	first, err := Build(map[string]string{"/admin/assets/": dir})
	if err != nil {
		t.Fatal(err)
	}
	firstURL := first.URL("/admin/assets/style.css")
	info, err := os.Stat(fontPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fontPath, []byte("font B"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(fontPath, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	second, err := Build(map[string]string{"/admin/assets/": dir})
	if err != nil {
		t.Fatal(err)
	}
	if second.URL("/admin/assets/style.css") == firstURL {
		t.Fatalf("parent CSS URL did not change after font bytes changed")
	}
}

func TestSnapshotCyclicCSSImportReturnsError(t *testing.T) {
	dir := t.TempDir()
	writeAsset(t, dir, "a.css", "@import url('b.css');")
	writeAsset(t, dir, "b.css", "@import url('a.css');")
	if _, err := Build(map[string]string{"/admin/assets/": dir}); err == nil {
		t.Fatal("Build accepted cyclic CSS imports")
	}
}

func TestSnapshotRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeAsset(t, outside, "secret.txt", "secret")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "escape.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := Build(map[string]string{"/admin/assets/": root}); err == nil {
		t.Fatal("Build accepted a symlink escaping the mounted root")
	}
}

func TestSnapshotRejectsMissingMountRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	if _, err := Build(map[string]string{"/admin/assets/": root}); err == nil {
		t.Fatal("Build accepted a configured missing root")
	}
}

func TestSnapshotServeHasHashCacheAndContentETag(t *testing.T) {
	dir := t.TempDir()
	body := "console.log(1)"
	writeAsset(t, dir, "app.js", body)
	snap, err := Build(map[string]string{"/admin/assets/": dir})
	if err != nil {
		t.Fatal(err)
	}
	u := snap.URL("/admin/assets/app.js")
	w := serveAsset(t, snap, http.MethodGet, u, nil)
	if w.Code != http.StatusOK || w.Body.String() != body {
		t.Fatalf("GET: status=%d body=%q", w.Code, w.Body.String())
	}
	wantHash := fmt.Sprintf("%x", sha256.Sum256(w.Body.Bytes()))
	if !strings.Contains(u, "v="+wantHash) {
		t.Fatalf("URL hash does not match actual response bytes: %q", u)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "public") || !strings.Contains(cc, "max-age=31536000") || !strings.Contains(cc, "immutable") {
		t.Fatalf("hashed Cache-Control = %q", cc)
	}
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag")
	}
	conditional := serveAsset(t, snap, http.MethodGet, u, map[string]string{"If-None-Match": etag})
	if conditional.Code != http.StatusNotModified || conditional.Body.Len() != 0 {
		t.Fatalf("conditional GET: status=%d body=%q", conditional.Code, conditional.Body.String())
	}
	head := serveAsset(t, snap, http.MethodHead, u, nil)
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("ETag") != etag {
		t.Fatalf("HEAD: status=%d body=%q ETag=%q", head.Code, head.Body.String(), head.Header().Get("ETag"))
	}
}

func TestSnapshotUnversionedRevalidatesAndBadHashIsNeverCached(t *testing.T) {
	dir := t.TempDir()
	writeAsset(t, dir, "app.js", "current bytes")
	snap, err := Build(map[string]string{"/admin/assets/": dir})
	if err != nil {
		t.Fatal(err)
	}
	unversioned := serveAsset(t, snap, http.MethodGet, "/admin/assets/app.js", nil)
	if unversioned.Code != http.StatusOK || !strings.Contains(unversioned.Header().Get("Cache-Control"), "no-cache") {
		t.Fatalf("unversioned: status=%d Cache-Control=%q", unversioned.Code, unversioned.Header().Get("Cache-Control"))
	}
	for _, raw := range []string{"/admin/assets/app.js?v=deadbeef", "/admin/assets/app.js?v=" + strings.Repeat("0", 64)} {
		w := serveAsset(t, snap, http.MethodGet, raw, nil)
		if w.Code != http.StatusNotFound || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
			t.Errorf("bad hash %q: status=%d Cache-Control=%q", raw, w.Code, w.Header().Get("Cache-Control"))
		}
	}
}

func TestSnapshotDoesNotUseModificationTimeAsETag(t *testing.T) {
	dir := t.TempDir()
	path := writeAsset(t, dir, "app.js", "bytes A")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Build(map[string]string{"/admin/assets/": dir})
	if err != nil {
		t.Fatal(err)
	}
	firstURL := first.URL("/admin/assets/app.js")
	firstResponse := serveAsset(t, first, http.MethodGet, firstURL, nil)
	if err := os.WriteFile(path, []byte("bytes B"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	second, err := Build(map[string]string{"/admin/assets/": dir})
	if err != nil {
		t.Fatal(err)
	}
	secondURL := second.URL("/admin/assets/app.js")
	secondResponse := serveAsset(t, second, http.MethodGet, secondURL, map[string]string{"If-None-Match": firstResponse.Header().Get("ETag")})
	if secondURL == firstURL || secondResponse.Code != http.StatusOK || secondResponse.Body.String() != "bytes B" {
		t.Fatalf("same-mtime update mishandled: oldURL=%q newURL=%q status=%d body=%q", firstURL, secondURL, secondResponse.Code, secondResponse.Body.String())
	}
}
