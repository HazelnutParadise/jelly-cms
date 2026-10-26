package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/bcrypt"
)

// Acceptance tests for personal API tokens (docs/tickets/06-accounts.md, D4:
// S14 to S18, 管理 API scopes, 互動限定). The helpers below are also used by the
// notification and two-step verification tests in this package; their names
// carry the tokTest prefix so no other test file needs to share state with them.

const tokTestPassword = "token-test-password-1"

const tokTestUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

var tokTestRemoteSeq atomic.Int64

// tokTestRemote returns a fresh client address per call so the login limiter
// and the comment limiter never throttle a fixture request.
func tokTestRemote() string {
	n := tokTestRemoteSeq.Add(1)
	return fmt.Sprintf("192.0.2.%d:%d", 1+n%250, 20000+n%20000)
}

// tokTestReq sends one request from the given client address. Non-GET requests
// carry the in-process CSRF marker, and extra headers override the defaults.
func tokTestReq(t *testing.T, h *accessHarness, remote, method, path, body, token string, headers map[string]string) accessResult {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	req.RemoteAddr = remote
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
		// This trusted in-process fixture supplies the request's source evidence.
		req.Header.Set("X-Jelly-CSRF", "1")
	}
	req.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
	if body != "" {
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	if token != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	var panicked any
	func() {
		defer func() { panicked = recover() }()
		h.echo.ServeHTTP(rec, req)
	}()
	res := accessResult{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone(), panicked: panicked}
	h.mustNoPanic(res, method+" "+path)
	return res
}

// tokTestCall is tokTestReq from a fresh client address.
func tokTestCall(t *testing.T, h *accessHarness, method, path, body, token string) accessResult {
	t.Helper()
	return tokTestReq(t, h, tokTestRemote(), method, path, body, token, nil)
}

// tokTestUser creates a local account with the shared fixture password.
func tokTestUser(t *testing.T, h *accessHarness, username, role string) core.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(tokTestPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password for %s: %v", username, err)
	}
	u := core.User{Username: username, Email: username + "@example.test", Role: role, Provider: "local", Password: string(hash)}
	if err := h.db.Create(&u).Error; err != nil {
		t.Fatalf("create %s user %s: %v", role, username, err)
	}
	return u
}

// tokTestLogin signs in through POST /auth/login with the fixture password and
// returns the JWT from the JSON response.
func tokTestLogin(t *testing.T, h *accessHarness, username string) string {
	t.Helper()
	return tokTestLoginAs(t, h, username, tokTestUserAgent)
}

// tokTestLoginAs is tokTestLogin with a chosen User-Agent, used to simulate a
// second device.
func tokTestLoginAs(t *testing.T, h *accessHarness, username, userAgent string) string {
	t.Helper()
	body, err := json.Marshal(map[string]string{"username": username, "password": tokTestPassword})
	if err != nil {
		t.Fatalf("encode login body: %v", err)
	}
	res := tokTestReq(t, h, tokTestRemote(), http.MethodPost, "/auth/login", string(body), "", map[string]string{"User-Agent": userAgent})
	if res.code != http.StatusOK {
		t.Fatalf("login %s returned %d, want 200; body=%q", username, res.code, res.body)
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(res.body), &payload); err != nil || payload.Token == "" {
		t.Fatalf("login %s response has no token: err=%v body=%q", username, err, res.body)
	}
	return payload.Token
}

// tokTestHash is the SHA-256 hex digest the server must store for a raw token.
func tokTestHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// tokTestCreate creates a token through POST /api/account/tokens as jwt's owner
// and returns its row ID and raw value. expiresInDays nil sends null (never).
func tokTestCreate(t *testing.T, h *accessHarness, jwt string, owner core.User, name string, expiresInDays any, scopes []string) (uint, string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"name": name, "note": "fixture", "expires_in_days": expiresInDays, "scopes": scopes})
	if err != nil {
		t.Fatalf("encode token body: %v", err)
	}
	res := tokTestCall(t, h, http.MethodPost, "/api/account/tokens", string(body), jwt)
	if res.code != http.StatusCreated {
		t.Fatalf("create token %q returned %d, want 201; body=%q", name, res.code, res.body)
	}
	var created struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(res.body), &created); err != nil || created.Token == "" {
		t.Fatalf("create token %q response has no token: err=%v body=%q", name, err, res.body)
	}
	var id uint
	if err := h.db.Raw("SELECT id FROM api_tokens WHERE user_id = ? AND name = ? ORDER BY id DESC LIMIT 1", owner.ID, name).Row().Scan(&id); err != nil {
		t.Fatalf("find row for token %q: %v", name, err)
	}
	return id, created.Token
}

// tokTestRowTexts returns each row of query as JSON text, for scans that must
// not find a raw secret anywhere in a table.
func tokTestRowTexts(t *testing.T, h *accessHarness, query string) []string {
	t.Helper()
	rows, err := h.db.Raw(query).Rows()
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan %q: %v", query, err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %q: %v", query, err)
	}
	return out
}

// tokTestListTokens lists the caller's tokens and decodes the items.
func tokTestListTokens(t *testing.T, h *accessHarness, jwt string) (accessResult, []map[string]any) {
	t.Helper()
	res := tokTestCall(t, h, http.MethodGet, "/api/account/tokens", "", jwt)
	var items []map[string]any
	if res.code == http.StatusOK {
		if err := json.Unmarshal([]byte(res.body), &items); err != nil {
			t.Errorf("decode GET /api/account/tokens: %v body=%q", err, res.body)
		}
	} else {
		t.Errorf("GET /api/account/tokens returned %d, want 200; body=%q", res.code, res.body)
	}
	return res, items
}

func tokTestIntPtr(v int) *int { return &v }

// --- S15: create, shown once, stored hashed ---------------------------------

func TestTokenCreateShownOnceAndStoredHashedOnly(t *testing.T) {
	h := newAccessHarness(t)
	owner := tokTestUser(t, h, "tok-create-owner", "author")
	jwt := tokTestLogin(t, h, owner.Username)

	body := `{"name":"Deploy bot","note":"ci","expires_in_days":30,"scopes":["author"]}`
	created := tokTestCall(t, h, http.MethodPost, "/api/account/tokens", body, jwt)
	if created.code != http.StatusCreated {
		t.Fatalf("POST /api/account/tokens returned %d, want 201; body=%q", created.code, created.body)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(created.body), &out); err != nil {
		t.Fatalf("decode create response: %v body=%q", err, created.body)
	}
	raw := out.Token
	if !strings.HasPrefix(raw, "jly_") {
		t.Fatalf("create response token = %q, want a jly_ token", raw)
	}

	list := tokTestCall(t, h, http.MethodGet, "/api/account/tokens", "", jwt)
	if list.code != http.StatusOK {
		t.Fatalf("GET /api/account/tokens returned %d, want 200; body=%q", list.code, list.body)
	}
	if strings.Contains(list.body, raw) {
		t.Errorf("token list shows the raw token again: %q", list.body)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(list.body), &items); err != nil {
		t.Fatalf("decode token list: %v body=%q", err, list.body)
	}
	if len(items) != 1 {
		t.Fatalf("token list has %d items, want 1; body=%q", len(items), list.body)
	}
	for _, key := range []string{"token", "token_hash"} {
		if _, ok := items[0][key]; ok {
			t.Errorf("token list item exposes %q", key)
		}
	}

	var row core.APIToken
	if err := h.db.Where("user_id = ? AND name = ?", owner.ID, "Deploy bot").First(&row).Error; err != nil {
		t.Fatalf("load stored token row: %v", err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(row.TokenHash) {
		t.Errorf("token_hash = %q, want 64 lowercase hex characters", row.TokenHash)
	}
	if row.TokenHash != tokTestHash(raw) {
		t.Errorf("token_hash = %q, want the SHA-256 of the raw token", row.TokenHash)
	}
	if len(raw) < 4 || row.Last4 != raw[len(raw)-4:] {
		t.Errorf("last4 = %q, want the final four characters of the raw token", row.Last4)
	}
	for _, text := range tokTestRowTexts(t, h, "SELECT row_to_json(t)::text FROM api_tokens t") {
		if strings.Contains(text, raw) {
			t.Errorf("api_tokens row contains the raw token: %s", text)
		}
	}
}

// --- scopes reach the admin routes ------------------------------------------

func TestTokenAuthenticatesAdminRoutesWithScopes(t *testing.T) {
	h := newAccessHarness(t)
	admin := tokTestUser(t, h, "tok-scope-admin", "admin")
	jwt := tokTestLogin(t, h, admin.Username)
	_, adminRaw := tokTestCreate(t, h, jwt, admin, "admin scope", nil, []string{"admin"})
	_, authorRaw := tokTestCreate(t, h, jwt, admin, "author scope", nil, []string{"author"})

	if r := tokTestCall(t, h, http.MethodGet, "/api/admin/users", "", adminRaw); r.code != http.StatusOK {
		t.Errorf("GET /api/admin/users with [admin] token returned %d, want 200; body=%q", r.code, r.body)
	}
	if r := tokTestCall(t, h, http.MethodGet, "/api/admin/users", "", authorRaw); r.code != http.StatusForbidden {
		t.Errorf("GET /api/admin/users with [author] token returned %d, want 403; body=%q", r.code, r.body)
	}
}

// --- scopes never exceed the owner's role -----------------------------------

func TestTokenScopeCannotExceedOwnerRole(t *testing.T) {
	h := newAccessHarness(t)
	author := tokTestUser(t, h, "tok-exceed-author", "author")
	jwt := tokTestLogin(t, h, author.Username)

	r := tokTestCall(t, h, http.MethodPost, "/api/account/tokens", `{"name":"escalate","expires_in_days":null,"scopes":["admin"]}`, jwt)
	if r.code != http.StatusBadRequest {
		t.Errorf("author creating an [admin] token returned %d, want 400; body=%q", r.code, r.body)
	}
	var n int64
	if err := h.db.Model(&core.APIToken{}).Where("user_id = ?", author.ID).Count(&n).Error; err != nil {
		t.Fatalf("count author tokens: %v", err)
	}
	if n != 0 {
		t.Errorf("rejected [admin] token request left %d token rows", n)
	}
}

// --- owner downgrade takes effect on the next request -----------------------

func TestTokenOwnerDowngradeCutsPowerImmediately(t *testing.T) {
	h := newAccessHarness(t)
	owner := tokTestUser(t, h, "tok-downgrade-owner", "admin")
	boss := tokTestUser(t, h, "tok-downgrade-boss", "admin")
	ownerJWT := tokTestLogin(t, h, owner.Username)
	bossJWT := tokTestLogin(t, h, boss.Username)
	_, raw := tokTestCreate(t, h, ownerJWT, owner, "downgrade probe", nil, []string{"admin"})

	if r := tokTestCall(t, h, http.MethodGet, "/api/admin/users", "", raw); r.code != http.StatusOK {
		t.Fatalf("admin token before downgrade returned %d, want 200; body=%q", r.code, r.body)
	}
	patch := tokTestCall(t, h, http.MethodPatch, fmt.Sprintf("/api/admin/users/%d", owner.ID), `{"role":"author"}`, bossJWT)
	if patch.code != http.StatusOK {
		t.Fatalf("PATCH owner to author returned %d, want 200; body=%q", patch.code, patch.body)
	}
	if r := tokTestCall(t, h, http.MethodGet, "/api/admin/users", "", raw); r.code != http.StatusForbidden {
		t.Errorf("token of downgraded owner on /api/admin/users returned %d, want 403; body=%q", r.code, r.body)
	}
}

// --- expired, revoked and disabled-owner tokens fail authentication ---------

func TestTokenExpiredRevokedAndDisabledOwnerGet401(t *testing.T) {
	h := newAccessHarness(t)
	owner := tokTestUser(t, h, "tok-dead-owner", "author")
	jwt := tokTestLogin(t, h, owner.Username)

	_, okRaw := tokTestCreate(t, h, jwt, owner, "still valid", tokTestIntPtr(30), []string{"author"})
	expID, expRaw := tokTestCreate(t, h, jwt, owner, "expired", tokTestIntPtr(7), []string{"author"})
	if err := h.db.Model(&core.APIToken{}).Where("id = ?", expID).Update("expires_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatalf("age token into the past: %v", err)
	}
	revID, revRaw := tokTestCreate(t, h, jwt, owner, "revoked", tokTestIntPtr(30), []string{"author"})
	if r := tokTestCall(t, h, http.MethodPost, fmt.Sprintf("/api/account/tokens/%d/revoke", revID), "", jwt); r.code < 200 || r.code >= 300 {
		t.Fatalf("revoke returned %d, want 2xx; body=%q", r.code, r.body)
	}

	disabled := tokTestUser(t, h, "tok-dead-disabled", "author")
	disabledJWT := tokTestLogin(t, h, disabled.Username)
	_, disRaw := tokTestCreate(t, h, disabledJWT, disabled, "owner disabled", nil, []string{"author"})
	if err := h.db.Model(&core.User{}).Where("id = ?", disabled.ID).Update("status", "disabled").Error; err != nil {
		t.Fatalf("disable token owner: %v", err)
	}

	if r := tokTestCall(t, h, http.MethodGet, "/api/account/profile", "", okRaw); r.code != http.StatusOK {
		t.Errorf("control: active token on GET /api/account/profile returned %d, want 200; body=%q", r.code, r.body)
	}
	for label, raw := range map[string]string{"expired": expRaw, "revoked": revRaw, "disabled owner": disRaw, "unknown": "jly_not-a-real-token-value-0000000000000000000"} {
		if r := tokTestCall(t, h, http.MethodGet, "/api/account/profile", "", raw); r.code != http.StatusUnauthorized {
			t.Errorf("%s token on GET /api/account/profile returned %d, want 401; body=%q", label, r.code, r.body)
		}
	}
}

// --- restore only clears revoked_at -----------------------------------------

func TestTokenRestoreReactivatesUntilExpiry(t *testing.T) {
	h := newAccessHarness(t)
	owner := tokTestUser(t, h, "tok-restore-owner", "author")
	jwt := tokTestLogin(t, h, owner.Username)

	id, raw := tokTestCreate(t, h, jwt, owner, "restorable", tokTestIntPtr(30), []string{"author"})
	if r := tokTestCall(t, h, http.MethodPost, fmt.Sprintf("/api/account/tokens/%d/revoke", id), "", jwt); r.code < 200 || r.code >= 300 {
		t.Fatalf("revoke returned %d, want 2xx; body=%q", r.code, r.body)
	}
	if r := tokTestCall(t, h, http.MethodGet, "/api/account/profile", "", raw); r.code != http.StatusUnauthorized {
		t.Errorf("revoked token returned %d, want 401", r.code)
	}
	if r := tokTestCall(t, h, http.MethodPost, fmt.Sprintf("/api/account/tokens/%d/restore", id), "", jwt); r.code != http.StatusOK {
		t.Errorf("restore returned %d, want 200; body=%q", r.code, r.body)
	}
	if r := tokTestCall(t, h, http.MethodGet, "/api/account/profile", "", raw); r.code != http.StatusOK {
		t.Errorf("restored token returned %d, want 200; body=%q", r.code, r.body)
	}

	expiredID, expiredRaw := tokTestCreate(t, h, jwt, owner, "restorable but expired", tokTestIntPtr(7), []string{"author"})
	if r := tokTestCall(t, h, http.MethodPost, fmt.Sprintf("/api/account/tokens/%d/revoke", expiredID), "", jwt); r.code < 200 || r.code >= 300 {
		t.Fatalf("revoke of second token returned %d, want 2xx; body=%q", r.code, r.body)
	}
	if err := h.db.Model(&core.APIToken{}).Where("id = ?", expiredID).Update("expires_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatalf("age token into the past: %v", err)
	}
	if r := tokTestCall(t, h, http.MethodPost, fmt.Sprintf("/api/account/tokens/%d/restore", expiredID), "", jwt); r.code != http.StatusOK {
		t.Errorf("restore of expired token returned %d, want 200; body=%q", r.code, r.body)
	}
	if r := tokTestCall(t, h, http.MethodGet, "/api/account/profile", "", expiredRaw); r.code != http.StatusUnauthorized {
		t.Errorf("restored but expired token returned %d, want 401", r.code)
	}
	_, items := tokTestListTokens(t, h, jwt)
	found := false
	for _, item := range items {
		if fmt.Sprint(item["id"]) == fmt.Sprint(expiredID) {
			found = true
			if item["status"] != "expired" {
				t.Errorf("restored expired token status = %v, want expired", item["status"])
			}
		}
	}
	if !found {
		t.Errorf("token list omitted restored expired token %d", expiredID)
	}
}

// --- interactive-only routes refuse API tokens -------------------------------

func TestTokenCannotManageTokensPasswordTwoFactorOrSessions(t *testing.T) {
	h := newAccessHarness(t)
	owner := tokTestUser(t, h, "tok-manage-admin", "admin")
	jwt := tokTestLogin(t, h, owner.Username)
	_, raw := tokTestCreate(t, h, jwt, owner, "all powers", nil, []string{"admin"})

	cases := []struct{ method, path, body string }{
		{http.MethodPost, "/api/account/tokens", `{"name":"escalate","expires_in_days":null,"scopes":["admin"]}`},
		{http.MethodPost, "/api/account/password", fmt.Sprintf(`{"current_password":%q,"new_password":"token-changed-password-2"}`, tokTestPassword)},
		{http.MethodPost, "/api/account/2fa/enroll", ""},
		{http.MethodDelete, "/api/account/sessions/x", ""},
	}
	for _, c := range cases {
		if r := tokTestCall(t, h, c.method, c.path, c.body, raw); r.code != http.StatusForbidden {
			t.Errorf("token on %s %s returned %d, want 403; body=%q", c.method, c.path, r.code, r.body)
		}
	}
}

// --- list never leaks secrets and only lists the caller's tokens ------------

func TestTokenListNeverReturnsSecretAndOnlyOwn(t *testing.T) {
	h := newAccessHarness(t)
	a := tokTestUser(t, h, "tok-list-a", "author")
	b := tokTestUser(t, h, "tok-list-b", "author")
	jwtA := tokTestLogin(t, h, a.Username)
	jwtB := tokTestLogin(t, h, b.Username)

	idA1, rawA1 := tokTestCreate(t, h, jwtA, a, "a-one", nil, []string{"author"})
	idA2, rawA2 := tokTestCreate(t, h, jwtA, a, "a-two", tokTestIntPtr(90), []string{"author"})
	idB1, rawB1 := tokTestCreate(t, h, jwtB, b, "b-one", nil, []string{"author"})

	list, items := tokTestListTokens(t, h, jwtA)
	for _, raw := range []string{rawA1, rawA2, rawB1} {
		if strings.Contains(list.body, raw) {
			t.Errorf("token list contains a raw token")
		}
		if strings.Contains(list.body, tokTestHash(raw)) {
			t.Errorf("token list contains a token hash")
		}
	}
	got := map[string]bool{}
	for _, item := range items {
		got[fmt.Sprint(item["id"])] = true
		for _, key := range []string{"token", "token_hash"} {
			if _, ok := item[key]; ok {
				t.Errorf("token list item exposes %q", key)
			}
		}
		if status := fmt.Sprint(item["status"]); status != "active" && status != "expired" && status != "revoked" {
			t.Errorf("token list item status = %q, want active, expired or revoked", status)
		}
	}
	if len(items) != 2 || !got[fmt.Sprint(idA1)] || !got[fmt.Sprint(idA2)] || got[fmt.Sprint(idB1)] {
		t.Errorf("token list for owner A = %v, want exactly rows %d and %d", got, idA1, idA2)
	}
}

// --- read-only scopes allow GET and deny writes -----------------------------

func TestTokenReadOnlyScopeAllowsGETDeniesWrites(t *testing.T) {
	h := newAccessHarness(t)
	owner := tokTestUser(t, h, "tok-readonly-admin", "admin")
	jwt := tokTestLogin(t, h, owner.Username)
	_, contentRaw := tokTestCreate(t, h, jwt, owner, "content reader", nil, []string{"content_read"})
	_, ordersRaw := tokTestCreate(t, h, jwt, owner, "orders reader", nil, []string{"orders_read"})

	if r := tokTestCall(t, h, http.MethodGet, "/api/admin/posts?type=post", "", contentRaw); r.code != http.StatusOK {
		t.Errorf("content_read GET /api/admin/posts returned %d, want 200; body=%q", r.code, r.body)
	}
	postBody := `{"title":"read-only probe","content":"x","type":"post","status":"draft"}`
	if r := tokTestCall(t, h, http.MethodPost, "/api/admin/posts", postBody, contentRaw); r.code != http.StatusForbidden {
		t.Errorf("content_read POST /api/admin/posts returned %d, want 403; body=%q", r.code, r.body)
	}
	var probes int64
	if err := h.db.Model(&core.Post{}).Where("title = ?", "read-only probe").Count(&probes).Error; err != nil {
		t.Fatalf("count probe posts: %v", err)
	}
	if probes != 0 {
		t.Errorf("content_read POST created %d posts", probes)
	}
	if r := tokTestCall(t, h, http.MethodGet, "/api/admin/orders", "", ordersRaw); r.code != http.StatusOK {
		t.Errorf("orders_read GET /api/admin/orders returned %d, want 200; body=%q", r.code, r.body)
	}
	// There is no POST /api/admin/orders route today, so a 405 is as good as a 403: the point is that a
	// read-only scope never writes. If a write route appears, it must answer 403 for orders_read.
	if r := tokTestCall(t, h, http.MethodPost, "/api/admin/orders", `{"status":"paid"}`, ordersRaw); r.code != http.StatusForbidden && r.code != http.StatusMethodNotAllowed {
		t.Errorf("orders_read POST /api/admin/orders returned %d, want 403 or 405; body=%q", r.code, r.body)
	}
}

// --- delete requires revocation first ---------------------------------------

func TestTokenDeleteOnlyWhenRevoked(t *testing.T) {
	h := newAccessHarness(t)
	owner := tokTestUser(t, h, "tok-delete-owner", "author")
	jwt := tokTestLogin(t, h, owner.Username)
	id, _ := tokTestCreate(t, h, jwt, owner, "delete me", nil, []string{"author"})
	path := fmt.Sprintf("/api/account/tokens/%d", id)

	if r := tokTestCall(t, h, http.MethodDelete, path, "", jwt); r.code != http.StatusConflict {
		t.Errorf("DELETE of an active token returned %d, want 409; body=%q", r.code, r.body)
	}
	if r := tokTestCall(t, h, http.MethodPost, fmt.Sprintf("/api/account/tokens/%d/revoke", id), "", jwt); r.code < 200 || r.code >= 300 {
		t.Fatalf("revoke returned %d, want 2xx; body=%q", r.code, r.body)
	}
	if r := tokTestCall(t, h, http.MethodDelete, path, "", jwt); r.code != http.StatusOK {
		t.Errorf("DELETE of a revoked token returned %d, want 200; body=%q", r.code, r.body)
	}
	var n int64
	if err := h.db.Model(&core.APIToken{}).Where("id = ?", id).Count(&n).Error; err != nil {
		t.Fatalf("count deleted token: %v", err)
	}
	if n != 0 {
		t.Errorf("deleted token row still exists")
	}
}

// --- last-used bookkeeping ---------------------------------------------------

func TestTokenLastUsedUpdatedAndIPMasked(t *testing.T) {
	h := newAccessHarness(t)
	owner := tokTestUser(t, h, "tok-lastused-owner", "author")
	jwt := tokTestLogin(t, h, owner.Username)
	id, raw := tokTestCreate(t, h, jwt, owner, "usage probe", nil, []string{"author"})

	var before core.APIToken
	if err := h.db.First(&before, id).Error; err != nil {
		t.Fatalf("load token before use: %v", err)
	}
	if before.LastUsedAt != nil {
		t.Errorf("last_used_at before first use = %v, want NULL", before.LastUsedAt)
	}

	r := tokTestReq(t, h, "198.51.100.77:4321", http.MethodGet, "/api/account/profile", "", raw, nil)
	if r.code != http.StatusOK {
		t.Fatalf("GET /api/account/profile with token returned %d, want 200; body=%q", r.code, r.body)
	}

	var after core.APIToken
	if err := h.db.First(&after, id).Error; err != nil {
		t.Fatalf("load token after use: %v", err)
	}
	if after.LastUsedAt == nil {
		t.Fatalf("last_used_at not set after an authenticated request")
	}
	if !strings.HasPrefix(after.LastUsedIPMasked, "198.51.100") {
		t.Errorf("last_used_ip_masked = %q, want the /24 prefix 198.51.100", after.LastUsedIPMasked)
	}
	if strings.Contains(after.LastUsedIPMasked, "77") {
		t.Errorf("last_used_ip_masked = %q still contains the final octet", after.LastUsedIPMasked)
	}
}
