package core

import (
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/goccy/go-json"
	"gorm.io/gorm"
)

// Category is a hierarchical term. Posts may belong to several categories.
type Category struct {
	ID           uint            `json:"id" gorm:"primaryKey"`
	Name         string          `json:"name" gorm:"size:120"`
	Slug         string          `json:"slug" gorm:"uniqueIndex;size:160"`
	Description  string          `json:"description" gorm:"type:text"`
	ParentID     *uint           `json:"parent_id" gorm:"index"`
	Color        string          `json:"color" gorm:"size:16"` // theme flavor name: primary, secondary, accent, warning, ink
	Cover        string          `json:"cover"`
	HideFromList bool            `json:"hide_from_list"` // hidden from front lists, still reachable by URL
	NoCascade    bool            `json:"no_cascade"`     // parent archive does not include this category's posts
	Template     string          `json:"template"`       // theme template override for the archive page
	Position     int             `json:"position" gorm:"index"`
	Meta         json.RawMessage `json:"meta" gorm:"type:jsonb"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	PostCount    int64           `json:"post_count" gorm:"-"`
}

// Tag is a flat term.
type Tag struct {
	ID          uint      `json:"id" gorm:"primaryKey"`
	Name        string    `json:"name" gorm:"size:120"`
	Slug        string    `json:"slug" gorm:"uniqueIndex;size:160"`
	Color       string    `json:"color" gorm:"size:16"`
	Cover       string    `json:"cover"`
	Description string    `json:"description" gorm:"type:text"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	PostCount   int64     `json:"post_count" gorm:"-"`
}

// Comment belongs to a post. Status is one of pending, approved, spam.
// Trashed comments carry DeletedAt.
type Comment struct {
	ID          uint           `json:"id" gorm:"primaryKey"`
	PostID      uint           `json:"post_id" gorm:"index"`
	Post        Post           `json:"-" gorm:"foreignKey:PostID"`
	ParentID    *uint          `json:"parent_id" gorm:"index"`
	AuthorID    *uint          `json:"author_id" gorm:"index"` // signed-in commenter, nil for guests
	Author      *User          `json:"author,omitempty" gorm:"foreignKey:AuthorID"`
	Name        string         `json:"name" gorm:"size:120"`
	Email       string         `json:"-" gorm:"size:255;index"`
	Body        string         `json:"body" gorm:"type:text"`      // Markdown as typed
	BodyHTML    string         `json:"body_html" gorm:"type:text"` // sanitized render
	Status      string         `json:"status" gorm:"index;default:pending"`
	IP          string         `json:"-" gorm:"size:64"`
	UserAgent   string         `json:"-" gorm:"size:255"`
	DeletedByID *uint          `json:"deleted_by_id,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `json:"deleted_at,omitempty" gorm:"index"`
	Children    []Comment      `json:"children,omitempty" gorm:"-"`
}

// Menu is a navigation menu assigned to a theme location.
type Menu struct {
	ID        uint       `json:"id" gorm:"primaryKey"`
	Name      string     `json:"name" gorm:"size:120"`
	Slug      string     `json:"slug" gorm:"uniqueIndex;size:160"`
	Location  string     `json:"location" gorm:"size:40;index"` // header, footer, mobile, "" = unassigned
	Items     []MenuItem `json:"items" gorm:"foreignKey:MenuID"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// MenuItem is one entry; Type decides how URL is resolved when rendering:
// home, page, post, category, tag, shop, custom.
type MenuItem struct {
	ID        uint       `json:"id" gorm:"primaryKey"`
	MenuID    uint       `json:"menu_id" gorm:"index"`
	ParentID  *uint      `json:"parent_id" gorm:"index"`
	Position  int        `json:"position" gorm:"index"`
	Label     string     `json:"label" gorm:"size:120"`
	Type      string     `json:"type" gorm:"size:20"`
	RefID     uint       `json:"ref_id"` // id of the page/post/category/tag for typed items
	URL       string     `json:"url"`    // custom items
	Icon      string     `json:"icon" gorm:"size:40"`
	NewTab    bool       `json:"new_tab"`
	LoginOnly bool       `json:"login_only"`
	CSSClass  string     `json:"css_class" gorm:"size:120"`
	Children  []MenuItem `json:"children,omitempty" gorm:"-"`
}

// MenuNode is what themes receive from the menu template function.
type MenuNode struct {
	Label    string
	URL      string
	Icon     string
	NewTab   bool
	Active   bool
	CSSClass string
	Children []MenuNode
}

// Revision is a snapshot of a post. Kind is autosave, manual, publish or
// restore. Only the newest RevisionAutosaveKeep autosaves are kept per post.
type Revision struct {
	ID        uint            `json:"id" gorm:"primaryKey"`
	PostID    uint            `json:"post_id" gorm:"index"`
	UserID    uint            `json:"user_id"`
	User      User            `json:"user" gorm:"foreignKey:UserID"`
	Kind      string          `json:"kind" gorm:"size:20;index"`
	Title     string          `json:"title"`
	Content   string          `json:"content" gorm:"type:text"`
	Excerpt   string          `json:"excerpt" gorm:"type:text"`
	Meta      json.RawMessage `json:"meta" gorm:"type:jsonb"`
	Added     int             `json:"added"`                   // lines added versus the previous revision
	Removed   int             `json:"removed"`                 // lines removed versus the previous revision
	Source    string          `json:"source" gorm:"type:text"` // Markdown or Jelly MD source (ticket 09)
	Format    string          `json:"format" gorm:"size:8"`    // html, md or jmd
	CreatedAt time.Time       `json:"created_at"`
}

// RevisionAutosaveKeep bounds autosaves per post.
const RevisionAutosaveKeep = 20

// TrashRetentionDays is the default number of days before trashed items are
// purged. The option key trash_retention_days overrides it.
const TrashRetentionDays = 30

// TrashItem is one row of the recycle bin across kinds.
type TrashItem struct {
	Kind        string    `json:"kind"` // post, page, product, comment
	ID          uint      `json:"id"`
	Title       string    `json:"title"`
	DeletedAt   time.Time `json:"deleted_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	DeletedByID uint      `json:"deleted_by_id"`
	DeletedBy   string    `json:"deleted_by"`
}

var slugDashes = regexp.MustCompile(`-{2,}`)

// Slugify lowercases s, turns whitespace and underscores into dashes and
// drops everything that is not a letter, number or dash. Unicode letters
// (including CJK) are kept so Chinese slugs stay readable. Empty input yields
// fallback.
func Slugify(s, fallback string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			b.WriteRune(r)
		case unicode.IsSpace(r) || r == '_' || r == '-' || r == '/':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(slugDashes.ReplaceAllString(b.String(), "-"), "-")
	if out == "" {
		return fallback
	}
	return out
}
