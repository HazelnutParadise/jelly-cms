package server

// notify.channel (docs/tickets/29-plugin-read-api-and-extensions.md, PR B):
// plugin notifiers. notify.Emit queues one notification_deliveries row per
// active provider (channel "plugin", to_address = plugin id) and the
// notifications.deliver job hands each row to deliverPluginNotification
// through sendSystemNotification. Retries, attempts and pruning are the
// existing delivery machinery.

import (
	"context"
	"errors"

	"github.com/HazelnutParadise/jelly-cms/internal/notify"
)

// errPluginNotifierUnavailable reports that the plugin a delivery targets has
// no live notify.channel callback (not loaded, disabled, stale approval).
var errPluginNotifierUnavailable = errors.New("plugin notifier is unavailable")

// notifyPluginChannelProviders is installed as notify.SetPluginChannel by
// ConfigurePluginHost. It lists the live notify.channel providers
// (extensionActiveProviders on data.DB) so Emit can queue one delivery row
// per provider; nil when there is no runtime or database.
func notifyPluginChannelProviders(ctx context.Context) []string {
	// TODO(B3): return extensionActiveProviders(ctx, data.DB, ext.PointNotifyChannel).
	return nil
}

// deliverPluginNotification sends one queued plugin delivery (d.Channel is
// "plugin", d.Target the plugin id) to that plugin's notify.channel callback.
// A returned error makes notify.ProcessDelivery count an attempt and retry.
//
// Wanted results:
//   - The provider must be live right now (extensionPluginMeta for
//     ext.PointNotifyChannel on data.DB) and must hold the events permission
//     (requirePluginHostPermission(ctx, db, meta, "events")): the row carries
//     recipient emails. Otherwise return an error naming the reason; the
//     delivery retries like any other failure.
//   - The callback input is {"kind": d.Kind, "category": …, "params": …,
//     "recipients": {"roles": […], "user_ids": […], "emails": […]},
//     "at": RFC 3339 UTC time of this attempt}, where category, params and
//     recipients come from d.Params as notify stored them (arrays never null).
//   - Any returned JSON value counts as success. An error, a timeout or an
//     unavailable provider is logged with pluginExtensionLog(id, "error", …)
//     and returned.
func deliverPluginNotification(ctx context.Context, d notify.Delivery) error {
	// TODO(B3): implement.
	_ = ctx
	_ = d
	return errPluginNotifierUnavailable
}
