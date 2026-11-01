package server

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

// Acceptance tests for ticket 22 (docs/tickets/22-api-keys-consolidation.md)
// and ticket 25 (docs/tickets/25-integration-internal-rename.md). Ticket 22
// merged personal API tokens into access keys; ticket 25 renamed the agent
// tables to integration. These tests seed integrations and integration_keys
// directly through SQL and exercise the same runtime behaviour.

var (
	apiKeysAllPermissions = []string{
		"posts.read", "posts.write", "pages.read", "pages.write", "media.read", "media.write",
		"products.read", "products.write", "orders.read", "themes.read", "themes.write",
		"plugins.read", "plugins.write", "settings.read", "settings.write", "logs.read",
	}
	apiKeysAuthorPermissions = []string{"posts.read", "posts.write", "pages.read", "media.read", "media.write"}
	apiKeysEditorPermissions = []string{"posts.read", "posts.write", "pages.read", "pages.write", "media.read", "media.write"}
	apiKeysReaderPermissions = []string{"posts.read", "pages.read"}
)

// apiKeysSeedIntegration inserts one integration and one active jly_ access key
// with the given permissions and role cap, returning the raw key.
func apiKeysSeedIntegration(t *testing.T, h *accessHarness, owner core.User, name string, permissions []string, roleCap string, expiresAt, revokedAt *time.Time) string {
	t.Helper()
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		t.Fatalf("generate key entropy: %v", err)
	}
	raw := "jly_" + base64.RawURLEncoding.EncodeToString(entropy[:])
	var integrationID uint
	if err := h.db.Raw(`INSERT INTO integrations (name, description, permissions, owner_id, created_by, status, role_cap)
		VALUES (?, '', ?::text[], ?, ?, 'active', NULLIF(?::text, '')) RETURNING id`,
		name, "{"+strings.Join(permissions, ",")+"}", owner.ID, owner.ID, roleCap).Row().Scan(&integrationID); err != nil {
		t.Fatalf("seed integration %q: %v", name, err)
	}
	if err := h.db.Exec(`INSERT INTO integration_keys (integration_id, key_hash, prefix, created_by, expires_at, revoked_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		integrationID, tokTestHash(raw), raw[:12], owner.ID, expiresAt, revokedAt).Error; err != nil {
		t.Fatalf("seed integration key for %q: %v", name, err)
	}
	return raw
}

// apiKeysSeedToken keeps the scope vocabulary of the removed personal tokens
// for the older regression that still describes a token's scopes. It maps them
// the way migration 0026 did and seeds the resulting integration directly.
func apiKeysSeedToken(t *testing.T, h *accessHarness, owner core.User, name string, scopes []string, expiresAt, revokedAt *time.Time) string {
	t.Helper()
	permissions, roleCap := apiKeysScopesToPermissions(scopes)
	return apiKeysSeedIntegration(t, h, owner, name, permissions, roleCap, expiresAt, revokedAt)
}

func apiKeysScopesToPermissions(scopes []string) ([]string, string) {
	seen := map[string]bool{}
	var permissions []string
	add := func(list ...string) {
		for _, p := range list {
			if !seen[p] {
				seen[p] = true
				permissions = append(permissions, p)
			}
		}
	}
	roleCap := ""
	for _, scope := range scopes {
		switch scope {
		case "content_read":
			add(apiKeysReaderPermissions...)
		case "orders_read":
			add("orders.read")
		case "author":
			add(apiKeysAuthorPermissions...)
			if roleCap == "" {
				roleCap = "author"
			}
		case "editor":
			add(apiKeysEditorPermissions...)
			if roleCap == "" || roleCap == "author" {
				roleCap = "editor"
			}
		case "admin":
			add(apiKeysAllPermissions...)
			roleCap = "admin"
		}
	}
	return permissions, roleCap
}

// apiKeysRunMigration is kept for the older agent regression that used to
// migrate a personal token. Integrations are seeded directly now, so there is
// no migration step left here.
func apiKeysRunMigration(t *testing.T, h *accessHarness) {
	t.Helper()
}

func TestAPIKeysMigratedTokenAuthenticatesAgentAPIWithOwnerRole(t *testing.T) {
	h := newAccessHarness(t)
	owner := tokTestUser(t, h, "apikeys-author-owner", "author")
	raw := apiKeysSeedIntegration(t, h, owner, "author bot", apiKeysAllPermissions, "admin", nil, nil)

	r := agtMe(h, raw)
	agtExpectOK(t, r, "GET /api/v1/me with the seeded jly_ key")
	var me struct {
		Permissions          []string `json:"permissions"`
		EffectivePermissions []string `json:"effective_permissions"`
		OwnerRole            string   `json:"owner_role"`
	}
	agtDecode(t, r, &me, "me with the seeded key")
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
	revokedRaw := apiKeysSeedIntegration(t, h, owner, "revoked bot", apiKeysReaderPermissions, "", nil, &past)
	expiredRaw := apiKeysSeedIntegration(t, h, owner, "expired bot", apiKeysReaderPermissions, "", &past, nil)

	agtExpectError(t, agtMe(h, revokedRaw), http.StatusUnauthorized, "key_revoked", "GET /api/v1/me with a revoked key")
	agtExpectError(t, agtMe(h, expiredRaw), http.StatusUnauthorized, "key_expired", "GET /api/v1/me with an expired key")
}

func TestAPIKeysMigratedTokenIsNoLongerABackendCredential(t *testing.T) {
	h := newAccessHarness(t)
	owner := tokTestUser(t, h, "apikeys-backend-owner", "admin")
	raw := apiKeysSeedIntegration(t, h, owner, "backend bot", apiKeysAllPermissions, "admin", nil, nil)
	// A jly_ access key is never a backend credential.
	for _, path := range []string{"/api/admin/posts", "/api/admin/orders"} {
		res := tokTestCall(t, h, http.MethodGet, path, "", raw)
		if res.code != http.StatusUnauthorized {
			t.Errorf("GET %s with a jly_ key returned %d, want 401; body=%q", path, res.code, res.body)
		}
	}
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
	agtExpectOK(t, agtMe(h, key), "GET /api/v1/me with the new jly_ key")

	// A key issued before this ticket keeps working: jla_ with its own
	// integration row.
	var legacyAgentID uint
	if err := h.db.Raw(`INSERT INTO integrations (name, owner_id, created_by, permissions, status)
		VALUES ('舊 jla_ 金鑰', ?, ?, '{posts.read}'::text[], 'active') RETURNING id`, admin.ID, admin.ID).Row().Scan(&legacyAgentID); err != nil {
		t.Fatalf("seed legacy integration: %v", err)
	}
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		t.Fatalf("generate legacy key entropy: %v", err)
	}
	legacyKey := "jla_" + base64.RawURLEncoding.EncodeToString(entropy[:])
	if err := h.db.Exec(`INSERT INTO integration_keys (integration_id, key_hash, prefix, created_by) VALUES (?, ?, ?, ?)`,
		legacyAgentID, tokTestHash(legacyKey), legacyKey[:12], admin.ID).Error; err != nil {
		t.Fatalf("seed legacy jla_ key: %v", err)
	}
	agtExpectOK(t, agtMe(h, legacyKey), "GET /api/v1/me with a legacy jla_ key")
}

func TestAPIKeysMigratedTokenWritesAgentEvent(t *testing.T) {
	h := newAccessHarness(t)
	owner := tokTestUser(t, h, "apikeys-event-owner", "admin")
	raw := apiKeysSeedIntegration(t, h, owner, "event bot", apiKeysReaderPermissions, "", nil, nil)

	agtExpectOK(t, agtMe(h, raw), "GET /api/v1/me with the seeded jly_ key")
	var n int64
	if err := h.db.Raw(`SELECT count(*) FROM integration_events WHERE key_prefix = ? AND route LIKE '%/api/v1/me'`, raw[:12]).Row().Scan(&n); err != nil {
		t.Fatalf("count integration_events for the key: %v", err)
	}
	if n < 1 {
		t.Errorf("integration_events has %d rows for the key on /api/v1/me, want at least one", n)
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

// /me must report the role cap and the role the integration actually runs with,
// so an author-capped integration is visibly capped while a read-only one is not.
func TestAPIKeysMeReportsRoleCapAndEffectiveRole(t *testing.T) {
	h := newAccessHarness(t)
	owner := tokTestUser(t, h, "apikeys-rolecap-owner", "admin")
	capped := apiKeysSeedIntegration(t, h, owner, "capped bot", apiKeysAuthorPermissions, "author", nil, nil)
	readOnly := apiKeysSeedIntegration(t, h, owner, "read bot", apiKeysReaderPermissions, "", nil, nil)

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
	check(capped, "GET /me with the author-capped key", "author", "author")
	check(readOnly, "GET /me with the read-only key", "", "admin")

	newAdmin := h.user("admin", "apikeys-rolecap-new-admin")
	created := agtCreate(t, h, h.token(newAdmin), "無上限金鑰", "posts.read")
	check(created.APIKey, "GET /me with a new uncapped agent key", "", "admin")

}
