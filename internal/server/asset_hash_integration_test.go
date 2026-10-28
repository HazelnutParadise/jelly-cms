package server

import (
	"crypto/sha256"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
)

var assetHashHTMLAttribute = regexp.MustCompile(`(?i)(?:src|href|data-[a-z0-9_-]+)\s*=\s*["']([^"']+)["']`)
var assetHashCSSURL = regexp.MustCompile(`(?i)url\(\s*["']?([^"')]+)["']?\s*\)`)

func TestAssetHashHTMLResponsesRequireRevalidation(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "asset-hash-cache-admin")
	token := h.token(admin)
	paths := []struct {
		path  string
		token string
		code  int
	}{
		{path: "/login", code: http.StatusOK},
		{path: "/", code: http.StatusOK},
		{path: "/admin", token: token, code: http.StatusOK},
		{path: "/missing-asset-hash-page", code: http.StatusNotFound},
	}
	for _, tc := range paths {
		r := h.request(http.MethodGet, tc.path, "", tc.token, echo.MIMETextHTML, "")
		if r.panicked != nil {
			t.Errorf("GET %s panicked: %v", tc.path, r.panicked)
			continue
		}
		if r.code != tc.code {
			t.Errorf("GET %s status = %d, want %d", tc.path, r.code, tc.code)
		}
		if !strings.Contains(strings.ToLower(r.header.Get(echo.HeaderContentType)), "text/html") {
			t.Errorf("GET %s Content-Type = %q, want HTML (status %d)", tc.path, r.header.Get(echo.HeaderContentType), r.code)
		}
		cache := r.header.Get(echo.HeaderCacheControl)
		if !strings.Contains(cache, "no-cache") && !strings.Contains(cache, "no-store") {
			t.Errorf("GET %s Cache-Control = %q, want revalidation or stricter no-store", tc.path, cache)
		}
	}
	installed := h.request(http.MethodGet, "/install", "", "", echo.MIMETextHTML, "")
	if installed.code != http.StatusFound || installed.header.Get(echo.HeaderLocation) != "/" {
		t.Errorf("GET /install on installed site: status=%d Location=%q, want 302 to /", installed.code, installed.header.Get(echo.HeaderLocation))
	}
}

func TestAssetHashUninstalledInstallHTMLRequiresRevalidation(t *testing.T) {
	e := newInstallRoutesServer(t)
	r := callRoute(t, e, http.MethodGet, "/install", "")
	if r.panicked != nil || r.recorder.Code != http.StatusOK {
		t.Fatalf("GET /install on uninstalled site: panic=%v status=%d", r.panicked, r.recorder.Code)
	}
	if !strings.Contains(strings.ToLower(r.recorder.Header().Get(echo.HeaderContentType)), "text/html") {
		t.Fatalf("GET /install Content-Type = %q", r.recorder.Header().Get(echo.HeaderContentType))
	}
	cache := r.recorder.Header().Get(echo.HeaderCacheControl)
	if !strings.Contains(cache, "no-cache") && !strings.Contains(cache, "no-store") {
		t.Fatalf("GET /install Cache-Control = %q, want revalidation or stricter no-store", cache)
	}
}

func TestAssetHashRenderedThemeAndAdminAssetsServeMatchingBytes(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "asset-hash-reference-admin")
	token := h.token(admin)
	paths := []struct {
		path  string
		token string
	}{
		{path: "/login"},
		{path: "/", token: ""},
		{path: "/admin", token: token},
		{path: "/admin/posts/new", token: token},
	}
	seen := map[string]bool{}
	editorLazyURLs := map[string]bool{}
	for _, tc := range paths {
		page := h.request(http.MethodGet, tc.path, "", tc.token, echo.MIMETextHTML, "")
		if !strings.Contains(strings.ToLower(page.header.Get(echo.HeaderContentType)), "text/html") {
			t.Fatalf("GET %s returned non-HTML content type %q (status %d)", tc.path, page.header.Get(echo.HeaderContentType), page.code)
		}
		for _, match := range assetHashHTMLAttribute.FindAllStringSubmatch(page.body, -1) {
			raw := html.UnescapeString(match[1])
			parsed, err := url.Parse(raw)
			if err != nil || parsed.IsAbs() || !strings.HasPrefix(parsed.Path, "/admin/assets/") && !strings.HasPrefix(parsed.Path, "/theme/assets/") && !strings.HasPrefix(parsed.Path, "/_jelly/") && !strings.HasPrefix(parsed.Path, "/vendor/katex/") {
				continue
			}
			values := parsed.Query()["v"]
			if len(values) == 0 {
				t.Errorf("GET %s rendered managed asset without a hash: %q", tc.path, raw)
				continue
			}
			if len(values) != 1 || len(values[0]) != 64 {
				t.Errorf("GET %s generated malformed asset hash URL %q", tc.path, raw)
				continue
			}
			if tc.path == "/admin/posts/new" && (parsed.Path == "/admin/assets/pele/pele.global.js" || parsed.Path == "/admin/assets/jelly-mermaid.js") {
				editorLazyURLs[parsed.Path] = true
			}
			if seen[raw] {
				continue
			}
			seen[raw] = true
			asset := h.request(http.MethodGet, parsed.RequestURI(), "", "", "", "")
			if asset.code != http.StatusOK {
				t.Errorf("asset referenced by %s returned %d: %s", tc.path, asset.code, parsed.RequestURI())
				continue
			}
			want := fmt.Sprintf("%x", sha256.Sum256([]byte(asset.body)))
			if values[0] != want {
				t.Errorf("asset hash mismatch for %s: URL has %s, response bytes hash to %s", parsed.Path, values[0], want)
			}
			cache := asset.header.Get(echo.HeaderCacheControl)
			if !strings.Contains(cache, "public") || !strings.Contains(cache, "max-age=31536000") || !strings.Contains(cache, "immutable") {
				t.Errorf("hashed asset %s Cache-Control = %q", parsed.Path, cache)
			}
			if strings.Contains(strings.ToLower(asset.header.Get(echo.HeaderContentType)), "text/css") {
				for _, dep := range assetHashCSSURL.FindAllStringSubmatch(asset.body, -1) {
					depURL, parseErr := url.Parse(html.UnescapeString(strings.TrimSpace(dep[1])))
					if parseErr != nil || depURL.IsAbs() || strings.HasPrefix(depURL.Path, "//") {
						continue
					}
					if !strings.HasPrefix(depURL.Path, "/") {
						depURL.Path = path.Join(path.Dir(parsed.Path), depURL.Path)
					}
					if !strings.HasPrefix(depURL.Path, "/admin/assets/") && !strings.HasPrefix(depURL.Path, "/theme/assets/") && !strings.HasPrefix(depURL.Path, "/vendor/katex/") {
						continue
					}
					if strings.HasPrefix(depURL.Path, "/vendor/katex/fonts/") && (strings.HasSuffix(depURL.Path, ".woff") || strings.HasSuffix(depURL.Path, ".ttf")) {
						fallbackPath := filepath.Join("web", filepath.FromSlash(strings.TrimPrefix(depURL.Path, "/")))
						if _, statErr := os.Stat(fallbackPath); os.IsNotExist(statErr) {
							continue
						} else if statErr != nil {
							t.Errorf("stat KaTeX fallback %s: %v", fallbackPath, statErr)
							continue
						}
					}
					depHash := depURL.Query().Get("v")
					if len(depHash) != 64 {
						t.Errorf("CSS %s dependency has no full hash URL: %q", parsed.Path, dep[1])
						continue
					}
					dependency := h.request(http.MethodGet, depURL.RequestURI(), "", "", "", "")
					if dependency.code != http.StatusOK {
						t.Errorf("CSS dependency %s returned %d", depURL.RequestURI(), dependency.code)
						continue
					}
					if actual := fmt.Sprintf("%x", sha256.Sum256([]byte(dependency.body))); depHash != actual {
						t.Errorf("CSS dependency hash mismatch for %s: URL has %s, bytes hash to %s", depURL.Path, depHash, actual)
					}
				}
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("rendered pages contained no hash-versioned built-in assets")
	}
	for _, required := range []string{"/admin/assets/pele/pele.global.js", "/admin/assets/jelly-mermaid.js"} {
		if !editorLazyURLs[required] {
			t.Errorf("editor page has no hashed data attribute or resource reference for lazy Mermaid asset %s", required)
		}
	}
}

func TestAssetHashThemeCSSHeadAndAdminNotFoundUseRealHTTPHeaders(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "asset-hash-real-http-admin")
	ts := httptest.NewServer(h.echo)
	t.Cleanup(ts.Close)
	client := ts.Client()
	cssPath := "/theme/assets/content.css"
	get, err := client.Get(ts.URL + cssPath)
	if err != nil {
		t.Fatalf("GET %s: %v", cssPath, err)
	}
	getBody, err := io.ReadAll(get.Body)
	get.Body.Close()
	if err != nil {
		t.Fatalf("read GET %s: %v", cssPath, err)
	}
	if get.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", cssPath, get.StatusCode)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(getBody))
	versionedURL := ts.URL + cssPath + "?v=" + hash
	versioned, err := client.Get(versionedURL)
	if err != nil {
		t.Fatalf("GET versioned %s: %v", cssPath, err)
	}
	versionedBody, err := io.ReadAll(versioned.Body)
	versioned.Body.Close()
	if err != nil {
		t.Fatalf("read versioned CSS: %v", err)
	}
	if versioned.StatusCode != http.StatusOK || !strings.EqualFold(fmt.Sprintf("%x", sha256.Sum256(versionedBody)), hash) {
		t.Fatalf("versioned CSS GET status/hash = %d/%x, want 200/%s", versioned.StatusCode, sha256.Sum256(versionedBody), hash)
	}
	head, err := client.Head(versionedURL)
	if err != nil {
		t.Fatalf("HEAD versioned %s: %v", cssPath, err)
	}
	headBody, err := io.ReadAll(head.Body)
	head.Body.Close()
	if err != nil {
		t.Fatalf("read HEAD response body: %v", err)
	}
	if head.StatusCode != http.StatusOK || len(headBody) != 0 {
		t.Fatalf("HEAD versioned CSS status/body length = %d/%d, want 200/0", head.StatusCode, len(headBody))
	}
	if head.Header.Get("ETag") == "" || head.Header.Get("ETag") != versioned.Header.Get("ETag") {
		t.Errorf("HEAD ETag %q differs from GET ETag %q", head.Header.Get("ETag"), versioned.Header.Get("ETag"))
	}
	if head.ContentLength != int64(len(versionedBody)) || versioned.ContentLength != int64(len(versionedBody)) {
		t.Errorf("HEAD/GET Content-Length = %d/%d, want %d", head.ContentLength, versioned.ContentLength, len(versionedBody))
	}

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/admin/not-found", nil)
	if err != nil {
		t.Fatal(err)
	}
	token := h.token(admin)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	missing, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /admin/not-found: %v", err)
	}
	defer missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound || !strings.Contains(strings.ToLower(missing.Header.Get(echo.HeaderContentType)), "text/html") {
		t.Fatalf("GET /admin/not-found status/content-type = %d/%q, want 404 HTML", missing.StatusCode, missing.Header.Get(echo.HeaderContentType))
	}
	cache := missing.Header.Get(echo.HeaderCacheControl)
	if !strings.Contains(cache, "no-cache") && !strings.Contains(cache, "no-store") {
		t.Errorf("GET /admin/not-found Cache-Control = %q, want revalidation or stricter no-store", cache)
	}
}

func TestAssetHashThemeSettingsReloadFailureRetainsPersistedSettingsAndSnapshot(t *testing.T) {
	h := newWebMCPHarness(t)
	const cssPath = "/theme/assets/content.css"
	oldURL := h.tm.AssetURL(cssPath)
	if oldURL == cssPath {
		t.Fatal("active theme did not resolve its content CSS URL")
	}
	assetsDir := filepath.Join("web", "themes", "default", "assets")
	cycleA := filepath.Join(assetsDir, "asset-hash-cycle-a.css")
	cycleB := filepath.Join(assetsDir, "asset-hash-cycle-b.css")
	for _, filename := range []string{cycleA, cycleB} {
		if _, err := os.Stat(filename); err == nil {
			t.Fatalf("refusing to overwrite existing theme fixture %s", filename)
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat theme fixture %s: %v", filename, err)
		}
	}
	if err := os.WriteFile(cycleA, []byte(`@import "asset-hash-cycle-b.css";`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cycleB, []byte(`@import "asset-hash-cycle-a.css";`), 0644); err != nil {
		t.Fatal(err)
	}
	response := h.adminJSON(http.MethodPost, "/api/admin/themes/default/settings", `{"colors":{"primary":"#abcdef"},"layout":{},"custom":{}}`)
	if response.code != http.StatusInternalServerError {
		t.Fatalf("settings update with broken asset graph returned %d, want 500: %s", response.code, response.body)
	}
	message := strings.ToLower(response.body)
	if !strings.Contains(message, "sav") || !strings.Contains(message, "reload") {
		t.Errorf("500 response does not explain settings were saved but theme reload failed: %s", response.body)
	}
	var saved core.ThemeSettings
	if err := h.db.First(&saved, "theme_name = ?", "default").Error; err != nil {
		t.Fatalf("read persisted theme settings after failed reload: %v", err)
	}
	if !strings.Contains(string(saved.Colors), "#abcdef") {
		t.Errorf("settings were not persisted before reload failure: %s", saved.Colors)
	}
	if got := h.tm.AssetURL(cssPath); got != oldURL {
		t.Fatalf("failed settings reload replaced asset snapshot URL: old=%q new=%q", oldURL, got)
	}
	oldAsset := h.request(http.MethodGet, oldURL, "", "", "", "")
	if oldAsset.code != http.StatusOK {
		t.Errorf("asset from retained old snapshot returned %d, want 200", oldAsset.code)
	}
}

func TestAssetHashWebMCPInjectedScriptIsVersionedAndDisabledRouteIsNotServed(t *testing.T) {
	h := newWebMCPHarness(t)
	page := h.page("/")
	if page.code != http.StatusOK {
		t.Fatalf("GET / returned %d", page.code)
	}
	var scriptURL string
	for _, match := range assetHashHTMLAttribute.FindAllStringSubmatch(page.body, -1) {
		raw := html.UnescapeString(match[1])
		parsed, err := url.Parse(raw)
		if err == nil && parsed.Path == "/_jelly/webmcp.js" && parsed.Query().Get("v") != "" {
			scriptURL = parsed.RequestURI()
			break
		}
	}
	if scriptURL == "" {
		t.Fatalf("enabled WebMCP page has no versioned /_jelly/webmcp.js reference: %s", page.body)
	}
	script := h.request(http.MethodGet, scriptURL, "", "", "", "")
	if script.code != http.StatusOK {
		t.Fatalf("versioned WebMCP script returned %d", script.code)
	}
	parsed, err := url.ParseRequestURI(scriptURL)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := parsed.Query().Get("v"), fmt.Sprintf("%x", sha256.Sum256([]byte(script.body))); got != want {
		t.Fatalf("WebMCP script hash = %q, response bytes hash to %q", got, want)
	}
	h.setEnabled(t, "false")
	for _, path := range []string{"/_jelly/webmcp.js", scriptURL} {
		disabled := h.request(http.MethodGet, path, "", "", "", "")
		if disabled.code != http.StatusNotFound {
			t.Errorf("disabled WebMCP script %s status = %d, want 404", path, disabled.code)
		}
	}
}

func TestAssetHashDoesNotRewriteArticleCodeSamples(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "asset-hash-code-sample-author")
	post := archivesSeedPost(t, h, author, archivesPost{title: "Asset Hash Code Sample"})
	sample := "```text\n/admin/assets/jelly-mermaid.js?v=example\n/_jelly/webmcp.js\n```"
	if err := h.db.Model(&post).Update("content", sample).Error; err != nil {
		t.Fatalf("update code sample: %v", err)
	}
	r := archivesGet(t, h, "/post/"+post.Slug)
	if r.code != http.StatusOK {
		t.Fatalf("GET post: status=%d", r.code)
	}
	for _, literal := range []string{"/admin/assets/jelly-mermaid.js?v=example", "/_jelly/webmcp.js"} {
		if !strings.Contains(r.body, literal) {
			t.Errorf("rendered code sample lost or rewrote %q", literal)
		}
	}
}
