package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/markbates/goth/providers/github"
	"github.com/markbates/goth/providers/google"
)

const (
	oauthHTTPBudgetID     = "oauth-budget-client"
	oauthHTTPBudgetSecret = "oauth-budget-secret"
)

// This route-level check deliberately waits through both real Goth operations:
// the token exchange consumes twenty seconds and profile retrieval remains
// blocked until the shared 30-second callback budget expires.
func TestOAuthHTTPBudgetTotalProviderOperationIsThirtySeconds(t *testing.T) {
	for _, name := range []string{"google", "github"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
			t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
			t.Setenv("APP_ENV", "test")
			h := oauthTxnPrepareHarness(t)
			before := accessUsers(t, h.db)
			var response *httptest.ResponseRecorder
			var transport *oauthHTTPBudgetTransport
			var profileElapsed time.Duration
			synctest.Test(t, func(t *testing.T) {
				transport = oauthHTTPBudgetNewTransport()
				transport.tokenDelay = 20 * time.Second
				transport.blockProfile = true
				defer transport.release()
				provider := oauthHTTPBudgetProvider(name, transport, nil)
				goth.UseProviders(provider)
				entry := oauthHTTPBudgetBegin(t, h, name)
				done := make(chan *httptest.ResponseRecorder, 1)
				callbackStarted := time.Now()
				fallback := time.AfterFunc(31*time.Second, transport.release)
				defer fallback.Stop()
				go func() { done <- oauthHTTPBudgetCallback(h, entry, "budget-code", nil) }()
				select {
				case <-transport.profileStarted:
				case <-time.After(25 * time.Second):
					transport.release()
					select {
					case response = <-done:
					case <-time.After(time.Second):
						t.Fatal("callback did not join after test release")
					}
					t.Fatal("callback did not reach its profile request")
				}
				select {
				case response = <-done:
				case <-time.After(12 * time.Second):
					transport.release()
					select {
					case response = <-done:
					case <-time.After(time.Second):
						t.Fatal("callback did not join after test release")
					}
					t.Fatalf("%s callback did not stop at its shared 30-second provider budget", name)
				}
				transport.release()
				if callbackElapsed := time.Since(callbackStarted); callbackElapsed > 30*time.Second {
					t.Errorf("%s callback took %s from callback start, want at most 30s", name, callbackElapsed)
				}
				profileElapsed = transport.profileWait()
			})
			if response.Code != http.StatusBadGateway {
				t.Errorf("%s callback status=%d, want generic 502", name, response.Code)
			}
			if response.Body.String() != "OAuth provider authentication failed" {
				t.Errorf("%s callback body=%q, want the generic provider failure", name, response.Body.String())
			}
			assertOAuthTxnNoLoginCookie(t, response)
			if got := accessUsers(t, h.db); !sameAccessUsers(before, got) {
				t.Errorf("timed-out %s callback changed users", name)
			}
			if profileElapsed > 10*time.Second {
				t.Errorf("profile request remained active %s after the 20-second token operation", profileElapsed)
			}
		})
	}
}

func TestOAuthHTTPBudgetShorterCallbackDeadlineSurvivesHeadersAndBody(t *testing.T) {
	for _, afterHeaders := range []bool{false, true} {
		name := "before_headers"
		if afterHeaders {
			name = "body_read"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
			t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
			t.Setenv("APP_ENV", "test")
			h := oauthTxnPrepareHarness(t)
			transport := oauthHTTPBudgetNewTransport()
			transport.blockProfile = true
			transport.profileHeaders = afterHeaders
			t.Cleanup(transport.release)
			provider := oauthHTTPBudgetProvider("google", transport, nil)
			goth.UseProviders(provider)
			before := accessUsers(t, h.db)
			entry := oauthHTTPBudgetBegin(t, h, "google")
			ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
			defer cancel()
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- oauthHTTPBudgetCallback(h, entry, "short-context-code", ctx) }()
			select {
			case <-transport.profileStarted:
			case <-time.After(2 * time.Second):
				transport.release()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("callback did not join after test release")
				}
				t.Fatal("callback did not reach the profile request")
			}
			profileStartedAt := time.Now()
			fallback := time.AfterFunc(2*time.Second, transport.release)
			defer fallback.Stop()
			var response *httptest.ResponseRecorder
			select {
			case response = <-done:
				if response.Code != http.StatusBadGateway {
					t.Errorf("callback status=%d, want 502 after caller deadline", response.Code)
				}
				if response.Body.String() != "OAuth provider authentication failed" {
					t.Errorf("callback body=%q, want generic provider failure", response.Body.String())
				}
			case <-time.After(2 * time.Second):
				transport.release()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("callback did not join after test release")
				}
				t.Fatal("callback remained blocked past its original request deadline")
			}
			transport.release()
			if elapsed := time.Since(profileStartedAt); elapsed > 1500*time.Millisecond {
				t.Errorf("callback took %s after profile request began, want caller cancellation before 1.5s", elapsed)
			}
			if got := accessUsers(t, h.db); !sameAccessUsers(before, got) {
				t.Error("short-deadline callback changed users")
			}
		})
	}
}

func TestOAuthHTTPBudgetRetainsShortClientTimeoutForBodyRead(t *testing.T) {
	for _, name := range []string{"google", "github"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
			t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
			t.Setenv("APP_ENV", "test")
			h := oauthTxnPrepareHarness(t)
			transport := oauthHTTPBudgetNewTransport()
			transport.blockTokenBody = true
			t.Cleanup(transport.release)
			client := &http.Client{Timeout: 35 * time.Millisecond, Transport: transport}
			provider := oauthHTTPBudgetProvider(name, nil, client)
			goth.UseProviders(provider)
			entry := oauthHTTPBudgetBegin(t, h, name)
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- oauthHTTPBudgetCallback(h, entry, "client-timeout-code", nil) }()
			select {
			case <-transport.tokenStarted:
			case <-time.After(2 * time.Second):
				transport.release()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("callback did not join after test release")
				}
				t.Fatal("callback did not begin the token exchange")
			}
			startedAt := transport.tokenWaitStarted()
			fallback := time.AfterFunc(500*time.Millisecond, transport.release)
			defer fallback.Stop()
			var response *httptest.ResponseRecorder
			select {
			case response = <-done:
			case <-time.After(time.Second):
				transport.release()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("callback did not join after test release")
				}
				t.Fatal("callback did not finish after its short Client.Timeout")
			}
			elapsed := time.Since(startedAt)
			if response.Code != http.StatusBadGateway {
				t.Errorf("callback status=%d, want 502", response.Code)
			}
			if response.Body.String() != "OAuth provider authentication failed" {
				t.Errorf("callback body=%q, want generic provider failure", response.Body.String())
			}
			// GitHub may retry the code exchange with a second auth style; each
			// attempt has the original 35ms Client.Timeout.
			limit := 400 * time.Millisecond
			if name == "github" {
				limit = 450 * time.Millisecond
			}
			if elapsed > limit {
				t.Errorf("%s callback took %s with 35ms Client.Timeout (allowed auth-style retries: %s)", name, elapsed, limit)
			}
		})
	}
}

func TestOAuthHTTPBudgetConcurrentCallbacksKeepProviderConfigurationIsolated(t *testing.T) {
	for _, name := range []string{"google", "github"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
			t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
			t.Setenv("APP_ENV", "test")
			h := oauthTxnPrepareHarness(t)
			transport := oauthHTTPBudgetNewTransport()
			transport.blockCode = "cancel-A"
			t.Cleanup(transport.release)
			provider := oauthHTTPBudgetProvider(name, transport, nil)
			clientBefore := oauthHTTPBudgetClient(provider)
			transportBefore := clientBefore.Transport
			defaultClient, defaultTransport := http.DefaultClient, http.DefaultTransport
			goth.UseProviders(provider)
			before := accessUsers(t, h.db)
			a := oauthHTTPBudgetBegin(t, h, name)
			b := oauthHTTPBudgetBegin(t, h, name)
			ctxA, cancelA := context.WithCancel(context.Background())
			resultA, resultB := make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)
			doneA, doneB := make(chan struct{}), make(chan struct{})
			startedA, startedB := false, false
			t.Cleanup(func() {
				cancelA()
				transport.release()
				join := func(label string, started bool, done <-chan struct{}) {
					if !started {
						return
					}
					select {
					case <-done:
					case <-time.After(2 * time.Second):
						t.Errorf("callback %s did not stop before fixture cleanup", label)
					}
				}
				join("A", startedA, doneA)
				join("B", startedB, doneB)
			})
			startedA = true
			go func() {
				defer close(doneA)
				resultA <- oauthHTTPBudgetCallback(h, a, "cancel-A", ctxA)
			}()
			select {
			case <-transport.blockStarted:
			case <-time.After(2 * time.Second):
				transport.release()
				select {
				case <-resultA:
				case <-time.After(time.Second):
					t.Fatal("callback A did not join after test release")
				}
				t.Fatal("first callback did not enter its blocked request")
			}
			startedB = true
			go func() {
				defer close(doneB)
				resultB <- oauthHTTPBudgetCallback(h, b, "success-B", nil)
			}()
			cancelA()
			var canceled *httptest.ResponseRecorder
			select {
			case canceled = <-resultA:
			case <-time.After(2 * time.Second):
				transport.release()
				select {
				case <-resultA:
				case <-time.After(time.Second):
					t.Fatal("callback A did not join after test release")
				}
				t.Fatal("canceling callback A did not finish")
			}
			var success *httptest.ResponseRecorder
			select {
			case success = <-resultB:
			case <-time.After(3 * time.Second):
				transport.release()
				select {
				case <-resultB:
				case <-time.After(time.Second):
					t.Fatal("callback B did not join after test release")
				}
				t.Fatal("callback B did not finish independently")
			}
			transport.release()
			if canceled.Code != http.StatusBadGateway || canceled.Body.String() != "OAuth provider authentication failed" {
				t.Errorf("canceled callback A status/body=%d/%q, want generic 502 provider failure", canceled.Code, canceled.Body.String())
			}
			assertOAuthTxnNoLoginCookie(t, canceled)
			if success.Code != http.StatusOK {
				t.Errorf("callback B status=%d, want 200: %s", success.Code, success.Body.String())
			} else {
				token := oauthHTTPBudgetJSONToken(t, success)
				me := h.request(http.MethodGet, "/api/auth/me", "", token, echo.MIMEApplicationJSON, "")
				if me.code != http.StatusOK {
					t.Errorf("callback B token /api/auth/me status=%d, want 200", me.code)
				} else if !strings.Contains(me.body, "oauth-budget-success-B@example.test") {
					t.Errorf("callback B token resolved identity %s, want B's profile", me.body)
				}
			}
			if got := accessUsers(t, h.db); len(got) != len(before)+1 {
				t.Errorf("concurrent callbacks created %d users, want exactly B's one", len(got)-len(before))
			} else {
				foundB, foundA := false, false
				for _, user := range got {
					foundB = foundB || user.Email == "oauth-budget-success-B@example.test"
					foundA = foundA || user.Email == "oauth-budget-cancel-A@example.test"
				}
				if !foundB || foundA {
					t.Errorf("users include B=%v A=%v, want only B's new identity", foundB, foundA)
				}
			}
			if oauthHTTPBudgetClient(provider) != clientBefore || clientBefore.Transport != transportBefore {
				t.Error("callback mutated the registered provider client or transport")
			}
			if http.DefaultClient != defaultClient || http.DefaultTransport != defaultTransport {
				t.Error("callback mutated the process-wide default HTTP client or transport")
			}
		})
	}
}

type oauthHTTPBudgetTransport struct {
	mu             sync.Mutex
	tokenDelay     time.Duration
	blockProfile   bool
	profileHeaders bool
	blockTokenBody bool
	blockCode      string
	tokenStarted   chan struct{}
	profileStarted chan struct{}
	blockStarted   chan struct{}
	releaseCh      chan struct{}
	profileSince   time.Time
	tokenSince     time.Time
	startedOnce    sync.Once
	blockOnce      sync.Once
	tokenOnce      sync.Once
	releaseOnce    sync.Once
}

func oauthHTTPBudgetNewTransport() *oauthHTTPBudgetTransport {
	return &oauthHTTPBudgetTransport{
		tokenStarted:   make(chan struct{}),
		profileStarted: make(chan struct{}),
		blockStarted:   make(chan struct{}),
		releaseCh:      make(chan struct{}),
	}
}

func (rt *oauthHTTPBudgetTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	path := req.URL.Path
	if strings.Contains(path, "access_token") || strings.Contains(path, "/token") {
		code := req.FormValue("code")
		rt.tokenOnce.Do(func() {
			rt.mu.Lock()
			rt.tokenSince = time.Now()
			rt.mu.Unlock()
			close(rt.tokenStarted)
		})
		if rt.blockCode != "" && code == rt.blockCode {
			rt.blockOnce.Do(func() { close(rt.blockStarted) })
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-rt.releaseCh:
				return nil, errOAuthHTTPBudgetReleased
			}
		}
		if rt.blockTokenBody {
			return oauthHTTPBudgetResponse(req, http.StatusOK, "application/json", `{"access_token":"body-token","token_type":"bearer","expires_in":3600}`, true, rt.releaseCh), nil
		}
		if rt.tokenDelay > 0 {
			timer := time.NewTimer(rt.tokenDelay)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
		}
		return oauthHTTPBudgetResponse(req, http.StatusOK, "application/json", `{"access_token":"oauth-budget-`+code+`","token_type":"bearer","expires_in":3600}`, false, rt.releaseCh), nil
	}
	if rt.blockProfile {
		rt.mu.Lock()
		rt.profileSince = time.Now()
		rt.mu.Unlock()
		rt.startedOnce.Do(func() { close(rt.profileStarted) })
		if rt.profileHeaders {
			return oauthHTTPBudgetResponse(req, http.StatusOK, "application/json", "", true, rt.releaseCh), nil
		}
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-rt.releaseCh:
			return nil, errOAuthHTTPBudgetReleased
		}
	}
	return oauthHTTPBudgetResponse(req, http.StatusOK, "application/json", oauthHTTPBudgetProfile(path, oauthHTTPBudgetToken(req)), false, rt.releaseCh), nil
}

func (rt *oauthHTTPBudgetTransport) release() {
	rt.releaseOnce.Do(func() { close(rt.releaseCh) })
}
func (rt *oauthHTTPBudgetTransport) profileWait() time.Duration {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.profileSince.IsZero() {
		return 0
	}
	return time.Since(rt.profileSince)
}

var errOAuthHTTPBudgetReleased = errors.New("OAuth budget test transport released a blocked operation")

func oauthHTTPBudgetResponse(req *http.Request, status int, contentType, body string, hold bool, release <-chan struct{}) *http.Response {
	var responseBody io.ReadCloser = io.NopCloser(strings.NewReader(body))
	if hold {
		responseBody = &oauthHTTPBudgetBody{ctx: req.Context(), release: release, initial: []byte(body), closed: make(chan struct{})}
	}
	return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)), Header: http.Header{"Content-Type": []string{contentType}}, Body: responseBody, Request: req}
}

type oauthHTTPBudgetBody struct {
	ctx     context.Context
	release <-chan struct{}
	initial []byte
	closed  chan struct{}
	once    sync.Once
}

func (body *oauthHTTPBudgetBody) Read(p []byte) (int, error) {
	if len(body.initial) > 0 {
		n := copy(p, body.initial)
		body.initial = body.initial[n:]
		return n, nil
	}
	select {
	case <-body.ctx.Done():
		return 0, body.ctx.Err()
	case <-body.release:
		return 0, errOAuthHTTPBudgetReleased
	case <-body.closed:
		return 0, errOAuthHTTPBudgetReleased
	}
}

func (body *oauthHTTPBudgetBody) Close() error {
	body.once.Do(func() { close(body.closed) })
	return nil
}

func (rt *oauthHTTPBudgetTransport) tokenWaitStarted() time.Time {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.tokenSince
}

func oauthHTTPBudgetToken(req *http.Request) string {
	if token := req.URL.Query().Get("access_token"); token != "" {
		return token
	}
	return strings.TrimPrefix(req.Header.Get(echo.HeaderAuthorization), "Bearer ")
}

func oauthHTTPBudgetProfile(path, token string) string {
	identity := strings.TrimPrefix(token, "oauth-budget-")
	if identity == "" {
		identity = "unknown"
	}
	if path == "/user" {
		return fmt.Sprintf(`{"id":8042,"email":"oauth-budget-%s@example.test","login":"oauth-budget-%s","name":"Budget %s"}`, identity, identity, identity)
	}
	return fmt.Sprintf(`{"id":"oauth-budget-%s","email":"oauth-budget-%s@example.test","name":"Budget %s","verified_email":true}`, identity, identity, identity)
}

func oauthHTTPBudgetProvider(name string, transport http.RoundTripper, client *http.Client) goth.Provider {
	if client == nil && transport != nil {
		client = &http.Client{Transport: transport}
	}
	switch name {
	case "google":
		p := google.New(oauthHTTPBudgetID, oauthHTTPBudgetSecret, "http://cms.example.test/auth/google/callback")
		p.HTTPClient = client
		return p
	case "github":
		p := github.New(oauthHTTPBudgetID, oauthHTTPBudgetSecret, "http://cms.example.test/auth/github/callback")
		p.HTTPClient = client
		return p
	default:
		panic("unknown provider " + name)
	}
}

func oauthHTTPBudgetClient(provider goth.Provider) *http.Client {
	switch p := provider.(type) {
	case *google.Provider:
		return p.HTTPClient
	case *github.Provider:
		return p.HTTPClient
	default:
		panic(fmt.Sprintf("unexpected provider type %T", provider))
	}
}

type oauthHTTPBudgetEntry struct {
	name, state string
	cookies     []*http.Cookie
}

func oauthHTTPBudgetBegin(t *testing.T, h *accessHarness, name string) oauthHTTPBudgetEntry {
	t.Helper()
	rec := oauthTxnServe(h, http.MethodGet, "/auth/"+name, nil)
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("%s entry returned %d: %s", name, rec.Code, rec.Body.String())
	}
	u, err := url.Parse(rec.Header().Get(echo.HeaderLocation))
	if err != nil {
		t.Fatal(err)
	}
	state := u.Query().Get("state")
	var cookies []*http.Cookie
	for _, cookieName := range []string{gothic.SessionName, "jelly-cms-session"} {
		if c := oauthTxnResponseCookie(rec, cookieName); c != nil {
			cookies = append(cookies, c)
		}
	}
	if state == "" || len(cookies) != 2 {
		t.Fatalf("%s entry omitted state/session cookies", name)
	}
	return oauthHTTPBudgetEntry{name: name, state: state, cookies: cookies}
}

func oauthHTTPBudgetCallback(h *accessHarness, entry oauthHTTPBudgetEntry, code string, ctx context.Context) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/auth/"+entry.name+"/callback?state="+url.QueryEscape(entry.state)+"&code="+url.QueryEscape(code), nil)
	req.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
	for _, cookie := range entry.cookies {
		req.AddCookie(cookie)
	}
	if ctx != nil {
		req = req.WithContext(ctx)
	}
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	return rec
}

func oauthHTTPBudgetJSONToken(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil || payload.Token == "" {
		t.Fatalf("decode callback token: %v (%s)", err, rec.Body.String())
	}
	return payload.Token
}
