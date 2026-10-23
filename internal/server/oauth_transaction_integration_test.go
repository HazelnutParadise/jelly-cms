package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/i18n"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"golang.org/x/oauth2"
)

const oauthTxnSessionSecret = "oauth-transaction-integration-session-secret-012345"
const oauthTxnJWTSecret = "oauth-transaction-integration-jwt-secret-0123456789"

func TestOAuthTransactionEntryCallbackAndReplayUseOneTimeProviderCode(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "production")
	h := oauthTxnPrepareHarness(t)
	p := newOAuthTxnIntegrationProvider("google")
	resetOAuthInputProviders(p)
	entry := oauthTxnServe(h, http.MethodGet, "/auth/google?state=caller&redirect=%2Fadmin", nil)
	if entry.Code != http.StatusTemporaryRedirect {
		t.Fatalf("OAuth entry status=%d, want 307", entry.Code)
	}
	state, err := url.Parse(entry.Header().Get(echo.HeaderLocation))
	if err != nil {
		t.Fatalf("parse provider redirect: %v", err)
	}
	stateValue := state.Query().Get("state")
	if stateValue == "" || stateValue == "caller" {
		t.Fatalf("provider redirect did not contain a fresh state")
	}
	entryCookie := oauthTxnResponseCookie(entry, gothic.SessionName)
	if entryCookie == nil {
		t.Fatal("OAuth entry omitted the Goth transaction cookie")
	}
	if entryCookie.MaxAge != 600 || entryCookie.Path != "/" || !entryCookie.HttpOnly || entryCookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("entry OAuth cookie attributes MaxAge=%d Path=%q HttpOnly=%v SameSite=%v", entryCookie.MaxAge, entryCookie.Path, entryCookie.HttpOnly, entryCookie.SameSite)
	}
	if entryCookie.Secure != (os.Getenv("APP_ENV") == "production") {
		t.Errorf("entry Secure=%v, APP_ENV=%q", entryCookie.Secure, os.Getenv("APP_ENV"))
	}
	entryAppCookie := oauthTxnResponseCookie(entry, "jelly-cms-session")
	if entryAppCookie == nil {
		t.Fatal("OAuth entry omitted the application session cookie")
	}
	if entryAppCookie.MaxAge != 7*24*60*60 || !entryAppCookie.HttpOnly || !entryAppCookie.Secure || entryAppCookie.SameSite != http.SameSiteLaxMode || entryAppCookie.Path != "/" {
		t.Errorf("entry app cookie attributes MaxAge=%d Path=%q HttpOnly=%v Secure=%v SameSite=%v", entryAppCookie.MaxAge, entryAppCookie.Path, entryAppCookie.HttpOnly, entryAppCookie.Secure, entryAppCookie.SameSite)
	}

	path := "/auth/google/callback?state=" + url.QueryEscape(stateValue) + "&code=single-use-code"
	firstReq := httptest.NewRequest(http.MethodGet, path, nil)
	firstReq.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
	firstReq.AddCookie(entryCookie)
	first := httptest.NewRecorder()
	h.echo.ServeHTTP(first, firstReq)
	if first.Code != http.StatusOK {
		t.Fatalf("valid OAuth callback returned %d, want 200: %s", first.Code, first.Body.String())
	}
	var payload struct {
		Message string    `json:"message"`
		Token   string    `json:"token"`
		User    core.User `json:"user"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode OAuth response: %v", err)
	}
	if payload.Token == "" || payload.User.ID == 0 {
		t.Fatalf("OAuth callback omitted authenticated identity")
	}
	var appCookie *http.Cookie
	for _, c := range (&http.Response{Header: first.Header()}).Cookies() {
		if c.Name == "jelly-cms-session" {
			appCookie = c
		}
	}
	if appCookie == nil || appCookie.MaxAge != 7*24*60*60 || !appCookie.HttpOnly || !appCookie.Secure || appCookie.SameSite != http.SameSiteLaxMode || appCookie.Path != "/" {
		if appCookie == nil {
			t.Error("application login cookie missing")
		} else {
			t.Errorf("application login cookie attributes MaxAge=%d Path=%q HttpOnly=%v Secure=%v SameSite=%v", appCookie.MaxAge, appCookie.Path, appCookie.HttpOnly, appCookie.Secure, appCookie.SameSite)
		}
	}
	if appCookie != nil {
		beforeSession := oauthTxnReadAppSession(t, appCookie)
		reentry := oauthTxnServe(h, http.MethodGet, "/auth/google", []*http.Cookie{appCookie})
		if reentry.Code != http.StatusTemporaryRedirect {
			t.Errorf("OAuth re-entry status=%d, want 307", reentry.Code)
		}
		resignedCookie := oauthTxnResponseCookie(reentry, "jelly-cms-session")
		if resignedCookie == nil {
			t.Fatal("OAuth re-entry omitted the re-signed application session cookie")
		}
		afterSession := oauthTxnReadAppSession(t, resignedCookie)
		for _, key := range []string{"login_id", "user_id", "login_expires_at"} {
			if !reflect.DeepEqual(beforeSession.Values[key], afterSession.Values[key]) {
				t.Errorf("OAuth re-entry changed app session %s", key)
			}
		}
		if resignedCookie.MaxAge != 7*24*60*60 || !resignedCookie.HttpOnly || !resignedCookie.Secure || resignedCookie.SameSite != http.SameSiteLaxMode || resignedCookie.Path != "/" {
			t.Errorf("re-signed app cookie attributes MaxAge=%d Path=%q HttpOnly=%v Secure=%v SameSite=%v", resignedCookie.MaxAge, resignedCookie.Path, resignedCookie.HttpOnly, resignedCookie.Secure, resignedCookie.SameSite)
		}
	}
	if got := h.request(http.MethodGet, "/api/auth/me", "", payload.Token, echo.MIMEApplicationJSON, ""); got.code != http.StatusOK {
		t.Fatalf("/api/auth/me with OAuth token returned %d, want 200: %s", got.code, got.body)
	}
	if p.authorizeCount() != 1 || p.fetchCount() < 1 {
		t.Errorf("first callback provider calls Authorize=%d FetchUser=%d", p.authorizeCount(), p.fetchCount())
	}
	fetchesAfterLogin := p.fetchCount()
	for _, c := range (&http.Response{Header: first.Header()}).Cookies() {
		if c.Name == gothic.SessionName && c.MaxAge > 0 {
			t.Errorf("callback re-saved positive-lifetime Goth cookie")
		}
	}
	assertOAuthTxnGothCleared(t, first)
	before := accessUsers(t, h.db)

	for _, replay := range []struct {
		name   string
		query  string
		cookie *http.Cookie
	}{
		{name: "original callback", query: path, cookie: entryCookie},
		{name: "authorized Goth session without code", query: "/auth/google/callback?state=" + url.QueryEscape(stateValue), cookie: oauthTxnSignedAuthorizedCookie(t, p, stateValue, "legacy-authorized-token")},
	} {
		t.Run(replay.name, func(t *testing.T) {
			if replay.cookie == nil {
				t.Fatal("fixture has no signed Goth cookie to replay")
			}
			req := httptest.NewRequest(http.MethodGet, replay.query, nil)
			req.AddCookie(replay.cookie)
			rec := httptest.NewRecorder()
			h.echo.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("replayed transaction returned %d, want 400", rec.Code)
			}
			assertOAuthTxnNoLoginCookie(t, rec)
			if got := p.authorizeCount(); got != 1 {
				t.Errorf("replay caused provider Authorize count=%d, want 1", got)
			}
			if got := p.fetchCount(); got != fetchesAfterLogin {
				t.Errorf("replay caused FetchUser count=%d, want unchanged %d", got, fetchesAfterLogin)
			}
			if after := accessUsers(t, h.db); !sameAccessUsers(before, after) {
				t.Errorf("replay changed users")
			}
		})
	}
}

func TestOAuthTransactionConsumedStateOutlivesCookieBoundary(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	p := newOAuthTxnIntegrationProvider("google")
	resetOAuthInputProviders(p)
	usersBefore := accessUsers(t, h.db)

	synctest.Test(t, func(t *testing.T) {
		// Align cookie issuance to a Unix-second boundary. securecookie compares
		// its integer timestamp, while the database expiry retains subsecond time.
		nextSecond := time.Unix(time.Now().Unix()+1, 0)
		if delay := time.Until(nextSecond); delay > 0 {
			time.Sleep(delay)
		}
		issuedSecond := time.Now().Unix()
		entry := oauthTxnServe(h, http.MethodGet, "/auth/google", nil)
		if entry.Code != http.StatusTemporaryRedirect {
			t.Fatalf("OAuth entry returned %d, want 307", entry.Code)
		}
		location, err := url.Parse(entry.Header().Get(echo.HeaderLocation))
		if err != nil {
			t.Fatalf("parse provider redirect: %v", err)
		}
		state := location.Query().Get("state")
		cookie := oauthTxnResponseCookie(entry, gothic.SessionName)
		appCookie := oauthTxnResponseCookie(entry, "jelly-cms-session")
		if state == "" || cookie == nil || appCookie == nil {
			t.Fatal("OAuth entry omitted state or a session cookie")
		}
		if time.Now().Unix() != issuedSecond {
			t.Fatal("OAuth entry crossed the aligned cookie signing second")
		}

		// Keep an independent, unconsumed cookie for the genuine-expiry control.
		controlEntry := oauthTxnServe(h, http.MethodGet, "/auth/google", nil)
		controlLocation, err := url.Parse(controlEntry.Header().Get(echo.HeaderLocation))
		if err != nil {
			t.Fatalf("parse expiry-control redirect: %v", err)
		}
		controlState := controlLocation.Query().Get("state")
		controlCookie := oauthTxnResponseCookie(controlEntry, gothic.SessionName)
		controlAppCookie := oauthTxnResponseCookie(controlEntry, "jelly-cms-session")
		if controlEntry.Code != http.StatusTemporaryRedirect || controlState == "" || controlCookie == nil || controlAppCookie == nil {
			t.Fatal("expiry-control OAuth entry omitted state or a session cookie")
		}
		if time.Now().Unix() != issuedSecond {
			t.Fatal("expiry-control entry crossed the aligned cookie signing second")
		}

		callback := func(callbackState, code string, gothCookie, sessionCookie *http.Cookie) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state="+url.QueryEscape(callbackState)+"&code="+url.QueryEscape(code), nil)
			req.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
			req.AddCookie(gothCookie)
			req.AddCookie(sessionCookie)
			rec := httptest.NewRecorder()
			h.echo.ServeHTTP(rec, req)
			return rec
		}

		time.Sleep(200 * time.Millisecond)
		first := callback(state, "boundary-one-time-code", cookie, appCookie)
		if first.Code != http.StatusOK {
			t.Fatalf("first valid OAuth callback returned %d, want 200", first.Code)
		}
		var payload struct {
			User core.User `json:"user"`
		}
		if err := json.Unmarshal(first.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode first OAuth response: %v", err)
		}
		if payload.User.ID == 0 {
			t.Fatal("first OAuth callback omitted the authenticated user")
		}
		if p.authorizeCount() != 1 || p.fetchCount() < 1 {
			t.Fatalf("first callback provider calls Authorize=%d FetchUser=%d", p.authorizeCount(), p.fetchCount())
		}
		usersAfterLogin := accessUsers(t, h.db)
		if len(usersAfterLogin) != len(usersBefore)+1 {
			t.Fatalf("first callback users=%d, want one new user", len(usersAfterLogin))
		}
		fetchesAfterLogin := p.fetchCount()

		time.Sleep(600*time.Second + 100*time.Millisecond)
		replay := callback(state, "boundary-one-time-code", cookie, appCookie)
		if replay.Code != http.StatusBadRequest {
			t.Errorf("callback replay at 600.3 seconds returned %d, want 400", replay.Code)
		}
		assertOAuthTxnNoLoginCookie(t, replay)
		if p.authorizeCount() != 1 || p.fetchCount() != fetchesAfterLogin {
			t.Errorf("expired callback replay reached provider: Authorize=%d FetchUser=%d", p.authorizeCount(), p.fetchCount())
		}
		if afterReplay := accessUsers(t, h.db); !sameAccessUsers(usersAfterLogin, afterReplay) {
			t.Error("expired callback replay changed users")
		}

		time.Sleep(700 * time.Millisecond)
		authorizeAfterReplay, fetchAfterReplay := p.authorizeCount(), p.fetchCount()
		expiredCookie := callback(controlState, "expiry-control-code", controlCookie, controlAppCookie)
		if expiredCookie.Code != http.StatusBadRequest {
			t.Errorf("callback with genuinely expired cookie returned %d, want 400", expiredCookie.Code)
		}
		assertOAuthTxnNoLoginCookie(t, expiredCookie)
		if p.authorizeCount() != authorizeAfterReplay || p.fetchCount() != fetchAfterReplay {
			t.Error("genuinely expired cookie reached provider")
		}
	})
}

func TestOAuthTransactionConcurrentCallbacksConsumeStateOnce(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	p := newOAuthTxnIntegrationProvider("google")
	resetOAuthInputProviders(p)
	entry := oauthTxnServe(h, http.MethodGet, "/auth/google", nil)
	stateURL, _ := url.Parse(entry.Header().Get(echo.HeaderLocation))
	cookie := oauthTxnResponseCookie(entry, gothic.SessionName)
	if cookie == nil {
		t.Fatal("entry omitted Goth cookie")
	}
	path := "/auth/google/callback?state=" + url.QueryEscape(stateURL.Query().Get("state")) + "&code=concurrent-code"
	const n = 8
	status := make([]int, n)
	var wg sync.WaitGroup
	for i := range status {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
			req.AddCookie(cookie)
			rec := httptest.NewRecorder()
			h.echo.ServeHTTP(rec, req)
			status[i] = rec.Code
			if rec.Code != http.StatusOK {
				assertOAuthTxnNoLoginCookie(t, rec)
			}
		}(i)
	}
	wg.Wait()
	successes, rejected := 0, 0
	for _, code := range status {
		if code == http.StatusOK {
			successes++
		}
		if code == http.StatusBadRequest {
			rejected++
		}
	}
	if successes != 1 || rejected != n-1 {
		t.Errorf("concurrent callback statuses success=%d rejected=%d all=%v, want 1 success and %d 400s", successes, rejected, status, n-1)
	}
	if p.authorizeCount() != 1 {
		t.Errorf("concurrent callbacks reached Authorize %d times, want once", p.authorizeCount())
	}
	if got := p.fetchCount(); got < 1 || got > 2 {
		t.Errorf("FetchUser calls=%d, want 1 or 2 across the single successful exchange", got)
	}
}

func TestOAuthTransactionRejectsBadCodeAndAuthorizationDenial(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	for _, tc := range []struct {
		name, query string
		wantCalls   int
	}{
		{"missing code", "state=%s", 0}, {"blank code", "state=%s&code=+", 0}, {"duplicate code", "state=%s&code=a&code=b", 0}, {"code and error", "state=%s&code=a&error=access_denied", 0},
		{"empty error", "state=%s&error=", 0}, {"blank error", "state=%s&error=+", 0}, {"duplicate error", "state=%s&error=access_denied&error=server_error", 0},
		{"code and empty error", "state=%s&code=a&error=", 0}, {"denied", "state=%s&error=access_denied", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newOAuthTxnIntegrationProvider("google")
			resetOAuthInputProviders(p)
			entry := oauthTxnServe(h, http.MethodGet, "/auth/google", nil)
			u, _ := url.Parse(entry.Header().Get(echo.HeaderLocation))
			cookie := oauthTxnResponseCookie(entry, gothic.SessionName)
			path := "/auth/google/callback?" + fmt.Sprintf(tc.query, url.QueryEscape(u.Query().Get("state")))
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.AddCookie(cookie)
			rec := httptest.NewRecorder()
			h.echo.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("callback status=%d, want 400", rec.Code)
			}
			if p.authorizeCount() != tc.wantCalls || p.fetchCount() != 0 {
				t.Errorf("denied/malformed request called provider: Authorize=%d FetchUser=%d", p.authorizeCount(), p.fetchCount())
			}
			assertOAuthTxnNoLoginCookie(t, rec)
			if tc.name == "denied" {
				assertOAuthTxnGothCleared(t, rec)
			} else {
				assertOAuthTxnNoGothClear(t, rec)
			}
			if tc.name == "denied" {
				beforeAuth, beforeFetch := p.authorizeCount(), p.fetchCount()
				retry := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state="+url.QueryEscape(u.Query().Get("state"))+"&code=after-denial", nil)
				retry.AddCookie(cookie)
				retryRec := httptest.NewRecorder()
				h.echo.ServeHTTP(retryRec, retry)
				if retryRec.Code != http.StatusBadRequest {
					t.Errorf("replayed denial returned %d, want 400", retryRec.Code)
				}
				if p.authorizeCount() != beforeAuth || p.fetchCount() != beforeFetch {
					t.Error("replayed denial reached provider")
				}
			} else {
				validRetry := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state="+url.QueryEscape(u.Query().Get("state"))+"&code=after-invalid-input", nil)
				validRetry.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
				validRetry.AddCookie(cookie)
				validRec := httptest.NewRecorder()
				h.echo.ServeHTTP(validRec, validRetry)
				if validRec.Code != http.StatusOK {
					t.Errorf("valid retry after malformed callback returned %d, want 200", validRec.Code)
				}
			}
		})
	}
}

func TestOAuthTransactionProviderFailureConsumesState(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	p := newOAuthTxnIntegrationProvider("google")
	resetOAuthInputProviders(p)
	entry := oauthTxnServe(h, http.MethodGet, "/auth/google", nil)
	u, _ := url.Parse(entry.Header().Get(echo.HeaderLocation))
	cookie := oauthTxnResponseCookie(entry, gothic.SessionName)
	path := "/auth/google/callback?state=" + url.QueryEscape(u.Query().Get("state")) + "&code=provider-failure"
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("provider failure returned %d, want 502", rec.Code)
	}
	for _, secret := range []string{"provider-failure", u.Query().Get("state"), "provider-token:provider-failure"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Error("provider failure response exposed callback state, code, or token")
		}
	}
	assertOAuthTxnNoLoginCookie(t, rec)
	assertOAuthTxnGothCleared(t, rec)
	beforeAuth, beforeFetch := p.authorizeCount(), p.fetchCount()
	retry := httptest.NewRequest(http.MethodGet, path, nil)
	retry.AddCookie(cookie)
	retryRec := httptest.NewRecorder()
	h.echo.ServeHTTP(retryRec, retry)
	if retryRec.Code != http.StatusBadRequest {
		t.Errorf("provider failure replay returned %d, want 400", retryRec.Code)
	}
	if p.authorizeCount() != beforeAuth || p.fetchCount() != beforeFetch {
		t.Error("provider failure replay reached provider")
	}
}

func TestOAuthTransactionDatabaseFailureAndCanceledCallbackStopProvider(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	p := newOAuthTxnIntegrationProvider("google")
	resetOAuthInputProviders(p)
	entry := oauthTxnServe(h, http.MethodGet, "/auth/google", nil)
	u, _ := url.Parse(entry.Header().Get(echo.HeaderLocation))
	cookie := oauthTxnResponseCookie(entry, gothic.SessionName)
	path := "/auth/google/callback?state=" + url.QueryEscape(u.Query().Get("state")) + "&code=cancel-code"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusServiceUnavailable {
		t.Errorf("canceled callback status=%d, want 400 or 503", rec.Code)
	}
	if p.authorizeCount() != 0 || p.fetchCount() != 0 {
		t.Errorf("canceled callback contacted provider")
	}
	retryCanceled := httptest.NewRequest(http.MethodGet, path, nil)
	retryCanceled.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
	retryCanceled.AddCookie(cookie)
	retryCanceledRec := httptest.NewRecorder()
	h.echo.ServeHTTP(retryCanceledRec, retryCanceled)
	if retryCanceledRec.Code != http.StatusOK {
		t.Errorf("retry after cancellation returned %d, want 200", retryCanceledRec.Code)
	}
	p = newOAuthTxnIntegrationProvider("google")
	resetOAuthInputProviders(p)

	// Fail the state INSERT transaction, then remove the fixture trigger and retry the same signed transaction.
	if err := h.db.Exec(`CREATE FUNCTION reject_oauth_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture insert failure'; END $$`).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.db.Exec(`CREATE TRIGGER reject_oauth_insert BEFORE INSERT ON revoked_logins FOR EACH ROW EXECUTE FUNCTION reject_oauth_insert()`).Error; err != nil {
		t.Fatal(err)
	}
	entry = oauthTxnServe(h, http.MethodGet, "/auth/google", nil)
	u, _ = url.Parse(entry.Header().Get(echo.HeaderLocation))
	cookie = oauthTxnResponseCookie(entry, gothic.SessionName)
	path = "/auth/google/callback?state=" + url.QueryEscape(u.Query().Get("state")) + "&code=db-failure-code"
	req = httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("callback with failed state insert returned %d, want 503", rec.Code)
	}
	if p.authorizeCount() != 0 || p.fetchCount() != 0 {
		t.Errorf("database failure contacted provider: Authorize=%d FetchUser=%d", p.authorizeCount(), p.fetchCount())
	}
	assertOAuthTxnNoLoginCookie(t, rec)
	if err := h.db.Exec("DROP TRIGGER reject_oauth_insert ON revoked_logins").Error; err != nil {
		t.Fatal(err)
	}
	if err := h.db.Exec("DROP FUNCTION reject_oauth_insert()").Error; err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("retry after rolled-back state insertion returned %d, want 200", rec.Code)
	}
}

func TestOAuthTransactionDeleteFailureAndCleanupIsolation(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	// Seed a non-expired login revocation row. A successful OAuth transaction must leave it untouched.
	p := newOAuthTxnIntegrationProvider("google")
	resetOAuthInputProviders(p)
	entry := oauthTxnServe(h, http.MethodGet, "/auth/google", nil)
	u, _ := url.Parse(entry.Header().Get(echo.HeaderLocation))
	state := u.Query().Get("state")
	stateDigest := sha256.Sum256([]byte(state))
	stateHash := hex.EncodeToString(stateDigest[:])
	loginID := base64.RawURLEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	loginDigest := sha256.Sum256([]byte(loginID))
	loginHash := hex.EncodeToString(loginDigest[:])
	loginExpiry := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Microsecond)
	stateExpiry := time.Now().Add(36 * time.Hour).UTC().Truncate(time.Microsecond)
	if err := h.db.Create(&core.RevokedLogin{ID: stateHash, ExpiresAt: stateExpiry}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.db.Create(&core.RevokedLogin{ID: loginHash, ExpiresAt: loginExpiry}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.db.Create(&core.RevokedLogin{ID: strings.Repeat("e", 64), ExpiresAt: time.Now().Add(-time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	cookie := oauthTxnResponseCookie(entry, gothic.SessionName)
	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state="+url.QueryEscape(u.Query().Get("state"))+"&code=cleanup-code", nil)
	req.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid OAuth callback status=%d, want 200", rec.Code)
	}
	var still core.RevokedLogin
	if err := h.db.First(&still, "id = ?", loginHash).Error; err != nil {
		t.Fatalf("unexpired login revocation row was removed: %v", err)
	}
	if !still.ExpiresAt.Equal(loginExpiry) {
		t.Errorf("existing login revocation expiry changed from %s to %s", loginExpiry, still.ExpiresAt)
	}
	var stateRow core.RevokedLogin
	if err := h.db.First(&stateRow, "id = ?", stateHash).Error; err != nil {
		t.Fatalf("OAuth transaction collided with SHA-256(state) legacy row: %v", err)
	}
	if !stateRow.ExpiresAt.Equal(stateExpiry) {
		t.Errorf("state-digest sentinel expiry changed from %s to %s", stateExpiry, stateRow.ExpiresAt)
	}
	var expired int64
	h.db.Model(&core.RevokedLogin{}).Where("id = ?", strings.Repeat("e", 64)).Count(&expired)
	if expired != 0 {
		t.Errorf("expired revocation row remained after OAuth cleanup")
	}

	// A DELETE trigger makes the same transaction fail before any provider call.
	if err := h.db.Exec(`CREATE FUNCTION reject_revoked_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture cleanup failure'; END $$`).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.db.Exec(`CREATE TRIGGER reject_revoked_cleanup BEFORE DELETE ON revoked_logins FOR EACH ROW EXECUTE FUNCTION reject_revoked_cleanup()`).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.db.Create(&core.RevokedLogin{ID: strings.Repeat("f", 64), ExpiresAt: time.Now().Add(-time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	p = newOAuthTxnIntegrationProvider("google")
	resetOAuthInputProviders(p)
	entry = oauthTxnServe(h, http.MethodGet, "/auth/google", nil)
	u, _ = url.Parse(entry.Header().Get(echo.HeaderLocation))
	cookie = oauthTxnResponseCookie(entry, gothic.SessionName)
	req = httptest.NewRequest(http.MethodGet, "/auth/google/callback?state="+url.QueryEscape(u.Query().Get("state"))+"&code=cleanup-failure-code", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("cleanup failure returned %d, want 503", rec.Code)
	}
	assertOAuthTxnNoLoginCookie(t, rec)
	if p.authorizeCount() != 0 || p.fetchCount() != 0 {
		t.Errorf("cleanup failure contacted provider")
	}
	if err := h.db.Exec("DROP TRIGGER reject_revoked_cleanup ON revoked_logins").Error; err != nil {
		t.Fatal(err)
	}
	if err := h.db.Exec("DROP FUNCTION reject_revoked_cleanup()").Error; err != nil {
		t.Fatal(err)
	}
	retry := httptest.NewRequest(http.MethodGet, req.URL.RequestURI(), nil)
	retry.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
	retry.AddCookie(cookie)
	retryRec := httptest.NewRecorder()
	h.echo.ServeHTTP(retryRec, retry)
	if retryRec.Code != http.StatusOK {
		t.Errorf("retry after cleanup failure returned %d, want 200", retryRec.Code)
	}
}

func TestOAuthTransactionGothCookieClearSaveFailureStopsLogin(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	p := newOAuthTxnIntegrationProvider("google")
	resetOAuthInputProviders(p)
	entry := oauthTxnServe(h, http.MethodGet, "/auth/google", nil)
	u, _ := url.Parse(entry.Header().Get(echo.HeaderLocation))
	cookie := oauthTxnResponseCookie(entry, gothic.SessionName)
	oldStore := gothic.Store
	gothic.Store = &oauthTxnFailDeleteStore{Store: oldStore}
	t.Cleanup(func() { gothic.Store = oldStore })
	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state="+url.QueryEscape(u.Query().Get("state"))+"&code=clear-save-failure", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("Goth cookie clear save failure returned %d, want 503", rec.Code)
	}
	if p.authorizeCount() != 0 || p.fetchCount() != 0 {
		t.Errorf("failed Goth cookie clearing reached provider")
	}
	assertOAuthTxnNoLoginCookie(t, rec)
	gothic.Store = oldStore
	retry := httptest.NewRequest(http.MethodGet, req.URL.RequestURI(), nil)
	retry.AddCookie(cookie)
	retryRec := httptest.NewRecorder()
	h.echo.ServeHTTP(retryRec, retry)
	if retryRec.Code != http.StatusBadRequest {
		t.Errorf("retry after failed Goth cookie clear returned %d, want 400", retryRec.Code)
	}
	if p.authorizeCount() != 0 || p.fetchCount() != 0 {
		t.Error("retry after failed Goth clear reached provider")
	}
}

func TestOAuthTransactionCrossProcessReplayIsRejected(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	h := oauthTxnPrepareHarness(t)
	p := newOAuthTxnIntegrationProvider("google")
	resetOAuthInputProviders(p)
	entry := oauthTxnServe(h, http.MethodGet, "/auth/google", nil)
	u, _ := url.Parse(entry.Header().Get(echo.HeaderLocation))
	cookie := oauthTxnResponseCookie(entry, gothic.SessionName)
	workDir, err := os.MkdirTemp("/tmp", "jelly-oauth-transaction-tests-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(workDir); err != nil {
			t.Errorf("remove owned OAuth test fixture: %v", err)
		}
	})
	path := filepath.Join(workDir, "goth-cookie")
	if err := os.WriteFile(path, []byte(cookie.Value), 0600); err != nil {
		t.Fatal(err)
	}
	first := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state="+url.QueryEscape(u.Query().Get("state"))+"&code=one-time-child-code", nil)
	first.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
	first.AddCookie(cookie)
	firstRec := httptest.NewRecorder()
	h.echo.ServeHTTP(firstRec, first)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("first callback before cross-process replay returned %d", firstRec.Code)
	}
	userCount := len(accessUsers(t, h.db))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOAuthTransactionReplayHelperProcess$", "-test.count=1")
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "JELLY_OAUTH_TXN_REPLAY_HELPER=1", "JELLY_OAUTH_TXN_COOKIE_FILE="+path, "JELLY_OAUTH_TXN_STATE="+u.Query().Get("state"), "JELLY_OAUTH_TXN_CODE=one-time-child-code", fmt.Sprintf("JELLY_OAUTH_TXN_USER_COUNT=%d", userCount))
	// Pass the isolated fixture database to a fresh process.
	configName := oauthTxnConfigDatabaseName(t)
	cmd.Env = append(cmd.Env, "JELLY_OAUTH_TXN_DB_NAME="+configName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cross-process OAuth replay helper failed: %v; output=%s", err, output)
	}
}

func TestOAuthTransactionReplayHelperProcess(t *testing.T) {
	if os.Getenv("JELLY_OAUTH_TXN_REPLAY_HELPER") != "1" {
		t.Skip("helper process only")
	}
	dbName := os.Getenv("JELLY_OAUTH_TXN_DB_NAME")
	cookiePath := os.Getenv("JELLY_OAUTH_TXN_COOKIE_FILE")
	state := os.Getenv("JELLY_OAUTH_TXN_STATE")
	code := os.Getenv("JELLY_OAUTH_TXN_CODE")
	var expectedUsers int
	if _, err := fmt.Sscanf(os.Getenv("JELLY_OAUTH_TXN_USER_COUNT"), "%d", &expectedUsers); err != nil {
		t.Fatal("missing expected user count")
	}
	db, err := data.Open(config.DatabaseConfig{Host: accessTestHost, Port: accessTestPort, User: accessTestUser, Password: accessTestPass, Name: dbName, Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	defer accessCloseDB(db)
	data.DB = db
	config.GlobalConfig = &config.Config{Database: config.DatabaseConfig{Host: accessTestHost, Port: accessTestPort, User: accessTestUser, Password: accessTestPass, Name: dbName, Timezone: "UTC"}}
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	if err := auth.Init(); err != nil {
		t.Fatal(err)
	}
	i18n.Init()
	goth.UseProviders(newOAuthTxnIntegrationProvider("google"))
	e := echo.New()
	RegisterRoutes(e, theme.NewManager("web/themes"), nil)
	var beforeUsers int64
	if err := db.Model(&core.User{}).Count(&beforeUsers).Error; err != nil {
		t.Fatal(err)
	}
	if int(beforeUsers) != expectedUsers {
		t.Fatalf("cross-process replay fixture users=%d, want %d", beforeUsers, expectedUsers)
	}
	b, err := os.ReadFile(cookiePath)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state="+url.QueryEscape(state), nil)
	req.URL.RawQuery += "&code=" + url.QueryEscape(code)
	req.AddCookie(&http.Cookie{Name: gothic.SessionName, Value: string(b)})
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("cross-process callback replay status=%d, want 400", rec.Code)
	}
	assertOAuthTxnNoLoginCookie(t, rec)
	provider, _ := goth.GetProvider("google")
	if p, ok := provider.(*oauthTxnIntegrationProvider); !ok || p.authorizeCount() != 0 || p.fetchCount() != 0 {
		t.Fatal("cross-process replay reached the provider")
	}
	var afterUsers int64
	if err := db.Model(&core.User{}).Count(&afterUsers).Error; err != nil {
		t.Fatal(err)
	}
	if afterUsers != beforeUsers {
		t.Fatalf("cross-process replay changed user count from %d to %d", beforeUsers, afterUsers)
	}
}

func oauthTxnConfigDatabaseName(t *testing.T) string {
	t.Helper()
	if config.GlobalConfig == nil {
		t.Fatal("fixture database config unavailable")
	}
	return config.GlobalConfig.Database.Name
}

func oauthTxnPrepareHarness(t *testing.T) *accessHarness {
	t.Helper()
	previousProviders := cloneGothProviders(goth.GetProviders())
	previousStore := gothic.Store
	t.Cleanup(func() {
		goth.ClearProviders()
		providers := make([]goth.Provider, 0, len(previousProviders))
		for _, p := range previousProviders {
			providers = append(providers, p)
		}
		goth.UseProviders(providers...)
		gothic.Store = previousStore
	})
	return newAccessHarness(t)
}

func oauthTxnServe(h *accessHarness, method, path string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	return rec
}

func oauthTxnReadAppSession(t *testing.T, cookie *http.Cookie) *sessions.Session {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	ctx := echo.New().NewContext(req, httptest.NewRecorder())
	session, err := auth.GetSession(ctx)
	if err != nil {
		t.Fatalf("read app session: %v", err)
	}
	return session
}

func oauthTxnResponseCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	return cookieFromHeader(rec.Header(), name)
}

func assertOAuthTxnNoLoginCookie(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	for _, c := range (&http.Response{Header: rec.Header()}).Cookies() {
		if c.Name == "jwt_token" || c.Name == "jelly-cms-session" {
			t.Errorf("failed OAuth transaction issued login/session cookie %q", c.Name)
		}
	}
}

func assertOAuthTxnGothCleared(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	for _, c := range (&http.Response{Header: rec.Header()}).Cookies() {
		if c.Name == gothic.SessionName && (c.MaxAge < 0 || c.Value == "") {
			return
		}
	}
	t.Error("OAuth response did not clear the short-lived Goth cookie")
}

func assertOAuthTxnNoGothClear(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	for _, c := range (&http.Response{Header: rec.Header()}).Cookies() {
		if c.Name == gothic.SessionName && (c.MaxAge < 0 || c.Value == "") {
			t.Error("pre-consumption OAuth rejection cleared pending Goth cookie")
			return
		}
	}
}

func oauthTxnSignedAuthorizedCookie(t *testing.T, p *oauthTxnIntegrationProvider, state, token string) *http.Cookie {
	t.Helper()
	session := &oauthTxnIntegrationSession{owner: p, State: state, AccessToken: token}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	if err := gothic.StoreInSession(p.Name(), session.Marshal(), req, rec); err != nil {
		t.Fatal(err)
	}
	cookie := cookieFromHeader(rec.Header(), gothic.SessionName)
	if cookie == nil {
		t.Fatal("Goth public Store did not serialize authorized session")
	}
	p.mu.Lock()
	p.tokens[token] = true
	p.mu.Unlock()
	return cookie
}

type oauthTxnFailDeleteStore struct{ sessions.Store }

func (s *oauthTxnFailDeleteStore) Get(r *http.Request, name string) (*sessions.Session, error) {
	inner, err := s.Store.Get(r, name)
	if err != nil {
		return nil, err
	}
	copy := sessions.NewSession(s, name)
	copy.ID, copy.Values, copy.IsNew = inner.ID, inner.Values, inner.IsNew
	if inner.Options != nil {
		options := *inner.Options
		copy.Options = &options
	}
	return copy, nil
}

func (s *oauthTxnFailDeleteStore) New(r *http.Request, name string) (*sessions.Session, error) {
	inner, err := s.Store.New(r, name)
	if err != nil {
		return nil, err
	}
	copy := sessions.NewSession(s, name)
	copy.ID, copy.Values, copy.IsNew = inner.ID, inner.Values, inner.IsNew
	if inner.Options != nil {
		options := *inner.Options
		copy.Options = &options
	}
	return copy, nil
}

func (s *oauthTxnFailDeleteStore) Save(r *http.Request, w http.ResponseWriter, session *sessions.Session) error {
	if session.Options != nil && session.Options.MaxAge < 0 {
		return errors.New("fixture Goth deletion failure")
	}
	return s.Store.Save(r, w, session)
}

type oauthTxnIntegrationProvider struct {
	name                    string
	mu                      sync.Mutex
	codes                   map[string]bool
	tokens                  map[string]bool
	begin, authorize, fetch int
	user                    goth.User
}

func newOAuthTxnIntegrationProvider(name string) *oauthTxnIntegrationProvider {
	return &oauthTxnIntegrationProvider{name: name, codes: map[string]bool{}, tokens: map[string]bool{}, user: goth.User{Provider: name, UserID: "subject-transaction-fixture", Email: "oauth-transaction@example.test", Name: "OAuth Transaction"}}
}
func (p *oauthTxnIntegrationProvider) Name() string { return p.name }
func (*oauthTxnIntegrationProvider) SetName(string) {}
func (p *oauthTxnIntegrationProvider) BeginAuth(state string) (goth.Session, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.begin++
	return &oauthTxnIntegrationSession{owner: p, State: state}, nil
}
func (p *oauthTxnIntegrationProvider) UnmarshalSession(raw string) (goth.Session, error) {
	var s oauthTxnIntegrationSession
	err := json.Unmarshal([]byte(raw), &s)
	s.owner = p
	return &s, err
}
func (p *oauthTxnIntegrationProvider) FetchUser(session goth.Session) (goth.User, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fetch++
	s, ok := session.(*oauthTxnIntegrationSession)
	if !ok || s.AccessToken == "" {
		return goth.User{}, errors.New("no authorized provider token")
	}
	if strings.Contains(s.AccessToken, "provider-failure") {
		return goth.User{}, errors.New("fixture provider failure")
	}
	if _, ok := p.tokens[s.AccessToken]; !ok {
		return goth.User{}, errors.New("unknown provider token")
	}
	return p.user, nil
}
func (*oauthTxnIntegrationProvider) Debug(bool)                                 {}
func (*oauthTxnIntegrationProvider) RefreshToken(string) (*oauth2.Token, error) { return nil, nil }
func (*oauthTxnIntegrationProvider) RefreshTokenAvailable() bool                { return false }
func (p *oauthTxnIntegrationProvider) consumeCode(code string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.authorize++
	if code == "" || p.codes[code] {
		return "", errors.New("authorization code already used or empty")
	}
	p.codes[code] = true
	token := "provider-token:" + code
	p.tokens[token] = true
	return token, nil
}
func (p *oauthTxnIntegrationProvider) authorizeCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.authorize
}
func (p *oauthTxnIntegrationProvider) fetchCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fetch
}

type oauthTxnIntegrationSession struct {
	owner       *oauthTxnIntegrationProvider `json:"-"`
	State       string                       `json:"state"`
	AccessToken string                       `json:"access_token,omitempty"`
}

func (s *oauthTxnIntegrationSession) GetAuthURL() (string, error) {
	return "https://provider.invalid/authorize?" + url.Values{"state": {s.State}}.Encode(), nil
}
func (s *oauthTxnIntegrationSession) Marshal() string { b, _ := json.Marshal(s); return string(b) }
func (s *oauthTxnIntegrationSession) Authorize(_ goth.Provider, params goth.Params) (string, error) {
	token, err := s.owner.consumeCode(params.Get("code"))
	s.AccessToken = token
	return token, err
}
