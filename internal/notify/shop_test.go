package notify

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"gorm.io/gorm"
)

const (
	notifyShopHost     = "127.0.0.1"
	notifyShopPort     = "55432"
	notifyShopUser     = "jelly_test"
	notifyShopPass     = "jelly-local-acceptance"
	notifyShopAdminDB  = "jelly_acceptance"
	notifyShopDBPrefix = "jelly_notify_shop_"
)

var notifyShopDBCounter atomic.Int64

func newNotifyShopDB(t *testing.T) *gorm.DB {
	t.Helper()
	if os.Getenv("JELLY_TEST_POSTGRES") != "1" {
		t.Skip("set JELLY_TEST_POSTGRES=1 to run notification shop tests against dedicated PostgreSQL")
	}
	open := func(name string) (*gorm.DB, error) {
		return data.Open(config.DatabaseConfig{Host: notifyShopHost, Port: notifyShopPort, User: notifyShopUser,
			Password: notifyShopPass, Name: name, Timezone: "UTC"})
	}
	admin, err := open(notifyShopAdminDB)
	if err != nil {
		t.Fatalf("open dedicated PostgreSQL admin database: %v", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatalf("get admin pool: %v", err)
	}
	t.Cleanup(func() { _ = adminSQL.Close() })
	dbName := fmt.Sprintf("%s%d_%d", notifyShopDBPrefix, os.Getpid(), notifyShopDBCounter.Add(1))
	if err := admin.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, dbName)).Error; err != nil {
		t.Fatalf("create isolated notification database: %v", err)
	}
	t.Cleanup(func() {
		if !strings.HasPrefix(dbName, notifyShopDBPrefix) {
			t.Errorf("refusing to drop unexpected database %q", dbName)
			return
		}
		if err := admin.Exec(fmt.Sprintf(`DROP DATABASE "%s" WITH (FORCE)`, dbName)).Error; err != nil {
			t.Errorf("drop isolated notification database: %v", err)
		}
	})
	db, err := open(dbName)
	if err != nil {
		t.Fatalf("open isolated notification database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get isolated database pool: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := data.MigrateDB(db); err != nil {
		t.Fatalf("migrate isolated notification database: %v", err)
	}
	return db
}

func notifyShopAdmin(t *testing.T, db *gorm.DB) core.User {
	t.Helper()
	u := core.User{Username: "notify-shop-admin", Email: "staff@example.test", Role: "admin", Status: "active", Provider: "local"}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("create notification recipient: %v", err)
	}
	return u
}

func TestTicket07OrderNotificationKindsAndRefundPreference(t *testing.T) {
	for _, kind := range []string{
		"order.confirmation", "order.shipped", "order.lookup_link", "order.offline_instructions", "customer.set_password",
		"order.refunded",
	} {
		spec, ok := LookupKind(kind)
		if !ok {
			t.Errorf("ticket 07 notification kind %q is not registered", kind)
			continue
		}
		if kind == "order.refunded" && spec.Preference != KindOrderPaymentFailed {
			t.Errorf("order.refunded preference = %q, want %q", spec.Preference, KindOrderPaymentFailed)
		}
	}
}

func TestGuestOrderEmailIsNormalizedDeduplicatedAndIndependentOfPreferences(t *testing.T) {
	db := newNotifyShopDB(t)
	admin := notifyShopAdmin(t, db)
	if err := db.Create(&core.NotificationPref{UserID: admin.ID, Event: KindOrderPaymentFailed, InApp: false, Email: false, Webhook: false}).Error; err != nil {
		t.Fatalf("disable staff payment notification: %v", err)
	}
	guest := Event{Kind: "order.confirmation", Category: CategoryOrders, Params: map[string]any{"order": "JL-2026-0001"},
		Recipients: Recipients{Emails: []string{" Guest@Example.Test ", "guest@example.test", "GUEST@EXAMPLE.TEST"}}}
	if err := Emit(context.Background(), db, guest); err != nil {
		t.Fatalf("Emit guest confirmation: %v", err)
	}
	if n := notifyShopCount(t, db, "SELECT count(*) FROM notification_deliveries WHERE channel = 'email' AND to_address = ? AND kind = ?", "guest@example.test", "order.confirmation"); n != 1 {
		t.Errorf("normalized guest confirmation deliveries = %d, want one", n)
	}
	var nullRows int64
	if err := db.Raw("SELECT count(*) FROM notification_deliveries WHERE to_address = ? AND notification_id IS NULL", "guest@example.test").Scan(&nullRows).Error; err != nil {
		t.Fatalf("count guest-only deliveries: %v", err)
	}
	if nullRows != 1 {
		t.Errorf("guest-only deliveries with NULL notification_id = %d, want 1", nullRows)
	}

	refund := Event{Kind: "order.refunded", Category: CategoryOrders, Params: map[string]any{"order": "JL-2026-0001"}, Recipients: Recipients{Roles: []string{"admin"}}}
	if err := Emit(context.Background(), db, refund); err != nil {
		t.Fatalf("Emit refund notification: %v", err)
	}
	if n := notifyShopCount(t, db, "SELECT count(*) FROM notifications WHERE user_id = ? AND kind = 'order.refunded'", admin.ID); n != 0 {
		t.Errorf("refund created %d staff notifications despite disabled order.payment_failed preference", n)
	}
	if n := notifyShopCount(t, db, "SELECT count(*) FROM notification_deliveries WHERE kind = 'order.refunded' AND to_address = ?", "staff@example.test"); n != 0 {
		t.Errorf("refund queued %d staff emails despite disabled order.payment_failed preference", n)
	}
}

func TestRequiredCustomerEmailAllowsOnlyExplicitInvitedCustomer(t *testing.T) {
	db := newNotifyShopDB(t)
	invitedCustomer := core.User{Username: "invited-customer", Email: "invited@example.test", Role: "customer", Status: "invited", Provider: "local"}
	if err := db.Create(&invitedCustomer).Error; err != nil {
		t.Fatalf("create invited customer: %v", err)
	}
	invitedStaff := core.User{Username: "invited-staff", Email: "staff-invited@example.test", Role: "admin", Status: "invited", Provider: "local"}
	if err := db.Create(&invitedStaff).Error; err != nil {
		t.Fatalf("create invited staff: %v", err)
	}
	disabledCustomer := core.User{Username: "disabled-customer", Email: "disabled@example.test", Role: "customer", Status: "disabled", Provider: "local"}
	if err := db.Create(&disabledCustomer).Error; err != nil {
		t.Fatalf("create disabled customer: %v", err)
	}

	confirmation := Event{Kind: KindOrderConfirmation, Category: CategoryOrders,
		Params: map[string]any{"order": "JL-2026-0005"}, Recipients: Recipients{UserIDs: []uint{invitedCustomer.ID}}}
	if err := Emit(context.Background(), db, confirmation); err != nil {
		t.Fatalf("Emit explicit invited-customer confirmation: %v", err)
	}
	if n := notifyShopCount(t, db, `SELECT count(*) FROM notification_deliveries WHERE channel = 'email' AND to_address = ? AND kind = ?`, invitedCustomer.Email, KindOrderConfirmation); n != 1 {
		t.Errorf("explicit invited-customer confirmation deliveries = %d, want one", n)
	}
	if n := notifyShopCount(t, db, `SELECT count(*) FROM notifications WHERE user_id = ?`, invitedCustomer.ID); n != 0 {
		t.Errorf("invited customer received %d unnecessary in-app notifications", n)
	}

	for _, event := range []Event{
		{Kind: KindOrderPaymentFailed, Category: CategoryOrders, Recipients: Recipients{UserIDs: []uint{invitedCustomer.ID}}},
		{Kind: KindOrderConfirmation, Category: CategoryOrders, Recipients: Recipients{UserIDs: []uint{invitedStaff.ID}}},
		{Kind: KindOrderConfirmation, Category: CategoryOrders, Recipients: Recipients{UserIDs: []uint{disabledCustomer.ID}}},
		{Kind: KindOrderConfirmation, Category: CategoryOrders, Recipients: Recipients{Roles: []string{"customer"}}},
	} {
		if err := Emit(context.Background(), db, event); err != nil {
			t.Errorf("Emit disallowed recipient kind=%q recipients=%+v: %v", event.Kind, event.Recipients, err)
		}
	}
	for _, tc := range []struct {
		user  core.User
		count int64
	}{
		{user: invitedCustomer, count: 1}, // only the explicitly targeted required confirmation is allowed
		{user: invitedStaff, count: 0},
		{user: disabledCustomer, count: 0},
	} {
		if n := notifyShopCount(t, db, `SELECT count(*) FROM notification_deliveries WHERE to_address = ?`, tc.user.Email); n != tc.count {
			t.Errorf("recipient %q email deliveries = %d, want %d", tc.user.Email, n, tc.count)
		}
	}
}

func TestOrderRefundUsesPaymentFailedPreference(t *testing.T) {
	db := newNotifyShopDB(t)
	admin := notifyShopAdmin(t, db)
	if err := db.Create(&core.NotificationPref{UserID: admin.ID, Event: KindOrderPaymentFailed, InApp: false, Email: true, Webhook: false}).Error; err != nil {
		t.Fatalf("set payment-failed preference: %v", err)
	}
	if err := Emit(context.Background(), db, Event{Kind: "order.refunded", Category: CategoryOrders,
		Params: map[string]any{"order": "JL-2026-0002"}, Recipients: Recipients{Roles: []string{"admin"}}}); err != nil {
		t.Fatalf("Emit refund notification: %v", err)
	}
	if n := notifyShopCount(t, db, "SELECT count(*) FROM notifications WHERE user_id = ? AND kind = 'order.refunded' AND in_app_visible = false", admin.ID); n != 1 {
		t.Errorf("refund in-app visibility rows = %d, want 1 hidden row governed by payment_failed preference", n)
	}
	if n := notifyShopCount(t, db, "SELECT count(*) FROM notification_deliveries WHERE kind = 'order.refunded' AND to_address = ?", "staff@example.test"); n != 1 {
		t.Errorf("refund email deliveries = %d, want 1 governed by payment_failed preference", n)
	}
}

func TestEmailOnlyGroupedEventsCoalesceWithoutDuplicateUnreadGroup(t *testing.T) {
	db := newNotifyShopDB(t)
	admin := notifyShopAdmin(t, db)
	if err := db.Create(&core.NotificationPref{UserID: admin.ID, Event: KindOrderPaymentFailed, InApp: false, Email: true, Webhook: false}).Error; err != nil {
		t.Fatalf("set email-only payment-failed preference: %v", err)
	}
	event := Event{Kind: KindOrderPaymentFailed, Category: CategoryOrders,
		Params: map[string]any{"order": "JL-2026-0004"}, GroupKey: "order:JL-2026-0004",
		Recipients: Recipients{Roles: []string{"admin"}}}
	for range 2 {
		if err := Emit(context.Background(), db, event); err != nil {
			t.Fatalf("Emit repeated email-only grouped event: %v", err)
		}
	}
	if n := notifyShopCount(t, db, `SELECT count(*) FROM notifications WHERE user_id = ? AND group_key = ? AND read_at IS NULL`, admin.ID, event.GroupKey); n != 1 {
		t.Errorf("unread grouped rows = %d, want one coalesced row", n)
	}
	if n := notifyShopCount(t, db, `SELECT count(*) FROM notifications WHERE user_id = ? AND group_key = ? AND count = 2 AND in_app_visible = false`, admin.ID, event.GroupKey); n != 1 {
		t.Errorf("hidden coalesced row count = %d, want one row with count 2", n)
	}
	if n := notifyShopCount(t, db, `SELECT count(*) FROM notification_deliveries WHERE kind = ? AND to_address = ? AND channel = 'email'`, KindOrderPaymentFailed, admin.Email); n != 2 {
		t.Errorf("email deliveries = %d, want one delivery for each event", n)
	}
}

func TestShopNotificationActionsUseRecipientFacingDestinations(t *testing.T) {
	tests := []struct {
		kind, label, target string
	}{
		{KindOrderNew, "orders.open", "/admin/orders"},
		{KindOrderPaymentFailed, "orders.open", "/admin/orders"},
		{KindOrderRefunded, "orders.open", "/admin/orders"},
		{KindOrderConfirmation, "orders.open", "/account/orders"},
		{KindOrderShipped, "orders.open", "/account/orders"},
		{KindOrderLookupLink, "orders.open", "/account/orders"},
		{KindOrderOfflineInstructions, "orders.open", "/account/orders"},
		{KindSecurityNewDevice, "account.security", "/admin/account?tab=security"},
		{KindSecurityPasswordChanged, "account.security", "/admin/account?tab=security"},
	}
	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			got := actionsFor(tt.kind)
			if len(got) != 1 {
				t.Fatalf("actionsFor(%q) returned %d actions, want one", tt.kind, len(got))
			}
			if got[0].LabelKey != tt.label || got[0].URL != tt.target {
				t.Errorf("actionsFor(%q) = (%q, %q), want (%q, %q)", tt.kind, got[0].LabelKey, got[0].URL, tt.label, tt.target)
			}
		})
	}
}

func TestNotificationAndDeliveryRollbackTogetherOnQueueFailure(t *testing.T) {
	db := newNotifyShopDB(t)
	admin := notifyShopAdmin(t, db)
	if err := db.Create(&core.NotificationPref{UserID: admin.ID, Event: KindOrderPaymentFailed, InApp: true, Email: true, Webhook: false}).Error; err != nil {
		t.Fatalf("enable staff email notification: %v", err)
	}
	if err := db.Exec(`CREATE FUNCTION reject_shop_notification_delivery() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'delivery queue rejected'; END $$`).Error; err != nil {
		t.Fatalf("create delivery failure function: %v", err)
	}
	if err := db.Exec(`CREATE TRIGGER reject_shop_notification_delivery BEFORE INSERT ON notification_deliveries FOR EACH ROW EXECUTE FUNCTION reject_shop_notification_delivery()`).Error; err != nil {
		t.Fatalf("create delivery failure trigger: %v", err)
	}
	err := Emit(context.Background(), db, Event{Kind: KindOrderPaymentFailed, Category: CategoryOrders,
		Params: map[string]any{"order": "JL-2026-0003"}, Recipients: Recipients{Roles: []string{"admin"}}})
	if err == nil {
		t.Fatal("Emit returned nil after the delivery queue rejected its insert")
	}
	if n := notifyShopCount(t, db, "SELECT count(*) FROM notifications WHERE user_id = ? AND kind = 'order.payment_failed'", admin.ID); n != 0 {
		t.Errorf("notifications left after delivery failure = %d, want 0", n)
	}
	if n := notifyShopCount(t, db, "SELECT count(*) FROM notification_deliveries WHERE kind = 'order.payment_failed'"); n != 0 {
		t.Errorf("deliveries left after rollback = %d, want 0", n)
	}
}

func notifyShopCount(t *testing.T, db *gorm.DB, query string, args ...any) int64 {
	t.Helper()
	var count int64
	if err := db.Raw(query, args...).Scan(&count).Error; err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return count
}
