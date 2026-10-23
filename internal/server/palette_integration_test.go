package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

// paletteGet calls GET /api/admin/palette and decodes the 200 response.
func paletteGet(t *testing.T, h *accessHarness, token, query string) PaletteResponse {
	t.Helper()
	path := "/api/admin/palette"
	if query != "" {
		path += "?q=" + url.QueryEscape(query)
	}
	result := h.request(http.MethodGet, path, "", token, "", "")
	h.mustNoPanic(result, "GET "+path)
	if result.code != http.StatusOK {
		t.Fatalf("GET %s returned %d, want 200; body=%q", path, result.code, result.body)
	}
	var resp PaletteResponse
	if err := json.Unmarshal([]byte(result.body), &resp); err != nil {
		t.Fatalf("GET %s body is not palette JSON: %v; body=%q", path, err, result.body)
	}
	return resp
}

func paletteGroups(resp PaletteResponse) map[string][]PaletteItem {
	groups := map[string][]PaletteItem{}
	for _, group := range resp.Groups {
		groups[group.Key] = group.Items
	}
	return groups
}

func paletteSeedPost(t *testing.T, h *accessHarness, post core.Post) core.Post {
	t.Helper()
	if err := h.db.Create(&post).Error; err != nil {
		t.Fatalf("create post %q: %v", post.Title, err)
	}
	return post
}

func paletteItemLabels(items []PaletteItem) []string {
	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, item.Label)
	}
	return labels
}

func TestPaletteAnonymousRequestIsUnauthorizedJSON(t *testing.T) {
	h := newAccessHarness(t)
	result := h.request(http.MethodGet, "/api/admin/palette?q=x", "", "", "", "")
	h.mustNoPanic(result, "anonymous GET /api/admin/palette")
	if result.code != http.StatusUnauthorized {
		t.Fatalf("anonymous palette request returned %d, want 401; body=%q", result.code, result.body)
	}
	if ct := strings.ToLower(result.header.Get("Content-Type")); !strings.Contains(ct, "application/json") {
		t.Errorf("anonymous palette Content-Type = %q, want application/json", ct)
	}
}

func TestPaletteSearchScopesGroupsByRole(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "pal-admin")
	author := h.user("author", "pal-author")
	editor := h.user("editor", "pal-editor")

	pudding := paletteSeedPost(t, h, core.Post{Title: "焦糖布丁", Slug: "pal-pudding", Content: "pudding", Type: "post", Status: "published", AuthorID: author.ID})
	paletteSeedPost(t, h, core.Post{Title: "焦糖榛果醬", Slug: "pal-hazelnut", Content: "jam", Type: "post", Status: "published", AuthorID: admin.ID})
	paletteSeedPost(t, h, core.Post{Title: "關於焦糖", Slug: "pal-about", Content: "about", Type: "page", Status: "published", AuthorID: admin.ID})
	if err := h.db.Create(&core.Product{Title: "焦糖禮盒", Slug: "pal-gift-box", Price: 1200, SKU: "PAL-GIFT", Stock: 3, Status: "active"}).Error; err != nil {
		t.Fatalf("create product: %v", err)
	}

	t.Run("admin sees posts, pages and products", func(t *testing.T) {
		groups := paletteGroups(paletteGet(t, h, h.token(admin), "焦糖"))
		posts := paletteItemLabels(groups["posts"])
		if len(posts) != 2 {
			t.Fatalf("admin posts group = %v, want both posts", posts)
		}
		if len(groups["pages"]) != 1 {
			t.Errorf("admin pages group = %v, want one page", paletteItemLabels(groups["pages"]))
		}
		if len(groups["products"]) != 1 {
			t.Errorf("admin products group = %v, want one product", paletteItemLabels(groups["products"]))
		}
	})

	t.Run("author sees only own posts and no pages or products", func(t *testing.T) {
		groups := paletteGroups(paletteGet(t, h, h.token(author), "焦糖"))
		posts := groups["posts"]
		if len(posts) != 1 || posts[0].Label != "焦糖布丁" {
			t.Fatalf("author posts group = %v, want only 焦糖布丁", paletteItemLabels(posts))
		}
		if want := "/admin/posts/" + itoa(pudding.ID); posts[0].Href != want {
			t.Errorf("author post href = %q, want %q", posts[0].Href, want)
		}
		if _, ok := groups["pages"]; ok {
			t.Errorf("author saw a pages group: %v", paletteItemLabels(groups["pages"]))
		}
		if _, ok := groups["products"]; ok {
			t.Errorf("author saw a products group: %v", paletteItemLabels(groups["products"]))
		}
	})

	t.Run("editor sees posts and pages but no products", func(t *testing.T) {
		groups := paletteGroups(paletteGet(t, h, h.token(editor), "焦糖"))
		if len(groups["posts"]) == 0 {
			t.Error("editor posts group is empty, want posts")
		}
		if len(groups["pages"]) != 1 {
			t.Errorf("editor pages group = %v, want one page", paletteItemLabels(groups["pages"]))
		}
		if _, ok := groups["products"]; ok {
			t.Errorf("editor saw a products group: %v", paletteItemLabels(groups["products"]))
		}
	})
}

func TestPaletteEmptyQueryReturnsCommandsAndNavOnly(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "pal-empty-admin")
	paletteSeedPost(t, h, core.Post{Title: "焦糖布丁", Slug: "pal-empty-post", Content: "x", Type: "post", Status: "published", AuthorID: admin.ID})

	groups := paletteGroups(paletteGet(t, h, h.token(admin), ""))
	if _, ok := groups["commands"]; !ok {
		t.Error("empty query has no commands group")
	}
	if _, ok := groups["pages-nav"]; !ok {
		t.Error("empty query has no pages-nav group")
	}
	for _, key := range []string{"posts", "pages", "products"} {
		if _, ok := groups[key]; ok {
			t.Errorf("empty query returned content group %q", key)
		}
	}
}

func TestPaletteCommandsFilteredByRole(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "pal-cmd-admin")
	author := h.user("author", "pal-cmd-author")

	hrefsOf := func(token string) map[string]bool {
		hrefs := map[string]bool{}
		for _, item := range paletteGroups(paletteGet(t, h, token, ""))["commands"] {
			hrefs[item.Href] = true
		}
		return hrefs
	}

	authorHrefs := hrefsOf(h.token(author))
	if !authorHrefs["/admin/posts/new"] {
		t.Errorf("author commands = %v, want /admin/posts/new", authorHrefs)
	}
	if authorHrefs["/admin/products/new"] {
		t.Error("author commands include /admin/products/new, want it filtered out")
	}

	adminHrefs := hrefsOf(h.token(admin))
	if !adminHrefs["/admin/products/new"] {
		t.Errorf("admin commands = %v, want /admin/products/new", adminHrefs)
	}
}

func TestPaletteLimitsEachGroupToFiveItems(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "pal-limit-admin")
	for i := 1; i <= 7; i++ {
		paletteSeedPost(t, h, core.Post{
			Title:    "多筆文章 " + itoa(uint(i)),
			Slug:     "pal-many-" + itoa(uint(i)),
			Content:  "many",
			Type:     "post",
			Status:   "published",
			AuthorID: admin.ID,
		})
	}

	groups := paletteGroups(paletteGet(t, h, h.token(admin), "多筆"))
	posts := groups["posts"]
	if len(posts) != paletteLimit {
		t.Fatalf("posts group has %d items, want %d (paletteLimit); labels=%v", len(posts), paletteLimit, paletteItemLabels(posts))
	}
	for _, item := range posts {
		if !strings.Contains(item.Label, "多筆") {
			t.Errorf("unexpected palette item %q in posts group", item.Label)
		}
	}
}
