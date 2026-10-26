package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

func TestShopTaxIDMod5VectorsAndInvoiceHTTPValidation(t *testing.T) {
	valid := []string{"04595252", "10458570", "10458579", "04595257", "10458575", "10458574", "12345675"}
	for _, id := range valid {
		if !shopValidTaxID(id) {
			t.Errorf("shopValidTaxID(%q) = false, want true", id)
		}
	}
	invalid := []string{"12345678", "04595253", "10458571", "1045-8570", " 10458570", "10458570 "}
	for _, id := range invalid {
		if shopValidTaxID(id) {
			t.Errorf("shopValidTaxID(%q) = true, want false", id)
		}
	}

	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "tax-id-validation", 12000, 10)
	variant := shopVariantRows(t, h, p.ID)[0]
	browser := newShopBrowser(h)
	shopAddToCart(t, browser, variant.ID, 1)
	checkoutBody := shopCheckoutBody("tax-id-checkout@example.test", "credit_card", "tax-id-valid-checkout")
	checkoutBody["invoice"] = map[string]any{"kind": "tax_id", "tax_id": "04595252", "company_name": "測試股份有限公司"}
	checkout := browser.send(http.MethodPost, "/api/checkout", checkoutBody)
	shopWantCode(t, checkout, "checkout with valid leading-zero tax ID", http.StatusCreated)
	var checkoutOrder struct {
		Order struct {
			Number string `json:"number"`
		} `json:"order"`
	}
	shopDecode(t, checkout, "checkout with valid leading-zero tax ID", &checkoutOrder)
	storedOrder := shopOrderRow(t, h, checkoutOrder.Order.Number)
	var invoice struct {
		TaxID string `json:"tax_id"`
	}
	if err := json.Unmarshal(storedOrder.Invoice, &invoice); err != nil {
		t.Fatalf("decode stored checkout invoice: %v", err)
	}
	if invoice.TaxID != "04595252" {
		t.Errorf("checkout stored tax ID %q, want leading zero preserved", invoice.TaxID)
	}

	owner := h.user("customer", "tax-invoice-profile-owner")
	profileBrowser := newShopBrowser(h)
	profileBrowser.token = h.token(owner)
	created := profileBrowser.send(http.MethodPost, "/api/account/invoice-profiles", map[string]any{
		"kind": "tax_id", "tax_id": "04595252", "company_name": "測試股份有限公司", "is_default": true,
	})
	shopWantCode(t, created, "create invoice profile with valid leading-zero tax ID", http.StatusCreated)
	var profile core.InvoiceProfile
	shopDecode(t, created, "created invoice profile", &profile)
	if profile.TaxID != "04595252" {
		t.Fatalf("created profile tax ID %q, want leading zero preserved", profile.TaxID)
	}

	profilePath := "/api/account/invoice-profiles/" + shopIDText(profile.ID)
	updated := profileBrowser.send(http.MethodPut, profilePath, map[string]any{
		"kind": "tax_id", "tax_id": "10458570", "company_name": "更新後公司", "is_default": true,
	})
	shopWantCode(t, updated, "update invoice profile with valid tax ID", http.StatusOK)
	shopDecode(t, updated, "updated invoice profile", &profile)
	if profile.TaxID != "10458570" {
		t.Fatalf("updated profile tax ID %q, want 10458570", profile.TaxID)
	}

	invalidUpdate := profileBrowser.send(http.MethodPut, profilePath, map[string]any{
		"kind": "tax_id", "tax_id": "12345678", "company_name": "不應保存", "is_default": true,
	})
	shopWantCode(t, invalidUpdate, "reject invalid invoice-profile update", http.StatusBadRequest)
	var persisted core.InvoiceProfile
	if err := h.db.First(&persisted, profile.ID).Error; err != nil {
		t.Fatalf("read invoice profile after rejected update: %v", err)
	}
	if persisted.TaxID != "10458570" || persisted.CompanyName != "更新後公司" {
		t.Errorf("invalid update changed profile to tax ID %q and company %q", persisted.TaxID, persisted.CompanyName)
	}
}

func TestAdminProductStockAdjustmentsAppendLedgerMovements(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "stock-ledger-admin")
	token := h.token(admin)
	created := productsAdminDo(t, h, http.MethodPost, "/api/admin/products", map[string]any{
		"title": "Ledger Product", "status": "active",
		"variants": []map[string]any{{"name": "Default", "sku": "LEDGER-1", "price": "12", "stock": 5, "status": "active"}},
	}, token)
	productsAdminWantSuccess(t, created, "create product with initial stock")
	var product productsAdminProduct
	productsAdminDecode(t, created, "create product with initial stock", &product)
	if len(product.Variants) != 1 {
		t.Fatalf("created variants = %d, want 1", len(product.Variants))
	}
	variant := product.Variants[0]
	path := fmt.Sprintf("/api/admin/products/%d", product.ID)
	assertMovements := func(want []int) {
		t.Helper()
		var rows []core.StockMovement
		if err := h.db.Where("variant_id = ? AND reason = ?", variant.ID, "adjust").Order("id").Find(&rows).Error; err != nil {
			t.Fatalf("read stock adjustment ledger: %v", err)
		}
		if len(rows) != len(want) {
			t.Fatalf("stock adjustment movements = %d, want %d", len(rows), len(want))
		}
		for i, row := range rows {
			if row.Delta != want[i] || row.OrderID != nil || row.Reason != "adjust" {
				t.Errorf("movement %d = delta %d, order %v, reason %q; want delta %d, no order, adjust", i, row.Delta, row.OrderID, row.Reason, want[i])
			}
		}
	}
	assertMovements([]int{5})

	update := func(stock int) {
		t.Helper()
		r := productsAdminDo(t, h, http.MethodPut, path, map[string]any{
			"title": "Ledger Product", "status": "active",
			"variants": []map[string]any{{"id": variant.ID, "name": "Default", "sku": "LEDGER-1", "price": "12", "stock": stock, "status": "active"}},
		}, token)
		productsAdminWantSuccess(t, r, fmt.Sprintf("update stock to %d", stock))
	}
	update(9)
	assertMovements([]int{5, 4})
	update(9)
	assertMovements([]int{5, 4})
	update(6)
	assertMovements([]int{5, 4, -3})
	var stored core.ProductVariant
	if err := h.db.First(&stored, variant.ID).Error; err != nil {
		t.Fatalf("read updated product variant: %v", err)
	}
	if stored.Stock != 6 {
		t.Errorf("stored stock = %d, want 6", stored.Stock)
	}
}

func TestOrderStatusReturnsLatestPaymentStatusWithoutDisclosure(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	owner := h.user("customer", "status-latest-owner")
	other := h.user("customer", "status-latest-other")
	ownerID := owner.ID
	order := shopSeedOrder(t, h, shopOrderSeed{
		number: fmt.Sprintf("JL-%d-9001", time.Now().Year()), email: owner.Email,
		status: core.OrderStatusPending, userID: &ownerID, total: 12000,
	})
	checkoutProof := "fixture-checkout-proof-for-status-test-0123456789"
	if err := h.db.Model(&core.Order{}).Where("id = ?", order.ID).Update("lookup_token_hash", shopSHA256Hex(checkoutProof)).Error; err != nil {
		t.Fatalf("seed order status access proof: %v", err)
	}
	payments := []core.Payment{
		{OrderID: order.ID, Provider: "sandbox", Method: "credit_card", MerchantTradeNo: "STATUSPAYMENT0000001", Amount: order.TotalAmount, Currency: "TWD", Status: "pending"},
		{OrderID: order.ID, Provider: "sandbox", Method: "credit_card", MerchantTradeNo: "STATUSPAYMENT0000002", Amount: order.TotalAmount, Currency: "TWD", Status: "failed"},
	}
	for i := range payments {
		if err := h.db.Create(&payments[i]).Error; err != nil {
			t.Fatalf("seed payment attempt %d: %v", i, err)
		}
	}
	path := "/api/orders/" + order.Number + "/status"
	unauthorized := newShopBrowser(h)
	unauthorized.token = h.token(other)
	denied := unauthorized.get(path)
	if denied.code != http.StatusNotFound {
		t.Fatalf("another customer read order status returned %d, want 404", denied.code)
	}
	if strings.Contains(denied.body, order.Number) || strings.Contains(denied.body, owner.Email) || strings.Contains(denied.body, "failed") {
		t.Errorf("unauthorized status response disclosed order or payment data")
	}

	authorized := newShopBrowser(h)
	result := authorized.get(path + "?access=" + checkoutProof)
	shopWantCode(t, result, "authorized order status", http.StatusOK)
	var status struct {
		Number        string `json:"number"`
		Status        string `json:"status"`
		PaymentStatus string `json:"payment_status"`
	}
	shopDecode(t, result, "authorized order status", &status)
	if status.Number != order.Number || status.Status != core.OrderStatusPending || status.PaymentStatus != "failed" {
		t.Errorf("order status response = number %q, status %q, payment_status %q; want bound number, pending, failed", status.Number, status.Status, status.PaymentStatus)
	}
	if got := shopOrderRow(t, h, order.Number).Status; got != core.OrderStatusPending {
		t.Errorf("latest failed payment changed order status to %q, want pending", got)
	}
}

func TestAdminRefundIdempotencySurvivesTerminalAndRemainingBalance(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	admin := h.user("admin", "refund-replay-admin")
	operator := newShopBrowser(h)
	operator.token = h.token(admin)
	makeOrder := func(numberSuffix string, status string) core.Order {
		t.Helper()
		return shopSeedOrder(t, h, shopOrderSeed{
			number: fmt.Sprintf("JL-%d-%s", time.Now().Year(), numberSuffix), email: "refund-replay@example.test",
			status: status, provider: "manual_transfer", method: "bank_transfer", total: 10000,
		})
	}
	refund := func(order core.Order, body map[string]any) shopResponse {
		t.Helper()
		return operator.send(http.MethodPost, fmt.Sprintf("/api/admin/orders/%d/refund", order.ID), body)
	}
	count := func(query string, args ...any) int64 {
		t.Helper()
		return shopCount(t, h, query, args...)
	}

	full := makeOrder("9101", core.OrderStatusShipped)
	product := shopSeedProduct(t, h, "refund-replay-restock", 10000, 5)
	variant := shopVariantRows(t, h, product.ID)[0]
	productID, variantID := product.ID, variant.ID
	if err := h.db.Create(&core.OrderItem{OrderID: full.ID, ProductID: &productID, VariantID: &variantID, Quantity: 1, Price: 10000, UnitPrice: 10000, LineTotal: 10000, Title: "Refund item"}).Error; err != nil {
		t.Fatalf("seed refundable order item: %v", err)
	}
	if err := h.db.Model(&core.ProductVariant{}).Where("id = ?", variant.ID).Update("stock", 4).Error; err != nil {
		t.Fatalf("seed reserved stock: %v", err)
	}
	if err := h.db.Create(&core.StockMovement{VariantID: variant.ID, OrderID: &full.ID, Delta: -1, Reason: "reserve"}).Error; err != nil {
		t.Fatalf("seed reservation movement: %v", err)
	}
	fullPayload := map[string]any{"amount": 10000, "reason": "full refund replay", "restock": true, "idempotency_key": "full-refund-replay-key"}
	shopWantCode(t, refund(full, fullPayload), "initial full refund", http.StatusOK)
	if got := shopOrderRow(t, h, full.Number).Status; got != core.OrderStatusRefunded {
		t.Fatalf("full refund status = %q, want refunded", got)
	}
	shopWantCode(t, refund(full, fullPayload), "same-key full refund replay after terminal state", http.StatusOK)
	if got := shopOrderRow(t, h, full.Number).RefundedTotal; got != 10000 {
		t.Errorf("refunded total after full replay = %d, want 10000", got)
	}
	if n := count("SELECT count(*) FROM order_events WHERE order_id = ? AND kind = 'refund'", full.ID); n != 1 {
		t.Errorf("full refund order events = %d, want 1", n)
	}
	if n := count("SELECT count(*) FROM payment_events WHERE provider = 'admin-refund' AND dedupe_key = ?", "full-refund-replay-key"); n != 1 {
		t.Errorf("full refund payment events = %d, want 1", n)
	}
	if n := count("SELECT count(*) FROM stock_movements WHERE order_id = ? AND reason = 'restock'", full.ID); n != 1 {
		t.Errorf("full refund restock movements = %d, want 1", n)
	}
	var stockAfterFull int
	if err := h.db.Model(&core.ProductVariant{}).Where("id = ?", variant.ID).Select("stock").Scan(&stockAfterFull).Error; err != nil {
		t.Fatalf("read stock after full refund replay: %v", err)
	}
	if stockAfterFull != 5 {
		t.Errorf("stock after full refund replay = %d, want 5", stockAfterFull)
	}
	deliveriesAfterFull := count("SELECT count(*) FROM notification_deliveries WHERE kind = 'order.refunded' AND to_address = ?", full.Email)
	if deliveriesAfterFull != 1 {
		t.Errorf("full refund email deliveries = %d, want 1", deliveriesAfterFull)
	}

	partial := makeOrder("9102", core.OrderStatusShipped)
	partialPayload := map[string]any{"amount": 7000, "reason": "partial refund replay", "restock": false, "idempotency_key": "partial-refund-replay-key"}
	shopWantCode(t, refund(partial, partialPayload), "initial partial refund", http.StatusOK)
	shopWantCode(t, refund(partial, partialPayload), "same-key partial replay with smaller remaining balance", http.StatusOK)
	if got := shopOrderRow(t, h, partial.Number).RefundedTotal; got != 7000 {
		t.Errorf("partial refunded total after replay = %d, want 7000", got)
	}
	conflictPayload := map[string]any{"amount": 8000, "reason": "different payload", "restock": false, "idempotency_key": "partial-refund-replay-key"}
	shopWantCode(t, refund(partial, conflictPayload), "different refund payload with reused key", http.StatusConflict)
	if n := count("SELECT count(*) FROM order_events WHERE order_id = ? AND kind = 'refund'", partial.ID); n != 1 {
		t.Errorf("partial refund order events after conflict = %d, want 1", n)
	}
	if n := count("SELECT count(*) FROM notification_deliveries WHERE kind = 'order.refunded' AND to_address = ?", partial.Email); n != 2 {
		t.Errorf("refund email deliveries for two distinct refund keys = %d, want 2", n)
	}

	omitted := makeOrder("9103", core.OrderStatusShipped)
	omittedPayload := map[string]any{"reason": "omitted means full", "restock": false, "idempotency_key": "omitted-refund-replay-key"}
	shopWantCode(t, refund(omitted, omittedPayload), "full refund with omitted amount", http.StatusOK)
	shopWantCode(t, refund(omitted, omittedPayload), "same-key omitted-amount refund replay", http.StatusOK)
	if got := shopOrderRow(t, h, omitted.Number); got.RefundedTotal != got.TotalAmount || got.Status != core.OrderStatusRefunded {
		t.Errorf("omitted-amount replay resulted in refunded_total=%d status=%q, want full refunded state", got.RefundedTotal, got.Status)
	}
	nullAmount := makeOrder("9104", core.OrderStatusShipped)
	shopWantCode(t, refund(nullAmount, map[string]any{"amount": nil, "reason": "null rejected", "restock": false, "idempotency_key": "null-refund-amount-key"}), "refund with explicit null amount", http.StatusBadRequest)
}
