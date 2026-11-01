package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Security regressions for account API tokens. These tests run against the
// isolated PostgreSQL acceptance harness and never use production data.

// TestAccountsSecurityAuthorScopedAdminTokenCannotReadOrChangeAnotherAuthorsPost
// was rewritten for ticket 22: personal tokens now live as access keys, so the
// author-scoped token is seeded as a legacy api_tokens row, moved by migration
// 0026, and then used against the agent API. The owner is an admin, so the
// role cap (agents.role_cap = author) is what keeps it at author level.
func TestAccountsSecurityAuthorScopedAdminTokenCannotReadOrChangeAnotherAuthorsPost(t *testing.T) {
	h := newAccessHarness(t)
	admin := tokTestUser(t, h, "accounts-scope-admin", "admin")
	other := tokTestUser(t, h, "accounts-scope-other", "author")
	authorScopeToken := apiKeysSeedToken(t, h, admin, "limited author token", []string{"author"}, nil, nil)
	apiKeysRunMigration(t, h)

	foreign := core.Post{
		Title: "Private other-author draft", Slug: "accounts-scope-foreign",
		Content: "original content", Type: "post", Status: "draft", AuthorID: other.ID,
	}
	if err := h.db.Create(&foreign).Error; err != nil {
		t.Fatalf("create foreign post fixture: %v", err)
	}

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		before := core.Post{}
		if err := h.db.First(&before, foreign.ID).Error; err != nil {
			t.Fatalf("reload fixture before %s: %v", method, err)
		}
		body := ""
		if method == http.MethodPut {
			body = fmt.Sprintf(`{"title":"attempted takeover","content":"changed","type":"post","status":"published","base_updated_at":%q}`,
				before.UpdatedAt.Format(time.RFC3339Nano))
		}
		res := agtDo(h, method, fmt.Sprintf("/api/v1/posts/%d", foreign.ID), body, authorScopeToken, "")
		if res.panicked != nil {
			t.Fatalf("migrated author-scoped token %s foreign post panicked: %v", method, res.panicked)
		}
		if res.code != http.StatusNotFound && res.code != http.StatusForbidden {
			t.Errorf("migrated author-scoped token %s foreign post returned %d, want 403 or 404; body=%q", method, res.code, res.body)
		}
		after := core.Post{}
		if err := h.db.First(&after, foreign.ID).Error; err != nil {
			t.Errorf("foreign post missing after denied %s: %v", method, err)
			continue
		}
		if after.Title != before.Title || after.Content != before.Content || after.Type != before.Type || after.Status != before.Status || after.AuthorID != before.AuthorID {
			t.Errorf("migrated author-scoped token %s changed foreign post: before=%+v after=%+v", method, before, after)
		}
	}

	own := core.Post{Title: "Own scoped draft", Slug: "accounts-scope-own", Content: "before", Type: "post", Status: "draft", AuthorID: admin.ID}
	if err := h.db.Create(&own).Error; err != nil {
		t.Fatalf("create own post fixture: %v", err)
	}
	if err := h.db.First(&own, own.ID).Error; err != nil {
		t.Fatalf("reload own post fixture: %v", err)
	}
	getOwn := agtDo(h, http.MethodGet, fmt.Sprintf("/api/v1/posts/%d", own.ID), "", authorScopeToken, "")
	agtExpectOK(t, getOwn, "migrated author-scoped token GET own post")
	updateBody := fmt.Sprintf(`{"title":"updated own","content":"after","type":"post","status":"draft","base_updated_at":%q}`, own.UpdatedAt.Format(time.RFC3339Nano))
	updateOwn := agtDo(h, http.MethodPut, fmt.Sprintf("/api/v1/posts/%d", own.ID), updateBody, authorScopeToken, "")
	if updateOwn.panicked != nil {
		t.Fatalf("migrated author-scoped token PUT own post panicked: %v", updateOwn.panicked)
	}
	if updateOwn.code < 200 || updateOwn.code >= 300 {
		t.Fatalf("migrated author-scoped token PUT own post returned %d, want 2xx; body=%q", updateOwn.code, updateOwn.body)
	}
	afterOwn := core.Post{}
	if err := h.db.First(&afterOwn, own.ID).Error; err != nil || afterOwn.Title != "updated own" || afterOwn.Content != "after" || afterOwn.AuthorID != admin.ID {
		t.Errorf("migrated author-scoped token did not update own post correctly: post=%+v err=%v", afterOwn, err)
	}
}

func TestAccountsSecurityRevokeOthersInvalidatesLegacyJWTWithoutSessionRow(t *testing.T) {
	h := newAccessHarness(t)
	u := tokTestUser(t, h, "accounts-legacy-revoke", "author")
	legacyJWT := h.token(u)
	currentJWT := tokTestLogin(t, h, u.Username)
	if n := acctCount(t, h, "SELECT COUNT(*) FROM login_sessions WHERE user_id = ? AND method = 'legacy'", u.ID); n != 0 {
		t.Fatalf("fixture legacy JWT already has %d session rows, want none", n)
	}

	revoke := tokTestCall(t, h, http.MethodPost, "/api/account/sessions/revoke-others", "", currentJWT)
	if revoke.code != http.StatusOK {
		t.Fatalf("POST /api/account/sessions/revoke-others returned %d, want 200; body=%q", revoke.code, revoke.body)
	}
	if current := tokTestCall(t, h, http.MethodGet, "/api/auth/me", "", currentJWT); current.code != http.StatusOK {
		t.Errorf("current session after revoke-others returned %d, want 200; body=%q", current.code, current.body)
	}
	if legacy := tokTestCall(t, h, http.MethodGet, "/api/auth/me", "", legacyJWT); legacy.code != http.StatusUnauthorized {
		t.Errorf("legacy JWT without a session row after revoke-others returned %d, want 401; body=%q", legacy.code, legacy.body)
	}
}

func TestAccountsSecurityPasswordResetBetweenPasswordCheckAndSessionIssueRejectsLogin(t *testing.T) {
	h := newAccessHarness(t)
	target := acctCreateUser(t, h, acctUserSpec{Username: "accounts-login-reset-race", Role: "author", Password: acctPassword})
	admin := acctCreateUser(t, h, acctUserSpec{Username: "accounts-login-reset-admin", Role: "admin"})
	adminToken := h.token(admin)
	reset := acctJSON(t, h, http.MethodPost, fmt.Sprintf("/api/admin/users/%d/reset-password", target.ID), "", adminToken)
	if !acctWantOK(t, reset, "POST admin reset-password for login race") {
		return
	}
	var link struct {
		ResetURL string `json:"reset_url"`
	}
	acctDecode(t, reset, "race reset response", &link)
	resetToken := acctTokenFromURL(t, link.ResetURL, "/password-reset/")

	type loginGateKey struct{}
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseBarrier := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseBarrier()
	var armed atomic.Bool
	armed.Store(true)
	callbackName := "accounts_security_pause_before_login_user_lock"
	if err := h.db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Context.Value(loginGateKey{}) != true || !armed.CompareAndSwap(true, false) {
			return
		}
		close(entered)
		<-release
	}); err != nil {
		t.Fatalf("register login race barrier: %v", err)
	}
	t.Cleanup(func() { _ = h.db.Callback().Query().Remove(callbackName) })

	loginDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		body := fmt.Sprintf(`{"username":%q,"password":%q}`, target.Username, acctPassword)
		req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
		req.Host = "127.0.0.1"
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		req.Header.Set(echo.HeaderOrigin, browserWriteOrigin)
		req.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
		req.Header.Set("X-Jelly-CSRF", "1")
		req = req.WithContext(context.WithValue(req.Context(), loginGateKey{}, true))
		rec := httptest.NewRecorder()
		h.echo.ServeHTTP(rec, req)
		loginDone <- rec
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		releaseBarrier()
		select {
		case login := <-loginDone:
			t.Fatalf("login did not reach the pre-user-lock barrier; it returned %d: %s", login.Code, login.Body.String())
		case <-time.After(3 * time.Second):
			t.Fatal("login did not reach the pre-user-lock barrier or return")
		}
	}

	consume := acctJSON(t, h, http.MethodPost, "/api/password-reset", fmt.Sprintf(`{"token":%q,"password":"race-reset-new-password"}`, resetToken), "")
	if !acctWant(t, consume, "consume reset link during in-flight login", http.StatusOK) {
		releaseBarrier()
		return
	}
	releaseBarrier()
	login := <-loginDone
	if login.Code == http.StatusOK {
		t.Errorf("login verified before reset returned 200 after reset completed: body=%q", login.Body.String())
	}
	if sessions := acctCount(t, h, "SELECT count(*) FROM login_sessions WHERE user_id = ? AND revoked_at IS NULL AND expires_at > now()", target.ID); sessions != 0 {
		t.Errorf("in-flight pre-reset login left %d active sessions after the reset", sessions)
	}
}

func accountsSecurityEnableTOTP(t *testing.T, h *accessHarness, user core.User) []string {
	t.Helper()
	token := h.token(user)
	enroll := acctJSON(t, h, http.MethodPost, "/api/account/2fa/enroll", "", token)
	if !acctWantOK(t, enroll, "POST /api/account/2fa/enroll for reset race") {
		t.FailNow()
	}
	var enrollment struct {
		Secret string `json:"secret"`
	}
	acctDecode(t, enroll, "reset race TOTP enrollment", &enrollment)
	verify := acctJSON(t, h, http.MethodPost, "/api/account/2fa/verify", fmt.Sprintf(`{"code":%q}`, auth.TOTPCodeAt(enrollment.Secret, time.Now())), token)
	if !acctWantOK(t, verify, "POST /api/account/2fa/verify for reset race") {
		t.FailNow()
	}
	var result struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	acctDecode(t, verify, "reset race recovery codes", &result)
	if len(result.RecoveryCodes) == 0 {
		t.Fatal("TOTP enrollment returned no recovery codes")
	}
	return result.RecoveryCodes
}

func accountsSecurityMFAToken(t *testing.T, h *accessHarness, username, password string) string {
	t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
	res := acctCall(t, h, acctRemote(), http.MethodPost, "/auth/login", body, "", map[string]string{echo.HeaderContentType: echo.MIMEApplicationJSON})
	if res.code != http.StatusOK {
		t.Fatalf("password step for %s returned %d, want 200; body=%q", username, res.code, res.body)
	}
	var response struct {
		MFAToken string `json:"mfa_token"`
	}
	if err := json.Unmarshal([]byte(res.body), &response); err != nil || response.MFAToken == "" {
		t.Fatalf("password step for %s returned no MFA token: err=%v body=%q", username, err, res.body)
	}
	return response.MFAToken
}

func TestAccountsSecurityPasswordResetBeforeMFATokenUserRefreshRejectsMFA(t *testing.T) {
	h := newAccessHarness(t)
	target := acctCreateUser(t, h, acctUserSpec{Username: "accounts-mfa-reset-race", Role: "author", Password: acctPassword})
	admin := acctCreateUser(t, h, acctUserSpec{Username: "accounts-mfa-reset-admin", Role: "admin"})
	recoveryCodes := accountsSecurityEnableTOTP(t, h, target)
	adminToken := h.token(admin)
	reset := acctJSON(t, h, http.MethodPost, fmt.Sprintf("/api/admin/users/%d/reset-password", target.ID), "", adminToken)
	if !acctWantOK(t, reset, "POST admin reset-password for MFA race") {
		return
	}
	var link struct {
		ResetURL string `json:"reset_url"`
	}
	acctDecode(t, reset, "MFA race reset response", &link)
	resetToken := acctTokenFromURL(t, link.ResetURL, "/password-reset/")

	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseBarrier := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseBarrier()
	var armed atomic.Bool
	armed.Store(true)
	callbackName := "accounts_security_pause_before_mfa_user_refresh"
	if err := h.db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table != "users" || tx.Statement.Dest == nil {
			return
		}
		_, ok := tx.Statement.Dest.(*core.User)
		locking, locked := tx.Statement.Clauses["FOR"].Expression.(clause.Locking)
		if !ok || !locked || locking.Strength != clause.LockingStrengthUpdate || !armed.CompareAndSwap(true, false) {
			return
		}
		close(entered)
		<-release
	}); err != nil {
		t.Fatalf("register MFA refresh barrier: %v", err)
	}
	t.Cleanup(func() { _ = h.db.Callback().Query().Remove(callbackName) })

	loginDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		body := fmt.Sprintf(`{"username":%q,"password":%q}`, target.Username, acctPassword)
		req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
		req.Host = "127.0.0.1"
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		req.Header.Set(echo.HeaderOrigin, browserWriteOrigin)
		req.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
		req.Header.Set("X-Jelly-CSRF", "1")
		rec := httptest.NewRecorder()
		h.echo.ServeHTTP(rec, req)
		loginDone <- rec
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		releaseBarrier()
		select {
		case login := <-loginDone:
			t.Fatalf("MFA login did not reach the pre-refresh user read barrier; login returned %d: %s", login.Code, login.Body.String())
		case <-time.After(3 * time.Second):
			t.Fatal("MFA login did not reach the pre-refresh user read barrier or return")
		}
	}
	consume := acctJSON(t, h, http.MethodPost, "/api/password-reset", fmt.Sprintf(`{"token":%q,"password":"mfa-race-reset-new-password"}`, resetToken), "")
	if !acctWant(t, consume, "consume reset link before MFA token user refresh", http.StatusOK) {
		releaseBarrier()
		return
	}
	releaseBarrier()
	login := <-loginDone
	if login.Code == http.StatusOK {
		var response struct {
			MFAToken string `json:"mfa_token"`
		}
		if err := json.Unmarshal(login.Body.Bytes(), &response); err != nil || response.MFAToken == "" {
			t.Errorf("post-reset MFA login returned 200 without a usable mfa_token: err=%v body=%q", err, login.Body.String())
		} else if complete := totpTestRecoveryStep(t, h, response.MFAToken, recoveryCodes[0]); complete.code != http.StatusUnauthorized {
			t.Errorf("MFA token issued after reset accepted a valid recovery code with status %d, want 401; body=%q", complete.code, complete.body)
		}
	}
	if sessions := acctCount(t, h, "SELECT count(*) FROM login_sessions WHERE user_id = ? AND revoked_at IS NULL AND expires_at > now()", target.ID); sessions != 0 {
		t.Errorf("reset left %d active sessions for the MFA race user", sessions)
	}
}
