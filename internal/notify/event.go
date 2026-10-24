// Package notify turns domain events into notification rows, honours each
// user's preferences and queues Email and Webhook deliveries
// (docs/tickets/06-accounts.md, 通知).
package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Recipients selects who receives an event. Roles and UserIDs are combined.
type Recipients struct {
	// Roles lists role names; every active user holding one of them is a recipient.
	Roles []string
	// UserIDs lists explicit recipients.
	UserIDs []uint
}

// Event is one thing that happened and should reach someone's notification center.
type Event struct {
	// Kind is a registered kind such as "comment.pending".
	Kind string
	// Category is one of comments, orders, system, security. It must match the kind's category.
	Category string
	// Params fill the localised message; they are stored as JSON.
	Params map[string]any
	// GroupKey merges unread rows: a second event with the same key adds to Count.
	GroupKey string
	// Recipients selects who receives the event.
	Recipients Recipients
}

// KindSpec describes one registered kind.
type KindSpec struct {
	// Kind is the event name.
	Kind string
	// Category is the notification category the kind is filed under.
	Category string
	// Preference is the notification_prefs event key that governs channels.
	// Empty means the kind has no per-user preference and is in-app only.
	Preference string
}

// Categories are the four notification tabs.
const (
	CategoryComments = "comments"
	CategoryOrders   = "orders"
	CategorySystem   = "system"
	CategorySecurity = "security"
)

// Registered kinds. The first eight are the preference events shown in the
// notification settings; user.invite_accepted has no preference (in-app only).
const (
	KindCommentPending          = "comment.pending"
	KindCommentReply            = "comment.reply"
	KindOrderNew                = "order.new"
	KindOrderPaymentFailed      = "order.payment_failed"
	KindSecurityNewDevice       = "security.new_device"
	KindSecurityPasswordChanged = "security.password_changed"
	KindSystemUpdateAvailable   = "system.update_available"
	KindSystemPluginFailed      = "system.plugin_failed"
	KindUserInviteAccepted      = "user.invite_accepted"
)

// kinds is the registry of accepted event kinds.
var kinds = map[string]KindSpec{
	KindCommentPending:          {Kind: KindCommentPending, Category: CategoryComments, Preference: KindCommentPending},
	KindCommentReply:            {Kind: KindCommentReply, Category: CategoryComments, Preference: KindCommentReply},
	KindOrderNew:                {Kind: KindOrderNew, Category: CategoryOrders, Preference: KindOrderNew},
	KindOrderPaymentFailed:      {Kind: KindOrderPaymentFailed, Category: CategoryOrders, Preference: KindOrderPaymentFailed},
	KindSecurityNewDevice:       {Kind: KindSecurityNewDevice, Category: CategorySecurity, Preference: KindSecurityNewDevice},
	KindSecurityPasswordChanged: {Kind: KindSecurityPasswordChanged, Category: CategorySecurity, Preference: KindSecurityPasswordChanged},
	KindSystemUpdateAvailable:   {Kind: KindSystemUpdateAvailable, Category: CategorySystem, Preference: KindSystemUpdateAvailable},
	KindSystemPluginFailed:      {Kind: KindSystemPluginFailed, Category: CategorySystem, Preference: KindSystemPluginFailed},
	KindUserInviteAccepted:      {Kind: KindUserInviteAccepted, Category: CategorySystem, Preference: ""},
}

// LookupKind returns the registry entry for kind.
func LookupKind(kind string) (KindSpec, bool) {
	spec, ok := kinds[kind]
	return spec, ok
}

// PreferenceEvents lists the event keys shown in notification settings, in display order.
func PreferenceEvents() []string {
	return []string{
		KindCommentPending,
		KindCommentReply,
		KindOrderNew,
		KindOrderPaymentFailed,
		KindSecurityNewDevice,
		KindSecurityPasswordChanged,
		KindSystemUpdateAvailable,
		KindSystemPluginFailed,
	}
}

// Emit resolves the recipients of ev, applies their preferences, stores in-app
// rows (merging unread rows that share GroupKey) and queues Email and Webhook
// deliveries. Each recipient is handled in a transaction so group coalescing
// and delivery rows stay consistent.
func Emit(ctx context.Context, db *gorm.DB, ev Event) error {
	if db == nil {
		return fmt.Errorf("notify: database is not initialized")
	}
	spec, ok := LookupKind(ev.Kind)
	if !ok {
		return fmt.Errorf("notify: unknown event kind %q", ev.Kind)
	}
	if ev.Category != "" && ev.Category != spec.Category {
		return fmt.Errorf("notify: kind %q belongs to category %q", ev.Kind, spec.Category)
	}
	if len(ev.GroupKey) > 255 {
		return fmt.Errorf("notify: group key exceeds 255 characters")
	}

	params, err := json.Marshal(ev.Params)
	if err != nil {
		return fmt.Errorf("notify: encode event parameters: %w", err)
	}
	if ev.Params == nil {
		params = []byte(`{}`)
	}
	actions, err := json.Marshal(actionsFor(ev.Kind))
	if err != nil {
		return fmt.Errorf("notify: encode actions: %w", err)
	}

	users, err := resolveRecipients(ctx, db, ev.Recipients)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, candidate := range users {
		if err := emitForRecipient(ctx, db, candidate.ID, ev.Recipients, spec, ev, params, actions, now); err != nil {
			return err
		}
	}
	return nil
}

func resolveRecipients(ctx context.Context, db *gorm.DB, recipients Recipients) ([]core.User, error) {
	query := db.WithContext(ctx).Model(&core.User{}).Where("status = ?", "active")
	roles := uniqueStrings(recipients.Roles)
	ids := uniqueIDs(recipients.UserIDs)
	switch {
	case len(roles) > 0 && len(ids) > 0:
		query = query.Where("role IN ? OR id IN ?", roles, ids)
	case len(roles) > 0:
		query = query.Where("role IN ?", roles)
	case len(ids) > 0:
		query = query.Where("id IN ?", ids)
	default:
		return nil, nil
	}
	var users []core.User
	if err := query.Order("id").Find(&users).Error; err != nil {
		return nil, fmt.Errorf("notify: resolve recipients: %w", err)
	}
	return users, nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func uniqueIDs(values []uint) []uint {
	seen := make(map[uint]struct{}, len(values))
	result := make([]uint, 0, len(values))
	for _, value := range values {
		if value == 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

type channelPreferences struct {
	inApp   bool
	email   bool
	webhook bool
}

func emitForRecipient(ctx context.Context, db *gorm.DB, userID uint, recipients Recipients, spec KindSpec, ev Event, params, actions []byte, now time.Time) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Locking the recipient row serializes even the first emission, when no
		// group row exists yet and there is nothing else to lock.
		var user core.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userID).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return nil
			}
			return fmt.Errorf("notify: lock recipient %d: %w", userID, err)
		}
		if user.Status != "active" {
			return nil
		}
		if !stillRecipient(user, recipients) {
			return nil
		}

		prefs := channelPreferences{inApp: true}
		if spec.Preference != "" {
			var pref core.NotificationPref
			err := tx.Where("user_id = ? AND event = ?", userID, spec.Preference).First(&pref).Error
			if err != nil && err != gorm.ErrRecordNotFound {
				return fmt.Errorf("notify: load preferences for user %d: %w", userID, err)
			}
			if err == nil {
				prefs = channelPreferences{inApp: pref.InApp, email: pref.Email, webhook: pref.Webhook}
			}
		}
		if !prefs.inApp && !prefs.email && !prefs.webhook {
			return nil
		}

		var settings core.NotificationSettings
		if prefs.email || prefs.webhook {
			err := tx.Where("user_id = ?", userID).First(&settings).Error
			if err != nil && err != gorm.ErrRecordNotFound {
				return fmt.Errorf("notify: load settings for user %d: %w", userID, err)
			}
		}
		held := quietNow(now, settings)

		if ev.GroupKey != "" {
			// Expired unread rows still occupy idx_notifications_unread_group.
			// Detach them before searching for a live coalescing target while
			// preserving their unread state and historical timestamps.
			if err := tx.Model(&core.Notification{}).
				Where("user_id = ? AND group_key = ? AND read_at IS NULL AND expires_at <= ?", userID, ev.GroupKey, now).
				Update("group_key", "").Error; err != nil {
				return fmt.Errorf("notify: detach expired group rows for user %d: %w", userID, err)
			}
			var existing core.Notification
			err := tx.Where("user_id = ? AND group_key = ? AND read_at IS NULL AND expires_at > ?", userID, ev.GroupKey, now).
				Clauses(clause.Locking{Strength: "UPDATE"}).First(&existing).Error
			if err != nil && err != gorm.ErrRecordNotFound {
				return fmt.Errorf("notify: find group row for user %d: %w", userID, err)
			}
			if err == nil && existing.InAppVisible == prefs.inApp {
				if err := tx.Model(&core.Notification{}).Where("id = ?", existing.ID).Update("count", gorm.Expr("count + 1")).Error; err != nil {
					return fmt.Errorf("notify: merge event for user %d: %w", userID, err)
				}
				return createDeliveries(tx, user, existing.ID, prefs, settings, held, now)
			}
			if err == nil {
				// A group cannot mix in-app and email-only events: that would
				// either reveal an event created while hidden or hide one created
				// while visible. Detach the prior row before inserting the new
				// visibility generation, preserving its read/unread state.
				if err := tx.Model(&core.Notification{}).Where("id = ? AND read_at IS NULL", existing.ID).Update("group_key", "").Error; err != nil {
					return fmt.Errorf("notify: detach visibility-changed group for user %d: %w", userID, err)
				}
			}
		}

		notification := core.Notification{
			UserID: userID, Kind: spec.Kind, Category: spec.Category,
			TitleKey: "notify." + spec.Kind, Params: params, Actions: actions,
			GroupKey: ev.GroupKey, Count: 1, InAppVisible: prefs.inApp,
			CreatedAt: now, ExpiresAt: now.Add(90 * 24 * time.Hour),
		}
		if err := tx.Select("UserID", "Kind", "Category", "TitleKey", "Params", "Actions", "GroupKey", "Count", "InAppVisible", "CreatedAt", "ExpiresAt").Create(&notification).Error; err != nil {
			return fmt.Errorf("notify: create event for user %d: %w", userID, err)
		}
		// GORM expands a zero-value bool with a default tag to that default
		// during Create. Preserve the database's default-true behavior for old
		// callers while explicitly persisting false for email-only events.
		if !prefs.inApp {
			if err := tx.Model(&core.Notification{}).Where("id = ?", notification.ID).Update("in_app_visible", false).Error; err != nil {
				return fmt.Errorf("notify: hide email-only notification for user %d: %w", userID, err)
			}
		}
		return createDeliveries(tx, user, notification.ID, prefs, settings, held, now)
	})
}

func stillRecipient(user core.User, recipients Recipients) bool {
	for _, id := range recipients.UserIDs {
		if id == user.ID {
			return true
		}
	}
	for _, role := range recipients.Roles {
		if role != "" && role == user.Role {
			return true
		}
	}
	return false
}

func createDeliveries(tx *gorm.DB, user core.User, notificationID uint, prefs channelPreferences, settings core.NotificationSettings, held bool, now time.Time) error {
	status := "pending"
	if held {
		status = "held"
	}
	rows := make([]core.NotificationDelivery, 0, 2)
	if prefs.email {
		rows = append(rows, core.NotificationDelivery{NotificationID: notificationID, Channel: "email", Status: status, NextAttemptAt: now})
	}
	if prefs.webhook {
		rows = append(rows, core.NotificationDelivery{NotificationID: notificationID, Channel: "webhook", Status: status, NextAttemptAt: now})
	}
	if len(rows) == 0 {
		return nil
	}
	if err := tx.Create(&rows).Error; err != nil {
		return fmt.Errorf("notify: queue delivery for user %d: %w", user.ID, err)
	}
	return nil
}

type action struct {
	LabelKey string `json:"label_key"`
	URL      string `json:"url"`
}

func actionsFor(kind string) []action {
	label, target := "notifications.open", "/admin/notifications"
	switch kind {
	case KindCommentPending:
		label, target = "comments.review", "/admin/comments?status=pending"
	case KindCommentReply:
		label, target = "comments.open", "/admin/comments"
	case KindOrderNew, KindOrderPaymentFailed:
		label, target = "orders.open", "/admin/orders"
	case KindSecurityNewDevice, KindSecurityPasswordChanged:
		label, target = "account.security", "/admin/account?tab=security"
	case KindSystemPluginFailed:
		label, target = "plugins.open", "/admin/plugins"
	case KindUserInviteAccepted:
		label, target = "users.open", "/admin/users"
	}
	return []action{{LabelKey: label, URL: target}}
}

func quietNow(now time.Time, settings core.NotificationSettings) bool {
	if settings.QuietStart == "" || settings.QuietEnd == "" || settings.QuietTZ == "" {
		return false
	}
	location, err := time.LoadLocation(settings.QuietTZ)
	if err != nil {
		return false
	}
	start, err := time.Parse("15:04", settings.QuietStart)
	if err != nil {
		return false
	}
	end, err := time.Parse("15:04", settings.QuietEnd)
	if err != nil {
		return false
	}
	local := now.In(location)
	minute := local.Hour()*60 + local.Minute()
	startMinute := start.Hour()*60 + start.Minute()
	endMinute := end.Hour()*60 + end.Minute()
	if startMinute == endMinute {
		return true
	}
	if startMinute < endMinute {
		return minute >= startMinute && minute < endMinute
	}
	return minute >= startMinute || minute < endMinute
}

// Delivery is one queued send handed to a Sender.
type Delivery struct {
	// ID is the notification_deliveries row.
	ID uint
	// NotificationID is the notification being delivered.
	NotificationID uint
	// Channel is email or webhook.
	Channel string
	// Kind is the notification kind.
	Kind string
	// Params are the notification parameters.
	Params map[string]any
	// Target is the recipient address for email or the endpoint URL for webhook.
	Target string
}

// Sender delivers one queued message. The default LogSender only records it.
type Sender interface {
	Send(Delivery) error
}

// LogSender is the default Sender until mail and webhook delivery are configured
// (ticket 08). It records only delivery metadata and never sends externally.
type LogSender struct{}

// Send implements Sender without delivering mail or making a webhook request.
func (LogSender) Send(d Delivery) error {
	log.Printf("notification delivery queued: id=%d notification=%d channel=%s kind=%s", d.ID, d.NotificationID, d.Channel, d.Kind)
	return nil
}

// UnreadCount returns the number of unread, unexpired notifications for userID.
func UnreadCount(db *gorm.DB, userID uint) int {
	if db == nil || userID == 0 {
		return 0
	}
	var count int64
	if err := VisibleForUser(db, userID, time.Now().UTC()).
		Where("read_at IS NULL").
		Count(&count).Error; err != nil {
		return 0
	}
	return int(count)
}

// VisibleForUser returns the in-app notification rows that belong in one
// user's inbox at now. Callers add unread, category, or pagination filters.
func VisibleForUser(db *gorm.DB, userID uint, now time.Time) *gorm.DB {
	return db.Model(&core.Notification{}).
		Where("user_id = ? AND in_app_visible = TRUE AND expires_at > ?", userID, now)
}
