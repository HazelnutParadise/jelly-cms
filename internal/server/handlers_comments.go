package server

import (
	"bytes"
	"errors"
	"net/http"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"gorm.io/gorm"
)

// Comments (slot C3).
//
// Public:
//	GET  /api/comments?post_id=            → {count, items:[{id,parent_id,name,body_html,created_at,author_role?,children:[...]}]} approved only, oldest first, nested one level deep for display (deeper replies flatten under their top-level ancestor)
//	POST /api/comments                     {post_id, parent_id?, name, email, body, website} → 201 {id, status}
//	   - website is a honeypot: non-empty → 400 silently shaped like success? No: → 400 {"error":"rejected"}.
//	   - post must exist, be published, public and CommentsAllowed(); option comments_enabled != "false" → else 403
//	   - option comments_registered_only == "true" and no signed-in user → 401
//	   - body: trimmed, 1..5000 chars → else 400; name 1..120, email valid format (guests) → else 400
//	   - signed-in user: name/email taken from the account; status approved when role is admin/editor/author, else per approval rule
//	   - approval rule: option comments_require_approval (default "true") → pending; "false" → approved
//	   - rate limit: 5 posts per minute per IP (in-memory) → 429
//	   - BodyHTML = renderCommentMarkdown(body): Markdown → HTML with a strict sanitizer (links get rel="nofollow ugc", no images/html/scripts)
//	   - calls OnCommentPending(comment) when a hook is set (notifications land in ticket 05)
// Admin (admin/editor):
//	GET  /api/admin/comments?status=all|pending|approved|spam|trash&q=&post_id=&page=&per_page=
//	     → {items:[comment + post:{id,title,slug} + author?], total, page, per_page, counts:{all,pending,approved,spam,trash}}
//	     q matches name, email or body (ILIKE). trash lists soft-deleted rows.
//	POST /api/admin/comments/:id/approve | unapprove | spam | trash | restore   → 200 comment
//	POST /api/admin/comments/bulk          {ids:[...], action:"approve|unapprove|spam|trash|restore|delete"} → 200 {"updated": n}
//	POST /api/admin/comments/:id/reply     {body} → 201 comment (author = current user, status approved, parent = :id, post = parent's post); a pending parent becomes approved
//	PUT  /api/admin/comments/:id           {body} → 200 comment with body and body_html replaced; trashed → 404; body 1..5000 chars else 400
//	DELETE /api/admin/comments/:id         permanent; only allowed when the row is in trash → 200, otherwise 409
//
// Theme: post pages receive "Comments" ([]core.Comment tree as in GET), "CommentCount" (int) and "CommentsOpen" (bool);
// commentsForPost(postID) builds that data and is used by handlers_public.go.
// Template partial web/themes/default/_comments.html ({{define "comments"}}; files named _*.html are parsed with every page) renders list + form per
// design/project/Archive.dc.html (留言區): form with name/email/body, pending notice for the author's own
// unapproved comment is not required in this slot.
//
// Admin page: web/admin/comments.html per design/project/Comments.dc.html (status segments with counts, bulk bar,
// row actions approve/reply/edit/spam/trash, J/K keyboard, reply sheet).

// OnCommentPending is called after a pending comment is stored. Nil by default.
var OnCommentPending func(comment core.Comment)

const (
	commentMaxBody       = 5000
	commentMaxName       = 120
	commentMaxEmail      = 255
	commentMaxBulk       = 500
	commentMaxAdminPer   = 100
	commentDefaultPer    = 20
	commentRateLimit     = 5
	commentRateWindow    = time.Minute
	commentRateSweepSize = 4096
)

func registerCommentRoutes(e *echo.Echo, manage *echo.Group, tm *theme.Manager) {
	e.GET("/api/comments", listPublicComments)
	e.POST("/api/comments", createPublicComment)

	manage.GET("/comments", listAdminComments)
	manage.POST("/comments/:id/approve", commentAction("approve"))
	manage.POST("/comments/:id/unapprove", commentAction("unapprove"))
	manage.POST("/comments/:id/spam", commentAction("spam"))
	manage.POST("/comments/:id/trash", commentAction("trash"))
	manage.POST("/comments/:id/restore", commentAction("restore"))
	manage.POST("/comments/bulk", bulkCommentAction)
	manage.POST("/comments/:id/reply", replyComment)
	manage.PUT("/comments/:id", updateComment)
	manage.DELETE("/comments/:id", deleteCommentPermanently)
}

// pendingCommentCount feeds the rail badge; 0 when the database is missing.
func pendingCommentCount(c echo.Context) int {
	if data.DB == nil {
		return 0
	}
	var n int64
	if err := data.DB.Model(&core.Comment{}).Where("status = ?", "pending").Count(&n).Error; err != nil {
		return 0
	}
	return int(n)
}

// commentOption reads one global option and returns fallback when it is absent.
func commentOption(key, fallback string) string {
	if data.DB == nil {
		return fallback
	}
	var opt core.Option
	if err := data.DB.Where("key = ?", key).First(&opt).Error; err != nil {
		return fallback
	}
	return opt.Value
}

func commentsEnabled() bool { return commentOption("comments_enabled", "true") != "false" }
func commentsRegisteredOnly() bool {
	return commentOption("comments_registered_only", "false") == "true"
}
func commentsNeedApproval() bool {
	return commentOption("comments_require_approval", "true") != "false"
}

// commentPostVisible is the public-read rule: published and public.
func commentPostVisible(post core.Post) bool {
	return post.Status == "published" && post.Visibility == "public"
}

// commentPostOpen is the write rule: visible and CommentsAllowed().
func commentPostOpen(post core.Post) bool {
	return commentPostVisible(post) && post.CommentsAllowed()
}

// commentsForPost returns the approved tree, its count and whether the form
// should be shown for the post.
func commentsForPost(post core.Post, signedIn bool) ([]core.Comment, int, bool) {
	rows, err := approvedCommentRows(data.DB, post.ID)
	if err != nil {
		return []core.Comment{}, 0, false
	}
	open := post.CommentsAllowed() && commentsEnabled() && (signedIn || !commentsRegisteredOnly())
	return buildCommentTree(rows), len(rows), open
}

// commentViewerSignedIn reports whether the request carries a valid account.
// A missing or bad token is a guest, not an error.
func commentViewerSignedIn(c echo.Context) bool {
	_, ok := commentViewer(c)
	return ok
}

func commentViewer(c echo.Context) (*core.User, bool) {
	user, err := auth.GetCurrentUser(c)
	if err != nil || user == nil {
		return nil, false
	}
	return user, true
}

func commentPrivileged(role string) bool {
	return role == "admin" || role == "editor" || role == "author"
}

// approvedCommentRows loads the approved rows of a post, oldest first.
func approvedCommentRows(db *gorm.DB, postID uint) ([]core.Comment, error) {
	var rows []core.Comment
	err := db.Preload("Author", commentPreviewAuthor).
		Where("post_id = ? AND status = ?", postID, "approved").
		Order("created_at ASC, id ASC").
		Find(&rows).Error
	return rows, err
}

// buildCommentTree groups approved rows under their top-level ancestor. Deeper
// replies flatten under that ancestor, keeping their own parent_id. A reply
// whose parent is not approved has no visible ancestor and becomes top-level.
func buildCommentTree(rows []core.Comment) []core.Comment {
	known := make(map[uint]core.Comment, len(rows))
	for _, row := range rows {
		known[row.ID] = row
	}
	rootOf := func(row core.Comment) uint {
		root := row.ID
		cur := row
		for steps := 0; cur.ParentID != nil && steps < len(rows); steps++ {
			parent, ok := known[*cur.ParentID]
			if !ok {
				break
			}
			root = parent.ID
			cur = parent
		}
		return root
	}

	tops := make([]core.Comment, 0)
	index := make(map[uint]int)
	for _, row := range rows {
		if rootOf(row) == row.ID {
			index[row.ID] = len(tops)
			tops = append(tops, row)
		}
	}
	for _, row := range rows {
		root := rootOf(row)
		if root == row.ID {
			continue
		}
		i := index[root]
		tops[i].Children = append(tops[i].Children, row)
	}
	return tops
}

// renderCommentMarkdown converts comment Markdown to sanitized HTML. Raw HTML
// in the Markdown is omitted by goldmark; bluemonday then keeps a small set of
// elements and only http, https, mailto and relative links.
func renderCommentMarkdown(body string) string {
	var buf bytes.Buffer
	if err := commentMarkdown.Convert([]byte(body), &buf); err != nil {
		return commentPolicy.Sanitize(body)
	}
	out := commentPolicy.Sanitize(buf.String())
	return strings.TrimSpace(commentNofollowRel.ReplaceAllString(out, "${1}nofollow ugc${2}"))
}

var (
	commentMarkdown = goldmark.New()
	commentPolicy   = newCommentPolicy()
	// bluemonday writes rel="nofollow" on links; the comment rule also wants ugc.
	commentNofollowRel = regexp.MustCompile(`(<a\s[^>]*\brel=")nofollow(")`)
)

func newCommentPolicy() *bluemonday.Policy {
	p := bluemonday.StrictPolicy()
	p.AllowElements("p", "br", "strong", "em", "code", "pre", "blockquote", "ul", "ol", "li")
	p.AllowElements("a")
	p.AllowAttrs("href").OnElements("a")
	p.AllowStandardURLs()
	p.RequireNoFollowOnLinks(true)
	return p
}

// commentRateLimiter allows commentRateLimit posts per IP per rolling window.
type commentRateLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

var commentLimiter = &commentRateLimiter{hits: make(map[string][]time.Time)}

func (l *commentRateLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := now.Add(-commentRateWindow)
	kept := make([]time.Time, 0, commentRateLimit)
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	allowed := len(kept) < commentRateLimit
	if allowed {
		kept = append(kept, now)
	}
	if len(kept) == 0 {
		delete(l.hits, key)
	} else {
		l.hits[key] = kept
	}
	if len(l.hits) > commentRateSweepSize {
		for k, times := range l.hits {
			if len(times) == 0 || !times[len(times)-1].After(cutoff) {
				delete(l.hits, k)
			}
		}
	}
	return allowed
}

type publicComment struct {
	ID         uint            `json:"id"`
	ParentID   *uint           `json:"parent_id"`
	Name       string          `json:"name"`
	BodyHTML   string          `json:"body_html"`
	CreatedAt  time.Time       `json:"created_at"`
	AuthorRole string          `json:"author_role,omitempty"`
	Children   []publicComment `json:"children"`
}

type publicCommentList struct {
	Count int             `json:"count"`
	Items []publicComment `json:"items"`
}

func toPublicComments(rows []core.Comment) []publicComment {
	out := make([]publicComment, 0, len(rows))
	for _, row := range rows {
		item := publicComment{
			ID:        row.ID,
			ParentID:  row.ParentID,
			Name:      row.Name,
			BodyHTML:  row.BodyHTML,
			CreatedAt: row.CreatedAt,
			Children:  toPublicComments(row.Children),
		}
		if row.Author != nil {
			item.AuthorRole = row.Author.Role
		}
		out = append(out, item)
	}
	return out
}

func commentPostError(c echo.Context, err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Post not found"})
	}
	return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load post"})
}

func listPublicComments(c echo.Context) error {
	postID, ok := parseTermID(c.QueryParam("post_id"))
	if !ok {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "post_id is required"})
	}
	var post core.Post
	if err := data.DB.Where("id = ?", postID).First(&post).Error; err != nil {
		return commentPostError(c, err)
	}
	if !commentPostVisible(post) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Post not found"})
	}
	rows, err := approvedCommentRows(data.DB, postID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load comments"})
	}
	return c.JSON(http.StatusOK, publicCommentList{
		Count: len(rows),
		Items: toPublicComments(buildCommentTree(rows)),
	})
}

type commentInput struct {
	PostID   uint   `json:"post_id"`
	ParentID *uint  `json:"parent_id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Body     string `json:"body"`
	Website  string `json:"website"`
}

func validCommentBody(body string) bool {
	n := utf8.RuneCountInString(body)
	return n >= 1 && n <= commentMaxBody
}

func validCommentEmail(email string) bool {
	if email == "" || len(email) > commentMaxEmail {
		return false
	}
	addr, err := mail.ParseAddress(email)
	return err == nil && addr.Address == email
}

// clipRunes shortens s to at most limit characters without splitting a rune.
func clipRunes(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit])
}

func createPublicComment(c echo.Context) error {
	var in commentInput
	if err := decodeTermBody(c, &in); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}
	if strings.TrimSpace(in.Website) != "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "rejected"})
	}
	if !commentLimiter.allow(c.RealIP(), time.Now()) {
		return c.JSON(http.StatusTooManyRequests, map[string]string{"error": "Too many comments, try again later"})
	}
	if !commentsEnabled() {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "Comments are disabled"})
	}
	if in.PostID == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "post_id is required"})
	}
	var post core.Post
	if err := data.DB.Where("id = ?", in.PostID).First(&post).Error; err != nil {
		return commentPostError(c, err)
	}
	if !commentPostOpen(post) {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "Comments are closed for this post"})
	}
	user, signedIn := commentViewer(c)
	if !signedIn && commentsRegisteredOnly() {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Sign in to comment"})
	}

	body := strings.TrimSpace(in.Body)
	if !validCommentBody(body) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Comment must be 1 to 5000 characters"})
	}

	var name, email string
	if signedIn {
		name, email = user.Username, user.Email
	} else {
		name = strings.TrimSpace(in.Name)
		if n := utf8.RuneCountInString(name); n < 1 || n > commentMaxName {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Name must be 1 to 120 characters"})
		}
		email = strings.TrimSpace(in.Email)
		if !validCommentEmail(email) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "A valid email is required"})
		}
	}

	var parentID *uint
	if in.ParentID != nil {
		var parent core.Comment
		err := data.DB.Where("id = ? AND post_id = ? AND status = ?", *in.ParentID, post.ID, "approved").First(&parent).Error
		if *in.ParentID == 0 || err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Reply target is not available"})
		}
		parentID = &parent.ID
	}

	status := "pending"
	if !commentsNeedApproval() || (signedIn && commentPrivileged(user.Role)) {
		status = "approved"
	}

	comment := core.Comment{
		PostID:    post.ID,
		ParentID:  parentID,
		Name:      clipRunes(name, commentMaxName),
		Email:     email,
		Body:      body,
		BodyHTML:  renderCommentMarkdown(body),
		Status:    status,
		IP:        clipRunes(c.RealIP(), 64),
		UserAgent: clipRunes(c.Request().UserAgent(), 255),
	}
	if signedIn {
		comment.AuthorID = &user.ID
	}
	if err := data.DB.Create(&comment).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to save comment"})
	}
	if status == "pending" && OnCommentPending != nil {
		OnCommentPending(comment)
	}
	return c.JSON(http.StatusCreated, map[string]any{"id": comment.ID, "status": comment.Status})
}

// Admin moderation.

type adminPostRef struct {
	ID    uint   `json:"id"`
	Title string `json:"title"`
	Slug  string `json:"slug"`
}

type adminAuthorRef struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

// adminCommentRow is a comment with its email (hidden from the public JSON),
// the post it belongs to and the account that wrote it, if any.
type adminCommentRow struct {
	core.Comment
	Email  string          `json:"email"`
	Author *adminAuthorRef `json:"author,omitempty"`
	Post   adminPostRef    `json:"post"`
}

type adminCommentList struct {
	Items   []adminCommentRow `json:"items"`
	Total   int64             `json:"total"`
	Page    int               `json:"page"`
	PerPage int               `json:"per_page"`
	Counts  map[string]int64  `json:"counts"`
}

func commentPreviewPost(db *gorm.DB) *gorm.DB {
	return db.Unscoped().Select("id", "title", "slug")
}

func commentPreviewAuthor(db *gorm.DB) *gorm.DB {
	return db.Select("id", "username", "role")
}

func toAdminRow(c core.Comment) adminCommentRow {
	row := adminCommentRow{
		Comment: c,
		Email:   c.Email,
		Post:    adminPostRef{ID: c.Post.ID, Title: c.Post.Title, Slug: c.Post.Slug},
	}
	if c.Author != nil {
		row.Author = &adminAuthorRef{ID: c.Author.ID, Username: c.Author.Username, Role: c.Author.Role}
	}
	return row
}

// loadAdminComment reads one comment, trashed or not, for an admin response.
func loadAdminComment(id uint) (adminCommentRow, error) {
	var row core.Comment
	err := data.DB.Unscoped().
		Preload("Post", commentPreviewPost).
		Preload("Author", commentPreviewAuthor).
		Where("id = ?", id).
		First(&row).Error
	if err != nil {
		return adminCommentRow{}, err
	}
	return toAdminRow(row), nil
}

// applyCommentFilters applies the admin list filters. Trash lists soft-deleted
// rows only; every other status excludes them.
func applyCommentFilters(db *gorm.DB, status, q string, postID uint) *gorm.DB {
	query := db.Model(&core.Comment{})
	switch status {
	case "trash":
		query = query.Unscoped().Where("deleted_at IS NOT NULL")
	case "all":
	default:
		query = query.Where("status = ?", status)
	}
	if q != "" {
		pattern := "%" + likeEscaper.Replace(q) + "%"
		query = query.Where("(name ILIKE ? OR email ILIKE ? OR body ILIKE ?)", pattern, pattern, pattern)
	}
	if postID != 0 {
		query = query.Where("post_id = ?", postID)
	}
	return query
}

// commentCounts returns the tab counts. all excludes trash.
func commentCounts(db *gorm.DB) (map[string]int64, error) {
	counts := map[string]int64{"all": 0, "pending": 0, "approved": 0, "spam": 0, "trash": 0}
	var rows []struct {
		Status string
		Total  int64
	}
	if err := db.Model(&core.Comment{}).Select("status, COUNT(*) AS total").Group("status").Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		switch r.Status {
		case "pending", "approved", "spam":
			counts[r.Status] = r.Total
			counts["all"] += r.Total
		}
	}
	var trash int64
	if err := db.Unscoped().Model(&core.Comment{}).Where("deleted_at IS NOT NULL").Count(&trash).Error; err != nil {
		return nil, err
	}
	counts["trash"] = trash
	return counts, nil
}

func positiveQuery(raw string, fallback int) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return fallback
	}
	return n
}

func listAdminComments(c echo.Context) error {
	status := c.QueryParam("status")
	if status == "" {
		status = "all"
	}
	switch status {
	case "all", "pending", "approved", "spam", "trash":
	default:
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Unknown status"})
	}
	page := positiveQuery(c.QueryParam("page"), 1)
	perPage := positiveQuery(c.QueryParam("per_page"), commentDefaultPer)
	if perPage > commentMaxAdminPer {
		perPage = commentMaxAdminPer
	}
	var postID uint
	if raw := c.QueryParam("post_id"); raw != "" {
		id, ok := parseTermID(raw)
		if !ok {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid post_id"})
		}
		postID = id
	}
	q := strings.TrimSpace(c.QueryParam("q"))

	var total int64
	if err := applyCommentFilters(data.DB, status, q, postID).Count(&total).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load comments"})
	}
	var rows []core.Comment
	err := applyCommentFilters(data.DB, status, q, postID).
		Preload("Post", commentPreviewPost).
		Preload("Author", commentPreviewAuthor).
		Order("created_at DESC, id DESC").
		Offset((page - 1) * perPage).
		Limit(perPage).
		Find(&rows).Error
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load comments"})
	}
	counts, err := commentCounts(data.DB)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load comments"})
	}
	items := make([]adminCommentRow, 0, len(rows))
	for _, row := range rows {
		items = append(items, toAdminRow(row))
	}
	return c.JSON(http.StatusOK, adminCommentList{Items: items, Total: total, Page: page, PerPage: perPage, Counts: counts})
}

// commentAction moderates one comment. Trashed comments are only restorable.
func commentAction(action string) echo.HandlerFunc {
	return func(c echo.Context) error {
		user, err := currentUser(c)
		if err != nil {
			return err
		}
		id, ok := parseTermID(c.Param("id"))
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "Comment not found"})
		}
		var row core.Comment
		if err := data.DB.Unscoped().Where("id = ?", id).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return c.JSON(http.StatusNotFound, map[string]string{"error": "Comment not found"})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load comment"})
		}
		trashed := row.DeletedAt.Valid
		if action == "restore" && !trashed {
			return c.JSON(http.StatusConflict, map[string]string{"error": "Comment is not in trash"})
		}
		if action != "restore" && trashed {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "Comment not found"})
		}

		var result *gorm.DB
		switch action {
		case "approve", "unapprove", "spam":
			status := map[string]string{"approve": "approved", "unapprove": "pending", "spam": "spam"}[action]
			result = data.DB.Model(&core.Comment{}).Where("id = ?", id).Update("status", status)
		case "trash":
			deletedBy := user.ID
			result = data.DB.Model(&core.Comment{}).Where("id = ?", id).Updates(map[string]any{
				"deleted_at":    time.Now(),
				"deleted_by_id": deletedBy,
			})
		case "restore":
			result = data.DB.Unscoped().Model(&core.Comment{}).Where("id = ?", id).Updates(map[string]any{
				"deleted_at":    nil,
				"deleted_by_id": nil,
			})
		default:
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Unknown action"})
		}
		if result.Error != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to update comment"})
		}
		saved, err := loadAdminComment(id)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load comment"})
		}
		return c.JSON(http.StatusOK, saved)
	}
}

type bulkCommentInput struct {
	IDs    []uint `json:"ids"`
	Action string `json:"action"`
}

func bulkCommentAction(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	var in bulkCommentInput
	if err := decodeTermBody(c, &in); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}
	seen := make(map[uint]bool, len(in.IDs))
	ids := make([]uint, 0, len(in.IDs))
	for _, id := range in.IDs {
		if id != 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "ids is required"})
	}
	if len(ids) > commentMaxBulk {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Too many ids"})
	}

	var result *gorm.DB
	switch in.Action {
	case "approve", "unapprove", "spam":
		status := map[string]string{"approve": "approved", "unapprove": "pending", "spam": "spam"}[in.Action]
		result = data.DB.Model(&core.Comment{}).Where("id IN ?", ids).Update("status", status)
	case "trash":
		deletedBy := user.ID
		result = data.DB.Model(&core.Comment{}).Where("id IN ?", ids).Updates(map[string]any{
			"deleted_at":    time.Now(),
			"deleted_by_id": deletedBy,
		})
	case "restore":
		result = data.DB.Unscoped().Model(&core.Comment{}).
			Where("id IN ? AND deleted_at IS NOT NULL", ids).
			Updates(map[string]any{"deleted_at": nil, "deleted_by_id": nil})
	case "delete":
		// Permanent removal only applies to rows already in trash.
		result = data.DB.Unscoped().Where("id IN ? AND deleted_at IS NOT NULL", ids).Delete(&core.Comment{})
	default:
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Unknown action"})
	}
	if result.Error != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to update comments"})
	}
	return c.JSON(http.StatusOK, map[string]int64{"updated": result.RowsAffected})
}

func replyComment(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	id, ok := parseTermID(c.Param("id"))
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Comment not found"})
	}
	var parent core.Comment
	if err := data.DB.Where("id = ?", id).First(&parent).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "Comment not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load comment"})
	}
	var in struct {
		Body string `json:"body"`
	}
	if err := decodeTermBody(c, &in); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}
	body := strings.TrimSpace(in.Body)
	if !validCommentBody(body) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Comment must be 1 to 5000 characters"})
	}

	parentID := parent.ID
	authorID := user.ID
	reply := core.Comment{
		PostID:    parent.PostID,
		ParentID:  &parentID,
		AuthorID:  &authorID,
		Name:      clipRunes(user.Username, commentMaxName),
		Email:     user.Email,
		Body:      body,
		BodyHTML:  renderCommentMarkdown(body),
		Status:    "approved",
		IP:        clipRunes(c.RealIP(), 64),
		UserAgent: clipRunes(c.Request().UserAgent(), 255),
	}
	// A staff reply shows the thread, so a pending parent is approved in the
	// same transaction as the reply insert.
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&reply).Error; err != nil {
			return err
		}
		if parent.Status == "pending" {
			return tx.Model(&core.Comment{}).
				Where("id = ? AND status = ?", parent.ID, "pending").
				Update("status", "approved").Error
		}
		return nil
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to save reply"})
	}
	saved, err := loadAdminComment(reply.ID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load reply"})
	}
	return c.JSON(http.StatusCreated, saved)
}

// updateComment replaces the Markdown body of a comment and re-renders its
// HTML. Status, name, email and parent are kept. Trashed rows are not found.
func updateComment(c echo.Context) error {
	id, ok := parseTermID(c.Param("id"))
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Comment not found"})
	}
	var row core.Comment
	if err := data.DB.Unscoped().Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "Comment not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load comment"})
	}
	if row.DeletedAt.Valid {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Comment not found"})
	}
	var in struct {
		Body string `json:"body"`
	}
	if err := decodeTermBody(c, &in); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}
	body := strings.TrimSpace(in.Body)
	if !validCommentBody(body) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Comment must be 1 to 5000 characters"})
	}
	if err := data.DB.Model(&core.Comment{}).Where("id = ?", id).Updates(map[string]any{
		"body":      body,
		"body_html": renderCommentMarkdown(body),
	}).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to update comment"})
	}
	saved, err := loadAdminComment(id)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load comment"})
	}
	return c.JSON(http.StatusOK, saved)
}

// deleteCommentPermanently removes a comment that is already in trash.
func deleteCommentPermanently(c echo.Context) error {
	id, ok := parseTermID(c.Param("id"))
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Comment not found"})
	}
	var row core.Comment
	if err := data.DB.Unscoped().Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "Comment not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load comment"})
	}
	if !row.DeletedAt.Valid {
		return c.JSON(http.StatusConflict, map[string]string{"error": "Move the comment to trash before deleting it"})
	}
	if err := data.DB.Unscoped().Where("id = ? AND deleted_at IS NOT NULL", id).Delete(&core.Comment{}).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to delete comment"})
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "Deleted"})
}
