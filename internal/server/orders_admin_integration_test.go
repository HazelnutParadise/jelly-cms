package server

// Admin order integration tests for ticket 07, section E8 (contract: 管理 API for orders, settings and the order
// state machine in docs/tickets/07-shop.md). They run through /api/admin/* on the dedicated PostgreSQL database and
// are skipped unless JELLY_TEST_POSTGRES=1. They must fail until the implementation exists. Do not edit them to
// match an implementation.
//
// Assumed request and response keys (the contract names the operations, not the JSON keys): the note body is sent as
// "body" and "note"; the address patch carries "recipient", "name" and "address"; the list returns
// {items, total, counts, kpi:{revenue_30d, orders_30d, pending, avg_order}}; the order detail carries items and
// timeline. The KPI assumes revenue from paid, shipped and completed orders created in the last 30 days.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

var shopOrderStatuses = []string{
	core.OrderStatusPending, core.OrderStatusPaid, core.OrderStatusShipped, core.OrderStatusCompleted,
	core.OrderStatusCancelled, core.OrderStatusExpired, core.OrderStatusRefunded,
}

// shopStateTable is the allowed set of the state machine in the contract (pending to paid, cancelled or expired;
// paid to shipped or cancelled; shipped to completed or refunded; completed to refunded). Terminal states have none.
var shopStateTable = map[string][]string{
	core.OrderStatusPending:   {core.OrderStatusPaid, core.OrderStatusCancelled, core.OrderStatusExpired},
	core.OrderStatusPaid:      {core.OrderStatusShipped, core.OrderStatusCancelled},
	core.OrderStatusShipped:   {core.OrderStatusCompleted, core.OrderStatusRefunded},
	core.OrderStatusCompleted: {core.OrderStatusRefunded},
}

// shopOptionsDump returns every options row in key order, for "this request changed nothing" checks.
func shopOptionsDump(t *testing.T, h *accessHarness) string {
	t.Helper()
	var rows []core.Option
	if err := h.db.Order("key").Find(&rows).Error; err != nil {
		t.Fatalf("read options: %v", err)
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("encode options: %v", err)
	}
	return string(raw)
}

func TestOrdersAdminListFiltersCountsKPI(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	admin := h.user("admin", "listing")
	now := time.Now()
	old := now.Add(-40 * 24 * time.Hour)
	year := now.Year()
	shopSeedOrder(t, h, shopOrderSeed{number: fmt.Sprintf("JL-%d-0001", year), email: "p1@example.test", status: "pending", total: 5000, created: now})
	shopSeedOrder(t, h, shopOrderSeed{number: fmt.Sprintf("JL-%d-0002", year), email: "p2@example.test", status: "paid", total: 12000, created: now})
	cancelled := shopSeedOrder(t, h, shopOrderSeed{number: fmt.Sprintf("JL-%d-0003", year), email: "p3@example.test", status: "cancelled", total: 3000, created: now})
	shopSeedOrder(t, h, shopOrderSeed{number: fmt.Sprintf("JL-%d-0004", year), email: "p4@example.test", status: "paid", total: 99999, created: old})

	adm := newShopBrowser(h)
	adm.token = h.token(admin)
	type listing struct {
		Items []struct {
			Number string `json:"number"`
			Status string `json:"status"`
		} `json:"items"`
		Total  int            `json:"total"`
		Counts map[string]int `json:"counts"`
		KPI    struct {
			Revenue30d int64 `json:"revenue_30d"`
			Orders30d  int   `json:"orders_30d"`
			Pending    int   `json:"pending"`
			AvgOrder   int64 `json:"avg_order"`
		} `json:"kpi"`
	}
	r := adm.get("/api/admin/orders?status=paid")
	shopWantCode(t, r, "GET /api/admin/orders?status=paid", http.StatusOK)
	var page listing
	shopDecode(t, r, "GET /api/admin/orders?status=paid", &page)
	if len(page.Items) != 2 || page.Total != 2 {
		t.Errorf("status=paid returned %d items (total %d), want 2", len(page.Items), page.Total)
	}
	for _, item := range page.Items {
		if item.Status != core.OrderStatusPaid {
			t.Errorf("status filter returned %s with status %s", item.Number, item.Status)
		}
	}
	if page.Counts["paid"] != 2 || page.Counts["pending"] != 1 || page.Counts["cancelled"] != 1 {
		t.Errorf("counts = %v, want paid 2, pending 1, cancelled 1", page.Counts)
	}
	if page.KPI.Revenue30d != 12000 {
		t.Errorf("kpi.revenue_30d = %d, want 12000 (the 40-day-old paid order is outside the window)", page.KPI.Revenue30d)
	}
	if page.KPI.Orders30d != 3 || page.KPI.Pending != 1 {
		t.Errorf("kpi orders_30d=%d pending=%d, want 3 and 1", page.KPI.Orders30d, page.KPI.Pending)
	}

	var searched listing
	shopDecode(t, adm.get("/api/admin/orders?q="+cancelled.Number), "search by number", &searched)
	if len(searched.Items) != 1 || searched.Items[0].Number != cancelled.Number {
		t.Errorf("q=%s returned %+v, want exactly that order", cancelled.Number, searched.Items)
	}
}

func TestOrdersAdminStateMachineTableDriven(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	admin := h.user("admin", "statemachine")
	adm := newShopBrowser(h)
	adm.token = h.token(admin)
	year := time.Now().Year()
	n := 0
	for _, from := range shopOrderStatuses {
		for _, to := range shopOrderStatuses {
			from, to := from, to
			t.Run(from+" to "+to, func(t *testing.T) {
				n++
				order := shopSeedOrder(t, h, shopOrderSeed{
					number: fmt.Sprintf("JL-%d-%04d", year, 100+n), email: fmt.Sprintf("sm%d@example.test", n),
					status: from, provider: "manual_transfer", total: 12000,
				})
				r := adm.send(http.MethodPut, fmt.Sprintf("/api/admin/orders/%d", order.ID), map[string]any{
					"status": to, "carrier": "TEST-CARRIER", "tracking_no": "TRACK-1",
				})
				want := shopStateTable[from]
				if slices.Contains(want, to) {
					shopWantOK(t, r, "PUT status "+from+" to "+to)
					if got := shopOrderRow(t, h, order.Number).Status; got != to {
						t.Errorf("status after allowed move = %q, want %q", got, to)
					}
					return
				}
				shopWantCode(t, r, "PUT status "+from+" to "+to, http.StatusConflict)
				var body struct {
					Allowed []string `json:"allowed"`
				}
				shopDecode(t, r, "PUT status "+from+" to "+to, &body)
				got := slices.Clone(body.Allowed)
				expected := slices.Clone(want)
				slices.Sort(got)
				slices.Sort(expected)
				if !slices.Equal(got, expected) {
					t.Errorf("allowed = %v, want %v", got, expected)
				}
				if status := shopOrderRow(t, h, order.Number).Status; status != from {
					t.Errorf("illegal move changed status to %q", status)
				}
			})
		}
	}
}

func TestOrdersAdminRefundPartialFullOverAndIdempotent(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	admin := h.user("admin", "refunder")
	adm := newShopBrowser(h)
	adm.token = h.token(admin)
	paidAt := time.Now().Add(-time.Hour)
	order := shopSeedOrder(t, h, shopOrderSeed{
		number: fmt.Sprintf("JL-%d-0001", time.Now().Year()), email: "refund@example.test", status: "shipped",
		provider: "sandbox", method: "credit_card", total: 10000, paidAt: &paidAt,
	})
	refundPath := fmt.Sprintf("/api/admin/orders/%d/refund", order.ID)

	shopWantOK(t, adm.send(http.MethodPost, refundPath, map[string]any{"amount": 3000, "reason": "partial", "restock": true}), "partial refund 3000")
	if got := shopOrderRow(t, h, order.Number).RefundedTotal; got != 3000 {
		t.Errorf("refunded_total after partial refund = %d, want 3000", got)
	}

	over := adm.send(http.MethodPost, refundPath, map[string]any{"amount": 8000, "reason": "over", "restock": false})
	if over.code >= 200 && over.code < 300 {
		t.Errorf("refund of 8000 on 7000 remaining was accepted (%d)", over.code)
	}
	if got := shopOrderRow(t, h, order.Number).RefundedTotal; got != 3000 {
		t.Errorf("refunded_total after over-refund attempt = %d, want 3000", got)
	}

	shopWantOK(t, adm.send(http.MethodPost, refundPath, map[string]any{"amount": 7000, "reason": "full", "restock": false}), "remaining refund 7000")
	full := shopOrderRow(t, h, order.Number)
	if full.RefundedTotal != 10000 || full.Status != core.OrderStatusRefunded {
		t.Errorf("after the full refund: refunded_total=%d status=%q, want 10000 and refunded", full.RefundedTotal, full.Status)
	}

	again := adm.send(http.MethodPost, refundPath, map[string]any{"amount": 7000, "reason": "full", "restock": false})
	if again.code >= 200 && again.code < 300 {
		t.Errorf("a second refund on a refunded order was accepted (%d)", again.code)
	}
	if got := shopOrderRow(t, h, order.Number).RefundedTotal; got != 10000 {
		t.Errorf("refunded_total after repeat = %d, want 10000 (never more than the total)", got)
	}
}

func TestOrdersAdminMarkPaidOnlyManualTransfer(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	admin := h.user("admin", "markpaid")
	adm := newShopBrowser(h)
	adm.token = h.token(admin)
	year := time.Now().Year()
	card := shopSeedOrder(t, h, shopOrderSeed{number: fmt.Sprintf("JL-%d-0001", year), email: "card@example.test", status: "pending", provider: "sandbox", method: "credit_card", total: 12000})
	transfer := shopSeedOrder(t, h, shopOrderSeed{number: fmt.Sprintf("JL-%d-0002", year), email: "wire@example.test", status: "pending", provider: "manual_transfer", method: "bank_transfer", total: 12000})

	cardReply := adm.send(http.MethodPost, fmt.Sprintf("/api/admin/orders/%d/mark-paid", card.ID), nil)
	if cardReply.code >= 200 && cardReply.code < 300 {
		t.Errorf("mark-paid accepted a card order (%d)", cardReply.code)
	}
	if got := shopOrderRow(t, h, card.Number).Status; got != core.OrderStatusPending {
		t.Errorf("card order status after refused mark-paid = %q, want pending", got)
	}

	shopWantOK(t, adm.send(http.MethodPost, fmt.Sprintf("/api/admin/orders/%d/mark-paid", transfer.ID), nil), "mark-paid bank transfer")
	paid := shopOrderRow(t, h, transfer.Number)
	if paid.Status != core.OrderStatusPaid || paid.PaidAt == nil {
		t.Errorf("transfer order after mark-paid: status=%q paid_at set=%v, want paid and set", paid.Status, paid.PaidAt != nil)
	}
}

func TestOrdersAdminNotesAddressAndTimeline(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	admin := h.user("admin", "notes")
	adm := newShopBrowser(h)
	adm.token = h.token(admin)
	order := shopSeedOrder(t, h, shopOrderSeed{
		number: fmt.Sprintf("JL-%d-0001", time.Now().Year()), email: "notes@example.test", status: "pending", total: 12000,
		shipping: `{"recipient":"Old Name","address":"Old Street 1"}`,
	})
	eventsBefore := shopCount(t, h, "SELECT COUNT(*) FROM order_events WHERE order_id = ?", order.ID)

	shopWantOK(t, adm.send(http.MethodPost, fmt.Sprintf("/api/admin/orders/%d/notes", order.ID), map[string]any{
		"body": "note-marker-7731", "note": "note-marker-7731",
	}), "POST notes")
	if after := shopCount(t, h, "SELECT COUNT(*) FROM order_events WHERE order_id = ?", order.ID); after <= eventsBefore {
		t.Errorf("order_events grew from %d to %d after a note, want more", eventsBefore, after)
	}

	shopWantOK(t, adm.send(http.MethodPatch, fmt.Sprintf("/api/admin/orders/%d/address", order.ID), map[string]any{
		"recipient": "New Name", "name": "New Name", "address": "New Street 9",
	}), "PATCH address")
	if n := shopCount(t, h, "SELECT COUNT(*) FROM orders WHERE id = ? AND shipping::text LIKE ?", order.ID, "%New Street 9%"); n != 1 {
		t.Errorf("shipping address was not updated to New Street 9")
	}

	r := adm.get(fmt.Sprintf("/api/admin/orders/%d", order.ID))
	shopWantCode(t, r, "GET /api/admin/orders/:id", http.StatusOK)
	var detail struct {
		Timeline []json.RawMessage `json:"timeline"`
	}
	shopDecode(t, r, "GET /api/admin/orders/:id", &detail)
	if len(detail.Timeline) == 0 {
		t.Error("order detail has no timeline")
	}
	if !strings.Contains(r.body, "note-marker-7731") {
		t.Error("order detail does not show the internal note in the timeline")
	}
}

func TestOrdersAdminRoleGateAndReadOnlyToken(t *testing.T) {
	// The read-only token sub-case needs the orders_read scope of ticket 06. It is reported as skipped on every run.
	t.Run("read-only token (ticket 06 orders_read scope)", func(t *testing.T) {
		t.Skip("out of scope here: the read-only token scope comes from ticket 06")
	})

	h := newAccessHarness(t)
	shopSetup(t, h)
	admin := h.user("admin", "gate-admin")
	editor := h.user("editor", "gate-editor")
	author := h.user("author", "gate-author")
	order := shopSeedOrder(t, h, shopOrderSeed{number: fmt.Sprintf("JL-%d-0001", time.Now().Year()), email: "gate@example.test", status: "pending", total: 12000})
	path := fmt.Sprintf("/api/admin/orders/%d", order.ID)

	adm := newShopBrowser(h)
	adm.token = h.token(admin)
	shopWantCode(t, adm.get(path), "admin GET order detail", http.StatusOK)

	ed := newShopBrowser(h)
	ed.token = h.token(editor)
	shopWantCode(t, ed.get(path), "editor GET order detail", http.StatusForbidden)

	au := newShopBrowser(h)
	au.token = h.token(author)
	shopWantCode(t, au.get(path), "author GET order detail", http.StatusForbidden)

}

func TestOrdersAdminTrashedProductKeepsOrderSnapshot(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	admin := h.user("admin", "snapshot")
	adm := newShopBrowser(h)
	adm.token = h.token(admin)
	product := shopSeedProduct(t, h, "trash-tea", 12000, 5)
	variant := shopVariantRows(t, h, product.ID)[0]
	order := shopSeedOrder(t, h, shopOrderSeed{
		number: fmt.Sprintf("JL-%d-0001", time.Now().Year()), email: "snap@example.test", status: "completed", total: 12000,
		items: []shopItemSeed{{variantID: &variant.ID, productID: &product.ID, title: "Snapshot Tea", qty: 1, unitPrice: 12000}},
	})
	if err := h.db.Delete(&core.Product{}, product.ID).Error; err != nil {
		t.Fatalf("trash product: %v", err)
	}

	r := adm.get(fmt.Sprintf("/api/admin/orders/%d", order.ID))
	shopWantCode(t, r, "GET order detail after the product was trashed", http.StatusOK)
	var detail struct {
		Items []struct {
			Title string `json:"title"`
		} `json:"items"`
	}
	shopDecode(t, r, "GET order detail", &detail)
	if len(detail.Items) != 1 || detail.Items[0].Title != "Snapshot Tea" {
		t.Errorf("order items after trashing the product = %+v, want the snapshot title Snapshot Tea", detail.Items)
	}
}

func TestShopSettingsMaskedAndPartialKeysCannotEnable(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	shopSetOptions(t, h, map[string]string{
		"payment_ecpay_merchant_id": "2000132",
		"payment_ecpay_hash_key":    "hk-live-abcdef1234",
		"payment_ecpay_hash_iv":     "iv-live-zz5678",
	})
	admin := h.user("admin", "settings")
	editor := h.user("editor", "settings-editor")
	adm := newShopBrowser(h)
	adm.token = h.token(admin)

	before := shopOptionsDump(t, h)
	r := adm.get("/api/admin/shop/settings")
	shopWantCode(t, r, "GET /api/admin/shop/settings", http.StatusOK)
	if !strings.Contains(r.body, "****1234") {
		t.Errorf("settings do not show the hash key masked as ****1234")
	}
	if strings.Contains(r.body, "hk-live-abcdef1234") || strings.Contains(r.body, "iv-live-zz5678") {
		t.Errorf("settings response contains a gateway key in clear text")
	}
	if after := shopOptionsDump(t, h); after != before {
		t.Error("GET /api/admin/shop/settings changed the options table; GET must never write")
	}

	ed := newShopBrowser(h)
	ed.token = h.token(editor)
	shopWantCode(t, ed.get("/api/admin/shop/settings"), "editor GET settings", http.StatusForbidden)

	// Turning ECPay on with only one of its keys is refused and enables nothing.
	partial := adm.send(http.MethodPut, "/api/admin/shop/settings", map[string]any{
		"payment_ecpay_enabled":  "true",
		"payment_ecpay_hash_key": "partial-key-0001",
	})
	shopWantCode(t, partial, "PUT settings with one ECPay key", http.StatusBadRequest)
	if n := shopCount(t, h, "SELECT COUNT(*) FROM options WHERE key = ? AND value = ?", "payment_ecpay_enabled", "true"); n != 0 {
		t.Errorf("ECPay was enabled with a partial key set")
	}
}
