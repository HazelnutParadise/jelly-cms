package server

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

// Acceptance tests for ticket 22 (docs/tickets/22-api-keys-consolidation.md):
// personal API tokens (jly_, api_tokens) are merged into access keys (agent
// keys on /api/agent/v1). The migration is 0026_tokens_into_keys.sql, which
// the tests run from the repository because the server package does not embed
// the migrations.

const apiKeysMigrationRelativePath = "internal/data/migrations/0026_tokens_into_keys.sql"

// apiKeysSeedToken inserts an api_tokens row for owner, as it exists before
// 0026, and returns the raw jly_ token the owner was shown once.
func apiKeysSeedToken(t *testing.T, h *accessHarness, owner core.User, name string, scopes []string, expiresAt, revokedAt *time.Time) string {
	t.Helper()
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		t.Fatalf("generate token entropy: %v", err)
	}
	raw := "jly_" + base64.RawURLEncoding.EncodeToString(entropy[:])
	if err := h.db.Exec(`INSERT INTO api_tokens (user_id, name, note, token_hash, last4, scopes, expires_at, revoked_at)
		VALUES (?, ?, '', ?, ?, ?::text[], ?, ?)`,
		owner.ID, name, tokTestHash(raw), raw[len(raw)-4:], "{"+strings.Join(scopes, ",")+"}", expiresAt, revokedAt).Error; err != nil {
		t.Fatalf("seed api_tokens row %q: %v", name, err)
	}
	return raw
}

// apiKeysRunMigration applies 0026_tokens_into_keys.sql to the test database.
func apiKeysRunMigration(t *testing.T, h *accessHarness) {
	t.Helper()
	sqlText, err := os.ReadFile(filepath.Join(repoRoot(t), apiKeysMigrationRelativePath))
	if err != nil {
		t.Fatalf("migration 0026_tokens_into_keys.sql is missing (ticket 22 requires it): %v", err)
	}
	if err := h.db.Exec(string(sqlText)).Error; err != nil {
		t.Fatalf("apply 0026_tokens_into_keys.sql: %v", err)
	}
}

func TestAPIKeysMigratedTokenAuthenticatesAgentAPIWithOwnerRole(t *testing.T) {
	h := newAccessHarness(t)
	owner := tokTestUser(t, h, "apikeys-author-owner", "author")
	raw := apiKeysSeedToken(t, h, owner, "author bot", []string{"admin"}, nil, nil)
	apiKeysRunMigration(t, h)

	r := agtMe(h, raw)
	agtExpectOK(t, r, "GET /api/agent/v1/me with the migrated jly_ token")
	var me struct {
		Permissions          []string `json:"permissions"`
		EffectivePermissions []string `json:"effective_permissions"`
		OwnerRole            string   `json:"owner_role"`
	}
	agtDecode(t, r, &me, "me with migrated token")
	if len(me.Permissions) != 16 {
		t.Errorf("permissions=%v, want all 16 permissions because the admin scope maps to them", me.Permissions)
	}
	if !agtContains(me.EffectivePermissions, "posts.write") {
		t.Errorf("effective_permissions=%v, want posts.write for an author owner", me.EffectivePermissions)
	}
	if agtContains(me.EffectivePermissions, "settings.write") || len(me.EffectivePermissions) >= len(me.Permissions) {
		t.Errorf("effective_permissions=%v, want the owner role to cut admin-only permissions such as settings.write", me.EffectivePermissions)
	}
	if me.OwnerRole != "author" {
		t.Errorf("owner_role=%q, want author", me.OwnerRole)
	}
}

func TestAPIKeysMigratedRevokedAndExpiredTokensGetKeyErrors(t *testing.T) {
	h := newAccessHarness(t)
	owner := tokTestUser(t, h, "apikeys-state-owner", "admin")
	past := time.Now().Add(-time.Hour)
	revokedRaw := apiKeysSeedToken(t, h, owner, "revoked bot", []string{"content_read"}, nil, &past)
	expiredRaw := apiKeysSeedToken(t, h, owner, "expired bot", []string{"content_read"}, &past, nil)
	apiKeysRunMigration(t, h)

	agtExpectError(t, agtMe(h, revokedRaw), http.StatusUnauthorized, "key_revoked", "GET /api/agent/v1/me with a migrated revoked token")
	agtExpectError(t, agtMe(h, expiredRaw), http.StatusUnauthorized, "key_expired", "GET /api/agent/v1/me with a migrated expired token")
}

func TestAPIKeysMigratedTokenIsNoLongerABackendCredential(t *testing.T) {
	h := newAccessHarness(t)
	owner := tokTestUser(t, h, "apikeys-backend-owner", "admin")
	raw := apiKeysSeedToken(t, h, owner, "backend bot", []string{"admin"}, nil, nil)
	// A jly_ token is never a backend credential, so the probe must fail both
	// before and after 0026 has moved the token into access keys.
	probe := func(stage string) {
		t.Helper()
		for _, path := range []string{"/api/admin/posts", "/api/admin/orders"} {
			res := tokTestCall(t, h, http.MethodGet, path, "", raw)
			if res.code != http.StatusUnauthorized {
				t.Errorf("GET %s with a jly_ token %s returned %d, want 401; body=%q", path, stage, res.code, res.body)
			}
		}
	}
	probe("before 0026")
	apiKeysRunMigration(t, h)
	probe("after 0026")
}

func TestAPIKeysNewAgentKeyUsesJLYPrefixAndLegacyJLAKeysStillWork(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "apikeys-new-key-admin")
	token := h.token(admin)

	created := agtCreate(t, h, token, "新金鑰前綴", "posts.read")
	key := created.APIKey
	if !strings.HasPrefix(key, "jly_") || len(key) != 47 {
		t.Errorf("new api_key=%q, want a 47-character key starting with jly_", key)
	}
	if want := key[:12]; created.Key.Prefix != want || !strings.HasPrefix(created.Key.Prefix, "jly_") {
		t.Errorf("key.prefix=%q, want jly_ followed by 8 characters: %q", created.Key.Prefix, want)
	}
	agtExpectOK(t, agtMe(h, key), "GET /api/agent/v1/me with the new jly_ key")

	// A key issued before this ticket keeps working: jla_ with its own agent.
	var legacyAgentID uint
	if err := h.db.Raw(`INSERT INTO agents (name, owner_id, created_by, permissions, status)
		VALUES ('舊 jla_ 金鑰', ?, ?, '{posts.read}'::text[], 'active') RETURNING id`, admin.ID, admin.ID).Row().Scan(&legacyAgentID); err != nil {
		t.Fatalf("seed legacy agent: %v", err)
	}
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		t.Fatalf("generate legacy key entropy: %v", err)
	}
	legacyKey := "jla_" + base64.RawURLEncoding.EncodeToString(entropy[:])
	if err := h.db.Exec(`INSERT INTO agent_keys (agent_id, key_hash, prefix, created_by) VALUES (?, ?, ?, ?)`,
		legacyAgentID, tokTestHash(legacyKey), legacyKey[:12], admin.ID).Error; err != nil {
		t.Fatalf("seed legacy jla_ key: %v", err)
	}
	agtExpectOK(t, agtMe(h, legacyKey), "GET /api/agent/v1/me with a legacy jla_ key")
}

func TestAPIKeysMigratedTokenWritesAgentEvent(t *testing.T) {
	h := newAccessHarness(t)
	owner := tokTestUser(t, h, "apikeys-event-owner", "admin")
	raw := apiKeysSeedToken(t, h, owner, "event bot", []string{"content_read"}, nil, nil)
	apiKeysRunMigration(t, h)

	agtExpectOK(t, agtMe(h, raw), "GET /api/agent/v1/me with the migrated jly_ token")
	var n int64
	if err := h.db.Raw(`SELECT count(*) FROM agent_events WHERE key_prefix = ? AND route LIKE '%/api/agent/v1/me'`, "jly_"+raw[len(raw)-4:]).Row().Scan(&n); err != nil {
		t.Fatalf("count agent_events for the migrated key: %v", err)
	}
	if n < 1 {
		t.Errorf("agent_events has %d rows for the migrated key on /api/agent/v1/me, want at least one", n)
	}
}

func TestAPIKeysAccountTokenEndpointsAreRemoved(t *testing.T) {
	h := newAccessHarness(t)
	u := tokTestUser(t, h, "apikeys-removed-author", "author")
	jwt := tokTestLogin(t, h, u.Username)

	for _, probe := range []struct{ method, body string }{
		{http.MethodGet, ""},
		{http.MethodPost, `{"name":"removed","scopes":["author"]}`},
	} {
		res := tokTestCall(t, h, probe.method, "/api/account/tokens", probe.body, jwt)
		if res.code != http.StatusNotFound {
			t.Errorf("%s /api/account/tokens returned %d, want 404 after the personal token feature is removed; body=%q", probe.method, res.code, res.body)
		}
	}
}

func TestAPIKeysProfilePageHasNoTokenCreationForm(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "apikeys-profile-admin")
	res := h.request(http.MethodGet, "/admin/account?tab=tokens", "", h.token(admin), "text/html", "")
	h.mustNoPanic(res, "GET /admin/account?tab=tokens")
	if res.code != http.StatusOK {
		t.Fatalf("GET /admin/account?tab=tokens returned %d, want 200; body=%q", res.code, res.body)
	}
	for _, marker := range []string{`id="tokenForm"`, `id="tokenDialog"`, `id="newToken"`, "/api/account/tokens"} {
		if strings.Contains(res.body, marker) {
			t.Errorf("profile page still renders the personal token UI (%s); want an explanation that points to access keys instead", marker)
		}
	}
}

// /me must report the role cap and the role the agent actually runs with, so a
// migrated author-scoped token is visibly capped while a read-only one is not.
func TestAPIKeysMeReportsRoleCapAndEffectiveRole(t *testing.T) {
	h := newAccessHarness(t)
	owner := tokTestUser(t, h, "apikeys-rolecap-owner", "admin")
	capped := apiKeysSeedToken(t, h, owner, "capped bot", []string{"author"}, nil, nil)
	readOnly := apiKeysSeedToken(t, h, owner, "read bot", []string{"content_read"}, nil, nil)
	apiKeysRunMigration(t, h)

	check := func(key, where, wantRoleCap, wantEffective string) {
		t.Helper()
		r := agtMe(h, key)
		agtExpectOK(t, r, where)
		var me map[string]any
		agtDecode(t, r, &me, where)
		roleCap, ok := me["role_cap"]
		if !ok {
			t.Errorf("%s: /me has no role_cap key; body=%q", where, r.body)
		} else if wantRoleCap == "" {
			if roleCap != nil {
				t.Errorf("%s: role_cap=%v, want null", where, roleCap)
			}
		} else if roleCap != wantRoleCap {
			t.Errorf("%s: role_cap=%v, want %q", where, roleCap, wantRoleCap)
		}
		if got := me["effective_role"]; got != wantEffective {
			t.Errorf("%s: effective_role=%v, want %q", where, got, wantEffective)
		}
	}
	check(capped, "GET /me with the migrated author-scoped token", "author", "author")
	check(readOnly, "GET /me with the migrated content_read token", "", "admin")

	newAdmin := h.user("admin", "apikeys-rolecap-new-admin")
	created := agtCreate(t, h, h.token(newAdmin), "無上限金鑰", "posts.read")
	check(created.APIKey, "GET /me with a new uncapped agent key", "", "admin")

}
