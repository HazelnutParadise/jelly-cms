package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

func TestExtensionSearchEngineFiltersProviderIDsThroughPublicHTTP(t *testing.T) {
	h := newPluginReloadHarness(t)
	tm := theme.NewManager("web/themes")
	if err := tm.Activate("default"); err != nil {
		t.Fatalf("activate default theme: %v", err)
	}
	e := echo.New()
	RegisterRoutes(e, tm, h.runtime)
	h.echo = e

	author := h.user("author", "extension-search-author")
	now := time.Now().UTC()
	first := pubThemeSeedPost(t, h.accessHarness, author, pubThemePost{
		title: "Extension Search Alpha", slug: "extension-search-alpha", publishedAt: now.Add(-48 * time.Hour),
	})
	second := pubThemeSeedPost(t, h.accessHarness, author, pubThemePost{
		title: "Extension Search Beta", slug: "extension-search-beta", publishedAt: now.Add(-24 * time.Hour),
	})
	private := pubThemeSeedPost(t, h.accessHarness, author, pubThemePost{
		title: "Extension Search Private", slug: "extension-search-private", visibility: "private", publishedAt: now.Add(-12 * time.Hour),
	})
	draft := pubThemeSeedPost(t, h.accessHarness, author, pubThemePost{
		title: "Extension Search Draft", slug: "extension-search-draft", status: "draft", publishedAt: now.Add(-10 * time.Hour),
	})
	future := pubThemeSeedPost(t, h.accessHarness, author, pubThemePost{
		title: "Extension Search Future", slug: "extension-search-future", publishedAt: now.Add(24 * time.Hour),
	})
	deleted := pubThemeSeedPost(t, h.accessHarness, author, pubThemePost{
		title: "Extension Search Deleted", slug: "extension-search-deleted", trashed: true, publishedAt: now.Add(-6 * time.Hour),
	})
	pubThemeSetOption(t, h.accessHarness, keyPerPageSearch, "1")

	const id = "search-runtime-red"
	plgSeed(t, h.accessHarness, id, `{"id":"search-runtime-red","name":"Search runtime regression","version":"1.0.0","entrypoint":"index.js","extensions":[{"point":"search.engine"}]}`, false)
	hostWriteEntry(t, id, fmt.Sprintf(`JellyCMS.registerExtension("search.engine", function (input) {
  if (input.query !== "provider-only-query" || input.max_candidates !== 10000) throw new Error("search input contract mismatch");
  return {ids:[%d,%d,%d,%d,%d,%d,%d],complete:true};
});`, second.ID, private.ID, draft.ID, future.ID, deleted.ID, first.ID, second.ID))
	setGrantedPermissions(t, h.accessHarness, id)
	admin := h.user("admin", "extension-search-admin")
	token := h.token(admin)
	if result := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+id+"/enable", "", token); result.code != http.StatusOK {
		t.Fatalf("enable search extension = %d: %s", result.code, result.body)
	}
	if result := updCall(h.accessHarness, http.MethodPut, "/api/admin/extensions/search.engine", `{"active":["search-runtime-red"]}`, token); result.code != http.StatusOK {
		t.Fatalf("select search extension = %d: %s", result.code, result.body)
	}

	path := "/search?" + url.Values{"q": {"provider-only-query"}}.Encode()
	pageOne := archivesPage(t, h.accessHarness, path)
	archivesWantText(t, path, pageOne, "找到 2 篇文章", "Extension Search Beta")
	archivesWantAbsent(t, path, pageOne, "Extension Search Alpha", "Extension Search Private", "Extension Search Draft", "Extension Search Future", "Extension Search Deleted")

	path = "/search?" + url.Values{"q": {"provider-only-query"}, "page": {"2"}}.Encode()
	pageTwo := archivesPage(t, h.accessHarness, path)
	archivesWantText(t, path, pageTwo, "找到 2 篇文章", "Extension Search Alpha")
	archivesWantAbsent(t, path, pageTwo, "Extension Search Beta", "Extension Search Private", "Extension Search Draft", "Extension Search Future", "Extension Search Deleted")
}

func TestExtensionMediaStorageUploadsAndProxiesThroughHTTP(t *testing.T) {
	t.Setenv("JELLY_TEST_ALLOW_LOOPBACK", "1")

	var mu sync.Mutex
	objects := map[string][]byte{}
	methods := map[string]int{}
	storageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Extension-Storage") != "allowed" {
			http.Error(w, "missing provider header", http.StatusForbidden)
			return
		}
		key := r.URL.Path
		mu.Lock()
		methods[r.Method]++
		switch r.Method {
		case http.MethodPut:
			data, err := io.ReadAll(r.Body)
			if err != nil {
				mu.Unlock()
				http.Error(w, "read body", http.StatusBadRequest)
				return
			}
			objects[key] = data
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		case http.MethodHead:
			data, ok := objects[key]
			mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set(echo.HeaderContentLength, fmt.Sprint(len(data)))
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			data, ok := objects[key]
			mu.Unlock()
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(data)
		case http.MethodDelete:
			delete(objects, key)
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			mu.Unlock()
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer storageServer.Close()

	h := newPluginReloadHarness(t)
	const id = "media-runtime-red"
	manifest := `{"id":"media-runtime-red","name":"Media runtime regression","version":"1.0.0","entrypoint":"index.js","permissions":[{"name":"http.fetch","hosts":["127.0.0.1"]}],"extensions":[{"point":"media.storage"}]}`
	plgSeed(t, h.accessHarness, id, manifest, false)
	base, err := json.Marshal(storageServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	hostWriteEntry(t, id, fmt.Sprintf(`const base = %s;
JellyCMS.registerExtension("media.storage", function (input) {
  return {url:base + "/objects/" + input.path,headers:{"X-Extension-Storage":"allowed"}};
});`, string(base)))
	setGrantedPermissions(t, h.accessHarness, id)
	admin := h.user("admin", "extension-media-admin")
	token := h.token(admin)
	if result := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+id+"/enable", "", token); result.code != http.StatusOK {
		t.Fatalf("enable media extension = %d: %s", result.code, result.body)
	}
	policy := updCall(h.accessHarness, http.MethodPost, "/api/admin/media/policies", `{"name":"Extension storage","driver":"plugin:media-runtime-red","purpose":"media"}`, token)
	if policy.code != http.StatusCreated {
		t.Fatalf("create plugin media policy = %d: %s", policy.code, policy.body)
	}
	policyID := int64(impJSONNumber(t, policy.body, "id"))

	payload := append(medPNG(), bytes.Repeat([]byte{0x5a}, (1<<20)+17)...)
	upload := extRuntimeMediaUpload(h.accessHarness, token, policyID, "runtime-proof.png", payload)
	if upload.code != http.StatusCreated {
		t.Fatalf("upload through plugin storage = %d: %s", upload.code, upload.body)
	}
	var uploaded struct {
		ID  int64  `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(upload.body), &uploaded); err != nil || uploaded.ID <= 0 || uploaded.URL != fmt.Sprintf("/media/%d", uploaded.ID) {
		t.Fatalf("uploaded media metadata = %s, err=%v; want stable /media/:id URL", upload.body, err)
	}

	public := h.request(http.MethodGet, uploaded.URL, "", "", "*/*", "")
	if public.code != http.StatusOK || !bytes.Equal([]byte(public.body), payload) {
		t.Fatalf("public media proxy status=%d bytes=%d, want exact %d-byte upload", public.code, len(public.body), len(payload))
	}
	check := updCall(h.accessHarness, http.MethodPost, fmt.Sprintf("/api/admin/media/policies/%d/test", policyID), "", token)
	if check.code != http.StatusOK || !strings.Contains(check.body, `"ok":true`) {
		t.Fatalf("media policy Test() = %d %s, want ok", check.code, check.body)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, method := range []string{http.MethodPut, http.MethodHead, http.MethodGet, http.MethodDelete} {
		if methods[method] == 0 {
			t.Errorf("fake storage saw no %s request; methods=%v", method, methods)
		}
	}
}

func extRuntimeMediaUpload(h *accessHarness, token string, policyID int64, name string, payload []byte) accessResult {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", name)
	if err != nil {
		h.t.Fatalf("create multipart file: %v", err)
	}
	if _, err := file.Write(payload); err != nil {
		h.t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.WriteField("policy_id", fmt.Sprint(policyID)); err != nil {
		h.t.Fatalf("write policy id: %v", err)
	}
	if err := writer.Close(); err != nil {
		h.t.Fatalf("close multipart body: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/media/files", &body)
	request.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
	request.Header.Set("X-Jelly-CSRF", "1")
	request.AddCookie(&http.Cookie{Name: "jwt_token", Value: token})
	recorder := httptest.NewRecorder()
	h.echo.ServeHTTP(recorder, request)
	return accessResult{code: recorder.Code, body: recorder.Body.String(), header: recorder.Header().Clone()}
}

func TestExtensionMediaStorageConditionalWriteAndAvailability(t *testing.T) {
	t.Setenv("JELLY_TEST_ALLOW_LOOPBACK", "1")

	var mu sync.Mutex
	objects := map[string][]byte{}
	requests := 0
	storageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Extension-Storage") != "allowed" {
			http.Error(w, "missing provider header", http.StatusForbidden)
			return
		}
		mu.Lock()
		requests++
		key := r.URL.Path
		switch r.Method {
		case http.MethodPut:
			data, err := io.ReadAll(r.Body)
			if err != nil {
				mu.Unlock()
				http.Error(w, "read body", http.StatusBadRequest)
				return
			}
			if r.Header.Get("If-None-Match") == "*" {
				if _, exists := objects[key]; exists {
					mu.Unlock()
					w.WriteHeader(http.StatusPreconditionFailed)
					return
				}
				objects[key] = data
				mu.Unlock()
				w.WriteHeader(http.StatusCreated)
				return
			}
			objects[key] = data
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		case http.MethodHead:
			data, ok := objects[key]
			mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set(echo.HeaderContentLength, fmt.Sprint(len(data)))
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			data, ok := objects[key]
			mu.Unlock()
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(data)
		case http.MethodDelete:
			delete(objects, key)
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			mu.Unlock()
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(storageServer.Close)

	h := newPluginReloadHarness(t)
	const id = "media-conditional-runtime"
	manifest := `{"id":"media-conditional-runtime","name":"Media conditional regression","version":"1.0.0","entrypoint":"index.js","permissions":[{"name":"http.fetch","hosts":["127.0.0.1"]}],"extensions":[{"point":"media.storage"}]}`
	plgSeed(t, h.accessHarness, id, manifest, false)
	base, err := json.Marshal(storageServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	hostWriteEntry(t, id, fmt.Sprintf(`const base = %s;
JellyCMS.registerExtension("media.storage", function (input) {
  return {url:base + "/objects/" + input.path,headers:{"X-Extension-Storage":"allowed"}};
});`, string(base)))
	setGrantedPermissions(t, h.accessHarness, id)
	admin := h.user("admin", "extension-media-conditional-admin")
	token := h.token(admin)
	if result := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+id+"/enable", "", token); result.code != http.StatusOK {
		t.Fatalf("enable media extension = %d: %s", result.code, result.body)
	}

	driver, err := policyDriver(mediaPolicyRow{Driver: "plugin:" + id, Purpose: "media"}, context.Background())
	if err != nil {
		t.Fatalf("create enabled plugin storage driver: %v", err)
	}
	exclusive, ok := driver.(interface {
		PutExclusive(context.Context, string, io.Reader, int64, string) error
	})
	if !ok {
		t.Fatalf("plugin storage driver %T does not implement PutExclusive", driver)
	}

	path := "concurrent/same-object.bin"
	payloads := [][]byte{[]byte("writer-one"), []byte("writer-two")}
	start := make(chan struct{})
	type putResult struct {
		payload []byte
		err     error
	}
	results := make(chan putResult, len(payloads))
	for _, payload := range payloads {
		payload := append([]byte(nil), payload...)
		go func() {
			<-start
			results <- putResult{payload: payload, err: exclusive.PutExclusive(context.Background(), path, bytes.NewReader(payload), int64(len(payload)), "application/octet-stream")}
		}()
	}
	close(start)
	var winner []byte
	successes, existsErrors := 0, 0
	for range payloads {
		result := <-results
		switch {
		case result.err == nil:
			successes++
			winner = result.payload
		case errors.Is(result.err, fs.ErrExist):
			existsErrors++
		default:
			t.Errorf("PutExclusive returned unexpected error: %v", result.err)
		}
	}
	if successes != 1 || existsErrors != 1 {
		t.Fatalf("concurrent PutExclusive outcomes = %d success, %d already-exists; want 1 each", successes, existsErrors)
	}
	reader, err := driver.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open winning object: %v", err)
	}
	stored, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(stored, winner) {
		t.Fatalf("stored object = %q (read=%v close=%v), want winning payload %q", stored, readErr, closeErr, winner)
	}

	// The existing driver must recheck current provider availability before every
	// network operation, including operations on an already-created instance.
	setRequests := func(n int) {
		t.Helper()
		mu.Lock()
		requests = n
		mu.Unlock()
	}
	requestCount := func() int {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		return requests
	}
	checkUnavailable := func(label string) {
		t.Helper()
		setRequests(0)
		if _, err := driver.Stat(context.Background(), path); err == nil {
			t.Errorf("Stat succeeded with %s", label)
		}
		if _, err := driver.Open(context.Background(), path); err == nil {
			t.Errorf("Open succeeded with %s", label)
		}
		if got := requestCount(); got != 0 {
			t.Errorf("%s caused %d storage HTTP requests; want none", label, got)
		}
	}

	if err := h.db.Exec(`UPDATE plugins SET enabled = false WHERE id = ?`, id).Error; err != nil {
		t.Fatal(err)
	}
	checkUnavailable("provider disabled after driver creation")
	if err := h.db.Exec(`UPDATE plugins SET enabled = true, permissions_granted_hash = 'stale' WHERE id = ?`, id).Error; err != nil {
		t.Fatal(err)
	}
	checkUnavailable("provider grant hash is stale")
	setGrantedPermissions(t, h.accessHarness, id)
	if err := h.db.Exec(`INSERT INTO extension_bindings (point, provider_id, position) VALUES ('media.storage', 'builtin', 0)`).Error; err != nil {
		t.Fatal(err)
	}
	checkUnavailable("plugin provider is excluded by active bindings")

	const secondID = "media-conditional-second-provider"
	secondManifest := `{"id":"media-conditional-second-provider","name":"Second active media provider","version":"1.0.0","entrypoint":"index.js","permissions":[{"name":"http.fetch","hosts":["127.0.0.1"]}],"extensions":[{"point":"media.storage"}]}`
	plgSeed(t, h.accessHarness, secondID, secondManifest, false)
	hostWriteEntry(t, secondID, fmt.Sprintf(`const base = %s;
JellyCMS.registerExtension("media.storage", function (input) {
  return {url:base + "/objects/" + input.path,headers:{"X-Extension-Storage":"allowed"}};
});`, string(base)))
	setGrantedPermissions(t, h.accessHarness, secondID)
	if result := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+secondID+"/enable", "", token); result.code != http.StatusOK {
		t.Fatalf("enable second media extension = %d: %s", result.code, result.body)
	}
	if err := h.db.Exec(`DELETE FROM extension_bindings WHERE point = 'media.storage'`).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.db.Exec(`INSERT INTO extension_bindings (point, provider_id, position) VALUES ('media.storage', ?, 0), ('media.storage', ?, 1)`, secondID, id).Error; err != nil {
		t.Fatal(err)
	}
	setRequests(0)
	if _, err := driver.Stat(context.Background(), path); err != nil {
		t.Fatalf("first plugin storage provider failed while another provider is also active: %v", err)
	}
	if got := requestCount(); got == 0 {
		t.Fatal("Stat with a second active provider made no storage request")
	}
	if err := driver.Delete(context.Background(), path); err != nil {
		t.Fatalf("delete test object: %v", err)
	}
}
