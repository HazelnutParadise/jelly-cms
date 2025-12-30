package core

import "encoding/json"

// ThemeConfig represents the theme.json configuration.
type ThemeConfig struct {
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description string            `json:"description"`
	Author      string            `json:"author"`
	Parent      string            `json:"parent,omitempty"` // For child themes
	Colors      ThemeColors       `json:"colors,omitempty"` // Default color scheme
	Layout      ThemeLayout       `json:"layout,omitempty"` // Default layout settings
	CustomFields []ThemeField     `json:"custom_fields,omitempty"` // Customizable fields
}

// ThemeColors defines the color scheme for a theme
type ThemeColors struct {
	Primary      string `json:"primary"`       // Primary color
	Secondary    string `json:"secondary"`    // Secondary color
	Background   string `json:"background"`   // Background color
	Text         string `json:"text"`          // Text color
	Accent       string `json:"accent"`        // Accent color
	Link         string `json:"link"`          // Link color
	LinkHover    string `json:"link_hover"`    // Link hover color
	Border       string `json:"border"`        // Border color
	Success      string `json:"success"`      // Success color
	Warning      string `json:"warning"`       // Warning color
	Error        string `json:"error"`         // Error color
}

// ThemeLayout defines layout settings for a theme
type ThemeLayout struct {
	HeaderStyle    string `json:"header_style"`     // fixed, sticky, static
	Sidebar        bool   `json:"sidebar"`          // Show sidebar
	SidebarPosition string `json:"sidebar_position"` // left, right
	Footer         bool   `json:"footer"`           // Show footer
	ContainerWidth string `json:"container_width"`  // full, wide, narrow
	PostLayout     string `json:"post_layout"`      // single, grid, list
}

// ThemeField represents a customizable field in the theme
type ThemeField struct {
	ID          string      `json:"id"`
	Label       string      `json:"label"`
	Type        string      `json:"type"`        // text, color, number, select, checkbox, textarea
	Default     interface{} `json:"default"`
	Options     []string    `json:"options,omitempty"` // For select type
	Description string      `json:"description,omitempty"`
	Category    string      `json:"category,omitempty"` // colors, layout, typography, etc.
}

// ThemeSettings stores the current theme customization settings
type ThemeSettings struct {
	ThemeName string                 `json:"theme_name" gorm:"primaryKey"`
	Colors    json.RawMessage        `json:"colors" gorm:"type:jsonb"`
	Layout    json.RawMessage        `json:"layout" gorm:"type:jsonb"`
	Custom    json.RawMessage        `json:"custom" gorm:"type:jsonb"` // Custom field values
}

// ThemeManager handles theme loading and switching.
type ThemeManager interface {
	Load(name string) error
	Activate(name string) error
	GetActive() string
	Render(templateName string, data interface{}) ([]byte, error)
}
