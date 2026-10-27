package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/update"
	"github.com/HazelnutParadise/jelly-cms/internal/version"
	"github.com/labstack/echo/v4"
)

// The update and store tests use these test-only seams, which the
// implementation must honour only when set by a test run:
//
//	JELLY_TEST_ALLOW_LOOPBACK=1  allow safehttp to dial 127.0.0.1 (fake servers)
//	JELLY_UPDATE_API_BASE        replaces https://api.github.com (also for asset URLs)
//	JELLY_UPDATE_PUBLIC_KEY      replaces the embedded minisign public key (base64 line)
//	JELLY_UPDATE_EXECUTABLE      replaces os.Executable() as the file to swap
//	JELLY_STORE_CATALOG_URL      replaces the official store catalog URL
//	JELLY_STORE_PUBLIC_KEY       replaces the store's minisign public key

// updSigner produces minisign-compatible signatures (legacy "Ed" algorithm).
type updSigner struct {
	pub   ed25519.PublicKey
	priv  ed25519.PrivateKey
	keyID [8]byte
}

func updNewSigner(t *testing.T) updSigner {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s := updSigner{pub: pub, priv: priv}
	if _, err := rand.Read(s.keyID[:]); err != nil {
		t.Fatal(err)
	}
	return s
}

// publicKeyLine is the base64 line of a minisign public key file.
func (s updSigner) publicKeyLine() string {
	return base64.StdEncoding.EncodeToString(append(append([]byte("Ed"), s.keyID[:]...), s.pub...))
}

// sign returns a minisign signature file for data.
func (s updSigner) sign(data []byte) string {
	sig := ed25519.Sign(s.priv, data)
	raw := append(append([]byte("Ed"), s.keyID[:]...), sig...)
	trusted := "timestamp:1700000000\tfile:fixture"
	global := ed25519.Sign(s.priv, append(append([]byte{}, sig...), []byte(trusted)...))
	return "untrusted comment: signature from minisign secret key\n" +
		base64.StdEncoding.EncodeToString(raw) + "\n" +
		"trusted comment: " + trusted + "\n" +
		base64.StdEncoding.EncodeToString(global) + "\n"
}

type updTarEntry struct {
	Name string
	Body []byte
}

// updTarGz builds a gzip-compressed tar with the given entries, in order.
func updTarGz(t *testing.T, entries ...updTarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: e.Name, Mode: 0o755, Size: int64(len(e.Body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.Body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func updPlatformArchive() string {
	return fmt.Sprintf("jelly-cms_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
}

// updAssets returns the release files for archive. checksumOverride, when
// set, replaces the sha256 line; signatureOverride replaces the minisig file.
func updAssets(s updSigner, archive []byte, checksumOverride, signatureOverride string) map[string][]byte {
	sum := sha256.Sum256(archive)
	hexSum := hex.EncodeToString(sum[:])
	if checksumOverride != "" {
		hexSum = checksumOverride
	}
	sums := []byte(hexSum + "  " + updPlatformArchive() + "\n")
	sig := s.sign(sums)
	if signatureOverride != "" {
		sig = signatureOverride
	}
	return map[string][]byte{
		updPlatformArchive():    archive,
		"checksums.txt":         sums,
		"checksums.txt.minisig": []byte(sig),
	}
}

type updRelease struct {
	Tag        string
	Prerelease bool
	Assets     map[string][]byte
}

// updFakeGitHub stands in for api.github.com and the release download host.
type updFakeGitHub struct {
	srv      *httptest.Server
	releases []updRelease
	mu       sync.Mutex
	seen     []*http.Request
}

func updNewFakeGitHub(t *testing.T, releases ...updRelease) *updFakeGitHub {
	t.Helper()
	f := &updFakeGitHub{releases: releases}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/HazelnutParadise/jelly-cms/releases", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.writeReleases(w, f.releases)
	})
	mux.HandleFunc("/repos/HazelnutParadise/jelly-cms/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		for _, rel := range f.releases {
			if !rel.Prerelease {
				f.writeReleases(w, []updRelease{rel}, true)
				return
			}
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/download/", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		rest := strings.TrimPrefix(r.URL.Path, "/download/")
		for _, rel := range f.releases {
			if strings.HasPrefix(rest, rel.Tag+"/") {
				if body, ok := rel.Assets[strings.TrimPrefix(rest, rel.Tag+"/")]; ok {
					_, _ = w.Write(body)
					return
				}
			}
		}
		http.NotFound(w, r)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *updFakeGitHub) record(r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, r.Clone(r.Context()))
}

func (f *updFakeGitHub) requests() []*http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*http.Request(nil), f.seen...)
}

func (f *updFakeGitHub) writeReleases(w http.ResponseWriter, releases []updRelease, single ...bool) {
	type asset struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int    `json:"size"`
	}
	type release struct {
		TagName     string  `json:"tag_name"`
		Prerelease  bool    `json:"prerelease"`
		PublishedAt string  `json:"published_at"`
		Assets      []asset `json:"assets"`
	}
	out := make([]release, 0, len(releases))
	for _, rel := range releases {
		item := release{TagName: rel.Tag, Prerelease: rel.Prerelease, PublishedAt: "2026-10-01T00:00:00Z"}
		for name, body := range rel.Assets {
			item.Assets = append(item.Assets, asset{Name: name, BrowserDownloadURL: f.srv.URL + "/download/" + rel.Tag + "/" + name, Size: len(body)})
		}
		out = append(out, item)
	}
	w.Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if len(single) > 0 && single[0] {
		_ = json.NewEncoder(w).Encode(out[0])
		return
	}
	_ = json.NewEncoder(w).Encode(out)
}

// updCall sends a request with the admin session cookie.
func updCall(h *accessHarness, method, path, body, token string) accessResult {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-Jelly-CSRF", "1")
	if body != "" {
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	req.AddCookie(&http.Cookie{Name: "jwt_token", Value: token})
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	return accessResult{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone()}
}

// updSetVersion sets the running version for one test.
func updSetVersion(t *testing.T, v string) {
	t.Helper()
	prev := version.Version
	version.Version = v
	t.Cleanup(func() { version.Version = prev })
}

// updExecutable writes the file that Apply swaps and returns its path.
func updExecutable(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "jelly-cms")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func updReadFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// updSeams points the update client at the fake and the executable at exe.
func updSeams(t *testing.T, fake *updFakeGitHub, signer updSigner, exe string) {
	t.Helper()
	t.Setenv("JELLY_TEST_ALLOW_LOOPBACK", "1")
	t.Setenv("JELLY_UPDATE_API_BASE", fake.srv.URL)
	t.Setenv("JELLY_UPDATE_PUBLIC_KEY", signer.publicKeyLine())
	t.Setenv("JELLY_UPDATE_EXECUTABLE", exe)
}

func TestUpdateStatusShape(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "upd-shape-admin")
	editor := h.user("editor", "upd-shape-editor")

	res := updCall(h, http.MethodGet, "/api/admin/update", "", h.token(admin))
	if res.code != http.StatusOK {
		t.Fatalf("GET /api/admin/update = %d, want 200: %s", res.code, res.body)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(res.body), &body); err != nil {
		t.Fatalf("status is not a JSON object: %v", err)
	}
	for _, key := range []string{"current", "latest", "checked_at", "runtime", "can_update", "commands", "channel", "state"} {
		if _, ok := body[key]; !ok {
			t.Errorf("status lacks key %q: %s", key, res.body)
		}
	}
	if res := updCall(h, http.MethodGet, "/api/admin/update", "", h.token(editor)); res.code != http.StatusForbidden {
		t.Errorf("editor GET /api/admin/update = %d, want 403", res.code)
	}
}

func TestUpdateCheckUsesFakeGitHubAndChannel(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "upd-channel-admin")
	token := h.token(admin)
	fake := updNewFakeGitHub(t,
		updRelease{Tag: "v1.2.0-rc.1", Prerelease: true},
		updRelease{Tag: "v1.1.0"},
	)
	t.Setenv("JELLY_TEST_ALLOW_LOOPBACK", "1")
	t.Setenv("JELLY_UPDATE_API_BASE", fake.srv.URL)

	if r := updCall(h, http.MethodPut, "/api/admin/update/settings", `{"channel":"stable"}`, token); r.code != http.StatusOK {
		t.Fatalf("PUT settings stable = %d: %s", r.code, r.body)
	}
	if r := updCall(h, http.MethodPost, "/api/admin/update/check", "", token); r.code != http.StatusOK {
		t.Fatalf("check on stable = %d: %s", r.code, r.body)
	}
	stable := updCall(h, http.MethodGet, "/api/admin/update", "", token)
	if !strings.Contains(stable.body, "1.1.0") || strings.Contains(stable.body, "rc.1") {
		t.Errorf("stable channel latest should be 1.1.0 without prereleases: %s", stable.body)
	}

	if r := updCall(h, http.MethodPut, "/api/admin/update/settings", `{"channel":"preview"}`, token); r.code != http.StatusOK {
		t.Fatalf("PUT settings preview = %d: %s", r.code, r.body)
	}
	if r := updCall(h, http.MethodPost, "/api/admin/update/check", "", token); r.code != http.StatusOK {
		t.Fatalf("check on preview = %d: %s", r.code, r.body)
	}
	preview := updCall(h, http.MethodGet, "/api/admin/update", "", token)
	if !strings.Contains(preview.body, "1.2.0-rc.1") {
		t.Errorf("preview channel should see the prerelease 1.2.0-rc.1: %s", preview.body)
	}
}

func TestUpdateApplyRefusedInContainer(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "upd-container-admin")
	t.Setenv("JELLY_RUNTIME", "container")
	res := updCall(h, http.MethodPost, "/api/admin/update/apply", `{"version":"v1.2.0"}`, h.token(admin))
	if res.code != http.StatusConflict {
		t.Fatalf("apply in container = %d, want 409", res.code)
	}
	if !strings.Contains(res.body, "runtime") {
		t.Errorf("409 body lacks runtime: %s", res.body)
	}
}

func TestUpdateApplyVerifiesChecksumAndSignature(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "upd-verify-admin")
	token := h.token(admin)
	updSetVersion(t, "1.0.0")

	signer := updNewSigner(t)
	archive := updTarGz(t, updTarEntry{Name: "jelly-cms", Body: []byte("new-binary")})
	cases := map[string]map[string][]byte{
		"bad sha256":   updAssets(signer, archive, strings.Repeat("0", 64), ""),
		"bad minisign": updAssets(signer, archive, "", "untrusted comment: forged\nAAAA\n"),
	}
	for name, assets := range cases {
		t.Run(name, func(t *testing.T) {
			fake := updNewFakeGitHub(t, updRelease{Tag: "v1.2.0", Assets: assets})
			exe := updExecutable(t, "old-binary")
			updSeams(t, fake, signer, exe)

			res := updCall(h, http.MethodPost, "/api/admin/update/apply", `{"version":"v1.2.0"}`, token)
			if res.code != http.StatusBadRequest && res.code != http.StatusUnprocessableEntity {
				t.Fatalf("apply with %s = %d, want 400 or 422: %s", name, res.code, res.body)
			}
			if got := updReadFile(t, exe); got != "old-binary" {
				t.Errorf("executable replaced despite %s: now %q", name, got)
			}
			st, err := update.LoadState("data/update-state.json")
			if err != nil {
				t.Fatalf("load update state: %v", err)
			}
			if st.Status != "failed" {
				t.Errorf("state status = %q, want failed", st.Status)
			}
		})
	}
}

func TestUpdateApplyRejectsTarTraversalAndUnknownEntries(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "upd-tar-admin")
	token := h.token(admin)
	updSetVersion(t, "1.0.0")

	cases := map[string][]updTarEntry{
		"traversal":     {{Name: "../jelly-cms", Body: []byte("evil")}},
		"unknown entry": {{Name: "jelly-cms", Body: []byte("new-binary")}, {Name: "evil.sh", Body: []byte("rm -rf /")}},
		"absolute path": {{Name: "/etc/jelly-cms", Body: []byte("evil")}},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			signer := updNewSigner(t)
			archive := updTarGz(t, entries...)
			fake := updNewFakeGitHub(t, updRelease{Tag: "v1.2.0", Assets: updAssets(signer, archive, "", "")})
			exe := updExecutable(t, "old-binary")
			updSeams(t, fake, signer, exe)

			res := updCall(h, http.MethodPost, "/api/admin/update/apply", `{"version":"v1.2.0"}`, token)
			if res.code != http.StatusBadRequest && res.code != http.StatusUnprocessableEntity {
				t.Fatalf("apply with %s archive = %d, want 400 or 422: %s", name, res.code, res.body)
			}
			if got := updReadFile(t, exe); got != "old-binary" {
				t.Errorf("executable replaced by %s archive: now %q", name, got)
			}
		})
	}
}

func TestUpdateApplyRollsBackOnFailedHealthcheck(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "upd-rollback-admin")
	token := h.token(admin)
	updSetVersion(t, "1.0.0")

	signer := updNewSigner(t)
	archive := updTarGz(t, updTarEntry{Name: "jelly-cms", Body: []byte("#!/bin/sh\nexit 1\n")})
	fake := updNewFakeGitHub(t, updRelease{Tag: "v1.2.0", Assets: updAssets(signer, archive, "", "")})
	exe := updExecutable(t, "old-binary")
	updSeams(t, fake, signer, exe)

	res := updCall(h, http.MethodPost, "/api/admin/update/apply", `{"version":"v1.2.0"}`, token)
	if res.code < 400 || res.code == http.StatusNotImplemented || res.code == http.StatusNotFound {
		t.Fatalf("apply of a binary that fails its healthcheck = %d, want an error status: %s", res.code, res.body)
	}
	if got := updReadFile(t, exe); got != "old-binary" {
		t.Errorf("previous binary not restored after failed healthcheck: now %q", got)
	}
	st, err := update.LoadState("data/update-state.json")
	if err != nil {
		t.Fatalf("load update state: %v", err)
	}
	if st.Status != "rolled_back" {
		t.Errorf("state status = %q, want rolled_back", st.Status)
	}
}

func TestUpdateRefusesDowngradeUnlessRollback(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "upd-downgrade-admin")
	token := h.token(admin)
	updSetVersion(t, "1.2.0")

	signer := updNewSigner(t)
	archive := updTarGz(t, updTarEntry{Name: "jelly-cms", Body: []byte("older-binary")})
	fake := updNewFakeGitHub(t, updRelease{Tag: "v1.1.0", Assets: updAssets(signer, archive, "", "")})
	exe := updExecutable(t, "current-binary")
	updSeams(t, fake, signer, exe)

	res := updCall(h, http.MethodPost, "/api/admin/update/apply", `{"version":"v1.1.0"}`, token)
	if res.code != http.StatusBadRequest && res.code != http.StatusConflict {
		t.Fatalf("apply of older v1.1.0 = %d, want 400 or 409 refusal: %s", res.code, res.body)
	}
	if got := updReadFile(t, exe); got != "current-binary" {
		t.Errorf("downgrade replaced the executable: now %q", got)
	}
}

func TestUpdateCheckSendsOnlyVersionAndOS(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "upd-privacy-admin")
	token := h.token(admin)
	updSetVersion(t, "1.0.0")
	fake := updNewFakeGitHub(t, updRelease{Tag: "v1.1.0"})
	t.Setenv("JELLY_TEST_ALLOW_LOOPBACK", "1")
	t.Setenv("JELLY_UPDATE_API_BASE", fake.srv.URL)

	if r := updCall(h, http.MethodPost, "/api/admin/update/check", "", token); r.code != http.StatusOK {
		t.Fatalf("check = %d: %s", r.code, r.body)
	}
	reqs := fake.requests()
	if len(reqs) == 0 {
		t.Fatal("fake GitHub received no request")
	}
	for _, r := range reqs {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("check request carries credentials: Cookie=%q Authorization set=%v", r.Header.Get("Cookie"), r.Header.Get("Authorization") != "")
		}
		if strings.Contains(r.URL.String(), "upd-privacy-admin") || strings.Contains(r.Header.Get("User-Agent"), "upd-privacy-admin") {
			t.Errorf("check request leaks the account name: %s", r.URL.String())
		}
		if !strings.Contains(r.Header.Get("User-Agent"), runtime.GOOS) {
			t.Errorf("User-Agent %q does not carry the OS", r.Header.Get("User-Agent"))
		}
		if !strings.Contains(r.Header.Get("User-Agent"), "1.0.0") && r.URL.Query().Get("version") != "1.0.0" {
			t.Errorf("check request does not carry the running version: UA=%q query=%q", r.Header.Get("User-Agent"), r.URL.RawQuery)
		}
	}
}

func TestUpdateNotificationOnNewVersion(t *testing.T) {
	t.Skip("system.update_available needs internal/notify, which is ticket 06 and is not in the skeleton")
}
