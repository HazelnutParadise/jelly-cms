package server

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/i18n"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
)

// newInstallRoutesServer builds the real RegisterRoutes stack in a temporary
// working directory. The web assets are linked from the repository so the
// handlers can load templates and locales without writing to the checkout.
func newInstallRoutesServer(t *testing.T) *echo.Echo {
	t.Helper()

	root := repoRoot(t)
	work := t.TempDir()
	if err := os.Symlink(filepath.Join(root, "web"), filepath.Join(work, "web")); err != nil {
		t.Fatalf("link the web assets into the temporary working directory: %v", err)
	}
	t.Chdir(work)

	previousBundle := i18n.Bundle
	t.Cleanup(func() { i18n.Bundle = previousBundle })

	i18n.Init()
	if i18n.Bundle == nil {
		t.Fatal("i18n.Init() left the bundle nil")
	}
	if _, err := i18n.GetLocalizer("zh-TW").Localize(&goi18n.LocalizeConfig{MessageID: "welcome"}); err != nil {
		t.Fatalf("the zh-TW locale was not loaded: %v", err)
	}

	previousConfig := config.GlobalConfig
	previousDB := data.DB
	config.GlobalConfig = nil
	data.DB = nil
	t.Cleanup(func() {
		config.GlobalConfig = previousConfig
		data.DB = previousDB
	})

	e := echo.New()
	RegisterRoutes(e, theme.NewManager("web/themes"), nil)
	return e
}

// installValidConfig represents a site that already completed installation but
// whose database is down after a restart: the config is valid and the live
// database handle is nil.
func installValidConfig(t *testing.T) {
	t.Helper()

	cfg := &config.Config{Database: config.DatabaseConfig{
		Host:     "127.0.0.1",
		Port:     "1",
		User:     "jelly",
		Password: "ExistingDbPass123",
		Name:     "jelly",
		Timezone: "UTC",
	}}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("save the installed site config: %v", err)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatalf("load the installed site config: %v", err)
	}
	config.GlobalConfig = loaded
	data.DB = nil
}

// resetUninstalled clears every trace of a previous installation so a case
// cannot inherit config or state from an earlier one.
func resetUninstalled(t *testing.T) {
	t.Helper()

	config.GlobalConfig = nil
	data.DB = nil
	if err := os.RemoveAll("data"); err != nil {
		t.Fatalf("clear the temporary data directory: %v", err)
	}
}

type routeResponse struct {
	recorder *httptest.ResponseRecorder
	panicked any
}

func callRoute(t *testing.T, e *echo.Echo, method, target, body string) routeResponse {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	if body != "" {
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	rec := httptest.NewRecorder()

	var panicked any
	func() {
		defer func() { panicked = recover() }()
		e.ServeHTTP(rec, req)
	}()

	return routeResponse{recorder: rec, panicked: panicked}
}

// countingListener accepts and closes connections on a local port so a test can
// tell whether a handler actually tried to reach the database. Nothing else
// listens on the returned port.
func countingListener(t *testing.T) (port string, attempts *atomic.Int32) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on a local port to detect connection attempts: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	attempts = &atomic.Int32{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			attempts.Add(1)
			_ = conn.Close()
		}
	}()

	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port), attempts
}

// observedAttempts gives the accept loop a moment to record a connection that
// the handler opened, without turning a correct implementation into a slow
// test.
func observedAttempts(attempts *atomic.Int32) int32 {
	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		seen := attempts.Load()
		if seen > 0 || time.Now().After(deadline) {
			return seen
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func validInstallPayload(dbPass string) map[string]string {
	return map[string]string{
		"db_host":     "127.0.0.1",
		"db_port":     "1",
		"db_user":     "jelly",
		"db_pass":     dbPass,
		"db_name":     "jelly",
		"db_timezone": "UTC",
		"site_title":  "Boundary Site",
		"admin_user":  "admin",
		"admin_email": "admin@example.com",
		"admin_pass":  "AdminBoundary123!",
	}
}

func marshalPayload(t *testing.T, payload map[string]string) string {
	t.Helper()

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode the request payload: %v", err)
	}
	return string(raw)
}

func assertNoSensitiveResponse(t *testing.T, body string, forbidden ...string) {
	t.Helper()

	for _, value := range forbidden {
		if value == "" {
			continue
		}
		if strings.Contains(body, value) {
			t.Errorf("response exposes %q: %q", value, body)
		}
	}
}

// TestInstallRoutesInstalledSiteWithDatabaseDown covers the restart case where
// a valid config exists but the database is unreachable. Installation must stay
// closed and neither install endpoint may attempt a new connection.
func TestInstallRoutesInstalledSiteWithDatabaseDown(t *testing.T) {
	e := newInstallRoutesServer(t)

	t.Run("GET /install does not render the installer", func(t *testing.T) {
		installValidConfig(t)

		response := callRoute(t, e, http.MethodGet, "/install", "")
		if response.panicked != nil {
			t.Fatalf("GET /install panicked: %v", response.panicked)
		}

		body := response.recorder.Body.String()
		if response.recorder.Code == http.StatusOK {
			t.Fatalf("GET /install returned 200 for an installed site with a down database, want the installer to stay closed; body=%q", body)
		}
		if strings.Contains(body, "Install Jelly CMS") {
			t.Fatalf("GET /install returned the installation form for an installed site with a down database; body=%q", body)
		}
	})

	t.Run("POST /api/install is rejected without connecting", func(t *testing.T) {
		installValidConfig(t)
		port, attempts := countingListener(t)

		payload := validInstallPayload("SecretBoundary123")
		payload["db_port"] = port
		response := callRoute(t, e, http.MethodPost, "/api/install", marshalPayload(t, payload))
		if response.panicked != nil {
			t.Fatalf("POST /api/install panicked: %v", response.panicked)
		}

		body := response.recorder.Body.String()
		if response.recorder.Code != http.StatusForbidden && response.recorder.Code != http.StatusConflict {
			t.Errorf("POST /api/install returned %d, want 403 or 409 for an already installed site; body=%q", response.recorder.Code, body)
		}
		if seen := observedAttempts(attempts); seen != 0 {
			t.Errorf("POST /api/install opened %d database connection(s) although the site is already installed", seen)
		}
	})

	t.Run("POST /api/install/test-db is rejected without connecting", func(t *testing.T) {
		installValidConfig(t)
		port, attempts := countingListener(t)

		payload := validInstallPayload("SecretBoundary123")
		payload["db_port"] = port
		response := callRoute(t, e, http.MethodPost, "/api/install/test-db", marshalPayload(t, payload))
		if response.panicked != nil {
			t.Fatalf("POST /api/install/test-db panicked: %v", response.panicked)
		}

		body := response.recorder.Body.String()
		if response.recorder.Code != http.StatusForbidden && response.recorder.Code != http.StatusConflict {
			t.Errorf("POST /api/install/test-db returned %d, want 403 or 409 for an already installed site; body=%q", response.recorder.Code, body)
		}
		if seen := observedAttempts(attempts); seen != 0 {
			t.Errorf("POST /api/install/test-db opened %d database connection(s) although the site is already installed", seen)
		}
	})
}

// TestUnavailableSiteWhenDatabaseDown checks the general site endpoints while
// the database is down. They must answer with a plain 503 instead of redirecting
// visitors into the installer or panicking on the nil database handle.
func TestUnavailableSiteWhenDatabaseDown(t *testing.T) {
	e := newInstallRoutesServer(t)
	installValidConfig(t)

	for _, target := range []string{"/", "/api/oauth/status", "/login"} {
		t.Run(target, func(t *testing.T) {
			response := callRoute(t, e, http.MethodGet, target, "")
			if response.panicked != nil {
				t.Fatalf("GET %s panicked although the database handle is nil: %v", target, response.panicked)
			}

			if response.recorder.Code != http.StatusServiceUnavailable {
				t.Errorf("GET %s returned %d, want 503 while the database is down; location=%q body=%q",
					target, response.recorder.Code, response.recorder.Header().Get("Location"), response.recorder.Body.String())
			}
			if location := response.recorder.Header().Get("Location"); strings.Contains(location, "/install") {
				t.Errorf("GET %s redirected to the installer while the site is installed: Location=%q", target, location)
			}
		})
	}
}

// TestInstallRoutesRejectInvalidInput checks that invalid installation input is
// rejected with 400 before anything is persisted or dialed, and that the error
// does not echo passwords, connection details, or file paths.
func TestInstallRoutesRejectInvalidInput(t *testing.T) {
	e := newInstallRoutesServer(t)

	const dbPass = "SecretBoundary123"
	const adminPass = "AdminBoundary123!"

	cases := []struct {
		name   string
		mutate func(map[string]string)
	}{
		{name: "blank admin username", mutate: func(p map[string]string) { p["admin_user"] = "   " }},
		{name: "empty admin email", mutate: func(p map[string]string) { p["admin_email"] = "" }},
		{name: "invalid admin email", mutate: func(p map[string]string) { p["admin_email"] = "not-an-email" }},
		{name: "blank site title", mutate: func(p map[string]string) { p["site_title"] = "  \t " }},
		{name: "short admin password", mutate: func(p map[string]string) { p["admin_pass"] = "Short1!" }},
		{name: "overlong admin password", mutate: func(p map[string]string) { p["admin_pass"] = strings.Repeat("a", 73) }},
		{name: "blank database name", mutate: func(p map[string]string) { p["db_name"] = "   " }},
		{name: "invalid database port", mutate: func(p map[string]string) { p["db_port"] = "not-a-port" }},
		{name: "invalid timezone", mutate: func(p map[string]string) { p["db_timezone"] = "Not/A-Timezone" }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetUninstalled(t)

			payload := validInstallPayload(dbPass)
			payload["admin_pass"] = adminPass
			tc.mutate(payload)

			response := callRoute(t, e, http.MethodPost, "/api/install", marshalPayload(t, payload))
			if response.panicked != nil {
				t.Fatalf("POST /api/install panicked for %s: %v", tc.name, response.panicked)
			}

			body := response.recorder.Body.String()
			if response.recorder.Code != http.StatusBadRequest {
				t.Errorf("POST /api/install with %s returned %d, want 400 before any persistence or connection; body=%q",
					tc.name, response.recorder.Code, body)
			}
			assertNoSensitiveResponse(t, body, dbPass, adminPass, repoRoot(t), "data/config.json", "host=", "password=")

			if _, err := os.Stat(filepath.Join("data", "config.json")); !os.IsNotExist(err) {
				t.Errorf("POST /api/install with %s wrote data/config.json (stat error %v), want nothing persisted before validation", tc.name, err)
			}
		})
	}
}

// TestInstallRoutesTestDatabaseErrorDoesNotExposeSecrets sends a test-connection
// request to an unreachable local port and checks that the failure message is
// generic: no password, host, port, or internal path.
func TestInstallRoutesTestDatabaseErrorDoesNotExposeSecrets(t *testing.T) {
	e := newInstallRoutesServer(t)
	resetUninstalled(t)

	const dbPass = "SecretBoundary123"
	const adminPass = "AdminBoundary123!"

	payload := validInstallPayload(dbPass)
	payload["db_host"] = "127.0.0.1"
	payload["db_port"] = "1"
	payload["admin_pass"] = adminPass

	response := callRoute(t, e, http.MethodPost, "/api/install/test-db", marshalPayload(t, payload))
	if response.panicked != nil {
		t.Fatalf("POST /api/install/test-db panicked: %v", response.panicked)
	}

	body := response.recorder.Body.String()
	if response.recorder.Code != http.StatusBadRequest {
		t.Errorf("POST /api/install/test-db against an unreachable database returned %d, want 400; body=%q", response.recorder.Code, body)
	}
	assertNoSensitiveResponse(t, body,
		dbPass,
		adminPass,
		"127.0.0.1",
		"127.0.0.1:1",
		":1",
		"port=1",
		"host=",
		"dbname=",
		"sslmode",
		repoRoot(t),
		"data/config.json",
	)
}
