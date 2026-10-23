package server

import (
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// Revisions (slot C4b). Same visibility rules as the post itself (authors: own posts only).
//
//	GET  /api/admin/posts/:id/revisions                 → [{id, kind, user:{id,username}, created_at, added, removed}] newest first
//	GET  /api/admin/posts/:id/revisions/:rid            → full revision (title, content, excerpt, meta)
//	GET  /api/admin/posts/:id/revisions/diff?from=&to=  → {title:[DiffOp], content:[DiffOp]}; "to" may be "current"
//	POST /api/admin/posts/:id/revisions/:rid/restore    → 200 post; records a new "restore" revision first
//
// Recording (in handlers_posts.go, via RecordRevision):
//   - createPost: kind "manual" (or "publish" when status is published)
//   - updatePost: when title, content, excerpt or meta changed: kind "autosave" if input has "autosave": true,
//     "publish" if status moved to published, else "manual". Unchanged saves record nothing.
//   - keep only the newest core.RevisionAutosaveKeep autosaves per post; manual/publish/restore are kept forever.
//   - Added/Removed count changed lines of Content versus the previous revision (0/0 for the first).
//
// Admin page: web/admin/revisions.html per design/project/Revisions.dc.html (left list with avatars and +n −n,
// right side 逐行比較 / 只看 vN / 左右並排 segments, red/green inline diff, 還原到這個版本).

// DiffOp is one line of a diff: Op is "=", "+" or "-".
type DiffOp struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

func registerRevisionRoutes(content *echo.Group) {
	content.GET("/posts/:id/revisions", listRevisions)
	content.GET("/posts/:id/revisions/diff", diffRevisions)
	content.GET("/posts/:id/revisions/:rid", getRevision)
	content.POST("/posts/:id/revisions/:rid/restore", restoreRevision)
}

// RecordRevision stores a snapshot of post for user with kind and trims old
// autosaves. It returns the stored revision.
func RecordRevision(db *gorm.DB, post core.Post, userID uint, kind string) (*core.Revision, error) {
	return nil, nil
}

// LineDiff returns a line-based diff of a against b (LCS based; good enough for
// prose, never quadratic blow-up beyond 20k lines: fall back to all -/+ then).
func LineDiff(a, b string) []DiffOp { return nil }

func listRevisions(c echo.Context) error   { return notImplemented(c) }
func getRevision(c echo.Context) error     { return notImplemented(c) }
func diffRevisions(c echo.Context) error   { return notImplemented(c) }
func restoreRevision(c echo.Context) error { return notImplemented(c) }
