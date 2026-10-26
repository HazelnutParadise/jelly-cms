package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"golang.org/x/crypto/bcrypt"
)

// These tests use the public cart and checkout routes. A cart cookie is a
// bearer capability for exactly one cart owner; changing the authenticated
// principal must never reveal or mutate the previous owner's private data.
func shopCartOwnershipMember(t *testing.T, h *accessHarness, username string) core.User {
	t.Helper()
	password := "shop-cart-ownership-password"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password for %s: %v", username, err)
	}
	user := core.User{
		Username: username, Email: username + "@example.test", Password: string(hash),
		Role: "customer", Provider: "local", Status: "active",
	}
	if err := h.db.Create(&user).Error; err != nil {
		t.Fatalf("create member %s: %v", username, err)
	}
	return user
}

func shopCartOwnershipLogin(t *testing.T, b *shopBrowser, user core.User) {
	t.Helper()
	r := b.sendWith(http.MethodPost, "/auth/login", map[string]string{
		"username": user.Username, "password": "shop-cart-ownership-password",
	}, map[string]string{"Accept": "application/json"})
	shopWantCode(t, r, "login as "+user.Username, http.StatusOK)
}

func shopCartOwnershipUseCookie(b *shopBrowser, raw string) {
	b.jar[shopCartCookie] = &http.Cookie{Name: shopCartCookie, Value: raw, Path: "/"}
}

func shopCartOwnershipRow(t *testing.T, h *accessHarness, raw string) core.Cart {
	t.Helper()
	var cart core.Cart
	if err := h.db.Where("token_hash = ?", shopSHA256Hex(raw)).First(&cart).Error; err != nil {
		t.Fatalf("load cart by token hash: %v", err)
	}
	return cart
}

func TestShopCartForeignOwnerCookieCannotReadOrMutateCart(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	ownerProduct := shopSeedProduct(t, h, "owner-private-item", 5000, 20)
	otherProduct := shopSeedProduct(t, h, "other-private-item", 7000, 20)
	ownerVariant := shopVariantRows(t, h, ownerProduct.ID)[0]
	otherVariant := shopVariantRows(t, h, otherProduct.ID)[0]
	owner := shopCartOwnershipMember(t, h, "cart-owner-a")
	other := shopCartOwnershipMember(t, h, "cart-owner-b")
	shopSeedCoupon(t, h, core.Coupon{Code: "OWNER-A", Kind: "fixed", Value: 1000, Scope: "all"})
	shopSeedCoupon(t, h, core.Coupon{Code: "OWNER-B", Kind: "fixed", Value: 500, Scope: "all"})

	a := newShopBrowser(h)
	shopCartOwnershipLogin(t, a, owner)
	shopAddToCart(t, a, ownerVariant.ID, 2)
	shopWantOK(t, a.send(http.MethodPost, "/api/cart/coupon", map[string]any{"code": "OWNER-A"}), "apply owner A coupon")
	aCookie := shopCartCookieValue(a)
	if aCookie == "" {
		t.Fatal("owner A has no cart cookie")
	}
	ownerCart := shopCartOwnershipRow(t, h, aCookie)
	if ownerCart.UserID == nil || *ownerCart.UserID != owner.ID {
		t.Fatalf("owner A cart is associated with %v, want user %d", ownerCart.UserID, owner.ID)
	}

	b := newShopBrowser(h)
	shopCartOwnershipLogin(t, b, other)
	shopAddToCart(t, b, otherVariant.ID, 1)
	bHistoryCookie := shopCartCookieValue(b)
	bHistory := shopCartOwnershipRow(t, h, bHistoryCookie)
	var bHistoryItem core.CartItem
	if err := h.db.Where("cart_id = ? AND variant_id = ?", bHistory.ID, otherVariant.ID).First(&bHistoryItem).Error; err != nil {
		t.Fatalf("load owner B history before foreign-cookie request: %v", err)
	}
	if bHistory.UserID == nil || *bHistory.UserID != other.ID || bHistoryItem.Qty != 1 {
		t.Fatalf("owner B history fixture is invalid: cart_id=%d owner=%v item_qty=%d", bHistory.ID, bHistory.UserID, bHistoryItem.Qty)
	}
	shopCartOwnershipUseCookie(b, aCookie)
	foreignRead := b.get("/api/cart")
	shopWantCode(t, foreignRead, "owner B GET using owner A cookie", http.StatusOK)
	var hidden shopCartView
	shopDecode(t, foreignRead, "owner B foreign cart response", &hidden)
	if len(hidden.Items) != 0 || hidden.Subtotal != 0 || hidden.Discount != 0 {
		t.Fatalf("owner B GET exposed owner A cart: %+v", hidden)
	}

	patch := b.send(http.MethodPatch, "/api/cart/items/"+shopIDText(ownerVariant.ID), map[string]any{"qty": 9})
	shopWantCode(t, patch, "owner B PATCH owner A line", http.StatusNotFound)
	remove := b.do(http.MethodDelete, "/api/cart/items/"+shopIDText(ownerVariant.ID), "", "", map[string]string{"X-Jelly-CSRF": "1"})
	shopWantCode(t, remove, "owner B DELETE owner A line", http.StatusNoContent)
	removeCoupon := b.do(http.MethodDelete, "/api/cart/coupon", "", "", map[string]string{"X-Jelly-CSRF": "1"})
	shopWantCode(t, removeCoupon, "owner B DELETE owner A coupon", http.StatusNoContent)
	applyOtherCoupon := b.send(http.MethodPost, "/api/cart/coupon", map[string]any{"code": "OWNER-B"})
	if applyOtherCoupon.code != http.StatusOK {
		bCurrentCookie := shopCartCookieValue(b)
		var replacement core.Cart
		replacementFound := bCurrentCookie != "" && bCurrentCookie != aCookie && h.db.Where("token_hash = ?", shopSHA256Hex(bCurrentCookie)).First(&replacement).Error == nil
		replacementOwner := "none"
		replacementCoupon := ""
		replacementQty := 0
		if replacementFound {
			if replacement.UserID != nil {
				replacementOwner = shopIDText(*replacement.UserID)
			}
			replacementCoupon = replacement.CouponCode
			var line core.CartItem
			if h.db.Where("cart_id = ? AND variant_id = ?", replacement.ID, otherVariant.ID).First(&line).Error == nil {
				replacementQty = line.Qty
			}
		}
		quoteResponse := b.get("/api/cart")
		quote := shopCartView{}
		if quoteResponse.code == http.StatusOK {
			shopDecode(t, quoteResponse, "owner B failure diagnostic quote", &quote)
		}
		var memberCartCount int64
		if err := h.db.Model(&core.Cart{}).Where("user_id = ?", other.ID).Count(&memberCartCount).Error; err != nil {
			t.Fatalf("load owner B cart count after coupon failure: %v", err)
		}
		t.Fatalf("owner B coupon apply returned %d, want 200; fixture diagnosis: history{cart_id=%d owner_ok=%t item_qty=%d}; replacement{cookie_changed=%t found=%t cart_id=%d owner=%s coupon_present=%t item_qty=%d}; carts_for_owner=%d; quote{http=%d subtotal=%d discount=%d coupon_present=%t}; response=%s",
			applyOtherCoupon.code, bHistory.ID, bHistory.UserID != nil && *bHistory.UserID == other.ID, bHistoryItem.Qty,
			bCurrentCookie != "" && bCurrentCookie != aCookie, replacementFound, replacement.ID, replacementOwner, replacementCoupon != "", replacementQty,
			memberCartCount, quoteResponse.code, quote.Subtotal, quote.Discount, len(quote.Coupon) > 0 && string(quote.Coupon) != "null", strings.TrimSpace(applyOtherCoupon.body))
	}
	bCookie := shopCartCookieValue(b)
	if bCookie == "" || bCookie == aCookie {
		t.Fatalf("owner B coupon apply did not replace foreign cookie with a fresh cart token: old=%t new=%t", aCookie != "", bCookie != "")
	}
	bCart := shopCartOwnershipRow(t, h, bCookie)
	if bCart.UserID == nil || *bCart.UserID != other.ID || bCart.CouponCode != "OWNER-B" {
		t.Fatalf("owner B coupon was not stored on a new owner B cart: owner=%v coupon=%q", bCart.UserID, bCart.CouponCode)
	}
	var bItem core.CartItem
	if err := h.db.Where("cart_id = ? AND variant_id = ?", bCart.ID, otherVariant.ID).First(&bItem).Error; err != nil {
		t.Fatalf("owner B historical cart item was not merged into the replacement cart: %v", err)
	}
	if bItem.Qty != 1 {
		t.Fatalf("owner B replacement cart quantity = %d, want its historical quantity 1", bItem.Qty)
	}

	added := b.send(http.MethodPost, "/api/cart/items", map[string]any{"variant_id": otherVariant.ID, "qty": 1})
	shopWantCode(t, added, "owner B add item with owner A cookie", http.StatusOK)
	bCart = shopCartOwnershipRow(t, h, bCookie)
	if bCart.UserID == nil || *bCart.UserID != other.ID {
		t.Fatalf("owner B replacement cart owner is %v, want user %d", bCart.UserID, other.ID)
	}
	var bView shopCartView
	shopDecode(t, added, "owner B add response", &bView)
	if len(bView.Items) != 1 || bView.Items[0].VariantID != otherVariant.ID || bView.Items[0].Qty != 2 {
		t.Errorf("owner B replacement cart contains unexpected lines: %+v", bView.Items)
	}

	var ownerItem core.CartItem
	if err := h.db.Where("cart_id = ? AND variant_id = ?", ownerCart.ID, ownerVariant.ID).First(&ownerItem).Error; err != nil {
		t.Fatalf("owner A line was removed or moved: %v", err)
	}
	if ownerItem.Qty != 2 {
		t.Errorf("owner A quantity changed to %d, want 2", ownerItem.Qty)
	}
	var ownerCartAfter core.Cart
	if err := h.db.First(&ownerCartAfter, ownerCart.ID).Error; err != nil {
		t.Fatalf("reload owner A cart: %v", err)
	}
	if ownerCartAfter.CouponCode != "OWNER-A" || ownerCartAfter.UserID == nil || *ownerCartAfter.UserID != owner.ID {
		t.Errorf("owner A cart changed after owner B requests: owner=%v coupon=%q", ownerCartAfter.UserID, ownerCartAfter.CouponCode)
	}

	guest := newShopBrowser(h)
	shopCartOwnershipUseCookie(guest, aCookie)
	guestRead := guest.get("/api/cart")
	shopWantCode(t, guestRead, "guest GET using owner A cookie", http.StatusOK)
	var guestHidden shopCartView
	shopDecode(t, guestRead, "guest foreign cart response", &guestHidden)
	if len(guestHidden.Items) != 0 || guestHidden.Subtotal != 0 {
		t.Fatalf("guest GET exposed owner A cart: %+v", guestHidden)
	}
	guestAdd := guest.send(http.MethodPost, "/api/cart/items", map[string]any{"variant_id": otherVariant.ID, "qty": 1})
	shopWantCode(t, guestAdd, "guest add using owner A cookie", http.StatusOK)
	guestCookie := shopCartCookieValue(guest)
	if guestCookie == "" || guestCookie == aCookie {
		t.Fatal("guest add did not replace the foreign cookie with a fresh guest cart token")
	}
	guestCart := shopCartOwnershipRow(t, h, guestCookie)
	if guestCart.UserID != nil {
		t.Errorf("guest replacement cart was assigned to user %d", *guestCart.UserID)
	}
	if got := shopCartGet(t, a); len(got.Items) != 1 || got.Items[0].VariantID != ownerVariant.ID || got.Items[0].Qty != 2 {
		t.Errorf("owner A cart changed after guest requests: %+v", got.Items)
	}
}

func TestShopCartForeignOwnerCookieCannotReplayCheckout(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "checkout-owner-private-item", 5000, 20)
	v := shopVariantRows(t, h, p.ID)[0]
	owner := shopCartOwnershipMember(t, h, "checkout-cart-owner-a")
	other := shopCartOwnershipMember(t, h, "checkout-cart-owner-b")

	a := newShopBrowser(h)
	shopCartOwnershipLogin(t, a, owner)
	shopAddToCart(t, a, v.ID, 1)
	aCookie := shopCartCookieValue(a)
	const key = "owner-bound-checkout-replay"
	createdResponse := a.send(http.MethodPost, "/api/checkout", shopCheckoutBody(owner.Email, "credit_card", key))
	shopWantCode(t, createdResponse, "owner A checkout", http.StatusCreated)
	var created shopCheckoutResult
	shopDecode(t, createdResponse, "owner A checkout", &created)
	if created.Order.Number == "" || created.AccessToken == "" {
		t.Fatalf("owner A checkout did not produce an order and access token: %+v", created)
	}

	b := newShopBrowser(h)
	shopCartOwnershipLogin(t, b, other)
	shopCartOwnershipUseCookie(b, aCookie)
	foreignRead := b.get("/api/cart")
	shopWantCode(t, foreignRead, "owner B GET before checkout replay", http.StatusOK)
	var hidden shopCartView
	shopDecode(t, foreignRead, "owner B GET before replay", &hidden)
	if len(hidden.Items) != 0 {
		t.Fatalf("owner B saw owner A cart before replay: %+v", hidden.Items)
	}
	ordersBefore := shopCount(t, h, "SELECT count(*) FROM orders")
	replay := b.send(http.MethodPost, "/api/checkout", shopCheckoutBody(other.Email, "credit_card", key))
	if replay.code != http.StatusConflict {
		t.Fatalf("owner B replay returned %d, want 409 without revealing owner A's order; body=%q", replay.code, replay.body)
	}
	if strings.Contains(replay.body, created.Order.Number) || strings.Contains(replay.body, created.AccessToken) {
		t.Errorf("owner B replay disclosed owner A order or access token: %q", replay.body)
	}
	if got := shopCount(t, h, "SELECT count(*) FROM orders"); got != ordersBefore {
		t.Errorf("foreign checkout replay changed order count from %d to %d", ordersBefore, got)
	}
	var stored core.Order
	if err := h.db.Where("idempotency_key = ?", key).First(&stored).Error; err != nil {
		t.Fatalf("load original order after foreign replay: %v", err)
	}
	if stored.UserID == nil || *stored.UserID != owner.ID || stored.Email != owner.Email || stored.Number != created.Order.Number {
		t.Errorf("foreign checkout replay changed original order ownership/data: owner=%v email=%q number=%q", stored.UserID, stored.Email, stored.Number)
	}
}

func shopWaitForDBBlockedRequest(t *testing.T, h *accessHarness, completed <-chan struct{}) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var blocked int64
		if err := h.db.Raw(`SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND pid <> pg_backend_pid()
			AND cardinality(pg_blocking_pids(pid)) > 0`).Scan(&blocked).Error; err != nil {
			t.Fatalf("observe PostgreSQL lock wait: %v", err)
		}
		if blocked > 0 {
			return
		}
		select {
		case <-completed:
			t.Fatal("login cart promotion finished before it queued behind the anonymous cart-row lock")
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("login cart promotion did not reach a PostgreSQL row-lock wait")
		}
	}
}

func TestShopCartAnonymousGuardLocksAgainstLoginPromotion(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "cart-promotion-locked-item", 6000, 20)
	v := shopVariantRows(t, h, p.ID)[0]
	member := shopCartOwnershipMember(t, h, "cart-promotion-member")
	coupon := shopSeedCoupon(t, h, core.Coupon{Code: "PROMOTION-OLD-GUEST", Kind: "fixed", Value: 500, Scope: "all"})
	otherCoupon := shopSeedCoupon(t, h, core.Coupon{Code: "PROMOTION-NEW-GUEST", Kind: "fixed", Value: 100, Scope: "all"})

	guest := newShopBrowser(h)
	shopAddToCart(t, guest, v.ID, 2)
	shopWantOK(t, guest.send(http.MethodPost, "/api/cart/coupon", map[string]any{"code": coupon.Code}), "apply guest promotion coupon")
	rawToken := shopCartCookieValue(guest)
	if rawToken == "" {
		t.Fatal("guest cart has no cookie before login promotion")
	}
	guestCart := shopCartOwnershipRow(t, h, rawToken)
	var guestLine core.CartItem
	if err := h.db.Where("cart_id = ? AND variant_id = ?", guestCart.ID, v.ID).First(&guestLine).Error; err != nil {
		t.Fatalf("load guest line before promotion: %v", err)
	}

	// Hold the same row lock acquired by an anonymous cart operation after it
	// authenticated the still-guest owner. The member request must queue behind
	// this lock before promoting the row.
	anonymousTx := h.db.Begin()
	if anonymousTx.Error != nil {
		t.Fatalf("begin anonymous cart transaction: %v", anonymousTx.Error)
	}
	defer anonymousTx.Rollback()
	if _, err := shopLockOwnedCart(anonymousTx, guestCart, nil); err != nil {
		t.Fatalf("anonymous cart guard rejected its current guest owner: %v", err)
	}

	memberBrowser := newShopBrowser(h)
	shopCartOwnershipLogin(t, memberBrowser, member)
	shopCartOwnershipUseCookie(memberBrowser, rawToken)
	completed := make(chan struct{})
	var promoted shopResponse
	go func() {
		promoted = memberBrowser.get("/api/cart")
		close(completed)
	}()
	shopWaitForDBBlockedRequest(t, h, completed)
	if err := anonymousTx.Commit().Error; err != nil {
		t.Fatalf("release anonymous row lock: %v", err)
	}
	select {
	case <-completed:
	case <-time.After(5 * time.Second):
		t.Fatal("member cart promotion did not finish after the anonymous transaction released its row lock")
	}
	shopWantCode(t, promoted, "member promotion after anonymous guard", http.StatusOK)
	if got := shopCartGet(t, memberBrowser); len(got.Items) != 1 || got.Items[0].VariantID != v.ID || got.Items[0].Qty != guestLine.Qty {
		t.Fatalf("member promotion did not retain the guest cart line: %+v", got.Items)
	}
	promotedCart := shopCartOwnershipRow(t, h, rawToken)
	if promotedCart.UserID == nil || *promotedCart.UserID != member.ID || promotedCart.CouponCode != coupon.Code {
		t.Fatalf("promoted cart owner/coupon = %v/%q, want member %d/%q", promotedCart.UserID, promotedCart.CouponCode, member.ID, coupon.Code)
	}

	oldGuestRead := guest.get("/api/cart")
	shopWantCode(t, oldGuestRead, "old anonymous GET after promotion", http.StatusOK)
	var hidden shopCartView
	shopDecode(t, oldGuestRead, "old anonymous quote after promotion", &hidden)
	if len(hidden.Items) != 0 || hidden.Subtotal != 0 || hidden.Discount != 0 {
		t.Fatalf("old anonymous quote exposed the promoted member cart: %+v", hidden)
	}
	patch := guest.send(http.MethodPatch, "/api/cart/items/"+shopIDText(v.ID), map[string]any{"qty": 9})
	shopWantCode(t, patch, "old anonymous PATCH after promotion", http.StatusNotFound)
	remove := guest.do(http.MethodDelete, "/api/cart/items/"+shopIDText(v.ID), "", "", map[string]string{"X-Jelly-CSRF": "1"})
	shopWantCode(t, remove, "old anonymous DELETE after promotion", http.StatusNoContent)
	apply := guest.send(http.MethodPost, "/api/cart/coupon", map[string]any{"code": otherCoupon.Code})
	shopWantCode(t, apply, "old anonymous coupon apply to its empty replacement cart", http.StatusUnprocessableEntity)

	// Coupon evaluation above creates a fresh replacement cart after the old
	// anonymous cookie was rejected. Replay the promoted cart's original cookie
	// here so this checkout specifically proves the old capability is unusable.
	shopCartOwnershipUseCookie(guest, rawToken)
	ordersBefore := shopCount(t, h, "SELECT count(*) FROM orders")
	checkout := guest.send(http.MethodPost, "/api/checkout", shopCheckoutBody("old-guest@example.test", "credit_card", "old-guest-after-promotion"))
	shopWantCode(t, checkout, "old anonymous checkout after promotion", http.StatusConflict)
	var unavailable struct {
		Code string `json:"code"`
	}
	shopDecode(t, checkout, "old anonymous checkout after promotion", &unavailable)
	if unavailable.Code != "cart_unavailable" {
		t.Fatalf("old anonymous checkout code = %q, want cart_unavailable", unavailable.Code)
	}
	if got := shopCount(t, h, "SELECT count(*) FROM orders"); got != ordersBefore {
		t.Errorf("old anonymous checkout created an order: before=%d after=%d", ordersBefore, got)
	}
	var promotedLine core.CartItem
	if err := h.db.Where("cart_id = ? AND variant_id = ?", promotedCart.ID, v.ID).First(&promotedLine).Error; err != nil {
		t.Fatalf("promoted member line was removed or moved: %v", err)
	}
	if promotedLine.Qty != guestLine.Qty {
		t.Errorf("old anonymous operations changed promoted member quantity from %d to %d", guestLine.Qty, promotedLine.Qty)
	}
	var promotedAfter core.Cart
	if err := h.db.First(&promotedAfter, promotedCart.ID).Error; err != nil {
		t.Fatalf("reload promoted member cart: %v", err)
	}
	if promotedAfter.CouponCode != coupon.Code || promotedAfter.UserID == nil || *promotedAfter.UserID != member.ID {
		t.Errorf("old anonymous coupon apply changed promoted cart owner/coupon: %v/%q", promotedAfter.UserID, promotedAfter.CouponCode)
	}
}

func TestShopCartQuoteWithSingleDBConnectionDoesNotDeadlock(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "single-connection-cart-item", 5000, 20)
	v := shopVariantRows(t, h, p.ID)[0]
	b := newShopBrowser(h)
	shopAddToCart(t, b, v.ID, 2)

	sqlDB, err := h.db.DB()
	if err != nil {
		t.Fatalf("get SQL pool: %v", err)
	}
	previousMax := sqlDB.Stats().MaxOpenConnections
	sqlDB.SetMaxOpenConns(1)
	defer sqlDB.SetMaxOpenConns(previousMax)
	result := make(chan shopResponse, 1)
	go func() { result <- b.get("/api/cart") }()
	select {
	case response := <-result:
		shopWantCode(t, response, "GET /api/cart with one SQL connection", http.StatusOK)
		var quote shopCartView
		shopDecode(t, response, "single-connection cart quote", &quote)
		if len(quote.Items) != 1 || quote.Items[0].VariantID != v.ID || quote.Items[0].Qty != 2 || quote.Subtotal != 10000 {
			t.Errorf("single-connection quote = %+v, want the two-item cart totaling 10000", quote)
		}
	case <-time.After(3 * time.Second):
		// Restore the pool limit before failing so a global-DB lookup nested in
		// the quote transaction can finish rather than stranding the test.
		sqlDB.SetMaxOpenConns(previousMax)
		select {
		case <-result:
		case <-time.After(3 * time.Second):
		}
		t.Fatal("cart quote waited for a second pooled SQL connection while holding its transaction")
	}
}
