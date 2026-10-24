package payment

// Direct contract coverage for ticket 07 E6 Finalize: amount/currency verification must happen
// before anything changes, repeated notifications are applied once, and a failed attempt leaves the
// order retryable. The existing HTTP notification tests cover the routes; these tests pin the
// transaction boundary of Finalize itself. They are skipped unless JELLY_TEST_POSTGRES=1.
//
// Do not edit production code or the existing tests to make these pass.

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	paymentShopTestHost    = "127.0.0.1"
	paymentShopTestPort    = "55432"
	paymentShopTestUser    = "jelly_test"
	paymentShopTestPass    = "jelly-local-acceptance"
	paymentShopTestAdminDB = "jelly_acceptance"
	paymentShopTestPrefix  = "jelly_payment_shop_"
)

var paymentShopTestDBCounter atomic.Int64

func paymentShopTestDSN(name string) string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable TimeZone=UTC",
		paymentShopTestHost, paymentShopTestPort, paymentShopTestUser, paymentShopTestPass, name)
}

func newPaymentShopTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	if os.Getenv("JELLY_TEST_POSTGRES") != "1" {
		t.Skip("set JELLY_TEST_POSTGRES=1 to run payment database tests against the dedicated PostgreSQL container")
	}
	admin, err := gorm.Open(postgres.Open(paymentShopTestDSN(paymentShopTestAdminDB)), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open dedicated PostgreSQL admin database: %v", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatalf("get admin pool: %v", err)
	}
	t.Cleanup(func() { _ = adminSQL.Close() })

	dbName := fmt.Sprintf("%s%d_%d", paymentShopTestPrefix, os.Getpid(), paymentShopTestDBCounter.Add(1))
	if err := admin.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, dbName)).Error; err != nil {
		t.Fatalf("create isolated payment database %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		if !strings.HasPrefix(dbName, paymentShopTestPrefix) {
			t.Errorf("refusing to drop unexpected database %q", dbName)
			return
		}
		if err := admin.Exec(fmt.Sprintf(`DROP DATABASE IF EXISTS "%s"`, dbName)).Error; err != nil {
			t.Errorf("drop isolated payment database %s: %v", dbName, err)
		}
	})

	db, err := gorm.Open(postgres.Open(paymentShopTestDSN(dbName)), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open isolated payment database %s: %v", dbName, err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get isolated pool: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := data.MigrateDB(db); err != nil {
		t.Fatalf("migrate payment database: %v", err)
	}
	return db
}

type paymentShopFixture struct {
	orderID   uint
	paymentID uint
	trade     string
	number    string
}

func paymentShopSeed(t *testing.T, db *gorm.DB, index int, total int64, currency string) paymentShopFixture {
	t.Helper()
	number := fmt.Sprintf("JL-2026-%04d", 2000+index)
	trade := fmt.Sprintf("SBX%017d", index)
	var orderID uint
	if err := db.Raw(`INSERT INTO orders (number, status, total_amount, currency, email, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'pay-extra@example.test', now(), now()) RETURNING id`,
		number, core.OrderStatusPending, total, currency).Row().Scan(&orderID); err != nil {
		t.Fatalf("seed order %s: %v", number, err)
	}
	row := core.Payment{OrderID: orderID, Provider: "sandbox", Method: "credit_card",
		MerchantTradeNo: trade, Amount: total, Currency: currency, Status: "pending"}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed payment %s: %v", trade, err)
	}
	return paymentShopFixture{orderID: orderID, paymentID: row.ID, trade: trade, number: number}
}

func paymentShopAddProductItem(t *testing.T, db *gorm.DB, fx paymentShopFixture) core.Product {
	t.Helper()
	product := core.Product{Title: "Payment fixture item", Slug: fmt.Sprintf("payment-fixture-%d", fx.orderID), Price: 12000, Status: "active"}
	if err := db.Create(&product).Error; err != nil {
		t.Fatalf("seed product for order %d: %v", fx.orderID, err)
	}
	productID, quantity, unitPrice := product.ID, 1, int64(12000)
	item := core.OrderItem{OrderID: fx.orderID, ProductID: &productID, Quantity: quantity, Price: unitPrice, UnitPrice: unitPrice, LineTotal: unitPrice}
	if err := db.Create(&item).Error; err != nil {
		t.Fatalf("seed order item for order %d: %v", fx.orderID, err)
	}
	return product
}

func paymentShopStatus(t *testing.T, db *gorm.DB, orderID uint) string {
	t.Helper()
	var status string
	if err := db.Raw("SELECT status FROM orders WHERE id = ?", orderID).Row().Scan(&status); err != nil {
		t.Fatalf("read order status: %v", err)
	}
	return status
}

func paymentShopCount(t *testing.T, db *gorm.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.Raw(query, args...).Scan(&n).Error; err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return n
}

func paymentShopCallback(fx paymentShopFixture, status PaymentStatus, amount int64, currency, dedupe string) VerifiedCallback {
	return VerifiedCallback{
		Provider:        "sandbox",
		MerchantTradeNo: fx.trade,
		ProviderTradeNo: "PROVIDER-" + dedupe,
		Method:          "credit_card",
		Status:          status,
		Amount:          amount,
		Currency:        currency,
		DedupeKey:       dedupe,
		Payload:         map[string]interface{}{"nonce": dedupe},
	}
}

// TestFinalizeAmountOrCurrencyMismatchChangesNothing pins the order of checks: an unverified amount
// or currency must not touch the order, the payment row or the event log.
func TestFinalizeAmountOrCurrencyMismatchChangesNothing(t *testing.T) {
	db := newPaymentShopTestDB(t)
	fx := paymentShopSeed(t, db, 1, 12000, "TWD")

	cases := []struct {
		name  string
		cb    VerifiedCallback
		label string
	}{
		{"amount", paymentShopCallback(fx, PaymentStatusPaid, 12001, "TWD", "mismatch-amount"), "amount"},
		{"currency", paymentShopCallback(fx, PaymentStatusPaid, 12000, "USD", "mismatch-currency"), "currency"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := db.Transaction(func(tx *gorm.DB) error { return Finalize(tx, "sandbox", tc.cb) }); err == nil {
				t.Errorf("Finalize accepted a callback with a wrong %s", tc.label)
			}
			if got := paymentShopStatus(t, db, fx.orderID); got != core.OrderStatusPending {
				t.Errorf("order status after a %s mismatch = %q, want pending", tc.label, got)
			}
			var payment core.Payment
			db.First(&payment, fx.paymentID)
			if payment.Status != "pending" || payment.PaidAt != nil {
				t.Errorf("payment after a %s mismatch = status %q paid_at %v, want pending/nil", tc.label, payment.Status, payment.PaidAt)
			}
			if n := paymentShopCount(t, db, "SELECT count(*) FROM payment_events WHERE provider = ?", "sandbox"); n != 0 {
				t.Errorf("payment_events after a %s mismatch = %d, want 0", tc.label, n)
			}
			if n := paymentShopCount(t, db, "SELECT count(*) FROM order_events WHERE order_id = ?", fx.orderID); n != 0 {
				t.Errorf("order_events after a %s mismatch = %d, want 0", tc.label, n)
			}
		})
	}

	// Control: the same fixture accepts the correct notification, so the rejections above are not
	// caused by a Finalize that refuses everything.
	ok := paymentShopCallback(fx, PaymentStatusPaid, 12000, "TWD", "verification-control")
	if err := db.Transaction(func(tx *gorm.DB) error { return Finalize(tx, "sandbox", ok) }); err != nil {
		t.Fatalf("Finalize with the correct amount and currency: %v", err)
	}
	if got := paymentShopStatus(t, db, fx.orderID); got != core.OrderStatusPaid {
		t.Errorf("order status after the control callback = %q, want paid", got)
	}
}

// TestFinalizeDedupesReplayAndAllowsRetryAfterFailure covers the two notification lifecycles that
// matter to the customer: a replay must be applied once, and a failed attempt must leave room for a
// later successful one under a new dedupe key.
func TestFinalizeDedupesReplayAndAllowsRetryAfterFailure(t *testing.T) {
	db := newPaymentShopTestDB(t)
	fx := paymentShopSeed(t, db, 2, 12000, "TWD")

	failed := paymentShopCallback(fx, PaymentStatusFailed, 12000, "TWD", "attempt-1-failed")
	if err := db.Transaction(func(tx *gorm.DB) error { return Finalize(tx, "sandbox", failed) }); err != nil {
		t.Fatalf("Finalize(failed): %v", err)
	}
	if got := paymentShopStatus(t, db, fx.orderID); got != core.OrderStatusPending {
		t.Errorf("order status after a failed payment = %q, want pending (retryable)", got)
	}
	var payment core.Payment
	db.First(&payment, fx.paymentID)
	if payment.Status != "failed" {
		t.Errorf("payment status after a failed notification = %q, want failed", payment.Status)
	}
	if n := paymentShopCount(t, db, "SELECT count(*) FROM payment_events WHERE provider = ?", "sandbox"); n != 1 {
		t.Errorf("payment_events after the failed notification = %d, want 1", n)
	}

	paid := paymentShopCallback(fx, PaymentStatusPaid, 12000, "TWD", "attempt-2-paid")
	if err := db.Transaction(func(tx *gorm.DB) error { return Finalize(tx, "sandbox", paid) }); err != nil {
		t.Fatalf("Finalize(paid retry): %v", err)
	}
	if got := paymentShopStatus(t, db, fx.orderID); got != core.OrderStatusPaid {
		t.Errorf("order status after the successful retry = %q, want paid", got)
	}
	db.First(&payment, fx.paymentID)
	if payment.Status != "paid" || payment.PaidAt == nil {
		t.Errorf("payment after the retry = status %q paid_at %v, want paid with a timestamp", payment.Status, payment.PaidAt)
	}

	// The provider repeats the success notification: acknowledged, but applied once.
	if err := db.Transaction(func(tx *gorm.DB) error { return Finalize(tx, "sandbox", paid) }); err != nil {
		t.Fatalf("Finalize(replayed paid): %v", err)
	}
	if n := paymentShopCount(t, db, "SELECT count(*) FROM payment_events WHERE provider = ?", "sandbox"); n != 2 {
		t.Errorf("payment_events after the replay = %d, want 2 (failed + paid, no duplicate)", n)
	}
	if n := paymentShopCount(t, db, "SELECT count(*) FROM order_events WHERE order_id = ? AND to_status = ?", fx.orderID, core.OrderStatusPaid); n != 1 {
		t.Errorf("order_events to paid after the replay = %d, want 1", n)
	}
}

// TestFinalizeUnknownTradeNoChangesNothing checks that a correctly signed notification for a trade
// number that is not ours cannot create events or move any order.
func TestFinalizeUnknownTradeNoChangesNothing(t *testing.T) {
	db := newPaymentShopTestDB(t)
	fx := paymentShopSeed(t, db, 3, 12000, "TWD")

	cb := paymentShopCallback(fx, PaymentStatusPaid, 12000, "TWD", "unknown-trade")
	cb.MerchantTradeNo = "NOSUCHTRADE000000001"
	if err := db.Transaction(func(tx *gorm.DB) error { return Finalize(tx, "sandbox", cb) }); err == nil {
		t.Error("Finalize accepted an unknown merchant_trade_no")
	}
	if got := paymentShopStatus(t, db, fx.orderID); got != core.OrderStatusPending {
		t.Errorf("order status after an unknown trade = %q, want pending", got)
	}
	if n := paymentShopCount(t, db, "SELECT count(*) FROM payment_events"); n != 0 {
		t.Errorf("payment_events after an unknown trade = %d, want 0", n)
	}
}

func TestFinalizeLatePaidAndFailedCallbacksDoNotRegressRefundedOrder(t *testing.T) {
	db := newPaymentShopTestDB(t)
	fx := paymentShopSeed(t, db, 5, 12000, "TWD")
	product := paymentShopAddProductItem(t, db, fx)
	paidAt := time.Date(2026, time.October, 24, 4, 5, 6, 0, time.UTC)
	if err := db.Model(&core.Order{}).Where("id = ?", fx.orderID).Updates(map[string]any{
		"status": core.OrderStatusRefunded, "paid_at": paidAt, "refunded_total": 12000,
	}).Error; err != nil {
		t.Fatalf("seed refunded order state: %v", err)
	}
	if err := db.Model(&core.Payment{}).Where("id = ?", fx.paymentID).Updates(map[string]any{
		"status": "refunded", "paid_at": paidAt,
	}).Error; err != nil {
		t.Fatalf("seed refunded payment state: %v", err)
	}
	if err := db.Model(&core.Product{}).Where("id = ?", product.ID).Update("sold_count", 1).Error; err != nil {
		t.Fatalf("seed sold count for refunded order: %v", err)
	}

	for i, status := range []PaymentStatus{PaymentStatusPaid, PaymentStatusFailed} {
		cb := paymentShopCallback(fx, status, 12000, "TWD", fmt.Sprintf("late-refund-%d", i))
		if err := db.Transaction(func(tx *gorm.DB) error { return Finalize(tx, "sandbox", cb) }); err != nil {
			t.Fatalf("Finalize(%s callback after refund): %v", status, err)
		}
	}
	if got := paymentShopStatus(t, db, fx.orderID); got != core.OrderStatusRefunded {
		t.Errorf("order status after late paid/failed callbacks = %q, want refunded", got)
	}
	var storedOrder core.Order
	if err := db.First(&storedOrder, fx.orderID).Error; err != nil {
		t.Fatalf("reload refunded order: %v", err)
	}
	var storedPayment core.Payment
	if err := db.First(&storedPayment, fx.paymentID).Error; err != nil {
		t.Fatalf("reload refunded payment: %v", err)
	}
	if storedPayment.Status != "refunded" || storedPayment.PaidAt == nil || !storedPayment.PaidAt.Equal(paidAt) {
		t.Errorf("payment after late callbacks = status %q paid_at %v, want refunded/%s", storedPayment.Status, storedPayment.PaidAt, paidAt)
	}
	if storedOrder.PaidAt == nil || !storedOrder.PaidAt.Equal(paidAt) {
		t.Errorf("order paid_at after late callbacks = %v, want %s", storedOrder.PaidAt, paidAt)
	}
	var storedProduct core.Product
	if err := db.First(&storedProduct, product.ID).Error; err != nil {
		t.Fatalf("reload product after late callbacks: %v", err)
	}
	if storedProduct.SoldCount != 1 {
		t.Errorf("product sold_count after late callbacks = %d, want 1", storedProduct.SoldCount)
	}
}

func TestFinalizePaidReplayWithNewNoncePreservesPaidAtAndSoldCount(t *testing.T) {
	db := newPaymentShopTestDB(t)
	fx := paymentShopSeed(t, db, 6, 12000, "TWD")
	product := paymentShopAddProductItem(t, db, fx)
	firstPaidAt := time.Date(2026, time.October, 24, 4, 5, 6, 0, time.UTC)
	first := paymentShopCallback(fx, PaymentStatusPaid, 12000, "TWD", "paid-first-nonce")
	first.PaidAt = &firstPaidAt
	if err := db.Transaction(func(tx *gorm.DB) error { return Finalize(tx, "sandbox", first) }); err != nil {
		t.Fatalf("Finalize(first paid callback): %v", err)
	}
	var paidPayment core.Payment
	if err := db.First(&paidPayment, fx.paymentID).Error; err != nil {
		t.Fatalf("reload payment after first paid callback: %v", err)
	}
	if paidPayment.PaidAt == nil || !paidPayment.PaidAt.Equal(firstPaidAt) {
		t.Fatalf("payment paid_at after first callback = %v, want %s", paidPayment.PaidAt, firstPaidAt)
	}
	var paidOrder core.Order
	if err := db.First(&paidOrder, fx.orderID).Error; err != nil {
		t.Fatalf("reload order after first paid callback: %v", err)
	}
	if paidOrder.PaidAt == nil {
		t.Fatal("order paid_at after first callback is nil")
	}
	orderPaidAt := *paidOrder.PaidAt

	replay := paymentShopCallback(fx, PaymentStatusPaid, 12000, "TWD", "paid-second-nonce")
	laterPaidAt := firstPaidAt.Add(time.Hour)
	replay.PaidAt = &laterPaidAt
	if err := db.Transaction(func(tx *gorm.DB) error { return Finalize(tx, "sandbox", replay) }); err != nil {
		t.Fatalf("Finalize(paid replay with new nonce): %v", err)
	}
	if err := db.First(&paidPayment, fx.paymentID).Error; err != nil {
		t.Fatalf("reload payment after paid replay: %v", err)
	}
	if paidPayment.Status != "paid" || paidPayment.PaidAt == nil || !paidPayment.PaidAt.Equal(firstPaidAt) {
		t.Errorf("payment after new-nonce replay = status %q paid_at %v, want paid/%s", paidPayment.Status, paidPayment.PaidAt, firstPaidAt)
	}
	if err := db.First(&paidOrder, fx.orderID).Error; err != nil {
		t.Fatalf("reload order after paid replay: %v", err)
	}
	if paidOrder.Status != core.OrderStatusPaid || paidOrder.PaidAt == nil || !paidOrder.PaidAt.Equal(orderPaidAt) {
		t.Errorf("order after new-nonce replay = status %q paid_at %v, want paid/%s", paidOrder.Status, paidOrder.PaidAt, orderPaidAt)
	}
	var storedProduct core.Product
	if err := db.First(&storedProduct, product.ID).Error; err != nil {
		t.Fatalf("reload product after paid replay: %v", err)
	}
	if storedProduct.SoldCount != 1 {
		t.Errorf("product sold_count after paid replay = %d, want 1", storedProduct.SoldCount)
	}
	if n := paymentShopCount(t, db, "SELECT count(*) FROM payment_events WHERE provider = ?", "sandbox"); n != 2 {
		t.Errorf("payment events after distinct-nonce replay = %d, want 2", n)
	}
	if n := paymentShopCount(t, db, "SELECT count(*) FROM order_events WHERE order_id = ? AND to_status = ?", fx.orderID, core.OrderStatusPaid); n != 1 {
		t.Errorf("paid order transitions after distinct-nonce replay = %d, want 1", n)
	}
}

// TestFinalizeConcurrentDuplicateAppliesOnce is the race the provider retry loop can create when it
// sends the same notification twice at the same time.
func TestFinalizeConcurrentDuplicateAppliesOnce(t *testing.T) {
	db := newPaymentShopTestDB(t)
	fx := paymentShopSeed(t, db, 4, 12000, "TWD")
	cb := paymentShopCallback(fx, PaymentStatusPaid, 12000, "TWD", "concurrent-duplicate")

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = db.Transaction(func(tx *gorm.DB) error { return Finalize(tx, "sandbox", cb) })
		}(i)
	}
	close(start)
	wg.Wait()

	success := 0
	for _, err := range errs {
		if err == nil {
			success++
		}
	}
	if success == 0 {
		t.Fatalf("both concurrent duplicates failed; errors=%v", errs)
	}
	if got := paymentShopStatus(t, db, fx.orderID); got != core.OrderStatusPaid {
		t.Errorf("order status after concurrent duplicates = %q, want paid", got)
	}
	if n := paymentShopCount(t, db, "SELECT count(*) FROM payment_events WHERE provider = ?", "sandbox"); n != 1 {
		t.Errorf("payment_events after concurrent duplicates = %d, want 1", n)
	}
	if n := paymentShopCount(t, db, "SELECT count(*) FROM order_events WHERE order_id = ? AND to_status = ?", fx.orderID, core.OrderStatusPaid); n != 1 {
		t.Errorf("order_events to paid after concurrent duplicates = %d, want 1", n)
	}
}
