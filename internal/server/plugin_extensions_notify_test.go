package server

// notify.channel end-to-end tests (docs/tickets/29-plugin-read-api-and-extensions.md, PR B3).
//
// Control cases: none. Every test here must FAIL on the empty skeleton, where
// notifyPluginChannelProviders returns nil (so no plugin rows are queued) and
// deliverPluginNotification returns errPluginNotifierUnavailable. Each negative
// assertion (no call, no row) is paired with a positive assertion in the same
// test (a row or a delivery that does exist).

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/ext"
	"github.com/HazelnutParadise/jelly-cms/internal/notify"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"gorm.io/gorm"
)

const (
	pluginNotifyOK     = "notifier-ok"
	pluginNotifyNoPerm = "notifier-noperm"
	pluginNotifyThrow  = "notifier-throw"
	pluginNotifyOKJS   = `JellyCMS.registerExtension("notify.channel", function (input) {
  JellyCMS.log.info("notify:" + JSON.stringify(input));
  return {ok: true};
});`
	pluginNotifyThrowJS = `JellyCMS.registerExtension("notify.channel", function (input) {
  throw new Error("notifier boom");
});`
)

type pluginNotifyRow struct {
	ID               uint      `gorm:"column:id"`
	ToAddress        string    `gorm:"column:to_address"`
	Kind             string    `gorm:"column:kind"`
	Status           string    `gorm:"column:status"`
	Attempts         int       `gorm:"column:attempts"`
	NotificationNull bool      `gorm:"column:notification_null"`
	Params           string    `gorm:"column:params"`
	NextAttemptAt    time.Time `gorm:"column:next_attempt_at"`
}

// pluginNotifyRows returns the channel 'plugin' delivery rows with id above
// afterID, ordered by id.
func pluginNotifyRows(t *testing.T, db *gorm.DB, afterID uint) []pluginNotifyRow {
	t.Helper()
	var rows []pluginNotifyRow
	if err := db.Raw(`SELECT id, to_address, kind, status, attempts, notification_id IS NULL AS notification_null,
		params::text AS params, next_attempt_at
		FROM notification_deliveries WHERE channel = 'plugin' AND id > ? ORDER BY id`, afterID).Scan(&rows).Error; err != nil {
		t.Fatalf("read plugin notifier delivery rows: %v", err)
	}
	return rows
}

func pluginNotifyRowFor(t *testing.T, rows []pluginNotifyRow, pluginID string) pluginNotifyRow {
	t.Helper()
	for _, row := range rows {
		if row.ToAddress == pluginID {
			return row
		}
	}
	t.Fatalf("no plugin delivery row for notifier %q; rows = %+v", pluginID, rows)
	return pluginNotifyRow{}
}

func pluginNotifyTargets(rows []pluginNotifyRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.ToAddress)
	}
	sort.Strings(out)
	return out
}

// installPluginNotifier writes and loads a plugin that declares the notify.channel
// extension. withEvents controls the events permission, which the callback
// needs to receive recipient emails. The enabled row carries the approval hash
// of its manifest, as an approved install would.
func installPluginNotifier(t *testing.T, h *pluginEventsHarness, id string, withEvents bool, script string) {
	t.Helper()
	meta := plugin.PluginMetadata{
		ID: id, Name: "Notifier " + id, Version: "1.0.0", Entrypoint: "index.js",
		Extensions: []plugin.ExtensionSpec{{Point: ext.PointNotifyChannel}},
	}
	if withEvents {
		meta.Permissions = []plugin.PermissionSpec{{Name: "events"}}
	}
	dir := filepath.Join("data", "plugins", id)
	writeReloadPlugin(t, dir, meta, script)
	granted := plugin.PermissionNamesHash(plugin.ManifestPermissionNames(meta))
	if err := h.db.Exec(`INSERT INTO plugins (id, kind, version, source, enabled, signature, permissions_granted_hash) VALUES (?, 'plugin', '1.0.0', 'upload', TRUE, 'unsigned', ?)`, id, granted).Error; err != nil {
		t.Fatalf("insert plugins row for %s: %v", id, err)
	}
	if err := h.runtime.Load(context.Background(), dir); err != nil {
		t.Fatalf("load notifier plugin %s: %v", id, err)
	}
}

// newPluginNotifyHarness boots the events harness (its own events-fixture
// plugin has no notify.channel extension) and installs three notifiers: one
// approved with events, one without the events permission and one approved
// whose callback throws.
func newPluginNotifyHarness(t *testing.T) *pluginEventsHarness {
	t.Helper()
	h := newPluginEventsHarness(t)
	installPluginNotifier(t, h, pluginNotifyOK, true, pluginNotifyOKJS)
	installPluginNotifier(t, h, pluginNotifyNoPerm, false, pluginNotifyOKJS)
	installPluginNotifier(t, h, pluginNotifyThrow, true, pluginNotifyThrowJS)
	return h
}

// emitCommentPending emits one comment.pending to the admin role through the
// production notify.Emit, the way a real caller does.
func emitCommentPending(t *testing.T, h *pluginEventsHarness, post string) {
	t.Helper()
	ev := notify.Event{
		Kind: notify.KindCommentPending, Category: notify.CategoryComments,
		Params:     map[string]any{"post": post},
		Recipients: notify.Recipients{Roles: []string{"admin"}},
	}
	if err := notify.Emit(context.Background(), h.db, ev); err != nil {
		t.Fatalf("notify.Emit comment.pending: %v", err)
	}
}

// waitPluginNotifyLog polls the captured plugin log until pluginID writes a
// line starting with prefix, and returns the rest of that line.
func waitPluginNotifyLog(t *testing.T, h *pluginEventsHarness, pluginID, prefix string) string {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		for _, line := range h.sink.snapshot() {
			if line.pluginID == pluginID && strings.HasPrefix(line.message, prefix) {
				return strings.TrimPrefix(line.message, prefix)
			}
		}
		select {
		case <-deadline.C:
			t.Fatalf("no %q log line from %s within 5s; captured log:\n%s", prefix, pluginID, h.sink.dump())
		case <-tick.C:
		}
	}
}

func TestPluginExtensionsNotifyEmitQueuesOneRowPerLiveNotifier(t *testing.T) {
	h := newPluginNotifyHarness(t)
	admin := h.user("admin", "notify-plugin-admin")

	emitCommentPending(t, h, "x")

	rows := pluginNotifyRows(t, h.db, 0)
	want := []string{pluginNotifyOK, pluginNotifyNoPerm, pluginNotifyThrow}
	sort.Strings(want)
	if got := pluginNotifyTargets(rows); !reflect.DeepEqual(got, want) {
		t.Fatalf("plugin delivery targets = %v, want one row per live notifier %v", got, want)
	}
	for _, row := range rows {
		if row.Kind != notify.KindCommentPending {
			t.Errorf("row for %s kind = %q, want %q", row.ToAddress, row.Kind, notify.KindCommentPending)
		}
		if row.Status != "pending" || row.Attempts != 0 {
			t.Errorf("row for %s status = %q attempts %d, want pending/0", row.ToAddress, row.Status, row.Attempts)
		}
		if !row.NotificationNull {
			t.Errorf("row for %s has a notification_id, want NULL", row.ToAddress)
		}
	}
	// Positive control: the admin's in-app notification row is still created.
	if n := notifyServerCount(t, h, "SELECT count(*) FROM notifications WHERE user_id = ? AND kind = ?", admin.ID, notify.KindCommentPending); n != 1 {
		t.Errorf("admin in-app comment.pending rows = %d, want 1", n)
	}
}

func TestPluginExtensionsNotifyDeliversEventToNotifierWithPermission(t *testing.T) {
	h := newPluginNotifyHarness(t)
	h.user("admin", "notify-deliver-admin")
	emitCommentPending(t, h, "x")
	row := pluginNotifyRowFor(t, pluginNotifyRows(t, h.db, 0), pluginNotifyOK)

	before := time.Now().UTC()
	if err := notify.ProcessDelivery(context.Background(), h.db, row.ID, sendSystemNotification); err != nil {
		t.Fatalf("ProcessDelivery(%s) = %v, want nil", pluginNotifyOK, err)
	}
	if after := pluginNotifyRowFor(t, pluginNotifyRows(t, h.db, 0), pluginNotifyOK); after.Status != "sent" {
		t.Errorf("row status after successful callback = %q, want sent", after.Status)
	}

	raw := waitPluginNotifyLog(t, h, pluginNotifyOK, "notify:")
	var input struct {
		Kind       string         `json:"kind"`
		Category   string         `json:"category"`
		Params     map[string]any `json:"params"`
		Recipients struct {
			Roles   []string `json:"roles"`
			UserIDs []any    `json:"user_ids"`
			Emails  []any    `json:"emails"`
		} `json:"recipients"`
		At string `json:"at"`
	}
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatalf("notify callback input is not JSON: %v: %s", err, raw)
	}
	if input.Kind != notify.KindCommentPending {
		t.Errorf("callback kind = %q, want %q", input.Kind, notify.KindCommentPending)
	}
	if input.Category != notify.CategoryComments {
		t.Errorf("callback category = %q, want %q", input.Category, notify.CategoryComments)
	}
	if input.Params["post"] != "x" {
		t.Errorf("callback params.post = %v, want x", input.Params["post"])
	}
	if !reflect.DeepEqual(input.Recipients.Roles, []string{"admin"}) {
		t.Errorf("callback recipients.roles = %v, want [admin]", input.Recipients.Roles)
	}
	if input.Recipients.UserIDs == nil || len(input.Recipients.UserIDs) != 0 {
		t.Errorf("callback recipients.user_ids = %#v, want an empty array", input.Recipients.UserIDs)
	}
	if input.Recipients.Emails == nil || len(input.Recipients.Emails) != 0 {
		t.Errorf("callback recipients.emails = %#v, want an empty array", input.Recipients.Emails)
	}
	at, err := time.Parse(time.RFC3339, input.At)
	if err != nil {
		t.Fatalf("callback at = %q is not RFC 3339: %v", input.At, err)
	}
	if at.Before(before.Add(-time.Second)) || at.After(time.Now().UTC().Add(time.Minute)) {
		t.Errorf("callback at = %s, want the attempt time between %s and now", at, before)
	}
}

func TestPluginExtensionsNotifyWithoutEventsPermissionFailsAndRetries(t *testing.T) {
	h := newPluginNotifyHarness(t)
	h.user("admin", "notify-noperm-admin")
	emitCommentPending(t, h, "x")
	rows := pluginNotifyRows(t, h.db, 0)
	okRow := pluginNotifyRowFor(t, rows, pluginNotifyOK)
	noPermRow := pluginNotifyRowFor(t, rows, pluginNotifyNoPerm)

	// Positive control: the approved notifier of the same event is delivered.
	if err := notify.ProcessDelivery(context.Background(), h.db, okRow.ID, sendSystemNotification); err != nil {
		t.Fatalf("ProcessDelivery(%s) = %v, want nil", pluginNotifyOK, err)
	}

	if err := notify.ProcessDelivery(context.Background(), h.db, noPermRow.ID, sendSystemNotification); err == nil {
		t.Errorf("ProcessDelivery(%s) = nil, want an error (no events permission)", pluginNotifyNoPerm)
	}
	got := pluginNotifyRowFor(t, pluginNotifyRows(t, h.db, 0), pluginNotifyNoPerm)
	if got.Status != "pending" || got.Attempts != 1 {
		t.Errorf("%s row = status %q attempts %d, want pending/1 (retry later)", pluginNotifyNoPerm, got.Status, got.Attempts)
	}

	if err := plugin.FlushLogs(context.Background()); err != nil {
		t.Fatalf("flush plugin logs: %v", err)
	}
	if err := flushPluginLogForwarder(context.Background()); err != nil {
		t.Fatalf("flush plugin log forwarder: %v", err)
	}
	if n := notifyServerCount(t, h, "SELECT count(*) FROM system_logs WHERE plugin_id = ? AND level = ?", pluginNotifyNoPerm, 3); n < 1 {
		t.Errorf("system_logs error rows for %s = %d, want at least 1", pluginNotifyNoPerm, n)
	}
}

func TestPluginExtensionsNotifyThrowingCallbackFailsAndRetries(t *testing.T) {
	h := newPluginNotifyHarness(t)
	h.user("admin", "notify-throw-admin")
	emitCommentPending(t, h, "x")
	rows := pluginNotifyRows(t, h.db, 0)
	okRow := pluginNotifyRowFor(t, rows, pluginNotifyOK)
	throwRow := pluginNotifyRowFor(t, rows, pluginNotifyThrow)

	// Positive control: the approved notifier of the same event is delivered.
	if err := notify.ProcessDelivery(context.Background(), h.db, okRow.ID, sendSystemNotification); err != nil {
		t.Fatalf("ProcessDelivery(%s) = %v, want nil", pluginNotifyOK, err)
	}

	if err := notify.ProcessDelivery(context.Background(), h.db, throwRow.ID, sendSystemNotification); err == nil {
		t.Errorf("ProcessDelivery(%s) = nil, want an error (callback threw)", pluginNotifyThrow)
	}
	got := pluginNotifyRowFor(t, pluginNotifyRows(t, h.db, 0), pluginNotifyThrow)
	if got.Status != "pending" || got.Attempts != 1 {
		t.Errorf("%s row = status %q attempts %d, want pending/1 (retry later)", pluginNotifyThrow, got.Status, got.Attempts)
	}
}

func TestPluginExtensionsNotifyDisabledNotifierIsNotQueued(t *testing.T) {
	h := newPluginNotifyHarness(t)
	admin := h.user("admin", "notify-disable-admin")
	token := h.token(admin)

	emitCommentPending(t, h, "x")
	first := pluginNotifyTargets(pluginNotifyRows(t, h.db, 0))
	if !reflect.DeepEqual(first, []string{pluginNotifyNoPerm, pluginNotifyOK, pluginNotifyThrow}) {
		t.Fatalf("first emit targets = %v, want all three live notifiers", first)
	}
	var maxID uint
	for _, row := range pluginNotifyRows(t, h.db, 0) {
		if row.ID > maxID {
			maxID = row.ID
		}
	}

	if r := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+pluginNotifyThrow+"/disable", "", token); r.code != http.StatusOK {
		t.Fatalf("disable %s = %d: %s", pluginNotifyThrow, r.code, r.body)
	}

	emitCommentPending(t, h, "y")
	added := pluginNotifyTargets(pluginNotifyRows(t, h.db, maxID))
	want := []string{pluginNotifyNoPerm, pluginNotifyOK}
	if !reflect.DeepEqual(added, want) {
		t.Errorf("rows queued after disabling %s = %v, want %v", pluginNotifyThrow, added, want)
	}
	// Positive control: the enabled notifiers keep receiving the new event.
	if n := notifyServerCount(t, h, "SELECT count(*) FROM notification_deliveries WHERE channel = 'plugin' AND to_address = ? AND id > ?", pluginNotifyOK, maxID); n != 1 {
		t.Errorf("new %s rows after disable = %d, want 1", pluginNotifyOK, n)
	}
}

// notifyServerCount runs a count query and fails the test on error.
func notifyServerCount(t *testing.T, h *pluginEventsHarness, query string, args ...any) int64 {
	t.Helper()
	var count int64
	if err := h.db.Raw(query, args...).Scan(&count).Error; err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return count
}
