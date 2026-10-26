package core

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ExpireUnpaidOrders expires orders that are still pending and past their hold
// time. Each order transition and its stock/coupon release commit atomically.
func ExpireUnpaidOrders(db *gorm.DB, now time.Time) (int, error) {
	if db == nil {
		return 0, nil
	}
	var orderIDs []uint
	if err := db.Model(&Order{}).
		Where("status = ? AND expires_at IS NOT NULL AND expires_at < ?", OrderStatusPending, now).
		Order("id").Pluck("id", &orderIDs).Error; err != nil {
		return 0, err
	}

	svc := NewOrderService(db)
	expired := 0
	for _, orderID := range orderIDs {
		transitioned := false
		err := db.Transaction(func(tx *gorm.DB) error {
			var order Order
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, orderID).Error; err != nil {
				return err
			}
			if order.Status != OrderStatusPending || order.ExpiresAt == nil || !order.ExpiresAt.Before(now) {
				return nil
			}
			if err := svc.Transition(tx, orderID, OrderStatusPending, OrderStatusExpired, nil, "expired"); err != nil {
				return err
			}
			if err := Release(tx, orderID); err != nil {
				return err
			}
			if err := ReleaseRedemption(tx, orderID); err != nil {
				return err
			}
			transitioned = true
			return nil
		})
		if err != nil {
			return expired, err
		}
		if transitioned {
			expired++
		}
	}
	return expired, nil
}
