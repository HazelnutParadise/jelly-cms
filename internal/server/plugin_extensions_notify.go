package server

// notify.channel (docs/tickets/29-plugin-read-api-and-extensions.md, PR B):
// plugin notifiers. notify.Emit queues one notification_deliveries row per
// active provider (channel "plugin", to_address = plugin id) and the
// notifications.deliver job hands each row to deliverPluginNotification
// through sendSystemNotification. Retries, attempts and pruning are the
// existing delivery machinery.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/ext"
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
	if data.DB == nil {
		return nil
	}
	return extensionActiveProviders(ctx, data.DB, ext.PointNotifyChannel)
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
	db := data.DB
	if db == nil {
		pluginExtensionLog(d.Target, "error", fmt.Sprintf("notify.channel delivery of %s not sent: database is not initialized", d.Kind))
		return fmt.Errorf("%w: database is not initialized", errPluginNotifierUnavailable)
	}
	runtime, meta, err := extensionPluginMeta(ctx, db, d.Target, ext.PointNotifyChannel)
	if err != nil {
		pluginExtensionLog(d.Target, "error", fmt.Sprintf("notify.channel delivery of %s not sent: provider is not live: %v", d.Kind, err))
		return fmt.Errorf("%w: %v", errPluginNotifierUnavailable, err)
	}
	if err := requirePluginHostPermission(ctx, db, meta, "events"); err != nil {
		pluginExtensionLog(d.Target, "error", fmt.Sprintf("notify.channel delivery of %s not sent: events permission is required: %v", d.Kind, err))
		return fmt.Errorf("notify.channel delivery of %s needs the events permission: %w", d.Kind, err)
	}
	category, _ := d.Params["category"].(string)
	params, ok := d.Params["params"].(map[string]any)
	if !ok {
		params = map[string]any{}
	}
	recipients, _ := d.Params["recipients"].(map[string]any)
	input, err := json.Marshal(map[string]any{
		"kind":     d.Kind,
		"category": category,
		"params":   params,
		"recipients": map[string]any{
			"roles":    jsonArrayOrEmpty(recipients["roles"]),
			"user_ids": jsonArrayOrEmpty(recipients["user_ids"]),
			"emails":   jsonArrayOrEmpty(recipients["emails"]),
		},
		"at": time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		pluginExtensionLog(d.Target, "error", fmt.Sprintf("notify.channel delivery of %s not sent: encode input: %v", d.Kind, err))
		return fmt.Errorf("notify.channel delivery of %s: encode input: %w", d.Kind, err)
	}
	if _, err := runtime.CallExtension(ctx, meta.ID, ext.PointNotifyChannel, input); err != nil {
		pluginExtensionLog(d.Target, "error", fmt.Sprintf("notify.channel callback failed for %s: %v", d.Kind, err))
		return fmt.Errorf("notify.channel callback failed for %s: %w", d.Kind, err)
	}
	return nil
}

// jsonArrayOrEmpty returns v when it is a decoded JSON array, otherwise an empty
// array, so the callback never sees null.
func jsonArrayOrEmpty(v any) []any {
	if arr, ok := v.([]any); ok && arr != nil {
		return arr
	}
	return []any{}
}
