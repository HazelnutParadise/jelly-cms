package data

import "testing"

func TestShopRoundingMigrationIsIdempotentAndConstrainsAdjustment(t *testing.T) {
	db := newShopMigrationDB(t)
	if !db.Migrator().HasColumn("orders", "rounding_adjustment") {
		t.Fatal("orders.rounding_adjustment is missing after migration")
	}
	var adjustment int64
	if err := db.Raw(`INSERT INTO orders (number, status, total_amount, payment_method, shipping_address)
		VALUES ('JL-2099-0001', 'pending', 0, 'none', '') RETURNING rounding_adjustment`).Scan(&adjustment).Error; err != nil {
		t.Fatalf("insert legacy-shaped order using migration default: %v", err)
	}
	if adjustment != 0 {
		t.Errorf("migration default rounding_adjustment=%d, want 0", adjustment)
	}
	if err := db.Exec(`INSERT INTO orders (number, status, total_amount, payment_method, shipping_address)
		VALUES ('JL-2099-0002', 'paid', 9450, 'credit_card', '')`).Error; err != nil {
		t.Fatalf("insert pre-rounding legacy snapshot: %v", err)
	}

	for _, invalid := range []int64{-50, 51} {
		err := db.Exec("UPDATE orders SET rounding_adjustment = ? WHERE number = ?", invalid, "JL-2099-0001").Error
		if err == nil {
			t.Errorf("rounding_adjustment=%d was accepted, want the -49..50 constraint to reject it", invalid)
		}
		// A PostgreSQL constraint error aborts the current transaction only when
		// the caller explicitly started one; GORM executes each statement separately.
	}
	for _, valid := range []int64{-49, 50} {
		if err := db.Exec("UPDATE orders SET rounding_adjustment = ? WHERE number = ?", valid, "JL-2099-0001").Error; err != nil {
			t.Errorf("valid rounding_adjustment=%d rejected: %v", valid, err)
		}
	}

	if err := MigrateDB(db); err != nil {
		t.Fatalf("rerun MigrateDB with rounding migration applied: %v", err)
	}
	var after int64
	if err := db.Raw("SELECT rounding_adjustment FROM orders WHERE number = ?", "JL-2099-0001").Scan(&after).Error; err != nil {
		t.Fatalf("read order after migration rerun: %v", err)
	}
	if after != 50 {
		t.Errorf("rerunning migration changed existing rounding_adjustment to %d, want 50", after)
	}
	var legacy struct {
		TotalAmount        int64
		RoundingAdjustment int64
	}
	if err := db.Raw("SELECT total_amount, rounding_adjustment FROM orders WHERE number = ?", "JL-2099-0002").Scan(&legacy).Error; err != nil {
		t.Fatalf("read legacy order after migration rerun: %v", err)
	}
	if legacy.TotalAmount != 9450 || legacy.RoundingAdjustment != 0 {
		t.Errorf("legacy snapshot after migration total=%d adjustment=%d, want unchanged 9450 and default 0", legacy.TotalAmount, legacy.RoundingAdjustment)
	}
}
