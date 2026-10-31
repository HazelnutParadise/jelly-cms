package server

// Member account integration tests for ticket 07, section E7 (contract: /account, /api/account/* in
// docs/tickets/07-shop.md). They use the public routes against the dedicated PostgreSQL database and are skipped
// unless JELLY_TEST_POSTGRES=1. They must fail until the implementation exists. Do not edit them to match an
// implementation.
//
// Assumed response shapes (the contract names the features but not the JSON keys): GET /api/account/orders returns
// {items:[{number,...}], counts:{all, <status>...}}; reorder returns {unavailable:[variant ids or {variant_id}]}.

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

func TestAccountOrdersListOwnOnlyWithCounts(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	a := h.user("customer", "member-a")
	other := h.user("customer", "member-b")
	aid, oid := a.ID, other.ID
	year := time.Now().Year()
	shopSeedOrder(t, h, shopOrderSeed{number: fmt.Sprintf("JL-%d-0001", year), email: a.Email, status: "pending", total: 12000, userID: &aid})
	shopSeedOrder(t, h, shopOrderSeed{number: fmt.Sprintf("JL-%d-0002", year), email: a.Email, status: "paid", total: 5000, userID: &aid})
	shopSeedOrder(t, h, shopOrderSeed{number: fmt.Sprintf("JL-%d-0003", year), email: other.Email, status: "pending", total: 7000, userID: &oid})

	member := newShopBrowser(h)
	member.token = h.token(a)
	r := member.get("/api/account/orders")
	shopWantCode(t, r, "GET /api/account/orders", http.StatusOK)
	var page struct {
		Items []struct {
			Number string `json:"number"`
		} `json:"items"`
		Counts map[string]int `json:"counts"`
	}
	shopDecode(t, r, "GET /api/account/orders", &page)
	var numbers []string
	for _, item := range page.Items {
		numbers = append(numbers, item.Number)
	}
	slices.Sort(numbers)
	if want := []string{fmt.Sprintf("JL-%d-0001", year), fmt.Sprintf("JL-%d-0002", year)}; !slices.Equal(numbers, want) {
		t.Errorf("member sees orders %v, want only their own %v", numbers, want)
	}
	if page.Counts["all"] != 2 || page.Counts["pending"] != 1 || page.Counts["paid"] != 1 {
		t.Errorf("counts = %v, want all 2, pending 1, paid 1", page.Counts)
	}

	anonymous := newShopBrowser(h)
	shopWantCode(t, anonymous.get("/api/account/orders"), "anonymous GET /api/account/orders", http.StatusUnauthorized)
}

func TestAccountOrderDetailOthers404(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	a := h.user("customer", "owner-a")
	other := h.user("customer", "owner-b")
	aid := a.ID
	number := fmt.Sprintf("JL-%d-0001", time.Now().Year())
	shopSeedOrder(t, h, shopOrderSeed{number: number, email: a.Email, status: "pending", total: 12000, userID: &aid})

	owner := newShopBrowser(h)
	owner.token = h.token(a)
	shopWantCode(t, owner.get("/api/account/orders/"+number), "owner GET detail", http.StatusOK)

	stranger := newShopBrowser(h)
	stranger.token = h.token(other)
	shopWantCode(t, stranger.get("/api/account/orders/"+number), "other member GET detail", http.StatusNotFound)
}

func TestAccountOrderCancelOnlyBeforeShipping(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	a := h.user("customer", "canceller")
	other := h.user("customer", "bystander")
	aid := a.ID
	year := time.Now().Year()
	pendingProduct := shopSeedProduct(t, h, "member-cancel-pending", 6000, 5)
	pendingVariant := shopVariantRows(t, h, pendingProduct.ID)[0]
	pending := shopSeedOrder(t, h, shopOrderSeed{
		number: fmt.Sprintf("JL-%d-0001", year), email: a.Email, status: "pending", total: 12000, userID: &aid,
		items: []shopItemSeed{{variantID: &pendingVariant.ID, productID: &pendingVariant.ProductID, title: pendingProduct.Title, qty: 1, unitPrice: 6000}},
	})
	if err := h.db.Create(&core.StockMovement{VariantID: pendingVariant.ID, OrderID: &pending.ID, Delta: -1, Reason: "reserve"}).Error; err != nil {
		t.Fatalf("seed pending order reservation: %v", err)
	}
	if err := h.db.Model(&core.ProductVariant{}).Where("id = ?", pendingVariant.ID).Update("stock", 4).Error; err != nil {
		t.Fatalf("set pending reserved variant stock: %v", err)
	}
	product := shopSeedProduct(t, h, "member-cancel-paid", 6000, 7)
	variant := shopVariantRows(t, h, product.ID)[0]
	coupon := shopSeedCoupon(t, h, core.Coupon{Code: "CANCEL10", Kind: "fixed", Value: 1000, UsedCount: 1})
	paid := shopSeedOrder(t, h, shopOrderSeed{
		number: fmt.Sprintf("JL-%d-0002", year), email: a.Email, status: "paid", provider: "manual_transfer", method: "bank_transfer", total: 12000,
		userID: &aid, couponID: &coupon.ID, couponCode: coupon.Code,
		items: []shopItemSeed{{variantID: &variant.ID, productID: &variant.ProductID, title: product.Title, qty: 2, unitPrice: 6000}},
	})
	paidSandbox := shopSeedOrder(t, h, shopOrderSeed{
		number: fmt.Sprintf("JL-%d-0005", year), email: a.Email, status: "paid", provider: "sandbox", method: "credit_card", total: 12000, userID: &aid,
	})
	if err := h.db.Create(&core.StockMovement{VariantID: variant.ID, OrderID: &paid.ID, Delta: -2, Reason: "reserve"}).Error; err != nil {
		t.Fatalf("seed paid order reservation: %v", err)
	}
	if err := h.db.Model(&core.ProductVariant{}).Where("id = ?", variant.ID).Update("stock", 5).Error; err != nil {
		t.Fatalf("set reserved variant stock: %v", err)
	}
	if err := h.db.Create(&core.CouponRedemption{CouponID: coupon.ID, OrderID: paid.ID, UserID: &aid, Email: a.Email}).Error; err != nil {
		t.Fatalf("seed paid order coupon redemption: %v", err)
	}
	shipped := shopSeedOrder(t, h, shopOrderSeed{number: fmt.Sprintf("JL-%d-0003", year), email: a.Email, status: "shipped", total: 12000, userID: &aid})
	second := shopSeedOrder(t, h, shopOrderSeed{number: fmt.Sprintf("JL-%d-0004", year), email: a.Email, status: "pending", total: 12000, userID: &aid})

	member := newShopBrowser(h)
	member.token = h.token(a)
	cancel := func(number string) string { return "/api/account/orders/" + number + "/cancel" }

	shopWantOK(t, member.send(http.MethodPost, cancel(pending.Number), nil), "cancel pending order")
	if got := shopOrderRow(t, h, pending.Number).Status; got != core.OrderStatusCancelled {
		t.Errorf("pending order status after cancel = %q, want cancelled", got)
	}
	var pendingStock int
	if err := h.db.Model(&core.ProductVariant{}).Select("stock").Where("id = ?", pendingVariant.ID).Scan(&pendingStock).Error; err != nil {
		t.Fatalf("read pending stock after cancellation: %v", err)
	}
	if pendingStock != 5 || shopCount(t, h, "SELECT COUNT(*) FROM stock_movements WHERE order_id = ? AND reason = ?", pending.ID, "release") != 1 {
		t.Errorf("pending cancellation did not release its reservation: stock=%d, want 5 and one release movement", pendingStock)
	}

	shopWantOK(t, member.send(http.MethodPost, cancel(paid.Number), nil), "cancel paid unshipped order")
	if got := shopOrderRow(t, h, paid.Number); got.Status != core.OrderStatusPaid || got.RefundedTotal != 0 {
		t.Errorf("paid order after customer refund request = status %q refunded_total %d, want paid/0", got.Status, got.RefundedTotal)
	}
	if n := shopCount(t, h, "SELECT COUNT(*) FROM order_events WHERE order_id = ? AND kind = ? AND from_status = ? AND to_status = ?", paid.ID, "refund_requested", core.OrderStatusPaid, core.OrderStatusPaid); n != 1 {
		t.Errorf("refund_requested events = %d, want one idempotent request that leaves the order paid", n)
	}
	var stock int
	if err := h.db.Model(&core.ProductVariant{}).Select("stock").Where("id = ?", variant.ID).Scan(&stock).Error; err != nil {
		t.Fatalf("read reserved stock after customer request: %v", err)
	}
	if stock != 5 {
		t.Errorf("customer refund request changed reserved stock to %d, want 5", stock)
	}
	var released int64
	if err := h.db.Model(&core.CouponRedemption{}).Where("order_id = ? AND released_at IS NOT NULL", paid.ID).Count(&released).Error; err != nil {
		t.Fatalf("count released paid-order redemptions: %v", err)
	}
	var storedCoupon core.Coupon
	if err := h.db.First(&storedCoupon, coupon.ID).Error; err != nil {
		t.Fatalf("reload paid-order coupon: %v", err)
	}
	if released != 0 || storedCoupon.UsedCount != 1 {
		t.Errorf("customer refund request released %d redemptions and coupon used_count is %d, want 0/1", released, storedCoupon.UsedCount)
	}
	shopWantOK(t, member.send(http.MethodPost, cancel(paid.Number), nil), "retry paid-order refund request")
	if n := shopCount(t, h, "SELECT COUNT(*) FROM order_events WHERE order_id = ? AND kind = ?", paid.ID, "refund_requested"); n != 1 {
		t.Errorf("repeated cancellation recorded %d refund requests, want one", n)
	}
	shopWantOK(t, member.send(http.MethodPost, cancel(paidSandbox.Number), nil), "cancel paid sandbox order")
	if got := shopOrderRow(t, h, paidSandbox.Number); got.Status != core.OrderStatusPaid || got.RefundedTotal != 0 {
		t.Errorf("paid sandbox order after customer refund request = status %q refunded_total %d, want paid/0", got.Status, got.RefundedTotal)
	}
	if n := shopCount(t, h, "SELECT COUNT(*) FROM order_events WHERE order_id = ? AND kind = ? AND from_status = ? AND to_status = ?", paidSandbox.ID, "refund_requested", core.OrderStatusPaid, core.OrderStatusPaid); n != 1 {
		t.Errorf("paid sandbox refund_requested events = %d, want one pending actual refund", n)
	}
	admin := newShopBrowser(h)
	admin.token = h.token(h.user("admin", "refund-reviewer"))
	shopWantOK(t, admin.send(http.MethodPost, fmt.Sprintf("/api/admin/orders/%d/refund", paid.ID), map[string]any{
		"reason": "customer cancellation request", "restock": false,
		"idempotency_key": "member-cancel-admin-refund",
	}), "admin actual refund after customer request")
	if got := shopOrderRow(t, h, paid.Number); got.RefundedTotal != got.TotalAmount {
		t.Errorf("admin refund total = %d, want full order amount %d", got.RefundedTotal, got.TotalAmount)
	}

	shopWantCode(t, member.send(http.MethodPost, cancel(shipped.Number), nil), "cancel shipped order", http.StatusConflict)

	bystander := newShopBrowser(h)
	bystander.token = h.token(other)
	shopWantCode(t, bystander.send(http.MethodPost, cancel(second.Number), nil), "another member cancels", http.StatusNotFound)
	if got := shopOrderRow(t, h, second.Number).Status; got != core.OrderStatusPending {
		t.Errorf("another member changed the order: status %q", got)
	}
}

func TestAccountOrderReorderReportsUnavailable(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	a := h.user("customer", "reorderer")
	aid := a.ID
	inStock := shopVariantRows(t, h, shopSeedProduct(t, h, "reorder-in", 12000, 5).ID)[0]
	soldOut := shopVariantRows(t, h, shopSeedProduct(t, h, "reorder-out", 5000, 0).ID)[0]
	number := fmt.Sprintf("JL-%d-0001", time.Now().Year())
	shopSeedOrder(t, h, shopOrderSeed{
		number: number, email: a.Email, status: "completed", total: 17000, userID: &aid,
		items: []shopItemSeed{
			{variantID: &inStock.ID, productID: &inStock.ProductID, title: "reorder-in", qty: 1, unitPrice: 12000},
			{variantID: &soldOut.ID, productID: &soldOut.ProductID, title: "reorder-out", qty: 1, unitPrice: 5000},
		},
	})

	member := newShopBrowser(h)
	member.token = h.token(a)
	r := member.send(http.MethodPost, "/api/account/orders/"+number+"/reorder", nil)
	shopWantCode(t, r, "POST reorder", http.StatusOK)
	var result struct {
		Unavailable []any `json:"unavailable"`
	}
	shopDecode(t, r, "POST reorder", &result)
	found := false
	for _, entry := range result.Unavailable {
		switch value := entry.(type) {
		case float64:
			found = found || uint(value) == soldOut.ID
		case map[string]any:
			if id, ok := value["variant_id"].(float64); ok && uint(id) == soldOut.ID {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("reorder did not report the sold-out variant %d as unavailable; body=%q", soldOut.ID, r.body)
	}

	cart := shopCartGet(t, member)
	if _, ok := cart.line(inStock.ID); !ok {
		t.Error("reorder did not add the in-stock item to the cart")
	}
	if _, ok := cart.line(soldOut.ID); ok {
		t.Error("reorder added the unavailable item to the cart")
	}
}

func TestAccountAddressesAndInvoiceProfilesOwnership(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	a := h.user("customer", "addr-owner")
	other := h.user("customer", "addr-other")
	owner := newShopBrowser(h)
	owner.token = h.token(a)
	stranger := newShopBrowser(h)
	stranger.token = h.token(other)

	created := owner.send(http.MethodPost, "/api/account/addresses", map[string]any{
		"label": "home", "recipient": "Alice Tester", "phone": "0912345678", "postcode": "100",
		"city": "Taipei", "address": "1 Test Road", "is_default": true,
	})
	shopWantOK(t, created, "POST /api/account/addresses")
	addressID := shopJSONID(t, created, "POST /api/account/addresses")
	shopWantCode(t, stranger.send(http.MethodPut, "/api/account/addresses/"+shopIDText(addressID), map[string]any{"recipient": "hijack"}),
		"other member PUT address", http.StatusNotFound)
	shopWantCode(t, stranger.send(http.MethodDelete, "/api/account/addresses/"+shopIDText(addressID), nil),
		"other member DELETE address", http.StatusNotFound)
	var list []map[string]any
	shopDecode(t, owner.get("/api/account/addresses"), "GET /api/account/addresses", &list)
	if len(list) != 1 {
		t.Errorf("owner sees %d addresses, want 1 (the other member's delete must not touch it)", len(list))
	}

	profile := owner.send(http.MethodPost, "/api/account/invoice-profiles", map[string]any{
		"kind": "tax_id", "tax_id": "12345675", "company_name": "Test Co",
	})
	shopWantOK(t, profile, "POST /api/account/invoice-profiles")
	profileID := shopJSONID(t, profile, "POST /api/account/invoice-profiles")
	shopWantCode(t, owner.send(http.MethodPost, "/api/account/invoice-profiles", map[string]any{
		"kind": "tax_id", "tax_id": "12345678", "company_name": "Bad Co",
	}), "POST invoice profile with a bad tax id", http.StatusBadRequest)
	shopWantCode(t, stranger.send(http.MethodPut, "/api/account/invoice-profiles/"+shopIDText(profileID), map[string]any{"company_name": "hijack"}),
		"other member PUT invoice profile", http.StatusNotFound)
	shopWantCode(t, stranger.send(http.MethodDelete, "/api/account/invoice-profiles/"+shopIDText(profileID), nil),
		"other member DELETE invoice profile", http.StatusNotFound)
}

func TestAccountRedirectSplitsCustomerAndStaff(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	customer := h.user("customer", "redirect-customer")
	staff := h.user("admin", "redirect-admin")

	cb := newShopBrowser(h)
	cb.token = h.token(customer)
	if r := cb.get("/account"); r.code != http.StatusFound || r.header.Get("Location") != "/account/orders" {
		t.Errorf("customer GET /account = %d Location %q, want 302 to /account/orders", r.code, r.header.Get("Location"))
	}

	sb := newShopBrowser(h)
	sb.token = h.token(staff)
	if r := sb.get("/account"); r.code != http.StatusFound || r.header.Get("Location") != "/admin/account?tab=identities" {
		t.Errorf("staff GET /account = %d Location %q, want 302 to /admin/account?tab=identities", r.code, r.header.Get("Location"))
	}
}

func TestCustomerCannotReachAdmin(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	customer := h.user("customer", "shopper")
	// Ticket 23: the admin API takes only the browser login, so the customer logs in with the jwt_token
	// cookie. A Bearer header would now stop at the 401 gate before the role check this test is about.
	cb := newShopBrowser(h)
	cb.jar["jwt_token"] = &http.Cookie{Name: "jwt_token", Value: h.token(customer), Path: "/"}

	for _, path := range []string{"/admin", "/admin/orders", "/admin/orders/1", "/admin/coupons"} {
		r := cb.get(path)
		if r.code != http.StatusFound && r.code != http.StatusForbidden {
			t.Errorf("customer GET %s = %d, want 302 or 403", path, r.code)
		}
	}
	for _, path := range []string{"/api/admin/orders", "/api/admin/coupons", "/api/admin/shop/settings", "/api/admin/product-categories"} {
		shopWantCode(t, cb.get(path), "customer GET "+path, http.StatusForbidden)
	}
}
