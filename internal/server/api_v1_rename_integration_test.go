package server

// Acceptance tests for ticket 24 (docs/tickets/24-api-v1-rename.md): the machine API moves from
// /api/agent/v1 to /api/v1, the admin pages move from /admin/agents to /admin/integrations, and the
// user-visible wording changes from "AI agent" and "agent key" to "API integration" and "access key".
// The old paths are removed without aliases, except that the old admin pages redirect with 302.
// They need JELLY_TEST_POSTGRES=1 and the dedicated PostgreSQL container, like the other access tests.

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

// renameV1OldNames lists the old spellings that must not reach a machine client.
var renameV1OldNames = []string{"/api/agent/v1", "agentkey", "agent key"}

// renameV1CheckNoOldNames fails when body still contains one of the old spellings. The match ignores case.
func renameV1CheckNoOldNames(t *testing.T, where, body string) {
	t.Helper()
	lower := strings.ToLower(body)
	for _, name := range renameV1OldNames {
		if strings.Contains(lower, name) {
			t.Errorf("%s still contains the old name %q", where, name)
		}
	}
}

// renameV1CheckAccessKeyMessage fails unless message names an access key and does not name an agent key.
func renameV1CheckAccessKeyMessage(t *testing.T, where, message string) {
	t.Helper()
	lower := strings.ToLower(message)
	if !strings.Contains(lower, "access key") {
		t.Errorf("%s: message %q does not mention an access key", where, message)
	}
	if strings.Contains(lower, "agent key") {
		t.Errorf("%s: message %q still mentions an agent key", where, message)
	}
}

// renameV1Cookie requests an admin page with the jwt_token browser login, as a signed-in browser would.
func renameV1Cookie(h *accessHarness, method, target, token string) accessResult {
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set(echo.HeaderAccept, echo.MIMETextHTML)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: "jwt_token", Value: token})
	}
	rec := httptest.NewRecorder()
	var panicked any
	func() { defer func() { panicked = recover() }(); h.echo.ServeHTTP(rec, req) }()
	return accessResult{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone(), panicked: panicked}
}

// renameV1UserVisibleFiles are the files whose visible text must not say "AI agent".
func renameV1UserVisibleFiles(t *testing.T) []string {
	t.Helper()
	root := repoRoot(t)
	files := []string{
		filepath.Join(root, "web", "admin", "assets", "agents.js"),
		filepath.Join(root, "web", "admin", "agents.html"),
		filepath.Join(root, "web", "admin", "agent_detail.html"),
	}
	locales, err := filepath.Glob(filepath.Join(root, "web", "locales", "*.json"))
	if err != nil {
		t.Fatalf("list locale files: %v", err)
	}
	if len(locales) == 0 {
		t.Fatal("no locale files under web/locales")
	}
	return append(files, locales...)
}

func TestAPIV1RenameOldMachineURLsAreGone(t *testing.T) {
	h := newAccessHarness(t)
	for _, target := range []string{"/api/agent/v1", "/api/agent/v1/openapi.json", "/api/agent/v1/me"} {
		r := agtDo(h, http.MethodGet, target, "", "", "")
		if r.code != http.StatusNotFound {
			t.Errorf("GET %s without a key returned %d, want 404 (the old path is removed without an alias); body=%q", target, r.code, r.body)
		}
	}
}

func TestAPIV1RenameDiscoveryIndexUsesNewPaths(t *testing.T) {
	h := newAccessHarness(t)
	r := agtDo(h, http.MethodGet, "/api/v1", "", "", "")
	if r.code != http.StatusOK {
		t.Fatalf("GET /api/v1 returned %d, want 200; body=%q", r.code, r.body)
	}
	var index struct {
		BasePath   string `json:"base_path"`
		OpenAPIURL string `json:"openapi_url"`
	}
	if err := json.Unmarshal([]byte(r.body), &index); err != nil {
		t.Fatalf("GET /api/v1 body is not JSON: %v; body=%q", err, r.body)
	}
	if index.BasePath != "/api/v1" {
		t.Errorf("base_path=%q, want /api/v1", index.BasePath)
	}
	if index.OpenAPIURL != "/api/v1/openapi.json" {
		t.Errorf("openapi_url=%q, want /api/v1/openapi.json", index.OpenAPIURL)
	}
	renameV1CheckNoOldNames(t, "GET /api/v1 body", r.body)
}

func TestAPIV1RenameOpenAPIDocumentUsesAccessKey(t *testing.T) {
	h := newAccessHarness(t)
	r := agtDo(h, http.MethodGet, "/api/v1/openapi.json", "", "", "")
	if r.code != http.StatusOK {
		t.Fatalf("GET /api/v1/openapi.json returned %d, want 200; body=%q", r.code, r.body)
	}
	var doc struct {
		OpenAPI    string `json:"openapi"`
		Paths      map[string]json.RawMessage
		Components struct {
			SecuritySchemes map[string]json.RawMessage `json:"securitySchemes"`
		} `json:"components"`
	}
	if err := json.Unmarshal([]byte(r.body), &doc); err != nil {
		t.Fatalf("GET /api/v1/openapi.json body is not JSON: %v", err)
	}
	if doc.OpenAPI != "3.1.0" {
		t.Errorf("openapi=%q, want 3.1.0", doc.OpenAPI)
	}
	if len(doc.Paths) == 0 {
		t.Error("paths is empty")
	}
	for path := range doc.Paths {
		if !strings.HasPrefix(path, "/api/v1") {
			t.Errorf("path %q is outside /api/v1", path)
		}
	}
	if len(doc.Components.SecuritySchemes) != 1 {
		t.Errorf("securitySchemes has %d entries, want exactly one", len(doc.Components.SecuritySchemes))
	}
	if _, ok := doc.Components.SecuritySchemes["AccessKey"]; !ok {
		t.Errorf("securitySchemes keys do not include AccessKey")
	}
	renameV1CheckNoOldNames(t, "GET /api/v1/openapi.json body", r.body)
}

func TestAPIV1RenameMissingKeyMessageNamesAccessKey(t *testing.T) {
	h := newAccessHarness(t)
	r := agtDo(h, http.MethodGet, "/api/v1/me", "", "", "")
	env := agtExpectError(t, r, http.StatusUnauthorized, "key_missing", "GET /api/v1/me without a key")
	renameV1CheckAccessKeyMessage(t, "GET /api/v1/me without a key", env.Message)
	renameV1CheckNoOldNames(t, "GET /api/v1/me without a key body", r.body)
}

func TestAPIV1RenameUnknownKeyMessageNamesAccessKey(t *testing.T) {
	h := newAccessHarness(t)
	unknown := "jly_" + strings.Repeat("A", 43)
	r := agtDo(h, http.MethodGet, "/api/v1/me", "", unknown, "")
	env := agtExpectError(t, r, http.StatusUnauthorized, "key_invalid", "GET /api/v1/me with an unknown key")
	renameV1CheckAccessKeyMessage(t, "GET /api/v1/me with an unknown key", env.Message)
	renameV1CheckNoOldNames(t, "GET /api/v1/me with an unknown key body", r.body)
}

func TestAPIV1RenameDiscoveryOpenDuringMaintenance(t *testing.T) {
	h := newAccessHarness(t)
	maintenanceEnable(t, h, 1)
	if r := agtDo(h, http.MethodGet, "/api/search?q=x", "", "", ""); r.code != http.StatusServiceUnavailable {
		t.Fatalf("maintenance is not on: /api/search returned %d", r.code)
	}
	agtExpectOK(t, agtDo(h, http.MethodGet, "/api/v1", "", "", ""), "GET /api/v1 during maintenance")
}

func TestAPIV1RenameUploadAboveGlobalLimitIsNotCapped(t *testing.T) {
	h := newAccessHarness(t)
	_, _, uploader := agtSetup(t, h, "Rename upload", "media.write")

	// The global request cap is 2 MiB. A file just above it must reach the media handler.
	payload := append(medPNG(), bytes.Repeat([]byte{0}, 2*1024*1024)...)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "api-v1-rename-large.png")
	if err != nil {
		t.Fatalf("create multipart file part: %v", err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatalf("write multipart file part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/files", &body)
	req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
	req.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+uploader.APIKey)
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	result := agtResult{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone()}
	if result.code == http.StatusRequestEntityTooLarge || strings.Contains(result.body, "payload_too_large") {
		t.Errorf("POST /api/v1/media/files with a %d-byte file was stopped by the global limit: %d %q", len(payload), result.code, result.body)
	}
	if result.code == http.StatusNotFound {
		t.Errorf("POST /api/v1/media/files returned 404; the route does not exist yet; body=%q", result.body)
	}
}

func TestAPIV1RenameOldAdminAgentPagesRedirect(t *testing.T) {
	h := newAccessHarness(t)
	adminToken := h.token(h.user("admin", "api-v1-rename-admin"))
	cases := []struct{ from, to string }{
		{"/admin/agents", "/admin/integrations"},
		{"/admin/agents/1", "/admin/integrations/1"},
		{"/admin/agents/1?tab=keys", "/admin/integrations/1?tab=keys"},
	}
	for _, tc := range cases {
		r := renameV1Cookie(h, http.MethodGet, tc.from, adminToken)
		if r.panicked != nil {
			t.Fatalf("GET %s panicked: %v", tc.from, r.panicked)
		}
		if r.code != http.StatusFound {
			t.Errorf("GET %s returned %d, want 302", tc.from, r.code)
		}
		if got := r.header.Get("Location"); got != tc.to {
			t.Errorf("GET %s Location=%q, want %q", tc.from, got, tc.to)
		}
	}
}

func TestAPIV1RenameIntegrationsPageIsChineseByDefault(t *testing.T) {
	h := newAccessHarness(t)
	adminToken := h.token(h.user("admin", "api-v1-rename-zh"))
	for _, target := range []string{"/admin/integrations", "/admin/integrations/1"} {
		r := renameV1Cookie(h, http.MethodGet, target, adminToken)
		if r.panicked != nil {
			t.Fatalf("GET %s panicked: %v", target, r.panicked)
		}
		if r.code != http.StatusOK {
			t.Errorf("GET %s returned %d, want 200", target, r.code)
			continue
		}
		if !strings.Contains(r.body, "API 串接") {
			t.Errorf("GET %s does not show the title %q", target, "API 串接")
		}
		if strings.Contains(r.body, "AI agent") {
			t.Errorf("GET %s still shows %q", target, "AI agent")
		}
	}
}

func TestAPIV1RenameIntegrationsPageIsEnglishWithLangParam(t *testing.T) {
	h := newAccessHarness(t)
	adminToken := h.token(h.user("admin", "api-v1-rename-en"))
	r := renameV1Cookie(h, http.MethodGet, "/admin/integrations?lang=en", adminToken)
	if r.panicked != nil {
		t.Fatalf("GET /admin/integrations?lang=en panicked: %v", r.panicked)
	}
	if r.code != http.StatusOK {
		t.Fatalf("GET /admin/integrations?lang=en returned %d, want 200", r.code)
	}
	if !strings.Contains(r.body, "API integrations") {
		t.Errorf("GET /admin/integrations?lang=en does not show %q", "API integrations")
	}
}

func TestAPIV1RenameSidebarLinksToIntegrations(t *testing.T) {
	h := newAccessHarness(t)
	adminToken := h.token(h.user("admin", "api-v1-rename-rail"))
	r := renameV1Cookie(h, http.MethodGet, "/admin", adminToken)
	if r.panicked != nil {
		t.Fatalf("GET /admin panicked: %v", r.panicked)
	}
	if r.code != http.StatusOK {
		t.Fatalf("GET /admin returned %d, want 200", r.code)
	}
	if !strings.Contains(r.body, `href="/admin/integrations"`) {
		t.Error("sidebar has no link to /admin/integrations")
	}
	if strings.Contains(r.body, `href="/admin/agents"`) {
		t.Error("sidebar still links to /admin/agents")
	}
}

func TestAPIV1RenameUserVisibleTextHasNoAIAgent(t *testing.T) {
	for _, path := range renameV1UserVisibleFiles(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		for number, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			// Comments are not visible to users. Skip them, and ignore the HTTP header name User-Agent.
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "<!--") {
				continue
			}
			visible := strings.ReplaceAll(line, "User-Agent", "")
			if strings.Contains(visible, "AI agent") {
				t.Errorf("%s:%d shows %q to users: %s", path, number+1, "AI agent", trimmed)
			}
		}
	}
}
