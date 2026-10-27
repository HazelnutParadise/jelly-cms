package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestSearchLifecycleRevisionRestoreKeepsSearchTextInSync(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "search-lifecycle-revision")
	token := h.token(admin)
	id := revTestCreate(t, h, token, map[string]any{"title": "Revision Restored Search", "content": "revision-needle-original", "status": "published"})
	revisions := revTestList(t, h, token, id)
	if len(revisions) != 1 {
		t.Fatalf("initial revisions=%d, want 1", len(revisions))
	}
	revTestPut(t, h, token, id, map[string]any{"title": "Revision Current Search", "content": "revision-needle-current", "status": "published"})
	if got := searchLifecycleTitles(t, h, "revision-needle-current"); !strings.Contains(got, "Revision Current Search") {
		t.Fatalf("current text missing from search: %s", got)
	}
	r := h.request(http.MethodPost, fmt.Sprintf("/api/admin/posts/%d/revisions/%d/restore", id, revisions[0].ID), "", token, "application/json", "")
	if r.code != http.StatusOK {
		t.Fatalf("restore revision status=%d body=%q", r.code, r.body)
	}
	if got := searchLifecycleTitles(t, h, "revision-needle-original"); !strings.Contains(got, "Revision Restored Search") {
		t.Fatalf("restored source missing from search: %s", got)
	}
	if got := searchLifecycleTitles(t, h, "revision-needle-current"); strings.Contains(got, "Revision Current Search") {
		t.Fatalf("superseded source remains searchable after restore: %s", got)
	}
}

func TestSearchLifecycleMediaRenameUpdatesVisibleURLIndex(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "search-lifecycle-media")
	now := time.Now().UTC()
	post := core.Post{Title: "Media Rename Search", Slug: "media-rename-search", Content: "<p>https://old-media.example.test/assets/lifecycle-marker.jpg</p>", Type: "post", Status: "published", Visibility: "public", AuthorID: author.ID, PublishedAt: &now}
	if err := h.db.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	const oldURL = "https://old-media.example.test/assets/lifecycle-marker.jpg"
	const newURL = "https://new-media.example.test/assets/lifecycle-marker.jpg"
	deleted := core.Post{Title: "Deleted Media Search", Slug: "deleted-media-search", Content: "<p>" + oldURL + "</p>", Excerpt: oldURL, Source: oldURL, Type: "post", Status: "published", Visibility: "public", AuthorID: author.ID, PublishedAt: &now}
	if err := h.db.Create(&deleted).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.db.Delete(&deleted).Error; err != nil {
		t.Fatal(err)
	}
	contentOnly := core.Post{Title: "Media Content Null Excerpt", Slug: "media-content-null-excerpt", Content: "<p>" + oldURL + "</p>", Type: "post", Status: "published", Visibility: "private", AuthorID: author.ID, PublishedAt: &now}
	excerptOnly := core.Post{Title: "Media Excerpt Null Content", Slug: "media-excerpt-null-content", Excerpt: oldURL, Type: "post", Status: "published", Visibility: "private", AuthorID: author.ID, PublishedAt: &now}
	for _, fixture := range []*core.Post{&contentOnly, &excerptOnly} {
		if err := h.db.Create(fixture).Error; err != nil {
			t.Fatal(err)
		}
	}
	const preservedAtText = "2020-01-02 03:04:05+00"
	for _, fixture := range []struct {
		id         uint
		nullColumn string
	}{{contentOnly.ID, "excerpt"}, {excerptOnly.ID, "content"}} {
		if err := h.db.Exec("UPDATE posts SET "+fixture.nullColumn+" = NULL, updated_at = ? WHERE id = ?", preservedAtText, fixture.id).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := h.db.Exec("UPDATE posts SET updated_at = ? WHERE id = ?", preservedAtText, deleted.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(searchLifecycleTitles(t, h, "old-media.example.test"), post.Title) {
		t.Fatal("old visible URL was not initially searchable")
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error { return rewriteKnownMediaReferences(tx, oldURL, newURL) }); err != nil {
		t.Fatalf("rewrite media reference: %v", err)
	}
	if got := searchLifecycleTitles(t, h, "new-media.example.test"); !strings.Contains(got, post.Title) {
		t.Fatalf("new visible URL missing from search: %s", got)
	}
	if got := searchLifecycleTitles(t, h, "old-media.example.test"); strings.Contains(got, post.Title) {
		t.Fatalf("old visible URL remains searchable: %s", got)
	}
	var stored core.Post
	if err := h.db.First(&stored, post.ID).Error; err != nil || !strings.Contains(stored.Content, newURL) {
		t.Fatalf("post content after media rewrite=%q err=%v", stored.Content, err)
	}
	var preserved struct {
		ID                       uint
		Content, Excerpt, Source string
		DeletedAt                *time.Time
		UpdatedAt                time.Time
	}
	if err := h.db.Unscoped().Raw("SELECT id, content, excerpt, source, deleted_at, updated_at FROM posts WHERE id = ?", deleted.ID).Scan(&preserved).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preserved.Content, newURL) || preserved.Excerpt != newURL || preserved.Source != newURL || preserved.DeletedAt == nil {
		t.Fatalf("soft-deleted media references were not all rewritten: %+v", preserved)
	}
	if !preserved.UpdatedAt.Equal(time.Date(2020, time.January, 2, 3, 4, 5, 0, time.UTC)) {
		t.Errorf("media rewrite changed soft-deleted updated_at: %s", preserved.UpdatedAt)
	}
	for _, fixture := range []struct {
		post                   core.Post
		wantColumn, nullColumn string
	}{{contentOnly, "content", "excerpt"}, {excerptOnly, "excerpt", "content"}} {
		var source, index string
		var nullCount int
		query := "SELECT " + fixture.wantColumn + ", search_" + fixture.wantColumn + ", (" + fixture.nullColumn + " IS NULL)::int FROM posts WHERE id = ?"
		if err := h.db.Raw(query, fixture.post.ID).Row().Scan(&source, &index, &nullCount); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(source, newURL) || !strings.Contains(index, "new-media.example.test") {
			t.Errorf("%s source/index after rename = %q / %q", fixture.wantColumn, source, index)
		}
		if nullCount != 1 {
			t.Errorf("untouched %s became NULL=%t, want it to remain SQL NULL", fixture.nullColumn, nullCount == 1)
		}
	}
}

func TestSearchLifecycleProviderIDsKeepRankPublicFilteringAndBoundedPageReads(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "search-lifecycle-provider")
	ids := make([]uint, 0, 215)
	now := time.Now().UTC()
	for i := 0; i < 215; i++ {
		post := core.Post{Title: fmt.Sprintf("Provider Search Result %03d", i), Slug: fmt.Sprintf("provider-search-%03d", i), Content: "provider-shared-search-term", Type: "post", Status: "published", Visibility: "public", AuthorID: author.ID, PublishedAt: &now}
		if err := h.db.Create(&post).Error; err != nil {
			t.Fatalf("create provider fixture %d: %v", i, err)
		}
		ids = append(ids, post.ID)
	}
	for _, idx := range []int{10, 20, 30, 40, 50, 60, 70, 80, 90, 100} {
		if err := h.db.Model(&core.Post{}).Where("id = ?", ids[idx]).Update("visibility", "private").Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := h.db.Model(&core.Post{}).Where("id = ?", ids[110]).Update("published_at", time.Now().Add(48*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.db.Delete(&core.Post{}, ids[120]).Error; err != nil {
		t.Fatal(err)
	}
	providerIDs := append([]uint(nil), ids...)
	providerIDs = append(providerIDs, ids[0], 0) // duplicate IDs are normally removed before this helper; zero is invalid provider input.
	var loaded int64
	const callback = "search_lifecycle_count_posts_loaded"
	if err := h.db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == "posts" && tx.Error == nil {
			loaded += tx.RowsAffected
		}
	}); err != nil {
		t.Fatalf("register post row counter: %v", err)
	}
	defer func() { _ = h.db.Callback().Query().Remove(callback) }()
	for _, page := range []int{1, 2, 3} {
		loaded = 0
		results, total, err := searchPublicPostsByIDs(t.Context(), h.db, theme.PrepareSearchQuery("provider-shared-search-term"), providerIDs[:len(providerIDs)-1], page, 75)
		if err != nil {
			t.Fatalf("provider ID search page %d: %v", page, err)
		}
		if total != 203 {
			t.Fatalf("provider total page %d=%d, want 203 public matches", page, total)
		}
		want := 75
		if left := int(total) - (page-1)*75; left < want {
			want = left
		}
		if len(results) != want {
			t.Fatalf("provider page %d rows=%d, want %d", page, len(results), want)
		}
		expected := make([]uint, 0, len(ids))
		for i, id := range ids {
			if (i >= 10 && i <= 100 && i%10 == 0) || i == 110 || i == 120 {
				continue
			}
			expected = append(expected, id)
		}
		for i, got := range results {
			if wantID := expected[(page-1)*75+i]; got.Post.ID != wantID {
				t.Fatalf("provider page %d rank %d returned ID %d, want %d", page, (page-1)*75+i, got.Post.ID, wantID)
			}
		}
		if len(results) > 200 {
			t.Fatalf("provider query loaded %d page rows, above 200", len(results))
		}
		if loaded > 200 {
			t.Fatalf("provider page %d loaded %d posts into Go, want at most 200", page, loaded)
		}
		encoded, err := json.Marshal(results)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "search_title") || strings.Contains(string(encoded), "search_content") || strings.Contains(string(encoded), "search_excerpt") {
			t.Fatalf("derived search data leaked from provider result JSON: %s", encoded)
		}
	}
}

func TestSearchLifecyclePageCountAndResultsSharePublicationCutoff(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "search-lifecycle-cutoff")
	query := "cutoff-shared-scope-needle"
	post := core.Post{
		Title: "Cutoff Shared Scope Result", Slug: "cutoff-shared-scope-result",
		Content: "<p>" + query + "</p>", Type: "post", Status: "published", Visibility: "public",
		AuthorID: author.ID,
	}
	initiallyPublished := time.Now().UTC().Add(-time.Hour)
	post.PublishedAt = &initiallyPublished
	if err := h.db.Create(&post).Error; err != nil {
		t.Fatalf("create cutoff fixture: %v", err)
	}

	var callbackRan bool
	var cutoffFound bool
	const callback = "search_lifecycle_set_post_after_first_count_cutoff"
	if err := h.db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if callbackRan || tx.Statement == nil || tx.Statement.Table != "posts" {
			return
		}
		if _, isCount := tx.Statement.Dest.(*int64); !isCount {
			return
		}
		callbackRan = true
		where, ok := tx.Statement.Clauses["WHERE"].Expression.(clause.Where)
		if !ok {
			return
		}
		for _, expression := range where.Exprs {
			if cutoff, ok := searchLifecyclePublicationCutoff(expression); ok {
				cutoffFound = true
				// PostgreSQL stores timestamps at microsecond precision. Put the
				// post exactly one representable tick beyond Count's captured cutoff.
				publishedAt := cutoff.Truncate(time.Microsecond).Add(time.Microsecond)
				if err := h.db.Session(&gorm.Session{NewDB: true}).Model(&core.Post{}).
					Where("id = ?", post.ID).Update("published_at", publishedAt).Error; err != nil {
					t.Errorf("move post just beyond count cutoff: %v", err)
				}
				return
			}
		}
	}); err != nil {
		t.Fatalf("register cutoff callback: %v", err)
	}
	defer func() { _ = h.db.Callback().Query().Remove(callback) }()

	page := followupHTMLGet(h, "/search?q="+url.QueryEscape(query))
	if page.code != http.StatusOK {
		t.Fatalf("GET /search status=%d body=%q", page.code, archivesSnippet(page.body))
	}
	if !callbackRan || !cutoffFound {
		t.Fatalf("cutoff callback ran=%t cutoff-found=%t; expected the posts Count predicate before SQL", callbackRan, cutoffFound)
	}
	if got := followupFoundCount(page.body); got != 0 {
		t.Errorf("GET /search reported %d matches, want 0 at the shared cutoff", got)
	}
	doc := followupParse(t, page.body)
	if cards := followupByClass(doc, "search-hit"); len(cards) != 0 {
		t.Errorf("GET /search rendered %d result cards while reporting zero matches", len(cards))
	}
	if strings.Contains(page.body, post.Title) {
		t.Errorf("GET /search included the just-future post %q", post.Title)
	}
}

func searchLifecyclePublicationCutoff(expression clause.Expression) (time.Time, bool) {
	switch e := expression.(type) {
	case clause.Expr:
		if strings.Contains(strings.ToLower(e.SQL), "coalesce(posts.published_at, posts.created_at)") && strings.Contains(e.SQL, "<= ?") {
			for _, value := range e.Vars {
				if cutoff, ok := value.(time.Time); ok {
					return cutoff, true
				}
			}
		}
	case clause.AndConditions:
		for _, nested := range e.Exprs {
			if cutoff, ok := searchLifecyclePublicationCutoff(nested); ok {
				return cutoff, true
			}
		}
	case clause.OrConditions:
		for _, nested := range e.Exprs {
			if cutoff, ok := searchLifecyclePublicationCutoff(nested); ok {
				return cutoff, true
			}
		}
	case clause.NotConditions:
		for _, nested := range e.Exprs {
			if cutoff, ok := searchLifecyclePublicationCutoff(nested); ok {
				return cutoff, true
			}
		}
	case clause.Where:
		for _, nested := range e.Exprs {
			if cutoff, ok := searchLifecyclePublicationCutoff(nested); ok {
				return cutoff, true
			}
		}
	}
	return time.Time{}, false
}

func searchLifecycleTitles(t *testing.T, h *accessHarness, query string) string {
	t.Helper()
	r := followupJSONGet(h, "/api/search?q="+url.QueryEscape(query))
	if r.code != http.StatusOK {
		t.Fatalf("search %q status=%d body=%q", query, r.code, archivesSnippet(r.body))
	}
	return r.body
}
