package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/markbates/goth"
)

func TestOAuthProviderConfigurationLoadsIndependentOptions(t *testing.T) {
	t.Setenv("APP_URL", "https://oauth-config.example.test")
	t.Setenv("SESSION_SECRET", "oauth-provider-config-session-secret")
	t.Setenv("JWT_SECRET", "oauth-provider-config-jwt-secret")
	t.Setenv("APP_ENV", "test")

	h := newAccessHarness(t)
	defer goth.ClearProviders()
	seedOAuthProviderConfiguration(t, h, true, true, true, true)

	// The rows deliberately use non-semantic insertion order and include an
	// unrelated option. Their primary keys are their distinct option keys.
	auth.ReloadOAuthProviders()
	assertOAuthProviderConfig(t, h, "google", "google-client-configured", "https://oauth-config.example.test/auth/google/callback")
	assertOAuthProviderConfig(t, h, "github", "github-client-configured", "https://oauth-config.example.test/auth/github/callback")

	status := h.request(http.MethodGet, "/api/oauth/status", "", "", "application/json", "")
	if status.code != http.StatusOK {
		t.Fatalf("GET /api/oauth/status returned %d: %s", status.code, status.body)
	}
	var publicStatus map[string]bool
	if err := json.Unmarshal([]byte(status.body), &publicStatus); err != nil {
		t.Fatalf("decode OAuth status: %v; body=%s", err, status.body)
	}
	if len(publicStatus) != 2 || !publicStatus["google"] || !publicStatus["github"] {
		t.Errorf("OAuth status=%v, want both configured providers enabled", publicStatus)
	}

	user := h.user("author", "oauth-config-identities")
	login := oauthAccountLoginFor(t, h, user)
	identities := oauthAccountServe(h, http.MethodGet, "/api/auth/identities", []*http.Cookie{login.jwtCookie}, nil, "", "")
	assertOAuthIdentityEnabled(t, identities, map[string]bool{"google": true, "github": true})
}

func TestOAuthProviderConfigurationRequiresEnabledCredentials(t *testing.T) {
	cases := []struct {
		name          string
		googleConfig  bool
		googleEnabled bool
		googleID      bool
		googleSecret  bool
	}{
		{name: "disabled", googleConfig: true, googleEnabled: false, googleID: true, googleSecret: true},
		{name: "missing client secret", googleConfig: true, googleEnabled: true, googleID: true, googleSecret: false},
		{name: "missing client id", googleConfig: true, googleEnabled: true, googleID: false, googleSecret: true},
		{name: "false enabled flag", googleConfig: true, googleEnabled: false},
		{name: "not configured"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("APP_URL", "https://oauth-config.example.test")
			t.Setenv("SESSION_SECRET", "oauth-provider-config-session-secret")
			t.Setenv("JWT_SECRET", "oauth-provider-config-jwt-secret")
			t.Setenv("APP_ENV", "test")
			h := newAccessHarness(t)
			defer goth.ClearProviders()
			seedOAuthProviderConfiguration(t, h, tc.googleConfig, tc.googleEnabled, tc.googleID, tc.googleSecret)

			auth.ReloadOAuthProviders()
			assertOAuthProviderAbsent(t, h, "google")
			assertOAuthProviderConfig(t, h, "github", "github-client-configured", "https://oauth-config.example.test/auth/github/callback")

			// The public endpoint reports stored enabled flags, even when the
			// configuration is incomplete. Its missing-secret policy is outside
			// this test; the authenticated list must reflect the loaded provider.
			if tc.googleEnabled && tc.googleID && tc.googleSecret {
				t.Fatal("incomplete-configuration case unexpectedly has all Google credentials")
			}
			user := h.user("author", "oauth-config-"+tc.name)
			login := oauthAccountLoginFor(t, h, user)
			identities := oauthAccountServe(h, http.MethodGet, "/api/auth/identities", []*http.Cookie{login.jwtCookie}, nil, "", "")
			assertOAuthIdentityEnabled(t, identities, map[string]bool{"google": false, "github": true})
		})
	}
}

func seedOAuthProviderConfiguration(t *testing.T, h *accessHarness, googleConfig, googleEnabled, googleID, googleSecret bool) {
	t.Helper()
	options := []core.Option{
		{Key: "oauth_github_client_secret", Value: "github-secret-configured"},
		{Key: "unrelated_oauth_note", Value: "unrelated-value"},
		{Key: "oauth_github_enabled", Value: "true"},
		{Key: "oauth_github_client_id", Value: "github-client-configured"},
	}
	if googleConfig {
		options = append(options, core.Option{Key: "oauth_google_enabled", Value: boolString(googleEnabled)})
		if googleSecret {
			options = append(options, core.Option{Key: "oauth_google_client_secret", Value: "google-secret-configured"})
		}
	}
	if googleID {
		options = append(options, core.Option{Key: "oauth_google_client_id", Value: "google-client-configured"})
	}
	if err := h.db.Create(&options).Error; err != nil {
		t.Fatalf("seed OAuth provider options: %v", err)
	}
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func assertOAuthProviderConfig(t *testing.T, h *accessHarness, name, wantClientID, wantRedirectURI string) {
	t.Helper()
	provider, err := goth.GetProvider(name)
	if err != nil {
		t.Errorf("ReloadOAuthProviders did not register %s: %v", name, err)
		return
	}
	session, err := provider.BeginAuth("configuration-probe-state")
	if err != nil {
		t.Fatalf("begin %s OAuth config probe: %v", name, err)
	}
	authURL, err := session.GetAuthURL()
	if err != nil {
		t.Fatalf("build %s OAuth URL: %v", name, err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse %s OAuth URL %q: %v", name, authURL, err)
	}
	query := parsed.Query()
	if got := query.Get("client_id"); got != wantClientID {
		t.Errorf("%s OAuth URL client_id=%q, want %q", name, got, wantClientID)
	}
	if got := query.Get("redirect_uri"); got != wantRedirectURI {
		t.Errorf("%s OAuth URL redirect_uri=%q, want %q", name, got, wantRedirectURI)
	}

	entry := h.request(http.MethodGet, "/auth/"+name, "", "", "", "")
	if entry.code != http.StatusTemporaryRedirect {
		t.Errorf("GET /auth/%s returned %d, want 307; body=%q", name, entry.code, entry.body)
		return
	}
	entryURL, err := url.Parse(entry.header.Get("Location"))
	if err != nil {
		t.Errorf("GET /auth/%s Location=%q is invalid: %v", name, entry.header.Get("Location"), err)
		return
	}
	entryQuery := entryURL.Query()
	if got := entryQuery.Get("client_id"); got != wantClientID {
		t.Errorf("GET /auth/%s Location client_id=%q, want %q", name, got, wantClientID)
	}
	if got := entryQuery.Get("redirect_uri"); got != wantRedirectURI {
		t.Errorf("GET /auth/%s Location redirect_uri=%q, want %q", name, got, wantRedirectURI)
	}
}

func assertOAuthProviderAbsent(t *testing.T, h *accessHarness, name string) {
	t.Helper()
	if _, err := goth.GetProvider(name); err == nil {
		t.Errorf("ReloadOAuthProviders left %s registered without enabled complete credentials", name)
	}
	entry := h.request(http.MethodGet, "/auth/"+name, "", "", "", "")
	if entry.code != http.StatusBadRequest {
		t.Errorf("GET /auth/%s returned %d, want 400 when provider is not loaded; body=%q", name, entry.code, entry.body)
	}
}

func assertOAuthIdentityEnabled(t *testing.T, response *httptest.ResponseRecorder, want map[string]bool) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/auth/identities returned %d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		Providers []struct {
			Provider string `json:"provider"`
			Enabled  bool   `json:"enabled"`
			Linked   bool   `json:"linked"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode identity list: %v; body=%s", err, response.Body.String())
	}
	got := make(map[string]struct{ enabled, linked bool }, len(payload.Providers))
	for _, item := range payload.Providers {
		got[item.Provider] = struct{ enabled, linked bool }{item.Enabled, item.Linked}
	}
	if len(got) != 2 {
		t.Errorf("identity providers=%v, want fixed Google and GitHub entries", got)
	}
	for name, enabled := range want {
		if state, ok := got[name]; !ok || state.enabled != enabled || state.linked {
			t.Errorf("identity status for %s=%+v (present=%t), want enabled=%t linked=false", name, state, ok, enabled)
		}
	}
}
