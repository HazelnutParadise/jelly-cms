package server

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/labstack/echo/v4"
)

// PaletteCommand is an action the ⌘K palette can run. Features register
// their own commands next to their nav entries.
type PaletteCommand struct {
	Key string
	// Label is a translation key.
	Label string
	// Keywords are extra lowercase words that match the command.
	Keywords []string
	Icon     string
	Href     string
	// Shortcut is shown as a hint, e.g. "⌘N".
	Shortcut string
	Roles    []string
}

// PaletteItem is one row in a palette group.
type PaletteItem struct {
	Label string `json:"label"`
	Hint  string `json:"hint,omitempty"`
	Href  string `json:"href"`
	Icon  string `json:"icon,omitempty"`
	Badge string `json:"badge,omitempty"`
}

// PaletteGroup is one section of palette results.
type PaletteGroup struct {
	Key   string        `json:"key"`
	Label string        `json:"label"`
	Items []PaletteItem `json:"items"`
}

// PaletteResponse is the JSON body of GET /api/admin/palette.
type PaletteResponse struct {
	Query  string         `json:"query"`
	Groups []PaletteGroup `json:"groups"`
}

const paletteLimit = 5

var (
	paletteMu       sync.RWMutex
	paletteCommands []PaletteCommand
)

// RegisterPaletteCommands adds commands; the same Key replaces the earlier one.
func RegisterPaletteCommands(cmds ...PaletteCommand) {
	paletteMu.Lock()
	defer paletteMu.Unlock()
	for _, cmd := range cmds {
		replaced := false
		for i := range paletteCommands {
			if paletteCommands[i].Key == cmd.Key {
				paletteCommands[i] = cmd
				replaced = true
				break
			}
		}
		if !replaced {
			paletteCommands = append(paletteCommands, cmd)
		}
	}
}

func init() {
	RegisterPaletteCommands(
		PaletteCommand{Key: "new-post", Label: "createPost", Keywords: []string{"new post", "write", "文章"}, Icon: "i-plus", Href: "/admin/posts/new", Shortcut: "⌘N"},
		PaletteCommand{Key: "new-page", Label: "createPage", Keywords: []string{"new page", "頁面"}, Icon: "i-layout", Href: "/admin/pages/new", Roles: []string{"admin", "editor"}},
		PaletteCommand{Key: "new-product", Label: "createProduct", Keywords: []string{"new product", "商品", "產品"}, Icon: "i-box", Href: "/admin/products/new", Roles: []string{"admin"}},
		PaletteCommand{Key: "view-site", Label: "viewSite", Keywords: []string{"site", "home", "網站", "前台"}, Icon: "i-globe", Href: "/", Shortcut: "⌘⇧V"},
	)
}

// registerPaletteRoutes mounts GET /api/admin/palette for every signed-in role.
func registerPaletteRoutes(e *echo.Echo) {
	e.GET("/api/admin/palette", handlePalette, auth.RequireRole("admin", "editor", "author"))
}

// handlePalette searches content titles and matches commands and nav pages.
// An empty query returns only commands and pages so the palette can open
// with something useful.
func handlePalette(c echo.Context) error {
	user, _ := auth.GetUserFromContext(c)
	lang, _ := c.Get("lang").(string)
	t := loadTranslations(lang)
	q := strings.TrimSpace(c.QueryParam("q"))
	lower := strings.ToLower(q)

	resp := PaletteResponse{Query: q, Groups: []PaletteGroup{}}

	if q != "" && data.DB != nil {
		if items := paletteContent(user, "post", q, "/admin/posts/", "/post/"); len(items) > 0 {
			resp.Groups = append(resp.Groups, PaletteGroup{Key: "posts", Label: translate(t, "posts"), Items: items})
		}
		if roleAllowed(user, []string{"admin", "editor"}) {
			if items := paletteContent(user, "page", q, "/admin/pages/", "/"); len(items) > 0 {
				resp.Groups = append(resp.Groups, PaletteGroup{Key: "pages", Label: translate(t, "pages"), Items: items})
			}
		}
		if roleAllowed(user, []string{"admin"}) {
			if items := paletteProducts(q); len(items) > 0 {
				resp.Groups = append(resp.Groups, PaletteGroup{Key: "products", Label: translate(t, "products"), Items: items})
			}
		}
	}

	if cmds := paletteMatchCommands(user, t, lower); len(cmds) > 0 {
		resp.Groups = append(resp.Groups, PaletteGroup{Key: "commands", Label: translate(t, "commands"), Items: cmds})
	}
	if pages := paletteMatchNav(c, user, t, lower); len(pages) > 0 {
		resp.Groups = append(resp.Groups, PaletteGroup{Key: "pages-nav", Label: translate(t, "goTo"), Items: pages})
	}
	return c.JSON(http.StatusOK, resp)
}

func paletteContent(user *core.User, kind, q, adminPrefix, publicPrefix string) []PaletteItem {
	var posts []core.Post
	query := data.DB.Where("type = ? AND title ILIKE ?", kind, "%"+q+"%")
	if user != nil && user.Role == "author" {
		query = query.Where("author_id = ?", user.ID)
	}
	if err := query.Order("updated_at DESC").Limit(paletteLimit).Find(&posts).Error; err != nil {
		return nil
	}
	items := make([]PaletteItem, 0, len(posts))
	for _, p := range posts {
		items = append(items, PaletteItem{
			Label: p.Title,
			Hint:  publicPrefix + p.Slug,
			Href:  adminPrefix + itoa(p.ID),
			Icon:  "i-file-text",
			Badge: p.Status,
		})
	}
	return items
}

func paletteProducts(q string) []PaletteItem {
	var products []core.Product
	if err := data.DB.Where("title ILIKE ?", "%"+q+"%").Order("updated_at DESC").Limit(paletteLimit).Find(&products).Error; err != nil {
		return nil
	}
	items := make([]PaletteItem, 0, len(products))
	for _, p := range products {
		items = append(items, PaletteItem{Label: p.Title, Hint: p.SKU, Href: "/admin/products/" + itoa(p.ID), Icon: "i-box", Badge: p.Status})
	}
	return items
}

func paletteMatchCommands(user *core.User, t map[string]string, lower string) []PaletteItem {
	paletteMu.RLock()
	cmds := make([]PaletteCommand, len(paletteCommands))
	copy(cmds, paletteCommands)
	paletteMu.RUnlock()

	var items []PaletteItem
	for _, cmd := range cmds {
		if !roleAllowed(user, cmd.Roles) {
			continue
		}
		label := translate(t, cmd.Label)
		if lower != "" && !paletteMatches(lower, label, cmd.Keywords) {
			continue
		}
		items = append(items, PaletteItem{Label: label, Hint: cmd.Shortcut, Href: cmd.Href, Icon: cmd.Icon})
	}
	return items
}

func paletteMatchNav(c echo.Context, user *core.User, t map[string]string, lower string) []PaletteItem {
	var items []PaletteItem
	for _, section := range navFor(c, user, "", t) {
		for _, entry := range section.Items {
			if lower != "" && !paletteMatches(lower, entry.Label, []string{entry.Key}) {
				continue
			}
			items = append(items, PaletteItem{Label: entry.Label, Hint: section.Label, Href: entry.Href, Icon: entry.Icon})
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Label < items[j].Label })
	if len(items) > 8 {
		items = items[:8]
	}
	return items
}

func paletteMatches(lower, label string, keywords []string) bool {
	if strings.Contains(strings.ToLower(label), lower) {
		return true
	}
	for _, k := range keywords {
		if strings.Contains(strings.ToLower(k), lower) {
			return true
		}
	}
	return false
}

func itoa(v uint) string {
	const digits = "0123456789"
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = digits[v%10]
		v /= 10
	}
	return string(buf[i:])
}
