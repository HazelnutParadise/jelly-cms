package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/markbates/goth/providers/github"
	"github.com/markbates/goth/providers/google"
)

const (
	oauthHTTPClientID     = "oauth-http-client-fixture"
	oauthHTTPClientSecret = "oauth-http-secret-fixture"
	oauthHTTPAccessToken  = "oauth-http-access-token-fixture"
)

func TestOAuthHTTPRealProvidersKeepJSONLoginAndInSiteRedirect(t *testing.T) {
	for _, providerName := range []string{"google", "github"} {
		t.Run(providerName, func(t *testing.T) {
			h := oauthTxnPrepareHarness(t)
			upstream := newOAuthHTTPUpstream(t, oauthHTTPBehavior{})
			provider := oauthHTTPProvider(providerName, upstream.server)
			originalHTTPClient := oauthHTTPProviderClient(provider)
			originalTransport := http.DefaultTransport
			originalDefaultClient := http.DefaultClient
			goth.UseProviders(provider)

			before := accessUsers(t, h.db)
			entry := oauthHTTPBegin(t, h, providerName, "/admin/posts?tab=all")
			callback := oauthHTTPCallback(h, entry, "success-code", nil)
			if callback.Code != http.StatusOK {
				t.Fatalf("real %s callback returned %d, want 200: %s", providerName, callback.Code, callback.Body.String())
			}
			token := oauthHTTPAssertJSONLogin(t, callback)
			me := h.request(http.MethodGet, "/api/auth/me", "", token, echo.MIMEApplicationJSON, "")
			if me.code != http.StatusOK {
				t.Fatalf("GET /api/auth/me with %s OAuth token returned %d, want 200: %s", providerName, me.code, me.body)
			}
			var authenticated core.User
			if err := json.Unmarshal([]byte(me.body), &authenticated); err != nil {
				t.Fatalf("decode /api/auth/me response: %v", err)
			}
			if authenticated.Email != "oauth-http@example.test" || authenticated.Provider != providerName {
				t.Errorf("/api/auth/me identity=%q provider=%q, want oauth-http@example.test/%s", authenticated.Email, authenticated.Provider, providerName)
			}
			if got := accessUsers(t, h.db); len(got) != len(before)+1 {
				t.Errorf("successful %s callback created %d users, want exactly one", providerName, len(got)-len(before))
			}

			redirectEntry := oauthHTTPBegin(t, h, providerName, "/admin/posts?tab=all")
			redirectCallback := oauthHTTPCallbackAccept(h, redirectEntry, "redirect-code", nil, "text/html")
			if redirectCallback.Code != http.StatusFound || redirectCallback.Header().Get(echo.HeaderLocation) != "/admin/posts?tab=all" {
				t.Errorf("HTML %s callback status/location=%d/%q, want 302 /admin/posts?tab=all", providerName, redirectCallback.Code, redirectCallback.Header().Get(echo.HeaderLocation))
			}
			if oauthHTTPProviderClient(provider) != originalHTTPClient {
				t.Error("callback replaced the registered provider HTTP client")
			}
			if http.DefaultTransport != originalTransport || http.DefaultClient != originalDefaultClient {
				t.Error("callback changed a process-wide default HTTP transport or client")
			}
		})
	}
}

func TestOAuthHTTPCancelStopsRealUpstreamAndConsumesState(t *testing.T) {
	for _, providerName := range []string{"google", "github"} {
		for _, operation := range []string{"token", "profile"} {
			for _, phase := range []string{"before headers", "after 200 headers while reading body"} {
				name := strings.ReplaceAll(strings.Join([]string{providerName, operation, phase}, "_"), " ", "_")
				t.Run(name, func(t *testing.T) {
					h := oauthTxnPrepareHarness(t)
					behavior := oauthHTTPBehavior{blockOperation: operation, blockAfterHeaders: phase != "before headers"}
					upstream := newOAuthHTTPUpstream(t, behavior)
					provider := oauthHTTPProvider(providerName, upstream.server)
					oauthHTTPObserveProviderBody(provider, upstream)
					originalHTTPClient := oauthHTTPProviderClient(provider)
					var originalClientTransport http.RoundTripper
					if originalHTTPClient != nil {
						originalClientTransport = originalHTTPClient.Transport
					}
					originalProvider := provider
					originalTransport := http.DefaultTransport
					originalDefaultClient := http.DefaultClient
					goth.UseProviders(provider)
					usersBefore := accessUsers(t, h.db)

					entry := oauthHTTPBegin(t, h, providerName, "/admin")
					callbackContext, cancelCallback := context.WithCancel(context.Background())
					defer cancelCallback()
					callbackRequestWithContext := httptest.NewRequest(http.MethodGet, "/auth/"+providerName+"/callback?state="+url.QueryEscape(entry.state)+"&code=cancel-code", nil).WithContext(callbackContext)
					callbackRequestWithContext.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
					for _, cookie := range []*http.Cookie{entry.gothCookie, entry.appCookie} {
						callbackRequestWithContext.AddCookie(cookie)
					}
					response := make(chan *httptest.ResponseRecorder, 1)
					callbackDone := make(chan struct{})
					go func() {
						defer close(callbackDone)
						rec := httptest.NewRecorder()
						h.echo.ServeHTTP(rec, callbackRequestWithContext)
						response <- rec
					}()
					t.Cleanup(func() {
						upstream.release()
						cancelCallback()
						select {
						case <-callbackDone:
						case <-time.After(4 * time.Second):
							t.Error("callback goroutine remained after releasing the test-owned upstream")
						}
					})

					upstreamStarted := upstream.blockStarted
					if behavior.blockAfterHeaders {
						upstreamStarted = upstream.bodyReadStarted
					}
					select {
					case <-upstreamStarted:
					case <-time.After(3 * time.Second):
						t.Fatal("callback did not reach the selected real upstream operation")
					}

					var callback *httptest.ResponseRecorder
					finishedBeforeCancel := false
					select {
					case callback = <-response:
						finishedBeforeCancel = true
					default:
					}
					cancelAt := time.Now()
					cancelCallback()
					cancelObserved := false
					select {
					case <-upstream.blockCanceled:
						cancelObserved = true
					case <-time.After(750 * time.Millisecond):
					}

					callbackFinishedAt := time.Now()
					if !finishedBeforeCancel {
						select {
						case callback = <-response:
							callbackFinishedAt = time.Now()
						case <-time.After(2 * time.Second):
							// Old Goth behavior ignores the callback context. Releasing our own
							// gate lets its handler finish before the test server is closed.
							upstream.release()
							select {
							case callback = <-response:
								callbackFinishedAt = time.Now()
							case <-time.After(3 * time.Second):
								t.Fatal("callback handler did not finish after the test-owned upstream release")
							}
						}
					}
					upstream.release()
					if finishedBeforeCancel {
						t.Error("callback finished before cancellation was requested")
					}
					if elapsed := callbackFinishedAt.Sub(cancelAt); elapsed > 2*time.Second {
						t.Errorf("canceled %s/%s callback took %s to return after cancellation, want at most 2s", providerName, operation, elapsed)
					}

					if !cancelObserved {
						t.Errorf("canceled callback was not observed by the %s upstream handler", operation)
					}
					if callback.Code != http.StatusBadGateway {
						t.Errorf("canceled %s/%s callback returned %d, want generic 502", providerName, operation, callback.Code)
					}
					if strings.TrimSpace(callback.Body.String()) != "OAuth provider authentication failed" {
						t.Errorf("canceled callback body=%q, want generic provider failure", strings.TrimSpace(callback.Body.String()))
					}
					if strings.Contains(callback.Body.String(), oauthHTTPClientSecret) || strings.Contains(callback.Body.String(), oauthHTTPAccessToken) {
						t.Error("callback response exposed a provider secret")
					}
					if strings.Contains(callback.Body.String(), `"token"`) {
						t.Error("failed callback returned a JWT token")
					}
					assertOAuthTxnNoLoginCookie(t, callback)
					if usersAfter := accessUsers(t, h.db); !sameAccessUsers(usersBefore, usersAfter) {
						t.Errorf("canceled %s/%s callback changed users", providerName, operation)
					}
					replay := oauthHTTPCallback(h, entry, "cancel-code", nil)
					if replay.Code != http.StatusBadRequest {
						t.Errorf("replaying canceled %s state returned %d, want 400", providerName, replay.Code)
					}
					assertOAuthTxnNoLoginCookie(t, replay)

					fresh := oauthHTTPBegin(t, h, providerName, "/admin")
					freshCallback := oauthHTTPCallback(h, fresh, "fresh-after-cancel-code", nil)
					if freshCallback.Code != http.StatusOK {
						t.Errorf("fresh %s authorization after cancellation returned %d, want 200: %s", providerName, freshCallback.Code, freshCallback.Body.String())
					}
					if oauthHTTPProviderClient(originalProvider) != originalHTTPClient {
						t.Error("canceled callback replaced the registered provider HTTP client")
					}
					if originalHTTPClient != nil && originalHTTPClient.Transport != originalClientTransport {
						t.Error("canceled callback replaced the registered provider transport")
					}
					if http.DefaultTransport != originalTransport || http.DefaultClient != originalDefaultClient {
						t.Error("canceled callback changed a process-wide default HTTP transport or client")
					}
					if got := upstream.requestCount.Load(); got < 2 {
						t.Errorf("upstream handled %d requests, want the blocked request and a fresh successful authorization", got)
					}
				})
			}
		}
	}
}

func oauthHTTPProvider(name string, server *httptest.Server) goth.Provider {
	switch name {
	case "google":
		p := google.New(oauthHTTPClientID, oauthHTTPClientSecret, "http://cms.example.test/auth/google/callback")
		p.HTTPClient = &http.Client{Transport: &oauthHTTPRedirectTransport{target: server.URL, allowedHosts: map[string]bool{
			"accounts.google.com":   true,
			"oauth2.googleapis.com": true,
			"www.googleapis.com":    true,
		}, base: server.Client().Transport}}
		return p
	case "github":
		// The public constructor keeps Goth's real GitHub Provider/Session while
		// directing all provider endpoints to this test-owned loopback server.
		return github.NewCustomisedURL(oauthHTTPClientID, oauthHTTPClientSecret,
			"http://cms.example.test/auth/github/callback",
			server.URL+"/authorize", server.URL+"/login/oauth/access_token",
			server.URL+"/user", server.URL+"/emails", "read:user")
	default:
		panic("unknown OAuth provider " + name)
	}
}

func oauthHTTPProviderClient(provider goth.Provider) *http.Client {
	switch p := provider.(type) {
	case *google.Provider:
		return p.HTTPClient
	case *github.Provider:
		return p.HTTPClient
	default:
		panic(fmt.Sprintf("unexpected provider type %T", provider))
	}
}

type oauthHTTPEntry struct {
	provider   string
	state      string
	gothCookie *http.Cookie
	appCookie  *http.Cookie
}

func oauthHTTPBegin(t *testing.T, h *accessHarness, providerName, redirect string) oauthHTTPEntry {
	t.Helper()
	entry := oauthTxnServe(h, http.MethodGet, "/auth/"+providerName+"?redirect="+url.QueryEscape(redirect), nil)
	if entry.Code != http.StatusTemporaryRedirect {
		t.Fatalf("%s OAuth entry returned %d, want 307: %s", providerName, entry.Code, entry.Body.String())
	}
	location, err := url.Parse(entry.Header().Get(echo.HeaderLocation))
	if err != nil {
		t.Fatalf("parse %s authorization URL: %v", providerName, err)
	}
	gothCookie := oauthTxnResponseCookie(entry, gothic.SessionName)
	appCookie := oauthTxnResponseCookie(entry, "jelly-cms-session")
	state := location.Query().Get("state")
	if state == "" || gothCookie == nil || appCookie == nil {
		t.Fatalf("%s OAuth entry omitted its state or signed cookies", providerName)
	}
	return oauthHTTPEntry{provider: providerName, state: state, gothCookie: gothCookie, appCookie: appCookie}
}

func oauthHTTPCallback(h *accessHarness, entry oauthHTTPEntry, code string, ctx context.Context) *httptest.ResponseRecorder {
	return oauthHTTPCallbackAccept(h, entry, code, ctx, echo.MIMEApplicationJSON)
}

func oauthHTTPCallbackAccept(h *accessHarness, entry oauthHTTPEntry, code string, ctx context.Context, accept string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet,
		"/auth/"+entry.provider+"/callback?state="+url.QueryEscape(entry.state)+"&code="+url.QueryEscape(code), nil)
	request.Header.Set(echo.HeaderAccept, accept)
	request.AddCookie(entry.gothCookie)
	request.AddCookie(entry.appCookie)
	if ctx != nil {
		request = request.WithContext(ctx)
	}
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, request)
	return rec
}

func oauthHTTPAssertJSONLogin(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode OAuth JSON response: %v", err)
	}
	if len(payload) != 3 || payload["message"] == nil || payload["user"] == nil || payload["token"] == nil {
		t.Fatalf("OAuth response keys=%v, want exactly message, user, token", sortedRawKeys(payload))
	}
	var token string
	if err := json.Unmarshal(payload["token"], &token); err != nil || token == "" {
		t.Fatalf("OAuth JSON response token missing or invalid: %v", err)
	}
	return token
}

type oauthHTTPBehavior struct {
	blockOperation    string
	blockAfterHeaders bool
}

type oauthHTTPUpstream struct {
	server          *httptest.Server
	behavior        oauthHTTPBehavior
	blockStarted    chan struct{}
	bodyReadStarted chan struct{}
	blockCanceled   chan struct{}
	blockRelease    chan struct{}
	startOnce       sync.Once
	bodyReadOnce    sync.Once
	cancelOnce      sync.Once
	releaseOnce     sync.Once
	blocked         atomic.Bool
	requestCount    atomic.Int32
}

func newOAuthHTTPUpstream(t *testing.T, behavior oauthHTTPBehavior) *oauthHTTPUpstream {
	t.Helper()
	u := &oauthHTTPUpstream{
		behavior:        behavior,
		blockStarted:    make(chan struct{}),
		bodyReadStarted: make(chan struct{}),
		blockCanceled:   make(chan struct{}),
		blockRelease:    make(chan struct{}),
	}
	u.server = httptest.NewServer(http.HandlerFunc(u.serveHTTP))
	t.Cleanup(func() {
		u.release()
		u.server.Close()
	})
	return u
}

func (u *oauthHTTPUpstream) release() {
	u.releaseOnce.Do(func() { close(u.blockRelease) })
}

func (u *oauthHTTPUpstream) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		return
	}
	u.requestCount.Add(1)
	operation := oauthHTTPOperation(r.URL.Path)
	if u.behavior.blockOperation == operation && u.blocked.CompareAndSwap(false, true) {
		if u.behavior.blockAfterHeaders {
			w.Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		} else {
			u.startOnce.Do(func() { close(u.blockStarted) })
		}
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			u.cancelOnce.Do(func() { close(u.blockCanceled) })
			return
		case <-u.blockRelease:
		case <-timer.C:
		}
	}

	w.Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	switch operation {
	case "token":
		_, _ = io.WriteString(w, `{"access_token":"`+oauthHTTPAccessToken+`","token_type":"Bearer","expires_in":3600}`)
	case "profile":
		if strings.Contains(r.URL.Host, "google") || strings.HasPrefix(r.URL.Path, "/oauth2/") {
			_, _ = io.WriteString(w, `{"id":"oauth-http-subject","email":"oauth-http@example.test","name":"OAuth HTTP Fixture","verified_email":true}`)
		} else {
			_, _ = io.WriteString(w, `{"id":7421,"email":"oauth-http@example.test","login":"oauth-http","name":"OAuth HTTP Fixture","avatar_url":"https://images.example.test/avatar.png"}`)
		}
	default:
		http.Error(w, "unexpected local OAuth request", http.StatusNotFound)
	}
}

func oauthHTTPObserveProviderBody(provider goth.Provider, upstream *oauthHTTPUpstream) {
	switch p := provider.(type) {
	case *google.Provider:
		p.HTTPClient.Transport = &oauthHTTPObserveBodyTransport{base: p.HTTPClient.Transport, upstream: upstream}
	case *github.Provider:
		p.HTTPClient = &http.Client{Transport: &oauthHTTPObserveBodyTransport{base: upstream.server.Client().Transport, upstream: upstream}}
	default:
		panic(fmt.Sprintf("unexpected provider type %T", provider))
	}
}

func oauthHTTPOperation(path string) string {
	switch {
	case strings.Contains(path, "/token"), strings.Contains(path, "/access_token"):
		return "token"
	case strings.Contains(path, "/userinfo"), path == "/user":
		return "profile"
	default:
		return "other"
	}
}

type oauthHTTPRedirectTransport struct {
	target       string
	allowedHosts map[string]bool
	base         http.RoundTripper
}

type oauthHTTPObserveBodyTransport struct {
	base     http.RoundTripper
	upstream *oauthHTTPUpstream
}

func (rt *oauthHTTPObserveBodyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := rt.base.RoundTrip(request)
	if err != nil || response == nil || response.Body == nil {
		return response, err
	}
	if rt.upstream.behavior.blockAfterHeaders && oauthHTTPOperation(request.URL.Path) == rt.upstream.behavior.blockOperation {
		response.Body = &oauthHTTPObserveBody{ReadCloser: response.Body, onRead: func() {
			rt.upstream.bodyReadOnce.Do(func() { close(rt.upstream.bodyReadStarted) })
		}}
	}
	return response, nil
}

type oauthHTTPObserveBody struct {
	io.ReadCloser
	once   sync.Once
	onRead func()
}

func (body *oauthHTTPObserveBody) Read(p []byte) (int, error) {
	body.once.Do(body.onRead)
	return body.ReadCloser.Read(p)
}

func (rt *oauthHTTPRedirectTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != "https" || !rt.allowedHosts[request.URL.Hostname()] {
		return nil, fmt.Errorf("OAuth test transport refused unexpected destination %q", request.URL.Redacted())
	}
	forwarded := request.Clone(request.Context())
	local, err := url.Parse(rt.target)
	if err != nil {
		return nil, err
	}
	forwarded.URL.Scheme = local.Scheme
	forwarded.URL.Host = local.Host
	if rt.base == nil {
		rt.base = http.DefaultTransport
	}
	return rt.base.RoundTrip(forwarded)
}
