package server

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
)

type legacySourceFixture struct {
	postID     uint
	revisionID uint
	meta       json.RawMessage
}

type legacySourcePostState struct {
	ID            uint
	Source        sql.NullString
	Format        sql.NullString
	RenderVersion int
	RenderTrust   string
	MetaJSON      string
}

type legacySourceRevisionState struct {
	ID       uint
	Source   sql.NullString
	Format   sql.NullString
	MetaJSON string
}

// TestLegacySourceReconciliationBackfillsOnlyMissingSources covers rows inserted
// after the schema version was recorded: legacy markdown is recovered, while a
// present source (including an explicit empty source) remains authoritative.
func TestLegacySourceReconciliationBackfillsOnlyMissingSources(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("admin", "legacy-source-reconciliation")

	metadataCases := []struct {
		name string
		meta json.RawMessage
	}{
		{name: "empty array", meta: json.RawMessage(`[]`)},
		{name: "array values", meta: json.RawMessage(`[1,"value",true]`)},
		{name: "scalar string", meta: json.RawMessage(`"plain scalar"`)},
		{name: "explicit null", meta: json.RawMessage(`null`)},
		{name: "HTML only without markdown", meta: json.RawMessage(`{"custom":"value"}`)},
	}
	fixtures := make([]legacySourceFixture, 0, len(metadataCases))
	for i, tc := range metadataCases {
		post := core.Post{
			Title: "legacy metadata row", Slug: "legacy-source-reconciliation-meta-" + string(rune('a'+i)),
			Content: "<p>legacy HTML</p>", Type: "post", Status: "draft", AuthorID: author.ID,
			Meta: tc.meta,
		}
		if err := h.db.Create(&post).Error; err != nil {
			t.Fatalf("seed post with %s metadata: %v", tc.name, err)
		}
		revision := core.Revision{
			PostID: post.ID, UserID: author.ID, Kind: "manual", Title: post.Title,
			Content: post.Content, Meta: tc.meta,
		}
		if err := h.db.Create(&revision).Error; err != nil {
			t.Fatalf("seed revision with %s metadata: %v", tc.name, err)
		}
		fixtures = append(fixtures, legacySourceFixture{postID: post.ID, revisionID: revision.ID, meta: tc.meta})
	}

	// Include a real legacy markdown row so the no-overwrite cases are checked
	// alongside successful backfill in this same reconciliation run.
	legacyPostSource := "# 舊文章\n\n原稿內容\n"
	legacyPost := core.Post{
		Title: "legacy markdown row", Slug: "legacy-source-reconciliation-markdown",
		Content: "<h1>舊文章</h1>", Type: "post", Status: "draft", AuthorID: author.ID,
		Meta: json.RawMessage(`{"markdown":"# 舊文章\n\n原稿內容\n"}`),
	}
	if err := h.db.Create(&legacyPost).Error; err != nil {
		t.Fatalf("seed legacy markdown post: %v", err)
	}
	legacyRevisionSource := "## 舊修訂\n\n修訂原稿\n"
	legacyRevision := core.Revision{
		PostID: legacyPost.ID, UserID: author.ID, Kind: "manual", Title: legacyPost.Title,
		Content: legacyPost.Content, Meta: json.RawMessage(`{"markdown":"## 舊修訂\n\n修訂原稿\n"}`),
	}
	if err := h.db.Create(&legacyRevision).Error; err != nil {
		t.Fatalf("seed legacy markdown revision: %v", err)
	}

	newPostSource := "## New source must win\n"
	newPost := core.Post{
		Title: "new source row", Slug: "legacy-source-reconciliation-new-post",
		Content: "<p>new source</p>", Type: "post", Status: "draft", AuthorID: author.ID,
		Source: newPostSource, Format: "jmd",
		Meta: json.RawMessage(`{"markdown":"stale legacy metadata"}`),
	}
	if err := h.db.Create(&newPost).Error; err != nil {
		t.Fatalf("seed post with current source: %v", err)
	}
	emptyPost := core.Post{
		Title: "explicit empty post source", Slug: "legacy-source-reconciliation-empty-post",
		Content: "<p>empty source</p>", Type: "post", Status: "draft", AuthorID: author.ID,
		Source: "", Format: "md",
		Meta: json.RawMessage(`{"markdown":"must not replace explicit empty source"}`),
	}
	if err := h.db.Create(&emptyPost).Error; err != nil {
		t.Fatalf("seed post with explicit empty source: %v", err)
	}
	newRevisionSource := "### Current revision source\n"
	newRevision := core.Revision{
		PostID: newPost.ID, UserID: author.ID, Kind: "manual", Title: newPost.Title,
		Content: newPost.Content, Source: newRevisionSource, Format: "jmd",
		Meta: json.RawMessage(`{"markdown":"stale revision metadata"}`),
	}
	if err := h.db.Create(&newRevision).Error; err != nil {
		t.Fatalf("seed revision with current source: %v", err)
	}
	emptyRevision := core.Revision{
		PostID: newPost.ID, UserID: author.ID, Kind: "manual", Title: "explicit empty source",
		Content: newPost.Content, Source: "", Format: "jmd",
		Meta: json.RawMessage(`{"markdown":"must not replace explicit empty source"}`),
	}
	if err := h.db.Create(&emptyRevision).Error; err != nil {
		t.Fatalf("seed revision with explicit empty source: %v", err)
	}

	// Model rows predating migration 0015, even though this database has already
	// recorded the newer migration version through newAccessHarness.
	legacyPostIDs := make([]uint, 0, len(fixtures)+1)
	legacyRevisionIDs := make([]uint, 0, len(fixtures)+1)
	for _, fixture := range fixtures {
		legacyPostIDs = append(legacyPostIDs, fixture.postID)
		legacyRevisionIDs = append(legacyRevisionIDs, fixture.revisionID)
	}
	legacyPostIDs = append(legacyPostIDs, legacyPost.ID)
	legacyRevisionIDs = append(legacyRevisionIDs, legacyRevision.ID)
	if err := h.db.Exec("UPDATE posts SET source = NULL, format = NULL, render_version = NULL, render_trust = NULL WHERE id IN ?", legacyPostIDs).Error; err != nil {
		t.Fatalf("make legacy post fields NULL: %v", err)
	}
	if err := h.db.Exec("UPDATE revisions SET source = NULL, format = NULL WHERE id IN ?", legacyRevisionIDs).Error; err != nil {
		t.Fatalf("make legacy revision fields NULL: %v", err)
	}

	var postCountBefore, revisionCountBefore int64
	if err := h.db.Model(&core.Post{}).Count(&postCountBefore).Error; err != nil {
		t.Fatalf("count posts before reconciliation: %v", err)
	}
	if err := h.db.Model(&core.Revision{}).Count(&revisionCountBefore).Error; err != nil {
		t.Fatalf("count revisions before reconciliation: %v", err)
	}

	verify := func(run int) {
		t.Helper()
		var posts []legacySourcePostState
		postIDs := legacySourcePostIDs(legacyPostIDs, newPost.ID, emptyPost.ID)
		if err := h.db.Raw(`SELECT id, source, format, render_version, render_trust, meta::text AS meta_json
			FROM posts WHERE id IN ? ORDER BY id`, postIDs).Scan(&posts).Error; err != nil {
			t.Fatalf("read post state after migration run %d: %v", run, err)
		}
		wantPostCount := len(legacyPostIDs) + 2
		if len(posts) != wantPostCount {
			t.Fatalf("migration run %d returned %d fixture posts, want %d", run, len(posts), wantPostCount)
		}
		postByID := make(map[uint]legacySourcePostState, len(posts))
		for _, post := range posts {
			postByID[post.ID] = post
		}
		for i, fixture := range fixtures {
			got := postByID[fixture.postID]
			if got.Source.Valid || !got.Format.Valid || got.Format.String != "html" || got.RenderVersion != 0 || got.RenderTrust != "strict" {
				t.Errorf("run %d non-object post %d source/format/render = %#v/%#v/%d/%q, want SQL NULL/html/0/strict", run, fixture.postID, got.Source, got.Format, got.RenderVersion, got.RenderTrust)
			}
			assertLegacyMetaEquivalent(t, run, "post", fixture.postID, got.MetaJSON, metadataCases[i].meta)
		}
		legacy := postByID[legacyPost.ID]
		if !legacy.Source.Valid || legacy.Source.String != legacyPostSource || !legacy.Format.Valid || legacy.Format.String != "md" {
			t.Errorf("run %d legacy post source/format = %#v/%#v, want exact markdown/md", run, legacy.Source, legacy.Format)
		}
		current := postByID[newPost.ID]
		if !current.Source.Valid || current.Source.String != newPostSource || !current.Format.Valid || current.Format.String != "jmd" {
			t.Errorf("run %d current post source/format = %#v/%#v, want preserved source/jmd", run, current.Source, current.Format)
		}
		empty := postByID[emptyPost.ID]
		if !empty.Source.Valid || empty.Source.String != "" || !empty.Format.Valid || empty.Format.String != "md" {
			t.Errorf("run %d explicit empty post source/format = %#v/%#v, want empty/md", run, empty.Source, empty.Format)
		}

		var revisions []legacySourceRevisionState
		if err := h.db.Raw(`SELECT id, source, format, meta::text AS meta_json
			FROM revisions WHERE id IN ? ORDER BY id`, legacySourceRevisionIDs(legacyRevisionIDs, newRevision.ID, emptyRevision.ID)).Scan(&revisions).Error; err != nil {
			t.Fatalf("read revision state after migration run %d: %v", run, err)
		}
		wantRevisionCount := len(legacyRevisionIDs) + 2
		if len(revisions) != wantRevisionCount {
			t.Fatalf("migration run %d returned %d fixture revisions, want %d", run, len(revisions), wantRevisionCount)
		}
		revisionByID := make(map[uint]legacySourceRevisionState, len(revisions))
		for _, revision := range revisions {
			revisionByID[revision.ID] = revision
		}
		for i, fixture := range fixtures {
			got := revisionByID[fixture.revisionID]
			if got.Source.Valid || !got.Format.Valid || got.Format.String != "html" {
				t.Errorf("run %d non-object revision %d source/format = %#v/%#v, want SQL NULL/html", run, fixture.revisionID, got.Source, got.Format)
			}
			assertLegacyMetaEquivalent(t, run, "revision", fixture.revisionID, got.MetaJSON, metadataCases[i].meta)
		}
		legacyRev := revisionByID[legacyRevision.ID]
		if !legacyRev.Source.Valid || legacyRev.Source.String != legacyRevisionSource || !legacyRev.Format.Valid || legacyRev.Format.String != "md" {
			t.Errorf("run %d legacy revision source/format = %#v/%#v, want exact markdown/md", run, legacyRev.Source, legacyRev.Format)
		}
		currentRev := revisionByID[newRevision.ID]
		if !currentRev.Source.Valid || currentRev.Source.String != newRevisionSource || !currentRev.Format.Valid || currentRev.Format.String != "jmd" {
			t.Errorf("run %d current revision source/format = %#v/%#v, want preserved source/jmd", run, currentRev.Source, currentRev.Format)
		}
		emptyRev := revisionByID[emptyRevision.ID]
		if !emptyRev.Source.Valid || emptyRev.Source.String != "" || !emptyRev.Format.Valid || emptyRev.Format.String != "jmd" {
			t.Errorf("run %d explicit empty revision source/format = %#v/%#v, want empty/jmd", run, emptyRev.Source, emptyRev.Format)
		}

		var postCount, revisionCount int64
		if err := h.db.Model(&core.Post{}).Count(&postCount).Error; err != nil {
			t.Fatalf("count posts after migration run %d: %v", run, err)
		}
		if err := h.db.Model(&core.Revision{}).Count(&revisionCount).Error; err != nil {
			t.Fatalf("count revisions after migration run %d: %v", run, err)
		}
		if postCount != postCountBefore || revisionCount != revisionCountBefore {
			t.Errorf("run %d changed row counts posts/revisions=%d/%d from %d/%d", run, postCount, revisionCount, postCountBefore, revisionCountBefore)
		}
	}

	var previousPostStates []legacySourcePostState
	var previousRevisionStates []legacySourceRevisionState
	for run := 1; run <= 2; run++ {
		if err := data.MigrateDB(h.db); err != nil {
			t.Fatalf("MigrateDB run %d: %v", run, err)
		}
		verify(run)
		var postStates []legacySourcePostState
		if err := h.db.Raw(`SELECT id, source, format, render_version, render_trust, meta::text AS meta_json
			FROM posts WHERE id IN ? ORDER BY id`, legacySourcePostIDs(legacyPostIDs, newPost.ID, emptyPost.ID)).Scan(&postStates).Error; err != nil {
			t.Fatalf("snapshot posts after migration run %d: %v", run, err)
		}
		var revisionStates []legacySourceRevisionState
		if err := h.db.Raw(`SELECT id, source, format, meta::text AS meta_json
			FROM revisions WHERE id IN ? ORDER BY id`, legacySourceRevisionIDs(legacyRevisionIDs, newRevision.ID, emptyRevision.ID)).Scan(&revisionStates).Error; err != nil {
			t.Fatalf("snapshot revisions after migration run %d: %v", run, err)
		}
		if run == 1 {
			previousPostStates, previousRevisionStates = postStates, revisionStates
		} else {
			if !reflect.DeepEqual(postStates, previousPostStates) {
				t.Errorf("post rows changed between migration runs: first=%#v second=%#v", previousPostStates, postStates)
			}
			if !reflect.DeepEqual(revisionStates, previousRevisionStates) {
				t.Errorf("revision rows changed between migration runs: first=%#v second=%#v", previousRevisionStates, revisionStates)
			}
		}
	}
}

func legacySourcePostIDs(legacyIDs []uint, currentID, emptyID uint) []uint {
	ids := append([]uint(nil), legacyIDs...)
	ids = append(ids, currentID)
	return append(ids, emptyID)
}

func legacySourceRevisionIDs(legacyIDs []uint, currentID, emptyID uint) []uint {
	ids := append([]uint(nil), legacyIDs...)
	ids = append(ids, currentID)
	return append(ids, emptyID)
}

func assertLegacyMetaEquivalent(t *testing.T, run int, kind string, id uint, gotJSON string, wantJSON json.RawMessage) {
	t.Helper()
	var got, want any
	if err := json.Unmarshal([]byte(gotJSON), &got); err != nil {
		t.Errorf("run %d %s %d metadata is invalid JSON %q: %v", run, kind, id, gotJSON, err)
		return
	}
	if err := json.Unmarshal(wantJSON, &want); err != nil {
		t.Fatalf("invalid expected fixture metadata %q: %v", wantJSON, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("run %d %s %d metadata = %#v, want JSON-equivalent %#v", run, kind, id, got, want)
	}
}
