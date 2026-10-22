package auth_test

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"golang.org/x/oauth2"
)

func TestOAuthInputEntryUsesPathProviderAndFreshUntrustedState(t *testing.T) {
	oauthInputSetup(t)
	google := &oauthInputProvider{name: "google"}
	github := &oauthInputProvider{name: "github"}
	goth.UseProviders(google, github)
	e := echo.New()
	auth.RegisterRoutes(e)

	first := oauthInputRequest(e, "/auth/google?provider=github&:provider=github&provider=github&state=attacker&state=chosen")
	if first.Code != http.StatusTemporaryRedirect {
		t.Fatalf("GET /auth/google returned %d, want provider redirect %d: %s", first.Code, http.StatusTemporaryRedirect, first.Body.String())
	}
	second := oauthInputRequest(e, "/auth/google?provider=github&:provider=github&state=attacker")
	if second.Code != http.StatusTemporaryRedirect {
		t.Fatalf("second GET /auth/google returned %d, want provider redirect %d: %s", second.Code, http.StatusTemporaryRedirect, second.Body.String())
	}
	if google.beginCount() != 2 || github.beginCount() != 0 {
		t.Errorf("BeginAuth calls google=%d github=%d, want 2 and 0", google.beginCount(), github.beginCount())
	}
	selected := google
	if selected.beginCount() == 0 {
		selected = github
	}
	states := selected.states()
	if len(states) != 2 {
		t.Fatalf("captured %d authorization states, want 2", len(states))
	}
	for i, state := range states {
		decoded, err := base64.RawURLEncoding.DecodeString(state)
		if err != nil {
			decoded, err = base64.URLEncoding.DecodeString(state)
		}
		if err != nil || len(decoded) < 32 {
			t.Errorf("state %d is not at least 32 bytes of raw URL base64: decoded length=%d err=%v", i+1, len(decoded), err)
		}
		if state == "attacker" || state == "chosen" || state == "github" {
			t.Errorf("state %d reused caller-controlled input %q", i+1, state)
		}
	}
	if states[0] == states[1] {
		t.Errorf("two OAuth entries reused state %q", states[0])
	}
	for i, rec := range []*httptest.ResponseRecorder{first, second} {
		location, err := url.Parse(rec.Header().Get(echo.HeaderLocation))
		if err != nil {
			t.Fatalf("authorization redirect %d URL %q: %v", i+1, rec.Header().Get(echo.HeaderLocation), err)
		}
		if got := location.Query().Get("state"); got != states[i] {
			t.Errorf("authorization redirect %d state=%q, want provider-captured state %q", i+1, got, states[i])
		}
		if got := location.Query().Get("provider"); got != "google" {
			t.Errorf("authorization redirect %d provider=%q, want path provider google", i+1, got)
		}
	}
}

func TestOAuthInputEntryRandomFailureReturnsUnavailableWithoutRedirect(t *testing.T) {
	oauthInputSetup(t)
	provider := &oauthInputProvider{name: "google"}
	goth.UseProviders(provider)
	e := echo.New()
	auth.RegisterRoutes(e)

	oldReader := rand.Reader
	rand.Reader = oauthInputBrokenReader{}
	t.Cleanup(func() { rand.Reader = oldReader })
	rec := oauthInputRequest(e, "/auth/google?state=untrusted")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /auth/google with failed randomness returned %d, want 503: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get(echo.HeaderLocation) != "" || provider.beginCount() != 0 {
		t.Errorf("random failure continued authorization: Location=%q BeginAuth calls=%d", rec.Header().Get(echo.HeaderLocation), provider.beginCount())
	}
	if strings.Contains(rec.Body.String(), "fixture random source unavailable") {
		t.Error("randomness failure response exposed the underlying error")
	}
}

func TestOAuthInputGitHubEntryCannotBeOverriddenByProviderQuery(t *testing.T) {
	oauthInputSetup(t)
	google := &oauthInputProvider{name: "google"}
	github := &oauthInputProvider{name: "github"}
	goth.UseProviders(google, github)
	e := echo.New()
	auth.RegisterRoutes(e)

	rec := oauthInputRequest(e, "/auth/github?provider=google&:provider=google&state=attacker")
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("GET /auth/github returned %d, want provider redirect %d: %s", rec.Code, http.StatusTemporaryRedirect, rec.Body.String())
	}
	if github.beginCount() != 1 || google.beginCount() != 0 {
		t.Errorf("GitHub-path entry BeginAuth calls github=%d google=%d, want 1 and 0", github.beginCount(), google.beginCount())
	}
	location, err := url.Parse(rec.Header().Get(echo.HeaderLocation))
	if err != nil {
		t.Fatalf("GitHub authorization redirect URL %q: %v", rec.Header().Get(echo.HeaderLocation), err)
	}
	if got := location.Query().Get("provider"); got != "github" {
		t.Errorf("GitHub authorization provider=%q, want github", got)
	}
	state := location.Query().Get("state")
	decoded, decodeErr := base64.RawURLEncoding.DecodeString(state)
	if decodeErr != nil {
		decoded, decodeErr = base64.URLEncoding.DecodeString(state)
	}
	if decodeErr != nil || len(decoded) < 32 || state == "attacker" {
		t.Errorf("GitHub authorization state is caller-controlled or too short: decoded length=%d err=%v", len(decoded), decodeErr)
	}
}

func oauthInputSetup(t *testing.T) {
	t.Helper()
	previousProviders := make(map[string]goth.Provider)
	for name, provider := range goth.GetProviders() {
		previousProviders[name] = provider
	}
	previousStore := gothic.Store
	previousDB := data.DB
	data.DB = nil
	t.Setenv("SESSION_SECRET", "oauth-input-session-secret-fixture-32-bytes")
	t.Setenv("JWT_SECRET", "oauth-input-jwt-secret-fixture-32-bytes")
	t.Cleanup(func() {
		data.DB = previousDB
		goth.ClearProviders()
		for _, provider := range previousProviders {
			goth.UseProviders(provider)
		}
		gothic.Store = previousStore
	})
	if err := auth.Init(); err != nil {
		t.Fatalf("initialize OAuth test session store: %v", err)
	}
}

func oauthInputRequest(e *echo.Echo, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

type oauthInputBrokenReader struct{}

func (oauthInputBrokenReader) Read([]byte) (int, error) {
	return 0, errors.New("fixture random source unavailable")
}

type oauthInputProvider struct {
	name string
	mu   sync.Mutex
	seen []string
}

func (p *oauthInputProvider) Name() string   { return p.name }
func (p *oauthInputProvider) SetName(string) {}
func (p *oauthInputProvider) BeginAuth(state string) (goth.Session, error) {
	p.mu.Lock()
	p.seen = append(p.seen, state)
	p.mu.Unlock()
	return oauthInputSession{State: state, Provider: p.name}, nil
}
func (*oauthInputProvider) UnmarshalSession(raw string) (goth.Session, error) {
	var session oauthInputSession
	err := json.Unmarshal([]byte(raw), &session)
	return session, err
}
func (*oauthInputProvider) FetchUser(goth.Session) (goth.User, error) {
	return goth.User{}, errors.New("unexpected FetchUser")
}
func (*oauthInputProvider) Debug(bool)                                 {}
func (*oauthInputProvider) RefreshToken(string) (*oauth2.Token, error) { return nil, nil }
func (*oauthInputProvider) RefreshTokenAvailable() bool                { return false }
func (p *oauthInputProvider) beginCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.seen)
}
func (p *oauthInputProvider) states() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.seen...)
}

type oauthInputSession struct {
	State    string
	Provider string
}

func (s oauthInputSession) GetAuthURL() (string, error) {
	values := url.Values{"state": {s.State}, "provider": {s.Provider}}
	return "https://provider.invalid/authorize?" + values.Encode(), nil
}
func (s oauthInputSession) Marshal() string {
	encoded, _ := json.Marshal(s)
	return string(encoded)
}
func (s oauthInputSession) Authorize(_ goth.Provider, params goth.Params) (string, error) {
	if strings.TrimSpace(params.Get("code")) == "" {
		return "", errors.New("authorization code is required")
	}
	return "fixture-access-token", nil
}
