package server

// Ticket 08b (docs/tickets/08-system.md): code injection. Covers the three raw
// HTML slots (<head> site-wide, <head> on content pages, footer), output on public
// pages only, commerce pages skipping injection by default, the interactive-only
// admin write that re-checks the password, the 64 KiB limit with its audit row, and
// head_content on content pages only. The contract is the 「程式碼注入」 bullet and
// the 安全規則 section. These tests fail until 08b lands. Do not edit them to match
// an implementation.
//
// Writes go through PUT /api/admin/settings/injection with the jwt_token cookie and
// no Authorization header. That is the interactive path. Commerce routes belong to
// ticket 07, so /account is the registered commerce placeholder here. API token
// principals (jly_) belong to ticket 06.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/bcrypt"
)

// s08bInjectionPassword is the password the injection writes must re-enter.
const s08bInjectionPassword = "Inject08b-Password!"

// s08bPasswordUser creates a local user with a known password, so an interactive
// write can be re-authenticated with current_password.
func s08bPasswordUser(t *testing.T, h *accessHarness, username, role string) core.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(s08bInjectionPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	u := core.User{Username: username, Email: username + "@example.test", Role: role, Provider: "local", Password: string(hash)}
	if err := h.db.Create(&u).Error; err != nil {
		t.Fatalf("create %s user %q: %v", role, username, err)
	}
	return u
}

// s08bInjectionPut sends an interactive injection write.
func s08bInjectionPut(t *testing.T, h *accessHarness, jwt string, fields map[string]string) accessResult {
	t.Helper()
	return s08bInteractive(h, http.MethodPut, "/api/admin/settings/injection", strings.NewReader(s08bJSON(t, fields)), jwt, echo.MIMEApplicationJSON)
}

func TestInjectionRendersOnPublicPagesOnly(t *testing.T) {
	h := archivesHarness(t)
	admin := s08bPasswordUser(t, h, "s08b-inject-admin", "admin")
	token := h.token(admin)
	author := h.user("author", "s08b-inject-author")
	s08bSeedPost(t, h, author, s08bPost{title: "Inject Probe Post", slug: "inject-probe"})

	const probe = `<meta name="inject-probe-all" content="yes">`
	if r := s08bInjectionPut(t, h, token, map[string]string{"head_all": probe, "current_password": s08bInjectionPassword}); r.code != http.StatusOK {
		t.Fatalf("seed public-page injection = %d: %s", r.code, r.body)
	}

	archivesWantText(t, "/", archivesPage(t, h, "/"), probe)
	archivesWantText(t, "/post/inject-probe", archivesPage(t, h, "/post/inject-probe"), probe)

	for _, path := range []string{"/admin", "/admin/settings/site", "/login", "/install"} {
		r := h.request(http.MethodGet, path, "", token, echo.MIMETextHTML, "")
		h.mustNoPanic(r, "GET "+path)
		if strings.Contains(r.body, probe) {
			t.Errorf("GET %s outputs the site-wide head injection; it must be public pages only", path)
		}
	}
}

func TestInjectionNotOnCommerceByDefault(t *testing.T) {
	h := archivesHarness(t)
	admin := s08bPasswordUser(t, h, "s08b-commerce-admin", "admin")
	token := h.token(admin)
	shopper := h.user("customer", "s08b-commerce-shopper")
	shopperToken := h.token(shopper)

	const probe = `<meta name="inject-probe-commerce" content="yes">`
	if r := s08bInjectionPut(t, h, token, map[string]string{"head_all": probe, "current_password": s08bInjectionPassword}); r.code != http.StatusOK {
		t.Fatalf("seed commerce injection = %d: %s", r.code, r.body)
	}

	t.Run("checkout", func(t *testing.T) {
		r := h.request(http.MethodGet, "/checkout", "", shopperToken, echo.MIMETextHTML, "")
		h.mustNoPanic(r, "GET /checkout")
		if r.code == http.StatusNotFound {
			t.Skip("/checkout is not registered in this branch; ticket 07 adds the commerce routes. /account covers the default-off rule here.")
		}
		if strings.Contains(r.body, probe) {
			t.Errorf("GET /checkout outputs the injection by default")
		}
	})

	// /account redirects staff to the admin; a customer's own page is /account/orders.
	r := h.request(http.MethodGet, "/account/orders", "", shopperToken, echo.MIMETextHTML, "")
	h.mustNoPanic(r, "GET /account/orders by default")
	if strings.Contains(r.body, probe) {
		t.Errorf("GET /account/orders outputs the injection while inject_on_commerce is off")
	}

	onCommerce := s08bInteractive(h, http.MethodPut, "/api/admin/settings/injection", strings.NewReader(`{"on_commerce":true,"current_password":"Inject08b-Password!"}`), token, echo.MIMEApplicationJSON)
	if onCommerce.code != http.StatusOK {
		t.Fatalf("enable commerce injection through the dedicated API = %d: %s", onCommerce.code, onCommerce.body)
	}
	on := h.request(http.MethodGet, "/account/orders", "", shopperToken, echo.MIMETextHTML, "")
	h.mustNoPanic(on, "GET /account/orders with inject_on_commerce")
	s08bWant(t, "GET /account/orders with inject_on_commerce=true", on, http.StatusOK)
	if !strings.Contains(on.body, probe) {
		t.Errorf("GET /account/orders does not output the injection with inject_on_commerce=true")
	}
}

func TestInjectionWriteNeedsAdminInteractiveAndPassword(t *testing.T) {
	h := archivesHarness(t)
	admin := s08bPasswordUser(t, h, "s08b-inject-write-admin", "admin")
	editor := s08bPasswordUser(t, h, "s08b-inject-write-editor", "editor")
	adminJWT, editorJWT := h.token(admin), h.token(editor)

	const probe = `<meta name="inject-write-probe" content="yes">`
	withPassword := func(pw string) map[string]string {
		return map[string]string{"head_all": probe, "current_password": pw}
	}

	// Editors are refused even with the right password.
	if r := s08bInjectionPut(t, h, editorJWT, withPassword(s08bInjectionPassword)); r.code != http.StatusForbidden {
		t.Errorf("editor PUT /api/admin/settings/injection returned %d, want 403", r.code)
	}
	// Admin without current_password.
	if r := s08bInjectionPut(t, h, adminJWT, map[string]string{"head_all": probe}); r.code != http.StatusBadRequest && r.code != http.StatusForbidden {
		t.Errorf("admin without current_password returned %d, want 400 or 403", r.code)
	}
	// Admin with the wrong password.
	if r := s08bInjectionPut(t, h, adminJWT, withPassword("not-the-password")); r.code != http.StatusForbidden {
		t.Errorf("admin with wrong current_password returned %d, want 403", r.code)
	}
	// The interactive path refuses an Authorization header, even with the JWT and the right password.
	bearerReq := httptest.NewRequest(http.MethodPut, "/api/admin/settings/injection", strings.NewReader(s08bJSON(t, withPassword(s08bInjectionPassword))))
	bearerReq.Header.Set("X-Jelly-CSRF", "1")
	bearerReq.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
	bearerReq.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	bearerReq.Header.Set(echo.HeaderAuthorization, "Bearer "+adminJWT)
	bearer := s08bDo(h, bearerReq)
	h.mustNoPanic(bearer, "PUT injection with Authorization header")
	if bearer.code != http.StatusForbidden {
		t.Errorf("PUT injection with a Bearer header returned %d, want 403", bearer.code)
	}

	t.Run("API token principal", func(t *testing.T) {
		r := h.request(http.MethodPut, "/api/admin/settings/injection", s08bJSON(t, withPassword(s08bInjectionPassword)), "jly_probe", echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
		h.mustNoPanic(r, "PUT injection with jly_ token")
		if r.code != http.StatusUnauthorized {
			t.Errorf("PUT injection with an unknown jly_ token returned %d, want 401", r.code)
		}
		t.Skip("API token principals (jly_) are ticket 06; the 403 for a real token is asserted once tokens exist")
	})

	// The interactive admin with the right password writes, and the value is read back.
	ok := s08bInjectionPut(t, h, adminJWT, withPassword(s08bInjectionPassword))
	h.mustNoPanic(ok, "PUT injection as admin")
	s08bWant(t, "PUT /api/admin/settings/injection as interactive admin", ok, http.StatusOK)
	read := h.request(http.MethodGet, "/api/admin/settings/injection", "", adminJWT, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(read, "GET /api/admin/settings/injection")
	s08bWant(t, "GET /api/admin/settings/injection", read, http.StatusOK)
	// The value comes back as JSON, which escapes quotes and angle brackets, so compare the decoded field.
	var stored struct {
		HeadAll string `json:"head_all"`
	}
	if err := json.Unmarshal([]byte(read.body), &stored); err != nil || stored.HeadAll != probe {
		t.Errorf("GET /api/admin/settings/injection head_all = %q (decode error %v), want %q", stored.HeadAll, err, probe)
	}
	archivesWantText(t, "/", archivesPage(t, h, "/"), probe)
}

func TestInjectionSizeLimitAndAudit(t *testing.T) {
	h := newAccessHarness(t)
	admin := s08bPasswordUser(t, h, "s08b-inject-size-admin", "admin")
	jwt := h.token(admin)

	const limit = 64 << 10
	const marker = "<!-- AUDIT-MARK-08B -->"
	exact := marker + strings.Repeat("a", limit-len(marker))
	over := strings.Repeat("b", limit+1)

	tooLarge := s08bInjectionPut(t, h, jwt, map[string]string{"head_all": over, "current_password": s08bInjectionPassword})
	h.mustNoPanic(tooLarge, "PUT injection over the limit")
	if tooLarge.code != http.StatusRequestEntityTooLarge {
		t.Errorf("PUT injection of 64 KiB + 1 returned %d, want 413", tooLarge.code)
	}

	atLimit := s08bInjectionPut(t, h, jwt, map[string]string{"head_all": exact, "current_password": s08bInjectionPassword})
	h.mustNoPanic(atLimit, "PUT injection at the limit")
	s08bWant(t, "PUT injection of exactly 64 KiB", atLimit, http.StatusOK)

	sum := sha256.Sum256([]byte(exact))
	digest := hex.EncodeToString(sum[:])
	if n := s08bCount(t, h, `SELECT COUNT(*) FROM system_logs WHERE source = 'audit' AND fields::text LIKE ?`, "%"+digest+"%"); n < 1 {
		t.Errorf("no audit row carries the sha256 of the written injection")
	}
	if n := s08bCount(t, h, `SELECT COUNT(*) FROM system_logs WHERE source = 'audit' AND fields::text LIKE ?`, "%"+strconv.Itoa(len(exact))+"%"); n < 1 {
		t.Errorf("no audit row carries the length %d of the written injection", len(exact))
	}
	if n := s08bCount(t, h, `SELECT COUNT(*) FROM system_logs WHERE message LIKE '%AUDIT-MARK-08B%' OR fields::text LIKE '%AUDIT-MARK-08B%'`); n != 0 {
		t.Errorf("audit log holds the injected content in %d rows; it must hold only who, length and sha256", n)
	}
}

func TestInjectionContentHeadOnlyOnContentPages(t *testing.T) {
	h := archivesHarness(t)
	admin := s08bPasswordUser(t, h, "s08b-content-head-admin", "admin")
	token := h.token(admin)
	author := h.user("author", "s08b-content-head-author")
	s08bSeedPost(t, h, author, s08bPost{title: "Content Head Post", slug: "content-head-post"})
	s08bSeedPost(t, h, author, s08bPost{title: "Content Head Page", slug: "content-head-page", typ: "page"})

	const probe = `<meta name="content-head-probe" content="yes">`
	if r := s08bInjectionPut(t, h, token, map[string]string{"head_content": probe, "current_password": s08bInjectionPassword}); r.code != http.StatusOK {
		t.Fatalf("seed content-page injection = %d: %s", r.code, r.body)
	}

	archivesWantText(t, "/post/content-head-post", archivesPage(t, h, "/post/content-head-post"), probe)
	archivesWantText(t, "/content-head-page", archivesPage(t, h, "/content-head-page"), probe)

	home := archivesPage(t, h, "/")
	archivesWantAbsent(t, "/", home, probe)
	search := archivesPage(t, h, "/search?q=content")
	archivesWantAbsent(t, "/search?q=content", search, probe)
}
