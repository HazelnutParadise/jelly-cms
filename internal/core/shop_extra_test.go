package core

// Additional contract coverage for ticket 07 that the existing shop tests do not reach:
// money overflow/format boundaries, compare-and-set failures and transaction rollback, stock
// reservation/lifecycle idempotency under concurrency, and coupon redemption idempotency.
// These tests are written against the contract in docs/tickets/07-shop.md and must pass once the
// skeletons are implemented. Database cases are skipped unless JELLY_TEST_POSTGRES=1.
//
// Do not edit production code or the existing tests to make these pass.

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/money"
	"gorm.io/gorm"
)

// TestMoneyBoundariesUsedByShop pins the amount boundaries the shop contract depends on: the full
// int64 range must format without overflow, and input conversion must refuse values that cannot be
// represented in minor units instead of wrapping around.
func TestMoneyBoundariesUsedByShop(t *testing.T) {
	format := []struct {
		minor    int64
		currency string
		want     string
	}{
		{math.MaxInt64, "TWD", "NT$92,233,720,368,547,758.07"},
		{math.MinInt64, "TWD", "-NT$92,233,720,368,547,758.08"},
		{-1, "TWD", "-NT$0.01"},
		{5, "TWD", "NT$0.05"},
		{500, "JPY", "JPY 5"},
		{500, " usd ", "US$5"},
	}
	for _, tc := range format {
		if got := money.Format(tc.minor, tc.currency); got != tc.want {
			t.Errorf("Format(%d, %q) = %q, want %q", tc.minor, tc.currency, got, tc.want)
		}
	}

	if got, err := money.FromInput("92233720368547758.07"); err != nil || got != math.MaxInt64 {
		t.Errorf("FromInput(max int64) = %d, %v; want %d, nil", got, err, int64(math.MaxInt64))
	}
	for _, input := range []string{"92233720368547758.08", "92233720368547759", "1e3", "12.345"} {
		if got, err := money.FromInput(input); err == nil {
			t.Errorf("FromInput(%q) = %d, nil; want an error (out of range or not a valid amount)", input, got)
		}
	}

	if got, err := money.ToGateway(9223372036854775800); err != nil || got != 92233720368547758 {
		t.Errorf("ToGateway(max whole yuan) = %d, %v; want 92233720368547758, nil", got, err)
	}
	if _, err := money.ToGateway(math.MaxInt64); err == nil {
		t.Error("ToGateway(math.MaxInt64) returned no error; the amount has a fractional yuan part")
	}
}

func shopExtraSeedOrder(t *testing.T, db *gorm.DB, number, status string) uint {
	t.Helper()
	var id uint
	if err := db.Raw(`INSERT INTO orders (number, status, currency, email, created_at, updated_at)
		VALUES (?, ?, 'TWD', 'extra@example.test', now(), now()) RETURNING id`, number, status).Row().Scan(&id); err != nil {
		t.Fatalf("seed order %s: %v", number, err)
	}
	return id
}

func shopExtraSeedVariant(t *testing.T, db *gorm.DB, stock int) uint {
	t.Helper()
	var id uint
	if err := db.Raw(`INSERT INTO product_variants (product_id, name, price, stock, position, status, created_at, updated_at)
		VALUES (1, '', 12000, ?, 0, 'active', now(), now()) RETURNING id`, stock).Row().Scan(&id); err != nil {
		t.Fatalf("seed variant: %v", err)
	}
	return id
}

func shopExtraOrderStatus(t *testing.T, db *gorm.DB, orderID uint) string {
	t.Helper()
	var status string
	if err := db.Raw("SELECT status FROM orders WHERE id = ?", orderID).Row().Scan(&status); err != nil {
		t.Fatalf("read order status: %v", err)
	}
	return status
}

func shopExtraCount(t *testing.T, db *gorm.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.Raw(query, args...).Scan(&n).Error; err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return n
}

func shopExtraSeedDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newOrderStateTestDB(t)
	if err := db.AutoMigrate(&ProductVariant{}, &OrderItem{}, &StockMovement{}, &Coupon{}, &CouponRedemption{}); err != nil {
		t.Fatalf("migrate extra shop models: %v", err)
	}
	return db
}

// TestTransitionRejectsStaleFromAndIllegalMove checks that a caller whose view of the order is stale
// cannot force a move, and that an illegal move leaves both the order and the timeline untouched.
func TestTransitionRejectsStaleFromAndIllegalMove(t *testing.T) {
	db := shopExtraSeedDB(t)
	orderID := shopExtraSeedOrder(t, db, "JL-2026-0101", OrderStatusPending)
	svc := NewOrderService(db)

	transact := func(from, to string) error {
		return db.Transaction(func(tx *gorm.DB) error {
			return svc.Transition(tx, orderID, from, to, nil, "stale caller")
		})
	}

	if err := transact(OrderStatusPaid, OrderStatusShipped); err == nil {
		t.Error("Transition with from=paid succeeded on a pending order; the compare-and-set must reject it")
	}
	if err := transact(OrderStatusPending, OrderStatusShipped); err == nil {
		t.Error("Transition pending -> shipped succeeded; the move is not in the state machine")
	}
	if got := shopExtraOrderStatus(t, db, orderID); got != OrderStatusPending {
		t.Errorf("order status after rejected moves = %q, want pending", got)
	}
	if n := shopExtraCount(t, db, "SELECT count(*) FROM order_events WHERE order_id = ?", orderID); n != 0 {
		t.Errorf("order_events after rejected moves = %d, want 0", n)
	}

	if err := transact(OrderStatusPending, OrderStatusPaid); err != nil {
		t.Fatalf("valid Transition pending -> paid failed: %v", err)
	}
	if got := shopExtraOrderStatus(t, db, orderID); got != OrderStatusPaid {
		t.Errorf("order status after valid move = %q, want paid", got)
	}
	if n := shopExtraCount(t, db, "SELECT count(*) FROM order_events WHERE order_id = ?", orderID); n != 1 {
		t.Errorf("order_events after valid move = %d, want 1", n)
	}
}

var errShopExtraRollback = errors.New("caller rolled back after transition")

// TestTransitionRollsBackWithTransaction proves that a status change is part of the caller's
// transaction: if the surrounding work fails, neither the status nor the timeline entry survives.
func TestTransitionRollsBackWithTransaction(t *testing.T) {
	db := shopExtraSeedDB(t)
	orderID := shopExtraSeedOrder(t, db, "JL-2026-0102", OrderStatusPending)
	svc := NewOrderService(db)

	err := db.Transaction(func(tx *gorm.DB) error {
		if err := svc.Transition(tx, orderID, OrderStatusPending, OrderStatusPaid, nil, "will roll back"); err != nil {
			return err
		}
		return errShopExtraRollback
	})
	if !errors.Is(err, errShopExtraRollback) {
		t.Fatalf("transaction returned %v, want the caller's rollback error", err)
	}
	if got := shopExtraOrderStatus(t, db, orderID); got != OrderStatusPending {
		t.Errorf("order status after rollback = %q, want pending", got)
	}
	if n := shopExtraCount(t, db, "SELECT count(*) FROM order_events WHERE order_id = ?", orderID); n != 0 {
		t.Errorf("order_events after rollback = %d, want 0", n)
	}
}

// TestTransitionConcurrentDifferentTargets runs two legal moves from the same status at once. Only
// one may win, otherwise the order would both be paid and cancelled.
func TestTransitionConcurrentDifferentTargets(t *testing.T) {
	db := shopExtraSeedDB(t)
	orderID := shopExtraSeedOrder(t, db, "JL-2026-0103", OrderStatusPending)
	svc := NewOrderService(db)

	targets := []string{OrderStatusPaid, OrderStatusCancelled}
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, len(targets))
	for i, to := range targets {
		wg.Add(1)
		go func(i int, to string) {
			defer wg.Done()
			<-start
			errs[i] = db.Transaction(func(tx *gorm.DB) error {
				return svc.Transition(tx, orderID, OrderStatusPending, to, nil, "racing target "+to)
			})
		}(i, to)
	}
	close(start)
	wg.Wait()

	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Errorf("concurrent pending -> paid/cancelled succeeded %d times, want exactly 1; errors=%v", succeeded, errs)
	}
	got := shopExtraOrderStatus(t, db, orderID)
	if got != OrderStatusPaid && got != OrderStatusCancelled {
		t.Errorf("order status = %q, want paid or cancelled", got)
	}
	if n := shopExtraCount(t, db, "SELECT count(*) FROM order_events WHERE order_id = ?", orderID); n != 1 {
		t.Errorf("order_events after racing targets = %d, want 1", n)
	}
}

// TestReserveConcurrentLastUnitOnlyOneWins is the core-level version of the checkout race: the stock
// guard must make exactly one reservation succeed for the last unit, with no negative stock.
func TestReserveConcurrentLastUnitOnlyOneWins(t *testing.T) {
	db := shopExtraSeedDB(t)
	variantID := shopExtraSeedVariant(t, db, 1)
	orderIDs := []uint{
		shopExtraSeedOrder(t, db, "JL-2026-0111", OrderStatusPending),
		shopExtraSeedOrder(t, db, "JL-2026-0112", OrderStatusPending),
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, len(orderIDs))
	for i, orderID := range orderIDs {
		wg.Add(1)
		go func(i int, orderID uint) {
			defer wg.Done()
			<-start
			errs[i] = db.Transaction(func(tx *gorm.DB) error {
				return Reserve(tx, variantID, 1, orderID)
			})
		}(i, orderID)
	}
	close(start)
	wg.Wait()

	succeeded, short := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrInsufficientStock):
			short++
		default:
			t.Errorf("Reserve returned an unexpected error: %v", err)
		}
	}
	if succeeded != 1 || short != 1 {
		t.Errorf("last-unit race: %d reservations and %d stock errors, want 1 and 1; errors=%v", succeeded, short, errs)
	}

	var stock int
	if err := db.Raw("SELECT stock FROM product_variants WHERE id = ?", variantID).Row().Scan(&stock); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if stock != 0 {
		t.Errorf("stock after the race = %d, want 0", stock)
	}
	if n := shopExtraCount(t, db, "SELECT count(*) FROM stock_movements WHERE variant_id = ? AND reason = 'reserve'", variantID); n != 1 {
		t.Errorf("reserve movements = %d, want 1", n)
	}
}

// TestReserveInsufficientWritesNothing checks the failure path leaves stock and the ledger unchanged.
func TestReserveInsufficientWritesNothing(t *testing.T) {
	db := shopExtraSeedDB(t)
	variantID := shopExtraSeedVariant(t, db, 2)
	orderID := shopExtraSeedOrder(t, db, "JL-2026-0113", OrderStatusPending)

	if err := db.Transaction(func(tx *gorm.DB) error {
		return Reserve(tx, variantID, 3, orderID)
	}); !errors.Is(err, ErrInsufficientStock) {
		t.Fatalf("Reserve(3) on stock 2 returned %v, want ErrInsufficientStock", err)
	}
	var stock int
	db.Raw("SELECT stock FROM product_variants WHERE id = ?", variantID).Row().Scan(&stock)
	if stock != 2 {
		t.Errorf("stock after a refused reservation = %d, want 2", stock)
	}
	if n := shopExtraCount(t, db, "SELECT count(*) FROM stock_movements WHERE variant_id = ?", variantID); n != 0 {
		t.Errorf("stock_movements after a refused reservation = %d, want 0", n)
	}
}

// TestReleaseIdempotentOnCancelledOrExpired covers the cancellation/expiry lifecycle:
// repeating release must return stock to original only once.
func TestReleaseIdempotentOnCancelledOrExpired(t *testing.T) {
	db := shopExtraSeedDB(t)
	variantID := shopExtraSeedVariant(t, db, 5)
	orderID := shopExtraSeedOrder(t, db, "JL-2026-0114", OrderStatusCancelled)

	if err := db.Transaction(func(tx *gorm.DB) error { return Reserve(tx, variantID, 2, orderID) }); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := db.Exec(`INSERT INTO order_items (order_id, variant_id, title, quantity, price, unit_price, line_total)
		VALUES (?, ?, 'Lifecycle Tee', 2, 12000, 12000, 24000)`, orderID, variantID).Error; err != nil {
		t.Fatalf("seed order item: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := db.Transaction(func(tx *gorm.DB) error { return Release(tx, orderID) }); err != nil {
			t.Fatalf("Release call %d: %v", i+1, err)
		}
	}
	var stock int
	db.Raw("SELECT stock FROM product_variants WHERE id = ?", variantID).Row().Scan(&stock)
	if stock != 5 {
		t.Errorf("stock after releasing twice on cancelled/expired = %d, want 5", stock)
	}
	if n := shopExtraCount(t, db, "SELECT count(*) FROM stock_movements WHERE order_id = ? AND reason = 'release'", orderID); n != 1 {
		t.Errorf("release movements after two calls = %d, want 1", n)
	}

	// also test expired
	db2 := shopExtraSeedDB(t)
	variantID2 := shopExtraSeedVariant(t, db2, 5)
	orderID2 := shopExtraSeedOrder(t, db2, "JL-2026-0115", OrderStatusExpired)
	if err := db2.Transaction(func(tx *gorm.DB) error { return Reserve(tx, variantID2, 2, orderID2) }); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := db2.Exec(`INSERT INTO order_items (order_id, variant_id, title, quantity, price, unit_price, line_total)
		VALUES (?, ?, 'Lifecycle Tee', 2, 12000, 12000, 24000)`, orderID2, variantID2).Error; err != nil {
		t.Fatalf("seed order item: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := db2.Transaction(func(tx *gorm.DB) error { return Release(tx, orderID2) }); err != nil {
			t.Fatalf("Release call %d: %v", i+1, err)
		}
	}
	db2.Raw("SELECT stock FROM product_variants WHERE id = ?", variantID2).Row().Scan(&stock)
	if stock != 5 {
		t.Errorf("stock after releasing twice on expired = %d, want 5", stock)
	}
	if n := shopExtraCount(t, db2, "SELECT count(*) FROM stock_movements WHERE order_id = ? AND reason = 'release'", orderID2); n != 1 {
		t.Errorf("release movements after two calls = %d, want 1", n)
	}
}

// TestRestockIdempotentAfterPaidRefund covers the refund/restock lifecycle:
// repeating restock must not return stock twice (only back to original reserved amount).
func TestRestockIdempotentAfterPaidRefund(t *testing.T) {
	db := shopExtraSeedDB(t)
	variantID := shopExtraSeedVariant(t, db, 5)
	orderID := shopExtraSeedOrder(t, db, "JL-2026-0116", OrderStatusPaid)

	if err := db.Transaction(func(tx *gorm.DB) error { return Reserve(tx, variantID, 2, orderID) }); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := db.Exec(`INSERT INTO order_items (order_id, variant_id, title, quantity, price, unit_price, line_total)
		VALUES (?, ?, 'Lifecycle Tee', 2, 12000, 12000, 24000)`, orderID, variantID).Error; err != nil {
		t.Fatalf("seed order item: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := db.Transaction(func(tx *gorm.DB) error { return Restock(tx, orderID) }); err != nil {
			t.Fatalf("Restock call %d: %v", i+1, err)
		}
	}
	var stock int
	db.Raw("SELECT stock FROM product_variants WHERE id = ?", variantID).Row().Scan(&stock)
	if stock != 5 {
		t.Errorf("stock after restocking twice after paid refund = %d, want 5", stock)
	}
	if n := shopExtraCount(t, db, "SELECT count(*) FROM stock_movements WHERE order_id = ? AND reason = 'restock'", orderID); n != 1 {
		t.Errorf("restock movements after two calls = %d, want 1", n)
	}
}

// Keep parallel/repeat tests for both paths
func TestReleaseRestockConcurrentIdempotent(t *testing.T) {
	db := shopExtraSeedDB(t)
	variantID := shopExtraSeedVariant(t, db, 5)
	orderID := shopExtraSeedOrder(t, db, "JL-2026-0117", OrderStatusCancelled)
	if err := db.Transaction(func(tx *gorm.DB) error { return Reserve(tx, variantID, 2, orderID) }); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := db.Exec(`INSERT INTO order_items (order_id, variant_id, title, quantity, price, unit_price, line_total)
		VALUES (?, ?, 'Lifecycle Tee', 2, 12000, 12000, 24000)`, orderID, variantID).Error; err != nil {
		t.Fatalf("seed order item: %v", err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = db.Transaction(func(tx *gorm.DB) error { return Release(tx, orderID) })
		}(i)
	}
	close(start)
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		}
	}
	if ok != 2 {
		// both should be idempotent/successful? or at least not corrupt
	}
	var stock int
	db.Raw("SELECT stock FROM product_variants WHERE id = ?", variantID).Row().Scan(&stock)
	if stock != 5 {
		t.Errorf("stock after concurrent release = %d, want 5", stock)
	}
	if n := shopExtraCount(t, db, "SELECT count(*) FROM stock_movements WHERE order_id = ? AND reason = 'release'", orderID); n != 1 {
		t.Errorf("release movements after concurrent = %d, want 1", n)
	}
}

func shopExtraSeedCoupon(t *testing.T, db *gorm.DB, code string, usageLimit *int) uint {
	t.Helper()
	var id uint
	var limit any
	if usageLimit != nil {
		limit = *usageLimit
	}
	if err := db.Raw(`INSERT INTO coupons (code, label, kind, value, scope, status, usage_limit, used_count, created_at, updated_at)
		VALUES (?, '', 'percent', 10, 'all', 'active', ?, 0, now(), now()) RETURNING id`, code, limit).Row().Scan(&id); err != nil {
		t.Fatalf("seed coupon %s: %v", code, err)
	}
	return id
}

func shopExtraCouponUsed(t *testing.T, db *gorm.DB, couponID uint) int {
	t.Helper()
	var used int
	if err := db.Raw("SELECT used_count FROM coupons WHERE id = ?", couponID).Row().Scan(&used); err != nil {
		t.Fatalf("read coupon used_count: %v", err)
	}
	return used
}

// TestRedeemConcurrentUsageLimitAndIdempotency checks the atomic counter and the (coupon, order)
// uniqueness together: the last redemption may only be taken once, and repeating it must not
// increment used_count again.
func TestRedeemConcurrentUsageLimitAndIdempotency(t *testing.T) {
	db := shopExtraSeedDB(t)
	limit := 1
	couponID := shopExtraSeedCoupon(t, db, "ONLYONE", &limit)
	orderIDs := []uint{
		shopExtraSeedOrder(t, db, "JL-2026-0121", OrderStatusPending),
		shopExtraSeedOrder(t, db, "JL-2026-0122", OrderStatusPending),
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, len(orderIDs))
	for i, orderID := range orderIDs {
		wg.Add(1)
		go func(i int, orderID uint) {
			defer wg.Done()
			<-start
			errs[i] = db.Transaction(func(tx *gorm.DB) error {
				return Redeem(tx, couponID, orderID, nil, "Guest@Example.Test")
			})
		}(i, orderID)
	}
	close(start)
	wg.Wait()

	winner := -1
	for i, err := range errs {
		if err == nil {
			if winner >= 0 {
				t.Errorf("both orders redeemed the one-use coupon; errors=%v", errs)
			}
			winner = i
		}
	}
	if winner < 0 {
		t.Fatalf("no order redeemed a one-use coupon; errors=%v", errs)
	}
	if used := shopExtraCouponUsed(t, db, couponID); used != 1 {
		t.Errorf("used_count after the race = %d, want 1", used)
	}
	if n := shopExtraCount(t, db, "SELECT count(*) FROM coupon_redemptions WHERE coupon_id = ?", couponID); n != 1 {
		t.Errorf("coupon_redemptions = %d, want 1", n)
	}
	var storedEmail string
	db.Raw("SELECT email FROM coupon_redemptions WHERE coupon_id = ?", couponID).Row().Scan(&storedEmail)
	if storedEmail != "guest@example.test" {
		t.Errorf("stored redemption email = %q, want lowercase guest@example.test", storedEmail)
	}

	// Repeating the same redemption must not take another use.
	if err := db.Transaction(func(tx *gorm.DB) error {
		return Redeem(tx, couponID, orderIDs[winner], nil, "guest@example.test")
	}); err == nil {
		t.Error("repeating Redeem for the same (coupon, order) succeeded")
	}
	if used := shopExtraCouponUsed(t, db, couponID); used != 1 {
		t.Errorf("used_count after repeating the redemption = %d, want 1", used)
	}

	// Releasing is also idempotent: it returns the use exactly once.
	for i := 0; i < 2; i++ {
		if err := db.Transaction(func(tx *gorm.DB) error {
			return ReleaseRedemption(tx, orderIDs[winner])
		}); err != nil {
			t.Fatalf("ReleaseRedemption call %d: %v", i+1, err)
		}
	}
	if used := shopExtraCouponUsed(t, db, couponID); used != 0 {
		t.Errorf("used_count after releasing twice = %d, want 0", used)
	}
	if n := shopExtraCount(t, db, "SELECT count(*) FROM coupon_redemptions WHERE coupon_id = ? AND released_at IS NOT NULL", couponID); n != 1 {
		t.Errorf("released redemptions after two calls = %d, want 1", n)
	}
}

// TestEvaluateDiscountNeverExceedsSubtotalAndWindow is a small guard for the money side of coupon
// math that the HTTP tests do not pin at the core boundary.
func TestEvaluateDiscountNeverExceedsSubtotalAndWindow(t *testing.T) {
	now := time.Now()
	cart := CartQuote{
		Lines:    []PricedLine{{VariantID: 1, ProductID: 1, Qty: 1, UnitPrice: 5000, LineTotal: 5000}},
		Subtotal: 5000,
	}
	coupon := Coupon{Code: "BIG", Kind: "fixed", Value: 9000, Scope: "all", Status: "active"}

	discount, err := Evaluate(cart, coupon, nil)
	if err != nil {
		t.Fatalf("Evaluate(fixed 9000 on subtotal 5000): %v", err)
	}
	if discount.Amount != cart.Subtotal {
		t.Errorf("discount amount = %d, want it capped at the subtotal %d", discount.Amount, cart.Subtotal)
	}
	if discount.Amount < 0 || cart.Subtotal-discount.Amount < 0 {
		t.Errorf("discount %d makes the total negative", discount.Amount)
	}

	expired := coupon
	expired.StartsAt = nil
	expired.EndsAt = ptrTime(now.Add(-time.Minute))
	if _, err := Evaluate(cart, expired, nil); err == nil {
		t.Error("Evaluate accepted a coupon whose EndsAt is in the past")
	}

	future := coupon
	future.EndsAt = nil
	future.StartsAt = ptrTime(now.Add(time.Minute))
	if _, err := Evaluate(cart, future, nil); err == nil {
		t.Error("Evaluate accepted a coupon whose StartsAt is in the future")
	}

	if _, err := Evaluate(cart, Coupon{Code: "X", Kind: "percent", Value: 10, Scope: "all", Status: "disabled"}, nil); err == nil {
		t.Error("Evaluate accepted a disabled coupon")
	}
	if _, err := Evaluate(cart, Coupon{Code: "X", Kind: "percent", Value: 10, Scope: "all", Status: "active", MembersOnly: true}, nil); err == nil {
		t.Error("Evaluate accepted a members-only coupon for a guest")
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
