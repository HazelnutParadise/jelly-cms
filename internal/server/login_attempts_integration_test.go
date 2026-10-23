package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const loginAttemptIntegrationPassword = "login-attempt-test-password"

func loginAttemptUser(t *testing.T, h *accessHarness, name string) core.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(loginAttemptIntegrationPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash test password: %v", err)
	}
	u := core.User{
		Username: name,
		Email:    name + "@example.test",
		Password: string(hash),
		Role:     "admin",
		Provider: "local",
	}
	if err := h.db.Create(&u).Error; err != nil {
		t.Fatalf("create login-attempt test user %q: %v", name, err)
	}
	return u
}

func loginAttemptHTTP(h *accessHarness, method, path, remoteAddr, body string, sourceHeaders map[string]string) accessResult {
	h.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = remoteAddr
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
	}
	for name, value := range sourceHeaders {
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	return accessResult{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone()}
}

func loginAttemptPayload(username, password string) string {
	return fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
}

func assertNoLoginCookies(t *testing.T, result accessResult) {
	t.Helper()
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(result.body), &payload); err != nil {
		t.Errorf("429 body must be JSON (decode error: %v)", err)
	} else {
		var message string
		if err := json.Unmarshal(payload["error"], &message); err != nil || strings.TrimSpace(message) == "" {
			t.Errorf("429 body must contain a non-empty JSON error message")
		}
	}
	if !strings.Contains(strings.ToLower(result.header.Get("Content-Type")), "application/json") {
		t.Errorf("429 content type = %q, want JSON", result.header.Get("Content-Type"))
	}
	for _, value := range result.header.Values("Set-Cookie") {
		name := strings.SplitN(value, "=", 2)[0]
		if name == "jwt_token" || name == "jelly-cms-session" {
			t.Errorf("429 response set login cookie %q", name)
		}
	}
}

func TestLoginAttemptHTTPRejectsThirtyFirstWithoutAuthenticating(t *testing.T) {
	h := newAccessHarness(t)
	user := loginAttemptUser(t, h, "throttle-http")
	var userQueries atomic.Int64
	const callbackName = "login_attempt_test:count_user_queries"
	if err := h.db.Callback().Query().Before("gorm:query").Register(callbackName, func(db *gorm.DB) {
		if db.Statement.Schema != nil && db.Statement.Schema.Table == "users" {
			userQueries.Add(1)
		}
	}); err != nil {
		t.Fatalf("register user-query callback: %v", err)
	}
	t.Cleanup(func() {
		if err := h.db.Callback().Query().Remove(callbackName); err != nil {
			t.Errorf("remove user-query callback before closing fixture DB: %v", err)
		}
	})
	const addr = "192.0.2.31:44001"
	for i := 1; i <= 30; i++ {
		got := loginAttemptHTTP(h, http.MethodPost, "/auth/login", addr, loginAttemptPayload(user.Username, "wrong-password"), map[string]string{"X-Jelly-CSRF": "1"})
		if got.code != http.StatusUnauthorized {
			t.Fatalf("bad-password attempt %d returned %d, want 401; body=%q", i, got.code, got.body)
		}
	}
	if got := userQueries.Load(); got != 30 {
		t.Fatalf("allowed login attempts issued %d users queries, want 30", got)
	}
	blocked := loginAttemptHTTP(h, http.MethodPost, "/auth/login", addr, loginAttemptPayload(user.Username, loginAttemptIntegrationPassword), map[string]string{"X-Jelly-CSRF": "1"})
	if blocked.code != http.StatusTooManyRequests {
		t.Errorf("correct-password attempt 31 returned %d, want 429", blocked.code)
	}
	retryAfter, err := strconv.Atoi(blocked.header.Get("Retry-After"))
	if err != nil || retryAfter <= 0 {
		t.Errorf("Retry-After = %q, want a positive integer number of seconds", blocked.header.Get("Retry-After"))
	}
	assertNoLoginCookies(t, blocked)
	if got := userQueries.Load(); got != 30 {
		t.Errorf("blocked 31st attempt issued %d extra user queries, want 0 (total remains 30)", got-30)
	}
}

func TestLoginAttemptSuccessfulLoginsConsumeQuota(t *testing.T) {
	h := newAccessHarness(t)
	user := loginAttemptUser(t, h, "throttle-success-counts")
	const addr = "192.0.2.38:44001"
	for round := 1; round <= 5; round++ {
		for attempt := 1; attempt <= 5; attempt++ {
			got := loginAttemptHTTP(h, http.MethodPost, "/auth/login", addr, loginAttemptPayload(user.Username, "wrong-password"), map[string]string{"X-Jelly-CSRF": "1"})
			if got.code != http.StatusUnauthorized {
				t.Fatalf("round %d bad-password attempt %d returned %d, want 401", round, attempt, got.code)
			}
		}
		loggedIn := loginAttemptHTTP(h, http.MethodPost, "/auth/login", addr, loginAttemptPayload(user.Username, loginAttemptIntegrationPassword), map[string]string{"X-Jelly-CSRF": "1"})
		if loggedIn.code != http.StatusOK {
			t.Fatalf("round %d correct-password attempt returned %d, want 200", round, loggedIn.code)
		}
	}
	blocked := loginAttemptHTTP(h, http.MethodPost, "/auth/login", addr, loginAttemptPayload(user.Username, loginAttemptIntegrationPassword), map[string]string{"X-Jelly-CSRF": "1"})
	if blocked.code != http.StatusTooManyRequests {
		t.Fatalf("31st request after 25 wrong and 5 successful logins returned %d, want 429", blocked.code)
	}
	if retryAfter, err := strconv.Atoi(blocked.header.Get("Retry-After")); err != nil || retryAfter <= 0 {
		t.Errorf("Retry-After = %q, want a positive integer number of seconds", blocked.header.Get("Retry-After"))
	}
	assertNoLoginCookies(t, blocked)
}

func TestLoginAttemptOtherRemoteAddressCanLoginAndUseToken(t *testing.T) {
	h := newAccessHarness(t)
	user := loginAttemptUser(t, h, "throttle-other-source")
	for i := 0; i < 30; i++ {
		got := loginAttemptHTTP(h, http.MethodPost, "/auth/login", "192.0.2.32:44001", loginAttemptPayload(user.Username, "wrong-password"), map[string]string{"X-Jelly-CSRF": "1"})
		if got.code != http.StatusUnauthorized {
			t.Fatalf("first source bad-password attempt %d returned %d, want 401", i+1, got.code)
		}
	}
	login := loginAttemptHTTP(h, http.MethodPost, "/auth/login", "192.0.2.33:44001", loginAttemptPayload(user.Username, loginAttemptIntegrationPassword), map[string]string{"X-Jelly-CSRF": "1"})
	if login.code != http.StatusOK {
		t.Fatalf("different source login returned %d, want 200", login.code)
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(login.body), &payload); err != nil || payload.Token == "" {
		t.Fatalf("decode successful login token: missing token or malformed JSON (error=%v)", err)
	}
	me := loginAttemptHTTP(h, http.MethodGet, "/api/auth/me", "192.0.2.33:44001", "", map[string]string{"Authorization": "Bearer " + payload.Token})
	if me.code != http.StatusOK {
		t.Fatalf("authenticated /api/auth/me returned %d, want 200; body=%q", me.code, me.body)
	}
	var meUser core.User
	if err := json.Unmarshal([]byte(me.body), &meUser); err != nil {
		t.Fatalf("decode authenticated /api/auth/me response: %v", err)
	}
	if meUser.ID != user.ID || meUser.Email != user.Email {
		t.Errorf("authenticated /api/auth/me identity = (%d, %q), want (%d, %q)", meUser.ID, meUser.Email, user.ID, user.Email)
	}
}

func TestLoginAttemptRotatingAccountsDoesNotResetSourceQuota(t *testing.T) {
	h := newAccessHarness(t)
	first := loginAttemptUser(t, h, "throttle-rotate-a")
	second := loginAttemptUser(t, h, "throttle-rotate-b")
	const addr = "192.0.2.34:44001"
	for i := 0; i < 15; i++ {
		for _, user := range []core.User{first, second} {
			got := loginAttemptHTTP(h, http.MethodPost, "/auth/login", addr, loginAttemptPayload(user.Username, "wrong-password"), map[string]string{"X-Jelly-CSRF": "1"})
			if got.code != http.StatusUnauthorized {
				t.Fatalf("rotating account %q attempt %d returned %d, want 401", user.Username, i+1, got.code)
			}
		}
	}
	blocked := loginAttemptHTTP(h, http.MethodPost, "/auth/login", addr, loginAttemptPayload(second.Username, loginAttemptIntegrationPassword), map[string]string{"X-Jelly-CSRF": "1"})
	if blocked.code != http.StatusTooManyRequests {
		t.Fatalf("rotated account correct-password attempt 31 returned %d, want 429", blocked.code)
	}
	assertNoLoginCookies(t, blocked)
}

func TestLoginAttemptLimitDoesNotAffectPublicOrAuthenticatedReads(t *testing.T) {
	h := newAccessHarness(t)
	user := loginAttemptUser(t, h, "throttle-read-isolation")
	themeToken := h.token(user)
	activateTheme := loginAttemptHTTP(h, http.MethodPost, "/api/admin/themes/activate", "192.0.2.36:44001", `{"name":"default"}`, map[string]string{
		"Authorization": "Bearer " + themeToken,
		"X-Jelly-CSRF":  "1",
	})
	if activateTheme.code != http.StatusOK {
		t.Fatalf("activate default theme returned %d, want 200", activateTheme.code)
	}
	publicBefore := loginAttemptHTTP(h, http.MethodGet, "/", "192.0.2.35:44001", "", nil)
	if publicBefore.code != http.StatusOK {
		t.Fatalf("public homepage before login throttling returned %d, want 200", publicBefore.code)
	}
	goodLogin := loginAttemptHTTP(h, http.MethodPost, "/auth/login", "192.0.2.36:44001", loginAttemptPayload(user.Username, loginAttemptIntegrationPassword), map[string]string{"X-Jelly-CSRF": "1"})
	if goodLogin.code != http.StatusOK {
		t.Fatalf("seed login returned %d, want 200", goodLogin.code)
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(goodLogin.body), &payload); err != nil || payload.Token == "" {
		t.Fatalf("decode seed token: missing token or malformed JSON (error=%v)", err)
	}
	for i := 0; i < 31; i++ {
		got := loginAttemptHTTP(h, http.MethodPost, "/auth/login", "192.0.2.35:44001", loginAttemptPayload(user.Username, "wrong-password"), map[string]string{"X-Jelly-CSRF": "1"})
		want := http.StatusUnauthorized
		if i == 30 {
			want = http.StatusTooManyRequests
		}
		if got.code != want {
			t.Fatalf("exhaustion attempt %d returned %d, want %d", i+1, got.code, want)
		}
	}
	public := loginAttemptHTTP(h, http.MethodGet, "/", "192.0.2.35:44001", "", nil)
	if public.code != http.StatusOK {
		t.Errorf("public homepage from throttled source returned %d, want 200", public.code)
	}
	apiRead := loginAttemptHTTP(h, http.MethodGet, "/api/auth/me", "192.0.2.35:44001", "", map[string]string{"Authorization": "Bearer " + payload.Token})
	if apiRead.code != http.StatusOK {
		t.Errorf("authenticated API read from throttled source returned %d, want 200; body=%q", apiRead.code, apiRead.body)
	}
}

func TestLoginAttemptCSRFRejectionDoesNotConsumeQuota(t *testing.T) {
	h := newAccessHarness(t)
	user := loginAttemptUser(t, h, "throttle-csrf")
	const addr = "192.0.2.37:44001"
	denied := loginAttemptHTTP(h, http.MethodPost, "/auth/login", addr, loginAttemptPayload(user.Username, "wrong-password"), map[string]string{
		"Origin":       "https://attacker.invalid",
		"X-Jelly-CSRF": "1",
	})
	if denied.code != http.StatusForbidden {
		t.Fatalf("cross-origin login returned %d, want 403", denied.code)
	}
	for i := 1; i <= 30; i++ {
		got := loginAttemptHTTP(h, http.MethodPost, "/auth/login", addr, loginAttemptPayload(user.Username, "wrong-password"), map[string]string{"X-Jelly-CSRF": "1"})
		if got.code != http.StatusUnauthorized {
			t.Fatalf("valid-source attempt %d after CSRF rejection returned %d, want 401", i, got.code)
		}
	}
	blocked := loginAttemptHTTP(h, http.MethodPost, "/auth/login", addr, loginAttemptPayload(user.Username, "wrong-password"), map[string]string{"X-Jelly-CSRF": "1"})
	if blocked.code != http.StatusTooManyRequests {
		t.Fatalf("valid-source attempt 31 returned %d, want 429", blocked.code)
	}
}
