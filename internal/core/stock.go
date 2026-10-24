package core

import (
	"errors"
	"fmt"
	"math"

	"gorm.io/gorm"
)

// ErrInsufficientStock is returned when a reservation asks for more than the
// variant has. The API answers 409 with code "stock".
var ErrInsufficientStock = errors.New("insufficient stock")

// lockOrder row-locks the order so stock changes of one order are serialised.
func lockOrder(tx *gorm.DB, orderID uint) error {
	var id uint
	if err := tx.Raw("SELECT id FROM orders WHERE id = ? FOR UPDATE", orderID).Row().Scan(&id); err != nil {
		return fmt.Errorf("lock order %d: %w", orderID, err)
	}
	if id == 0 {
		return fmt.Errorf("lock order %d: %w", orderID, gorm.ErrRecordNotFound)
	}
	return nil
}

// Reserve takes qty units of variantID for orderID inside tx. It writes a
// reserve row to stock_movements. The database check stock >= 0 is the last
// guard against overselling. Returns ErrInsufficientStock when stock is short.
func Reserve(tx *gorm.DB, variantID uint, qty int, orderID uint) error {
	if tx == nil {
		return errors.New("nil tx")
	}
	if qty <= 0 {
		return fmt.Errorf("reserve: qty must be positive, got %d", qty)
	}
	if int64(qty) > math.MaxInt32 {
		return fmt.Errorf("reserve: qty exceeds inventory range")
	}
	if err := lockOrder(tx, orderID); err != nil {
		return err
	}
	var previous int64
	err := tx.Raw(`SELECT COALESCE(SUM(-delta), 0) FROM stock_movements
		WHERE order_id = ? AND variant_id = ? AND reason = 'reserve'`, orderID, variantID).Row().Scan(&previous)
	if err != nil {
		return fmt.Errorf("reserve ledger variant %d: %w", variantID, err)
	}
	var hasPrior bool
	if err := tx.Raw("SELECT EXISTS (SELECT 1 FROM stock_movements WHERE order_id = ? AND variant_id = ? AND reason = 'reserve')", orderID, variantID).Row().Scan(&hasPrior); err != nil {
		return err
	}
	if hasPrior {
		if previous == int64(qty) {
			return nil
		}
		return fmt.Errorf("reserve: order %d already has a reservation for variant %d", orderID, variantID)
	}
	var stock int
	if err := tx.Raw("SELECT stock FROM product_variants WHERE id = ? FOR UPDATE", variantID).Row().Scan(&stock); err != nil {
		return fmt.Errorf("reserve variant %d: %w", variantID, err)
	}
	if stock < qty {
		return ErrInsufficientStock
	}
	res := tx.Exec("UPDATE product_variants SET stock = stock - ?, updated_at = now() WHERE id = ? AND stock = ?",
		qty, variantID, stock)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("reserve variant %d: stock changed while locked", variantID)
	}
	return tx.Create(&StockMovement{
		VariantID: variantID,
		OrderID:   &orderID,
		Delta:     -qty,
		Reason:    "reserve",
	}).Error
}

// outstandingReservations returns, per variant, the units still held by the
// order. Reserve rows add, release and restock rows subtract, so running a
// release twice never gives stock back twice.
func outstandingReservations(tx *gorm.DB, orderID uint) ([]struct {
	VariantID uint
	Qty       int64
}, error) {
	var rows []struct {
		VariantID uint
		Qty       int64
	}
	err := tx.Raw(`SELECT variant_id, SUM(-delta) AS qty
		FROM stock_movements
		WHERE order_id = ? AND reason IN ('reserve', 'release', 'restock')
		GROUP BY variant_id
		HAVING SUM(-delta) > 0
		ORDER BY variant_id`, orderID).Scan(&rows).Error
	return rows, err
}

// applyReturn gives the outstanding units of orderID back as reason inside tx.
// The order row is locked first, so two sweeps of the same order serialise and
// the ledger stays one-for-one.
func applyReturn(tx *gorm.DB, orderID uint, reason string) error {
	if tx == nil {
		return errors.New("nil tx")
	}
	if err := lockOrder(tx, orderID); err != nil {
		return err
	}
	rows, err := outstandingReservations(tx, orderID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Qty <= 0 {
			continue
		}
		if row.Qty > math.MaxInt32 {
			return fmt.Errorf("%s: outstanding quantity for variant %d exceeds inventory range", reason, row.VariantID)
		}
		var stock int64
		if err := tx.Raw("SELECT stock FROM product_variants WHERE id = ? FOR UPDATE", row.VariantID).Row().Scan(&stock); err != nil {
			return fmt.Errorf("%s variant %d: %w", reason, row.VariantID, err)
		}
		if stock > math.MaxInt32-row.Qty {
			return fmt.Errorf("%s: stock overflow for variant %d", reason, row.VariantID)
		}
		res := tx.Exec("UPDATE product_variants SET stock = stock + ?, updated_at = now() WHERE id = ?", row.Qty, row.VariantID)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("%s: variant %d not found", reason, row.VariantID)
		}
		if err := tx.Create(&StockMovement{
			VariantID: row.VariantID,
			OrderID:   &orderID,
			Delta:     int(row.Qty),
			Reason:    reason,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

// Release gives back the reservations of orderID (reason release) when the
// order expires or is cancelled before shipping.
func Release(tx *gorm.DB, orderID uint) error {
	return applyReturn(tx, orderID, "release")
}

// Restock returns the stock of orderID (reason restock) after a refund that
// asks for it.
func Restock(tx *gorm.DB, orderID uint) error {
	return applyReturn(tx, orderID, "restock")
}
