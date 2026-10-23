package server

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

const oauthProviderAdminPath = "/api/admin/oauth/providers"

type oauthProviderAdminSettings struct {
	Enabled bool              `json:"enabled"`
	Config  map[string]string `json:"config"`
}

type oauthProviderAdminResponse struct {
	Google oauthProviderAdminSettings `json:"google"`
	GitHub oauthProviderAdminSettings `json:"github"`
}

func TestOAuthProviderAdminReadsIndependentCompleteSettingsWithoutWrites(t *testing.T) {
	h := newAccessHarness(t)
	options := []core.Option{
		{Key: "oauth_google_enabled", Value: "true"},
		{Key: "oauth_google_client_id", Value: "google-client-id-sentinel"},
		{Key: "oauth_google_client_secret", Value: "google-client-secret-sentinel"},
		{Key: "oauth_github_enabled", Value: "true"},
		{Key: "oauth_github_client_id", Value: "github-client-id-sentinel"},
		{Key: "oauth_github_client_secret", Value: "github-client-secret-sentinel"},
		{Key: "unrelated_oauth_setting", Value: "unrelated-value-sentinel"},
	}
	oauthProviderAdminSeed(t, h, options)
	admin := h.user("admin", "oauth-provider-admin-complete")
	before := oauthProviderAdminSnapshot(t, h)
	response := h.request(http.MethodGet, oauthProviderAdminPath, "", h.token(admin), "application/json", "")
	if response.code != http.StatusOK {
		t.Fatalf("admin GET %s returned %d, want 200: %s", oauthProviderAdminPath, response.code, response.body)
	}
	oauthProviderAdminAssertResponse(t, response.body,
		oauthProviderAdminSettings{Enabled: true, Config: map[string]string{
			"oauth_google_client_id":     "google-client-id-sentinel",
			"oauth_google_client_secret": "google-client-secret-sentinel",
		}},
		oauthProviderAdminSettings{Enabled: true, Config: map[string]string{
			"oauth_github_client_id":     "github-client-id-sentinel",
			"oauth_github_client_secret": "github-client-secret-sentinel",
		}},
	)
	if after := oauthProviderAdminSnapshot(t, h); !reflect.DeepEqual(before, after) {
		t.Errorf("GET %s changed Options: before=%v after=%v", oauthProviderAdminPath, before, after)
	}
}

func TestOAuthProviderAdminKeepsDisabledProviderCredentialsAndPartialValuesSeparate(t *testing.T) {
	t.Run("disabled-provider-keeps-stored-values", func(t *testing.T) {
		h := newAccessHarness(t)
		oauthProviderAdminSeed(t, h, []core.Option{
			{Key: "oauth_google_enabled", Value: "false"},
			{Key: "oauth_google_client_id", Value: "google-disabled-client-id-sentinel"},
			{Key: "oauth_google_client_secret", Value: "google-disabled-client-secret-sentinel"},
			{Key: "oauth_github_enabled", Value: "true"},
			{Key: "oauth_github_client_id", Value: "github-client-id-sentinel"},
			{Key: "oauth_github_client_secret", Value: "github-client-secret-sentinel"},
		})
		admin := h.user("admin", "oauth-provider-admin-disabled")
		before := oauthProviderAdminSnapshot(t, h)
		response := h.request(http.MethodGet, oauthProviderAdminPath, "", h.token(admin), "application/json", "")
		if response.code != http.StatusOK {
			t.Fatalf("admin GET %s returned %d, want 200: %s", oauthProviderAdminPath, response.code, response.body)
		}
		oauthProviderAdminAssertResponse(t, response.body,
			oauthProviderAdminSettings{Enabled: false, Config: map[string]string{
				"oauth_google_client_id":     "google-disabled-client-id-sentinel",
				"oauth_google_client_secret": "google-disabled-client-secret-sentinel",
			}},
			oauthProviderAdminSettings{Enabled: true, Config: map[string]string{
				"oauth_github_client_id":     "github-client-id-sentinel",
				"oauth_github_client_secret": "github-client-secret-sentinel",
			}},
		)
		if after := oauthProviderAdminSnapshot(t, h); !reflect.DeepEqual(before, after) {
			t.Errorf("GET %s changed Options: before=%v after=%v", oauthProviderAdminPath, before, after)
		}
	})

	t.Run("partial-provider-does-not-borrow-other-options", func(t *testing.T) {
		h := newAccessHarness(t)
		oauthProviderAdminSeed(t, h, []core.Option{
			{Key: "oauth_google_enabled", Value: "true"},
			{Key: "oauth_google_client_secret", Value: "google-partial-secret-sentinel"},
			{Key: "oauth_github_enabled", Value: "false"},
			{Key: "oauth_github_client_id", Value: "github-disabled-client-id-sentinel"},
			{Key: "oauth_github_client_secret", Value: "github-disabled-client-secret-sentinel"},
			{Key: "oauth_google_client_id_backup", Value: "wrong-google-client-id-sentinel"},
			{Key: "unrelated_oauth_setting", Value: "unrelated-value-sentinel"},
		})
		admin := h.user("admin", "oauth-provider-admin-partial")
		before := oauthProviderAdminSnapshot(t, h)
		response := h.request(http.MethodGet, oauthProviderAdminPath, "", h.token(admin), "application/json", "")
		if response.code != http.StatusOK {
			t.Fatalf("admin GET %s returned %d, want 200: %s", oauthProviderAdminPath, response.code, response.body)
		}
		oauthProviderAdminAssertResponse(t, response.body,
			oauthProviderAdminSettings{Enabled: true, Config: map[string]string{
				"oauth_google_client_secret": "google-partial-secret-sentinel",
			}},
			oauthProviderAdminSettings{Enabled: false, Config: map[string]string{
				"oauth_github_client_id":     "github-disabled-client-id-sentinel",
				"oauth_github_client_secret": "github-disabled-client-secret-sentinel",
			}},
		)
		for _, unrelated := range []string{"wrong-google-client-id-sentinel", "unrelated-value-sentinel"} {
			if strings.Contains(response.body, unrelated) {
				t.Errorf("OAuth provider response included unrelated Option value %q", unrelated)
			}
		}
		if after := oauthProviderAdminSnapshot(t, h); !reflect.DeepEqual(before, after) {
			t.Errorf("GET %s changed Options: before=%v after=%v", oauthProviderAdminPath, before, after)
		}
	})
}

func TestOAuthProviderAdminReadRequiresAdministrator(t *testing.T) {
	h := newAccessHarness(t)
	oauthProviderAdminSeed(t, h, []core.Option{
		{Key: "oauth_google_enabled", Value: "true"},
		{Key: "oauth_google_client_id", Value: "google-authz-client-sentinel"},
		{Key: "oauth_google_client_secret", Value: "google-authz-secret-sentinel"},
	})
	before := oauthProviderAdminSnapshot(t, h)
	for _, tc := range []struct {
		name string
		role string
		want int
	}{
		{name: "anonymous", want: http.StatusUnauthorized},
		{name: "author", role: "author", want: http.StatusForbidden},
		{name: "editor", role: "editor", want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := ""
			if tc.role != "" {
				user := h.user(tc.role, "oauth-provider-admin-denied-"+tc.role)
				token = h.token(user)
			}
			response := h.request(http.MethodGet, oauthProviderAdminPath, "", token, "application/json", "")
			if response.code != tc.want {
				t.Errorf("GET %s as %s returned %d, want %d: %s", oauthProviderAdminPath, tc.name, response.code, tc.want, response.body)
			}
			if after := oauthProviderAdminSnapshot(t, h); !reflect.DeepEqual(before, after) {
				t.Errorf("denied GET by %s changed Options: before=%v after=%v", tc.name, before, after)
			}
		})
	}
}

func TestOAuthProviderAdminOptionReadFailureIsUnavailableAndRecovers(t *testing.T) {
	h := newAccessHarness(t)
	options := []core.Option{
		{Key: "oauth_google_enabled", Value: "true"},
		{Key: "oauth_google_client_id", Value: "google-failure-client-sentinel"},
		{Key: "oauth_google_client_secret", Value: "google-failure-secret-sentinel"},
		{Key: "oauth_github_enabled", Value: "true"},
		{Key: "oauth_github_client_id", Value: "github-failure-client-sentinel"},
		{Key: "oauth_github_client_secret", Value: "github-failure-secret-sentinel"},
	}
	oauthProviderAdminSeed(t, h, options)
	admin := h.user("admin", "oauth-provider-admin-db-failure")
	token := h.token(admin)
	before := oauthProviderAdminSnapshot(t, h)
	if err := h.db.Exec("ALTER TABLE options RENAME TO oauth_provider_admin_options_saved").Error; err != nil {
		t.Fatalf("hide Options table in isolated database: %v", err)
	}
	tableRenamed := true
	t.Cleanup(func() {
		if tableRenamed {
			if err := h.db.Exec("ALTER TABLE oauth_provider_admin_options_saved RENAME TO options").Error; err != nil {
				t.Errorf("restore isolated Options table: %v", err)
			}
		}
	})

	// This proves the authenticated-user lookup still works while the settings
	// table alone is unavailable.
	me := h.request(http.MethodGet, "/api/auth/me", "", token, "application/json", "")
	if me.code != http.StatusOK {
		t.Fatalf("GET /api/auth/me during Options failure returned %d, want 200: %s", me.code, me.body)
	}
	failed := h.request(http.MethodGet, oauthProviderAdminPath, "", token, "application/json", "")
	if failed.code != http.StatusServiceUnavailable {
		t.Errorf("GET %s with unavailable Options table returned %d, want 503: %s", oauthProviderAdminPath, failed.code, failed.body)
	}
	if !strings.Contains(strings.ToLower(failed.header.Get("Content-Type")), "application/json") {
		t.Errorf("Options failure Content-Type=%q, want JSON", failed.header.Get("Content-Type"))
	}
	for _, secret := range []string{
		"oauth_provider_admin_options_saved",
		"relation \"options\" does not exist",
		"google-failure-client-sentinel",
		"google-failure-secret-sentinel",
		"github-failure-client-sentinel",
		"github-failure-secret-sentinel",
	} {
		if strings.Contains(failed.body, secret) {
			t.Errorf("Options failure response leaked internal detail or stored sentinel %q", secret)
		}
	}
	if err := h.db.Exec("ALTER TABLE oauth_provider_admin_options_saved RENAME TO options").Error; err != nil {
		t.Fatalf("restore isolated Options table after failure request: %v", err)
	}
	tableRenamed = false

	recovered := h.request(http.MethodGet, oauthProviderAdminPath, "", token, "application/json", "")
	if recovered.code != http.StatusOK {
		t.Fatalf("GET %s after restoring Options table returned %d, want 200: %s", oauthProviderAdminPath, recovered.code, recovered.body)
	}
	oauthProviderAdminAssertResponse(t, recovered.body,
		oauthProviderAdminSettings{Enabled: true, Config: map[string]string{
			"oauth_google_client_id":     "google-failure-client-sentinel",
			"oauth_google_client_secret": "google-failure-secret-sentinel",
		}},
		oauthProviderAdminSettings{Enabled: true, Config: map[string]string{
			"oauth_github_client_id":     "github-failure-client-sentinel",
			"oauth_github_client_secret": "github-failure-secret-sentinel",
		}},
	)
	if after := oauthProviderAdminSnapshot(t, h); !reflect.DeepEqual(before, after) {
		t.Errorf("GET after Options recovery changed data: before=%v after=%v", before, after)
	}
}

func oauthProviderAdminSeed(t *testing.T, h *accessHarness, options []core.Option) {
	t.Helper()
	if err := h.db.Create(&options).Error; err != nil {
		t.Fatalf("seed isolated OAuth provider Options: %v", err)
	}
}

func oauthProviderAdminSnapshot(t *testing.T, h *accessHarness) []core.Option {
	t.Helper()
	var options []core.Option
	if err := h.db.Order("key ASC").Find(&options).Error; err != nil {
		t.Fatalf("snapshot all Options: %v", err)
	}
	return options
}

func oauthProviderAdminAssertResponse(t *testing.T, body string, google, github oauthProviderAdminSettings) {
	t.Helper()
	var got map[string]oauthProviderAdminSettings
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode OAuth provider settings response: %v; body=%s", err, body)
	}
	if len(got) != 2 {
		t.Errorf("OAuth provider settings keys=%v, want exactly google and github", got)
	}
	if actual, ok := got["google"]; !ok || actual.Enabled != google.Enabled || !reflect.DeepEqual(actual.Config, google.Config) {
		t.Errorf("Google settings=%+v (present=%t), want %+v", actual, ok, google)
	}
	if actual, ok := got["github"]; !ok || actual.Enabled != github.Enabled || !reflect.DeepEqual(actual.Config, github.Config) {
		t.Errorf("GitHub settings=%+v (present=%t), want %+v", actual, ok, github)
	}
}
