package data

// Shop migration contract (ticket 07, data section). The shop schema is added by the versioned SQL files
// under internal/data/migrations (0008 onward) through the same runner as MigrateDB. These tests run
// against the dedicated PostgreSQL database and need JELLY_TEST_POSTGRES=1. They are written before the
// shop migration exists and must fail until it does. Do not edit them to match an implementation.

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"gorm.io/gorm"
)

const (
	shopMigrationTestHost    = "127.0.0.1"
	shopMigrationTestPort    = "55432"
	shopMigrationTestUser    = "jelly_test"
	shopMigrationTestPass    = "jelly-local-acceptance"
	shopMigrationTestAdminDB = "jelly_acceptance"
	shopMigrationTestPrefix  = "jelly_shop_migration_"
)

var shopMigrationTestDBCounter atomic.Int64

// shopMigrationTables are the tables the contract adds for the shop (ticket 07, 資料).
var shopMigrationTables = []string{
	"product_categories", "product_category_links", "product_variants", "stock_movements",
	"carts", "cart_items", "order_events", "order_counters", "addresses", "invoice_profiles",
	"invoices", "coupons", "coupon_targets", "coupon_batches", "coupon_redemptions",
	"payments", "payment_events", "order_access_tokens", "stock_alerts",
}

type shopMigrationVariantRow struct {
	ID        uint
	ProductID uint
	Name      string
	Price     int64
	Stock     int64
}

func newShopMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	if os.Getenv("JELLY_TEST_POSTGRES") != "1" {
		t.Skip("set JELLY_TEST_POSTGRES=1 to run shop migration tests against the dedicated PostgreSQL container")
	}

	admin, err := Open(config.DatabaseConfig{
		Host: shopMigrationTestHost, Port: shopMigrationTestPort, User: shopMigrationTestUser,
		Password: shopMigrationTestPass, Name: shopMigrationTestAdminDB, Timezone: "UTC",
	})
	if err != nil {
		t.Fatalf("open dedicated PostgreSQL admin database: %v", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatalf("get admin pool: %v", err)
	}
	t.Cleanup(func() { _ = adminSQL.Close() })

	dbName := fmt.Sprintf("%s%d_%d", shopMigrationTestPrefix, os.Getpid(), shopMigrationTestDBCounter.Add(1))
	if err := admin.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, dbName)).Error; err != nil {
		t.Fatalf("create isolated shop migration database %s: %v", dbName, err)
	}
	// Cleanups run last-in first-out: the pool closes before the drop runs.
	t.Cleanup(func() {
		if !strings.HasPrefix(dbName, shopMigrationTestPrefix) {
			t.Errorf("refusing to drop unexpected database %q", dbName)
			return
		}
		if err := admin.Exec(fmt.Sprintf(`DROP DATABASE IF EXISTS "%s"`, dbName)).Error; err != nil {
			t.Errorf("drop isolated shop migration database %s: %v", dbName, err)
		}
	})

	db, err := Open(config.DatabaseConfig{
		Host: shopMigrationTestHost, Port: shopMigrationTestPort, User: shopMigrationTestUser,
		Password: shopMigrationTestPass, Name: dbName, Timezone: "UTC",
	})
	if err != nil {
		t.Fatalf("open isolated shop migration database %s: %v", dbName, err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get isolated pool: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := MigrateDB(db); err != nil {
		t.Fatalf("first MigrateDB on isolated database: %v", err)
	}
	return db
}

func shopMigrationVariants(t *testing.T, db *gorm.DB) []shopMigrationVariantRow {
	t.Helper()
	var rows []shopMigrationVariantRow
	if err := db.Raw("SELECT id, product_id, name, price, stock FROM product_variants ORDER BY id").Scan(&rows).Error; err != nil {
		t.Fatalf("read product_variants: %v", err)
	}
	return rows
}

// TestShopMigrationIdempotentRerun checks that the shop tables exist and that repeated runs leave the
// data unchanged once the first run has backfilled the product.
func TestShopMigrationIdempotentRerun(t *testing.T) {
	db := newShopMigrationDB(t)
	product := core.Product{Title: "Rerun Kettle", Slug: "rerun-kettle", Price: 12000, Stock: 7, Status: "active"}
	if err := db.Create(&product).Error; err != nil {
		t.Fatalf("create product: %v", err)
	}

	if err := MigrateDB(db); err != nil {
		t.Fatalf("second MigrateDB: %v", err)
	}
	afterFirstRerun := shopMigrationVariants(t, db)
	if err := MigrateDB(db); err != nil {
		t.Fatalf("third MigrateDB: %v", err)
	}
	afterSecondRerun := shopMigrationVariants(t, db)

	if !reflect.DeepEqual(afterFirstRerun, afterSecondRerun) {
		t.Errorf("product_variants changed on a repeated run:\nfirst=%+v\nsecond=%+v", afterFirstRerun, afterSecondRerun)
	}
	var count int64
	if err := db.Raw("SELECT count(*) FROM product_variants WHERE product_id = ?", product.ID).Scan(&count).Error; err != nil {
		t.Fatalf("count variants: %v", err)
	}
	if count != 1 {
		t.Errorf("variants for product after reruns = %d, want exactly 1", count)
	}
	for _, table := range shopMigrationTables {
		if !db.Migrator().HasTable(table) {
			t.Errorf("MigrateDB returned nil but shop table %q does not exist", table)
		}
	}
}

// TestShopMigrationBackfillsDefaultVariant inserts a product that has no variant yet, as an existing
// shop product would before the shop migration. The next run must backfill exactly one variant with an
// empty name that mirrors the product's price and stock.
func TestShopMigrationBackfillsDefaultVariant(t *testing.T) {
	db := newShopMigrationDB(t)
	product := core.Product{Title: "Backfill Kettle", Slug: "backfill-kettle", Price: 12000, Stock: 7, Status: "active"}
	if err := db.Create(&product).Error; err != nil {
		t.Fatalf("create product before backfill: %v", err)
	}

	if err := MigrateDB(db); err != nil {
		t.Fatalf("MigrateDB backfill run: %v", err)
	}

	var variants []shopMigrationVariantRow
	for _, row := range shopMigrationVariants(t, db) {
		if row.ProductID == product.ID {
			variants = append(variants, row)
		}
	}
	if len(variants) != 1 {
		t.Fatalf("variants for backfilled product = %d, want exactly 1: %+v", len(variants), variants)
	}
	if variants[0].Name != "" {
		t.Errorf("default variant name = %q, want empty", variants[0].Name)
	}
	if variants[0].Price != 12000 {
		t.Errorf("default variant price = %d, want 12000 (mirrors product)", variants[0].Price)
	}
	if variants[0].Stock != 7 {
		t.Errorf("default variant stock = %d, want 7 (mirrors product)", variants[0].Stock)
	}
}

func TestShopMigrationConstraints(t *testing.T) {
	db := newShopMigrationDB(t)

	product := core.Product{Title: "Constraint Kettle", Slug: "constraint-kettle", Price: 12000, Stock: 7, Status: "active"}
	if err := db.Create(&product).Error; err != nil {
		t.Fatalf("create product: %v", err)
	}

	t.Run("variant stock check rejects negative", func(t *testing.T) {
		if err := db.Exec(`INSERT INTO product_variants (product_id, name, price, stock, position, status)
			VALUES (?, 'Zero', 100, 0, 0, 'active')`, product.ID).Error; err != nil {
			t.Fatalf("control insert with stock 0 failed: %v", err)
		}
		if err := db.Exec(`INSERT INTO product_variants (product_id, name, price, stock, position, status)
			VALUES (?, 'Negative', 100, -1, 1, 'active')`, product.ID).Error; err == nil {
			t.Error("insert with stock -1 succeeded; the database must reject negative stock")
		}
	})

	t.Run("orders.user_id is nullable", func(t *testing.T) {
		var nullable string
		if err := db.Raw(`SELECT is_nullable FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'orders' AND column_name = 'user_id'`).Row().Scan(&nullable); err != nil {
			t.Fatalf("inspect orders.user_id: %v", err)
		}
		if nullable != "YES" {
			t.Errorf("orders.user_id is_nullable = %q, want YES", nullable)
		}
		if err := db.Exec(`INSERT INTO orders (number, status, currency, email, created_at, updated_at)
			VALUES ('JL-2026-0002', 'pending', 'TWD', 'guest@example.test', now(), now())`).Error; err != nil {
			t.Errorf("guest order with NULL user_id failed: %v", err)
		}
	})

	t.Run("order_items.product_id is set null on hard delete", func(t *testing.T) {
		// A product without variants, so that only the order_items foreign key is exercised.
		doomed := core.Product{Title: "Hard Delete Kettle", Slug: "hard-delete-kettle", Price: 12000, Stock: 1, Status: "active"}
		if err := db.Create(&doomed).Error; err != nil {
			t.Fatalf("create product to delete: %v", err)
		}
		var orderID uint
		if err := db.Raw(`INSERT INTO orders (number, status, currency, email, created_at, updated_at)
			VALUES ('JL-2026-0003', 'pending', 'TWD', 'item@example.test', now(), now()) RETURNING id`).Row().Scan(&orderID); err != nil {
			t.Fatalf("insert order: %v", err)
		}
		var itemID uint
		if err := db.Raw(`INSERT INTO order_items (order_id, product_id, title, quantity, price, unit_price, line_total)
			VALUES (?, ?, 'Constraint Kettle', 1, 12000, 12000, 12000) RETURNING id`, orderID, doomed.ID).Row().Scan(&itemID); err != nil {
			t.Fatalf("insert order item: %v", err)
		}
		if err := db.Exec("DELETE FROM products WHERE id = ?", doomed.ID).Error; err != nil {
			t.Fatalf("hard delete product: %v", err)
		}
		var isNull bool
		if err := db.Raw("SELECT product_id IS NULL FROM order_items WHERE id = ?", itemID).Row().Scan(&isNull); err != nil {
			t.Fatalf("read order item after product delete: %v", err)
		}
		if !isNull {
			t.Error("order_items.product_id was not set to NULL after the product was hard deleted")
		}
	})

	t.Run("coupons.code is unique case-insensitively", func(t *testing.T) {
		if err := db.Exec(`INSERT INTO coupons (code, label, kind, value, scope, status)
			VALUES ('spring10', 'Spring', 'percent', 10, 'all', 'active')`).Error; err != nil {
			t.Fatalf("first coupon insert failed: %v", err)
		}
		if err := db.Exec(`INSERT INTO coupons (code, label, kind, value, scope, status)
			VALUES ('spring10', 'Spring again', 'percent', 10, 'all', 'active')`).Error; err == nil {
			t.Error("duplicate coupon code was accepted")
		}
		if err := db.Exec(`INSERT INTO coupons (code, label, kind, value, scope, status)
			VALUES ('SPRING10', 'Spring upper', 'percent', 10, 'all', 'active')`).Error; err == nil {
			t.Error("coupon code differing only by case was accepted; code must be unique lower")
		}
	})
}
