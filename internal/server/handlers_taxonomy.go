package server

import (
	"net/http"

	"github.com/labstack/echo/v4"
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

func listCategories(c echo.Context) error    { return notImplemented(c) }
func createCategory(c echo.Context) error    { return notImplemented(c) }
func updateCategory(c echo.Context) error    { return notImplemented(c) }
func deleteCategory(c echo.Context) error    { return notImplemented(c) }
func reorderCategories(c echo.Context) error { return notImplemented(c) }
func listTags(c echo.Context) error          { return notImplemented(c) }
func createTag(c echo.Context) error         { return notImplemented(c) }
func updateTag(c echo.Context) error         { return notImplemented(c) }
func deleteTag(c echo.Context) error         { return notImplemented(c) }
