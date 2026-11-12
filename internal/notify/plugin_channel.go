package notify

// Plugin notifiers (ticket 29, PR B).
// The server registers a function that lists the active notify.channel plugin
// providers; Emit then queues one delivery row per provider so the plugin
// receives the event through the ordinary retrying outbox. The rows carry no
// notification_id (the plugin channel is event-level, not per recipient).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"gorm.io/gorm"
)

// ChannelPlugin is the delivery channel of plugin notifier rows. ToAddress is
// the plugin id.
const ChannelPlugin = "plugin"

// PluginChannelFunc lists the plugin ids that should receive an event. It is
// called once per Emit with the database (or transaction) Emit runs on, so it
// never opens a second connection; nil or an empty list queues nothing.
type PluginChannelFunc func(ctx context.Context, db *gorm.DB) []string

var pluginChannel atomic.Pointer[PluginChannelFunc]

// SetPluginChannel installs fn (nil removes it). It is safe to call at any
// time; Emit reads the current value atomically.
func SetPluginChannel(fn PluginChannelFunc) {
	if fn == nil {
		pluginChannel.Store(nil)
		return
	}
	pluginChannel.Store(&fn)
}

func pluginChannelProviders(ctx context.Context, db *gorm.DB) []string {
	fn := pluginChannel.Load()
	if fn == nil {
		return nil
	}
	return (*fn)(ctx, db)
}

// PluginChannelCarries reports whether kind is sent to plugin notifiers. The
// required customer emails (order confirmation, shipping, lookup link,
// offline payment instructions, set-password invitation) carry access tokens
// and links meant for one customer only, so they never leave the core.
func PluginChannelCarries(kind string) bool {
	return !isRequiredEmailKind(kind)
}

// queuePluginDeliveries creates one pending notification_deliveries row per
// provider id for ev.
//
// Wanted results:
//   - Each row: channel ChannelPlugin, to_address = provider id, kind = ev.Kind,
//     notification_id NULL (create through a map like queueGuestEmails; a
//     struct zero value violates the foreign key), status pending, attempts 0,
//     next_attempt_at now, error "", params = {"category": spec.Category,
//     "params": ev.Params or {}, "recipients": {"roles": [...], "user_ids":
//     [...], "emails": [...]}} with every array present (empty, never null).
//   - All rows are created in one transaction on db; an error aborts Emit.
//   - Duplicate provider ids are queued once; empty ids are skipped.
func queuePluginDeliveries(ctx context.Context, db *gorm.DB, spec KindSpec, ev Event, providers []string, now time.Time) error {
	ids := uniqueStrings(providers)
	if len(ids) == 0 {
		return nil
	}
	params := ev.Params
	if params == nil {
		params = map[string]any{}
	}
	// uniqueStrings and uniqueIDs return non-nil slices, so the arrays encode as [] rather than null.
	payload, err := json.Marshal(map[string]any{
		"category": spec.Category,
		"params":   params,
		"recipients": map[string]any{
			"roles":    uniqueStrings(ev.Recipients.Roles),
			"user_ids": uniqueIDs(ev.Recipients.UserIDs),
			"emails":   pluginRecipientEmails(ev.Recipients.Emails),
		},
	})
	if err != nil {
		return fmt.Errorf("notify: encode plugin delivery parameters: %w", err)
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, id := range ids {
			if err := tx.Model(&core.NotificationDelivery{}).Create(map[string]interface{}{
				"notification_id": nil,
				"kind":            ev.Kind,
				"to_address":      id,
				"channel":         ChannelPlugin,
				"status":          "pending",
				"attempts":        0,
				"next_attempt_at": now,
				"error":           "",
				"params":          payload,
			}).Error; err != nil {
				return fmt.Errorf("notify: queue plugin delivery for %q: %w", id, err)
			}
		}
		return nil
	})
}

// pluginRecipientEmails normalises and de-duplicates guest addresses for a
// plugin delivery. An address that does not parse is kept as written.
func pluginRecipientEmails(emails []string) []string {
	seen := make(map[string]struct{}, len(emails))
	result := make([]string, 0, len(emails))
	for _, raw := range emails {
		addr, err := normalizeEmail(raw)
		if err != nil {
			addr = strings.TrimSpace(raw)
		}
		if addr == "" {
			continue
		}
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		result = append(result, addr)
	}
	return result
}
