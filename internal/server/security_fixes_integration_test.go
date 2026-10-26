package server

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
)

// secZipEntry is one file inside an in-memory plugin archive.
type secZipEntry struct {
	name string
	body []byte
}

// secZip builds a ZIP archive in memory. Names are written verbatim, so tests
// can use absolute and ".." names that a well-behaved zipper would never write.
func secZip(t *testing.T, entries ...secZipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		f, err := w.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Deflate})
		if err != nil {
			t.Fatalf("create zip entry %q: %v", e.name, err)
		}
		if _, err := f.Write(e.body); err != nil {
			t.Fatalf("write zip entry %q: %v", e.name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// secManifest returns a valid plugin.json for id.
func secManifest(t *testing.T, id string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"id": id, "name": "Security probe", "version": "1.0.0", "entrypoint": "main.js"})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	return raw
}

// secUploadPlugin posts archive bytes to /api/admin/plugins/upload as the
// multipart field "plugin", the way the admin screen does.
func secUploadPlugin(t *testing.T, h *accessHarness, token, filename string, archive []byte) accessResult {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("plugin", filename)
	if err != nil {
		t.Fatalf("create plugin form part: %v", err)
	}
	if _, err := part.Write(archive); err != nil {
		t.Fatalf("write plugin form part: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close plugin form: %v", err)
	}
	return h.requestReader(http.MethodPost, "/api/admin/plugins/upload", &body, token, "application/json", w.FormDataContentType())
}

// secTreeSnapshot lists every file under the isolated working directory with
// its content hash. The harness chdirs into that directory, so a write that
// escapes the plugins directory shows up as a difference.
func secTreeSnapshot(t *testing.T) string {
	t.Helper()
	var entries []string
	if err := filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		entries = append(entries, path+":"+hex.EncodeToString(sum[:]))
		return nil
	}); err != nil {
		t.Fatalf("snapshot working directory: %v", err)
	}
	sort.Strings(entries)
	return strings.Join(entries, "\n")
}

func TestPluginUploadRejectsTraversalID(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "sec-plugin-admin")
	token := h.token(admin)

	for _, id := range []string{"../../etc", "..", "a/b", "A B"} {
		t.Run(id, func(t *testing.T) {
			before := secTreeSnapshot(t)
			archive := secZip(t, secZipEntry{name: "plugin.json", body: secManifest(t, id)}, secZipEntry{name: "main.js", body: []byte("// probe")})

			result := secUploadPlugin(t, h, token, "probe.zip", archive)
			h.mustNoPanic(result, "plugin upload with id "+id)
			if result.code != http.StatusBadRequest {
				t.Errorf("upload with manifest id %q returned %d, want 400; body=%q", id, result.code, result.body)
			}
			if after := secTreeSnapshot(t); after != before {
				t.Errorf("upload with manifest id %q wrote files under the working directory:\nbefore:\n%s\nafter:\n%s", id, before, after)
			}
			for _, escaped := range []string{"etc", filepath.Join("data", "etc"), filepath.Join("data", "plugins", "etc")} {
				if _, err := os.Stat(escaped); err == nil {
					t.Errorf("upload with manifest id %q created %s", id, escaped)
				}
			}
		})
	}
}

func TestPluginUploadRejectsOversizeAndAbsoluteEntries(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "sec-oversize-admin")
	token := h.token(admin)

	t.Run("entry over 20 MiB", func(t *testing.T) {
		before := secTreeSnapshot(t)
		// Zeros compress to almost nothing, so the request itself stays small
		// while the extracted entry is one byte over the per-file cap.
		big := make([]byte, 20<<20+1)
		archive := secZip(t, secZipEntry{name: "plugin.json", body: secManifest(t, "oversize-probe")}, secZipEntry{name: "big.bin", body: big})

		result := secUploadPlugin(t, h, token, "oversize.zip", archive)
		h.mustNoPanic(result, "oversize plugin upload")
		if result.code != http.StatusRequestEntityTooLarge && result.code != http.StatusBadRequest {
			t.Errorf("oversize entry returned %d, want 413 or 400; body=%q", result.code, result.body)
		}
		if after := secTreeSnapshot(t); after != before {
			t.Errorf("oversize entry left files behind in the working directory")
		}
	})

	for _, name := range []string{"/etc/passwd", "../x"} {
		t.Run("entry "+name, func(t *testing.T) {
			before := secTreeSnapshot(t)
			archive := secZip(t,
				secZipEntry{name: "plugin.json", body: secManifest(t, "abs-probe")},
				secZipEntry{name: name, body: []byte("probe")},
			)

			result := secUploadPlugin(t, h, token, "abs.zip", archive)
			h.mustNoPanic(result, "plugin upload with entry "+name)
			if result.code != http.StatusBadRequest {
				t.Errorf("entry %q returned %d, want 400; body=%q", name, result.code, result.body)
			}
			if after := secTreeSnapshot(t); after != before {
				t.Errorf("entry %q wrote files outside the plugin directory", name)
			}
		})
	}
}

func TestUploadsServeNosniffAndAttachmentForNonImages(t *testing.T) {
	h := newAccessHarness(t)
	if err := os.MkdirAll(filepath.Join("data", "uploads"), 0o755); err != nil {
		t.Fatalf("create uploads directory: %v", err)
	}
	files := map[string][]byte{
		"sec-probe.svg":  []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(document.cookie)"/>`),
		"sec-probe.html": []byte(`<script>alert(document.cookie)</script>`),
		"sec-probe.png":  []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89"),
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join("data", "uploads", name), content, 0o644); err != nil {
			t.Fatalf("write upload %s: %v", name, err)
		}
	}

	for _, name := range []string{"sec-probe.svg", "sec-probe.html"} {
		result := h.request(http.MethodGet, "/uploads/"+name, "", "", "", "")
		h.mustNoPanic(result, "GET /uploads/"+name)
		if got := result.header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("GET /uploads/%s X-Content-Type-Options=%q, want nosniff", name, got)
		}
		if got := result.header.Get("Content-Disposition"); !strings.HasPrefix(got, "attachment") {
			t.Errorf("GET /uploads/%s Content-Disposition=%q, want attachment", name, got)
		}
		if got := result.header.Get("Content-Security-Policy"); !strings.Contains(got, "sandbox") {
			t.Errorf("GET /uploads/%s Content-Security-Policy=%q, want sandbox", name, got)
		}
	}

	result := h.request(http.MethodGet, "/uploads/sec-probe.png", "", "", "", "")
	h.mustNoPanic(result, "GET /uploads/sec-probe.png")
	if result.code != http.StatusOK {
		t.Fatalf("GET /uploads/sec-probe.png returned %d, want 200", result.code)
	}
	if got := result.header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("GET /uploads/sec-probe.png X-Content-Type-Options=%q, want nosniff", got)
	}
	if got := result.header.Get("Content-Disposition"); strings.HasPrefix(got, "attachment") {
		t.Errorf("GET /uploads/sec-probe.png Content-Disposition=%q, an image must render inline", got)
	}
	if got := result.header.Get(echo.HeaderContentType); got != "image/png" {
		t.Errorf("GET /uploads/sec-probe.png Content-Type=%q, want image/png", got)
	}
}

// secCommentRequest posts to the public comment endpoint from remote, with an
// optional X-Forwarded-For header. The body has no post_id, so the handler
// rejects it after the rate limiter has counted the attempt.
func secCommentRequest(h *accessHarness, remote, forwarded string) accessResult {
	req := httptest.NewRequest(http.MethodPost, "/api/comments", strings.NewReader("{}"))
	req.RemoteAddr = remote
	if forwarded != "" {
		req.Header.Set("X-Forwarded-For", forwarded)
	}
	req.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	var panicked any
	func() {
		defer func() { panicked = recover() }()
		h.echo.ServeHTTP(rec, req)
	}()
	return accessResult{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone(), panicked: panicked}
}

func TestRealIPHonoursTrustedProxiesOnly(t *testing.T) {
	t.Run("untrusted without JELLY_TRUSTED_PROXIES", func(t *testing.T) {
		t.Setenv("JELLY_TRUSTED_PROXIES", "")
		h := newAccessHarness(t)
		// One connection, six posts, a different forwarded address each time.
		// The limiter must key on the connection because XFF cannot be trusted.
		remote := commentsNextIP() + ":40000"
		var last accessResult
		for i := 1; i <= 6; i++ {
			last = secCommentRequest(h, remote, fmt.Sprintf("198.51.100.%d", i))
			h.mustNoPanic(last, "forged comment post")
		}
		if last.code != http.StatusTooManyRequests {
			t.Errorf("sixth post from one connection with forged X-Forwarded-For returned %d, want 429", last.code)
		}
	})

	t.Run("trusted with JELLY_TRUSTED_PROXIES=127.0.0.0/8", func(t *testing.T) {
		t.Setenv("JELLY_TRUSTED_PROXIES", "127.0.0.0/8")
		h := newAccessHarness(t)
		// The proxy is loopback, so each forwarded client is a separate limiter key.
		for i := 1; i <= 6; i++ {
			forwarded := commentsNextIP()
			result := secCommentRequest(h, "127.0.0.1:40000", forwarded)
			h.mustNoPanic(result, "proxied comment post")
			if result.code == http.StatusTooManyRequests {
				t.Errorf("post %d for forwarded client %s returned 429; X-Forwarded-For must be honoured from a trusted proxy", i, forwarded)
			}
		}
	})
}

func TestBodyLimitRejectsLargeJSONButAllowsUploads(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "sec-body-admin")
	token := h.token(admin)

	const threeMiB = 3 << 20
	payload := `{"title":"big","content":"` + strings.Repeat("a", threeMiB) + `","type":"post","status":"draft"}`
	result := h.request(http.MethodPost, "/api/admin/posts", payload, token, "application/json", "application/json")
	h.mustNoPanic(result, "3 MiB JSON post")
	if result.code != http.StatusRequestEntityTooLarge {
		t.Errorf("3 MiB JSON to POST /api/admin/posts returned %d, want 413", result.code)
	}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", "sec-body-probe.bin")
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := io.Copy(part, bytes.NewReader(make([]byte, threeMiB))); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart body: %v", err)
	}
	upload := h.requestReader(http.MethodPost, "/api/admin/media", &body, token, "application/json", w.FormDataContentType())
	h.mustNoPanic(upload, "3 MiB multipart upload")
	if upload.code == http.StatusRequestEntityTooLarge {
		t.Errorf("3 MiB multipart to POST /api/admin/media returned 413; upload routes must keep their own, larger limit")
	}
}

func TestSettingsGetMasksSecretsAndPostRejectsUnknownKeys(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "sec-settings-admin")
	token := h.token(admin)
	if err := h.db.Create(&core.Option{Key: "payment_ecpay_hash_key", Value: "abcdef1234"}).Error; err != nil {
		t.Fatalf("seed secret option: %v", err)
	}

	get := h.request(http.MethodGet, "/api/admin/settings", "", token, "application/json", "")
	h.mustNoPanic(get, "GET /api/admin/settings")
	if get.code != http.StatusOK {
		t.Fatalf("GET /api/admin/settings returned %d, want 200; body=%q", get.code, get.body)
	}
	if strings.Contains(get.body, "abcdef1234") {
		t.Errorf("GET /api/admin/settings returned the plaintext secret")
	}
	if !strings.Contains(get.body, "****1234") {
		t.Errorf("GET /api/admin/settings did not mask payment_ecpay_hash_key as ****1234; body=%q", get.body)
	}

	unknown := h.request(http.MethodPost, "/api/admin/settings", `{"totally_unknown":"x"}`, token, "application/json", "application/json")
	h.mustNoPanic(unknown, "POST unknown settings key")
	if unknown.code != http.StatusBadRequest {
		t.Errorf("POST with unregistered key returned %d, want 400; body=%q", unknown.code, unknown.body)
	}

	known := h.request(http.MethodPost, "/api/admin/settings", `{"site_title":"Security probe"}`, token, "application/json", "application/json")
	h.mustNoPanic(known, "POST registered settings key")
	if known.code != http.StatusOK {
		t.Fatalf("POST with registered key returned %d, want 200; body=%q", known.code, known.body)
	}
	var audited int64
	if err := h.db.Raw("SELECT COUNT(*) FROM system_logs WHERE source = 'audit'").Scan(&audited).Error; err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if audited == 0 {
		t.Errorf("registered settings write left no audit line in system_logs")
	}
}
