package core

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

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

// TransitionError carries allowed transitions
type TransitionError struct {
	Allowed []string
	Err     error
}

func (e *TransitionError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return "invalid order transition"
}

func (e *TransitionError) Unwrap() error { return e.Err }

// Transition moves order orderID from status from to status to inside tx.
// The update is compare-and-set on from (a concurrent change fails), and the
// move is recorded in order_events with actor (nil for system actions) and
// note. An illegal move returns an error that carries AllowedTransitions.
func (s *OrderService) Transition(tx *gorm.DB, orderID uint, from, to string, actor *uint, note string) error {
	if tx == nil {
		return errors.New("nil tx")
	}
	// Check if transition is allowed
	allowed := AllowedTransitions(from)
	valid := false
	for _, a := range allowed {
		if a == to {
			valid = true
			break
		}
	}
	if !valid {
		return &TransitionError{Allowed: allowed, Err: fmt.Errorf("invalid transition from %s to %s", from, to)}
	}

	now := time.Now()
	updates := map[string]interface{}{
		"status":     to,
		"updated_at": now,
	}
	switch to {
	case OrderStatusPaid:
		updates["paid_at"] = now
	case OrderStatusShipped:
		updates["shipped_at"] = now
	case OrderStatusCompleted:
		updates["completed_at"] = now
	case OrderStatusCancelled:
		updates["cancelled_at"] = now
	}

	// Compare-and-set: update only if current status still matches from.
	res := tx.Model(&Order{}).Where("id = ? AND status = ?", orderID, from).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		var current Order
		if err := tx.Select("id", "status").First(&current, orderID).Error; err != nil {
			return err
		}
		return &TransitionError{
			Allowed: AllowedTransitions(current.Status),
			Err:     fmt.Errorf("transition from %s to %s lost compare-and-set; current status is %s", from, to, current.Status),
		}
	}

	ev := OrderEvent{
		OrderID:     orderID,
		At:          now,
		ActorUserID: actor,
		Kind:        "status_change",
		FromStatus:  from,
		ToStatus:    to,
		Message:     note,
	}
	if err := tx.Create(&ev).Error; err != nil {
		return err
	}
	return nil
}
