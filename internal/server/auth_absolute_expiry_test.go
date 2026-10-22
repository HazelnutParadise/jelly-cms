package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
)

const (
	absoluteExpiryTestSessionSecret = "absolute-expiry-session-secret-0123456789"
	absoluteExpiryCookieName        = "jelly-cms-session"
)

func TestAuthAbsoluteExpiry(t *testing.T) {
	oldSessionSecret, hadSessionSecret := getenvForAbsoluteExpiry("SESSION_SECRET")
	oldJWTSecret, hadJWTSecret := getenvForAbsoluteExpiry("JWT_SECRET")
	t.Setenv("SESSION_SECRET", absoluteExpiryTestSessionSecret)
	t.Setenv("JWT_SECRET", "absolute-expiry-jwt-secret-0123456789")

	h := newAccessHarness(t)
	t.Cleanup(func() {
		restoreAbsoluteExpiryEnv("SESSION_SECRET", oldSessionSecret, hadSessionSecret)
		restoreAbsoluteExpiryEnv("JWT_SECRET", oldJWTSecret, hadJWTSecret)
		if err := auth.Init(); err != nil {
			t.Errorf("restore auth globals after test: %v", err)
		}
	})
	store := sessions.NewCookieStore([]byte(absoluteExpiryTestSessionSecret))
	store.MaxAge(7 * 24 * 60 * 60)

	t.Run("login cookie has fixed seven day absolute expiry", func(t *testing.T) {
		_, _, session := absoluteExpiryLogin(t, h)
		decoded := absoluteExpiryDecode(t, store, session)
		expiresAt, ok := decoded.Values["login_expires_at"].(int64)
		if !ok {
			t.Fatalf("login_expires_at has type %T, want int64", decoded.Values["login_expires_at"])
		}
		remaining := time.Unix(expiresAt, 0).Sub(time.Now())
		if remaining < 6*24*time.Hour+23*time.Hour || remaining > 7*24*time.Hour+time.Minute {
			t.Errorf("login_expires_at remaining lifetime is %s, want between 6d23h and 7d1m", remaining)
		}
	})

	for _, tc := range []struct {
		name   string
		mutate func(map[interface{}]interface{})
		want   int
	}{
		{name: "missing expiry", mutate: func(values map[interface{}]interface{}) { delete(values, "login_expires_at") }, want: http.StatusUnauthorized},
		{name: "wrong expiry type", mutate: func(values map[interface{}]interface{}) { values["login_expires_at"] = "tomorrow" }, want: http.StatusUnauthorized},
		{name: "zero expiry", mutate: func(values map[interface{}]interface{}) { values["login_expires_at"] = int64(0) }, want: http.StatusUnauthorized},
		{name: "expired expiry", mutate: func(values map[interface{}]interface{}) {
			values["login_expires_at"] = time.Now().Add(-time.Minute).Unix()
		}, want: http.StatusUnauthorized},
		{name: "future expiry", mutate: func(values map[interface{}]interface{}) {
			values["login_expires_at"] = time.Now().Add(time.Hour).Unix()
		}, want: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, sessionCookie := absoluteExpiryLogin(t, h)
			decoded := absoluteExpiryDecode(t, store, sessionCookie)
			originalLoginID, originalUserID := decoded.Values["login_id"], decoded.Values["user_id"]
			tc.mutate(decoded.Values)
			mutatedCookie := absoluteExpirySave(t, store, decoded, sessionCookie)
			resigned := absoluteExpiryDecode(t, store, mutatedCookie)
			if resigned.Values["login_id"] != originalLoginID || resigned.Values["user_id"] != originalUserID {
				t.Errorf("re-signed fixture changed login_id or user_id")
			}
			got := revocationRequest(h, http.MethodGet, "/api/auth/me", []*http.Cookie{mutatedCookie}, "", "")
			if got.panicked != nil {
				t.Errorf("GET /api/auth/me panicked: %v", got.panicked)
			}
			if got.code != tc.want {
				t.Errorf("GET /api/auth/me returned %d, want %d; body=%q", got.code, tc.want, got.body)
			}
		})
	}

	t.Run("GET auth not configured cannot refresh expired absolute expiry", func(t *testing.T) {
		_, _, sessionCookie := absoluteExpiryLogin(t, h)
		decoded := absoluteExpiryDecode(t, store, sessionCookie)
		originalLoginID, originalUserID := decoded.Values["login_id"], decoded.Values["user_id"]
		decoded.Values["login_expires_at"] = time.Now().Add(-time.Minute).Unix()
		expiredCookie := absoluteExpirySave(t, store, decoded, sessionCookie)
		expired := absoluteExpiryDecode(t, store, expiredCookie)
		originalExpiry := expired.Values["login_expires_at"]

		response := revocationRequest(h, http.MethodGet, "/auth/not-configured", []*http.Cookie{expiredCookie}, "", "")
		if response.panicked != nil {
			t.Errorf("GET /auth/not-configured panicked: %v", response.panicked)
		}
		if responseCookie := revocationCookie((&http.Response{Header: response.header}).Cookies(), absoluteExpiryCookieName); responseCookie == nil {
			t.Errorf("GET /auth/not-configured did not return an updated session cookie; status=%d", response.code)
		} else {
			if responseCookie.Value == expiredCookie.Value {
				t.Errorf("GET /auth/not-configured returned the original cookie, want a re-signed cookie")
			}
			refreshed := absoluteExpiryDecode(t, store, responseCookie)
			if refreshed.Values["login_expires_at"] != originalExpiry {
				t.Errorf("re-signed session expiry changed from %v to %v", originalExpiry, refreshed.Values["login_expires_at"])
			}
			if refreshed.Values["login_id"] != originalLoginID || refreshed.Values["user_id"] != originalUserID {
				t.Errorf("re-signed session changed login_id or user_id")
			}
			me := revocationRequest(h, http.MethodGet, "/api/auth/me", []*http.Cookie{responseCookie}, "", "")
			if me.code != http.StatusUnauthorized {
				t.Errorf("GET /api/auth/me accepted re-signed expired session: status=%d body=%q", me.code, me.body)
			}
		}
	})

	t.Run("explicit empty JWT cookie does not fall back to valid session", func(t *testing.T) {
		_, _, sessionCookie := absoluteExpiryLogin(t, h)
		got := revocationRequest(h, http.MethodGet, "/api/auth/me", []*http.Cookie{
			sessionCookie,
			{Name: "jwt_token", Value: "", Path: "/"},
		}, "", "")
		if got.code != http.StatusUnauthorized {
			t.Errorf("empty jwt_token with valid session returned %d, want 401; body=%q", got.code, got.body)
		}
	})

	t.Run("low level session decoding remains independent of expiry and database", func(t *testing.T) {
		_, _, sessionCookie := absoluteExpiryLogin(t, h)
		decoded := absoluteExpiryDecode(t, store, sessionCookie)
		decoded.Values["login_expires_at"] = int64(0)
		invalidExpiryCookie := absoluteExpirySave(t, store, decoded, sessionCookie)
		previousDB := data.DB
		data.DB = nil
		defer func() { data.DB = previousDB }()
		ctx := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/", nil), httptest.NewRecorder())
		ctx.Request().AddCookie(invalidExpiryCookie)
		userID, err := auth.GetUserIDFromSession(ctx)
		if err != nil {
			t.Errorf("GetUserIDFromSession rejected decodable session: %v", err)
		}
		if userID == 0 {
			t.Errorf("GetUserIDFromSession returned zero user ID")
		}
	})
}

func absoluteExpiryLogin(t *testing.T, h *accessHarness) (string, []*http.Cookie, *http.Cookie) {
	t.Helper()
	_, token, cookies := revocationLogin(t, h, "absolute-expiry-"+time.Now().Format("150405.000000000"))
	sessionCookie := revocationCookie(cookies, absoluteExpiryCookieName)
	if sessionCookie == nil {
		t.Fatalf("login did not return %s cookie", absoluteExpiryCookieName)
	}
	return token, cookies, sessionCookie
}

func absoluteExpiryDecode(t *testing.T, store *sessions.CookieStore, cookie *http.Cookie) *sessions.Session {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	session, err := store.Get(req, absoluteExpiryCookieName)
	if err != nil {
		t.Fatalf("decode signed session cookie: %v", err)
	}
	return session
}

func absoluteExpirySave(t *testing.T, store *sessions.CookieStore, session *sessions.Session, previous *http.Cookie) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(previous)
	rec := httptest.NewRecorder()
	if err := store.Save(req, rec, session); err != nil {
		t.Fatalf("sign session cookie: %v", err)
	}
	updated := revocationCookie((&http.Response{Header: rec.Header()}).Cookies(), absoluteExpiryCookieName)
	if updated == nil {
		t.Fatalf("session store did not emit %s cookie", absoluteExpiryCookieName)
	}
	return updated
}

func getenvForAbsoluteExpiry(key string) (string, bool) {
	return os.LookupEnv(key)
}

func restoreAbsoluteExpiryEnv(key, value string, wasSet bool) {
	if wasSet {
		_ = os.Setenv(key, value)
	} else {
		_ = os.Unsetenv(key)
	}
}
