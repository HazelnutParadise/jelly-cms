package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/install"
)

// Public API filtering is independent of caller identity; an authenticated request must not expose drafts.
func TestWebMCPPublicPostsFilteringDoesNotChangeForAuthenticatedCaller(t *testing.T) {
	h := newWebMCPHarness(t)
	h.seed(t, pubThemePost{title: "公開", slug: "webmcp-auth-public"})
	h.seed(t, pubThemePost{title: "草稿", slug: "webmcp-auth-draft", status: "draft"})
	guest := h.getJSON("/api/public/posts")
	admin := h.request(http.MethodGet, "/api/public/posts", "", h.token, "application/json", "")
	if guest.code != http.StatusOK || admin.code != http.StatusOK {
		t.Fatalf("guest/admin status = %d/%d, want 200", guest.code, admin.code)
	}
	var g, a struct {
		Items []map[string]json.RawMessage `json:"items"`
		Total int                          `json:"total"`
	}
	if err := json.Unmarshal([]byte(guest.body), &g); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(admin.body), &a); err != nil {
		t.Fatal(err)
	}
	if g.Total != 1 || a.Total != g.Total || len(a.Items) != len(g.Items) {
		t.Fatalf("guest items/total=%d/%d, authenticated=%d/%d; expected same single public post", len(g.Items), g.Total, len(a.Items), a.Total)
	}
}

func TestWebMCPPublicPostsHugePageReturnsEmptyPage(t *testing.T) {
	h := newWebMCPHarness(t)
	h.seed(t, pubThemePost{title: "有內容", slug: "webmcp-overflow"})
	r := h.getJSON("/api/public/posts?page=922337203685477581&per_page=10")
	if r.code != http.StatusOK {
		t.Fatalf("huge page status = %d, want 200; body=%q", r.code, r.body)
	}
	var result struct {
		Items []json.RawMessage `json:"items"`
		Page  int               `json:"page"`
		Total int               `json:"total"`
	}
	if err := json.Unmarshal([]byte(r.body), &result); err != nil {
		t.Fatal(err)
	}
	if result.Page < 1 || len(result.Items) != 0 || result.Total != 1 {
		t.Fatalf("huge page returned page=%d items=%d total=%d; want positive page, empty items, total 1", result.Page, len(result.Items), result.Total)
	}
}

func TestWebMCPPublicPostsPinnedAndEffectiveDateOrdering(t *testing.T) {
	h := newWebMCPHarness(t)
	old := archivesDay(2024, time.January, 1)
	newer := archivesDay(2024, time.March, 1)
	h.seed(t, pubThemePost{title: "較新", slug: "webmcp-order-new", publishedAt: newer})
	h.seed(t, pubThemePost{title: "置頂較舊", slug: "webmcp-order-pinned", pinned: true, publishedAt: old})
	r := h.getJSON("/api/public/posts?per_page=10")
	if r.code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", r.code)
	}
	var result struct {
		Items []struct {
			URL         string `json:"url"`
			PublishedAt string `json:"published_at"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(r.body), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) < 2 || result.Items[0].URL != "/post/webmcp-order-pinned" || result.Items[1].URL != "/post/webmcp-order-new" {
		t.Fatalf("pinned ordering = %+v; expected pinned item before newer unpinned item", result.Items)
	}
	if _, err := time.Parse(time.RFC3339, result.Items[0].PublishedAt); err != nil {
		t.Errorf("published_at %q is not RFC3339: %v", result.Items[0].PublishedAt, err)
	}
}

func TestWebMCPPublicPostURLUsesConfiguredPermalink(t *testing.T) {
	h := newWebMCPHarness(t)
	pubThemeSetOption(t, h.accessHarness, "permalink_post", "/stories/{slug}")
	h.seed(t, pubThemePost{title: "自訂網址", slug: "webmcp-custom-url", publishedAt: archivesDay(2024, time.January, 2)})
	r := h.getJSON("/api/public/posts")
	if r.code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", r.code)
	}
	var result struct {
		Items []struct {
			URL string `json:"url"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(r.body), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].URL != "/stories/webmcp-custom-url" {
		t.Fatalf("API URL = %+v, want /stories/webmcp-custom-url", result.Items)
	}
	page := h.page("/stories/webmcp-custom-url")
	if page.code != http.StatusOK {
		t.Fatalf("configured public URL status = %d, want 200", page.code)
	}
}

func TestWebMCPPublicContentUsesCreatedAtWhenPublicationTimeIsUnset(t *testing.T) {
	h := newWebMCPHarness(t)
	created := archivesDay(2024, time.January, 2)
	post := h.seed(t, pubThemePost{title: "建立時間備援", slug: "webmcp-created-at", publishedAt: created})
	if err := h.db.Model(&post).Update("published_at", nil).Error; err != nil {
		t.Fatalf("clear published_at: %v", err)
	}
	r := h.getJSON("/api/public/content/webmcp-created-at")
	if r.code != http.StatusOK {
		t.Fatalf("content status = %d, want 200", r.code)
	}
	var result struct {
		PublishedAt string `json:"published_at"`
	}
	if err := json.Unmarshal([]byte(r.body), &result); err != nil {
		t.Fatal(err)
	}
	got, err := time.Parse(time.RFC3339, result.PublishedAt)
	if err != nil {
		t.Fatalf("published_at %q is not RFC3339: %v", result.PublishedAt, err)
	}
	if !got.Equal(created) {
		t.Errorf("published_at = %s, want created_at %s when published_at is NULL", got, created)
	}
}

func TestWebMCPRejectsEncodedAndDotSegmentPrivilegedEndpoints(t *testing.T) {
	h := newWebMCPHarness(t)
	dir := t.TempDir()
	paths := []string{
		"/api/%61dmin/settings", "/api/public/../admin/settings", "/api/public/%2e%2e/admin/settings",
		"/admin%2fsettings", "/%61dmin/settings", "/api/public/%2e%2e/%61dmin/settings",
	}
	decls := make([]map[string]any, 0, len(paths))
	for i, endpoint := range paths {
		decls = append(decls, map[string]any{
			"name": fmt.Sprintf("bypass_%d", i), "title": "Path bypass probe",
			"description": "Probe a normalized privileged path.", "endpoint": endpoint,
			"input_schema": map[string]any{"type": "object", "properties": map[string]any{}},
		})
	}
	manifest, err := json.Marshal(map[string]any{"id": "webmcp-path-probe", "name": "Path probe", "version": "1.0.0", "entrypoint": "index.js", "webmcp_tools": decls})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("// no hooks\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := h.runtime.Load(context.Background(), dir); err != nil {
		t.Fatalf("load path probe plugin: %v", err)
	}
	for _, endpoint := range paths {
		for _, tool := range h.tools(t) {
			if tool.Endpoint == endpoint {
				t.Errorf("privileged endpoint %q was exposed as tool %q", endpoint, tool.Name)
			}
		}
	}
}

func TestWebMCPPublicAPIMissingDatabaseReturnsCorrelated503(t *testing.T) {
	h := newWebMCPHarness(t)
	db := data.DB
	data.DB = nil
	t.Cleanup(func() { data.DB = db })
	for _, path := range []string{"/api/public/site", "/api/public/posts", "/api/public/content/any-slug"} {
		r := h.getJSON(path)
		if r.code != http.StatusServiceUnavailable {
			t.Errorf("GET %s status = %d, want 503", path, r.code)
			continue
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal([]byte(r.body), &body); err != nil {
			t.Errorf("GET %s invalid JSON: %v", path, err)
			continue
		}
		var id string
		_ = json.Unmarshal(body["correlation_id"], &id)
		if id == "" || id != r.header.Get("X-Request-ID") {
			t.Errorf("GET %s correlation_id=%q X-Request-ID=%q", path, id, r.header.Get("X-Request-ID"))
		}
		if string(body["code"]) != `"service_unavailable"` {
			t.Errorf("GET %s code=%s, want service_unavailable", path, body["code"])
		}
	}
}

func TestWebMCPPublicAPIMissingDatabaseBeforeInstallDoesNotChangeOtherRoutes(t *testing.T) {
	h := newWebMCPHarness(t)
	previousDB, previousConfig := data.DB, config.GlobalConfig
	data.DB = nil
	config.GlobalConfig = nil
	t.Cleanup(func() { data.DB, config.GlobalConfig = previousDB, previousConfig })
	if install.IsInstalled() {
		t.Fatal("test harness unexpectedly has an installation marker")
	}
	for _, path := range []string{"/api/public/site", "/api/public/posts", "/api/public/content/any-slug"} {
		r := h.getJSON(path)
		if r.code != http.StatusServiceUnavailable {
			t.Errorf("GET %s status = %d, want 503", path, r.code)
			continue
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal([]byte(r.body), &body); err != nil {
			t.Errorf("GET %s invalid JSON: %v", path, err)
			continue
		}
		var id string
		_ = json.Unmarshal(body["correlation_id"], &id)
		if id == "" || id != r.header.Get("X-Request-ID") {
			t.Errorf("GET %s correlation_id=%q X-Request-ID=%q", path, id, r.header.Get("X-Request-ID"))
		}
	}
	root := h.request(http.MethodGet, "/", "", "", "text/html", "")
	if root.code != http.StatusFound || root.header.Get("Location") != "/install" {
		t.Errorf("GET / without install/db status/location=%d/%q; want redirect to /install", root.code, root.header.Get("Location"))
	}
	installer := h.request(http.MethodGet, "/install", "", "", "text/html", "")
	if installer.code != http.StatusOK {
		t.Errorf("GET /install without install/db status=%d, want 200", installer.code)
	}
	health := h.getJSON("/healthz")
	if health.code != http.StatusOK {
		t.Errorf("GET /healthz without install/db status=%d, want 200", health.code)
	}
}

func TestWebMCPPublicAPIDiscoveryDocumentsResponseSchemas(t *testing.T) {
	h := newWebMCPHarness(t)
	overview := h.getJSON("/api/public")
	if overview.code != http.StatusOK {
		t.Fatalf("public API overview status = %d, want 200", overview.code)
	}
	var index struct {
		OpenAPIURL string `json:"openapi_url"`
		Operations []struct {
			Method string `json:"method"`
			Path   string `json:"path"`
		} `json:"operations"`
	}
	if err := json.Unmarshal([]byte(overview.body), &index); err != nil {
		t.Fatal(err)
	}
	if index.OpenAPIURL != "/api/public/openapi.json" {
		t.Fatalf("openapi_url = %q", index.OpenAPIURL)
	}
	for _, path := range []string{"/api/public/posts", "/api/public/content/{slug}", "/api/public/site"} {
		found := false
		for _, operation := range index.Operations {
			if operation.Method == http.MethodGet && operation.Path == path {
				found = true
			}
		}
		if !found {
			t.Errorf("public API overview does not discover GET %s", path)
		}
	}
	doc := h.getJSON(index.OpenAPIURL)
	if doc.code != http.StatusOK {
		t.Fatalf("OpenAPI status = %d, want 200", doc.code)
	}
	var schema struct {
		Version string `json:"openapi"`
		Paths   map[string]map[string]struct {
			Responses map[string]json.RawMessage `json:"responses"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]struct {
				Type     string   `json:"type"`
				Required []string `json:"required"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal([]byte(doc.body), &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Version == "" {
		t.Error("OpenAPI document has no version")
	}
	for _, path := range []string{"/api/public/posts", "/api/public/content/{slug}", "/api/public/site"} {
		get, ok := schema.Paths[path]["get"]
		if !ok {
			t.Errorf("OpenAPI document lacks GET %s", path)
			continue
		}
		if _, ok := get.Responses["200"]; !ok {
			t.Errorf("OpenAPI GET %s lacks a 200 response", path)
		}
	}
	for _, name := range []string{"PublicPostList", "PublicPostSummary", "PublicContent", "PublicSite", "PublicAPIError"} {
		if got, ok := schema.Components.Schemas[name]; !ok || got.Type != "object" || len(got.Required) == 0 {
			t.Errorf("OpenAPI schema %s = %+v, present=%v; want required object schema", name, got, ok)
		}
	}
}
