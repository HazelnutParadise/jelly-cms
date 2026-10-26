package server

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

func TestCheckoutIdempotencyKeyCannotBeReusedForDifferentCart(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	firstProduct := shopSeedProduct(t, h, "idem-owner-one", 12000, 10)
	secondProduct := shopSeedProduct(t, h, "idem-owner-two", 9000, 10)
	firstVariant := shopVariantRows(t, h, firstProduct.ID)[0]
	secondVariant := shopVariantRows(t, h, secondProduct.ID)[0]
	b := newShopBrowser(h)
	shopAddToCart(t, b, firstVariant.ID, 1)
	body := shopCheckoutBody("idem-owner@example.test", "credit_card", "cart-bound-key")
	first := b.send(http.MethodPost, "/api/checkout", body)
	shopWantCode(t, first, "initial checkout", http.StatusCreated)
	var created shopCheckoutResult
	shopDecode(t, first, "initial checkout", &created)

	shopAddToCart(t, b, secondVariant.ID, 1)
	replay := b.send(http.MethodPost, "/api/checkout", body)
	if replay.code != http.StatusConflict {
		t.Fatalf("reusing an idempotency key with a different cart returned %d, want 409; body=%q", replay.code, replay.body)
	}
	if replay.body != "" && created.Order.Number != "" && strings.Contains(replay.body, created.Order.Number) {
		t.Errorf("conflict response disclosed the original order number: %q", replay.body)
	}
	if n := shopCount(t, h, "SELECT count(*) FROM orders WHERE idempotency_key = ?", "cart-bound-key"); n != 1 {
		t.Errorf("orders using the key = %d, want the original order only", n)
	}
	if n := shopCount(t, h, "SELECT count(*) FROM order_items oi JOIN orders o ON o.id = oi.order_id WHERE o.idempotency_key = ? AND oi.variant_id = ?", "cart-bound-key", secondVariant.ID); n != 0 {
		t.Errorf("original idempotency key was attached to the second cart's item")
	}
}

func TestConcurrentCheckoutSameCartAndIdempotencyKeyReservesOnce(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "concurrent-idem-tee", 12000, 10)
	variant := shopVariantRows(t, h, p.ID)[0]

	original := newShopBrowser(h)
	shopAddToCart(t, original, variant.ID, 1)
	makeCopy := func() *shopBrowser {
		copy := newShopBrowser(h)
		for name, cookie := range original.jar {
			cookieCopy := *cookie
			copy.jar[name] = &cookieCopy
		}
		return copy
	}
	browsers := []*shopBrowser{makeCopy(), makeCopy()}
	body := shopCheckoutBody("concurrent-idem@example.test", "credit_card", "same-cart-concurrent-key")
	start := make(chan struct{})
	type result struct {
		response shopResponse
	}
	results := make(chan result, len(browsers))
	var wg sync.WaitGroup
	for _, browser := range browsers {
		wg.Add(1)
		go func(b *shopBrowser) {
			defer wg.Done()
			<-start
			results <- result{response: b.send(http.MethodPost, "/api/checkout", body)}
		}(browser)
	}
	close(start)
	wg.Wait()
	close(results)

	var orderNumber string
	for result := range results {
		if result.response.panicked != nil {
			t.Fatalf("concurrent checkout panicked: %v", result.response.panicked)
		}
		if result.response.code != http.StatusOK && result.response.code != http.StatusCreated {
			t.Fatalf("concurrent checkout returned %d, want 200 or 201; body=%q", result.response.code, result.response.body)
		}
		var checkout shopCheckoutResult
		shopDecode(t, result.response, "concurrent checkout", &checkout)
		if orderNumber == "" {
			orderNumber = checkout.Order.Number
		} else if checkout.Order.Number != orderNumber {
			t.Errorf("concurrent response order = %q, want same order %q", checkout.Order.Number, orderNumber)
		}
	}
	if orderNumber == "" {
		t.Fatal("concurrent checkouts returned no order number")
	}
	if n := shopCount(t, h, "SELECT count(*) FROM orders WHERE idempotency_key = ?", "same-cart-concurrent-key"); n != 1 {
		t.Errorf("orders for same idempotency key = %d, want 1", n)
	}
	if n := shopCount(t, h, `SELECT count(*) FROM stock_movements sm JOIN orders o ON o.id = sm.order_id
		WHERE o.idempotency_key = ? AND sm.variant_id = ? AND sm.reason = 'reserve' AND sm.delta = -1`,
		"same-cart-concurrent-key", variant.ID); n != 1 {
		t.Errorf("one-unit reserve movements for the idempotency key = %d, want 1", n)
	}
	var remaining int
	if err := h.db.Model(&core.ProductVariant{}).Where("id = ?", variant.ID).Select("stock").Scan(&remaining).Error; err != nil {
		t.Fatalf("read stock after concurrent checkout: %v", err)
	}
	if remaining != 9 {
		t.Errorf("stock after concurrent same-cart checkout = %d, want 9", remaining)
	}
}

func TestCheckoutIPv6LoopbackDoesNotPanic(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "ipv6-checkout-tee", 12000, 10)
	variant := shopVariantRows(t, h, p.ID)[0]
	b := newShopBrowser(h)
	b.remote = "[::1]:40000"
	shopAddToCart(t, b, variant.ID, 1)
	r := b.send(http.MethodPost, "/api/checkout", shopCheckoutBody("ipv6@example.test", "credit_card", "ipv6-checkout-key"))
	if r.panicked != nil {
		t.Fatalf("checkout with IPv6 loopback remote address panicked: %v", r.panicked)
	}
	shopWantCode(t, r, "IPv6 loopback checkout", http.StatusCreated)
}

func TestProductCategoryParentNullMovesToRootAndOmissionPreservesParent(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	adminToken := h.token(h.user("admin", "category-parent-update-admin"))
	parent := core.ProductCategory{Name: "Parent", Slug: "parent-category"}
	if err := h.db.Create(&parent).Error; err != nil {
		t.Fatalf("seed parent category: %v", err)
	}
	child := core.ProductCategory{Name: "Child", Slug: "child-category", ParentID: &parent.ID}
	if err := h.db.Create(&child).Error; err != nil {
		t.Fatalf("seed child category: %v", err)
	}
	path := fmt.Sprintf("/api/admin/product-categories/%d", child.ID)

	withoutParent := productsAdminDo(t, h, http.MethodPut, path, map[string]any{"name": "Renamed Child"}, adminToken)
	productsAdminWantSuccess(t, withoutParent, "PUT category without parent_id")
	var afterOmission core.ProductCategory
	productsAdminDecode(t, withoutParent, "PUT category without parent_id", &afterOmission)
	if afterOmission.ParentID == nil || *afterOmission.ParentID != parent.ID {
		t.Fatalf("omitting parent_id changed parent to %v, want parent %d", afterOmission.ParentID, parent.ID)
	}
	var stored core.ProductCategory
	if err := h.db.First(&stored, child.ID).Error; err != nil {
		t.Fatalf("reload child after omitted parent_id: %v", err)
	}
	if stored.ParentID == nil || *stored.ParentID != parent.ID {
		t.Fatalf("stored parent after omitted parent_id = %v, want %d", stored.ParentID, parent.ID)
	}

	toRoot := productsAdminDo(t, h, http.MethodPut, path, map[string]any{"parent_id": nil}, adminToken)
	productsAdminWantSuccess(t, toRoot, "PUT category with parent_id null")
	var root core.ProductCategory
	productsAdminDecode(t, toRoot, "PUT category with parent_id null", &root)
	if root.ParentID != nil {
		t.Errorf("response parent_id after explicit null = %d, want null/root", *root.ParentID)
	}
	if err := h.db.First(&stored, child.ID).Error; err != nil {
		t.Fatalf("reload child after clearing parent_id: %v", err)
	}
	if stored.ParentID != nil {
		t.Errorf("stored parent_id after explicit null = %d, want null/root", *stored.ParentID)
	}
}

func TestCheckoutIncludedTaxMatchesQuoteAndStoredAmounts(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	product := shopSeedProduct(t, h, "included-tax-105", 10500, 10)
	variant := shopVariantRows(t, h, product.ID)[0]
	for _, tc := range []struct {
		rate string
		want int64
	}{
		{rate: "5", want: 500},
		{rate: "0", want: 0},
		{rate: "5.5", want: 547},
	} {
		t.Run("rate_"+strings.ReplaceAll(tc.rate, ".", "_"), func(t *testing.T) {
			shopSetOptions(t, h, map[string]string{"shop_tax_rate": tc.rate})
			b := newShopBrowser(h)
			shopAddToCart(t, b, variant.ID, 1)
			quoteResponse := b.get("/api/cart")
			shopWantCode(t, quoteResponse, "cart quote for tax rate "+tc.rate, http.StatusOK)
			var quote struct {
				TaxTotal int64 `json:"tax_total"`
			}
			shopDecode(t, quoteResponse, "cart quote for tax rate "+tc.rate, &quote)
			if quote.TaxTotal != tc.want {
				t.Fatalf("cart quote tax at rate %s = %d, want %d", tc.rate, quote.TaxTotal, tc.want)
			}

			body := shopCheckoutBody("tax-"+strings.ReplaceAll(tc.rate, ".", "-")+"@example.test", "credit_card", "included-tax-"+strings.ReplaceAll(tc.rate, ".", "-"))
			body["shipping"] = map[string]any{"method": "pickup", "name": "測試買家", "phone": "0912345678"}
			checkoutResponse := b.send(http.MethodPost, "/api/checkout", body)
			shopWantCode(t, checkoutResponse, "pickup checkout for tax rate "+tc.rate, http.StatusCreated)
			var checkout struct {
				Order struct {
					Number   string `json:"number"`
					Total    int64  `json:"total"`
					TaxTotal int64  `json:"tax_total"`
				} `json:"order"`
			}
			shopDecode(t, checkoutResponse, "pickup checkout for tax rate "+tc.rate, &checkout)
			if checkout.Order.Total != 10500 || checkout.Order.TaxTotal != tc.want {
				t.Errorf("checkout total/tax at rate %s = %d/%d, want 10500/%d", tc.rate, checkout.Order.Total, checkout.Order.TaxTotal, tc.want)
			}
			stored := shopOrderRow(t, h, checkout.Order.Number)
			if stored.TotalAmount != 10500 || stored.Subtotal != 10500 || stored.ShippingTotal != 0 || stored.TaxTotal != tc.want {
				t.Errorf("stored total/subtotal/shipping/tax at rate %s = %d/%d/%d/%d, want 10500/10500/0/%d",
					tc.rate, stored.TotalAmount, stored.Subtotal, stored.ShippingTotal, stored.TaxTotal, tc.want)
			}
			if stored.TaxTotal != quote.TaxTotal || checkout.Order.TaxTotal != quote.TaxTotal {
				t.Errorf("quote/response/stored taxes at rate %s = %d/%d/%d, want equal values", tc.rate, quote.TaxTotal, checkout.Order.TaxTotal, stored.TaxTotal)
			}
		})
	}
}

func TestOrderStatusEndpointHidesAnotherCustomersOrder(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	owner := h.user("customer", "status-owner")
	other := h.user("customer", "status-other")
	ownerID := owner.ID
	order := shopSeedOrder(t, h, shopOrderSeed{
		number: fmt.Sprintf("JL-%d-8001", time.Now().Year()), email: owner.Email,
		status: core.OrderStatusPending, userID: &ownerID, total: 12000,
	})
	b := newShopBrowser(h)
	b.token = h.token(other)
	r := b.get("/api/orders/" + order.Number + "/status")
	if r.code != http.StatusNotFound {
		t.Fatalf("another customer read order status returned %d, want 404; body=%q", r.code, r.body)
	}
	if strings.Contains(r.body, order.Number) || strings.Contains(r.body, owner.Email) {
		t.Errorf("another customer response disclosed order/customer data: %q", r.body)
	}
}

func TestSandboxPaymentRejectedWhenDisabledOrProduction(t *testing.T) {
	for _, tc := range []struct {
		name       string
		production bool
	}{
		{name: "disabled"},
		{name: "production", production: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newAccessHarness(t)
			shopPaymentFixture(t, h)
			placed := shopSeedPayableOrder(t, h, 8100, "sandbox-gate@example.test", 12000)
			if tc.production {
				t.Setenv("APP_ENV", "production")
			} else {
				shopSetOptions(t, h, map[string]string{"payment_sandbox_enabled": "false"})
			}
			r := shopNotify(newShopBrowser(h), shopSandboxNotice(t, placed.trade, "paid", placed.total, "TWD", "sandbox-gate-"+tc.name))
			if r.code >= 200 && r.code < 300 {
				t.Fatalf("sandbox callback in %s mode returned success status %d", tc.name, r.code)
			}
			if order := shopOrderRow(t, h, placed.number); order.Status != core.OrderStatusPending {
				t.Errorf("order status after rejected sandbox callback = %q, want pending", order.Status)
			}
		})
	}
}

func TestPaidOrderDoesNotRegressOnLaterFailedNotification(t *testing.T) {
	h := newAccessHarness(t)
	shopPaymentFixture(t, h)
	placed := shopSeedPayableOrder(t, h, 8101, "paid-fail-race@example.test", 12000)
	shopWantCode(t, shopNotify(newShopBrowser(h), shopSandboxNotice(t, placed.trade, "paid", placed.total, "TWD", "paid-first")), "paid notification", http.StatusOK)
	shopWantCode(t, shopNotify(newShopBrowser(h), shopSandboxNotice(t, placed.trade, "failed", placed.total, "TWD", "failed-late")), "late failed notification", http.StatusOK)
	if order := shopOrderRow(t, h, placed.number); order.Status != core.OrderStatusPaid {
		t.Errorf("order status after late failed notification = %q, want paid", order.Status)
	}
}

func TestPaymentNotificationDatabaseFailureIsNotAcknowledged(t *testing.T) {
	h := newAccessHarness(t)
	shopPaymentFixture(t, h)
	placed := shopSeedPayableOrder(t, h, 8102, "db-failure@example.test", 12000)
	if err := h.db.Exec(`CREATE FUNCTION reject_shop_payment_event() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'payment event write rejected'; END $$`).Error; err != nil {
		t.Fatalf("create payment event failure trigger function: %v", err)
	}
	if err := h.db.Exec(`CREATE TRIGGER reject_shop_payment_event BEFORE INSERT ON payment_events FOR EACH ROW EXECUTE FUNCTION reject_shop_payment_event()`).Error; err != nil {
		t.Fatalf("create payment event failure trigger: %v", err)
	}
	r := shopNotify(newShopBrowser(h), shopSandboxNotice(t, placed.trade, "paid", placed.total, "TWD", "db-failure"))
	if r.code >= 200 && r.code < 300 {
		t.Fatalf("notification with failed DB event write returned success status %d", r.code)
	}
	if order := shopOrderRow(t, h, placed.number); order.Status != core.OrderStatusPending {
		t.Errorf("order status after payment-event write failure = %q, want pending", order.Status)
	}
	if n := shopCount(t, h, "SELECT count(*) FROM payment_events WHERE provider = 'sandbox'"); n != 0 {
		t.Errorf("payment events after failed transaction = %d, want 0", n)
	}
}

func TestLegacyPaymentCreateCannotSetClientAmount(t *testing.T) {
	h := newAccessHarness(t)
	shopPaymentFixture(t, h)
	product := shopSeedProduct(t, h, "legacy-payment", 12000, 10)
	variant := shopVariantRows(t, h, product.ID)[0]
	owner := h.user("customer", "legacy-payment-owner")
	ownerID := owner.ID
	order := shopSeedOrder(t, h, shopOrderSeed{
		number: fmt.Sprintf("JL-%d-8103", time.Now().Year()), email: owner.Email,
		status: core.OrderStatusPending, provider: "sandbox", method: "credit_card", userID: &ownerID, total: 12000,
		items: []shopItemSeed{{variantID: &variant.ID, productID: &product.ID, title: product.Title, qty: 1, unitPrice: 12000}},
	})
	request := map[string]any{
		"order_id": fmt.Sprint(order.ID), "gateway": "sandbox", "method": "credit_card",
		"amount": 1, "total_amount": 1, "price": 1,
	}
	ownerBrowser := newShopBrowser(h)
	ownerBrowser.token = h.token(owner)
	ownerResponse := ownerBrowser.send(http.MethodPost, "/api/payment/create", request)
	if ownerResponse.panicked != nil {
		t.Fatalf("authenticated owner legacy payment-create panicked: %v", ownerResponse.panicked)
	}
	var paymentRows int64
	var actualAmount int64
	if err := h.db.Raw("SELECT count(*), COALESCE(max(amount), 0) FROM payments WHERE order_id = ?", order.ID).Row().Scan(&paymentRows, &actualAmount); err != nil {
		t.Fatalf("read payment attempts after owner request: %v", err)
	}
	ownerPaymentRows, ownerPaymentAmount := paymentRows, actualAmount
	if ownerResponse.code >= 200 && ownerResponse.code < 300 {
		if paymentRows != 1 || actualAmount != 12000 {
			t.Errorf("owner request created %d payment row(s) with amount %d, want one row with server amount 12000", paymentRows, actualAmount)
		}
	} else if paymentRows != 0 {
		t.Errorf("rejected owner request left %d payment rows (amount %d), want no state change", paymentRows, actualAmount)
	}
	if stored := shopOrderRow(t, h, order.Number); stored.TotalAmount != 12000 || stored.Status != core.OrderStatusPending {
		t.Errorf("order after owner amount-tampering request: total=%d status=%q, want 12000/pending", stored.TotalAmount, stored.Status)
	}

	other := h.user("customer", "legacy-payment-other")
	otherBrowser := newShopBrowser(h)
	otherBrowser.token = h.token(other)
	otherResponse := otherBrowser.send(http.MethodPost, "/api/payment/create", request)
	if otherResponse.panicked != nil {
		t.Fatalf("other customer legacy payment-create panicked: %v", otherResponse.panicked)
	}
	if otherResponse.code != http.StatusNotFound {
		t.Errorf("another customer's payment-create request returned %d, want 404; body=%q", otherResponse.code, otherResponse.body)
	}
	if err := h.db.Raw("SELECT count(*), COALESCE(max(amount), 0) FROM payments WHERE order_id = ?", order.ID).Row().Scan(&paymentRows, &actualAmount); err != nil {
		t.Fatalf("re-read payment attempts after other customer request: %v", err)
	}
	if paymentRows != ownerPaymentRows || actualAmount != ownerPaymentAmount {
		t.Errorf("other-customer attempt changed payment rows/amount from %d/%d to %d/%d", ownerPaymentRows, ownerPaymentAmount, paymentRows, actualAmount)
	}
}

func TestShopSettingsRejectsUnrelatedOAuthSecret(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	const sentinel = "oauth-secret-must-not-leak-or-change"
	shopSetOptions(t, h, map[string]string{"oauth_google_client_secret": sentinel})
	admin := h.user("admin", "settings-whitelist")
	adm := newShopBrowser(h)
	adm.token = h.token(admin)

	before := shopOptionsDump(t, h)
	get := adm.get("/api/admin/shop/settings")
	shopWantCode(t, get, "GET shop settings with unrelated OAuth secret", http.StatusOK)
	if strings.Contains(get.body, sentinel) || strings.Contains(get.body, "oauth_google_client_secret") {
		t.Errorf("shop settings GET exposed an unrelated OAuth option: %q", get.body)
	}
	if after := shopOptionsDump(t, h); after != before {
		t.Error("shop settings GET changed options")
	}

	put := adm.send(http.MethodPut, "/api/admin/shop/settings", map[string]any{"oauth_google_client_secret": "attacker-controlled-value"})
	if put.code != http.StatusBadRequest && put.code != http.StatusUnprocessableEntity {
		t.Fatalf("PUT unrelated OAuth key returned %d, want 400 or 422; body=%q", put.code, put.body)
	}
	if after := shopOptionsDump(t, h); after != before {
		t.Errorf("rejected unrelated-key PUT changed options from %s to %s", before, after)
	}
}

func TestShopSettingsPreservesNumericValues(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	adm := shopSettingsRegressionAdmin(t, h, "numeric-values")

	response := adm.send(http.MethodPut, "/api/admin/shop/settings", map[string]any{
		"shop_stock_hold_minutes_card": 100,
		"shop_low_stock_default":       0,
	})
	shopWantCode(t, response, "PUT numeric shop settings 100 and 0", http.StatusOK)
	for key, want := range map[string]string{"shop_stock_hold_minutes_card": "100", "shop_low_stock_default": "0"} {
		var option core.Option
		if err := h.db.Where("key = ?", key).First(&option).Error; err != nil {
			t.Fatalf("read saved option %s: %v", key, err)
		}
		if option.Value != want {
			t.Errorf("saved %s = %q, want exact value %q", key, option.Value, want)
		}
	}
}

func TestShopSettingsCanEnableUsingMaskedStoredCredentials(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	shopSetOptions(t, h, map[string]string{
		"payment_ecpay_merchant_id": "2000132",
		"payment_ecpay_hash_key":    "hk-live-abcdef1234",
		"payment_ecpay_hash_iv":     "iv-live-zz5678",
		"payment_ecpay_enabled":     "false",
	})
	adm := shopSettingsRegressionAdmin(t, h, "masked-enable")

	response := adm.send(http.MethodPut, "/api/admin/shop/settings", map[string]any{
		"payment_ecpay_enabled":     true,
		"payment_ecpay_merchant_id": "2000132",
		"payment_ecpay_hash_key":    "****1234",
		"payment_ecpay_hash_iv":     "****5678",
	})
	shopWantCode(t, response, "enable ECPay with masked stored credentials", http.StatusOK)
	for key, want := range map[string]string{
		"payment_ecpay_merchant_id": "2000132",
		"payment_ecpay_hash_key":    "hk-live-abcdef1234",
		"payment_ecpay_hash_iv":     "iv-live-zz5678",
		"payment_ecpay_enabled":     "true",
	} {
		var option core.Option
		if err := h.db.Where("key = ?", key).First(&option).Error; err != nil {
			t.Fatalf("read saved option %s: %v", key, err)
		}
		if option.Value != want {
			t.Errorf("saved %s = %q, want %q", key, option.Value, want)
		}
	}
}

func TestShopSettingsCannotClearCredentialWhileEnabling(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	shopSetOptions(t, h, map[string]string{
		"payment_ecpay_merchant_id": "2000132",
		"payment_ecpay_hash_key":    "hk-live-abcdef1234",
		"payment_ecpay_hash_iv":     "iv-live-zz5678",
		"payment_ecpay_enabled":     "false",
	})
	adm := shopSettingsRegressionAdmin(t, h, "clear-enable")
	before := shopOptionsDump(t, h)

	response := adm.send(http.MethodPut, "/api/admin/shop/settings", map[string]any{
		"payment_ecpay_enabled":     true,
		"payment_ecpay_merchant_id": "2000132",
		"payment_ecpay_hash_key":    "",
		"payment_ecpay_hash_iv":     "****5678",
	})
	if response.code != http.StatusBadRequest && response.code != http.StatusUnprocessableEntity {
		t.Fatalf("enable with explicit credential clear returned %d, want 400 or 422; body=%q", response.code, response.body)
	}
	if after := shopOptionsDump(t, h); after != before {
		t.Errorf("rejected clear-and-enable request changed options from %s to %s", before, after)
	}
}

func TestShopSettingsRejectsExcludedTaxMode(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	adm := shopSettingsRegressionAdmin(t, h, "excluded-tax")
	before := shopOptionsDump(t, h)

	response := adm.send(http.MethodPut, "/api/admin/shop/settings", map[string]any{"shop_tax_mode": "excluded"})
	if response.code != http.StatusBadRequest && response.code != http.StatusUnprocessableEntity {
		t.Fatalf("PUT excluded tax mode returned %d, want 400 or 422; body=%q", response.code, response.body)
	}
	if after := shopOptionsDump(t, h); after != before {
		t.Errorf("rejected excluded tax mode changed options from %s to %s", before, after)
	}
}

func shopSettingsRegressionAdmin(t *testing.T, h *accessHarness, name string) *shopBrowser {
	t.Helper()
	admin := h.user("admin", "shop-settings-"+name)
	browser := newShopBrowser(h)
	browser.token = h.token(admin)
	return browser
}
