package server

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

var ecpayClientBackInput = regexp.MustCompile(`name="ClientBackURL"\s+value="([^"]*)"`)

func TestECPayClientBackUsesOrderScopedDoneCookie(t *testing.T) {
	h := archivesHarness(t)
	shopSetup(t, h)
	t.Setenv("APP_URL", "http://127.0.0.1:8080")
	shopSetOptions(t, h, map[string]string{
		"payment_methods":           `{"credit_card":"ecpay"}`,
		"payment_ecpay_enabled":     "true",
		"payment_ecpay_merchant_id": "LOCAL-TEST-MERCHANT",
		"payment_ecpay_hash_key":    "local-test-hash-key-only",
		"payment_ecpay_hash_iv":     "local-test-hash-iv-only",
		"payment_ecpay_test_mode":   "true",
	})

	type ecpayFixture struct {
		order core.Order
		trade string
		proof string
	}
	seed := func(index int, title string) ecpayFixture {
		t.Helper()
		proof := fmt.Sprintf("ecpay-checkout-proof-%d-0123456789", index)
		order := shopSeedOrder(t, h, shopOrderSeed{
			number: fmt.Sprintf("JL-%d-%04d", time.Now().Year(), 7200+index),
			email:  fmt.Sprintf("ecpay-%d@example.test", index), status: core.OrderStatusPending,
			provider: "ecpay", method: "credit_card", total: 12000,
			items: []shopItemSeed{{title: title, qty: 1, unitPrice: 12000}},
		})
		if err := h.db.Model(&core.Order{}).Where("id = ?", order.ID).Update("lookup_token_hash", shopSHA256Hex(proof)).Error; err != nil {
			t.Fatalf("set order proof hash: %v", err)
		}
		trade := shopTrade(7200 + index)
		if err := h.db.Create(&core.Payment{OrderID: order.ID, Provider: "ecpay", Method: "credit_card", MerchantTradeNo: trade, Amount: 12000, Currency: "TWD", Status: "pending"}).Error; err != nil {
			t.Fatalf("seed ECPay payment: %v", err)
		}
		return ecpayFixture{order: order, trade: trade, proof: proof}
	}
	first, second := seed(1, "private marker first order"), seed(2, "private marker second order")

	handoff := func(fx ecpayFixture, proof string) shopResponse {
		return newShopBrowser(h).get("/checkout/pay/" + fx.trade + "?t=" + url.QueryEscape(proof))
	}
	firstHandoff := handoff(first, first.proof)
	shopWantCode(t, firstHandoff, "valid ECPay handoff", http.StatusOK)
	match := ecpayClientBackInput.FindStringSubmatch(firstHandoff.body)
	if len(match) != 2 {
		t.Fatalf("ECPay handoff omitted hidden ClientBackURL field")
	}
	clientBackURL, err := url.Parse(html.UnescapeString(match[1]))
	if err != nil {
		t.Fatalf("parse ECPay ClientBackURL: %v", err)
	}
	query := clientBackURL.Query()
	if clientBackURL.Path != "/api/payment/ecpay/return" || query.Get("MerchantTradeNo") != first.trade {
		t.Errorf("ClientBackURL = %q, want the ECPay return route carrying MerchantTradeNo=%s", clientBackURL.String(), first.trade)
	}
	if query.Get("t") != "" || strings.Contains(clientBackURL.String(), first.proof) {
		t.Errorf("ClientBackURL exposed checkout proof: %q", clientBackURL.String())
	}
	firstCookie := ecpayDoneCookie(t, firstHandoff, first.order.Number)

	secondHandoff := handoff(second, second.proof)
	shopWantCode(t, secondHandoff, "second valid ECPay handoff", http.StatusOK)
	secondCookie := ecpayDoneCookie(t, secondHandoff, second.order.Number)
	if firstCookie.Name != secondCookie.Name {
		t.Errorf("order-scoped cookies use different names %q and %q", firstCookie.Name, secondCookie.Name)
	}
	if firstCookie.Path == secondCookie.Path || firstCookie.Path != "/checkout/done/"+first.order.Number || secondCookie.Path != "/checkout/done/"+second.order.Number {
		t.Errorf("cookie paths = %q and %q, want one path per order", firstCookie.Path, secondCookie.Path)
	}

	wrong := handoff(first, "wrong-proof")
	if wrong.code != http.StatusNotFound {
		t.Errorf("handoff with wrong checkout proof returned %d, want 404", wrong.code)
	}
	if len(wrong.cookies) != 0 {
		t.Errorf("wrong checkout proof set cookies: %v", wrong.header.Values("Set-Cookie"))
	}

	// ECPay's browser return only visits ClientBackURL. It does not post or append payment fields.
	ret := newShopBrowser(h).get(clientBackURL.RequestURI())
	shopWantCode(t, ret, "ECPay browser return", http.StatusFound)
	if loc := ret.header.Get("Location"); loc != "/checkout/done/"+first.order.Number {
		t.Errorf("ECPay return redirected to %q, want the matching done page", loc)
	}
	if current := shopOrderRow(t, h, first.order.Number); current.Status != core.OrderStatusPending || current.RefundedTotal != 0 {
		t.Errorf("browser return changed order state: status=%q refunded_total=%d", current.Status, current.RefundedTotal)
	}
	var paymentRow core.Payment
	if err := h.db.Where("merchant_trade_no = ?", first.trade).First(&paymentRow).Error; err != nil {
		t.Fatalf("reload payment after browser return: %v", err)
	}
	if paymentRow.Status != "pending" {
		t.Errorf("browser return changed payment status to %q, want pending", paymentRow.Status)
	}

	anonymous := newShopBrowser(h).get("/checkout/done/" + first.order.Number)
	if strings.Contains(anonymous.body, first.order.Number) || strings.Contains(anonymous.body, "private marker first order") || strings.Contains(anonymous.body, first.order.Email) {
		t.Errorf("anonymous done page exposed private order data: %q", anonymous.body)
	}
	if got := ecpayDonePageWithCookie(h, first.order.Number, firstCookie); !strings.Contains(got.body, "private marker first order") {
		t.Errorf("matching ECPay done cookie did not open its order")
	}
	if got := ecpayDonePageWithCookie(h, second.order.Number, firstCookie); strings.Contains(got.body, "private marker second order") || strings.Contains(got.body, second.order.Email) {
		t.Errorf("first order cookie exposed a different order")
	}
	if got := ecpayDonePageWithCookie(h, second.order.Number, secondCookie); !strings.Contains(got.body, "private marker second order") {
		t.Errorf("second ECPay done cookie did not open its order")
	}
}

func TestPaidConfirmationLookupURLExchangesIntoItsOwnOrder(t *testing.T) {
	h := archivesHarness(t)
	shopPaymentFixture(t, h)
	product := shopSeedProduct(t, h, "paid-confirmation-order-view", 12000, 10)
	variant := shopVariantRows(t, h, product.ID)[0]
	const email = "paid-confirmation-view@example.test"
	placed := shopPlaceOrder(t, h, variant.ID, email, "credit_card", "paid-confirmation-view-key")
	shopWantCode(t, shopNotify(newShopBrowser(h), shopSandboxNotice(t, placed.trade, "paid", placed.total, "TWD", "paid-confirmation-view-nonce")),
		"paid confirmation notification", http.StatusOK)

	var delivery core.NotificationDelivery
	if err := h.db.Where("kind = ? AND to_address = ?", "order.confirmation", email).First(&delivery).Error; err != nil {
		t.Fatalf("read paid confirmation outbox: %v", err)
	}
	var params struct {
		AccessURL string `json:"access_url"`
	}
	if err := json.Unmarshal(delivery.Params, &params); err != nil {
		t.Fatalf("decode paid confirmation outbox: %v", err)
	}
	lookupURL, err := url.Parse(params.AccessURL)
	if err != nil {
		t.Fatalf("parse paid confirmation access_url: %v", err)
	}
	if lookupURL.Path != "/orders/view" || lookupURL.Query().Get("t") == "" {
		t.Fatalf("paid confirmation access_url = %q, want /orders/view?t=<lookup token>", params.AccessURL)
	}
	if strings.Contains(lookupURL.Path, placed.number) {
		t.Fatalf("lookup token URL exposes order number in path: %q", params.AccessURL)
	}

	visitor := newShopBrowser(h)
	exchanged := visitor.get(lookupURL.RequestURI())
	shopWantCode(t, exchanged, "exchange paid confirmation token", http.StatusFound)
	if location := exchanged.header.Get("Location"); location != "/orders/view/"+placed.number {
		t.Errorf("exchange redirected to %q, want its own order %s", location, placed.number)
	}
	opened := visitor.get("/orders/view/" + placed.number)
	shopWantCode(t, opened, "open order with exchanged paid confirmation cookie", http.StatusOK)
	if !strings.Contains(opened.body, "paid-confirmation-order-view") {
		t.Errorf("exchanged confirmation token opened no matching order item")
	}
	replay := newShopBrowser(h).get(lookupURL.RequestURI())
	if replay.code == http.StatusFound {
		t.Errorf("one-use paid confirmation link was accepted twice: %q", replay.header.Get("Location"))
	}
}

func ecpayDoneCookie(t *testing.T, handoff shopResponse, number string) *http.Cookie {
	t.Helper()
	for _, cookie := range handoff.cookies {
		if cookie.Path == "/checkout/done/"+number {
			if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != 1800 || cookie.Value == "" {
				t.Fatalf("ECPay done cookie has wrong flags: %#v", cookie)
			}
			return cookie
		}
	}
	t.Fatalf("handoff did not set an order-path-scoped HttpOnly 30-minute cookie: %v", handoff.header.Values("Set-Cookie"))
	return nil
}

func ecpayDonePageWithCookie(h *accessHarness, number string, cookie *http.Cookie) shopResponse {
	b := newShopBrowser(h)
	return b.do(http.MethodGet, "/checkout/done/"+number, "", "", map[string]string{"Cookie": cookie.Name + "=" + cookie.Value})
}
