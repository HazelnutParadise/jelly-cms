// Package notify is a minimal stub of the notification package that ticket 06
// creates (internal/notify on the accounts branch). It is shared with ticket
// 06: when 06 lands, replace this file with 06's package and keep the names
// below, which ticket 07 uses.
package notify

import (
	"context"
	"errors"
)

// ErrNotImplemented is returned by the stub Emit.
var ErrNotImplemented = errors.New("notify: not implemented")

// Shop notification kinds (ticket 07). Staff kinds reuse the preference of the
// existing row: order.refunded follows the order.payment_failed preference.
// Customer kinds ignore preferences.
const (
	KindOrderNew                 = "order.new"
	KindOrderPaymentFailed       = "order.payment_failed"
	KindOrderRefunded            = "order.refunded"
	KindOrderConfirmation        = "order.confirmation"
	KindOrderShipped             = "order.shipped"
	KindOrderLookupLink          = "order.lookup_link"
	KindOrderOfflineInstructions = "order.offline_instructions"
	KindCustomerSetPassword      = "customer.set_password"
)

// Recipients names who receives an event. Emails carries guest addresses that
// have no account (added by ticket 07).
type Recipients struct {
	Roles   []string `json:"roles,omitempty"`
	UserIDs []uint   `json:"user_ids,omitempty"`
	Emails  []string `json:"emails,omitempty"`
}

// Event is one notification to emit.
type Event struct {
	Kind       string         `json:"kind"`
	Params     map[string]any `json:"params,omitempty"`
	GroupKey   string         `json:"group_key,omitempty"`
	Recipients Recipients     `json:"recipients"`
}

// Delivery is one email or webhook to send from the outbox.
type Delivery struct {
	Kind    string `json:"kind"`
	Channel string `json:"channel"` // email, webhook
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// Sender sends one delivery. The default implementation only records it and
// marks it held (ticket 06).
type Sender interface {
	Send(d Delivery) error
}

// Emit resolves recipients, applies preferences and writes the outbox rows.
//
// Skeleton: returns ErrNotImplemented until ticket 06 lands.
func Emit(ctx context.Context, ev Event) error {
	return ErrNotImplemented
}
