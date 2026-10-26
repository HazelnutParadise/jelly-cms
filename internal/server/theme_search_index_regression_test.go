package server

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"gorm.io/gorm"
)

// These HTTP-level checks ensure that public search stays in sync with the
// persisted, private folded columns across the supported GORM write paths.
func TestSearchIndexRegressionWritePaths(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "search-index-write-paths")
	fixtureTime := func() time.Time { return time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC) }
	seed := func(title, content, excerpt string) core.Post {
		t.Helper()
		published := fixtureTime()
		p := core.Post{Title: title, Slug: strings.ToLower(strings.ReplaceAll(title, " ", "-")), Content: content,
			Excerpt: excerpt, Type: "post", Status: "published", Visibility: "public", AuthorID: author.ID,
			PublishedAt: &published, CreatedAt: published, UpdatedAt: published}
		if err := h.db.Create(&p).Error; err != nil {
			t.Fatalf("create post %q: %v", title, err)
		}
		return p
	}
	assertSearch := func(query, title string, want bool) {
		t.Helper()
		r := followupJSONGet(h, "/api/search?q="+url.QueryEscape(query))
		if r.code != 200 {
			t.Fatalf("search %q status=%d body=%q", query, r.code, archivesSnippet(r.body))
		}
		var body struct {
			Posts []struct {
				Title string `json:"title"`
			} `json:"posts"`
		}
		if err := json.Unmarshal([]byte(r.body), &body); err != nil {
			t.Fatalf("decode search response: %v", err)
		}
		found := false
		for _, post := range body.Posts {
			if post.Title == title {
				found = true
			}
		}
		if found != want {
			t.Errorf("search %q contains %q = %v, want %v (response %s)", query, title, found, want, archivesSnippet(r.body))
		}
	}
	assertNoOldTerms := func(title string, terms ...string) {
		t.Helper()
		for _, term := range terms {
			assertSearch(term, title, false)
		}
	}

	// CreateInBatches is used by imports and must populate the same search data.
	batchTime1, batchTime2 := fixtureTime(), fixtureTime()
	batch := []core.Post{
		{Title: "Batch Search Title", Slug: "batch-search-title", Content: `<p>batch <b>body</b></p>`, Excerpt: "batch excerpt", Type: "post", Status: "published", Visibility: "public", AuthorID: author.ID, PublishedAt: &batchTime1, CreatedAt: batchTime1, UpdatedAt: batchTime1},
		{Title: "Batch Search Second", Slug: "batch-search-second", Content: `<p>second batch body</p>`, Type: "post", Status: "published", Visibility: "public", AuthorID: author.ID, PublishedAt: &batchTime2, CreatedAt: batchTime2, UpdatedAt: batchTime2},
	}
	if err := h.db.CreateInBatches(&batch, 1).Error; err != nil {
		t.Fatalf("CreateInBatches: %v", err)
	}
	assertSearch("batch", "Batch Search Title", true)
	assertSearch("body", "Batch Search Title", true)
	assertSearch("excerpt", "Batch Search Title", true)
	// Bulk map updates must refresh every matching row, not only a loaded model.
	if err := h.db.Model(&core.Post{}).Where("slug IN ?", []string{"batch-search-title", "batch-search-second"}).
		Updates(map[string]any{"content": `<p>bulk refreshed body</p>`}).Error; err != nil {
		t.Fatalf("bulk map Updates: %v", err)
	}
	assertSearch("bulk refreshed body", "Batch Search Title", true)
	assertSearch("bulk refreshed body", "Batch Search Second", true)
	assertNoOldTerms("Batch Search Title", "batch body")
	assertNoOldTerms("Batch Search Second", "second batch body")

	selectedCreateTime := fixtureTime()
	selectedCreate := core.Post{Title: "Select Create Title", Slug: "select-create-title", Content: `<p>selected create content</p>`, Excerpt: "omitted select excerpt", Type: "post", Status: "published", Visibility: "public", AuthorID: author.ID, PublishedAt: &selectedCreateTime, CreatedAt: selectedCreateTime, UpdatedAt: selectedCreateTime}
	if err := h.db.Select("title", "slug", "content", "type", "status", "visibility", "author_id", "published_at", "created_at", "updated_at").Create(&selectedCreate).Error; err != nil {
		t.Fatalf("Select Create: %v", err)
	}
	assertSearch("Select Create Title", "Select Create Title", true)
	assertSearch("selected create content", "Select Create Title", true)
	assertSearch("omitted select excerpt", "Select Create Title", false)

	omittedCreateTime := fixtureTime()
	omittedCreate := core.Post{Title: "Omit Create Title", Slug: "omit-create-title", Content: `<p>omitted create content</p>`, Excerpt: "omitted create excerpt", Type: "post", Status: "published", Visibility: "public", AuthorID: author.ID, PublishedAt: &omittedCreateTime, CreatedAt: omittedCreateTime, UpdatedAt: omittedCreateTime}
	if err := h.db.Omit("Content").Create(&omittedCreate).Error; err != nil {
		t.Fatalf("Omit Create: %v", err)
	}
	assertSearch("Omit Create Title", "Omit Create Title", true)
	assertSearch("omitted create excerpt", "Omit Create Title", true)
	assertSearch("omitted create content", "Omit Create Title", false)

	// Struct updates, Select/Omit and map Updates refresh only affected sources.
	structPost := seed("Struct Before", `<p>struct old body</p>`, "struct old excerpt")
	if err := h.db.Model(&structPost).Updates(core.Post{Title: "Struct After", Content: `<p>struct new body</p>`, Excerpt: "struct new excerpt"}).Error; err != nil {
		t.Fatalf("struct Updates: %v", err)
	}
	assertNoOldTerms("Struct After", "Struct Before", "old body", "old excerpt")
	assertSearch("Struct After", "Struct After", true)
	assertSearch("new body", "Struct After", true)
	assertSearch("new excerpt", "Struct After", true)
	if err := h.db.Model(&structPost).Select("Content").Updates(core.Post{Title: "Ignored Selected Title", Content: `<p>selected content</p>`, Excerpt: "ignored selected excerpt"}).Error; err != nil {
		t.Fatalf("Select(Content).Updates: %v", err)
	}
	assertSearch("Struct After", "Struct After", true)
	assertSearch("selected content", "Struct After", true)
	assertNoOldTerms("Struct After", "new body")
	assertSearch("new excerpt", "Struct After", true)
	if err := h.db.Model(&structPost).Omit("Content").Updates(map[string]any{"title": "Omitted Content Title", "content": `<p>must stay selected content</p>`, "excerpt": "omitted content excerpt"}).Error; err != nil {
		t.Fatalf("Omit(Content).Updates: %v", err)
	}
	assertSearch("selected content", "Omitted Content Title", true)
	assertSearch("Omitted Content Title", "Omitted Content Title", true)
	assertSearch("omitted content excerpt", "Omitted Content Title", true)
	assertNoOldTerms("Omitted Content Title", "must stay selected content", "Struct After")

	mapPost := seed("Map Before", `<p>map old body</p>`, "map old excerpt")
	if err := h.db.Model(&mapPost).Updates(map[string]any{"title": "Map After", "content": "", "excerpt": ""}).Error; err != nil {
		t.Fatalf("map Updates clearing values: %v", err)
	}
	assertSearch("Map After", "Map After", true)
	assertNoOldTerms("Map After", "Map Before", "map old body", "map old excerpt")

	// UpdateColumns and UpdateColumn bypass GORM hooks; the custom index callback
	// contract explicitly requires them to keep derived data current.
	columnsPost := seed("Columns Before", `<p>columns old body</p>`, "columns old excerpt")
	if err := h.db.Session(&gorm.Session{SkipHooks: true}).Model(&columnsPost).UpdateColumns(map[string]any{"title": "Columns After", "content": `<p>columns new body</p>`, "excerpt": "columns new excerpt"}).Error; err != nil {
		t.Fatalf("UpdateColumns: %v", err)
	}
	assertSearch("Columns After", "Columns After", true)
	assertSearch("columns new body", "Columns After", true)
	assertSearch("columns new excerpt", "Columns After", true)
	assertNoOldTerms("Columns After", "Columns Before", "columns old body", "columns old excerpt")
	if err := h.db.Model(&columnsPost).UpdateColumn("content", `<p>column singular body</p>`).Error; err != nil {
		t.Fatalf("UpdateColumn: %v", err)
	}
	assertSearch("column singular body", "Columns After", true)
	assertNoOldTerms("Columns After", "columns new body")

	// Save updates a persisted model and a failed transaction must not expose a
	// newly indexed value while the underlying row remains unchanged.
	savePost := seed("Save Before", `<p>save old body</p>`, "")
	savePost.Title, savePost.Content = "Save After", `<p>save new body</p>`
	if err := h.db.Save(&savePost).Error; err != nil {
		t.Fatalf("Save: %v", err)
	}
	assertSearch("Save After", "Save After", true)
	assertSearch("save new body", "Save After", true)

	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&savePost).Updates(map[string]any{"title": "Rolled Back Title", "content": `<p>rolled back body</p>`}).Error; err != nil {
			return err
		}
		return fmt.Errorf("force rollback")
	}); err == nil {
		t.Fatal("transaction unexpectedly committed")
	}
	assertSearch("Save After", "Save After", true)
	assertNoOldTerms("Save After", "Rolled Back Title", "rolled back body")

	// Search remains restricted to currently public, published, non-deleted posts.
	private := seed("Index Private Secret", `<p>index private body</p>`, "")
	if err := h.db.Model(&private).Update("visibility", "private").Error; err != nil {
		t.Fatalf("make post private: %v", err)
	}
	futureAt := time.Now().Add(24 * time.Hour).UTC()
	future := seed("Index Future Secret", `<p>index future body</p>`, "")
	if err := h.db.Model(&future).Updates(map[string]any{"published_at": futureAt}).Error; err != nil {
		t.Fatalf("make post future dated: %v", err)
	}
	deleted := seed("Index Deleted Secret", `<p>index deleted body</p>`, "")
	if err := h.db.Delete(&deleted).Error; err != nil {
		t.Fatalf("soft-delete post: %v", err)
	}
	for _, term := range []string{"index private", "index future", "index deleted"} {
		r := followupJSONGet(h, "/api/search?q="+url.QueryEscape(term))
		if r.code != 200 || strings.Contains(r.body, "Index "+strings.Title(strings.TrimPrefix(term, "index "))+" Secret") {
			t.Errorf("non-public term %q leaked: status=%d body=%q", term, r.code, archivesSnippet(r.body))
		}
	}

	// gorm.Expr cannot safely provide a source value for the derived index. The
	// callback contract requires rejecting it before either source or index data
	// changes, while preserving the original searchable text.
	exprPost := seed("Expr Before", `<p>expr original body</p>`, "")
	assertSearch("expr original body", "Expr Before", true)
	err := h.db.Model(&exprPost).Update("content", gorm.Expr("?", `<p>expr replacement body</p>`)).Error
	if err == nil {
		t.Fatal("gorm.Expr source update succeeded; expected explicit rejection")
	}
	assertSearch("expr original body", "Expr Before", true)
	assertSearch("expr replacement body", "Expr Before", false)

}
