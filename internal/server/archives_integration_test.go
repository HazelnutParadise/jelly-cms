package server

// Archive integration tests for ticket 04 slot C5 (public archive pages). The contract is the header
// comment of handlers_archives.go. They are written before the implementation exists and are expected
// to fail (the handlers return 501 from notImplemented) until the archive handlers and templates are
// implemented. Do not edit them to match an implementation. Fixtures are seeded directly into the
// isolated PostgreSQL database; the behavior under test always goes through HTTP. Run with
// JELLY_TEST_POSTGRES=1, otherwise the harness skips.
//
// Assertions check status codes and rendered text only (term names, post titles, usernames). Titles
// are chosen so that none is a substring of another, and every fixture date is at noon UTC so month
// boundaries do not depend on the local time zone.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

// archivesPost describes one seeded post. An empty status means published, an empty visibility
// means public. trashed soft-deletes the post after it is created.
type archivesPost struct {
	title       string
	status      string
	visibility  string
	pinned      bool
	trashed     bool
	publishedAt time.Time
	categories  []core.Category
	tags        []core.Tag
}

func archivesDay(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 12, 0, 0, 0, time.UTC)
}

// archivesHarness returns the shared access harness with its routes rebuilt on a theme manager that has
// the "default" theme active, as cmd/server/main.go does. The shared harness never activates a theme, so
// its public HTML routes would answer 500 "no active theme". Only this file's tests use the rebuilt echo.
func archivesHarness(t *testing.T) *accessHarness {
	t.Helper()
	h := newAccessHarness(t)
	tm := theme.NewManager("web/themes")
	if err := tm.Activate("default"); err != nil {
		t.Fatalf("activate default theme: %v", err)
	}
	e := echo.New()
	RegisterRoutes(e, tm, nil)
	h.echo = e
	return h
}

func archivesSeedCategory(t *testing.T, h *accessHarness, name, slug string, parentID *uint, hideFromList, noCascade bool) core.Category {
	t.Helper()
	row := core.Category{Name: name, Slug: slug, ParentID: parentID, HideFromList: hideFromList, NoCascade: noCascade}
	if err := h.db.Create(&row).Error; err != nil {
		t.Fatalf("seed category %q: %v", slug, err)
	}
	return row
}

func archivesSeedTag(t *testing.T, h *accessHarness, name, slug string) core.Tag {
	t.Helper()
	row := core.Tag{Name: name, Slug: slug}
	if err := h.db.Create(&row).Error; err != nil {
		t.Fatalf("seed tag %q: %v", slug, err)
	}
	return row
}

func archivesSeedPost(t *testing.T, h *accessHarness, author core.User, spec archivesPost) core.Post {
	t.Helper()
	status := spec.status
	if status == "" {
		status = "published"
	}
	visibility := spec.visibility
	if visibility == "" {
		visibility = "public"
	}
	published := spec.publishedAt
	post := core.Post{
		Title:       spec.title,
		Slug:        core.Slugify(spec.title, "archive-post"),
		Content:     "seed body",
		Type:        "post",
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
	for i := range spec.tags {
		if err := h.db.Model(&post).Association("Tags").Append(&spec.tags[i]); err != nil {
			t.Fatalf("assign tag %q to post %q: %v", spec.tags[i].Slug, spec.title, err)
		}
	}
	if spec.trashed {
		if err := h.db.Delete(&post).Error; err != nil {
			t.Fatalf("trash post %q: %v", spec.title, err)
		}
	}
	return post
}

// archivesSeedSite builds one shared fixture:
//
//	Atlas Tech (root)
//	  Gopher Lab (child)
//	    Unit Bench (grandchild)
//	  Side Garden (child, NoCascade)
//	Hidden Shelf (root, HideFromList)
//	Kitchen Notes (root)
//
// plus tags Rust Lantern and Quiet Mint, and authors archwriter and archguest.
func archivesSeedSite(t *testing.T, h *accessHarness) {
	t.Helper()
	writer := h.user("author", "archwriter")
	guest := h.user("author", "archguest")

	atlas := archivesSeedCategory(t, h, "Atlas Tech", "atlas-tech", nil, false, false)
	gopher := archivesSeedCategory(t, h, "Gopher Lab", "gopher-lab", &atlas.ID, false, false)
	unit := archivesSeedCategory(t, h, "Unit Bench", "unit-bench", &gopher.ID, false, false)
	garden := archivesSeedCategory(t, h, "Side Garden", "side-garden", &atlas.ID, false, true)
	hidden := archivesSeedCategory(t, h, "Hidden Shelf", "hidden-shelf", nil, true, false)
	kitchen := archivesSeedCategory(t, h, "Kitchen Notes", "kitchen-notes", nil, false, false)

	rust := archivesSeedTag(t, h, "Rust Lantern", "rust-lantern")
	quiet := archivesSeedTag(t, h, "Quiet Mint", "quiet-mint")

	// Atlas and its descendants.
	archivesSeedPost(t, h, writer, archivesPost{title: "Atlas Pinned Entry", pinned: true,
		publishedAt: archivesDay(2025, time.January, 1), categories: []core.Category{atlas}})
	archivesSeedPost(t, h, writer, archivesPost{title: "Unit Older Entry",
		publishedAt: archivesDay(2025, time.February, 1), categories: []core.Category{unit}})
	archivesSeedPost(t, h, writer, archivesPost{title: "Gopher Newest Entry",
		publishedAt: archivesDay(2025, time.March, 1), categories: []core.Category{gopher}})
	// Same post in two categories: must appear once on Atlas and once on Gopher Lab.
	archivesSeedPost(t, h, writer, archivesPost{title: "Multi Home Entry",
		publishedAt: archivesDay(2025, time.February, 15), categories: []core.Category{atlas, gopher}})

	// Posts on Atlas (or a descendant) that must not be listed.
	archivesSeedPost(t, h, writer, archivesPost{title: "Atlas Draft Secret", status: "draft",
		publishedAt: archivesDay(2025, time.April, 1), categories: []core.Category{atlas}})
	archivesSeedPost(t, h, writer, archivesPost{title: "Atlas Private Secret", visibility: "private",
		publishedAt: archivesDay(2025, time.April, 2), categories: []core.Category{atlas}})
	archivesSeedPost(t, h, writer, archivesPost{title: "Atlas Trashed Secret", trashed: true,
		publishedAt: archivesDay(2025, time.April, 3), categories: []core.Category{atlas}})
	archivesSeedPost(t, h, writer, archivesPost{title: "Atlas Future Secret",
		publishedAt: time.Now().UTC().Add(30 * 24 * time.Hour), categories: []core.Category{atlas}})

	// NoCascade descendant: its own page lists it, Atlas does not.
	archivesSeedPost(t, h, writer, archivesPost{title: "Garden Only Entry",
		publishedAt: archivesDay(2025, time.April, 4), categories: []core.Category{garden}})

	// Other term, and the hidden category.
	archivesSeedPost(t, h, writer, archivesPost{title: "Kitchen Plain Entry",
		publishedAt: archivesDay(2025, time.April, 5), categories: []core.Category{kitchen}})
	archivesSeedPost(t, h, writer, archivesPost{title: "Vault Entry Plain",
		publishedAt: archivesDay(2025, time.May, 1), categories: []core.Category{hidden}})

	// Tags: Rust Lantern has Alpha, Bravo and a draft; Quiet Mint has Bravo and Other.
	archivesSeedPost(t, h, writer, archivesPost{title: "Lantern Tagged Alpha",
		publishedAt: archivesDay(2025, time.June, 1), tags: []core.Tag{rust}})
	archivesSeedPost(t, h, writer, archivesPost{title: "Lantern Tagged Bravo",
		publishedAt: archivesDay(2025, time.June, 2), tags: []core.Tag{rust, quiet}})
	archivesSeedPost(t, h, writer, archivesPost{title: "Lantern Other Entry",
		publishedAt: archivesDay(2025, time.June, 3), tags: []core.Tag{quiet}})
	archivesSeedPost(t, h, writer, archivesPost{title: "Lantern Draft Secret", status: "draft",
		publishedAt: archivesDay(2025, time.June, 4), tags: []core.Tag{rust}})

	// Authors: archwriter has Alpha, Beta and a draft; archguest has Gamma.
	archivesSeedPost(t, h, writer, archivesPost{title: "Writer Byline Alpha",
		publishedAt: archivesDay(2025, time.July, 1)})
	archivesSeedPost(t, h, writer, archivesPost{title: "Writer Byline Beta",
		publishedAt: archivesDay(2025, time.July, 2)})
	archivesSeedPost(t, h, writer, archivesPost{title: "Writer Draft Secret", status: "draft",
		publishedAt: archivesDay(2025, time.July, 3)})
	archivesSeedPost(t, h, guest, archivesPost{title: "Guest Byline Gamma",
		publishedAt: archivesDay(2025, time.July, 4)})
}

// archivesSeedPager seeds Pager Shelf with 13 published posts, newest first (Alpha) to oldest (Mike).
func archivesSeedPager(t *testing.T, h *accessHarness) []string {
	t.Helper()
	author := h.user("author", "archpager")
	shelf := archivesSeedCategory(t, h, "Pager Shelf", "pager-shelf", nil, false, false)
	names := []string{"Alpha", "Bravo", "Charlie", "Delta", "Echo", "Foxtrot", "Golf", "Hotel", "India", "Juliet", "Kilo", "Lima", "Mike"}
	titles := make([]string, 0, len(names))
	for i, name := range names {
		title := "Pager Entry " + name
		titles = append(titles, title)
		archivesSeedPost(t, h, author, archivesPost{title: title,
			publishedAt: archivesDay(2025, time.September, 30-i), categories: []core.Category{shelf}})
	}
	return titles
}

// archivesSeedDozen seeds Dozen Shelf with exactly 12 published posts.
func archivesSeedDozen(t *testing.T, h *accessHarness) []string {
	t.Helper()
	author := h.user("author", "archdozen")
	shelf := archivesSeedCategory(t, h, "Dozen Shelf", "dozen-shelf", nil, false, false)
	titles := make([]string, 0, 12)
	for i := 0; i < 12; i++ {
		title := fmt.Sprintf("Dozen Entry %02d", i+1)
		titles = append(titles, title)
		archivesSeedPost(t, h, author, archivesPost{title: title,
			publishedAt: archivesDay(2025, time.August, i+1), categories: []core.Category{shelf}})
	}
	return titles
}

// archivesSeedMonths seeds two posts in May 2025, one draft in May 2025 and one post in June 2025.
func archivesSeedMonths(t *testing.T, h *accessHarness) {
	t.Helper()
	author := h.user("author", "archmonth")
	archivesSeedPost(t, h, author, archivesPost{title: "Spring Mark Early", publishedAt: archivesDay(2025, time.May, 3)})
	archivesSeedPost(t, h, author, archivesPost{title: "Spring Mark Late", publishedAt: archivesDay(2025, time.May, 28)})
	archivesSeedPost(t, h, author, archivesPost{title: "Spring Mark Draft Secret", status: "draft",
		publishedAt: archivesDay(2025, time.May, 15)})
	archivesSeedPost(t, h, author, archivesPost{title: "Summer Mark Entry", publishedAt: archivesDay(2025, time.June, 12)})
}

func archivesGet(t *testing.T, h *accessHarness, path string) accessResult {
	t.Helper()
	r := h.request(http.MethodGet, path, "", "", echo.MIMETextHTML, "")
	if r.panicked != nil {
		t.Fatalf("GET %s panicked: %v", path, r.panicked)
	}
	return r
}

func archivesSnippet(body string) string {
	if len(body) > 200 {
		return body[:200]
	}
	return body
}

func archivesWantStatus(t *testing.T, path string, r accessResult, want int) {
	t.Helper()
	if r.code != want {
		t.Fatalf("GET %s status = %d, want %d; body=%q", path, r.code, want, archivesSnippet(r.body))
	}
}

// archivesPage fetches path, requires 200 and returns the body.
func archivesPage(t *testing.T, h *accessHarness, path string) string {
	t.Helper()
	r := archivesGet(t, h, path)
	archivesWantStatus(t, path, r, http.StatusOK)
	return r.body
}

func archivesWantText(t *testing.T, path, body string, want ...string) {
	t.Helper()
	for _, s := range want {
		if !strings.Contains(body, s) {
			t.Errorf("GET %s: body does not contain %q", path, s)
		}
	}
}

func archivesWantAbsent(t *testing.T, path, body string, unwanted ...string) {
	t.Helper()
	for _, s := range unwanted {
		if strings.Contains(body, s) {
			t.Errorf("GET %s: body must not contain %q", path, s)
		}
	}
}

// archivesWantOrder requires both texts to appear and first to appear before second.
func archivesWantOrder(t *testing.T, path, body, first, second string) {
	t.Helper()
	i, j := strings.Index(body, first), strings.Index(body, second)
	if i < 0 || j < 0 {
		t.Errorf("GET %s: order check needs %q and %q in the body", path, first, second)
		return
	}
	if i > j {
		t.Errorf("GET %s: %q must appear before %q", path, first, second)
	}
}

func TestArchivesCategoryListsDescendantPosts(t *testing.T) {
	h := archivesHarness(t)
	archivesSeedSite(t, h)

	path := "/c/atlas-tech"
	body := archivesPage(t, h, path)
	archivesWantText(t, path, body,
		"Atlas Tech",
		"Atlas Pinned Entry",
		"Gopher Newest Entry", // child category
		"Unit Older Entry",    // grandchild category
		"Multi Home Entry",    // post in Atlas and Gopher Lab
	)
}

func TestArchivesCategoryPinnedFirstThenNewest(t *testing.T) {
	h := archivesHarness(t)
	archivesSeedSite(t, h)

	path := "/c/atlas-tech"
	body := archivesPage(t, h, path)
	// Atlas Pinned Entry is the oldest post but pinned, so it comes first.
	archivesWantOrder(t, path, body, "Atlas Pinned Entry", "Gopher Newest Entry")
	archivesWantOrder(t, path, body, "Gopher Newest Entry", "Multi Home Entry")
	archivesWantOrder(t, path, body, "Multi Home Entry", "Unit Older Entry")
}

func TestArchivesCategoryExcludesNonPublicPosts(t *testing.T) {
	h := archivesHarness(t)
	archivesSeedSite(t, h)

	path := "/c/atlas-tech"
	body := archivesPage(t, h, path)
	archivesWantText(t, path, body, "Atlas Pinned Entry")
	archivesWantAbsent(t, path, body,
		"Atlas Draft Secret",   // draft
		"Atlas Private Secret", // visibility private
		"Atlas Trashed Secret", // soft deleted
		"Atlas Future Secret",  // published_at in the future
		"Kitchen Plain Entry",  // other term
		"Vault Entry Plain",    // hidden category is not a descendant of Atlas
	)
}

func TestArchivesCategoryUnknownSlugIsNotFound(t *testing.T) {
	h := archivesHarness(t)
	archivesSeedSite(t, h)
	archivesSeedTag(t, h, "Tag Only Name", "tag-only-slug")

	for _, path := range []string{"/c/no-such-category", "/c/tag-only-slug"} {
		archivesWantStatus(t, path, archivesGet(t, h, path), http.StatusNotFound)
	}
}

func TestArchivesCategoryNoCascadeDescendantExcluded(t *testing.T) {
	h := archivesHarness(t)
	archivesSeedSite(t, h)

	atlasPath := "/c/atlas-tech"
	archivesWantAbsent(t, atlasPath, archivesPage(t, h, atlasPath), "Garden Only Entry")

	gardenPath := "/c/side-garden"
	archivesWantText(t, gardenPath, archivesPage(t, h, gardenPath), "Side Garden", "Garden Only Entry")
}

func TestArchivesHideFromListCategoryReachable(t *testing.T) {
	h := archivesHarness(t)
	archivesSeedSite(t, h)

	path := "/c/hidden-shelf"
	archivesWantText(t, path, archivesPage(t, h, path), "Hidden Shelf", "Vault Entry Plain")
}

func TestArchivesCategoryPostInTwoCategoriesAppearsOnce(t *testing.T) {
	h := archivesHarness(t)
	archivesSeedSite(t, h)

	for _, path := range []string{"/c/atlas-tech", "/c/gopher-lab"} {
		body := archivesPage(t, h, path)
		if n := strings.Count(body, "Multi Home Entry"); n != 1 {
			t.Errorf("GET %s: %q appears %d times, want 1", path, "Multi Home Entry", n)
		}
	}
}

func TestArchivesCategoryPagination(t *testing.T) {
	h := archivesHarness(t)
	titles := archivesSeedPager(t, h)
	newest, oldest := titles[0], titles[len(titles)-1]
	firstPage := titles[:12]

	// Page 1: the 12 newest; the oldest is on page 2.
	path := "/c/pager-shelf"
	body := archivesPage(t, h, path)
	archivesWantText(t, path, body, firstPage...)
	archivesWantAbsent(t, path, body, oldest)

	// Page 2: exactly the one remaining post.
	path = "/c/pager-shelf?page=2"
	body = archivesPage(t, h, path)
	archivesWantText(t, path, body, oldest)
	archivesWantAbsent(t, path, body, firstPage...)

	// Past the end.
	path = "/c/pager-shelf?page=3"
	archivesWantStatus(t, path, archivesGet(t, h, path), http.StatusNotFound)

	// Invalid page numbers are treated as page 1.
	for _, path := range []string{"/c/pager-shelf?page=0", "/c/pager-shelf?page=abc"} {
		body := archivesPage(t, h, path)
		archivesWantText(t, path, body, newest)
		archivesWantAbsent(t, path, body, oldest)
	}
}

func TestArchivesCategoryExactlyTwelvePostsPageTwoNotFound(t *testing.T) {
	h := archivesHarness(t)
	titles := archivesSeedDozen(t, h)

	path := "/c/dozen-shelf"
	archivesWantText(t, path, archivesPage(t, h, path), titles...)

	path = "/c/dozen-shelf?page=2"
	archivesWantStatus(t, path, archivesGet(t, h, path), http.StatusNotFound)
}

func TestArchivesTagPageListsItsPosts(t *testing.T) {
	h := archivesHarness(t)
	archivesSeedSite(t, h)

	path := "/t/rust-lantern"
	body := archivesPage(t, h, path)
	archivesWantText(t, path, body, "Rust Lantern", "Lantern Tagged Alpha", "Lantern Tagged Bravo")
	archivesWantAbsent(t, path, body, "Lantern Other Entry", "Lantern Draft Secret")

	archivesWantStatus(t, "/t/no-such-tag", archivesGet(t, h, "/t/no-such-tag"), http.StatusNotFound)
	// A category slug is not a tag.
	archivesWantStatus(t, "/t/atlas-tech", archivesGet(t, h, "/t/atlas-tech"), http.StatusNotFound)
}

func TestArchivesAuthorPageListsTheirPosts(t *testing.T) {
	h := archivesHarness(t)
	archivesSeedSite(t, h)

	path := "/author/archwriter"
	body := archivesPage(t, h, path)
	archivesWantText(t, path, body, "Writer Byline Alpha", "Writer Byline Beta")
	archivesWantAbsent(t, path, body, "Guest Byline Gamma", "Writer Draft Secret")

	path = "/author/archguest"
	body = archivesPage(t, h, path)
	archivesWantText(t, path, body, "Guest Byline Gamma")
	archivesWantAbsent(t, path, body, "Writer Byline Alpha")
}

func TestArchivesAuthorUnknownUsernameIsNotFound(t *testing.T) {
	h := archivesHarness(t)
	archivesSeedSite(t, h)

	archivesWantStatus(t, "/author/no-such-writer", archivesGet(t, h, "/author/no-such-writer"), http.StatusNotFound)
}

func TestArchivesIndexRendersVisibleTermsAndAuthors(t *testing.T) {
	h := archivesHarness(t)
	archivesSeedSite(t, h)

	path := "/archive"
	archivesWantText(t, path, archivesPage(t, h, path),
		"Atlas Tech",
		"Gopher Lab",
		"Unit Bench",
		"Side Garden",
		"Kitchen Notes",
		"Rust Lantern",
		"Quiet Mint",
		"archwriter",
		"archguest",
	)
}

func TestArchivesIndexExcludesHiddenCategories(t *testing.T) {
	h := archivesHarness(t)
	archivesSeedSite(t, h)

	path := "/archive"
	body := archivesPage(t, h, path)
	archivesWantText(t, path, body, "Atlas Tech")
	archivesWantAbsent(t, path, body, "Hidden Shelf", "/c/hidden-shelf")
}

func TestArchivesMonthListsPostsPublishedInThatMonth(t *testing.T) {
	h := archivesHarness(t)
	archivesSeedMonths(t, h)

	path := "/archive/2025/5"
	body := archivesPage(t, h, path)
	archivesWantText(t, path, body, "Spring Mark Early", "Spring Mark Late")
	archivesWantAbsent(t, path, body, "Summer Mark Entry", "Spring Mark Draft Secret")

	path = "/archive/2025/6"
	body = archivesPage(t, h, path)
	archivesWantText(t, path, body, "Summer Mark Entry")
	archivesWantAbsent(t, path, body, "Spring Mark Early")
}

func TestArchivesMonthInvalidMonthIsNotFound(t *testing.T) {
	h := archivesHarness(t)
	archivesSeedMonths(t, h)

	for _, path := range []string{"/archive/2025/0", "/archive/2025/13", "/archive/2025/abc"} {
		archivesWantStatus(t, path, archivesGet(t, h, path), http.StatusNotFound)
	}
}

// The /:slug catch-all must keep serving published pages after the archive routes are registered.
func TestArchivesPageCatchAllStillServesPages(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "archguard")
	published := archivesDay(2025, time.March, 10)
	page := core.Post{
		Title:       "Guard Page Heading",
		Slug:        "guard-archive-page",
		Content:     "<p>guard body marker</p>",
		Type:        "page",
		Status:      "published",
		Visibility:  "public",
		AuthorID:    author.ID,
		PublishedAt: &published,
		CreatedAt:   published,
		UpdatedAt:   published,
	}
	if err := h.db.Create(&page).Error; err != nil {
		t.Fatalf("seed page: %v", err)
	}

	path := "/guard-archive-page"
	archivesWantText(t, path, archivesPage(t, h, path), "guard body marker")
}
