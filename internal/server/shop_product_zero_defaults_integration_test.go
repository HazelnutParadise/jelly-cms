package server

// AGENTS.md Follow-ups, first item: a product created with requires_shipping=false or low_stock_threshold=0
// is stored with the default instead. core.Product carries gorm default:true / default:5 tags, and GORM
// replaces a zero value of such a field with the tag's default on Create. Cases 1 and 4 (false / 0 through
// POST /api/admin/products and POST /api/admin/products/:id/duplicate) fail on the current code; cases 2,
// 3 and 5 pin behavior that must keep passing after the fix. They run through the real route table against
// the dedicated PostgreSQL database and are skipped unless JELLY_TEST_POSTGRES=1. Do not edit them to match
// an implementation.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

// zeroDefaultCreate posts a product body with the fields a product form sends (variants included) plus the
// extra fields of the case, and returns the decoded response.
func zeroDefaultCreate(t *testing.T, h *accessHarness, token, slug string, extra map[string]any) map[string]any {
	t.Helper()
	body := map[string]any{
		"title": "Zero default " + slug, "slug": slug, "description": "", "price": "100", "compare_at_price": "",
		"status": "draft", "images": []string{}, "categories": []uint{},
		"variants":  []map[string]any{{"name": "", "sku": "", "price": "100", "compare_at_price": "", "stock": 0, "position": 0, "status": "active"}},
		"seo_title": "", "seo_description": "", "seo_index": "inherit", "seo_ai": "inherit",
	}
	for k, v := range extra {
		body[k] = v
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal product body: %v", err)
	}
	return consistencyPost(t, h, token, http.MethodPost, "/api/admin/products", string(raw), http.StatusOK)
}

// zeroDefaultStored reads the product row straight from the database.
func zeroDefaultStored(t *testing.T, h *accessHarness, id uint) core.Product {
	t.Helper()
	var p core.Product
	if err := h.db.First(&p, id).Error; err != nil {
		t.Fatalf("read product %d from database: %v", id, err)
	}
	return p
}

// Case 1: requested false / 0 must be stored as false / 0, in the row and in the API response.
func TestZeroDefaultProductCreateKeepsFalseAndZero(t *testing.T) {
	h := archivesHarness(t)
	token := h.token(h.user("admin", "zero-default-create-false"))
	out := zeroDefaultCreate(t, h, token, "zero-default-create-false", map[string]any{"requires_shipping": false, "low_stock_threshold": 0})
	id := consistencyID(t, out, "product create")
	p := zeroDefaultStored(t, h, id)
	if p.RequiresShipping || p.LowStockThreshold != 0 {
		t.Errorf("POST /api/admin/products (adminProductsCreate) with requires_shipping=false, low_stock_threshold=0: database stored requires_shipping=%v low_stock_threshold=%d, want false and 0",
			p.RequiresShipping, p.LowStockThreshold)
	}
	if v, ok := out["requires_shipping"].(bool); !ok || v {
		t.Errorf("POST /api/admin/products response requires_shipping = %#v, want false", out["requires_shipping"])
	}
	if v, ok := out["low_stock_threshold"].(float64); !ok || v != 0 {
		t.Errorf("POST /api/admin/products response low_stock_threshold = %#v, want 0", out["low_stock_threshold"])
	}
}

// Case 2: omitted fields keep the current defaults, requires_shipping true and low_stock_threshold 5.
func TestZeroDefaultProductCreateOmittedUsesDefaults(t *testing.T) {
	h := archivesHarness(t)
	token := h.token(h.user("admin", "zero-default-create-omitted"))
	out := zeroDefaultCreate(t, h, token, "zero-default-create-omitted", nil)
	p := zeroDefaultStored(t, h, consistencyID(t, out, "product create"))
	if !p.RequiresShipping || p.LowStockThreshold != 5 {
		t.Errorf("POST /api/admin/products without requires_shipping and low_stock_threshold: database stored requires_shipping=%v low_stock_threshold=%d, want true and 5",
			p.RequiresShipping, p.LowStockThreshold)
	}
}

// Case 3: explicit true / 7 is stored as given.
func TestZeroDefaultProductCreateKeepsExplicitValues(t *testing.T) {
	h := archivesHarness(t)
	token := h.token(h.user("admin", "zero-default-create-explicit"))
	out := zeroDefaultCreate(t, h, token, "zero-default-create-explicit", map[string]any{"requires_shipping": true, "low_stock_threshold": 7})
	p := zeroDefaultStored(t, h, consistencyID(t, out, "product create"))
	if !p.RequiresShipping || p.LowStockThreshold != 7 {
		t.Errorf("POST /api/admin/products with requires_shipping=true, low_stock_threshold=7: database stored requires_shipping=%v low_stock_threshold=%d, want true and 7",
			p.RequiresShipping, p.LowStockThreshold)
	}
}

// Case 4: a duplicate of a product that holds false / 0 must hold false / 0. The source row is set directly
// in the database so this case isolates the duplicate path from the create path.
func TestZeroDefaultProductDuplicateKeepsFalseAndZero(t *testing.T) {
	h := archivesHarness(t)
	token := h.token(h.user("admin", "zero-default-duplicate-false"))
	src := consistencyID(t, zeroDefaultCreate(t, h, token, "zero-default-duplicate-source", nil), "product create")
	if err := h.db.Model(&core.Product{}).Where("id = ?", src).Updates(map[string]any{"requires_shipping": false, "low_stock_threshold": 0}).Error; err != nil {
		t.Fatalf("set source product %d to false / 0 in database: %v", src, err)
	}
	if p := zeroDefaultStored(t, h, src); p.RequiresShipping || p.LowStockThreshold != 0 {
		t.Fatalf("precondition: source product %d stored requires_shipping=%v low_stock_threshold=%d, want false and 0", src, p.RequiresShipping, p.LowStockThreshold)
	}
	out := consistencyPost(t, h, token, http.MethodPost, fmt.Sprintf("/api/admin/products/%d/duplicate", src), "", http.StatusOK)
	dup := zeroDefaultStored(t, h, consistencyID(t, out, "product duplicate"))
	if dup.ID == src {
		t.Fatalf("duplicate returned the source id %d", src)
	}
	if dup.RequiresShipping || dup.LowStockThreshold != 0 {
		t.Errorf("POST /api/admin/products/:id/duplicate of a product with requires_shipping=false, low_stock_threshold=0: copy stored requires_shipping=%v low_stock_threshold=%d, want false and 0 (adminProductsDuplicate Omit(...).Create)",
			dup.RequiresShipping, dup.LowStockThreshold)
	}
}

// Case 5: a duplicate of a product that holds true / 5 keeps true / 5.
func TestZeroDefaultProductDuplicateKeepsTrueAndFive(t *testing.T) {
	h := archivesHarness(t)
	token := h.token(h.user("admin", "zero-default-duplicate-true"))
	src := consistencyID(t, zeroDefaultCreate(t, h, token, "zero-default-duplicate-true-source", map[string]any{"requires_shipping": true, "low_stock_threshold": 5}), "product create")
	if p := zeroDefaultStored(t, h, src); !p.RequiresShipping || p.LowStockThreshold != 5 {
		t.Fatalf("precondition: source product %d stored requires_shipping=%v low_stock_threshold=%d, want true and 5", src, p.RequiresShipping, p.LowStockThreshold)
	}
	out := consistencyPost(t, h, token, http.MethodPost, fmt.Sprintf("/api/admin/products/%d/duplicate", src), "", http.StatusOK)
	dup := zeroDefaultStored(t, h, consistencyID(t, out, "product duplicate"))
	if !dup.RequiresShipping || dup.LowStockThreshold != 5 {
		t.Errorf("POST /api/admin/products/:id/duplicate of a product with requires_shipping=true, low_stock_threshold=5: copy stored requires_shipping=%v low_stock_threshold=%d, want true and 5",
			dup.RequiresShipping, dup.LowStockThreshold)
	}
}
