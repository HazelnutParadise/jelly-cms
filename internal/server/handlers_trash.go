package server

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/jackc/pgx/v5/pgconn"
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
//	Unknown kind → 400, unknown id or not trashed → 404, author touching someone else's post → 404,
//	permanent delete of a row other records still reference (a product on an order) → 409.
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

// trashKinds lists the bin kinds in display order.
var trashKinds = []string{"post", "page", "product", "comment"}

// trashExcerptRunes bounds the title shown for a trashed comment, which has no title.
const trashExcerptRunes = 80

// trashRow is one bin entry as read from its table.
type trashRow struct {
	ID          uint
	Title       string
	DeletedAt   time.Time
	DeletedByID *uint
}

// PurgeExpiredTrash permanently deletes trashed rows older than the retention
// window and returns how many rows it removed. A row that cannot be removed
// (for example a product still referenced by an order) is skipped and reported
// in the returned error; the rest are still purged.
func PurgeExpiredTrash(db *gorm.DB, now time.Time) (int64, error) {
	cutoff := now.Add(-time.Duration(trashRetentionDays(db)) * 24 * time.Hour)
	var removed int64
	var errs []error
	for _, kind := range trashKinds {
		var ids []uint
		if err := db.Unscoped().Table(trashTable(kind)).
			Where("deleted_at IS NOT NULL AND deleted_at < ?", cutoff).
			Pluck("id", &ids).Error; err != nil {
			return removed, err
		}
		for _, id := range ids {
			err := db.Transaction(func(tx *gorm.DB) error {
				return purgeTrashed(tx, nil, kind, id)
			})
			if err != nil {
				errs = append(errs, fmt.Errorf("purge %s %d: %w", kind, id, err))
				continue
			}
			removed++
		}
	}
	return removed, errors.Join(errs...)
}

// trashRetentionDays reads trash_retention_days, falling back to the default for
// a missing, non-numeric or non-positive value.
func trashRetentionDays(db *gorm.DB) int {
	var opts []core.Option
	if err := db.Where("key = ?", "trash_retention_days").Limit(1).Find(&opts).Error; err != nil || len(opts) == 0 {
		return core.TrashRetentionDays
	}
	days, err := strconv.Atoi(strings.TrimSpace(opts[0].Value))
	if err != nil || days < 1 {
		return core.TrashRetentionDays
	}
	return days
}

func parseTrashKind(value string) (string, bool) {
	for _, kind := range trashKinds {
		if value == kind {
			return kind, true
		}
	}
	return "", false
}

// trashVisibleKinds returns the kinds the caller may see: authors see only their own posts.
func trashVisibleKinds(user *core.User) []string {
	if user.Role == "author" {
		return []string{"post"}
	}
	return trashKinds
}

func trashAllowed(user *core.User, kind string) bool {
	for _, visible := range trashVisibleKinds(user) {
		if visible == kind {
			return true
		}
	}
	return false
}

func trashTable(kind string) string {
	switch kind {
	case "product":
		return "products"
	case "comment":
		return "comments"
	default:
		return "posts"
	}
}

func trashModel(kind string) any {
	switch kind {
	case "product":
		return &core.Product{}
	case "comment":
		return &core.Comment{}
	default:
		return &core.Post{}
	}
}

// trashListQuery selects the trashed rows of one kind that the caller may see.
// q filters by title (body for comments), case-insensitively.
func trashListQuery(db *gorm.DB, kind string, user *core.User, q string) *gorm.DB {
	query := db.Table(trashTable(kind)).Where("deleted_at IS NOT NULL")
	if kind == "post" || kind == "page" {
		query = query.Where("type = ?", kind)
	}
	if user.Role == "author" {
		query = query.Where("author_id = ?", user.ID)
	}
	if q != "" {
		column := "title"
		if kind == "comment" {
			column = "body"
		}
		query = query.Where(column+" ILIKE ?", "%"+escapeLike(q)+"%")
	}
	return query
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// excerpt returns the first trashExcerptRunes runes of s on one line.
func excerpt(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= trashExcerptRunes {
		return s
	}
	return string([]rune(s)[:trashExcerptRunes]) + "…"
}

// trashedRows selects the row of one kind with id that is in the bin and visible to user.
// A nil user applies no visibility rule (used by the purge).
func trashedRows(db *gorm.DB, kind string, user *core.User, id uint) *gorm.DB {
	query := db.Unscoped().Model(trashModel(kind)).Where("id = ? AND deleted_at IS NOT NULL", id)
	if kind == "post" || kind == "page" {
		query = query.Where("type = ?", kind)
	}
	if user != nil && user.Role == "author" {
		query = query.Where("author_id = ?", user.ID)
	}
	return query
}

// trashedSlug returns the slug a trashed row carries: "<slug>~<id>", with the base
// shortened so the result still fits the 255-character column.
func trashedSlug(slug string, id uint) string {
	suffix := fmt.Sprintf("~%d", id)
	base := []rune(slug)
	if limit := 255 - utf8.RuneCountInString(suffix); len(base) > limit {
		base = base[:limit]
	}
	return string(base) + suffix
}

// restoredSlug returns the original slug of a trashed row when no row of the same
// table uses it, otherwise the suffixed slug.
func restoredSlug(db *gorm.DB, model any, trashed string, id uint) (string, error) {
	original := strings.TrimSuffix(trashed, fmt.Sprintf("~%d", id))
	if original == trashed {
		return trashed, nil
	}
	var taken int64
	if err := db.Unscoped().Model(model).Where("slug = ?", original).Count(&taken).Error; err != nil {
		return "", err
	}
	if taken > 0 {
		return trashed, nil
	}
	return original, nil
}

// purgeTrashed permanently removes one trashed row and the rows that depend on it.
// The row must be in the bin and visible to user (nil user: no visibility rule).
func purgeTrashed(tx *gorm.DB, user *core.User, kind string, id uint) error {
	var count int64
	if err := trashedRows(tx, kind, user, id).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return gorm.ErrRecordNotFound
	}
	if kind == "post" || kind == "page" {
		post := core.Post{ID: id}
		if err := tx.Model(&post).Association("Categories").Clear(); err != nil {
			return err
		}
		if err := tx.Model(&post).Association("Tags").Clear(); err != nil {
			return err
		}
		if err := tx.Where("post_id = ?", id).Delete(&core.Revision{}).Error; err != nil {
			return err
		}
		// Comments reference posts by a foreign key, so they go with the post.
		if err := tx.Unscoped().Where("post_id = ?", id).Delete(&core.Comment{}).Error; err != nil {
			return err
		}
	}
	return tx.Unscoped().Where("id = ?", id).Delete(trashModel(kind)).Error
}

func listTrash(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	requested := c.QueryParam("kind")
	if requested != "" && requested != "all" {
		if _, ok := parseTrashKind(requested); !ok {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Unknown kind"})
		}
	}
	q := strings.TrimSpace(c.QueryParam("q"))

	counts := map[string]int{"all": 0}
	for _, kind := range trashKinds {
		counts[kind] = 0
	}
	for _, kind := range trashVisibleKinds(user) {
		var n int64
		if err := trashListQuery(data.DB, kind, user, "").Count(&n).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load trash"})
		}
		counts[kind] = int(n)
		counts["all"] += int(n)
	}

	selected := trashVisibleKinds(user)
	if requested != "" && requested != "all" {
		selected = nil
		if trashAllowed(user, requested) {
			selected = []string{requested}
		}
	}
	items, err := loadTrashItems(data.DB, user, selected, q, trashRetentionDays(data.DB))
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load trash"})
	}
	return c.JSON(http.StatusOK, map[string]any{
		"items":          items,
		"counts":         counts,
		"retention_days": trashRetentionDays(data.DB),
	})
}

// loadTrashItems returns the bin entries of the given kinds, newest deletion first.
func loadTrashItems(db *gorm.DB, user *core.User, kinds []string, q string, retention int) ([]core.TrashItem, error) {
	items := make([]core.TrashItem, 0)
	for _, kind := range kinds {
		column := "title"
		if kind == "comment" {
			column = "body AS title"
		}
		var rows []trashRow
		if err := trashListQuery(db, kind, user, q).
			Select("id, " + column + ", deleted_at, deleted_by_id").
			Order("deleted_at DESC, id DESC").
			Scan(&rows).Error; err != nil {
			return nil, err
		}
		names, err := trashUsernames(db, rows)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			item := core.TrashItem{
				Kind:      kind,
				ID:        row.ID,
				Title:     row.Title,
				DeletedAt: row.DeletedAt,
				ExpiresAt: row.DeletedAt.Add(time.Duration(retention) * 24 * time.Hour),
			}
			if kind == "comment" {
				item.Title = excerpt(row.Title)
			}
			if row.DeletedByID != nil {
				item.DeletedByID = *row.DeletedByID
				item.DeletedBy = names[*row.DeletedByID]
			}
			items = append(items, item)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].DeletedAt.Equal(items[j].DeletedAt) {
			return items[i].DeletedAt.After(items[j].DeletedAt)
		}
		return items[i].ID > items[j].ID
	})
	return items, nil
}

// trashUsernames maps the deleting users of rows to their usernames.
func trashUsernames(db *gorm.DB, rows []trashRow) (map[uint]string, error) {
	seen := make(map[uint]bool)
	var ids []uint
	for _, row := range rows {
		if row.DeletedByID != nil && !seen[*row.DeletedByID] {
			seen[*row.DeletedByID] = true
			ids = append(ids, *row.DeletedByID)
		}
	}
	names := make(map[uint]string, len(ids))
	if len(ids) == 0 {
		return names, nil
	}
	var users []struct {
		ID       uint
		Username string
	}
	if err := db.Table("users").Select("id, username").Where("id IN ?", ids).Scan(&users).Error; err != nil {
		return nil, err
	}
	for _, u := range users {
		names[u.ID] = u.Username
	}
	return names, nil
}

func restoreTrash(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	kind, ok := parseTrashKind(c.Param("kind"))
	if !ok {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Unknown kind"})
	}
	id, ok := parseTermID(c.Param("id"))
	if !ok || !trashAllowed(user, kind) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Item not found"})
	}
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{"deleted_at": nil, "deleted_by_id": nil}
		if kind != "comment" {
			var trashed string
			if kind == "product" {
				var product core.Product
				if err := trashedRows(tx, kind, user, id).Select("id, slug").Take(&product).Error; err != nil {
					return err
				}
				trashed = product.Slug
			} else {
				var post core.Post
				if err := trashedRows(tx, kind, user, id).Select("id, slug").Take(&post).Error; err != nil {
					return err
				}
				trashed = post.Slug
			}
			slug, err := restoredSlug(tx, trashModel(kind), trashed, id)
			if err != nil {
				return err
			}
			updates["slug"] = slug
		}
		result := trashedRows(tx, kind, user, id).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Item not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to restore item"})
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "Restored"})
}

func deleteTrashItem(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	kind, ok := parseTrashKind(c.Param("kind"))
	if !ok {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Unknown kind"})
	}
	id, ok := parseTermID(c.Param("id"))
	if !ok || !trashAllowed(user, kind) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Item not found"})
	}
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		return purgeTrashed(tx, user, kind, id)
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Item not found"})
	}
	if isForeignKeyViolation(err) {
		return c.JSON(http.StatusConflict, map[string]string{"error": "Item is still referenced by other records"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to delete item"})
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "Deleted"})
}

// isForeignKeyViolation reports a PostgreSQL foreign-key failure, for example a
// product that an order still references.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

func emptyTrash(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	deleted := 0
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		for _, kind := range trashVisibleKinds(user) {
			var ids []uint
			if err := trashListQuery(tx, kind, user, "").Pluck("id", &ids).Error; err != nil {
				return err
			}
			for _, id := range ids {
				if err := purgeTrashed(tx, user, kind, id); err != nil {
					return err
				}
				deleted++
			}
		}
		return nil
	})
	if isForeignKeyViolation(err) {
		return c.JSON(http.StatusConflict, map[string]string{"error": "An item is still referenced by other records"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to empty trash"})
	}
	return c.JSON(http.StatusOK, map[string]int{"deleted": deleted})
}
