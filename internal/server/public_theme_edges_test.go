package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

func TestPublicThemeEdgeSearchAPIShapeScopeAndSafeSummary(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "pubedge-search")
	visible := pubThemeSeedPost(t, h, author, pubThemePost{
		title:       "Edge Search Visible",
		slug:        "edge-search-visible",
		content:     "<p>Kabc</p><p>&lt;script&gt;needle&lt;/script&gt;</p>",
		publishedAt: archivesDay(2025, time.July, 1),
	})
	pubThemeSeedPost(t, h, author, pubThemePost{
		title: "Edge Search Private Secret", slug: "edge-search-private", visibility: "private",
		content: "<p>needle</p>", publishedAt: archivesDay(2025, time.July, 2),
	})
	pubThemeSeedPost(t, h, author, pubThemePost{
		title: "Edge Search Future Secret", slug: "edge-search-future",
		content: "<p>needle</p>", publishedAt: time.Now().UTC().AddDate(1, 0, 0),
	})
	pubThemeSeedPost(t, h, author, pubThemePost{
		title: "Edge Search Trash Secret", slug: "edge-search-trash", trashed: true,
		content: "<p>needle</p>", publishedAt: archivesDay(2025, time.July, 3),
	})

	api := h.request(http.MethodGet, "/api/search?q=needle", "", "", echo.MIMEApplicationJSON, "")
	if api.panicked != nil {
		t.Fatalf("GET /api/search panicked: %v", api.panicked)
	}
	if api.code != http.StatusOK {
		t.Fatalf("GET /api/search returned %d, want 200: %s", api.code, api.body)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal([]byte(api.body), &document); err != nil {
		t.Fatalf("decode /api/search response: %v; body=%q", err, api.body)
	}
	if len(document) != 3 {
		t.Fatalf("/api/search JSON keys = %v, want exactly query, posts, count", document)
	}
	var query string
	if err := json.Unmarshal(document["query"], &query); err != nil || query != "needle" {
		t.Errorf("/api/search query = %q (error %v), want needle", query, err)
	}
	var count int
	if err := json.Unmarshal(document["count"], &count); err != nil || count != 1 {
		t.Errorf("/api/search count = %d (error %v), want 1", count, err)
	}
	var posts []map[string]json.RawMessage
	if err := json.Unmarshal(document["posts"], &posts); err != nil {
		t.Fatalf("decode /api/search posts: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("/api/search returned %d posts, want only the public visible post: %s", len(posts), api.body)
	}
	wantFields := map[string]bool{"id": true, "title": true, "slug": true, "excerpt": true, "created_at": true}
	if len(posts[0]) != len(wantFields) {
		t.Errorf("/api/search post keys = %v, want exactly %v", posts[0], wantFields)
	}
	for key := range posts[0] {
		if !wantFields[key] {
			t.Errorf("/api/search post contains unexpected field %q", key)
		}
	}
	var gotTitle, gotSlug, excerpt string
	_ = json.Unmarshal(posts[0]["title"], &gotTitle)
	_ = json.Unmarshal(posts[0]["slug"], &gotSlug)
	_ = json.Unmarshal(posts[0]["excerpt"], &excerpt)
	if gotTitle != visible.Title || gotSlug != visible.Slug {
		t.Errorf("/api/search returned post %q (%q), want %q (%q)", gotTitle, gotSlug, visible.Title, visible.Slug)
	}
	if !strings.Contains(excerpt, "Kabc") {
		t.Errorf("/api/search excerpt lost its Unicode text: %q", excerpt)
	}
	if strings.Contains(api.body, "Edge Search Private Secret") ||
		strings.Contains(api.body, "Edge Search Future Secret") ||
		strings.Contains(api.body, "Edge Search Trash Secret") {
		t.Errorf("/api/search leaked a non-public post: %s", api.body)
	}

	path := "/search?q=needle"
	body := archivesPage(t, h, path)
	archivesWantText(t, path, body, "&lt;script&gt;<mark>needle</mark>&lt;/script&gt;")
	archivesWantAbsent(t, path, body, "<script>needle</script>")

	path = "/search?q=abc"
	body = archivesPage(t, h, path)
	archivesWantText(t, path, body, "K<mark>abc</mark>")

	pubThemeSeedPost(t, h, author, pubThemePost{
		title: "Edge Search Across Blocks", slug: "edge-search-across-blocks",
		content: "<p>alpha</p><p>beta</p>", publishedAt: archivesDay(2025, time.July, 4),
	})
	path = "/search?q=alpha+beta"
	body = archivesPage(t, h, path)
	archivesWantText(t, path, body, "<mark>alpha beta</mark>")
}

func TestPublicThemeEdgePublishedAtFallbackAndStablePrevNext(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "pubedge-dates")
	old := core.Post{
		Title: "Edge Nil Date Older", Slug: "edge-nil-date-older", Content: "<p>body</p>", Type: "post",
		Status: "published", Visibility: "public", AuthorID: author.ID,
		CreatedAt: archivesDay(2025, time.January, 10), UpdatedAt: archivesDay(2025, time.January, 10),
	}
	nilDate := core.Post{
		Title: "Edge Nil Date Newer", Slug: "edge-nil-date-newer", Content: "<p>body</p>", Type: "post",
		Status: "published", Visibility: "public", AuthorID: author.ID,
		CreatedAt: archivesDay(2025, time.March, 10), UpdatedAt: archivesDay(2025, time.March, 10),
	}
	publishedDate := archivesDay(2025, time.February, 10)
	middle := core.Post{
		Title: "Edge Nil Date Middle", Slug: "edge-nil-date-middle", Content: "<p>body</p>", Type: "post",
		Status: "published", Visibility: "public", AuthorID: author.ID,
		PublishedAt: &publishedDate, CreatedAt: publishedDate, UpdatedAt: publishedDate,
	}
	for _, post := range []*core.Post{&old, &nilDate, &middle} {
		if err := h.db.Create(post).Error; err != nil {
			t.Fatalf("seed %q: %v", post.Slug, err)
		}
	}

	path := "/posts"
	body := archivesPage(t, h, path)
	archivesWantOrder(t, path, body, nilDate.Title, middle.Title)
	archivesWantOrder(t, path, body, middle.Title, old.Title)

	sameDate := archivesDay(2025, time.April, 10)
	for _, post := range []core.Post{
		{Title: "Edge Equal Date First", Slug: "edge-equal-date-first"},
		{Title: "Edge Equal Date Middle", Slug: "edge-equal-date-middle"},
		{Title: "Edge Equal Date Last", Slug: "edge-equal-date-last"},
	} {
		post.Content = "<p>body</p>"
		post.Type = "post"
		post.Status = "published"
		post.Visibility = "public"
		post.AuthorID = author.ID
		post.PublishedAt = &sameDate
		post.CreatedAt = sameDate
		post.UpdatedAt = sameDate
		if err := h.db.Create(&post).Error; err != nil {
			t.Fatalf("seed %q: %v", post.Slug, err)
		}
	}
	body = archivesPage(t, h, "/post/edge-equal-date-middle")
	prev, hasPrev := pubThemeFindAnchor(body, "/post/edge-equal-date-first")
	next, hasNext := pubThemeFindAnchor(body, "/post/edge-equal-date-last")
	if !hasPrev || !strings.Contains(prev.text, "上一篇") ||
		!hasNext || !strings.Contains(next.text, "下一篇") {
		t.Errorf("equal-date neighbors are not stable by insertion/ID order: %s", archivesSnippet(body))
	}
}

func TestPublicThemeEdgeExtremePageDoesNotWrap(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "pubedge-page")
	pubThemeSeedPost(t, h, author, pubThemePost{
		title: "Edge Page Overflow Control", publishedAt: archivesDay(2025, time.August, 1),
	})

	path := "/posts?page=9223372036854775807"
	r := archivesGet(t, h, path)
	archivesWantStatus(t, path, r, http.StatusNotFound)
}

func TestPublicThemeEdgeMaintenancePathBoundariesAndDBFailure(t *testing.T) {
	h := archivesHarness(t)
	maintenanceEnable(t, h, 0)

	status := h.request(http.MethodGet, "/api/oauth/status", "", "", echo.MIMEApplicationJSON, "")
	if status.panicked != nil || status.code != http.StatusOK {
		t.Errorf("GET /api/oauth/status during maintenance: status=%d panic=%v body=%q, want readable 200", status.code, status.panicked, status.body)
	}
	font := archivesGet(t, h, "/admin/assets/fonts/bricolage-grotesque.woff2")
	if font.code != http.StatusOK || len(font.body) == 0 {
		t.Errorf("GET admin font during maintenance: status=%d bytes=%d, want readable 200 asset", font.code, len(font.body))
	}
	for _, path := range []string{"/themes/default/post.html", "/administer", "/api/administrator", "/unknown-public-edge-route"} {
		r := archivesGet(t, h, path)
		if r.code != http.StatusServiceUnavailable {
			t.Errorf("GET %s during maintenance returned %d, want 503; body=%q", path, r.code, archivesSnippet(r.body))
		}
		if path == "/themes/default/post.html" && strings.Contains(r.body, "{{") {
			t.Errorf("GET %s exposed unrendered theme source", path)
		}
	}

	const callbackName = "public_theme_edge_fail_option_query"
	err := h.db.Callback().Query().Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == "options" {
			tx.AddError(errors.New("injected maintenance option lookup failure"))
		}
	})
	if err != nil {
		t.Fatalf("register options-query failure callback: %v", err)
	}
	defer func() {
		if err := h.db.Callback().Query().Remove(callbackName); err != nil {
			t.Errorf("remove options-query failure callback: %v", err)
		}
	}()

	login := archivesGet(t, h, "/login")
	if login.code != http.StatusOK {
		t.Errorf("GET /login with option reads failing returned %d, want 200", login.code)
	}
	font = archivesGet(t, h, "/admin/assets/fonts/bricolage-grotesque.woff2")
	if font.code != http.StatusOK || len(font.body) == 0 {
		t.Errorf("GET admin font with option reads failing: status=%d bytes=%d, want readable 200 asset", font.code, len(font.body))
	}
	for _, path := range []string{"/administer", "/api/administrator", "/themes/default/post.html", "/unknown-public-edge-route"} {
		r := archivesGet(t, h, path)
		if r.code != http.StatusServiceUnavailable {
			t.Errorf("GET %s with option reads failing returned %d, want 503", path, r.code)
		}
	}
}

func TestPublicThemeEdgeMaintenanceAPIOwnsTimestampLifecycle(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "pubedge-maint-api")
	save := func(payload string) accessResult {
		return h.request(http.MethodPost, "/api/admin/settings", payload, h.token(admin),
			echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	}

	r := save(`{"maintenance_enabled":"true","maintenance_message":"edge maintenance"}`)
	if r.panicked != nil || r.code != http.StatusOK {
		t.Fatalf("enable maintenance through settings API: status=%d panic=%v body=%q", r.code, r.panicked, r.body)
	}
	startedValue, found := maintenanceOption(t, h, "maintenance_started_at")
	started, err := time.Parse(time.RFC3339, startedValue)
	if !found || err != nil {
		t.Fatalf("API did not create an automatic RFC3339 maintenance start time %q: found=%v err=%v", startedValue, found, err)
	}
	if delta := time.Since(started); delta < -5*time.Second || delta > 5*time.Second {
		t.Errorf("automatic start time %s is not near now", startedValue)
	}

	r = save(`{"maintenance_enabled":"true","maintenance_message":"reopened maintenance"}`)
	if r.code != http.StatusOK {
		t.Fatalf("reopen maintenance through settings API: status=%d body=%q", r.code, r.body)
	}
	if kept, ok := maintenanceOption(t, h, "maintenance_started_at"); !ok || kept != startedValue {
		t.Errorf("reopening maintenance changed start time to %q (found %v), want %q", kept, ok, startedValue)
	}

	tm := theme.NewManager("web/themes")
	if err := tm.Activate("default"); err != nil {
		t.Fatalf("activate default theme after route reconstruction: %v", err)
	}
	restarted := echo.New()
	RegisterRoutes(restarted, tm, nil)
	h.echo = restarted
	if r := archivesGet(t, h, "/"); r.code != http.StatusServiceUnavailable {
		t.Errorf("maintenance state was not preserved after route reconstruction: GET / returned %d, want 503", r.code)
	}

	r = save(`{"maintenance_enabled":"false","maintenance_message":""}`)
	if r.code != http.StatusOK {
		t.Fatalf("disable maintenance through settings API: status=%d body=%q", r.code, r.body)
	}
	if cleared, ok := maintenanceOption(t, h, "maintenance_started_at"); ok && cleared != "" {
		t.Errorf("disabled maintenance retained start time %q", cleared)
	}
}

func TestPublicThemeEdgeMaintenanceRejectsExpiredAndRevokedCookies(t *testing.T) {
	const sessionSecret = "public-theme-edge-session-secret-0123456789"
	const jwtSecret = "public-theme-edge-jwt-secret-0123456789"
	oldSession, hadSession := os.LookupEnv("SESSION_SECRET")
	oldJWT, hadJWT := os.LookupEnv("JWT_SECRET")
	if err := os.Setenv("SESSION_SECRET", sessionSecret); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("JWT_SECRET", jwtSecret); err != nil {
		t.Fatal(err)
	}
	h := archivesHarness(t)
	t.Cleanup(func() {
		if hadSession {
			_ = os.Setenv("SESSION_SECRET", oldSession)
		} else {
			_ = os.Unsetenv("SESSION_SECRET")
		}
		if hadJWT {
			_ = os.Setenv("JWT_SECRET", oldJWT)
		} else {
			_ = os.Unsetenv("JWT_SECRET")
		}
		if err := auth.Init(); err != nil {
			t.Errorf("restore auth secrets: %v", err)
		}
	})

	admin := h.user("admin", "pubedge-cookie-admin")
	maintenanceEnable(t, h, 0)
	token, err := auth.GenerateJWT(admin.ID, admin.Email, admin.Role)
	if err != nil {
		t.Fatalf("generate admin login cookie: %v", err)
	}
	claims, err := auth.ValidateJWT(token)
	if err != nil {
		t.Fatalf("decode admin login cookie: %v", err)
	}
	loginID, ok := claims["jti"].(string)
	if !ok {
		t.Fatalf("JWT jti has type %T, want string", claims["jti"])
	}

	requestWithCookie := func(method, path, value string) accessResult {
		req := httptest.NewRequest(method, path, nil)
		req.AddCookie(&http.Cookie{Name: "jwt_token", Value: value})
		req.Header.Set("X-Jelly-CSRF", "1")
		rec := httptest.NewRecorder()
		var panicked any
		func() {
			defer func() { panicked = recover() }()
			h.echo.ServeHTTP(rec, req)
		}()
		return accessResult{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone(), panicked: panicked}
	}

	if valid := requestWithCookie(http.MethodGet, "/", token); valid.code != http.StatusOK || valid.panicked != nil {
		t.Errorf("valid admin cookie during maintenance: status=%d panic=%v, want 200", valid.code, valid.panicked)
	}

	expired, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"jti": loginID, "user_id": admin.ID, "email": admin.Email, "role": admin.Role,
		"exp": time.Now().Add(-time.Minute).Unix(), "iat": time.Now().Add(-time.Hour).Unix(),
	}).SignedString([]byte(jwtSecret))
	if err != nil {
		t.Fatalf("sign expired admin cookie: %v", err)
	}
	if r := requestWithCookie(http.MethodGet, "/", expired); r.code != http.StatusServiceUnavailable || r.panicked != nil {
		t.Errorf("expired admin cookie bypassed maintenance: status=%d panic=%v, want 503", r.code, r.panicked)
	}

	logout := requestWithCookie(http.MethodPost, "/auth/logout", token)
	if logout.code != http.StatusOK || logout.panicked != nil {
		t.Fatalf("revoke admin cookie: status=%d panic=%v body=%q", logout.code, logout.panicked, logout.body)
	}
	if r := requestWithCookie(http.MethodGet, "/", token); r.code != http.StatusServiceUnavailable || r.panicked != nil {
		t.Errorf("revoked admin cookie bypassed maintenance: status=%d panic=%v, want 503", r.code, r.panicked)
	}
}

func TestPublicThemeEdgeErrorHandlerRendersWithoutDatabase(t *testing.T) {
	h := archivesHarness(t)
	previousDB := data.DB
	data.DB = nil
	defer func() { data.DB = previousDB }()

	for _, code := range []int{http.StatusNotFound, http.StatusServiceUnavailable} {
		req := httptest.NewRequest(http.MethodGet, "/edge-error", nil)
		rec := httptest.NewRecorder()
		c := h.echo.NewContext(req, rec)
		var panicked any
		func() {
			defer func() { panicked = recover() }()
			h.echo.HTTPErrorHandler(echo.NewHTTPError(code), c)
		}()
		if panicked != nil {
			t.Errorf("public HTTP error handler for %d panicked with data.DB=nil: %v", code, panicked)
			continue
		}
		if rec.Code != code {
			t.Errorf("public HTTP error handler wrote %d for error %d", rec.Code, code)
		}
		wantCode := map[int]string{http.StatusNotFound: "404", http.StatusServiceUnavailable: "503"}[code]
		if !strings.Contains(rec.Body.String(), `data-code="`+wantCode+`"`) {
			t.Errorf("public error %d did not render the theme fallback page: %q", code, archivesSnippet(rec.Body.String()))
		}
	}
}
