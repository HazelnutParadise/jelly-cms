package server

// Acceptance tests for ticket 14 (docs/tickets/14-agent-api.md): AI agent identities and keys.
// Slots: A1 (schema), A2 (management API M1–M9: create, rotate, revoke, disable, enable, delete and
// their events), A5 (admin event log across rotations) and A6 (admin-only pages). The contract is the
// 契約 section of the ticket plus its 「測試另外釘下的約定」. Several tests also call GET
// /api/agent/v1/me from slot A3 to prove a key works or fails.
//
// These tests were introduced against the draft skeleton; they now define the regression contract.
// The agent schema is migration 0025_agents.sql. They need JELLY_TEST_POSTGRES=1 and the dedicated
// PostgreSQL container. Grace expiry is simulated by moving agent_keys.expires_at into the past with
// SQL; owner downgrade and deletion change the users row with SQL.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/labstack/echo/v4"
)

var (
	agtKeyPattern       = regexp.MustCompile(`^jly_[A-Za-z0-9_-]{43}$`)
	agtRequestIDPattern = regexp.MustCompile(`^req_[0-9A-Za-z_-]{8,64}$`)
)

// agtAllPermissions is the permission catalogue in order.
var agtAllPermissions = []string{
	"posts.read", "posts.write", "pages.read", "pages.write", "media.read", "media.write",
	"products.read", "products.write", "orders.read", "themes.read", "themes.write",
	"plugins.read", "plugins.write", "settings.read", "settings.write", "logs.read",
}

type agtResult struct {
	code     int
	body     string
	header   http.Header
	panicked any
}

type agtKey struct {
	ID         uint    `json:"id"`
	Prefix     string  `json:"prefix"`
	Status     string  `json:"status"`
	CreatedAt  *string `json:"created_at"`
	LastUsedAt *string `json:"last_used_at"`
	ExpiresAt  *string `json:"expires_at"`
	RevokedAt  *string `json:"revoked_at"`
}

type agtOwner struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

type agtAgent struct {
	ID                   uint      `json:"id"`
	Name                 string    `json:"name"`
	Description          string    `json:"description"`
	Permissions          []string  `json:"permissions"`
	EffectivePermissions []string  `json:"effective_permissions"`
	Status               string    `json:"status"`
	Owner                *agtOwner `json:"owner"`
	DisabledAt           *string   `json:"disabled_at"`
	Keys                 []agtKey  `json:"keys"`
}

type agtCreated struct {
	Agent  agtAgent `json:"agent"`
	Key    agtKey   `json:"key"`
	APIKey string   `json:"api_key"`
}

type agtRotated struct {
	Agent       agtAgent `json:"agent"`
	Key         agtKey   `json:"key"`
	APIKey      string   `json:"api_key"`
	PreviousKey *agtKey  `json:"previous_key"`
}

type agtEnvelope struct {
	Code          string         `json:"code"`
	Message       string         `json:"message"`
	Details       map[string]any `json:"details"`
	CorrelationID string         `json:"correlation_id"`
}

type agtEvent struct {
	ID            int64          `json:"id"`
	At            string         `json:"at"`
	ActorType     string         `json:"actor_type"`
	ActorUserID   *uint          `json:"actor_user_id"`
	AgentID       uint           `json:"agent_id"`
	AgentName     string         `json:"agent_name"`
	KeyID         *uint          `json:"key_id"`
	KeyPrefix     string         `json:"key_prefix"`
	Action        string         `json:"action"`
	Module        string         `json:"module"`
	Method        string         `json:"method"`
	Route         string         `json:"route"`
	Status        int            `json:"status"`
	IPMasked      string         `json:"ip_masked"`
	Summary       map[string]any `json:"summary"`
	CorrelationID string         `json:"correlation_id"`
}

type agtEventPage struct {
	Items      []agtEvent `json:"items"`
	NextCursor *string    `json:"next_cursor"`
	Total      int        `json:"total"`
}

// agtDo sends one request the way an agent would: no cookies, no CSRF header, the key as a bearer
// token and the given RemoteAddr as the source address. It never calls t methods, so goroutines may use it.
func agtDo(h *accessHarness, method, target, body, bearer, remoteAddr string) agtResult {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, r)
	req.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
	if body != "" {
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	if bearer != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+bearer)
	}
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	rec := httptest.NewRecorder()
	var panicked any
	func() { defer func() { panicked = recover() }(); h.echo.ServeHTTP(rec, req) }()
	return agtResult{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone(), panicked: panicked}
}

// agtAdmin calls the management API as a signed-in admin (JWT bearer, like the admin screens' fetches).
func agtAdmin(h *accessHarness, token, method, target, body string) agtResult {
	h.t.Helper()
	r := h.request(method, target, body, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	return agtResult{code: r.code, body: r.body, header: r.header, panicked: r.panicked}
}

func agtDecode(t *testing.T, r agtResult, into any, where string) {
	t.Helper()
	if r.panicked != nil {
		t.Fatalf("%s panicked: %v", where, r.panicked)
	}
	if err := json.Unmarshal([]byte(r.body), into); err != nil {
		t.Fatalf("%s: decode JSON: %v; status=%d body=%q", where, err, r.code, r.body)
	}
}

// agtExpectError checks the agent error envelope: status, code, details object and a correlation_id
// equal to the X-Request-ID header.
func agtExpectError(t *testing.T, r agtResult, status int, code, where string) agtEnvelope {
	t.Helper()
	if r.panicked != nil {
		t.Fatalf("%s panicked: %v", where, r.panicked)
	}
	if r.code != status {
		t.Errorf("%s returned %d, want %d (%s); body=%q", where, r.code, status, code, r.body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r.body), &raw); err != nil {
		t.Errorf("%s: error body is not a JSON object: %v; body=%q", where, err, r.body)
		return agtEnvelope{}
	}
	for _, key := range []string{"code", "message", "details", "correlation_id"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("%s: error body is missing %q; body=%q", where, key, r.body)
		}
	}
	if _, legacy := raw["error"]; legacy {
		t.Errorf("%s: error body still uses the legacy \"error\" key; body=%q", where, r.body)
	}
	var env agtEnvelope
	_ = json.Unmarshal([]byte(r.body), &env)
	if env.Code != code {
		t.Errorf("%s: code=%q, want %q; body=%q", where, env.Code, code, r.body)
	}
	if env.Message == "" {
		t.Errorf("%s: message is empty", where)
	}
	if d := strings.TrimSpace(string(raw["details"])); !strings.HasPrefix(d, "{") {
		t.Errorf("%s: details=%s, want a JSON object", where, d)
	}
	id := r.header.Get(echo.HeaderXRequestID)
	if !agtRequestIDPattern.MatchString(id) {
		t.Errorf("%s: X-Request-ID=%q, want req_ followed by 8–64 id characters", where, id)
	}
	if env.CorrelationID != id {
		t.Errorf("%s: correlation_id=%q, want it to equal X-Request-ID %q", where, env.CorrelationID, id)
	}
	return env
}

func agtCreateBody(name string, permissions ...string) string {
	payload, _ := json.Marshal(map[string]any{"name": name, "description": name + " 的說明", "permissions": permissions})
	return string(payload)
}

// agtCreate creates an agent through M2 and returns the one-time response.
func agtCreate(t *testing.T, h *accessHarness, adminToken, name string, permissions ...string) agtCreated {
	t.Helper()
	r := agtAdmin(h, adminToken, http.MethodPost, "/api/admin/agents", agtCreateBody(name, permissions...))
	if r.code != http.StatusCreated {
		t.Fatalf("POST /api/admin/agents %q returned %d, want 201; body=%q", name, r.code, r.body)
	}
	var created agtCreated
	agtDecode(t, r, &created, "create agent")
	if !agtKeyPattern.MatchString(created.APIKey) {
		t.Fatalf("api_key=%q, want jly_ followed by 43 base64url characters", created.APIKey)
	}
	if created.Agent.ID == 0 {
		t.Fatalf("created agent has no id; body=%q", r.body)
	}
	return created
}

func agtRotate(t *testing.T, h *accessHarness, adminToken string, agentID uint, body string) agtRotated {
	t.Helper()
	r := agtAdmin(h, adminToken, http.MethodPost, fmt.Sprintf("/api/admin/agents/%d/rotate", agentID), body)
	if r.code != http.StatusOK {
		t.Fatalf("rotate agent %d with %s returned %d, want 200; body=%q", agentID, body, r.code, r.body)
	}
	var rotated agtRotated
	agtDecode(t, r, &rotated, "rotate agent")
	if !agtKeyPattern.MatchString(rotated.APIKey) {
		t.Fatalf("rotated api_key=%q, want a jly_ key", rotated.APIKey)
	}
	return rotated
}

func agtDetail(t *testing.T, h *accessHarness, adminToken string, agentID uint) agtAgent {
	t.Helper()
	r := agtAdmin(h, adminToken, http.MethodGet, fmt.Sprintf("/api/admin/agents/%d", agentID), "")
	if r.code != http.StatusOK {
		t.Fatalf("GET /api/admin/agents/%d returned %d, want 200; body=%q", agentID, r.code, r.body)
	}
	var a agtAgent
	agtDecode(t, r, &a, "agent detail")
	return a
}

func agtEvents(t *testing.T, h *accessHarness, adminToken string, query url.Values) (agtEventPage, agtResult) {
	t.Helper()
	r := agtAdmin(h, adminToken, http.MethodGet, "/api/admin/agents/events?"+query.Encode(), "")
	if r.code != http.StatusOK {
		t.Fatalf("GET /api/admin/agents/events?%s returned %d, want 200; body=%q", query.Encode(), r.code, r.body)
	}
	var page agtEventPage
	agtDecode(t, r, &page, "admin agent events")
	return page, r
}

func agtMe(h *accessHarness, key string) agtResult {
	return agtDo(h, http.MethodGet, "/api/agent/v1/me", "", key, "")
}

func agtExpectOK(t *testing.T, r agtResult, where string) {
	t.Helper()
	if r.panicked != nil {
		t.Fatalf("%s panicked: %v", where, r.panicked)
	}
	if r.code != http.StatusOK {
		t.Errorf("%s returned %d, want 200; body=%q", where, r.code, r.body)
	}
}

func agtSameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, v := range a {
		seen[v]++
	}
	for _, v := range b {
		seen[v]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func agtContains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func agtParseTime(t *testing.T, value *string, where string) time.Time {
	t.Helper()
	if value == nil {
		t.Fatalf("%s is null, want a timestamp", where)
	}
	parsed, err := time.Parse(time.RFC3339Nano, *value)
	if err != nil {
		t.Fatalf("%s=%q is not RFC3339: %v", where, *value, err)
	}
	return parsed
}

func agtKeyByPrefix(keys []agtKey, prefix string) (agtKey, bool) {
	for _, k := range keys {
		if k.Prefix == prefix {
			return k, true
		}
	}
	return agtKey{}, false
}

func TestAgentSchemaMigrationIsRepeatable(t *testing.T) {
	h := newAccessHarness(t)
	if err := data.MigrateDB(h.db); err != nil {
		t.Fatalf("second MigrateDB on a migrated database: %v", err)
	}
	want := map[string][]string{
		"agents":       {"id", "name", "description", "permissions", "owner_id", "created_by", "status", "disabled_at", "created_at", "updated_at", "deleted_at"},
		"agent_keys":   {"id", "agent_id", "key_hash", "prefix", "created_at", "created_by", "last_used_at", "last_used_ip_masked", "expires_at", "revoked_at", "revoked_by"},
		"agent_events": {"id", "at", "actor_type", "actor_user_id", "agent_id", "agent_name", "key_id", "key_prefix", "action", "module", "method", "route", "status", "ip_masked", "summary", "request_id"},
	}
	for table, columns := range want {
		var got []string
		if err := h.db.Raw("SELECT column_name FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = ?", table).Scan(&got).Error; err != nil {
			t.Fatalf("read columns of %s: %v", table, err)
		}
		for _, column := range columns {
			if !agtContains(got, column) {
				t.Errorf("table %s is missing column %s (has %v)", table, column, got)
			}
		}
		for _, forbidden := range []string{"key", "api_key", "plaintext", "secret", "key_enc", "key_plain"} {
			if agtContains(got, forbidden) {
				t.Errorf("table %s has column %s; only the key hash and prefix may be stored", table, forbidden)
			}
		}
	}
}

func TestAgentCreateShowsKeyOnceAndStoresOnlyHash(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "agt-create-admin")
	token := h.token(admin)

	created := agtCreate(t, h, token, "內容小幫手", "posts.read", "posts.write")
	key := created.APIKey
	prefix := key[:12]
	if created.Key.Prefix != prefix {
		t.Errorf("key.prefix=%q, want the first 12 characters of api_key %q", created.Key.Prefix, prefix)
	}
	if created.Key.Status != "active" {
		t.Errorf("key.status=%q, want active", created.Key.Status)
	}
	a := created.Agent
	if a.Name != "內容小幫手" || !agtSameStrings(a.Permissions, []string{"posts.read", "posts.write"}) || a.Status != "active" {
		t.Errorf("created agent = %+v, want name 內容小幫手, permissions posts.read+posts.write, status active", a)
	}
	if a.Owner == nil || a.Owner.ID != admin.ID || a.Owner.Role != "admin" {
		t.Errorf("owner=%+v, want the creating admin %d", a.Owner, admin.ID)
	}
	if len(a.Keys) != 1 || a.Keys[0].Prefix != prefix {
		t.Errorf("agent.keys=%+v, want exactly the new key %s", a.Keys, prefix)
	}

	// The plaintext never appears again: list, detail and event log.
	list := agtAdmin(h, token, http.MethodGet, "/api/admin/agents", "")
	agtExpectOK(t, list, "GET /api/admin/agents")
	var listed struct {
		Items []agtAgent `json:"items"`
		Total int        `json:"total"`
	}
	agtDecode(t, list, &listed, "agent list")
	if listed.Total != 1 || len(listed.Items) != 1 || listed.Items[0].ID != a.ID {
		t.Errorf("agent list = %+v, want the one created agent", listed)
	}
	detail := agtAdmin(h, token, http.MethodGet, fmt.Sprintf("/api/admin/agents/%d", a.ID), "")
	agtExpectOK(t, detail, "GET /api/admin/agents/:id")
	events := agtAdmin(h, token, http.MethodGet, fmt.Sprintf("/api/admin/agents/events?agent_id=%d", a.ID), "")
	agtExpectOK(t, events, "GET /api/admin/agents/events")
	for _, r := range []struct {
		name string
		res  agtResult
	}{{"list", list}, {"detail", detail}, {"events", events}} {
		if strings.Contains(r.res.body, key) || strings.Contains(r.res.body, key[4:]) {
			t.Errorf("%s response contains the plaintext key", r.name)
		}
		if strings.Contains(r.res.body, "key_hash") {
			t.Errorf("%s response exposes key_hash", r.name)
		}
	}

	// The database holds only the hash and the prefix.
	for _, table := range []string{"agents", "agent_keys", "agent_events"} {
		var dump string
		if err := h.db.Raw(fmt.Sprintf("SELECT coalesce(string_agg(row_to_json(t)::text, E'\\n'), '') FROM %s t", table)).Scan(&dump).Error; err != nil {
			t.Fatalf("dump %s: %v", table, err)
		}
		if strings.Contains(dump, key[4:]) {
			t.Errorf("table %s contains the plaintext key", table)
		}
	}
	var stored struct {
		KeyHash string
		Prefix  string
	}
	if err := h.db.Raw("SELECT key_hash, prefix FROM agent_keys WHERE agent_id = ?", a.ID).Scan(&stored).Error; err != nil {
		t.Fatalf("read agent_keys: %v", err)
	}
	sum := sha256.Sum256([]byte(key))
	if stored.KeyHash != hex.EncodeToString(sum[:]) {
		t.Errorf("agent_keys.key_hash=%q, want the lowercase hex SHA-256 of the whole key", stored.KeyHash)
	}
	if stored.Prefix != prefix {
		t.Errorf("agent_keys.prefix=%q, want %q", stored.Prefix, prefix)
	}

	// The one-time key works, and creation was recorded as a user action.
	agtExpectOK(t, agtMe(h, key), "GET /api/agent/v1/me with the new key")
	page, _ := agtEvents(t, h, token, url.Values{"agent_id": {fmt.Sprint(a.ID)}, "action": {"agent.create"}})
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("agent.create events = %+v, want exactly one", page)
	}
	ev := page.Items[0]
	if ev.ActorType != "user" || ev.ActorUserID == nil || *ev.ActorUserID != admin.ID || ev.Module != "agents" || ev.KeyPrefix != prefix {
		t.Errorf("agent.create event = %+v, want actor user %d, module agents, key_prefix %s", ev, admin.ID, prefix)
	}
}

func TestAgentCreateValidation(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "agt-validate-admin"))

	cases := []struct {
		name, body, code string
		status           int
	}{
		{"empty name", `{"name":"","permissions":["posts.read"]}`, "name_invalid", http.StatusBadRequest},
		{"blank name", `{"name":"   ","permissions":["posts.read"]}`, "name_invalid", http.StatusBadRequest},
		{"81-character name", agtCreateBody(strings.Repeat("名", 81), "posts.read"), "name_invalid", http.StatusBadRequest},
		{"501-character description", fmt.Sprintf(`{"name":"long description","description":%q,"permissions":["posts.read"]}`, strings.Repeat("x", 501)), "description_too_long", http.StatusBadRequest},
		{"no permissions", `{"name":"no permissions","permissions":[]}`, "permissions_required", http.StatusBadRequest},
		{"agents permission", `{"name":"manager","permissions":["posts.read","agents.write"]}`, "permission_unknown", http.StatusBadRequest},
		{"unknown permission", `{"name":"deleter","permissions":["posts.delete"]}`, "permission_unknown", http.StatusBadRequest},
	}
	for _, tc := range cases {
		r := agtAdmin(h, token, http.MethodPost, "/api/admin/agents", tc.body)
		env := agtExpectError(t, r, tc.status, tc.code, "create with "+tc.name)
		if tc.code == "permission_unknown" {
			unknown, _ := env.Details["unknown"].([]any)
			if len(unknown) != 1 {
				t.Errorf("create with %s: details.unknown=%v, want the one unknown permission", tc.name, env.Details["unknown"])
			}
		}
	}

	first := agtAdmin(h, token, http.MethodPost, "/api/admin/agents", agtCreateBody("Dup Agent", "posts.read"))
	if first.code != http.StatusCreated {
		t.Fatalf("first create returned %d, want 201; body=%q", first.code, first.body)
	}
	agtExpectError(t, agtAdmin(h, token, http.MethodPost, "/api/admin/agents", agtCreateBody("dup agent", "posts.read")), http.StatusConflict, "agent_name_taken", "create with a name that differs only in case")

	var count int64
	if err := h.db.Raw("SELECT count(*) FROM agents").Scan(&count).Error; err != nil {
		t.Fatalf("count agents: %v", err)
	}
	if count != 1 {
		t.Errorf("agents rows = %d, want 1 (rejected requests must not create agents)", count)
	}
}

func TestAgentManagementRequiresSignedInAdmin(t *testing.T) {
	h := newAccessHarness(t)
	adminToken := h.token(h.user("admin", "agt-mgmt-admin"))
	editorToken := h.token(h.user("editor", "agt-mgmt-editor"))
	created := agtCreate(t, h, adminToken, "被保護的 agent", "posts.read")
	id, keyID, key := created.Agent.ID, created.Key.ID, created.APIKey

	routes := []struct{ method, path, body string }{
		{http.MethodGet, "/api/admin/agents", ""},
		{http.MethodPost, "/api/admin/agents", agtCreateBody("by agent", "posts.read")},
		{http.MethodGet, "/api/admin/agents/events", ""},
		{http.MethodGet, fmt.Sprintf("/api/admin/agents/%d", id), ""},
		{http.MethodPatch, fmt.Sprintf("/api/admin/agents/%d", id), `{"permissions":["posts.read","settings.write"]}`},
		{http.MethodPost, fmt.Sprintf("/api/admin/agents/%d/rotate", id), `{"mode":"immediate"}`},
		{http.MethodPost, fmt.Sprintf("/api/admin/agents/%d/keys/%d/revoke", id, keyID), ""},
		{http.MethodPost, fmt.Sprintf("/api/admin/agents/%d/disable", id), ""},
		{http.MethodPost, fmt.Sprintf("/api/admin/agents/%d/enable", id), ""},
		{http.MethodDelete, fmt.Sprintf("/api/admin/agents/%d", id), ""},
	}
	for _, route := range routes {
		where := route.method + " " + route.path
		if r := agtDo(h, route.method, route.path, route.body, "", ""); r.code != http.StatusUnauthorized {
			t.Errorf("anonymous %s returned %d, want 401", where, r.code)
		}
		if r := agtAdmin(h, editorToken, route.method, route.path, route.body); r.code != http.StatusForbidden {
			t.Errorf("editor %s returned %d, want 403", where, r.code)
		}
		if r := agtDo(h, route.method, route.path, route.body, key, ""); r.code != http.StatusUnauthorized {
			t.Errorf("agent key %s returned %d, want 401", where, r.code)
		}
	}

	// None of those calls changed the agent: same single key, same permissions, still active.
	after := agtDetail(t, h, adminToken, id)
	if len(after.Keys) != 1 || after.Keys[0].ID != keyID || after.Keys[0].Status != "active" || after.Status != "active" || !agtSameStrings(after.Permissions, []string{"posts.read"}) {
		t.Errorf("agent after rejected management calls = %+v, want unchanged", after)
	}
	agtExpectOK(t, agtMe(h, key), "the agent's key after the rejected calls")

	// The agent API has nowhere to manage agents or rotate keys.
	for _, probe := range []struct{ method, path string }{
		{http.MethodGet, "/api/agent/v1/agents"},
		{http.MethodPost, "/api/agent/v1/agents"},
		{http.MethodPost, fmt.Sprintf("/api/agent/v1/agents/%d/rotate", id)},
		{http.MethodPost, "/api/agent/v1/me/rotate"},
		{http.MethodPost, "/api/agent/v1/keys/rotate"},
	} {
		agtExpectError(t, agtDo(h, probe.method, probe.path, `{"mode":"immediate"}`, key, ""), http.StatusNotFound, "not_found", "agent "+probe.method+" "+probe.path)
	}
}

func TestAgentRotateImmediateRevokesOldKeyAtOnce(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "agt-rot-imm-admin")
	token := h.token(admin)
	created := agtCreate(t, h, token, "立即輪替", "posts.read", "logs.read")
	old := created.APIKey
	agtExpectOK(t, agtMe(h, old), "old key before rotation")
	agtExpectOK(t, agtDo(h, http.MethodGet, "/api/agent/v1/posts", "", old, ""), "old key GET /posts before rotation")

	rotated := agtRotate(t, h, token, created.Agent.ID, `{"mode":"immediate"}`)
	fresh := rotated.APIKey
	if fresh == old {
		t.Fatal("rotation returned the old key")
	}
	if rotated.Key.Prefix != fresh[:12] || rotated.Key.Status != "active" {
		t.Errorf("rotated key = %+v, want active with prefix %s", rotated.Key, fresh[:12])
	}
	if rotated.PreviousKey == nil || rotated.PreviousKey.Prefix != old[:12] || rotated.PreviousKey.Status != "revoked" || rotated.PreviousKey.RevokedAt == nil {
		t.Errorf("previous_key = %+v, want the old key %s revoked", rotated.PreviousKey, old[:12])
	}
	a := rotated.Agent
	if a.ID != created.Agent.ID || a.Name != created.Agent.Name || !agtSameStrings(a.Permissions, created.Agent.Permissions) || a.Owner == nil || a.Owner.ID != admin.ID {
		t.Errorf("agent after rotation = %+v, want the same id, name, permissions and owner as %+v", a, created.Agent)
	}

	agtExpectOK(t, agtMe(h, fresh), "new key right after rotation")
	agtExpectError(t, agtMe(h, old), http.StatusUnauthorized, "key_revoked", "old key after immediate rotation")

	detail := agtDetail(t, h, token, created.Agent.ID)
	if len(detail.Keys) != 2 || detail.Keys[0].Prefix != fresh[:12] {
		t.Fatalf("keys after rotation = %+v, want the new key first and the old key kept", detail.Keys)
	}
	if k, _ := agtKeyByPrefix(detail.Keys, old[:12]); k.Status != "revoked" {
		t.Errorf("old key status = %q, want revoked", k.Status)
	}

	page, _ := agtEvents(t, h, token, url.Values{"agent_id": {fmt.Sprint(a.ID)}, "action": {"key.rotate"}})
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("key.rotate events = %+v, want exactly one", page)
	}
	ev := page.Items[0]
	if ev.ActorType != "user" || ev.ActorUserID == nil || *ev.ActorUserID != admin.ID {
		t.Errorf("key.rotate actor = %s/%v, want user %d", ev.ActorType, ev.ActorUserID, admin.ID)
	}
	if ev.Summary["mode"] != "immediate" || ev.Summary["old_key_prefix"] != old[:12] || ev.Summary["new_key_prefix"] != fresh[:12] {
		t.Errorf("key.rotate summary = %v, want mode immediate, old_key_prefix %s, new_key_prefix %s", ev.Summary, old[:12], fresh[:12])
	}

	// Requests made with the old key stay under the same agent.
	reqs, _ := agtEvents(t, h, token, url.Values{"agent_id": {fmt.Sprint(a.ID)}, "action": {"request"}})
	sawOld, sawNew := false, false
	for _, item := range reqs.Items {
		sawOld = sawOld || item.KeyPrefix == old[:12]
		sawNew = sawNew || item.KeyPrefix == fresh[:12]
	}
	if !sawOld || !sawNew {
		t.Errorf("request events for agent %d: saw old key=%v, new key=%v; want both under the same agent", a.ID, sawOld, sawNew)
	}
}

func TestAgentRotateGraceKeepsBothKeysUntilExpiry(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "agt-rot-grace-admin"))
	created := agtCreate(t, h, token, "過渡輪替", "posts.read")
	old := created.APIKey

	before := time.Now()
	rotated := agtRotate(t, h, token, created.Agent.ID, `{"mode":"grace","grace_minutes":30}`)
	fresh := rotated.APIKey
	if rotated.PreviousKey == nil || rotated.PreviousKey.Prefix != old[:12] || rotated.PreviousKey.Status != "grace" || rotated.PreviousKey.RevokedAt != nil {
		t.Fatalf("previous_key = %+v, want the old key in grace and not revoked", rotated.PreviousKey)
	}
	expires := agtParseTime(t, rotated.PreviousKey.ExpiresAt, "previous_key.expires_at")
	if d := expires.Sub(before.Add(30 * time.Minute)); d < -2*time.Minute || d > 2*time.Minute {
		t.Errorf("previous_key.expires_at=%s, want about 30 minutes after the rotation", expires)
	}

	oldMe := agtMe(h, old)
	agtExpectOK(t, oldMe, "old key during grace")
	var me struct {
		Key struct {
			Prefix    string  `json:"prefix"`
			ExpiresAt *string `json:"expires_at"`
		} `json:"key"`
	}
	agtDecode(t, oldMe, &me, "old key /me")
	if me.Key.Prefix != old[:12] || me.Key.ExpiresAt == nil {
		t.Errorf("/me with the old key = %+v, want its prefix and grace expiry", me.Key)
	}
	newMe := agtMe(h, fresh)
	agtExpectOK(t, newMe, "new key during grace")
	agtDecode(t, newMe, &me, "new key /me")
	if me.Key.Prefix != fresh[:12] || me.Key.ExpiresAt != nil {
		t.Errorf("/me with the new key = %+v, want its prefix and expires_at null", me.Key)
	}

	detail := agtDetail(t, h, token, created.Agent.ID)
	if k, _ := agtKeyByPrefix(detail.Keys, old[:12]); k.Status != "grace" {
		t.Errorf("old key status during grace = %q, want grace", k.Status)
	}
	if k, _ := agtKeyByPrefix(detail.Keys, fresh[:12]); k.Status != "active" {
		t.Errorf("new key status = %q, want active", k.Status)
	}

	// The grace period ends.
	if err := h.db.Exec("UPDATE agent_keys SET expires_at = now() - interval '1 second' WHERE prefix = ?", old[:12]).Error; err != nil {
		t.Fatalf("expire the old key: %v", err)
	}
	agtExpectError(t, agtMe(h, old), http.StatusUnauthorized, "key_expired", "old key after the grace period")
	agtExpectOK(t, agtMe(h, fresh), "new key after the grace period")

	page, _ := agtEvents(t, h, token, url.Values{"agent_id": {fmt.Sprint(created.Agent.ID)}, "action": {"key.rotate"}})
	if len(page.Items) != 1 || page.Items[0].Summary["mode"] != "grace" || page.Items[0].Summary["grace_minutes"] != float64(30) {
		t.Errorf("key.rotate events = %+v, want one with mode grace and grace_minutes 30", page.Items)
	}

	// At most two valid keys: rotating again revokes the key that was already in grace.
	second := agtRotate(t, h, token, created.Agent.ID, `{"mode":"grace","grace_minutes":60}`)
	third := agtRotate(t, h, token, created.Agent.ID, `{"mode":"grace","grace_minutes":60}`)
	if second.PreviousKey == nil || second.PreviousKey.Prefix != fresh[:12] || third.PreviousKey == nil || third.PreviousKey.Prefix != second.APIKey[:12] {
		t.Fatalf("previous keys of the second and third rotations = %+v, %+v; want %s then %s", second.PreviousKey, third.PreviousKey, fresh[:12], second.APIKey[:12])
	}
	agtExpectError(t, agtMe(h, fresh), http.StatusUnauthorized, "key_revoked", "the key that was in grace when the agent rotated again")
	agtExpectOK(t, agtMe(h, second.APIKey), "the second key, now in grace")
	agtExpectOK(t, agtMe(h, third.APIKey), "the third key, now active")
	last, _ := agtEvents(t, h, token, url.Values{"agent_id": {fmt.Sprint(created.Agent.ID)}, "action": {"key.rotate"}, "limit": {"1"}})
	if len(last.Items) != 1 {
		t.Fatalf("latest key.rotate event missing: %+v", last)
	}
	revoked, _ := last.Items[0].Summary["revoked_key_prefixes"].([]any)
	if len(revoked) != 1 || revoked[0] != fresh[:12] {
		t.Errorf("latest key.rotate summary.revoked_key_prefixes=%v, want [%s]", last.Items[0].Summary["revoked_key_prefixes"], fresh[:12])
	}
}

func TestAgentRotateGraceBounds(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "agt-rot-bounds-admin"))
	created := agtCreate(t, h, token, "過渡上限", "posts.read")
	rotate := func(body string) agtResult {
		return agtAdmin(h, token, http.MethodPost, fmt.Sprintf("/api/admin/agents/%d/rotate", created.Agent.ID), body)
	}

	for _, body := range []string{
		`{"mode":"grace","grace_minutes":1441}`,
		`{"mode":"grace","grace_minutes":100000}`,
		`{"mode":"grace","grace_minutes":0}`,
		`{"mode":"grace","grace_minutes":-5}`,
		`{"mode":"grace"}`,
	} {
		env := agtExpectError(t, rotate(body), http.StatusBadRequest, "grace_out_of_range", "rotate "+body)
		if env.Details["min"] != float64(1) || env.Details["max"] != float64(1440) {
			t.Errorf("rotate %s: details=%v, want min 1 and max 1440", body, env.Details)
		}
		if !strings.Contains(env.Message, "1440") {
			t.Errorf("rotate %s: message %q does not explain the 1440-minute limit", body, env.Message)
		}
	}
	agtExpectError(t, rotate(`{"mode":"later"}`), http.StatusBadRequest, "rotation_mode_invalid", "rotate with an unknown mode")

	// Rejected rotations change nothing.
	agtExpectOK(t, agtMe(h, created.APIKey), "original key after rejected rotations")
	if d := agtDetail(t, h, token, created.Agent.ID); len(d.Keys) != 1 {
		t.Errorf("keys after rejected rotations = %+v, want only the original", d.Keys)
	}

	before := time.Now()
	max := agtRotate(t, h, token, created.Agent.ID, `{"mode":"grace","grace_minutes":1440}`)
	expires := agtParseTime(t, max.PreviousKey.ExpiresAt, "previous_key.expires_at for 1440 minutes")
	if d := expires.Sub(before.Add(24 * time.Hour)); d < -2*time.Minute || d > 2*time.Minute {
		t.Errorf("1440-minute grace expires at %s, want about 24 hours after the rotation", expires)
	}

	// No mode means immediate, with an empty object or an empty body.
	for _, body := range []string{`{}`, ``} {
		r := agtRotate(t, h, token, created.Agent.ID, body)
		if r.PreviousKey == nil || r.PreviousKey.Status != "revoked" {
			t.Errorf("rotate with body %q: previous_key=%+v, want it revoked (immediate is the default)", body, r.PreviousKey)
		}
	}
}

func TestAgentRevokeGraceKeyEarly(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "agt-revoke-admin")
	token := h.token(admin)
	created := agtCreate(t, h, token, "提前撤銷", "posts.read")
	other := agtCreate(t, h, token, "別的 agent", "posts.read")
	old := created.APIKey
	rotated := agtRotate(t, h, token, created.Agent.ID, `{"mode":"grace","grace_minutes":60}`)
	oldID := rotated.PreviousKey.ID

	revokePath := fmt.Sprintf("/api/admin/agents/%d/keys/%d/revoke", created.Agent.ID, oldID)
	r := agtAdmin(h, token, http.MethodPost, revokePath, "")
	agtExpectOK(t, r, "revoke the grace key")
	var k agtKey
	agtDecode(t, r, &k, "revoked key")
	if k.ID != oldID || k.Status != "revoked" || k.RevokedAt == nil {
		t.Errorf("revoke response = %+v, want key %d revoked", k, oldID)
	}
	agtExpectError(t, agtMe(h, old), http.StatusUnauthorized, "key_revoked", "grace key right after early revocation")
	agtExpectOK(t, agtMe(h, rotated.APIKey), "new key after the old one was revoked early")

	again := agtAdmin(h, token, http.MethodPost, revokePath, "")
	agtExpectOK(t, again, "revoke the same key again")

	agtExpectError(t, agtAdmin(h, token, http.MethodPost, fmt.Sprintf("/api/admin/agents/%d/keys/%d/revoke", created.Agent.ID, other.Key.ID), ""), http.StatusNotFound, "key_not_found", "revoke another agent's key through this agent")
	agtExpectError(t, agtAdmin(h, token, http.MethodPost, fmt.Sprintf("/api/admin/agents/%d/keys/987654321/revoke", created.Agent.ID), ""), http.StatusNotFound, "key_not_found", "revoke a key that does not exist")
	agtExpectOK(t, agtMe(h, other.APIKey), "the other agent's key")

	page, _ := agtEvents(t, h, token, url.Values{"agent_id": {fmt.Sprint(created.Agent.ID)}, "action": {"key.revoke"}})
	if len(page.Items) == 0 {
		t.Fatal("no key.revoke event")
	}
	ev := page.Items[len(page.Items)-1]
	if ev.ActorType != "user" || ev.ActorUserID == nil || *ev.ActorUserID != admin.ID || ev.KeyPrefix != old[:12] {
		t.Errorf("key.revoke event = %+v, want actor user %d and key_prefix %s", ev, admin.ID, old[:12])
	}
}

func TestAgentDisableBlocksEveryKeyIncludingGrace(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "agt-disable-admin"))
	created := agtCreate(t, h, token, "停用測試", "posts.read")
	old := created.APIKey
	rotated := agtRotate(t, h, token, created.Agent.ID, `{"mode":"grace","grace_minutes":60}`)
	fresh := rotated.APIKey

	r := agtAdmin(h, token, http.MethodPost, fmt.Sprintf("/api/admin/agents/%d/disable", created.Agent.ID), "")
	agtExpectOK(t, r, "disable")
	var a agtAgent
	agtDecode(t, r, &a, "disabled agent")
	if a.Status != "disabled" || a.DisabledAt == nil {
		t.Errorf("disabled agent = %+v, want status disabled and disabled_at set", a)
	}
	agtExpectError(t, agtMe(h, fresh), http.StatusUnauthorized, "agent_disabled", "active key of a disabled agent")
	agtExpectError(t, agtMe(h, old), http.StatusUnauthorized, "agent_disabled", "grace key of a disabled agent")
	agtExpectError(t, agtDo(h, http.MethodGet, "/api/agent/v1/posts", "", fresh, ""), http.StatusUnauthorized, "agent_disabled", "GET /posts as a disabled agent")

	r = agtAdmin(h, token, http.MethodPost, fmt.Sprintf("/api/admin/agents/%d/enable", created.Agent.ID), "")
	agtExpectOK(t, r, "enable")
	agtDecode(t, r, &a, "enabled agent")
	if a.Status != "active" || a.DisabledAt != nil {
		t.Errorf("enabled agent = %+v, want status active and disabled_at null", a)
	}
	agtExpectOK(t, agtMe(h, fresh), "active key after enabling")
	agtExpectOK(t, agtMe(h, old), "grace key after enabling, still inside its grace period")

	for _, action := range []string{"agent.disable", "agent.enable"} {
		page, _ := agtEvents(t, h, token, url.Values{"agent_id": {fmt.Sprint(created.Agent.ID)}, "action": {action}})
		if page.Total != 1 {
			t.Errorf("%s events total=%d, want 1", action, page.Total)
		}
	}
}

func TestAgentOwnerDowngradeAndDeletionLimitAccess(t *testing.T) {
	h := newAccessHarness(t)
	owner := h.user("admin", "agt-owner")
	watcher := h.token(h.user("admin", "agt-owner-watcher"))
	created := agtCreate(t, h, h.token(owner), "擁有者測試", "posts.read", "settings.read")
	key := created.APIKey

	agtExpectOK(t, agtDo(h, http.MethodGet, "/api/agent/v1/settings", "", key, ""), "settings.read while the owner is admin")
	agtExpectOK(t, agtDo(h, http.MethodGet, "/api/agent/v1/posts", "", key, ""), "posts.read while the owner is admin")

	if err := h.db.Model(&core.User{}).Where("id = ?", owner.ID).Update("role", "editor").Error; err != nil {
		t.Fatalf("downgrade owner: %v", err)
	}
	env := agtExpectError(t, agtDo(h, http.MethodGet, "/api/agent/v1/settings", "", key, ""), http.StatusForbidden, "owner_role_insufficient", "settings.read after the owner became an editor")
	roles, _ := env.Details["required_roles"].([]any)
	if len(roles) != 1 || roles[0] != "admin" || env.Details["owner_role"] != "editor" || env.Details["permission"] != "settings.read" {
		t.Errorf("owner_role_insufficient details = %v, want permission settings.read, required_roles [admin], owner_role editor", env.Details)
	}
	agtExpectOK(t, agtDo(h, http.MethodGet, "/api/agent/v1/posts", "", key, ""), "posts.read after the owner became an editor")
	meRes := agtMe(h, key)
	agtExpectOK(t, meRes, "/me after downgrade")
	var me struct {
		Permissions          []string `json:"permissions"`
		EffectivePermissions []string `json:"effective_permissions"`
		OwnerRole            string   `json:"owner_role"`
	}
	agtDecode(t, meRes, &me, "/me after downgrade")
	if !agtSameStrings(me.Permissions, []string{"posts.read", "settings.read"}) || !agtSameStrings(me.EffectivePermissions, []string{"posts.read"}) || me.OwnerRole != "editor" {
		t.Errorf("/me after downgrade = %+v, want permissions kept, effective [posts.read], owner_role editor", me)
	}

	if err := h.db.Model(&core.User{}).Where("id = ?", owner.ID).Update("role", "customer").Error; err != nil {
		t.Fatalf("downgrade owner to customer: %v", err)
	}
	agtExpectError(t, agtDo(h, http.MethodGet, "/api/agent/v1/posts", "", key, ""), http.StatusForbidden, "owner_role_insufficient", "posts.read after the owner lost every staff role")
	if d := agtDetail(t, h, watcher, created.Agent.ID); len(d.EffectivePermissions) != 0 || d.Owner == nil || d.Owner.Role != "customer" {
		t.Errorf("detail after downgrade = %+v, want no effective permissions and owner role customer", d)
	}

	if err := h.db.Exec("DELETE FROM users WHERE id = ?", owner.ID).Error; err != nil {
		t.Fatalf("delete the owner (agents, keys and events must not block it): %v", err)
	}
	agtExpectError(t, agtMe(h, key), http.StatusUnauthorized, "owner_unavailable", "/me after the owner was deleted")
	agtExpectError(t, agtDo(h, http.MethodGet, "/api/agent/v1/posts", "", key, ""), http.StatusUnauthorized, "owner_unavailable", "GET /posts after the owner was deleted")
	if d := agtDetail(t, h, watcher, created.Agent.ID); d.Owner != nil {
		t.Errorf("detail owner after deletion = %+v, want null", d.Owner)
	}
}

func TestAgentDeleteKeepsEvents(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "agt-delete-admin"))
	created := agtCreate(t, h, token, "要刪除的 agent", "posts.read")
	key, id := created.APIKey, created.Agent.ID
	agtExpectOK(t, agtMe(h, key), "/me before deletion")
	agtExpectOK(t, agtDo(h, http.MethodGet, "/api/agent/v1/posts", "", key, ""), "GET /posts before deletion")

	r := agtAdmin(h, token, http.MethodDelete, fmt.Sprintf("/api/admin/agents/%d", id), "")
	if r.code != http.StatusNoContent {
		t.Fatalf("DELETE /api/admin/agents/%d returned %d, want 204; body=%q", id, r.code, r.body)
	}
	agtExpectError(t, agtMe(h, key), http.StatusUnauthorized, "key_revoked", "key of a deleted agent")
	agtExpectError(t, agtAdmin(h, token, http.MethodGet, fmt.Sprintf("/api/admin/agents/%d", id), ""), http.StatusNotFound, "agent_not_found", "detail of a deleted agent")
	list := agtAdmin(h, token, http.MethodGet, "/api/admin/agents", "")
	agtExpectOK(t, list, "list after deletion")
	var listed struct {
		Items []agtAgent `json:"items"`
		Total int        `json:"total"`
	}
	agtDecode(t, list, &listed, "list after deletion")
	if listed.Total != 0 || len(listed.Items) != 0 {
		t.Errorf("list after deletion = %+v, want empty", listed)
	}

	page, _ := agtEvents(t, h, token, url.Values{"agent_id": {fmt.Sprint(id)}})
	actions := map[string]bool{}
	for _, ev := range page.Items {
		actions[ev.Action] = true
		if ev.AgentName != "要刪除的 agent" {
			t.Errorf("event %d agent_name=%q, want the name snapshot", ev.ID, ev.AgentName)
		}
	}
	for _, want := range []string{"agent.create", "request", "agent.delete"} {
		if !actions[want] {
			t.Errorf("events of the deleted agent lack %s; got %v", want, actions)
		}
	}
	var deletedAt *time.Time
	if err := h.db.Raw("SELECT deleted_at FROM agents WHERE id = ?", id).Scan(&deletedAt).Error; err != nil {
		t.Fatalf("read the deleted agent row: %v", err)
	}
	if deletedAt == nil {
		t.Error("agents row lost or deleted_at not set; deletion must be a soft delete")
	}
}

func TestAgentAdminEventsFollowAgentAcrossRotation(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "agt-events-admin"))
	a := agtCreate(t, h, token, "跨輪替紀錄", "posts.read")
	b := agtCreate(t, h, token, "旁觀者", "posts.read")
	k1 := a.APIKey
	for i := 0; i < 2; i++ {
		agtExpectOK(t, agtDo(h, http.MethodGet, "/api/agent/v1/posts", "", k1, "198.51.100.77:4321"), "first key GET /posts")
	}
	agtExpectOK(t, agtDo(h, http.MethodGet, "/api/agent/v1/posts", "", b.APIKey, "192.0.2.10:1000"), "other agent GET /posts")

	k2 := agtRotate(t, h, token, a.Agent.ID, `{"mode":"immediate"}`).APIKey
	agtExpectOK(t, agtDo(h, http.MethodGet, "/api/agent/v1/posts", "", k2, "[2001:db8:abcd:12::5]:5555"), "second key GET /posts")
	agtExpectError(t, agtDo(h, http.MethodGet, "/api/agent/v1/posts", "", k1, "198.51.100.77:4321"), http.StatusUnauthorized, "key_revoked", "first key after rotation")

	page, raw := agtEvents(t, h, token, url.Values{"agent_id": {fmt.Sprint(a.Agent.ID)}, "actor_type": {"agent"}})
	if page.Total != 4 || len(page.Items) != 4 {
		t.Fatalf("agent events across rotation total=%d items=%d, want 4 (two with the first key, one with the second, one rejected)", page.Total, len(page.Items))
	}
	prefixes := map[string]int{}
	ips := map[string]bool{}
	rejected := false
	for _, ev := range page.Items {
		if ev.AgentID != a.Agent.ID || ev.ActorType != "agent" || ev.Action != "request" || ev.Module != "posts" {
			t.Errorf("event %+v, want agent %d request on module posts", ev, a.Agent.ID)
		}
		if ev.Method != http.MethodGet || ev.Route != "/api/agent/v1/posts" {
			t.Errorf("event method/route = %s %s, want GET /api/agent/v1/posts", ev.Method, ev.Route)
		}
		if !agtRequestIDPattern.MatchString(ev.CorrelationID) {
			t.Errorf("event correlation_id=%q, want a req_ id", ev.CorrelationID)
		}
		prefixes[ev.KeyPrefix]++
		ips[ev.IPMasked] = true
		if ev.KeyPrefix == k1[:12] && ev.Status == http.StatusUnauthorized {
			rejected = true
		}
	}
	if prefixes[k1[:12]] != 3 || prefixes[k2[:12]] != 1 {
		t.Errorf("key prefixes in events = %v, want 3× %s and 1× %s", prefixes, k1[:12], k2[:12])
	}
	if !ips["198.51.100.0"] || !ips["2001:db8:abcd::"] {
		t.Errorf("ip_masked values = %v, want 198.51.100.0 and 2001:db8:abcd::", ips)
	}
	if !rejected {
		t.Error("the rejected request with the revoked key was not recorded with status 401")
	}
	for _, rawIP := range []string{"198.51.100.77", "2001:db8:abcd:12::5", b.APIKey[:12]} {
		if strings.Contains(raw.body, rawIP) {
			t.Errorf("filtered event log contains %q", rawIP)
		}
	}

	all, _ := agtEvents(t, h, token, url.Values{"agent_id": {fmt.Sprint(a.Agent.ID)}})
	if all.Total != 6 {
		t.Errorf("all events of the agent total=%d, want 6 (create, rotate and four requests)", all.Total)
	}
	rot, _ := agtEvents(t, h, token, url.Values{"agent_id": {fmt.Sprint(a.Agent.ID)}, "action": {"key.rotate"}})
	if rot.Total != 1 {
		t.Errorf("key.rotate events total=%d, want 1", rot.Total)
	}
	everyone, _ := agtEvents(t, h, token, url.Values{"actor_type": {"agent"}})
	if everyone.Total != 5 {
		t.Errorf("agent events across all agents total=%d, want 5", everyone.Total)
	}
}

func TestAgentAdminPagesAdminOnly(t *testing.T) {
	h := newAccessHarness(t)
	adminToken := h.token(h.user("admin", "agt-page-admin"))
	editorToken := h.token(h.user("editor", "agt-page-editor"))
	authorToken := h.token(h.user("author", "agt-page-author"))

	for _, path := range []string{"/admin/agents", "/admin/agents/1"} {
		r := h.request(http.MethodGet, path, "", adminToken, echo.MIMETextHTML, "")
		if r.code != http.StatusOK {
			t.Errorf("admin GET %s returned %d, want 200", path, r.code)
		}
		if !strings.Contains(r.body, `href="/admin/agents"`) {
			t.Errorf("admin GET %s: rail has no AI agent entry", path)
		}
		for role, token := range map[string]string{"editor": editorToken, "author": authorToken} {
			if r := h.request(http.MethodGet, path, "", token, echo.MIMETextHTML, ""); r.code != http.StatusForbidden {
				t.Errorf("%s GET %s returned %d, want 403", role, path, r.code)
			}
		}
		if r := h.request(http.MethodGet, path, "", "", echo.MIMETextHTML, ""); r.code != http.StatusFound {
			t.Errorf("anonymous GET %s returned %d, want 302 to the login page", path, r.code)
		}
	}
	editorPage := h.request(http.MethodGet, "/admin", "", editorToken, echo.MIMETextHTML, "")
	if strings.Contains(editorPage.body, `href="/admin/agents"`) {
		t.Error("editor's rail shows the AI agent entry")
	}
}
