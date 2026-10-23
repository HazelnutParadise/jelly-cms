package server

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/payment"
	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/bcrypt"
)

const browserWriteOrigin = "http://127.0.0.1"

type browserWriteRequest struct {
	method  string
	target  string
	body    string
	host    string
	headers http.Header
	cookies []*http.Cookie
	tls     bool
}

// browserWriteCall intentionally does not manufacture browser provenance.
// Tests must supply every Origin, Referer, fetch metadata, and custom header.
func browserWriteCall(t *testing.T, e *echo.Echo, in browserWriteRequest) accessResult {
	t.Helper()
	req := httptest.NewRequest(in.method, in.target, strings.NewReader(in.body))
	if in.host != "" {
		req.Host = in.host
	} else {
		req.Host = "127.0.0.1"
	}
	if in.tls {
		req.TLS = &tls.ConnectionState{}
	}
	for key, values := range in.headers {
		req.Header[key] = append([]string(nil), values...)
	}
	for _, cookie := range in.cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	var panicked any
	func() { defer func() { panicked = recover() }(); e.ServeHTTP(rec, req) }()
	if panicked != nil {
		t.Errorf("HTTP %s %s panicked: %v", in.method, in.target, panicked)
	}
	return accessResult{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone(), panicked: panicked}
}

type browserLogin struct {
	user    core.User
	token   string
	cookies []*http.Cookie
}

func browserWriteLogin(t *testing.T, h *accessHarness, username, role string) browserLogin {
	t.Helper()
	password := "BrowserWrite123!"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash login password: %v", err)
	}
	u := core.User{Username: username, Email: username + "@example.test", Role: role, Provider: "local", Password: string(hash)}
	if err := h.db.Create(&u).Error; err != nil {
		t.Fatalf("create browser-write user: %v", err)
	}
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
	headers := http.Header{"Origin": {browserWriteOrigin}, "Content-Type": {echo.MIMEApplicationJSON}, "Accept": {echo.MIMEApplicationJSON}}
	got := browserWriteCall(t, h.echo, browserWriteRequest{method: http.MethodPost, target: "/auth/login", body: body, headers: headers})
	if got.code != http.StatusOK {
		t.Fatalf("same-origin login returned %d: %s", got.code, got.body)
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(got.body), &payload); err != nil || payload.Token == "" {
		t.Fatalf("login returned no token")
	}
	cookies := (&http.Response{Header: got.header}).Cookies()
	return browserLogin{user: u, token: payload.Token, cookies: cookies}
}

func browserWriteCookie(name string, cookies []*http.Cookie) *http.Cookie {
	for _, c := range cookies {
		if c.Name == name {
			cloned := *c
			return &cloned
		}
	}
	return nil
}

func browserWriteSnapshot(t *testing.T, h *accessHarness) string {
	t.Helper()
	return accessDatabaseSnapshot(t, h.db)
}

func browserWriteUploadsSnapshot(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir("data/uploads")
	if os.IsNotExist(err) {
		return "<missing>"
	}
	if err != nil {
		t.Fatalf("read uploads snapshot: %v", err)
	}
	var snapshot strings.Builder
	for _, entry := range entries {
		content, err := os.ReadFile("data/uploads/" + entry.Name())
		if err != nil {
			t.Fatalf("read upload %q for snapshot: %v", entry.Name(), err)
		}
		fmt.Fprintf(&snapshot, "%s:%q\n", entry.Name(), content)
	}
	return snapshot.String()
}

func TestBrowserWriteProtection(t *testing.T) {
	t.Setenv("APP_URL", "")
	h := newAccessHarness(t)
	login := browserWriteLogin(t, h, "browser-write-author", "author")
	session := browserWriteCookie("jelly-cms-session", login.cookies)
	jwtCookie := browserWriteCookie("jwt_token", login.cookies)
	if session == nil || jwtCookie == nil {
		t.Fatal("login did not issue both expected authentication cookies")
	}
	cookieAuth := []*http.Cookie{session, jwtCookie}
	postBody := `{"title":"browser write protection","content":"proof text","type":"post","status":"draft"}`
	request := func(method, target string, body string, headers http.Header, cookies []*http.Cookie, token, host string, tls bool) accessResult {
		hdr := make(http.Header)
		for k, v := range headers {
			hdr[k] = append([]string(nil), v...)
		}
		if token != "" {
			hdr.Set(echo.HeaderAuthorization, "Bearer "+token)
		}
		if body != "" && hdr.Get(echo.HeaderContentType) == "" {
			hdr.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		}
		return browserWriteCall(t, h.echo, browserWriteRequest{method: method, target: target, body: body, headers: hdr, cookies: cookies, host: host, tls: tls})
	}

	t.Run("login_requires_provenance_before_credentials", func(t *testing.T) {
		password := "BrowserWrite123!"
		body := fmt.Sprintf(`{"username":%q,"password":%q}`, login.user.Username, password)
		for _, tc := range []struct {
			name    string
			headers http.Header
		}{
			{"no source", http.Header{"Content-Type": {echo.MIMEApplicationJSON}, "Accept": {echo.MIMEApplicationJSON}}},
			{"external form", http.Header{"Content-Type": {"application/x-www-form-urlencoded"}, "Accept": {echo.MIMEApplicationJSON}, "Origin": {"https://evil.test"}, "Sec-Fetch-Site": {"cross-site"}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				before := browserWriteSnapshot(t, h)
				loginBody := body
				if strings.HasPrefix(tc.headers.Get("Content-Type"), "application/x-www-form-urlencoded") {
					loginBody = "username=" + login.user.Username + "&password=" + password
				}
				got := request(http.MethodPost, "/auth/login", loginBody, tc.headers, nil, "", "", false)
				if got.code != http.StatusForbidden {
					t.Errorf("login returned %d, want 403", got.code)
				}
				if len((&http.Response{Header: got.header}).Cookies()) != 0 {
					t.Error("rejected login set a cookie")
				}
				if after := browserWriteSnapshot(t, h); after != before {
					t.Error("rejected login changed database state")
				}
			})
		}
		for _, headers := range []http.Header{{"Origin": {browserWriteOrigin}, "Accept": {echo.MIMEApplicationJSON}}, {"X-Jelly-Csrf": {"1"}, "Accept": {echo.MIMEApplicationJSON}}} {
			got := request(http.MethodPost, "/auth/login", body, headers, nil, "", "", false)
			if got.code != http.StatusOK {
				t.Errorf("accepted login evidence returned %d", got.code)
			}
		}
	})

	t.Run("cookie_write_rejects_bad_provenance_without_database_change", func(t *testing.T) {
		cases := []struct {
			name    string
			headers http.Header
			host    string
		}{
			{name: "no evidence", headers: http.Header{}},
			{name: "evil origin", headers: http.Header{"Origin": {"https://evil.test"}}},
			{name: "sibling same-site", headers: http.Header{"Origin": {"http://shop.example.test"}}, host: "cms.example.test"},
			{name: "null origin", headers: http.Header{"Origin": {"null"}}},
			{name: "duplicate origin", headers: http.Header{"Origin": {browserWriteOrigin, browserWriteOrigin}}},
			{name: "duplicate fetch metadata", headers: http.Header{"Sec-Fetch-Site": {"same-origin", "same-origin"}}},
			{name: "same site metadata cannot be overridden", headers: http.Header{"Sec-Fetch-Site": {"same-site"}, "X-Jelly-Csrf": {"1"}}},
			{name: "cross site metadata cannot be overridden", headers: http.Header{"Sec-Fetch-Site": {"cross-site"}, "X-Jelly-Csrf": {"1"}}},
			{name: "unknown metadata cannot be overridden", headers: http.Header{"Sec-Fetch-Site": {"unknown"}, "X-Jelly-Csrf": {"1"}}},
			{name: "none metadata lacks proof", headers: http.Header{"Sec-Fetch-Site": {"none"}}},
			{name: "same origin conflicts with cross site metadata", headers: http.Header{"Origin": {browserWriteOrigin}, "Sec-Fetch-Site": {"cross-site"}}},
			{name: "same origin metadata conflicts", headers: http.Header{"Origin": {"https://evil.test"}, "Sec-Fetch-Site": {"same-origin"}}},
			{name: "evil referer", headers: http.Header{"Referer": {"https://evil.test/path"}}},
			{name: "same origin origin conflicts with evil referer", headers: http.Header{"Origin": {browserWriteOrigin}, "Referer": {"https://evil.test/path"}}},
			{name: "same origin origin with duplicate referer", headers: http.Header{"Origin": {browserWriteOrigin}, "Referer": {browserWriteOrigin + "/one", browserWriteOrigin + "/two"}}},
			{name: "wrong scheme", headers: http.Header{"Origin": {"https://127.0.0.1"}}},
			{name: "wrong port", headers: http.Header{"Origin": {"http://127.0.0.1:81"}}},
			{name: "cross site with custom header", headers: http.Header{"Origin": {"https://evil.test"}, "X-Jelly-Csrf": {"1"}}},
			{name: "wrong custom header", headers: http.Header{"X-Jelly-Csrf": {"0"}}},
		}
		for i, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				before := browserWriteSnapshot(t, h)
				body := fmt.Sprintf(`{"title":"negative %d","content":"blocked","type":"post","status":"draft"}`, i)
				got := request(http.MethodPost, "/api/admin/posts", body, tc.headers, cookieAuth, "", tc.host, false)
				if got.panicked != nil {
					t.Errorf("request panicked: %v", got.panicked)
				}
				if got.code != http.StatusForbidden {
					t.Errorf("write returned %d, want JSON 403: %s", got.code, got.body)
				}
				if !strings.Contains(got.header.Get(echo.HeaderContentType), "json") {
					t.Errorf("403 response is not JSON: %q", got.header.Get(echo.HeaderContentType))
				}
				if after := browserWriteSnapshot(t, h); after != before {
					t.Error("rejected write changed database snapshot")
				}
			})
		}
	})

	t.Run("cookie_write_accepts_each_documented_same_origin_proof", func(t *testing.T) {
		cases := []struct {
			name    string
			headers http.Header
		}{
			{"origin", http.Header{"Origin": {browserWriteOrigin}}},
			{"referer path and query", http.Header{"Referer": {browserWriteOrigin + "/admin/posts?draft=1"}}},
			{"same origin metadata", http.Header{"Sec-Fetch-Site": {"same-origin"}}},
			{"none metadata with custom header", http.Header{"Sec-Fetch-Site": {"none"}, "X-Jelly-Csrf": {"1"}}},
			{"custom header", http.Header{"X-Jelly-Csrf": {"1"}}},
		}
		for i, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				body := fmt.Sprintf(`{"title":"allowed %d","content":"proof","type":"post","status":"draft"}`, i)
				got := request(http.MethodPost, "/api/admin/posts", body, tc.headers, cookieAuth, "", "", false)
				if got.code != http.StatusOK {
					t.Errorf("write returned %d, want 200: %s", got.code, got.body)
					return
				}
				var created core.Post
				if err := h.db.Where("title = ?", fmt.Sprintf("allowed %d", i)).First(&created).Error; err != nil {
					t.Errorf("read created post: %v", err)
					return
				}
				if created.AuthorID != login.user.ID || created.Content != "proof" {
					t.Errorf("stored post author/content mismatch: author=%d content=%q", created.AuthorID, created.Content)
				}
			})
		}
	})

	t.Run("same_origin_target_normalization_and_proxy_rules", func(t *testing.T) {
		for i, tc := range []struct {
			name, appURL, host, origin string
			tls                        bool
			want                       int
		}{
			{"default port normalized", "", "127.0.0.1:80", "http://127.0.0.1", false, http.StatusOK},
			{"ipv6 host", "", "[::1]", "http://[::1]", false, http.StatusOK},
			{"expanded app ipv6 matches compressed origin", "http://[0:0:0:0:0:0:0:1]:8080", "[::1]:8080", "http://[::1]:8080", false, http.StatusOK},
			{"compressed app ipv6 matches expanded origin", "http://[::1]:8080", "[::1]:8080", "http://[0:0:0:0:0:0:0:1]:8080", false, http.StatusOK},
			{"mapped ipv6 hex matches dotted origin", "http://[::ffff:7f00:1]:8080", "[::ffff:7f00:1]:8080", "http://[::ffff:127.0.0.1]:8080", false, http.StatusOK},
			{"mapped ipv6 dotted matches hex origin", "http://[::ffff:127.0.0.1]:8080", "[::ffff:127.0.0.1]:8080", "http://[::ffff:7f00:1]:8080", false, http.StatusOK},
			{"ipv4 target differs from mapped ipv6 origin", "http://127.0.0.1:8080", "127.0.0.1:8080", "http://[::ffff:7f00:1]:8080", false, http.StatusForbidden},
			{"mapped ipv6 target differs from ipv4 origin", "http://[::ffff:7f00:1]:8080", "[::ffff:7f00:1]:8080", "http://127.0.0.1:8080", false, http.StatusForbidden},
			{"tls uses request state", "", "127.0.0.1", "https://127.0.0.1", true, http.StatusOK},
			{"tls does not trust forwarded proto", "", "127.0.0.1", "https://127.0.0.1", false, http.StatusForbidden},
			{"explicit https proxy origin", "https://cms.example.test", "internal:8080", "https://cms.example.test", false, http.StatusOK},
			{"invalid app url unavailable", "https://cms.example.test/path?bad=1", "internal:8080", "https://cms.example.test", false, http.StatusServiceUnavailable},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Setenv("APP_URL", tc.appURL)
				headers := http.Header{"Origin": {tc.origin}, "X-Forwarded-Proto": {"https"}, "X-Forwarded-Host": {"cms.example.test"}, "Forwarded": {"proto=https;host=cms.example.test"}}
				before := browserWriteSnapshot(t, h)
				body := fmt.Sprintf(`{"title":"target check %d","content":"x","type":"post"}`, i)
				got := request(http.MethodPost, "/api/admin/posts", body, headers, cookieAuth, "", tc.host, tc.tls)
				if got.code != tc.want {
					t.Errorf("write returned %d, want %d: %s", got.code, tc.want, got.body)
				}
				if tc.want != http.StatusOK {
					if after := browserWriteSnapshot(t, h); after != before {
						t.Error("rejected target check modified database")
					}
				}
			})
		}
	})

	t.Run("bearer_and_mixed_credentials", func(t *testing.T) {
		good := request(http.MethodPost, "/api/admin/posts", `{"title":"bearer only","content":"ok","type":"post"}`, http.Header{}, nil, login.token, "", false)
		if good.code != http.StatusOK {
			t.Errorf("valid bearer write returned %d", good.code)
		}
		bad := request(http.MethodPost, "/api/admin/posts", postBody, http.Header{}, nil, "not.a.jwt", "", false)
		if bad.code != http.StatusUnauthorized {
			t.Errorf("invalid bearer write returned %d, want 401", bad.code)
		}
		mixed := request(http.MethodPost, "/api/admin/posts", postBody, http.Header{"Origin": {"https://evil.test"}}, cookieAuth, login.token, "", false)
		if mixed.code != http.StatusForbidden {
			t.Errorf("bearer plus cookie external write returned %d, want 403", mixed.code)
		}
	})

	t.Run("reads_auth_and_payment_boundaries", func(t *testing.T) {
		read := request(http.MethodGet, "/api/admin/posts", "", http.Header{}, cookieAuth, "", "", false)
		if read.code != http.StatusOK {
			t.Errorf("protected GET returned %d", read.code)
		}
		anon := request(http.MethodPost, "/api/admin/posts", postBody, http.Header{"Origin": {browserWriteOrigin}}, nil, "", "", false)
		if anon.code != http.StatusUnauthorized {
			t.Errorf("unauthenticated write returned %d, want 401", anon.code)
		}
		oldPayment := paymentService
		paymentService = payment.NewService(h.db)
		defer func() { paymentService = oldPayment }()
		before := browserWriteSnapshot(t, h)
		callback := request(http.MethodPost, "/api/payment/unknown-gateway/callback", `{"order_id":"missing","status":"paid"}`, http.Header{}, nil, "", "", false)
		if callback.code == http.StatusForbidden {
			t.Errorf("payment callback was intercepted as CSRF: %s", callback.body)
		}
		if callback.code < 400 || callback.code >= 500 {
			t.Errorf("unknown gateway callback returned %d, want existing client rejection", callback.code)
		}
		if after := browserWriteSnapshot(t, h); after != before {
			t.Error("rejected payment callback modified database")
		}
		create := request(http.MethodPost, "/api/payment/create", `{}`, http.Header{"Origin": {"https://evil.test"}}, cookieAuth, "", "", false)
		if create.code != http.StatusForbidden {
			t.Errorf("external cookie payment create returned %d, want 403", create.code)
		}
	})

	t.Run("each_authentication_cookie_requires_and_accepts_its_own_proof", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			cookie *http.Cookie
		}{
			{"session only", session},
			{"jwt only", jwtCookie},
		} {
			t.Run(tc.name, func(t *testing.T) {
				before := browserWriteSnapshot(t, h)
				blocked := request(http.MethodPost, "/api/admin/posts", `{"title":"single-cookie-blocked-`+strings.ReplaceAll(tc.name, " ", "-")+`","content":"blocked","type":"post"}`, http.Header{}, []*http.Cookie{tc.cookie}, "", "", false)
				if blocked.code != http.StatusForbidden {
					t.Errorf("cookie-only no-source write returned %d, want 403", blocked.code)
				}
				if after := browserWriteSnapshot(t, h); after != before {
					t.Error("rejected cookie-only write changed database snapshot")
				}
				title := "single-cookie-allowed-" + strings.ReplaceAll(tc.name, " ", "-")
				allowed := request(http.MethodPost, "/api/admin/posts", fmt.Sprintf(`{"title":%q,"content":"allowed","type":"post"}`, title), http.Header{"Origin": {browserWriteOrigin}}, []*http.Cookie{tc.cookie}, "", "", false)
				if allowed.code != http.StatusOK {
					t.Errorf("same-origin cookie-only write returned %d, want 200: %s", allowed.code, allowed.body)
					return
				}
				var created core.Post
				if err := h.db.Where("title = ?", title).First(&created).Error; err != nil {
					t.Errorf("read same-origin cookie-only post: %v", err)
				} else if created.AuthorID != login.user.ID || created.Content != "allowed" {
					t.Errorf("stored cookie-only post mismatch: author=%d content=%q", created.AuthorID, created.Content)
				}
			})
		}
	})

	t.Run("valid_bearer_with_empty_login_cookie_still_requires_provenance", func(t *testing.T) {
		before := browserWriteSnapshot(t, h)
		got := request(http.MethodPost, "/api/admin/posts", `{"title":"empty-cookie-bearer","content":"blocked","type":"post"}`, http.Header{"Cookie": {"jwt_token="}}, nil, login.token, "", false)
		if got.code != http.StatusForbidden {
			t.Errorf("valid bearer with empty JWT cookie returned %d, want 403", got.code)
		}
		if after := browserWriteSnapshot(t, h); after != before {
			t.Error("empty-cookie mixed-auth write changed database snapshot")
		}
	})

	t.Run("cookie_protected_updates_and_deletes_reject_external_and_missing_sources", func(t *testing.T) {
		for _, method := range []string{http.MethodPut, http.MethodDelete} {
			for _, provenance := range []struct {
				name    string
				headers http.Header
			}{{"no source", http.Header{}}, {"external", http.Header{"Origin": {"https://evil.test"}}}} {
				t.Run(method+"/"+provenance.name, func(t *testing.T) {
					post := core.Post{Title: "owned mutation fixture", Slug: fmt.Sprintf("owned-mutation-%s-%s", strings.ToLower(method), strings.ReplaceAll(provenance.name, " ", "-")), Content: "before", Type: "post", Status: "draft", AuthorID: login.user.ID}
					if err := h.db.Create(&post).Error; err != nil {
						t.Fatalf("create author-owned post fixture: %v", err)
					}
					before := browserWriteSnapshot(t, h)
					body := ""
					if method == http.MethodPut {
						body = `{"title":"must remain unchanged","content":"after","type":"post","status":"published"}`
					}
					got := request(method, fmt.Sprintf("/api/admin/posts/%d", post.ID), body, provenance.headers, cookieAuth, "", "", false)
					if got.code != http.StatusForbidden {
						t.Errorf("%s returned %d, want 403", method, got.code)
					}
					if after := browserWriteSnapshot(t, h); after != before {
						t.Errorf("rejected %s changed complete database snapshot", method)
					}
				})
			}
		}
	})

	t.Run("multipart_media_upload_rejects_bad_origin_without_file_or_database_changes", func(t *testing.T) {
		mediaType, mediaBody := accessMultipartBody(t, "file", "csrf-blocked-media.txt", "must not be written")
		headers := http.Header{"Content-Type": {mediaType}, "Origin": {"https://evil.test"}}
		dbBefore := browserWriteSnapshot(t, h)
		filesBefore := browserWriteUploadsSnapshot(t)
		got := request(http.MethodPost, "/api/admin/media", string(mediaBody), headers, cookieAuth, "", "", false)
		if got.code != http.StatusForbidden {
			t.Errorf("multipart upload returned %d, want 403: %s", got.code, got.body)
		}
		if after := browserWriteSnapshot(t, h); after != dbBefore {
			t.Error("rejected multipart upload changed database snapshot")
		}
		if after := browserWriteUploadsSnapshot(t); after != filesBefore {
			t.Error("rejected multipart upload changed uploads files")
		}
	})

	t.Run("logout_protects_cookie_auth_but_preserves_bearer_idempotence", func(t *testing.T) {
		crossSite := request(http.MethodPost, "/auth/logout", "", http.Header{"Origin": {"https://evil.test"}, "Sec-Fetch-Site": {"cross-site"}}, nil, "", "", false)
		if crossSite.code != http.StatusForbidden {
			t.Errorf("cross-site logout without credentials returned %d, want 403", crossSite.code)
		}
		if len((&http.Response{Header: crossSite.header}).Cookies()) != 0 {
			t.Error("cross-site rejected logout emitted cookie deletion")
		}
		for i, headers := range []http.Header{{}, {"Origin": {"https://evil.test"}}} {
			fresh := browserWriteLogin(t, h, fmt.Sprintf("%s-logout-%d", login.user.Username, i), login.user.Role)
			freshCookies := []*http.Cookie{browserWriteCookie("jelly-cms-session", fresh.cookies), browserWriteCookie("jwt_token", fresh.cookies)}
			got := request(http.MethodPost, "/auth/logout", "", headers, freshCookies, "", "", false)
			if got.code != http.StatusForbidden {
				t.Errorf("untrusted logout returned %d, want 403", got.code)
			}
			if len((&http.Response{Header: got.header}).Cookies()) != 0 {
				t.Error("rejected logout emitted cookie deletion")
			}
			me := request(http.MethodGet, "/api/auth/me", "", http.Header{}, freshCookies, "", "", false)
			if me.code != http.StatusOK {
				t.Errorf("rejected logout invalidated original cookie session: %d", me.code)
			}
		}
		fresh := browserWriteLogin(t, h, login.user.Username+"-logout-success", login.user.Role)
		freshCookies := []*http.Cookie{browserWriteCookie("jelly-cms-session", fresh.cookies), browserWriteCookie("jwt_token", fresh.cookies)}
		out := request(http.MethodPost, "/auth/logout", "", http.Header{"Origin": {browserWriteOrigin}}, freshCookies, "", "", false)
		if out.code != http.StatusOK {
			t.Errorf("same-origin logout returned %d", out.code)
		}
		me := request(http.MethodGet, "/api/auth/me", "", http.Header{}, freshCookies, "", "", false)
		if me.code != http.StatusUnauthorized {
			t.Errorf("logout replay returned %d, want 401", me.code)
		}
		bearer := request(http.MethodPost, "/auth/logout", "", http.Header{}, nil, login.token, "", false)
		if bearer.code != http.StatusOK {
			t.Errorf("bearer-only logout returned %d, want 200", bearer.code)
		}
		anonymous := request(http.MethodPost, "/auth/logout", "", http.Header{}, nil, "", "", false)
		if anonymous.code != http.StatusOK {
			t.Errorf("credential-free logout returned %d, want 200", anonymous.code)
		}
		if len((&http.Response{Header: anonymous.header}).Cookies()) != 0 {
			t.Error("credential-free logout emitted unnecessary cookie deletion")
		}
		bearerReplay := request(http.MethodGet, "/api/auth/me", "", http.Header{}, nil, login.token, "", false)
		if bearerReplay.code != http.StatusUnauthorized {
			t.Errorf("bearer logout replay returned %d, want 401", bearerReplay.code)
		}
	})

	t.Run("install_endpoints_reject_before_database_or_filesystem_work", func(t *testing.T) {
		previousConfig, previousDB := config.GlobalConfig, data.DB
		installEcho := newInstallRoutesServer(t)
		config.GlobalConfig, data.DB = previousConfig, previousDB
		defer func() { config.GlobalConfig, data.DB = previousConfig, previousDB }()
		config.GlobalConfig, data.DB = nil, nil
		port, attempts := countingListener(t)
		t.Setenv("DB_HOST", "127.0.0.1")
		t.Setenv("DB_PORT", port)
		payload := `{"db_host":"127.0.0.1","db_port":"` + port + `","db_user":"jelly","db_pass":"BrowserWrite123!","db_name":"jelly","db_timezone":"UTC","site_title":"test","admin_user":"admin","admin_email":"admin@example.test","admin_pass":"BrowserWrite123!"}`
		for _, path := range []string{"/api/install", "/api/install/test-db"} {
			for _, provenance := range []struct {
				name    string
				headers http.Header
			}{{"no source", http.Header{}}, {"external origin", http.Header{"Origin": {"https://evil.test"}}}} {
				t.Run(strings.TrimPrefix(path, "/api/")+"/"+provenance.name, func(t *testing.T) {
					headers := http.Header{"Content-Type": {echo.MIMEApplicationJSON}}
					for key, values := range provenance.headers {
						headers[key] = append([]string(nil), values...)
					}
					got := browserWriteCall(t, installEcho, browserWriteRequest{method: http.MethodPost, target: path, body: payload, headers: headers})
					if got.code != http.StatusForbidden {
						t.Errorf("%s returned %d, want 403", path, got.code)
					}
				})
			}
		}
		if n := observedAttempts(attempts); n != 0 {
			t.Errorf("install requests attempted %d database connections", n)
		}
		if config.GlobalConfig != nil || data.DB != nil {
			t.Error("rejected install changed global configuration or database handle")
		}
		if _, err := os.Stat("data/config.json"); !os.IsNotExist(err) {
			t.Errorf("rejected install left config file, stat error=%v", err)
		}
		got := browserWriteCall(t, installEcho, browserWriteRequest{method: http.MethodPost, target: "/api/install", body: "{}", headers: http.Header{"Content-Type": {echo.MIMEApplicationJSON}, "Origin": {browserWriteOrigin}}})
		if got.code != http.StatusBadRequest {
			t.Errorf("same-origin invalid install returned %d, want 400", got.code)
		}
		if config.GlobalConfig != nil || data.DB != nil {
			t.Error("invalid install changed global configuration or database handle")
		}
	})
}
