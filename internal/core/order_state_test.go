package core

// Order state machine contract (ticket 07, E1): pending -> paid|cancelled|expired; paid -> shipped|cancelled;
// shipped -> completed|refunded; completed -> refunded; refunded, cancelled and expired are terminal.
// Every transition writes order_events. These tests are written before OrderService.Transition exists
// and must fail until it does. Do not edit them to match an implementation.
//
// The package cannot import internal/data (data imports core), so the database test migrates the core
// models directly. It runs only when JELLY_TEST_POSTGRES=1.

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var orderStateWantTransitions = map[string][]string{
	"pending":   {"cancelled", "expired", "paid"},
	"paid":      {"cancelled", "shipped"},
	"shipped":   {"completed", "refunded"},
	"completed": {"refunded"},
	"refunded":  {},
	"cancelled": {},
	"expired":   {},
}

func TestAllowedTransitionsTable(t *testing.T) {
	for status, want := range orderStateWantTransitions {
		got := append([]string(nil), AllowedTransitions(status)...)
		sort.Strings(got)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("AllowedTransitions(%q) = %v, want %v", status, got, want)
		}
	}
	if got := AllowedTransitions("no-such-status"); len(got) != 0 {
		t.Errorf("AllowedTransitions(unknown) = %v, want empty", got)
	}
}

const (
	orderStateTestHost    = "127.0.0.1"
	orderStateTestPort    = "55432"
	orderStateTestUser    = "jelly_test"
	orderStateTestPass    = "jelly-local-acceptance"
	orderStateTestAdminDB = "jelly_acceptance"
	orderStateTestPrefix  = "jelly_core_order_state_"
)

var orderStateTestDBCounter atomic.Int64

func orderStateTestDSN(name string) string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable TimeZone=UTC",
		orderStateTestHost, orderStateTestPort, orderStateTestUser, orderStateTestPass, name)
}

func newOrderStateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	if os.Getenv("JELLY_TEST_POSTGRES") != "1" {
		t.Skip("set JELLY_TEST_POSTGRES=1 to run order state database tests against the dedicated PostgreSQL container")
	}
	admin, err := gorm.Open(postgres.Open(orderStateTestDSN(orderStateTestAdminDB)), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open dedicated PostgreSQL admin database: %v", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatalf("get admin pool: %v", err)
	}
	t.Cleanup(func() { _ = adminSQL.Close() })

	dbName := fmt.Sprintf("%s%d_%d", orderStateTestPrefix, os.Getpid(), orderStateTestDBCounter.Add(1))
	if err := admin.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, dbName)).Error; err != nil {
		t.Fatalf("create isolated order state database %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		if !strings.HasPrefix(dbName, orderStateTestPrefix) {
			t.Errorf("refusing to drop unexpected database %q", dbName)
			return
		}
		if err := admin.Exec(fmt.Sprintf(`DROP DATABASE IF EXISTS "%s"`, dbName)).Error; err != nil {
			t.Errorf("drop isolated order state database %s: %v", dbName, err)
		}
	})

	db, err := gorm.Open(postgres.Open(orderStateTestDSN(dbName)), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open isolated order state database %s: %v", dbName, err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get isolated pool: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(&User{}, &Order{}, &OrderEvent{}); err != nil {
		t.Fatalf("migrate order state models: %v", err)
	}
	return db
}

// TestTransitionCompareAndSet runs two Transition(pending -> paid) calls on the same order at once. The
// compare-and-set must let exactly one win, leave the order paid, and write exactly one order_events row.
func TestTransitionCompareAndSet(t *testing.T) {
	db := newOrderStateTestDB(t)

	var orderID uint
	if err := db.Raw(`INSERT INTO orders (number, status, currency, email, created_at, updated_at)
		VALUES ('JL-2026-0001', 'pending', 'TWD', 'race@example.test', now(), now()) RETURNING id`).Row().Scan(&orderID); err != nil {
		t.Fatalf("insert pending order: %v", err)
	}

	svc := &OrderService{}
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = db.Transaction(func(tx *gorm.DB) error {
				return svc.Transition(tx, orderID, "pending", "paid", nil, "concurrent payment")
			})
		}(i)
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
		t.Fatalf("concurrent Transition(pending -> paid) succeeded %d times, want exactly 1; errors=%v", succeeded, errs)
	}

	var status string
	if err := db.Raw("SELECT status FROM orders WHERE id = ?", orderID).Row().Scan(&status); err != nil {
		t.Fatalf("read order status: %v", err)
	}
	if status != "paid" {
		t.Errorf("order status = %q, want paid", status)
	}

	var events int64
	if err := db.Raw("SELECT count(*) FROM order_events WHERE order_id = ?", orderID).Row().Scan(&events); err != nil {
		t.Fatalf("count order_events: %v", err)
	}
	if events != 1 {
		t.Errorf("order_events rows = %d, want exactly 1", events)
	}
}
