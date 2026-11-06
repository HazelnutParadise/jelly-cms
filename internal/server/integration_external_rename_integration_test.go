package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	integrationkey "github.com/HazelnutParadise/jelly-cms/internal/integration"
)

func TestIntegrationExternalRenameOldAdminRoutesAreGone(t *testing.T) {
	h := newAccessHarness(t)
	adminToken := h.token(h.user("admin", "integration-external-rename-admin"))
	for _, route := range h.echo.Routes() {
		if route.Method != "echo_route_not_found" && (route.Path == "/api/admin/agents" || route.Path == "/api/admin/agents/:id") {
			t.Errorf("legacy management route %s %s is still registered", route.Method, route.Path)
		}
	}
	for _, target := range []string{
		"/api/admin/agents",
		"/api/admin/agents/1",
		"/admin/agents",
		"/admin/agents/1",
	} {
		var result accessResult
		if strings.HasPrefix(target, "/api/admin/") {
			result = h.request(http.MethodGet, target, "", adminToken, "application/json", "application/json")
		} else {
			result = renameV1Cookie(h, http.MethodGet, target, adminToken)
		}
		if result.panicked != nil {
			t.Fatalf("GET %s panicked: %v", target, result.panicked)
		}
		if result.code != http.StatusNotFound {
			t.Errorf("GET %s returned %d, want 404; body=%q", target, result.code, result.body)
		}
	}
}

func TestIntegrationExternalRenameOwnEventsUseIntegrationFields(t *testing.T) {
	h := newAccessHarness(t)
	owner := h.user("admin", "integration-external-events-admin")
	name := "External event response"
	integration := core.Integration{
		Name: name, Permissions: core.StringArray{"logs.read"},
		OwnerID: &owner.ID, CreatedBy: &owner.ID, Status: "active",
	}
	if err := h.db.Create(&integration).Error; err != nil {
		t.Fatalf("create integration fixture: %v", err)
	}
	key, err := integrationkey.GenerateKey()
	if err != nil {
		t.Fatalf("generate integration key: %v", err)
	}
	keyRow := core.IntegrationKey{
		IntegrationID: integration.ID, KeyHash: integrationkey.HashKey(key),
		Prefix: integrationkey.PrefixOf(key), CreatedBy: &owner.ID,
	}
	if err := h.db.Create(&keyRow).Error; err != nil {
		t.Fatalf("create integration key fixture: %v", err)
	}
	event := core.IntegrationEvent{
		At: time.Now().UTC(), ActorType: "integration",
		IntegrationID: integration.ID, IntegrationName: name,
		KeyID: &keyRow.ID, KeyPrefix: keyRow.Prefix,
		Action: "request", Module: "me", Method: http.MethodGet,
		Route: "/api/v1/me", Status: http.StatusOK,
		Summary: json.RawMessage(`{}`),
	}
	if err := h.db.Create(&event).Error; err != nil {
		t.Fatalf("create integration event fixture: %v", err)
	}

	result := agtDo(h, http.MethodGet, "/api/v1/me/events", "", key, "")
	if result.code != http.StatusOK {
		t.Fatalf("GET /api/v1/me/events returned %d, want 200; body=%q", result.code, result.body)
	}
	var response struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal([]byte(result.body), &response); err != nil {
		t.Fatalf("decode GET /api/v1/me/events response: %v; body=%q", err, result.body)
	}
	if len(response.Items) == 0 {
		t.Fatal("GET /api/v1/me/events returned no event items")
	}
	wantID, _ := json.Marshal(integration.ID)
	wantName, _ := json.Marshal(name)
	foundFixture := false
	for i, item := range response.Items {
		for _, required := range []string{"integration_id", "integration_name"} {
			if _, ok := item[required]; !ok {
				t.Errorf("event item %d lacks %q: %s", i, required, result.body)
			}
		}
		for _, legacy := range []string{"agent_id", "agent_name"} {
			if _, ok := item[legacy]; ok {
				t.Errorf("event item %d still has legacy %q: %s", i, legacy, result.body)
			}
		}
		if string(item["integration_id"]) == string(wantID) && string(item["integration_name"]) == string(wantName) {
			foundFixture = true
		}
	}
	if !foundFixture {
		t.Errorf("GET /api/v1/me/events has no item for integration %d named %q: %s", integration.ID, name, result.body)
	}
}

func TestIntegrationExternalRenameDiscoveryResponsesContainNoAgent(t *testing.T) {
	h := newAccessHarness(t)
	for _, target := range []string{"/api/v1", "/api/v1/openapi.json"} {
		result := agtDo(h, http.MethodGet, target, "", "", "")
		if result.code != http.StatusOK {
			t.Fatalf("GET %s returned %d, want 200; body=%q", target, result.code, result.body)
		}
		body := strings.ToLower(withoutUserAgentTerms(result.body))
		if strings.Contains(body, "agent") {
			t.Errorf("GET %s response contains agent after removing User-Agent spellings", target)
		}
	}
}

func TestIntegrationExternalRenameStaticSourcesContainNoAgent(t *testing.T) {
	root := repoRoot(t)
	dirs := []string{
		filepath.Join(root, "web", "admin"),
		filepath.Join(root, "web", "locales"),
		filepath.Join(root, "design", "project"),
		filepath.Join(root, "internal", "server"),
		filepath.Join(root, "internal", "integration"),
		filepath.Join(root, "internal", "auth"),
	}
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("static scan directory %s is unavailable: %v", dir, err)
		}
	}
	for _, dir := range dirs {
		var matches []staticAgentNameError
		err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				// Vendored packages carry their own LICENSE and keep upstream text.
				if _, err := os.Stat(filepath.Join(path, "LICENSE")); err == nil {
					return filepath.SkipDir
				}
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if !integrationRenameStaticFile(dir, path) {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.ContainsRune(string(raw), '\x00') {
				return nil
			}
			body := strings.ToLower(withoutUserAgentTerms(string(raw)))
			match := strings.Index(body, "agent")
			if match >= 0 {
				line := strings.Count(body[:match], "\n") + 1
				matches = append(matches, staticAgentNameError{path: filepath.ToSlash(rel), line: line})
			}
			return nil
		})
		if err != nil {
			t.Errorf("scan %s: %v", dir, err)
		}
		for _, match := range matches {
			t.Errorf("%s:%d contains agent outside an allowed User-Agent spelling", match.path, match.line)
		}
	}
}

type staticAgentNameError struct {
	path string
	line int
}

func integrationRenameStaticFile(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	switch {
	case strings.HasSuffix(root, filepath.Join("web", "admin")):
		return true
	case strings.HasSuffix(root, filepath.Join("web", "locales")):
		return true
	case strings.HasSuffix(root, filepath.Join("design", "project")):
		return strings.HasSuffix(path, ".dc.html") || filepath.Base(path) == "canvas.json"
	default:
		return strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") && rel != "."
	}
}

// withoutUserAgentTerms drops the spellings that name the HTTP User-Agent
// header and the meta-externalagent crawler, which keep their real names, and
// the colour name magenta, which only contains the letters.
func withoutUserAgentTerms(s string) string {
	lower := strings.ToLower(s)
	for _, term := range []string{"user-agent", "user_agent", "user agent", "useragent", "meta-externalagent", "magenta"} {
		lower = strings.ReplaceAll(lower, term, "")
	}
	return lower
}
