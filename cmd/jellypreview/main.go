// Command jellypreview renders every admin page template to static HTML with
// fake data so the screens can be inspected (or screenshotted) without a
// database. It mirrors internal/server.renderAdminPage: the same template
// files in the same order and the same FuncMap.
//
//	go run ./cmd/jellypreview -out /tmp/preview
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"os"
	"path/filepath"
)

type crumb struct {
	Label string
	Href  string
}

type navEntry struct {
	Key    string
	Label  string
	Icon   string
	Href   string
	Active bool
	Badge  int
}

type navSection struct {
	Key   string
	Label string
	Items []navEntry
}

type viewer struct {
	ID       uint
	Name     string
	Initial  string
	Role     string
	Email    string
	Avatar   string
	IsAdmin  bool
	Unread   int
	Settings string
}

type errorPage struct {
	Code      int
	Title     string
	Message   string
	RequestID string
	Path      string
}

// page describes one preview output.
type page struct {
	out    string // output name: admin-<out>.html
	file   string // template under web/admin
	active string
	title  string
	crumbs []crumb
	data   map[string]interface{}
}

// adminFuncs is copied from internal/server/admin_render.go.
func adminFuncs() template.FuncMap {
	return template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		"dict": func(values ...interface{}) map[string]interface{} {
			dict := make(map[string]interface{})
			for i := 0; i+1 < len(values); i += 2 {
				key, _ := values[i].(string)
				dict[key] = values[i+1]
			}
			return dict
		},
		"list": func(items ...interface{}) []interface{} { return items },
		"json": func(v interface{}) template.JS {
			b, err := json.Marshal(v)
			if err != nil {
				return template.JS("null")
			}
			return template.JS(b)
		},
		"safeHTML": func(s string) template.HTML { return template.HTML(s) },
	}
}

func main() {
	out := flag.String("out", "", "output directory (required)")
	webDir := flag.String("web", "web", "path to the web directory")
	lang := flag.String("lang", "zh-TW", "locale file to use")
	flag.Parse()
	if *out == "" {
		flag.Usage()
		os.Exit(2)
	}

	t, err := loadLocale(filepath.Join(*webDir, "locales", *lang+".json"))
	if err != nil {
		log.Fatal(err)
	}
	adminDir := filepath.Join(*webDir, "admin")
	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	// Pages reference /admin/assets/*, which the real server serves statically.
	if err := copyDir(filepath.Join(adminDir, "assets"), filepath.Join(*out, "admin", "assets")); err != nil {
		log.Fatal(err)
	}

	pages := []page{
		{out: "index", file: "index.html", active: "dashboard"},
		{out: "posts", file: "posts.html", active: "posts"},
		{out: "pages", file: "pages.html", active: "pages"},
		{out: "taxonomy", file: "taxonomy.html", active: "taxonomy"},
		{out: "menus", file: "menus.html", active: "menus"},
		{out: "products", file: "products.html", active: "products"},
		{out: "orders", file: "orders.html", active: "orders"},
		{out: "media", file: "media.html", active: "media"},
		{out: "themes", file: "themes.html", active: "themes"},
		{out: "plugins", file: "plugins.html", active: "plugins"},
		{out: "settings", file: "settings.html", active: "settings"},
		{out: "post_editor", file: "post_editor.html", active: "posts", crumbs: []crumb{{t["posts"], "/admin/posts"}, {"為什麼我們決定用 Go 重寫一個 CMS", ""}}},
		{out: "page_editor", file: "page_editor.html", active: "pages", crumbs: []crumb{{t["pages"], "/admin/pages"}, {t["createPage"], ""}}},
		{out: "product_editor", file: "product_editor.html", active: "products", crumbs: []crumb{{t["products"], "/admin/products"}, {t["createProduct"], ""}}},
		{out: "error-404", file: "error.html", data: map[string]interface{}{"Error": errorPage{Code: 404, Title: "Not Found", Message: "Not Found", Path: "/admin/posts/9999"}}},
		{out: "error-500", file: "error.html", data: map[string]interface{}{"Error": errorPage{Code: 500, Title: "Internal Server Error", Message: "Internal Server Error", RequestID: "req_8f3a21", Path: "/admin/posts"}}},
		{out: "error-403", file: "error.html", data: map[string]interface{}{"Error": errorPage{Code: 403, Title: "Forbidden", Message: "Forbidden", Path: "/admin/settings"}, "_role": "editor"}},
	}
	// "error" is the States board default: the 500 screen.
	pages = append(pages, page{out: "error", file: "error.html", data: map[string]interface{}{"Error": errorPage{Code: 500, Title: "Internal Server Error", Message: "Internal Server Error", RequestID: "req_8f3a21", Path: "/admin/posts"}}})

	for _, p := range pages {
		path := filepath.Join(*out, "admin-"+p.out+".html")
		if err := render(adminDir, path, p, t, *lang); err != nil {
			log.Fatalf("%s: %v", p.out, err)
		}
		fmt.Println("wrote", path)
	}
}

func render(adminDir, path string, p page, t map[string]string, lang string) error {
	tmpl, err := template.New("base").Funcs(adminFuncs()).ParseFiles(
		filepath.Join(adminDir, "components.html"),
		filepath.Join(adminDir, "layout.html"),
		filepath.Join(adminDir, p.file),
	)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	v := viewer{ID: 1, Name: "Hazel", Initial: "H", Role: "admin", Email: "hazel@example.com", IsAdmin: true, Unread: 3}
	data := map[string]interface{}{
		"Active":    p.active,
		"Lang":      lang,
		"T":         t,
		"Nav":       fakeNav(t, p.active),
		"Viewer":    v,
		"Crumbs":    p.crumbs,
		"Title":     p.title,
		"SiteTitle": "Jelly CMS",
	}
	for k, val := range p.data {
		if k == "_role" {
			v.Role, v.IsAdmin = val.(string), false
			data["Viewer"] = v
			continue
		}
		data[k] = val
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return tmpl.ExecuteTemplate(f, "base", data)
}

func fakeNav(t map[string]string, active string) []navSection {
	e := func(key, icon, href string, badge int) navEntry {
		return navEntry{Key: key, Label: tr(t, key), Icon: icon, Href: href, Active: key == active, Badge: badge}
	}
	return []navSection{
		{Key: "content", Label: tr(t, "content"), Items: []navEntry{
			e("dashboard", "i-grid", "/admin", 0),
			e("posts", "i-file-text", "/admin/posts", 0),
			e("pages", "i-layout", "/admin/pages", 0),
			e("taxonomy", "i-tag", "/admin/taxonomy", 0),
			{Key: "comments", Label: "留言", Icon: "i-comment", Href: "/admin/comments", Active: active == "comments", Badge: 7},
			e("media", "i-image", "/admin/media", 0),
		}},
		{Key: "shop", Label: tr(t, "shop"), Items: []navEntry{
			e("products", "i-box", "/admin/products", 0),
			e("orders", "i-cart", "/admin/orders", 3),
		}},
		{Key: "appearance", Label: tr(t, "appearance"), Items: []navEntry{
			e("themes", "i-layers", "/admin/themes", 0),
			e("menus", "i-menu", "/admin/menus", 0),
			e("plugins", "i-plug", "/admin/plugins", 0),
		}},
		{Key: "system", Label: tr(t, "system"), Items: []navEntry{
			e("settings", "i-gear", "/admin/settings", 0),
		}},
	}
}

func tr(t map[string]string, key string) string {
	if v := t[key]; v != "" {
		return v
	}
	return key
}

func loadLocale(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		o, err := os.Create(target)
		if err != nil {
			return err
		}
		defer o.Close()
		_, err = io.Copy(o, in)
		return err
	})
}
