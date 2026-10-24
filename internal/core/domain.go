package core

import (
	"time"

	"github.com/goccy/go-json"
	"gorm.io/gorm"
)

// User represents a registered user.
type User struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	Username  string    `json:"username" gorm:"uniqueIndex;size:50"`
	Email     string    `json:"email" gorm:"uniqueIndex;size:255"`
	Password  string    `json:"-"` // Hash
	AvatarURL string    `json:"avatar_url"`
	Role      string    `json:"role" gorm:"default:author"` // admin, editor, author, customer
	Provider  string    `json:"provider"`                   // local, google, github
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Account self-service (ticket 06). Columns are created by migration 0002.
	DisplayName       string     `json:"display_name" gorm:"size:120;not null;default:''"`
	Bio               string     `json:"bio" gorm:"type:text;not null;default:''"`
	Locale            string     `json:"locale" gorm:"size:10;not null;default:'zh-TW'"`
	UITheme           string     `json:"ui_theme" gorm:"column:ui_theme;size:10;not null;default:'system'"` // system, light, dark
	Status            string     `json:"status" gorm:"size:16;not null;default:'active'"`                   // active, invited, disabled
	DisabledAt        *time.Time `json:"disabled_at"`
	LastLoginAt       *time.Time `json:"last_login_at"`
	PasswordChangedAt *time.Time `json:"password_changed_at"`
	EmailVerifiedAt   *time.Time `json:"email_verified_at"`

	// Pending email change; never serialised to clients.
	PendingEmail          string     `json:"-" gorm:"size:255;not null;default:''"`
	PendingEmailHash      string     `json:"-" gorm:"column:pending_email_hash;size:64;not null;default:''"`
	PendingEmailExpiresAt *time.Time `json:"-"`
}

// Post represents a content page or blog post.
type Post struct {
	ID          uint            `json:"id" gorm:"primaryKey"`
	Title       string          `json:"title" gorm:"size:255"`
	Slug        string          `json:"slug" gorm:"uniqueIndex;size:255"`
	Content     string          `json:"content" gorm:"type:text"`          // HTML or Markdown
	Type        string          `json:"type" gorm:"index;default:post"`    // post, page
	Status      string          `json:"status" gorm:"index;default:draft"` // published, draft
	AuthorID    uint            `json:"author_id"`
	Author      User            `json:"author" gorm:"foreignKey:AuthorID"`
	Template    string          `json:"template"`               // Custom template file
	Meta        json.RawMessage `json:"meta" gorm:"type:jsonb"` // Custom fields
	PublishedAt *time.Time      `json:"published_at"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`

	// Content organisation (ticket 04).
	Excerpt       string         `json:"excerpt" gorm:"type:text"`
	Cover         string         `json:"cover"`
	AllowComments *bool          `json:"allow_comments" gorm:"default:true"`
	Pinned        bool           `json:"pinned" gorm:"index"`
	Visibility    string         `json:"visibility" gorm:"size:16;default:public"` // public, private
	Categories    []Category     `json:"categories" gorm:"many2many:post_categories;"`
	Tags          []Tag          `json:"tags" gorm:"many2many:post_tags;"`
	DeletedByID   *uint          `json:"deleted_by_id,omitempty"`
	DeletedAt     gorm.DeletedAt `json:"deleted_at,omitempty" gorm:"index"`
}

// CommentsAllowed treats a nil AllowComments as true.
func (p Post) CommentsAllowed() bool { return p.AllowComments == nil || *p.AllowComments }

// Option represents global settings.
type Option struct {
	Key   string `json:"key" gorm:"primaryKey;size:100"`
	Value string `json:"value" gorm:"type:text"`
}

// Product represents an e-commerce product.
type Product struct {
	ID          uint            `json:"id" gorm:"primaryKey"`
	Title       string          `json:"title" gorm:"size:255"`
	Slug        string          `json:"slug" gorm:"uniqueIndex;size:255"`
	Description string          `json:"description" gorm:"type:text"`
	Price       int64           `json:"price"` // In smallest currency unit (e.g., cents)
	SKU         string          `json:"sku" gorm:"size:100"`
	Stock       int             `json:"stock"`
	Images      json.RawMessage `json:"images" gorm:"type:jsonb"`          // Array of image URLs
	Status      string          `json:"status" gorm:"index;default:draft"` // active, draft, archived
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	DeletedByID *uint           `json:"deleted_by_id,omitempty"`
	DeletedAt   gorm.DeletedAt  `json:"deleted_at,omitempty" gorm:"index"`

	// Shop columns (ticket 07, migration 0008). Migration 0008 adds the same
	// columns with the same defaults, so the two definitions must stay equal.
	//
	// Caution for writers: a field with a default tag and a zero value is
	// omitted from INSERT, so the database default wins. Create a product with
	// RequiresShipping=false (or LowStockThreshold=0) through Select(...) so the
	// explicit zero value is stored.
	CompareAtPrice    int64           `json:"compare_at_price" gorm:"not null;default:0"` // 0 = no compare price
	LowStockThreshold int             `json:"low_stock_threshold" gorm:"not null;default:5"`
	RequiresShipping  bool            `json:"requires_shipping" gorm:"not null;default:true"`
	ColdChain         bool            `json:"cold_chain" gorm:"not null;default:false"`
	Attributes        json.RawMessage `json:"attributes" gorm:"type:jsonb"`
	RelatedIDs        json.RawMessage `json:"related_ids" gorm:"type:jsonb"`
	SoldCount         int64           `json:"sold_count" gorm:"not null;default:0"`
	SEOTitle          string          `json:"seo_title" gorm:"size:255;not null;default:''"`
	SEODescription    string          `json:"seo_description" gorm:"type:text;not null;default:''"`
	Kind              string          `json:"kind" gorm:"size:16;not null;default:'physical'"` // physical, digital
}

// Order represents a customer order.
//
// The first block of fields predates the shop. The shop columns (ticket 07,
// migrations 0010 and 0012) follow it. UserID is nil for guest orders; the
// order keeps its snapshot when the account is deleted.
type Order struct {
	ID                 uint        `json:"id" gorm:"primaryKey"`
	UserID             *uint       `json:"user_id"`
	User               User        `json:"user" gorm:"foreignKey:UserID"`
	TotalAmount        int64       `json:"total_amount"`
	RoundingAdjustment int64       `json:"rounding_adjustment" gorm:"not null;default:0"`
	Status             string      `json:"status" gorm:"index;default:pending"` // pending, paid, shipped, completed, cancelled, expired, refunded
	PaymentMethod      string      `json:"payment_method"`
	ShippingAddress    string      `json:"shipping_address" gorm:"type:text"`
	Items              []OrderItem `json:"items" gorm:"foreignKey:OrderID"`
	CreatedAt          time.Time   `json:"created_at"`
	UpdatedAt          time.Time   `json:"updated_at"`

	// Shop columns (ticket 07). Amounts are in minor units (分). The unique
	// indexes on number and idempotency_key are created by migration 0010, not
	// by these tags.
	Number          string          `json:"number" gorm:"size:32"` // JL-YYYY-NNNN
	Email           string          `json:"email" gorm:"size:255;not null;default:''"`
	Phone           string          `json:"phone" gorm:"size:40;not null;default:''"`
	Currency        string          `json:"currency" gorm:"size:3;not null;default:'TWD'"`
	Subtotal        int64           `json:"subtotal" gorm:"not null;default:0"`
	DiscountTotal   int64           `json:"discount_total" gorm:"not null;default:0"`
	ShippingTotal   int64           `json:"shipping_total" gorm:"not null;default:0"`
	TaxTotal        int64           `json:"tax_total" gorm:"not null;default:0"`
	CouponCode      string          `json:"coupon_code" gorm:"size:64;not null;default:''"`
	CouponID        *uint           `json:"coupon_id"`
	ShippingMethod  string          `json:"shipping_method" gorm:"size:40;not null;default:''"`
	Shipping        json.RawMessage `json:"shipping" gorm:"type:jsonb"`
	Invoice         json.RawMessage `json:"invoice" gorm:"type:jsonb"`
	PaymentProvider string          `json:"payment_provider" gorm:"size:40;not null;default:''"`
	LookupTokenHash string          `json:"-" gorm:"size:64"`
	IdempotencyKey  *string         `json:"-" gorm:"size:64"`
	ExpiresAt       *time.Time      `json:"expires_at"`
	PaidAt          *time.Time      `json:"paid_at"`
	ShippedAt       *time.Time      `json:"shipped_at"`
	CompletedAt     *time.Time      `json:"completed_at"`
	CancelledAt     *time.Time      `json:"cancelled_at"`
	Carrier         string          `json:"carrier" gorm:"size:40;not null;default:''"`
	TrackingNo      string          `json:"tracking_no" gorm:"size:80;not null;default:''"`
	RefundedTotal   int64           `json:"refunded_total" gorm:"not null;default:0"`
	InternalNote    string          `json:"internal_note" gorm:"type:text;not null;default:''"`
	Locale          string          `json:"locale" gorm:"size:10;not null;default:'zh-TW'"`
}

// OrderItem represents an item in an order.
//
// ProductID is nil once the product is deleted (migration 0010 sets it to
// NULL). The snapshot columns keep the title, SKU and prices as sold.
type OrderItem struct {
	ID        uint    `json:"id" gorm:"primaryKey"`
	OrderID   uint    `json:"order_id"`
	ProductID *uint   `json:"product_id"`
	Product   Product `json:"product" gorm:"foreignKey:ProductID"`
	Quantity  int     `json:"quantity"`
	Price     int64   `json:"price"` // Price at the time of purchase

	// Shop snapshot columns (ticket 07, migration 0010).
	VariantID   *uint  `json:"variant_id"`
	Title       string `json:"title" gorm:"size:255;not null;default:''"`
	VariantName string `json:"variant_name" gorm:"size:120;not null;default:''"`
	SKU         string `json:"sku" gorm:"size:100;not null;default:''"`
	Image       string `json:"image" gorm:"type:text;not null;default:''"`
	UnitPrice   int64  `json:"unit_price" gorm:"not null;default:0"`
	LineTotal   int64  `json:"line_total" gorm:"not null;default:0"`
}
