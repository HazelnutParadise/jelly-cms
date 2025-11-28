package core

import (
	"time"

	"github.com/goccy/go-json"
)

// User represents a registered user.
type User struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	Username  string    `json:"username" gorm:"uniqueIndex;size:50"`
	Email     string    `json:"email" gorm:"uniqueIndex;size:255"`
	Password  string    `json:"-"` // Hash
	AvatarURL string    `json:"avatar_url"`
	Role      string    `json:"role" gorm:"default:editor"` // admin, editor, author
	Provider  string    `json:"provider"`                   // local, google, github
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Post represents a content page or blog post.
type Post struct {
	ID           uint            `json:"id" gorm:"primaryKey"`
	Title        string          `json:"title" gorm:"size:255"`
	Slug         string          `json:"slug" gorm:"uniqueIndex;size:255"`
	Content      string          `json:"content" gorm:"type:text"` // HTML or Markdown
	Type         string          `json:"type" gorm:"index;default:post"` // post, page
	Status       string          `json:"status" gorm:"index;default:draft"` // published, draft
	AuthorID     uint            `json:"author_id"`
	Author       User            `json:"author" gorm:"foreignKey:AuthorID"`
	Template     string          `json:"template"` // Custom template file
	Meta         json.RawMessage `json:"meta" gorm:"type:jsonb"` // Custom fields
	PublishedAt  *time.Time      `json:"published_at"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// Option represents global settings.
type Option struct {
	Key   string `json:"key" gorm:"primaryKey;size:100"`
	Value string `json:"value" gorm:"type:text"`
}
