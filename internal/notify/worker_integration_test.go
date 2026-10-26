package notify

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

func TestProcessDeliveryHonorsNextAttemptAt(t *testing.T) {
	db := newNotifyShopDB(t)
	user := notifyShopAdmin(t, db)
	now := time.Now().UTC()
	if err := db.Create(&core.NotificationSettings{UserID: user.ID}).Error; err != nil {
		t.Fatalf("create notification settings: %v", err)
	}
	notification := core.Notification{
		UserID: user.ID, Kind: "notify.delivery.retry", Category: CategorySecurity,
		Params: json.RawMessage(`{}`), Actions: json.RawMessage(`[]`), Count: 1,
		ExpiresAt: now.Add(24 * time.Hour),
	}
	if err := db.Create(&notification).Error; err != nil {
		t.Fatalf("create delivery notification: %v", err)
	}
	rows := []core.NotificationDelivery{
		{
			NotificationID: notification.ID, Kind: "notify.delivery.future", ToAddress: "future@example.test",
			Channel: "email", Status: "pending", Attempts: 0, NextAttemptAt: now.Add(time.Hour),
			Params: json.RawMessage(`{}`),
		},
		{
			NotificationID: notification.ID, Kind: "notify.delivery.due", ToAddress: "due@example.test",
			Channel: "email", Status: "pending", Attempts: 0, NextAttemptAt: now.Add(-time.Minute),
			Params: json.RawMessage(`{}`),
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create future and due delivery rows: %v", err)
	}
	sent := make([]uint, 0, 2)
	spy := func(_ context.Context, delivery Delivery) error {
		sent = append(sent, delivery.ID)
		return nil
	}

	if err := ProcessDelivery(context.Background(), db, rows[0].ID, spy); err != nil {
		t.Fatalf("process future pending delivery: %v", err)
	}
	var future core.NotificationDelivery
	if err := db.Take(&future, rows[0].ID).Error; err != nil {
		t.Fatalf("read future delivery after processor call: %v", err)
	}
	if len(sent) != 0 {
		t.Errorf("future delivery invoked sender %d times, want zero", len(sent))
	}
	if future.Status != "pending" || future.Attempts != 0 {
		t.Errorf("future delivery state = %q attempts %d, want pending/0", future.Status, future.Attempts)
	}

	if err := ProcessDelivery(context.Background(), db, rows[1].ID, spy); err != nil {
		t.Fatalf("process due delivery: %v", err)
	}
	var due core.NotificationDelivery
	if err := db.Take(&due, rows[1].ID).Error; err != nil {
		t.Fatalf("read due delivery after processor call: %v", err)
	}
	if len(sent) != 1 || sent[0] != rows[1].ID {
		t.Errorf("sender calls after future and due rows = %v, want only due delivery %d", sent, rows[1].ID)
	}
	if due.Status != "sent" || due.Attempts != 1 {
		t.Errorf("due delivery state = %q attempts %d, want sent/1", due.Status, due.Attempts)
	}
}
