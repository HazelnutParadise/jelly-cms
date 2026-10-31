package server

// Comments integration tests for ticket 04 slot C3 (contract in the header of
// handlers_comments.go). They are written against the contract before the
// implementation exists and are expected to fail until the public comment API,
// the moderation API and commentsForPost are implemented. Do not edit them to
// match an implementation.
//
// Where the contract leaves room, these tests assume:
//   - admin counts.all is the number of non-trashed rows (pending + approved + spam);
//     trashed rows are counted only under counts.trash.
//   - public GET count is the number of approved, visible comments, replies included.
//   - admin page numbers start at 1.
//   - an existing post that is draft, private or has allow_comments=false answers 403;
//     a missing post answers 404.
//   - bulk "delete" on a row that is not in trash answers 409, or 200 with updated 0;
//     either way the row must stay in the database.
//   - restore returns a trashed row to its pre-trash status; the test covers a pending row only.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

var (
	commentsTestIPCounter atomic.Int64
	commentsBase          = time.Date(2026, 1, 10, 9, 0, 0, 0, time.UTC)

	commentsScriptTag = regexp.MustCompile(`(?i)<script`)
	commentsImgTag    = regexp.MustCompile(`(?i)<img\b`)
	commentsEventAttr = regexp.MustCompile(`(?i)<[^>]*\bon[a-z]+\s*=`)
	commentsAnchorTag = regexp.MustCompile(`(?i)<a\s[^>]*>`)
	commentsNofollow  = regexp.MustCompile(`(?i)\brel="[^"]*\bnofollow\b[^"]*"`)
)

type commentsPublicRow struct {
	ID       uint                `json:"id"`
	ParentID *uint               `json:"parent_id"`
	Name     string              `json:"name"`
	BodyHTML string              `json:"body_html"`
	Children []commentsPublicRow `json:"children"`
}

type commentsPublicList struct {
	Count int                 `json:"count"`
	Items []commentsPublicRow `json:"items"`
}

type commentsAdminRow struct {
	ID       uint   `json:"id"`
	PostID   uint   `json:"post_id"`
	ParentID *uint  `json:"parent_id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Post     struct {
		ID    uint   `json:"id"`
		Title string `json:"title"`
		Slug  string `json:"slug"`
	} `json:"post"`
}

type commentsAdminList struct {
	Items   []commentsAdminRow `json:"items"`
	Total   int64              `json:"total"`
	Page    int                `json:"page"`
	PerPage int                `json:"per_page"`
	Counts  map[string]int64   `json:"counts"`
}

// commentsNextIP gives each request its own address so the per-IP limit of
// 5 posts per minute cannot leak between requests of unrelated checks.
func commentsNextIP() string {
	n := commentsTestIPCounter.Add(1)
	return fmt.Sprintf("10.%d.%d.%d", (n>>16)&255, (n>>8)&255, n&255)
}

func commentsAt(minute int) time.Time {
	return commentsBase.Add(time.Duration(minute) * time.Minute)
}

func commentsUint(v uint) *uint { return &v }

func commentsBoolPtr(v bool) *bool { return &v }

// commentsDo sends a JSON request through the harness router. The client
// address is set both as RemoteAddr and as X-Forwarded-For so the limiter
// sees the same IP whichever source it reads.
func commentsDo(t *testing.T, h *accessHarness, method, path, body, token, ip string) accessResult {
	t.Helper()
	if ip == "" {
		ip = commentsNextIP()
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = ip + ":40000"
	req.Header.Set("X-Forwarded-For", ip)
	req.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if token != "" {
		setAccessToken(req, token)
	}
	rec := httptest.NewRecorder()
	var panicked any
	func() {
		defer func() { panicked = recover() }()
		h.echo.ServeHTTP(rec, req)
	}()
	return accessResult{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone(), panicked: panicked}
}

func commentsJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	return string(raw)
}

func commentsOK(t *testing.T, r accessResult, where string, want int) bool {
	t.Helper()
	if r.panicked != nil {
		t.Errorf("%s panicked: %v", where, r.panicked)
		return false
	}
	if r.code != want {
		t.Errorf("%s returned %d, want %d; body=%q", where, r.code, want, r.body)
		return false
	}
	return true
}

func commentsDecode(t *testing.T, r accessResult, where string, out any) bool {
	t.Helper()
	if err := json.Unmarshal([]byte(r.body), out); err != nil {
		t.Errorf("%s: decode response: %v; body=%q", where, err, r.body)
		return false
	}
	return true
}

func commentsGuestBody(t *testing.T, postID uint, overrides map[string]any) string {
	t.Helper()
	m := map[string]any{
		"post_id": postID,
		"name":    "Guest",
		"email":   "guest@example.test",
		"body":    "A plain comment",
		"website": "",
	}
	for k, v := range overrides {
		m[k] = v
	}
	return commentsJSON(t, m)
}

func commentsPublishedPost(t *testing.T, h *accessHarness, author core.User, slug string, mutate func(*core.Post)) core.Post {
	t.Helper()
	published := commentsAt(0)
	p := core.Post{
		Title: "Post " + slug, Slug: slug, Content: "content " + slug, Type: "post",
		Status: "published", Visibility: "public", AuthorID: author.ID, PublishedAt: &published,
	}
	if mutate != nil {
		mutate(&p)
	}
	if err := h.db.Create(&p).Error; err != nil {
		t.Fatalf("seed post %s: %v", slug, err)
	}
	return p
}

func commentsRow(postID uint, name, status string, minute int) core.Comment {
	return core.Comment{
		PostID: postID, Name: name, Email: strings.ToLower(name) + "@example.test",
		Body: name + " body", BodyHTML: "<p>" + name + " body</p>", Status: status,
		CreatedAt: commentsAt(minute), UpdatedAt: commentsAt(minute),
	}
}

func commentsReply(c core.Comment, parent uint) core.Comment {
	c.ParentID = commentsUint(parent)
	return c
}

func commentsTrashed(c core.Comment) core.Comment {
	c.DeletedAt = gorm.DeletedAt{Time: commentsAt(0), Valid: true}
	return c
}

func commentsSeed(t *testing.T, h *accessHarness, c core.Comment) core.Comment {
	t.Helper()
	if err := h.db.Create(&c).Error; err != nil {
		t.Fatalf("seed comment %q: %v", c.Name, err)
	}
	return c
}

func commentsSetOption(t *testing.T, h *accessHarness, key, value string) {
	t.Helper()
	if err := h.db.Create(&core.Option{Key: key, Value: value}).Error; err != nil {
		t.Fatalf("seed option %s: %v", key, err)
	}
}

// commentsLoad reads a comment through the normal (soft-delete aware) scope.
func commentsLoad(t *testing.T, db *gorm.DB, id uint) (core.Comment, bool) {
	t.Helper()
	var c core.Comment
	if err := db.First(&c, id).Error; err != nil {
		t.Errorf("load comment %d: %v", id, err)
		return c, false
	}
	return c, true
}

// commentsLoadAny reads a comment including soft-deleted rows.
func commentsLoadAny(db *gorm.DB, id uint) (core.Comment, error) {
	var c core.Comment
	err := db.Unscoped().First(&c, id).Error
	return c, err
}

func commentsCountRows(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&core.Comment{}).Count(&n).Error; err != nil {
		t.Fatalf("count comments: %v", err)
	}
	return n
}

// commentsSnapshot renders every comment row, soft-deleted ones included, so
// that a rejected request can be shown to have left the table untouched.
func commentsSnapshot(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var rows []core.Comment
	if err := db.Unscoped().Order("id").Find(&rows).Error; err != nil {
		t.Fatalf("snapshot comments: %v", err)
	}
	var b strings.Builder
	for _, c := range rows {
		parent := "-"
		if c.ParentID != nil {
			parent = fmt.Sprint(*c.ParentID)
		}
		fmt.Fprintf(&b, "%d|post=%d|parent=%s|name=%s|email=%s|body=%s|status=%s|deleted=%v\n",
			c.ID, c.PostID, parent, c.Name, c.Email, c.Body, c.Status, c.DeletedAt.Valid)
	}
	return b.String()
}

func commentsPublicGet(t *testing.T, h *accessHarness, postID uint) (commentsPublicList, bool) {
	t.Helper()
	path := fmt.Sprintf("/api/comments?post_id=%d", postID)
	r := commentsDo(t, h, http.MethodGet, path, "", "", "")
	if !commentsOK(t, r, "GET "+path, http.StatusOK) {
		return commentsPublicList{}, false
	}
	var out commentsPublicList
	return out, commentsDecode(t, r, "GET "+path, &out)
}

func commentsAdminGet(t *testing.T, h *accessHarness, token string, params map[string]string) (commentsAdminList, bool) {
	t.Helper()
	path := "/api/admin/comments"
	if len(params) > 0 {
		v := url.Values{}
		for k, val := range params {
			v.Set(k, val)
		}
		path += "?" + v.Encode()
	}
	r := commentsDo(t, h, http.MethodGet, path, "", token, "")
	if !commentsOK(t, r, "GET "+path, http.StatusOK) {
		return commentsAdminList{}, false
	}
	var out commentsAdminList
	return out, commentsDecode(t, r, "GET "+path, &out)
}

func commentsAdminIDs(rows []commentsAdminRow) []uint {
	ids := make([]uint, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids
}

func commentsPublicIDs(items []commentsPublicRow) []uint {
	var ids []uint
	for _, item := range items {
		ids = append(ids, item.ID)
		ids = append(ids, commentsPublicIDs(item.Children)...)
	}
	return ids
}

func commentsSameIDs(t *testing.T, label string, got []uint, want ...uint) {
	t.Helper()
	g := append([]uint(nil), got...)
	w := append([]uint(nil), want...)
	sort.Slice(g, func(i, j int) bool { return g[i] < g[j] })
	sort.Slice(w, func(i, j int) bool { return w[i] < w[j] })
	if fmt.Sprint(g) != fmt.Sprint(w) {
		t.Errorf("%s ids = %v, want %v", label, g, w)
	}
}

func commentsCheckCounts(t *testing.T, label string, got map[string]int64, all, pending, approved, spam, trash int64) {
	t.Helper()
	want := map[string]int64{"all": all, "pending": pending, "approved": approved, "spam": spam, "trash": trash}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s counts[%s] = %d, want %d (full counts %v)", label, k, got[k], v, got)
		}
	}
}

func commentsStatusOf(t *testing.T, r accessResult, where string) (string, bool) {
	t.Helper()
	var out struct {
		Status string `json:"status"`
	}
	if !commentsDecode(t, r, where, &out) {
		return "", false
	}
	return out.Status, true
}

func commentsDeletedCount(t *testing.T, db *gorm.DB, id uint) int64 {
	t.Helper()
	var n int64
	if err := db.Unscoped().Model(&core.Comment{}).Where("id = ?", id).Count(&n).Error; err != nil {
		t.Fatalf("count unscoped comment %d: %v", id, err)
	}
	return n
}

func commentsHasNofollowLink(html, target string) bool {
	found := false
	for _, tag := range commentsAnchorTag.FindAllString(html, -1) {
		if !strings.Contains(tag, target) {
			continue
		}
		found = true
		if !commentsNofollow.MatchString(tag) {
			return false
		}
	}
	return found
}

// --- 1. public create: storage, sanitizing, approval default ---------------

func TestCommentsPublicCreateSanitizesAndStoresPending(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "cmt-open-author")
	post := commentsPublishedPost(t, h, author, "open-post", nil)

	input := "Hi <script>alert(1)</script> **粗** [x](https://e.com) <img src=x onerror=alert(1)>"
	r := commentsDo(t, h, http.MethodPost, "/api/comments", commentsGuestBody(t, post.ID, map[string]any{"body": input}), "", "")
	if !commentsOK(t, r, "guest POST /api/comments", http.StatusCreated) {
		return
	}
	var created struct {
		ID     uint   `json:"id"`
		Status string `json:"status"`
	}
	if !commentsDecode(t, r, "guest POST /api/comments", &created) {
		return
	}
	if created.ID == 0 {
		t.Fatalf("created comment has no id; body=%q", r.body)
	}
	if created.Status != "pending" {
		t.Errorf("guest comment status in response = %q, want pending", created.Status)
	}

	row, ok := commentsLoad(t, h.db, created.ID)
	if !ok {
		return
	}
	if row.Status != "pending" {
		t.Errorf("stored status = %q, want pending (comments_require_approval defaults to true)", row.Status)
	}
	if row.PostID != post.ID {
		t.Errorf("stored post_id = %d, want %d", row.PostID, post.ID)
	}
	if row.AuthorID != nil {
		t.Errorf("guest comment has author_id %d, want nil", *row.AuthorID)
	}
	if row.Name != "Guest" || row.Email != "guest@example.test" {
		t.Errorf("stored guest name/email = %q/%q, want Guest/guest@example.test", row.Name, row.Email)
	}
	if row.Body != input {
		t.Errorf("stored body = %q, want the Markdown as typed %q", row.Body, input)
	}

	html := row.BodyHTML
	if !strings.Contains(html, "<strong>") {
		t.Errorf("body_html = %q, want Markdown **粗** rendered as <strong>", html)
	}
	if commentsScriptTag.MatchString(html) {
		t.Errorf("body_html = %q contains a script tag", html)
	}
	if commentsImgTag.MatchString(html) {
		t.Errorf("body_html = %q contains an image tag; images are not allowed in comments", html)
	}
	if commentsEventAttr.MatchString(html) {
		t.Errorf("body_html = %q contains an event-handler attribute on a tag", html)
	}
	if !commentsHasNofollowLink(html, "https://e.com") {
		t.Errorf("body_html = %q, want the [x](https://e.com) link with rel containing nofollow", html)
	}
}

// --- 2. public create: honeypot, length, name and email validation ---------

func TestCommentsPublicCreateRejectsInvalidInput(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "cmt-invalid-author")
	post := commentsPublishedPost(t, h, author, "invalid-post", nil)

	cases := []struct {
		name      string
		overrides map[string]any
		want      int
	}{
		{"honeypot website filled", map[string]any{"website": "https://spam.example.test"}, http.StatusBadRequest},
		{"empty body", map[string]any{"body": ""}, http.StatusBadRequest},
		{"whitespace-only body", map[string]any{"body": "   \n\t"}, http.StatusBadRequest},
		{"body of 5001 characters", map[string]any{"body": strings.Repeat("a", 5001)}, http.StatusBadRequest},
		{"body of 5000 characters and name of 120 accepted", map[string]any{"body": strings.Repeat("a", 5000), "name": strings.Repeat("n", 120)}, http.StatusCreated},
		{"empty name", map[string]any{"name": ""}, http.StatusBadRequest},
		{"name of 121 characters", map[string]any{"name": strings.Repeat("n", 121)}, http.StatusBadRequest},
		{"malformed email", map[string]any{"email": "not-an-email"}, http.StatusBadRequest},
		{"missing guest email", map[string]any{"email": ""}, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := commentsCountRows(t, h.db)
			r := commentsDo(t, h, http.MethodPost, "/api/comments", commentsGuestBody(t, post.ID, c.overrides), "", "")
			if !commentsOK(t, r, c.name, c.want) {
				return
			}
			after := commentsCountRows(t, h.db)
			if c.want == http.StatusBadRequest {
				if after != before {
					t.Errorf("%s stored a row (count %d -> %d)", c.name, before, after)
				}
				if c.name == "honeypot website filled" && !strings.Contains(r.body, "rejected") {
					t.Errorf("honeypot response body = %q, want {\"error\":\"rejected\"}", r.body)
				}
			} else if after != before+1 {
				t.Errorf("%s did not store exactly one row (count %d -> %d)", c.name, before, after)
			}
		})
	}
}

// --- 2b. public create: unavailable posts ---------------------------------

func TestCommentsPublicCreateUnavailablePost(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "cmt-gate-author")
	draft := commentsPublishedPost(t, h, author, "draft-post", func(p *core.Post) { p.Status = "draft" })
	private := commentsPublishedPost(t, h, author, "private-post", func(p *core.Post) { p.Visibility = "private" })
	closed := commentsPublishedPost(t, h, author, "closed-post", func(p *core.Post) { p.AllowComments = commentsBoolPtr(false) })

	cases := []struct {
		name   string
		postID uint
		want   int
	}{
		{"post that does not exist", 999999, http.StatusNotFound},
		{"draft post", draft.ID, http.StatusForbidden},
		{"private post", private.ID, http.StatusForbidden},
		{"post with allow_comments=false", closed.ID, http.StatusForbidden},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := commentsCountRows(t, h.db)
			r := commentsDo(t, h, http.MethodPost, "/api/comments", commentsGuestBody(t, c.postID, nil), "", "")
			commentsOK(t, r, c.name, c.want)
			if after := commentsCountRows(t, h.db); after != before {
				t.Errorf("%s stored a row (count %d -> %d)", c.name, before, after)
			}
		})
	}
}

// --- 3. settings gate creation -------------------------------------------

func TestCommentsSettingsDisabledBlocksCreation(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "cmt-off-author")
	post := commentsPublishedPost(t, h, author, "off-post", nil)
	commentsSetOption(t, h, "comments_enabled", "false")

	before := commentsCountRows(t, h.db)
	commentsOK(t, commentsDo(t, h, http.MethodPost, "/api/comments", commentsGuestBody(t, post.ID, nil), "", ""),
		"guest POST with comments_enabled=false", http.StatusForbidden)
	commentsOK(t, commentsDo(t, h, http.MethodPost, "/api/comments", commentsGuestBody(t, post.ID, nil), h.token(author), ""),
		"signed-in POST with comments_enabled=false", http.StatusForbidden)
	if after := commentsCountRows(t, h.db); after != before {
		t.Errorf("comments_enabled=false still stored rows (count %d -> %d)", before, after)
	}
}

func TestCommentsSettingsRegisteredOnlyRequiresSignIn(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "cmt-reg-author")
	post := commentsPublishedPost(t, h, author, "registered-post", nil)
	commentsSetOption(t, h, "comments_registered_only", "true")

	commentsOK(t, commentsDo(t, h, http.MethodPost, "/api/comments", commentsGuestBody(t, post.ID, nil), "", ""),
		"guest POST with comments_registered_only=true", http.StatusUnauthorized)
	if n := commentsCountRows(t, h.db); n != 0 {
		t.Fatalf("guest comment was stored while registered-only is on (count %d)", n)
	}

	r := commentsDo(t, h, http.MethodPost, "/api/comments", commentsGuestBody(t, post.ID, map[string]any{"body": "from a member"}), h.token(author), "")
	if !commentsOK(t, r, "author POST with comments_registered_only=true", http.StatusCreated) {
		return
	}
	var created struct {
		ID uint `json:"id"`
	}
	if commentsDecode(t, r, "author POST", &created) {
		if row, ok := commentsLoad(t, h.db, created.ID); ok && (row.AuthorID == nil || *row.AuthorID != author.ID) {
			t.Errorf("member comment author_id = %v, want %d", row.AuthorID, author.ID)
		}
	}
}

func TestCommentsSettingsNoApprovalPublishesGuestComments(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "cmt-noapp-author")
	post := commentsPublishedPost(t, h, author, "noapproval-post", nil)
	commentsSetOption(t, h, "comments_require_approval", "false")

	r := commentsDo(t, h, http.MethodPost, "/api/comments", commentsGuestBody(t, post.ID, nil), "", "")
	if !commentsOK(t, r, "guest POST with comments_require_approval=false", http.StatusCreated) {
		return
	}
	if status, ok := commentsStatusOf(t, r, "guest POST response"); ok && status != "approved" {
		t.Errorf("response status = %q, want approved", status)
	}
	var created struct {
		ID uint `json:"id"`
	}
	if commentsDecode(t, r, "guest POST", &created) {
		if row, ok := commentsLoad(t, h.db, created.ID); ok && row.Status != "approved" {
			t.Errorf("stored status = %q, want approved", row.Status)
		}
	}
}

// --- 4. signed-in commenters take identity from the account ---------------

func TestCommentsSignedInUsesAccountIdentityAndApproves(t *testing.T) {
	h := newAccessHarness(t)
	postAuthor := h.user("author", "cmt-signed-postauthor")
	post := commentsPublishedPost(t, h, postAuthor, "signed-post", nil)

	for _, role := range []string{"admin", "editor", "author"} {
		role := role
		u := h.user(role, "cmt-signed-"+role)
		token := h.token(u)
		t.Run(role, func(t *testing.T) {
			body := commentsJSON(t, map[string]any{
				"post_id": post.ID, "name": "Mallory", "email": "mallory@example.test",
				"body": "signed in as " + role, "website": "",
			})
			r := commentsDo(t, h, http.MethodPost, "/api/comments", body, token, "")
			if !commentsOK(t, r, role+" POST /api/comments", http.StatusCreated) {
				return
			}
			if status, ok := commentsStatusOf(t, r, role+" POST response"); ok && status != "approved" {
				t.Errorf("%s comment response status = %q, want approved", role, status)
			}
			var created struct {
				ID uint `json:"id"`
			}
			if !commentsDecode(t, r, role+" POST", &created) {
				return
			}
			row, ok := commentsLoad(t, h.db, created.ID)
			if !ok {
				return
			}
			if row.Status != "approved" {
				t.Errorf("%s comment stored status = %q, want approved", role, row.Status)
			}
			if row.Name != u.Username {
				t.Errorf("%s comment name = %q, want account username %q (body name Mallory must be ignored)", role, row.Name, u.Username)
			}
			if row.Email != u.Email {
				t.Errorf("%s comment email = %q, want account email %q", role, row.Email, u.Email)
			}
			if row.AuthorID == nil || *row.AuthorID != u.ID {
				t.Errorf("%s comment author_id = %v, want %d", role, row.AuthorID, u.ID)
			}
		})
	}
}

// --- 5. rate limit ---------------------------------------------------------

func TestCommentsGuestRateLimitFifthPerMinute(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "cmt-rate-author")
	post := commentsPublishedPost(t, h, author, "rate-post", nil)
	const ip = "203.0.113.50"

	for i := 1; i <= 5; i++ {
		r := commentsDo(t, h, http.MethodPost, "/api/comments", commentsGuestBody(t, post.ID, map[string]any{"body": fmt.Sprintf("burst %d", i)}), "", ip)
		if !commentsOK(t, r, fmt.Sprintf("comment %d from %s", i, ip), http.StatusCreated) {
			return
		}
	}
	r := commentsDo(t, h, http.MethodPost, "/api/comments", commentsGuestBody(t, post.ID, map[string]any{"body": "burst 6"}), "", ip)
	commentsOK(t, r, "sixth comment from the same IP within a minute", http.StatusTooManyRequests)
	if n := commentsCountRows(t, h.db); n != 5 {
		t.Errorf("rows after the rate-limited request = %d, want 5", n)
	}

	other := commentsDo(t, h, http.MethodPost, "/api/comments", commentsGuestBody(t, post.ID, map[string]any{"body": "other ip"}), "", "203.0.113.51")
	commentsOK(t, other, "first comment from a different IP", http.StatusCreated)
}

// --- 6. public list --------------------------------------------------------

func TestCommentsPublicListApprovedOnlyOldestFirstNested(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "cmt-list-author")
	postA := commentsPublishedPost(t, h, author, "list-a", nil)
	postB := commentsPublishedPost(t, h, author, "list-b", nil)

	// Rows are inserted out of created_at order so the ordering cannot come from the primary key.
	later := commentsSeed(t, h, commentsRow(postA.ID, "Later", "approved", 3))
	first := commentsSeed(t, h, commentsRow(postA.ID, "First", "approved", 1))
	reply := commentsSeed(t, h, commentsReply(commentsRow(postA.ID, "Reply", "approved", 2), first.ID))
	grandchild := commentsSeed(t, h, commentsReply(commentsRow(postA.ID, "Grandchild", "approved", 4), reply.ID))
	pending := commentsSeed(t, h, commentsRow(postA.ID, "Pending", "pending", 5))
	spam := commentsSeed(t, h, commentsRow(postA.ID, "Spam", "spam", 6))
	trashed := commentsSeed(t, h, commentsTrashed(commentsRow(postA.ID, "Trashed", "approved", 7)))
	other := commentsSeed(t, h, commentsRow(postB.ID, "OtherPost", "approved", 8))

	list, ok := commentsPublicGet(t, h, postA.ID)
	if !ok {
		return
	}
	if list.Count != 4 {
		t.Errorf("count = %d, want 4 (approved visible comments including replies)", list.Count)
	}
	commentsSameIDs(t, "public list", commentsPublicIDs(list.Items), first.ID, reply.ID, later.ID, grandchild.ID)
	for _, excluded := range []struct {
		label string
		id    uint
	}{{"pending", pending.ID}, {"spam", spam.ID}, {"trashed", trashed.ID}, {"other post", other.ID}} {
		for _, id := range commentsPublicIDs(list.Items) {
			if id == excluded.id {
				t.Errorf("public list contains %s comment %d", excluded.label, id)
			}
		}
	}

	if len(list.Items) != 2 {
		t.Fatalf("top-level items = %d, want 2 (First, Later); items=%+v", len(list.Items), list.Items)
	}
	if list.Items[0].ID != first.ID || list.Items[1].ID != later.ID {
		t.Errorf("top-level order = [%d %d], want oldest first [%d %d]", list.Items[0].ID, list.Items[1].ID, first.ID, later.ID)
	}
	children := list.Items[0].Children
	if len(children) != 2 || children[0].ID != reply.ID || children[1].ID != grandchild.ID {
		t.Errorf("children of First = %+v, want [Reply, Grandchild] flattened under the top-level ancestor, oldest first", children)
	}
	if len(children) > 0 && len(children[0].Children) != 0 {
		t.Errorf("Reply has nested children %+v; replies must be nested one level deep only", children[0].Children)
	}
	if len(list.Items[1].Children) != 0 {
		t.Errorf("Later has children %+v, want none", list.Items[1].Children)
	}
}

// --- 7. admin role gates ---------------------------------------------------

func TestCommentsAdminRoleGates(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "cmt-gate-role-author")
	editor := h.user("editor", "cmt-gate-role-editor")
	post := commentsPublishedPost(t, h, editor, "gate-post", nil)
	pending := commentsSeed(t, h, commentsRow(post.ID, "Gate", "pending", 1))
	authorToken, editorToken := h.token(author), h.token(editor)

	probes := []struct{ method, path, body string }{
		{http.MethodGet, "/api/admin/comments", ""},
		{http.MethodPost, fmt.Sprintf("/api/admin/comments/%d/approve", pending.ID), ""},
		{http.MethodPost, "/api/admin/comments/bulk", fmt.Sprintf(`{"ids":[%d],"action":"approve"}`, pending.ID)},
		{http.MethodPost, fmt.Sprintf("/api/admin/comments/%d/reply", pending.ID), `{"body":"probe"}`},
		{http.MethodDelete, fmt.Sprintf("/api/admin/comments/%d", pending.ID), ""},
	}
	before := commentsSnapshot(t, h.db)
	for _, p := range probes {
		commentsOK(t, commentsDo(t, h, p.method, p.path, p.body, "", ""), "anonymous "+p.method+" "+p.path, http.StatusUnauthorized)
		commentsOK(t, commentsDo(t, h, p.method, p.path, p.body, authorToken, ""), "author "+p.method+" "+p.path, http.StatusForbidden)
	}
	if after := commentsSnapshot(t, h.db); after != before {
		t.Errorf("anonymous or author requests changed the comments table:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	commentsOK(t, commentsDo(t, h, http.MethodGet, "/api/admin/comments", "", editorToken, ""), "editor GET /api/admin/comments", http.StatusOK)
}

// --- 7b. admin list: status, counts, search, post filter -------------------

func TestCommentsAdminListStatusCountsSearchAndPostFilter(t *testing.T) {
	h := newAccessHarness(t)
	editor := h.user("editor", "cmt-list-editor")
	postX := commentsPublishedPost(t, h, editor, "filter-x", nil)
	postY := commentsPublishedPost(t, h, editor, "filter-y", nil)

	pendingA := commentsSeed(t, h, commentsRow(postX.ID, "Alice", "pending", 1))
	pendingB := commentsRow(postY.ID, "Bob", "pending", 2)
	pendingB.Body = "NEEDLE-body-ZZ"
	pendingB.Email = "needle-mail@example.test"
	pendingB = commentsSeed(t, h, pendingB)
	approvedC := commentsSeed(t, h, commentsRow(postX.ID, "Carol", "approved", 3))
	approvedD := commentsSeed(t, h, commentsRow(postY.ID, "Dan", "approved", 4))
	approvedE := commentsSeed(t, h, commentsRow(postX.ID, "Eve", "approved", 5))
	spamF := commentsSeed(t, h, commentsRow(postX.ID, "Spammer", "spam", 6))
	trashG := commentsSeed(t, h, commentsTrashed(commentsRow(postX.ID, "Trent", "approved", 7)))
	editorToken := h.token(editor)

	all, ok := commentsAdminGet(t, h, editorToken, map[string]string{"status": "all"})
	if ok {
		commentsSameIDs(t, "status=all", commentsAdminIDs(all.Items), pendingA.ID, pendingB.ID, approvedC.ID, approvedD.ID, approvedE.ID, spamF.ID)
		if all.Total != 6 {
			t.Errorf("status=all total = %d, want 6 (trashed rows excluded)", all.Total)
		}
		commentsCheckCounts(t, "status=all", all.Counts, 6, 2, 3, 1, 1)
	}

	for _, c := range []struct {
		status string
		want   []uint
	}{
		{"pending", []uint{pendingA.ID, pendingB.ID}},
		{"approved", []uint{approvedC.ID, approvedD.ID, approvedE.ID}},
		{"spam", []uint{spamF.ID}},
		{"trash", []uint{trashG.ID}},
	} {
		res, ok := commentsAdminGet(t, h, editorToken, map[string]string{"status": c.status})
		if !ok {
			continue
		}
		commentsSameIDs(t, "status="+c.status, commentsAdminIDs(res.Items), c.want...)
		if res.Total != int64(len(c.want)) {
			t.Errorf("status=%s total = %d, want %d", c.status, res.Total, len(c.want))
		}
		for _, row := range res.Items {
			if c.status != "trash" && row.Status != c.status {
				t.Errorf("status=%s returned row %d with status %q", c.status, row.ID, row.Status)
			}
		}
	}

	if res, ok := commentsAdminGet(t, h, editorToken, map[string]string{"status": "trash"}); ok {
		for _, row := range res.Items {
			if row.Post.ID != postX.ID || row.Post.Slug != postX.Slug || row.Post.Title == "" {
				t.Errorf("admin row %d post = %+v, want id/title/slug of post %q", row.ID, row.Post, postX.Slug)
			}
		}
	}

	if res, ok := commentsAdminGet(t, h, editorToken, map[string]string{"status": "all", "q": "Carol"}); ok {
		commentsSameIDs(t, "q=Carol (name)", commentsAdminIDs(res.Items), approvedC.ID)
	}
	if res, ok := commentsAdminGet(t, h, editorToken, map[string]string{"status": "all", "q": "needle-body"}); ok {
		commentsSameIDs(t, "q=needle-body (body, case-insensitive)", commentsAdminIDs(res.Items), pendingB.ID)
	}
	if res, ok := commentsAdminGet(t, h, editorToken, map[string]string{"status": "all", "q": "needle-mail"}); ok {
		commentsSameIDs(t, "q=needle-mail (email)", commentsAdminIDs(res.Items), pendingB.ID)
	}
	if res, ok := commentsAdminGet(t, h, editorToken, map[string]string{"status": "trash", "q": "Trent"}); ok {
		commentsSameIDs(t, "status=trash q=Trent", commentsAdminIDs(res.Items), trashG.ID)
	}

	if res, ok := commentsAdminGet(t, h, editorToken, map[string]string{"status": "all", "post_id": fmt.Sprint(postY.ID)}); ok {
		commentsSameIDs(t, "post_id filter for post Y", commentsAdminIDs(res.Items), pendingB.ID, approvedD.ID)
	}
}

func TestCommentsAdminListPagination(t *testing.T) {
	h := newAccessHarness(t)
	editor := h.user("editor", "cmt-page-editor")
	post := commentsPublishedPost(t, h, editor, "page-post", nil)
	var seeded []uint
	for i := 1; i <= 3; i++ {
		seeded = append(seeded, commentsSeed(t, h, commentsRow(post.ID, fmt.Sprintf("Page%d", i), "approved", i)).ID)
	}
	token := h.token(editor)

	first, okFirst := commentsAdminGet(t, h, token, map[string]string{"status": "all", "per_page": "2", "page": "1"})
	if okFirst {
		if len(first.Items) != 2 {
			t.Errorf("page 1 items = %d, want 2", len(first.Items))
		}
		if first.Total != 3 {
			t.Errorf("page 1 total = %d, want 3", first.Total)
		}
		if first.Page != 1 || first.PerPage != 2 {
			t.Errorf("page 1 echoed page=%d per_page=%d, want 1 and 2", first.Page, first.PerPage)
		}
	}
	second, okSecond := commentsAdminGet(t, h, token, map[string]string{"status": "all", "per_page": "2", "page": "2"})
	if okSecond {
		if len(second.Items) != 1 {
			t.Errorf("page 2 items = %d, want 1", len(second.Items))
		}
		if second.Total != 3 {
			t.Errorf("page 2 total = %d, want 3", second.Total)
		}
	}
	if okFirst && okSecond {
		union := append(commentsAdminIDs(first.Items), commentsAdminIDs(second.Items)...)
		commentsSameIDs(t, "pages 1 and 2 together", union, seeded...)
	}
}

// --- 8. moderation actions -------------------------------------------------

func TestCommentsModerationActions(t *testing.T) {
	h := newAccessHarness(t)
	editor := h.user("editor", "cmt-act-editor")
	post := commentsPublishedPost(t, h, editor, "action-post", nil)
	token := h.token(editor)
	c := commentsSeed(t, h, commentsRow(post.ID, "Action", "pending", 1))

	for _, step := range []struct{ action, want string }{
		{"approve", "approved"},
		{"unapprove", "pending"},
		{"spam", "spam"},
	} {
		r := commentsDo(t, h, http.MethodPost, fmt.Sprintf("/api/admin/comments/%d/%s", c.ID, step.action), "", token, "")
		if !commentsOK(t, r, step.action, http.StatusOK) {
			continue
		}
		if status, ok := commentsStatusOf(t, r, step.action+" response"); ok && status != step.want {
			t.Errorf("%s response status = %q, want %q", step.action, status, step.want)
		}
		if row, ok := commentsLoad(t, h.db, c.ID); ok && row.Status != step.want {
			t.Errorf("after %s stored status = %q, want %q", step.action, row.Status, step.want)
		}
	}

	visible := commentsSeed(t, h, commentsRow(post.ID, "Visible", "approved", 2))
	before, ok := commentsPublicGet(t, h, post.ID)
	if ok {
		commentsSameIDs(t, "public list before trash", commentsPublicIDs(before.Items), visible.ID)
	}
	commentsOK(t, commentsDo(t, h, http.MethodPost, fmt.Sprintf("/api/admin/comments/%d/trash", visible.ID), "", token, ""),
		"trash approved comment", http.StatusOK)
	if row, err := commentsLoadAny(h.db, visible.ID); err != nil {
		t.Errorf("load trashed comment %d: %v", visible.ID, err)
	} else if !row.DeletedAt.Valid {
		t.Errorf("trashed comment %d has no deleted_at", visible.ID)
	}
	var scoped core.Comment
	if err := h.db.First(&scoped, visible.ID).Error; err == nil {
		t.Errorf("trashed comment %d is still returned by the normal scope", visible.ID)
	}
	if res, ok := commentsAdminGet(t, h, token, map[string]string{"status": "trash"}); ok {
		commentsSameIDs(t, "status=trash after trash action", commentsAdminIDs(res.Items), visible.ID)
	}
	if after, ok := commentsPublicGet(t, h, post.ID); ok {
		if after.Count != 0 || len(after.Items) != 0 {
			t.Errorf("public list after trash = count %d items %+v, want none", after.Count, after.Items)
		}
	}

	pend := commentsSeed(t, h, commentsRow(post.ID, "Restorable", "pending", 3))
	commentsOK(t, commentsDo(t, h, http.MethodPost, fmt.Sprintf("/api/admin/comments/%d/trash", pend.ID), "", token, ""),
		"trash pending comment", http.StatusOK)
	commentsOK(t, commentsDo(t, h, http.MethodPost, fmt.Sprintf("/api/admin/comments/%d/restore", pend.ID), "", token, ""),
		"restore trashed comment", http.StatusOK)
	if row, ok := commentsLoad(t, h.db, pend.ID); ok {
		if row.DeletedAt.Valid {
			t.Errorf("restored comment %d is still soft-deleted", pend.ID)
		}
		if row.Status != "pending" {
			t.Errorf("restored comment status = %q, want pending (its status before trash)", row.Status)
		}
	}
	if res, ok := commentsAdminGet(t, h, token, map[string]string{"status": "pending"}); ok {
		commentsSameIDs(t, "status=pending after restore", commentsAdminIDs(res.Items), pend.ID)
	}
	if res, ok := commentsAdminGet(t, h, token, map[string]string{"status": "trash"}); ok {
		for _, row := range res.Items {
			if row.ID == pend.ID {
				t.Errorf("restored comment %d still listed under status=trash", pend.ID)
			}
		}
	}
}

// --- 8b. bulk actions ------------------------------------------------------

func TestCommentsBulkActions(t *testing.T) {
	h := newAccessHarness(t)
	editor := h.user("editor", "cmt-bulk-editor")
	post := commentsPublishedPost(t, h, editor, "bulk-post", nil)
	token := h.token(editor)
	p1 := commentsSeed(t, h, commentsRow(post.ID, "Bulk1", "pending", 1))
	p2 := commentsSeed(t, h, commentsRow(post.ID, "Bulk2", "pending", 2))
	p3 := commentsSeed(t, h, commentsRow(post.ID, "Bulk3", "approved", 3))

	r := commentsDo(t, h, http.MethodPost, "/api/admin/comments/bulk",
		commentsJSON(t, map[string]any{"ids": []uint{p1.ID, p2.ID}, "action": "approve"}), token, "")
	if commentsOK(t, r, "bulk approve", http.StatusOK) {
		var out struct {
			Updated int `json:"updated"`
		}
		if commentsDecode(t, r, "bulk approve", &out) && out.Updated != 2 {
			t.Errorf("bulk approve updated = %d, want 2; body=%q", out.Updated, r.body)
		}
	}
	for _, id := range []uint{p1.ID, p2.ID} {
		if row, ok := commentsLoad(t, h.db, id); ok && row.Status != "approved" {
			t.Errorf("bulk-approved comment %d status = %q, want approved", id, row.Status)
		}
	}
	if row, ok := commentsLoad(t, h.db, p3.ID); ok && row.Status != "approved" {
		t.Errorf("comment %d outside the bulk ids changed to %q", p3.ID, row.Status)
	}

	del := commentsDo(t, h, http.MethodPost, "/api/admin/comments/bulk",
		commentsJSON(t, map[string]any{"ids": []uint{p3.ID}, "action": "delete"}), token, "")
	switch del.code {
	case http.StatusConflict:
	case http.StatusOK:
		var out struct {
			Updated int `json:"updated"`
		}
		if commentsDecode(t, del, "bulk delete of a non-trashed row", &out) && out.Updated != 0 {
			t.Errorf("bulk delete of a non-trashed row updated = %d, want 0", out.Updated)
		}
	default:
		t.Errorf("bulk delete of a non-trashed row returned %d, want 409 or 200 with updated 0; body=%q", del.code, del.body)
	}
	if _, ok := commentsLoad(t, h.db, p3.ID); !ok {
		t.Errorf("bulk delete removed non-trashed comment %d", p3.ID)
	}
}

// --- 9. reply by the site owner -------------------------------------------

func TestCommentsReplyByEditor(t *testing.T) {
	h := newAccessHarness(t)
	editor := h.user("editor", "cmt-reply-editor")
	author := h.user("author", "cmt-reply-author")
	post := commentsPublishedPost(t, h, author, "reply-post", nil)
	parent := commentsSeed(t, h, commentsRow(post.ID, "Guest", "approved", 1))

	r := commentsDo(t, h, http.MethodPost, fmt.Sprintf("/api/admin/comments/%d/reply", parent.ID),
		commentsJSON(t, map[string]any{"body": "站長回覆 **好**"}), h.token(editor), "")
	if !commentsOK(t, r, "editor reply", http.StatusCreated) {
		return
	}
	var reply struct {
		ID       uint   `json:"id"`
		ParentID *uint  `json:"parent_id"`
		PostID   uint   `json:"post_id"`
		AuthorID *uint  `json:"author_id"`
		Name     string `json:"name"`
		Status   string `json:"status"`
	}
	if !commentsDecode(t, r, "editor reply", &reply) {
		return
	}
	if reply.ID == 0 {
		t.Fatalf("reply has no id; body=%q", r.body)
	}
	row, ok := commentsLoad(t, h.db, reply.ID)
	if !ok {
		return
	}
	if row.Status != "approved" || reply.Status != "approved" {
		t.Errorf("reply status = %q (response %q), want approved", row.Status, reply.Status)
	}
	if row.ParentID == nil || *row.ParentID != parent.ID {
		t.Errorf("reply parent_id = %v, want %d", row.ParentID, parent.ID)
	}
	if row.PostID != post.ID {
		t.Errorf("reply post_id = %d, want %d (the parent's post)", row.PostID, post.ID)
	}
	if row.AuthorID == nil || *row.AuthorID != editor.ID {
		t.Errorf("reply author_id = %v, want editor %d", row.AuthorID, editor.ID)
	}
	if row.Name != editor.Username {
		t.Errorf("reply name = %q, want editor username %q", row.Name, editor.Username)
	}
	if row.Body != "站長回覆 **好**" {
		t.Errorf("reply body = %q, want the Markdown as typed", row.Body)
	}
}

// --- 10. permanent delete only from trash ----------------------------------

func TestCommentsPermanentDeleteRequiresTrash(t *testing.T) {
	h := newAccessHarness(t)
	editor := h.user("editor", "cmt-del-editor")
	post := commentsPublishedPost(t, h, editor, "delete-post", nil)
	token := h.token(editor)
	c := commentsSeed(t, h, commentsRow(post.ID, "Keep", "pending", 1))

	commentsOK(t, commentsDo(t, h, http.MethodDelete, fmt.Sprintf("/api/admin/comments/%d", c.ID), "", token, ""),
		"DELETE of a comment that is not in trash", http.StatusConflict)
	if _, ok := commentsLoad(t, h.db, c.ID); !ok {
		t.Fatalf("comment %d was removed although it was not in trash", c.ID)
	}

	commentsOK(t, commentsDo(t, h, http.MethodPost, fmt.Sprintf("/api/admin/comments/%d/trash", c.ID), "", token, ""),
		"trash before permanent delete", http.StatusOK)
	commentsOK(t, commentsDo(t, h, http.MethodDelete, fmt.Sprintf("/api/admin/comments/%d", c.ID), "", token, ""),
		"DELETE of a trashed comment", http.StatusOK)
	if n := commentsDeletedCount(t, h.db, c.ID); n != 0 {
		t.Errorf("rows for comment %d including soft-deleted = %d after permanent delete, want 0", c.ID, n)
	}
}

// --- 11. commentsForPost ---------------------------------------------------

func TestCommentsForPostTreeCountAndOpen(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "cmt-tree-author")
	post := commentsPublishedPost(t, h, author, "tree-post", nil)
	a := commentsSeed(t, h, commentsRow(post.ID, "TreeA", "approved", 1))
	b := commentsSeed(t, h, commentsRow(post.ID, "TreeB", "approved", 2))
	commentsSeed(t, h, commentsRow(post.ID, "TreeP", "pending", 3))

	loaded := core.Post{}
	if err := h.db.First(&loaded, post.ID).Error; err != nil {
		t.Fatalf("reload post: %v", err)
	}
	tree, count, open := commentsForPost(loaded, false)
	if count != 2 {
		t.Errorf("commentsForPost count = %d, want 2 (approved only)", count)
	}
	if !open {
		t.Errorf("commentsForPost open = false, want true for an open published post")
	}
	var ids []uint
	var walk func([]core.Comment)
	walk = func(cs []core.Comment) {
		for _, c := range cs {
			ids = append(ids, c.ID)
			walk(c.Children)
		}
	}
	walk(tree)
	commentsSameIDs(t, "commentsForPost tree", ids, a.ID, b.ID)

	closed := commentsPublishedPost(t, h, author, "tree-closed", func(p *core.Post) { p.AllowComments = commentsBoolPtr(false) })
	closedLoaded := core.Post{}
	if err := h.db.First(&closedLoaded, closed.ID).Error; err != nil {
		t.Fatalf("reload closed post: %v", err)
	}
	if closedLoaded.CommentsAllowed() {
		t.Fatalf("fixture error: closed post reports CommentsAllowed() = true")
	}
	if _, _, open := commentsForPost(closedLoaded, false); open {
		t.Errorf("commentsForPost open = true for a post with allow_comments=false, want false")
	}

	commentsSetOption(t, h, "comments_enabled", "false")
	if _, _, open := commentsForPost(loaded, false); open {
		t.Errorf("commentsForPost open = true while comments_enabled=false, want false")
	}
}

// --- 12. admin edit of a comment body --------------------------------------

// commentsEditRow is the admin comment row returned by PUT /api/admin/comments/:id.
type commentsEditRow struct {
	ID       uint   `json:"id"`
	ParentID *uint  `json:"parent_id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Body     string `json:"body"`
	BodyHTML string `json:"body_html"`
	Status   string `json:"status"`
}

func commentsEditPath(id uint) string {
	return fmt.Sprintf("/api/admin/comments/%d", id)
}

func commentsEditBody(t *testing.T, body string) string {
	t.Helper()
	return commentsJSON(t, map[string]any{"body": body})
}

func TestCommentsEditRewritesBodyAndKeepsIdentity(t *testing.T) {
	h := newAccessHarness(t)
	editor := h.user("editor", "cmt-edit-editor")
	author := h.user("author", "cmt-edit-author")
	post := commentsPublishedPost(t, h, author, "edit-post", nil)
	parent := commentsSeed(t, h, commentsRow(post.ID, "Parent", "approved", 1))
	c := commentsSeed(t, h, commentsReply(commentsRow(post.ID, "Edited", "approved", 2), parent.ID))

	input := "  Changed **bold** <script>alert(1)</script> [site](https://e.com)  "
	const want = "Changed **bold** <script>alert(1)</script> [site](https://e.com)"
	wantHTML := renderCommentMarkdown(want)

	r := commentsDo(t, h, http.MethodPut, commentsEditPath(c.ID), commentsEditBody(t, input), h.token(editor), "")
	if !commentsOK(t, r, "editor PUT comment", http.StatusOK) {
		return
	}
	var out commentsEditRow
	if !commentsDecode(t, r, "editor PUT comment", &out) {
		return
	}
	if out.ID != c.ID {
		t.Errorf("PUT response id = %d, want %d", out.ID, c.ID)
	}
	if out.Body != want {
		t.Errorf("PUT response body = %q, want trimmed %q", out.Body, want)
	}
	if out.BodyHTML != wantHTML {
		t.Errorf("PUT response body_html = %q, want the sanitized render %q", out.BodyHTML, wantHTML)
	}
	if commentsScriptTag.MatchString(out.BodyHTML) {
		t.Errorf("PUT response body_html kept a script tag: %q", out.BodyHTML)
	}
	if !strings.Contains(out.BodyHTML, "<strong>bold</strong>") {
		t.Errorf("PUT response body_html did not render Markdown emphasis: %q", out.BodyHTML)
	}
	if !commentsHasNofollowLink(out.BodyHTML, "https://e.com") {
		t.Errorf("PUT response body_html link lacks rel nofollow: %q", out.BodyHTML)
	}
	if out.Status != "approved" {
		t.Errorf("PUT response status = %q, want approved (unchanged)", out.Status)
	}
	if out.Name != c.Name || out.Email != c.Email {
		t.Errorf("PUT response name/email = %q/%q, want %q/%q", out.Name, out.Email, c.Name, c.Email)
	}
	if out.ParentID == nil || *out.ParentID != parent.ID {
		t.Errorf("PUT response parent_id = %v, want %d", out.ParentID, parent.ID)
	}

	row, ok := commentsLoad(t, h.db, c.ID)
	if !ok {
		return
	}
	if row.Body != want {
		t.Errorf("stored body = %q, want %q", row.Body, want)
	}
	if row.BodyHTML != wantHTML {
		t.Errorf("stored body_html = %q, want %q", row.BodyHTML, wantHTML)
	}
	if row.Status != "approved" {
		t.Errorf("stored status = %q, want approved (unchanged)", row.Status)
	}
	if row.Name != c.Name || row.Email != c.Email {
		t.Errorf("stored name/email = %q/%q, want %q/%q", row.Name, row.Email, c.Name, c.Email)
	}
	if row.ParentID == nil || *row.ParentID != parent.ID {
		t.Errorf("stored parent_id = %v, want %d", row.ParentID, parent.ID)
	}
	if row.PostID != post.ID {
		t.Errorf("stored post_id = %d, want %d", row.PostID, post.ID)
	}
}

func TestCommentsEditKeepsStatus(t *testing.T) {
	h := newAccessHarness(t)
	editor := h.user("editor", "cmt-edst-editor")
	author := h.user("author", "cmt-edst-author")
	post := commentsPublishedPost(t, h, author, "edit-status-post", nil)
	token := h.token(editor)

	for i, status := range []string{"pending", "approved", "spam"} {
		c := commentsSeed(t, h, commentsRow(post.ID, "Kept"+status, status, i+1))
		r := commentsDo(t, h, http.MethodPut, commentsEditPath(c.ID), commentsEditBody(t, "Edited while "+status), token, "")
		if !commentsOK(t, r, "PUT "+status+" comment", http.StatusOK) {
			continue
		}
		if got, ok := commentsStatusOf(t, r, "PUT "+status+" response"); ok && got != status {
			t.Errorf("PUT response status for a %s comment = %q, want %q", status, got, status)
		}
		if row, ok := commentsLoad(t, h.db, c.ID); ok && row.Status != status {
			t.Errorf("stored status after editing a %s comment = %q, want %q", status, row.Status, status)
		}
	}
}

func TestCommentsEditValidatesBody(t *testing.T) {
	h := newAccessHarness(t)
	editor := h.user("editor", "cmt-edv-editor")
	author := h.user("author", "cmt-edv-author")
	post := commentsPublishedPost(t, h, author, "edit-valid-post", nil)
	c := commentsSeed(t, h, commentsRow(post.ID, "Valid", "approved", 1))
	token := h.token(editor)
	path := commentsEditPath(c.ID)

	before := commentsSnapshot(t, h.db)
	for _, tc := range []struct{ name, body string }{
		{"empty", ""},
		{"whitespace only", " \n\t "},
		{"5001 characters", strings.Repeat("一", 5001)},
	} {
		commentsOK(t, commentsDo(t, h, http.MethodPut, path, commentsEditBody(t, tc.body), token, ""),
			"PUT body "+tc.name, http.StatusBadRequest)
	}
	if after := commentsSnapshot(t, h.db); after != before {
		t.Errorf("rejected PUT requests changed the comments table:\nbefore:\n%s\nafter:\n%s", before, after)
	}

	limit := strings.Repeat("一", 5000)
	r := commentsDo(t, h, http.MethodPut, path, commentsEditBody(t, "  "+limit+"  "), token, "")
	if commentsOK(t, r, "PUT 5000 characters after trimming", http.StatusOK) {
		if row, ok := commentsLoad(t, h.db, c.ID); ok && row.Body != limit {
			t.Errorf("stored body length = %d runes, want the trimmed 5000", len([]rune(row.Body)))
		}
	}
}

func TestCommentsEditMissingOrTrashedIsNotFound(t *testing.T) {
	h := newAccessHarness(t)
	editor := h.user("editor", "cmt-edn-editor")
	author := h.user("author", "cmt-edn-author")
	post := commentsPublishedPost(t, h, author, "edit-missing-post", nil)
	trashed := commentsSeed(t, h, commentsTrashed(commentsRow(post.ID, "Gone", "approved", 1)))
	token := h.token(editor)

	before := commentsSnapshot(t, h.db)
	for _, id := range []uint{999999, trashed.ID} {
		r := commentsDo(t, h, http.MethodPut, commentsEditPath(id), commentsEditBody(t, "x"), token, "")
		if commentsOK(t, r, fmt.Sprintf("PUT comment %d", id), http.StatusNotFound) &&
			!strings.Contains(r.body, "Comment not found") {
			t.Errorf("PUT comment %d 404 body = %q, want the handler's \"Comment not found\" error", id, r.body)
		}
	}
	if after := commentsSnapshot(t, h.db); after != before {
		t.Errorf("PUT on a missing or trashed comment changed the comments table:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestCommentsEditRoleGates(t *testing.T) {
	h := newAccessHarness(t)
	editor := h.user("editor", "cmt-edg-editor")
	author := h.user("author", "cmt-edg-author")
	post := commentsPublishedPost(t, h, editor, "edit-gate-post", nil)
	c := commentsSeed(t, h, commentsRow(post.ID, "Gate", "pending", 1))
	path := commentsEditPath(c.ID)
	body := commentsEditBody(t, "Changed by a probe")

	before := commentsSnapshot(t, h.db)
	commentsOK(t, commentsDo(t, h, http.MethodPut, path, body, "", ""), "anonymous PUT comment", http.StatusUnauthorized)
	commentsOK(t, commentsDo(t, h, http.MethodPut, path, body, h.token(author), ""), "author PUT comment", http.StatusForbidden)
	if after := commentsSnapshot(t, h.db); after != before {
		t.Errorf("anonymous or author PUT changed the comments table:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	commentsOK(t, commentsDo(t, h, http.MethodPut, path, body, h.token(editor), ""), "editor PUT comment", http.StatusOK)
}

// --- 13. a staff reply approves the parent comment -------------------------

func TestCommentsReplyApprovesParentWhenPending(t *testing.T) {
	h := newAccessHarness(t)
	editor := h.user("editor", "cmt-rpp-editor")
	author := h.user("author", "cmt-rpp-author")
	post := commentsPublishedPost(t, h, author, "reply-pending-post", nil)
	parent := commentsSeed(t, h, commentsRow(post.ID, "Waiting", "pending", 1))

	r := commentsDo(t, h, http.MethodPost, fmt.Sprintf("/api/admin/comments/%d/reply", parent.ID),
		commentsJSON(t, map[string]any{"body": "Thanks for waiting"}), h.token(editor), "")
	if !commentsOK(t, r, "editor reply to pending parent", http.StatusCreated) {
		return
	}
	var reply struct {
		ID uint `json:"id"`
	}
	if !commentsDecode(t, r, "editor reply to pending parent", &reply) {
		return
	}

	row, ok := commentsLoad(t, h.db, parent.ID)
	if ok {
		if row.Status != "approved" {
			t.Errorf("parent status after a staff reply = %q, want approved", row.Status)
		}
		if row.Name != parent.Name || row.Email != parent.Email || row.Body != parent.Body {
			t.Errorf("parent name/email/body changed by the reply: %q/%q/%q", row.Name, row.Email, row.Body)
		}
	}
	if res, ok := commentsPublicGet(t, h, post.ID); ok {
		commentsSameIDs(t, "public list after a staff reply", commentsPublicIDs(res.Items), parent.ID, reply.ID)
	}
}

func TestCommentsReplyApprovesParentLeavesApprovedAlone(t *testing.T) {
	h := newAccessHarness(t)
	editor := h.user("editor", "cmt-rpa-editor")
	author := h.user("author", "cmt-rpa-author")
	post := commentsPublishedPost(t, h, author, "reply-approved-post", nil)
	parent := commentsSeed(t, h, commentsRow(post.ID, "Settled", "approved", 1))

	r := commentsDo(t, h, http.MethodPost, fmt.Sprintf("/api/admin/comments/%d/reply", parent.ID),
		commentsJSON(t, map[string]any{"body": "Noted"}), h.token(editor), "")
	commentsOK(t, r, "editor reply to approved parent", http.StatusCreated)

	if row, ok := commentsLoad(t, h.db, parent.ID); ok && row.Status != "approved" {
		t.Errorf("approved parent status after a staff reply = %q, want approved", row.Status)
	}
}
