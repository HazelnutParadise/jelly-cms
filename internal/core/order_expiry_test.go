package core

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"
)

func expirySeed(t *testing.T, db *gorm.DB, index int, variantID uint, expires time.Time, couponID *uint) uint {
	t.Helper()
	if err := db.AutoMigrate(&OrderItem{}); err != nil {
		t.Fatalf("migrate order items: %v", err)
	}
	orderID := shopExtraSeedOrder(t, db, fmt.Sprintf("JL-2026-%04d", 3000+index), OrderStatusPending)
	if err := db.Model(&Order{}).Where("id = ?", orderID).Updates(map[string]interface{}{"expires_at": expires, "coupon_id": couponID}).Error; err != nil {
		t.Fatalf("set expiry on order %d: %v", orderID, err)
	}
	if err := db.Exec(`INSERT INTO order_items (order_id, variant_id, title, quantity, price, unit_price, line_total)
		VALUES (?, ?, 'Expiry Tee', 2, 12000, 12000, 24000)`, orderID, variantID).Error; err != nil {
		t.Fatalf("seed order item: %v", err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return Reserve(tx, variantID, 2, orderID) }); err != nil {
		t.Fatalf("reserve stock for order %d: %v", orderID, err)
	}
	if couponID != nil {
		if err := db.Exec("UPDATE coupons SET used_count = 1 WHERE id = ?", *couponID).Error; err != nil {
			t.Fatalf("seed coupon usage: %v", err)
		}
		if err := db.Create(&CouponRedemption{CouponID: *couponID, OrderID: orderID, Email: "expiry@example.test"}).Error; err != nil {
			t.Fatalf("seed coupon redemption: %v", err)
		}
	}
	return orderID
}

func expiryWaitUntilOrderLock(db *gorm.DB) error {
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		err := db.Raw(`SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'
			AND query ILIKE '%orders%' AND query ILIKE '%FOR UPDATE%'
		)`).Scan(&waiting).Error
		if err != nil {
			return fmt.Errorf("inspect expiry row-lock wait: %w", err)
		}
		if waiting {
			return nil
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			return errors.New("expiry worker did not wait on the held order lock")
		}
	}
}

func expireWithLockedOrderUpdate(t *testing.T, db *gorm.DB, orderID uint, update map[string]interface{}, now time.Time) (int, error) {
	t.Helper()
	holder := db.Begin()
	if holder.Error != nil {
		t.Fatalf("begin lock holder: %v", holder.Error)
	}
	var id uint
	if err := holder.Raw("SELECT id FROM orders WHERE id = ? FOR UPDATE", orderID).Row().Scan(&id); err != nil {
		_ = holder.Rollback().Error
		t.Fatalf("hold order row lock: %v", err)
	}
	type result struct {
		count int
		err   error
	}
	done := make(chan result, 1)
	go func() {
		count, err := ExpireUnpaidOrders(db, now)
		done <- result{count: count, err: err}
	}()
	if err := expiryWaitUntilOrderLock(db); err != nil {
		_ = holder.Rollback().Error
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		t.Fatalf("wait for expiry row lock: %v", err)
	}
	if err := holder.Model(&Order{}).Where("id = ?", orderID).Updates(update).Error; err != nil {
		_ = holder.Rollback().Error
		t.Fatalf("change order while holding lock: %v", err)
	}
	if err := holder.Commit().Error; err != nil {
		t.Fatalf("commit locked order change: %v", err)
	}
	select {
	case got := <-done:
		return got.count, got.err
	case <-time.After(5 * time.Second):
		t.Fatal("expiry worker did not finish after releasing the row lock")
		return 0, nil
	}
}

func TestExpireUnpaidOrdersRechecksPaidAfterLockAndCountsTransitions(t *testing.T) {
	db := shopExtraSeedDB(t)
	if err := db.AutoMigrate(&OrderItem{}); err != nil {
		t.Fatalf("migrate order items: %v", err)
	}
	variantID := shopExtraSeedVariant(t, db, 10)
	now := time.Now().UTC()
	lateID := expirySeed(t, db, 1, variantID, now.Add(-time.Minute), nil)
	couponID := shopExtraSeedCoupon(t, db, "PAID-RACE", nil)
	paidRaceID := expirySeed(t, db, 2, variantID, now.Add(-time.Minute), &couponID)
	paidAt := now
	count, err := expireWithLockedOrderUpdate(t, db, paidRaceID, map[string]interface{}{"status": OrderStatusPaid, "paid_at": paidAt}, now)
	if err != nil {
		t.Fatalf("ExpireUnpaidOrders: %v", err)
	}
	if count != 1 {
		t.Errorf("expired count = %d, want 1 actual pending-to-expired transition", count)
	}
	if got := shopExtraOrderStatus(t, db, lateID); got != OrderStatusExpired {
		t.Errorf("unpaid expired order status = %q, want expired", got)
	}
	if got := shopExtraOrderStatus(t, db, paidRaceID); got != OrderStatusPaid {
		t.Errorf("paid race order status = %q, want paid", got)
	}
	var stock int
	if err := db.Raw("SELECT stock FROM product_variants WHERE id = ?", variantID).Scan(&stock).Error; err != nil {
		t.Fatal(err)
	}
	if stock != 8 {
		t.Errorf("stock after expiry = %d, want 8 (only the genuinely expired order released)", stock)
	}
	if n := shopExtraCount(t, db, "SELECT count(*) FROM stock_movements WHERE order_id = ? AND reason = 'release'", paidRaceID); n != 0 {
		t.Errorf("paid order release movements = %d, want 0", n)
	}
	var coupon Coupon
	if err := db.First(&coupon, couponID).Error; err != nil {
		t.Fatal(err)
	}
	if coupon.UsedCount != 1 {
		t.Errorf("paid order coupon used_count = %d, want 1", coupon.UsedCount)
	}
	var redemption CouponRedemption
	if err := db.Where("order_id = ?", paidRaceID).First(&redemption).Error; err != nil {
		t.Fatal(err)
	}
	if redemption.ReleasedAt != nil {
		t.Errorf("paid order redemption was released at %v", redemption.ReleasedAt)
	}
}

func TestExpireUnpaidOrdersRechecksExpiryAfterLock(t *testing.T) {
	db := shopExtraSeedDB(t)
	variantID := shopExtraSeedVariant(t, db, 5)
	now := time.Now().UTC()
	orderID := expirySeed(t, db, 3, variantID, now.Add(-time.Minute), nil)
	count, err := expireWithLockedOrderUpdate(t, db, orderID, map[string]interface{}{"expires_at": now.Add(time.Minute)}, now)
	if err != nil {
		t.Fatalf("ExpireUnpaidOrders: %v", err)
	}
	if count != 0 {
		t.Errorf("expired count = %d, want 0 for an order extended before lock acquisition", count)
	}
	if got := shopExtraOrderStatus(t, db, orderID); got != OrderStatusPending {
		t.Errorf("extended order status = %q, want pending", got)
	}
	var stock int
	if err := db.Raw("SELECT stock FROM product_variants WHERE id = ?", variantID).Scan(&stock).Error; err != nil {
		t.Fatal(err)
	}
	if stock != 3 {
		t.Errorf("stock after skipping extended order = %d, want reserved stock 3", stock)
	}
}

func TestExpireUnpaidOrdersEventWriteFailureRollsBackEverything(t *testing.T) {
	db := shopExtraSeedDB(t)
	if err := db.AutoMigrate(&OrderItem{}); err != nil {
		t.Fatalf("migrate order items: %v", err)
	}
	variantID := shopExtraSeedVariant(t, db, 5)
	couponID := shopExtraSeedCoupon(t, db, "EXPIRE-ROLLBACK", nil)
	now := time.Now().UTC()
	orderID := expirySeed(t, db, 4, variantID, now.Add(-time.Minute), &couponID)
	if err := db.Exec(`CREATE FUNCTION reject_expiry_event() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.to_status = 'expired' THEN RAISE EXCEPTION 'expiry event rejected'; END IF; RETURN NEW; END $$`).Error; err != nil {
		t.Fatalf("create event failure trigger function: %v", err)
	}
	if err := db.Exec(`CREATE TRIGGER reject_expiry_event BEFORE INSERT ON order_events FOR EACH ROW EXECUTE FUNCTION reject_expiry_event()`).Error; err != nil {
		t.Fatalf("create event failure trigger: %v", err)
	}
	count, err := ExpireUnpaidOrders(db, now)
	if err == nil {
		t.Fatal("ExpireUnpaidOrders returned nil after order_events insert failed")
	}
	if count != 0 {
		t.Errorf("expired count after event failure = %d, want 0", count)
	}
	if got := shopExtraOrderStatus(t, db, orderID); got != OrderStatusPending {
		t.Errorf("order status after rollback = %q, want pending", got)
	}
	var stock int
	if err := db.Raw("SELECT stock FROM product_variants WHERE id = ?", variantID).Scan(&stock).Error; err != nil {
		t.Fatal(err)
	}
	if stock != 3 {
		t.Errorf("stock after rollback = %d, want reserved stock 3", stock)
	}
	if n := shopExtraCount(t, db, "SELECT count(*) FROM stock_movements WHERE order_id = ? AND reason = 'release'", orderID); n != 0 {
		t.Errorf("release movements after rollback = %d, want 0", n)
	}
	var coupon Coupon
	if err := db.First(&coupon, couponID).Error; err != nil {
		t.Fatal(err)
	}
	if coupon.UsedCount != 1 {
		t.Errorf("coupon used_count after rollback = %d, want 1", coupon.UsedCount)
	}
	var redemption CouponRedemption
	if err := db.Where("order_id = ?", orderID).First(&redemption).Error; err != nil {
		t.Fatal(err)
	}
	if redemption.ReleasedAt != nil {
		t.Errorf("redemption released_at after rollback = %v, want nil", redemption.ReleasedAt)
	}
}
