// Package notify turns domain events into notification rows, honours each
// user's preferences and queues Email and Webhook deliveries
// (docs/tickets/06-accounts.md, 通知). The skeleton only declares the shapes;
// Emit and UnreadCount are stubs until the notification ticket is implemented.
package notify

import (
	"context"

	"gorm.io/gorm"
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
// deliveries. Stub: it does nothing yet and returns nil.
func Emit(ctx context.Context, db *gorm.DB, ev Event) error {
	_ = ctx
	_ = db
	_ = ev
	return nil
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
// (ticket 08). Stub: it reports success and records nothing yet.
type LogSender struct{}

// Send implements Sender. Stub: returns nil.
func (LogSender) Send(d Delivery) error {
	_ = d
	return nil
}

// UnreadCount returns the number of unread, unexpired notifications for userID.
// Stub: returns 0 until the notification ticket is implemented.
func UnreadCount(db *gorm.DB, userID uint) int {
	_ = db
	_ = userID
	return 0
}
