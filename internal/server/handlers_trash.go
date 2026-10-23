package server

import (
	"time"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// Recycle bin (slot C4a).
//
// Soft delete: core.Post, core.Product and core.Comment carry gorm.DeletedAt, so the existing
// DELETE handlers now move rows to the bin instead of dropping them. When a post or product is
// trashed its slug is rewritten to "<slug>~<id>" so a new item may reuse the slug; restoring puts the
// original slug back when it is free, otherwise keeps the suffixed one. DeletedByID records who trashed it.
//
//	GET    /api/admin/trash?kind=all|post|page|product|comment&q=   admin/editor see everything; authors see only their own posts
//	       → {items:[core.TrashItem], counts:{all,post,page,product,comment}, retention_days}
//	POST   /api/admin/trash/:kind/:id/restore                        → 200 {"message"}  (kind: post|page|product|comment)
//	DELETE /api/admin/trash/:kind/:id                                 permanent → 200
//	DELETE /api/admin/trash                                           empty everything visible to the caller → 200 {"deleted": n}
//	Unknown kind → 400, unknown id or not trashed → 404, author touching someone else's post → 404.
//
// Retention: option trash_retention_days (default core.TrashRetentionDays). PurgeExpiredTrash deletes
// rows whose DeletedAt + retention < now. cmd/server runs it at start and every 24h.
//
// Admin page: web/admin/trash.html per design/project/Trash.dc.html (retention notice, kind segments,
// table with 誰刪的/何時/剩餘, restore + permanent delete, confirm sheet that requires typing 刪除).

func registerTrashRoutes(manage, content *echo.Group) {
	content.GET("/trash", listTrash)
	content.POST("/trash/:kind/:id/restore", restoreTrash)
	content.DELETE("/trash/:kind/:id", deleteTrashItem)
	manage.DELETE("/trash", emptyTrash)
}

// PurgeExpiredTrash permanently deletes trashed rows older than the retention
// window and returns how many rows it removed.
func PurgeExpiredTrash(db *gorm.DB, now time.Time) (int64, error) { return 0, nil }

func listTrash(c echo.Context) error       { return notImplemented(c) }
func restoreTrash(c echo.Context) error    { return notImplemented(c) }
func deleteTrashItem(c echo.Context) error { return notImplemented(c) }
func emptyTrash(c echo.Context) error      { return notImplemented(c) }
