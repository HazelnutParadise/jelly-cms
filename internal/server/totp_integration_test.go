package server

import (
	"bytes"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth"
)

// Acceptance tests for two-step verification (docs/tickets/06-accounts.md, D6:
// S6 to S10, S22, 登入兩步驟, OAuth 不再要求驗證碼). Login and token fixtures
// come from the tokTest helpers.

const (
	totpTestSessionSecret = "totp-integration-session-secret-0123456789-abcdef"
	totpTestJWTSecret     = "totp-integration-jwt-secret-0123456789-abcdef"
)

var totpBase32Shape = regexp.MustCompile(`^[A-Z2-7]+=*$`)

// totpTestCode returns the code for the time slice `steps` slices from now.
func totpTestCode(secret string, steps int) string {
	return auth.TOTPCodeAt(secret, time.Now().Add(time.Duration(steps)*30*time.Second))
}

// totpTestWrongCode returns a six-digit code that is valid in no slice the
// server accepts around now.
func totpTestWrongCode(secret string) string {
	valid := map[string]bool{}
	for steps := -1; steps <= 1; steps++ {
		valid[totpTestCode(secret, steps)] = true
	}
	for _, candidate := range []string{"000000", "111111", "222222"} {
		if !valid[candidate] {
			return candidate
		}
	}
	return "999999"
}

// totpTestEnable runs S7 and S8 for username and returns the secret, the JWT
// that completed the enrollment, and the ten recovery codes.
func totpTestEnable(t *testing.T, h *accessHarness, username string) (string, string, []string) {
	t.Helper()
	jwt := tokTestLogin(t, h, username)
	enroll := tokTestCall(t, h, http.MethodPost, "/api/account/2fa/enroll", "", jwt)
	if enroll.code < 200 || enroll.code >= 300 {
		t.Fatalf("POST /api/account/2fa/enroll returned %d, want 2xx; body=%q", enroll.code, enroll.body)
	}
	var enrolled struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal([]byte(enroll.body), &enrolled); err != nil || enrolled.Secret == "" {
		t.Fatalf("enroll response has no secret: err=%v body=%q", err, enroll.body)
	}
	verify := tokTestCall(t, h, http.MethodPost, "/api/account/2fa/verify", fmt.Sprintf(`{"code":%q}`, auth.TOTPCodeAt(enrolled.Secret, time.Now())), jwt)
	if verify.code != http.StatusOK {
		t.Fatalf("POST /api/account/2fa/verify returned %d, want 200; body=%q", verify.code, verify.body)
	}
	var verified struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal([]byte(verify.body), &verified); err != nil || len(verified.RecoveryCodes) != 10 {
		t.Fatalf("verify response recovery_codes = %d entries, want 10; err=%v body=%q", len(verified.RecoveryCodes), err, verify.body)
	}
	return enrolled.Secret, jwt, verified.RecoveryCodes
}

// totpTestPasswordLogin posts the password step of /auth/login.
func totpTestPasswordLogin(t *testing.T, h *accessHarness, username, password string) accessResult {
	t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
	return tokTestReq(t, h, tokTestRemote(), http.MethodPost, "/auth/login", body, "", nil)
}

// totpTestMFAToken runs the password step for a user with two-step verification
// enabled and returns the mfa_token that the step must issue.
func totpTestMFAToken(t *testing.T, h *accessHarness, username string) string {
	t.Helper()
	res := totpTestPasswordLogin(t, h, username, tokTestPassword)
	if res.code != http.StatusOK {
		t.Fatalf("password step for %s returned %d, want 200; body=%q", username, res.code, res.body)
	}
	var payload struct {
		MFARequired bool   `json:"mfa_required"`
		MFAToken    string `json:"mfa_token"`
	}
	if err := json.Unmarshal([]byte(res.body), &payload); err != nil || !payload.MFARequired || payload.MFAToken == "" {
		t.Fatalf("password step for %s did not require a second step: err=%v body=%q", username, err, res.body)
	}
	return payload.MFAToken
}

// totpTestSecondStep posts /auth/login/2fa with a six-digit code.
func totpTestSecondStep(t *testing.T, h *accessHarness, mfaToken, code string) accessResult {
	t.Helper()
	body := fmt.Sprintf(`{"mfa_token":%q,"code":%q}`, mfaToken, code)
	return tokTestReq(t, h, tokTestRemote(), http.MethodPost, "/auth/login/2fa", body, "", nil)
}

// totpTestRecoveryStep posts /auth/login/2fa with a recovery code.
func totpTestRecoveryStep(t *testing.T, h *accessHarness, mfaToken, recovery string) accessResult {
	t.Helper()
	body := fmt.Sprintf(`{"mfa_token":%q,"recovery_code":%q}`, mfaToken, recovery)
	return tokTestReq(t, h, tokTestRemote(), http.MethodPost, "/auth/login/2fa", body, "", nil)
}

// totpTestLoginToken decodes the JWT that a successful second step issues.
func totpTestLoginToken(t *testing.T, res accessResult) string {
	t.Helper()
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(res.body), &payload); err != nil || payload.Token == "" {
		t.Fatalf("second step returned no token: err=%v body=%q", err, res.body)
	}
	return payload.Token
}

// totpTestS6 reads the S6 status with the given JWT.
func totpTestS6(t *testing.T, h *accessHarness, jwt string) (enabled bool, remaining int) {
	t.Helper()
	res := tokTestCall(t, h, http.MethodGet, "/api/account/2fa", "", jwt)
	if res.code != http.StatusOK {
		t.Fatalf("GET /api/account/2fa returned %d, want 200; body=%q", res.code, res.body)
	}
	var status struct {
		Enabled           bool `json:"enabled"`
		RecoveryRemaining int  `json:"recovery_remaining"`
	}
	if err := json.Unmarshal([]byte(res.body), &status); err != nil {
		t.Fatalf("decode GET /api/account/2fa: %v body=%q", err, res.body)
	}
	return status.Enabled, status.RecoveryRemaining
}

// --- S6 to S8: enrollment ----------------------------------------------------

func TestTOTPEnrollReturnsSecretAndQRThenVerifyEnables(t *testing.T) {
	h := newAccessHarness(t)
	u := tokTestUser(t, h, "totp-enroll-user", "author")
	jwt := tokTestLogin(t, h, u.Username)

	if enabled, _ := totpTestS6(t, h, jwt); enabled {
		t.Errorf("S6 reports enabled before any enrollment")
	}

	enroll := tokTestCall(t, h, http.MethodPost, "/api/account/2fa/enroll", "", jwt)
	if enroll.code < 200 || enroll.code >= 300 {
		t.Fatalf("S7 POST /api/account/2fa/enroll returned %d, want 2xx; body=%q", enroll.code, enroll.body)
	}
	var enrolled struct {
		Secret     string `json:"secret"`
		OTPAuthURI string `json:"otpauth_uri"`
		QRSVG      string `json:"qr_svg"`
	}
	if err := json.Unmarshal([]byte(enroll.body), &enrolled); err != nil {
		t.Fatalf("decode S7 response: %v body=%q", err, enroll.body)
	}
	if !totpBase32Shape.MatchString(enrolled.Secret) {
		t.Errorf("secret = %q, want base32", enrolled.Secret)
	}
	if !strings.HasPrefix(enrolled.OTPAuthURI, "otpauth://totp/") {
		t.Errorf("otpauth_uri = %q, want the otpauth://totp/ scheme", enrolled.OTPAuthURI)
	}
	if !strings.Contains(enrolled.QRSVG, "<svg") {
		t.Errorf("qr_svg does not contain an <svg> element")
	}

	verify := tokTestCall(t, h, http.MethodPost, "/api/account/2fa/verify", fmt.Sprintf(`{"code":%q}`, auth.TOTPCodeAt(enrolled.Secret, time.Now())), jwt)
	if verify.code != http.StatusOK {
		t.Fatalf("S8 POST /api/account/2fa/verify returned %d, want 200; body=%q", verify.code, verify.body)
	}
	var verified struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal([]byte(verify.body), &verified); err != nil || len(verified.RecoveryCodes) != 10 {
		t.Errorf("S8 recovery_codes = %d entries, want 10; err=%v body=%q", len(verified.RecoveryCodes), err, verify.body)
	}

	enabled, remaining := totpTestS6(t, h, jwt)
	if !enabled || remaining != 10 {
		t.Errorf("S6 after verify = enabled %v recovery_remaining %d, want true and 10", enabled, remaining)
	}
}

// --- login step ---------------------------------------------------------------

func TestTOTPLoginRequiresSecondStep(t *testing.T) {
	h := newAccessHarness(t)
	u := tokTestUser(t, h, "totp-login-user", "author")
	secret, _, _ := totpTestEnable(t, h, u.Username)

	first := totpTestPasswordLogin(t, h, u.Username, tokTestPassword)
	if first.code != http.StatusOK {
		t.Fatalf("password step returned %d, want 200; body=%q", first.code, first.body)
	}
	var step1 map[string]any
	if err := json.Unmarshal([]byte(first.body), &step1); err != nil {
		t.Fatalf("decode password step: %v body=%q", err, first.body)
	}
	if step1["mfa_required"] != true {
		t.Errorf("password step mfa_required = %v, want true", step1["mfa_required"])
	}
	if mfa, _ := step1["mfa_token"].(string); mfa == "" {
		t.Errorf("password step returned no mfa_token: %q", first.body)
	}
	if tok, ok := step1["token"]; ok && tok != nil && tok != "" {
		t.Errorf("password step issued a JWT before the second step")
	}
	for _, cookie := range first.header.Values("Set-Cookie") {
		if strings.HasPrefix(cookie, "jwt_token=") || strings.HasPrefix(cookie, "jelly-cms-session=") {
			t.Errorf("password step set a login cookie before the second step: %q", cookie)
		}
	}

	mfa, _ := step1["mfa_token"].(string)
	second := totpTestSecondStep(t, h, mfa, totpTestCode(secret, 1))
	if second.code != http.StatusOK {
		t.Fatalf("POST /auth/login/2fa with a valid code returned %d, want 200; body=%q", second.code, second.body)
	}
	issued := totpTestLoginToken(t, second)
	get := tokTestCall(t, h, http.MethodGet, "/api/admin/posts?type=post", "", issued)
	if get.code != http.StatusOK {
		t.Errorf("GET /api/admin/posts with the issued login returned %d, want 200; body=%q", get.code, get.body)
	}
}

// --- lockout -------------------------------------------------------------------

func TestTOTPWrongCodeLocksAfterFiveFailures(t *testing.T) {
	h := newAccessHarness(t)
	u := tokTestUser(t, h, "totp-lock-user", "author")
	secret, _, _ := totpTestEnable(t, h, u.Username)
	wrong := totpTestWrongCode(secret)

	for attempt := 1; attempt <= 5; attempt++ {
		mfa := totpTestMFAToken(t, h, u.Username)
		if r := totpTestSecondStep(t, h, mfa, wrong); r.code != http.StatusUnauthorized {
			t.Errorf("wrong code attempt %d returned %d, want 401; body=%q", attempt, r.code, r.body)
		}
	}

	mfa := totpTestMFAToken(t, h, u.Username)
	if r := totpTestSecondStep(t, h, mfa, totpTestCode(secret, 1)); r.code != http.StatusTooManyRequests {
		t.Errorf("valid code during the lock returned %d, want 429; body=%q", r.code, r.body)
	}

	var row core.UserTOTP
	if err := h.db.Where("user_id = ?", u.ID).First(&row).Error; err != nil {
		t.Fatalf("load user_totp: %v", err)
	}
	if row.LockedUntil == nil || row.LockedUntil.Before(time.Now().Add(14*time.Minute)) {
		t.Errorf("locked_until = %v, want about 15 minutes from now", row.LockedUntil)
	}
}

// --- replay ----------------------------------------------------------------------

func TestTOTPCodeReplaySameStepRejected(t *testing.T) {
	h := newAccessHarness(t)
	u := tokTestUser(t, h, "totp-replay-user", "author")
	secret, _, _ := totpTestEnable(t, h, u.Username)

	// The enrollment verification consumed the current slice.
	if r := totpTestSecondStep(t, h, totpTestMFAToken(t, h, u.Username), totpTestCode(secret, 0)); r.code != http.StatusUnauthorized {
		t.Errorf("code of the slice used at enrollment returned %d, want 401; body=%q", r.code, r.body)
	}
	next := totpTestCode(secret, 1)
	if r := totpTestSecondStep(t, h, totpTestMFAToken(t, h, u.Username), next); r.code != http.StatusOK {
		t.Fatalf("code of the next slice returned %d, want 200; body=%q", r.code, r.body)
	}
	if r := totpTestSecondStep(t, h, totpTestMFAToken(t, h, u.Username), next); r.code != http.StatusUnauthorized {
		t.Errorf("reused code of the same slice returned %d, want 401; body=%q", r.code, r.body)
	}
}

func TestTOTPWrongCodeDoesNotRollBackLastAcceptedStep(t *testing.T) {
	h := newAccessHarness(t)
	u := tokTestUser(t, h, "totp-last-step-stable", "author")
	secret, _, _ := totpTestEnable(t, h, u.Username)
	var before core.UserTOTP
	if err := h.db.Where("user_id = ?", u.ID).First(&before).Error; err != nil {
		t.Fatalf("load enabled TOTP row: %v", err)
	}
	if before.LastStep <= 0 {
		t.Fatalf("enrollment LastStep=%d, want a consumed TOTP step", before.LastStep)
	}
	wrongMFA := totpTestMFAToken(t, h, u.Username)
	if r := totpTestSecondStep(t, h, wrongMFA, totpTestWrongCode(secret)); r.code != http.StatusUnauthorized {
		t.Fatalf("wrong-code attempt returned %d, want 401; body=%q", r.code, r.body)
	}
	reuseMFA := totpTestMFAToken(t, h, u.Username)
	consumedCode := auth.TOTPCodeAt(secret, time.Unix(before.LastStep*30, 0))
	if r := totpTestSecondStep(t, h, reuseMFA, consumedCode); r.code != http.StatusUnauthorized {
		t.Errorf("code consumed during enrollment returned %d after an incorrect attempt, want 401; body=%q", r.code, r.body)
	}
	var after core.UserTOTP
	if err := h.db.Where("user_id = ?", u.ID).First(&after).Error; err != nil {
		t.Fatalf("reload enabled TOTP row: %v", err)
	}
	if after.LastStep != before.LastStep {
		t.Errorf("wrong-code attempt changed LastStep from %d to %d", before.LastStep, after.LastStep)
	}
}

// --- mfa_token rules ---------------------------------------------------------

func TestTOTPMFATokenSingleUseExpiredAndCrossUserRejected(t *testing.T) {
	// The expired-token case re-signs a real mfa_token with the configured JWT
	// secret, so the secret must be fixed before the harness starts auth.
	t.Setenv("APP_ENV", "test")
	t.Setenv("SESSION_SECRET", totpTestSessionSecret)
	t.Setenv("JWT_SECRET", totpTestJWTSecret)
	h := newAccessHarness(t)

	userA := tokTestUser(t, h, "totp-mfa-a", "author")
	userB := tokTestUser(t, h, "totp-mfa-b", "author")
	secretA, _, recoveryA := totpTestEnable(t, h, userA.Username)
	secretB, _, _ := totpTestEnable(t, h, userB.Username)

	// Single use: a token that completed a login cannot be reused.
	mfaA := totpTestMFAToken(t, h, userA.Username)
	if r := totpTestSecondStep(t, h, mfaA, totpTestCode(secretA, 1)); r.code != http.StatusOK {
		t.Fatalf("first use of mfa_token returned %d, want 200; body=%q", r.code, r.body)
	}
	if r := totpTestRecoveryStep(t, h, mfaA, recoveryA[0]); r.code != http.StatusUnauthorized {
		t.Errorf("reuse of a consumed mfa_token with a valid recovery code returned %d, want 401; body=%q", r.code, r.body)
	}

	// Cross user: a token issued to B does not accept A's code, and the reverse.
	mfaB := totpTestMFAToken(t, h, userB.Username)
	if r := totpTestSecondStep(t, h, mfaB, totpTestCode(secretA, 2)); r.code != http.StatusUnauthorized {
		t.Errorf("B's mfa_token with A's code returned %d, want 401; body=%q", r.code, r.body)
	}
	mfaA2 := totpTestMFAToken(t, h, userA.Username)
	if r := totpTestSecondStep(t, h, mfaA2, totpTestCode(secretB, 1)); r.code != http.StatusUnauthorized {
		t.Errorf("A's mfa_token with B's code returned %d, want 401; body=%q", r.code, r.body)
	}

	// Expiry: re-sign a fresh token with exp in the past. The control token,
	// re-signed with its real exp, must still work so the signing key is proven.
	control := totpTestMFAToken(t, h, userB.Username)
	if r := totpTestSecondStep(t, h, resignMFATokenWithOriginalExpiry(t, control), totpTestCode(secretB, 1)); r.code != http.StatusOK {
		t.Fatalf("control: re-signed mfa_token with its original expiry returned %d, want 200; body=%q", r.code, r.body)
	}
	expired := totpTestMFAToken(t, h, userB.Username)
	if r := totpTestSecondStep(t, h, resignMFAToken(t, expired, time.Now().Add(-time.Minute)), totpTestCode(secretB, 2)); r.code != http.StatusUnauthorized {
		t.Errorf("expired mfa_token returned %d, want 401; body=%q", r.code, r.body)
	}
}

func resignMFATokenWithOriginalExpiry(t *testing.T, token string) string {
	t.Helper()
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
		t.Fatalf("parse mfa_token claims: %v", err)
	}
	exp, err := claims.GetExpirationTime()
	if err != nil || exp == nil {
		t.Fatalf("mfa_token has no valid exp claim: exp=%v err=%v", claims["exp"], err)
	}
	return resignMFAToken(t, token, exp.Time)
}

func TestTOTPMFATokenIsNotBearerAndConcurrentRecoveryUseIsSingleUse(t *testing.T) {
	h := newAccessHarness(t)
	u := tokTestUser(t, h, "totp-mfa-concurrent", "author")
	_, _, recoveryCodes := totpTestEnable(t, h, u.Username)
	mfa := totpTestMFAToken(t, h, u.Username)

	if r := tokTestCall(t, h, http.MethodGet, "/api/account/profile", "", mfa); r.code != http.StatusUnauthorized {
		t.Errorf("GET /api/account/profile with an mfa_token returned %d, want 401; body=%q", r.code, r.body)
	}

	results := make(chan accessResult, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- totpTestRecoveryStep(t, h, mfa, recoveryCodes[0])
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	successes, rejected := 0, 0
	for r := range results {
		switch r.code {
		case http.StatusOK:
			successes++
		case http.StatusUnauthorized:
			rejected++
		default:
			t.Errorf("concurrent recovery login returned %d, want exactly one 200 and one 401; body=%q", r.code, r.body)
		}
	}
	if successes != 1 || rejected != 1 {
		t.Errorf("concurrent use of one mfa_token and recovery code: successes=%d rejected=%d, want 1 each", successes, rejected)
	}
}

// resignMFAToken replaces exp of an mfa_token and signs it again with the
// configured JWT secret. The other claims stay as the server issued them.
func resignMFAToken(t *testing.T, token string, exp time.Time) string {
	t.Helper()
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
		t.Fatalf("parse mfa_token claims: %v", err)
	}
	claims["exp"] = exp.Unix()
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(totpTestJWTSecret))
	if err != nil {
		t.Fatalf("sign mfa_token: %v", err)
	}
	return signed
}

// --- recovery codes -----------------------------------------------------------

func TestTOTPRecoveryCodeWorksOnceAndCountDrops(t *testing.T) {
	h := newAccessHarness(t)
	u := tokTestUser(t, h, "totp-recovery-user", "author")
	_, _, codes := totpTestEnable(t, h, u.Username)
	if len(codes) != 10 {
		t.Fatalf("enrollment issued %d recovery codes, want 10", len(codes))
	}

	first := totpTestRecoveryStep(t, h, totpTestMFAToken(t, h, u.Username), codes[0])
	if first.code != http.StatusOK {
		t.Fatalf("recovery code login returned %d, want 200; body=%q", first.code, first.body)
	}
	jwt := totpTestLoginToken(t, first)
	if enabled, remaining := totpTestS6(t, h, jwt); !enabled || remaining != 9 {
		t.Errorf("S6 after one recovery login = enabled %v recovery_remaining %d, want true and 9", enabled, remaining)
	}
	if r := totpTestRecoveryStep(t, h, totpTestMFAToken(t, h, u.Username), codes[0]); r.code != http.StatusUnauthorized {
		t.Errorf("reused recovery code returned %d, want 401; body=%q", r.code, r.body)
	}
}

func TestTOTPRegenerateInvalidatesOldCodes(t *testing.T) {
	h := newAccessHarness(t)
	u := tokTestUser(t, h, "totp-regen-user", "author")
	secret, jwt, oldCodes := totpTestEnable(t, h, u.Username)

	regen := tokTestCall(t, h, http.MethodPost, "/api/account/2fa/recovery-codes", fmt.Sprintf(`{"code":%q}`, totpTestCode(secret, 1)), jwt)
	if regen.code < 200 || regen.code >= 300 {
		t.Fatalf("S10 regenerate returned %d, want 2xx; body=%q", regen.code, regen.body)
	}
	var regenerated struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal([]byte(regen.body), &regenerated); err != nil || len(regenerated.RecoveryCodes) != 10 {
		t.Fatalf("S10 response recovery_codes = %d entries, want 10; err=%v body=%q", len(regenerated.RecoveryCodes), err, regen.body)
	}
	if r := totpTestRecoveryStep(t, h, totpTestMFAToken(t, h, u.Username), oldCodes[0]); r.code != http.StatusUnauthorized {
		t.Errorf("code from before regeneration returned %d, want 401; body=%q", r.code, r.body)
	}
	if r := totpTestRecoveryStep(t, h, totpTestMFAToken(t, h, u.Username), regenerated.RecoveryCodes[0]); r.code != http.StatusOK {
		t.Errorf("code from the new set returned %d, want 200; body=%q", r.code, r.body)
	}
}

// --- S9: disable --------------------------------------------------------------

func TestTOTPDisableNeedsPasswordAndCode(t *testing.T) {
	h := newAccessHarness(t)
	u := tokTestUser(t, h, "totp-disable-user", "author")
	secret, jwt, _ := totpTestEnable(t, h, u.Username)
	disable := func(password, code string) accessResult {
		body := fmt.Sprintf(`{"current_password":%q,"code":%q}`, password, code)
		return tokTestCall(t, h, http.MethodPost, "/api/account/2fa/disable", body, jwt)
	}

	if r := disable("not-the-password-1", totpTestCode(secret, 1)); r.code < 400 {
		t.Errorf("disable with a wrong password returned %d, want an error; body=%q", r.code, r.body)
	}
	if enabled, _ := totpTestS6(t, h, jwt); !enabled {
		t.Fatalf("disable with a wrong password turned two-step verification off")
	}
	if r := disable(tokTestPassword, totpTestWrongCode(secret)); r.code < 400 {
		t.Errorf("disable with a wrong code returned %d, want an error; body=%q", r.code, r.body)
	}
	if enabled, _ := totpTestS6(t, h, jwt); !enabled {
		t.Fatalf("disable with a wrong code turned two-step verification off")
	}
	if r := disable(tokTestPassword, totpTestCode(secret, 1)); r.code != http.StatusOK {
		t.Fatalf("disable with password and code returned %d, want 200; body=%q", r.code, r.body)
	}
	if enabled, _ := totpTestS6(t, h, jwt); enabled {
		t.Errorf("S6 still reports enabled after disable")
	}
	after := totpTestPasswordLogin(t, h, u.Username, tokTestPassword)
	if after.code != http.StatusOK || strings.Contains(after.body, "mfa_required") {
		t.Errorf("password login after disable = %d %q, want a direct login", after.code, after.body)
	}
}

// --- storage ----------------------------------------------------------------------

func TestTOTPSecretStoredEncrypted(t *testing.T) {
	h := newAccessHarness(t)
	u := tokTestUser(t, h, "totp-encrypted-user", "author")
	secret, _, _ := totpTestEnable(t, h, u.Username)

	var row core.UserTOTP
	if err := h.db.Where("user_id = ?", u.ID).First(&row).Error; err != nil {
		t.Fatalf("load user_totp: %v", err)
	}
	if len(row.SecretEnc) == 0 {
		t.Fatalf("secret_enc is empty")
	}
	if bytes.Contains(row.SecretEnc, []byte(secret)) {
		t.Errorf("secret_enc contains the base32 secret in plain text")
	}
	if raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.TrimRight(secret, "=")); err == nil && bytes.Contains(row.SecretEnc, raw) {
		t.Errorf("secret_enc contains the decoded secret bytes")
	}
}

// --- OAuth trusts the provider ------------------------------------------------

func TestTOTPOAuthLoginSkipsSecondStep(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)

	u := tokTestUser(t, h, "totp-oauth-user", "author")
	totpTestEnable(t, h, u.Username)

	const subject = "totp-oauth-subject"
	if err := h.db.Exec("INSERT INTO oauth_identities (user_id, provider, subject) VALUES (?, 'google', ?)", u.ID, subject).Error; err != nil {
		t.Fatalf("link google identity to the two-step account: %v", err)
	}
	provider := newOAuthTxnIntegrationProvider("google")
	provider.user = goth.User{UserID: subject, Email: u.Email, Name: "TOTP OAuth", Provider: "google"}
	resetOAuthInputProviders(provider)

	entry := oauthIdentityBegin(t, h, "google", "/admin/posts")
	callback := oauthIdentityCallback(h, entry, "totp-oauth-code", echo.MIMEApplicationJSON)
	if callback.Code != http.StatusOK {
		t.Fatalf("OAuth JSON callback returned %d, want 200; body=%q", callback.Code, callback.Body.String())
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(callback.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode OAuth callback: %v", err)
	}
	if _, ok := payload["mfa_required"]; ok {
		t.Errorf("OAuth callback asked for a second step: %s", callback.Body.String())
	}
	var token string
	if err := json.Unmarshal(payload["token"], &token); err != nil || token == "" {
		t.Errorf("OAuth callback issued no token: %s", callback.Body.String())
	}
}
