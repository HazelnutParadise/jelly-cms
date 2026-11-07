package notify

// Plugin notifier channel tests (docs/tickets/29-plugin-read-api-and-extensions.md, PR B3).
//
// Control cases: TestPluginChannelNoProvidersQueuesNothing passes on the empty
// skeleton on purpose. It pins the "nothing configured, nothing queued" rule
// that queuePluginDeliveries must keep, and it carries an in-app positive
// control in the same test. Every other test here must FAIL on the skeleton,
// where queuePluginDeliveries is still a stub that queues nothing.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"
)

type pluginChannelRow struct {
	ID               uint      `gorm:"column:id"`
	Channel          string    `gorm:"column:channel"`
	ToAddress        string    `gorm:"column:to_address"`
	Kind             string    `gorm:"column:kind"`
	Status           string    `gorm:"column:status"`
	Attempts         int       `gorm:"column:attempts"`
	Error            string    `gorm:"column:error"`
	NotificationNull bool      `gorm:"column:notification_null"`
	Params           string    `gorm:"column:params"`
	NextAttemptAt    time.Time `gorm:"column:next_attempt_at"`
}

// pluginChannelRows returns every channel 'plugin' delivery row ordered by
// to_address then id.
func pluginChannelRows(t *testing.T, db *gorm.DB) []pluginChannelRow {
	t.Helper()
	var rows []pluginChannelRow
	if err := db.Raw(`SELECT id, channel, to_address, kind, status, attempts, error,
		notification_id IS NULL AS notification_null, params::text AS params, next_attempt_at
		FROM notification_deliveries WHERE channel = 'plugin' ORDER BY to_address, id`).Scan(&rows).Error; err != nil {
		t.Fatalf("read plugin delivery rows: %v", err)
	}
	return rows
}

func pluginChannelRowByAddress(t *testing.T, rows []pluginChannelRow, address string) pluginChannelRow {
	t.Helper()
	for _, row := range rows {
		if row.ToAddress == address {
			return row
		}
	}
	t.Fatalf("no plugin delivery row for %q; rows = %+v", address, rows)
	return pluginChannelRow{}
}

func pluginChannelDecode(t *testing.T, raw string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("plugin delivery params are not a JSON object: %v: %s", err, raw)
	}
	return doc
}

// pluginChannelArray returns doc[key] as a JSON array. A missing key or a JSON
// null fails, because the contract says every array is present and empty when
// unused.
func pluginChannelArray(t *testing.T, doc map[string]any, key string) []any {
	t.Helper()
	value, ok := doc[key]
	if !ok {
		t.Fatalf("recipients has no %q key: %v", key, doc)
	}
	array, ok := value.([]any)
	if !ok {
		t.Fatalf("recipients.%s = %#v, want a JSON array (not null)", key, value)
	}
	return array
}

func pluginChannelRecipients(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	recipients, ok := doc["recipients"].(map[string]any)
	if !ok {
		t.Fatalf("params has no recipients object: %v", doc)
	}
	return recipients
}

func pluginChannelAdminNotifications(t *testing.T, db *gorm.DB, userID uint, kind string) int64 {
	t.Helper()
	return notifyShopCount(t, db, "SELECT count(*) FROM notifications WHERE user_id = ? AND kind = ?", userID, kind)
}

func TestPluginChannelEmitQueuesOneRowPerDistinctProvider(t *testing.T) {
	db := newNotifyShopDB(t)
	admin := notifyShopAdmin(t, db)
	SetPluginChannel(func(context.Context, *gorm.DB) []string { return []string{"p-a", "p-b", "p-a", ""} })
	t.Cleanup(func() { SetPluginChannel(nil) })

	ev := Event{Kind: KindCommentPending, Category: CategoryComments, Params: map[string]any{"post": "x"},
		Recipients: Recipients{Roles: []string{"admin"}}}
	if err := Emit(context.Background(), db, ev); err != nil {
		t.Fatalf("Emit comment.pending: %v", err)
	}

	rows := pluginChannelRows(t, db)
	if len(rows) != 2 {
		t.Fatalf("plugin delivery rows = %d, want 2 (p-a and p-b; duplicate and empty ids skipped): %+v", len(rows), rows)
	}
	for i, want := range []string{"p-a", "p-b"} {
		row := rows[i]
		if row.ToAddress != want {
			t.Errorf("row %d to_address = %q, want %q", i, row.ToAddress, want)
		}
		if row.Channel != ChannelPlugin {
			t.Errorf("row %q channel = %q, want %q", want, row.Channel, ChannelPlugin)
		}
		if !row.NotificationNull {
			t.Errorf("row %q notification_id is set, want NULL (plugin rows are event-level)", want)
		}
		if row.Kind != KindCommentPending {
			t.Errorf("row %q kind = %q, want %q", want, row.Kind, KindCommentPending)
		}
		if row.Status != "pending" {
			t.Errorf("row %q status = %q, want pending", want, row.Status)
		}
		if row.Attempts != 0 {
			t.Errorf("row %q attempts = %d, want 0", want, row.Attempts)
		}
		if row.Error != "" {
			t.Errorf("row %q error = %q, want empty", want, row.Error)
		}

		doc := pluginChannelDecode(t, row.Params)
		if doc["category"] != CategoryComments {
			t.Errorf("row %q params.category = %v, want %q", want, doc["category"], CategoryComments)
		}
		wantParams := map[string]any{"post": "x"}
		if !reflect.DeepEqual(doc["params"], wantParams) {
			t.Errorf("row %q params.params = %v, want the event params %v", want, doc["params"], wantParams)
		}
		recipients := pluginChannelRecipients(t, doc)
		if got := pluginChannelArray(t, recipients, "roles"); !reflect.DeepEqual(got, []any{"admin"}) {
			t.Errorf("row %q recipients.roles = %v, want [admin]", want, got)
		}
		if got := pluginChannelArray(t, recipients, "user_ids"); len(got) != 0 {
			t.Errorf("row %q recipients.user_ids = %v, want an empty array", want, got)
		}
		if got := pluginChannelArray(t, recipients, "emails"); len(got) != 0 {
			t.Errorf("row %q recipients.emails = %v, want an empty array", want, got)
		}
	}

	// Positive control: the in-app row for the admin is still created next to the plugin rows.
	if n := pluginChannelAdminNotifications(t, db, admin.ID, KindCommentPending); n != 1 {
		t.Errorf("admin in-app comment.pending rows = %d, want 1", n)
	}
}

func TestPluginChannelCarriesRecipientEmails(t *testing.T) {
	db := newNotifyShopDB(t)
	notifyShopAdmin(t, db)
	SetPluginChannel(func(context.Context, *gorm.DB) []string { return []string{"p-a"} })
	t.Cleanup(func() { SetPluginChannel(nil) })

	ev := Event{Kind: KindCommentPending, Category: CategoryComments, Params: map[string]any{"post": "y"},
		Recipients: Recipients{Roles: []string{"admin"}, Emails: []string{"guest@example.test"}}}
	if err := Emit(context.Background(), db, ev); err != nil {
		t.Fatalf("Emit with guest email: %v", err)
	}

	// Positive control: the existing guest email row is queued as before.
	if n := notifyShopCount(t, db, "SELECT count(*) FROM notification_deliveries WHERE channel = 'email' AND to_address = ? AND notification_id IS NULL", "guest@example.test"); n != 1 {
		t.Errorf("guest email deliveries = %d, want 1", n)
	}

	rows := pluginChannelRows(t, db)
	if len(rows) != 1 {
		t.Fatalf("plugin delivery rows = %d, want 1: %+v", len(rows), rows)
	}
	doc := pluginChannelDecode(t, rows[0].Params)
	recipients := pluginChannelRecipients(t, doc)
	if got := pluginChannelArray(t, recipients, "emails"); !reflect.DeepEqual(got, []any{"guest@example.test"}) {
		t.Errorf("recipients.emails = %v, want [guest@example.test]", got)
	}
	if got := pluginChannelArray(t, recipients, "roles"); !reflect.DeepEqual(got, []any{"admin"}) {
		t.Errorf("recipients.roles = %v, want [admin]", got)
	}
	if got := pluginChannelArray(t, recipients, "user_ids"); len(got) != 0 {
		t.Errorf("recipients.user_ids = %v, want an empty array", got)
	}
}

// Control case: passes on the skeleton (see the file header).
func TestPluginChannelNoProvidersQueuesNothing(t *testing.T) {
	db := newNotifyShopDB(t)
	admin := notifyShopAdmin(t, db)
	t.Cleanup(func() { SetPluginChannel(nil) })
	ev := Event{Kind: KindCommentPending, Category: CategoryComments, Params: map[string]any{"post": "z"},
		Recipients: Recipients{Roles: []string{"admin"}}}

	SetPluginChannel(nil)
	if err := Emit(context.Background(), db, ev); err != nil {
		t.Fatalf("Emit with nil provider func: %v", err)
	}
	if n := len(pluginChannelRows(t, db)); n != 0 {
		t.Errorf("plugin rows with nil provider func = %d, want 0", n)
	}
	// Positive control: the event itself was emitted.
	if n := pluginChannelAdminNotifications(t, db, admin.ID, KindCommentPending); n != 1 {
		t.Errorf("admin in-app rows after first Emit = %d, want 1", n)
	}

	SetPluginChannel(func(context.Context, *gorm.DB) []string { return []string{} })
	if err := Emit(context.Background(), db, ev); err != nil {
		t.Fatalf("Emit with empty provider list: %v", err)
	}
	if n := len(pluginChannelRows(t, db)); n != 0 {
		t.Errorf("plugin rows with empty provider list = %d, want 0", n)
	}
	if n := pluginChannelAdminNotifications(t, db, admin.ID, KindCommentPending); n != 2 {
		t.Errorf("admin in-app rows after second Emit = %d, want 2", n)
	}
}

func TestPluginChannelProcessDeliveryRetriesFailuresAndMarksSuccess(t *testing.T) {
	db := newNotifyShopDB(t)
	notifyShopAdmin(t, db)
	SetPluginChannel(func(context.Context, *gorm.DB) []string { return []string{"p-a", "p-b"} })
	t.Cleanup(func() { SetPluginChannel(nil) })
	ev := Event{Kind: KindCommentPending, Category: CategoryComments, Params: map[string]any{"post": "x"},
		Recipients: Recipients{Roles: []string{"admin"}}}
	if err := Emit(context.Background(), db, ev); err != nil {
		t.Fatalf("Emit comment.pending: %v", err)
	}
	rows := pluginChannelRows(t, db)
	if len(rows) != 2 {
		t.Fatalf("plugin delivery rows = %d, want 2: %+v", len(rows), rows)
	}
	rowA := pluginChannelRowByAddress(t, rows, "p-a")
	rowB := pluginChannelRowByAddress(t, rows, "p-b")

	var seen []Delivery
	send := func(_ context.Context, d Delivery) error {
		seen = append(seen, d)
		if d.Target == "p-a" {
			return errors.New("notifier unavailable")
		}
		return nil
	}

	before := time.Now().UTC()
	if err := ProcessDelivery(context.Background(), db, rowA.ID, send); err == nil {
		t.Errorf("ProcessDelivery(p-a) error = nil, want the send error")
	}
	if len(seen) != 1 {
		t.Fatalf("sender calls for p-a = %d, want 1", len(seen))
	}
	d := seen[0]
	if d.Channel != ChannelPlugin {
		t.Errorf("delivery channel = %q, want %q", d.Channel, ChannelPlugin)
	}
	if d.Target != "p-a" {
		t.Errorf("delivery target = %q, want p-a", d.Target)
	}
	if d.Kind != KindCommentPending {
		t.Errorf("delivery kind = %q, want %q", d.Kind, KindCommentPending)
	}
	if d.Params["category"] != CategoryComments {
		t.Errorf("delivery params.category = %v, want %q", d.Params["category"], CategoryComments)
	}
	if _, ok := d.Params["recipients"].(map[string]any); !ok {
		t.Errorf("delivery params.recipients = %v, want an object", d.Params["recipients"])
	}

	failed := pluginChannelRowByAddress(t, pluginChannelRows(t, db), "p-a")
	if failed.Status != "pending" || failed.Attempts != 1 || failed.Error != "delivery failed" {
		t.Errorf("failed plugin row = status %q attempts %d error %q, want pending/1/%q",
			failed.Status, failed.Attempts, failed.Error, "delivery failed")
	}
	if !failed.NextAttemptAt.After(before) {
		t.Errorf("failed plugin row next_attempt_at = %s, want later than %s (retry delayed)", failed.NextAttemptAt, before)
	}

	if err := ProcessDelivery(context.Background(), db, rowB.ID, send); err != nil {
		t.Fatalf("ProcessDelivery(p-b) error = %v, want nil", err)
	}
	if len(seen) != 2 || seen[1].Target != "p-b" || seen[1].Channel != ChannelPlugin {
		t.Errorf("sender calls after p-b = %+v, want a second plugin delivery to p-b", seen)
	}
	sent := pluginChannelRowByAddress(t, pluginChannelRows(t, db), "p-b")
	if sent.Status != "sent" || sent.Attempts != 1 {
		t.Errorf("successful plugin row = status %q attempts %d, want sent/1", sent.Status, sent.Attempts)
	}
	// Negative control: the failed row was not marked sent.
	if after := pluginChannelRowByAddress(t, pluginChannelRows(t, db), "p-a"); after.Status == "sent" {
		t.Errorf("failed plugin row status = %q after a send error, want pending", after.Status)
	}
}

func TestPluginChannelPruneRemovesOldSentRowsOnly(t *testing.T) {
	db := newNotifyShopDB(t)
	notifyShopAdmin(t, db)
	SetPluginChannel(func(context.Context, *gorm.DB) []string { return []string{"p-a", "p-b"} })
	t.Cleanup(func() { SetPluginChannel(nil) })
	ev := Event{Kind: KindCommentPending, Category: CategoryComments, Params: map[string]any{"post": "x"},
		Recipients: Recipients{Roles: []string{"admin"}}}
	if err := Emit(context.Background(), db, ev); err != nil {
		t.Fatalf("Emit comment.pending: %v", err)
	}
	rows := pluginChannelRows(t, db)
	if len(rows) != 2 {
		t.Fatalf("plugin delivery rows = %d, want 2: %+v", len(rows), rows)
	}
	sentRow := pluginChannelRowByAddress(t, rows, "p-a")
	pendingRow := pluginChannelRowByAddress(t, rows, "p-b")

	ok := func(context.Context, Delivery) error { return nil }
	if err := ProcessDelivery(context.Background(), db, sentRow.ID, ok); err != nil {
		t.Fatalf("ProcessDelivery(p-a): %v", err)
	}
	old := time.Now().UTC().Add(-100 * 24 * time.Hour)
	if err := db.Exec("UPDATE notification_deliveries SET next_attempt_at = ? WHERE id IN ?", old, []uint{sentRow.ID, pendingRow.ID}).Error; err != nil {
		t.Fatalf("age plugin delivery rows: %v", err)
	}
	// Positive control: the sent row exists before pruning.
	if n := notifyShopCount(t, db, "SELECT count(*) FROM notification_deliveries WHERE id = ? AND status = 'sent'", sentRow.ID); n != 1 {
		t.Fatalf("sent plugin row before Prune = %d, want 1", n)
	}

	if err := Prune(context.Background(), db, time.Now().UTC()); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n := notifyShopCount(t, db, "SELECT count(*) FROM notification_deliveries WHERE id = ?", sentRow.ID); n != 0 {
		t.Errorf("old sent plugin row survived Prune: count = %d, want 0", n)
	}
	if n := notifyShopCount(t, db, "SELECT count(*) FROM notification_deliveries WHERE id = ? AND status = 'pending' AND channel = 'plugin'", pendingRow.ID); n != 1 {
		t.Errorf("old pending plugin row after Prune = %d, want 1 (only sent and failed are pruned)", n)
	}
}

// TestPluginChannelSkipsRequiredCustomerEmails: the customer-only transactional
// emails carry order access tokens and set-password links, so they are never
// queued for plugin notifiers; a notification-center kind in the same test is
// the positive control.
func TestPluginChannelSkipsRequiredCustomerEmails(t *testing.T) {
	db := newNotifyShopDB(t)
	notifyShopAdmin(t, db)
	SetPluginChannel(func(context.Context, *gorm.DB) []string { return []string{"p-a"} })
	t.Cleanup(func() { SetPluginChannel(nil) })

	for _, kind := range []string{KindOrderConfirmation, KindOrderShipped, KindOrderLookupLink, KindOrderOfflineInstructions, KindCustomerSetPassword} {
		if PluginChannelCarries(kind) {
			t.Errorf("PluginChannelCarries(%s) = true, want false", kind)
		}
		spec, _ := LookupKind(kind)
		ev := Event{Kind: kind, Category: spec.Category, Params: map[string]any{"number": "JL-1", "url": "/orders/view?t=secret"},
			Recipients: Recipients{Emails: []string{"guest@example.test"}}}
		if err := Emit(context.Background(), db, ev); err != nil {
			t.Fatalf("Emit %s: %v", kind, err)
		}
	}
	if n := len(pluginChannelRows(t, db)); n != 0 {
		t.Errorf("plugin rows after required customer emails = %d, want 0", n)
	}
	// Positive control: the guest emails themselves were queued, and a
	// notification-center kind reaches the plugin.
	if n := notifyShopCount(t, db, "SELECT count(*) FROM notification_deliveries WHERE channel = 'email' AND to_address = ?", "guest@example.test"); n != 5 {
		t.Errorf("guest email deliveries = %d, want 5", n)
	}
	if !PluginChannelCarries(KindOrderNew) {
		t.Errorf("PluginChannelCarries(%s) = false, want true", KindOrderNew)
	}
	ev := Event{Kind: KindOrderNew, Category: CategoryOrders, Params: map[string]any{"number": "JL-1"}, Recipients: Recipients{Roles: []string{"admin"}}}
	if err := Emit(context.Background(), db, ev); err != nil {
		t.Fatalf("Emit order.new: %v", err)
	}
	if rows := pluginChannelRows(t, db); len(rows) != 1 || rows[0].Kind != KindOrderNew {
		t.Errorf("plugin rows after order.new = %+v, want one order.new row", rows)
	}
}
