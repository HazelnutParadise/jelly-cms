package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/goccy/go-json"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// Taxonomy API (slot C1). JSON in and out; errors are {"error": "..."}.
//
//	GET    /api/admin/categories            all roles   flat list ordered by (parent, position); each row has post_count
//	POST   /api/admin/categories            admin/editor {name, slug?, description?, parent_id?, color?, cover?, hide_from_list?, no_cascade?, template?} → 201 category
//	PUT    /api/admin/categories/:id        admin/editor same fields, partial → 200 category
//	DELETE /api/admin/categories/:id        admin/editor posts keep existing; children move to the deleted node's parent → 200 {"message"}
//	POST   /api/admin/categories/reorder    admin/editor [{id, parent_id, position}] → 200 list
//	GET    /api/admin/tags?q=               all roles   list ordered by post_count desc then name; q filters by name prefix (case-insensitive); each row has post_count
//	POST   /api/admin/tags                  all roles   {name, slug?, color?, cover?, description?} → 201 tag (authors may create tags)
//	PUT    /api/admin/tags/:id              admin/editor → 200 tag
//	DELETE /api/admin/tags/:id              admin/editor → 200 {"message"}
//
// Rules:
//   - Slug: when empty, core.Slugify(name, "category"/"tag"); when taken, append -2, -3 … (never fail on a duplicate name).
//   - parent_id must exist and must not create a cycle (a node cannot be its own ancestor) → 400.
//   - Unknown id → 404. Invalid JSON or empty name → 400.
//   - Post input gains "category_ids" ([]uint), "tag_ids" ([]uint) and "tags" ([]string, created on the fly when missing)
//     — handled in handlers_posts.go by applyPostTerms; list/get responses preload Categories and Tags.
//
// Admin page: web/admin/taxonomy.html per design/project/Taxonomy.dc.html
// (segmented 分類/標籤, tree with indent, counts, row actions, right-side edit card with color flavors).

func registerTaxonomyRoutes(manage, content *echo.Group) {
	content.GET("/categories", listCategories)
	manage.POST("/categories", createCategory)
	manage.PUT("/categories/:id", updateCategory)
	manage.DELETE("/categories/:id", deleteCategory)
	manage.POST("/categories/reorder", reorderCategories)

	content.GET("/tags", listTags)
	content.POST("/tags", createTag)
	manage.PUT("/tags/:id", updateTag)
	manage.DELETE("/tags/:id", deleteTag)
}

func notImplemented(c echo.Context) error {
	return c.JSON(http.StatusNotImplemented, map[string]string{"error": "not implemented"})
}

// termError is a client error in a taxonomy or post-term write. Handlers
// answer it with 400 and its text; any other error is a server error.
type termError string

func (e termError) Error() string { return string(e) }

// termWriteError answers a failed write: a termError becomes 400 with its
// text, anything else becomes 500 with failure.
func termWriteError(c echo.Context, err error, failure string) error {
	var bad termError
	if errors.As(err, &bad) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": string(bad)})
	}
	return c.JSON(http.StatusInternalServerError, map[string]string{"error": failure})
}

func parseTermID(value string) (uint, bool) {
	id, err := strconv.ParseUint(value, 10, strconv.IntSize)
	if err != nil || id == 0 {
		return 0, false
	}
	return uint(id), true
}

// decodeTermBody reads one JSON value into dst and rejects trailing data.
func decodeTermBody(c echo.Context, dst any) error {
	defer c.Request().Body.Close()
	decoder := json.NewDecoder(c.Request().Body)
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("unexpected trailing data")
	}
	return nil
}

// termWrite is a decoded category or tag request. A nil field was absent
// from the request, so partial updates leave it alone. parentSet separates an
// absent parent_id from an explicit null, which moves the node to the root.
type termWrite struct {
	name, slug, description, color, cover, template *string
	hideFromList, noCascade                         *bool
	parentSet                                       bool
	parent                                          *uint
}

func decodeTermWrite(input map[string]json.RawMessage) (termWrite, error) {
	var w termWrite
	var err error
	if w.name, err = optionalField[string](input, "name"); err != nil {
		return w, err
	}
	if w.slug, err = optionalField[string](input, "slug"); err != nil {
		return w, err
	}
	if w.description, err = optionalField[string](input, "description"); err != nil {
		return w, err
	}
	if w.color, err = optionalField[string](input, "color"); err != nil {
		return w, err
	}
	if w.cover, err = optionalField[string](input, "cover"); err != nil {
		return w, err
	}
	if w.template, err = optionalField[string](input, "template"); err != nil {
		return w, err
	}
	if w.hideFromList, err = optionalField[bool](input, "hide_from_list"); err != nil {
		return w, err
	}
	if w.noCascade, err = optionalField[bool](input, "no_cascade"); err != nil {
		return w, err
	}
	parent, err := optionalField[*uint](input, "parent_id")
	if err != nil {
		return w, err
	}
	if parent != nil {
		w.parentSet = true
		w.parent = *parent
	}
	if w.name != nil {
		trimmed := strings.TrimSpace(*w.name)
		if trimmed == "" {
			return w, termError("name is required")
		}
		if utf8.RuneCountInString(trimmed) > 120 {
			return w, termError("name is too long")
		}
		w.name = &trimmed
	}
	return w, nil
}

// optionalField decodes input[key] when the key is present. A nil result
// means the key was absent.
func optionalField[T any](input map[string]json.RawMessage, key string) (*T, error) {
	raw, ok := input[key]
	if !ok {
		return nil, nil
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, termError(key + " has an invalid value")
	}
	return &value, nil
}

func strValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func boolValue(p *bool) bool {
	return p != nil && *p
}

func parentValue(p *uint) any {
	if p == nil {
		return nil
	}
	return *p
}

// termSlugBase is the slug a term asks for before uniqueness is checked: the
// requested slug when given, else the name, both run through core.Slugify.
func termSlugBase(requested, name, fallback string) string {
	return core.Slugify(requested, core.Slugify(name, fallback))
}

// uniqueTermSlug returns base, or base-2, base-3 … for the first value that no
// other row of model holds. exceptID is the row being updated, 0 for a new row.
func uniqueTermSlug(db *gorm.DB, model any, base string, exceptID uint) (string, error) {
	slug := base
	for n := 2; ; n++ {
		taken, err := termSlugTaken(db, model, slug, exceptID)
		if err != nil {
			return "", err
		}
		if !taken {
			return slug, nil
		}
		slug = fmt.Sprintf("%s-%d", base, n)
	}
}

func termSlugTaken(db *gorm.DB, model any, slug string, exceptID uint) (bool, error) {
	query := db.Model(model).Where("slug = ?", slug)
	if exceptID != 0 {
		query = query.Where("id <> ?", exceptID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// termPostCounts counts the non-deleted posts per term. joinTable is the
// many-to-many table (post_categories or post_tags) and termColumn its term
// key column.
func termPostCounts(db *gorm.DB, joinTable, termColumn string) (map[uint]int64, error) {
	type termCount struct {
		TermID uint  `gorm:"column:term_id"`
		Total  int64 `gorm:"column:total"`
	}
	var rows []termCount
	err := db.Table(joinTable + " AS j").
		Select("j." + termColumn + " AS term_id, COUNT(*) AS total").
		Joins("JOIN posts AS p ON p.id = j.post_id AND p.deleted_at IS NULL").
		Group("j." + termColumn).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	counts := make(map[uint]int64, len(rows))
	for _, row := range rows {
		counts[row.TermID] = row.Total
	}
	return counts, nil
}

// categoryParents maps every category id to its parent id.
func categoryParents(db *gorm.DB) (map[uint]*uint, error) {
	var rows []core.Category
	if err := db.Select("id", "parent_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	parents := make(map[uint]*uint, len(rows))
	for _, row := range rows {
		parents[row.ID] = row.ParentID
	}
	return parents, nil
}

// checkCategoryParent rejects a parent that does not exist or that would make
// category id its own ancestor. The walk follows parents, which for a batch
// already holds the proposed parents. A new category passes id 0.
func checkCategoryParent(parents map[uint]*uint, id uint, parent *uint) error {
	seen := map[uint]bool{id: true}
	for parent != nil {
		pid := *parent
		if _, ok := parents[pid]; !ok {
			return termError("parent_id does not exist")
		}
		if seen[pid] {
			return termError("a category cannot be its own ancestor")
		}
		seen[pid] = true
		parent = parents[pid]
	}
	return nil
}

// nextCategoryPosition returns the position after the last sibling under parent.
func nextCategoryPosition(db *gorm.DB, parent *uint) (int, error) {
	query := db.Model(&core.Category{}).Order("position DESC")
	if parent == nil {
		query = query.Where("parent_id IS NULL")
	} else {
		query = query.Where("parent_id = ?", *parent)
	}
	var last []core.Category
	if err := query.Select("position").Limit(1).Find(&last).Error; err != nil {
		return 0, err
	}
	if len(last) == 0 {
		return 0, nil
	}
	return last[0].Position + 1, nil
}

func loadCategoryRows(db *gorm.DB) ([]core.Category, error) {
	rows := make([]core.Category, 0)
	if err := db.Order("parent_id ASC NULLS FIRST, position ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	counts, err := termPostCounts(db, "post_categories", "category_id")
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].PostCount = counts[rows[i].ID]
	}
	return rows, nil
}

func findCategory(db *gorm.DB, id uint) (core.Category, error) {
	var row core.Category
	if err := db.First(&row, id).Error; err != nil {
		return row, err
	}
	counts, err := termPostCounts(db, "post_categories", "category_id")
	if err != nil {
		return row, err
	}
	row.PostCount = counts[id]
	return row, nil
}

func findTag(db *gorm.DB, id uint) (core.Tag, error) {
	var row core.Tag
	if err := db.First(&row, id).Error; err != nil {
		return row, err
	}
	counts, err := termPostCounts(db, "post_tags", "tag_id")
	if err != nil {
		return row, err
	}
	row.PostCount = counts[id]
	return row, nil
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func listCategories(c echo.Context) error {
	rows, err := loadCategoryRows(data.DB)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load categories"})
	}
	return c.JSON(http.StatusOK, rows)
}

func createCategory(c echo.Context) error {
	input, err := decodePostInput(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}
	w, err := decodeTermWrite(input)
	if err != nil {
		return termWriteError(c, err, "Unable to create category")
	}
	if w.name == nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "name is required"})
	}
	parents, err := categoryParents(data.DB)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to create category"})
	}
	if err := checkCategoryParent(parents, 0, w.parent); err != nil {
		return termWriteError(c, err, "Unable to create category")
	}
	position, err := nextCategoryPosition(data.DB, w.parent)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to create category"})
	}
	slug, err := uniqueTermSlug(data.DB, &core.Category{}, termSlugBase(strValue(w.slug), *w.name, "category"), 0)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to create category"})
	}
	row := core.Category{
		Name:         *w.name,
		Slug:         slug,
		Description:  strValue(w.description),
		ParentID:     w.parent,
		Color:        strValue(w.color),
		Cover:        strValue(w.cover),
		HideFromList: boolValue(w.hideFromList),
		NoCascade:    boolValue(w.noCascade),
		Template:     strValue(w.template),
		Position:     position,
	}
	if err := data.DB.Create(&row).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to create category"})
	}
	saved, err := findCategory(data.DB, row.ID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load category"})
	}
	return c.JSON(http.StatusCreated, saved)
}

func updateCategory(c echo.Context) error {
	id, ok := parseTermID(c.Param("id"))
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Category not found"})
	}
	input, err := decodePostInput(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}
	w, err := decodeTermWrite(input)
	if err != nil {
		return termWriteError(c, err, "Unable to update category")
	}
	var existing core.Category
	if err := data.DB.First(&existing, id).Error; err != nil {
		return categoryNotFoundOr(c, err, "Unable to load category")
	}
	parents, err := categoryParents(data.DB)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to update category"})
	}
	updates := map[string]any{"updated_at": time.Now()}
	if w.name != nil {
		updates["name"] = *w.name
	}
	if w.slug != nil {
		name := existing.Name
		if w.name != nil {
			name = *w.name
		}
		slug, err := uniqueTermSlug(data.DB, &core.Category{}, termSlugBase(*w.slug, name, "category"), id)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to update category"})
		}
		updates["slug"] = slug
	}
	if w.parentSet {
		if err := checkCategoryParent(parents, id, w.parent); err != nil {
			return termWriteError(c, err, "Unable to update category")
		}
		updates["parent_id"] = parentValue(w.parent)
	}
	if w.description != nil {
		updates["description"] = *w.description
	}
	if w.color != nil {
		updates["color"] = *w.color
	}
	if w.cover != nil {
		updates["cover"] = *w.cover
	}
	if w.template != nil {
		updates["template"] = *w.template
	}
	if w.hideFromList != nil {
		updates["hide_from_list"] = *w.hideFromList
	}
	if w.noCascade != nil {
		updates["no_cascade"] = *w.noCascade
	}
	if err := data.DB.Model(&core.Category{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to update category"})
	}
	saved, err := findCategory(data.DB, id)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load category"})
	}
	return c.JSON(http.StatusOK, saved)
}

func deleteCategory(c echo.Context) error {
	id, ok := parseTermID(c.Param("id"))
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Category not found"})
	}
	var existing core.Category
	if err := data.DB.First(&existing, id).Error; err != nil {
		return categoryNotFoundOr(c, err, "Unable to load category")
	}
	err := data.DB.Transaction(func(tx *gorm.DB) error {
		var children []core.Category
		if err := tx.Where("parent_id = ?", id).Order("position, id").Find(&children).Error; err != nil {
			return err
		}
		next, err := nextCategoryPosition(tx, existing.ParentID)
		if err != nil {
			return err
		}
		for _, child := range children {
			moved := map[string]any{
				"parent_id":  parentValue(existing.ParentID),
				"position":   next,
				"updated_at": time.Now(),
			}
			if err := tx.Model(&core.Category{}).Where("id = ?", child.ID).Updates(moved).Error; err != nil {
				return err
			}
			next++
		}
		if err := tx.Exec("DELETE FROM post_categories WHERE category_id = ?", id).Error; err != nil {
			return err
		}
		return tx.Delete(&core.Category{}, id).Error
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to delete category"})
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "Deleted"})
}

// categoryPlacement is one row of the reorder request.
type categoryPlacement struct {
	ID       uint  `json:"id"`
	ParentID *uint `json:"parent_id"`
	Position int   `json:"position"`
}

func reorderCategories(c echo.Context) error {
	var placements []categoryPlacement
	if err := decodeTermBody(c, &placements); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}
	parents, err := categoryParents(data.DB)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to reorder categories"})
	}
	// Apply every proposed parent first so the cycle check sees the whole batch.
	seen := make(map[uint]bool, len(placements))
	for _, p := range placements {
		if _, ok := parents[p.ID]; !ok {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "category does not exist"})
		}
		if seen[p.ID] {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "category listed more than once"})
		}
		seen[p.ID] = true
		parents[p.ID] = p.ParentID
	}
	for _, p := range placements {
		if err := checkCategoryParent(parents, p.ID, p.ParentID); err != nil {
			return termWriteError(c, err, "Unable to reorder categories")
		}
	}
	now := time.Now()
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		for _, p := range placements {
			placed := map[string]any{
				"parent_id":  parentValue(p.ParentID),
				"position":   p.Position,
				"updated_at": now,
			}
			if err := tx.Model(&core.Category{}).Where("id = ?", p.ID).Updates(placed).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to reorder categories"})
	}
	rows, err := loadCategoryRows(data.DB)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load categories"})
	}
	return c.JSON(http.StatusOK, rows)
}

func listTags(c echo.Context) error {
	query := data.DB.Model(&core.Tag{})
	if q := c.QueryParam("q"); q != "" {
		query = query.Where("name ILIKE ?", likeEscaper.Replace(q)+"%")
	}
	tags := make([]core.Tag, 0)
	if err := query.Find(&tags).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load tags"})
	}
	counts, err := termPostCounts(data.DB, "post_tags", "tag_id")
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load tags"})
	}
	for i := range tags {
		tags[i].PostCount = counts[tags[i].ID]
	}
	sort.SliceStable(tags, func(i, j int) bool {
		if tags[i].PostCount != tags[j].PostCount {
			return tags[i].PostCount > tags[j].PostCount
		}
		return tags[i].Name < tags[j].Name
	})
	return c.JSON(http.StatusOK, tags)
}

func createTag(c echo.Context) error {
	input, err := decodePostInput(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}
	w, err := decodeTermWrite(input)
	if err != nil {
		return termWriteError(c, err, "Unable to create tag")
	}
	if w.name == nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "name is required"})
	}
	slug, err := uniqueTermSlug(data.DB, &core.Tag{}, termSlugBase(strValue(w.slug), *w.name, "tag"), 0)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to create tag"})
	}
	row := core.Tag{
		Name:        *w.name,
		Slug:        slug,
		Color:       strValue(w.color),
		Cover:       strValue(w.cover),
		Description: strValue(w.description),
	}
	if err := data.DB.Create(&row).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to create tag"})
	}
	saved, err := findTag(data.DB, row.ID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load tag"})
	}
	return c.JSON(http.StatusCreated, saved)
}

func updateTag(c echo.Context) error {
	id, ok := parseTermID(c.Param("id"))
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Tag not found"})
	}
	input, err := decodePostInput(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}
	w, err := decodeTermWrite(input)
	if err != nil {
		return termWriteError(c, err, "Unable to update tag")
	}
	var existing core.Tag
	if err := data.DB.First(&existing, id).Error; err != nil {
		return tagNotFoundOr(c, err, "Unable to load tag")
	}
	updates := map[string]any{"updated_at": time.Now()}
	if w.name != nil {
		updates["name"] = *w.name
	}
	if w.slug != nil {
		name := existing.Name
		if w.name != nil {
			name = *w.name
		}
		slug, err := uniqueTermSlug(data.DB, &core.Tag{}, termSlugBase(*w.slug, name, "tag"), id)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to update tag"})
		}
		updates["slug"] = slug
	}
	if w.description != nil {
		updates["description"] = *w.description
	}
	if w.color != nil {
		updates["color"] = *w.color
	}
	if w.cover != nil {
		updates["cover"] = *w.cover
	}
	if err := data.DB.Model(&core.Tag{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to update tag"})
	}
	saved, err := findTag(data.DB, id)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load tag"})
	}
	return c.JSON(http.StatusOK, saved)
}

func deleteTag(c echo.Context) error {
	id, ok := parseTermID(c.Param("id"))
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Tag not found"})
	}
	var existing core.Tag
	if err := data.DB.First(&existing, id).Error; err != nil {
		return tagNotFoundOr(c, err, "Unable to load tag")
	}
	err := data.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DELETE FROM post_tags WHERE tag_id = ?", id).Error; err != nil {
			return err
		}
		return tx.Delete(&core.Tag{}, id).Error
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to delete tag"})
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "Deleted"})
}

func categoryNotFoundOr(c echo.Context, err error, failure string) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Category not found"})
	}
	return c.JSON(http.StatusInternalServerError, map[string]string{"error": failure})
}

func tagNotFoundOr(c echo.Context, err error, failure string) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Tag not found"})
	}
	return c.JSON(http.StatusInternalServerError, map[string]string{"error": failure})
}

// findOrCreateTag returns the tag with exactly this name, creating it when
// none exists. Post term input uses it so names can be typed freely.
func findOrCreateTag(db *gorm.DB, name string) (core.Tag, error) {
	var tag core.Tag
	err := db.Where("name = ?", name).Order("id").First(&tag).Error
	if err == nil {
		return tag, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return tag, err
	}
	slug, err := uniqueTermSlug(db, &core.Tag{}, termSlugBase("", name, "tag"), 0)
	if err != nil {
		return tag, err
	}
	tag = core.Tag{Name: name, Slug: slug}
	return tag, db.Create(&tag).Error
}
