package server

// Integration tests for ticket 10 (docs/tickets/10-theme-followups.md), the follow-ups of issue #28 on the
// public theme of ticket 05. Slots: 10-A search (items 1, 2), 10-B 404 text (item 3), 10-C post navigation
// (item 4), 10-D maintenance path (item 8), 10-E fonts and HEAD (items 10, 13), 10-F list/page markup
// (items 5, 12). Run with JELLY_TEST_POSTGRES=1; the harness skips otherwise.
//
// These tests are expected to fail now, except the ones marked "guard", which pin behavior that already
// works and must not regress. Do not edit them to match an implementation. Fixtures are seeded directly
// into the isolated database; the behavior under test always goes through HTTP. Pure visual conditions
// (gaps, widths, contrast, mobile hero height) are browser acceptance conditions in the ticket, not here.

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
	nethtml "golang.org/x/net/html"
	"gorm.io/gorm"
)

// ---------- helpers ----------

func followupParse(t *testing.T, body string) *nethtml.Node {
	t.Helper()
	doc, err := nethtml.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	return doc
}

func followupWalk(n *nethtml.Node, visit func(*nethtml.Node)) {
	visit(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		followupWalk(c, visit)
	}
}

func followupFind(n *nethtml.Node, match func(*nethtml.Node) bool) []*nethtml.Node {
	var out []*nethtml.Node
	followupWalk(n, func(x *nethtml.Node) {
		if x.Type == nethtml.ElementNode && match(x) {
			out = append(out, x)
		}
	})
	return out
}

func followupAttr(n *nethtml.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func followupHasClass(n *nethtml.Node, class string) bool {
	for _, token := range strings.Fields(followupAttr(n, "class")) {
		if token == class {
			return true
		}
	}
	return false
}

func followupByClass(n *nethtml.Node, class string) []*nethtml.Node {
	return followupFind(n, func(x *nethtml.Node) bool { return followupHasClass(x, class) })
}

func followupText(n *nethtml.Node) string {
	var b strings.Builder
	followupWalk(n, func(x *nethtml.Node) {
		if x.Type == nethtml.TextNode {
			b.WriteString(x.Data)
		}
	})
	return strings.TrimSpace(b.String())
}

func followupHasAncestor(n *nethtml.Node, ancestor *nethtml.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p == ancestor {
			return true
		}
	}
	return false
}

// followupLocale reads one value of web/locales/<lang>.json.
func followupLocale(t *testing.T, lang, key string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "web", "locales", lang+".json"))
	if err != nil {
		t.Fatalf("read locale %s: %v", lang, err)
	}
	var values map[string]string
	if err := json.Unmarshal(raw, &values); err != nil {
		t.Fatalf("decode locale %s: %v", lang, err)
	}
	return values[key]
}

var followupFoundPattern = regexp.MustCompile(`找到\s*(\d+)\s*篇文章`)

// followupFoundCount returns N of "找到 N 篇文章", or -1 when the sentence is absent.
func followupFoundCount(body string) int {
	m := followupFoundPattern.FindStringSubmatch(body)
	if m == nil {
		return -1
	}
	var n int
	fmt.Sscanf(m[1], "%d", &n)
	return n
}

func followupSeed(t *testing.T, h *accessHarness, author core.User, title, slug, content string, day int) core.Post {
	t.Helper()
	return pubThemeSeedPost(t, h, author, pubThemePost{title: title, slug: slug, content: content,
		publishedAt: archivesDay(2025, time.June, day)})
}

func followupHTMLGet(h *accessHarness, path string) accessResult {
	return h.request(http.MethodGet, path, "", "", echo.MIMETextHTML, "")
}

func followupJSONGet(h *accessHarness, path string) accessResult {
	return h.request(http.MethodGet, path, "", "", echo.MIMEApplicationJSON, "")
}

// ---------- item 2: trim ----------

// Item 2 (page): the query is trimmed before it is used, counted and echoed.
func TestFollowupSearchTrimsQueryOnPage(t *testing.T) {
	gd := url.QueryEscape("果凍")
	h := archivesHarness(t)
	author := h.user("author", "fu-trim")
	for i, title := range []string{"Trim Hit One", "Trim Hit Two", "Trim Hit Three"} {
		followupSeed(t, h, author, title, fmt.Sprintf("fu-trim-%d", i), "<p>果凍 在這裡</p>", i+1)
	}
	followupSeed(t, h, author, "Trim Miss Entry", "fu-trim-miss", "<p>no match here</p>", 9)

	for _, raw := range []string{gd, gd + "%20", "%20" + gd + "%20", "%09" + gd + "%0A", "%E3%80%80" + gd + "%E3%80%80"} {
		path := "/search?q=" + raw
		body := archivesPage(t, h, path)
		if got := followupFoundCount(body); got != 3 {
			t.Errorf("GET %s: 找到 %d 篇文章, want 3", path, got)
		}
		doc := followupParse(t, body)
		inputs := followupFind(doc, func(n *nethtml.Node) bool { return n.Data == "input" && followupAttr(n, "name") == "q" })
		var withValue []string
		for _, input := range inputs {
			if v := followupAttr(input, "value"); v != "" {
				withValue = append(withValue, v)
			}
		}
		if len(withValue) == 0 || withValue[len(withValue)-1] != "果凍" {
			t.Errorf("GET %s: search input values = %q, want the trimmed query 果凍", path, withValue)
		}
		for _, h1 := range followupFind(doc, func(n *nethtml.Node) bool { return n.Data == "h1" }) {
			if text := followupText(h1); strings.Contains(text, "果凍") && text != "「果凍」" {
				t.Errorf("GET %s: heading = %q, want 「果凍」", path, text)
			}
		}
	}
}

// Item 2 (page): a query of only whitespace is the empty state, not "list everything".
func TestFollowupSearchWhitespaceOnlyShowsEmptyState(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "fu-blank")
	followupSeed(t, h, author, "Blank Visible Entry", "fu-blank-entry", "<p>anything</p>", 1)

	for _, raw := range []string{"%20", "%20%20%09", "%E3%80%80", "+"} {
		path := "/search?q=" + raw
		body := archivesPage(t, h, path)
		if strings.Contains(body, "Blank Visible Entry") {
			t.Errorf("GET %s listed a post for a whitespace-only query", path)
		}
		if got := followupFoundCount(body); got != -1 {
			t.Errorf("GET %s shows the result count (%d), want the empty state without a count", path, got)
		}
		if strings.Contains(body, "沒有找到「") {
			t.Errorf("GET %s shows the no-result card; whitespace-only equals an empty query", path)
		}
	}
}

// Item 2 (API): trimmed, and whitespace-only behaves exactly like the empty string whatever that is.
func TestFollowupSearchAPITrimsQuery(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "fu-api-trim")
	followupSeed(t, h, author, "Api Trim Hit", "fu-api-trim-hit", "<p>果凍 在這裡</p>", 1)
	followupSeed(t, h, author, "Api Trim Miss", "fu-api-trim-miss", "<p>nothing</p>", 2)

	gd := url.QueryEscape("果凍")
	for _, raw := range []string{gd + "%20", "%20" + gd, "%09" + gd + "%0A"} {
		path := "/api/search?q=" + raw
		r := followupJSONGet(h, path)
		if r.code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200; body=%q", path, r.code, archivesSnippet(r.body))
			continue
		}
		var doc struct {
			Query string           `json:"query"`
			Posts []map[string]any `json:"posts"`
			Count int              `json:"count"`
		}
		if err := json.Unmarshal([]byte(r.body), &doc); err != nil {
			t.Errorf("GET %s: not JSON: %v", path, err)
			continue
		}
		if doc.Query != "果凍" || doc.Count != 1 || len(doc.Posts) != 1 {
			t.Errorf("GET %s: query=%q count=%d posts=%d, want trimmed 果凍 with exactly one hit", path, doc.Query, doc.Count, len(doc.Posts))
		}
	}

	empty := followupJSONGet(h, "/api/search?q=")
	for _, raw := range []string{"%20", "%20%20%09", "%E3%80%80"} {
		path := "/api/search?q=" + raw
		r := followupJSONGet(h, path)
		if r.code != empty.code || r.body != empty.body {
			t.Errorf("GET %s = %d %q, want the same response as q= (%d %q)", path, r.code, r.body, empty.code, empty.body)
		}
		if strings.Contains(r.body, "Api Trim") {
			t.Errorf("GET %s leaked posts for a whitespace-only query", path)
		}
	}
}

// ---------- item 1: length limit and cost ----------

func followupRunes(r string, n int) string { return strings.Repeat(r, n) }

// Item 1 (page): at most 100 runes after trimming; 101 is a 400 that still renders the search page.
func TestFollowupSearchQueryLengthLimitOnPage(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "fu-limit")
	followupSeed(t, h, author, "Limit Visible Entry", "fu-limit-entry", "<p>something</p>", 1)

	for name, q := range map[string]string{
		"100 CJK runes (300 bytes)":    followupRunes("果", 100),
		"100 ASCII":                    followupRunes("a", 100),
		"100 runes plus blanks":        "  " + followupRunes("果", 100) + "  ",
		"100 four-byte runes":          followupRunes("𠮷", 100),
		"100 runes, trailing newlines": followupRunes("b", 100) + "\n\n",
	} {
		path := "/search?q=" + url.QueryEscape(q)
		if r := archivesGet(t, h, path); r.code != http.StatusOK {
			t.Errorf("%s: GET /search status = %d, want 200; body=%q", name, r.code, archivesSnippet(r.body))
		}
	}

	tooLong := map[string]string{
		"101 CJK runes":        followupRunes("果", 101),
		"101 ASCII":            followupRunes("a", 101),
		"101 four-byte runes":  followupRunes("𠮷", 101),
		"100 KB":               followupRunes("x", 100*1024),
		"101 runes after trim": "  " + followupRunes("c", 101) + "  ",
	}
	for name, q := range tooLong {
		path := "/search?q=" + url.QueryEscape(q)
		start := time.Now()
		r := archivesGet(t, h, path)
		elapsed := time.Since(start)
		if r.code != http.StatusBadRequest {
			t.Errorf("%s: GET /search status = %d, want 400", name, r.code)
			continue
		}
		if elapsed > time.Second {
			t.Errorf("%s: rejecting took %v, want well under 1s", name, elapsed)
		}
		if want := followupLocale(t, "zh-TW", "searchQueryTooLong"); want == "" || !strings.Contains(r.body, want) {
			t.Errorf("%s: 400 body does not contain the notice %q", name, want)
		}
		if !strings.Contains(r.body, `id="search-query"`) {
			t.Errorf("%s: the 400 page is not the search page (no search form input)", name)
		}
		if strings.Contains(r.body, strings.TrimSpace(q)) {
			t.Errorf("%s: the 400 page echoes the over-long query", name)
		}
		if strings.Contains(r.body, "Limit Visible Entry") {
			t.Errorf("%s: the 400 page lists posts", name)
		}
	}

	en := archivesGet(t, h, "/search?lang=en&q="+followupRunes("a", 101))
	if want := followupLocale(t, "en", "searchQueryTooLong"); en.code != http.StatusBadRequest || want == "" || !strings.Contains(en.body, want) {
		t.Errorf("English 400: status=%d, want 400 with the notice %q", en.code, want)
	}
}

// Item 1 (API): 400 with the error code query_too_long, same limit, same trimming.
func TestFollowupSearchQueryLengthLimitOnAPI(t *testing.T) {
	h := archivesHarness(t)

	for _, q := range []string{followupRunes("果", 100), followupRunes("a", 100) + " ", " " + followupRunes("𠮷", 100)} {
		path := "/api/search?q=" + url.QueryEscape(q)
		if r := followupJSONGet(h, path); r.code != http.StatusOK {
			t.Errorf("GET /api/search with a 100-rune query: status = %d, want 200; body=%q", r.code, archivesSnippet(r.body))
		}
	}
	for name, q := range map[string]string{
		"101 CJK runes": followupRunes("果", 101),
		"101 ASCII":     followupRunes("a", 101),
		"100 KB":        followupRunes("x", 100*1024),
	} {
		path := "/api/search?q=" + url.QueryEscape(q)
		start := time.Now()
		r := followupJSONGet(h, path)
		elapsed := time.Since(start)
		if r.code != http.StatusBadRequest {
			t.Errorf("%s: GET /api/search status = %d, want 400", name, r.code)
			continue
		}
		if elapsed > time.Second {
			t.Errorf("%s: rejecting took %v, want well under 1s", name, elapsed)
		}
		if ct := r.header.Get(echo.HeaderContentType); !strings.HasPrefix(ct, echo.MIMEApplicationJSON) {
			t.Errorf("%s: Content-Type = %q, want application/json", name, ct)
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(r.body), &doc); err != nil {
			t.Errorf("%s: 400 body is not JSON: %v", name, err)
			continue
		}
		if doc["code"] != "query_too_long" {
			t.Errorf("%s: JSON code = %v, want \"query_too_long\"; body=%q", name, doc["code"], archivesSnippet(r.body))
		}
		if msg, ok := doc["error"].(string); !ok || msg == "" {
			t.Errorf("%s: JSON error = %v, want a non-empty message string", name, doc["error"])
		}
	}
}

// Item 1 (cost): 2000 public posts of about 4 KB. A 100-rune query answers within 2 seconds and the search
// does not read every post into Go: the SQL narrows the candidates first.
func TestFollowupSearchLargeCorpus(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "fu-corpus")

	needle := "zq" + followupRunes("k", 98) // 100 runes
	filler := "<p>" + strings.Repeat("lorem ipsum dolor sit amet consectetur ", 100) + "</p>"
	posts := make([]core.Post, 0, 2000)
	base := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 2000; i++ {
		content := filler
		if i%700 == 5 { // posts 5, 705, 1405
			content = filler + "<p>" + needle + "</p>"
		}
		published := base.Add(time.Duration(i) * time.Hour)
		posts = append(posts, core.Post{
			Title: fmt.Sprintf("Corpus Entry %04d", i), Slug: fmt.Sprintf("corpus-%04d", i), Content: content,
			Type: "post", Status: "published", Visibility: "public", AuthorID: author.ID,
			PublishedAt: &published, CreatedAt: published, UpdatedAt: published,
		})
	}
	if err := h.db.CreateInBatches(&posts, 100).Error; err != nil {
		t.Fatalf("seed 2000 posts: %v", err)
	}

	var loaded int64
	const callback = "followup_count_posts_loaded"
	if err := h.db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == "posts" && tx.Error == nil {
			loaded += tx.RowsAffected
		}
	}); err != nil {
		t.Fatalf("register row counter: %v", err)
	}
	defer func() { _ = h.db.Callback().Query().Remove(callback) }()

	start := time.Now()
	api := followupJSONGet(h, "/api/search?q="+needle)
	apiElapsed := time.Since(start)
	if api.code != http.StatusOK {
		t.Fatalf("GET /api/search status = %d, want 200; body=%q", api.code, archivesSnippet(api.body))
	}
	var doc struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(api.body), &doc); err != nil || doc.Count != 3 {
		t.Errorf("GET /api/search count = %d (err %v), want 3", doc.Count, err)
	}
	if apiElapsed > 2*time.Second {
		t.Errorf("GET /api/search over 2000 posts took %v, want at most 2s", apiElapsed)
	}
	if loaded > 200 {
		t.Errorf("the API search read %d post rows into Go; narrow the candidates in SQL (limit 200 rows)", loaded)
	}

	loaded = 0
	start = time.Now()
	page := followupHTMLGet(h, "/search?q="+needle)
	pageElapsed := time.Since(start)
	if page.code != http.StatusOK || followupFoundCount(page.body) != 3 {
		t.Errorf("GET /search: status=%d count=%d, want 200 and 3", page.code, followupFoundCount(page.body))
	}
	if pageElapsed > 2*time.Second {
		t.Errorf("GET /search over 2000 posts took %v, want at most 2s", pageElapsed)
	}
	if loaded > 200 {
		t.Errorf("the page search read %d post rows into Go; narrow the candidates in SQL (limit 200 rows)", loaded)
	}
}

// Guard: matching stays on visible text and case-insensitive after the rewrite.
func TestFollowupSearchStillMatchesVisibleTextCaseInsensitively(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "fu-guard")
	followupSeed(t, h, author, "Guard Entity Post", "fu-guard-entity", "<p>Tom &amp; Jerry meet</p>", 1)
	followupSeed(t, h, author, "Guard Case Post", "fu-guard-case", "<p>MiXeD CaSe Needle</p>", 2)
	followupSeed(t, h, author, "Guard Attr Post", "fu-guard-attr", `<p><a href="/hidden-attrword">visible text</a></p>`, 3)

	path := "/search?q=" + url.QueryEscape("Tom & Jerry")
	archivesWantText(t, path, archivesPage(t, h, path), "Guard Entity Post")
	path = "/search?q=mixed+case+needle"
	archivesWantText(t, path, archivesPage(t, h, path), "Guard Case Post")
	path = "/search?q=hidden-attrword"
	if body := archivesPage(t, h, path); strings.Contains(body, "Guard Attr Post") {
		t.Errorf("GET %s matched text inside a tag attribute", path)
	}
}

// ---------- item 3: 404 text ----------

func TestFollowup404BodyIsLocalized(t *testing.T) {
	h := archivesHarness(t)
	want := followupNotFoundBodyZH // the paragraph of the NotFound board; served from the i18n key errorNotFoundBody

	for _, path := range []string{"/no-such-page", "/post/no-such-post", "/c/no-such-category"} {
		r := archivesGet(t, h, path)
		if r.code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", path, r.code)
			continue
		}
		doc := followupParse(t, r.body)
		messages := followupByClass(doc, "error-message")
		if len(messages) != 1 || followupText(messages[0]) != want {
			var got []string
			for _, m := range messages {
				got = append(got, followupText(m))
			}
			t.Errorf("GET %s: .error-message = %q, want exactly %q", path, got, want)
		}
		if strings.Contains(r.body, "Not Found") {
			t.Errorf("GET %s: the zh-TW 404 page contains the English status text \"Not Found\"", path)
		}
	}

	en := archivesGet(t, h, "/no-such-page?lang=en")
	wantEN := followupLocale(t, "en", "errorNotFoundBody")
	if en.code != http.StatusNotFound || wantEN == "" || !strings.Contains(en.body, wantEN) {
		t.Errorf("English 404: status=%d, want 404 containing %q", en.code, wantEN)
	}

	api := followupJSONGet(h, "/api/no-such-endpoint")
	if api.code != http.StatusNotFound || !strings.Contains(api.body, `"error":"Not Found"`) {
		t.Errorf("/api/* 404 must stay JSON with the status text: status=%d body=%q", api.code, api.body)
	}
}

// ---------- item 4: post navigation ----------

func followupPostNav(t *testing.T, h *accessHarness, slug string) map[string][]string {
	t.Helper()
	path := "/post/" + slug
	doc := followupParse(t, archivesPage(t, h, path))
	navs := followupByClass(doc, "post-nav")
	if len(navs) != 1 {
		t.Fatalf("GET %s: %d .post-nav elements, want 1", path, len(navs))
	}
	out := map[string][]string{}
	for _, a := range followupFind(navs[0], func(n *nethtml.Node) bool { return n.Data == "a" }) {
		out[followupAttr(a, "href")] = strings.Fields(followupAttr(a, "class"))
	}
	return out
}

func followupWantClasses(t *testing.T, where, href string, got []string, want, forbidden string) {
	t.Helper()
	has := map[string]bool{}
	for _, c := range got {
		has[c] = true
	}
	if !has[want] {
		t.Errorf("%s: link %s has classes %q, want %s", where, href, got, want)
	}
	if has[forbidden] {
		t.Errorf("%s: link %s has classes %q, must not have %s", where, href, got, forbidden)
	}
}

func TestFollowupPostNavLinksCarryTheirDirection(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "fu-nav")
	followupSeed(t, h, author, "Nav Oldest Entry", "fu-nav-old", "<p>a</p>", 1)
	followupSeed(t, h, author, "Nav Middle Entry", "fu-nav-mid", "<p>b</p>", 2)
	followupSeed(t, h, author, "Nav Newest Entry", "fu-nav-new", "<p>c</p>", 3)

	// middle: both
	both := followupPostNav(t, h, "fu-nav-mid")
	if len(both) != 2 {
		t.Fatalf("middle post nav has %d links, want 2: %v", len(both), both)
	}
	followupWantClasses(t, "middle", "/post/fu-nav-old", both["/post/fu-nav-old"], "post-nav-prev", "post-nav-next")
	followupWantClasses(t, "middle", "/post/fu-nav-new", both["/post/fu-nav-new"], "post-nav-next", "post-nav-prev")

	// newest: only the previous (older) post; it must not look like a "next" link
	newest := followupPostNav(t, h, "fu-nav-new")
	if len(newest) != 1 {
		t.Fatalf("newest post nav has %d links, want 1: %v", len(newest), newest)
	}
	followupWantClasses(t, "newest", "/post/fu-nav-mid", newest["/post/fu-nav-mid"], "post-nav-prev", "post-nav-next")

	// oldest: only the next (newer) post; it must not look like a "previous" link
	oldest := followupPostNav(t, h, "fu-nav-old")
	if len(oldest) != 1 {
		t.Fatalf("oldest post nav has %d links, want 1: %v", len(oldest), oldest)
	}
	followupWantClasses(t, "oldest", "/post/fu-nav-mid", oldest["/post/fu-nav-mid"], "post-nav-next", "post-nav-prev")
}

// ---------- item 8: maintenance path ----------

func TestFollowupMaintenanceEncodedPathsAreNotBypassed(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "fu-mnt-admin")
	maintenanceEnable(t, h, 0)

	for _, path := range []string{"/%61dmin", "/admin%2fx", "/%61dmin/posts", "/admin%2Fposts", "/%2561dmin", "/uploads%2fx.png", "/install%2fx", "/auth%2flogin"} {
		r := archivesGet(t, h, path)
		if r.code != http.StatusServiceUnavailable || r.panicked != nil {
			t.Errorf("GET %s during maintenance: status=%d panic=%v, want 503", path, r.code, r.panicked)
		}
		if r.header.Get(echo.HeaderRetryAfter) == "" {
			t.Errorf("GET %s during maintenance: no Retry-After header", path)
		}
	}
	for _, path := range []string{"/api/%61dmin/posts", "/api%2fadmin%2fposts"} {
		r := h.request(http.MethodGet, path, "", "", echo.MIMEApplicationJSON, "")
		if r.code != http.StatusServiceUnavailable {
			t.Errorf("GET %s during maintenance: status=%d, want 503", path, r.code)
		}
	}

	// Paths that are really under the bypass prefixes stay reachable, encoded filenames included.
	for _, path := range []string{"/login", "/admin", "/admin/assets/fonts/bricolage-grotesque.woff2", "/uploads/%E6%96%87.png", "/install", "/api/oauth/status"} {
		if r := archivesGet(t, h, path); r.code == http.StatusServiceUnavailable {
			t.Errorf("GET %s during maintenance: 503, want reachable", path)
		}
	}
	// A signed-in admin is not held back by the gate (the route itself may then be 404).
	if r := h.request(http.MethodGet, "/%61dmin", "", h.token(admin), echo.MIMETextHTML, ""); r.code == http.StatusServiceUnavailable {
		t.Errorf("GET /%%61dmin as admin during maintenance: 503, want the gate to let the admin through")
	}
}

// ---------- item 10: font cache header ----------

func TestFollowupAdminFontsAreCacheable(t *testing.T) {
	h := archivesHarness(t)
	for _, name := range []string{"bricolage-grotesque.woff2", "noto-sans-tc.woff2"} {
		path := "/admin/assets/fonts/" + name
		r := archivesGet(t, h, path)
		if r.code != http.StatusOK || len(r.body) == 0 {
			t.Errorf("GET %s: status=%d bytes=%d, want 200 with the font", path, r.code, len(r.body))
			continue
		}
		cc := r.header.Get(echo.HeaderCacheControl)
		if !strings.Contains(cc, "no-cache") {
			t.Errorf("unhashed GET %s: Cache-Control = %q, want no-cache", path, cc)
		}
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(r.body)))
		versioned := archivesGet(t, h, path+"?v="+hash)
		if versioned.code != http.StatusOK || versioned.body != r.body {
			t.Errorf("hashed GET %s: status=%d body matches unversioned response=%t", path, versioned.code, versioned.body == r.body)
			continue
		}
		immutable := versioned.header.Get(echo.HeaderCacheControl)
		if !strings.Contains(immutable, "public") || !strings.Contains(immutable, "max-age=31536000") || !strings.Contains(immutable, "immutable") {
			t.Errorf("hashed GET %s: Cache-Control = %q, want public, one-year immutable", path, immutable)
		}
		if actual := fmt.Sprintf("%x", sha256.Sum256([]byte(versioned.body))); actual != hash {
			t.Errorf("hashed GET %s: response SHA-256 %s differs from URL hash %s", path, actual, hash)
		}
	}
}

// ---------- item 13: HEAD ----------

func TestFollowupPublicPagesAcceptHead(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "fu-head")
	cat := pubThemeCategory(t, h, "Head Category", "fu-head-cat", nil, 1, false)
	tag := archivesSeedTag(t, h, "Head Tag", "fu-head-tag")
	post := archivesSeedPost(t, h, author, archivesPost{title: "Head Visible Post", publishedAt: archivesDay(2025, time.June, 12),
		categories: []core.Category{cat}, tags: []core.Tag{tag}})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Head Static Page", slug: "fu-head-page", kind: "page",
		content: "<p>page</p>", publishedAt: archivesDay(2025, time.June, 1)})

	paths := []string{
		"/", "/posts", "/post/" + post.Slug, "/fu-head-page", "/c/fu-head-cat", "/t/fu-head-tag", "/author/fu-head",
		"/archive", "/archive/2025/06", "/search?q=Head", "/search",
		// error statuses must match as well
		"/post/fu-head-missing", "/fu-head-missing-page", "/c/fu-head-missing", "/posts?page=99",
	}
	server := httptest.NewServer(h.echo)
	defer server.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	for _, path := range paths {
		get := followupHTMLGet(h, path)
		head := h.request(http.MethodHead, path, "", "", echo.MIMETextHTML, "")
		if head.panicked != nil {
			t.Errorf("HEAD %s panicked: %v", path, head.panicked)
			continue
		}
		if head.code != get.code {
			t.Errorf("HEAD %s status = %d, want the GET status %d", path, head.code, get.code)
			continue
		}
		if get.code == http.StatusOK {
			if ct := head.header.Get(echo.HeaderContentType); !strings.HasPrefix(ct, echo.MIMETextHTML) {
				t.Errorf("HEAD %s Content-Type = %q, want text/html", path, ct)
			}
		}
		req, err := http.NewRequest(http.MethodHead, server.URL+path, nil)
		if err != nil {
			t.Fatalf("build HEAD request: %v", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Errorf("HEAD %s over a real connection: %v", path, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != get.code || len(body) != 0 {
			t.Errorf("HEAD %s over a real connection: status=%d bytes=%d, want %d with no body", path, resp.StatusCode, len(body), get.code)
		}
	}
}

// Guard: HEAD is gated by maintenance like GET (the gate runs before routing, so this holds today).
func TestFollowupHeadDuringMaintenanceIs503(t *testing.T) {
	h := archivesHarness(t)
	maintenanceEnable(t, h, 0)
	for _, path := range []string{"/", "/posts", "/search?q=x"} {
		r := h.request(http.MethodHead, path, "", "", echo.MIMETextHTML, "")
		if r.code != http.StatusServiceUnavailable || r.header.Get(echo.HeaderRetryAfter) == "" {
			t.Errorf("HEAD %s during maintenance: status=%d Retry-After=%q, want 503 with Retry-After", path, r.code, r.header.Get(echo.HeaderRetryAfter))
		}
	}
}

// ---------- items 5 and 12: markup the server can check ----------

// followupStripeCount counts the stripe decorations drawn inside the page header of a rendered page:
// spans with class stripes plus a ::before rule that paints a gradient on one of the header's classes.
func followupStripeCount(t *testing.T, body string) (count int, header string) {
	t.Helper()
	doc := followupParse(t, body)
	headers := followupFind(doc, func(n *nethtml.Node) bool {
		return followupHasClass(n, "page-head") || followupHasClass(n, "article-head") || followupHasClass(n, "search-band")
	})
	if len(headers) == 0 {
		t.Fatal("page has no .page-head, .article-head or .search-band header")
	}
	first := headers[0]
	count = len(followupByClass(first, "stripes"))
	var css strings.Builder
	followupWalk(doc, func(n *nethtml.Node) {
		if n.Type == nethtml.ElementNode && n.Data == "style" {
			css.WriteString("<style>" + followupText(n) + "</style>")
		}
	})
	drawn := false
	for _, rule := range followupParseCSS(css.String()) {
		for _, class := range strings.Fields(followupAttr(first, "class")) {
			pseudo := regexp.MustCompile(`\.` + regexp.QuoteMeta(class) + `::?before$`)
			if _, ok := rule.selectorMatching(pseudo); !ok {
				continue
			}
			if strings.Contains(rule.decls["background"], "linear-gradient") && rule.decls["content"] != "" && rule.decls["content"] != "none" {
				drawn = true
			}
			if rule.decls["display"] == "none" || rule.decls["content"] == "none" {
				drawn = false
			}
		}
	}
	if drawn {
		count++
	}
	return count, followupAttr(first, "class")
}

// Item 5: the slanted stripe is drawn once in the header of /posts, a static page and /search.
func TestFollowupStripeDecorationDrawnOnce(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "fu-stripe")
	followupSeed(t, h, author, "Stripe Listed Post", "fu-stripe-post", "<p>stripe</p>", 1)
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Stripe Static Page", slug: "fu-stripe-page", kind: "page",
		content: "<p>page</p>", publishedAt: archivesDay(2025, time.June, 2)})

	for _, path := range []string{"/posts", "/fu-stripe-page", "/search?q=stripe", "/search"} {
		count, class := followupStripeCount(t, archivesPage(t, h, path))
		if count != 1 {
			t.Errorf("GET %s: header %q draws %d stripe decorations, want exactly 1", path, class, count)
		}
	}
}

// Item 12: a static page has the quick links panel of the Page board in its side column.
func TestFollowupStaticPageHasQuickLinksPanel(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "fu-quick")
	for i, slug := range []string{"fu-quick-a", "about"} {
		pubThemeSeedPost(t, h, author, pubThemePost{title: fmt.Sprintf("Quick Page %d", i), slug: slug, kind: "page",
			content: "<p>page body</p>", publishedAt: archivesDay(2025, time.June, i+1)})
	}
	for _, slug := range []string{"fu-quick-a", "about"} {
		path := "/" + slug
		doc := followupParse(t, archivesPage(t, h, path))
		panels := followupByClass(doc, "page-quicklinks")
		if len(panels) != 1 {
			t.Errorf("GET %s: %d .page-quicklinks panels, want 1", path, len(panels))
			continue
		}
		panel := panels[0]
		if !strings.Contains(followupText(panel), "快速連結") {
			t.Errorf("GET %s: the panel does not carry the label 快速連結: %q", path, followupText(panel))
		}
		links := map[string]string{}
		for _, a := range followupFind(panel, func(n *nethtml.Node) bool { return n.Data == "a" }) {
			links[followupAttr(a, "href")] = followupText(a)
		}
		if links["/posts"] != "所有文章" || links["/search"] != "搜尋站內" || len(links) != 2 {
			t.Errorf("GET %s: panel links = %v, want exactly /posts 所有文章 and /search 搜尋站內", path, links)
		}
		if aside := followupFind(doc, func(n *nethtml.Node) bool { return n.Data == "aside" && followupHasAncestor(panel, n) }); len(aside) == 0 {
			t.Errorf("GET %s: the quick links panel is not inside an <aside> side column", path)
		}
	}
}

// Item 12: list rows without a cover get a placeholder thumbnail, rows with one get the image only.
func TestFollowupPostListCoverPlaceholder(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "fu-cover")
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Cover Explicit Entry", slug: "fu-cover-explicit", cover: "/uploads/fu-cover.png",
		publishedAt: archivesDay(2025, time.June, 4)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Cover Inline Entry", slug: "fu-cover-inline",
		content: `<p><img src="/uploads/fu-inline.png" alt=""></p>`, publishedAt: archivesDay(2025, time.June, 3)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Cover None Entry A", slug: "fu-cover-none-a", publishedAt: archivesDay(2025, time.June, 2)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Cover None Entry B", slug: "fu-cover-none-b", publishedAt: archivesDay(2025, time.June, 1)})

	doc := followupParse(t, archivesPage(t, h, "/posts"))
	entries := followupFind(doc, func(n *nethtml.Node) bool { return n.Data == "li" && followupHasClass(n, "entry") })
	if len(entries) != 4 {
		t.Fatalf("GET /posts: %d .entry rows, want 4", len(entries))
	}
	for _, entry := range entries {
		title := followupText(followupByClass(entry, "entry-title")[0])
		images := followupFind(entry, func(n *nethtml.Node) bool { return n.Data == "img" })
		placeholders := followupByClass(entry, "entry-cover-placeholder")
		if strings.HasPrefix(title, "Cover None") {
			if len(placeholders) != 1 {
				t.Errorf("%q: %d .entry-cover-placeholder elements, want 1", title, len(placeholders))
				continue
			}
			if followupAttr(placeholders[0], "aria-hidden") != "true" {
				t.Errorf("%q: the placeholder must be aria-hidden=\"true\"", title)
			}
			if len(images) != 0 {
				t.Errorf("%q: has %d <img> although it has no cover", title, len(images))
			}
			continue
		}
		if len(placeholders) != 0 {
			t.Errorf("%q has a cover but also a placeholder", title)
		}
		if len(images) != 1 || !followupHasClass(images[0], "entry-cover") {
			t.Errorf("%q: want one img.entry-cover, got %d images", title, len(images))
		}
	}
}

// Item 12: the category dot sits in the same row (.entry-cats) as the category name, not in a row of its own.
func TestFollowupPostListCategoryDotSharesRowWithCategory(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "fu-dot")
	cat := pubThemeCategory(t, h, "Dot Category", "fu-dot-cat", nil, 1, false)
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Dot Filed Entry", slug: "fu-dot-filed", categories: []core.Category{cat},
		publishedAt: archivesDay(2025, time.June, 2)})

	doc := followupParse(t, archivesPage(t, h, "/posts"))
	entries := followupFind(doc, func(n *nethtml.Node) bool { return n.Data == "li" && followupHasClass(n, "entry") })
	if len(entries) != 1 {
		t.Fatalf("GET /posts: %d .entry rows, want 1", len(entries))
	}
	entry := entries[0]
	rows := followupByClass(entry, "entry-cats")
	if len(rows) != 1 {
		t.Fatalf("%d .entry-cats rows, want 1", len(rows))
	}
	if !strings.Contains(followupText(rows[0]), "Dot Category") {
		t.Errorf(".entry-cats = %q, want it to hold the category name", followupText(rows[0]))
	}
	for _, dot := range followupByClass(entry, "entry-flavor") {
		if !followupHasAncestor(dot, rows[0]) {
			t.Errorf("the category dot (.entry-flavor) is outside .entry-cats; it must share the row with the category name")
		}
	}
	if len(followupByClass(rows[0], "entry-flavor")) == 0 {
		t.Errorf(".entry-cats holds no .entry-flavor dot")
	}
}
