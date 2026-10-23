package core

import "gorm.io/gorm"

// Order statuses. Terminal statuses are refunded, cancelled and expired.
const (
	OrderStatusPending   = "pending"
	OrderStatusPaid      = "paid"
	OrderStatusShipped   = "shipped"
	OrderStatusCompleted = "completed"
	OrderStatusCancelled = "cancelled"
	OrderStatusExpired   = "expired"
	OrderStatusRefunded  = "refunded"
)

// orderTransitions is the state machine of ticket 07:
// pending -> paid | cancelled | expired; paid -> shipped | cancelled (with refund);
// shipped -> completed | refunded; completed -> refunded.
var orderTransitions = map[string][]string{
	OrderStatusPending:   {OrderStatusPaid, OrderStatusCancelled, OrderStatusExpired},
	OrderStatusPaid:      {OrderStatusShipped, OrderStatusCancelled},
	OrderStatusShipped:   {OrderStatusCompleted, OrderStatusRefunded},
	OrderStatusCompleted: {OrderStatusRefunded},
}

// AllowedTransitions lists the statuses an order in status may move to. It
// returns nil for terminal or unknown statuses.
func AllowedTransitions(status string) []string {
	next := orderTransitions[status]
	if len(next) == 0 {
		return nil
	}
	return append([]string(nil), next...)
}

// OrderService changes orders. Every status change goes through Transition.
type OrderService struct {
	db *gorm.DB
}

// NewOrderService returns an OrderService bound to db.
func NewOrderService(db *gorm.DB) *OrderService {
	return &OrderService{db: db}
}

// Transition moves order orderID from status from to status to inside tx.
// The update is compare-and-set on from (a concurrent change fails), and the
// move is recorded in order_events with actor (nil for system actions) and
// note. An illegal move returns an error that carries AllowedTransitions.
//
// Skeleton: not implemented yet (ticket 07, E5).
func (s *OrderService) Transition(tx *gorm.DB, orderID uint, from, to string, actor *uint, note string) error {
	return ErrNotImplemented
}
