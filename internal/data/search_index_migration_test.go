package data

import (
	"fmt"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

// This creates legacy rows before removing the 0024 schema marker and derived
// columns, then exercises the public migration runner as an upgrade would.
func TestSearchIndexMigrationBackfillsLegacyPostsAndRollsBackOnFailure(t *testing.T) {
	db := newAccountsMigrationDB(t)
	if err := MigrateDB(db); err != nil {
		t.Fatalf("initial MigrateDB: %v", err)
	}
	author := core.User{Username: "search-index-migration-author", Email: "search-index-migration@example.test", Provider: "local"}
	if err := db.Create(&author).Error; err != nil {
		t.Fatalf("create author: %v", err)
	}
	published := time.Date(2024, time.March, 4, 12, 0, 0, 0, time.UTC)
	legacy := core.Post{Title: "Straße Legacy Title", Slug: "search-index-legacy", Content: `<p>old <b>Body</b> &amp; mark</p>`,
		Excerpt: "Legacy Excerpt", Type: "post", Status: "published", Visibility: "public", AuthorID: author.ID,
		PublishedAt: &published, CreatedAt: published, UpdatedAt: published}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatalf("create legacy post: %v", err)
	}
	legacySecond := core.Post{Title: "Second Legacy Title", Slug: "search-index-legacy-second", Content: `<p>second <i>Body</i></p>`,
		Excerpt: "Second Excerpt", Type: "post", Status: "published", Visibility: "public", AuthorID: author.ID,
		PublishedAt: &published, CreatedAt: published, UpdatedAt: published}
	if err := db.Create(&legacySecond).Error; err != nil {
		t.Fatalf("create second legacy post: %v", err)
	}
	legacyPosts := []core.Post{legacy, legacySecond}
	for i := 3; i <= 201; i++ {
		legacyPosts = append(legacyPosts, core.Post{
			Title: fmt.Sprintf("Bulk Legacy Title %03d", i), Slug: fmt.Sprintf("search-index-legacy-%03d", i),
			Content: fmt.Sprintf("<p>bulk legacy body %03d</p>", i), Excerpt: fmt.Sprintf("Bulk legacy excerpt %03d", i),
			Type: "post", Status: "published", Visibility: "public", AuthorID: author.ID,
			PublishedAt: &published, CreatedAt: published, UpdatedAt: published,
		})
	}
	extraLegacyPosts := legacyPosts[2:]
	if err := db.CreateInBatches(&extraLegacyPosts, 100).Error; err != nil {
		t.Fatalf("create 199 additional legacy posts: %v", err)
	}
	legacyIDs := make([]uint, len(legacyPosts))
	for i := range legacyPosts {
		legacyIDs[i] = legacyPosts[i].ID
	}
	// Reconstruct the pre-0024 state, preserving all ordinary post fields.
	if err := db.Exec(`ALTER TABLE posts DROP COLUMN IF EXISTS search_title, DROP COLUMN IF EXISTS search_content, DROP COLUMN IF EXISTS search_excerpt`).Error; err != nil {
		t.Fatalf("remove derived columns to model legacy schema: %v", err)
	}
	if err := db.Exec(`DELETE FROM schema_migrations WHERE version = 24`).Error; err != nil {
		t.Fatalf("remove 0024 marker: %v", err)
	}
	if err := MigrateDB(db); err != nil {
		t.Fatalf("upgrade legacy database through MigrateDB: %v", err)
	}
	assertFolded := func(stage string, post core.Post, wantTitle, wantContent, wantExcerpt string) {
		t.Helper()
		var got struct {
			Title   *string `gorm:"column:search_title"`
			Content *string `gorm:"column:search_content"`
			Excerpt *string `gorm:"column:search_excerpt"`
		}
		if err := db.Raw(`SELECT search_title, search_content, search_excerpt FROM posts WHERE id = ?`, post.ID).Scan(&got).Error; err != nil {
			t.Fatalf("read folded fields %s: %v", stage, err)
		}
		if got.Title == nil || *got.Title != wantTitle || got.Content == nil || *got.Content != wantContent || got.Excerpt == nil || *got.Excerpt != wantExcerpt {
			t.Errorf("folded fields %s for %s = (%v, %v, %v), want (%s, %s, %s)", stage, post.Slug, got.Title, got.Content, got.Excerpt, wantTitle, wantContent, wantExcerpt)
		}
	}
	assertFolded("after upgrade", legacy, "strasse legacy title", "old body & mark", "legacy excerpt")
	assertFolded("after upgrade", legacySecond, "second legacy title", "second body", "second excerpt")
	var applied int64
	if err := db.Raw(`SELECT count(*) FROM schema_migrations WHERE version = 24`).Scan(&applied).Error; err != nil {
		t.Fatalf("read 0024 marker after upgrade: %v", err)
	}
	if applied != 1 {
		t.Fatalf("0024 marker count = %d, want 1", applied)
	}
	if err := MigrateDB(db); err != nil {
		t.Fatalf("repeat MigrateDB: %v", err)
	}
	assertFolded("after stable rerun", legacy, "strasse legacy title", "old body & mark", "legacy excerpt")
	assertFolded("after stable rerun", legacySecond, "second legacy title", "second body", "second excerpt")

	// A failed backfill must roll back its marker and all row changes together.
	if err := db.Exec(`UPDATE posts SET search_title = NULL, search_content = NULL, search_excerpt = NULL WHERE id IN ?`, legacyIDs).Error; err != nil {
		t.Fatalf("reset derived values for rollback check: %v", err)
	}
	if err := db.Exec(`DELETE FROM schema_migrations WHERE version = 24`).Error; err != nil {
		t.Fatalf("remove marker for rollback check: %v", err)
	}
	if err := db.Exec(`CREATE SEQUENCE search_index_backfill_probe`).Error; err != nil {
		t.Fatalf("create non-transactional backfill probe: %v", err)
	}
	if err := db.Exec(`CREATE FUNCTION reject_search_index_backfill() RETURNS trigger LANGUAGE plpgsql AS $$
		DECLARE attempt bigint;
		BEGIN
			attempt := nextval('search_index_backfill_probe');
			IF attempt = 201 THEN RAISE EXCEPTION 'forced failure after first 200-row batch'; END IF;
			RETURN NEW;
		END $$`).Error; err != nil {
		t.Fatalf("create deliberate backfill failure function: %v", err)
	}
	if err := db.Exec(`CREATE TRIGGER reject_search_index_backfill BEFORE UPDATE OF search_title ON posts
		FOR EACH ROW EXECUTE FUNCTION reject_search_index_backfill()`).Error; err != nil {
		t.Fatalf("install deliberate backfill failure trigger: %v", err)
	}
	if err := MigrateDB(db); err == nil {
		t.Fatal("MigrateDB succeeded despite a deliberate backfill failure")
	}
	var triggerAttempts int64
	if err := db.Raw(`SELECT last_value FROM search_index_backfill_probe`).Scan(&triggerAttempts).Error; err != nil {
		t.Fatalf("read non-transactional trigger probe: %v", err)
	}
	if triggerAttempts != 201 {
		t.Fatalf("backfill trigger attempts before failure = %d, want 201 (first 200-row batch completed before the next row failed)", triggerAttempts)
	}
	if err := db.Exec(`DROP TRIGGER reject_search_index_backfill ON posts`).Error; err != nil {
		t.Fatalf("remove deliberate failure trigger: %v", err)
	}
	if err := db.Exec(`DROP FUNCTION reject_search_index_backfill()`).Error; err != nil {
		t.Fatalf("remove deliberate failure function: %v", err)
	}
	var markerCount int64
	if err := db.Raw(`SELECT count(*) FROM schema_migrations WHERE version = 24`).Scan(&markerCount).Error; err != nil {
		t.Fatalf("check marker after failed migration: %v", err)
	}
	if markerCount != 0 {
		t.Errorf("0024 marker count after failed backfill = %d, want 0", markerCount)
	}
	var nullCount int64
	if err := db.Raw(`SELECT count(*) FROM posts WHERE id IN ? AND search_title IS NULL AND search_content IS NULL AND search_excerpt IS NULL`, legacyIDs).Scan(&nullCount).Error; err != nil {
		t.Fatalf("check data after failed migration: %v", err)
	}
	if nullCount != int64(len(legacyIDs)) {
		t.Errorf("rows left in pre-backfill state after failure = %d, want %d", nullCount, len(legacyIDs))
	}
	if err := MigrateDB(db); err != nil {
		t.Fatalf("retry migration after removing failure: %v", err)
	}
	assertFolded("after successful retry", legacy, "strasse legacy title", "old body & mark", "legacy excerpt")
	assertFolded("after successful retry", legacySecond, "second legacy title", "second body", "second excerpt")
	var populatedCount int64
	if err := db.Raw(`SELECT count(*) FROM posts WHERE id IN ? AND search_title IS NOT NULL AND search_content IS NOT NULL AND search_excerpt IS NOT NULL`, legacyIDs).Scan(&populatedCount).Error; err != nil {
		t.Fatalf("check all derived fields after successful retry: %v", err)
	}
	if populatedCount != int64(len(legacyIDs)) {
		t.Errorf("posts with all derived fields after successful retry = %d, want %d", populatedCount, len(legacyIDs))
	}
}
