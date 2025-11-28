package core

// ThemeConfig represents the theme.json configuration.
type ThemeConfig struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Author      string `json:"author"`
	Parent      string `json:"parent,omitempty"` // For child themes
}

// ThemeManager handles theme loading and switching.
type ThemeManager interface {
	Load(name string) error
	Activate(name string) error
	GetActive() string
	Render(templateName string, data interface{}) ([]byte, error)
}
