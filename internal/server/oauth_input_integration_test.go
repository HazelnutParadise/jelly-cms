package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"golang.org/x/oauth2"
	"gorm.io/gorm"
)

func TestOAuthInputCallbackRejectsInvalidStateBeforeProviderOrLogin(t *testing.T) {
	h := oauthInputPrepareHarness(t)
	provider := &oauthInputIntegrationProvider{name: "google", fetchErr: errors.New("unexpected FetchUser fixture")}
	resetOAuthInputProviders(provider)
	queryCalls := oauthInputCountQueries(t, h)

	cases := []struct {
		name        string
		query       string
		cookieState string
		cookie      string
		authURL     string
	}{
		{name: "missing state", query: "code=fixture-code", cookieState: "signed-state", cookie: "signed"},
		{name: "empty state", query: "state=&code=fixture-code", cookieState: "signed-state", cookie: "signed"},
		{name: "whitespace state", query: "state=+%09&code=fixture-code", cookieState: "signed-state", cookie: "signed"},
		{name: "duplicate state", query: "state=signed-state&state=signed-state&code=fixture-code", cookieState: "signed-state", cookie: "signed"},
		{name: "mismatched state", query: "state=other-state&code=fixture-code", cookieState: "signed-state", cookie: "signed"},
		{name: "bad percent encoding", query: "state=%ZZ&code=fixture-code", cookieState: "signed-state", cookie: "signed"},
		{name: "semicolon separator", query: "state=signed-state;state=signed-state&code=fixture-code", cookieState: "signed-state", cookie: "signed"},
		{name: "signed original state empty", query: "state=callback-state&code=fixture-code", cookieState: "", cookie: "signed"},
		{name: "signed original state whitespace", query: "state=callback-state&code=fixture-code", cookieState: " \t", cookie: "signed"},
		{name: "missing provider cookie", query: "state=signed-state&code=fixture-code", cookieState: "signed-state"},
		{name: "damaged cookie", query: "state=signed-state&code=fixture-code", cookie: "damaged", cookieState: "signed-state"},
		{name: "duplicate original auth URL state", query: "state=signed-state&code=fixture-code", cookie: "signed", cookieState: "signed-state", authURL: "https://provider.invalid/authorize?state=signed-state&state=attacker"},
		{name: "bad percent in original auth URL", query: "state=signed-state&code=fixture-code", cookie: "signed", cookieState: "signed-state", authURL: "https://provider.invalid/authorize?state=signed-state&bad=%ZZ"},
		{name: "semicolon in original auth URL", query: "state=signed-state&code=fixture-code", cookie: "signed", cookieState: "signed-state", authURL: "https://provider.invalid/authorize?state=signed-state;bad=value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider.resetCounts()
			provider.authURLOverride = tc.authURL
			req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?"+tc.query, nil)
			if tc.cookie == "damaged" {
				req.AddCookie(&http.Cookie{Name: gothic.SessionName, Value: "damaged"})
			} else if tc.cookie == "signed" {
				req.AddCookie(signedGothSessionCookie(t, provider, tc.cookieState))
			}
			rec := httptest.NewRecorder()
			h.echo.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("OAuth callback status=%d, want 400: %s", rec.Code, rec.Body.String())
			}
			if provider.authorizeCount() != 0 || provider.fetchCount() != 0 {
				t.Errorf("invalid state reached provider: Authorize=%d FetchUser=%d", provider.authorizeCount(), provider.fetchCount())
			}
			if got := queryCalls.Load(); got != 0 {
				t.Errorf("invalid callback executed %d database queries, want 0", got)
			}
			if cookieFromHeader(rec.Header(), "jwt_token") != nil || cookieFromHeader(rec.Header(), "jelly-cms-session") != nil {
				t.Errorf("invalid callback issued a login or app-session cookie: %v", rec.Header().Values(echo.HeaderSetCookie))
			}
			if strings.Contains(rec.Body.String(), "unexpected FetchUser fixture") {
				t.Error("invalid callback exposed provider failure text")
			}
			for _, secret := range []string{"signed-state", "callback-state", "fixture-code", "damaged"} {
				if strings.Contains(rec.Body.String(), secret) {
					t.Errorf("invalid callback response exposed input %q", secret)
				}
			}
		})
	}
}

func TestOAuthInputCallbackProviderMismatchStopsBeforeDatabase(t *testing.T) {
	h := oauthInputPrepareHarness(t)
	google := &oauthInputIntegrationProvider{name: "google", user: oauthInputUser("github")}
	github := &oauthInputIntegrationProvider{name: "github", user: oauthInputUser("github")}
	resetOAuthInputProviders(google, github)
	before := accessUsers(t, h.db)
	queryCalls := oauthInputCountQueries(t, h)

	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state=valid-state&code=fixture-code&provider=github&:provider=github", nil)
	req.AddCookie(signedGothSessionCookie(t, google, "valid-state"))
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("callback for mismatched user provider returned %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if google.authorizeCount() != 1 || google.fetchCount() == 0 || github.authorizeCount() != 0 || github.fetchCount() != 0 {
		t.Errorf("provider counts google authorize/fetch=%d/%d github=%d/%d, want one authorization, a user fetch, and no GitHub calls", google.authorizeCount(), google.fetchCount(), github.authorizeCount(), github.fetchCount())
	}
	if got := queryCalls.Load(); got != 0 {
		t.Errorf("mismatched provider callback executed %d database queries, want 0", got)
	}
	if after := accessUsers(t, h.db); !sameAccessUsers(before, after) {
		t.Errorf("mismatched provider callback changed users: before=%+v after=%+v", before, after)
	}
	assertOAuthInputNoLoginCookies(t, rec)
}

func TestOAuthInputProviderFailureIsGenericBadGateway(t *testing.T) {
	h := oauthInputPrepareHarness(t)
	const fakeSecret = "provider-secret-fixture-do-not-leak"
	provider := &oauthInputIntegrationProvider{name: "google", fetchErr: errors.New("remote error: " + fakeSecret)}
	resetOAuthInputProviders(provider)
	before := accessUsers(t, h.db)
	queryCalls := oauthInputCountQueries(t, h)

	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state=valid-state&code=fixture-code", nil)
	req.AddCookie(signedGothSessionCookie(t, provider, "valid-state"))
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("callback provider failure returned %d, want 502: %s", rec.Code, rec.Body.String())
	}
	for _, secret := range []string{fakeSecret, "valid-state", "fixture-access-token"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("provider callback response exposed secret %q", secret)
		}
	}
	if provider.authorizeCount() != 1 || provider.fetchCount() == 0 {
		t.Errorf("valid state provider calls Authorize=%d FetchUser=%d, want one authorization and a user fetch", provider.authorizeCount(), provider.fetchCount())
	}
	if got := queryCalls.Load(); got != 0 {
		t.Errorf("provider failure callback executed %d database queries, want 0", got)
	}
	if after := accessUsers(t, h.db); !sameAccessUsers(before, after) {
		t.Errorf("provider failure callback changed users: before=%+v after=%+v", before, after)
	}
	assertOAuthInputNoLoginCookies(t, rec)
}

func TestOAuthInputValidCallbackKeepsJSONLoginAndInSiteRedirect(t *testing.T) {
	h := oauthInputPrepareHarness(t)
	provider := &oauthInputIntegrationProvider{name: "google", user: oauthInputUser("google")}
	resetOAuthInputProviders(provider)
	path := "/auth/google/callback?state=valid-state&code=fixture-code"
	request := func(accept string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set(echo.HeaderAccept, accept)
		req.AddCookie(signedGothSessionCookie(t, provider, "valid-state"))
		req.AddCookie(oauthInputSignedAppCookie(t, map[string]any{"oauth_redirect": "/admin/posts?tab=all"}))
		rec := httptest.NewRecorder()
		h.echo.ServeHTTP(rec, req)
		return rec
	}
	jsonResponse := request(echo.MIMEApplicationJSON)
	if jsonResponse.Code != http.StatusOK {
		t.Fatalf("valid JSON OAuth callback returned %d, want 200: %s", jsonResponse.Code, jsonResponse.Body.String())
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(jsonResponse.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode OAuth JSON response: %v", err)
	}
	if len(payload) != 3 || payload["message"] == nil || payload["user"] == nil || payload["token"] == nil {
		t.Fatalf("OAuth JSON keys=%v, want exactly message, user, token", sortedRawKeys(payload))
	}
	var message string
	if err := json.Unmarshal(payload["message"], &message); err != nil || message != "Login successful" {
		t.Errorf("OAuth JSON message=%q, want Login successful (decode error: %v)", message, err)
	}
	var token string
	if err := json.Unmarshal(payload["token"], &token); err != nil || token == "" {
		t.Fatalf("OAuth response token missing or invalid: decode error=%v", err)
	}
	var responseUser struct {
		ID    uint   `json:"id"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(payload["user"], &responseUser); err != nil {
		t.Fatalf("decode OAuth response user: %v", err)
	}
	me := h.request(http.MethodGet, "/api/auth/me", "", token, echo.MIMEApplicationJSON, "")
	if me.code != http.StatusOK {
		t.Fatalf("GET /api/auth/me with OAuth token returned %d, want 200: %s", me.code, me.body)
	}
	var meUser struct {
		ID    uint   `json:"id"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal([]byte(me.body), &meUser); err != nil {
		t.Fatalf("decode authenticated OAuth user: %v", err)
	}
	if meUser.ID != responseUser.ID || meUser.Email != responseUser.Email || meUser.Email != "oauth-input@example.test" {
		t.Errorf("OAuth token identifies user (%d, %q), response user is (%d, %q)", meUser.ID, meUser.Email, responseUser.ID, responseUser.Email)
	}
	redirect := request("text/html")
	if redirect.Code != http.StatusFound || redirect.Header().Get(echo.HeaderLocation) != "/admin/posts?tab=all" {
		t.Fatalf("valid HTML OAuth callback status/location=%d/%q, want 302 /admin/posts?tab=all", redirect.Code, redirect.Header().Get(echo.HeaderLocation))
	}
	if provider.authorizeCount() != 2 || provider.fetchCount() < 2 {
		t.Errorf("positive OAuth cases Authorize=%d FetchUser=%d, want two authorizations and at least one fetch per callback", provider.authorizeCount(), provider.fetchCount())
	}
}

func TestOAuthInputEntrySessionFailuresStopAuthorization(t *testing.T) {
	h := oauthInputPrepareHarness(t)
	provider := &oauthInputIntegrationProvider{name: "google"}
	resetOAuthInputProviders(provider)
	for _, tc := range []struct {
		name   string
		cookie *http.Cookie
	}{
		{name: "damaged app session", cookie: &http.Cookie{Name: "jelly-cms-session", Value: "damaged"}},
		{name: "session cookie save limit", cookie: oauthInputNearLimitAppCookie(t)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider.resetCounts()
			req := httptest.NewRequest(http.MethodGet, "/auth/google?redirect=%2Fadmin%2Fposts%3Ftab%3Dall", nil)
			req.AddCookie(tc.cookie)
			rec := httptest.NewRecorder()
			h.echo.ServeHTTP(rec, req)
			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("OAuth entry with %s returned %d, want 503: %s", tc.name, rec.Code, rec.Body.String())
			}
			if rec.Header().Get(echo.HeaderLocation) != "" || provider.beginCount() != 0 {
				t.Errorf("OAuth entry with %s continued authorization: Location=%q BeginAuth=%d", tc.name, rec.Header().Get(echo.HeaderLocation), provider.beginCount())
			}
			if cookieFromHeader(rec.Header(), "jelly-cms-session") != nil {
				t.Error("OAuth entry failure issued an app-session cookie")
			}
		})
	}
}

func TestOAuthInputEntryRejectsUnknownAndUnregisteredProviders(t *testing.T) {
	h := oauthInputPrepareHarness(t)
	google := &oauthInputIntegrationProvider{name: "google"}
	facebook := &oauthInputIntegrationProvider{name: "facebook"}
	resetOAuthInputProviders(google, facebook)
	for _, path := range []string{"/auth/facebook", "/auth/github"} {
		google.resetCounts()
		facebook.resetCounts()
		rec := requestOAuthInput(h.echo, path)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s returned %d, want 400: %s", path, rec.Code, rec.Body.String())
		}
		if google.beginCount() != 0 || rec.Header().Get(echo.HeaderLocation) != "" {
			t.Errorf("GET %s started authorization: BeginAuth=%d Location=%q", path, google.beginCount(), rec.Header().Get(echo.HeaderLocation))
		}
		if facebook.beginCount() != 0 {
			t.Errorf("GET %s started Facebook authorization: BeginAuth=%d", path, facebook.beginCount())
		}
	}
}

func TestOAuthInputGitHubEntryAndCallbackUsePathProvider(t *testing.T) {
	h := oauthInputPrepareHarness(t)
	google := &oauthInputIntegrationProvider{name: "google", user: oauthInputUser("google")}
	github := &oauthInputIntegrationProvider{name: "github", user: oauthInputUser("github")}
	resetOAuthInputProviders(google, github)
	entryReq := httptest.NewRequest(http.MethodGet, "/auth/github?provider=google&:provider=google&state=attacker", nil)
	entry := httptest.NewRecorder()
	h.echo.ServeHTTP(entry, entryReq)
	if entry.Code != http.StatusTemporaryRedirect {
		t.Fatalf("GitHub entry status=%d, want 307: %s", entry.Code, entry.Body.String())
	}
	if github.beginCount() != 1 || google.beginCount() != 0 {
		t.Fatalf("GitHub entry BeginAuth github=%d google=%d, want 1 and 0", github.beginCount(), google.beginCount())
	}
	location, err := url.Parse(entry.Header().Get(echo.HeaderLocation))
	if err != nil {
		t.Fatalf("parse GitHub authorization URL: %v", err)
	}
	state := location.Query().Get("state")
	if state == "" || state == "attacker" || location.Query().Get("provider") != "github" {
		t.Fatalf("GitHub redirect has unsafe state/provider: %q", location.String())
	}
	callbackReq := httptest.NewRequest(http.MethodGet, "/auth/github/callback?state="+url.QueryEscape(state)+"&code=fixture-code&provider=google&:provider=google", nil)
	for _, cookie := range entry.Result().Cookies() {
		callbackReq.AddCookie(cookie)
	}
	callbackReq.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
	callback := httptest.NewRecorder()
	h.echo.ServeHTTP(callback, callbackReq)
	if callback.Code != http.StatusOK {
		t.Fatalf("GitHub callback status=%d, want 200: %s", callback.Code, callback.Body.String())
	}
	if github.authorizeCount() != 1 || github.fetchCount() == 0 || google.authorizeCount() != 0 || google.fetchCount() != 0 {
		t.Errorf("GitHub callback authorize/fetch github=%d/%d google=%d/%d", github.authorizeCount(), github.fetchCount(), google.authorizeCount(), google.fetchCount())
	}
}

func TestOAuthInputUnsupportedAndUnregisteredCallbackRoutesRejectBeforeProviderOrDatabase(t *testing.T) {
	h := oauthInputPrepareHarness(t)
	google := &oauthInputIntegrationProvider{name: "google", fetchErr: errors.New("unexpected FetchUser fixture")}
	facebook := &oauthInputIntegrationProvider{name: "facebook"}
	resetOAuthInputProviders(google, facebook)
	queries := oauthInputCountQueries(t, h)
	for _, path := range []string{"/auth/facebook/callback", "/auth/github/callback"} {
		t.Run(path, func(t *testing.T) {
			google.resetCounts()
			facebook.resetCounts()
			queries.Store(0)
			req := httptest.NewRequest(http.MethodGet, path+"?state=valid-state&code=fixture-code&provider=google", nil)
			req.AddCookie(signedGothSessionCookie(t, google, "valid-state"))
			rec := httptest.NewRecorder()
			h.echo.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("callback %s returned %d, want 400: %s", path, rec.Code, rec.Body.String())
			}
			if google.authorizeCount() != 0 || google.fetchCount() != 0 || facebook.authorizeCount() != 0 || facebook.fetchCount() != 0 {
				t.Errorf("rejected callback called providers google=%d/%d facebook=%d/%d", google.authorizeCount(), google.fetchCount(), facebook.authorizeCount(), facebook.fetchCount())
			}
			if queries.Load() != 0 {
				t.Errorf("rejected callback executed %d user queries", queries.Load())
			}
			assertOAuthInputNoLoginCookies(t, rec)
		})
	}
}

func TestOAuthInputEntryDoesNotExtendExistingLoginExpiry(t *testing.T) {
	h := oauthInputPrepareHarness(t)
	provider := &oauthInputIntegrationProvider{name: "google"}
	resetOAuthInputProviders(provider)
	expiry := time.Now().Add(time.Hour).Unix()
	cookie := oauthInputSignedAppCookie(t, map[string]any{"user_id": uint(41), "login_id": "0123456789abcdef0123456789abcdef", "login_expires_at": expiry})
	req := httptest.NewRequest(http.MethodGet, "/auth/google?redirect=%2Fadmin%2Fposts", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("OAuth entry with existing login returned %d, want 307: %s", rec.Code, rec.Body.String())
	}
	updated := cookieFromHeader(rec.Header(), "jelly-cms-session")
	if updated == nil {
		t.Fatal("OAuth entry did not save updated app session cookie")
	}
	check := httptest.NewRequest(http.MethodGet, "/", nil)
	check.AddCookie(updated)
	session, err := gothic.Store.Get(check, "jelly-cms-session")
	if err != nil {
		t.Fatalf("decode saved app session: %v", err)
	}
	if got, ok := session.Values["login_expires_at"].(int64); !ok || got != expiry {
		t.Errorf("OAuth entry changed login_expires_at to %#v, want %d", session.Values["login_expires_at"], expiry)
	}
}

func oauthInputUser(provider string) goth.User {
	return goth.User{UserID: "subject-fixture", Email: "oauth-input@example.test", Name: "OAuth Input", NickName: "oauth-input", Provider: provider}
}

func oauthInputPrepareHarness(t *testing.T) *accessHarness {
	t.Helper()
	previousProviders := cloneGothProviders(goth.GetProviders())
	previousStore := gothic.Store
	t.Cleanup(func() {
		goth.ClearProviders()
		providers := make([]goth.Provider, 0, len(previousProviders))
		for _, provider := range previousProviders {
			providers = append(providers, provider)
		}
		goth.UseProviders(providers...)
		gothic.Store = previousStore
	})
	return newAccessHarness(t)
}

func resetOAuthInputProviders(providers ...goth.Provider) {
	goth.ClearProviders()
	goth.UseProviders(providers...)
}

func requestOAuthInput(e *echo.Echo, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func assertOAuthInputNoLoginCookies(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if cookieFromHeader(rec.Header(), "jwt_token") != nil || cookieFromHeader(rec.Header(), "jelly-cms-session") != nil {
		t.Errorf("OAuth failure issued login or app-session cookie: %v", rec.Header().Values(echo.HeaderSetCookie))
	}
}

func sameAccessUsers(a, b []core.User) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Username != b[i].Username || a[i].Email != b[i].Email || a[i].Provider != b[i].Provider || a[i].Role != b[i].Role {
			return false
		}
	}
	return true
}

func sortedRawKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func oauthInputCountQueries(t *testing.T, h *accessHarness) *atomic.Int64 {
	t.Helper()
	var count atomic.Int64
	name := fmt.Sprintf("oauth_input_query_count_%d", accessTestDBCounter.Add(1))
	if err := h.db.Callback().Query().Before("gorm:query").Register(name, func(db *gorm.DB) {
		if db.Statement.Table == "users" || strings.Contains(strings.ToLower(db.Statement.SQL.String()), "users") {
			count.Add(1)
		}
	}); err != nil {
		t.Fatalf("register isolated database query counter: %v", err)
	}
	t.Cleanup(func() {
		if err := h.db.Callback().Query().Remove(name); err != nil {
			t.Errorf("remove isolated database query counter: %v", err)
		}
	})
	return &count
}

func oauthInputSignedAppCookie(t *testing.T, values map[string]any) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	session, err := gothic.Store.New(req, "jelly-cms-session")
	if err != nil {
		t.Fatalf("create app session: %v", err)
	}
	for key, value := range values {
		session.Values[key] = value
	}
	if err := session.Save(req, rec); err != nil {
		t.Fatalf("save app session fixture: %v", err)
	}
	cookie := cookieFromHeader(rec.Header(), "jelly-cms-session")
	if cookie == nil {
		t.Fatal("saved app session fixture did not produce cookie")
	}
	return cookie
}

func oauthInputNearLimitAppCookie(t *testing.T) *http.Cookie {
	t.Helper()
	e := echo.New()
	baseReq := httptest.NewRequest(http.MethodGet, "/", nil)
	baseRec := httptest.NewRecorder()
	if err := auth.SetUserSession(e.NewContext(baseReq, baseRec), 43); err != nil {
		t.Fatalf("create base app session through public SetUserSession: %v", err)
	}
	baseCookie := cookieFromHeader(baseRec.Header(), "jelly-cms-session")
	if baseCookie == nil {
		t.Fatal("SetUserSession did not create app-session cookie")
	}
	var best *http.Cookie
	for length := 0; length < 4096; length++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(baseCookie)
		rec := httptest.NewRecorder()
		session, err := auth.GetSession(e.NewContext(req, rec))
		if err != nil {
			t.Fatalf("read app session through public GetSession: %v", err)
		}
		session.Values["padding"] = strings.Repeat("x", length)
		if err := session.Save(req, rec); err != nil {
			break
		}
		cookie := cookieFromHeader(rec.Header(), "jelly-cms-session")
		if cookie != nil {
			checkReq := httptest.NewRequest(http.MethodGet, "/", nil)
			checkReq.AddCookie(cookie)
			checkRec := httptest.NewRecorder()
			checkSession, readErr := auth.GetSession(e.NewContext(checkReq, checkRec))
			if readErr != nil {
				continue
			}
			checkSession.Values["oauth_redirect"] = "/admin/posts?tab=all"
			if saveErr := checkSession.Save(checkReq, checkRec); saveErr != nil {
				best = cookie
				break
			}
		}
	}
	if best == nil {
		t.Fatal("could not create a readable app-session cookie whose added OAuth redirect exceeds the store's size limit")
	}
	return best
}

type oauthInputIntegrationProvider struct {
	name            string
	mu              sync.Mutex
	states          []string
	authorize       int
	fetch           int
	user            goth.User
	fetchErr        error
	authURLOverride string
}

func (p *oauthInputIntegrationProvider) Name() string { return p.name }
func (*oauthInputIntegrationProvider) SetName(string) {}
func (p *oauthInputIntegrationProvider) BeginAuth(state string) (goth.Session, error) {
	p.mu.Lock()
	p.states = append(p.states, state)
	p.mu.Unlock()
	return &oauthInputIntegrationSession{owner: p, State: state, Provider: p.name}, nil
}
func (p *oauthInputIntegrationProvider) UnmarshalSession(raw string) (goth.Session, error) {
	var session oauthInputIntegrationSession
	err := json.Unmarshal([]byte(raw), &session)
	session.owner = p
	return &session, err
}
func (p *oauthInputIntegrationProvider) FetchUser(session goth.Session) (goth.User, error) {
	return p.fetchUser(session)
}
func (p *oauthInputIntegrationProvider) fetchUser(session goth.Session) (goth.User, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fetch++
	state, ok := session.(*oauthInputIntegrationSession)
	if !ok || !state.authorized {
		return goth.User{}, errors.New("provider session needs authorization")
	}
	if p.fetchErr != nil {
		return goth.User{}, p.fetchErr
	}
	return p.user, nil
}
func (*oauthInputIntegrationProvider) Debug(bool)                                 {}
func (*oauthInputIntegrationProvider) RefreshToken(string) (*oauth2.Token, error) { return nil, nil }
func (*oauthInputIntegrationProvider) RefreshTokenAvailable() bool                { return false }
func (p *oauthInputIntegrationProvider) resetCounts() {
	p.mu.Lock()
	p.states = nil
	p.authorize = 0
	p.fetch = 0
	p.mu.Unlock()
}
func (p *oauthInputIntegrationProvider) beginCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.states)
}
func (p *oauthInputIntegrationProvider) authorizeCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.authorize
}
func (p *oauthInputIntegrationProvider) fetchCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fetch
}

type oauthInputIntegrationSession struct {
	owner      *oauthInputIntegrationProvider `json:"-"`
	State      string                         `json:"state"`
	Provider   string                         `json:"provider"`
	authorized bool                           `json:"-"`
}

func (s oauthInputIntegrationSession) GetAuthURL() (string, error) {
	if s.owner.authURLOverride != "" {
		return s.owner.authURLOverride, nil
	}
	return "https://provider.invalid/authorize?" + url.Values{"state": {s.State}, "provider": {s.Provider}}.Encode(), nil
}
func (s oauthInputIntegrationSession) Marshal() string {
	encoded, _ := json.Marshal(s)
	return string(encoded)
}
func (s *oauthInputIntegrationSession) Authorize(_ goth.Provider, params goth.Params) (string, error) {
	if strings.TrimSpace(params.Get("code")) == "" {
		return "", errors.New("authorization code required")
	}
	s.owner.mu.Lock()
	s.owner.authorize++
	s.owner.mu.Unlock()
	s.authorized = true
	return "fixture-access-token", nil
}
