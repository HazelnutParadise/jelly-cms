package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
)

// Revision history contract (handlers_revisions.go, recording in handlers_posts.go). Runs through the
// real route table against the dedicated PostgreSQL database; skipped unless JELLY_TEST_POSTGRES=1.

type revTestRevision struct {
	ID   uint   `json:"id"`
	Kind string `json:"kind"`
	User struct {
		ID       uint   `json:"id"`
		Username string `json:"username"`
	} `json:"user"`
	CreatedAt time.Time `json:"created_at"`
	Added     int       `json:"added"`
	Removed   int       `json:"removed"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
}

type revTestDiff struct {
	Title   []DiffOp `json:"title"`
	Content []DiffOp `json:"content"`
}

func revTestBody(t *testing.T, fields map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode request body: %v", err)
	}
	return string(raw)
}

func revTestCreate(t *testing.T, h *accessHarness, token string, fields map[string]any) uint {
	t.Helper()
	r := h.request(http.MethodPost, "/api/admin/posts", revTestBody(t, fields), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(r, "create post")
	var got struct {
		ID uint `json:"id"`
	}
	if r.code < 200 || r.code >= 300 || json.Unmarshal([]byte(r.body), &got) != nil || got.ID == 0 {
		t.Fatalf("create post returned %d body=%q", r.code, r.body)
	}
	return got.ID
}

func revTestPut(t *testing.T, h *accessHarness, token string, id uint, fields map[string]any) {
	t.Helper()
	r := h.request(http.MethodPut, fmt.Sprintf("/api/admin/posts/%d", id), revTestBody(t, fields), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(r, "update post")
	if r.code != http.StatusOK {
		t.Fatalf("PUT /api/admin/posts/%d returned %d body=%q", id, r.code, r.body)
	}
}

// revTestList returns the revision list newest first, as the API orders it.
func revTestList(t *testing.T, h *accessHarness, token string, id uint) []revTestRevision {
	t.Helper()
	r := h.request(http.MethodGet, fmt.Sprintf("/api/admin/posts/%d/revisions", id), "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(r, "list revisions")
	if r.code != http.StatusOK {
		t.Fatalf("GET revisions of post %d returned %d body=%q", id, r.code, r.body)
	}
	var revs []revTestRevision
	if err := json.Unmarshal([]byte(r.body), &revs); err != nil {
		t.Fatalf("decode revision list: %v body=%q", err, r.body)
	}
	return revs
}

// revTestNewest returns the id of the newest revision and fails the test cleanly when there is none.
func revTestNewest(t *testing.T, h *accessHarness, token string, id uint) uint {
	t.Helper()
	revs := revTestList(t, h, token, id)
	if len(revs) == 0 {
		t.Fatalf("post %d has no revisions", id)
	}
	return revs[0].ID
}

func revTestGet(h *accessHarness, token string, id, rid uint) (revTestRevision, int) {
	r := h.request(http.MethodGet, fmt.Sprintf("/api/admin/posts/%d/revisions/%d", id, rid), "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(r, "get revision")
	var rev revTestRevision
	if r.code == http.StatusOK {
		_ = json.Unmarshal([]byte(r.body), &rev)
	}
	return rev, r.code
}

func TestRevisionsRecordKindsAndLineCounts(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "rev-admin")
	token := h.token(admin)

	const first = "line one\nline two\nline three"
	id := revTestCreate(t, h, token, map[string]any{"title": "Rev title", "content": first, "status": "draft"})

	revs := revTestList(t, h, token, id)
	if len(revs) != 1 {
		t.Fatalf("after create: %d revisions, want 1", len(revs))
	}
	created := revs[0]
	if created.Kind != "manual" {
		t.Errorf("create kind = %q, want manual", created.Kind)
	}
	if created.User.ID != admin.ID {
		t.Errorf("create revision user id = %d, want %d", created.User.ID, admin.ID)
	}
	if created.Added != 3 || created.Removed != 0 {
		t.Errorf("create revision added=%d removed=%d, want added=3 (content lines) removed=0", created.Added, created.Removed)
	}
	full, code := revTestGet(h, token, id, created.ID)
	if code != http.StatusOK {
		t.Fatalf("GET first revision returned %d, want 200", code)
	}
	if full.Title != "Rev title" || full.Content != first {
		t.Errorf("first revision title=%q content=%q, want the saved title and content", full.Title, full.Content)
	}

	second := first + "\nline four"
	revTestPut(t, h, token, id, map[string]any{"content": second})
	revs = revTestList(t, h, token, id)
	if len(revs) != 2 {
		t.Fatalf("after adding a line: %d revisions, want 2", len(revs))
	}
	if revs[0].Kind != "manual" || revs[0].Added != 1 || revs[0].Removed != 0 {
		t.Errorf("content edit revision kind=%q added=%d removed=%d, want manual 1 0", revs[0].Kind, revs[0].Added, revs[0].Removed)
	}
	if revs[0].User.ID != admin.ID {
		t.Errorf("edit revision user id = %d, want %d", revs[0].User.ID, admin.ID)
	}

	revTestPut(t, h, token, id, map[string]any{"title": "Rev title", "content": second, "status": "draft"})
	if n := len(revTestList(t, h, token, id)); n != 2 {
		t.Errorf("saving identical content changed the revision count to %d, want 2", n)
	}

	third := second + "\nline five"
	revTestPut(t, h, token, id, map[string]any{"content": third, "status": "published"})
	revs = revTestList(t, h, token, id)
	if len(revs) != 3 || revs[0].Kind != "publish" {
		t.Fatalf("publishing with changed content: %d revisions, newest kind %q; want 3 and publish", len(revs), revs[0].Kind)
	}

	fourth := third + "\nline six"
	revTestPut(t, h, token, id, map[string]any{"content": fourth, "autosave": true})
	revs = revTestList(t, h, token, id)
	if len(revs) != 4 || revs[0].Kind != "autosave" {
		t.Fatalf("autosave with changed content: %d revisions, newest kind %q; want 4 and autosave", len(revs), revs[0].Kind)
	}
}

func TestRevisionsAutosaveKeepsNewestTwentyAndLeavesManualAlone(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "rev-autosave-admin")
	token := h.token(admin)

	id := revTestCreate(t, h, token, map[string]any{"title": "Autosave", "content": "base", "status": "draft"})
	revTestPut(t, h, token, id, map[string]any{"content": "manual edit"})
	for i := 1; i <= 25; i++ {
		revTestPut(t, h, token, id, map[string]any{"content": fmt.Sprintf("autosave %02d", i), "autosave": true})
	}

	var autosaveContents []string
	if err := h.db.Model(&core.Revision{}).Where("post_id = ? AND kind = ?", id, "autosave").Order("id").Pluck("content", &autosaveContents).Error; err != nil {
		t.Fatalf("read autosave contents: %v", err)
	}
	var wantContents []string
	for i := 6; i <= 25; i++ {
		wantContents = append(wantContents, fmt.Sprintf("autosave %02d", i))
	}
	if !reflect.DeepEqual(autosaveContents, wantContents) {
		t.Errorf("kept autosaves = %q, want the newest 20 (autosave 06 through 25)", autosaveContents)
	}

	manual := 0
	for _, rev := range revTestList(t, h, token, id) {
		if rev.Kind == "manual" {
			manual++
		}
	}
	if manual != 2 {
		t.Errorf("manual revisions = %d, want 2 (create and manual edit kept)", manual)
	}
}

func TestRevisionsListRequiresAuthorAndOwnership(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "rev-list-author")
	other := h.user("author", "rev-list-other")
	authorToken, otherToken := h.token(author), h.token(other)

	own := revTestCreate(t, h, authorToken, map[string]any{"title": "Own", "content": "one", "status": "draft"})
	revTestPut(t, h, authorToken, own, map[string]any{"content": "one\ntwo"})
	foreign := revTestCreate(t, h, otherToken, map[string]any{"title": "Foreign", "content": "x", "status": "draft"})

	anon := h.request(http.MethodGet, fmt.Sprintf("/api/admin/posts/%d/revisions", own), "", "", echo.MIMEApplicationJSON, "")
	if anon.code != http.StatusUnauthorized {
		t.Errorf("anonymous revision list returned %d, want 401", anon.code)
	}
	cross := h.request(http.MethodGet, fmt.Sprintf("/api/admin/posts/%d/revisions", foreign), "", authorToken, echo.MIMEApplicationJSON, "")
	if cross.code != http.StatusNotFound {
		t.Errorf("author revision list of another author's post returned %d, want 404", cross.code)
	}

	revs := revTestList(t, h, authorToken, own)
	if len(revs) != 2 {
		t.Fatalf("own revisions = %d, want 2", len(revs))
	}
	if revs[0].ID <= revs[1].ID {
		t.Errorf("revision list is not newest first: ids %d then %d", revs[0].ID, revs[1].ID)
	}
	for _, rev := range revs {
		if rev.ID == 0 || rev.Kind == "" || rev.CreatedAt.IsZero() {
			t.Errorf("revision missing id, kind or created_at: %+v", rev)
		}
		if rev.User.ID != author.ID || rev.User.Username != author.Username {
			t.Errorf("revision user = %+v, want author %d %q", rev.User, author.ID, author.Username)
		}
	}
	if revs[0].Added != 1 || revs[0].Removed != 0 {
		t.Errorf("newest revision added=%d removed=%d, want 1 0", revs[0].Added, revs[0].Removed)
	}
}

func TestRevisionsGetReturnsFullSnapshotOfOwnPostOnly(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "rev-get-admin")
	token := h.token(admin)

	postA := revTestCreate(t, h, token, map[string]any{"title": "A title", "content": "A body", "status": "draft"})
	postB := revTestCreate(t, h, token, map[string]any{"title": "B title", "content": "B body", "status": "draft"})
	ridA := revTestNewest(t, h, token, postA)
	ridB := revTestNewest(t, h, token, postB)

	full, code := revTestGet(h, token, postA, ridA)
	if code != http.StatusOK {
		t.Fatalf("GET own revision returned %d, want 200", code)
	}
	if full.Title != "A title" || full.Content != "A body" {
		t.Errorf("full revision title=%q content=%q, want the stored snapshot", full.Title, full.Content)
	}
	if _, code := revTestGet(h, token, postA, ridB); code != http.StatusNotFound {
		t.Errorf("revision of post B requested under post A returned %d, want 404", code)
	}
	if _, code := revTestGet(h, token, postB, ridA); code != http.StatusNotFound {
		t.Errorf("revision of post A requested under post B returned %d, want 404", code)
	}
}

func TestRevisionsDiffComparesVersionsAndCurrent(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "rev-diff-admin")
	token := h.token(admin)

	id := revTestCreate(t, h, token, map[string]any{"title": "Diff T1", "content": "a\nb\nc", "status": "draft"})
	r1 := revTestNewest(t, h, token, id)
	revTestPut(t, h, token, id, map[string]any{"title": "Diff T2", "content": "a\nc\nd"})
	r2 := revTestNewest(t, h, token, id)

	fetch := func(query string) revTestDiff {
		t.Helper()
		r := h.request(http.MethodGet, fmt.Sprintf("/api/admin/posts/%d/revisions/diff%s", id, query), "", token, echo.MIMEApplicationJSON, "")
		h.mustNoPanic(r, "diff revisions")
		if r.code != http.StatusOK {
			t.Fatalf("GET diff%s returned %d body=%q", query, r.code, r.body)
		}
		var d revTestDiff
		if err := json.Unmarshal([]byte(r.body), &d); err != nil {
			t.Fatalf("decode diff: %v body=%q", err, r.body)
		}
		return d
	}

	between := fetch(fmt.Sprintf("?from=%d&to=%d", r1, r2))
	wantContent := []DiffOp{{Op: "=", Text: "a"}, {Op: "-", Text: "b"}, {Op: "=", Text: "c"}, {Op: "+", Text: "d"}}
	if !reflect.DeepEqual(between.Content, wantContent) {
		t.Errorf("content diff = %+v, want %+v", between.Content, wantContent)
	}
	// Title ops are checked as a set: the exact order of a single-line replacement is not part of the contract.
	var titleRemoved, titleAdded, titleKept bool
	for _, op := range between.Title {
		switch {
		case op.Op == "-" && op.Text == "Diff T1":
			titleRemoved = true
		case op.Op == "+" && op.Text == "Diff T2":
			titleAdded = true
		case op.Op == "=":
			titleKept = true
		}
	}
	if !titleRemoved || !titleAdded || titleKept {
		t.Errorf("title diff = %+v, want Diff T1 removed and Diff T2 added", between.Title)
	}

	toCurrent := fetch(fmt.Sprintf("?from=%d&to=current", r1))
	if !reflect.DeepEqual(toCurrent, between) {
		t.Errorf("to=current diff = %+v, want the same as comparing with the latest revision %+v", toCurrent, between)
	}
}

func TestRevisionsRestoreReturnsSnapshotAndRecordsRestoreKind(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "rev-restore-author")
	other := h.user("author", "rev-restore-other")
	authorToken, otherToken := h.token(author), h.token(other)

	id := revTestCreate(t, h, authorToken, map[string]any{"title": "Restore T1", "content": "first body", "status": "draft"})
	r1 := revTestNewest(t, h, authorToken, id)
	revTestPut(t, h, authorToken, id, map[string]any{"title": "Restore T2", "content": "second body"})
	before := len(revTestList(t, h, authorToken, id))

	restore := h.request(http.MethodPost, fmt.Sprintf("/api/admin/posts/%d/revisions/%d/restore", id, r1), "", authorToken, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(restore, "restore own revision")
	if restore.code != http.StatusOK {
		t.Fatalf("restore own revision returned %d body=%q, want 200", restore.code, restore.body)
	}
	var returned struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(restore.body), &returned); err != nil {
		t.Fatalf("decode restore response: %v body=%q", err, restore.body)
	}
	if returned.Title != "Restore T1" || returned.Content != "first body" {
		t.Errorf("restore response title=%q content=%q, want the restored snapshot", returned.Title, returned.Content)
	}

	post, code := trashTestGetPost(h, authorToken, id)
	if code != http.StatusOK || post.Title != "Restore T1" {
		t.Errorf("GET after restore: code=%d title=%q, want 200 and Restore T1", code, post.Title)
	}
	revs := revTestList(t, h, authorToken, id)
	if len(revs) != before+1 {
		t.Errorf("revisions after restore = %d, want %d", len(revs), before+1)
	}
	if len(revs) > 0 && revs[0].Kind != "restore" {
		t.Errorf("newest revision kind after restore = %q, want restore", revs[0].Kind)
	}

	adminPost := revTestCreate(t, h, otherToken, map[string]any{"title": "Other T1", "content": "other body", "status": "draft"})
	otherRev := revTestNewest(t, h, otherToken, adminPost)
	foreign := h.request(http.MethodPost, fmt.Sprintf("/api/admin/posts/%d/revisions/%d/restore", adminPost, otherRev), "", authorToken, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(foreign, "restore another author's revision")
	if foreign.code != http.StatusNotFound {
		t.Errorf("author restoring another author's revision returned %d, want 404", foreign.code)
	}
}
