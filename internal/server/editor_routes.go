package server

import (
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/markup"
	"github.com/labstack/echo/v4"
)

// Editor API (ticket 09). JSON in and out; errors are {"error": "..."}.
//
//	POST   /api/admin/render                          admin/editor/author {source, post_id?, mode: fragment|page, sourcemap, expand}
//	                                                  → {html, toc, stats, features, diagnostics, blocks, renderer}; never writes; 2 MB; per-user rate limit
//	GET    /api/admin/render/schema                   admin/editor/author → blocks, attributes, enumerations, slash-menu items
//	POST   /api/admin/posts/:id/autosave              owner or editor {title, source, excerpt} → writes an autosave revision only
//	GET    /api/admin/posts/:id/export?format=jmd|md|html|pdf&images=1&frontmatter=1&history=1
//	                                                  owner or editor; 404 for another author's post, 400 bad format, 501 pdf_engine_unavailable
//	GET    /api/admin/widgets                         admin → registry (built-in, custom, from plugins), drafts and published versions
//	POST   /api/admin/widgets                         admin → create a custom widget (HTML, CSS, JS, parameter declarations)
//	PUT    /api/admin/widgets/:id                     admin → update a draft; runs the automatic publish checks
//	DELETE /api/admin/widgets/:id                     admin
//	POST   /api/admin/widgets/:id/publish             admin → runs the publish checks, then publishes a version
//	GET    /widgets/:name/:v/frame?…                  public → sandboxed widget frame with the CSP from the ticket; parameters checked against the declaration
//
// Every route here answers 501 through notImplemented until its slice of
// docs/tickets/09-editor.md is built. The post-write changes (source-based
// saves, autosave rules and the export rules) belong to the handlers in
// handlers_posts.go, not to this file.
//
// Role rules: render and schema use the content group (admin, editor, author);
// autosave and export use the content group too and check ownership inside the
// handler; widgets use the admin-only group; the frame is public.

// registerEditorRoutes registers the editor endpoints. content is the group
// for admin, editor and author; api is the admin-only group.
func registerEditorRoutes(e *echo.Echo, content, api *echo.Group) {
	content.POST("/render", notImplemented)
	content.GET("/render/schema", notImplemented)
	content.POST("/posts/:id/autosave", notImplemented)
	content.GET("/posts/:id/export", notImplemented)

	api.GET("/widgets", notImplemented)
	api.POST("/widgets", notImplemented)
	api.PUT("/widgets/:id", notImplemented)
	api.DELETE("/widgets/:id", notImplemented)
	api.POST("/widgets/:id/publish", notImplemented)

	e.GET("/widgets/:name/:v/frame", notImplemented)
}

// renderTrustFor returns the sanitizer policy for a user's content. Admins and
// editors may use the rich policy (allow-listed html blocks); every other role,
// including author, gets strict. A nil user gets strict.
func renderTrustFor(user *core.User) string {
	if user != nil && (user.Role == "admin" || user.Role == "editor") {
		return markup.TrustRich
	}
	return markup.TrustStrict
}
