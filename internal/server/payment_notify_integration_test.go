package server

// Payment notification integration tests for ticket 07, section E6 (contract: 付款通知, 沙盒, 轉帳 and the retry
// route in docs/tickets/07-shop.md). They post signed sandbox notifications to POST /api/payment/:gateway/notify
// against the dedicated PostgreSQL database and are skipped unless JELLY_TEST_POSTGRES=1. They must fail until the
// implementation exists. Do not edit them to match an implementation.
//
// Sandbox signing (the ticket names the scheme only loosely; this file is the contract for it): the option
// payment_sandbox_secret holds a hex key, the HMAC-SHA256 key is its hex decoding, and the signed string is the
// sorted key=value pairs of every form field except signature, joined by "&". The form fields are
// merchant_trade_no, status (paid, failed or pending), amount (minor units, decimal), currency, nonce and signature.
// The nonce is the dedupe key of the notification.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

// shopSignSandbox returns the signature of a sandbox notification (see the file comment).
func shopSignSandbox(t *testing.T, values url.Values) string {
	t.Helper()
	key, err := hex.DecodeString(shopSandboxSecret)
	if err != nil {
		t.Fatalf("decode sandbox secret: %v", err)
	}
	keys := make([]string, 0, len(values))
	for name := range values {
		if name != "signature" {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, name := range keys {
		parts = append(parts, name+"="+values.Get(name))
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(strings.Join(parts, "&")))
	return hex.EncodeToString(mac.Sum(nil))
}

// shopSandboxNotice returns a correctly signed sandbox notification form.
func shopSandboxNotice(t *testing.T, trade, status string, amount int64, currency, nonce string) url.Values {
	t.Helper()
	values := url.Values{}
	values.Set("merchant_trade_no", trade)
	values.Set("status", status)
	values.Set("amount", strconv.FormatInt(amount, 10))
	values.Set("currency", currency)
	values.Set("nonce", nonce)
	values.Set("signature", shopSignSandbox(t, values))
	return values
}

// shopNotify posts a form notification to the sandbox gateway. Gateways send no browser CSRF proof.
func shopNotify(b *shopBrowser, values url.Values) shopResponse {
	return b.do(http.MethodPost, "/api/payment/sandbox/notify", values.Encode(), "application/x-www-form-urlencoded", nil)
}

// shopCount runs a COUNT(*) query with arguments.
func shopCount(t *testing.T, h *accessHarness, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := h.db.Raw(query, args...).Scan(&n).Error; err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return n
}

// shopPlacedOrder is an order created through checkout, with the values the payment tests need.
type shopPlacedOrder struct {
	number  string
	total   int64
	trade   string
	access  string
	kind    string
	orderID uint
}

func shopPlaceOrder(t *testing.T, h *accessHarness, variantID uint, email, method, key string) shopPlacedOrder {
	t.Helper()
	_, result := shopPlaceCheckout(t, h, variantID, 1, email, method, key)
	order := shopOrderRow(t, h, result.Order.Number)
	var trade string
	if err := h.db.Raw("SELECT merchant_trade_no FROM payments WHERE order_id = ? ORDER BY id DESC LIMIT 1", order.ID).Scan(&trade).Error; err != nil || trade == "" {
		t.Fatalf("order %s has no payments row with a merchant_trade_no (err %v)", result.Order.Number, err)
	}
	return shopPlacedOrder{number: result.Order.Number, total: result.Order.Total, trade: trade, access: result.AccessToken, kind: result.Payment.Kind, orderID: order.ID}
}

// shopPaymentFixture seeds the shop settings and the staff user who receives order notifications.
func shopPaymentFixture(t *testing.T, h *accessHarness) {
	t.Helper()
	shopSetup(t, h)
	h.user("admin", "payowner")
}

// shopPaymentVariant seeds one product for the tests that place orders through checkout.
func shopPaymentVariant(t *testing.T, h *accessHarness) uint {
	t.Helper()
	p := shopSeedProduct(t, h, "pay-tee", 12000, 50)
	return shopVariantRows(t, h, p.ID)[0].ID
}

// shopTrade returns a 20-character merchant_trade_no for the n-th fixture.
func shopTrade(n int) string {
	return fmt.Sprintf("SBX%017d", n)
}

// shopSeedPayableOrder inserts what checkout leaves behind for a card order: a pending order and one pending
// sandbox payment attempt. Notification tests use it so they do not depend on the cart or checkout code.
func shopSeedPayableOrder(t *testing.T, h *accessHarness, index int, email string, total int64) shopPlacedOrder {
	t.Helper()
	number := fmt.Sprintf("JL-%d-%04d", time.Now().Year(), index)
	trade := shopTrade(index)
	order := shopSeedOrder(t, h, shopOrderSeed{number: number, email: email, status: core.OrderStatusPending, provider: "sandbox", method: "credit_card", total: total})
	if err := h.db.Create(&core.Payment{OrderID: order.ID, Provider: "sandbox", Method: "credit_card", MerchantTradeNo: trade, Amount: total, Currency: "TWD", Status: "pending"}).Error; err != nil {
		t.Fatalf("seed payment for %s: %v", number, err)
	}
	return shopPlacedOrder{number: number, total: total, trade: trade, orderID: order.ID}
}

func TestNotifySignedPaidMarksPaidAndEmitsOrderNew(t *testing.T) {
	h := newAccessHarness(t)
	shopPaymentFixture(t, h)
	placed := shopSeedPayableOrder(t, h, 1, "payer@example.test", 12000)

	values := shopSandboxNotice(t, placed.trade, "paid", placed.total, "TWD", "nonce-paid-1")
	shopWantCode(t, shopNotify(newShopBrowser(h), values), "signed paid notification", http.StatusOK)

	order := shopOrderRow(t, h, placed.number)
	if order.Status != core.OrderStatusPaid {
		t.Errorf("order status = %q, want paid", order.Status)
	}
	if order.PaidAt == nil {
		t.Error("order paid_at is not set")
	}
	if n := shopCount(t, h, "SELECT COUNT(*) FROM payment_events WHERE provider = ? AND verified = true", "sandbox"); n != 1 {
		t.Errorf("verified payment_events = %d, want 1", n)
	}
	if n := shopCount(t, h, "SELECT COUNT(*) FROM notification_deliveries WHERE kind = ?", "order.new"); n != 1 {
		t.Errorf("order.new notifications = %d, want 1 for the one administrator", n)
	}
}

func TestNotifyDuplicateAcknowledgedOnce(t *testing.T) {
	h := newAccessHarness(t)
	shopPaymentFixture(t, h)
	placed := shopSeedPayableOrder(t, h, 2, "dup@example.test", 12000)

	values := shopSandboxNotice(t, placed.trade, "paid", placed.total, "TWD", "nonce-dup-1")
	shopWantCode(t, shopNotify(newShopBrowser(h), values), "first notification", http.StatusOK)
	shopWantCode(t, shopNotify(newShopBrowser(h), values), "duplicate notification", http.StatusOK)

	if n := shopCount(t, h, "SELECT COUNT(*) FROM payment_events WHERE provider = ?", "sandbox"); n != 1 {
		t.Errorf("payment_events rows after a duplicate = %d, want 1", n)
	}
	if n := shopCount(t, h, "SELECT COUNT(*) FROM notification_deliveries WHERE kind = ?", "order.new"); n != 1 {
		t.Errorf("order.new notifications after a duplicate = %d, want 1", n)
	}
	if order := shopOrderRow(t, h, placed.number); order.Status != core.OrderStatusPaid {
		t.Errorf("order status = %q, want paid", order.Status)
	}
}

func TestNotifyAmountMismatchOrBadSignatureRejected(t *testing.T) {
	h := newAccessHarness(t)
	shopPaymentFixture(t, h)
	// The control order proves the gateway accepts a correct notification, so the rejections below are not just a
	// gateway that refuses everything.
	control := shopSeedPayableOrder(t, h, 7, "control@example.test", 12000)
	shopWantCode(t, shopNotify(newShopBrowser(h), shopSandboxNotice(t, control.trade, "paid", control.total, "TWD", "nonce-control")),
		"control: correctly signed notification", http.StatusOK)

	placed := shopSeedPayableOrder(t, h, 3, "mismatch@example.test", 12000)
	b := newShopBrowser(h)

	shopWantCode(t, shopNotify(b, shopSandboxNotice(t, placed.trade, "paid", placed.total+1, "TWD", "nonce-amount")),
		"notification with a wrong amount", http.StatusBadRequest)
	shopWantCode(t, shopNotify(b, shopSandboxNotice(t, placed.trade, "paid", placed.total, "USD", "nonce-currency")),
		"notification with a wrong currency", http.StatusBadRequest)
	bad := shopSandboxNotice(t, placed.trade, "paid", placed.total, "TWD", "nonce-sig")
	bad.Set("signature", "00000000000000000000000000000000000000000000000000000000deadbeef")
	shopWantCode(t, shopNotify(b, bad), "notification with a bad signature", http.StatusBadRequest)

	if order := shopOrderRow(t, h, placed.number); order.Status != core.OrderStatusPending {
		t.Errorf("order status after rejected notifications = %q, want pending", order.Status)
	}
	if n := shopCount(t, h, "SELECT COUNT(*) FROM payment_events WHERE verified = true AND payload::text LIKE ?", "%"+placed.trade+"%"); n != 0 {
		t.Errorf("verified payment_events for the rejected order = %d, want 0", n)
	}
}

func TestNotifyUnknownOrder404NoDetail(t *testing.T) {
	h := newAccessHarness(t)
	shopPaymentFixture(t, h)
	const trade = "UNKNOWNTRADE00000001"
	r := shopNotify(newShopBrowser(h), shopSandboxNotice(t, trade, "paid", 12000, "TWD", "nonce-unknown"))
	shopWantCode(t, r, "signed notification for an unknown order", http.StatusNotFound)
	if strings.Contains(r.body, trade) {
		t.Errorf("404 body echoes the trade number: %q", r.body)
	}
}

func TestNotifyFailureKeepsOrderRetryable(t *testing.T) {
	h := newAccessHarness(t)
	shopPaymentFixture(t, h)
	var admin core.User
	if err := h.db.Where("role = ?", "admin").First(&admin).Error; err != nil {
		t.Fatalf("load order notification admin: %v", err)
	}
	if err := h.db.Create(&core.NotificationPref{
		UserID: admin.ID, Event: "order.payment_failed", InApp: true, Email: true, Webhook: false,
	}).Error; err != nil {
		t.Fatalf("enable admin payment-failed email notification: %v", err)
	}
	placed := shopSeedPayableOrder(t, h, 4, "failed@example.test", 12000)

	shopWantCode(t, shopNotify(newShopBrowser(h), shopSandboxNotice(t, placed.trade, "failed", placed.total, "TWD", "nonce-failed")),
		"failed-payment notification", http.StatusOK)

	order := shopOrderRow(t, h, placed.number)
	if order.Status != core.OrderStatusPending {
		t.Errorf("order status after a failed payment = %q, want pending (retryable, not cancelled)", order.Status)
	}
	if n := shopCount(t, h, "SELECT COUNT(*) FROM payments WHERE order_id = ? AND status = ?", placed.orderID, "failed"); n != 1 {
		t.Errorf("payments with status failed = %d, want 1", n)
	}
	if n := shopCount(t, h, "SELECT COUNT(*) FROM notification_deliveries WHERE kind = ?", "order.payment_failed"); n != 1 {
		t.Errorf("order.payment_failed notifications = %d, want 1", n)
	}
	if n := shopCount(t, h, "SELECT COUNT(*) FROM order_events WHERE order_id = ? AND to_status = ?", placed.orderID, core.OrderStatusCancelled); n != 0 {
		t.Errorf("a failed payment cancelled the order (%d cancelled events)", n)
	}
}

func TestNotifyGETRejectedAndReturnURLNeverChangesState(t *testing.T) {
	h := newAccessHarness(t)
	shopPaymentFixture(t, h)
	placed := shopSeedPayableOrder(t, h, 5, "getcheck@example.test", 12000)
	values := shopSandboxNotice(t, placed.trade, "paid", placed.total, "TWD", "nonce-get")
	b := newShopBrowser(h)

	if r := b.get("/api/payment/sandbox/notify?" + values.Encode()); r.code == http.StatusOK {
		t.Errorf("GET notify answered 200; notifications must be POST only")
	}
	back := b.get("/api/payment/sandbox/return?" + values.Encode())
	if back.code != http.StatusFound {
		t.Fatalf("GET return = %d, want 302 to the order page; body=%q", back.code, back.body)
	}
	if loc := back.header.Get("Location"); !strings.HasPrefix(loc, "/checkout/done/"+placed.number) {
		t.Errorf("return Location = %q, want /checkout/done/%s", loc, placed.number)
	}
	if order := shopOrderRow(t, h, placed.number); order.Status != core.OrderStatusPending {
		t.Errorf("order status after GET requests = %q, want pending; the return page must not change orders", order.Status)
	}
}

func TestNotifyLateSuccessAfterExpiryFlagsNeedsRefund(t *testing.T) {
	h := newAccessHarness(t)
	shopPaymentFixture(t, h)
	placed := shopSeedPayableOrder(t, h, 6, "late@example.test", 12000)
	past := time.Now().Add(-time.Minute)
	if err := h.db.Model(&core.Order{}).Where("id = ?", placed.orderID).Updates(map[string]any{"status": core.OrderStatusExpired, "expires_at": past}).Error; err != nil {
		t.Fatalf("expire order: %v", err)
	}

	shopWantCode(t, shopNotify(newShopBrowser(h), shopSandboxNotice(t, placed.trade, "paid", placed.total, "TWD", "nonce-late")),
		"late paid notification", http.StatusOK)

	if order := shopOrderRow(t, h, placed.number); order.Status != core.OrderStatusExpired {
		t.Errorf("expired order moved to %q; expired is terminal", order.Status)
	}
	if n := shopCount(t, h, "SELECT COUNT(*) FROM order_events WHERE order_id = ? AND kind LIKE ?", placed.orderID, "%refund%"); n != 1 {
		t.Errorf("refund-flag events for the late payment = %d, want 1", n)
	}
	if n := shopCount(t, h, "SELECT COUNT(*) FROM payment_events WHERE verified = true"); n != 1 {
		t.Errorf("verified payment_events = %d, want the late payment recorded", n)
	}
}

func TestManualTransferStaysPendingUntilAdminMarkPaid(t *testing.T) {
	h := newAccessHarness(t)
	shopPaymentFixture(t, h)
	variantID := shopPaymentVariant(t, h)
	admin := h.user("admin", "offlineadmin")
	placed := shopPlaceOrder(t, h, variantID, "transfer@example.test", "bank_transfer", "transfer-1")
	if placed.kind != "offline" {
		t.Errorf("bank transfer payment kind = %q, want offline", placed.kind)
	}

	values := shopSandboxNotice(t, placed.trade, "paid", placed.total, "TWD", "nonce-transfer")
	if r := shopNotify(newShopBrowser(h), values); r.code == http.StatusOK {
		t.Errorf("a provider notification confirmed a manual transfer (status 200)")
	}
	if order := shopOrderRow(t, h, placed.number); order.Status != core.OrderStatusPending {
		t.Fatalf("order status before mark-paid = %q, want pending", order.Status)
	}

	adm := newShopBrowser(h)
	adm.token = h.token(admin)
	shopWantCode(t, adm.send(http.MethodPost, "/api/admin/orders/"+shopIDText(placed.orderID)+"/mark-paid", nil), "admin mark-paid", http.StatusOK)
	if order := shopOrderRow(t, h, placed.number); order.Status != core.OrderStatusPaid {
		t.Errorf("order status after mark-paid = %q, want paid", order.Status)
	}
}

func TestCheckoutRetryNewPaymentAttempt(t *testing.T) {
	h := newAccessHarness(t)
	shopPaymentFixture(t, h)
	variantID := shopPaymentVariant(t, h)
	placed := shopPlaceOrder(t, h, variantID, "retry@example.test", "credit_card", "retry-1")
	shopWantCode(t, shopNotify(newShopBrowser(h), shopSandboxNotice(t, placed.trade, "failed", placed.total, "TWD", "nonce-retry-failed")),
		"failed-payment notification", http.StatusOK)

	b := newShopBrowser(h)
	shopWantOK(t, b.send(http.MethodPost, "/api/checkout/retry", map[string]any{
		"number": placed.number, "access": placed.access, "method": "atm",
	}), "POST /api/checkout/retry")

	if order := shopOrderRow(t, h, placed.number); order.Status != core.OrderStatusPending {
		t.Errorf("order status after retry = %q, want pending", order.Status)
	}
	if n := shopCount(t, h, "SELECT COUNT(*) FROM payments WHERE order_id = ?", placed.orderID); n != 2 {
		t.Errorf("payments for the order = %d, want 2 (the failed attempt and the retry)", n)
	}
	if n := shopCount(t, h, "SELECT COUNT(DISTINCT merchant_trade_no) FROM payments WHERE order_id = ?", placed.orderID); n != 2 {
		t.Errorf("distinct merchant_trade_no values = %d, want 2", n)
	}
}

func TestOrderStatusEndpointNeedsOwnerOrToken(t *testing.T) {
	h := newAccessHarness(t)
	shopPaymentFixture(t, h)
	variantID := shopPaymentVariant(t, h)
	placed := shopPlaceOrder(t, h, variantID, "status@example.test", "credit_card", "status-1")

	anonymous := newShopBrowser(h)
	if r := anonymous.get("/api/orders/" + placed.number + "/status"); r.code != http.StatusUnauthorized && r.code != http.StatusForbidden && r.code != http.StatusNotFound {
		t.Errorf("anonymous status request = %d, want 401, 403 or 404; body=%q", r.code, r.body)
	}

	withToken := newShopBrowser(h)
	r := withToken.get("/api/orders/" + placed.number + "/status?t=" + url.QueryEscape(placed.access))
	shopWantCode(t, r, "status request with the access token", http.StatusOK)
	var status struct {
		Status string `json:"status"`
	}
	shopDecode(t, r, "status request with the access token", &status)
	if status.Status != core.OrderStatusPending {
		t.Errorf("status = %q, want pending", status.Status)
	}
}
