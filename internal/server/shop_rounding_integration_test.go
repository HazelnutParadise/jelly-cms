package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

func TestCheckoutRoundsDiscountedPayableAmountForEachPaymentKind(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "rounding-tee", 10500, 10)
	v := shopVariantRows(t, h, p.ID)[0]
	shopSeedCoupon(t, h, core.Coupon{Code: "ROUND10", Kind: "percent", Value: 10})

	for _, tc := range []struct {
		method string
		kind   string
	}{
		{method: "credit_card", kind: "redirect"},
		{method: "atm", kind: "redirect"},
		{method: "cvs", kind: "redirect"},
		{method: "bank_transfer", kind: "offline"},
	} {
		t.Run(tc.method, func(t *testing.T) {
			b := newShopBrowser(h)
			shopAddToCart(t, b, v.ID, 1)
			shopWantOK(t, b.send(http.MethodPost, "/api/cart/coupon", map[string]any{"code": "ROUND10"}), "apply ROUND10")
			body := shopCheckoutBody("round-"+tc.method+"@example.test", tc.method, "round-"+tc.method)
			body["coupon_code"] = "ROUND10"
			body["shipping"] = map[string]any{"method": "pickup", "name": "測試買家"}
			r := b.send(http.MethodPost, "/api/checkout", body)
			shopWantCode(t, r, "POST /api/checkout with fractional payable", http.StatusCreated)

			var result struct {
				Order struct {
					Number             string `json:"number"`
					Total              int64  `json:"total"`
					RoundingAdjustment int64  `json:"rounding_adjustment"`
				} `json:"order"`
				Payment struct {
					Kind string `json:"kind"`
				} `json:"payment"`
			}
			shopDecode(t, r, "rounded checkout response", &result)
			if result.Order.Total != 9500 || result.Order.RoundingAdjustment != 50 {
				t.Errorf("response total=%d adjustment=%d, want 9500 and 50", result.Order.Total, result.Order.RoundingAdjustment)
			}
			if result.Payment.Kind != tc.kind {
				t.Errorf("payment kind=%q, want %q", result.Payment.Kind, tc.kind)
			}

			order := shopOrderRow(t, h, result.Order.Number)
			if order.Subtotal != 10500 || order.DiscountTotal != 1050 || order.TotalAmount != 9500 || order.RoundingAdjustment != 50 {
				t.Errorf("stored subtotal=%d discount=%d adjustment=%d total=%d, want 10500, 1050, 50, 9500", order.Subtotal, order.DiscountTotal, order.RoundingAdjustment, order.TotalAmount)
			}
			var paymentAmounts []int64
			if err := h.db.Raw("SELECT amount FROM payments WHERE order_id = (SELECT id FROM orders WHERE number = ?) ORDER BY id", order.Number).Scan(&paymentAmounts).Error; err != nil {
				t.Fatalf("read payment attempts: %v", err)
			}
			if len(paymentAmounts) != 1 || paymentAmounts[0] != 9500 {
				t.Errorf("payment attempt amounts=%v, want [9500]", paymentAmounts)
			}

			// Replaying the idempotency key returns the same saved rounding snapshot.
			replay := b.send(http.MethodPost, "/api/checkout", body)
			if replay.code != http.StatusOK && replay.code != http.StatusCreated {
				t.Fatalf("checkout replay returned %d, want existing order; body=%q", replay.code, replay.body)
			}
			var replayResult struct {
				Order json.RawMessage `json:"order"`
			}
			shopDecode(t, replay, "checkout replay", &replayResult)
			var replayOrder struct {
				Number             string `json:"number"`
				Total              int64  `json:"total"`
				RoundingAdjustment int64  `json:"rounding_adjustment"`
			}
			if err := json.Unmarshal(replayResult.Order, &replayOrder); err != nil {
				t.Fatalf("decode replay order: %v", err)
			}
			if replayOrder.Number != result.Order.Number || replayOrder.Total != 9500 || replayOrder.RoundingAdjustment != 50 {
				t.Errorf("replay order=%+v, want same number, total 9500, adjustment 50", replayOrder)
			}
		})
	}
}

func TestCheckoutRoundsSmallPositivePayableToFreeOrder(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	admin := h.user("admin", "rounding-free-admin")
	var adminStatus string
	if err := h.db.Model(&core.User{}).Select("status").Where("id = ? AND role = ?", admin.ID, "admin").Scan(&adminStatus).Error; err != nil {
		t.Fatalf("verify active admin recipient: %v", err)
	}
	if adminStatus != "active" {
		t.Fatalf("admin status=%q, want active notification recipient", adminStatus)
	}
	var explicitOrderNewPrefs int64
	if err := h.db.Model(&core.NotificationPref{}).Where("user_id = ? AND event = ?", admin.ID, "order.new").Count(&explicitOrderNewPrefs).Error; err != nil {
		t.Fatalf("read admin order.new preference: %v", err)
	}
	if explicitOrderNewPrefs != 0 {
		t.Fatalf("unexpected explicit order.new preference rows=%d; test expects the default in-app-on preference", explicitOrderNewPrefs)
	}
	shopSetOptions(t, h, map[string]string{"payment_methods": `{}`, "payment_sandbox_enabled": "false"})
	p := shopSeedProduct(t, h, "rounding-free-tee", 49, 10)
	v := shopVariantRows(t, h, p.ID)[0]
	b := newShopBrowser(h)
	shopAddToCart(t, b, v.ID, 1)

	body := shopCheckoutBody("round-free@example.test", "credit_card", "round-free-1")
	delete(body, "payment")
	body["shipping"] = map[string]any{"method": "pickup", "name": "測試買家"}
	r := b.send(http.MethodPost, "/api/checkout", body)
	shopWantCode(t, r, "POST /api/checkout with a payable below 50 minor units", http.StatusCreated)
	var result struct {
		Order struct {
			Number             string `json:"number"`
			Status             string `json:"status"`
			Total              int64  `json:"total"`
			RoundingAdjustment int64  `json:"rounding_adjustment"`
		} `json:"order"`
		Payment struct {
			Kind string `json:"kind"`
		} `json:"payment"`
	}
	shopDecode(t, r, "free rounded checkout", &result)
	if result.Order.Total != 0 || result.Order.RoundingAdjustment != -49 || result.Order.Status != core.OrderStatusPaid || result.Payment.Kind != "none" {
		t.Errorf("free checkout order total=%d adjustment=%d status=%q payment.kind=%q, want 0, -49, paid, none", result.Order.Total, result.Order.RoundingAdjustment, result.Order.Status, result.Payment.Kind)
	}
	order := shopOrderRow(t, h, result.Order.Number)
	if order.PaymentProvider != "none" || order.PaymentMethod != "none" || order.ExpiresAt != nil || order.PaidAt == nil {
		t.Errorf("free order provider=%q method=%q expires=%v paid_at=%v, want none/none, no expiry, and paid timestamp", order.PaymentProvider, order.PaymentMethod, order.ExpiresAt, order.PaidAt)
	}
	var attempts int64
	if err := h.db.Model(&core.Payment{}).Where("order_id = ?", order.ID).Count(&attempts).Error; err != nil {
		t.Fatalf("count payment attempts for free order: %v", err)
	}
	if attempts != 0 {
		t.Errorf("free order created %d payment attempts, want none", attempts)
	}
	var stock int
	if err := h.db.Model(&core.ProductVariant{}).Select("stock").Where("id = ?", v.ID).Scan(&stock).Error; err != nil {
		t.Fatalf("read free order stock: %v", err)
	}
	var product core.Product
	if err := h.db.First(&product, p.ID).Error; err != nil {
		t.Fatalf("read free order sold count: %v", err)
	}
	eventsBeforeReplay := shopCount(t, h, "SELECT COUNT(*) FROM order_events WHERE order_id = ?", order.ID)
	deliveriesBeforeReplay := shopCount(t, h, "SELECT COUNT(*) FROM notification_deliveries")
	for _, kind := range []string{"order.confirmation", "order.lookup_link"} {
		var count int64
		if err := h.db.Model(&core.NotificationDelivery{}).Where("kind = ? AND lower(to_address) = lower(?)", kind, "round-free@example.test").Count(&count).Error; err != nil {
			t.Fatalf("count initial %s email: %v", kind, err)
		}
		if count != 1 {
			t.Errorf("initial zero order queued %d %s emails, want exactly one", count, kind)
		}
	}
	if count := shopCount(t, h, "SELECT COUNT(*) FROM notifications WHERE kind = 'order.new' AND params ->> 'number' = ?", result.Order.Number); count != 1 {
		t.Errorf("initial zero order created %d order.new admin notifications, want exactly one", count)
	}
	replay := b.send(http.MethodPost, "/api/checkout", body)
	if replay.code != http.StatusOK && replay.code != http.StatusCreated {
		t.Fatalf("zero checkout replay returned %d, body=%q", replay.code, replay.body)
	}
	if again := shopOrderRow(t, h, result.Order.Number); again.Status != core.OrderStatusPaid {
		t.Errorf("zero checkout replay order status=%q, want still paid", again.Status)
	}
	if got := shopCount(t, h, "SELECT COUNT(*) FROM payments WHERE order_id = ?", order.ID); got != 0 {
		t.Errorf("zero checkout replay created %d payment attempts, want none", got)
	}
	if got := shopCount(t, h, "SELECT COUNT(*) FROM order_events WHERE order_id = ?", order.ID); got != eventsBeforeReplay {
		t.Errorf("zero checkout replay changed order event count from %d to %d", eventsBeforeReplay, got)
	}
	if got := shopCount(t, h, "SELECT COUNT(*) FROM notification_deliveries"); got != deliveriesBeforeReplay {
		t.Errorf("zero checkout replay changed notification deliveries from %d to %d", deliveriesBeforeReplay, got)
	}
	if err := h.db.Model(&core.ProductVariant{}).Select("stock").Where("id = ?", v.ID).Scan(&stock).Error; err != nil {
		t.Fatalf("read free order stock after replay: %v", err)
	}
	var after core.Product
	if err := h.db.First(&after, p.ID).Error; err != nil {
		t.Fatalf("read free order sold count after replay: %v", err)
	}
	if stock != 9 || product.SoldCount != 1 || after.SoldCount != product.SoldCount {
		t.Errorf("free checkout/replay stock=%d sold_count=%d then %d, want stock 9 and sold count 1 unchanged", stock, product.SoldCount, after.SoldCount)
	}
}

func TestCheckoutRequiresPaymentMethodForPositiveRoundedTotal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remove bool
		method string
	}{
		{name: "omitted", remove: true},
		{name: "none", method: "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newAccessHarness(t)
			shopSetup(t, h)
			p := shopSeedProduct(t, h, "rounding-positive-"+tc.name, 100, 10)
			v := shopVariantRows(t, h, p.ID)[0]
			b := newShopBrowser(h)
			shopAddToCart(t, b, v.ID, 1)
			body := shopCheckoutBody("round-positive-"+tc.name+"@example.test", tc.method, "round-positive-"+tc.name)
			if tc.remove {
				delete(body, "payment")
			}
			r := b.send(http.MethodPost, "/api/checkout", body)
			shopWantCode(t, r, "positive checkout without a valid payment method", http.StatusBadRequest)
			var response map[string]string
			shopDecode(t, r, "invalid positive checkout", &response)
			if response["code"] != "payment.method" {
				t.Errorf("validation code=%q, want payment.method; body=%q", response["code"], r.body)
			}
			if got := shopCount(t, h, "SELECT COUNT(*) FROM orders WHERE idempotency_key = ?", "round-positive-"+tc.name); got != 0 {
				t.Errorf("invalid payment method created %d orders, want none", got)
			}
		})
	}
}

func TestCartAndCheckoutRoundUndiscountedFractionalAmounts(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	shopSetOptions(t, h, map[string]string{
		"shop_shipping_methods": `[{"id":"home","label":"自取","fee":0,"free_over":0,"eta_days":1,"requires_address":false,"enabled":true},{"id":"delivery","label":"配送","fee":51,"free_over":0,"eta_days":1,"requires_address":false,"enabled":true}]`,
	})
	p := shopSeedProduct(t, h, "rounding-fractional-price", 149, 10)
	v := shopVariantRows(t, h, p.ID)[0]
	b := newShopBrowser(h)
	shopAddToCart(t, b, v.ID, 1)

	var cart struct {
		Total              int64 `json:"total"`
		Discount           int64 `json:"discount"`
		RoundingAdjustment int64 `json:"rounding_adjustment"`
	}
	cartResponse := b.get("/api/cart")
	shopWantCode(t, cartResponse, "GET /api/cart rounding quote", http.StatusOK)
	shopDecode(t, cartResponse, "GET /api/cart rounding quote", &cart)
	if cart.Total != 100 || cart.RoundingAdjustment != -49 || cart.Discount != 0 {
		t.Errorf("cart total=%d adjustment=%d discount=%d, want 100, -49, 0 for un-discounted 149", cart.Total, cart.RoundingAdjustment, cart.Discount)
	}
	body := shopCheckoutBody("rounding-fractional@example.test", "credit_card", "round-fractional-1")
	body["shipping"] = map[string]any{"method": "home", "name": "測試買家"}
	r := b.send(http.MethodPost, "/api/checkout", body)
	shopWantCode(t, r, "checkout of un-discounted fractional price", http.StatusCreated)
	var homeQuote struct {
		Order struct {
			Number             string `json:"number"`
			Total              int64  `json:"total"`
			RoundingAdjustment int64  `json:"rounding_adjustment"`
		} `json:"order"`
	}
	shopDecode(t, r, "fractional home checkout", &homeQuote)
	if homeQuote.Order.Total != 100 || homeQuote.Order.RoundingAdjustment != -49 {
		t.Errorf("home total=%d adjustment=%d, want 100 and -49", homeQuote.Order.Total, homeQuote.Order.RoundingAdjustment)
	}

	// A 51-minor-unit delivery fee moves the same 149-unit item to exactly 200.
	b2 := newShopBrowser(h)
	shopAddToCart(t, b2, v.ID, 1)
	deliveryBody := shopCheckoutBody("rounding-delivery@example.test", "credit_card", "round-delivery-1")
	deliveryBody["shipping"] = map[string]any{"method": "delivery", "name": "測試買家"}
	delivery := b2.send(http.MethodPost, "/api/checkout", deliveryBody)
	shopWantCode(t, delivery, "checkout after changing shipping method", http.StatusCreated)
	var delivered struct {
		Order struct {
			Total              int64 `json:"total"`
			RoundingAdjustment int64 `json:"rounding_adjustment"`
		} `json:"order"`
	}
	shopDecode(t, delivery, "fractional delivery checkout", &delivered)
	if delivered.Order.Total != 200 || delivered.Order.RoundingAdjustment != 0 {
		t.Errorf("delivery total=%d adjustment=%d, want 200 and 0", delivered.Order.Total, delivered.Order.RoundingAdjustment)
	}
}

func TestFreeShippingThresholdAndCouponKeepCartAndCheckoutTotalsAligned(t *testing.T) {
	t.Run("threshold", func(t *testing.T) {
		h := newAccessHarness(t)
		shopSetup(t, h)
		shopSetOptions(t, h, map[string]string{
			"shop_shipping_methods":   `[{"id":"home","label":"宅配","fee":51,"free_over":149,"eta_days":1,"requires_address":true,"enabled":true},{"id":"pickup","label":"自取","fee":0,"free_over":0,"eta_days":1,"requires_address":false,"enabled":true}]`,
			"shop_free_shipping_over": "0",
		})
		p := shopSeedProduct(t, h, "rounding-free-over", 149, 5)
		v := shopVariantRows(t, h, p.ID)[0]
		b := newShopBrowser(h)
		shopAddToCart(t, b, v.ID, 1)
		cart := b.get("/api/cart")
		shopWantCode(t, cart, "GET /api/cart at free-shipping threshold", http.StatusOK)
		var quote struct {
			Shipping           int64 `json:"shipping"`
			Total              int64 `json:"total"`
			RoundingAdjustment int64 `json:"rounding_adjustment"`
		}
		shopDecode(t, cart, "threshold cart quote", &quote)
		if quote.Shipping != 0 || quote.Total != 100 || quote.RoundingAdjustment != -49 {
			t.Errorf("threshold cart shipping=%d total=%d adjustment=%d, want 0, 100, -49", quote.Shipping, quote.Total, quote.RoundingAdjustment)
		}
		body := shopCheckoutBody("round-free-over@example.test", "credit_card", "round-free-over")
		r := b.send(http.MethodPost, "/api/checkout", body)
		shopWantCode(t, r, "checkout at free-shipping threshold", http.StatusCreated)
		var result struct {
			Order struct {
				Number             string `json:"number"`
				Total              int64  `json:"total"`
				RoundingAdjustment int64  `json:"rounding_adjustment"`
			} `json:"order"`
		}
		shopDecode(t, r, "threshold checkout", &result)
		order := shopOrderRow(t, h, result.Order.Number)
		if order.ShippingTotal != 0 || result.Order.Total != 100 || result.Order.RoundingAdjustment != -49 {
			t.Errorf("threshold checkout shipping=%d total=%d adjustment=%d, want 0, 100, -49", order.ShippingTotal, result.Order.Total, result.Order.RoundingAdjustment)
		}
	})

	t.Run("free-shipping-coupon", func(t *testing.T) {
		h := newAccessHarness(t)
		shopSetup(t, h)
		shopSetOptions(t, h, map[string]string{
			"shop_shipping_methods": `[{"id":"home","label":"宅配","fee":51,"free_over":0,"eta_days":1,"requires_address":true,"enabled":true},{"id":"pickup","label":"自取","fee":0,"free_over":0,"eta_days":1,"requires_address":false,"enabled":true}]`,
		})
		p := shopSeedProduct(t, h, "rounding-free-shipping-coupon", 149, 5)
		v := shopVariantRows(t, h, p.ID)[0]
		coupon := shopSeedCoupon(t, h, core.Coupon{Code: "ROUND-FREESHIP", Kind: "free_shipping"})
		b := newShopBrowser(h)
		shopAddToCart(t, b, v.ID, 1)
		shopWantOK(t, b.send(http.MethodPost, "/api/cart/coupon", map[string]any{"code": coupon.Code}), "apply free-shipping coupon")
		var quote struct {
			Shipping           int64 `json:"shipping"`
			Total              int64 `json:"total"`
			RoundingAdjustment int64 `json:"rounding_adjustment"`
		}
		cart := b.get("/api/cart")
		shopWantCode(t, cart, "GET /api/cart after free-shipping coupon", http.StatusOK)
		shopDecode(t, cart, "free-shipping coupon cart quote", &quote)
		if quote.Shipping != 0 || quote.Total != 100 || quote.RoundingAdjustment != -49 {
			t.Errorf("coupon cart shipping=%d total=%d adjustment=%d, want 0, 100, -49", quote.Shipping, quote.Total, quote.RoundingAdjustment)
		}
		body := shopCheckoutBody("round-free-shipping-coupon@example.test", "credit_card", "round-free-shipping-coupon")
		body["coupon_code"] = coupon.Code
		r := b.send(http.MethodPost, "/api/checkout", body)
		shopWantCode(t, r, "checkout with free-shipping coupon", http.StatusCreated)
		var result struct {
			Order struct {
				Number             string `json:"number"`
				Total              int64  `json:"total"`
				RoundingAdjustment int64  `json:"rounding_adjustment"`
			} `json:"order"`
		}
		shopDecode(t, r, "free-shipping coupon checkout", &result)
		order := shopOrderRow(t, h, result.Order.Number)
		if order.ShippingTotal != 0 || result.Order.Total != 100 || result.Order.RoundingAdjustment != -49 {
			t.Errorf("coupon checkout shipping=%d total=%d adjustment=%d, want 0, 100, -49", order.ShippingTotal, result.Order.Total, result.Order.RoundingAdjustment)
		}
	})

	t.Run("global-threshold-before-discount", func(t *testing.T) {
		h := newAccessHarness(t)
		shopSetup(t, h)
		shopSetOptions(t, h, map[string]string{"shop_free_shipping_over": "150"})
		p := shopSeedProduct(t, h, "rounding-global-free-over", 150, 5)
		v := shopVariantRows(t, h, p.ID)[0]
		coupon := shopSeedCoupon(t, h, core.Coupon{Code: "ROUND-GLOBAL10", Kind: "percent", Value: 10})
		b := newShopBrowser(h)
		shopAddToCart(t, b, v.ID, 1)
		shopWantOK(t, b.send(http.MethodPost, "/api/cart/coupon", map[string]any{"code": coupon.Code}), "apply threshold coupon")
		var quote struct {
			Subtotal           int64 `json:"subtotal"`
			Discount           int64 `json:"discount"`
			Shipping           int64 `json:"shipping"`
			Total              int64 `json:"total"`
			RoundingAdjustment int64 `json:"rounding_adjustment"`
		}
		cart := b.get("/api/cart")
		shopWantCode(t, cart, "GET /api/cart after threshold discount", http.StatusOK)
		shopDecode(t, cart, "global threshold cart quote", &quote)
		if quote.Subtotal != 150 || quote.Discount != 15 || quote.Shipping != 0 || quote.Total != 100 || quote.RoundingAdjustment != -35 {
			t.Errorf("cart subtotal=%d discount=%d shipping=%d total=%d adjustment=%d, want 150,15,0,100,-35", quote.Subtotal, quote.Discount, quote.Shipping, quote.Total, quote.RoundingAdjustment)
		}
		body := shopCheckoutBody("round-global-free-over@example.test", "credit_card", "round-global-free-over")
		body["coupon_code"] = coupon.Code
		r := b.send(http.MethodPost, "/api/checkout", body)
		shopWantCode(t, r, "checkout above original-subtotal free-shipping threshold", http.StatusCreated)
		var result struct {
			Order struct {
				Number             string `json:"number"`
				Total              int64  `json:"total"`
				RoundingAdjustment int64  `json:"rounding_adjustment"`
			} `json:"order"`
		}
		shopDecode(t, r, "global threshold checkout", &result)
		order := shopOrderRow(t, h, result.Order.Number)
		if order.Subtotal != 150 || order.DiscountTotal != 15 || order.ShippingTotal != 0 || order.TotalAmount != 100 || result.Order.RoundingAdjustment != -35 {
			t.Errorf("checkout subtotal=%d discount=%d shipping=%d total=%d adjustment=%d, want 150,15,0,100,-35", order.Subtotal, order.DiscountTotal, order.ShippingTotal, order.TotalAmount, result.Order.RoundingAdjustment)
		}
	})
}

func TestCartRequiresShippingOnlyForAvailablePhysicalItems(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	shopSetOptions(t, h, map[string]string{
		"shop_shipping_methods":   `[{"id":"home","label":"宅配","fee":51,"free_over":0,"eta_days":1,"requires_address":true,"enabled":true},{"id":"pickup","label":"自取","fee":0,"free_over":0,"eta_days":1,"requires_address":false,"enabled":true}]`,
		"shop_free_shipping_over": "0",
	})
	digital := shopSeedProduct(t, h, "rounding-digital", 200, 5)
	if err := h.db.Model(&core.Product{}).Where("id = ?", digital.ID).Updates(map[string]any{"kind": "digital", "requires_shipping": false}).Error; err != nil {
		t.Fatalf("mark digital product non-shipping: %v", err)
	}
	digitalVariant := shopVariantRows(t, h, digital.ID)[0]
	soldOut := shopSeedProduct(t, h, "rounding-sold-out-physical", 300, 5)
	soldOutVariant := shopVariantRows(t, h, soldOut.ID)[0]
	b := newShopBrowser(h)
	shopAddToCart(t, b, digitalVariant.ID, 1)
	shopAddToCart(t, b, soldOutVariant.ID, 1)
	if err := h.db.Model(&core.ProductVariant{}).Where("id = ?", soldOutVariant.ID).Update("stock", 0).Error; err != nil {
		t.Fatalf("mark physical cart item sold out: %v", err)
	}
	var digitalOnly struct {
		RequiresShipping bool  `json:"requires_shipping"`
		Shipping         int64 `json:"shipping"`
		Total            int64 `json:"total"`
	}
	r := b.get("/api/cart")
	shopWantCode(t, r, "GET cart with digital and sold-out physical lines", http.StatusOK)
	shopDecode(t, r, "digital plus sold-out physical cart", &digitalOnly)
	if digitalOnly.RequiresShipping || digitalOnly.Shipping != 0 || digitalOnly.Total != 200 {
		t.Errorf("digital plus sold-out physical requires_shipping=%v shipping=%d total=%d, want false,0,200", digitalOnly.RequiresShipping, digitalOnly.Shipping, digitalOnly.Total)
	}

	available := shopSeedProduct(t, h, "rounding-available-physical", 300, 5)
	availableVariant := shopVariantRows(t, h, available.ID)[0]
	mixedBrowser := newShopBrowser(h)
	shopAddToCart(t, mixedBrowser, digitalVariant.ID, 1)
	shopAddToCart(t, mixedBrowser, availableVariant.ID, 1)
	var mixed struct {
		RequiresShipping   bool  `json:"requires_shipping"`
		Shipping           int64 `json:"shipping"`
		Total              int64 `json:"total"`
		RoundingAdjustment int64 `json:"rounding_adjustment"`
	}
	r = mixedBrowser.get("/api/cart")
	shopWantCode(t, r, "GET cart with available digital and physical items", http.StatusOK)
	shopDecode(t, r, "mixed available cart", &mixed)
	if !mixed.RequiresShipping || mixed.Shipping != 51 || mixed.Total != 600 || mixed.RoundingAdjustment != 49 {
		t.Errorf("mixed cart requires_shipping=%v shipping=%d total=%d adjustment=%d, want true,51,600,49", mixed.RequiresShipping, mixed.Shipping, mixed.Total, mixed.RoundingAdjustment)
	}
}

func TestZeroOrderCancellationRestocksAndReleasesCouponWithoutRefund(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	a := h.user("customer", "zero-canceller")
	p := shopSeedProduct(t, h, "rounding-zero-cancel", 49, 5)
	v := shopVariantRows(t, h, p.ID)[0]
	coupon := shopSeedCoupon(t, h, core.Coupon{Code: "ZEROCANCEL", Kind: "percent", Value: 100})
	b := newShopBrowser(h)
	b.token = h.token(a)
	shopAddToCart(t, b, v.ID, 1)
	shopWantOK(t, b.send(http.MethodPost, "/api/cart/coupon", map[string]any{"code": coupon.Code}), "apply free coupon")
	body := shopCheckoutBody(a.Email, "credit_card", "round-zero-cancel")
	body["coupon_code"] = coupon.Code
	body["shipping"] = map[string]any{"method": "pickup", "name": "測試買家"}
	r := b.send(http.MethodPost, "/api/checkout", body)
	shopWantCode(t, r, "create a coupon-covered zero order", http.StatusCreated)
	var result struct {
		Order struct {
			Number string `json:"number"`
		} `json:"order"`
	}
	shopDecode(t, r, "zero cancellation checkout", &result)
	order := shopOrderRow(t, h, result.Order.Number)
	detail := b.get("/api/account/orders/" + order.Number)
	shopWantCode(t, detail, "GET member zero-order detail", http.StatusOK)
	var detailView struct {
		Order map[string]any `json:"order"`
	}
	shopDecode(t, detail, "member zero-order detail", &detailView)
	if detailView.Order["CanCancel"] != true {
		t.Errorf("member zero-order CanCancel=%v, want true", detailView.Order["CanCancel"])
	}
	if adjustment, ok := detailView.Order["RoundingAdjustment"].(float64); !ok || adjustment != 0 {
		t.Errorf("member zero-order RoundingAdjustment=%v, want 0", detailView.Order["RoundingAdjustment"])
	}
	roundingActivateDefaultTheme(t, h)
	page := b.get("/account/orders/" + order.Number)
	shopWantCode(t, page, "GET member zero-order page", http.StatusOK)
	if !strings.Contains(page.body, `data-cancel`) || !strings.Contains(page.body, "無需付款") {
		t.Errorf("member zero-order page lacks cancel action or no-payment status: %q", page.body)
	}
	cancel := b.send(http.MethodPost, "/api/account/orders/"+order.Number+"/cancel", nil)
	shopWantOK(t, cancel, "cancel unpaid zero order")
	if got := shopOrderRow(t, h, order.Number); got.Status != core.OrderStatusCancelled {
		t.Errorf("zero order status after cancellation=%q, want cancelled", got.Status)
	}
	var stock int
	if err := h.db.Model(&core.ProductVariant{}).Select("stock").Where("id = ?", v.ID).Scan(&stock).Error; err != nil {
		t.Fatalf("read stock after zero-order cancellation: %v", err)
	}
	if count := shopCount(t, h, "SELECT COUNT(*) FROM stock_movements WHERE order_id = ? AND reason = ?", order.ID, "restock"); stock != 5 || count != 1 {
		t.Errorf("zero-order cancellation stock=%d restock movements=%d, want stock 5 and one restock", stock, count)
	}
	var storedCoupon core.Coupon
	if err := h.db.First(&storedCoupon, coupon.ID).Error; err != nil {
		t.Fatalf("read coupon after zero-order cancellation: %v", err)
	}
	var redemptions int64
	if err := h.db.Model(&core.CouponRedemption{}).Where("order_id = ? AND released_at IS NOT NULL", order.ID).Count(&redemptions).Error; err != nil {
		t.Fatalf("count released zero-order coupon redemptions: %v", err)
	}
	if storedCoupon.UsedCount != 0 || redemptions != 1 {
		t.Errorf("zero-order cancellation coupon used_count=%d released redemptions=%d, want 0/1", storedCoupon.UsedCount, redemptions)
	}
	if got := shopCount(t, h, "SELECT COUNT(*) FROM order_events WHERE order_id = ? AND kind = 'refund_requested'", order.ID); got != 0 {
		t.Errorf("zero-order cancellation created %d refund requests, want none", got)
	}
	if got := shopCount(t, h, "SELECT COUNT(*) FROM payments WHERE order_id = ?", order.ID); got != 0 {
		t.Errorf("zero-order cancellation created %d payment attempts, want none", got)
	}
}

func roundingActivateDefaultTheme(t *testing.T, h *accessHarness) {
	t.Helper()
	tm := theme.NewManager("web/themes")
	if err := tm.Activate("default"); err != nil {
		t.Fatalf("activate default theme for member page: %v", err)
	}
	e := echo.New()
	RegisterRoutes(e, tm, nil)
	h.echo = e
}
