package server

// Maintenance-mode payment callback integration tests for ticket 35
// (docs/tickets/35-maintenance-payment-callbacks.md). The contract is that, while maintenance is on, the three
// payment callback routes stay reachable: POST /api/payment/:gateway/notify, POST /api/payment/:gateway/callback
// and GET /api/payment/:gateway/return. Signed notifications must keep marking orders paid, a bad signature must
// still be rejected with 400, and every other anonymous API must keep answering 503.
//
// These tests run against the dedicated PostgreSQL database and are skipped unless JELLY_TEST_POSTGRES=1. They
// are written before maintenanceBypass allows /api/payment and are expected to fail on the current code: the
// callbacks answer 503 during maintenance. Do not edit them to match an implementation.

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

// TestMaintenancePaymentNotifyStillMarksPaid covers the main loss case: a provider notification that arrives
// while the site is in maintenance must still be accepted and mark the order paid.
func TestMaintenancePaymentNotifyStillMarksPaid(t *testing.T) {
	h := newAccessHarness(t)
	shopPaymentFixture(t, h)
	placed := shopSeedPayableOrder(t, h, 9101, "maintenance-paid@example.test", 12000)
	maintenanceEnable(t, h, 0)

	values := shopSandboxNotice(t, placed.trade, "paid", placed.total, "TWD", "nonce-maintenance-paid")
	shopWantCode(t, shopNotify(newShopBrowser(h), values),
		"signed paid notification during maintenance", http.StatusOK)

	order := shopOrderRow(t, h, placed.number)
	if order.Status != core.OrderStatusPaid {
		t.Errorf("order status during maintenance = %q, want %q", order.Status, core.OrderStatusPaid)
	}
	if order.PaidAt == nil {
		t.Error("order paid_at during maintenance is nil, want a timestamp")
	}
}

// TestMaintenancePaymentCallbackAliasReachable covers the second callback route, which goes through the same
// HandlePaymentCallback as /notify.
func TestMaintenancePaymentCallbackAliasReachable(t *testing.T) {
	h := newAccessHarness(t)
	shopPaymentFixture(t, h)
	placed := shopSeedPayableOrder(t, h, 9102, "maintenance-callback@example.test", 12000)
	maintenanceEnable(t, h, 0)

	values := shopSandboxNotice(t, placed.trade, "paid", placed.total, "TWD", "nonce-maintenance-callback")
	r := newShopBrowser(h).do(http.MethodPost, "/api/payment/sandbox/callback", values.Encode(),
		"application/x-www-form-urlencoded", nil)
	shopWantCode(t, r, "signed paid notification to the callback alias during maintenance", http.StatusOK)

	order := shopOrderRow(t, h, placed.number)
	if order.Status != core.OrderStatusPaid {
		t.Errorf("order status after the callback alias during maintenance = %q, want %q", order.Status, core.OrderStatusPaid)
	}
}

// TestMaintenancePaymentReturnNot503 covers the browser return URL. The route may redirect or answer 404 for an
// unknown trade, but it must not be swallowed by the maintenance gate.
func TestMaintenancePaymentReturnNot503(t *testing.T) {
	h := newAccessHarness(t)
	shopPaymentFixture(t, h)
	placed := shopSeedPayableOrder(t, h, 9103, "maintenance-return@example.test", 12000)
	maintenanceEnable(t, h, 0)

	query := url.Values{"merchant_trade_no": {placed.trade}}.Encode()
	r := newShopBrowser(h).get("/api/payment/sandbox/return?" + query)
	if r.panicked != nil {
		t.Fatalf("anonymous GET return during maintenance panicked: %v", r.panicked)
	}
	if r.code == http.StatusServiceUnavailable {
		t.Fatalf("anonymous GET return during maintenance returned 503, want anything else; body=%q", r.body)
	}
}

// TestMaintenancePaymentBadSignatureRejectedNot503 covers the rejection path: a bad signature must get the same
// 400 as with maintenance off, not the maintenance 503, and the order must stay pending.
func TestMaintenancePaymentBadSignatureRejectedNot503(t *testing.T) {
	h := newAccessHarness(t)
	shopPaymentFixture(t, h)
	placed := shopSeedPayableOrder(t, h, 9104, "maintenance-badsig@example.test", 12000)
	maintenanceEnable(t, h, 0)

	bad := shopSandboxNotice(t, placed.trade, "paid", placed.total, "TWD", "nonce-maintenance-badsig")
	bad.Set("signature", "00000000000000000000000000000000000000000000000000000000deadbeef")
	shopWantCode(t, shopNotify(newShopBrowser(h), bad),
		"bad-signature notification during maintenance", http.StatusBadRequest)

	order := shopOrderRow(t, h, placed.number)
	if order.Status != core.OrderStatusPending {
		t.Errorf("order status after a bad-signature notification during maintenance = %q, want %q", order.Status, core.OrderStatusPending)
	}
}

// TestMaintenanceStillBlocksOtherAnonymousAPI guards the bypass boundary: allowing /api/payment must not open
// other anonymous APIs.
func TestMaintenanceStillBlocksOtherAnonymousAPI(t *testing.T) {
	h := newAccessHarness(t)
	maintenanceEnable(t, h, 0)

	r := newShopBrowser(h).get("/api/comments?post_id=1")
	if r.panicked != nil {
		t.Fatalf("anonymous GET /api/comments during maintenance panicked: %v", r.panicked)
	}
	if r.code != http.StatusServiceUnavailable {
		t.Errorf("anonymous GET /api/comments during maintenance = %d, want %d; body=%q", r.code, http.StatusServiceUnavailable, r.body)
	}
}
