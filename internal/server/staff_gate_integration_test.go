package server

import (
	"net/http"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth"
)

// Staff gate: only admin, editor and author may enter /admin/* pages and
// /api/admin/*. A customer (role set by ticket 07) must never reach them.

func TestStaffGateAdminPagesRejectNonStaff(t *testing.T) {
	h := newAccessHarness(t)
	customerToken := h.token(h.user("customer", "staff-gate-customer"))

	for _, path := range []string{"/admin", "/admin/posts"} {
		r := h.request(http.MethodGet, path, "", customerToken, echo.MIMETextHTML, "")
		h.mustNoPanic(r, "customer GET "+path)
		if r.code == http.StatusOK {
			t.Errorf("customer GET %s returned 200, want 302 to \"/\" or 403", path)
			continue
		}
		switch r.code {
		case http.StatusFound:
			if location := r.header.Get(echo.HeaderLocation); location != "/" {
				t.Errorf("customer GET %s redirected to %q, want \"/\"", path, location)
			}
		case http.StatusForbidden:
		default:
			t.Errorf("customer GET %s returned %d, want 302 to \"/\" or 403", path, r.code)
		}
	}

	for _, role := range []string{"admin", "editor", "author"} {
		token := h.token(h.user(role, "staff-gate-"+role))
		for _, path := range []string{"/admin", "/admin/posts"} {
			r := h.request(http.MethodGet, path, "", token, echo.MIMETextHTML, "")
			h.mustNoPanic(r, role+" GET "+path)
			if r.code != http.StatusOK {
				t.Errorf("%s GET %s returned %d, want 200", role, path, r.code)
			}
		}
	}
}

func TestStaffGateAdminAPIRejectsNonStaff(t *testing.T) {
	h := newAccessHarness(t)
	customerToken := h.token(h.user("customer", "staff-gate-api-customer"))
	authorToken := h.token(h.user("author", "staff-gate-api-author"))

	for _, path := range []string{"/api/admin/posts", "/api/admin/palette"} {
		r := h.request(http.MethodGet, path, "", customerToken, echo.MIMEApplicationJSON, "")
		h.mustNoPanic(r, "customer GET "+path)
		if r.code != http.StatusForbidden {
			t.Errorf("customer GET %s returned %d, want 403; body=%q", path, r.code, r.body)
		}
	}

	r := h.request(http.MethodGet, "/api/admin/palette", "", authorToken, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(r, "author GET /api/admin/palette")
	if r.code != http.StatusOK {
		t.Errorf("author GET /api/admin/palette returned %d, want 200; body=%q", r.code, r.body)
	}
}

func TestOAuthFirstSignInRoleFromOption(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")

	for _, tc := range []struct {
		name  string
		seed  bool
		value string
		want  string
	}{
		{name: "unset option defaults to author", seed: false, want: "author"},
		{name: "customer option is honored", seed: true, value: "customer", want: "customer"},
		{name: "unknown role falls back to author", seed: true, value: "superuser", want: "author"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := oauthTxnPrepareHarness(t)
			if tc.seed {
				if err := h.db.Create(&core.Option{Key: "oauth_default_role", Value: tc.value}).Error; err != nil {
					t.Fatalf("seed oauth_default_role: %v", err)
				}
			}

			email := "staff-role-" + tc.want + "-" + tc.value + "@staff-gate.example.test"
			provider := newOAuthTxnIntegrationProvider("google")
			provider.user = goth.User{UserID: "staff-role-subject-" + tc.value, Email: email, Name: "Staff Role", NickName: "staff-role-" + tc.want, Provider: "google"}
			resetOAuthInputProviders(provider)

			entry := oauthIdentityBegin(t, h, "google", "/admin")
			callback := oauthIdentityCallback(h, entry, "staff-role-code-"+tc.value, echo.MIMEApplicationJSON)
			if callback.Code != http.StatusOK {
				t.Fatalf("OAuth JSON callback status=%d, want 200; body=%q", callback.Code, callback.Body.String())
			}
			oauthIdentityJSONLogin(t, callback)

			var created core.User
			if err := h.db.Where("email = ?", email).Take(&created).Error; err != nil {
				t.Fatalf("load first-sign-in user: %v", err)
			}
			if created.Role != tc.want {
				t.Errorf("first OAuth sign-in with oauth_default_role=%q created role=%q, want %q", tc.value, created.Role, tc.want)
			}
		})
	}
}
