package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

func TestAdminProductsLegacyPriceAndInventoryCompatibility(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "legacy-products-admin")
	token := h.token(admin)

	created := productsAdminDo(t, h, http.MethodPost, "/api/admin/products", map[string]any{
		"title": "Legacy price product", "status": "active", "price": json.RawMessage("1999"), "sku": "LEGACY-1", "stock": 3,
	}, token)
	productsAdminWantStatus(t, created, "create legacy product", http.StatusOK)
	var product productsAdminProduct
	productsAdminDecode(t, created, "create legacy product", &product)
	if product.Price != 1999 || product.Stock != 3 || len(product.Variants) != 1 {
		t.Fatalf("legacy create response price=%d stock=%d variants=%d; want 1999 minor units, stock 3 and one default variant", product.Price, product.Stock, len(product.Variants))
	}
	variant := product.Variants[0]
	if variant.Price != 1999 || variant.SKU != "LEGACY-1" || variant.Stock != 3 {
		t.Fatalf("synthesized default variant = %+v; want price 1999, sku LEGACY-1, stock 3", variant)
	}
	var stored core.Product
	if err := h.db.First(&stored, product.ID).Error; err != nil {
		t.Fatalf("read created legacy product: %v", err)
	}
	if stored.Price != 1999 || stored.Stock != 3 || stored.SKU != "LEGACY-1" {
		t.Errorf("stored legacy product price=%d stock=%d sku=%q; want 1999, 3, LEGACY-1", stored.Price, stored.Stock, stored.SKU)
	}

	path := fmt.Sprintf("/api/admin/products/%d", product.ID)
	priceUpdate := productsAdminDo(t, h, http.MethodPut, path, map[string]any{
		"title": "Legacy price product", "status": "active", "price": json.RawMessage("2499"),
	}, token)
	productsAdminWantStatus(t, priceUpdate, "legacy price update omitting stock", http.StatusOK)
	var updated productsAdminProduct
	productsAdminDecode(t, priceUpdate, "legacy price update omitting stock", &updated)
	if updated.Price != 2499 || updated.Stock != 3 || updated.Variants[0].Price != 2499 || updated.Variants[0].Stock != 3 {
		t.Errorf("price-only legacy update returned product=%+v; want price 2499 and existing stock 3", updated)
	}

	browser := newShopBrowser(h)
	shopAddToCart(t, browser, variant.ID, 1)
	cart := shopCartGet(t, browser)
	if line, ok := cart.line(variant.ID); !ok || line.UnitPrice != 2499 || line.LineTotal != 2499 || cart.Subtotal != 2499 {
		t.Errorf("cart after legacy price update has line=%+v found=%v subtotal=%d; want current price and subtotal 2499", line, ok, cart.Subtotal)
	}

	zeroStock := productsAdminDo(t, h, http.MethodPut, path, map[string]any{
		"title": "Legacy price product", "status": "active", "price": json.RawMessage("2499"), "sku": "", "stock": 0,
	}, token)
	productsAdminWantStatus(t, zeroStock, "legacy update clearing sku and setting stock to zero", http.StatusOK)
	productsAdminDecode(t, zeroStock, "legacy update clearing sku and setting stock to zero", &updated)
	if updated.Stock != 0 || len(updated.Variants) != 1 || updated.Variants[0].SKU != "" || updated.Variants[0].Stock != 0 {
		t.Errorf("legacy zero-stock update returned product=%+v; want empty SKU and stock 0", updated)
	}

	omittedStock := productsAdminDo(t, h, http.MethodPut, path, map[string]any{
		"title": "Legacy product after omitted stock", "status": "active", "price": json.RawMessage("2499"),
	}, token)
	productsAdminWantStatus(t, omittedStock, "legacy update omitting stock after zero", http.StatusOK)
	productsAdminDecode(t, omittedStock, "legacy update omitting stock after zero", &updated)
	if updated.Stock != 0 || len(updated.Variants) != 1 || updated.Variants[0].Stock != 0 {
		t.Errorf("legacy update without stock changed inventory: product stock=%d variants=%+v; want 0", updated.Stock, updated.Variants)
	}
	var adjustments []core.StockMovement
	if err := h.db.Where("variant_id = ? AND reason = ?", variant.ID, "adjust").Order("id").Find(&adjustments).Error; err != nil {
		t.Fatalf("read legacy inventory adjustments: %v", err)
	}
	if len(adjustments) != 2 || adjustments[0].Delta != 3 || adjustments[1].Delta != -3 {
		t.Errorf("legacy stock adjustment ledger = %+v; want one initial +3 and one -3 adjustment, without another movement for omitted stock", adjustments)
	}
}

func TestAdminProductsExplicitVariantsOverrideLegacyTopLevelSKUAndStock(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "legacy-variant-precedence-admin")
	token := h.token(admin)

	created := productsAdminDo(t, h, http.MethodPost, "/api/admin/products", map[string]any{
		"title": "Explicit variant wins", "status": "active", "price": json.RawMessage("4999"),
		"compare_at_price": "59.99", "sku": "TOP-LEVEL-SKU", "stock": 8,
		"variants": []map[string]any{{"name": "Default", "sku": "VARIANT-SKU", "price": "19.99", "compare_at_price": "25.99", "stock": 2, "status": "active"}},
	}, token)
	productsAdminWantStatus(t, created, "create with explicit variant and legacy top-level inventory", http.StatusOK)
	var product productsAdminProduct
	productsAdminDecode(t, created, "create with explicit variant and legacy top-level inventory", &product)
	if product.Price != 4999 || product.CompareAtPrice != 5999 || product.Stock != 2 || len(product.Variants) != 1 {
		t.Fatalf("product = %+v; want legacy integer price 4999, string compare-at 5999, explicit stock 2", product)
	}
	variant := product.Variants[0]
	if variant.SKU != "VARIANT-SKU" || variant.Price != 1999 || variant.CompareAtPrice != 2599 || variant.Stock != 2 {
		t.Errorf("explicit variant = %+v; want its SKU, stock, and dollar-string prices to win", variant)
	}
}

func TestAdminProductsLegacyTopLevelInventoryRejectsMultiVariantUpdate(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "legacy-multivariant-admin")
	token := h.token(admin)

	created := productsAdminDo(t, h, http.MethodPost, "/api/admin/products", map[string]any{
		"title": "Multi-variant legacy guard", "status": "active", "price": "10",
		"variants": []map[string]any{
			{"name": "Small", "sku": "SMALL", "price": "10", "stock": 4, "status": "active"},
			{"name": "Large", "sku": "LARGE", "price": "12", "stock": 6, "status": "active"},
		},
	}, token)
	productsAdminWantStatus(t, created, "create multi-variant product", http.StatusOK)
	var product productsAdminProduct
	productsAdminDecode(t, created, "create multi-variant product", &product)
	if len(product.Variants) != 2 {
		t.Fatalf("created variants = %d, want 2", len(product.Variants))
	}
	path := fmt.Sprintf("/api/admin/products/%d", product.ID)
	var beforeProduct core.Product
	if err := h.db.First(&beforeProduct, product.ID).Error; err != nil {
		t.Fatalf("load product before rejected legacy update: %v", err)
	}
	var beforeVariants []core.ProductVariant
	if err := h.db.Where("product_id = ?", product.ID).Order("id").Find(&beforeVariants).Error; err != nil {
		t.Fatalf("load variants before rejected legacy update: %v", err)
	}

	rejected := productsAdminDo(t, h, http.MethodPut, path, map[string]any{
		"title": "Multi-variant legacy guard changed", "status": "active", "price": "99", "sku": "LEGACY-SKU", "stock": 1,
	}, token)
	productsAdminWantStatus(t, rejected, "legacy top-level SKU/stock on multi-variant product", http.StatusConflict)
	var afterProduct core.Product
	if err := h.db.First(&afterProduct, product.ID).Error; err != nil {
		t.Fatalf("load product after rejected legacy update: %v", err)
	}
	var afterVariants []core.ProductVariant
	if err := h.db.Where("product_id = ?", product.ID).Order("id").Find(&afterVariants).Error; err != nil {
		t.Fatalf("load variants after rejected legacy update: %v", err)
	}
	if !reflect.DeepEqual(afterProduct, beforeProduct) || !reflect.DeepEqual(afterVariants, beforeVariants) {
		t.Errorf("rejected legacy update changed product/variant state: before product=%+v variants=%+v, after product=%+v variants=%+v", beforeProduct, beforeVariants, afterProduct, afterVariants)
	}
}

func TestAdminProductsPricesAboveJSSafeIntegerRemainExact(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "legacy-large-price-admin")
	token := h.token(admin)
	const want int64 = 9007199254740993
	cases := []struct {
		name  string
		price any
	}{
		{name: "legacy integer cents", price: json.RawMessage("9007199254740993")},
		{name: "dollar string", price: "90071992547409.93"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			created := productsAdminDo(t, h, http.MethodPost, "/api/admin/products", map[string]any{
				"title": fmt.Sprintf("Large exact price %d", i), "status": "active", "price": tc.price,
			}, token)
			productsAdminWantStatus(t, created, tc.name, http.StatusOK)
			var view productsAdminProduct
			productsAdminDecode(t, created, tc.name, &view)
			if view.Price != want || len(view.Variants) != 1 || view.Variants[0].Price != want {
				t.Fatalf("created %s price=%d variants=%+v; want exact %d on product and default variant", tc.name, view.Price, view.Variants, want)
			}
			var stored core.Product
			if err := h.db.First(&stored, view.ID).Error; err != nil {
				t.Fatalf("read created product: %v", err)
			}
			if stored.Price != want {
				t.Errorf("stored %s product price = %d, want exact %d", tc.name, stored.Price, want)
			}
			var variant core.ProductVariant
			if err := h.db.Where("product_id = ?", view.ID).Take(&variant).Error; err != nil {
				t.Fatalf("read default variant: %v", err)
			}
			if variant.Price != want {
				t.Errorf("stored %s default variant price = %d, want exact %d", tc.name, variant.Price, want)
			}
		})
	}
}

func TestAdminProductsLegacyIntegerPriceRejectsInvalidJSONTypesAndRanges(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "legacy-price-validation-admin")
	token := h.token(admin)

	valid := productsAdminDo(t, h, http.MethodPost, "/api/admin/products", map[string]any{
		"title": "Legacy integer price positive control", "status": "active", "price": json.RawMessage("1999"),
		"compare_at_price": "29.99", "variants": []map[string]any{{"name": "Default", "price": "19.99", "compare_at_price": "25.99", "stock": 1, "status": "active"}},
	}, token)
	productsAdminWantStatus(t, valid, "legacy integer price positive control", http.StatusOK)
	var validProduct productsAdminProduct
	productsAdminDecode(t, valid, "legacy integer price positive control", &validProduct)
	if validProduct.Price != 1999 || validProduct.CompareAtPrice != 2999 || len(validProduct.Variants) != 1 || validProduct.Variants[0].Price != 1999 || validProduct.Variants[0].CompareAtPrice != 2599 {
		t.Fatalf("legacy top-level integer and dollar-string compare/variant prices decoded as %+v; want 1999/2999 and 1999/2599 minor units", validProduct)
	}

	// Null remains equivalent to an omitted/empty top-level price for compatibility.
	nullPrice := productsAdminDo(t, h, http.MethodPost, "/api/admin/products", map[string]any{
		"title": "Legacy null price semantics", "status": "active", "price": json.RawMessage("null"),
	}, token)
	productsAdminWantStatus(t, nullPrice, "legacy null price follows empty value semantics", http.StatusOK)
	var nullProduct productsAdminProduct
	productsAdminDecode(t, nullPrice, "legacy null price follows empty value semantics", &nullProduct)
	if nullProduct.Price != 0 {
		t.Errorf("null legacy price = %d, want empty-value price 0", nullProduct.Price)
	}

	var before int64
	if err := h.db.Unscoped().Model(&core.Product{}).Count(&before).Error; err != nil {
		t.Fatalf("count products before invalid legacy prices: %v", err)
	}
	invalid := []struct {
		name  string
		value string
	}{
		{name: "negative number", value: "-1"},
		{name: "fractional number", value: "19.99"},
		{name: "exponent number", value: "1999e0"},
		{name: "int64 overflow", value: "9223372036854775808"},
		{name: "boolean", value: "true"},
		{name: "object", value: "{}"},
		{name: "array", value: "[]"},
	}
	for i, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			r := productsAdminDo(t, h, http.MethodPost, "/api/admin/products", map[string]any{
				"title": fmt.Sprintf("Invalid legacy price %d", i), "status": "active", "price": json.RawMessage(tc.value),
			}, token)
			productsAdminWantStatus(t, r, tc.name, http.StatusBadRequest)
		})
	}
	var after int64
	if err := h.db.Unscoped().Model(&core.Product{}).Count(&after).Error; err != nil {
		t.Fatalf("count products after invalid legacy prices: %v", err)
	}
	if after != before {
		t.Errorf("invalid legacy price requests changed product count from %d to %d", before, after)
	}
}
