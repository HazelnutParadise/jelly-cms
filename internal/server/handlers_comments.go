package server

import (
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
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
//	POST /api/admin/comments/:id/reply     {body} → 201 comment (author = current user, status approved, parent = :id, post = parent's post)
//	DELETE /api/admin/comments/:id         permanent; only allowed when the row is in trash → 200, otherwise 409
//
// Theme: post pages receive "Comments" ([]core.Comment tree as in GET), "CommentCount" (int) and "CommentsOpen" (bool);
// commentsForPost(postID) builds that data and is used by handlers_public.go.
// Template partial web/themes/default/comments.html ({{define "comments"}}) renders list + form per
// design/project/Archive.dc.html (留言區): form with name/email/body, pending notice for the author's own
// unapproved comment is not required in this slot.
//
// Admin page: web/admin/comments.html per design/project/Comments.dc.html (status segments with counts, bulk bar,
// row actions approve/reply/edit/spam/trash, J/K keyboard, reply sheet).

// OnCommentPending is called after a pending comment is stored. Nil by default.
var OnCommentPending func(comment core.Comment)

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
	manage.DELETE("/comments/:id", deleteCommentPermanently)
}

// pendingCommentCount feeds the rail badge; 0 when the database is missing.
func pendingCommentCount(c echo.Context) int { return 0 }

// commentsForPost returns the approved tree, its count and whether the form
// should be shown for the post.
func commentsForPost(post core.Post, signedIn bool) ([]core.Comment, int, bool) {
	return nil, 0, false
}

// renderCommentMarkdown converts comment Markdown to sanitized HTML.
func renderCommentMarkdown(body string) string { return "" }

func listPublicComments(c echo.Context) error       { return notImplemented(c) }
func createPublicComment(c echo.Context) error      { return notImplemented(c) }
func listAdminComments(c echo.Context) error        { return notImplemented(c) }
func commentAction(action string) echo.HandlerFunc  { return notImplemented }
func bulkCommentAction(c echo.Context) error        { return notImplemented(c) }
func replyComment(c echo.Context) error             { return notImplemented(c) }
func deleteCommentPermanently(c echo.Context) error { return notImplemented(c) }
