package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/install"
	"github.com/labstack/echo/v4"
)

const installedGateDeadline = time.Second

// signalReader reports the first Read so the test can prove that the login
// handler is consuming its body instead of relying on a sleep. The read then
// blocks on the pipe until the test releases it.
type signalReader struct {
	reader  io.Reader
	started chan struct{}
	once    sync.Once
}

func (r *signalReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	return r.reader.Read(p)
}

// gatedRequest is one request running through the real Echo routing stack in its
// own goroutine. result is written before done is closed, so reading it after
// receiving from done is safe.
type gatedRequest struct {
	recorder *httptest.ResponseRecorder
	done     chan struct{}
	result   installFlowResult
}

func startGatedRequest(e *echo.Echo, req *http.Request) *gatedRequest {
	call := &gatedRequest{
		recorder: httptest.NewRecorder(),
		done:     make(chan struct{}),
	}

	go func() {
		defer close(call.done)
		defer func() {
			if panicked := recover(); panicked != nil {
				call.result = installFlowResult{panicked: panicked}
			}
		}()

		e.ServeHTTP(call.recorder, req)
		call.result.code = call.recorder.Code
		call.result.body = call.recorder.Body.String()
	}()

	return call
}

func waitGatedRequest(t *testing.T, call *gatedRequest, label string) (installFlowResult, bool) {
	t.Helper()

	timer := time.NewTimer(installedGateDeadline)
	defer timer.Stop()

	select {
	case <-call.done:
		return call.result, true
	case <-timer.C:
		t.Errorf("%s did not finish within %s", label, installedGateDeadline)
		return installFlowResult{}, false
	}
}

func waitGatedRequestDone(t *testing.T, call *gatedRequest, label string) {
	t.Helper()

	timer := time.NewTimer(installedGateDeadline)
	defer timer.Stop()

	select {
	case <-call.done:
	case <-timer.C:
		t.Errorf("%s still had not finished within %s during cleanup", label, installedGateDeadline)
	}
}

// startBlockedLogin starts a public login request whose body never completes.
// The returned pipe writer must be closed to release the handler and its gate
// read lock before the test ends.
func startBlockedLogin(t *testing.T, h *installFlowHarness) (*io.PipeWriter, *gatedRequest) {
	t.Helper()

	reader, writer := io.Pipe()
	started := make(chan struct{})

	req := httptest.NewRequest(http.MethodPost, "/auth/login", &signalReader{
		reader:  reader,
		started: started,
	})
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
	// This trusted in-process fixture supplies the request's source evidence.
	req.Header.Set("X-Jelly-CSRF", "1")

	call := startGatedRequest(h.server, req)

	select {
	case <-started:
	case <-time.After(installedGateDeadline):
		_ = writer.Close()
		waitGatedRequestDone(t, call, "POST /auth/login")
		t.Fatalf("POST /auth/login never started reading its request body within %s, so the request cannot hold the installer gate open", installedGateDeadline)
	}

	return writer, call
}

func installedGateAdmins(t *testing.T) []core.User {
	t.Helper()

	var admins []core.User
	if err := data.DB.Where("role = ?", "admin").Order("id").Find(&admins).Error; err != nil {
		t.Fatalf("query the installed administrators: %v", err)
	}
	return admins
}

func installedGateSiteTitle(t *testing.T) string {
	t.Helper()

	var title core.Option
	if err := data.DB.Where("key = ?", "site_title").First(&title).Error; err != nil {
		t.Fatalf("query the installed site title: %v", err)
	}
	return title.Value
}

// runInstalledGateCase keeps a public login request blocked while it reads its
// body. That request holds the gate's read lock. A repeated anonymous install
// POST then asks for the write lock, and a public GET must still complete
// instead of queueing behind the writer preference of sync.RWMutex.
func runInstalledGateCase(t *testing.T, h *installFlowHarness, endpoint string) {
	t.Helper()

	writer, login := startBlockedLogin(t, h)

	var installCall, homeCall *gatedRequest
	defer func() {
		// Release the blocked body first so the read lock is returned, then
		// collect every request goroutine before the harness tears down.
		_ = writer.Close()
		waitGatedRequestDone(t, login, "POST /auth/login")
		if installCall != nil {
			waitGatedRequestDone(t, installCall, "POST "+endpoint)
		}
		if homeCall != nil {
			waitGatedRequestDone(t, homeCall, "GET /")
		}
	}()

	installReq := httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(h.installBody()))
	installReq.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	installCall = startGatedRequest(h.server, installReq)

	installResult, installFinished := waitGatedRequest(t, installCall, "POST "+endpoint)
	if installFinished {
		if installResult.panicked != nil {
			t.Errorf("POST %s panicked while the site is already installed: %v", endpoint, installResult.panicked)
		} else if installResult.code != http.StatusConflict {
			t.Errorf("POST %s returned %d, want 409 while another request holds the gate read lock; body=%q",
				endpoint, installResult.code, installResult.body)
		}
	}

	// If the install POST is still outstanding, its write lock is queued and a
	// public read must not be made to wait behind the RWMutex writer preference.
	homeReq := httptest.NewRequest(http.MethodGet, "/", nil)
	homeCall = startGatedRequest(h.server, homeReq)

	homeResult, homeFinished := waitGatedRequest(t, homeCall, "GET /")
	if homeFinished {
		if homeResult.panicked != nil {
			t.Errorf("GET / panicked while a repeated install request was waiting: %v", homeResult.panicked)
		} else if homeResult.code != http.StatusOK {
			t.Errorf("GET / returned %d, want 200 while a repeated install request was waiting; body=%q",
				homeResult.code, homeResult.body)
		}
	}
}

// TestInstalledInstallerDoesNotBlockTraffic reproduces the installed-site
// regression where a repeated install POST requests the gate write lock before
// checking the installed state. A slow public request then holds the read lock,
// the writer blocks, and every later reader queues behind it.
func TestInstalledInstallerDoesNotBlockTraffic(t *testing.T) {
	endpoints := []string{"/api/install", "/api/install/test-db"}

	for _, endpoint := range endpoints {
		t.Run(strings.TrimPrefix(endpoint, "/"), func(t *testing.T) {
			h := newInstallFlowHarness(t)
			h.expectInstallerOpen()
			h.installSite()
			h.loginSite()

			beforeAdmins := installedGateAdmins(t)
			if len(beforeAdmins) != 1 {
				t.Fatalf("admin count before the repeated install requests = %d, want exactly 1", len(beforeAdmins))
			}
			beforeTitle := installedGateSiteTitle(t)
			if beforeTitle != installFlowSiteTitle {
				t.Fatalf("site title before the repeated install requests = %q, want %q", beforeTitle, installFlowSiteTitle)
			}

			runInstalledGateCase(t, h, endpoint)

			afterAdmins := installedGateAdmins(t)
			if len(afterAdmins) != len(beforeAdmins) {
				t.Errorf("admin count after POST %s = %d, want the unchanged %d", endpoint, len(afterAdmins), len(beforeAdmins))
			} else {
				for i := range beforeAdmins {
					if afterAdmins[i].ID != beforeAdmins[i].ID ||
						afterAdmins[i].Username != beforeAdmins[i].Username ||
						afterAdmins[i].Email != beforeAdmins[i].Email {
						t.Errorf("administrator %d changed after POST %s: got %+v, want %+v",
							i, endpoint, afterAdmins[i], beforeAdmins[i])
					}
				}
			}

			if afterTitle := installedGateSiteTitle(t); afterTitle != beforeTitle {
				t.Errorf("site title after POST %s = %q, want the unchanged %q", endpoint, afterTitle, beforeTitle)
			}
			if !install.IsInstalled() {
				t.Errorf("install.IsInstalled() = false after POST %s, want the site to stay installed", endpoint)
			}
		})
	}
}
