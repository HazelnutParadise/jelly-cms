package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
)

const (
	authRedirectUsername = "auth-redirect-user"
	authRedirectPassword = "auth-redirect-test-password"
)

func TestAuthRedirectLocalLoginDestinationTable(t *testing.T) {
	h := newAccessHarness(t)
	user := createAuthRedirectUser(t, h)

	cases := []struct {
		name string
		dest string
		want string
	}{
		{name: "absolute URL", dest: "https://evil.invalid/p", want: "/admin"},
		{name: "protocol relative", dest: "//evil.invalid/p", want: "/admin"},
		{name: "single slash backslash authority", dest: `/\evil.invalid/p`, want: "/admin"},
		{name: "slash backslash authority", dest: `/\\evil.invalid/p`, want: "/admin"},
		{name: "single backslash authority", dest: `\evil.invalid/p`, want: "/admin"},
		{name: "backslash authority", dest: `\\evil.invalid/p`, want: "/admin"},
		{name: "encoded leading slash", dest: "%2f%2fevil.invalid/p", want: "/admin"},
		{name: "encoded leading backslashes", dest: "%5c%5cevil.invalid/p", want: "/admin"},
		{name: "parsed path encoded slash authority", dest: "/%2fevil.invalid/p", want: "/admin"},
		{name: "parsed path encoded backslash authority", dest: "/%5cevil.invalid/p", want: "/admin"},
		{name: "encoded backslash in path", dest: `/admin%5c%5cevil.invalid`, want: "/admin"},
		{name: "raw CRLF", dest: "/admin\r\nLocation: https://evil.invalid/p", want: "/admin"},
		{name: "control character", dest: "/admin%01", want: "/admin"},
		{name: "malformed percent encoding", dest: "%ZZ", want: "/admin"},
		{name: "malformed percent encoding in query", dest: "/admin?x=%GG", want: "/admin"},
		{name: "relative path", dest: "admin/posts", want: "/admin"},
		{name: "javascript URL", dest: "javascript:alert(1)", want: "/admin"},
		{name: "data URL", dest: "data:text/html,evil", want: "/admin"},
		{name: "empty", dest: "", want: "/admin"},
		{name: "legal admin URL", dest: "/admin/posts?search=hello#section", want: "/admin/posts?search=hello#section"},
		{name: "legal percent encoded query", dest: "/admin/posts?search=hello%20world&tag=%E5%8F%B0", want: "/admin/posts?search=hello%20world&tag=%E5%8F%B0"},
		{name: "legal public page", dest: "/about", want: "/about"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := "/auth/login?redirect=" + url.QueryEscape(tc.dest)
			form := url.Values{"username": {user.Username}, "password": {authRedirectPassword}}
			result := h.request(http.MethodPost, path, form.Encode(), "", "text/html", "application/x-www-form-urlencoded")
			h.mustNoPanic(result, "local login")
			if result.code != http.StatusFound {
				t.Fatalf("POST /auth/login status = %d, want %d", result.code, http.StatusFound)
			}
			if got := result.header.Get(echo.HeaderLocation); got != tc.want {
				t.Errorf("POST /auth/login Location = %q, want %q", got, tc.want)
			}
			if !hasCookie(result.header, "jwt_token") {
				t.Error("successful HTML login did not set jwt_token cookie")
			}
		})
	}
}

func TestAuthRedirectWrongPasswordDoesNotAuthenticate(t *testing.T) {
	h := newAccessHarness(t)
	user := createAuthRedirectUser(t, h)
	form := url.Values{"username": {user.Username}, "password": {"wrong-password"}}
	result := h.request(http.MethodPost, "/auth/login?redirect=https%3A%2F%2Fevil.invalid%2Fp", form.Encode(), "", "text/html", "application/x-www-form-urlencoded")

	if result.code != http.StatusUnauthorized {
		t.Fatalf("POST /auth/login status = %d, want %d", result.code, http.StatusUnauthorized)
	}
	if hasCookie(result.header, "jwt_token") {
		t.Error("wrong password response set jwt_token cookie")
	}
	if hasCookie(result.header, "jelly-cms-session") {
		t.Error("wrong password response set jelly-cms-session cookie")
	}
	if got := result.header.Get(echo.HeaderLocation); got != "" {
		t.Errorf("wrong password response unexpectedly redirected to %q", got)
	}
}

func TestAuthRedirectJSONLoginShapeAndTokenRemainValid(t *testing.T) {
	h := newAccessHarness(t)
	user := createAuthRedirectUser(t, h)
	form := url.Values{"username": {user.Username}, "password": {authRedirectPassword}}
	result := h.request(http.MethodPost, "/auth/login?redirect=https%3A%2F%2Fevil.invalid%2Fp", form.Encode(), "", "application/json", "application/x-www-form-urlencoded")
	if result.code != http.StatusOK {
		t.Fatalf("JSON POST /auth/login status = %d, want %d: %s", result.code, http.StatusOK, result.body)
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(result.body), &payload); err != nil {
		t.Fatalf("decode JSON login response: %v", err)
	}
	if len(payload) != 3 {
		t.Errorf("JSON login response has %d top-level fields, want exactly message, user, and token", len(payload))
	}
	for key := range payload {
		if key != "message" && key != "user" && key != "token" {
			t.Errorf("JSON login response has unexpected top-level field %q", key)
		}
	}
	var message string
	if err := json.Unmarshal(payload["message"], &message); err != nil || message != "Login successful" {
		t.Errorf("JSON login message = %q, want %q", message, "Login successful")
	}
	var token string
	if err := json.Unmarshal(payload["token"], &token); err != nil || token == "" {
		t.Fatalf("JSON login token missing or invalid")
	}
	var responseUser struct {
		ID    uint   `json:"id"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(payload["user"], &responseUser); err != nil {
		t.Fatalf("decode JSON login user: %v", err)
	}
	if responseUser.ID != user.ID || responseUser.Email != user.Email {
		t.Errorf("JSON login user = (%d, %q), want (%d, %q)", responseUser.ID, responseUser.Email, user.ID, user.Email)
	}
	me := h.request(http.MethodGet, "/api/auth/me", "", token, "application/json", "")
	if me.code != http.StatusOK {
		t.Fatalf("GET /api/auth/me with returned token status = %d, want %d: %s", me.code, http.StatusOK, me.body)
	}
	var meUser struct {
		ID    uint   `json:"id"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal([]byte(me.body), &meUser); err != nil {
		t.Fatalf("decode GET /api/auth/me response: %v", err)
	}
	if meUser.ID != user.ID || meUser.Email != user.Email {
		t.Errorf("GET /api/auth/me user = (%d, %q), want (%d, %q)", meUser.ID, meUser.Email, user.ID, user.Email)
	}
}

func TestAuthRedirectLoginPageDoesNotReflectExecutableInput(t *testing.T) {
	h := newAccessHarness(t)
	payload := `</script><script>window.__loginRedirectXSS=1</script>`
	result := h.request(http.MethodGet, "/login?redirect="+url.QueryEscape(payload), "", "", "text/html", "")
	if result.code != http.StatusOK {
		t.Fatalf("GET /login status = %d, want %d", result.code, http.StatusOK)
	}
	if strings.Contains(result.body, "window.__loginRedirectXSS") {
		t.Error("GET /login reflected executable redirect input")
	}
}

func TestAuthRedirectOAuthEntryStoresOnlySafeDestination(t *testing.T) {
	previousProviders := cloneGothProviders(goth.GetProviders())
	previousStore := gothic.Store
	h := newAccessHarness(t)
	t.Cleanup(func() {
		goth.ClearProviders()
		providers := make([]goth.Provider, 0, len(previousProviders))
		for _, provider := range previousProviders {
			providers = append(providers, provider)
		}
		goth.UseProviders(providers...)
		gothic.Store = previousStore
	})
	goth.UseProviders(redirectTestProvider{})

	result := h.request(http.MethodGet, "/auth/google?redirect="+url.QueryEscape("https://evil.invalid/p"), "", "", "text/html", "")
	if result.code != http.StatusTemporaryRedirect {
		t.Fatalf("GET /auth/google status = %d, want Goth's %d begin-auth redirect", result.code, http.StatusTemporaryRedirect)
	}
	if got := result.header.Get(echo.HeaderLocation); !strings.HasPrefix(got, "http://127.0.0.1/oauth/authorize?") {
		t.Fatalf("GET /auth/google Location = %q, want local fake-provider authorization URL", got)
	}
	cookie := cookieFromHeader(result.header, "jelly-cms-session")
	if cookie == nil {
		t.Fatal("OAuth entry did not persist the application session cookie")
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	session, err := gothic.Store.Get(req, "jelly-cms-session")
	if err != nil {
		t.Fatalf("decode signed application session: %v", err)
	}
	if got, _ := session.Values["oauth_redirect"].(string); got != "/admin" {
		t.Errorf("OAuth session oauth_redirect = %q, want %q", got, "/admin")
	}
}

func TestAuthRedirectOAuthCallbackRevalidatesSignedLegacyDestination(t *testing.T) {
	previousProviders := cloneGothProviders(goth.GetProviders())
	previousStore := gothic.Store
	h := newAccessHarness(t)
	t.Cleanup(func() {
		goth.ClearProviders()
		providers := make([]goth.Provider, 0, len(previousProviders))
		for _, provider := range previousProviders {
			providers = append(providers, provider)
		}
		goth.UseProviders(providers...)
		gothic.Store = previousStore
	})
	goth.UseProviders(redirectTestProvider{})

	for _, tc := range []struct {
		name string
		dest string
		want string
	}{
		{name: "signed legacy external URL", dest: "https://evil.invalid/p", want: "/admin"},
		{name: "signed legal in-site URL", dest: "/admin/posts?search=hello#section", want: "/admin/posts?search=hello#section"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			appCookie := signedAppSessionCookie(t, "oauth_redirect", tc.dest)
			gothCookie := signedGothSessionCookie(t, redirectTestProvider{}, "state-from-provider")
			req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state=state-from-provider", nil)
			req.AddCookie(appCookie)
			req.AddCookie(gothCookie)
			req.Header.Set(echo.HeaderAccept, "text/html")
			rec := httptest.NewRecorder()
			h.echo.ServeHTTP(rec, req)

			if rec.Code != http.StatusFound {
				t.Fatalf("OAuth callback status = %d, want %d: %s", rec.Code, http.StatusFound, rec.Body.String())
			}
			if got := rec.Header().Get(echo.HeaderLocation); got != tc.want {
				t.Errorf("OAuth callback Location = %q, want %q", got, tc.want)
			}
			if !hasCookie(rec.Header(), "jwt_token") {
				t.Error("successful OAuth callback did not set jwt_token cookie")
			}
		})
	}
}

func createAuthRedirectUser(t *testing.T, h *accessHarness) core.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(authRedirectPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash test password: %v", err)
	}
	user := core.User{Username: authRedirectUsername, Email: authRedirectUsername + "@example.test", Password: string(hash), Role: "admin", Provider: "local"}
	if err := h.db.Create(&user).Error; err != nil {
		t.Fatalf("create local test user: %v", err)
	}
	return user
}

func hasCookie(header http.Header, name string) bool {
	return cookieFromHeader(header, name) != nil
}

func cookieFromHeader(header http.Header, name string) *http.Cookie {
	for _, cookie := range header.Values(echo.HeaderSetCookie) {
		parsed, err := http.ParseSetCookie(cookie)
		if err == nil && parsed.Name == name {
			return parsed
		}
	}
	return nil
}

func cloneGothProviders(providers goth.Providers) goth.Providers {
	clone := make(goth.Providers, len(providers))
	for name, provider := range providers {
		clone[name] = provider
	}
	return clone
}

func signedAppSessionCookie(t *testing.T, key, value string) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	session, err := gothic.Store.New(req, "jelly-cms-session")
	if err != nil {
		t.Fatalf("create application session: %v", err)
	}
	session.Values[key] = value
	if err := session.Save(req, rec); err != nil {
		t.Fatalf("sign application session: %v", err)
	}
	cookie := cookieFromHeader(rec.Header(), "jelly-cms-session")
	if cookie == nil {
		t.Fatal("application session did not produce a cookie")
	}
	return cookie
}

func signedGothSessionCookie(t *testing.T, provider goth.Provider, state string) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	session, err := provider.BeginAuth(state)
	if err != nil {
		t.Fatalf("create Goth authorization session: %v", err)
	}
	if err := gothic.StoreInSession(provider.Name(), session.Marshal(), req, rec); err != nil {
		t.Fatalf("sign Goth authorization session: %v", err)
	}
	cookie := cookieFromHeader(rec.Header(), gothic.SessionName)
	if cookie == nil {
		t.Fatalf("Goth authorization session did not produce %q cookie", gothic.SessionName)
	}
	return cookie
}

type redirectTestProvider struct{}

func (redirectTestProvider) Name() string   { return "google" }
func (redirectTestProvider) SetName(string) {}
func (redirectTestProvider) BeginAuth(state string) (goth.Session, error) {
	return redirectTestSession{State: state}, nil
}
func (redirectTestProvider) UnmarshalSession(raw string) (goth.Session, error) {
	var session redirectTestSession
	err := json.Unmarshal([]byte(raw), &session)
	return session, err
}
func (redirectTestProvider) FetchUser(session goth.Session) (goth.User, error) {
	if _, ok := session.(redirectTestSession); !ok {
		return goth.User{}, fmt.Errorf("unexpected test session %T", session)
	}
	return goth.User{UserID: "fixed-goth-user-id", Email: "oauth-redirect@example.test", Name: "Redirect Test", NickName: "oauth-redirect", Provider: "google"}, nil
}
func (redirectTestProvider) Debug(bool)                                 {}
func (redirectTestProvider) RefreshToken(string) (*oauth2.Token, error) { return nil, nil }
func (redirectTestProvider) RefreshTokenAvailable() bool                { return false }

type redirectTestSession struct {
	State string
}

func (s redirectTestSession) GetAuthURL() (string, error) {
	return "http://127.0.0.1/oauth/authorize?state=" + url.QueryEscape(s.State), nil
}
func (s redirectTestSession) Marshal() string {
	encoded, _ := json.Marshal(s)
	return string(encoded)
}
func (redirectTestSession) Authorize(goth.Provider, goth.Params) (string, error) {
	return "local-test-access-token", nil
}
