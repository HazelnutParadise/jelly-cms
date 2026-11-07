package notify

// Plugin notifiers (docs/tickets/29-plugin-read-api-and-extensions.md, PR B).
// The server registers a function that lists the active notify.channel plugin
// providers; Emit then queues one delivery row per provider so the plugin
// receives the event through the ordinary retrying outbox. The rows carry no
// notification_id (the plugin channel is event-level, not per recipient).

import (
	"context"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
)

// ChannelPlugin is the delivery channel of plugin notifier rows. ToAddress is
// the plugin id.
const ChannelPlugin = "plugin"

// PluginChannelFunc lists the plugin ids that should receive every event.
// It is called once per Emit; nil or an empty list queues nothing.
type PluginChannelFunc func(ctx context.Context) []string

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

func pluginChannelProviders(ctx context.Context) []string {
	fn := pluginChannel.Load()
	if fn == nil {
		return nil
	}
	return (*fn)(ctx)
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
	// TODO(B3): implement.
	_, _, _, _, _, _ = ctx, db, spec, ev, providers, now
	return nil
}
