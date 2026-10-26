package server

// Coupon integration tests for ticket 07, section E4 (contract: 折扣碼 in docs/tickets/07-shop.md and the
// core.Evaluate / core.Redeem / core.ReleaseRedemption functions). Math tests call core.Evaluate directly; the rest
// go through /api/cart/coupon, /api/checkout and /api/admin/coupons on the dedicated PostgreSQL database. They are
// skipped unless JELLY_TEST_POSTGRES=1 and must fail until the implementation exists. Do not edit them to match an
// implementation.
//
// Rounding assumption: the ticket asks for whole 分 without naming a rule. The percent case uses round half up.

import (
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

func TestCouponMathPerKind(t *testing.T) {
	line := func(qty int, unit int64) core.PricedLine {
		return core.PricedLine{VariantID: 1, ProductID: 1, Qty: qty, UnitPrice: unit, LineTotal: unit * int64(qty)}
	}
	cases := []struct {
		name          string
		coupon        core.Coupon
		quote         core.CartQuote
		wantAmount    int64
		wantFree      bool
		wantFreeUnits int
	}{
		{
			name:       "percent 10 of subtotal 20000",
			coupon:     core.Coupon{Kind: "percent", Value: 10, Scope: "all"},
			quote:      core.CartQuote{Lines: []core.PricedLine{line(2, 10000)}, Subtotal: 20000, ShippingFee: 8000},
			wantAmount: 2000,
		},
		{
			name:       "percent 15 of 333 rounds to whole 分 (49.95 becomes 50)",
			coupon:     core.Coupon{Kind: "percent", Value: 15, Scope: "all"},
			quote:      core.CartQuote{Lines: []core.PricedLine{line(1, 333)}, Subtotal: 333, ShippingFee: 8000},
			wantAmount: 50,
		},
		{
			name:       "fixed 3000 is capped at subtotal 2000",
			coupon:     core.Coupon{Kind: "fixed", Value: 3000, Scope: "all"},
			quote:      core.CartQuote{Lines: []core.PricedLine{line(1, 2000)}, Subtotal: 2000, ShippingFee: 8000},
			wantAmount: 2000,
		},
		{
			name:     "free_shipping waives the fee and takes no money off",
			coupon:   core.Coupon{Kind: "free_shipping", Scope: "all"},
			quote:    core.CartQuote{Lines: []core.PricedLine{line(2, 10000)}, Subtotal: 20000, ShippingFee: 8000},
			wantFree: true,
		},
		{
			name:          "bogo buy 2 get 1 gives one free unit of three",
			coupon:        core.Coupon{Kind: "bogo", BuyQty: 2, GetQty: 1, Scope: "all"},
			quote:         core.CartQuote{Lines: []core.PricedLine{line(3, 10000)}, Subtotal: 30000, ShippingFee: 8000},
			wantAmount:    10000,
			wantFreeUnits: 1,
		},
		{
			name:       "percent 100 leaves only shipping to pay",
			coupon:     core.Coupon{Kind: "percent", Value: 100, Scope: "all"},
			quote:      core.CartQuote{Lines: []core.PricedLine{line(2, 10000)}, Subtotal: 20000, ShippingFee: 8000},
			wantAmount: 20000,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := core.Evaluate(tc.quote, tc.coupon, nil)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if d.Amount != tc.wantAmount {
				t.Errorf("Amount = %d, want %d", d.Amount, tc.wantAmount)
			}
			if d.FreeShipping != tc.wantFree {
				t.Errorf("FreeShipping = %v, want %v", d.FreeShipping, tc.wantFree)
			}
			if d.FreeUnits != tc.wantFreeUnits {
				t.Errorf("FreeUnits = %d, want %d", d.FreeUnits, tc.wantFreeUnits)
			}
			if d.Amount < 0 || d.Amount > tc.quote.Subtotal {
				t.Errorf("Amount %d must lie between 0 and the subtotal %d", d.Amount, tc.quote.Subtotal)
			}
			shipping := tc.quote.ShippingFee
			if d.FreeShipping {
				shipping = 0
			}
			if total := tc.quote.Subtotal + shipping - d.Amount; total < 0 {
				t.Errorf("total = %d is negative", total)
			}
		})
	}
	t.Run("bogo label reads 買 2 送 1", func(t *testing.T) {
		d, err := core.Evaluate(core.CartQuote{Lines: []core.PricedLine{line(3, 10000)}, Subtotal: 30000}, core.Coupon{Kind: "bogo", BuyQty: 2, GetQty: 1, Scope: "all"}, nil)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if d.Label != "買 2 送 1" {
			t.Errorf("Label = %q, want 買 2 送 1", d.Label)
		}
	})
}

func TestCouponScopeMinSubtotalAndWindow(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	tea := shopCatalogSeedCategory(t, h, "Tea", "tea-cat", nil, false)
	mug := shopCatalogSeedCategory(t, h, "Mug", "mug-cat", nil, false)
	inScope := shopCatalogSeedProduct(t, h, shopCatalogProduct{title: "In", slug: "in-tea", status: "active", price: 10000, stock: 10, categories: []core.ProductCategory{tea}})
	outScope := shopCatalogSeedProduct(t, h, shopCatalogProduct{title: "Out", slug: "out-mug", status: "active", price: 20000, stock: 10, categories: []core.ProductCategory{mug}})

	category := shopSeedCoupon(t, h, core.Coupon{Code: "CATSALE", Kind: "percent", Value: 10, Scope: "category"})
	if err := h.db.Create(&core.CouponTarget{CouponID: category.ID, TargetType: "category", TargetID: tea.ID}).Error; err != nil {
		t.Fatalf("target category: %v", err)
	}
	shopSeedCoupon(t, h, core.Coupon{Code: "MINSUB", Kind: "percent", Value: 10, Scope: "all", MinSubtotal: 50000})
	future := time.Now().Add(24 * time.Hour)
	past := time.Now().Add(-24 * time.Hour)
	shopSeedCoupon(t, h, core.Coupon{Code: "FUTURE", Kind: "percent", Value: 10, Scope: "all", StartsAt: &future})
	shopSeedCoupon(t, h, core.Coupon{Code: "PASTEND", Kind: "percent", Value: 10, Scope: "all", EndsAt: &past})

	b := newShopBrowser(h)
	shopAddToCart(t, b, shopVariantRows(t, h, inScope.ID)[0].ID, 1)
	shopAddToCart(t, b, shopVariantRows(t, h, outScope.ID)[0].ID, 1)

	shopWantOK(t, b.send(http.MethodPost, "/api/cart/coupon", map[string]any{"code": "CATSALE"}), "apply category coupon")
	if cart := shopCartGet(t, b); cart.Discount != 1000 {
		t.Errorf("category coupon discount = %d, want 1000 (10%% of the tea line only)", cart.Discount)
	}
	for _, code := range []string{"MINSUB", "FUTURE", "PASTEND"} {
		shopWantCode(t, b.send(http.MethodPost, "/api/cart/coupon", map[string]any{"code": code}), "apply "+code, http.StatusUnprocessableEntity)
	}
}

func TestCouponUsageLimitUnderConcurrency(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "limited-tee", 12000, 100)
	v := shopVariantRows(t, h, p.ID)[0]
	limit := 5
	shopSeedCoupon(t, h, core.Coupon{Code: "LIMIT5", Kind: "percent", Value: 10, UsageLimit: &limit})

	const buyers = 20
	browsers := make([]*shopBrowser, buyers)
	for i := range browsers {
		browsers[i] = newShopBrowser(h)
		shopAddToCart(t, browsers[i], v.ID, 1)
		shopWantOK(t, browsers[i].send(http.MethodPost, "/api/cart/coupon", map[string]any{"code": "LIMIT5"}), "apply LIMIT5")
	}

	responses := make([]shopResponse, buyers)
	var wg sync.WaitGroup
	for i := range browsers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := shopCheckoutBody(fmt.Sprintf("buyer%02d@example.test", i), "credit_card", fmt.Sprintf("limit-race-%02d", i))
			body["coupon_code"] = "LIMIT5"
			responses[i] = browsers[i].send(http.MethodPost, "/api/checkout", body)
		}(i)
	}
	wg.Wait()

	for i, r := range responses {
		if r.panicked != nil || r.code >= 500 {
			t.Errorf("buyer %d: checkout returned %d (panic %v); body=%q", i, r.code, r.panicked, r.body)
		}
	}
	var carrying int64
	if err := h.db.Model(&core.Order{}).Where("coupon_code = ?", "LIMIT5").Count(&carrying).Error; err != nil {
		t.Fatalf("count orders with LIMIT5: %v", err)
	}
	if carrying != int64(limit) {
		t.Errorf("%d orders carry LIMIT5, want exactly %d (usage_limit)", carrying, limit)
	}
	var used int
	if err := h.db.Raw("SELECT used_count FROM coupons WHERE lower(code) = 'limit5'").Scan(&used).Error; err != nil {
		t.Fatalf("read used_count: %v", err)
	}
	if used != limit {
		t.Errorf("used_count = %d, want %d", used, limit)
	}
}

func TestCouponPerUserLimitByUserAndGuestEmail(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "per-user-tee", 12000, 10)
	v := shopVariantRows(t, h, p.ID)[0]
	perUser := 1
	shopSeedCoupon(t, h, core.Coupon{Code: "ONCEEACH", Kind: "percent", Value: 10, PerUserLimit: &perUser})

	first := newShopBrowser(h)
	shopAddToCart(t, first, v.ID, 1)
	shopWantOK(t, first.send(http.MethodPost, "/api/cart/coupon", map[string]any{"code": "ONCEEACH"}), "apply ONCEEACH")
	body := shopCheckoutBody("Buyer@Example.test", "credit_card", "per-user-1")
	body["coupon_code"] = "ONCEEACH"
	shopWantCode(t, first.send(http.MethodPost, "/api/checkout", body), "first checkout", http.StatusCreated)

	// The same guest address in another case and another browser must count as the same person.
	second := newShopBrowser(h)
	shopAddToCart(t, second, v.ID, 1)
	_ = second.send(http.MethodPost, "/api/cart/coupon", map[string]any{"code": "ONCEEACH"})
	body = shopCheckoutBody("buyer@example.test", "credit_card", "per-user-2")
	body["coupon_code"] = "ONCEEACH"
	r := second.send(http.MethodPost, "/api/checkout", body)
	if r.panicked != nil || r.code >= 500 {
		t.Fatalf("second checkout returned %d (panic %v); body=%q", r.code, r.panicked, r.body)
	}
	var carrying int64
	if err := h.db.Model(&core.Order{}).Where("coupon_code = ?", "ONCEEACH").Count(&carrying).Error; err != nil {
		t.Fatalf("count orders: %v", err)
	}
	if carrying != 1 {
		t.Errorf("%d orders carry ONCEEACH, want 1 (per_user_limit 1, email compared case-insensitively)", carrying)
	}
}

func TestCouponMembersOnlyAndStackable(t *testing.T) {
	quote := core.CartQuote{Lines: []core.PricedLine{{VariantID: 1, ProductID: 1, Qty: 1, UnitPrice: 10000, LineTotal: 10000}}, Subtotal: 10000, ShippingFee: 8000}
	members := core.Coupon{Code: "MEMBERS", Kind: "percent", Value: 10, Scope: "all", MembersOnly: true}

	if _, err := core.Evaluate(quote, members, nil); err == nil {
		t.Error("members-only coupon accepted a guest")
	}
	member := &core.User{ID: 7, Email: "member@example.test", Role: "customer"}
	d, err := core.Evaluate(quote, members, member)
	if err != nil {
		t.Fatalf("members-only coupon rejected a member: %v", err)
	}
	if d.Amount != 1000 {
		t.Errorf("member Amount = %d, want 1000", d.Amount)
	}

	stackable := core.Coupon{Code: "STACK", Kind: "percent", Value: 10, Scope: "all", StackableWithFreeShipping: true}
	d, err = core.Evaluate(quote, stackable, nil)
	if err != nil {
		t.Fatalf("stackable percent coupon rejected: %v", err)
	}
	if d.Amount != 1000 || d.FreeShipping {
		t.Errorf("stackable percent coupon: Amount=%d FreeShipping=%v, want 1000 and false", d.Amount, d.FreeShipping)
	}
	free := core.Coupon{Code: "FREESHIP", Kind: "free_shipping", Scope: "all"}
	if d, err = core.Evaluate(quote, free, nil); err != nil || !d.FreeShipping {
		t.Errorf("free_shipping coupon: FreeShipping=%v err=%v, want true and nil", d.FreeShipping, err)
	}
}

func TestCouponBatchGenerateUniqueAndBound(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "batch-tee", 12000, 10)
	v := shopVariantRows(t, h, p.ID)[0]
	admin := h.user("admin", "batchadmin")
	adm := newShopBrowser(h)
	adm.token = h.token(admin)

	shopWantOK(t, adm.send(http.MethodPost, "/api/admin/coupons/generate", map[string]any{
		"prefix": "VIP", "size": 5,
		"template": map[string]any{"kind": "percent", "value": 10, "usage_limit": 1, "bound_email": "vip@example.test"},
	}), "POST /api/admin/coupons/generate")

	var codes []string
	if err := h.db.Raw("SELECT code FROM coupons WHERE batch_id IS NOT NULL ORDER BY id").Scan(&codes).Error; err != nil {
		t.Fatalf("read batch codes: %v", err)
	}
	if len(codes) != 5 {
		t.Fatalf("batch created %d codes, want 5", len(codes))
	}
	seen := map[string]bool{}
	for _, code := range codes {
		if seen[code] {
			t.Errorf("batch code %q is not unique", code)
		}
		seen[code] = true
	}
	code := codes[0]

	// A code bound to one address is refused for another address.
	other := newShopBrowser(h)
	shopAddToCart(t, other, v.ID, 1)
	_ = other.send(http.MethodPost, "/api/cart/coupon", map[string]any{"code": code})
	body := shopCheckoutBody("someone-else@example.test", "credit_card", "bound-other")
	body["coupon_code"] = code
	if r := other.send(http.MethodPost, "/api/checkout", body); r.code == http.StatusCreated {
		var result shopCheckoutResult
		shopDecode(t, r, "checkout with another email", &result)
		if shopOrderRow(t, h, result.Order.Number).CouponCode == code {
			t.Errorf("bound code %q was applied to another email", code)
		}
	}

	// The same code works for the bound address.
	bound := newShopBrowser(h)
	shopAddToCart(t, bound, v.ID, 1)
	_ = bound.send(http.MethodPost, "/api/cart/coupon", map[string]any{"code": code})
	body = shopCheckoutBody("vip@example.test", "credit_card", "bound-vip")
	body["coupon_code"] = code
	shopWantCode(t, bound.send(http.MethodPost, "/api/checkout", body), "checkout for the bound email", http.StatusCreated)
}

func TestCouponReleasedOnCancelOrExpiry(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "release-tee", 12000, 10)
	v := shopVariantRows(t, h, p.ID)[0]
	limit := 10
	coupon := shopSeedCoupon(t, h, core.Coupon{Code: "RELEASE10", Kind: "percent", Value: 10, UsageLimit: &limit})
	admin := h.user("admin", "releaseadmin")

	placeWithCoupon := func(email, key string) core.Order {
		buyer := newShopBrowser(h)
		shopAddToCart(t, buyer, v.ID, 1)
		shopWantOK(t, buyer.send(http.MethodPost, "/api/cart/coupon", map[string]any{"code": "RELEASE10"}), "apply RELEASE10")
		body := shopCheckoutBody(email, "credit_card", key)
		body["coupon_code"] = "RELEASE10"
		var result shopCheckoutResult
		r := buyer.send(http.MethodPost, "/api/checkout", body)
		shopWantCode(t, r, "checkout "+key, http.StatusCreated)
		shopDecode(t, r, "checkout "+key, &result)
		return shopOrderRow(t, h, result.Order.Number)
	}
	usedCount := func() int {
		var used int
		if err := h.db.Raw("SELECT used_count FROM coupons WHERE id = ?", coupon.ID).Scan(&used).Error; err != nil {
			t.Fatalf("read used_count: %v", err)
		}
		return used
	}

	cancelled := placeWithCoupon("cancel@example.test", "release-cancel")
	if got := usedCount(); got != 1 {
		t.Fatalf("used_count after checkout = %d, want 1", got)
	}
	adm := newShopBrowser(h)
	adm.token = h.token(admin)
	shopWantCode(t, adm.send(http.MethodPut, "/api/admin/orders/"+shopIDText(cancelled.ID), map[string]any{"status": "cancelled"}), "cancel pending order", http.StatusOK)
	if got := usedCount(); got != 0 {
		t.Errorf("used_count after cancel = %d, want 0", got)
	}
	var released int64
	h.db.Model(&core.CouponRedemption{}).Where("order_id = ? AND released_at IS NOT NULL", cancelled.ID).Count(&released)
	if released != 1 {
		t.Errorf("cancelled order has %d released redemptions, want 1", released)
	}

	expired := placeWithCoupon("expire@example.test", "release-expire")
	if got := usedCount(); got != 1 {
		t.Fatalf("used_count after second checkout = %d, want 1", got)
	}
	for i := 0; i < 2; i++ {
		tx := h.db.Begin()
		if err := core.ReleaseRedemption(tx, expired.ID); err != nil {
			tx.Rollback()
			t.Fatalf("ReleaseRedemption run %d: %v", i+1, err)
		}
		if err := tx.Commit().Error; err != nil {
			t.Fatalf("commit release run %d: %v", i+1, err)
		}
	}
	if got := usedCount(); got != 0 {
		t.Errorf("used_count after two releases of one order = %d, want 0 (released once)", got)
	}
}

func TestCouponApplyRateLimited(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "rate-tee", 12000, 10)
	v := shopVariantRows(t, h, p.ID)[0]
	b := newShopBrowser(h)
	shopAddToCart(t, b, v.ID, 1)

	sawInvalid, sawLimited := false, false
	for i := 0; i < 30 && !sawLimited; i++ {
		r := b.send(http.MethodPost, "/api/cart/coupon", map[string]any{"code": fmt.Sprintf("WRONG-%03d", i)})
		switch r.code {
		case http.StatusUnprocessableEntity:
			sawInvalid = true
		case http.StatusTooManyRequests:
			sawLimited = true
		}
	}
	if !sawInvalid {
		t.Error("wrong codes were never answered 422")
	}
	if !sawLimited {
		t.Error("30 rapid wrong codes from one IP never got 429")
	}
}

func TestCouponsAdminCRUDAndDeleteOnlyUnused(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	admin := h.user("admin", "couponadmin")
	editor := h.user("editor", "couponeditor")

	ed := newShopBrowser(h)
	ed.token = h.token(editor)
	shopWantCode(t, ed.send(http.MethodPost, "/api/admin/coupons", map[string]any{"code": "EDITORX", "kind": "percent", "value": 10}), "editor POST coupon", http.StatusForbidden)

	adm := newShopBrowser(h)
	adm.token = h.token(admin)
	created := adm.send(http.MethodPost, "/api/admin/coupons", map[string]any{
		"code": "SPRING10", "label": "春季", "kind": "percent", "value": 10, "scope": "all",
		"usage_limit": 100, "per_user_limit": 1, "status": "active",
	})
	shopWantOK(t, created, "POST /api/admin/coupons")
	springID := shopJSONID(t, created, "POST /api/admin/coupons")

	shopWantOK(t, adm.send(http.MethodPatch, "/api/admin/coupons/"+shopIDText(springID), map[string]any{"label": "春季折扣"}), "PATCH coupon label")
	var label string
	if err := h.db.Raw("SELECT label FROM coupons WHERE id = ?", springID).Scan(&label).Error; err != nil || label != "春季折扣" {
		t.Errorf("label after PATCH = %q (err %v), want 春季折扣", label, err)
	}

	if err := h.db.Exec("UPDATE coupons SET used_count = 1 WHERE id = ?", springID).Error; err != nil {
		t.Fatalf("mark coupon used: %v", err)
	}
	shopWantCode(t, adm.send(http.MethodDelete, "/api/admin/coupons/"+shopIDText(springID), nil), "DELETE used coupon", http.StatusConflict)
	shopWantOK(t, adm.send(http.MethodPatch, "/api/admin/coupons/"+shopIDText(springID), map[string]any{"status": "disabled"}), "disable used coupon")
	var status string
	if err := h.db.Raw("SELECT status FROM coupons WHERE id = ?", springID).Scan(&status).Error; err != nil || status != "disabled" {
		t.Errorf("status after disable = %q (err %v), want disabled", status, err)
	}

	unused := adm.send(http.MethodPost, "/api/admin/coupons", map[string]any{"code": "UNUSED10", "kind": "fixed", "value": 500})
	shopWantOK(t, unused, "POST unused coupon")
	unusedID := shopJSONID(t, unused, "POST unused coupon")
	shopWantOK(t, adm.send(http.MethodDelete, "/api/admin/coupons/"+shopIDText(unusedID), nil), "DELETE unused coupon")
	var remaining int64
	h.db.Model(&core.Coupon{}).Where("id = ?", unusedID).Count(&remaining)
	if remaining != 0 {
		t.Errorf("unused coupon still exists after DELETE")
	}
}

func TestCouponsAdminListStatusSegmentsAndImpact(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	admin := h.user("admin", "listadmin")
	future := time.Now().Add(24 * time.Hour)
	past := time.Now().Add(-24 * time.Hour)
	now := shopSeedCoupon(t, h, core.Coupon{Code: "NOW10", Label: "現在", Kind: "percent", Value: 10})
	shopSeedCoupon(t, h, core.Coupon{Code: "LATER10", Label: "之後", Kind: "percent", Value: 10, StartsAt: &future})
	shopSeedCoupon(t, h, core.Coupon{Code: "OLD10", Label: "已過", Kind: "percent", Value: 10, EndsAt: &past})

	// One order this month used NOW10 and saved NT$12.
	buyer := h.user("customer", "impactbuyer")
	buyerID := buyer.ID
	couponID := now.ID
	order := shopSeedOrder(t, h, shopOrderSeed{
		number: fmt.Sprintf("JL-%d-0001", time.Now().Year()), email: buyer.Email, status: "paid", total: 10800, discount: 1200,
		userID: &buyerID, couponID: &couponID, couponCode: "NOW10",
	})
	if err := h.db.Create(&core.CouponRedemption{CouponID: now.ID, OrderID: order.ID, Email: buyer.Email}).Error; err != nil {
		t.Fatalf("seed redemption: %v", err)
	}

	adm := newShopBrowser(h)
	adm.token = h.token(admin)
	type listing struct {
		Items []struct {
			Code string `json:"code"`
		} `json:"items"`
		Counts map[string]int `json:"counts"`
		Impact map[string]any `json:"impact"`
	}
	codesOf := func(query string) (listing, []string) {
		r := adm.get("/api/admin/coupons" + query)
		shopWantCode(t, r, "GET /api/admin/coupons"+query, http.StatusOK)
		var page listing
		shopDecode(t, r, "GET /api/admin/coupons"+query, &page)
		var codes []string
		for _, item := range page.Items {
			codes = append(codes, item.Code)
		}
		return page, codes
	}

	page, codes := codesOf("?status=active")
	if !slices.Equal(codes, []string{"NOW10"}) {
		t.Errorf("status=active lists %v, want [NOW10]", codes)
	}
	for _, segment := range []string{"active", "scheduled", "ended"} {
		if page.Counts[segment] != 1 {
			t.Errorf("counts[%s] = %d, want 1", segment, page.Counts[segment])
		}
	}
	if _, codes := codesOf("?status=scheduled"); !slices.Equal(codes, []string{"LATER10"}) {
		t.Errorf("status=scheduled lists %v, want [LATER10]", codes)
	}
	if _, codes := codesOf("?status=ended"); !slices.Equal(codes, []string{"OLD10"}) {
		t.Errorf("status=ended lists %v, want [OLD10]", codes)
	}
	if _, codes := codesOf("?q=later"); !slices.Equal(codes, []string{"LATER10"}) {
		t.Errorf("q=later lists %v, want [LATER10]", codes)
	}
	if orders, ok := page.Impact["orders"].(float64); !ok || orders != 1 {
		t.Errorf("impact.orders = %v, want 1 (orders this month that used NOW10)", page.Impact["orders"])
	}
	if discount, ok := page.Impact["discount_total"].(float64); !ok || discount != 1200 {
		t.Errorf("impact.discount_total = %v, want 1200", page.Impact["discount_total"])
	}
}
