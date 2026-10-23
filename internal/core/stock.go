package core

import (
	"errors"

	"gorm.io/gorm"
)

// ErrInsufficientStock is returned when a reservation asks for more than the
// variant has. The API answers 409 with code "stock".
var ErrInsufficientStock = errors.New("insufficient stock")

// Reserve takes qty units of variantID for orderID inside tx. It writes a
// reserve row to stock_movements. The database check stock >= 0 is the last
// guard against overselling. Returns ErrInsufficientStock when stock is short.
//
// Skeleton: not implemented yet (ticket 07, E5).
func Reserve(tx *gorm.DB, variantID uint, qty int, orderID uint) error {
	return ErrNotImplemented
}

// Release gives back the reservations of orderID (reason release) when the
// order expires or is cancelled before shipping. Skeleton.
func Release(tx *gorm.DB, orderID uint) error {
	return ErrNotImplemented
}

// Restock returns the stock of orderID (reason restock) after a refund that
// asks for it. Skeleton.
func Restock(tx *gorm.DB, orderID uint) error {
	return ErrNotImplemented
}
