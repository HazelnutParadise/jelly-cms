package server

// Checkout integration tests for ticket 07, section E5 (contract: POST /api/checkout, stock reservation, the order
// number and the customer account rule in docs/tickets/07-shop.md). They run through the public routes against the
// dedicated PostgreSQL database and are skipped unless JELLY_TEST_POSTGRES=1. They must fail until the implementation
// exists. Do not edit them to match an implementation.
//
// Assumptions the implementation must meet (the ticket does not name them): user_invites and users.status come from
// ticket 06 and are read with raw SQL; the notification table is notification_deliveries (kind, to_address).
// The expiry sweep has no name in the contract, so the sweep here is the sequence pending to expired, Release and
// ReleaseRedemption, run twice.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

var shopOrderNumberPattern = regexp.MustCompile(`^JL-(\d{4})-(\d{4})$`)

// shopSweepPendingOrder is the expiry sweep as this file drives it. A pending order moves to expired and its stock
// and coupon are released. Release and ReleaseRedemption run on every call, so running the sweep twice must release
// once.
func shopSweepPendingOrder(t *testing.T, h *accessHarness, orderID uint) {
	t.Helper()
	tx := h.db.Begin()
	var status string
	if err := tx.Raw("SELECT status FROM orders WHERE id = ? FOR UPDATE", orderID).Scan(&status).Error; err != nil {
		tx.Rollback()
		t.Fatalf("lock order %d: %v", orderID, err)
	}
	if status == core.OrderStatusPending {
		if err := core.NewOrderService(h.db).Transition(tx, orderID, core.OrderStatusPending, core.OrderStatusExpired, nil, "expired"); err != nil {
			tx.Rollback()
			t.Fatalf("expire order %d: %v", orderID, err)
		}
	}
	if err := core.Release(tx, orderID); err != nil {
		tx.Rollback()
		t.Fatalf("release stock of order %d: %v", orderID, err)
	}
	if err := core.ReleaseRedemption(tx, orderID); err != nil {
		tx.Rollback()
		t.Fatalf("release coupon of order %d: %v", orderID, err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit sweep of order %d: %v", orderID, err)
	}
}

// shopPlaceCheckout adds variantID x qty to a new browser's cart and posts a checkout. It fails the test unless the
// checkout answers 201.
func shopPlaceCheckout(t *testing.T, h *accessHarness, variantID uint, qty int, email, method, key string) (*shopBrowser, shopCheckoutResult) {
	t.Helper()
	b := newShopBrowser(h)
	shopAddToCart(t, b, variantID, qty)
	r := b.send(http.MethodPost, "/api/checkout", shopCheckoutBody(email, method, key))
	shopWantCode(t, r, "POST /api/checkout "+key, http.StatusCreated)
	var result shopCheckoutResult
	shopDecode(t, r, "POST /api/checkout "+key, &result)
	return b, result
}

func TestCheckoutTotalsComputedServerSide(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "totals-tee", 12000, 10)
	v := shopVariantRows(t, h, p.ID)[0]

	b := newShopBrowser(h)
	shopAddToCart(t, b, v.ID, 2)
	body := shopCheckoutBody("totals@example.test", "credit_card", "totals-1")
	body["total"] = 1
	body["shipping_fee"] = 1
	body["subtotal"] = 1
	body["discount_total"] = 999
	body["unit_price"] = 1
	body["price"] = 1
	r := b.send(http.MethodPost, "/api/checkout", body)
	shopWantCode(t, r, "POST /api/checkout with client totals", http.StatusCreated)
	var result shopCheckoutResult
	shopDecode(t, r, "POST /api/checkout", &result)

	// subtotal 24000 (2 x 12000) + home delivery 8000 - discount 0.
	if result.Order.Total != 32000 {
		t.Errorf("order total = %d, want 32000 (24000 + 8000 - 0)", result.Order.Total)
	}
	order := shopOrderRow(t, h, result.Order.Number)
	if order.Subtotal != 24000 || order.ShippingTotal != 8000 || order.DiscountTotal != 0 || order.TotalAmount != 32000 {
		t.Errorf("stored totals subtotal=%d shipping=%d discount=%d total=%d, want 24000, 8000, 0, 32000",
			order.Subtotal, order.ShippingTotal, order.DiscountTotal, order.TotalAmount)
	}
}

func TestCheckoutIdempotencyKeyReturnsSameOrder(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "idem-tee", 12000, 10)
	v := shopVariantRows(t, h, p.ID)[0]

	b := newShopBrowser(h)
	shopAddToCart(t, b, v.ID, 1)
	body := shopCheckoutBody("idem@example.test", "credit_card", "same-key-1")
	first := b.send(http.MethodPost, "/api/checkout", body)
	shopWantCode(t, first, "first POST /api/checkout", http.StatusCreated)
	var firstResult shopCheckoutResult
	shopDecode(t, first, "first POST /api/checkout", &firstResult)

	second := b.send(http.MethodPost, "/api/checkout", body)
	if second.code != http.StatusOK && second.code != http.StatusCreated {
		t.Fatalf("repeat POST with the same idempotency_key returned %d, want the existing order (200 or 201); body=%q", second.code, second.body)
	}
	var secondResult shopCheckoutResult
	shopDecode(t, second, "repeat POST /api/checkout", &secondResult)
	if secondResult.Order.Number != firstResult.Order.Number {
		t.Errorf("repeat returned order %s, want the same order %s", secondResult.Order.Number, firstResult.Order.Number)
	}
	var orders int64
	if err := h.db.Model(&core.Order{}).Where("idempotency_key = ?", "same-key-1").Count(&orders).Error; err != nil {
		t.Fatalf("count orders by key: %v", err)
	}
	if orders != 1 {
		t.Errorf("%d orders carry the idempotency key, want 1", orders)
	}
}

func TestCheckoutCreatesDormantCustomerAndNeverTouchesExistingStaff(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "account-tee", 12000, 10)
	v := shopVariantRows(t, h, p.ID)[0]

	// A new address becomes a dormant customer: role customer, status invited, no password.
	shopPlaceCheckout(t, h, v.ID, 1, "newbie@example.test", "credit_card", "dormant-1")
	type account struct {
		Role     string
		Password string
	}
	var newbie account
	if err := h.db.Raw("SELECT role, password FROM users WHERE lower(email) = ?", "newbie@example.test").Scan(&newbie).Error; err != nil {
		t.Fatalf("read new customer: %v", err)
	}
	if newbie.Role != "customer" || newbie.Password != "" {
		t.Errorf("new customer role=%q password-set=%v, want role customer and no password", newbie.Role, newbie.Password != "")
	}
	var newbieStatus string
	if err := h.db.Raw("SELECT status FROM users WHERE lower(email) = ?", "newbie@example.test").Scan(&newbieStatus).Error; err != nil {
		t.Errorf("read status of new customer: %v", err)
	} else if newbieStatus != "invited" {
		t.Errorf("new customer status = %q, want invited", newbieStatus)
	}

	// An existing administrator keeps role, password and status when the same address checks out.
	staff := h.user("admin", "staffboss")
	var before account
	if err := h.db.Raw("SELECT role, password FROM users WHERE id = ?", staff.ID).Scan(&before).Error; err != nil {
		t.Fatalf("read staff before checkout: %v", err)
	}
	shopPlaceCheckout(t, h, v.ID, 1, staff.Email, "credit_card", "staff-1")
	var after account
	if err := h.db.Raw("SELECT role, password FROM users WHERE id = ?", staff.ID).Scan(&after).Error; err != nil {
		t.Fatalf("read staff after checkout: %v", err)
	}
	if after != before || after.Role != "admin" {
		t.Errorf("existing admin changed from %+v to %+v; role and password must stay", before, after)
	}
	var rows int64
	h.db.Model(&core.User{}).Where("lower(email) = ?", staff.Email).Count(&rows)
	if rows != 1 {
		t.Errorf("%d users with the staff email, want 1", rows)
	}
}

func TestCheckoutCreateAccountSendsInvite(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "invite-tee", 12000, 10)
	v := shopVariantRows(t, h, p.ID)[0]

	b := newShopBrowser(h)
	shopAddToCart(t, b, v.ID, 1)
	body := shopCheckoutBody("invitee@example.test", "credit_card", "invite-1")
	body["create_account"] = true
	shopWantCode(t, b.send(http.MethodPost, "/api/checkout", body), "POST /api/checkout create_account", http.StatusCreated)

	var user core.User
	if err := h.db.Where("lower(email) = ?", "invitee@example.test").First(&user).Error; err != nil {
		t.Fatalf("load invited customer: %v", err)
	}
	var invites []struct {
		Kind string
		Role string
	}
	if err := h.db.Raw("SELECT kind, role FROM user_invites WHERE user_id = ?", user.ID).Scan(&invites).Error; err != nil {
		t.Fatalf("read user_invites: %v", err)
	}
	if len(invites) != 1 || invites[0].Kind != "invite" || invites[0].Role != "customer" {
		t.Errorf("user_invites for the new customer = %+v, want one row kind invite role customer", invites)
	}
}

func TestCheckoutInvoiceValidation(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "invoice-tee", 12000, 100)
	v := shopVariantRows(t, h, p.ID)[0]

	cases := []struct {
		name    string
		invoice map[string]any
		field   string
	}{
		{"carrier too short", map[string]any{"kind": "personal_carrier", "carrier_code": "/abc"}, "carrier_code"},
		{"carrier lowercase", map[string]any{"kind": "personal_carrier", "carrier_code": "/abcdefg"}, "carrier_code"},
		{"tax id bad checksum", map[string]any{"kind": "tax_id", "tax_id": "12345678", "company_name": "測試股份有限公司"}, "tax_id"},
		{"tax id not 8 digits", map[string]any{"kind": "tax_id", "tax_id": "1234567", "company_name": "測試股份有限公司"}, "tax_id"},
		{"donation code too short", map[string]any{"kind": "donation", "donation_code": "12"}, "donation_code"},
		{"donation code too long", map[string]any{"kind": "donation", "donation_code": "12345678"}, "donation_code"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newShopBrowser(h)
			shopAddToCart(t, b, v.ID, 1)
			body := shopCheckoutBody("invoice-"+tc.field+"@example.test", "credit_card", "invoice-"+tc.name)
			body["invoice"] = tc.invoice
			r := b.send(http.MethodPost, "/api/checkout", body)
			shopWantCode(t, r, "POST /api/checkout "+tc.name, http.StatusBadRequest)
			if !strings.Contains(r.body, tc.field) {
				t.Errorf("400 body %q does not name the invalid field %q", r.body, tc.field)
			}
		})
	}
	t.Run("valid tax id 12345675 passes", func(t *testing.T) {
		b := newShopBrowser(h)
		shopAddToCart(t, b, v.ID, 1)
		body := shopCheckoutBody("taxid-ok@example.test", "credit_card", "invoice-valid")
		body["invoice"] = map[string]any{"kind": "tax_id", "tax_id": "12345675", "company_name": "測試股份有限公司"}
		shopWantCode(t, b.send(http.MethodPost, "/api/checkout", body), "POST /api/checkout valid tax id", http.StatusCreated)
	})
}

func TestCheckoutHoneypotOriginAndRateLimit(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "guard-tee", 12000, 100)
	v := shopVariantRows(t, h, p.ID)[0]

	honey := newShopBrowser(h)
	shopAddToCart(t, honey, v.ID, 1)
	body := shopCheckoutBody("honey@example.test", "credit_card", "honey-1")
	body["website"] = "http://spam.example"
	shopWantCode(t, honey.send(http.MethodPost, "/api/checkout", body), "POST /api/checkout with website filled", http.StatusBadRequest)

	foreign := newShopBrowser(h)
	shopAddToCart(t, foreign, v.ID, 1)
	fbody := shopCheckoutBody("foreign@example.test", "credit_card", "foreign-1")
	shopWantCode(t, foreign.sendWith(http.MethodPost, "/api/checkout", fbody, map[string]string{"Origin": "https://evil.example"}),
		"POST /api/checkout from a foreign Origin", http.StatusForbidden)

	burst := newShopBrowser(h)
	burst.remote = "203.0.113.77:5000"
	limited := false
	for i := 0; i < 40 && !limited; i++ {
		r := burst.send(http.MethodPost, "/api/checkout", shopCheckoutBody("burst@example.test", "credit_card", fmt.Sprintf("burst-%02d", i)))
		if i == 0 && r.code == http.StatusTooManyRequests {
			t.Fatal("the first checkout attempt from an IP was already limited")
		}
		limited = r.code == http.StatusTooManyRequests
	}
	if !limited {
		t.Error("40 checkout attempts from one IP never got 429")
	}
}

func TestCheckoutStockRaceOneWins(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "last-one", 12000, 1)
	v := shopVariantRows(t, h, p.ID)[0]

	buyers := []*shopBrowser{newShopBrowser(h), newShopBrowser(h)}
	for i, b := range buyers {
		b.remote = fmt.Sprintf("198.51.100.%d:40000", 20+i)
		shopAddToCart(t, b, v.ID, 1)
	}
	responses := make([]shopResponse, len(buyers))
	var wg sync.WaitGroup
	for i, b := range buyers {
		wg.Add(1)
		go func(i int, b *shopBrowser) {
			defer wg.Done()
			responses[i] = b.send(http.MethodPost, "/api/checkout", shopCheckoutBody(fmt.Sprintf("racer%d@example.test", i), "credit_card", fmt.Sprintf("race-%d", i)))
		}(i, b)
	}
	wg.Wait()

	created, conflicts := 0, 0
	for _, r := range responses {
		switch r.code {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			var body struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal([]byte(r.body), &body); err == nil && body.Code == "stock" {
				conflicts++
			} else {
				t.Errorf("409 body %q does not carry code stock", r.body)
			}
		default:
			t.Errorf("checkout returned %d, want 201 or 409; body=%q", r.code, r.body)
		}
	}
	if created != 1 || conflicts != 1 {
		t.Errorf("two buyers for the last unit: %d created and %d stock conflicts, want 1 and 1", created, conflicts)
	}

	var stock int
	if err := h.db.Raw("SELECT stock FROM product_variants WHERE id = ?", v.ID).Scan(&stock).Error; err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if stock != 0 {
		t.Errorf("stock = %d, want 0 after one reservation", stock)
	}
	var moved int64
	if err := h.db.Raw("SELECT COALESCE(SUM(delta), 0) FROM stock_movements WHERE variant_id = ?", v.ID).Scan(&moved).Error; err != nil {
		t.Fatalf("sum stock movements: %v", err)
	}
	if moved != int64(stock)-1 {
		t.Errorf("sum of stock_movements delta = %d, want %d (initial 1 + delta = stock %d)", moved, int64(stock)-1, stock)
	}
}

func TestCheckoutReservesStockAndExpirySweepReleasesOnce(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "hold-tee", 12000, 5)
	v := shopVariantRows(t, h, p.ID)[0]
	shopSeedCoupon(t, h, core.Coupon{Code: "ONCEHOLD", Kind: "percent", Value: 10})

	b := newShopBrowser(h)
	shopAddToCart(t, b, v.ID, 2)
	shopWantOK(t, b.send(http.MethodPost, "/api/cart/coupon", map[string]any{"code": "ONCEHOLD"}), "apply ONCEHOLD")
	body := shopCheckoutBody("hold@example.test", "credit_card", "hold-1")
	body["coupon_code"] = "ONCEHOLD"
	r := b.send(http.MethodPost, "/api/checkout", body)
	shopWantCode(t, r, "POST /api/checkout", http.StatusCreated)
	var result shopCheckoutResult
	shopDecode(t, r, "POST /api/checkout", &result)
	order := shopOrderRow(t, h, result.Order.Number)

	var stock int
	h.db.Raw("SELECT stock FROM product_variants WHERE id = ?", v.ID).Scan(&stock)
	if stock != 3 {
		t.Errorf("stock after reserve = %d, want 3", stock)
	}
	if order.ExpiresAt == nil {
		t.Fatal("credit-card order has no expires_at")
	}
	if hold := time.Until(*order.ExpiresAt); hold < 29*time.Minute || hold > 31*time.Minute {
		t.Errorf("credit-card hold = %v, want about 30 minutes", hold)
	}
	var reserved int64
	h.db.Model(&core.StockMovement{}).Where("order_id = ? AND reason = ? AND delta = ?", order.ID, "reserve", -2).Count(&reserved)
	if reserved != 1 {
		t.Errorf("%d reserve rows for the order, want 1 with delta -2", reserved)
	}

	if err := h.db.Exec("UPDATE orders SET expires_at = now() - interval '1 minute' WHERE id = ?", order.ID).Error; err != nil {
		t.Fatalf("move expires_at into the past: %v", err)
	}
	shopSweepPendingOrder(t, h, order.ID)
	shopSweepPendingOrder(t, h, order.ID)

	if err := h.db.Raw("SELECT stock FROM product_variants WHERE id = ?", v.ID).Scan(&stock).Error; err != nil || stock != 5 {
		t.Errorf("stock after the sweep ran twice = %d (err %v), want 5", stock, err)
	}
	var released int64
	h.db.Model(&core.StockMovement{}).Where("order_id = ? AND reason = ?", order.ID, "release").Count(&released)
	if released != 1 {
		t.Errorf("%d release rows after two sweeps, want 1", released)
	}
	var used int
	h.db.Raw("SELECT used_count FROM coupons WHERE lower(code) = 'oncehold'").Scan(&used)
	if used != 0 {
		t.Errorf("coupon used_count after the expired order = %d, want 0", used)
	}
	var reloaded core.Order
	h.db.First(&reloaded, order.ID)
	if reloaded.Status != core.OrderStatusExpired {
		t.Errorf("order status after the sweep = %q, want expired", reloaded.Status)
	}
	var expiredEvents int64
	h.db.Model(&core.OrderEvent{}).Where("order_id = ? AND to_status = ?", order.ID, core.OrderStatusExpired).Count(&expiredEvents)
	if expiredEvents != 1 {
		t.Errorf("%d order_events to expired, want 1", expiredEvents)
	}
}

func TestCheckoutShippingMethodsOnlyEnabled(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "ship-tee", 12000, 10)
	v := shopVariantRows(t, h, p.ID)[0]

	list := newShopBrowser(h)
	r := list.get("/api/shop/shipping-methods")
	shopWantCode(t, r, "GET /api/shop/shipping-methods", http.StatusOK)
	var methods []struct {
		ID string `json:"id"`
	}
	shopDecode(t, r, "GET /api/shop/shipping-methods", &methods)
	var ids []string
	for _, m := range methods {
		ids = append(ids, m.ID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"home", "pickup"}) {
		t.Errorf("enabled shipping methods = %v, want [home pickup] (cvs is disabled)", ids)
	}

	b := newShopBrowser(h)
	shopAddToCart(t, b, v.ID, 1)
	body := shopCheckoutBody("ship-cvs@example.test", "credit_card", "ship-cvs-1")
	body["shipping"] = map[string]any{"method": "cvs"}
	shopWantCode(t, b.send(http.MethodPost, "/api/checkout", body), "POST /api/checkout with disabled cvs", http.StatusBadRequest)
	var orders int64
	h.db.Model(&core.Order{}).Count(&orders)
	if orders != 0 {
		t.Errorf("%d orders were created with a disabled shipping method", orders)
	}
}

func TestCheckoutOrderNumberFormatAndYearlySequence(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "number-tee", 12000, 10)
	v := shopVariantRows(t, h, p.ID)[0]
	year := time.Now().Year()

	for i, want := range []int{1, 2} {
		_, result := shopPlaceCheckout(t, h, v.ID, 1, fmt.Sprintf("number%d@example.test", i), "credit_card", fmt.Sprintf("number-%d", i))
		match := shopOrderNumberPattern.FindStringSubmatch(result.Order.Number)
		if match == nil {
			t.Fatalf("order number %q does not match JL-YYYY-NNNN", result.Order.Number)
		}
		if match[1] != fmt.Sprint(year) {
			t.Errorf("order number year %s, want %d", match[1], year)
		}
		if want := fmt.Sprintf("%04d", want); match[2] != want {
			t.Errorf("order sequence %s, want %s", match[2], want)
		}
	}
	var counter core.OrderCounter
	if err := h.db.First(&counter, "year = ?", year).Error; err != nil {
		t.Fatalf("read order_counters for %d: %v", year, err)
	}
	if counter.Seq != 2 {
		t.Errorf("order_counters seq = %d, want 2", counter.Seq)
	}
}

func TestCheckoutTermsRequired(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "terms-tee", 12000, 10)
	v := shopVariantRows(t, h, p.ID)[0]

	b := newShopBrowser(h)
	shopAddToCart(t, b, v.ID, 1)
	body := shopCheckoutBody("terms@example.test", "credit_card", "terms-1")
	body["accept_terms"] = false
	shopWantCode(t, b.send(http.MethodPost, "/api/checkout", body), "POST /api/checkout without terms", http.StatusBadRequest)
	var orders int64
	h.db.Model(&core.Order{}).Count(&orders)
	if orders != 0 {
		t.Errorf("%d orders created without accepting terms", orders)
	}
}

func TestCheckoutEmptyCartRejected(t *testing.T) {
	h := archivesHarness(t)
	shopSetup(t, h)

	empty := newShopBrowser(h)
	if page := empty.get("/checkout"); page.code != http.StatusFound || page.header.Get("Location") != "/cart" {
		t.Errorf("GET /checkout with an empty cart = %d Location %q, want 302 to /cart", page.code, page.header.Get("Location"))
	}
	r := empty.send(http.MethodPost, "/api/checkout", shopCheckoutBody("empty@example.test", "credit_card", "empty-1"))
	shopWantCode(t, r, "POST /api/checkout with an empty cart", http.StatusBadRequest)
	var orders int64
	h.db.Model(&core.Order{}).Count(&orders)
	if orders != 0 {
		t.Errorf("%d orders created from an empty cart", orders)
	}
}
