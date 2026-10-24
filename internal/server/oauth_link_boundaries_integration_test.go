package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/sessions"
	"github.com/markbates/goth/gothic"
	"gorm.io/gorm"
)

func TestOAuthLinkBoundaryStartLimitIsPerSourceAndSeparateFromNormalOAuth(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	p := newOAuthTxnIntegrationProvider("google")
	resetOAuthInputProviders(p)
	u := h.user("author", "link-boundary-limit")
	login := oauthAccountLoginFor(t, h, u)
	const addr = "192.0.2.81:40101"
	for i := 0; i < 30; i++ {
		rec := oauthBoundaryRequest(h, http.MethodPost, "/api/auth/identities/google/start", addr, []*http.Cookie{login.jwtCookie})
		if rec.Code != http.StatusOK {
			t.Fatalf("link start %d returned %d, want 200: %s", i+1, rec.Code, rec.Body.String())
		}
	}
	p.mu.Lock()
	starts := p.begin
	p.mu.Unlock()
	if starts != 30 {
		t.Fatalf("provider BeginAuth called %d times after 30 starts, want 30", starts)
	}
	blocked := oauthBoundaryRequest(h, http.MethodPost, "/api/auth/identities/google/start", addr, []*http.Cookie{login.jwtCookie})
	if blocked.Code != http.StatusTooManyRequests {
		t.Errorf("31st same-source link start returned %d, want 429", blocked.Code)
	}
	p.mu.Lock()
	startsAfter := p.begin
	p.mu.Unlock()
	if startsAfter != starts {
		t.Errorf("blocked link start called BeginAuth; count changed from %d to %d", starts, startsAfter)
	}
	if cookieFromHeader(blocked.Header(), gothic.SessionName) != nil {
		t.Error("blocked link start set a pending Goth cookie")
	}
	if retry, err := strconv.Atoi(blocked.Header().Get("Retry-After")); err != nil || retry <= 0 {
		t.Errorf("blocked link start Retry-After=%q, want positive integer seconds", blocked.Header().Get("Retry-After"))
	}
	other := oauthBoundaryRequest(h, http.MethodPost, "/api/auth/identities/google/start", "192.0.2.82:40102", []*http.Cookie{login.jwtCookie})
	if other.Code != http.StatusOK {
		t.Errorf("different source link start returned %d, want 200", other.Code)
	}
	normal := oauthBoundaryRequest(h, http.MethodGet, "/auth/google", addr, []*http.Cookie{login.jwtCookie})
	if normal.Code != http.StatusTemporaryRedirect {
		t.Errorf("normal OAuth start sharing source returned %d, want 307", normal.Code)
	}
}

func TestOAuthLinkBoundaryDamagedGothCookieCanBeReplaced(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	p := newOAuthTxnIntegrationProvider("google")
	resetOAuthInputProviders(p)
	u := h.user("author", "link-boundary-cookie")
	login := oauthAccountLoginFor(t, h, u)
	damaged := &http.Cookie{Name: gothic.SessionName, Value: "broken-old-goth-cookie"}
	entry := oauthAccountBeginLinkWithCookies(t, h, p, []*http.Cookie{login.jwtCookie, damaged}, login.jwtCookie, "")
	if entry.state == "" {
		t.Fatal("fresh state was not issued after damaged Goth cookie")
	}
	if entry.state == "broken-old-goth-cookie" {
		t.Fatal("new link state reused damaged cookie value")
	}
	callback := oauthAccountCallback(h, entry, "damaged-cookie-retry", true)
	if callback.Code != http.StatusOK {
		t.Fatalf("link callback after damaged cookie returned %d, want 200: %s", callback.Code, callback.Body.String())
	}
	if got := oauthAccountSubjectOwner(t, h.db, "google", p.user.UserID); got != int64(u.ID) {
		t.Errorf("callback identity owner=%d, want authenticated account %d", got, u.ID)
	}
	// Reusing the same subject is an idempotent clean-cookie control.
	control := oauthAccountBeginLink(t, h, p, login, "")
	cleanCallback := oauthAccountCallback(h, control, "clean-cookie-retry", true)
	if cleanCallback.Code != http.StatusOK {
		t.Errorf("clean-cookie callback returned %d, want 200: %s", cleanCallback.Code, cleanCallback.Body.String())
	}

}

func TestOAuthLinkBoundaryExpiredSignedGothCookieCanBeReplaced(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	p := newOAuthTxnIntegrationProvider("google")
	resetOAuthInputProviders(p)
	u := h.user("author", "link-boundary-expired-cookie")
	login := oauthAccountLoginFor(t, h, u)
	previousStore := gothic.Store
	cookieStore := sessions.NewCookieStore([]byte(oauthTxnSessionSecret))
	cookieStore.MaxAge(1)
	gothic.Store = cookieStore
	t.Cleanup(func() { gothic.Store = previousStore })
	expired := oauthTxnSignedAuthorizedCookie(t, p, "already-expired-state", "expired-provider-token")
	expiredReq := httptest.NewRequest(http.MethodGet, "/", nil)
	expiredReq.AddCookie(expired)
	time.Sleep(2 * time.Second)
	if _, err := cookieStore.Get(expiredReq, gothic.SessionName); err == nil || !strings.Contains(strings.ToLower(err.Error()), "expired") {
		t.Fatalf("public Goth cookie store did not report an expired signature after MaxAge: %v", err)
	}
	expiredStart := oauthBoundaryRequest(h, http.MethodPost, "/api/auth/identities/google/start", "192.0.2.83:40103", []*http.Cookie{login.jwtCookie, expired})
	if expiredStart.Code != http.StatusOK {
		t.Errorf("link start with verified-expired signed Goth cookie returned %d, want 200: %s", expiredStart.Code, expiredStart.Body.String())
	}
}

func TestOAuthLinkBoundaryJWTExpiryDuringIdentityReadRejectsWrite(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	p := newOAuthTxnIntegrationProvider("google")
	resetOAuthInputProviders(p)
	u := h.user("author", "link-boundary-expiry")
	login := oauthAccountLoginFor(t, h, u)
	shortToken, expiresAt := oauthAccountShortenJWT(t, login.token, 3*time.Second)
	shortCookie := *login.jwtCookie
	shortCookie.Value = shortToken
	entry := oauthAccountBeginLinkWithCookies(t, h, p, []*http.Cookie{&shortCookie}, &shortCookie, "")

	entered, release := make(chan struct{}), make(chan struct{})
	const callbackName = "oauth_link_boundary_pause_mapping_read"
	if err := h.db.Callback().Query().After("gorm:query").Register(callbackName, func(db *gorm.DB) {
		if db.Statement == nil || db.Statement.Table != "oauth_identities" {
			return
		}
		select {
		case <-entered:
			return
		default:
		}
		close(entered)
		<-release
	}); err != nil {
		t.Fatalf("register public GORM query callback: %v", err)
	}
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		if err := h.db.Callback().Query().Remove(callbackName); err != nil {
			t.Errorf("remove GORM query callback during cleanup: %v", err)
		}
	})
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- oauthAccountCallback(h, entry, "expiry-after-query", true) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		close(release)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("callback did not exit after releasing the GORM query marker")
		}
		t.Fatal("callback never reached the oauth_identities query marker")
	}
	// The marker proves the callback is paused after the mapping read began. Wait for
	// the signed credential's actual expiry before allowing that query to finish.
	if wait := time.Until(expiresAt); wait > 0 {
		timer := time.NewTimer(wait)
		<-timer.C
	}
	close(release)
	select {
	case rec := <-done:
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("callback released after JWT expiry returned %d, want 401: %s", rec.Code, rec.Body.String())
		}
		assertOAuthTxnNoLoginCookie(t, rec)
	case <-time.After(10 * time.Second):
		t.Fatal("callback remained blocked after releasing GORM query marker")
	}
	if got := oauthAccountActiveIdentityCountOrZero(t, h.db, u.ID); got != 0 {
		t.Errorf("expired callback created %d identity mappings, want 0", got)
	}
	// A live token against the same route is the success control.
	// The expired fixture shares the original token's jti, and principal loading
	// creates its legacy session row with the shortened expiry. Use a fresh login
	// for the success control so that fixture bookkeeping cannot invalidate it.
	controlLogin := oauthAccountLoginFor(t, h, u)
	control := oauthAccountBeginLink(t, h, p, controlLogin, "")
	valid := oauthAccountCallback(h, control, "not-expired-control", true)
	if valid.Code != http.StatusOK {
		t.Errorf("unexpired callback returned %d, want 200: %s", valid.Code, valid.Body.String())
	}
}

func TestOAuthLinkBoundaryJSONAuditLogsContainOnlySafeIdentityFields(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	p := newOAuthTxnIntegrationProvider("google")
	backup := newOAuthTxnIntegrationProvider("github")
	p.user.UserID = "unique-subject-secret-boundary"
	resetOAuthInputProviders(p, backup)
	u := h.user("author", "link-boundary-audit")
	login := oauthAccountLoginFor(t, h, u)
	var logs bytes.Buffer
	h.echo.Logger.SetOutput(&logs)
	logs.Reset()
	start := oauthAccountServe(h, http.MethodPost, "/api/auth/identities/google/start", []*http.Cookie{login.jwtCookie}, nil, "", "")
	if start.Code != http.StatusOK {
		t.Fatalf("link start returned %d, want 200", start.Code)
	}
	assertOAuthBoundaryAudit(t, logs.String(), "link_start", "success", http.StatusOK, "google", float64(u.ID))
	assertOAuthBoundaryNoSecrets(t, logs.String(), login.token, login.jwtCookie.Value)
	logs.Reset()
	entry := oauthAccountBeginLink(t, h, p, login, "")
	logs.Reset()
	callback := oauthAccountCallback(h, entry, "unique-code-secret-boundary", true)
	if callback.Code != http.StatusOK {
		t.Fatalf("link callback returned %d, want 200", callback.Code)
	}
	assertOAuthBoundaryAudit(t, logs.String(), "link", "success", http.StatusOK, "google", float64(u.ID))
	assertOAuthBoundaryNoSecrets(t, logs.String(), p.user.UserID, "unique-code-secret-boundary", entry.state, login.token, login.jwtCookie.Value)
	// Normal OAuth callback uses a separate intent and must not be recorded as linking.
	logs.Reset()
	normalEntry := oauthAccountBeginNormal(t, h, p, login, "")
	normalCallback := oauthAccountCallback(h, normalEntry, "normal-oauth-not-link", true)
	if normalCallback.Code != http.StatusOK {
		t.Fatalf("normal OAuth callback returned %d, want 200", normalCallback.Code)
	}
	if got := oauthBoundaryAuditEvents(t, logs.String()); len(got) != 0 {
		t.Errorf("normal OAuth callback emitted identity audit events: %+v", got)
	}
	assertOAuthBoundaryNoSecrets(t, logs.String(), "normal-oauth-not-link")
	if err := oauthAccountInsertIdentity(h.db, u.ID, "github", "audit-unlink-backup"); err != nil {
		t.Fatal(err)
	}
	logs.Reset()
	unlink := oauthAccountServe(h, http.MethodDelete, "/api/auth/identities/google", []*http.Cookie{login.jwtCookie}, nil, "", "")
	if unlink.Code != http.StatusOK {
		t.Fatalf("unlink returned %d, want 200", unlink.Code)
	}
	assertOAuthBoundaryAudit(t, logs.String(), "unlink", "success", http.StatusOK, "google", float64(u.ID))
	assertOAuthBoundaryNoSecrets(t, logs.String(), login.token, login.jwtCookie.Value)
	logs.Reset()
	unauth := oauthAccountServe(h, http.MethodPost, "/api/auth/identities/google/start", nil, nil, "", "")
	if unauth.Code != http.StatusUnauthorized {
		t.Errorf("anonymous link start returned %d, want 401", unauth.Code)
	}
	assertOAuthBoundaryAudit(t, logs.String(), "link_start", "failure", http.StatusUnauthorized, "google", nil)
	logs.Reset()
	forbidden := oauthAccountServe(h, http.MethodPost, "/api/auth/identities/google/start", []*http.Cookie{login.jwtCookie}, nil, "", "https://attacker.invalid")
	if forbidden.Code != http.StatusForbidden {
		t.Errorf("cross-origin link start returned %d, want 403", forbidden.Code)
	}
	assertOAuthBoundaryAudit(t, logs.String(), "link_start", "failure", http.StatusForbidden, "google", float64(u.ID))
	assertOAuthBoundaryNoSecrets(t, logs.String(), login.token, login.jwtCookie.Value)
	h.echo.Logger.SetOutput(oauthBoundaryErrorWriter{})
	writerFailure := oauthAccountServe(h, http.MethodPost, "/api/auth/identities/google/start", []*http.Cookie{login.jwtCookie}, nil, "", "")
	if writerFailure.Code != http.StatusOK {
		t.Errorf("link start with failing logger writer returned %d, want 200", writerFailure.Code)
	}
}

func oauthBoundaryAuditEvents(t *testing.T, output string) []map[string]any {
	t.Helper()
	events := make([]map[string]any, 0)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) == nil && record["event"] == "oauth_identity" {
			events = append(events, record)
		}
	}
	return events
}

func assertOAuthBoundaryAudit(t *testing.T, output, action, result string, status int, provider string, actor any) {
	t.Helper()
	events := oauthBoundaryAuditEvents(t, output)
	if len(events) != 1 {
		t.Errorf("got %d oauth_identity events for %s/%d, want exactly one; output=%q", len(events), action, status, output)
		return
	}
	record := events[0]
	if record["action"] != action || record["result"] != result || record["provider"] != provider || record["status"] != float64(status) {
		t.Errorf("audit action/result/provider/status=%v/%v/%v/%v, want %s/%s/%s/%d", record["action"], record["result"], record["provider"], record["status"], action, result, provider, status)
	}
	requestID, _ := record["request_id"].(string)
	if requestID == "" {
		t.Error("audit request_id is empty")
	}
	if record["actor_id"] != actor {
		t.Errorf("audit actor_id=%v, want %v", record["actor_id"], actor)
	}
}

func assertOAuthBoundaryNoSecrets(t *testing.T, output string, markers ...string) {
	t.Helper()
	for _, marker := range append(markers, "login_id") {
		if marker != "" && strings.Contains(output, marker) {
			t.Errorf("audit output exposed forbidden marker %q: %s", marker, output)
		}
	}
}

type oauthBoundaryErrorWriter struct{}

func (oauthBoundaryErrorWriter) Write([]byte) (int, error) {
	return 0, fmt.Errorf("fixture log writer failure")
}

func oauthBoundaryRequest(h *accessHarness, method, path, remote string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = remote
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
		req.Header.Set("X-Jelly-CSRF", "1")
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	return rec
}
