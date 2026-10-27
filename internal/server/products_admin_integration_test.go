package server

// Admin product catalog integration tests for ticket 07, section E2 (contract: 管理 API in
// docs/tickets/07-shop.md). They run through the real route table against the dedicated PostgreSQL
// database and are skipped unless JELLY_TEST_POSTGRES=1. They are written before the implementation and
// must fail until it exists: the current /api/admin/products handlers return an unfiltered array, and the
// product-category, duplicate and variant routes do not exist yet. Do not edit them to match an
// implementation.
//
// Input convention: admin money fields are typed in whole NT$ as strings ("120"), and JSON responses carry
// minor units (12000). Create responses may be 200 or 201; the tests accept any 2xx for writes.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
)

type productsAdminVariant struct {
	ID             uint   `json:"id"`
	Name           string `json:"name"`
	SKU            string `json:"sku"`
	Price          int64  `json:"price"`
	CompareAtPrice int64  `json:"compare_at_price"`
	Stock          int    `json:"stock"`
	Status         string `json:"status"`
}

type productsAdminProduct struct {
	ID             uint                   `json:"id"`
	Title          string                 `json:"title"`
	Slug           string                 `json:"slug"`
	Status         string                 `json:"status"`
	Price          int64                  `json:"price"`
	CompareAtPrice int64                  `json:"compare_at_price"`
	Stock          int                    `json:"stock"`
	Variants       []productsAdminVariant `json:"variants"`
	Categories     json.RawMessage        `json:"categories"`
	DeletedAt      json.RawMessage        `json:"deleted_at"`
}

type productsAdminListPage struct {
	Items  []productsAdminProduct `json:"items"`
	Total  int                    `json:"total"`
	Counts map[string]int         `json:"counts"`
}

func productsAdminBody(t *testing.T, fields any) string {
	t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode request body: %v", err)
	}
	return string(raw)
}

// productsAdminDo sends a JSON request. A nil body sends no payload.
func productsAdminDo(t *testing.T, h *accessHarness, method, path string, body any, token string) accessResult {
	t.Helper()
	payload, contentType := "", ""
	if body != nil {
		payload, contentType = productsAdminBody(t, body), echo.MIMEApplicationJSON
	}
	r := h.request(method, path, payload, token, echo.MIMEApplicationJSON, contentType)
	h.mustNoPanic(r, method+" "+path)
	return r
}

func productsAdminWantStatus(t *testing.T, r accessResult, where string, want int) {
	t.Helper()
	if r.code != want {
		t.Fatalf("%s returned %d, want %d; body=%s", where, r.code, want, r.body)
	}
}

func productsAdminWantSuccess(t *testing.T, r accessResult, where string) {
	t.Helper()
	if r.code < 200 || r.code > 299 {
		t.Fatalf("%s returned %d, want 2xx; body=%s", where, r.code, r.body)
	}
}

func productsAdminDecode(t *testing.T, r accessResult, where string, into any) {
	t.Helper()
	if err := json.Unmarshal([]byte(r.body), into); err != nil {
		t.Fatalf("%s: decode JSON: %v; body=%s", where, err, r.body)
	}
}

// productsAdminCategoryIDs accepts a categories value as either [7, 8] or [{"id":7}, {"id":8}].
func productsAdminCategoryIDs(raw json.RawMessage) []uint {
	var ids []uint
	if json.Unmarshal(raw, &ids) == nil {
		return ids
	}
	var objects []struct {
		ID uint `json:"id"`
	}
	if json.Unmarshal(raw, &objects) == nil {
		for _, object := range objects {
			ids = append(ids, object.ID)
		}
	}
	return ids
}

func productsAdminIsNull(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
}

func productsAdminTitles(page productsAdminListPage) map[string]bool {
	titles := make(map[string]bool, len(page.Items))
	for _, item := range page.Items {
		titles[item.Title] = true
	}
	return titles
}

// productsAdminWantTitles checks that the page holds exactly the wanted titles.
func productsAdminWantTitles(t *testing.T, where string, page productsAdminListPage, want ...string) {
	t.Helper()
	got := productsAdminTitles(page)
	if len(got) != len(want) {
		t.Errorf("%s returned %d items %v, want %d %v", where, len(got), got, len(want), want)
	}
	for _, title := range want {
		if !got[title] {
			t.Errorf("%s is missing %q; got %v", where, title, got)
		}
	}
}

// productsAdminSeed inserts a product with one default variant mirroring its price and stock.
func productsAdminSeed(t *testing.T, h *accessHarness, title, status string, stock int, price int64) core.Product {
	t.Helper()
	product := core.Product{
		Title:  title,
		Slug:   strings.ToLower(strings.ReplaceAll(title, " ", "-")),
		Status: status,
		Price:  price,
		Stock:  stock,
	}
	if err := h.db.Create(&product).Error; err != nil {
		t.Fatalf("seed product %q: %v", title, err)
	}
	variant := core.ProductVariant{ProductID: product.ID, Name: "", Price: price, Stock: stock, Status: "active"}
	if err := h.db.Create(&variant).Error; err != nil {
		t.Fatalf("seed default variant for %q: %v", title, err)
	}
	return product
}

// productsAdminTreeFind returns the first object named name anywhere in a decoded JSON value.
func productsAdminTreeFind(node any, name string) map[string]any {
	switch value := node.(type) {
	case []any:
		for _, child := range value {
			if found := productsAdminTreeFind(child, name); found != nil {
				return found
			}
		}
	case map[string]any:
		if value["name"] == name {
			return value
		}
		for _, child := range value {
			if found := productsAdminTreeFind(child, name); found != nil {
				return found
			}
		}
	}
	return nil
}

func productsAdminNodeHasChild(node map[string]any, name string) bool {
	children, _ := node["children"].([]any)
	for _, child := range children {
		if object, ok := child.(map[string]any); ok && object["name"] == name {
			return true
		}
	}
	return false
}

func productsAdminVariantByName(variants []productsAdminVariant, name string) (productsAdminVariant, bool) {
	for _, variant := range variants {
		if variant.Name == name {
			return variant, true
		}
	}
	return productsAdminVariant{}, false
}

func TestProductsAdminListFiltersCountsAndPaging(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "pa-list-admin")
	token := h.token(admin)

	productsAdminSeed(t, h, "Active Lamp", "active", 10, 12000)
	productsAdminSeed(t, h, "Empty Shelf", "active", 0, 8000)
	productsAdminSeed(t, h, "Draft Vase", "draft", 3, 5000)
	productsAdminSeed(t, h, "Archived Clock", "archived", 4, 30000)
	trashed := productsAdminSeed(t, h, "Trashed Bowl", "active", 2, 2000)
	if err := h.db.Delete(&trashed).Error; err != nil {
		t.Fatalf("trash product: %v", err)
	}

	list := func(query string) productsAdminListPage {
		t.Helper()
		r := productsAdminDo(t, h, http.MethodGet, "/api/admin/products"+query, nil, token)
		productsAdminWantStatus(t, r, "GET /api/admin/products"+query, http.StatusOK)
		var page productsAdminListPage
		productsAdminDecode(t, r, "GET /api/admin/products"+query, &page)
		return page
	}

	all := list("")
	productsAdminWantTitles(t, "unfiltered list", all, "Active Lamp", "Empty Shelf", "Draft Vase", "Archived Clock")
	wantCounts := map[string]int{"all": 4, "active": 2, "draft": 1, "out_of_stock": 1, "archived": 1}
	for key, want := range wantCounts {
		if got := all.Counts[key]; got != want {
			t.Errorf("counts[%q] = %d, want %d; counts=%v", key, got, want, all.Counts)
		}
	}

	productsAdminWantTitles(t, "status=draft", list("?status=draft"), "Draft Vase")
	productsAdminWantTitles(t, "status=archived", list("?status=archived"), "Archived Clock")
	productsAdminWantTitles(t, "status=active", list("?status=active"), "Active Lamp", "Empty Shelf")
	productsAdminWantTitles(t, "q=Lamp", list("?q=Lamp"), "Active Lamp")
	productsAdminWantTitles(t, "q=Trashed (trashed rows are hidden)", list("?q=Trashed"))

	// Paging: 100 more products bring the total to 104. per_page is capped at 100.
	for i := 1; i <= 100; i++ {
		productsAdminSeed(t, h, fmt.Sprintf("Paging Item %03d", i), "active", 1, 1000)
	}
	capped := list("?per_page=1000")
	if len(capped.Items) > 100 {
		t.Errorf("per_page=1000 returned %d items, want at most 100", len(capped.Items))
	}
	if capped.Total != 104 {
		t.Errorf("total = %d, want 104", capped.Total)
	}

	first := list("?per_page=7&page=1")
	second := list("?per_page=7&page=2")
	if len(first.Items) != 7 || len(second.Items) != 7 {
		t.Fatalf("per_page=7 pages returned %d and %d items, want 7 and 7", len(first.Items), len(second.Items))
	}
	seen := map[uint]bool{}
	for _, item := range first.Items {
		seen[item.ID] = true
	}
	for _, item := range second.Items {
		if seen[item.ID] {
			t.Errorf("page 2 repeats product %d from page 1", item.ID)
		}
	}
}

func TestProductsAdminTypedInputIgnoresIDAndDeletedAt(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "pa-typed-admin")
	token := h.token(admin)
	const foreignID = 987654

	r := productsAdminDo(t, h, http.MethodPost, "/api/admin/products", map[string]any{
		"id":         foreignID,
		"title":      "Typed Input Lamp",
		"slug":       "typed-input-lamp",
		"status":     "draft",
		"price":      "150",
		"deleted_at": "2020-01-01T00:00:00Z",
		"variants":   []map[string]any{{"name": "", "price": "150", "stock": 3, "status": "active"}},
	}, token)
	productsAdminWantSuccess(t, r, "POST typed product")
	var created productsAdminProduct
	productsAdminDecode(t, r, "POST typed product", &created)
	if created.ID == 0 || created.ID == foreignID {
		t.Fatalf("created product id = %d, want a server-assigned id (not %d)", created.ID, foreignID)
	}
	if !productsAdminIsNull(created.DeletedAt) {
		t.Errorf("created product deleted_at = %s, want null", created.DeletedAt)
	}
	var row core.Product
	if err := h.db.Unscoped().First(&row, created.ID).Error; err != nil {
		t.Fatalf("read created product: %v", err)
	}
	if row.DeletedAt.Valid {
		t.Error("created product was stored as deleted; deleted_at from the request must be ignored")
	}
	if len(created.Variants) == 0 {
		t.Fatalf("created product has no variants; body=%s", r.body)
	}

	r = productsAdminDo(t, h, http.MethodPut, fmt.Sprintf("/api/admin/products/%d", created.ID), map[string]any{
		"id":         foreignID,
		"title":      "Typed Input Lamp Renamed",
		"slug":       "typed-input-lamp",
		"status":     "active",
		"price":      "150",
		"deleted_at": "2020-01-01T00:00:00Z",
		"variants":   []map[string]any{{"id": created.Variants[0].ID, "name": "", "price": "150", "stock": 3, "status": "active"}},
	}, token)
	productsAdminWantSuccess(t, r, "PUT typed product")
	var updated productsAdminProduct
	productsAdminDecode(t, r, "PUT typed product", &updated)
	if updated.ID != created.ID {
		t.Errorf("PUT changed id to %d, want %d", updated.ID, created.ID)
	}
	if err := h.db.Unscoped().First(&row, created.ID).Error; err != nil {
		t.Fatalf("read updated product: %v", err)
	}
	if row.DeletedAt.Valid {
		t.Error("PUT with deleted_at in the body trashed the product; deleted_at must be ignored")
	}

	listed := productsAdminDo(t, h, http.MethodGet, "/api/admin/products?q=Renamed", nil, token)
	productsAdminWantStatus(t, listed, "list after PUT", http.StatusOK)
	var page productsAdminListPage
	productsAdminDecode(t, listed, "list after PUT", &page)
	productsAdminWantTitles(t, "list after PUT", page, "Typed Input Lamp Renamed")
}

func TestProductsAdminRejectsNegativePriceAndStock(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "pa-negative-admin")
	token := h.token(admin)
	existing := productsAdminSeed(t, h, "Negative Guard Mug", "active", 4, 12000)
	var variantID uint
	if err := h.db.Raw("SELECT id FROM product_variants WHERE product_id = ?", existing.ID).Row().Scan(&variantID); err != nil {
		t.Fatalf("read seeded variant id: %v", err)
	}
	updatePath := fmt.Sprintf("/api/admin/products/%d", existing.ID)

	// Positive controls: the same request shapes with valid values must succeed. Without them, a binder
	// that rejects every string price would make the 400 checks below pass for the wrong reason.
	control := productsAdminDo(t, h, http.MethodPost, "/api/admin/products", map[string]any{
		"title": "Valid Control Mug", "price": "120",
		"variants": []map[string]any{{"name": "", "price": "120", "stock": 1}},
	}, token)
	productsAdminWantSuccess(t, control, "positive control create")
	productsAdminWantSuccess(t, productsAdminDo(t, h, http.MethodPut, updatePath, map[string]any{
		"title": "Negative Guard Mug", "price": "120",
		"variants": []map[string]any{{"id": variantID, "name": "", "price": "120", "stock": 4}},
	}, token), "positive control update")

	var before int64
	if err := h.db.Unscoped().Model(&core.Product{}).Count(&before).Error; err != nil {
		t.Fatalf("count products: %v", err)
	}

	cases := []struct {
		name   string
		method string
		path   string
		body   map[string]any
	}{
		{"create with price -1", http.MethodPost, "/api/admin/products", map[string]any{
			"title": "Negative Price", "price": "-1",
			"variants": []map[string]any{{"name": "", "price": "-1", "stock": 1}},
		}},
		{"create with variant stock -1", http.MethodPost, "/api/admin/products", map[string]any{
			"title": "Negative Stock", "price": "100",
			"variants": []map[string]any{{"name": "", "price": "100", "stock": -1}},
		}},
		{"create with variant price -5", http.MethodPost, "/api/admin/products", map[string]any{
			"title": "Negative Variant Price", "price": "100",
			"variants": []map[string]any{{"name": "", "price": "-5", "stock": 1}},
		}},
		{"update with variant stock -1", http.MethodPut, updatePath, map[string]any{
			"title": "Negative Guard Mug", "price": "120",
			"variants": []map[string]any{{"id": variantID, "name": "", "price": "120", "stock": -1}},
		}},
	}
	for _, tc := range cases {
		r := productsAdminDo(t, h, tc.method, tc.path, tc.body, token)
		productsAdminWantStatus(t, r, tc.name, http.StatusBadRequest)
	}

	var after int64
	if err := h.db.Unscoped().Model(&core.Product{}).Count(&after).Error; err != nil {
		t.Fatalf("count products after rejections: %v", err)
	}
	if after != before {
		t.Errorf("rejected requests changed the product count from %d to %d", before, after)
	}
	var stock int
	if err := h.db.Raw("SELECT stock FROM product_variants WHERE id = ?", variantID).Row().Scan(&stock); err != nil {
		t.Fatalf("read variant stock: %v", err)
	}
	if stock != 4 {
		t.Errorf("variant stock after rejected update = %d, want 4", stock)
	}
}

func TestProductsAdminVariantsCreateUpdateDelete(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "pa-variant-admin")
	token := h.token(admin)

	r := productsAdminDo(t, h, http.MethodPost, "/api/admin/products", map[string]any{
		"title":  "Variant Mug",
		"status": "active",
		"variants": []map[string]any{
			{"name": "Red", "sku": "RED-1", "price": "120", "compare_at_price": "150", "stock": 5, "position": 0, "status": "active"},
			{"name": "Blue", "sku": "BLUE-1", "price": "130", "stock": 0, "position": 1, "status": "active"},
		},
	}, token)
	productsAdminWantSuccess(t, r, "POST product with variants")
	var created productsAdminProduct
	productsAdminDecode(t, r, "POST product with variants", &created)
	if len(created.Variants) != 2 {
		t.Fatalf("created variants = %d, want 2; body=%s", len(created.Variants), r.body)
	}
	red, ok := productsAdminVariantByName(created.Variants, "Red")
	if !ok {
		t.Fatalf("Red variant missing from create response: %+v", created.Variants)
	}
	if red.Price != 12000 || red.CompareAtPrice != 15000 || red.Stock != 5 || red.SKU != "RED-1" {
		t.Errorf("Red variant = %+v, want price 12000, compare_at_price 15000, stock 5, sku RED-1", red)
	}
	blue, ok := productsAdminVariantByName(created.Variants, "Blue")
	if !ok || blue.Price != 13000 || blue.Stock != 0 {
		t.Errorf("Blue variant = %+v, want price 13000 and stock 0", blue)
	}

	// Round trip: the stored variants come back on a read.
	get := productsAdminDo(t, h, http.MethodGet, fmt.Sprintf("/api/admin/products/%d", created.ID), nil, token)
	productsAdminWantStatus(t, get, "GET product", http.StatusOK)
	var read productsAdminProduct
	productsAdminDecode(t, get, "GET product", &read)
	if len(read.Variants) != 2 {
		t.Errorf("read variants = %d, want 2", len(read.Variants))
	}

	// Update: keep Red with new price and stock, leave Blue out so it is removed.
	r = productsAdminDo(t, h, http.MethodPut, fmt.Sprintf("/api/admin/products/%d", created.ID), map[string]any{
		"title":    "Variant Mug",
		"status":   "active",
		"variants": []map[string]any{{"id": red.ID, "name": "Red", "price": "125", "stock": 9, "status": "active"}},
	}, token)
	productsAdminWantSuccess(t, r, "PUT variants")
	var updated productsAdminProduct
	productsAdminDecode(t, r, "PUT variants", &updated)
	if len(updated.Variants) != 1 {
		t.Fatalf("updated variants = %d, want 1 (Blue removed); body=%s", len(updated.Variants), r.body)
	}
	if updated.Variants[0].ID != red.ID || updated.Variants[0].Price != 12500 || updated.Variants[0].Stock != 9 {
		t.Errorf("updated Red = %+v, want id %d, price 12500, stock 9", updated.Variants[0], red.ID)
	}
	var stored int64
	if err := h.db.Model(&core.ProductVariant{}).Where("product_id = ?", created.ID).Count(&stored).Error; err != nil {
		t.Fatalf("count stored variants: %v", err)
	}
	if stored != 1 {
		t.Errorf("stored variants = %d, want 1", stored)
	}

	// Deleting the last remaining variant is refused.
	r = productsAdminDo(t, h, http.MethodPut, fmt.Sprintf("/api/admin/products/%d", created.ID), map[string]any{
		"title":    "Variant Mug",
		"status":   "active",
		"variants": []map[string]any{},
	}, token)
	productsAdminWantStatus(t, r, "PUT removing the last variant", http.StatusConflict)
	if err := h.db.Model(&core.ProductVariant{}).Where("product_id = ?", created.ID).Count(&stored).Error; err != nil {
		t.Fatalf("count variants after refused delete: %v", err)
	}
	if stored != 1 {
		t.Errorf("stored variants after refused delete = %d, want 1", stored)
	}
}

func TestProductsAdminCategoriesCRUDAndAssignment(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "pa-category-admin")
	token := h.token(admin)

	r := productsAdminDo(t, h, http.MethodPost, "/api/admin/product-categories", map[string]any{
		"name": "Kitchen Ware", "slug": "kitchen-ware",
	}, token)
	productsAdminWantSuccess(t, r, "create parent product category")
	var parent struct {
		ID uint `json:"id"`
	}
	productsAdminDecode(t, r, "create parent product category", &parent)

	r = productsAdminDo(t, h, http.MethodPost, "/api/admin/product-categories", map[string]any{
		"name": "Tea Cups", "slug": "tea-cups", "parent_id": parent.ID,
	}, token)
	productsAdminWantSuccess(t, r, "create child product category")
	var child struct {
		ID uint `json:"id"`
	}
	productsAdminDecode(t, r, "create child product category", &child)
	if child.ID == 0 || parent.ID == 0 {
		t.Fatalf("category ids = parent %d child %d, want both assigned", parent.ID, child.ID)
	}

	tree := func(where string) any {
		t.Helper()
		r := productsAdminDo(t, h, http.MethodGet, "/api/admin/product-categories", nil, token)
		productsAdminWantStatus(t, r, where, http.StatusOK)
		var decoded any
		productsAdminDecode(t, r, where, &decoded)
		return decoded
	}
	kitchen := productsAdminTreeFind(tree("GET product-categories"), "Kitchen Ware")
	if kitchen == nil {
		t.Fatal("tree does not contain Kitchen Ware")
	}
	if !productsAdminNodeHasChild(kitchen, "Tea Cups") {
		t.Errorf("Kitchen Ware children do not include Tea Cups: %v", kitchen["children"])
	}

	r = productsAdminDo(t, h, http.MethodPut, fmt.Sprintf("/api/admin/product-categories/%d", child.ID), map[string]any{
		"name": "Tea Sets",
	}, token)
	productsAdminWantSuccess(t, r, "rename child product category")
	kitchen = productsAdminTreeFind(tree("GET product-categories after rename"), "Kitchen Ware")
	if kitchen == nil || !productsAdminNodeHasChild(kitchen, "Tea Sets") {
		t.Errorf("after rename, Kitchen Ware children = %v, want Tea Sets", kitchen)
	}

	// Assign the category to a product, read it back, then clear the assignment.
	r = productsAdminDo(t, h, http.MethodPost, "/api/admin/products", map[string]any{
		"title": "Assigned Teapot", "price": "300", "categories": []uint{child.ID},
	}, token)
	productsAdminWantSuccess(t, r, "create product with categories")
	var assigned productsAdminProduct
	productsAdminDecode(t, r, "create product with categories", &assigned)
	if ids := productsAdminCategoryIDs(assigned.Categories); len(ids) != 1 || ids[0] != child.ID {
		t.Errorf("created product categories = %v, want [%d]", ids, child.ID)
	}
	r = productsAdminDo(t, h, http.MethodPut, fmt.Sprintf("/api/admin/products/%d", assigned.ID), map[string]any{
		"title": "Assigned Teapot", "price": "300", "categories": []uint{},
	}, token)
	productsAdminWantSuccess(t, r, "clear product categories")
	get := productsAdminDo(t, h, http.MethodGet, fmt.Sprintf("/api/admin/products/%d", assigned.ID), nil, token)
	productsAdminWantStatus(t, get, "GET product after clearing categories", http.StatusOK)
	var cleared productsAdminProduct
	productsAdminDecode(t, get, "GET product after clearing categories", &cleared)
	if ids := productsAdminCategoryIDs(cleared.Categories); len(ids) != 0 {
		t.Errorf("categories after clearing = %v, want none", ids)
	}

	// Product categories are separate from post categories.
	r = productsAdminDo(t, h, http.MethodPost, "/api/admin/product-categories", map[string]any{
		"name": "Garden Tools", "slug": "garden-tools",
	}, token)
	productsAdminWantSuccess(t, r, "create second product category")
	var garden struct {
		ID uint `json:"id"`
	}
	productsAdminDecode(t, r, "create second product category", &garden)

	r = productsAdminDo(t, h, http.MethodPost, "/api/admin/categories", map[string]any{
		"name": "Post Only Notes", "slug": "post-only-notes",
	}, token)
	productsAdminWantSuccess(t, r, "create post category")
	posts := productsAdminDo(t, h, http.MethodGet, "/api/admin/categories", nil, token)
	productsAdminWantStatus(t, posts, "GET post categories", http.StatusOK)
	if !strings.Contains(posts.body, "Post Only Notes") {
		t.Fatalf("post category did not appear in GET /api/admin/categories; body=%s", posts.body)
	}
	if strings.Contains(posts.body, "Garden Tools") || strings.Contains(posts.body, "Tea Sets") {
		t.Errorf("product categories leaked into GET /api/admin/categories; body=%s", posts.body)
	}

	r = productsAdminDo(t, h, http.MethodDelete, fmt.Sprintf("/api/admin/product-categories/%d", garden.ID), nil, token)
	productsAdminWantSuccess(t, r, "delete product category")
	if productsAdminTreeFind(tree("GET product-categories after delete"), "Garden Tools") != nil {
		t.Error("deleted product category Garden Tools is still in the tree")
	}
}

func TestProductsAdminDuplicate(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "pa-duplicate-admin")
	token := h.token(admin)
	source := productsAdminSeed(t, h, "Copy Source", "active", 4, 12000)

	r := productsAdminDo(t, h, http.MethodPost, fmt.Sprintf("/api/admin/products/%d/duplicate", source.ID), nil, token)
	productsAdminWantSuccess(t, r, "duplicate product")
	var copied productsAdminProduct
	productsAdminDecode(t, r, "duplicate product", &copied)
	if copied.ID == source.ID {
		t.Fatalf("duplicate returned the source id %d", source.ID)
	}
	if copied.Slug != "copy-source-2" {
		t.Errorf("duplicate slug = %q, want copy-source-2", copied.Slug)
	}
	if copied.Status != "draft" {
		t.Errorf("duplicate status = %q, want draft", copied.Status)
	}
	if len(copied.Variants) != 1 {
		t.Errorf("duplicate variants = %d, want the source's 1", len(copied.Variants))
	}

	var original core.Product
	if err := h.db.First(&original, source.ID).Error; err != nil {
		t.Fatalf("read source after duplicate: %v", err)
	}
	if original.Slug != "copy-source" || original.Status != "active" {
		t.Errorf("source changed by duplicate: slug=%q status=%q", original.Slug, original.Status)
	}
}

func TestProductsAdminRoleGate(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "pa-gate-admin")
	editor := h.user("editor", "pa-gate-editor")
	author := h.user("author", "pa-gate-author")
	product := productsAdminSeed(t, h, "Gate Product", "active", 2, 4000)
	category := core.ProductCategory{Name: "Gate Category", Slug: "gate-category"}
	if err := h.db.Create(&category).Error; err != nil {
		t.Fatalf("seed product category: %v", err)
	}

	productPath := fmt.Sprintf("/api/admin/products/%d", product.ID)
	categoryPath := fmt.Sprintf("/api/admin/product-categories/%d", category.ID)
	denied := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"list products", http.MethodGet, "/api/admin/products", nil},
		{"create product", http.MethodPost, "/api/admin/products", map[string]any{"title": "Blocked", "price": "10"}},
		{"update product", http.MethodPut, productPath, map[string]any{"title": "Blocked", "price": "10"}},
		{"delete product", http.MethodDelete, productPath, nil},
		{"duplicate product", http.MethodPost, productPath + "/duplicate", nil},
		{"list product categories", http.MethodGet, "/api/admin/product-categories", nil},
		{"create product category", http.MethodPost, "/api/admin/product-categories", map[string]any{"name": "Blocked", "slug": "blocked"}},
		{"update product category", http.MethodPut, categoryPath, map[string]any{"name": "Blocked"}},
		{"delete product category", http.MethodDelete, categoryPath, nil},
	}
	for _, user := range []struct {
		label string
		user  core.User
	}{{"editor", editor}, {"author", author}} {
		token := h.token(user.user)
		for _, tc := range denied {
			r := productsAdminDo(t, h, tc.method, tc.path, tc.body, token)
			productsAdminWantStatus(t, r, user.label+" "+tc.name, http.StatusForbidden)
		}
	}

	var stillThere core.Product
	if err := h.db.First(&stillThere, product.ID).Error; err != nil {
		t.Fatalf("denied delete removed the product: %v", err)
	}
	if stillThere.Title != "Gate Product" {
		t.Errorf("denied update changed title to %q", stillThere.Title)
	}

	adminToken := h.token(admin)
	productsAdminWantStatus(t, productsAdminDo(t, h, http.MethodGet, "/api/admin/products", nil, adminToken), "admin list products", http.StatusOK)
	productsAdminWantStatus(t, productsAdminDo(t, h, http.MethodGet, "/api/admin/product-categories", nil, adminToken), "admin list product categories", http.StatusOK)
	r := productsAdminDo(t, h, http.MethodPost, "/api/admin/products", map[string]any{"title": "Admin Created", "price": "10"}, adminToken)
	productsAdminWantSuccess(t, r, "admin create product")
}

func TestProductsAdminPriceRoundTripIsMinorUnits(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "pa-price-admin")
	token := h.token(admin)

	r := productsAdminDo(t, h, http.MethodPost, "/api/admin/products", map[string]any{
		"title": "Round Trip Mug", "price": "120",
	}, token)
	productsAdminWantSuccess(t, r, "create priced product")
	var created productsAdminProduct
	productsAdminDecode(t, r, "create priced product", &created)
	if created.Price != 12000 {
		t.Errorf("created price in JSON = %d, want 12000 (NT$120 in minor units)", created.Price)
	}

	r = productsAdminDo(t, h, http.MethodPut, fmt.Sprintf("/api/admin/products/%d", created.ID), map[string]any{
		"title": "Round Trip Mug", "price": "200",
	}, token)
	productsAdminWantSuccess(t, r, "update priced product")

	listed := productsAdminDo(t, h, http.MethodGet, "/api/admin/products?q=Round", nil, token)
	productsAdminWantStatus(t, listed, "list priced product", http.StatusOK)
	var page productsAdminListPage
	productsAdminDecode(t, listed, "list priced product", &page)
	if len(page.Items) != 1 || page.Items[0].Price != 20000 {
		t.Errorf("listed price = %+v, want one item with price 20000", page.Items)
	}

	var stored core.Product
	if err := h.db.First(&stored, created.ID).Error; err != nil {
		t.Fatalf("read stored product: %v", err)
	}
	if stored.Price != 20000 {
		t.Errorf("stored price = %d, want 20000", stored.Price)
	}
}
