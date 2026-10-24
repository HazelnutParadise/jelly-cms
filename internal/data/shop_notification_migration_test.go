package data

import (
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

func TestShopNotificationDeliveryMigrationAddsGuestQueueWithoutChangingLegacyRows(t *testing.T) {
	db := newAccountsMigrationDB(t)
	if err := db.AutoMigrate(&core.User{}); err != nil {
		t.Fatalf("create legacy users table: %v", err)
	}
	legacy, err := embeddedMigrationFiles.ReadFile("migrations/0005_notifications.sql")
	if err != nil {
		t.Fatalf("read 0005 legacy notification schema: %v", err)
	}
	if strings.Contains(string(legacy), "to_address") {
		t.Fatal("legacy 0005 migration contains guest-delivery fields; shop support must be additive")
	}
	if err := db.Exec(string(legacy)).Error; err != nil {
		t.Fatalf("apply legacy 0005 notification schema: %v", err)
	}
	user := core.User{Username: "legacy-delivery-owner", Email: "legacy-delivery@example.test", Provider: "local"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create legacy notification user: %v", err)
	}
	var notificationID int64
	if err := db.Raw(`INSERT INTO notifications (user_id, kind, category, title_key, params, actions)
		VALUES (?, 'comment.pending', 'comments', 'legacy.title', '{"marker":"kept"}'::jsonb, '[]'::jsonb) RETURNING id`, user.ID).Row().Scan(&notificationID); err != nil {
		t.Fatalf("create legacy notification: %v", err)
	}
	var deliveryID int64
	if err := db.Raw(`INSERT INTO notification_deliveries (notification_id, channel, status)
		VALUES (?, 'email', 'pending') RETURNING id`, notificationID).Row().Scan(&deliveryID); err != nil {
		t.Fatalf("create legacy delivery: %v", err)
	}
	var legacyRequired string
	if err := db.Raw(`SELECT is_nullable FROM information_schema.columns WHERE table_schema = current_schema()
		AND table_name = 'notification_deliveries' AND column_name = 'notification_id'`).Row().Scan(&legacyRequired); err != nil {
		t.Fatalf("inspect legacy notification_id nullability: %v", err)
	}
	if legacyRequired != "NO" {
		t.Fatalf("legacy notification_id is_nullable = %q, want NO before the additive shop migration", legacyRequired)
	}

	migration, err := embeddedMigrationFiles.ReadFile("migrations/0016_shop_notification_deliveries.sql")
	if err != nil {
		t.Fatalf("read additive shop notification migration 0016: %v", err)
	}
	if err := db.Exec(string(migration)).Error; err != nil {
		t.Fatalf("apply additive shop notification migration: %v", err)
	}
	for column, wantNullable := range map[string]string{"notification_id": "YES", "to_address": "NO", "kind": "NO", "params": "NO"} {
		var nullable string
		if err := db.Raw(`SELECT is_nullable FROM information_schema.columns WHERE table_schema = current_schema()
			AND table_name = 'notification_deliveries' AND column_name = ?`, column).Row().Scan(&nullable); err != nil {
			t.Fatalf("inspect notification_deliveries.%s: %v", column, err)
		}
		if nullable != wantNullable {
			t.Errorf("notification_deliveries.%s is_nullable = %q, want %q", column, nullable, wantNullable)
		}
	}
	var preserved struct {
		NotificationID *int64
		Channel        string
		Status         string
	}
	if err := db.Raw(`SELECT notification_id, channel, status FROM notification_deliveries WHERE id = ?`, deliveryID).Scan(&preserved).Error; err != nil {
		t.Fatalf("read upgraded legacy delivery: %v", err)
	}
	if preserved.NotificationID == nil || *preserved.NotificationID != notificationID || preserved.Channel != "email" || preserved.Status != "pending" {
		t.Errorf("legacy delivery changed after migration: %+v", preserved)
	}
	if err := db.Exec(`INSERT INTO notification_deliveries (notification_id, channel, status, to_address, kind, params)
		VALUES (NULL, 'email', 'pending', 'guest@example.test', 'order.confirmation', '{"order":"JL-2026-0001"}'::jsonb)`).Error; err != nil {
		t.Fatalf("insert guest email delivery without an in-app notification: %v", err)
	}
	if err := db.Exec(string(migration)).Error; err != nil {
		t.Fatalf("rerun additive migration: %v", err)
	}
	var guestCount int64
	if err := db.Raw(`SELECT count(*) FROM notification_deliveries WHERE notification_id IS NULL AND to_address = ?
		AND kind = 'order.confirmation' AND params->>'order' = 'JL-2026-0001'`, "guest@example.test").Scan(&guestCount).Error; err != nil {
		t.Fatalf("read guest delivery after rerun: %v", err)
	}
	if guestCount != 1 {
		t.Errorf("guest delivery rows after rerunning migration = %d, want 1", guestCount)
	}
}
