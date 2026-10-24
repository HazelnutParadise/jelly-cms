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
}

// Order represents a customer order.
type Order struct {
	ID              uint        `json:"id" gorm:"primaryKey"`
	UserID          uint        `json:"user_id"`
	User            User        `json:"user" gorm:"foreignKey:UserID"`
	TotalAmount     int64       `json:"total_amount"`
	Status          string      `json:"status" gorm:"index;default:pending"` // pending, paid, shipped, completed, cancelled
	PaymentMethod   string      `json:"payment_method"`
	ShippingAddress string      `json:"shipping_address" gorm:"type:text"`
	Items           []OrderItem `json:"items" gorm:"foreignKey:OrderID"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
}

// OrderItem represents an item in an order.
type OrderItem struct {
	ID        uint    `json:"id" gorm:"primaryKey"`
	OrderID   uint    `json:"order_id"`
	ProductID uint    `json:"product_id"`
	Product   Product `json:"product" gorm:"foreignKey:ProductID"`
	Quantity  int     `json:"quantity"`
	Price     int64   `json:"price"` // Price at the time of purchase
}
