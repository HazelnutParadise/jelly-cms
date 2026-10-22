package auth_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
)

const (
	authSecretsHelperEnv     = "JELLY_AUTH_SECRETS_HELPER"
	authSecretsModeEnv       = "JELLY_AUTH_SECRETS_MODE"
	authSecretsTokenFileEnv  = "JELLY_AUTH_SECRETS_TOKEN_FILE"
	authSecretsCookieFileEnv = "JELLY_AUTH_SECRETS_COOKIE_FILE"
)

func callAuthInit() error {
	value := any(auth.Init)
	switch initFn := value.(type) {
	case func() error:
		return initFn()
	case func():
		initFn()
		return nil
	default:
		return fmt.Errorf("unexpected Init signature %T", value)
	}
}

func runAuthSecretsChild(t *testing.T, dir, mode string, extraEnv ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAuthSecretsHelperProcess$", "-test.count=1")
	cmd.Dir = dir
	env := make([]string, 0, len(os.Environ())+len(extraEnv)+4)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "SESSION_SECRET=") ||
			strings.HasPrefix(entry, "JWT_SECRET=") ||
			strings.HasPrefix(entry, "APP_ENV=") ||
			strings.HasPrefix(entry, authSecretsHelperEnv+"=") ||
			strings.HasPrefix(entry, authSecretsModeEnv+"=") ||
			strings.HasPrefix(entry, authSecretsTokenFileEnv+"=") ||
			strings.HasPrefix(entry, authSecretsCookieFileEnv+"=") {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, authSecretsHelperEnv+"=1")
	env = append(env, authSecretsModeEnv+"="+mode)
	env = append(env, extraEnv...)
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(output), ctx.Err()
	}
	return string(output), err
}

func decodeBase64Secret(raw string) ([]byte, error) {
	if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil {
		return decoded, nil
	}
	return base64.RawStdEncoding.DecodeString(raw)
}

func TestAuthSecretsHelperProcess(t *testing.T) {
	if os.Getenv(authSecretsHelperEnv) != "1" {
		t.Skip("helper process only")
	}

	mode := os.Getenv(authSecretsModeEnv)
	switch mode {
	case "init-generate":
		if err := callAuthInit(); err != nil {
			t.Fatalf("Init() error = %v", err)
		}
		e := echo.New()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		c := e.NewContext(req, rec)
		if err := auth.SetUserSession(c, 42); err != nil {
			t.Fatalf("SetUserSession() error = %v", err)
		}
		cookies := rec.Result().Cookies()
		if len(cookies) == 0 {
			t.Fatal("SetUserSession() did not write a cookie")
		}
		token, err := auth.GenerateJWT(42, "auth-secret-test@example.test", "author")
		if err != nil {
			t.Fatalf("GenerateJWT() error = %v", err)
		}
		if err := os.WriteFile(os.Getenv(authSecretsTokenFileEnv), []byte(token), 0600); err != nil {
			t.Fatalf("write token handoff: %v", err)
		}
		if err := os.WriteFile(os.Getenv(authSecretsCookieFileEnv), []byte(cookies[0].Value), 0600); err != nil {
			t.Fatalf("write cookie handoff: %v", err)
		}

	case "verify":
		if err := callAuthInit(); err != nil {
			t.Fatalf("Init() error = %v", err)
		}
		tokenBytes, err := os.ReadFile(os.Getenv(authSecretsTokenFileEnv))
		if err != nil {
			t.Fatalf("read token handoff: %v", err)
		}
		cookieBytes, err := os.ReadFile(os.Getenv(authSecretsCookieFileEnv))
		if err != nil {
			t.Fatalf("read cookie handoff: %v", err)
		}
		claims, err := auth.ValidateJWT(string(tokenBytes))
		if err != nil {
			t.Fatalf("ValidateJWT() error = %v", err)
		}
		if got, ok := claims["user_id"].(float64); !ok || got != 42 {
			t.Fatalf("ValidateJWT() user_id = %#v, want 42", claims["user_id"])
		}
		e := echo.New()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: "jelly-cms-session", Value: string(cookieBytes)})
		c := e.NewContext(req, rec)
		userID, err := auth.GetUserIDFromSession(c)
		if err != nil {
			t.Fatalf("GetUserIDFromSession() error = %v", err)
		}
		if userID != 42 {
			t.Fatalf("GetUserIDFromSession() = %d, want 42", userID)
		}

	case "reject":
		if err := callAuthInit(); err != nil {
			t.Fatalf("Init() error = %v", err)
		}
		tokenBytes, err := os.ReadFile(os.Getenv(authSecretsTokenFileEnv))
		if err != nil {
			t.Fatalf("read token handoff: %v", err)
		}
		cookieBytes, err := os.ReadFile(os.Getenv(authSecretsCookieFileEnv))
		if err != nil {
			t.Fatalf("read cookie handoff: %v", err)
		}
		if _, err := auth.ValidateJWT(string(tokenBytes)); err == nil {
			t.Errorf("ValidateJWT() accepted a token from another site")
		}
		e := echo.New()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: "jelly-cms-session", Value: string(cookieBytes)})
		c := e.NewContext(req, rec)
		if _, err := auth.GetUserIDFromSession(c); err == nil {
			t.Fatal("GetUserIDFromSession() accepted a cookie from another site")
		}

	case "init-only":
		if err := callAuthInit(); err != nil {
			t.Fatalf("Init() error = %v", err)
		}

	case "init-expect-error":
		if err := callAuthInit(); err == nil {
			t.Fatal("Init() succeeded, want an error")
		}

	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
}

func TestAuthSecrets(t *testing.T) {
	t.Run("first init creates a 0600 secret file and restart verifies both credentials", func(t *testing.T) {
		dir := t.TempDir()
		tokenFile := filepath.Join(dir, "token.txt")
		cookieFile := filepath.Join(dir, "cookie.txt")

		output, err := runAuthSecretsChild(t, dir, "init-generate",
			authSecretsTokenFileEnv+"="+tokenFile,
			authSecretsCookieFileEnv+"="+cookieFile,
		)
		if err != nil {
			t.Fatalf("first Init child failed: %v\n%s", err, output)
		}

		secretPath := filepath.Join(dir, "data", "auth-secrets.json")
		dataBytes, err := os.ReadFile(secretPath)
		if err != nil {
			t.Fatalf("secret file was not created: %v", err)
		}
		var doc map[string]any
		if err := json.Unmarshal(dataBytes, &doc); err != nil {
			t.Fatalf("secret file is not valid JSON: %v", err)
		}
		if got, ok := doc["version"].(float64); !ok || int(got) != 1 {
			t.Fatalf("version = %#v, want 1", doc["version"])
		}
		sessionRaw, hasSession := doc["session_secret"].(string)
		jwtRaw, hasJWT := doc["jwt_secret"].(string)
		if !hasSession || !hasJWT {
			t.Fatalf("secret file missing fields: keys=%v", mapKeys(doc))
		}
		sessionBytes, err := decodeBase64Secret(sessionRaw)
		if err != nil || len(sessionBytes) < 32 {
			t.Fatalf("session_secret is not base64 >= 32 bytes (len=%d)", len(sessionRaw))
		}
		jwtBytes, err := decodeBase64Secret(jwtRaw)
		if err != nil || len(jwtBytes) < 32 {
			t.Fatalf("jwt_secret is not base64 >= 32 bytes (len=%d)", len(jwtRaw))
		}
		if bytes.Equal(sessionBytes, jwtBytes) {
			t.Fatal("session and JWT secrets are identical")
		}
		if string(sessionBytes) == "jelly-cms-secret-key-change-in-production" {
			t.Fatal("session secret is the old fixed default")
		}
		if string(jwtBytes) == "jelly-cms-secret-key-change-in-production" {
			t.Fatal("JWT secret is the old fixed default")
		}
		if runtime.GOOS != "windows" {
			info, err := os.Stat(secretPath)
			if err != nil {
				t.Fatalf("stat secret file: %v", err)
			}
			if info.Mode().Perm() != 0600 {
				t.Fatalf("secret file mode = %v, want 0600", info.Mode().Perm())
			}
		}

		output, err = runAuthSecretsChild(t, dir, "verify",
			authSecretsTokenFileEnv+"="+tokenFile,
			authSecretsCookieFileEnv+"="+cookieFile,
		)
		if err != nil {
			t.Fatalf("restart verification failed: %v\n%s", err, output)
		}

		otherDir := t.TempDir()
		output, err = runAuthSecretsChild(t, otherDir, "reject",
			authSecretsTokenFileEnv+"="+tokenFile,
			authSecretsCookieFileEnv+"="+cookieFile,
		)
		if err != nil {
			t.Fatalf("cross-directory rejection child failed: %v\n%s", err, output)
		}
	})

	t.Run("both explicit environment values work without a file", func(t *testing.T) {
		dir := t.TempDir()
		oldWD, err := os.Getwd()
		if err != nil {
			t.Fatalf("getwd: %v", err)
		}
		if err := os.Chdir(dir); err != nil {
			t.Fatalf("chdir: %v", err)
		}
		t.Cleanup(func() { _ = os.Chdir(oldWD) })

		previousDB := data.DB
		data.DB = nil
		t.Cleanup(func() { data.DB = previousDB })

		const sessionSecret = "explicit-session-secret-for-auth-test"
		const jwtSecret = "explicit-jwt-secret-for-auth-test"
		t.Setenv("SESSION_SECRET", sessionSecret)
		t.Setenv("JWT_SECRET", jwtSecret)
		t.Setenv("APP_ENV", "")

		if err := callAuthInit(); err != nil {
			t.Fatalf("Init() error = %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "data", "auth-secrets.json")); !os.IsNotExist(err) {
			t.Fatalf("secret file exists with both env values: %v", err)
		}

		e := echo.New()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		c := e.NewContext(req, rec)
		if err := auth.SetUserSession(c, 42); err != nil {
			t.Fatalf("SetUserSession() error = %v", err)
		}
		cookies := rec.Result().Cookies()
		if len(cookies) == 0 {
			t.Fatal("SetUserSession() did not write a cookie")
		}
		cookieValue := cookies[0].Value

		token, err := auth.GenerateJWT(42, "env-test@example.test", "author")
		if err != nil {
			t.Fatalf("GenerateJWT() error = %v", err)
		}
		claims, err := auth.ValidateJWT(token)
		if err != nil {
			t.Fatalf("ValidateJWT() error = %v", err)
		}
		if got, ok := claims["user_id"].(float64); !ok || got != 42 {
			t.Fatalf("user_id = %#v, want 42", claims["user_id"])
		}

		// Independent JWT verification with the known JWT env secret.
		parsed, err := jwt.Parse(token, func(tok *jwt.Token) (interface{}, error) {
			if tok.Method != jwt.SigningMethodHS256 {
				return nil, fmt.Errorf("unexpected signing method")
			}
			return []byte(jwtSecret), nil
		})
		if err != nil || !parsed.Valid {
			t.Fatalf("independent JWT verification failed: %v", err)
		}
		// Independent session cookie verification with the known SESSION env secret.
		store := sessions.NewCookieStore([]byte(sessionSecret))
		req2 := httptest.NewRequest(http.MethodGet, "/", nil)
		req2.AddCookie(&http.Cookie{Name: "jelly-cms-session", Value: cookieValue})
		sess, err := store.Get(req2, "jelly-cms-session")
		if err != nil {
			t.Fatalf("independent session decode failed: %v", err)
		}
		userID, ok := sess.Values["user_id"].(uint)
		if !ok || userID != 42 {
			t.Fatalf("independent session user_id = %#v, want 42", sess.Values["user_id"])
		}

		// Same SESSION_SECRET but a different JWT_SECRET must not validate.
		t.Setenv("JWT_SECRET", "different-jwt-secret-for-auth-test")
		if err := callAuthInit(); err != nil {
			t.Fatalf("Init() error = %v", err)
		}
		if _, err := auth.ValidateJWT(token); err == nil {
			t.Fatal("ValidateJWT() succeeded with a different JWT_SECRET")
		}
	})

	t.Run("partial environment saves only generated values", func(t *testing.T) {
		// Session provided, JWT generated.
		t.Run("session provided jwt generated", func(t *testing.T) {
			runPartialEnvCase(t, "provided-session-secret-for-auth-test", "", "session")
		})
		// JWT provided, session generated.
		t.Run("jwt provided session generated", func(t *testing.T) {
			runPartialEnvCase(t, "", "provided-jwt-secret-for-auth-test", "jwt")
		})
	})

	t.Run("empty secret file causes Init error without changing it", func(t *testing.T) {
		checkFileInitError(t, []byte{})
	})
	t.Run("corrupt JSON causes Init error without changing it", func(t *testing.T) {
		checkFileInitError(t, []byte("{not json"))
	})
	t.Run("missing fields cause Init error without changing it", func(t *testing.T) {
		checkFileInitError(t, []byte(`{"version":1}`))
	})
	t.Run("short decoded key causes Init error without changing it", func(t *testing.T) {
		checkFileInitError(t, []byte(`{"version":1,"session_secret":"AAAA","jwt_secret":"AAAA"}`))
	})
	t.Run("invalid base64 key causes Init error without changing it", func(t *testing.T) {
		valid := base64.StdEncoding.EncodeToString(make([]byte, 32))
		checkFileInitError(t, []byte(fmt.Sprintf(`{"version":1,"session_secret":"not-base64!!!","jwt_secret":%q}`, valid)))
	})
	t.Run("unsupported version causes Init error without changing it", func(t *testing.T) {
		valid := base64.StdEncoding.EncodeToString(make([]byte, 32))
		checkFileInitError(t, []byte(fmt.Sprintf(`{"version":2,"session_secret":%q,"jwt_secret":%q}`, valid, valid)))
	})
	t.Run("secret path as directory causes Init error", func(t *testing.T) {
		dir := t.TempDir()
		dataDir := filepath.Join(dir, "data")
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			t.Fatalf("mkdir data: %v", err)
		}
		secretPath := filepath.Join(dataDir, "auth-secrets.json")
		if err := os.Mkdir(secretPath, 0755); err != nil {
			t.Fatalf("mkdir secret path: %v", err)
		}
		output, err := runAuthSecretsChild(t, dir, "init-expect-error")
		if err != nil {
			t.Fatalf("child did not report Init error: %v\n%s", err, output)
		}
		info, err := os.Stat(secretPath)
		if err != nil || !info.IsDir() {
			t.Fatalf("secret path is no longer a directory: %v", err)
		}
	})
	t.Run("data path occupied by a regular file causes Init error", func(t *testing.T) {
		dir := t.TempDir()
		dataPath := filepath.Join(dir, "data")
		if err := os.WriteFile(dataPath, []byte("not a directory"), 0600); err != nil {
			t.Fatalf("write data file: %v", err)
		}
		output, err := runAuthSecretsChild(t, dir, "init-expect-error")
		if err != nil {
			t.Fatalf("child did not report Init error: %v\n%s", err, output)
		}
		info, err := os.Stat(dataPath)
		if err != nil || info.IsDir() {
			t.Fatalf("data path changed type: %v", err)
		}
	})

	t.Run("parallel first Init leaves a complete secret file", func(t *testing.T) {
		dir := t.TempDir()
		type gen struct {
			tokenFile  string
			cookieFile string
		}
		gens := []gen{
			{filepath.Join(dir, "token0.txt"), filepath.Join(dir, "cookie0.txt")},
			{filepath.Join(dir, "token1.txt"), filepath.Join(dir, "cookie1.txt")},
		}
		outputs := make([]string, len(gens))
		errs := make([]error, len(gens))
		var wg sync.WaitGroup
		for i := range gens {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				outputs[i], errs[i] = runAuthSecretsChild(t, dir, "init-generate",
					authSecretsTokenFileEnv+"="+gens[i].tokenFile,
					authSecretsCookieFileEnv+"="+gens[i].cookieFile,
				)
			}(i)
		}
		wg.Wait()
		for i := range gens {
			if errs[i] != nil {
				t.Fatalf("parallel Init child %d failed: %v\n%s", i, errs[i], outputs[i])
			}
		}

		secretPath := filepath.Join(dir, "data", "auth-secrets.json")
		dataBytes, err := os.ReadFile(secretPath)
		if err != nil {
			t.Fatalf("secret file missing after parallel Init: %v", err)
		}
		var doc map[string]any
		if err := json.Unmarshal(dataBytes, &doc); err != nil {
			t.Fatalf("secret file is not valid JSON: %v", err)
		}
		if got, ok := doc["version"].(float64); !ok || int(got) != 1 {
			t.Fatalf("version = %#v, want 1", doc["version"])
		}

		for i := range gens {
			out, err := runAuthSecretsChild(t, dir, "verify",
				authSecretsTokenFileEnv+"="+gens[i].tokenFile,
				authSecretsCookieFileEnv+"="+gens[i].cookieFile,
			)
			if err != nil {
				t.Fatalf("post-race verification %d failed: %v\n%s", i, err, out)
			}
		}
	})

	t.Run("old default key signed JWT is rejected", func(t *testing.T) {
		dir := t.TempDir()
		oldWD, err := os.Getwd()
		if err != nil {
			t.Fatalf("getwd: %v", err)
		}
		if err := os.Chdir(dir); err != nil {
			t.Fatalf("chdir: %v", err)
		}
		t.Cleanup(func() { _ = os.Chdir(oldWD) })

		previousDB := data.DB
		data.DB = nil
		t.Cleanup(func() { data.DB = previousDB })

		t.Setenv("SESSION_SECRET", "")
		t.Setenv("JWT_SECRET", "")
		t.Setenv("APP_ENV", "")

		if err := callAuthInit(); err != nil {
			t.Fatalf("Init() error = %v", err)
		}
		claims := jwt.MapClaims{
			"user_id": float64(42),
			"exp":     time.Now().Add(time.Hour).Unix(),
		}
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("jelly-cms-secret-key-change-in-production"))
		if err != nil {
			t.Fatalf("sign old-default token: %v", err)
		}
		if _, err := auth.ValidateJWT(token); err == nil {
			t.Fatal("ValidateJWT() accepted a token signed with the old default key")
		}
	})
}

func runPartialEnvCase(t *testing.T, providedSession, providedJWT, providedKind string) {
	t.Helper()
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token.txt")
	cookieFile := filepath.Join(dir, "cookie.txt")

	extra := []string{
		authSecretsTokenFileEnv + "=" + tokenFile,
		authSecretsCookieFileEnv + "=" + cookieFile,
	}
	if providedKind == "session" {
		extra = append(extra, "SESSION_SECRET="+providedSession, "JWT_SECRET=")
	} else {
		extra = append(extra, "SESSION_SECRET=", "JWT_SECRET="+providedJWT)
	}

	output, err := runAuthSecretsChild(t, dir, "init-generate", extra...)
	if err != nil {
		t.Fatalf("partial Init child failed: %v\n%s", err, output)
	}

	secretPath := filepath.Join(dir, "data", "auth-secrets.json")
	dataBytes, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatalf("secret file was not created: %v", err)
	}
	if providedKind == "session" {
		if strings.Contains(string(dataBytes), providedSession) {
			t.Fatal("provided SESSION_SECRET was written to the secret file")
		}
	} else {
		if strings.Contains(string(dataBytes), providedJWT) {
			t.Fatal("provided JWT_SECRET was written to the secret file")
		}
	}
	var doc map[string]any
	if err := json.Unmarshal(dataBytes, &doc); err != nil {
		t.Fatalf("secret file is not valid JSON: %v", err)
	}
	if providedKind == "session" {
		if jwtRaw, ok := doc["jwt_secret"].(string); !ok {
			t.Fatalf("generated jwt_secret missing: field type=%T", doc["jwt_secret"])
		} else {
			jwtBytes, err := decodeBase64Secret(jwtRaw)
			if err != nil || len(jwtBytes) < 32 {
				t.Fatalf("generated jwt_secret is invalid (len=%d)", len(jwtRaw))
			}
		}
		if sessionRaw, ok := doc["session_secret"].(string); ok {
			sessionBytes, err := decodeBase64Secret(sessionRaw)
			if err == nil && string(sessionBytes) == providedSession {
				t.Fatal("provided SESSION_SECRET was encoded into the secret file")
			}
		}
	} else {
		if sessionRaw, ok := doc["session_secret"].(string); !ok {
			t.Fatalf("generated session_secret missing: field type=%T", doc["session_secret"])
		} else {
			sessionBytes, err := decodeBase64Secret(sessionRaw)
			if err != nil || len(sessionBytes) < 32 {
				t.Fatalf("generated session_secret is invalid (len=%d)", len(sessionRaw))
			}
		}
		if jwtRaw, ok := doc["jwt_secret"].(string); ok {
			jwtBytes, err := decodeBase64Secret(jwtRaw)
			if err == nil && string(jwtBytes) == providedJWT {
				t.Fatal("provided JWT_SECRET was encoded into the secret file")
			}
		}
	}

	// Restart verification.
	out, err := runAuthSecretsChild(t, dir, "verify", extra...)
	if err != nil {
		t.Fatalf("partial env restart verification failed: %v\n%s", err, out)
	}
}

func checkFileInitError(t *testing.T, original []byte) {
	t.Helper()
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	secretPath := filepath.Join(dataDir, "auth-secrets.json")
	if err := os.WriteFile(secretPath, original, 0600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}

	output, err := runAuthSecretsChild(t, dir, "init-expect-error")
	if err != nil {
		t.Fatalf("child did not report Init error: %v\n%s", err, output)
	}

	after, readErr := os.ReadFile(secretPath)
	if readErr != nil {
		t.Fatalf("read secret file after Init: %v", readErr)
	}
	if !bytes.Equal(after, original) {
		t.Errorf("Init changed the secret file (before len=%d, after len=%d)", len(original), len(after))
	}
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
