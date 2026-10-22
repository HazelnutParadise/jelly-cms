package auth_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"golang.org/x/oauth2"
)

func TestOAuthTransactionEntryExpiresAfterTenMinutesWithoutDatabase(t *testing.T) {
	previousDB := data.DB
	previousProviders := goth.GetProviders()
	previousStore := gothic.Store
	t.Setenv("SESSION_SECRET", "oauth-transaction-session-secret-0123456789")
	t.Setenv("JWT_SECRET", "oauth-transaction-jwt-secret-0123456789")
	t.Setenv("APP_ENV", "test")
	data.DB = nil
	t.Cleanup(func() {
		data.DB = previousDB
		goth.ClearProviders()
		providers := make([]goth.Provider, 0, len(previousProviders))
		for _, p := range previousProviders {
			providers = append(providers, p)
		}
		goth.UseProviders(providers...)
		gothic.Store = previousStore
	})
	if err := auth.Init(); err != nil {
		t.Fatal(err)
	}
	p := &oauthTxnProvider{name: "google"}
	goth.UseProviders(p)
	e := echo.New()
	auth.RegisterRoutes(e)
	synctest.Test(t, func(t *testing.T) {
		entry := httptest.NewRecorder()
		e.ServeHTTP(entry, httptest.NewRequest(http.MethodGet, "/auth/google", nil))
		if entry.Code != http.StatusTemporaryRedirect {
			t.Fatalf("entry status=%d, want 307", entry.Code)
		}
		oauthCookie := oauthTxnCookie(entry, gothic.SessionName)
		if oauthCookie == nil {
			t.Fatal("OAuth entry did not set its Goth cookie")
		}
		if oauthCookie.MaxAge != 600 || !oauthCookie.HttpOnly || oauthCookie.SameSite != http.SameSiteLaxMode || oauthCookie.Path != "/" {
			t.Errorf("OAuth entry cookie attributes: MaxAge=%d HttpOnly=%v SameSite=%v Path=%q", oauthCookie.MaxAge, oauthCookie.HttpOnly, oauthCookie.SameSite, oauthCookie.Path)
		}
		if oauthCookie.Secure != (os.Getenv("APP_ENV") == "production") {
			t.Errorf("OAuth cookie Secure=%v does not follow APP_ENV", oauthCookie.Secure)
		}
		if p.begin != 1 {
			t.Fatalf("provider BeginAuth calls=%d, want 1", p.begin)
		}
		time.Sleep(601 * time.Second)
		req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state="+url.QueryEscape(p.state)+"&code=code-fixture", nil)
		req.AddCookie(oauthCookie)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expired OAuth callback status=%d, want 400", rec.Code)
		}
		for _, c := range (&http.Response{Header: rec.Header()}).Cookies() {
			if c.Name == "jwt_token" || c.Name == "jelly-cms-session" {
				t.Errorf("expired OAuth callback issued login cookie %q", c.Name)
			}
		}
		if p.authorize != 0 || p.fetch != 0 {
			t.Errorf("expired state reached provider: Authorize=%d FetchUser=%d", p.authorize, p.fetch)
		}
	})
}

func TestOAuthTransactionStartingAnotherProviderDoesNotRenewOlderState(t *testing.T) {
	previousDB := data.DB
	previousProviders := goth.GetProviders()
	previousStore := gothic.Store
	t.Setenv("SESSION_SECRET", "oauth-transaction-renewal-session-secret-0123")
	t.Setenv("JWT_SECRET", "oauth-transaction-renewal-jwt-secret-0123456")
	t.Setenv("APP_ENV", "test")
	data.DB = nil
	t.Cleanup(func() {
		data.DB = previousDB
		goth.ClearProviders()
		providers := make([]goth.Provider, 0, len(previousProviders))
		for _, p := range previousProviders {
			providers = append(providers, p)
		}
		goth.UseProviders(providers...)
		gothic.Store = previousStore
	})
	if err := auth.Init(); err != nil {
		t.Fatal(err)
	}
	e := echo.New()
	auth.RegisterRoutes(e)

	synctest.Test(t, func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			firstName  string
			secondName string
		}{
			{name: "Google then GitHub", firstName: "google", secondName: "github"},
			{name: "GitHub then Google", firstName: "github", secondName: "google"},
		} {
			firstProvider := &oauthTxnProvider{name: tc.firstName}
			secondProvider := &oauthTxnProvider{name: tc.secondName}
			goth.UseProviders(firstProvider, secondProvider)
			enter := func(provider string, cookies ...*http.Cookie) (string, *http.Cookie, *http.Cookie) {
				req := httptest.NewRequest(http.MethodGet, "/auth/"+provider, nil)
				for _, cookie := range cookies {
					req.AddCookie(cookie)
				}
				rec := httptest.NewRecorder()
				e.ServeHTTP(rec, req)
				if rec.Code != http.StatusTemporaryRedirect {
					t.Fatalf("%s OAuth entry returned %d, want 307", provider, rec.Code)
				}
				location, err := url.Parse(rec.Header().Get(echo.HeaderLocation))
				if err != nil {
					t.Fatalf("parse %s provider redirect: %v", provider, err)
				}
				state := location.Query().Get("state")
				gothCookie := oauthTxnCookie(rec, gothic.SessionName)
				appCookie := oauthTxnCookie(rec, "jelly-cms-session")
				if state == "" || gothCookie == nil || appCookie == nil {
					t.Fatalf("%s OAuth entry omitted state or a session cookie", provider)
				}
				return state, gothCookie, appCookie
			}
			callback := func(provider, state string, gothCookie, appCookie *http.Cookie) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodGet, "/auth/"+provider+"/callback?state="+url.QueryEscape(state)+"&code=valid-code-fixture", nil)
				req.AddCookie(gothCookie)
				req.AddCookie(appCookie)
				rec := httptest.NewRecorder()
				e.ServeHTTP(rec, req)
				return rec
			}

			firstState, firstGothCookie, firstAppCookie := enter(tc.firstName)
			time.Sleep(590 * time.Second)
			secondState, secondGothCookie, secondAppCookie := enter(tc.secondName, firstGothCookie, firstAppCookie)
			time.Sleep(11 * time.Second)

			// Confirm the new provider state remains valid after its entry. The old
			// callback must fail because its own authorization age expired.
			checkReq := httptest.NewRequest(http.MethodGet, "/", nil)
			checkReq.AddCookie(secondGothCookie)
			serialized, err := gothic.GetFromSession(tc.secondName, checkReq)
			if err != nil {
				t.Fatalf("new %s session is unavailable after entry: %v", tc.secondName, err)
			}
			providerSession, err := goth.GetProvider(tc.secondName)
			if err != nil {
				t.Fatalf("get %s provider: %v", tc.secondName, err)
			}
			decoded, err := providerSession.UnmarshalSession(serialized)
			if err != nil {
				t.Fatalf("decode %s session: %v", tc.secondName, err)
			}
			if storedState := decoded.(*oauthTxnSession).State; storedState != secondState {
				t.Errorf("new %s session state changed after entry", tc.secondName)
			}

			if rec := callback(tc.firstName, firstState, secondGothCookie, secondAppCookie); rec.Code != http.StatusBadRequest {
				t.Errorf("%s callback using renewed %s cookie returned %d, want expired-state 400", tc.firstName, tc.secondName, rec.Code)
			}
			if rec := callback(tc.firstName, firstState, firstGothCookie, firstAppCookie); rec.Code != http.StatusBadRequest {
				t.Errorf("%s callback using original cookie after 601 seconds returned %d, want 400", tc.firstName, rec.Code)
			}
			if rec := callback(tc.secondName, secondState, secondGothCookie, secondAppCookie); rec.Code != http.StatusServiceUnavailable {
				t.Errorf("fresh %s callback with unavailable database returned %d, want 503 after state validation", tc.secondName, rec.Code)
			}
			if firstProvider.authorize != 0 || firstProvider.fetch != 0 || secondProvider.authorize != 0 || secondProvider.fetch != 0 {
				t.Errorf("expired-state fixture reached a provider: first Authorize/Fetch=%d/%d second=%d/%d", firstProvider.authorize, firstProvider.fetch, secondProvider.authorize, secondProvider.fetch)
			}
		}
	})
}

func TestOAuthTransactionEntryUsesIndependentThirtyPerMinuteLimit(t *testing.T) {
	previousDB := data.DB
	previousProviders := goth.GetProviders()
	previousStore := gothic.Store
	t.Setenv("SESSION_SECRET", "oauth-transaction-limit-session-secret-123")
	t.Setenv("JWT_SECRET", "oauth-transaction-limit-jwt-secret-123456")
	t.Setenv("APP_ENV", "test")
	data.DB = nil
	t.Cleanup(func() {
		data.DB = previousDB
		goth.ClearProviders()
		providers := make([]goth.Provider, 0, len(previousProviders))
		for _, p := range previousProviders {
			providers = append(providers, p)
		}
		goth.UseProviders(providers...)
		gothic.Store = previousStore
	})
	if err := auth.Init(); err != nil {
		t.Fatal(err)
	}
	p := &oauthTxnProvider{name: "google"}
	github := &oauthTxnProvider{name: "github"}
	goth.UseProviders(p, github)
	e := echo.New()
	auth.RegisterRoutes(e)
	synctest.Test(t, func(t *testing.T) {
		for i := 0; i < 30; i++ {
			path := "/auth/google"
			if i%2 == 1 {
				path = "/auth/github"
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusTemporaryRedirect {
				t.Fatalf("entry %d status=%d, want 307", i+1, rec.Code)
			}
		}
		limited := httptest.NewRecorder()
		e.ServeHTTP(limited, httptest.NewRequest(http.MethodGet, "/auth/google", nil))
		if limited.Code != http.StatusTooManyRequests {
			t.Errorf("31st OAuth entry status=%d, want 429", limited.Code)
		}
		if p.begin+github.begin != 30 {
			t.Errorf("combined provider BeginAuth calls=%d, want 30", p.begin+github.begin)
		}
		retryAfter, retryErr := strconv.Atoi(limited.Header().Get("Retry-After"))
		if retryErr != nil || retryAfter <= 0 {
			t.Errorf("OAuth rate rejection Retry-After=%q, want a positive integer", limited.Header().Get("Retry-After"))
		}
		local := httptest.NewRecorder()
		localReq := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader("{"))
		localReq.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		localReq.Header.Set("X-Jelly-CSRF", "1")
		e.ServeHTTP(local, localReq)
		if local.Code != http.StatusBadRequest {
			t.Errorf("malformed local login after OAuth exhaustion returned %d, want 400 (independent local allowance)", local.Code)
		}
		time.Sleep(60 * time.Second)
		recovered := httptest.NewRecorder()
		e.ServeHTTP(recovered, httptest.NewRequest(http.MethodGet, "/auth/github", nil))
		if recovered.Code != http.StatusTemporaryRedirect {
			t.Errorf("OAuth entry after its 60-second window returned %d, want 307", recovered.Code)
		}
		if p.begin+github.begin != 31 {
			t.Errorf("provider BeginAuth after window=%d, want 31", p.begin+github.begin)
		}
	})
}

func oauthTxnCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range (&http.Response{Header: rec.Header()}).Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

type oauthTxnProvider struct {
	mu                      sync.Mutex
	name, state             string
	begin, authorize, fetch int
}

func (p *oauthTxnProvider) Name() string { return p.name }
func (*oauthTxnProvider) SetName(string) {}
func (p *oauthTxnProvider) BeginAuth(state string) (goth.Session, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.begin++
	p.state = state
	return &oauthTxnSession{owner: p, State: state}, nil
}
func (p *oauthTxnProvider) UnmarshalSession(raw string) (goth.Session, error) {
	var s oauthTxnSession
	err := json.Unmarshal([]byte(raw), &s)
	s.owner = p
	return &s, err
}
func (p *oauthTxnProvider) FetchUser(goth.Session) (goth.User, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fetch++
	return goth.User{}, errors.New("unexpected FetchUser in expired-state fixture")
}
func (*oauthTxnProvider) Debug(bool)                                 {}
func (*oauthTxnProvider) RefreshToken(string) (*oauth2.Token, error) { return nil, nil }
func (*oauthTxnProvider) RefreshTokenAvailable() bool                { return false }

type oauthTxnSession struct {
	owner *oauthTxnProvider `json:"-"`
	State string            `json:"state"`
}

func (s *oauthTxnSession) GetAuthURL() (string, error) {
	return "https://provider.invalid/authorize?" + url.Values{"state": {s.State}}.Encode(), nil
}
func (s *oauthTxnSession) Marshal() string { b, _ := json.Marshal(s); return string(b) }
func (s *oauthTxnSession) Authorize(provider goth.Provider, params goth.Params) (string, error) {
	if p, ok := provider.(*oauthTxnProvider); ok {
		p.mu.Lock()
		p.authorize++
		p.mu.Unlock()
	}
	return params.Get("code"), nil
}
