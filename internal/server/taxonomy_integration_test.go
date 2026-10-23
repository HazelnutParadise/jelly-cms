package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
)

// Acceptance tests for the taxonomy API (ticket 04, slot C1). The contract is
// the comment block in handlers_taxonomy.go plus the post input rules that
// applyPostTerms must add to handlers_posts.go. Fixtures are seeded directly
// into the isolated PostgreSQL database; the behavior under test always goes
// through HTTP. Run with JELLY_TEST_POSTGRES=1, otherwise the harness skips.

// taxCategoryRow is the subset of a category row the contract promises.
type taxCategoryRow struct {
	ID           uint   `json:"id"`
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	Description  string `json:"description"`
	ParentID     *uint  `json:"parent_id"`
	Color        string `json:"color"`
	HideFromList bool   `json:"hide_from_list"`
	NoCascade    bool   `json:"no_cascade"`
	Position     int    `json:"position"`
	PostCount    int64  `json:"post_count"`
}

// taxTagRow is the subset of a tag row the contract promises.
type taxTagRow struct {
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Color       string `json:"color"`
	Description string `json:"description"`
	PostCount   int64  `json:"post_count"`
}

// taxPostRow is the subset of a post the taxonomy and new post fields touch.
type taxPostRow struct {
	ID            uint             `json:"id"`
	Title         string           `json:"title"`
	Slug          string           `json:"slug"`
	Excerpt       string           `json:"excerpt"`
	Cover         string           `json:"cover"`
	AllowComments *bool            `json:"allow_comments"`
	Pinned        bool             `json:"pinned"`
	Visibility    string           `json:"visibility"`
	Categories    []taxCategoryRow `json:"categories"`
	Tags          []taxTagRow      `json:"tags"`
}

// taxCall sends a JSON request. A string body is sent verbatim, so malformed
// JSON can be probed; any other non-nil body is marshalled.
func taxCall(h *accessHarness, method, path string, body any, token string) accessResult {
	h.t.Helper()
	raw := ""
	switch v := body.(type) {
	case nil:
	case string:
		raw = v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			h.t.Fatalf("marshal request body: %v", err)
		}
		raw = string(b)
	}
	return h.request(method, path, raw, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
}

// taxExpect fails unless the response has one of codes, a JSON content type
// and, when out is non-nil, a body that decodes into out.
func taxExpect(t *testing.T, r accessResult, out any, codes ...int) {
	t.Helper()
	if r.panicked != nil {
		t.Fatalf("handler panicked: %v", r.panicked)
	}
	matched := false
	for _, code := range codes {
		if r.code == code {
			matched = true
		}
	}
	if !matched {
		t.Fatalf("status = %d, want one of %v; body=%q", r.code, codes, r.body)
	}
	if !strings.Contains(strings.ToLower(r.header.Get(echo.HeaderContentType)), "application/json") {
		t.Fatalf("content type = %q, want JSON", r.header.Get(echo.HeaderContentType))
	}
	if out != nil {
		if err := json.Unmarshal([]byte(r.body), out); err != nil {
			t.Fatalf("decode body %q: %v", r.body, err)
		}
	}
}

// taxExpectError checks a rejected request: the given status and an
// {"error": "..."} JSON body with a message.
func taxExpectError(t *testing.T, r accessResult, code int) {
	t.Helper()
	var payload map[string]string
	taxExpect(t, r, &payload, code)
	if payload["error"] == "" {
		t.Fatalf("error body %q has no error message", r.body)
	}
}

func taxSeedCategory(t *testing.T, h *accessHarness, name, slug string, parentID *uint, position int) core.Category {
	t.Helper()
	row := core.Category{Name: name, Slug: slug, ParentID: parentID, Position: position}
	if err := h.db.Create(&row).Error; err != nil {
		t.Fatalf("seed category %q: %v", slug, err)
	}
	return row
}

func taxSeedTag(t *testing.T, h *accessHarness, name, slug string) core.Tag {
	t.Helper()
	row := core.Tag{Name: name, Slug: slug}
	if err := h.db.Create(&row).Error; err != nil {
		t.Fatalf("seed tag %q: %v", slug, err)
	}
	return row
}

func taxSeedPost(t *testing.T, h *accessHarness, author core.User, slug string) core.Post {
	t.Helper()
	post := core.Post{Title: "Seed " + slug, Slug: slug, Content: "seed body", Type: "post", Status: "published", AuthorID: author.ID}
	if err := h.db.Create(&post).Error; err != nil {
		t.Fatalf("seed post %q: %v", slug, err)
	}
	return post
}

func taxAssign(t *testing.T, h *accessHarness, post core.Post, categories []core.Category, tags []core.Tag) {
	t.Helper()
	for i := range categories {
		if err := h.db.Model(&post).Association("Categories").Append(&categories[i]); err != nil {
			t.Fatalf("assign category %q to post %d: %v", categories[i].Slug, post.ID, err)
		}
	}
	for i := range tags {
		if err := h.db.Model(&post).Association("Tags").Append(&tags[i]); err != nil {
			t.Fatalf("assign tag %q to post %d: %v", tags[i].Slug, post.ID, err)
		}
	}
}

func taxDBCategory(t *testing.T, h *accessHarness, id uint) core.Category {
	t.Helper()
	var row core.Category
	if err := h.db.First(&row, id).Error; err != nil {
		t.Fatalf("load category %d from database: %v", id, err)
	}
	return row
}

func taxDBTag(t *testing.T, h *accessHarness, id uint) core.Tag {
	t.Helper()
	var row core.Tag
	if err := h.db.First(&row, id).Error; err != nil {
		t.Fatalf("load tag %d from database: %v", id, err)
	}
	return row
}

func taxRowCounts(t *testing.T, h *accessHarness) (int64, int64) {
	t.Helper()
	var categories, tags int64
	if err := h.db.Model(&core.Category{}).Count(&categories).Error; err != nil {
		t.Fatalf("count categories: %v", err)
	}
	if err := h.db.Model(&core.Tag{}).Count(&tags).Error; err != nil {
		t.Fatalf("count tags: %v", err)
	}
	return categories, tags
}

func taxGetPost(t *testing.T, h *accessHarness, token string, id uint) taxPostRow {
	t.Helper()
	var row taxPostRow
	taxExpect(t, taxCall(h, http.MethodGet, fmt.Sprintf("/api/admin/posts/%d", id), nil, token), &row, http.StatusOK)
	return row
}

func taxListCategories(t *testing.T, h *accessHarness, token string) []taxCategoryRow {
	t.Helper()
	var rows []taxCategoryRow
	taxExpect(t, taxCall(h, http.MethodGet, "/api/admin/categories", nil, token), &rows, http.StatusOK)
	return rows
}

func taxListTags(t *testing.T, h *accessHarness, token, query string) []taxTagRow {
	t.Helper()
	path := "/api/admin/tags"
	if query != "" {
		path += "?q=" + url.QueryEscape(query)
	}
	var rows []taxTagRow
	taxExpect(t, taxCall(h, http.MethodGet, path, nil, token), &rows, http.StatusOK)
	return rows
}

func taxFindCategory(t *testing.T, rows []taxCategoryRow, id uint) taxCategoryRow {
	t.Helper()
	for _, row := range rows {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("category %d missing from listing %+v", id, rows)
	return taxCategoryRow{}
}

func taxFindTagByName(t *testing.T, rows []taxTagRow, name string) taxTagRow {
	t.Helper()
	for _, row := range rows {
		if row.Name == name {
			return row
		}
	}
	t.Fatalf("tag %q missing from listing %+v", name, rows)
	return taxTagRow{}
}

func taxIndexOf(rows []taxCategoryRow, id uint) int {
	for i, row := range rows {
		if row.ID == id {
			return i
		}
	}
	return -1
}

func taxCategoryIDs(rows []taxCategoryRow) []uint {
	ids := make([]uint, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

func taxTagIDs(rows []taxTagRow) []uint {
	ids := make([]uint, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

func taxTagNames(rows []taxTagRow) []string {
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Name)
	}
	return names
}

// taxSameSet reports whether got and want hold the same elements, counting
// repeats, in any order.
func taxSameSet[T comparable](got []T, want ...T) bool {
	if len(got) != len(want) {
		return false
	}
	counts := make(map[T]int, len(got))
	for _, g := range got {
		counts[g]++
	}
	for _, w := range want {
		counts[w]--
		if counts[w] < 0 {
			return false
		}
	}
	return true
}

func taxParentString(p *uint) string {
	if p == nil {
		return "nil"
	}
	return fmt.Sprint(*p)
}

func taxSameParent(got, want *uint) bool {
	if got == nil || want == nil {
		return got == want
	}
	return *got == *want
}

type taxLayout struct {
	id       uint
	parent   *uint
	position int
}

func taxCheckLayout(t *testing.T, rows []taxCategoryRow, want []taxLayout) {
	t.Helper()
	for _, w := range want {
		row := taxFindCategory(t, rows, w.id)
		if !taxSameParent(row.ParentID, w.parent) {
			t.Errorf("category %d parent_id = %s, want %s", w.id, taxParentString(row.ParentID), taxParentString(w.parent))
		}
		if row.Position != w.position {
			t.Errorf("category %d position = %d, want %d", w.id, row.Position, w.position)
		}
	}
}

func TestTaxonomyAnonymousRequestsReturn401JSON(t *testing.T) {
	h := newAccessHarness(t)
	probes := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/admin/categories", nil},
		{http.MethodPost, "/api/admin/categories", map[string]any{"name": "probe"}},
		{http.MethodPut, "/api/admin/categories/987654321", map[string]any{"name": "probe"}},
		{http.MethodDelete, "/api/admin/categories/987654321", nil},
		{http.MethodPost, "/api/admin/categories/reorder", []map[string]any{{"id": 1, "parent_id": nil, "position": 0}}},
		{http.MethodGet, "/api/admin/tags", nil},
		{http.MethodPost, "/api/admin/tags", map[string]any{"name": "probe"}},
		{http.MethodPut, "/api/admin/tags/987654321", map[string]any{"name": "probe"}},
		{http.MethodDelete, "/api/admin/tags/987654321", nil},
	}
	categoriesBefore, tagsBefore := taxRowCounts(t, h)
	for _, p := range probes {
		taxExpectError(t, taxCall(h, p.method, p.path, p.body, ""), http.StatusUnauthorized)
	}
	if categories, tags := taxRowCounts(t, h); categories != categoriesBefore || tags != tagsBefore {
		t.Fatalf("anonymous taxonomy requests changed rows: categories %d->%d, tags %d->%d", categoriesBefore, categories, tagsBefore, tags)
	}
}

func TestTaxonomyRoleGates(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "tax-gate-author")
	editor := h.user("editor", "tax-gate-editor")
	authorToken, editorToken := h.token(author), h.token(editor)
	cat := taxSeedCategory(t, h, "原分類", "gate-category", nil, 0)
	tag := taxSeedTag(t, h, "原標籤", "gate-tag")
	catPath := fmt.Sprintf("/api/admin/categories/%d", cat.ID)
	tagPath := fmt.Sprintf("/api/admin/tags/%d", tag.ID)

	// Authors read categories and tags.
	taxListCategories(t, h, authorToken)
	taxListTags(t, h, authorToken, "")

	// Authors cannot change categories.
	taxExpectError(t, taxCall(h, http.MethodPost, "/api/admin/categories", map[string]any{"name": "作者分類"}, authorToken), http.StatusForbidden)
	taxExpectError(t, taxCall(h, http.MethodPut, catPath, map[string]any{"name": "作者改名"}, authorToken), http.StatusForbidden)
	taxExpectError(t, taxCall(h, http.MethodDelete, catPath, nil, authorToken), http.StatusForbidden)
	taxExpectError(t, taxCall(h, http.MethodPost, "/api/admin/categories/reorder", []map[string]any{{"id": cat.ID, "parent_id": nil, "position": 5}}, authorToken), http.StatusForbidden)

	// Authors create tags but cannot edit or delete them.
	taxExpectError(t, taxCall(h, http.MethodPut, tagPath, map[string]any{"name": "作者改名"}, authorToken), http.StatusForbidden)
	taxExpectError(t, taxCall(h, http.MethodDelete, tagPath, nil, authorToken), http.StatusForbidden)
	var created taxTagRow
	taxExpect(t, taxCall(h, http.MethodPost, "/api/admin/tags", map[string]any{"name": "作者建立的標籤"}, authorToken), &created, http.StatusCreated)
	if created.Name != "作者建立的標籤" || created.ID == 0 {
		t.Fatalf("author-created tag = %+v, want name 作者建立的標籤 with an ID", created)
	}

	// The rejected author writes left the seeded rows alone.
	if got := taxDBCategory(t, h, cat.ID); got.Name != "原分類" || got.Position != 0 || got.ParentID != nil {
		t.Fatalf("category after rejected author writes = %+v, want unchanged", got)
	}
	if got := taxDBTag(t, h, tag.ID); got.Name != "原標籤" {
		t.Fatalf("tag after rejected author writes = %+v, want unchanged", got)
	}
	if categories, _ := taxRowCounts(t, h); categories != 1 {
		t.Fatalf("categories after rejected author POST = %d, want 1", categories)
	}

	// Editors manage categories and tags.
	taxExpect(t, taxCall(h, http.MethodPost, "/api/admin/categories", map[string]any{"name": "編輯分類"}, editorToken), nil, http.StatusCreated)
	var renamed taxTagRow
	taxExpect(t, taxCall(h, http.MethodPut, tagPath, map[string]any{"name": "編輯改名"}, editorToken), &renamed, http.StatusOK)
	if renamed.Name != "編輯改名" {
		t.Fatalf("editor tag rename = %+v, want 編輯改名", renamed)
	}
}

func TestTaxonomyCategoryCreateSlugAndValidation(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("editor", "tax-create-editor"))
	createPath := "/api/admin/categories"

	cases := []struct{ name, slug string }{
		{"食譜", "食譜"},
		{"食譜", "食譜-2"},
		{"食譜", "食譜-3"},
		{"Hello World", "hello-world"},
	}
	for _, c := range cases {
		var row taxCategoryRow
		taxExpect(t, taxCall(h, http.MethodPost, createPath, map[string]any{"name": c.name}, token), &row, http.StatusCreated)
		if row.Name != c.name || row.Slug != c.slug {
			t.Fatalf("created %q: name=%q slug=%q, want slug %q", c.name, row.Name, row.Slug, c.slug)
		}
		if row.PostCount != 0 {
			t.Fatalf("new category %q post_count = %d, want 0", c.name, row.PostCount)
		}
	}

	// A caller-supplied slug that is already taken also gets a numeric suffix.
	var explicit taxCategoryRow
	taxExpect(t, taxCall(h, http.MethodPost, createPath, map[string]any{"name": "Cooking", "slug": "食譜"}, token), &explicit, http.StatusCreated)
	if explicit.Slug != "食譜-4" {
		t.Fatalf("explicit taken slug = %q, want 食譜-4", explicit.Slug)
	}

	// Blank, missing and malformed input is rejected and creates nothing.
	rejected := []any{
		map[string]any{"name": "  "},
		map[string]any{"name": ""},
		map[string]any{"description": "沒有名稱"},
		"{not json",
	}
	for _, body := range rejected {
		taxExpectError(t, taxCall(h, http.MethodPost, createPath, body, token), http.StatusBadRequest)
	}
	if rows := taxListCategories(t, h, token); len(rows) != 5 {
		t.Fatalf("category count after rejected creates = %d, want 5", len(rows))
	}
}

func TestTaxonomyCategoryParentValidationAndCycles(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("editor", "tax-parent-editor"))
	a := taxSeedCategory(t, h, "A", "tree-a", nil, 0)
	b := taxSeedCategory(t, h, "B", "tree-b", &a.ID, 0)
	c := taxSeedCategory(t, h, "C", "tree-c", nil, 1)
	missing := uint(987654321)
	aPath := fmt.Sprintf("/api/admin/categories/%d", a.ID)
	cPath := fmt.Sprintf("/api/admin/categories/%d", c.ID)
	reorderPath := "/api/admin/categories/reorder"

	// Unknown parents are rejected.
	taxExpectError(t, taxCall(h, http.MethodPost, "/api/admin/categories", map[string]any{"name": "孤兒", "parent_id": missing}, token), http.StatusBadRequest)
	taxExpectError(t, taxCall(h, http.MethodPut, aPath, map[string]any{"parent_id": missing}, token), http.StatusBadRequest)
	taxExpectError(t, taxCall(h, http.MethodPost, reorderPath, []map[string]any{{"id": c.ID, "parent_id": missing, "position": 0}}, token), http.StatusBadRequest)

	// A category cannot become its own ancestor: its child, itself, or a descendant
	// reached through a deeper chain.
	taxExpectError(t, taxCall(h, http.MethodPut, aPath, map[string]any{"parent_id": b.ID}, token), http.StatusBadRequest)
	taxExpectError(t, taxCall(h, http.MethodPut, aPath, map[string]any{"parent_id": a.ID}, token), http.StatusBadRequest)
	taxExpectError(t, taxCall(h, http.MethodPost, reorderPath, []map[string]any{
		{"id": a.ID, "parent_id": b.ID, "position": 0},
		{"id": b.ID, "parent_id": a.ID, "position": 0},
	}, token), http.StatusBadRequest)

	// Rejected writes changed nothing.
	if got := taxDBCategory(t, h, a.ID); got.ParentID != nil {
		t.Fatalf("A parent after rejected writes = %s, want nil", taxParentString(got.ParentID))
	}
	if got := taxDBCategory(t, h, b.ID); got.ParentID == nil || *got.ParentID != a.ID {
		t.Fatalf("B parent after rejected writes = %s, want %d", taxParentString(got.ParentID), a.ID)
	}
	if categories, _ := taxRowCounts(t, h); categories != 3 {
		t.Fatalf("category count after rejected writes = %d, want 3", categories)
	}

	// Positive control: a legal move under a deeper node succeeds, and then
	// moving the root under that node is a cycle.
	var moved taxCategoryRow
	taxExpect(t, taxCall(h, http.MethodPut, cPath, map[string]any{"parent_id": b.ID}, token), &moved, http.StatusOK)
	if moved.ParentID == nil || *moved.ParentID != b.ID {
		t.Fatalf("C after legal move parent = %s, want %d", taxParentString(moved.ParentID), b.ID)
	}
	taxExpectError(t, taxCall(h, http.MethodPut, aPath, map[string]any{"parent_id": c.ID}, token), http.StatusBadRequest)
}

func TestTaxonomyCategoryUpdatePartialAndNotFound(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("editor", "tax-update-editor"))
	x := taxSeedCategory(t, h, "原名", "update-x", nil, 0)
	path := fmt.Sprintf("/api/admin/categories/%d", x.ID)

	var updated taxCategoryRow
	taxExpect(t, taxCall(h, http.MethodPut, path, map[string]any{
		"name": "食譜更新", "color": "accent", "hide_from_list": true, "no_cascade": true, "description": "說明",
	}, token), &updated, http.StatusOK)
	if updated.Name != "食譜更新" || updated.Color != "accent" || !updated.HideFromList || !updated.NoCascade || updated.Description != "說明" {
		t.Fatalf("PUT response = %+v, want new name, color, hide_from_list, no_cascade and description", updated)
	}
	row := taxFindCategory(t, taxListCategories(t, h, token), x.ID)
	if row.Name != "食譜更新" || row.Color != "accent" || !row.HideFromList || !row.NoCascade || row.Description != "說明" {
		t.Fatalf("GET after PUT = %+v, want the updated fields", row)
	}

	// Partial update: an explicit false is applied, omitted fields are kept.
	taxExpect(t, taxCall(h, http.MethodPut, path, map[string]any{"hide_from_list": false}, token), &updated, http.StatusOK)
	row = taxFindCategory(t, taxListCategories(t, h, token), x.ID)
	if row.HideFromList {
		t.Fatalf("hide_from_list after PUT false = true, want false")
	}
	if row.Name != "食譜更新" || row.Color != "accent" || !row.NoCascade {
		t.Fatalf("omitted fields changed by partial PUT: %+v", row)
	}
	taxExpect(t, taxCall(h, http.MethodPut, path, map[string]any{"color": "ink"}, token), &updated, http.StatusOK)
	row = taxFindCategory(t, taxListCategories(t, h, token), x.ID)
	if row.Color != "ink" || row.Name != "食譜更新" {
		t.Fatalf("GET after color-only PUT = %+v, want color ink and name kept", row)
	}

	// Blank names are rejected and leave the row unchanged.
	taxExpectError(t, taxCall(h, http.MethodPut, path, map[string]any{"name": "   "}, token), http.StatusBadRequest)
	if got := taxDBCategory(t, h, x.ID); got.Name != "食譜更新" {
		t.Fatalf("name after blank PUT = %q, want 食譜更新", got.Name)
	}

	// Unknown ids are 404.
	taxExpectError(t, taxCall(h, http.MethodPut, "/api/admin/categories/987654321", map[string]any{"name": "不存在"}, token), http.StatusNotFound)
}

func TestTaxonomyCategoryDeleteReparentsChildren(t *testing.T) {
	h := newAccessHarness(t)
	editorToken := h.token(h.user("editor", "tax-delete-editor"))
	admin := h.user("admin", "tax-delete-admin")
	adminToken := h.token(admin)
	owner := h.user("author", "tax-delete-owner")

	root := taxSeedCategory(t, h, "根", "delete-root", nil, 0)
	mid := taxSeedCategory(t, h, "中", "delete-mid", &root.ID, 0)
	leaf := taxSeedCategory(t, h, "葉", "delete-leaf", &mid.ID, 0)
	other := taxSeedCategory(t, h, "其他", "delete-other", nil, 1)
	p1 := taxSeedPost(t, h, owner, "delete-post-one")
	taxAssign(t, h, p1, []core.Category{mid, other}, nil)
	p2 := taxSeedPost(t, h, owner, "delete-post-two")
	taxAssign(t, h, p2, []core.Category{root}, nil)

	// Deleting the middle node moves its child up to the root node.
	taxExpect(t, taxCall(h, http.MethodDelete, fmt.Sprintf("/api/admin/categories/%d", mid.ID), nil, editorToken), nil, http.StatusOK)
	rows := taxListCategories(t, h, editorToken)
	for _, row := range rows {
		if row.ID == mid.ID {
			t.Fatalf("deleted category %d still listed", mid.ID)
		}
	}
	if got := taxFindCategory(t, rows, leaf.ID); got.ParentID == nil || *got.ParentID != root.ID {
		t.Fatalf("leaf parent after deleting mid = %s, want %d", taxParentString(got.ParentID), root.ID)
	}
	if got := taxFindCategory(t, rows, other.ID); got.ParentID != nil || got.PostCount != 1 {
		t.Fatalf("unrelated category after delete = parent %s post_count %d, want nil and 1", taxParentString(got.ParentID), got.PostCount)
	}
	var survivor taxPostRow
	taxExpect(t, taxCall(h, http.MethodGet, fmt.Sprintf("/api/admin/posts/%d", p1.ID), nil, adminToken), &survivor, http.StatusOK)
	if !taxSameSet(taxCategoryIDs(survivor.Categories), other.ID) {
		t.Fatalf("post categories after deleting mid = %v, want only %d", taxCategoryIDs(survivor.Categories), other.ID)
	}

	// Deleting a top-level node leaves its children top-level.
	taxExpect(t, taxCall(h, http.MethodDelete, fmt.Sprintf("/api/admin/categories/%d", root.ID), nil, editorToken), nil, http.StatusOK)
	rows = taxListCategories(t, h, editorToken)
	if got := taxFindCategory(t, rows, leaf.ID); got.ParentID != nil {
		t.Fatalf("leaf parent after deleting root = %s, want nil", taxParentString(got.ParentID))
	}
	var kept taxPostRow
	taxExpect(t, taxCall(h, http.MethodGet, fmt.Sprintf("/api/admin/posts/%d", p2.ID), nil, adminToken), &kept, http.StatusOK)
	if len(kept.Categories) != 0 {
		t.Fatalf("post categories after deleting its only category = %v, want none", taxCategoryIDs(kept.Categories))
	}
	if got := taxFindCategory(t, rows, other.ID); got.PostCount != 1 {
		t.Fatalf("unrelated category post_count after second delete = %d, want 1", got.PostCount)
	}

	taxExpectError(t, taxCall(h, http.MethodDelete, "/api/admin/categories/987654321", nil, editorToken), http.StatusNotFound)
}

func TestTaxonomyCategoryReorderSetsParentAndPosition(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("editor", "tax-order-editor"))
	x := taxSeedCategory(t, h, "排序X", "order-x", nil, 0)
	y := taxSeedCategory(t, h, "排序Y", "order-y", nil, 1)
	z := taxSeedCategory(t, h, "排序Z", "order-z", nil, 2)
	w := taxSeedCategory(t, h, "排序W", "order-w", nil, 3)

	first := []map[string]any{
		{"id": z.ID, "parent_id": nil, "position": 0},
		{"id": x.ID, "parent_id": nil, "position": 1},
		{"id": y.ID, "parent_id": z.ID, "position": 0},
		{"id": w.ID, "parent_id": z.ID, "position": 1},
	}
	var echoed []taxCategoryRow
	taxExpect(t, taxCall(h, http.MethodPost, "/api/admin/categories/reorder", first, token), &echoed, http.StatusOK)
	if len(echoed) != 4 {
		t.Fatalf("reorder response has %d rows, want 4", len(echoed))
	}
	rows := taxListCategories(t, h, token)
	taxCheckLayout(t, rows, []taxLayout{
		{z.ID, nil, 0}, {x.ID, nil, 1}, {y.ID, &z.ID, 0}, {w.ID, &z.ID, 1},
	})
	if taxIndexOf(rows, z.ID) > taxIndexOf(rows, x.ID) {
		t.Fatalf("root Z (position 0) is listed after root X (position 1)")
	}
	if taxIndexOf(rows, y.ID) > taxIndexOf(rows, w.ID) {
		t.Fatalf("sibling Y (position 0) is listed after sibling W (position 1)")
	}

	// Siblings follow position, not insertion order.
	second := []map[string]any{
		{"id": y.ID, "parent_id": z.ID, "position": 1},
		{"id": w.ID, "parent_id": z.ID, "position": 0},
	}
	taxExpect(t, taxCall(h, http.MethodPost, "/api/admin/categories/reorder", second, token), nil, http.StatusOK)
	rows = taxListCategories(t, h, token)
	taxCheckLayout(t, rows, []taxLayout{
		{z.ID, nil, 0}, {x.ID, nil, 1}, {y.ID, &z.ID, 1}, {w.ID, &z.ID, 0},
	})
	if taxIndexOf(rows, w.ID) > taxIndexOf(rows, y.ID) {
		t.Fatalf("sibling W (position 0) is listed after sibling Y (position 1)")
	}
}

func TestTaxonomyTagPrefixSearchAndPostCount(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "tax-tag-admin")
	author := h.user("author", "tax-tag-author")
	adminToken, authorToken := h.token(admin), h.token(author)
	jam := taxSeedTag(t, h, "果醬", "tag-fruit-jam")
	jelly := taxSeedTag(t, h, "果凍", "tag-fruit-jelly")
	taxSeedTag(t, h, "水果", "tag-fruit")
	taxSeedTag(t, h, "Golang", "golang")
	taxSeedTag(t, h, "gopher-tips", "gopher-tips")
	taxSeedTag(t, h, "Rust", "rust")

	// q matches the name prefix only, ignoring case for Latin letters.
	fruit := taxListTags(t, h, authorToken, "果")
	if !taxSameSet(taxTagNames(fruit), "果醬", "果凍") {
		t.Fatalf("q=果 names = %v, want only 果醬 and 果凍 (水果 is not a prefix match)", taxTagNames(fruit))
	}
	for _, q := range []string{"go", "GO", "Go"} {
		if names := taxListTags(t, h, authorToken, q); !taxSameSet(taxTagNames(names), "Golang", "gopher-tips") {
			t.Fatalf("q=%s names = %v, want Golang and gopher-tips", q, taxTagNames(names))
		}
	}
	if all := taxListTags(t, h, authorToken, ""); len(all) != 6 {
		t.Fatalf("unfiltered tag count = %d, want 6", len(all))
	}

	// post_count follows posts that are created with the tag.
	taxExpect(t, taxCall(h, http.MethodPost, "/api/admin/posts", map[string]any{
		"title": "標籤文章一", "slug": "tag-post-one", "content": "一", "status": "published",
		"tag_ids": []uint{jam.ID},
	}, adminToken), nil, http.StatusOK, http.StatusCreated)
	taxExpect(t, taxCall(h, http.MethodPost, "/api/admin/posts", map[string]any{
		"title": "標籤文章二", "slug": "tag-post-two", "content": "二", "status": "published",
		"tag_ids": []uint{jam.ID, jelly.ID},
	}, adminToken), nil, http.StatusOK, http.StatusCreated)

	fruit = taxListTags(t, h, authorToken, "果")
	if len(fruit) != 2 {
		t.Fatalf("q=果 after two posts returned %d tags, want 2", len(fruit))
	}
	if got := taxFindTagByName(t, fruit, "果醬"); got.PostCount != 2 {
		t.Fatalf("果醬 post_count = %d, want 2", got.PostCount)
	}
	if got := taxFindTagByName(t, fruit, "果凍"); got.PostCount != 1 {
		t.Fatalf("果凍 post_count = %d, want 1", got.PostCount)
	}
	if fruit[0].Name != "果醬" {
		t.Fatalf("first tag = %q, want 果醬 (highest post_count first)", fruit[0].Name)
	}
}

func TestTaxonomyTagCreateSlugAndNotFound(t *testing.T) {
	h := newAccessHarness(t)
	editorToken := h.token(h.user("editor", "tax-tagcreate-editor"))
	authorToken := h.token(h.user("author", "tax-tagcreate-author"))

	var first, second taxTagRow
	taxExpect(t, taxCall(h, http.MethodPost, "/api/admin/tags", map[string]any{"name": "Hello Tag"}, editorToken), &first, http.StatusCreated)
	if first.Slug != "hello-tag" {
		t.Fatalf("first tag slug = %q, want hello-tag", first.Slug)
	}
	taxExpect(t, taxCall(h, http.MethodPost, "/api/admin/tags", map[string]any{"name": "Hello Tag"}, editorToken), &second, http.StatusCreated)
	if second.Slug != "hello-tag-2" {
		t.Fatalf("duplicate tag slug = %q, want hello-tag-2", second.Slug)
	}
	taxExpectError(t, taxCall(h, http.MethodPost, "/api/admin/tags", map[string]any{"name": "   "}, authorToken), http.StatusBadRequest)

	path := fmt.Sprintf("/api/admin/tags/%d", first.ID)
	var updated taxTagRow
	taxExpect(t, taxCall(h, http.MethodPut, path, map[string]any{"color": "secondary", "description": "說明"}, editorToken), &updated, http.StatusOK)
	if updated.Color != "secondary" || updated.Description != "說明" || updated.Name != "Hello Tag" {
		t.Fatalf("tag PUT response = %+v, want color, description and name kept", updated)
	}
	if got := taxDBTag(t, h, first.ID); got.Color != "secondary" || got.Description != "說明" {
		t.Fatalf("tag in database after PUT = %+v", got)
	}

	taxExpectError(t, taxCall(h, http.MethodPut, "/api/admin/tags/987654321", map[string]any{"name": "不存在"}, editorToken), http.StatusNotFound)
	taxExpectError(t, taxCall(h, http.MethodDelete, "/api/admin/tags/987654321", nil, editorToken), http.StatusNotFound)
}

func TestTaxonomyPostTermsCreateReadAndList(t *testing.T) {
	h := newAccessHarness(t)
	adminToken := h.token(h.user("admin", "tax-terms-admin"))
	cook := taxSeedCategory(t, h, "食譜", "terms-cook", nil, 0)
	taxSeedCategory(t, h, "旅行", "terms-trip", nil, 1)
	existing := taxSeedTag(t, h, "既有標籤", "terms-existing")

	var created taxPostRow
	taxExpect(t, taxCall(h, http.MethodPost, "/api/admin/posts", map[string]any{
		"title": "第一篇", "slug": "terms-first", "content": "內容", "status": "published",
		"category_ids": []uint{cook.ID},
		"tag_ids":      []uint{existing.ID},
		"tags":         []string{"新標籤"},
	}, adminToken), &created, http.StatusOK, http.StatusCreated)
	if !taxSameSet(taxCategoryIDs(created.Categories), cook.ID) {
		t.Fatalf("created post categories = %v, want [%d]", taxCategoryIDs(created.Categories), cook.ID)
	}
	if !taxSameSet(taxTagNames(created.Tags), "既有標籤", "新標籤") {
		t.Fatalf("created post tags = %v, want 既有標籤 and 新標籤", taxTagNames(created.Tags))
	}
	var newTags int64
	if err := h.db.Model(&core.Tag{}).Where("name = ?", "新標籤").Count(&newTags).Error; err != nil {
		t.Fatalf("count new tag: %v", err)
	}
	if newTags != 1 {
		t.Fatalf("tags named 新標籤 in database = %d, want 1", newTags)
	}

	// Single-post read and the list both preload categories and tags.
	var got taxPostRow
	taxExpect(t, taxCall(h, http.MethodGet, fmt.Sprintf("/api/admin/posts/%d", created.ID), nil, adminToken), &got, http.StatusOK)
	if !taxSameSet(taxCategoryIDs(got.Categories), cook.ID) || !taxSameSet(taxTagNames(got.Tags), "既有標籤", "新標籤") {
		t.Fatalf("GET post preload categories=%v tags=%v", taxCategoryIDs(got.Categories), taxTagNames(got.Tags))
	}
	var list []taxPostRow
	taxExpect(t, taxCall(h, http.MethodGet, "/api/admin/posts", nil, adminToken), &list, http.StatusOK)
	found := false
	for _, post := range list {
		if post.ID != created.ID {
			continue
		}
		found = true
		if !taxSameSet(taxCategoryIDs(post.Categories), cook.ID) || !taxSameSet(taxTagNames(post.Tags), "既有標籤", "新標籤") {
			t.Fatalf("list post preload categories=%v tags=%v", taxCategoryIDs(post.Categories), taxTagNames(post.Tags))
		}
	}
	if !found {
		t.Fatalf("created post %d missing from list", created.ID)
	}

	// An existing tag name is reused rather than duplicated.
	var second taxPostRow
	taxExpect(t, taxCall(h, http.MethodPost, "/api/admin/posts", map[string]any{
		"title": "第二篇", "slug": "terms-second", "content": "內容", "status": "published",
		"tags": []string{"既有標籤"},
	}, adminToken), &second, http.StatusOK, http.StatusCreated)
	if !taxSameSet(taxTagIDs(second.Tags), existing.ID) {
		t.Fatalf("second post tag IDs = %v, want [%d]", taxTagIDs(second.Tags), existing.ID)
	}
	var existingCount int64
	if err := h.db.Model(&core.Tag{}).Where("name = ?", "既有標籤").Count(&existingCount).Error; err != nil {
		t.Fatalf("count existing tag: %v", err)
	}
	if existingCount != 1 {
		t.Fatalf("tags named 既有標籤 = %d, want 1", existingCount)
	}
}

func TestTaxonomyPostTermsPartialUpdateAndAuthorOwnership(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "tax-own-admin")
	author := h.user("author", "tax-own-author")
	other := h.user("author", "tax-own-other")
	adminToken, authorToken := h.token(admin), h.token(author)
	cook := taxSeedCategory(t, h, "食譜", "own-cook", nil, 0)
	trip := taxSeedCategory(t, h, "旅行", "own-trip", nil, 1)
	keep := taxSeedTag(t, h, "保留標籤", "own-keep")

	post := taxSeedPost(t, h, admin, "own-partial")
	taxAssign(t, h, post, []core.Category{cook}, []core.Tag{keep})
	postPath := fmt.Sprintf("/api/admin/posts/%d", post.ID)

	// A PUT that carries only category_ids clears categories and leaves tags alone.
	var updated taxPostRow
	taxExpect(t, taxCall(h, http.MethodPut, postPath, map[string]any{"category_ids": []uint{}}, adminToken), nil, http.StatusOK)
	updated = taxGetPost(t, h, adminToken, post.ID)
	if len(updated.Categories) != 0 {
		t.Fatalf("categories after category_ids:[] = %v, want none", taxCategoryIDs(updated.Categories))
	}
	if !taxSameSet(taxTagIDs(updated.Tags), keep.ID) {
		t.Fatalf("tags after category-only PUT = %v, want [%d]", taxTagIDs(updated.Tags), keep.ID)
	}

	// Setting categories without tags, and changing only the title, keep the other term set.
	taxExpect(t, taxCall(h, http.MethodPut, postPath, map[string]any{"category_ids": []uint{trip.ID}}, adminToken), nil, http.StatusOK)
	updated = taxGetPost(t, h, adminToken, post.ID)
	if !taxSameSet(taxCategoryIDs(updated.Categories), trip.ID) || !taxSameSet(taxTagIDs(updated.Tags), keep.ID) {
		t.Fatalf("after category_ids:[trip] categories=%v tags=%v", taxCategoryIDs(updated.Categories), taxTagIDs(updated.Tags))
	}
	taxExpect(t, taxCall(h, http.MethodPut, postPath, map[string]any{"title": "改標題"}, adminToken), nil, http.StatusOK)
	updated = taxGetPost(t, h, adminToken, post.ID)
	if updated.Title != "改標題" || !taxSameSet(taxCategoryIDs(updated.Categories), trip.ID) || !taxSameSet(taxTagIDs(updated.Tags), keep.ID) {
		t.Fatalf("after title-only PUT title=%q categories=%v tags=%v", updated.Title, taxCategoryIDs(updated.Categories), taxTagIDs(updated.Tags))
	}

	// Authors can assign terms to their own posts, including new tags.
	var own taxPostRow
	taxExpect(t, taxCall(h, http.MethodPost, "/api/admin/posts", map[string]any{
		"title": "作者文章", "slug": "author-created", "content": "內容", "status": "draft",
		"category_ids": []uint{cook.ID},
		"tags":         []string{"作者標籤"},
	}, authorToken), &own, http.StatusOK, http.StatusCreated)
	if !taxSameSet(taxCategoryIDs(own.Categories), cook.ID) || !taxSameSet(taxTagNames(own.Tags), "作者標籤") {
		t.Fatalf("author post categories=%v tags=%v", taxCategoryIDs(own.Categories), taxTagNames(own.Tags))
	}

	// Authors cannot assign terms to someone else's post: 404, and nothing changes,
	// including no tag created from the rejected request.
	foreign := taxSeedPost(t, h, other, "own-foreign")
	taxExpectError(t, taxCall(h, http.MethodPut, fmt.Sprintf("/api/admin/posts/%d", foreign.ID), map[string]any{
		"category_ids": []uint{trip.ID},
		"tags":         []string{"偷偷標籤"},
	}, authorToken), http.StatusNotFound)
	if n := h.db.Model(&foreign).Association("Categories").Count(); n != 0 {
		t.Fatalf("foreign post categories after rejected author PUT = %d, want 0", n)
	}
	var stray int64
	if err := h.db.Model(&core.Tag{}).Where("name = ?", "偷偷標籤").Count(&stray).Error; err != nil {
		t.Fatalf("count stray tag: %v", err)
	}
	if stray != 0 {
		t.Fatalf("rejected author PUT created %d tag(s) named 偷偷標籤, want 0", stray)
	}
}

func TestTaxonomyPostNewFieldsRoundTrip(t *testing.T) {
	h := newAccessHarness(t)
	adminToken := h.token(h.user("admin", "tax-fields-admin"))

	var created taxPostRow
	taxExpect(t, taxCall(h, http.MethodPost, "/api/admin/posts", map[string]any{
		"title": "欄位文章", "slug": "fields-post", "content": "內容", "status": "published",
		"excerpt": "摘要文字", "cover": "/uploads/cover.png", "allow_comments": false,
		"pinned": true, "visibility": "private",
	}, adminToken), &created, http.StatusOK, http.StatusCreated)
	checkFields := func(stage string, got taxPostRow, excerpt, cover string, allow bool, pinned bool, visibility string) {
		t.Helper()
		if got.Excerpt != excerpt || got.Cover != cover || got.Pinned != pinned || got.Visibility != visibility {
			t.Fatalf("%s: excerpt=%q cover=%q pinned=%v visibility=%q, want %q %q %v %q", stage, got.Excerpt, got.Cover, got.Pinned, got.Visibility, excerpt, cover, pinned, visibility)
		}
		if got.AllowComments == nil || *got.AllowComments != allow {
			t.Fatalf("%s: allow_comments = %v, want %v", stage, got.AllowComments, allow)
		}
	}
	checkFields("POST response", created, "摘要文字", "/uploads/cover.png", false, true, "private")

	var got taxPostRow
	postPath := fmt.Sprintf("/api/admin/posts/%d", created.ID)
	taxExpect(t, taxCall(h, http.MethodGet, postPath, nil, adminToken), &got, http.StatusOK)
	checkFields("GET after POST", got, "摘要文字", "/uploads/cover.png", false, true, "private")

	taxExpect(t, taxCall(h, http.MethodPut, postPath, map[string]any{
		"excerpt": "新摘要", "cover": "/uploads/next.png", "allow_comments": true, "pinned": false, "visibility": "public",
	}, adminToken), nil, http.StatusOK)
	taxExpect(t, taxCall(h, http.MethodGet, postPath, nil, adminToken), &got, http.StatusOK)
	checkFields("GET after PUT", got, "新摘要", "/uploads/next.png", true, false, "public")

	// visibility accepts only public and private.
	taxExpectError(t, taxCall(h, http.MethodPost, "/api/admin/posts", map[string]any{
		"title": "壞可見性", "slug": "bad-visibility", "content": "內容", "visibility": "draft",
	}, adminToken), http.StatusBadRequest)
	taxExpectError(t, taxCall(h, http.MethodPut, postPath, map[string]any{"visibility": "members"}, adminToken), http.StatusBadRequest)
	taxExpect(t, taxCall(h, http.MethodGet, postPath, nil, adminToken), &got, http.StatusOK)
	if got.Visibility != "public" {
		t.Fatalf("visibility after rejected PUT = %q, want public", got.Visibility)
	}
	var stray int64
	if err := h.db.Model(&core.Post{}).Where("slug = ?", "bad-visibility").Count(&stray).Error; err != nil {
		t.Fatalf("count rejected post: %v", err)
	}
	if stray != 0 {
		t.Fatalf("rejected visibility POST created %d post(s), want 0", stray)
	}

	// Omitted visibility defaults to public.
	var defaulted taxPostRow
	taxExpect(t, taxCall(h, http.MethodPost, "/api/admin/posts", map[string]any{
		"title": "預設", "slug": "default-visibility", "content": "內容",
	}, adminToken), &defaulted, http.StatusOK, http.StatusCreated)
	if defaulted.Visibility != "public" {
		t.Fatalf("default visibility = %q, want public", defaulted.Visibility)
	}
}

func TestTaxonomyDeleteTagRemovesItFromPosts(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "tax-deltag-admin")
	adminToken := h.token(admin)
	editorToken := h.token(h.user("editor", "tax-deltag-editor"))
	doomed := taxSeedTag(t, h, "刪除標籤", "tag-doomed")
	kept := taxSeedTag(t, h, "保留標籤", "tag-kept")
	post := taxSeedPost(t, h, admin, "deltag-post")
	taxAssign(t, h, post, nil, []core.Tag{doomed, kept})

	path := fmt.Sprintf("/api/admin/tags/%d", doomed.ID)
	taxExpect(t, taxCall(h, http.MethodDelete, path, nil, editorToken), nil, http.StatusOK)

	var got taxPostRow
	taxExpect(t, taxCall(h, http.MethodGet, fmt.Sprintf("/api/admin/posts/%d", post.ID), nil, adminToken), &got, http.StatusOK)
	if !taxSameSet(taxTagIDs(got.Tags), kept.ID) {
		t.Fatalf("post tags after deleting %d = %v, want [%d]", doomed.ID, taxTagIDs(got.Tags), kept.ID)
	}
	var joins int64
	if err := h.db.Table("post_tags").Where("tag_id = ?", doomed.ID).Count(&joins).Error; err != nil {
		t.Fatalf("count post_tags rows: %v", err)
	}
	if joins != 0 {
		t.Fatalf("post_tags rows for deleted tag = %d, want 0", joins)
	}
	taxExpectError(t, taxCall(h, http.MethodDelete, path, nil, editorToken), http.StatusNotFound)
}
