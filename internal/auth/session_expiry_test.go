package auth_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/gorilla/securecookie"
	"github.com/labstack/echo/v4"
)

const (
	sessionExpiryTestSecret = "session-expiry-test-secret"
	sessionExpiryCookieName = "jelly-cms-session"
	sessionExpiryUserID     = uint(42)
)

func TestSessionSignatureExpiry(t *testing.T) {
	t.Setenv("SESSION_SECRET", sessionExpiryTestSecret)
	t.Setenv("JWT_SECRET", "session-expiry-jwt-test-secret")

	previousDB := data.DB
	data.DB = nil
	t.Cleanup(func() { data.DB = previousDB })

	if err := auth.Init(); err != nil {
		t.Fatalf("auth.Init() error = %v", err)
	}
	if data.DB != nil {
		t.Fatal("auth.Init unexpectedly initialized the database")
	}

	e := echo.New()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	c := e.NewContext(req, rec)
	if err := auth.SetUserSession(c, sessionExpiryUserID); err != nil {
		t.Fatalf("auth.SetUserSession() error = %v", err)
	}

	cookies := rec.Result().Cookies()
	var freshCookie string
	for _, cookie := range cookies {
		if cookie.Name == sessionExpiryCookieName {
			freshCookie = cookie.Value
		}
	}
	if freshCookie == "" {
		t.Fatal("auth.SetUserSession() did not write a session cookie")
	}

	agedSixDays := ageSessionCookie(t, freshCookie, 6*24*time.Hour)
	agedEightDays := ageSessionCookie(t, freshCookie, 8*24*time.Hour)

	// Prove both aged samples still carry a valid HMAC under the 30-day
	// securecookie expiry before asking auth to apply its shorter lifetime.
	secure := securecookie.New([]byte(sessionExpiryTestSecret), nil).MaxAge(30 * 24 * 3600)
	for _, sample := range []struct {
		name   string
		cookie string
	}{
		{name: "6 days", cookie: agedSixDays},
		{name: "8 days", cookie: agedEightDays},
	} {
		var decoded map[interface{}]interface{}
		if err := secure.Decode(sessionExpiryCookieName, sample.cookie, &decoded); err != nil {
			t.Fatalf("securecookie 30-day sample %s did not validate: %v", sample.name, err)
		}
	}

	t.Run("fresh session is accepted", func(t *testing.T) {
		context := newSessionContext(freshCookie)
		userID, err := auth.GetUserIDFromSession(context)
		if err != nil {
			t.Fatalf("GetUserIDFromSession(fresh) error = %v", err)
		}
		if userID != sessionExpiryUserID {
			t.Fatalf("GetUserIDFromSession(fresh) = %d, want %d", userID, sessionExpiryUserID)
		}
	})

	t.Run("6-day-old session is accepted", func(t *testing.T) {
		context := newSessionContext(agedSixDays)
		userID, err := auth.GetUserIDFromSession(context)
		if err != nil {
			t.Fatalf("GetUserIDFromSession(6 days) error = %v", err)
		}
		if userID != sessionExpiryUserID {
			t.Fatalf("GetUserIDFromSession(6 days) = %d, want %d", userID, sessionExpiryUserID)
		}
	})

	t.Run("8-day-old session is rejected", func(t *testing.T) {
		context := newSessionContext(agedEightDays)
		if _, err := auth.GetUserIDFromSession(context); err == nil {
			t.Fatal("GetUserIDFromSession(8 days) accepted an expired session")
		}
	})
}

func ageSessionCookie(t *testing.T, original string, age time.Duration) string {
	t.Helper()

	raw, err := base64.URLEncoding.DecodeString(original)
	if err != nil {
		t.Fatalf("decode original cookie: %v", err)
	}
	parts := bytes.SplitN(raw, []byte("|"), 3)
	if len(parts) != 3 {
		t.Fatalf("original cookie has %d parts, want 3", len(parts))
	}
	payload := parts[1]
	newTimestamp := time.Now().UTC().Unix() - int64(age.Seconds())

	macInput := []byte(fmt.Sprintf("%s|%d|%s", sessionExpiryCookieName, newTimestamp, string(payload)))
	h := hmac.New(sha256.New, []byte(sessionExpiryTestSecret))
	h.Write(macInput)
	mac := h.Sum(nil)

	var aged bytes.Buffer
	aged.WriteString(strconv.FormatInt(newTimestamp, 10))
	aged.WriteByte('|')
	aged.Write(payload)
	aged.WriteByte('|')
	aged.Write(mac)

	return base64.URLEncoding.EncodeToString(aged.Bytes())
}

func newSessionContext(cookieValue string) echo.Context {
	e := echo.New()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionExpiryCookieName, Value: cookieValue})
	return e.NewContext(req, rec)
}
