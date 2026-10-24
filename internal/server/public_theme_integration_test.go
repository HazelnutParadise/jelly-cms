package server

// Public theme integration tests for ticket 05 slices B1 (public scope, home, posts, post, page, about) and
// B2 (search). The contract is docs/tickets/05-public-theme.md. They are written before the implementation
// and are expected to fail until the public handlers, search.go, the template functions and the templates
// follow the contract. Do not edit them to match an implementation. Run with JELLY_TEST_POSTGRES=1; the
// harness skips otherwise.
//
// Assertions check status codes, visible text and link attributes only. Titles are chosen so that none is a
// substring of another, and fixture dates are at noon UTC. Search queries appear only in post bodies, not in
// titles, because highlighting wraps the query inside a title with <mark> and would split its text.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
)

// pubThemePost describes one seeded post or page. Empty status means published, empty visibility means
// public, empty kind means post, empty content means a short body. trashed soft-deletes the row afterwards.
type pubThemePost struct {
	title       string
	slug        string
	content     string
	excerpt     string
	cover       string
	status      string
	visibility  string
	kind        string
	pinned      bool
	trashed     bool
	publishedAt time.Time
	categories  []core.Category
}

func pubThemeSeedPost(t *testing.T, h *accessHarness, author core.User, spec pubThemePost) core.Post {
	t.Helper()
	status, visibility, kind, content := spec.status, spec.visibility, spec.kind, spec.content
	if status == "" {
		status = "published"
	}
	if visibility == "" {
		visibility = "public"
	}
	if kind == "" {
		kind = "post"
	}
	if content == "" {
		content = "<p>seed body</p>"
	}
	slug := spec.slug
	if slug == "" {
		slug = core.Slugify(spec.title, "pub-theme")
	}
	published := spec.publishedAt
	post := core.Post{
		Title:       spec.title,
		Slug:        slug,
		Content:     content,
		Excerpt:     spec.excerpt,
		Cover:       spec.cover,
		Type:        kind,
		Status:      status,
		AuthorID:    author.ID,
		Visibility:  visibility,
		Pinned:      spec.pinned,
		PublishedAt: &published,
		CreatedAt:   published,
		UpdatedAt:   published,
	}
	if err := h.db.Create(&post).Error; err != nil {
		t.Fatalf("seed post %q: %v", spec.title, err)
	}
	for i := range spec.categories {
		if err := h.db.Model(&post).Association("Categories").Append(&spec.categories[i]); err != nil {
			t.Fatalf("assign category %q to post %q: %v", spec.categories[i].Slug, spec.title, err)
		}
	}
	if spec.trashed {
		if err := h.db.Delete(&post).Error; err != nil {
			t.Fatalf("trash post %q: %v", spec.title, err)
		}
	}
	return post
}

// pubThemeSeedRun seeds n public posts titled "<prefix> NN" (NN = 01..n), one per day of month, oldest first.
func pubThemeSeedRun(t *testing.T, h *accessHarness, author core.User, prefix, slugBase string, n, year int, month time.Month) {
	t.Helper()
	for i := 1; i <= n; i++ {
		pubThemeSeedPost(t, h, author, pubThemePost{
			title:       fmt.Sprintf("%s %02d", prefix, i),
			slug:        fmt.Sprintf("%s-%02d", slugBase, i),
			publishedAt: archivesDay(year, month, i),
		})
	}
}

func pubThemeCategory(t *testing.T, h *accessHarness, name, slug string, parentID *uint, position int, hideFromList bool) core.Category {
	t.Helper()
	row := core.Category{Name: name, Slug: slug, ParentID: parentID, Position: position, HideFromList: hideFromList}
	if err := h.db.Create(&row).Error; err != nil {
		t.Fatalf("seed category %q: %v", slug, err)
	}
	return row
}

// pubThemeSetOption writes one site option row (key/value), replacing an existing row with the same key.
func pubThemeSetOption(t *testing.T, h *accessHarness, key, value string) {
	t.Helper()
	if err := h.db.Save(&core.Option{Key: key, Value: value}).Error; err != nil {
		t.Fatalf("set option %s: %v", key, err)
	}
}

// pubThemeCJK returns one paragraph of n CJK characters. The contract reads 400 CJK characters per minute,
// so 800 characters is 2 minutes.
func pubThemeCJK(n int) string {
	return "<p>" + strings.Repeat("測", n) + "</p>"
}

var (
	pubThemeAnchorPattern = regexp.MustCompile(`(?s)<a\b([^>]*)>(.*?)</a>`)
	pubThemeHrefPattern   = regexp.MustCompile(`\bhref="([^"]*)"`)
	pubThemeTagPattern    = regexp.MustCompile(`<[^>]*>`)
	pubThemeHeadingID     = regexp.MustCompile(`<h2\b[^>]*\bid="([^"]+)"`)
	pubThemeReadingTwo    = regexp.MustCompile(`(^|[^0-9])2\s*分鐘`)
	pubThemeFoundCount    = regexp.MustCompile(`找到\s*\d`)
)

type pubThemeAnchor struct {
	attrs string
	href  string
	text  string
}

// pubThemeAnchors lists every <a> element with its attributes, its href and its visible text.
func pubThemeAnchors(body string) []pubThemeAnchor {
	var out []pubThemeAnchor
	for _, m := range pubThemeAnchorPattern.FindAllStringSubmatch(body, -1) {
		href := ""
		if hm := pubThemeHrefPattern.FindStringSubmatch(m[1]); hm != nil {
			href = hm[1]
		}
		text := strings.Join(strings.Fields(pubThemeTagPattern.ReplaceAllString(m[2], " ")), " ")
		out = append(out, pubThemeAnchor{attrs: m[1], href: href, text: text})
	}
	return out
}

// pubThemeFindAnchor returns the first link to href that has visible text.
func pubThemeFindAnchor(body, href string) (pubThemeAnchor, bool) {
	for _, a := range pubThemeAnchors(body) {
		if a.href == href && a.text != "" {
			return a, true
		}
	}
	return pubThemeAnchor{}, false
}

func pubThemeHasLink(body, href string) bool {
	for _, a := range pubThemeAnchors(body) {
		if a.href == href {
			return true
		}
	}
	return false
}

// pubThemeHasNumber reports whether text contains n as a whole number.
func pubThemeHasNumber(text string, n int) bool {
	return regexp.MustCompile(fmt.Sprintf(`(^|[^0-9])%d([^0-9]|$)`, n)).MatchString(text)
}

// pubThemeStandaloneNumber reports whether n is the whole text of an element (a stat value).
func pubThemeStandaloneNumber(body string, n int) bool {
	return regexp.MustCompile(fmt.Sprintf(`>\s*%d\s*<`, n)).MatchString(body)
}

// pubThemeCountTitles counts how many of "<prefix> 01" .. "<prefix> n" appear in body.
func pubThemeCountTitles(body, prefix string, n int) int {
	count := 0
	for i := 1; i <= n; i++ {
		if strings.Contains(body, fmt.Sprintf("%s %02d", prefix, i)) {
			count++
		}
	}
	return count
}

// pubThemeWantStatus is archivesWantStatus without the fatal exit, so a loop reports every failing path.
func pubThemeWantStatus(t *testing.T, path string, r accessResult, want int) {
	t.Helper()
	if r.code != want {
		t.Errorf("GET %s status = %d, want %d; body=%q", path, r.code, want, archivesSnippet(r.body))
	}
}

func pubThemeWantOnce(t *testing.T, path, body, text string) {
	t.Helper()
	if n := strings.Count(body, text); n != 1 {
		t.Errorf("GET %s: %q appears %d times, want 1", path, text, n)
	}
}

// pubThemeAPI sends one JSON request to the admin API as the given token holder.
func pubThemeAPI(h *accessHarness, token, method, path, body string) accessResult {
	return h.request(method, path, body, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
}

// pubThemeHeaderMenu creates a header menu with a 首頁 (home) item and a custom 文章總覽 item to /posts.
func pubThemeHeaderMenu(t *testing.T, h *accessHarness, token string) {
	t.Helper()
	r := pubThemeAPI(h, token, http.MethodPost, "/api/admin/menus", `{"name":"公開主選單","location":"header"}`)
	if r.code != http.StatusCreated {
		t.Fatalf("POST /api/admin/menus status = %d, want %d; body=%q", r.code, http.StatusCreated, archivesSnippet(r.body))
	}
	var menu struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal([]byte(r.body), &menu); err != nil || menu.ID == 0 {
		t.Fatalf("decode created menu: %v; body=%q", err, archivesSnippet(r.body))
	}
	path := fmt.Sprintf("/api/admin/menus/%d/items", menu.ID)
	r = pubThemeAPI(h, token, http.MethodPut, path,
		`[{"label":"首頁","type":"home"},{"label":"文章總覽","type":"custom","url":"/posts"}]`)
	if r.code != http.StatusOK {
		t.Fatalf("PUT %s status = %d, want %d; body=%q", path, r.code, http.StatusOK, archivesSnippet(r.body))
	}
}

// pubThemeWantHomeActive requires the 首頁 header link (href "/") to be present on path, and to carry
// aria-current="page" exactly when want is true.
func pubThemeWantHomeActive(t *testing.T, path, body string, want bool) {
	t.Helper()
	found, active := false, false
	for _, a := range pubThemeAnchors(body) {
		if a.href != "/" || !strings.Contains(a.text, "首頁") {
			continue
		}
		found = true
		if strings.Contains(a.attrs, `aria-current="page"`) {
			active = true
		}
	}
	if !found {
		t.Errorf("GET %s: no 首頁 link to / in the body", path)
		return
	}
	if active != want {
		t.Errorf("GET %s: 首頁 aria-current=\"page\" present = %v, want %v", path, active, want)
	}
}

// TestPublicHomeShowsStatsBand: the three stats are the public post count (5), the authors of public posts
// (2) and the year of the oldest public post (2019). The private post (oldest, 2010, by a third author), the
// future post, the draft and the trashed post are not counted.
func TestPublicHomeShowsStatsBand(t *testing.T) {
	h := archivesHarness(t)
	a := h.user("author", "pubstatsA")
	b := h.user("author", "pubstatsB")
	c := h.user("author", "pubstatsC")

	pubThemeSeedPost(t, h, a, pubThemePost{title: "Stats Alpha Entry", slug: "stats-alpha", publishedAt: archivesDay(2019, time.January, 15)})
	pubThemeSeedPost(t, h, a, pubThemePost{title: "Stats Bravo Entry", slug: "stats-bravo", publishedAt: archivesDay(2021, time.March, 1)})
	pubThemeSeedPost(t, h, a, pubThemePost{title: "Stats Charlie Entry", slug: "stats-charlie", publishedAt: archivesDay(2022, time.May, 1)})
	pubThemeSeedPost(t, h, b, pubThemePost{title: "Stats Delta Entry", slug: "stats-delta", publishedAt: archivesDay(2023, time.July, 1)})
	pubThemeSeedPost(t, h, b, pubThemePost{title: "Stats Echo Entry", slug: "stats-echo", publishedAt: archivesDay(2024, time.September, 1)})

	pubThemeSeedPost(t, h, c, pubThemePost{title: "Stats Private Hidden", slug: "stats-private", visibility: "private",
		publishedAt: archivesDay(2010, time.June, 1)})
	pubThemeSeedPost(t, h, a, pubThemePost{title: "Stats Future Hidden", slug: "stats-future",
		publishedAt: time.Date(2099, time.January, 1, 12, 0, 0, 0, time.UTC)})
	pubThemeSeedPost(t, h, b, pubThemePost{title: "Stats Draft Hidden", slug: "stats-draft", status: "draft",
		publishedAt: archivesDay(2020, time.February, 1)})
	pubThemeSeedPost(t, h, a, pubThemePost{title: "Stats Trashed Hidden", slug: "stats-trashed", trashed: true,
		publishedAt: archivesDay(2020, time.March, 1)})

	path := "/"
	body := archivesPage(t, h, path)
	if !pubThemeStandaloneNumber(body, 5) {
		t.Errorf("GET %s: post count 5 is not shown as a stat value", path)
	}
	if !pubThemeStandaloneNumber(body, 2) {
		t.Errorf("GET %s: author count 2 is not shown as a stat value", path)
	}
	archivesWantText(t, path, body, "2019")
	archivesWantAbsent(t, path, body, "2010", "Stats Private Hidden", "Stats Future Hidden",
		"Stats Draft Hidden", "Stats Trashed Hidden")
}

// TestPublicHomeFeaturedIsPinnedElseNewest: the 主打 card is the pinned public post, otherwise the newest
// public post. It is shown once, first, and a pinned private post is never featured.
func TestPublicHomeFeaturedIsPinnedElseNewest(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "pubfeat")

	pubThemeSeedPost(t, h, author, pubThemePost{title: "Feature Pinned Older", slug: "feature-pinned",
		pinned: true, publishedAt: archivesDay(2019, time.March, 1)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Feature Mid Plain", slug: "feature-mid",
		publishedAt: archivesDay(2024, time.January, 1)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Feature Newest Plain", slug: "feature-newest",
		publishedAt: archivesDay(2025, time.June, 1)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Feature Pinned Private", slug: "feature-private-pinned",
		pinned: true, visibility: "private", publishedAt: archivesDay(2026, time.February, 1)})

	path := "/"
	body := archivesPage(t, h, path)
	archivesWantText(t, path, body, "主打", "Feature Pinned Older")
	pubThemeWantOnce(t, path, body, "Feature Pinned Older")
	archivesWantOrder(t, path, body, "Feature Pinned Older", "Feature Newest Plain")
	archivesWantAbsent(t, path, body, "Feature Pinned Private")

	// With the pin removed, the newest public post is featured.
	if err := h.db.Model(&core.Post{}).Where("slug = ?", "feature-pinned").Update("pinned", false).Error; err != nil {
		t.Fatalf("unpin feature-pinned: %v", err)
	}
	body = archivesPage(t, h, path)
	archivesWantText(t, path, body, "主打", "Feature Newest Plain")
	pubThemeWantOnce(t, path, body, "Feature Newest Plain")
	archivesWantOrder(t, path, body, "Feature Newest Plain", "Feature Mid Plain")
	archivesWantOrder(t, path, body, "Feature Newest Plain", "Feature Pinned Older")
	archivesWantAbsent(t, path, body, "Feature Pinned Private")
}

// TestPublicHomeHidesPrivateDraftScheduledTrashed: only the public, published, not future, not trashed post
// appears on the home page and on the posts list.
func TestPublicHomeHidesPrivateDraftScheduledTrashed(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "pubhide")

	pubThemeSeedPost(t, h, author, pubThemePost{title: "Hide Visible Entry", slug: "hide-visible",
		publishedAt: archivesDay(2025, time.April, 1)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Hide Private Entry", slug: "hide-private", visibility: "private",
		publishedAt: archivesDay(2025, time.April, 2)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Hide Draft Entry", slug: "hide-draft", status: "draft",
		publishedAt: archivesDay(2025, time.April, 3)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Hide Future Entry", slug: "hide-future",
		publishedAt: time.Now().UTC().AddDate(1, 0, 0)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Hide Trashed Entry", slug: "hide-trashed", trashed: true,
		publishedAt: archivesDay(2025, time.April, 4)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Hide Pinned Private", slug: "hide-pinned-private",
		pinned: true, visibility: "private", publishedAt: archivesDay(2025, time.April, 5)})

	for _, path := range []string{"/", "/posts"} {
		body := archivesPage(t, h, path)
		archivesWantText(t, path, body, "Hide Visible Entry")
		archivesWantAbsent(t, path, body, "Hide Private Entry", "Hide Draft Entry", "Hide Future Entry",
			"Hide Trashed Entry", "Hide Pinned Private")
	}
}

// TestPublicHomeIntroBandFromSiteDescription: the site_description option is shown in the intro band. When it
// is empty, its text is gone from the page.
func TestPublicHomeIntroBandFromSiteDescription(t *testing.T) {
	h := archivesHarness(t)
	const description = "這是測試用的網站描述文字"

	pubThemeSetOption(t, h, "site_description", description)
	path := "/"
	archivesWantText(t, path, archivesPage(t, h, path), description)

	pubThemeSetOption(t, h, "site_description", "")
	archivesWantAbsent(t, path, archivesPage(t, h, path), description)
}

// TestPublicHomeMenuActiveState: with a header menu holding 首頁 (home), that link carries aria-current="page"
// on "/" and not on "/posts".
func TestPublicHomeMenuActiveState(t *testing.T) {
	h := archivesHarness(t)
	token := h.token(h.user("admin", "pubmenu-admin"))
	author := h.user("author", "pubmenu-author")
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Menu Seed Entry", slug: "menu-seed", publishedAt: archivesDay(2025, time.May, 1)})
	pubThemeHeaderMenu(t, h, token)

	pubThemeWantHomeActive(t, "/", archivesPage(t, h, "/"), true)
	pubThemeWantHomeActive(t, "/posts", archivesPage(t, h, "/posts"), false)
}

// TestPublicPostsPagination: 11 public posts give 10 on page 1 and the oldest on page 2. Out-of-range pages
// are 404; page=0 and a non-numeric page are page 1.
func TestPublicPostsPagination(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "pubpager")
	pubThemeSeedRun(t, h, author, "Pages Entry", "pages-entry", 11, 2025, time.July)

	path := "/posts"
	body := archivesPage(t, h, path)
	if n := pubThemeCountTitles(body, "Pages Entry", 11); n != 10 {
		t.Errorf("GET %s shows %d of the 11 titles, want 10", path, n)
	}
	archivesWantText(t, path, body, "Pages Entry 11", "Pages Entry 02")
	archivesWantAbsent(t, path, body, "Pages Entry 01")

	path = "/posts?page=2"
	body = archivesPage(t, h, path)
	archivesWantText(t, path, body, "Pages Entry 01")
	archivesWantAbsent(t, path, body, "Pages Entry 11")

	archivesWantStatus(t, "/posts?page=3", archivesGet(t, h, "/posts?page=3"), http.StatusNotFound)

	for _, path := range []string{"/posts?page=0", "/posts?page=abc"} {
		body := archivesPage(t, h, path)
		archivesWantText(t, path, body, "Pages Entry 11")
		archivesWantAbsent(t, path, body, "Pages Entry 01")
	}
}

// TestPublicPostsLabelShowsPageOfTotal: the posts label reads 第 N 頁，共 M 頁.
func TestPublicPostsLabelShowsPageOfTotal(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "publabel")
	pubThemeSeedRun(t, h, author, "Label Entry", "label-entry", 11, 2025, time.July)

	path := "/posts?page=2"
	archivesWantText(t, path, archivesPage(t, h, path), "第 2 頁，共 2 頁")
	path = "/posts"
	archivesWantText(t, path, archivesPage(t, h, path), "第 1 頁，共 2 頁")
}

// TestPublicPostsCategoryChipsWithCounts: the chips are 全部 plus the first six visible top-level categories,
// each with its post count, linking to /c/<slug>. The seventh, hidden, and child categories are absent.
func TestPublicPostsCategoryChipsWithCounts(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "pubchip")

	specs := []struct {
		name, slug string
		count      int
	}{
		{"Chip Garden", "chip-garden", 6},
		{"Chip Workshop", "chip-workshop", 5},
		{"Chip Kitchen", "chip-kitchen", 4},
		{"Chip Studio", "chip-studio", 3},
		{"Chip Harbor", "chip-harbor", 2},
		{"Chip Meadow", "chip-meadow", 1},
		{"Chip Seventh", "chip-seventh", 0},
	}
	var garden core.Category
	for i, spec := range specs {
		cat := pubThemeCategory(t, h, spec.name, spec.slug, nil, i+1, false)
		if i == 0 {
			garden = cat
		}
		for k := 1; k <= spec.count; k++ {
			pubThemeSeedPost(t, h, author, pubThemePost{
				title:       fmt.Sprintf("%s Post %02d", spec.name, k),
				slug:        fmt.Sprintf("%s-post-%02d", spec.slug, k),
				publishedAt: archivesDay(2025, time.August, k),
				categories:  []core.Category{cat},
			})
		}
	}
	pubThemeCategory(t, h, "Chip Hidden Vault", "chip-hidden-vault", nil, 0, true)
	pubThemeCategory(t, h, "Chip Child Sub", "chip-child-sub", &garden.ID, 1, false)

	path := "/posts"
	body := archivesPage(t, h, path)
	archivesWantText(t, path, body, "全部")
	for _, spec := range specs[:6] {
		a, ok := pubThemeFindAnchor(body, "/c/"+spec.slug)
		if !ok {
			t.Errorf("GET %s: no chip linking to /c/%s", path, spec.slug)
			continue
		}
		if !strings.Contains(a.text, spec.name) || !pubThemeHasNumber(a.text, spec.count) {
			t.Errorf("GET %s: chip for %q text = %q, want the name and count %d", path, spec.slug, a.text, spec.count)
		}
	}
	archivesWantAbsent(t, path, body, "Chip Seventh", "/c/chip-seventh", "Chip Hidden Vault",
		"/c/chip-hidden-vault", "Chip Child Sub", "/c/chip-child-sub")
}

// TestPublicPostsRowShowsReadingTimeAndCover: each row shows its cover image and its reading minutes.
func TestPublicPostsRowShowsReadingTimeAndCover(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "pubrow")
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Row Cover Entry", slug: "row-cover-entry",
		cover: "/uploads/pub-row-cover.jpg", content: pubThemeCJK(800), publishedAt: archivesDay(2025, time.October, 1)})

	path := "/posts"
	archivesWantText(t, path, archivesPage(t, h, path), "Row Cover Entry", "/uploads/pub-row-cover.jpg", "分鐘")
}

// TestPublicPostReadingTimeAndExcerptLede: 800 CJK characters read as 2 minutes; the excerpt is the lede.
func TestPublicPostReadingTimeAndExcerptLede(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "publede")
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Lede Reading Entry", slug: "lede-reading-entry",
		excerpt: "這是導語摘要用來測試", content: pubThemeCJK(800), publishedAt: archivesDay(2025, time.November, 1)})

	path := "/post/lede-reading-entry"
	body := archivesPage(t, h, path)
	archivesWantText(t, path, body, "這是導語摘要用來測試", "分鐘")
	if !pubThemeReadingTwo.MatchString(body) {
		t.Errorf("GET %s: reading time 2 分鐘 is not shown", path)
	}
}

// TestPublicPostTableOfContentsLinksHeadings: three h2 headings give a 目錄 whose links match the heading ids.
// Two headings give no 目錄.
func TestPublicPostTableOfContentsLinksHeadings(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "pubtoc")
	filler := "<p>" + strings.Repeat("內文", 20) + "</p>"

	pubThemeSeedPost(t, h, author, pubThemePost{title: "Toc Three Entry", slug: "toc-three-entry",
		content:     "<h2>前言</h2>" + filler + "<h2>方法</h2>" + filler + "<h2>結論</h2>" + filler,
		publishedAt: archivesDay(2025, time.December, 1)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Toc Two Entry", slug: "toc-two-entry",
		content:     "<h2>甲段</h2>" + filler + "<h2>乙段</h2>" + filler,
		publishedAt: archivesDay(2025, time.December, 2)})

	path := "/post/toc-three-entry"
	body := archivesPage(t, h, path)
	archivesWantText(t, path, body, "目錄")
	ids := pubThemeHeadingID.FindAllStringSubmatch(body, -1)
	if len(ids) != 3 {
		t.Fatalf("GET %s: %d h2 headings with an id, want 3", path, len(ids))
	}
	for _, m := range ids {
		if !strings.Contains(body, `href="#`+m[1]+`"`) {
			t.Errorf("GET %s: no table of contents link href=\"#%s\" for heading id %q", path, m[1], m[1])
		}
	}

	path = "/post/toc-two-entry"
	archivesWantAbsent(t, path, archivesPage(t, h, path), "目錄")
}

// TestPublicPostPrevNext: the middle post links to both neighbours by published_at order, ignoring a draft and
// a private post in between. The first has no 上一篇, the last has no 下一篇.
func TestPublicPostPrevNext(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "pubprevnext")

	// Seeded out of order so that row ID order differs from published_at order.
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Prev Next Charlie", slug: "prev-next-charlie",
		publishedAt: archivesDay(2025, time.March, 10)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Prev Next Draft Between", slug: "prev-next-draft", status: "draft",
		publishedAt: archivesDay(2025, time.February, 5)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Prev Next Private Between", slug: "prev-next-private", visibility: "private",
		publishedAt: archivesDay(2025, time.February, 6)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Prev Next Bravo", slug: "prev-next-bravo",
		publishedAt: archivesDay(2025, time.February, 10)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Prev Next Alpha", slug: "prev-next-alpha",
		publishedAt: archivesDay(2025, time.January, 10)})

	path := "/post/prev-next-bravo"
	body := archivesPage(t, h, path)
	archivesWantText(t, path, body, "上一篇", "下一篇", "Prev Next Alpha", "Prev Next Charlie")
	archivesWantAbsent(t, path, body, "Prev Next Draft Between", "Prev Next Private Between")

	path = "/post/prev-next-alpha"
	body = archivesPage(t, h, path)
	archivesWantText(t, path, body, "下一篇", "Prev Next Bravo")
	archivesWantAbsent(t, path, body, "上一篇")

	path = "/post/prev-next-charlie"
	body = archivesPage(t, h, path)
	archivesWantText(t, path, body, "上一篇", "Prev Next Bravo")
	archivesWantAbsent(t, path, body, "下一篇")
}

// TestPublicPostPrivateOrFutureIsNotFound: private, draft, future and trashed posts return 404 at /post/<slug>.
func TestPublicPostPrivateOrFutureIsNotFound(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "pubpostnf")

	pubThemeSeedPost(t, h, author, pubThemePost{title: "NF Control Entry", slug: "nf-control", publishedAt: archivesDay(2025, time.May, 1)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "NF Private Entry", slug: "nf-private", visibility: "private",
		publishedAt: archivesDay(2025, time.May, 2)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "NF Draft Entry", slug: "nf-draft", status: "draft",
		publishedAt: archivesDay(2025, time.May, 3)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "NF Future Entry", slug: "nf-future",
		publishedAt: time.Now().UTC().AddDate(1, 0, 0)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "NF Trashed Entry", slug: "nf-trashed", trashed: true,
		publishedAt: archivesDay(2025, time.May, 4)})

	// The control proves the route serves public posts, so the 404s below are not an accident of routing.
	archivesPage(t, h, "/post/nf-control")
	for _, slug := range []string{"nf-private", "nf-draft", "nf-future", "nf-trashed"} {
		pubThemeWantStatus(t, "/post/"+slug, archivesGet(t, h, "/post/"+slug), http.StatusNotFound)
	}
}

// TestPublicPostAuthorCardShowsBio: the author card shows the display name and bio; with no display name it
// falls back to the username.
func TestPublicPostAuthorCardShowsBio(t *testing.T) {
	h := archivesHarness(t)
	bioAuthor := core.User{Username: "pubbio", Email: "pubbio@example.test", Role: "author", Provider: "local",
		DisplayName: "Bio Display Name", Bio: "這是作者簡介文字"}
	if err := h.db.Create(&bioAuthor).Error; err != nil {
		t.Fatalf("create bio author: %v", err)
	}
	fallbackAuthor := core.User{Username: "pubfallback", Email: "pubfallback@example.test", Role: "author", Provider: "local"}
	if err := h.db.Create(&fallbackAuthor).Error; err != nil {
		t.Fatalf("create fallback author: %v", err)
	}
	pubThemeSeedPost(t, h, bioAuthor, pubThemePost{title: "Bio Writer Entry", slug: "bio-writer-entry",
		publishedAt: archivesDay(2025, time.June, 1)})
	pubThemeSeedPost(t, h, fallbackAuthor, pubThemePost{title: "Bio Fallback Entry", slug: "bio-fallback-entry",
		publishedAt: archivesDay(2025, time.June, 2)})

	path := "/post/bio-writer-entry"
	archivesWantText(t, path, archivesPage(t, h, path), "Bio Display Name", "這是作者簡介文字")

	path = "/post/bio-fallback-entry"
	archivesWantText(t, path, archivesPage(t, h, path), "pubfallback")
}

// TestPublicPageSlugDoesNotServePostsOrDrafts: /<slug> serves only a published, public, non-scheduled page.
// Posts, drafts, private, scheduled and trashed pages are 404.
func TestPublicPageSlugDoesNotServePostsOrDrafts(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "pubpage")

	pubThemeSeedPost(t, h, author, pubThemePost{title: "Team Page Heading", slug: "pub-team", kind: "page",
		content: "<p>team page marker</p>", publishedAt: archivesDay(2025, time.January, 5)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Alias Post Heading", slug: "pub-post-alias",
		publishedAt: archivesDay(2025, time.January, 6)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Draft Page Heading", slug: "pub-draft-page", kind: "page",
		status: "draft", publishedAt: archivesDay(2025, time.January, 7)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Private Page Heading", slug: "pub-private-page", kind: "page",
		visibility: "private", publishedAt: archivesDay(2025, time.January, 8)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Future Page Heading", slug: "pub-future-page", kind: "page",
		publishedAt: time.Now().UTC().AddDate(1, 0, 0)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Trashed Page Heading", slug: "pub-trashed-page", kind: "page",
		trashed: true, publishedAt: archivesDay(2025, time.January, 9)})

	path := "/pub-team"
	archivesWantText(t, path, archivesPage(t, h, path), "team page marker")
	for _, slug := range []string{"pub-post-alias", "pub-draft-page", "pub-private-page", "pub-future-page", "pub-trashed-page"} {
		pubThemeWantStatus(t, "/"+slug, archivesGet(t, h, "/"+slug), http.StatusNotFound)
	}
}

// TestPublicAboutMissingIsNotFound: /about is 404 when no about page exists, and the handler does not create one.
func TestPublicAboutMissingIsNotFound(t *testing.T) {
	h := archivesHarness(t)

	path := "/about"
	archivesWantStatus(t, path, archivesGet(t, h, path), http.StatusNotFound)
	var n int64
	if err := h.db.Model(&core.Post{}).Where("slug = ?", "about").Count(&n).Error; err != nil {
		t.Fatalf("count about rows: %v", err)
	}
	if n != 0 {
		t.Errorf("GET %s created %d about page rows, want none", path, n)
	}

	author := h.user("author", "pubabout")
	pubThemeSeedPost(t, h, author, pubThemePost{title: "About Page Heading", slug: "about", kind: "page",
		content: "<p>about page marker</p>", publishedAt: archivesDay(2025, time.January, 4)})
	archivesWantText(t, path, archivesPage(t, h, path), "about page marker")
}

// TestPublicSearchCountAndHighlight: the count line reads 找到 N 篇文章, and matches are marked with <mark>
// in the snippet. Private and draft matches are not counted.
func TestPublicSearchCountAndHighlight(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "pubsearch")

	pubThemeSeedPost(t, h, author, pubThemePost{title: "Glow Notes First", slug: "glow-first",
		content: "<p>emberlight near the door</p>", publishedAt: archivesDay(2025, time.June, 1)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Glow Second Log", slug: "glow-second",
		content: "<p>a second emberlight line</p>", publishedAt: archivesDay(2025, time.June, 2)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Glow Private Log", slug: "glow-private", visibility: "private",
		content: "<p>emberlight private</p>", publishedAt: archivesDay(2025, time.June, 3)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Glow Draft Log", slug: "glow-draft", status: "draft",
		content: "<p>emberlight draft</p>", publishedAt: archivesDay(2025, time.June, 4)})

	path := "/search?" + url.Values{"q": {"emberlight"}}.Encode()
	body := archivesPage(t, h, path)
	archivesWantText(t, path, body, "找到 2 篇文章", "<mark>emberlight</mark>", "Glow Notes First", "Glow Second Log")
	archivesWantAbsent(t, path, body, "Glow Private Log", "Glow Draft Log")
}

// TestPublicSearchNoResultCardEchoesQuery: with no matches, the card names the query and links to /posts.
func TestPublicSearchNoResultCardEchoesQuery(t *testing.T) {
	h := archivesHarness(t)

	path := "/search?" + url.Values{"q": {"zqxnone"}}.Encode()
	body := archivesPage(t, h, path)
	archivesWantText(t, path, body, "沒有找到「zqxnone」")
	if !pubThemeHasLink(body, "/posts") {
		t.Errorf("GET %s: no link to /posts in the no-result card", path)
	}
}

// TestPublicSearchEscapesWildcardsAndHTML: % and _ match only the literal characters, the query is HTML-escaped
// in the output, and words that appear only inside a tag attribute are not matched.
func TestPublicSearchEscapesWildcardsAndHTML(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "pubescape")

	pubThemeSeedPost(t, h, author, pubThemePost{title: "Wild Plain Entry", slug: "wild-plain",
		content: "<p>nothing special here</p>", publishedAt: archivesDay(2025, time.July, 1)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Wild Percent Entry", slug: "wild-percent",
		content: "<p>discount 50% today</p>", publishedAt: archivesDay(2025, time.July, 2)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Wild Score Entry", slug: "wild-score",
		content: "<p>snake_case name</p>", publishedAt: archivesDay(2025, time.July, 3)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Attr Hidden Entry", slug: "attr-hidden",
		content:     `<p><a title="zephyrattr">plain text</a></p>`,
		publishedAt: archivesDay(2025, time.July, 4)})

	path := "/search?" + url.Values{"q": {"%"}}.Encode()
	body := archivesPage(t, h, path)
	archivesWantText(t, path, body, "找到 1 篇文章", "Wild Percent Entry")
	archivesWantAbsent(t, path, body, "Wild Plain Entry", "Wild Score Entry")

	path = "/search?" + url.Values{"q": {"_"}}.Encode()
	body = archivesPage(t, h, path)
	archivesWantText(t, path, body, "找到 1 篇文章", "Wild Score Entry")
	archivesWantAbsent(t, path, body, "Wild Plain Entry", "Wild Percent Entry")

	const script = `<script>alertpub</script>`
	path = "/search?" + url.Values{"q": {script}}.Encode()
	body = archivesPage(t, h, path)
	archivesWantText(t, path, body, "&lt;script&gt;alertpub&lt;/script&gt;")
	archivesWantAbsent(t, path, body, script)

	path = "/search?" + url.Values{"q": {"zephyrattr"}}.Encode()
	body = archivesPage(t, h, path)
	archivesWantAbsent(t, path, body, "Attr Hidden Entry")
	archivesWantText(t, path, body, "沒有找到「zephyrattr」")
}

// TestPublicSearchExcludesNonPublic: private, draft, scheduled and trashed posts are not results.
func TestPublicSearchExcludesNonPublic(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "pubnonpublic")

	pubThemeSeedPost(t, h, author, pubThemePost{title: "Vault Open Entry", slug: "vault-open",
		content: "<p>quartzmark open</p>", publishedAt: archivesDay(2025, time.August, 1)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Vault Private Entry", slug: "vault-private", visibility: "private",
		content: "<p>quartzmark private</p>", publishedAt: archivesDay(2025, time.August, 2)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Vault Draft Entry", slug: "vault-draft", status: "draft",
		content: "<p>quartzmark draft</p>", publishedAt: archivesDay(2025, time.August, 3)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Vault Future Entry", slug: "vault-future",
		content: "<p>quartzmark future</p>", publishedAt: time.Now().UTC().AddDate(1, 0, 0)})
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Vault Trashed Entry", slug: "vault-trashed", trashed: true,
		content: "<p>quartzmark trashed</p>", publishedAt: archivesDay(2025, time.August, 4)})

	path := "/search?" + url.Values{"q": {"quartzmark"}}.Encode()
	body := archivesPage(t, h, path)
	archivesWantText(t, path, body, "找到 1 篇文章", "Vault Open Entry")
	archivesWantAbsent(t, path, body, "Vault Private Entry", "Vault Draft Entry", "Vault Future Entry", "Vault Trashed Entry")
}

// TestPublicSearchPagination: 12 matches give 10 on page 1 (newest first) and the two oldest on page 2.
func TestPublicSearchPagination(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "pubsearchpage")
	for i := 1; i <= 12; i++ {
		pubThemeSeedPost(t, h, author, pubThemePost{
			title:       fmt.Sprintf("Tile Entry %02d", i),
			slug:        fmt.Sprintf("tile-entry-%02d", i),
			content:     fmt.Sprintf("<p>mosaic note %02d</p>", i),
			publishedAt: archivesDay(2025, time.August, i),
		})
	}

	path := "/search?" + url.Values{"q": {"mosaic"}}.Encode()
	body := archivesPage(t, h, path)
	archivesWantText(t, path, body, "找到 12 篇文章")
	if n := pubThemeCountTitles(body, "Tile Entry", 12); n != 10 {
		t.Errorf("GET %s shows %d of the 12 results, want 10", path, n)
	}
	archivesWantAbsent(t, path, body, "Tile Entry 01", "Tile Entry 02")

	path = "/search?" + url.Values{"q": {"mosaic"}, "page": {"2"}}.Encode()
	body = archivesPage(t, h, path)
	archivesWantText(t, path, body, "找到 12 篇文章", "Tile Entry 01", "Tile Entry 02")
	if n := pubThemeCountTitles(body, "Tile Entry", 12); n != 2 {
		t.Errorf("GET %s shows %d results, want 2", path, n)
	}
	archivesWantAbsent(t, path, body, "Tile Entry 12")
}

// TestPublicSearchEmptyQuery: an empty query is a 200 empty state with no results list and no result count.
func TestPublicSearchEmptyQuery(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "pubemptyq")
	pubThemeSeedPost(t, h, author, pubThemePost{title: "Empty Query Entry", slug: "empty-query-entry",
		content: "<p>emptyquery body</p>", publishedAt: archivesDay(2025, time.September, 1)})

	path := "/search?q="
	body := archivesPage(t, h, path)
	archivesWantAbsent(t, path, body, "Empty Query Entry")
	if pubThemeFoundCount.MatchString(body) {
		t.Errorf("GET %s shows a result count, want none for an empty query", path)
	}
}
