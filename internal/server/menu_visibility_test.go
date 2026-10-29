package server

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
)

const menuVisibilityJWTSecret = "menu-visibility-integration-jwt-secret-2026"

func menuVisibilityHarness(t *testing.T) *accessHarness {
	t.Helper()
	t.Setenv("SESSION_SECRET", "menu-visibility-integration-session-secret-2026")
	t.Setenv("JWT_SECRET", menuVisibilityJWTSecret)
	return archivesHarness(t)
}

func menuVisibilityAddItem(t *testing.T, h *accessHarness, menuID uint, parent *uint, position int, label, kind, rawURL string, ref uint, loginOnly bool) core.MenuItem {
	t.Helper()
	item := core.MenuItem{MenuID: menuID, ParentID: parent, Position: position, Label: label, Type: kind, URL: rawURL, RefID: ref, LoginOnly: loginOnly}
	if err := h.db.Create(&item).Error; err != nil {
		t.Fatalf("seed menu item %q: %v", label, err)
	}
	return item
}

func menuVisibilitySeed(t *testing.T, h *accessHarness, author core.User) core.Menu {
	t.Helper()
	menu := core.Menu{Name: "Ticket 18 visibility", Slug: fmt.Sprintf("ticket-18-%d", time.Now().UnixNano()), Location: "header"}
	if err := h.db.Create(&menu).Error; err != nil {
		t.Fatalf("seed header menu: %v", err)
	}
	menuVisibilityAddItem(t, h, menu.ID, nil, 0, "Menu Home", "home", "", 0, false)
	menuVisibilityAddItem(t, h, menu.ID, nil, 1, "Menu Posts", "custom", "/posts", 0, false)
	loginParent := menuVisibilityAddItem(t, h, menu.ID, nil, 2, "Login Parent Secret", "custom", "/members", 0, true)
	menuVisibilityAddItem(t, h, menu.ID, &loginParent.ID, 0, "Login Child Secret", "custom", "/members/child", 0, false)

	now := time.Now().UTC().Truncate(time.Second)
	posts := []core.Post{
		{Title: "private menu target", Slug: "menu-target-private", Content: "hidden", Type: "post", Status: "published", Visibility: "private", AuthorID: author.ID, PublishedAt: &now},
		{Title: "unknown visibility menu target", Slug: "menu-target-unknown", Content: "hidden", Type: "page", Status: "published", Visibility: "unlisted", AuthorID: author.ID, PublishedAt: &now},
		{Title: "future menu target", Slug: "menu-target-future", Content: "hidden", Type: "post", Status: "published", Visibility: "public", AuthorID: author.ID, PublishedAt: timePtr(now.Add(24 * time.Hour))},
		{Title: "draft menu target", Slug: "menu-target-draft", Content: "hidden", Type: "page", Status: "draft", Visibility: "public", AuthorID: author.ID, PublishedAt: &now},
		{Title: "deleted menu target", Slug: "menu-target-deleted", Content: "hidden", Type: "post", Status: "published", Visibility: "public", AuthorID: author.ID, PublishedAt: &now},
	}
	for i := range posts {
		if err := h.db.Create(&posts[i]).Error; err != nil {
			t.Fatalf("seed typed target %q: %v", posts[i].Slug, err)
		}
	}
	if err := h.db.Delete(&posts[4]).Error; err != nil {
		t.Fatalf("soft-delete typed target: %v", err)
	}
	for i, post := range posts {
		if i == 4 {
			menuVisibilityAddItem(t, h, menu.ID, nil, 3+i, "Hidden deleted target", "post", "", post.ID, false)
			continue
		}
		kind := "post"
		if post.Type == "page" {
			kind = "page"
		}
		menuVisibilityAddItem(t, h, menu.ID, nil, 3+i, "Hidden typed target "+post.Slug, kind, "", post.ID, false)
	}
	return menu
}

func timePtr(value time.Time) *time.Time { return &value }

func menuVisibilityGet(t *testing.T, h *accessHarness, method, path, token string) accessResult {
	t.Helper()
	r := h.request(method, path, "", token, "text/html", "")
	h.mustNoPanic(r, method+" "+path)
	if r.code != http.StatusOK {
		t.Fatalf("%s %s status=%d, want 200; body=%q", method, path, r.code, archivesSnippet(r.body))
	}
	return r
}

func menuVisibilityAssert(t *testing.T, body string, signedIn bool) {
	t.Helper()
	contains := func(label string) bool { return strings.Contains(body, label) }
	if contains("Login Parent Secret") != signedIn {
		t.Errorf("login-only parent visibility = %v, signed-in = %v", contains("Login Parent Secret"), signedIn)
	}
	if contains("Login Child Secret") != signedIn {
		t.Errorf("login-only child visibility = %v, signed-in = %v", contains("Login Child Secret"), signedIn)
	}
	for _, hidden := range []string{"Hidden typed target", "Hidden deleted target"} {
		if contains(hidden) {
			t.Errorf("non-public typed menu target appeared for signed-in=%v: %q", signedIn, hidden)
		}
	}
	for _, hiddenURL := range []string{
		"/post/menu-target-private",
		"/menu-target-unknown",
		"/post/menu-target-future",
		"/menu-target-draft",
		"/post/menu-target-deleted",
	} {
		if strings.Contains(body, hiddenURL) {
			t.Errorf("non-public typed target URL appeared for signed-in=%v: %q", signedIn, hiddenURL)
		}
	}
}

func TestMenuVisibilityIsRequestIsolatedAcrossAuthenticationOrderAndPath(t *testing.T) {
	h := menuVisibilityHarness(t)
	author := h.user("author", "menu-visibility-author")
	menuVisibilitySeed(t, h, author)
	user := h.user("author", "menu-visibility-reader")
	token := h.token(user)

	for _, sequence := range []struct {
		name   string
		tokens []string
	}{
		{name: "anonymous warm then signed in then anonymous", tokens: []string{"", token, ""}},
		{name: "signed in warm then anonymous then signed in", tokens: []string{token, "", token}},
	} {
		t.Run(sequence.name, func(t *testing.T) {
			for _, path := range []string{"/", "/posts"} {
				for i, requestToken := range sequence.tokens {
					r := menuVisibilityGet(t, h, http.MethodGet, path, requestToken)
					menuVisibilityAssert(t, r.body, requestToken != "")
					if requestToken != "" {
						cache := strings.ToLower(r.header.Get(echo.HeaderCacheControl))
						if !strings.Contains(cache, "private") || !strings.Contains(cache, "no-store") {
							t.Errorf("authenticated request %d at %s Cache-Control=%q, want private and no-store", i, path, cache)
						}
					}
					postsLink, ok := pubThemeFindAnchor(r.body, "/posts")
					if !ok {
						t.Errorf("header menu lacks /posts link at %s", path)
					} else if (path == "/posts") != strings.Contains(postsLink.attrs, `aria-current="page"`) {
						t.Errorf("/posts active state at %s has attrs %q", path, postsLink.attrs)
					}
				}
			}
		})
	}

	server := httptest.NewServer(h.echo)
	defer server.Close()
	request, err := http.NewRequest(http.MethodHead, server.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	request.Header.Set(echo.HeaderAccept, "text/html")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("real HTTP authenticated HEAD /: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read/close authenticated HEAD response: read=%v close=%v", readErr, closeErr)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("authenticated HEAD status=%d, want 200", response.StatusCode)
	}
	if len(body) != 0 {
		t.Errorf("authenticated HEAD body has %d bytes, want none", len(body))
	}
	cache := strings.ToLower(response.Header.Get(echo.HeaderCacheControl))
	if !strings.Contains(cache, "private") || !strings.Contains(cache, "no-store") {
		t.Errorf("authenticated HEAD Cache-Control=%q, want private and no-store", cache)
	}
}

func TestMenuVisibilityConcurrentVisitorsKeepAuthAndActivePathLocal(t *testing.T) {
	h := menuVisibilityHarness(t)
	author := h.user("author", "menu-visibility-concurrent-author")
	menuVisibilitySeed(t, h, author)
	readerA := h.user("author", "menu-visibility-concurrent-a")
	readerB := h.user("author", "menu-visibility-concurrent-b")
	tokens := []string{h.token(readerA), h.token(readerB)}
	type result struct {
		label string
		err   string
	}
	const requestCount = 8
	results := make(chan result, requestCount)
	for i := 0; i < requestCount; i++ {
		idx := i
		go func() {
			path := "/"
			token := ""
			if idx%2 == 1 {
				path = "/posts"
				token = tokens[(idx/2)%len(tokens)]
			}
			r := h.request(http.MethodGet, path, "", token, "text/html", "")
			if r.panicked != nil || r.code != http.StatusOK {
				results <- result{label: path, err: fmt.Sprintf("status=%d panic=%v body=%q", r.code, r.panicked, archivesSnippet(r.body))}
				return
			}
			signedIn := token != ""
			if strings.Contains(r.body, "Login Parent Secret") != signedIn || strings.Contains(r.body, "Login Child Secret") != signedIn {
				results <- result{label: path, err: fmt.Sprintf("login-only visibility mismatch for authenticated=%v", signedIn)}
				return
			}
			link, ok := pubThemeFindAnchor(r.body, "/posts")
			postsActive := ok && strings.Contains(link.attrs, `aria-current="page"`)
			if postsActive != (path == "/posts") {
				results <- result{label: path, err: fmt.Sprintf("/posts active state leaked across request; attrs=%q", link.attrs)}
				return
			}
			results <- result{label: path}
		}()
	}
	for i := 0; i < requestCount; i++ {
		got := <-results
		if got.err != "" {
			t.Errorf("concurrent request %s: %s", got.label, got.err)
		}
	}
}

func menuVisibilitySignedToken(t *testing.T, user core.User, secret []byte, exp time.Time) string {
	t.Helper()
	loginID := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	claims := jwt.MapClaims{"jti": loginID, "user_id": user.ID, "email": user.Email, "role": user.Role, "exp": exp.Unix(), "iat": time.Now().Add(-time.Hour).Unix()}
	encoded, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func menuVisibilityCookieRequest(h *accessHarness, path, token string) accessResult {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: "jwt_token", Value: token})
	rec := httptest.NewRecorder()
	var panicked any
	func() { defer func() { panicked = recover() }(); h.echo.ServeHTTP(rec, req) }()
	return accessResult{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone(), panicked: panicked}
}

func TestMenuVisibilityRejectsRevokedExpiredAndForgedLogins(t *testing.T) {
	h := menuVisibilityHarness(t)
	author := h.user("author", "menu-visibility-invalid-author")
	menuVisibilitySeed(t, h, author)
	user := h.user("author", "menu-visibility-invalid-user")
	valid := h.token(user)
	validPage := menuVisibilityGet(t, h, http.MethodGet, "/", valid)
	menuVisibilityAssert(t, validPage.body, true)
	if err := auth.RevokeUserLogins(h.db, user.ID, ""); err != nil {
		t.Fatalf("revoke reader logins: %v", err)
	}
	revoked := menuVisibilityGet(t, h, http.MethodGet, "/", valid)
	menuVisibilityAssert(t, revoked.body, false)

	expired := menuVisibilitySignedToken(t, user, []byte(menuVisibilityJWTSecret), time.Now().Add(-time.Minute))
	forged := menuVisibilitySignedToken(t, user, []byte("different-secret-not-the-app-secret"), time.Now().Add(time.Hour))
	for _, tc := range []struct{ name, token string }{{"expired", expired}, {"forged", forged}} {
		t.Run(tc.name, func(t *testing.T) {
			r := menuVisibilityGet(t, h, http.MethodGet, "/", tc.token)
			menuVisibilityAssert(t, r.body, false)
		})
	}
	cookieOnly := menuVisibilityCookieRequest(h, "/", forged)
	h.mustNoPanic(cookieOnly, "forged jwt_token cookie")
	if cookieOnly.code != http.StatusOK {
		t.Fatalf("forged cookie request status=%d, want 200", cookieOnly.code)
	}
	menuVisibilityAssert(t, cookieOnly.body, false)
}

func TestMenuVisibilityMaintenanceIsStandaloneAndNilDatabaseErrorFallbackWorks(t *testing.T) {
	h := menuVisibilityHarness(t)
	author := h.user("author", "menu-visibility-maintenance-author")
	menuVisibilitySeed(t, h, author)
	maintenanceEnable(t, h, 0)
	maintenance := h.request(http.MethodGet, "/", "", "", "text/html", "")
	h.mustNoPanic(maintenance, "maintenance GET /")
	if maintenance.code != http.StatusServiceUnavailable || !strings.Contains(maintenance.body, maintenanceTestMessage) {
		t.Fatalf("maintenance response status/body=%d/%q, want standalone 503 with message", maintenance.code, archivesSnippet(maintenance.body))
	}
	if strings.Contains(maintenance.body, `class="masthead"`) || strings.Contains(maintenance.body, "Login Parent Secret") {
		t.Errorf("maintenance page was nested in public layout or rendered a menu")
	}

	// Invoke the full route set's global error handler directly. Going through
	// the install middleware would return its plain 503 before reaching it.
	previous := data.DB
	data.DB = nil
	request := httptest.NewRequest(http.MethodGet, "/no-such-page-menu-fallback", nil)
	recorder := httptest.NewRecorder()
	context := h.echo.NewContext(request, recorder)
	var panicValue any
	func() {
		defer func() { panicValue = recover() }()
		h.echo.HTTPErrorHandler(echo.ErrNotFound, context)
	}()
	data.DB = previous
	if panicValue != nil {
		t.Fatalf("nil-DB public error handler panicked: %v", panicValue)
	}
	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), `data-code="404"`) {
		t.Errorf("nil-DB error handler status/body=%d/%q, want themed 404", recorder.Code, archivesSnippet(recorder.Body.String()))
	}
}
