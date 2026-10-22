package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/i18n"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/bcrypt"
)

const (
	revocationTestPassword      = "revocation-password"
	revocationTestSessionSecret = "revocation-session-secret-0123456789abcdef"
	revocationTestJWTSecret     = "revocation-jwt-secret-0123456789abcdef"
)

func createRevocationUser(t *testing.T, h *accessHarness, username string) core.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(revocationTestPassword), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash revocation password: %v", err)
	}
	u := core.User{Username: username, Email: username + "@example.test", Role: "author", Provider: "local", Password: string(hash)}
	if err := h.db.Create(&u).Error; err != nil {
		t.Fatalf("create revocation user %s: %v", username, err)
	}
	return u
}

func revocationLoginWithUser(t *testing.T, h *accessHarness, u core.User) (string, []*http.Cookie) {
	t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, u.Username, revocationTestPassword)
	res := h.request(http.MethodPost, "/auth/login", body, "", "application/json", "application/json")
	if res.panicked != nil {
		t.Fatalf("login panicked: %v", res.panicked)
	}
	if res.code != http.StatusOK {
		t.Fatalf("login for %s returned %d, want 200; body=%q", u.Username, res.code, res.body)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(res.body), &payload); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	token, _ := payload["token"].(string)
	if token == "" {
		t.Fatalf("login response did not include a token: %q", res.body)
	}
	cookies := (&http.Response{Header: res.header}).Cookies()
	if len(cookies) == 0 {
		t.Fatalf("login response did not set cookies")
	}
	if revocationCookie(cookies, "jelly-cms-session") == nil {
		t.Fatalf("login response did not set session cookie")
	}
	if revocationCookie(cookies, "jwt_token") == nil {
		t.Fatalf("login response did not set jwt cookie")
	}

	me := revocationRequest(h, http.MethodGet, "/api/auth/me", cookies, token, "")
	if me.panicked != nil {
		t.Fatalf("issued credential check panicked: %v", me.panicked)
	}
	if me.code != http.StatusOK {
		t.Fatalf("issued credentials were not accepted before logout: status=%d body=%q", me.code, me.body)
	}
	return token, cookies
}

func revocationLogin(t *testing.T, h *accessHarness, username string) (core.User, string, []*http.Cookie) {
	t.Helper()
	u := createRevocationUser(t, h, username)
	token, cookies := revocationLoginWithUser(t, h, u)
	return u, token, cookies
}

func revocationRequest(h *accessHarness, method, path string, cookies []*http.Cookie, token, body string) accessResult {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set(echo.HeaderContentType, "application/json")
	}
	r.Header.Set(echo.HeaderAccept, "application/json")
	for _, c := range cookies {
		if c != nil {
			r.AddCookie(c)
		}
	}
	if token != "" {
		r.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	var panicked any
	func() {
		defer func() { panicked = recover() }()
		h.echo.ServeHTTP(rec, r)
	}()
	return accessResult{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone(), panicked: panicked}
}

func revocationCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, c := range cookies {
		if c != nil && c.Name == name {
			clone := *c
			return &clone
		}
	}
	return nil
}

func revocationResponseClearsCookie(res accessResult, name string) bool {
	for _, c := range (&http.Response{Header: res.header}).Cookies() {
		if c.Name == name && (c.MaxAge < 0 || c.Value == "") {
			return true
		}
	}
	return false
}

func runRevocationReplayChild(t *testing.T, workDir, dbName, jwtPath, sessionPath string, expectedStatus int) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAuthRevocationReplayHelperProcess$", "-test.count=1")
	cmd.Dir = workDir
	env := make([]string, 0, len(os.Environ())+10)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "SESSION_SECRET=") ||
			strings.HasPrefix(entry, "JWT_SECRET=") ||
			strings.HasPrefix(entry, "JELLY_AUTH_REVOCATION_REPLAY_HELPER=") ||
			strings.HasPrefix(entry, "JELLY_AUTH_REVOCATION_DB_NAME=") ||
			strings.HasPrefix(entry, "JELLY_AUTH_REVOCATION_JWT_FILE=") ||
			strings.HasPrefix(entry, "JELLY_AUTH_REVOCATION_SESSION_FILE=") ||
			strings.HasPrefix(entry, "JELLY_AUTH_REVOCATION_WORK_DIR=") ||
			strings.HasPrefix(entry, "JELLY_AUTH_REVOCATION_EXPECTED_STATUS=") {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "JELLY_AUTH_REVOCATION_REPLAY_HELPER=1")
	env = append(env, "JELLY_AUTH_REVOCATION_DB_NAME="+dbName)
	env = append(env, "JELLY_AUTH_REVOCATION_JWT_FILE="+jwtPath)
	env = append(env, "JELLY_AUTH_REVOCATION_SESSION_FILE="+sessionPath)
	env = append(env, "JELLY_AUTH_REVOCATION_WORK_DIR="+workDir)
	env = append(env, fmt.Sprintf("JELLY_AUTH_REVOCATION_EXPECTED_STATUS=%d", expectedStatus))
	env = append(env, "SESSION_SECRET="+revocationTestSessionSecret)
	env = append(env, "JWT_SECRET="+revocationTestJWTSecret)
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(output), ctx.Err()
	}
	return string(output), err
}

func TestAuthRevocationReplayHelperProcess(t *testing.T) {
	if os.Getenv("JELLY_AUTH_REVOCATION_REPLAY_HELPER") != "1" {
		t.Skip("helper process only")
	}
	dbName := os.Getenv("JELLY_AUTH_REVOCATION_DB_NAME")
	if dbName == "" {
		t.Fatal("missing database name")
	}
	jwtPath := os.Getenv("JELLY_AUTH_REVOCATION_JWT_FILE")
	sessionPath := os.Getenv("JELLY_AUTH_REVOCATION_SESSION_FILE")
	workDir := os.Getenv("JELLY_AUTH_REVOCATION_WORK_DIR")
	if workDir != "" {
		if err := os.Chdir(workDir); err != nil {
			t.Fatal(err)
		}
	}
	expectedStatus := 0
	switch os.Getenv("JELLY_AUTH_REVOCATION_EXPECTED_STATUS") {
	case "200":
		expectedStatus = http.StatusOK
	case "401":
		expectedStatus = http.StatusUnauthorized
	default:
		t.Fatal("missing or invalid expected status")
	}
	cfg := config.DatabaseConfig{Host: accessTestHost, Port: accessTestPort, User: accessTestUser, Password: accessTestPass, Name: dbName, Timezone: "UTC"}
	db, err := data.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { accessCloseDB(db) })
	data.DB = db
	config.GlobalConfig = &config.Config{Database: cfg}
	i18n.Init()
	if i18n.Bundle == nil {
		t.Fatal("i18n.Init() left bundle nil")
	}
	if err := auth.Init(); err != nil {
		t.Fatal(err)
	}
	e := echo.New()
	RegisterRoutes(e, theme.NewManager("web/themes"), nil)

	tokenBytes, err := os.ReadFile(jwtPath)
	if err != nil {
		t.Fatal(err)
	}
	sessionBytes, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	sessionValue := strings.TrimSpace(string(sessionBytes))

	jwtReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	jwtReq.Header.Set(echo.HeaderAccept, "application/json")
	jwtReq.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	jwtRec := httptest.NewRecorder()
	e.ServeHTTP(jwtRec, jwtReq)
	if jwtRec.Code != expectedStatus {
		t.Errorf("replayed JWT in a new process returned %d, want %d; body=%q", jwtRec.Code, expectedStatus, jwtRec.Body.String())
	}

	sessionReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	sessionReq.Header.Set(echo.HeaderAccept, "application/json")
	sessionReq.AddCookie(&http.Cookie{Name: "jelly-cms-session", Value: sessionValue})
	sessionRec := httptest.NewRecorder()
	e.ServeHTTP(sessionRec, sessionReq)
	if sessionRec.Code != expectedStatus {
		t.Errorf("replayed session in a new process returned %d, want %d; body=%q", sessionRec.Code, expectedStatus, sessionRec.Body.String())
	}
}

func TestAuthRevocation(t *testing.T) {
	t.Run("successful login proves JWT-only and session-only before logout revokes both", func(t *testing.T) {
		t.Setenv("SESSION_SECRET", revocationTestSessionSecret)
		t.Setenv("JWT_SECRET", revocationTestJWTSecret)
		h := newAccessHarness(t)
		_, token, cookies := revocationLogin(t, h, "revocation-both")

		sessionCookie := revocationCookie(cookies, "jelly-cms-session")

		jwtOnly := revocationRequest(h, http.MethodGet, "/api/auth/me", nil, token, "")
		if jwtOnly.panicked != nil {
			t.Errorf("JWT-only credential check panicked: %v", jwtOnly.panicked)
		}
		if jwtOnly.code != http.StatusOK {
			t.Errorf("JWT-only credential returned %d, want 200; body=%q", jwtOnly.code, jwtOnly.body)
		}

		sessionOnly := revocationRequest(h, http.MethodGet, "/api/auth/me", []*http.Cookie{sessionCookie}, "", "")
		if sessionOnly.panicked != nil {
			t.Errorf("session-only credential check panicked: %v", sessionOnly.panicked)
		}
		if sessionOnly.code != http.StatusOK {
			t.Errorf("session-only credential returned %d, want 200; body=%q", sessionOnly.code, sessionOnly.body)
		}

		logout := revocationRequest(h, http.MethodPost, "/auth/logout", cookies, token, "")
		if logout.panicked != nil {
			t.Errorf("logout panicked: %v", logout.panicked)
		}
		if logout.code != http.StatusOK {
			t.Errorf("logout returned %d, want 200; body=%q", logout.code, logout.body)
		}
		if !revocationResponseClearsCookie(logout, "jwt_token") {
			t.Errorf("logout did not clear jwt_token cookie")
		}
		if !revocationResponseClearsCookie(logout, "jelly-cms-session") {
			t.Errorf("logout did not clear jelly-cms-session cookie")
		}

		jwtReplay := revocationRequest(h, http.MethodGet, "/api/auth/me", nil, token, "")
		if jwtReplay.code != http.StatusUnauthorized {
			t.Errorf("JWT replay after logout returned %d, want 401; body=%q", jwtReplay.code, jwtReplay.body)
		}
		sessionReplay := revocationRequest(h, http.MethodGet, "/api/auth/me", []*http.Cookie{sessionCookie}, "", "")
		if sessionReplay.code != http.StatusUnauthorized {
			t.Errorf("session replay after logout returned %d, want 401; body=%q", sessionReplay.code, sessionReplay.body)
		}
	})

	t.Run("JWT-only logout revokes the same session and session-only logout revokes the same JWT", func(t *testing.T) {
		t.Setenv("SESSION_SECRET", revocationTestSessionSecret)
		t.Setenv("JWT_SECRET", revocationTestJWTSecret)
		h := newAccessHarness(t)
		_, token, cookies := revocationLogin(t, h, "revocation-jwt-first")
		sessionCookie := revocationCookie(cookies, "jelly-cms-session")

		jwtLogout := revocationRequest(h, http.MethodPost, "/auth/logout", nil, token, "")
		if jwtLogout.panicked != nil || jwtLogout.code != http.StatusOK {
			t.Errorf("JWT-only logout returned %d (panic=%v), want 200; body=%q", jwtLogout.code, jwtLogout.panicked, jwtLogout.body)
		}
		if jwtLogout.code == http.StatusOK {
			if !revocationResponseClearsCookie(jwtLogout, "jwt_token") {
				t.Errorf("JWT-only logout did not clear jwt_token cookie")
			}
			if !revocationResponseClearsCookie(jwtLogout, "jelly-cms-session") {
				t.Errorf("JWT-only logout did not clear jelly-cms-session cookie")
			}
		}
		if revocationRequest(h, http.MethodGet, "/api/auth/me", nil, token, "").code != http.StatusUnauthorized {
			t.Errorf("JWT replay after JWT-only logout was accepted")
		}
		if revocationRequest(h, http.MethodGet, "/api/auth/me", []*http.Cookie{sessionCookie}, "", "").code != http.StatusUnauthorized {
			t.Errorf("session replay after JWT-only logout was accepted")
		}

		_, token2, cookies2 := revocationLogin(t, h, "revocation-session-first")
		sessionCookie2 := revocationCookie(cookies2, "jelly-cms-session")

		sessionLogout := revocationRequest(h, http.MethodPost, "/auth/logout", []*http.Cookie{sessionCookie2}, "", "")
		if sessionLogout.panicked != nil || sessionLogout.code != http.StatusOK {
			t.Errorf("session-only logout returned %d (panic=%v), want 200; body=%q", sessionLogout.code, sessionLogout.panicked, sessionLogout.body)
		}
		if sessionLogout.code == http.StatusOK {
			if !revocationResponseClearsCookie(sessionLogout, "jwt_token") {
				t.Errorf("session-only logout did not clear jwt_token cookie")
			}
			if !revocationResponseClearsCookie(sessionLogout, "jelly-cms-session") {
				t.Errorf("session-only logout did not clear jelly-cms-session cookie")
			}
		}
		if revocationRequest(h, http.MethodGet, "/api/auth/me", nil, token2, "").code != http.StatusUnauthorized {
			t.Errorf("JWT replay after session-only logout was accepted")
		}
		if revocationRequest(h, http.MethodGet, "/api/auth/me", []*http.Cookie{sessionCookie2}, "", "").code != http.StatusUnauthorized {
			t.Errorf("session replay after session-only logout was accepted")
		}
	})

	t.Run("second login for the same account remains valid after first logout", func(t *testing.T) {
		t.Setenv("SESSION_SECRET", revocationTestSessionSecret)
		t.Setenv("JWT_SECRET", revocationTestJWTSecret)
		h := newAccessHarness(t)
		u := createRevocationUser(t, h, "revocation-second-login")
		token1, cookies1 := revocationLoginWithUser(t, h, u)
		session1 := revocationCookie(cookies1, "jelly-cms-session")
		token2, cookies2 := revocationLoginWithUser(t, h, u)
		session2 := revocationCookie(cookies2, "jelly-cms-session")

		if revocationRequest(h, http.MethodGet, "/api/auth/me", nil, token1, "").code != http.StatusOK {
			t.Errorf("first JWT was not accepted before logout")
		}
		if revocationRequest(h, http.MethodGet, "/api/auth/me", []*http.Cookie{session1}, "", "").code != http.StatusOK {
			t.Errorf("first session was not accepted before logout")
		}
		if revocationRequest(h, http.MethodGet, "/api/auth/me", nil, token2, "").code != http.StatusOK {
			t.Errorf("second JWT was not accepted before logout")
		}
		if revocationRequest(h, http.MethodGet, "/api/auth/me", []*http.Cookie{session2}, "", "").code != http.StatusOK {
			t.Errorf("second session was not accepted before logout")
		}

		logout := revocationRequest(h, http.MethodPost, "/auth/logout", cookies1, token1, "")
		if logout.code != http.StatusOK {
			t.Errorf("first logout returned %d, want 200; body=%q", logout.code, logout.body)
		}
		if !revocationResponseClearsCookie(logout, "jwt_token") || !revocationResponseClearsCookie(logout, "jelly-cms-session") {
			t.Errorf("first logout did not clear both credential cookies")
		}
		if revocationRequest(h, http.MethodGet, "/api/auth/me", nil, token1, "").code != http.StatusUnauthorized {
			t.Errorf("first JWT was not revoked")
		}
		if revocationRequest(h, http.MethodGet, "/api/auth/me", []*http.Cookie{session1}, "", "").code != http.StatusUnauthorized {
			t.Errorf("first session was not revoked")
		}
		if revocationRequest(h, http.MethodGet, "/api/auth/me", nil, token2, "").code != http.StatusOK {
			t.Errorf("second JWT was incorrectly revoked")
		}
		if revocationRequest(h, http.MethodGet, "/api/auth/me", []*http.Cookie{session2}, "", "").code != http.StatusOK {
			t.Errorf("second session was incorrectly revoked")
		}
	})

	t.Run("repeated and parallel logout return 200 and replay is rejected", func(t *testing.T) {
		t.Setenv("SESSION_SECRET", revocationTestSessionSecret)
		t.Setenv("JWT_SECRET", revocationTestJWTSecret)
		h := newAccessHarness(t)
		_, token, cookies := revocationLogin(t, h, "revocation-repeat")

		for i := 0; i < 2; i++ {
			res := revocationRequest(h, http.MethodPost, "/auth/logout", cookies, token, "")
			if res.code != http.StatusOK {
				t.Errorf("repeated logout %d returned %d, want 200; body=%q", i+1, res.code, res.body)
			}
			if i == 0 && res.code == http.StatusOK {
				if !revocationResponseClearsCookie(res, "jwt_token") || !revocationResponseClearsCookie(res, "jelly-cms-session") {
					t.Errorf("repeated logout %d did not clear both credential cookies", i+1)
				}
			}
		}
		if revocationRequest(h, http.MethodGet, "/api/auth/me", nil, token, "").code != http.StatusUnauthorized {
			t.Errorf("JWT replay after repeated logout was accepted")
		}

		_, token2, cookies2 := revocationLogin(t, h, "revocation-parallel")
		statuses := make([]int, 2)
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				res := revocationRequest(h, http.MethodPost, "/auth/logout", cookies2, token2, "")
				statuses[index] = res.code
				if res.panicked != nil {
					t.Errorf("parallel logout panicked: %v", res.panicked)
				}
			}(i)
		}
		wg.Wait()
		for i, code := range statuses {
			if code != http.StatusOK {
				t.Errorf("parallel logout %d returned %d, want 200", i+1, code)
			}
		}
		if revocationRequest(h, http.MethodGet, "/api/auth/me", nil, token2, "").code != http.StatusUnauthorized {
			t.Errorf("JWT replay after parallel logout was accepted")
		}
	})

	t.Run("reconnecting the original database and reinitializing auth keeps old credentials rejected", func(t *testing.T) {
		t.Setenv("SESSION_SECRET", revocationTestSessionSecret)
		t.Setenv("JWT_SECRET", revocationTestJWTSecret)
		h := newAccessHarness(t)
		u := createRevocationUser(t, h, "revocation-reconnect")
		token, cookies := revocationLoginWithUser(t, h, u)
		sessionCookie := revocationCookie(cookies, "jelly-cms-session")

		logout := revocationRequest(h, http.MethodPost, "/auth/logout", cookies, token, "")
		if logout.code != http.StatusOK {
			t.Errorf("logout returned %d, want 200; body=%q", logout.code, logout.body)
		}

		previousDB := data.DB
		previousConfig := config.GlobalConfig
		cfg := previousConfig.Database
		newDB, err := data.Open(cfg)
		if err != nil {
			t.Fatalf("reopen isolated database: %v", err)
		}
		t.Cleanup(func() {
			data.DB = previousDB
			config.GlobalConfig = previousConfig
			accessCloseDB(newDB)
		})
		data.DB = newDB
		config.GlobalConfig = &config.Config{Database: cfg}
		if err := auth.Init(); err != nil {
			t.Fatalf("reinitialize auth: %v", err)
		}

		if revocationRequest(h, http.MethodGet, "/api/auth/me", nil, token, "").code != http.StatusUnauthorized {
			t.Errorf("old JWT was accepted after database reconnect")
		}
		if revocationRequest(h, http.MethodGet, "/api/auth/me", []*http.Cookie{sessionCookie}, "", "").code != http.StatusUnauthorized {
			t.Errorf("old session was accepted after database reconnect")
		}

		newToken, newCookies := revocationLoginWithUser(t, h, u)
		newSession := revocationCookie(newCookies, "jelly-cms-session")
		if revocationRequest(h, http.MethodGet, "/api/auth/me", nil, newToken, "").code != http.StatusOK {
			t.Errorf("new JWT was rejected after reconnect")
		}
		if revocationRequest(h, http.MethodGet, "/api/auth/me", []*http.Cookie{newSession}, "", "").code != http.StatusOK {
			t.Errorf("new session was rejected after reconnect")
		}
	})

	t.Run("reopening the database in a new process rejects revoked credentials", func(t *testing.T) {
		t.Setenv("SESSION_SECRET", revocationTestSessionSecret)
		t.Setenv("JWT_SECRET", revocationTestJWTSecret)
		h := newAccessHarness(t)
		u := createRevocationUser(t, h, "revocation-new-process")
		token, cookies := revocationLoginWithUser(t, h, u)
		sessionCookie := revocationCookie(cookies, "jelly-cms-session")

		var dbName string
		if err := h.db.Raw("SELECT current_database()").Scan(&dbName).Error; err != nil {
			t.Fatalf("read database name: %v", err)
		}
		workDir, err := os.Getwd()
		if err != nil {
			t.Fatalf("read working directory: %v", err)
		}
		fixtureDir := t.TempDir()
		jwtPath := filepath.Join(fixtureDir, "jwt")
		sessionPath := filepath.Join(fixtureDir, "session")
		if err := os.WriteFile(jwtPath, []byte(token), 0600); err != nil {
			t.Fatalf("write JWT fixture: %v", err)
		}
		if err := os.WriteFile(sessionPath, []byte(sessionCookie.Value), 0600); err != nil {
			t.Fatalf("write session fixture: %v", err)
		}

		beforeOutput, beforeErr := runRevocationReplayChild(t, workDir, dbName, jwtPath, sessionPath, http.StatusOK)
		if beforeErr != nil {
			t.Fatalf("pre-logout replay helper did not accept credentials: %v\n%s", beforeErr, beforeOutput)
		}

		logout := revocationRequest(h, http.MethodPost, "/auth/logout", cookies, token, "")
		if logout.code != http.StatusOK {
			t.Fatalf("logout returned %d, want 200; body=%q", logout.code, logout.body)
		}

		afterOutput, afterErr := runRevocationReplayChild(t, workDir, dbName, jwtPath, sessionPath, http.StatusUnauthorized)
		if afterErr != nil {
			t.Fatalf("post-logout replay helper failed: %v\n%s", afterErr, afterOutput)
		}
	})

	t.Run("invalid Authorization header or invalid JWT cookie with a valid session is rejected without fallback", func(t *testing.T) {
		t.Setenv("SESSION_SECRET", revocationTestSessionSecret)
		t.Setenv("JWT_SECRET", revocationTestJWTSecret)
		h := newAccessHarness(t)
		_, _, cookies := revocationLogin(t, h, "revocation-no-fallback")
		sessionCookie := revocationCookie(cookies, "jelly-cms-session")

		invalidHeader := revocationRequest(h, http.MethodGet, "/api/auth/me", []*http.Cookie{sessionCookie}, "not-a-valid-jwt", "")
		if invalidHeader.code != http.StatusUnauthorized {
			t.Errorf("invalid Authorization with valid session returned %d, want 401; body=%q", invalidHeader.code, invalidHeader.body)
		}

		badCookie := &http.Cookie{Name: "jwt_token", Value: "not-a-valid-jwt", Path: "/"}
		invalidCookie := revocationRequest(h, http.MethodGet, "/api/auth/me", []*http.Cookie{sessionCookie, badCookie}, "", "")
		if invalidCookie.code != http.StatusUnauthorized {
			t.Errorf("invalid JWT cookie with valid session returned %d, want 401; body=%q", invalidCookie.code, invalidCookie.body)
		}
	})

	t.Run("dropping revoked_logins makes logout fail with 503 and leaves credentials rejected", func(t *testing.T) {
		t.Setenv("SESSION_SECRET", revocationTestSessionSecret)
		t.Setenv("JWT_SECRET", revocationTestJWTSecret)
		h := newAccessHarness(t)
		_, token, cookies := revocationLogin(t, h, "revocation-drop-table")
		sessionCookie := revocationCookie(cookies, "jelly-cms-session")

		if err := h.db.Exec("DROP TABLE IF EXISTS revoked_logins").Error; err != nil {
			t.Fatalf("drop revoked_logins: %v", err)
		}

		logout := revocationRequest(h, http.MethodPost, "/auth/logout", cookies, token, "")
		if logout.panicked != nil {
			t.Errorf("logout panicked: %v", logout.panicked)
		}
		if logout.code != http.StatusServiceUnavailable {
			t.Errorf("logout after dropped table returned %d, want 503; body=%q", logout.code, logout.body)
		}
		if revocationResponseClearsCookie(logout, "jwt_token") || revocationResponseClearsCookie(logout, "jelly-cms-session") {
			t.Errorf("logout failure cleared a credential cookie")
		}

		if revocationRequest(h, http.MethodGet, "/api/auth/me", nil, token, "").code == http.StatusOK {
			t.Errorf("JWT remained accepted after failed logout")
		}
		if revocationRequest(h, http.MethodGet, "/api/auth/me", []*http.Cookie{sessionCookie}, "", "").code == http.StatusOK {
			t.Errorf("session remained accepted after failed logout")
		}
	})

	t.Run("legacy JWT and session without login IDs are rejected by HTTP auth", func(t *testing.T) {
		t.Setenv("SESSION_SECRET", revocationTestSessionSecret)
		t.Setenv("JWT_SECRET", revocationTestJWTSecret)
		h := newAccessHarness(t)
		u := createRevocationUser(t, h, "revocation-legacy")

		legacyClaims := jwt.MapClaims{
			"user_id": float64(u.ID),
			"email":   u.Email,
			"role":    u.Role,
			"exp":     time.Now().Add(time.Hour).Unix(),
			"iat":     time.Now().Unix(),
		}
		legacyTokenObj := jwt.NewWithClaims(jwt.SigningMethodHS256, legacyClaims)
		legacyToken, err := legacyTokenObj.SignedString([]byte(revocationTestJWTSecret))
		if err != nil {
			t.Fatalf("sign legacy JWT: %v", err)
		}
		if _, err := auth.ValidateJWT(legacyToken); err != nil {
			t.Fatalf("legacy JWT was not signature-valid: %v", err)
		}

		store := sessions.NewCookieStore([]byte(revocationTestSessionSecret))
		sessionReq := httptest.NewRequest(http.MethodGet, "/", nil)
		session, err := store.Get(sessionReq, "jelly-cms-session")
		if err != nil {
			t.Fatalf("create legacy session: %v", err)
		}
		session.Values["user_id"] = u.ID
		session.Options = &sessions.Options{
			Path:     "/",
			MaxAge:   86400 * 7,
			HttpOnly: true,
			Secure:   false,
			SameSite: http.SameSiteLaxMode,
		}
		sessionRec := httptest.NewRecorder()
		if err := session.Save(sessionReq, sessionRec); err != nil {
			t.Fatalf("save legacy session: %v", err)
		}
		legacyCookies := sessionRec.Result().Cookies()

		echoInstance := echo.New()
		sessionContextReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
		for _, c := range legacyCookies {
			sessionContextReq.AddCookie(c)
		}
		sessionContextRec := httptest.NewRecorder()
		echoContext := echoInstance.NewContext(sessionContextReq, sessionContextRec)
		if _, err := auth.GetUserIDFromSession(echoContext); err != nil {
			t.Fatalf("legacy session was not low-level decodable: %v", err)
		}

		if revocationRequest(h, http.MethodGet, "/api/auth/me", nil, legacyToken, "").code != http.StatusUnauthorized {
			t.Errorf("legacy JWT was accepted by HTTP auth")
		}
		if revocationRequest(h, http.MethodGet, "/api/auth/me", legacyCookies, "", "").code != http.StatusUnauthorized {
			t.Errorf("legacy session was accepted by HTTP auth")
		}
	})

	t.Run("logout without a login is idempotent and does not panic", func(t *testing.T) {
		t.Setenv("SESSION_SECRET", revocationTestSessionSecret)
		t.Setenv("JWT_SECRET", revocationTestJWTSecret)
		h := newAccessHarness(t)

		logout := revocationRequest(h, http.MethodPost, "/auth/logout", nil, "", "")
		if logout.panicked != nil {
			t.Errorf("logout panicked: %v", logout.panicked)
		}
		if logout.code != http.StatusOK {
			t.Errorf("logout without login returned %d, want 200; body=%q", logout.code, logout.body)
		}
	})

	t.Run("relogging with a damaged session cookie succeeds and issues usable credentials", func(t *testing.T) {
		t.Setenv("SESSION_SECRET", revocationTestSessionSecret)
		t.Setenv("JWT_SECRET", revocationTestJWTSecret)
		h := newAccessHarness(t)
		u := createRevocationUser(t, h, "revocation-damaged-session")

		damagedCookie := &http.Cookie{Name: "jelly-cms-session", Value: "damaged-session-cookie", Path: "/"}
		body := fmt.Sprintf(`{"username":%q,"password":%q}`, u.Username, revocationTestPassword)
		res := revocationRequest(h, http.MethodPost, "/auth/login", []*http.Cookie{damagedCookie}, "", body)
		if res.panicked != nil {
			t.Errorf("relogin panicked: %v", res.panicked)
			return
		}
		if res.code != http.StatusOK {
			t.Errorf("relogin with damaged session cookie returned %d, want 200; body=%q", res.code, res.body)
			return
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(res.body), &payload); err != nil {
			t.Errorf("decode relogin response: %v", err)
			return
		}
		token, _ := payload["token"].(string)
		if token == "" {
			t.Errorf("relogin response did not include a token: %q", res.body)
			return
		}
		cookies := (&http.Response{Header: res.header}).Cookies()
		sessionCookie := revocationCookie(cookies, "jelly-cms-session")
		if sessionCookie == nil {
			t.Errorf("relogin response did not set session cookie")
			return
		}
		if revocationRequest(h, http.MethodGet, "/api/auth/me", nil, token, "").code != http.StatusOK {
			t.Errorf("new JWT from damaged-cookie relogin was rejected")
		}
		if revocationRequest(h, http.MethodGet, "/api/auth/me", []*http.Cookie{sessionCookie}, "", "").code != http.StatusOK {
			t.Errorf("new session from damaged-cookie relogin was rejected")
		}
	})
}
