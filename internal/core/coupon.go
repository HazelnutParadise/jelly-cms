package core

import "gorm.io/gorm"

// Discount is the result of applying one coupon to a cart.
type Discount struct {
	// Amount is the money taken off the subtotal, in minor units.
	Amount int64 `json:"amount"`
	// FreeShipping is true when the coupon waives the shipping fee.
	FreeShipping bool `json:"free_shipping"`
	// FreeUnits is the number of items given free by a bogo coupon.
	FreeUnits int `json:"free_units"`
	// Label is the text shown in the cart, e.g. "買 2 送 1".
	Label string `json:"label"`
}

// PricedLine is one cart line after the server has priced it.
type PricedLine struct {
	VariantID   uint   `json:"variant_id"`
	ProductID   uint   `json:"product_id"`
	CategoryIDs []uint `json:"category_ids"`
	Qty         int    `json:"qty"`
	UnitPrice   int64  `json:"unit_price"`
	LineTotal   int64  `json:"line_total"`
}

// CartQuote is the input of Evaluate: the priced cart plus the values a
// coupon rule can depend on.
type CartQuote struct {
	Lines       []PricedLine `json:"lines"`
	Subtotal    int64        `json:"subtotal"`
	ShippingFee int64        `json:"shipping_fee"`
	Email       string       `json:"email"`
}

// Evaluate applies coupon to the cart for user (nil for guests). It checks the
// window, minimum subtotal, scope, members-only and the per-user and total
// limits, then returns the discount. The discount never makes the total
// negative and never exceeds the subtotal. An invalid coupon returns an error
// that the API maps to 422.
//
// Skeleton: not implemented yet (ticket 07, E4).
func Evaluate(cart CartQuote, coupon Coupon, user *User) (Discount, error) {
	return Discount{}, ErrNotImplemented
}

// Redeem records that orderID used couponID inside tx. It increments
// used_count atomically and fails when the usage limit is already reached.
// email is stored lowercased and is the key for per-user limits.
//
// Skeleton: not implemented yet (ticket 07, E4).
func Redeem(tx *gorm.DB, couponID, orderID uint, userID *uint, email string) error {
	return ErrNotImplemented
}

// ReleaseRedemption undoes the redemption of orderID (the order expired or was
// cancelled before payment): it sets released_at and decrements used_count.
// Skeleton.
func ReleaseRedemption(tx *gorm.DB, orderID uint) error {
	return ErrNotImplemented
}
