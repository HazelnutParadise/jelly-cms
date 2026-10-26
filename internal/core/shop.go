package core

import (
	"errors"
	"time"

	"github.com/goccy/go-json"
)

// ErrNotImplemented marks shop functions whose skeleton has not been filled in
// yet (ticket 07). Callers must not treat it as a business error.
var ErrNotImplemented = errors.New("not implemented")

// The structs below mirror the tables created by migrations 0008 to 0014 in
// internal/data/migrations. They are not part of GORM AutoMigrate: the SQL
// files create the tables, and each column name below is the gorm default name
// of its field. Money is always stored in minor units (分) as int64.

// ProductCategory is a shop category, independent of the post category tree.
// Products are linked through product_category_links (product_id, category_id);
// no relation field is declared on Product so that AutoMigrate never creates
// a second join table.
type ProductCategory struct {
	ID          uint      `json:"id" gorm:"primaryKey"`
	ParentID    *uint     `json:"parent_id" gorm:"index"`
	Name        string    `json:"name" gorm:"size:120"`
	Slug        string    `json:"slug" gorm:"size:160;uniqueIndex"`
	Description string    `json:"description" gorm:"type:text"`
	Cover       string    `json:"cover"`
	Position    int       `json:"position" gorm:"index"`
	Hidden      bool      `json:"hidden"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ProductVariant is one sellable option of a product (size, colour, ...).
// Stock lives here; the database rejects negative stock (stock >= 0).
// Migration 0008 creates one variant with an empty name for every product.
type ProductVariant struct {
	ID             uint      `json:"id" gorm:"primaryKey"`
	ProductID      uint      `json:"product_id" gorm:"index;not null"`
	Name           string    `json:"name" gorm:"size:120;not null;default:''"`
	SKU            string    `json:"sku" gorm:"size:100;not null;default:''"`
	Price          int64     `json:"price" gorm:"not null;default:0"`
	CompareAtPrice int64     `json:"compare_at_price" gorm:"not null;default:0"`
	Stock          int       `json:"stock" gorm:"not null;default:0"`
	Position       int       `json:"position" gorm:"not null;default:0"`
	Status         string    `json:"status" gorm:"size:16;not null;default:'active'"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// StockMovement is one row of the stock ledger. Rows are only inserted, never
// updated or deleted (a trigger enforces this). Reason is reserve, release,
// restock or adjust.
type StockMovement struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	VariantID uint      `json:"variant_id" gorm:"index;not null"`
	OrderID   *uint     `json:"order_id" gorm:"index"`
	Delta     int       `json:"delta" gorm:"not null"`
	Reason    string    `json:"reason" gorm:"size:16;not null"`
	CreatedAt time.Time `json:"created_at"`
}

// Cart is a server-side cart. The browser holds a random token in an HttpOnly
// cookie; only its SHA-256 (TokenHash) is stored.
type Cart struct {
	ID         uint       `json:"id" gorm:"primaryKey"`
	TokenHash  string     `json:"-" gorm:"size:64;uniqueIndex;not null"`
	UserID     *uint      `json:"user_id" gorm:"index"`
	CouponCode string     `json:"coupon_code" gorm:"size:64;not null;default:''"`
	ExpiresAt  time.Time  `json:"expires_at" gorm:"index;not null"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	Items      []CartItem `json:"items,omitempty" gorm:"foreignKey:CartID"`
}

// CartItem is one line of a cart. Qty is 1 to 99 and is clamped to stock when
// the cart is read.
type CartItem struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	CartID    uint      `json:"cart_id" gorm:"uniqueIndex:idx_cart_items_cart_variant;not null"`
	VariantID uint      `json:"variant_id" gorm:"uniqueIndex:idx_cart_items_cart_variant;not null"`
	Qty       int       `json:"qty" gorm:"not null"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// OrderEvent is one entry of an order's timeline. Every status change writes
// one (see OrderService.Transition).
type OrderEvent struct {
	ID          uint            `json:"id" gorm:"primaryKey"`
	OrderID     uint            `json:"order_id" gorm:"index;not null"`
	At          time.Time       `json:"at"`
	ActorUserID *uint           `json:"actor_user_id"`
	Kind        string          `json:"kind" gorm:"size:40;not null"`
	FromStatus  string          `json:"from_status" gorm:"size:20;not null;default:''"`
	ToStatus    string          `json:"to_status" gorm:"size:20;not null;default:''"`
	Message     string          `json:"message" gorm:"type:text;not null;default:''"`
	Meta        json.RawMessage `json:"meta" gorm:"type:jsonb"`
}

// OrderCounter holds the last sequence number used in one year, for order
// numbers JL-YYYY-NNNN. The primary key is the year, so the count restarts
// every year.
type OrderCounter struct {
	Year int   `json:"year" gorm:"primaryKey"`
	Seq  int64 `json:"seq" gorm:"not null;default:0"`
}

// Address is a saved shipping address of a member.
type Address struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	UserID    uint      `json:"user_id" gorm:"index;not null"`
	Label     string    `json:"label" gorm:"size:60;not null;default:''"`
	Recipient string    `json:"recipient" gorm:"size:120;not null;default:''"`
	Phone     string    `json:"phone" gorm:"size:40;not null;default:''"`
	Postcode  string    `json:"postcode" gorm:"size:10;not null;default:''"`
	City      string    `json:"city" gorm:"size:60;not null;default:''"`
	Address   string    `json:"address" gorm:"size:255;not null;default:''"`
	IsDefault bool      `json:"is_default" gorm:"not null;default:false"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// InvoiceProfile is a member's saved invoice setting. Kind is none,
// personal_carrier, tax_id or donation.
type InvoiceProfile struct {
	ID           uint      `json:"id" gorm:"primaryKey"`
	UserID       uint      `json:"user_id" gorm:"index;not null"`
	Kind         string    `json:"kind" gorm:"size:16;not null;default:'none'"`
	CarrierCode  string    `json:"carrier_code" gorm:"size:16;not null;default:''"`
	TaxID        string    `json:"tax_id" gorm:"size:8;not null;default:''"`
	CompanyName  string    `json:"company_name" gorm:"size:120;not null;default:''"`
	DonationCode string    `json:"donation_code" gorm:"size:16;not null;default:''"`
	IsDefault    bool      `json:"is_default" gorm:"not null;default:false"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Invoice is the invoice record of an order. Electronic invoices are not
// issued yet; the table only keeps the submitted details.
type Invoice struct {
	ID           uint      `json:"id" gorm:"primaryKey"`
	OrderID      uint      `json:"order_id" gorm:"index;not null"`
	Kind         string    `json:"kind" gorm:"size:16;not null;default:'none'"`
	CarrierCode  string    `json:"carrier_code" gorm:"size:16;not null;default:''"`
	TaxID        string    `json:"tax_id" gorm:"size:8;not null;default:''"`
	CompanyName  string    `json:"company_name" gorm:"size:120;not null;default:''"`
	DonationCode string    `json:"donation_code" gorm:"size:16;not null;default:''"`
	Status       string    `json:"status" gorm:"size:16;not null;default:'pending'"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Coupon is a discount code. Code is matched case-insensitively (unique index
// on lower(code)). Kind is percent, fixed, free_shipping or bogo. Value is a
// percentage for percent and minor units for fixed. Scope limits the discount
// to all items, a category or a product (see CouponTarget).
type Coupon struct {
	ID                        uint       `json:"id" gorm:"primaryKey"`
	Code                      string     `json:"code" gorm:"size:64;not null"`
	Label                     string     `json:"label" gorm:"size:120;not null;default:''"`
	Kind                      string     `json:"kind" gorm:"size:16;not null"`
	Value                     int64      `json:"value" gorm:"not null;default:0"`
	BuyQty                    int        `json:"buy_qty" gorm:"not null;default:0"`
	GetQty                    int        `json:"get_qty" gorm:"not null;default:0"`
	MinSubtotal               int64      `json:"min_subtotal" gorm:"not null;default:0"`
	Scope                     string     `json:"scope" gorm:"size:16;not null;default:'all'"`
	UsageLimit                *int       `json:"usage_limit"`
	UsedCount                 int        `json:"used_count" gorm:"not null;default:0"`
	PerUserLimit              *int       `json:"per_user_limit"`
	StartsAt                  *time.Time `json:"starts_at"`
	EndsAt                    *time.Time `json:"ends_at"`
	StackableWithFreeShipping bool       `json:"stackable_with_free_shipping" gorm:"not null;default:false"`
	MembersOnly               bool       `json:"members_only" gorm:"not null;default:false"`
	BatchID                   *uint      `json:"batch_id" gorm:"index"`
	BoundEmail                string     `json:"bound_email" gorm:"size:255;not null;default:''"`
	Status                    string     `json:"status" gorm:"size:16;not null;default:'active'"`
	CreatedAt                 time.Time  `json:"created_at"`
	UpdatedAt                 time.Time  `json:"updated_at"`
}

// CouponTarget limits a coupon to categories or products. TargetID has no
// foreign key because it points at either product_categories or products.
type CouponTarget struct {
	ID         uint   `json:"id" gorm:"primaryKey"`
	CouponID   uint   `json:"coupon_id" gorm:"uniqueIndex:idx_coupon_targets_unique;not null"`
	TargetType string `json:"target_type" gorm:"size:16;uniqueIndex:idx_coupon_targets_unique;not null"`
	TargetID   uint   `json:"target_id" gorm:"uniqueIndex:idx_coupon_targets_unique;not null"`
}

// CouponBatch records a generated set of one-per-person codes.
type CouponBatch struct {
	ID          uint            `json:"id" gorm:"primaryKey"`
	Prefix      string          `json:"prefix" gorm:"size:20;not null;default:''"`
	Size        int             `json:"size" gorm:"not null;default:0"`
	Template    json.RawMessage `json:"template" gorm:"type:jsonb"`
	CreatedByID *uint           `json:"created_by_id"`
	CreatedAt   time.Time       `json:"created_at"`
}

// CouponRedemption records that a coupon was used by an order. Redeem
// increments Coupon.UsedCount in the same transaction; ReleaseRedemption sets
// ReleasedAt and decrements it again.
type CouponRedemption struct {
	ID         uint       `json:"id" gorm:"primaryKey"`
	CouponID   uint       `json:"coupon_id" gorm:"uniqueIndex:idx_coupon_redemptions_coupon_order;not null"`
	OrderID    uint       `json:"order_id" gorm:"uniqueIndex:idx_coupon_redemptions_coupon_order;not null"`
	UserID     *uint      `json:"user_id"`
	Email      string     `json:"email" gorm:"size:255;not null;default:''"`
	ReleasedAt *time.Time `json:"released_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Payment is one payment attempt of an order. MerchantTradeNo is the 20-character
// alphanumeric number sent to the provider and is unique.
type Payment struct {
	ID              uint            `json:"id" gorm:"primaryKey"`
	OrderID         uint            `json:"order_id" gorm:"index;not null"`
	Provider        string          `json:"provider" gorm:"size:40;not null"`
	Method          string          `json:"method" gorm:"size:40;not null;default:''"`
	MerchantTradeNo string          `json:"merchant_trade_no" gorm:"size:20;uniqueIndex;not null"`
	Amount          int64           `json:"amount" gorm:"not null"`
	Currency        string          `json:"currency" gorm:"size:3;not null;default:'TWD'"`
	Status          string          `json:"status" gorm:"size:20;not null;default:'pending'"`
	ProviderTradeNo string          `json:"provider_trade_no" gorm:"size:80;not null;default:''"`
	MaskedInfo      json.RawMessage `json:"masked_info" gorm:"type:jsonb"`
	PaidAt          *time.Time      `json:"paid_at"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// PaymentEvent records a payment notification. The pair (Provider, DedupeKey)
// is unique, so a repeated notification is recorded and applied only once.
// Only notifications whose signature was verified have Verified set.
type PaymentEvent struct {
	ID        uint            `json:"id" gorm:"primaryKey"`
	Provider  string          `json:"provider" gorm:"size:40;uniqueIndex:idx_payment_events_provider_dedupe;not null"`
	DedupeKey string          `json:"dedupe_key" gorm:"size:200;uniqueIndex:idx_payment_events_provider_dedupe;not null"`
	Verified  bool            `json:"verified" gorm:"not null;default:false"`
	Payload   json.RawMessage `json:"payload" gorm:"type:jsonb"`
	CreatedAt time.Time       `json:"created_at"`
}

// OrderAccessToken is a single-purpose link to an order. Kind is lookup (the
// emailed 15-minute link) or view (the 30-minute cookie after the link is
// used). Only the SHA-256 of the token is stored.
type OrderAccessToken struct {
	ID              uint       `json:"id" gorm:"primaryKey"`
	OrderID         uint       `json:"order_id" gorm:"index;not null"`
	TokenHash       string     `json:"-" gorm:"size:64;uniqueIndex;not null"`
	Kind            string     `json:"kind" gorm:"size:16;not null"`
	ExpiresAt       time.Time  `json:"expires_at" gorm:"not null"`
	UsedAt          *time.Time `json:"used_at"`
	CreatedIPMasked string     `json:"created_ip_masked" gorm:"size:64;not null;default:''"`
	CreatedAt       time.Time  `json:"created_at"`
}

// StockAlert records that a variant fell to or below its low stock threshold.
// The table exists; the feature comes later.
type StockAlert struct {
	ID         uint       `json:"id" gorm:"primaryKey"`
	VariantID  uint       `json:"variant_id" gorm:"index;not null"`
	Stock      int        `json:"stock" gorm:"not null;default:0"`
	Threshold  int        `json:"threshold" gorm:"not null;default:0"`
	CreatedAt  time.Time  `json:"created_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
}
