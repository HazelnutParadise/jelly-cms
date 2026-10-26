package server

// Ticket 08b (docs/tickets/08-system.md): URL settings, per-page counts, feed
// modes, home mode, reserved slugs and the slug strategy. The contract is the
// 「閱讀與網址」 bullet and the 選項鍵 list. These tests are written before the
// implementation: they fail with 501 or missing behaviour until 08b lands. Do not
// edit them to match an implementation.
//
// Admin APIs use newAccessHarness; public pages use archivesHarness (active
// default theme). Options are written through POST /api/admin/settings, the live
// write path. Posts are seeded straight into the isolated PostgreSQL database.
// Run with JELLY_TEST_POSTGRES=1, otherwise the harness skips.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
)

// s08bPost describes a post or page seeded for one 08b test. Empty fields take
// the defaults: a published public post dated 2025-09-30 12:00 UTC with body text.
type s08bPost struct {
	title       string
	slug        string
	typ         string
	status      string
	visibility  string
	content     string
	publishedAt time.Time
	categories  []core.Category
	tags        []core.Tag
}

// s08bSeedPost inserts one post or page directly into the isolated database.
func s08bSeedPost(t *testing.T, h *accessHarness, author core.User, spec s08bPost) core.Post {
	t.Helper()
	if spec.typ == "" {
		spec.typ = "post"
	}
	if spec.status == "" {
		spec.status = "published"
	}
	if spec.visibility == "" {
		spec.visibility = "public"
	}
	if spec.content == "" {
		spec.content = "seed body for " + spec.title
	}
	published := spec.publishedAt
	if published.IsZero() {
		published = archivesDay(2025, time.September, 30)
	}
	post := core.Post{
		Title:       spec.title,
		Slug:        spec.slug,
		Content:     spec.content,
		Type:        spec.typ,
		Status:      spec.status,
		AuthorID:    author.ID,
		Visibility:  spec.visibility,
		PublishedAt: &published,
		CreatedAt:   published,
		UpdatedAt:   published,
	}
	if err := h.db.Create(&post).Error; err != nil {
		t.Fatalf("seed %s %q: %v", spec.typ, spec.slug, err)
	}
	for i := range spec.categories {
		if err := h.db.Model(&post).Association("Categories").Append(&spec.categories[i]); err != nil {
			t.Fatalf("assign category %q to %q: %v", spec.categories[i].Slug, spec.slug, err)
		}
	}
	for i := range spec.tags {
		if err := h.db.Model(&post).Association("Tags").Append(&spec.tags[i]); err != nil {
			t.Fatalf("assign tag %q to %q: %v", spec.tags[i].Slug, spec.slug, err)
		}
	}
	return post
}

// s08bSnippet shortens a response body for failure messages.
func s08bSnippet(body string) string {
	if len(body) > 300 {
		return body[:300]
	}
	return body
}

// s08bJSON encodes v as JSON text for a request body or a JSON-valued option.
func s08bJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode %T: %v", v, err)
	}
	return string(b)
}

// s08bDecode decodes a JSON response body into generic values.
func s08bDecode(t *testing.T, body string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("response is not JSON: %v; body=%q", err, s08bSnippet(body))
	}
	return v
}

// s08bItems decodes a list that is either a bare JSON array or {"items": [...]}.
func s08bItems(t *testing.T, body string) []map[string]any {
	t.Helper()
	var arr []map[string]any
	if err := json.Unmarshal([]byte(body), &arr); err == nil {
		return arr
	}
	var wrapped struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &wrapped); err != nil {
		t.Fatalf("response is neither an array nor {items}: %v; body=%q", err, s08bSnippet(body))
	}
	return wrapped.Items
}

// s08bFindKey returns the first value stored under key anywhere in decoded JSON.
func s08bFindKey(v any, key string) (any, bool) {
	switch x := v.(type) {
	case map[string]any:
		if got, ok := x[key]; ok {
			return got, true
		}
		for _, child := range x {
			if got, ok := s08bFindKey(child, key); ok {
				return got, true
			}
		}
	case []any:
		for _, child := range x {
			if got, ok := s08bFindKey(child, key); ok {
				return got, true
			}
		}
	}
	return nil, false
}

// s08bNumber returns v as a float64 when it is a JSON number.
func s08bNumber(v any) (float64, bool) {
	n, ok := v.(float64)
	return n, ok
}

// s08bDo serves req through the harness router and recovers panics.
func s08bDo(h *accessHarness, req *http.Request) accessResult {
	rec := httptest.NewRecorder()
	var panicked any
	func() {
		defer func() { panicked = recover() }()
		h.echo.ServeHTTP(rec, req)
	}()
	return accessResult{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone(), panicked: panicked}
}

// s08bGet sends an anonymous GET with optional request headers, such as a
// crawler User-Agent or an Accept value.
func s08bGet(h *accessHarness, path string, headers map[string]string) accessResult {
	h.t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	r := s08bDo(h, req)
	h.mustNoPanic(r, "GET "+path)
	return r
}

// s08bInteractive sends a same-origin browser request authenticated by the
// jwt_token cookie. It carries no Authorization header, which is how the
// interactive-only endpoints (ticket 08 「互動限定」) tell browsers from API callers.
func s08bInteractive(h *accessHarness, method, path string, body io.Reader, jwt, contentType string) accessResult {
	h.t.Helper()
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("X-Jelly-CSRF", "1")
	req.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
	if contentType != "" {
		req.Header.Set(echo.HeaderContentType, contentType)
	}
	req.AddCookie(&http.Cookie{Name: "jwt_token", Value: jwt})
	r := s08bDo(h, req)
	h.mustNoPanic(r, method+" "+path)
	return r
}

// s08bSetOptions writes registered option keys through POST /api/admin/settings.
// JSON-valued keys take the JSON text as their value. A failed write stops the test.
func s08bSetOptions(t *testing.T, h *accessHarness, token string, kv map[string]string) {
	t.Helper()
	r := h.request(http.MethodPost, "/api/admin/settings", s08bJSON(t, kv), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(r, "POST /api/admin/settings")
	if r.code != http.StatusOK {
		t.Fatalf("set options %v: status %d, body=%q", kv, r.code, s08bSnippet(r.body))
	}
}

// s08bOptionValue reads one stored option row. An absent row yields "".
func s08bOptionValue(t *testing.T, h *accessHarness, key string) string {
	t.Helper()
	var value string
	if err := h.db.Raw(`SELECT value FROM options WHERE key = ?`, key).Scan(&value).Error; err != nil {
		t.Fatalf("read option %q: %v", key, err)
	}
	return value
}

// s08bCount runs a COUNT-style query against the isolated database.
func s08bCount(t *testing.T, h *accessHarness, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := h.db.Raw(query, args...).Scan(&n).Error; err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return n
}

// s08bWant requires one of the wanted status codes.
func s08bWant(t *testing.T, label string, r accessResult, want ...int) {
	t.Helper()
	for _, w := range want {
		if r.code == w {
			return
		}
	}
	t.Errorf("%s: status %d, want %v; body=%q", label, r.code, want, s08bSnippet(r.body))
}

// s08bWant2xx requires a 2xx status code.
func s08bWant2xx(t *testing.T, label string, r accessResult) {
	t.Helper()
	if r.code/100 != 2 {
		t.Errorf("%s: status %d, want 2xx; body=%q", label, r.code, s08bSnippet(r.body))
	}
}

// s08bTitlesShown counts how many of the numbered titles prefix-01 .. prefix-n appear
// in body. Each title may render more than once on a page, so this counts distinct titles.
func s08bTitlesShown(body, prefix string, n int) int {
	shown := 0
	for i := 1; i <= n; i++ {
		if strings.Contains(body, fmt.Sprintf("%s-%02d", prefix, i)) {
			shown++
		}
	}
	return shown
}

// s08bSlugOf returns the slug field of a created post response.
func s08bSlugOf(t *testing.T, body string) string {
	t.Helper()
	var created struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("decode created post: %v; body=%q", err, s08bSnippet(body))
	}
	return created.Slug
}

func TestPermalinkDefaultAndTokens(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "s08b-permalink-admin")
	token := h.token(admin)
	author := h.user("author", "s08b-permalink-author")
	shelf := archivesSeedCategory(t, h, "Permalink Shelf", "permalink-shelf", nil, false, false)
	post := s08bSeedPost(t, h, author, s08bPost{title: "Permalink Probe Post", slug: "permalink-probe",
		categories: []core.Category{shelf}})

	// Default pattern is /post/{slug}.
	archivesWantText(t, "/post/permalink-probe", archivesPage(t, h, "/post/permalink-probe"), "Permalink Probe Post")

	cases := []struct{ name, pattern, path string }{
		{"year month slug", "/{year}/{month}/{slug}", "/2025/09/permalink-probe"},
		{"year month day slug", "/{year}/{month}/{day}/{slug}", "/2025/09/30/permalink-probe"},
		{"category and id", "/{category}/{id}", fmt.Sprintf("/permalink-shelf/%d", post.ID)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s08bSetOptions(t, h, token, map[string]string{"permalink_post": tc.pattern})
			r := s08bGet(h, tc.path, nil)
			s08bWant(t, "GET "+tc.path, r, http.StatusOK)
			archivesWantText(t, tc.path, r.body, "Permalink Probe Post")
			// The old /post/ URL stops serving the post unless it is listed in permalink_legacy.
			old := s08bGet(h, "/post/permalink-probe", nil)
			if old.code == http.StatusOK {
				t.Errorf("GET /post/permalink-probe still serves the post after permalink_post became %q", tc.pattern)
			}
		})
	}
}

func TestPermalinkLegacyPatternsRedirect301(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "s08b-legacy-admin")
	token := h.token(admin)
	author := h.user("author", "s08b-legacy-author")
	s08bSeedPost(t, h, author, s08bPost{title: "Legacy Probe Post", slug: "legacy-probe"})
	s08bSetOptions(t, h, token, map[string]string{"permalink_post": "/{year}/{month}/{slug}"})

	five := []string{"/post/{slug}", "/blog/{slug}", "/news/{year}/{slug}", "/p/{id}", "/old/{category}/{slug}"}
	s08bSetOptions(t, h, token, map[string]string{"permalink_legacy": s08bJSON(t, five)})

	for _, from := range []string{"/post/legacy-probe", "/blog/legacy-probe"} {
		r := s08bGet(h, from, nil)
		s08bWant(t, "GET "+from, r, http.StatusMovedPermanently)
		if got := r.header.Get("Location"); got != "/2025/09/legacy-probe" {
			t.Errorf("GET %s Location = %q, want /2025/09/legacy-probe", from, got)
		}
	}

	six := append(append([]string(nil), five...), "/extra/{slug}")
	r := h.request(http.MethodPost, "/api/admin/settings", s08bJSON(t, map[string]string{"permalink_legacy": s08bJSON(t, six)}), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(r, "POST sixth legacy pattern")
	if r.code != http.StatusBadRequest {
		t.Errorf("sixth permalink_legacy pattern returned %d, want 400; body=%q", r.code, s08bSnippet(r.body))
	}
	s08bWant(t, "GET /blog/legacy-probe after the rejected sixth pattern", s08bGet(h, "/blog/legacy-probe", nil), http.StatusMovedPermanently)
}

func TestPrefixesForCategoryTagAuthorArchive(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "s08b-prefix-admin")
	token := h.token(admin)
	writer := h.user("author", "s08b-prefix-writer")
	topic := archivesSeedCategory(t, h, "Prefix Topic", "prefix-topic", nil, false, false)
	tag := archivesSeedTag(t, h, "Prefix Tag", "prefix-tag")
	s08bSeedPost(t, h, writer, s08bPost{title: "Prefix Probe Post", slug: "prefix-probe",
		publishedAt: archivesDay(2025, time.September, 15), categories: []core.Category{topic}, tags: []core.Tag{tag}})

	s08bSetOptions(t, h, token, map[string]string{
		"permalink_category_prefix": "/topics",
		"permalink_tag_prefix":      "/tags",
		"permalink_author_prefix":   "/writers",
		"permalink_archive_prefix":  "/timeline",
	})

	cases := []struct{ name, current, old, location string }{
		{"category", "/topics/prefix-topic", "/c/prefix-topic", "/topics/prefix-topic"},
		{"tag", "/tags/prefix-tag", "/t/prefix-tag", "/tags/prefix-tag"},
		{"author", "/writers/s08b-prefix-writer", "/author/s08b-prefix-writer", "/writers/s08b-prefix-writer"},
		{"archive month", "/timeline/2025/09", "/archive/2025/09", "/timeline/2025/09"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			current := s08bGet(h, tc.current, nil)
			s08bWant(t, "GET "+tc.current, current, http.StatusOK)
			archivesWantText(t, tc.current, current.body, "Prefix Probe Post")
			old := s08bGet(h, tc.old, nil)
			s08bWant(t, "GET "+tc.old, old, http.StatusMovedPermanently)
			if got := old.header.Get("Location"); got != tc.location {
				t.Errorf("GET %s Location = %q, want %q", tc.old, got, tc.location)
			}
		})
	}
}

func TestReservedSlugsCannotBeUsedByPages(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "s08b-reserved-admin")
	token := h.token(admin)

	reserved := []string{"robots.txt", "sitemap.xml", "feed.xml", "llms.txt", "healthz", "shop", "cart", "checkout", "orders", "account"}
	for _, slug := range reserved {
		body := s08bJSON(t, map[string]any{"title": "Reserved " + slug, "slug": slug, "type": "page", "status": "published", "content": "reserved probe"})
		r := h.request(http.MethodPost, "/api/admin/posts", body, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
		h.mustNoPanic(r, "POST /api/admin/posts reserved "+slug)
		if r.code != http.StatusBadRequest {
			t.Errorf("page with reserved slug %q returned %d, want 400; body=%q", slug, r.code, s08bSnippet(r.body))
		}
	}

	// Control: an ordinary slug still creates a page.
	control := h.request(http.MethodPost, "/api/admin/posts", s08bJSON(t, map[string]any{"title": "Reserved Control", "slug": "reserved-control-page", "type": "page", "status": "published", "content": "ok"}), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(control, "POST control page")
	s08bWant(t, "POST control page with an ordinary slug", control, http.StatusOK)
}

func TestHomeModeLatestOrPage(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "s08b-home-admin")
	token := h.token(admin)
	author := h.user("author", "s08b-home-author")
	s08bSeedPost(t, h, author, s08bPost{title: "Home Latest Probe", slug: "home-latest-probe"})
	landing := s08bSeedPost(t, h, author, s08bPost{title: "Home Landing Probe", slug: "home-landing-probe", typ: "page"})

	// Default mode lists the latest post.
	archivesWantText(t, "/", archivesPage(t, h, "/"), "Home Latest Probe")

	s08bSetOptions(t, h, token, map[string]string{"home_show": "page", "home_page_id": fmt.Sprint(landing.ID)})
	archivesWantText(t, "/", archivesPage(t, h, "/"), "Home Landing Probe")
}

func TestPerPageOptions(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "s08b-perpage-admin")
	token := h.token(admin)
	author := h.user("author", "s08b-perpage-author")

	for i := 1; i <= 7; i++ {
		s08bSeedPost(t, h, author, s08bPost{title: fmt.Sprintf("Homeprobe-%02d", i), slug: fmt.Sprintf("homeprobe-%02d", i),
			publishedAt: archivesDay(2025, time.September, i)})
	}
	s08bSetOptions(t, h, token, map[string]string{"per_page_home": "5"})
	if n := s08bTitlesShown(archivesPage(t, h, "/"), "Homeprobe", 7); n != 5 {
		t.Errorf("home shows %d of the seven probe posts with per_page_home=5, want 5", n)
	}

	tax := archivesSeedCategory(t, h, "Tax Shelf", "tax-shelf", nil, false, false)
	for i := 1; i <= 7; i++ {
		s08bSeedPost(t, h, author, s08bPost{title: fmt.Sprintf("Taxprobe-%02d", i), slug: fmt.Sprintf("taxprobe-%02d", i),
			publishedAt: archivesDay(2025, time.September, i), categories: []core.Category{tax}})
	}
	s08bSetOptions(t, h, token, map[string]string{"per_page_taxonomy": "3"})

	page1 := archivesPage(t, h, "/c/tax-shelf")
	if n := s08bTitlesShown(page1, "Taxprobe", 7); n != 3 {
		t.Errorf("/c/tax-shelf shows %d of the seven probe posts with per_page_taxonomy=3, want 3", n)
	}
	archivesWantText(t, "/c/tax-shelf", page1, "Taxprobe-07", "Taxprobe-06", "Taxprobe-05")
	archivesWantAbsent(t, "/c/tax-shelf", page1, "Taxprobe-04")

	page2 := archivesPage(t, h, "/c/tax-shelf?page=2")
	archivesWantText(t, "/c/tax-shelf?page=2", page2, "Taxprobe-04", "Taxprobe-03", "Taxprobe-02")
	archivesWantAbsent(t, "/c/tax-shelf?page=2", page2, "Taxprobe-07")
}

func TestFeedModesAndItems(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "s08b-feed-admin")
	token := h.token(admin)
	author := h.user("author", "s08b-feed-author")
	for i := 1; i <= 3; i++ {
		content := "Lead sentence for the feed entry. " + strings.Repeat("filler words ", 30) + fmt.Sprintf("TAILMARK-FEED-%d", i)
		s08bSeedPost(t, h, author, s08bPost{title: fmt.Sprintf("Feedprobe Entry %d", i), slug: fmt.Sprintf("feedprobe-%d", i),
			content: content, publishedAt: archivesDay(2025, time.September, 10+i)})
	}

	s08bSetOptions(t, h, token, map[string]string{"feed_mode": "full", "feed_items": "2"})
	full := s08bGet(h, "/feed.xml", nil)
	s08bWant(t, "GET /feed.xml (full)", full, http.StatusOK)
	if ct := full.header.Get("Content-Type"); !strings.HasPrefix(ct, "application/rss+xml") {
		t.Errorf("GET /feed.xml Content-Type = %q, want application/rss+xml", ct)
	}
	if n := strings.Count(full.body, "<item>"); n != 2 {
		t.Errorf("feed_items=2 produced %d <item> elements, want 2", n)
	}
	if !strings.Contains(full.body, "TAILMARK-FEED-3") {
		t.Errorf("full feed does not include the body text of the newest entry")
	}

	s08bSetOptions(t, h, token, map[string]string{"feed_mode": "summary"})
	summary := s08bGet(h, "/feed.xml", nil)
	s08bWant(t, "GET /feed.xml (summary)", summary, http.StatusOK)
	if strings.Contains(summary.body, "TAILMARK-FEED-") {
		t.Errorf("summary feed includes text beyond the excerpt length")
	}

	s08bSetOptions(t, h, token, map[string]string{"feed_mode": "off"})
	s08bWant(t, "GET /feed.xml (off)", s08bGet(h, "/feed.xml", nil), http.StatusNotFound)
}

func TestSlugStrategy(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "s08b-slug-admin")
	token := h.token(admin)

	// Baseline: the title strategy keeps the title-based slug.
	base := h.request(http.MethodPost, "/api/admin/posts", s08bJSON(t, map[string]any{"title": "Slug Title Probe", "content": "x", "status": "published", "type": "post"}), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(base, "POST post (title strategy)")
	s08bWant(t, "POST post (title strategy)", base, http.StatusOK)
	if slug := s08bSlugOf(t, base.body); slug != "slug-title-probe" {
		t.Errorf("title strategy slug = %q, want slug-title-probe", slug)
	}

	s08bSetOptions(t, h, token, map[string]string{"slug_strategy": "timestamp"})
	numeric := regexp.MustCompile(`^[0-9]+$`)
	seen := map[string]bool{}
	for _, title := range []string{"Timestamp Probe Post", "Timestamp Probe Second"} {
		r := h.request(http.MethodPost, "/api/admin/posts", s08bJSON(t, map[string]any{"title": title, "content": "x", "status": "published", "type": "post"}), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
		h.mustNoPanic(r, "POST post (timestamp strategy)")
		s08bWant(t, "POST post (timestamp strategy) "+title, r, http.StatusOK)
		slug := s08bSlugOf(t, r.body)
		if !numeric.MatchString(slug) {
			t.Errorf("timestamp strategy gave slug %q for %q, want a numeric slug", slug, title)
		}
		if seen[slug] {
			t.Errorf("timestamp strategy reused slug %q", slug)
		}
		seen[slug] = true
	}
}
