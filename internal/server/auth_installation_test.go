package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
)

func TestInstallationPreservesAuthState(t *testing.T) {
	t.Setenv("SESSION_SECRET", "installation-preserves-auth-session-secret-0123456789")
	t.Setenv("JWT_SECRET", "installation-preserves-auth-jwt-secret-0123456789!!")

	h := newInstallFlowHarness(t)

	originalToken, err := auth.GenerateJWT(42, "test-user-42@example.com", "author")
	if err != nil {
		t.Fatalf("GenerateJWT for the pre-installation test user failed: %v", err)
	}

	recorder := httptest.NewRecorder()
	context := h.server.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), recorder)
	if err := auth.SetUserSession(context, 42); err != nil {
		t.Fatalf("SetUserSession for the pre-installation test user failed: %v", err)
	}

	var sessionCookie string
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == "jelly-cms-session" {
			sessionCookie = cookie.Value
		}
	}
	if sessionCookie == "" {
		t.Fatal("SetUserSession did not issue a jelly-cms-session cookie")
	}

	t.Setenv("SESSION_SECRET", "changed-session-secret-must-not-rebind-login-0123456789")
	t.Setenv("JWT_SECRET", "changed-jwt-secret-must-not-rebind-login-0123456789!!!!")

	h.installSite()

	claims, err := auth.ValidateJWT(originalToken)
	if err != nil {
		t.Fatalf("the original JWT is no longer valid after installation: %v", err)
	}
	if userID, _ := claims["user_id"].(float64); userID != 42 {
		t.Fatalf("original JWT user_id = %v, want 42", claims["user_id"])
	}

	sessionRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	sessionRequest.AddCookie(&http.Cookie{Name: "jelly-cms-session", Value: sessionCookie})
	sessionContext := h.server.NewContext(sessionRequest, httptest.NewRecorder())
	userID, err := auth.GetUserIDFromSession(sessionContext)
	if err != nil {
		t.Fatalf("the original session cookie is no longer valid after installation: %v", err)
	}
	if userID != 42 {
		t.Fatalf("GetUserIDFromSession = %d, want 42", userID)
	}

	h.loginSite()
}
