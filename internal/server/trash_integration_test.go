package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// Recycle bin contract (handlers_trash.go). These run through the real route table against the
// dedicated PostgreSQL database and are skipped unless JELLY_TEST_POSTGRES=1.

const trashTestTolerance = 5 * time.Second

type trashTestItem struct {
	Kind        string    `json:"kind"`
	ID          uint      `json:"id"`
	Title       string    `json:"title"`
	DeletedAt   time.Time `json:"deleted_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	DeletedByID uint      `json:"deleted_by_id"`
	DeletedBy   string    `json:"deleted_by"`
}

type trashTestPage struct {
	Items         []trashTestItem `json:"items"`
	Counts        map[string]int  `json:"counts"`
	RetentionDays int             `json:"retention_days"`
}

type trashTestPostView struct {
	ID    uint   `json:"id"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

var trashTestKinds = []string{"all", "post", "page", "product", "comment"}

func trashTestBody(t *testing.T, fields map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode request body: %v", err)
	}
	return string(raw)
}

func trashTestCreatePost(t *testing.T, h *accessHarness, token string, fields map[string]any) uint {
	t.Helper()
	r := h.request(http.MethodPost, "/api/admin/posts", trashTestBody(t, fields), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(r, "create post")
	var got struct {
		ID uint `json:"id"`
	}
	if r.code < 200 || r.code >= 300 || json.Unmarshal([]byte(r.body), &got) != nil || got.ID == 0 {
		t.Fatalf("create post %v returned %d body=%q", fields["slug"], r.code, r.body)
	}
	return got.ID
}

func trashTestDeletePost(t *testing.T, h *accessHarness, token string, id uint) {
	t.Helper()
	r := h.request(http.MethodDelete, fmt.Sprintf("/api/admin/posts/%d", id), "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(r, "delete post")
	if r.code < 200 || r.code >= 300 {
		t.Fatalf("DELETE /api/admin/posts/%d returned %d body=%q", id, r.code, r.body)
	}
}

func trashTestFetch(t *testing.T, h *accessHarness, token, query string) trashTestPage {
	t.Helper()
	r := h.request(http.MethodGet, "/api/admin/trash"+query, "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(r, "list trash"+query)
	if r.code != http.StatusOK {
		t.Fatalf("GET /api/admin/trash%s returned %d body=%q", query, r.code, r.body)
	}
	var page trashTestPage
	if err := json.Unmarshal([]byte(r.body), &page); err != nil {
		t.Fatalf("decode trash listing: %v body=%q", err, r.body)
	}
	return page
}

func trashTestKeys(items []trashTestItem) []string {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, fmt.Sprintf("%s:%d", item.Kind, item.ID))
	}
	sort.Strings(keys)
	return keys
}

func trashTestWantKeys(t *testing.T, items []trashTestItem, want ...string) {
	t.Helper()
	sort.Strings(want)
	if got := trashTestKeys(items); !slices.Equal(got, want) {
		t.Errorf("trash items = %v, want %v", got, want)
	}
}

func trashTestWantCounts(t *testing.T, got map[string]int, want map[string]int) {
	t.Helper()
	for _, kind := range trashTestKinds {
		if got[kind] != want[kind] {
			t.Errorf("counts[%s] = %d, want %d (all counts %v)", kind, got[kind], want[kind], got)
		}
	}
}

func trashTestCheckExpiry(t *testing.T, item trashTestItem, days int) {
	t.Helper()
	if item.DeletedAt.IsZero() {
		t.Errorf("%s:%d has no deleted_at", item.Kind, item.ID)
		return
	}
	want := item.DeletedAt.Add(time.Duration(days) * 24 * time.Hour)
	if diff := item.ExpiresAt.Sub(want); diff < -trashTestTolerance || diff > trashTestTolerance {
		t.Errorf("%s:%d expires_at = %v, want deleted_at + %d days = %v", item.Kind, item.ID, item.ExpiresAt, days, want)
	}
}

func trashTestGetPost(h *accessHarness, token string, id uint) (trashTestPostView, int) {
	r := h.request(http.MethodGet, fmt.Sprintf("/api/admin/posts/%d", id), "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(r, "get post")
	var post trashTestPostView
	if r.code == http.StatusOK {
		_ = json.Unmarshal([]byte(r.body), &post)
	}
	return post, r.code
}

func trashTestLoadPost(db *gorm.DB, id uint) (core.Post, error) {
	var post core.Post
	err := db.Unscoped().First(&post, id).Error
	return post, err
}

func trashTestSoftDeleteProduct(t *testing.T, db *gorm.DB, slug string) uint {
	t.Helper()
	product := core.Product{Title: "Product " + slug, Slug: slug, Status: "active", Price: 100, Stock: 1}
	if err := db.Create(&product).Error; err != nil {
		t.Fatalf("create product fixture: %v", err)
	}
	if err := db.Delete(&product).Error; err != nil {
		t.Fatalf("soft delete product fixture: %v", err)
	}
	return product.ID
}

func trashTestSoftDeleteComment(t *testing.T, db *gorm.DB, postID uint) uint {
	t.Helper()
	comment := core.Comment{PostID: postID, Name: "Guest", Email: "guest@example.test", Body: "hello", BodyHTML: "<p>hello</p>", Status: "approved"}
	if err := db.Create(&comment).Error; err != nil {
		t.Fatalf("create comment fixture: %v", err)
	}
	if err := db.Delete(&comment).Error; err != nil {
		t.Fatalf("soft delete comment fixture: %v", err)
	}
	return comment.ID
}

func trashTestBackdate(t *testing.T, db *gorm.DB, model any, id uint, when time.Time) {
	t.Helper()
	if err := db.Unscoped().Model(model).Where("id = ?", id).Update("deleted_at", when).Error; err != nil {
		t.Fatalf("backdate %T %d: %v", model, id, err)
	}
}

func trashTestCountTrashed(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var total int64
	for _, model := range []any{&core.Post{}, &core.Product{}, &core.Comment{}} {
		var n int64
		if err := db.Unscoped().Model(model).Where("deleted_at IS NOT NULL").Count(&n).Error; err != nil {
			t.Fatalf("count trashed %T: %v", model, err)
		}
		total += n
	}
	return total
}

func trashTestSetRetention(t *testing.T, h *accessHarness, days string) {
	t.Helper()
	if err := h.db.Create(&core.Option{Key: "trash_retention_days", Value: days}).Error; err != nil {
		t.Fatalf("set trash_retention_days: %v", err)
	}
}

func TestTrashDeleteMovesPostToBinAndFreesSlug(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "trash-admin")
	adminToken := h.token(admin)

	id := trashTestCreatePost(t, h, adminToken, map[string]any{"title": "Bin me", "slug": "bin-me", "content": "body", "type": "post", "status": "draft"})
	trashTestDeletePost(t, h, adminToken, id)

	if _, code := trashTestGetPost(h, adminToken, id); code != http.StatusNotFound {
		t.Errorf("GET trashed post returned %d, want 404", code)
	}
	trashed, err := trashTestLoadPost(h.db, id)
	if err != nil {
		t.Fatalf("load trashed post row: %v", err)
	}
	if !trashed.DeletedAt.Valid {
		t.Errorf("deleted_at is null after DELETE, want a timestamp")
	}
	if trashed.DeletedByID == nil || *trashed.DeletedByID != admin.ID {
		t.Errorf("deleted_by_id = %v, want admin id %d", trashed.DeletedByID, admin.ID)
	}
	if want := fmt.Sprintf("bin-me~%d", id); trashed.Slug != want {
		t.Errorf("trashed slug = %q, want %q", trashed.Slug, want)
	}

	// The original slug is free again, so a new post may use it.
	reused := trashTestCreatePost(t, h, adminToken, map[string]any{"title": "Fresh", "slug": "bin-me", "content": "new", "type": "post", "status": "draft"})
	if reused == id {
		t.Errorf("new post reused the trashed row id %d", id)
	}
}

func TestTrashListScopesItemsByRole(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "trash-list-admin")
	editor := h.user("editor", "trash-list-editor")
	author := h.user("author", "trash-list-author")
	adminToken, editorToken, authorToken := h.token(admin), h.token(editor), h.token(author)

	adminPost := trashTestCreatePost(t, h, adminToken, map[string]any{"title": "Admin trashed", "slug": "admin-trashed", "content": "a", "type": "post", "status": "draft"})
	trashTestDeletePost(t, h, adminToken, adminPost)
	authorPost := trashTestCreatePost(t, h, authorToken, map[string]any{"title": "Author trashed", "slug": "author-trashed", "content": "b", "type": "post", "status": "draft"})
	trashTestDeletePost(t, h, authorToken, authorPost)

	if anon := h.request(http.MethodGet, "/api/admin/trash", "", "", echo.MIMEApplicationJSON, ""); anon.code != http.StatusUnauthorized {
		t.Errorf("anonymous GET /api/admin/trash returned %d, want 401", anon.code)
	}

	authorView := trashTestFetch(t, h, authorToken, "")
	trashTestWantKeys(t, authorView.Items, fmt.Sprintf("post:%d", authorPost))
	trashTestWantCounts(t, authorView.Counts, map[string]int{"all": 1, "post": 1})
	if authorView.RetentionDays != core.TrashRetentionDays {
		t.Errorf("author retention_days = %d, want %d", authorView.RetentionDays, core.TrashRetentionDays)
	}

	editorView := trashTestFetch(t, h, editorToken, "")
	trashTestWantKeys(t, editorView.Items, fmt.Sprintf("post:%d", adminPost), fmt.Sprintf("post:%d", authorPost))
	trashTestWantCounts(t, editorView.Counts, map[string]int{"all": 2, "post": 2})
	if editorView.RetentionDays != 30 {
		t.Errorf("retention_days = %d, want 30", editorView.RetentionDays)
	}

	wantTitle := map[uint]string{adminPost: "Admin trashed", authorPost: "Author trashed"}
	wantBy := map[uint]core.User{adminPost: admin, authorPost: author}
	for _, item := range editorView.Items {
		if item.Title != wantTitle[item.ID] {
			t.Errorf("item %d title = %q, want %q", item.ID, item.Title, wantTitle[item.ID])
		}
		trashTestCheckExpiry(t, item, 30)
		by := wantBy[item.ID]
		if item.DeletedByID != by.ID || item.DeletedBy != by.Username {
			t.Errorf("item %d deleted_by_id=%d deleted_by=%q, want %d %q", item.ID, item.DeletedByID, item.DeletedBy, by.ID, by.Username)
		}
	}
}

func TestTrashListReportsEveryKindAndFiltersByKind(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "trash-kinds-admin")
	adminToken := h.token(admin)

	host := core.Post{Title: "Comment host", Slug: "comment-host", Content: "host", Type: "post", Status: "published", AuthorID: admin.ID}
	if err := h.db.Create(&host).Error; err != nil {
		t.Fatalf("create comment host: %v", err)
	}
	post := trashTestCreatePost(t, h, adminToken, map[string]any{"title": "Kind post", "slug": "kind-post", "content": "p", "type": "post", "status": "draft"})
	trashTestDeletePost(t, h, adminToken, post)
	page := trashTestCreatePost(t, h, adminToken, map[string]any{"title": "Kind page", "slug": "kind-page", "content": "g", "type": "page", "status": "draft"})
	trashTestDeletePost(t, h, adminToken, page)
	product := trashTestSoftDeleteProduct(t, h.db, "kind-product")
	comment := trashTestSoftDeleteComment(t, h.db, host.ID)

	all := trashTestFetch(t, h, adminToken, "")
	wantAll := []string{
		fmt.Sprintf("post:%d", post), fmt.Sprintf("page:%d", page),
		fmt.Sprintf("product:%d", product), fmt.Sprintf("comment:%d", comment),
	}
	trashTestWantKeys(t, all.Items, wantAll...)
	trashTestWantCounts(t, all.Counts, map[string]int{"all": 4, "post": 1, "page": 1, "product": 1, "comment": 1})
	for _, item := range all.Items {
		if item.Kind == "post" && item.Title != "Kind post" {
			t.Errorf("post item title = %q, want %q", item.Title, "Kind post")
		}
		trashTestCheckExpiry(t, item, 30)
	}

	filters := []struct {
		kind string
		want string
	}{
		{"post", fmt.Sprintf("post:%d", post)},
		{"page", fmt.Sprintf("page:%d", page)},
		{"product", fmt.Sprintf("product:%d", product)},
		{"comment", fmt.Sprintf("comment:%d", comment)},
	}
	for _, f := range filters {
		filtered := trashTestFetch(t, h, adminToken, "?kind="+f.kind)
		trashTestWantKeys(t, filtered.Items, f.want)
	}
}

func TestTrashRetentionOptionChangesExpiry(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "trash-retention-admin")
	adminToken := h.token(admin)
	trashTestSetRetention(t, h, "7")

	id := trashTestCreatePost(t, h, adminToken, map[string]any{"title": "Short life", "slug": "short-life", "content": "s", "type": "post", "status": "draft"})
	trashTestDeletePost(t, h, adminToken, id)

	page := trashTestFetch(t, h, adminToken, "")
	if page.RetentionDays != 7 {
		t.Fatalf("retention_days = %d, want 7", page.RetentionDays)
	}
	if len(page.Items) != 1 {
		t.Fatalf("trash items = %d, want 1", len(page.Items))
	}
	trashTestCheckExpiry(t, page.Items[0], 7)
}

func TestTrashRestoreReturnsPostWithOriginalSlug(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "trash-restore-admin")
	adminToken := h.token(admin)

	id := trashTestCreatePost(t, h, adminToken, map[string]any{"title": "Restore me", "slug": "restore-me", "content": "r", "type": "post", "status": "draft"})
	trashTestDeletePost(t, h, adminToken, id)

	r := h.request(http.MethodPost, fmt.Sprintf("/api/admin/trash/post/%d/restore", id), "", adminToken, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(r, "restore post")
	if r.code != http.StatusOK {
		t.Fatalf("restore returned %d body=%q, want 200", r.code, r.body)
	}
	restored, code := trashTestGetPost(h, adminToken, id)
	if code != http.StatusOK {
		t.Fatalf("GET restored post returned %d, want 200", code)
	}
	if restored.Slug != "restore-me" {
		t.Errorf("restored slug = %q, want %q", restored.Slug, "restore-me")
	}
	row, err := trashTestLoadPost(h.db, id)
	if err != nil {
		t.Fatalf("load restored row: %v", err)
	}
	if row.DeletedAt.Valid {
		t.Errorf("deleted_at still set after restore")
	}
}

func TestTrashRestoreKeepsSuffixWhenSlugIsTaken(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "trash-taken-admin")
	adminToken := h.token(admin)

	id := trashTestCreatePost(t, h, adminToken, map[string]any{"title": "Original", "slug": "slug-taken", "content": "o", "type": "post", "status": "draft"})
	trashTestDeletePost(t, h, adminToken, id)
	trashTestCreatePost(t, h, adminToken, map[string]any{"title": "Squatter", "slug": "slug-taken", "content": "n", "type": "post", "status": "draft"})

	r := h.request(http.MethodPost, fmt.Sprintf("/api/admin/trash/post/%d/restore", id), "", adminToken, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(r, "restore post with taken slug")
	if r.code != http.StatusOK {
		t.Fatalf("restore with taken slug returned %d body=%q, want 200", r.code, r.body)
	}
	restored, code := trashTestGetPost(h, adminToken, id)
	if code != http.StatusOK {
		t.Fatalf("GET restored post returned %d, want 200", code)
	}
	if want := fmt.Sprintf("slug-taken~%d", id); restored.Slug != want {
		t.Errorf("restored slug = %q, want %q", restored.Slug, want)
	}
}

func TestTrashRestoreRejectsUnknownKindMissingAndForeignItems(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "trash-reject-admin")
	author := h.user("author", "trash-reject-author")
	adminToken, authorToken := h.token(admin), h.token(author)

	live := trashTestCreatePost(t, h, adminToken, map[string]any{"title": "Live", "slug": "reject-live", "content": "l", "type": "post", "status": "draft"})
	trashed := trashTestCreatePost(t, h, adminToken, map[string]any{"title": "Trashed", "slug": "reject-trashed", "content": "t", "type": "post", "status": "draft"})
	trashTestDeletePost(t, h, adminToken, trashed)

	cases := []struct {
		name  string
		kind  string
		id    uint
		token string
		want  int
	}{
		{"item not in the bin", "post", live, adminToken, http.StatusNotFound},
		{"unknown id", "post", 987654321, adminToken, http.StatusNotFound},
		{"unknown kind", "widget", trashed, adminToken, http.StatusBadRequest},
		{"author restoring another author's or admin's item", "post", trashed, authorToken, http.StatusNotFound},
	}
	for _, c := range cases {
		r := h.request(http.MethodPost, fmt.Sprintf("/api/admin/trash/%s/%d/restore", c.kind, c.id), "", c.token, echo.MIMEApplicationJSON, "")
		h.mustNoPanic(r, c.name)
		if r.code != c.want {
			t.Errorf("%s: restore returned %d, want %d; body=%q", c.name, r.code, c.want, r.body)
		}
	}
	row, err := trashTestLoadPost(h.db, trashed)
	if err != nil {
		t.Fatalf("load trashed row: %v", err)
	}
	if !row.DeletedAt.Valid {
		t.Errorf("rejected restore changed the trashed post")
	}
}

func TestTrashRestoreSupportsPagesProductsAndComments(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "trash-kinds-restore")
	adminToken := h.token(admin)

	host := core.Post{Title: "Comment host", Slug: "restore-host", Content: "host", Type: "post", Status: "published", AuthorID: admin.ID}
	if err := h.db.Create(&host).Error; err != nil {
		t.Fatalf("create comment host: %v", err)
	}
	page := trashTestCreatePost(t, h, adminToken, map[string]any{"title": "Restore page", "slug": "restore-page", "content": "g", "type": "page", "status": "draft"})
	trashTestDeletePost(t, h, adminToken, page)
	product := trashTestSoftDeleteProduct(t, h.db, "restore-product")
	comment := trashTestSoftDeleteComment(t, h.db, host.ID)

	restorePage := h.request(http.MethodPost, fmt.Sprintf("/api/admin/trash/page/%d/restore", page), "", adminToken, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(restorePage, "restore page")
	if restorePage.code != http.StatusOK {
		t.Errorf("restore page returned %d body=%q, want 200", restorePage.code, restorePage.body)
	} else if _, code := trashTestGetPost(h, adminToken, page); code != http.StatusOK {
		t.Errorf("GET restored page returned %d, want 200", code)
	}

	restoreProduct := h.request(http.MethodPost, fmt.Sprintf("/api/admin/trash/product/%d/restore", product), "", adminToken, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(restoreProduct, "restore product")
	if restoreProduct.code != http.StatusOK {
		t.Errorf("restore product returned %d body=%q, want 200", restoreProduct.code, restoreProduct.body)
	}
	var restoredProduct core.Product
	if err := h.db.Unscoped().First(&restoredProduct, product).Error; err != nil {
		t.Fatalf("load product row: %v", err)
	}
	if restoredProduct.DeletedAt.Valid {
		t.Errorf("product still trashed after restore")
	}

	restoreComment := h.request(http.MethodPost, fmt.Sprintf("/api/admin/trash/comment/%d/restore", comment), "", adminToken, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(restoreComment, "restore comment")
	if restoreComment.code != http.StatusOK {
		t.Errorf("restore comment returned %d body=%q, want 200", restoreComment.code, restoreComment.body)
	}
	var restoredComment core.Comment
	if err := h.db.Unscoped().First(&restoredComment, comment).Error; err != nil {
		t.Fatalf("load comment row: %v", err)
	}
	if restoredComment.DeletedAt.Valid {
		t.Errorf("comment still trashed after restore")
	}
}

func TestTrashPermanentDeleteRemovesRowOnlyForTrashedItems(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "trash-perm-admin")
	author := h.user("author", "trash-perm-author")
	adminToken, authorToken := h.token(admin), h.token(author)

	trashed := trashTestCreatePost(t, h, adminToken, map[string]any{"title": "Purge me", "slug": "purge-me", "content": "p", "type": "post", "status": "draft"})
	trashTestDeletePost(t, h, adminToken, trashed)
	live := trashTestCreatePost(t, h, adminToken, map[string]any{"title": "Keep me", "slug": "keep-me", "content": "k", "type": "post", "status": "draft"})

	foreign := h.request(http.MethodDelete, fmt.Sprintf("/api/admin/trash/post/%d", trashed), "", authorToken, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(foreign, "author permanent delete")
	if foreign.code != http.StatusNotFound {
		t.Errorf("author permanent delete of admin's trashed post returned %d, want 404", foreign.code)
	}

	notTrashed := h.request(http.MethodDelete, fmt.Sprintf("/api/admin/trash/post/%d", live), "", adminToken, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(notTrashed, "permanent delete of live post")
	if notTrashed.code != http.StatusNotFound {
		t.Errorf("permanent delete of a live post returned %d, want 404", notTrashed.code)
	}
	if _, err := trashTestLoadPost(h.db, live); err != nil {
		t.Errorf("live post was affected by a rejected permanent delete: %v", err)
	}

	ok := h.request(http.MethodDelete, fmt.Sprintf("/api/admin/trash/post/%d", trashed), "", adminToken, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(ok, "permanent delete")
	if ok.code != http.StatusOK {
		t.Fatalf("permanent delete returned %d body=%q, want 200", ok.code, ok.body)
	}
	if _, err := trashTestLoadPost(h.db, trashed); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("permanently deleted row still readable with Unscoped: err=%v", err)
	}
}

func TestTrashEmptyBinNeedsManageRoleAndDeletesEverything(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "trash-empty-admin")
	editor := h.user("editor", "trash-empty-editor")
	author := h.user("author", "trash-empty-author")
	adminToken, editorToken, authorToken := h.token(admin), h.token(editor), h.token(author)

	host := core.Post{Title: "Comment host", Slug: "empty-host", Content: "host", Type: "post", Status: "published", AuthorID: admin.ID}
	if err := h.db.Create(&host).Error; err != nil {
		t.Fatalf("create comment host: %v", err)
	}
	adminPost := trashTestCreatePost(t, h, adminToken, map[string]any{"title": "Empty a", "slug": "empty-a", "content": "a", "type": "post", "status": "draft"})
	trashTestDeletePost(t, h, adminToken, adminPost)
	authorPost := trashTestCreatePost(t, h, authorToken, map[string]any{"title": "Empty b", "slug": "empty-b", "content": "b", "type": "post", "status": "draft"})
	trashTestDeletePost(t, h, authorToken, authorPost)
	page := trashTestCreatePost(t, h, adminToken, map[string]any{"title": "Empty page", "slug": "empty-page", "content": "g", "type": "page", "status": "draft"})
	trashTestDeletePost(t, h, adminToken, page)
	trashTestSoftDeleteProduct(t, h.db, "empty-product")
	trashTestSoftDeleteComment(t, h.db, host.ID)

	before := trashTestCountTrashed(t, h.db)
	if before != 5 {
		t.Fatalf("fixture trashed rows = %d, want 5", before)
	}

	denied := h.request(http.MethodDelete, "/api/admin/trash", "", authorToken, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(denied, "author empty trash")
	if denied.code != http.StatusForbidden {
		t.Errorf("author DELETE /api/admin/trash returned %d, want 403", denied.code)
	}
	if after := trashTestCountTrashed(t, h.db); after != before {
		t.Errorf("author DELETE changed trashed rows: %d -> %d", before, after)
	}

	emptied := h.request(http.MethodDelete, "/api/admin/trash", "", editorToken, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(emptied, "editor empty trash")
	if emptied.code != http.StatusOK {
		t.Fatalf("editor DELETE /api/admin/trash returned %d body=%q, want 200", emptied.code, emptied.body)
	}
	var result map[string]int
	if err := json.Unmarshal([]byte(emptied.body), &result); err != nil {
		t.Fatalf("decode empty result: %v body=%q", err, emptied.body)
	}
	if result["deleted"] != 5 {
		t.Errorf("deleted = %d, want 5", result["deleted"])
	}
	if left := trashTestCountTrashed(t, h.db); left != 0 {
		t.Errorf("%d trashed rows remain after emptying the bin", left)
	}
	remaining := trashTestFetch(t, h, editorToken, "")
	trashTestWantKeys(t, remaining.Items)
	trashTestWantCounts(t, remaining.Counts, map[string]int{})
}

func TestTrashPurgeExpiredRemovesOnlyPostsPastRetention(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "trash-purge-admin")

	old := core.Post{Title: "Old", Slug: "purge-old", Content: "old", Type: "post", Status: "draft", AuthorID: admin.ID}
	fresh := core.Post{Title: "Fresh", Slug: "purge-fresh", Content: "fresh", Type: "post", Status: "draft", AuthorID: admin.ID}
	for _, p := range []*core.Post{&old, &fresh} {
		if err := h.db.Create(p).Error; err != nil {
			t.Fatalf("create post fixture: %v", err)
		}
		if err := h.db.Delete(p).Error; err != nil {
			t.Fatalf("soft delete post fixture: %v", err)
		}
	}
	now := time.Now()
	trashTestBackdate(t, h.db, &core.Post{}, old.ID, now.Add(-40*24*time.Hour))
	trashTestBackdate(t, h.db, &core.Post{}, fresh.ID, now.Add(-24*time.Hour))

	purged, err := PurgeExpiredTrash(h.db, now)
	if err != nil {
		t.Fatalf("PurgeExpiredTrash: %v", err)
	}
	if purged != 1 {
		t.Errorf("PurgeExpiredTrash removed %d rows, want 1", purged)
	}
	if _, err := trashTestLoadPost(h.db, old.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("post trashed 40 days ago still exists: err=%v", err)
	}
	if _, err := trashTestLoadPost(h.db, fresh.ID); err != nil {
		t.Errorf("post trashed 1 day ago was purged: %v", err)
	}
}

func TestTrashPurgeExpiredCoversCommentsAndProducts(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "trash-purge-kinds")
	host := core.Post{Title: "Host", Slug: "purge-host", Content: "host", Type: "post", Status: "published", AuthorID: admin.ID}
	if err := h.db.Create(&host).Error; err != nil {
		t.Fatalf("create host: %v", err)
	}

	oldComment := trashTestSoftDeleteComment(t, h.db, host.ID)
	freshComment := trashTestSoftDeleteComment(t, h.db, host.ID)
	oldProduct := trashTestSoftDeleteProduct(t, h.db, "purge-old-product")
	freshProduct := trashTestSoftDeleteProduct(t, h.db, "purge-fresh-product")

	now := time.Now()
	trashTestBackdate(t, h.db, &core.Comment{}, oldComment, now.Add(-40*24*time.Hour))
	trashTestBackdate(t, h.db, &core.Comment{}, freshComment, now.Add(-24*time.Hour))
	trashTestBackdate(t, h.db, &core.Product{}, oldProduct, now.Add(-40*24*time.Hour))
	trashTestBackdate(t, h.db, &core.Product{}, freshProduct, now.Add(-24*time.Hour))

	purged, err := PurgeExpiredTrash(h.db, now)
	if err != nil {
		t.Fatalf("PurgeExpiredTrash: %v", err)
	}
	if purged != 2 {
		t.Errorf("PurgeExpiredTrash removed %d rows, want 2 (one expired comment, one expired product)", purged)
	}
	// Separate destinations: gorm adds a non-zero primary key already held by the struct to the query.
	var oldRow, freshRow core.Comment
	if err := h.db.Unscoped().First(&oldRow, oldComment).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("expired comment still exists: err=%v", err)
	}
	if err := h.db.Unscoped().First(&freshRow, freshComment).Error; err != nil {
		t.Errorf("fresh comment was purged: %v", err)
	}
	var oldProductRow, freshProductRow core.Product
	if err := h.db.Unscoped().First(&oldProductRow, oldProduct).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("expired product still exists: err=%v", err)
	}
	if err := h.db.Unscoped().First(&freshProductRow, freshProduct).Error; err != nil {
		t.Errorf("fresh product was purged: %v", err)
	}
}

func TestTrashPurgeExpiredHonorsRetentionOption(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "trash-purge-option")
	trashTestSetRetention(t, h, "7")

	tenDays := core.Post{Title: "Ten days", Slug: "purge-ten", Content: "x", Type: "post", Status: "draft", AuthorID: admin.ID}
	threeDays := core.Post{Title: "Three days", Slug: "purge-three", Content: "y", Type: "post", Status: "draft", AuthorID: admin.ID}
	for _, p := range []*core.Post{&tenDays, &threeDays} {
		if err := h.db.Create(p).Error; err != nil {
			t.Fatalf("create post fixture: %v", err)
		}
		if err := h.db.Delete(p).Error; err != nil {
			t.Fatalf("soft delete post fixture: %v", err)
		}
	}
	now := time.Now()
	trashTestBackdate(t, h.db, &core.Post{}, tenDays.ID, now.Add(-10*24*time.Hour))
	trashTestBackdate(t, h.db, &core.Post{}, threeDays.ID, now.Add(-3*24*time.Hour))

	purged, err := PurgeExpiredTrash(h.db, now)
	if err != nil {
		t.Fatalf("PurgeExpiredTrash: %v", err)
	}
	if purged != 1 {
		t.Errorf("with trash_retention_days=7 PurgeExpiredTrash removed %d rows, want 1", purged)
	}
	if _, err := trashTestLoadPost(h.db, tenDays.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("post trashed 10 days ago survived a 7-day retention: err=%v", err)
	}
	if _, err := trashTestLoadPost(h.db, threeDays.ID); err != nil {
		t.Errorf("post trashed 3 days ago was purged under a 7-day retention: %v", err)
	}
}
