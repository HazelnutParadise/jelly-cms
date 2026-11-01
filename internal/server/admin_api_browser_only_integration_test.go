package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

// Ticket 23: /api/admin/* accepts only the browser login. Any Authorization
// header is refused before authentication, and sensitive writes need an
// interactive browser session (jwt_token cookie plus X-Jelly-CSRF).

const (
	broBrowserLoginRequired = "Browser login required"
	broInteractiveRequired  = "Interactive login required"
	broLoginCookie          = "jwt_token"
	broRequestDeadline      = 3 * time.Second
)

// broCall is one request built by hand so each test chooses its credentials.
type broCall struct {
	method string
	path   string
	body   string
	// authorization is sent verbatim as the Authorization header when set.
	authorization string
	// cookie is sent as the jwt_token cookie when set.
	cookie string
	// csrf adds X-Jelly-CSRF: 1 when true.
	csrf bool
}

func (h *accessHarness) broDo(c broCall) accessResult {
	h.t.Helper()
	var body io.Reader
	if c.body != "" {
		body = strings.NewReader(c.body)
	}
	req := httptest.NewRequest(c.method, c.path, body)
	req.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
	if c.body != "" {
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	if c.authorization != "" {
		req.Header.Set(echo.HeaderAuthorization, c.authorization)
	}
	if c.cookie != "" {
		req.AddCookie(&http.Cookie{Name: broLoginCookie, Value: c.cookie})
	}
	if c.csrf {
		req.Header.Set("X-Jelly-CSRF", "1")
	}
	// Streaming routes (the logs stream) never end on their own. The deadline
	// makes them return so the sweep reaches the remaining routes.
	ctx, cancel := context.WithTimeout(req.Context(), broRequestDeadline)
	defer cancel()
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	var panicked any
	func() { defer func() { panicked = recover() }(); h.echo.ServeHTTP(rec, req) }()
	return accessResult{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone(), panicked: panicked}
}

// broAdminRoute is one registered /api/admin route with its parameters filled in.
type broAdminRoute struct {
	method string
	path   string
}

// broAdminRoutes lists every /api/admin and /api/admin/* route from e.Routes().
// Path parameters and wildcards become "1", so each route has a concrete URL.
func broAdminRoutes(h *accessHarness) []broAdminRoute {
	var routes []broAdminRoute
	seen := map[string]bool{}
	for _, route := range h.echo.Routes() {
		if !isAdminAPIPath(route.Path) {
			continue
		}
		switch route.Method {
		case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			continue
		}
		key := route.Method + " " + route.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		segments := strings.Split(route.Path, "/")
		for i, segment := range segments {
			if strings.HasPrefix(segment, ":") || segment == "*" {
				segments[i] = "1"
			}
		}
		routes = append(routes, broAdminRoute{method: route.Method, path: strings.Join(segments, "/")})
	}
	return routes
}

// broAdmin creates the admin used by these tests. A filler user takes id 1,
// because the route table uses 1 for every :id, and the admin must not be it.
func broAdmin(t *testing.T, h *accessHarness) string {
	t.Helper()
	h.user("author", "browser-only-filler")
	admin := h.user("admin", "browser-only-admin")
	if admin.ID == 1 {
		t.Fatalf("admin got id 1; the route table would act on the admin account")
	}
	return h.token(admin)
}

func broExpectRefusal(t *testing.T, label string, res accessResult, wantCode int, wantBody string) {
	t.Helper()
	if res.panicked != nil {
		t.Errorf("%s panicked: %v", label, res.panicked)
		return
	}
	if res.code != wantCode || !strings.Contains(res.body, wantBody) {
		t.Errorf("%s returned %d body=%q, want %d with body containing %q", label, res.code, res.body, wantCode, wantBody)
	}
}

func broExpectNoGate(t *testing.T, label string, res accessResult, forbidden ...string) {
	t.Helper()
	if res.panicked != nil {
		t.Errorf("%s panicked: %v", label, res.panicked)
		return
	}
	for _, text := range forbidden {
		if strings.Contains(res.body, text) {
			t.Errorf("%s returned %d body=%q, want the body not to contain %q", label, res.code, res.body, text)
		}
	}
}

// TestAdminAPIBrowserOnlyRouteTableRejectsBearerJWT checks that a valid login
// JWT in Authorization: Bearer gets 401 on every /api/admin route. The gate
// runs before any handler, so write routes are refused without side effects.
func TestAdminAPIBrowserOnlyRouteTableRejectsBearerJWT(t *testing.T) {
	h := newAccessHarness(t)
	jwt := broAdmin(t, h)
	routes := broAdminRoutes(h)
	if len(routes) == 0 {
		t.Fatal("RegisterRoutes exposed no /api/admin routes")
	}
	for _, route := range routes {
		label := route.method + " " + route.path + " with Bearer login JWT"
		res := h.broDo(broCall{method: route.method, path: route.path, authorization: "Bearer " + jwt})
		broExpectRefusal(t, label, res, http.StatusUnauthorized, broBrowserLoginRequired)
	}
}

// TestAdminAPIBrowserOnlyRouteTableRejectsAccessKeyShapedBearer checks that
// jla_ and jly_ shaped Bearer strings get the same 401 on every /api/admin route.
func TestAdminAPIBrowserOnlyRouteTableRejectsAccessKeyShapedBearer(t *testing.T) {
	h := newAccessHarness(t)
	broAdmin(t, h)
	routes := broAdminRoutes(h)
	if len(routes) == 0 {
		t.Fatal("RegisterRoutes exposed no /api/admin routes")
	}
	for _, key := range []string{"jla_" + strings.Repeat("A", 43), "jly_" + strings.Repeat("A", 43)} {
		for _, route := range routes {
			label := route.method + " " + route.path + " with Bearer " + key[:4] + "… shaped key"
			res := h.broDo(broCall{method: route.method, path: route.path, authorization: "Bearer " + key})
			broExpectRefusal(t, label, res, http.StatusUnauthorized, broBrowserLoginRequired)
		}
	}
}

// TestAdminAPIBrowserOnlyRouteTableAcceptsCookieLogin checks that the same
// routes are not refused by the Authorization gate when the browser cookie is
// used. Only GET and HEAD routes are called, so no write runs with the cookie
// (cookie writes are covered by the interactive tests below).
func TestAdminAPIBrowserOnlyRouteTableAcceptsCookieLogin(t *testing.T) {
	h := newAccessHarness(t)
	jwt := broAdmin(t, h)
	routes := broAdminRoutes(h)
	if len(routes) == 0 {
		t.Fatal("RegisterRoutes exposed no /api/admin routes")
	}
	for _, route := range routes {
		if route.method != http.MethodGet && route.method != http.MethodHead {
			continue
		}
		label := route.method + " " + route.path + " with jwt_token cookie"
		res := h.broDo(broCall{method: route.method, path: route.path, cookie: jwt})
		broExpectNoGate(t, label, res, broBrowserLoginRequired)
	}
}

type broEndpoint struct {
	method string
	path   string
	body   string
	// bearerCode and bearerBody are the answer to a login JWT sent as Bearer.
	bearerCode int
	bearerBody string
}

// broInteractiveEndpoints are representative sensitive writes.
var broInteractiveEndpoints = []broEndpoint{
	{method: http.MethodPost, path: "/api/account/password", body: "{}", bearerCode: http.StatusForbidden, bearerBody: broInteractiveRequired},
	{method: http.MethodPost, path: "/api/admin/users/invite", body: "{}", bearerCode: http.StatusUnauthorized, bearerBody: broBrowserLoginRequired},
	{method: http.MethodPost, path: "/api/admin/backups", body: "{}", bearerCode: http.StatusUnauthorized, bearerBody: broBrowserLoginRequired},
	{method: http.MethodPut, path: "/api/admin/settings/injection", body: "{}", bearerCode: http.StatusUnauthorized, bearerBody: broBrowserLoginRequired},
	{method: http.MethodPost, path: "/api/admin/store/install", body: "{}", bearerCode: http.StatusUnauthorized, bearerBody: broBrowserLoginRequired},
}

// TestAdminAPIBrowserOnlyInteractiveBearerIsRefused checks that a Bearer login
// JWT is refused on the sensitive writes: 403 on the account route and 401 on
// the admin routes, where the gate runs first.
func TestAdminAPIBrowserOnlyInteractiveBearerIsRefused(t *testing.T) {
	h := newAccessHarness(t)
	jwt := broAdmin(t, h)
	for _, endpoint := range broInteractiveEndpoints {
		label := endpoint.method + " " + endpoint.path + " with Bearer login JWT"
		res := h.broDo(broCall{method: endpoint.method, path: endpoint.path, body: endpoint.body, authorization: "Bearer " + jwt})
		broExpectRefusal(t, label, res, endpoint.bearerCode, endpoint.bearerBody)
	}
}

// TestAdminAPIBrowserOnlyInteractiveCookieWriteNeedsCSRF checks that a cookie
// write without X-Jelly-CSRF is refused with the interactive message.
func TestAdminAPIBrowserOnlyInteractiveCookieWriteNeedsCSRF(t *testing.T) {
	h := newAccessHarness(t)
	jwt := broAdmin(t, h)
	for _, endpoint := range broInteractiveEndpoints {
		label := endpoint.method + " " + endpoint.path + " with cookie and no X-Jelly-CSRF"
		res := h.broDo(broCall{method: endpoint.method, path: endpoint.path, body: endpoint.body, cookie: jwt})
		// Two layers can refuse a cookie write without the header: the interactive rule
		// ("Interactive login required") and the request-origin protection that runs
		// before it on some routes. Either is a valid 403 refusal.
		if res.panicked != nil {
			t.Errorf("%s panicked: %v", label, res.panicked)
			continue
		}
		if res.code != http.StatusForbidden || !(strings.Contains(res.body, broInteractiveRequired) || strings.Contains(res.body, "Request origin is not allowed")) {
			t.Errorf("%s returned %d body=%q, want 403 with %q or the request-origin refusal", label, res.code, res.body, broInteractiveRequired)
		}
	}
}

// TestAdminAPIBrowserOnlyInteractiveCookieWithCSRFPassesRule checks that a
// cookie write with X-Jelly-CSRF passes the interactive rule. The response may
// be any other status, since the body is invalid on purpose.
func TestAdminAPIBrowserOnlyInteractiveCookieWithCSRFPassesRule(t *testing.T) {
	h := newAccessHarness(t)
	jwt := broAdmin(t, h)
	for _, endpoint := range broInteractiveEndpoints {
		label := endpoint.method + " " + endpoint.path + " with cookie and X-Jelly-CSRF"
		res := h.broDo(broCall{method: endpoint.method, path: endpoint.path, body: endpoint.body, cookie: jwt, csrf: true})
		broExpectNoGate(t, label, res, broInteractiveRequired, broBrowserLoginRequired)
	}
}

// TestAdminAPIBrowserOnlyDownloadNeedsNoCSRF checks that a cookie GET download
// is not refused by the interactive rule when no CSRF header is sent.
func TestAdminAPIBrowserOnlyDownloadNeedsNoCSRF(t *testing.T) {
	h := newAccessHarness(t)
	jwt := broAdmin(t, h)
	res := h.broDo(broCall{method: http.MethodGet, path: "/api/admin/logs/export", cookie: jwt})
	broExpectNoGate(t, "GET /api/admin/logs/export with cookie and no X-Jelly-CSRF", res, broInteractiveRequired, broBrowserLoginRequired)
}

// TestAdminAPIBrowserOnlyAnonymousInteractiveWriteIsUnauthorized checks that a
// request without credentials gets 401, not 403, on the sensitive writes.
func TestAdminAPIBrowserOnlyAnonymousInteractiveWriteIsUnauthorized(t *testing.T) {
	h := newAccessHarness(t)
	broAdmin(t, h)
	for _, endpoint := range broInteractiveEndpoints {
		label := endpoint.method + " " + endpoint.path + " without credentials"
		res := h.broDo(broCall{method: endpoint.method, path: endpoint.path, body: endpoint.body})
		if res.panicked != nil {
			t.Errorf("%s panicked: %v", label, res.panicked)
			continue
		}
		if res.code != http.StatusUnauthorized {
			t.Errorf("%s returned %d body=%q, want 401", label, res.code, res.body)
		}
	}
}

// TestAdminAPIBrowserOnlyInvariantAuthMeAcceptsBearer checks that /api/auth/me
// still accepts a login JWT in Bearer. Ticket 23 leaves /api/auth/* unchanged.
func TestAdminAPIBrowserOnlyInvariantAuthMeAcceptsBearer(t *testing.T) {
	h := newAccessHarness(t)
	jwt := broAdmin(t, h)
	res := h.broDo(broCall{method: http.MethodGet, path: "/api/auth/me", authorization: "Bearer " + jwt})
	if res.panicked != nil {
		t.Fatalf("GET /api/auth/me panicked: %v", res.panicked)
	}
	if res.code != http.StatusOK {
		t.Errorf("GET /api/auth/me with Bearer login JWT returned %d body=%q, want 200", res.code, res.body)
	}
}

// TestAdminAPIBrowserOnlyInvariantAgentMeAcceptsAccessKey checks that the
// agent API still accepts an access key. Ticket 24 moved this path from /api/agent/v1 to /api/v1.
func TestAdminAPIBrowserOnlyInvariantAgentMeAcceptsAccessKey(t *testing.T) {
	h := newAccessHarness(t)
	jwt := broAdmin(t, h)
	created := agtCreate(t, h, jwt, "browser-only 護欄", "posts.read")
	agtExpectOK(t, agtMe(h, created.APIKey), "GET /api/v1/me with an access key")
}
