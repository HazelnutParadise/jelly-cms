package server

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/bcrypt"
)

// Shared fixtures for the account self-service and devices acceptance tests
// (docs/tickets/06-accounts.md, sections S1 to S13 and A1 to A10).

const acctPassword = "account-test-password-1"

const acctUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

var acctRemoteSeq atomic.Int64

// acctRemote returns a fresh source address per call so the login limiter
// never throttles a fixture login.
func acctRemote() string {
	n := acctRemoteSeq.Add(1)
	return fmt.Sprintf("203.0.113.%d:%d", 1+n%250, 20000+n%20000)
}

type acctUserSpec struct {
	Username    string
	Email       string
	Role        string
	Status      string
	DisplayName string
	Password    string
	Provider    string
}

// acctCreateUser inserts a user through the ORM. Empty Status keeps the
// column default (active) and empty Provider means a local account.
func acctCreateUser(t *testing.T, h *accessHarness, spec acctUserSpec) core.User {
	t.Helper()
	if spec.Email == "" {
		spec.Email = spec.Username + "@example.test"
	}
	if spec.Role == "" {
		spec.Role = "author"
	}
	if spec.Provider == "" {
		spec.Provider = "local"
	}
	u := core.User{
		Username:    spec.Username,
		Email:       spec.Email,
		Role:        spec.Role,
		Status:      spec.Status,
		DisplayName: spec.DisplayName,
		Provider:    spec.Provider,
	}
	if spec.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(spec.Password), bcrypt.MinCost)
		if err != nil {
			t.Fatalf("hash password for %s: %v", spec.Username, err)
		}
		u.Password = string(hash)
	}
	if err := h.db.Create(&u).Error; err != nil {
		t.Fatalf("create user %s: %v", spec.Username, err)
	}
	return u
}

// acctCall sends one request with its own source address and returns the result.
func acctCall(t *testing.T, h *accessHarness, remote, method, path, body, token string, headers map[string]string) accessResult {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	return acctCallReader(t, h, remote, method, path, r, token, headers)
}

func acctCallReader(t *testing.T, h *accessHarness, remote, method, path string, body io.Reader, token string, headers map[string]string) accessResult {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	req.RemoteAddr = remote
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
		// This trusted in-process fixture supplies the request's source evidence.
		req.Header.Set("X-Jelly-CSRF", "1")
	}
	req.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
	if token != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	var panicked any
	func() {
		defer func() { panicked = recover() }()
		h.echo.ServeHTTP(rec, req)
	}()
	return accessResult{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone(), panicked: panicked}
}

// acctJSON sends a JSON request (Content-Type set only when body is not empty).
func acctJSON(t *testing.T, h *accessHarness, method, path, body, token string) accessResult {
	t.Helper()
	headers := map[string]string{}
	if body != "" {
		headers[echo.HeaderContentType] = echo.MIMEApplicationJSON
	}
	return acctCall(t, h, acctRemote(), method, path, body, token, headers)
}

// acctLogin signs in through POST /auth/login with JSON and returns the JWT and
// the cookies the login set.
func acctLogin(t *testing.T, h *accessHarness, username, password, userAgent, remote string) (string, []*http.Cookie) {
	t.Helper()
	if userAgent == "" {
		userAgent = acctUserAgent
	}
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		t.Fatalf("encode login body: %v", err)
	}
	res := acctCall(t, h, remote, http.MethodPost, "/auth/login", string(body), "", map[string]string{
		echo.HeaderContentType: echo.MIMEApplicationJSON,
		"User-Agent":           userAgent,
	})
	h.mustNoPanic(res, "login "+username)
	if res.code != http.StatusOK {
		t.Fatalf("login %s returned %d, want 200; body=%q", username, res.code, res.body)
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(res.body), &payload); err != nil || payload.Token == "" {
		t.Fatalf("login %s response has no token: err=%v body=%q", username, err, res.body)
	}
	return payload.Token, (&http.Response{Header: res.header}).Cookies()
}

// acctCookieHeader renders cookies for a Cookie request header.
func acctCookieHeader(cookies []*http.Cookie) string {
	parts := make([]string, 0, len(cookies))
	for _, c := range cookies {
		if c != nil {
			parts = append(parts, c.Name+"="+c.Value)
		}
	}
	return strings.Join(parts, "; ")
}

// acctJTI reads the login ID from a JWT without verifying it. Tests use it only
// to check that the raw value never appears in API responses.
func acctJTI(t *testing.T, token string) string {
	t.Helper()
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
		t.Fatalf("parse JWT claims: %v", err)
	}
	jti, _ := claims["jti"].(string)
	if jti == "" {
		t.Fatal("JWT has no jti claim")
	}
	return jti
}

// acctNullTime reads one nullable timestamp column straight from the database.
func acctNullTime(t *testing.T, h *accessHarness, query string, args ...any) sql.NullTime {
	t.Helper()
	var v sql.NullTime
	if err := h.db.Raw(query, args...).Row().Scan(&v); err != nil {
		t.Fatalf("read %q: %v", query, err)
	}
	return v
}

// acctString reads one text column straight from the database.
func acctString(t *testing.T, h *accessHarness, query string, args ...any) string {
	t.Helper()
	var v sql.NullString
	if err := h.db.Raw(query, args...).Row().Scan(&v); err != nil {
		t.Fatalf("read %q: %v", query, err)
	}
	return v.String
}

// acctCount runs a COUNT(*) style query.
func acctCount(t *testing.T, h *accessHarness, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := h.db.Raw(query, args...).Row().Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// acctTokenFromURL returns the path segment after marker, such as "/invite/".
func acctTokenFromURL(t *testing.T, raw, marker string) string {
	t.Helper()
	idx := strings.Index(raw, marker)
	if idx < 0 {
		t.Fatalf("URL %q does not contain %q", raw, marker)
	}
	rest := raw[idx+len(marker):]
	if cut := strings.IndexAny(rest, "?#/"); cut >= 0 {
		rest = rest[:cut]
	}
	if rest == "" {
		t.Fatalf("URL %q has an empty token segment", raw)
	}
	return rest
}

// acctRequestURI returns the path and query of a link the server produced.
func acctRequestURI(t *testing.T, raw string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse URL %q: %v", raw, err)
	}
	return parsed.RequestURI()
}

func acctSHA256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// acctDecode unmarshals a response body or fails the test.
func acctDecode(t *testing.T, r accessResult, where string, out any) {
	t.Helper()
	if err := json.Unmarshal([]byte(r.body), out); err != nil {
		t.Fatalf("decode %s: %v; body=%q", where, err, r.body)
	}
}

func acctWant(t *testing.T, r accessResult, where string, want int) bool {
	t.Helper()
	if r.code != want {
		t.Errorf("%s returned %d, want %d; body=%q", where, r.code, want, r.body)
		return false
	}
	return true
}

func acctWantOK(t *testing.T, r accessResult, where string) bool {
	t.Helper()
	if r.code < 200 || r.code >= 300 {
		t.Errorf("%s returned %d, want 2xx; body=%q", where, r.code, r.body)
		return false
	}
	return true
}

// acctTinyPNG is a valid 1x1 PNG.
func acctTinyPNG(t *testing.T) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=")
	if err != nil {
		t.Fatalf("decode PNG fixture: %v", err)
	}
	return raw
}

// acctMultipart builds a single-file multipart body.
func acctMultipart(t *testing.T, field, filename string, content []byte) (string, io.Reader) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile(field, filename)
	if err != nil {
		t.Fatalf("create multipart file part: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write multipart file part: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart body: %v", err)
	}
	return w.FormDataContentType(), &body
}

func TestProfileGetReturnsOwnFields(t *testing.T) {
	h := newAccessHarness(t)
	u := acctCreateUser(t, h, acctUserSpec{Username: "profile-get", Role: "author", DisplayName: "Get Person", Password: acctPassword})
	for i := 0; i < 2; i++ {
		p := core.Post{
			Title: fmt.Sprintf("Profile post %d", i), Slug: fmt.Sprintf("profile-get-%d", i), Content: "body",
			Type: "post", Status: "published", Visibility: "public", AuthorID: u.ID,
		}
		if err := h.db.Create(&p).Error; err != nil {
			t.Fatalf("seed post: %v", err)
		}
	}

	res := acctJSON(t, h, http.MethodGet, "/api/account/profile", "", h.token(u))
	h.mustNoPanic(res, "GET /api/account/profile")
	if !acctWant(t, res, "GET /api/account/profile", http.StatusOK) {
		return
	}
	var got map[string]any
	acctDecode(t, res, "profile", &got)
	for _, key := range []string{"id", "username", "display_name", "email", "email_verified", "pending_email", "avatar_url", "bio", "locale", "ui_theme", "role", "created_at", "post_count", "author_url"} {
		if _, ok := got[key]; !ok {
			t.Errorf("profile response is missing key %q; body=%q", key, res.body)
		}
	}
	for _, key := range []string{"password", "password_hash", "pending_email_hash", "pending_email_expires_at"} {
		if _, ok := got[key]; ok {
			t.Errorf("profile response exposes internal key %q", key)
		}
	}
	if got["id"] != float64(u.ID) {
		t.Errorf("profile id = %v, want %d", got["id"], u.ID)
	}
	if got["username"] != u.Username || got["email"] != u.Email || got["role"] != "author" {
		t.Errorf("profile identity = username %v email %v role %v, want %s %s author", got["username"], got["email"], got["role"], u.Username, u.Email)
	}
	if got["display_name"] != "Get Person" {
		t.Errorf("profile display_name = %v, want Get Person", got["display_name"])
	}
	if got["locale"] != "zh-TW" {
		t.Errorf("profile locale = %v, want default zh-TW", got["locale"])
	}
	if _, ok := got["email_verified"].(bool); !ok {
		t.Errorf("profile email_verified = %v, want a boolean", got["email_verified"])
	}
	if got["post_count"] != float64(2) {
		t.Errorf("profile post_count = %v, want 2", got["post_count"])
	}
}

func TestProfileUpdateWhitelistAndLengthLimits(t *testing.T) {
	h := newAccessHarness(t)
	u := acctCreateUser(t, h, acctUserSpec{Username: "profile-update", Role: "author", DisplayName: "Before Name", Password: acctPassword})
	token := h.token(u)
	patch := func(body string) accessResult {
		return acctJSON(t, h, http.MethodPatch, "/api/account/profile", body, token)
	}

	rejected := []struct{ name, body string }{
		{"display_name 121 characters", `{"display_name":"` + strings.Repeat("a", 121) + `"}`},
		{"bio 501 characters", `{"bio":"` + strings.Repeat("b", 501) + `"}`},
		{"unsupported locale", `{"locale":"ja"}`},
		{"unsupported ui_theme", `{"ui_theme":"neon"}`},
	}
	for _, tc := range rejected {
		res := patch(tc.body)
		h.mustNoPanic(res, tc.name)
		acctWant(t, res, "PATCH profile with "+tc.name, http.StatusBadRequest)
	}
	if got := acctString(t, h, "SELECT display_name FROM users WHERE id = ?", u.ID); got != "Before Name" {
		t.Errorf("rejected PATCH changed display_name to %q", got)
	}

	if res := patch(`{"display_name":"` + strings.Repeat("a", 120) + `","bio":"` + strings.Repeat("b", 500) + `","locale":"en","ui_theme":"dark"}`); !acctWant(t, res, "PATCH profile at the length limits", http.StatusOK) {
		return
	}
	if got := acctString(t, h, "SELECT locale FROM users WHERE id = ?", u.ID); got != "en" {
		t.Errorf("locale after PATCH = %q, want en", got)
	}

	injected := `{"display_name":"Whitelisted","role":"admin","status":"disabled","email":"hijack@example.test","id":123456,"password":"injected-secret-value"}`
	before := acctString(t, h, "SELECT password FROM users WHERE id = ?", u.ID)
	res := patch(injected)
	h.mustNoPanic(res, "PATCH profile with injected fields")
	if !acctWant(t, res, "PATCH profile with injected fields", http.StatusOK) {
		return
	}
	var after core.User
	if err := h.db.First(&after, u.ID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if after.Role != "author" {
		t.Errorf("role after injected PATCH = %q, want author", after.Role)
	}
	if after.Status != "active" {
		t.Errorf("status after injected PATCH = %q, want active", after.Status)
	}
	if after.Email != u.Email {
		t.Errorf("email after injected PATCH = %q, want %q", after.Email, u.Email)
	}
	if after.Password != before {
		t.Error("password hash changed through PATCH /api/account/profile")
	}
	if after.DisplayName != "Whitelisted" {
		t.Errorf("display_name = %q, want Whitelisted", after.DisplayName)
	}
	if n := acctCount(t, h, "SELECT COUNT(*) FROM users WHERE id = ?", 123456); n != 0 {
		t.Errorf("injected id created or moved a user row (%d rows with id 123456)", n)
	}
}

func TestProfileAvatarTypeSizeAndOwnerOnly(t *testing.T) {
	h := newAccessHarness(t)
	u := acctCreateUser(t, h, acctUserSpec{Username: "profile-avatar", Role: "author", Password: acctPassword})
	token := h.token(u)
	png := acctTinyPNG(t)

	anonType, anonBody := acctMultipart(t, "avatar", "anon.png", png)
	anon := acctCallReader(t, h, acctRemote(), http.MethodPost, "/api/account/avatar", anonBody, "", map[string]string{echo.HeaderContentType: anonType})
	h.mustNoPanic(anon, "anonymous avatar upload")
	acctWant(t, anon, "anonymous POST /api/account/avatar", http.StatusUnauthorized)

	pngType, pngBody := acctMultipart(t, "avatar", "face.png", png)
	upload := acctCallReader(t, h, acctRemote(), http.MethodPost, "/api/account/avatar", pngBody, token, map[string]string{echo.HeaderContentType: pngType})
	h.mustNoPanic(upload, "png avatar upload")
	if acctWant(t, upload, "POST png avatar", http.StatusOK) {
		var payload struct {
			AvatarURL string `json:"avatar_url"`
		}
		acctDecode(t, upload, "avatar upload", &payload)
		if payload.AvatarURL == "" {
			t.Error("png avatar upload returned an empty avatar_url")
		}
		if got := acctString(t, h, "SELECT avatar_url FROM users WHERE id = ?", u.ID); got == "" {
			t.Error("png avatar upload did not store avatar_url on the user")
		}
	}

	gifType, gifBody := acctMultipart(t, "avatar", "face.gif", []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"))
	gif := acctCallReader(t, h, acctRemote(), http.MethodPost, "/api/account/avatar", gifBody, token, map[string]string{echo.HeaderContentType: gifType})
	h.mustNoPanic(gif, "gif avatar upload")
	acctWant(t, gif, "POST gif avatar", http.StatusBadRequest)

	oversized := append(append([]byte{}, png...), bytes.Repeat([]byte{0}, 2*1024*1024+1)...)
	bigType, bigBody := acctMultipart(t, "avatar", "big.png", oversized)
	big := acctCallReader(t, h, acctRemote(), http.MethodPost, "/api/account/avatar", bigBody, token, map[string]string{echo.HeaderContentType: bigType})
	h.mustNoPanic(big, "oversized avatar upload")
	if big.code != http.StatusRequestEntityTooLarge && big.code != http.StatusBadRequest {
		t.Errorf("POST 2 MB+1 avatar returned %d, want 413 or 400; body=%q", big.code, big.body)
	}

	del := acctJSON(t, h, http.MethodDelete, "/api/account/avatar", "", token)
	h.mustNoPanic(del, "DELETE avatar")
	if acctWantOK(t, del, "DELETE /api/account/avatar") {
		if got := acctString(t, h, "SELECT avatar_url FROM users WHERE id = ?", u.ID); got != "" {
			t.Errorf("avatar_url after DELETE = %q, want empty", got)
		}
	}
}

func TestProfileEmailChangeNeedsPasswordAndVerification(t *testing.T) {
	h := newAccessHarness(t)
	u := acctCreateUser(t, h, acctUserSpec{Username: "profile-email", Role: "author", Password: acctPassword})
	acctCreateUser(t, h, acctUserSpec{Username: "profile-email-taken", Email: "email-taken-target@example.test", Role: "author"})
	token := h.token(u)
	newEmail := "profile-email-new@example.test"

	wrong := acctJSON(t, h, http.MethodPost, "/api/account/email", fmt.Sprintf(`{"new_email":%q,"current_password":"not-the-password"}`, newEmail), token)
	h.mustNoPanic(wrong, "wrong password email change")
	if wrong.code != http.StatusUnauthorized && wrong.code != http.StatusForbidden {
		t.Errorf("email change with wrong password returned %d, want 401 or 403; body=%q", wrong.code, wrong.body)
	}
	if got := acctString(t, h, "SELECT email FROM users WHERE id = ?", u.ID); got != u.Email {
		t.Errorf("email changed without the password: %q", got)
	}

	taken := acctJSON(t, h, http.MethodPost, "/api/account/email", fmt.Sprintf(`{"new_email":%q,"current_password":%q}`, "email-taken-target@example.test", acctPassword), token)
	h.mustNoPanic(taken, "taken email change")
	acctWant(t, taken, "email change to a taken address", http.StatusConflict)

	accepted := acctJSON(t, h, http.MethodPost, "/api/account/email", fmt.Sprintf(`{"new_email":%q,"current_password":%q}`, newEmail, acctPassword), token)
	h.mustNoPanic(accepted, "email change request")
	if !acctWant(t, accepted, "POST /api/account/email", http.StatusAccepted) {
		return
	}
	var request struct {
		ConfirmURL string `json:"confirm_url"`
	}
	acctDecode(t, accepted, "email change response", &request)
	if !strings.Contains(request.ConfirmURL, "/account/email/confirm") {
		t.Fatalf("confirm_url = %q, want a /account/email/confirm link", request.ConfirmURL)
	}
	if got := acctString(t, h, "SELECT email FROM users WHERE id = ?", u.ID); got != u.Email {
		t.Errorf("email changed before the link was opened: %q", got)
	}

	confirm := acctCall(t, h, acctRemote(), http.MethodGet, acctRequestURI(t, request.ConfirmURL), "", "", map[string]string{echo.HeaderAccept: "text/html"})
	h.mustNoPanic(confirm, "GET email confirm")
	if confirm.code >= 400 {
		t.Errorf("GET email confirm returned %d, want success or redirect; body=%q", confirm.code, confirm.body)
	}
	if got := acctString(t, h, "SELECT email FROM users WHERE id = ?", u.ID); got != newEmail {
		t.Errorf("email after confirmation = %q, want %q", got, newEmail)
	}
	if verified := acctNullTime(t, h, "SELECT email_verified_at FROM users WHERE id = ?", u.ID); !verified.Valid {
		t.Error("email_verified_at not set after confirming the new address")
	}
}

func TestProfilePasswordChangeRevokesOtherSessionsKeepsCurrent(t *testing.T) {
	h := newAccessHarness(t)
	u := acctCreateUser(t, h, acctUserSpec{Username: "profile-pw-revoke", Role: "author", Password: acctPassword})
	first, _ := acctLogin(t, h, u.Username, acctPassword, "", acctRemote())
	second, _ := acctLogin(t, h, u.Username, acctPassword, "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Firefox/121.0", acctRemote())

	change := acctJSON(t, h, http.MethodPost, "/api/account/password", `{"current_password":"`+acctPassword+`","new_password":"changed-password-22"}`, first)
	h.mustNoPanic(change, "password change")
	if !acctWant(t, change, "POST /api/account/password", http.StatusOK) {
		return
	}
	if r := acctJSON(t, h, http.MethodGet, "/api/account/profile", "", second); r.code != http.StatusUnauthorized {
		t.Errorf("GET profile with the other device's JWT returned %d, want 401", r.code)
	}
	if r := acctJSON(t, h, http.MethodGet, "/api/auth/me", "", second); r.code != http.StatusUnauthorized {
		t.Errorf("GET /api/auth/me with the other device's JWT returned %d, want 401", r.code)
	}
	acctWant(t, acctJSON(t, h, http.MethodGet, "/api/account/profile", "", first), "GET profile with the current JWT", http.StatusOK)
	if r := acctCall(t, h, acctRemote(), http.MethodPost, "/auth/login", fmt.Sprintf(`{"username":%q,"password":%q}`, u.Username, acctPassword), "", map[string]string{echo.HeaderContentType: echo.MIMEApplicationJSON}); r.code != http.StatusUnauthorized {
		t.Errorf("login with the old password returned %d, want 401", r.code)
	}
	if token, _ := acctLogin(t, h, u.Username, "changed-password-22", "", acctRemote()); token == "" {
		t.Error("login with the new password returned no token")
	}
}

func TestProfilePasswordWrongCurrentRejected(t *testing.T) {
	h := newAccessHarness(t)
	u := acctCreateUser(t, h, acctUserSpec{Username: "profile-pw-wrong", Role: "author", Password: acctPassword})
	token := h.token(u)

	wrong := acctJSON(t, h, http.MethodPost, "/api/account/password", `{"current_password":"not-the-password","new_password":"changed-password-33"}`, token)
	h.mustNoPanic(wrong, "wrong current password")
	if wrong.code != http.StatusUnauthorized && wrong.code != http.StatusForbidden {
		t.Errorf("password change with wrong current password returned %d, want 401 or 403; body=%q", wrong.code, wrong.body)
	}
	short := acctJSON(t, h, http.MethodPost, "/api/account/password", `{"current_password":"`+acctPassword+`","new_password":"short"}`, token)
	h.mustNoPanic(short, "short new password")
	acctWant(t, short, "password change with a 5-character password", http.StatusBadRequest)

	if token, _ := acctLogin(t, h, u.Username, acctPassword, "", acctRemote()); token == "" {
		t.Error("original password no longer logs in after rejected changes")
	}
}

func TestProfileOAuthOnlyUserCanSetPasswordThenLocalLogin(t *testing.T) {
	h := newAccessHarness(t)
	u := acctCreateUser(t, h, acctUserSpec{Username: "profile-oauth-only", Role: "author", Provider: "google"})
	token := h.token(u)

	set := acctJSON(t, h, http.MethodPost, "/api/account/password", `{"new_password":"first-local-password"}`, token)
	h.mustNoPanic(set, "OAuth-only password set")
	if !acctWant(t, set, "POST /api/account/password without current_password for an OAuth-only account", http.StatusOK) {
		return
	}
	if loginToken, _ := acctLogin(t, h, u.Username, "first-local-password", "", acctRemote()); loginToken == "" {
		t.Error("local login with the newly set password returned no token")
	}
}

func TestProfileEndpointsRejectAPITokenPrincipal(t *testing.T) {
	h := newAccessHarness(t)
	acctCreateUser(t, h, acctUserSpec{Username: "profile-token-principal", Role: "admin", Password: acctPassword})
	for _, bearer := range []string{"jly_x", "jly_" + strings.Repeat("A", 43)} {
		for _, probe := range []struct{ method, path, body string }{
			{http.MethodPatch, "/api/account/profile", `{"display_name":"via token"}`},
			{http.MethodGet, "/api/account/profile", ""},
			{http.MethodPost, "/api/account/password", `{"new_password":"token-password-1"}`},
			{http.MethodPost, "/api/account/email", `{"new_email":"token@example.test","current_password":"x"}`},
		} {
			res := acctJSON(t, h, probe.method, probe.path, probe.body, bearer)
			h.mustNoPanic(res, probe.method+" "+probe.path)
			if res.code != http.StatusUnauthorized {
				t.Errorf("%s %s with Bearer %q returned %d, want 401; body=%q", probe.method, probe.path, bearer[:4], res.code, res.body)
			}
		}
	}
}
