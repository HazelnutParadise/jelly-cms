package core

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

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

// Coupon errors. The API maps all of them to 422.
var (
	ErrCouponInvalid       = errors.New("coupon is not valid")
	ErrCouponNotApplicable = errors.New("coupon does not apply to this cart")
	ErrCouponUsageLimit    = errors.New("coupon usage limit reached")
	ErrCouponPerUserLimit  = errors.New("coupon per-user limit reached")
	ErrCouponBoundEmail    = errors.New("coupon is bound to another email")
	ErrCouponNotFound      = errors.New("coupon not found")
)

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// CouponTargets loads the category/product targets of one coupon. It needs the
// database handle of the caller; Evaluate never opens a connection itself.
func CouponTargets(tx *gorm.DB, couponID uint) ([]CouponTarget, error) {
	if tx == nil {
		return nil, errors.New("nil db")
	}
	var targets []CouponTarget
	err := tx.Where("coupon_id = ?", couponID).Order("id").Find(&targets).Error
	return targets, err
}

// CouponPerUserUsed counts the coupon's unreleased redemptions for one person.
// A member is matched by user ID or email; a guest only by lowercased email.
func CouponPerUserUsed(tx *gorm.DB, couponID uint, userID *uint, email string) (int64, error) {
	if tx == nil {
		return 0, errors.New("nil db")
	}
	q := tx.Model(&CouponRedemption{}).Where("coupon_id = ? AND released_at IS NULL", couponID)
	if userID != nil {
		q = q.Where("(user_id = ? OR lower(email) = ?)", *userID, normalizeEmail(email))
	} else {
		q = q.Where("lower(email) = ?", normalizeEmail(email))
	}
	var n int64
	err := q.Count(&n).Error
	return n, err
}

func couponInWindow(coupon Coupon, now time.Time) bool {
	if coupon.StartsAt != nil && now.Before(*coupon.StartsAt) {
		return false
	}
	if coupon.EndsAt != nil && !now.Before(*coupon.EndsAt) {
		return false
	}
	return true
}

func lineMatchesScope(coupon Coupon, line PricedLine, targets []CouponTarget) bool {
	switch coupon.Scope {
	case "", "all":
		return true
	case "category":
		for _, t := range targets {
			if t.TargetType != "category" {
				continue
			}
			for _, id := range line.CategoryIDs {
				if id == t.TargetID {
					return true
				}
			}
		}
		return false
	case "product":
		for _, t := range targets {
			if t.TargetType == "product" && t.TargetID == line.ProductID {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// roundHalfUpPercent returns subtotal*percent/100 rounded half up without
// overflowing int64.
func roundHalfUpPercent(subtotal, percent int64) (int64, error) {
	if subtotal < 0 || percent < 0 {
		return 0, ErrCouponInvalid
	}
	q := subtotal / 100
	r := subtotal % 100
	if percent != 0 && q > math.MaxInt64/percent {
		return 0, errors.New("coupon: discount overflow")
	}
	amount := q * percent
	extra := (r*percent + 50) / 100
	if amount > math.MaxInt64-extra {
		return 0, errors.New("coupon: discount overflow")
	}
	return amount + extra, nil
}

// Evaluate applies coupon to the cart for user (nil for guests). It checks the
// window, minimum subtotal, scope, members-only and the total usage limit,
// then returns the discount. The discount never makes the total negative and
// never exceeds the subtotal. An invalid coupon returns an error that the API
// maps to 422.
//
// Scope categories/products need the coupon targets; the server loads them
// with CouponTargets and calls EvaluateWithTargets. A scoped coupon evaluated
// without targets does not apply. Per-user limits need a database lookup and
// are enforced by Redeem (CouponPerUserUsed exposes the same count).
func Evaluate(cart CartQuote, coupon Coupon, user *User) (Discount, error) {
	return EvaluateWithTargets(cart, coupon, user, nil)
}

// EvaluateWithTargets is Evaluate with the coupon targets already loaded.
func EvaluateWithTargets(cart CartQuote, coupon Coupon, user *User, targets []CouponTarget) (Discount, error) {
	now := time.Now()
	if cart.Subtotal < 0 || cart.ShippingFee < 0 {
		return Discount{}, ErrCouponInvalid
	}
	if coupon.Status != "" && coupon.Status != "active" {
		return Discount{}, ErrCouponInvalid
	}
	if !couponInWindow(coupon, now) {
		return Discount{}, ErrCouponInvalid
	}
	if coupon.MinSubtotal > 0 && cart.Subtotal < coupon.MinSubtotal {
		return Discount{}, ErrCouponInvalid
	}
	if coupon.MembersOnly && user == nil {
		return Discount{}, ErrCouponInvalid
	}
	if coupon.UsageLimit != nil && coupon.UsedCount >= *coupon.UsageLimit {
		return Discount{}, ErrCouponUsageLimit
	}
	if coupon.BoundEmail != "" && normalizeEmail(coupon.BoundEmail) != normalizeEmail(cart.Email) {
		return Discount{}, ErrCouponBoundEmail
	}

	var discount Discount
	if coupon.Label != "" {
		discount.Label = coupon.Label
	}

	// Sum only the lines the coupon covers.
	var scopedSubtotal int64
	applicable := make([]PricedLine, 0, len(cart.Lines))
	for _, line := range cart.Lines {
		if line.Qty < 0 || line.UnitPrice < 0 || line.LineTotal < 0 {
			return Discount{}, ErrCouponInvalid
		}
		if line.Qty == 0 {
			continue
		}
		if lineMatchesScope(coupon, line, targets) {
			lineTotal := line.LineTotal
			if lineTotal == 0 && line.UnitPrice > 0 {
				if int64(line.Qty) > math.MaxInt64/line.UnitPrice {
					return Discount{}, errors.New("coupon: cart total overflow")
				}
				lineTotal = int64(line.Qty) * line.UnitPrice
			}
			if scopedSubtotal > math.MaxInt64-lineTotal {
				return Discount{}, errors.New("coupon: cart total overflow")
			}
			scopedSubtotal += lineTotal
			if line.UnitPrice == 0 && line.Qty > 0 {
				line.UnitPrice = lineTotal / int64(line.Qty)
			}
			applicable = append(applicable, line)
		}
	}
	if len(cart.Lines) == 0 && (coupon.Scope == "" || coupon.Scope == "all") {
		scopedSubtotal = cart.Subtotal
		applicable = nil
	}

	if len(cart.Lines) > 0 && len(applicable) == 0 {
		return Discount{}, ErrCouponNotApplicable
	}
	if scopedSubtotal <= 0 && coupon.Kind != "free_shipping" {
		return Discount{}, ErrCouponNotApplicable
	}
	if scopedSubtotal > cart.Subtotal {
		scopedSubtotal = cart.Subtotal
	}

	switch coupon.Kind {
	case "percent":
		if coupon.Value < 0 || coupon.Value > 100 {
			return Discount{}, ErrCouponInvalid
		}
		amount, err := roundHalfUpPercent(scopedSubtotal, coupon.Value)
		if err != nil {
			return Discount{}, err
		}
		discount.Amount = amount
		if coupon.Value == 100 {
			discount.Amount = scopedSubtotal
		}
		if discount.Label == "" {
			discount.Label = fmt.Sprintf("%d%% off", coupon.Value)
		}
	case "fixed":
		if coupon.Value < 0 {
			return Discount{}, ErrCouponInvalid
		}
		discount.Amount = coupon.Value
		if coupon.Label == "" {
			discount.Label = "discount"
		}
	case "free_shipping":
		discount.FreeShipping = true
		if discount.Label == "" {
			discount.Label = "free shipping"
		}
	case "bogo":
		if coupon.BuyQty <= 0 || coupon.GetQty <= 0 || coupon.BuyQty > math.MaxInt-coupon.GetQty {
			return Discount{}, ErrCouponInvalid
		}
		group := coupon.BuyQty + coupon.GetQty
		totalQty := 0
		for _, line := range applicable {
			if line.Qty > math.MaxInt-totalQty {
				return Discount{}, errors.New("coupon: quantity overflow")
			}
			totalQty += line.Qty
		}
		freeUnits := (totalQty / group) * coupon.GetQty
		if freeUnits <= 0 {
			return Discount{}, ErrCouponNotApplicable
		}
		lines := append([]PricedLine(nil), applicable...)
		sort.SliceStable(lines, func(i, j int) bool { return lines[i].UnitPrice < lines[j].UnitPrice })
		remaining := freeUnits
		var amount int64
		for _, line := range lines {
			if remaining <= 0 {
				break
			}
			take := line.Qty
			if take > remaining {
				take = remaining
			}
			price := line.UnitPrice
			if price <= 0 && line.Qty > 0 {
				price = line.LineTotal / int64(line.Qty)
			}
			if price > 0 && int64(take) > math.MaxInt64/price {
				return Discount{}, errors.New("coupon: discount overflow")
			}
			cost := int64(take) * price
			if amount > math.MaxInt64-cost {
				return Discount{}, errors.New("coupon: discount overflow")
			}
			amount += cost
			remaining -= take
		}
		discount.Amount = amount
		discount.FreeUnits = freeUnits
		if discount.Label == "" {
			discount.Label = fmt.Sprintf("買 %d 送 %d", coupon.BuyQty, coupon.GetQty)
		}
	default:
		return Discount{}, ErrCouponInvalid
	}

	if discount.Amount < 0 {
		discount.Amount = 0
	}
	if discount.Amount > scopedSubtotal {
		discount.Amount = scopedSubtotal
	}
	return discount, nil
}

// Redeem records that orderID used couponID inside tx. It increments
// used_count atomically and fails when the usage limit is already reached.
// email is stored lowercased and is the key for per-user limits.
func Redeem(tx *gorm.DB, couponID, orderID uint, userID *uint, email string) error {
	if tx == nil {
		return errors.New("nil tx")
	}
	var coupon Coupon
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&coupon, couponID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrCouponNotFound
		}
		return err
	}
	if coupon.Status != "" && coupon.Status != "active" {
		return ErrCouponInvalid
	}
	if !couponInWindow(coupon, time.Now()) {
		return ErrCouponInvalid
	}
	if coupon.MembersOnly && userID == nil {
		return ErrCouponInvalid
	}
	if userID != nil {
		var user User
		if err := tx.Select("id", "email", "status").First(&user, *userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrCouponInvalid
			}
			return err
		}
		if user.Status == "disabled" {
			return ErrCouponInvalid
		}
	}
	if coupon.BoundEmail != "" {
		if normalizeEmail(coupon.BoundEmail) != normalizeEmail(email) {
			return ErrCouponBoundEmail
		}
	}

	// A repeated redemption for the same order must not consume another use.
	var existing CouponRedemption
	err := tx.Where("coupon_id = ? AND order_id = ?", couponID, orderID).First(&existing).Error
	if err == nil {
		return ErrCouponInvalid
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	if coupon.UsageLimit != nil && coupon.UsedCount >= *coupon.UsageLimit {
		return ErrCouponUsageLimit
	}
	if coupon.PerUserLimit != nil {
		used, err := CouponPerUserUsed(tx, couponID, userID, email)
		if err != nil {
			return err
		}
		if used >= int64(*coupon.PerUserLimit) {
			return ErrCouponPerUserLimit
		}
	}

	redemption := CouponRedemption{
		CouponID: couponID,
		OrderID:  orderID,
		UserID:   userID,
		Email:    normalizeEmail(email),
	}
	if err := tx.Create(&redemption).Error; err != nil {
		return err
	}

	res := tx.Exec(`UPDATE coupons SET used_count = used_count + 1, updated_at = now()
		WHERE id = ? AND (usage_limit IS NULL OR used_count < usage_limit)`, couponID)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		// The limit was consumed by another transaction after our check.
		return ErrCouponUsageLimit
	}
	return nil
}

// ReleaseRedemption undoes the redemption of orderID (the order expired or was
// cancelled before payment): it sets released_at and decrements used_count.
// It is idempotent: rows that were already released are skipped.
func ReleaseRedemption(tx *gorm.DB, orderID uint) error {
	if tx == nil {
		return errors.New("nil tx")
	}
	var couponIDs []uint
	if err := tx.Model(&CouponRedemption{}).Distinct("coupon_id").
		Where("order_id = ? AND released_at IS NULL", orderID).Order("coupon_id").Pluck("coupon_id", &couponIDs).Error; err != nil {
		return err
	}
	// Redeem locks the coupon before reading or creating its redemption. Keep
	// that order here so concurrent redemption and release cannot deadlock.
	for _, couponID := range couponIDs {
		var coupon Coupon
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&coupon, couponID).Error; err != nil {
			return err
		}
	}
	var redemptions []CouponRedemption
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("order_id = ? AND released_at IS NULL", orderID).Order("coupon_id, id").Find(&redemptions).Error; err != nil {
		return err
	}
	if len(redemptions) == 0 {
		return nil
	}
	now := time.Now()
	for _, redemption := range redemptions {
		res := tx.Model(&CouponRedemption{}).
			Where("id = ? AND released_at IS NULL", redemption.ID).
			Update("released_at", now)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			continue
		}
		if err := tx.Exec(`UPDATE coupons SET used_count = GREATEST(used_count - 1, 0), updated_at = now()
			WHERE id = ?`, redemption.CouponID).Error; err != nil {
			return err
		}
	}
	return nil
}
