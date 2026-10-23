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
	pending := shopSeedOrder(t, h, shopOrderSeed{number: fmt.Sprintf("JL-%d-0001", year), email: a.Email, status: "pending", total: 12000, userID: &aid})
	paid := shopSeedOrder(t, h, shopOrderSeed{number: fmt.Sprintf("JL-%d-0002", year), email: a.Email, status: "paid", total: 12000, userID: &aid})
	shipped := shopSeedOrder(t, h, shopOrderSeed{number: fmt.Sprintf("JL-%d-0003", year), email: a.Email, status: "shipped", total: 12000, userID: &aid})
	second := shopSeedOrder(t, h, shopOrderSeed{number: fmt.Sprintf("JL-%d-0004", year), email: a.Email, status: "pending", total: 12000, userID: &aid})

	member := newShopBrowser(h)
	member.token = h.token(a)
	cancel := func(number string) string { return "/api/account/orders/" + number + "/cancel" }

	shopWantOK(t, member.send(http.MethodPost, cancel(pending.Number), nil), "cancel pending order")
	if got := shopOrderRow(t, h, pending.Number).Status; got != core.OrderStatusCancelled {
		t.Errorf("pending order status after cancel = %q, want cancelled", got)
	}

	shopWantOK(t, member.send(http.MethodPost, cancel(paid.Number), nil), "cancel paid unshipped order")
	if got := shopOrderRow(t, h, paid.Number).Status; got != core.OrderStatusCancelled {
		t.Errorf("paid order status after cancel = %q, want cancelled", got)
	}
	if n := shopCount(t, h, "SELECT COUNT(*) FROM order_events WHERE order_id = ? AND kind LIKE ?", paid.ID, "%refund%"); n < 1 {
		t.Errorf("cancelling a paid order started no refund workflow (no refund order_events)")
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
	cb := newShopBrowser(h)
	cb.token = h.token(customer)

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
