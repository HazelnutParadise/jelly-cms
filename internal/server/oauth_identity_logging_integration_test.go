package server

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth"
	"gorm.io/gorm/logger"
)

const oauthIdentityLoggingLimit = 64 * 1024

type oauthIdentityLoggingBuffer struct {
	mu   sync.Mutex
	data []byte
}

func (b *oauthIdentityLoggingBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(p) >= oauthIdentityLoggingLimit {
		b.data = append(b.data[:0], p[len(p)-oauthIdentityLoggingLimit:]...)
		return len(p), nil
	}
	if len(b.data)+len(p) > oauthIdentityLoggingLimit {
		remove := len(b.data) + len(p) - oauthIdentityLoggingLimit
		copy(b.data, b.data[remove:])
		b.data = b.data[:len(b.data)-remove]
	}
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *oauthIdentityLoggingBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(append([]byte(nil), b.data...))
}

func oauthIdentityLoggingCapture(t *testing.T, h *accessHarness) *oauthIdentityLoggingBuffer {
	t.Helper()
	buffer := &oauthIdentityLoggingBuffer{}
	previous := h.db.Logger
	h.db.Logger = logger.New(log.New(buffer, "", 0), logger.Config{
		SlowThreshold:             200 * time.Millisecond,
		LogLevel:                  logger.Warn,
		IgnoreRecordNotFoundError: false,
		ParameterizedQueries:      false,
	})
	data.DB = h.db
	t.Cleanup(func() {
		h.db.Logger = previous
	})
	return buffer
}

func oauthIdentityLoggingAssertNoMarker(t *testing.T, buffer *oauthIdentityLoggingBuffer, marker, route string) {
	t.Helper()
	if strings.Contains(buffer.String(), marker) {
		t.Errorf("GORM SQL logger leaked sensitive identity marker %q during %s", marker, route)
	}
}

func TestOAuthIdentityLogging(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthRecoveryJWTSecret)
	t.Setenv("APP_ENV", "test")

	t.Run("normal-new-subject-success", func(t *testing.T) {
		h := oauthTxnPrepareHarness(t)
		provider := newOAuthTxnIntegrationProvider("google")
		provider.user = goth.User{Provider: "google", UserID: "oauth-subject-log-success-7d2f", Email: "logging-success@example.test", Name: "Logging Success", NickName: "logging-success"}
		resetOAuthInputProviders(provider)
		entry := oauthIdentityBegin(t, h, "google", "/account")
		buffer := oauthIdentityLoggingCapture(t, h)
		callback := oauthIdentityCallback(h, entry, "logging-success-code", echo.MIMEApplicationJSON)
		if callback.Code != http.StatusOK {
			t.Fatalf("normal OAuth callback returned %d, want 200: %s", callback.Code, callback.Body.String())
		}
		oauthIdentityLoggingAssertNoMarker(t, buffer, provider.user.UserID, "normal OAuth login")
		token, userID := oauthIdentityJSONLogin(t, callback)
		if got := h.request(http.MethodGet, "/api/auth/me", "", token, echo.MIMEApplicationJSON, ""); got.code != http.StatusOK {
			t.Errorf("authenticated user readback returned %d, want 200: %s", got.code, got.body)
		}
		var identities int64
		if err := h.db.Table("oauth_identities").Where("user_id = ? AND provider = ? AND subject = ? AND deleted_at IS NULL", userID, "google", provider.user.UserID).Count(&identities).Error; err != nil || identities != 1 {
			t.Errorf("successful callback identity rows=%d, err=%v; want one linked row", identities, err)
		}
	})

	t.Run("self-link-success", func(t *testing.T) {
		h := oauthTxnPrepareHarness(t)
		provider := newOAuthTxnIntegrationProvider("google")
		provider.user = goth.User{Provider: "google", UserID: "oauth-subject-log-link-b931", Email: "link-subject@example.test", Name: "Link Subject"}
		resetOAuthInputProviders(provider)
		user := h.user("author", "oauth-logging-link")
		login := oauthAccountLoginFor(t, h, user)
		buffer := oauthIdentityLoggingCapture(t, h)
		entry := oauthAccountBeginLink(t, h, provider, login, "")
		callback := oauthAccountCallback(h, entry, "logging-link-code", true)
		if callback.Code != http.StatusOK {
			t.Fatalf("self-link callback returned %d, want 200: %s", callback.Code, callback.Body.String())
		}
		oauthIdentityLoggingAssertNoMarker(t, buffer, provider.user.UserID, "self-link")
		oauthIdentityAssertNoCredentials(t, callback)
		var identities int64
		if err := h.db.Table("oauth_identities").Where("user_id = ? AND provider = ? AND subject = ? AND deleted_at IS NULL", user.ID, "google", provider.user.UserID).Count(&identities).Error; err != nil || identities != 1 {
			t.Errorf("self-link identity rows=%d, err=%v; want one associated identity", identities, err)
		}
	})

	t.Run("recovery-success", func(t *testing.T) {
		h := oauthTxnPrepareHarness(t)
		legacy := oauthRecoveryCreateLegacyUser(t, h, "google", "admin")
		grant := oauthRecoveryGrant(t, legacy.ID, "google", time.Now().Add(9*time.Minute), nil)
		provider := newOAuthTxnIntegrationProvider("google")
		provider.user = goth.User{Provider: "google", UserID: "oauth-subject-log-recovery-f625", Email: "changed-recovery@example.test", Name: "Recovered Subject"}
		resetOAuthInputProviders(provider)
		buffer := oauthIdentityLoggingCapture(t, h)
		pending := oauthRecoveryBegin(t, h, grant)
		callback := oauthRecoveryCallback(h, "google", pending, "logging-recovery-code", nil, echo.MIMEApplicationJSON)
		if callback.Code != http.StatusOK {
			t.Fatalf("recovery callback returned %d, want 200: %s", callback.Code, callback.Body.String())
		}
		oauthIdentityLoggingAssertNoMarker(t, buffer, provider.user.UserID, "OAuth recovery")
		var payload struct {
			Token string    `json:"token"`
			User  core.User `json:"user"`
		}
		if err := json.Unmarshal(callback.Body.Bytes(), &payload); err != nil || payload.Token == "" || payload.User.ID != legacy.ID {
			t.Fatalf("recovery response token/user mismatch: user=%d expected=%d err=%v", payload.User.ID, legacy.ID, err)
		}
		var identities int64
		if err := h.db.Table("oauth_identities").Where("user_id = ? AND provider = ? AND subject = ? AND deleted_at IS NULL", legacy.ID, "google", provider.user.UserID).Count(&identities).Error; err != nil || identities != 1 {
			t.Errorf("recovery identity rows=%d, err=%v; want one associated identity", identities, err)
		}
	})

	for _, route := range []string{"normal-signup", "self-link", "recovery"} {
		t.Run("write-error-no-subject-leak-"+route, func(t *testing.T) {
			h := oauthTxnPrepareHarness(t)
			marker := "oauth-subject-log-trigger-" + route + "-c814"
			const triggerName = "oauth_identity_logging_test_reject_insert"
			const functionName = "oauth_identity_logging_test_reject_insert_fn"
			triggerSQL := `CREATE FUNCTION ` + functionName + `() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'rejected subject %', NEW.subject; END; $$`
			if err := h.db.Exec(triggerSQL).Error; err != nil {
				t.Fatalf("create identity failure function: %v", err)
			}
			if err := h.db.Exec(`CREATE TRIGGER ` + triggerName + ` BEFORE INSERT ON oauth_identities FOR EACH ROW EXECUTE FUNCTION ` + functionName + `()`).Error; err != nil {
				t.Fatalf("create identity failure trigger: %v", err)
			}
			t.Cleanup(func() {
				if err := h.db.Exec(`DROP TRIGGER IF EXISTS ` + triggerName + ` ON oauth_identities`).Error; err != nil {
					t.Errorf("drop identity failure trigger: %v", err)
				}
				if err := h.db.Exec(`DROP FUNCTION IF EXISTS ` + functionName + `()`).Error; err != nil {
					t.Errorf("drop identity failure function: %v", err)
				}
			})
			probe := h.db.Begin()
			if probe.Error != nil {
				t.Fatalf("begin trigger probe: %v", probe.Error)
			}
			probeUser := h.user("author", "oauth-log-probe-"+route)
			probeErr := probe.Create(&core.OAuthIdentity{UserID: probeUser.ID, Provider: "google", Subject: marker}).Error
			if probeErr == nil || !strings.Contains(probeErr.Error(), marker) {
				_ = probe.Rollback().Error
				t.Fatalf("test trigger error did not include subject marker: %v", probeErr)
			}
			if err := probe.Rollback().Error; err != nil {
				t.Fatalf("rollback trigger probe: %v", err)
			}

			provider := newOAuthTxnIntegrationProvider("google")
			provider.user = goth.User{Provider: "google", UserID: marker, Email: "trigger-" + route + "@example.test", Name: "Trigger Subject"}
			resetOAuthInputProviders(provider)
			var existing core.User
			var login oauthAccountLogin
			var grant string
			switch route {
			case "self-link":
				existing = h.user("author", "oauth-failing-link")
				login = oauthAccountLoginFor(t, h, existing)
			case "recovery":
				existing = oauthRecoveryCreateLegacyUser(t, h, "google", "admin")
				grant = oauthRecoveryGrant(t, existing.ID, "google", time.Now().Add(9*time.Minute), nil)
			}
			usersBefore := accessUsers(t, h.db)
			buffer := oauthIdentityLoggingCapture(t, h)
			var response *httptest.ResponseRecorder
			switch route {
			case "normal-signup":
				entry := oauthIdentityBegin(t, h, "google", "/account")
				response = oauthIdentityCallback(h, entry, "logging-failing-signup", echo.MIMEApplicationJSON)
			case "self-link":
				entry := oauthAccountBeginLink(t, h, provider, login, "")
				response = oauthAccountCallback(h, entry, "logging-failing-link", true)
			case "recovery":
				pending := oauthRecoveryBegin(t, h, grant)
				response = oauthRecoveryCallback(h, "google", pending, "logging-failing-recovery", nil, echo.MIMEApplicationJSON)
			}
			if response.Code != http.StatusServiceUnavailable {
				t.Errorf("%s callback returned %d, want 503 for identity write failure: %s", route, response.Code, response.Body.String())
			}
			oauthIdentityLoggingAssertNoMarker(t, buffer, marker, route+" failed identity write")
			oauthIdentityAssertNoCredentials(t, response)
			assertOAuthRecoveryNoCredential(t, response)
			if got := accessUsers(t, h.db); len(got) != len(usersBefore) {
				t.Errorf("%s identity write failure changed user count from %d to %d", route, len(usersBefore), len(got))
			}
			var identities int64
			if err := h.db.Table("oauth_identities").Where("provider = ? AND subject = ? AND deleted_at IS NULL", "google", marker).Count(&identities).Error; err != nil || identities != 0 {
				t.Errorf("%s identity write failure left %d associated identity rows, err=%v", route, identities, err)
			}
		})
	}
}
