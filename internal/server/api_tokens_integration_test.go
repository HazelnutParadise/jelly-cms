package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/bcrypt"
)

// Shared fixtures for the account tests in this package (login, users and
// requests). Personal API tokens were merged into access keys by ticket 22, so
// the token acceptance tests were removed; tokTestCreate is kept only because
// accounts_security_regression_test.go still calls it.

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
		setAccessToken(req, token)
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
