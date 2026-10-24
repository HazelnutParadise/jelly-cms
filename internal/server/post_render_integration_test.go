package server

// Ticket 09 G0 post-save contract (docs/tickets/09-editor.md, 流程與驗收 and 儲存). The server renders
// posts from source through internal/markup, ignores client content when source is present, sanitizes
// content-only writes by the caller's role, and backfills source for legacy rows. Requests go through
// the real route table on the dedicated PostgreSQL database; stored state is read with raw SQL so the
// assertions do not depend on Go struct fields that a later slice may rename. Run with
// JELLY_TEST_POSTGRES=1; otherwise the harness skips. Written before the handlers change and expected
// to fail until G0 lands. Do not edit them to match an implementation.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/labstack/echo/v4"
)

// edtPostRow is the stored state of one post that the G0 contract cares about.
type edtPostRow struct {
	Content     string
	Source      string
	Format      string
	RenderTrust string
	UpdatedAt   time.Time
}

func edtBody(t *testing.T, fields map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode request body: %v", err)
	}
	return string(raw)
}

func edtDecode(t *testing.T, body string) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode JSON object: %v body=%q", err, body)
	}
	return got
}

// edtCreate posts a new post and returns its id and the decoded response.
func edtCreate(t *testing.T, h *accessHarness, token string, fields map[string]any) (uint, map[string]any) {
	t.Helper()
	r := h.request(http.MethodPost, "/api/admin/posts", edtBody(t, fields), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(r, "create post")
	if r.code != http.StatusOK {
		t.Fatalf("POST /api/admin/posts returned %d body=%q", r.code, r.body)
	}
	got := edtDecode(t, r.body)
	id, _ := got["id"].(float64)
	if id == 0 {
		t.Fatalf("create post returned no id: %q", r.body)
	}
	return uint(id), got
}

// edtUpdate sends a PUT and returns the status and the raw body without failing.
func edtUpdate(h *accessHarness, token string, id uint, fields map[string]any) accessResult {
	raw, _ := json.Marshal(fields)
	return h.request(http.MethodPut, "/api/admin/posts/"+edtID(id), string(raw), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
}

func edtMustUpdate(t *testing.T, h *accessHarness, token string, id uint, fields map[string]any) map[string]any {
	t.Helper()
	r := edtUpdate(h, token, id, fields)
	h.mustNoPanic(r, "update post")
	if r.code != http.StatusOK {
		t.Fatalf("PUT /api/admin/posts/%d returned %d body=%q", id, r.code, r.body)
	}
	return edtDecode(t, r.body)
}

func edtID(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}

func edtLoadRow(t *testing.T, h *accessHarness, id uint) edtPostRow {
	t.Helper()
	var row edtPostRow
	err := h.db.Raw(`SELECT COALESCE(content, '') AS content, COALESCE(source, '') AS source,
		COALESCE(format, '') AS format, COALESCE(render_trust, '') AS render_trust, updated_at
		FROM posts WHERE id = ?`, id).Scan(&row).Error
	if err != nil {
		t.Fatalf("load post %d: %v", id, err)
	}
	return row
}

// edtPublicProse returns the prose wrapper of a rendered post page, from class="prose" up to the tag
// block that follows it in the theme. The slice may still include the theme's own script; the script
// count check in the test covers injection.
func edtPublicProse(body string) string {
	start := strings.Index(body, `class="prose"`)
	if start < 0 {
		return ""
	}
	end := strings.Index(body[start:], `class="article-tags"`)
	if end < 0 {
		end = len(body) - start
	}
	return body[start : start+end]
}

func TestPostSaveRendersSourceAndIgnoresClientContent(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "edt-save-author")
	token := h.token(author)

	source := "## 標題\n\n:::columns\n::: col\n左\n:::\n::: col\n右\n:::\n:::\n\n$$x^2$$\n"
	id, created := edtCreate(t, h, token, map[string]any{
		"title":   "保存測試",
		"status":  "published",
		"source":  source,
		"content": "<p>bogus-client</p>",
	})

	if _, ok := created["diagnostics"]; !ok {
		t.Errorf("create response must carry a diagnostics key: %v", created)
	}
	if got, _ := created["source"].(string); got != source {
		t.Errorf("response source = %q, want the submitted source", got)
	}
	content, _ := created["content"].(string)
	if !strings.Contains(content, "jc-columns") {
		t.Errorf("stored content must be the render of source (jc-columns missing): %q", content)
	}
	if strings.Contains(content, "bogus-client") {
		t.Errorf("client content must be ignored when source is present: %q", content)
	}

	row := edtLoadRow(t, h, id)
	if row.Content != content {
		t.Errorf("stored content differs from the response content")
	}
	if row.Source != source {
		t.Errorf("stored source = %q, want the submitted source", row.Source)
	}
	if row.Format != "jmd" {
		t.Errorf("stored format = %q, want jmd for a source post", row.Format)
	}
}

func TestPostLegacyContentIsSanitized(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "edt-legacy-author")
	token := h.token(author)

	id, _ := edtCreate(t, h, token, map[string]any{"title": "舊式寫入", "content": "<p>ok</p>"})
	edtMustUpdate(t, h, token, id, map[string]any{"content": "<p>x</p><script>alert(1)</script>"})

	row := edtLoadRow(t, h, id)
	if strings.Contains(strings.ToLower(row.Content), "<script") {
		t.Errorf("author content-only write stored a script: %q", row.Content)
	}
	if strings.Contains(row.Content, "alert(1)") {
		t.Errorf("author content-only write kept the script body: %q", row.Content)
	}
}

func TestPostAuthorHtmlFenceDroppedEditorKept(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "edt-fence-author")
	editor := h.user("editor", "edt-fence-editor")
	authorToken := h.token(author)
	editorToken := h.token(editor)

	id, _ := edtCreate(t, h, authorToken, map[string]any{"title": "作者文章", "source": "正文\n"})

	// The editor may use the rich policy, so the html fence renders as an allow-listed block.
	editorSource := "正文\n\n```html\n<p>HTMLMARK</p>\n```\n"
	edtMustUpdate(t, h, editorToken, id, map[string]any{"source": editorSource})
	kept := edtLoadRow(t, h, id)
	if !strings.Contains(kept.Content, "jc-html") || !strings.Contains(kept.Content, "<p>HTMLMARK</p>") {
		t.Errorf("editor save must keep the rich html block in content: %q", kept.Content)
	}

	// The author saves the same source with a new title. The rich block must leave content, and the
	// source must not change.
	edtMustUpdate(t, h, authorToken, id, map[string]any{"title": "改標題", "source": editorSource})
	after := edtLoadRow(t, h, id)
	if strings.Contains(after.Content, "jc-html") {
		t.Errorf("author save must not keep the rich html wrapper: %q", after.Content)
	}
	if strings.Contains(after.Content, "<p>HTMLMARK</p>") {
		t.Errorf("author save must not keep raw html from the editor block: %q", after.Content)
	}
	if after.Source != editorSource {
		t.Errorf("author save changed the source: got %q want %q", after.Source, editorSource)
	}
}

func TestPostRenderTrustStoredFromRole(t *testing.T) {
	h := newAccessHarness(t)
	editor := h.user("editor", "edt-trust-editor")
	author := h.user("author", "edt-trust-author")
	editorToken := h.token(editor)
	authorToken := h.token(author)

	editorPost, _ := edtCreate(t, h, editorToken, map[string]any{"title": "編輯者文章", "source": "x"})
	authorPost, _ := edtCreate(t, h, authorToken, map[string]any{"title": "作者文章", "source": "x"})
	edtMustUpdate(t, h, authorToken, authorPost, map[string]any{"title": "作者文章二"})

	if got := edtLoadRow(t, h, editorPost).RenderTrust; got != "rich" {
		t.Errorf("editor post render_trust = %q, want rich", got)
	}
	if got := edtLoadRow(t, h, authorPost).RenderTrust; got != "strict" {
		t.Errorf("author post render_trust = %q, want strict", got)
	}
}

func TestPostMigrationBackfillsSourceFromMetaMarkdown(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "edt-migrate-admin")

	legacyMarkdown := "# 舊文\n\n內容"
	legacy := core.Post{
		Title:    "舊文",
		Slug:     "edt-legacy-md",
		Content:  "<h1>舊文</h1>",
		Type:     "post",
		Status:   "published",
		AuthorID: admin.ID,
		Meta:     json.RawMessage(`{"markdown":"# 舊文\n\n內容"}`),
	}
	if err := h.db.Create(&legacy).Error; err != nil {
		t.Fatalf("seed legacy post: %v", err)
	}
	htmlOnly := core.Post{
		Title:    "舊 HTML",
		Slug:     "edt-legacy-html",
		Content:  "<p>只有 HTML</p>",
		Type:     "post",
		Status:   "published",
		AuthorID: admin.ID,
	}
	if err := h.db.Create(&htmlOnly).Error; err != nil {
		t.Fatalf("seed html-only post: %v", err)
	}
	// Rows that predate migration 0015 have NULL source and format.
	if err := h.db.Exec("UPDATE posts SET source = NULL, format = NULL WHERE id IN ?", []uint{legacy.ID, htmlOnly.ID}).Error; err != nil {
		t.Fatalf("reset rows to pre-migration state: %v", err)
	}
	var nulls int64
	if err := h.db.Raw("SELECT COUNT(*) FROM posts WHERE id IN ? AND source IS NULL AND format IS NULL", []uint{legacy.ID, htmlOnly.ID}).Scan(&nulls).Error; err != nil {
		t.Fatalf("check pre-migration state: %v", err)
	}
	if nulls != 2 {
		t.Fatalf("fixture has %d rows with NULL source and format, want 2", nulls)
	}

	if err := data.MigrateDB(h.db); err != nil {
		t.Fatalf("second MigrateDB run: %v", err)
	}

	md := edtLoadRow(t, h, legacy.ID)
	if md.Source != legacyMarkdown {
		t.Errorf("backfilled source = %q, want %q", md.Source, legacyMarkdown)
	}
	if md.Format != "md" {
		t.Errorf("backfilled format = %q, want md", md.Format)
	}
	plain := edtLoadRow(t, h, htmlOnly.ID)
	if plain.Format != "html" {
		t.Errorf("HTML-only row format = %q, want html", plain.Format)
	}
}

func TestPublicPostPageRendersJellyBlocks(t *testing.T) {
	h := archivesHarness(t)
	editor := h.user("editor", "edt-public-editor")
	token := h.token(editor)

	// The payload stays inside the source; the page must not gain any script from it.
	jellySource := ":::columns\n::: col\n左欄內容\n:::\n::: col\n右欄內容\n:::\n:::\n\n" +
		"<script>alertX()</script>\n\n$$x^2$$\n"
	_, _ = edtCreate(t, h, token, map[string]any{
		"title": "Jelly 區塊", "slug": "edt-jelly-blocks", "status": "published", "source": jellySource,
	})
	_, _ = edtCreate(t, h, token, map[string]any{
		"title": "純文字", "slug": "edt-plain-block", "status": "published", "source": "plain",
	})

	page := archivesPage(t, h, "/post/edt-jelly-blocks")
	baseline := archivesPage(t, h, "/post/edt-plain-block")

	prose := edtPublicProse(page)
	if !strings.Contains(prose, "jc-columns") {
		t.Errorf("public post must render :::columns as jc-columns: %q", prose)
	}
	if !strings.Contains(prose, "jc-math") {
		t.Errorf("public post must render $$…$$ as jc-math: %q", prose)
	}
	if got, want := strings.Count(page, "<script"), strings.Count(baseline, "<script"); got != want {
		t.Errorf("public post has %d <script> tags, the theme baseline has %d: content injected a script", got, want)
	}
	archivesWantAbsent(t, "/post/edt-jelly-blocks", page, "alertX()")
}

func TestRevisionsDiffUsesSourceWhenPresent(t *testing.T) {
	h := newAccessHarness(t)
	editor := h.user("editor", "edt-diff-editor")
	token := h.token(editor)

	id, _ := edtCreate(t, h, token, map[string]any{"title": "差異", "source": "第一行\n第二行\n"})
	// The title changes too, so the second save records a revision whatever the source rule is.
	edtMustUpdate(t, h, token, id, map[string]any{"title": "差異二", "source": "第一行\n第三行\n"})

	list := h.request(http.MethodGet, "/api/admin/posts/"+edtID(id)+"/revisions", "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(list, "list revisions")
	var revs []struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal([]byte(list.body), &revs); err != nil || len(revs) < 2 {
		t.Fatalf("revision list = %q (err %v), want at least two revisions", list.body, err)
	}
	oldest := revs[len(revs)-1].ID

	r := h.request(http.MethodGet, "/api/admin/posts/"+edtID(id)+"/revisions/diff?from="+edtID(oldest)+"&to=current", "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(r, "diff revisions")
	if r.code != http.StatusOK {
		t.Fatalf("diff returned %d body=%q", r.code, r.body)
	}
	got := edtDecode(t, r.body)
	if got["basis"] != "source" {
		t.Errorf("diff basis = %v, want source", got["basis"])
	}
	if !strings.Contains(r.body, "第三行") {
		t.Errorf("source diff must show the new source line: %s", r.body)
	}
}
