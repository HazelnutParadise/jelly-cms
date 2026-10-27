package server

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// stoCatalog is a signed store catalog served from a fake host. The signature
// is over catalog; /tampered.json carries different bytes under the original
// signature.
type stoCatalog struct {
	srv     *httptest.Server
	signer  updSigner
	catalog string
}

const stoCatalogBody = `{"items":[` +
	`{"id":"hello-search","name":"Hello Search","kind":"plugin","category":"search","version":"1.0.0","official":false,"permissions":["http.fetch","store"]},` +
	`{"id":"seo-tools","name":"SEO Tools","kind":"plugin","category":"seo","version":"2.0.0","official":true,"permissions":["settings"]}` +
	`]}`

func stoServeCatalog(t *testing.T, catalog string) *stoCatalog {
	t.Helper()
	s := updNewSigner(t)
	c := &stoCatalog{signer: s, catalog: catalog}
	sig := s.sign([]byte(catalog))
	tampered := strings.Replace(catalog, "Hello Search", "Evil Search", 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/catalog.json", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(catalog)) })
	mux.HandleFunc("/catalog.json.minisig", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(sig)) })
	mux.HandleFunc("/tampered.json", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tampered)) })
	mux.HandleFunc("/tampered.json.minisig", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(sig)) })
	c.srv = httptest.NewServer(mux)
	t.Cleanup(c.srv.Close)
	t.Setenv("JELLY_TEST_ALLOW_LOOPBACK", "1")
	t.Setenv("JELLY_STORE_PUBLIC_KEY", s.publicKeyLine())
	t.Setenv("JELLY_STORE_CATALOG_URL", c.srv.URL+"/catalog.json")
	return c
}

func TestStoreCatalogRefreshAndSearch(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "sto-catalog-admin")
	token := h.token(admin)
	cat := stoServeCatalog(t, stoCatalogBody)

	if r := updCall(h, http.MethodPost, "/api/admin/store/refresh", "", token); r.code < 200 || r.code > 299 {
		t.Fatalf("POST /api/admin/store/refresh = %d, want 2xx: %s", r.code, r.body)
	}

	q := updCall(h, http.MethodGet, "/api/admin/store?q=hello", "", token)
	if q.code != http.StatusOK || !strings.Contains(q.body, "hello-search") || strings.Contains(q.body, "seo-tools") {
		t.Errorf("search q=hello = %d %s, want only hello-search", q.code, q.body)
	}
	c := updCall(h, http.MethodGet, "/api/admin/store?category=seo", "", token)
	if c.code != http.StatusOK || !strings.Contains(c.body, "seo-tools") || strings.Contains(c.body, "hello-search") {
		t.Errorf("category=seo = %d %s, want only seo-tools", c.code, c.body)
	}
	inst := updCall(h, http.MethodGet, "/api/admin/store?installed=true", "", token)
	if inst.code != http.StatusOK || strings.Contains(inst.body, "hello-search") {
		t.Errorf("installed=true lists uninstalled items: %d %s", inst.code, inst.body)
	}
	upd := updCall(h, http.MethodGet, "/api/admin/store?updates=true", "", token)
	if upd.code != http.StatusOK {
		t.Errorf("updates=true = %d, want 200", upd.code)
	}

	// A catalog whose bytes do not match its signature must not replace the
	// stored one.
	t.Setenv("JELLY_STORE_CATALOG_URL", cat.srv.URL+"/tampered.json")
	if r := updCall(h, http.MethodPost, "/api/admin/store/refresh", "", token); r.code < 400 {
		t.Errorf("refresh with a tampered catalog = %d, want an error status", r.code)
	}
	after := updCall(h, http.MethodGet, "/api/admin/store?q=hello", "", token)
	if strings.Contains(after.body, "Evil Search") {
		t.Error("tampered catalog content reached the store listing")
	}
}

func TestStoreInstallRequiresAcceptedPermissionsHash(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "sto-install-admin")
	token := h.token(admin)
	stoServeCatalog(t, stoCatalogBody)
	if r := updCall(h, http.MethodPost, "/api/admin/store/refresh", "", token); r.code < 200 || r.code > 299 {
		t.Fatalf("refresh = %d: %s", r.code, r.body)
	}

	if r := updCall(h, http.MethodPost, "/api/admin/store/install", `{"id":"hello-search","accept_permissions":"0000000000000000000000000000000000000000000000000000000000000000"}`, token); r.code != http.StatusBadRequest {
		t.Errorf("install with a wrong permissions hash = %d, want 400: %s", r.code, r.body)
	}
	if r := updCall(h, http.MethodPost, "/api/admin/store/install", `{"id":"hello-search"}`, token); r.code != http.StatusBadRequest {
		t.Errorf("install of a third-party item without accepting permissions = %d, want 400: %s", r.code, r.body)
	}
}

func stoZip(t *testing.T, manifest string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(manifest))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPluginInstallURLBlocksPrivateHostsAndVerifiesSha256(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "sto-url-admin")
	token := h.token(admin)

	for _, raw := range []string{"http://127.0.0.1:1/p.zip", "http://169.254.169.254/latest/meta-data/p.zip", "http://[::1]:1/p.zip"} {
		r := updCall(h, http.MethodPost, "/api/admin/plugins/install-url", fmt.Sprintf(`{"url":%q}`, raw), token)
		if r.code != http.StatusBadRequest && r.code != http.StatusForbidden {
			t.Errorf("install-url %s = %d, want 400 or 403 (private host blocked)", raw, r.code)
		}
	}

	var hits atomic.Int64
	archive := stoZip(t, `{"id":"fixture-plg","name":"Fixture","version":"1.0.0"}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write(archive)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("JELLY_TEST_ALLOW_LOOPBACK", "1")

	wrong := fmt.Sprintf(`{"url":%q,"sha256":"%s"}`, srv.URL+"/p.zip", strings.Repeat("0", 64))
	if r := updCall(h, http.MethodPost, "/api/admin/plugins/install-url", wrong, token); r.code != http.StatusBadRequest {
		t.Errorf("install-url with a wrong sha256 = %d, want 400: %s", r.code, r.body)
	}
	if n := impCount(t, h, "SELECT COUNT(*) FROM plugins WHERE id = 'fixture-plg'"); n != 0 {
		t.Errorf("plugin installed despite sha256 mismatch: %d rows", n)
	}
	if hits.Load() == 0 {
		t.Error("fake plugin host was never fetched; the allowed-loopback seam did not reach the download")
	}
}
