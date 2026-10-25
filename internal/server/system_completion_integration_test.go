package server

// Ticket 08 completion regressions. These tests exercise the real HTTP route table
// against the dedicated PostgreSQL harness. They intentionally cover the missing
// contracts without depending on client-rendered controls in the server response.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
	"github.com/labstack/echo/v4"
)

func TestSystemCompletionMailSettingsMaskPreserveAndValidate(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "system-completion-mail-admin")
	token := h.token(admin)

	initial := map[string]any{
		"mail_host": "smtp.first.example.test", "mail_port": 2525,
		"mail_user": "mailer", "mail_password_enc": "completion-secret-4831",
		"mail_from": "site@example.test", "mail_tls": "starttls", "mail_enabled": true,
	}
	put := productsAdminDo(t, h, http.MethodPut, "/api/admin/mail/settings", initial, token)
	productsAdminWantStatus(t, put, "PUT initial mail settings", http.StatusOK)

	get := h.request(http.MethodGet, "/api/admin/mail/settings", "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(get, "GET /api/admin/mail/settings")
	productsAdminWantStatus(t, get, "GET mail settings", http.StatusOK)
	var got any
	if err := json.Unmarshal([]byte(get.body), &got); err != nil {
		t.Fatalf("decode mail settings: %v; body=%q", err, get.body)
	}
	secretValue, ok := s08bFindKey(got, "mail_password_enc")
	if !ok {
		t.Fatalf("mail settings response has no mail_password_enc field: %s", get.body)
	}
	mask, ok := secretValue.(string)
	if !ok || mask == "" || strings.Contains(mask, "completion-secret-4831") || !strings.Contains(mask, "*") {
		t.Fatalf("mail password response = %v, want a non-empty masked value without plaintext", secretValue)
	}
	if host, _ := s08bFindKey(got, "mail_host"); host != "smtp.first.example.test" {
		t.Errorf("mail_host = %v, want smtp.first.example.test", host)
	}

	// The mask returned by GET is a display value. Sending it back during an
	// unrelated edit must retain the stored secret rather than replacing it.
	updated := map[string]any{
		"mail_host": "smtp.updated.example.test", "mail_port": 2526,
		"mail_user": "mailer-updated", "mail_password_enc": mask,
		"mail_from": "updated@example.test", "mail_tls": "starttls", "mail_enabled": true,
	}
	put = productsAdminDo(t, h, http.MethodPut, "/api/admin/mail/settings", updated, token)
	productsAdminWantStatus(t, put, "PUT mail settings with the returned mask", http.StatusOK)

	storedPassword := s08bOptionValue(t, h, "mail_password_enc")
	if storedPassword == "" || storedPassword == "completion-secret-4831" {
		t.Errorf("stored mail_password_enc is empty or plaintext: %q", storedPassword)
	}
	if plaintext := settings.Get(context.Background(), "mail_password_enc"); plaintext != "completion-secret-4831" {
		t.Errorf("mail password after masked-value update decrypts to %q, want original secret", plaintext)
	}
	get = h.request(http.MethodGet, "/api/admin/mail/settings", "", token, echo.MIMEApplicationJSON, "")
	productsAdminWantStatus(t, get, "GET mail settings after update", http.StatusOK)
	if err := json.Unmarshal([]byte(get.body), &got); err != nil {
		t.Fatalf("decode updated mail settings: %v", err)
	}
	if host, _ := s08bFindKey(got, "mail_host"); host != "smtp.updated.example.test" {
		t.Errorf("updated mail_host = %v", host)
	}
	if port, _ := s08bFindKey(got, "mail_port"); fmt.Sprint(port) != "2526" {
		t.Errorf("updated mail_port = %v, want 2526", port)
	}
	if after, _ := s08bFindKey(got, "mail_password_enc"); after != mask {
		t.Errorf("password mask changed after an unrelated edit: before=%v after=%v", mask, after)
	}

	// Omitting the secret is also a supported way to save other settings.
	omittedSecret := map[string]any{
		"mail_host": "smtp.omitted-secret.example.test", "mail_port": 2527,
		"mail_user": "mailer-updated", "mail_from": "updated@example.test",
		"mail_tls": "starttls", "mail_enabled": true,
	}
	put = productsAdminDo(t, h, http.MethodPut, "/api/admin/mail/settings", omittedSecret, token)
	productsAdminWantStatus(t, put, "PUT mail settings without a password field", http.StatusOK)
	if plaintext := settings.Get(context.Background(), "mail_password_enc"); plaintext != "completion-secret-4831" {
		t.Errorf("omitting mail_password_enc changed decrypted secret to %q", plaintext)
	}

	invalid := map[string]any{"mail_host": "must-not-save.example.test", "mail_port": 65536, "mail_user": "mailer-updated", "mail_from": "updated@example.test", "mail_tls": "starttls", "mail_enabled": true}
	put = productsAdminDo(t, h, http.MethodPut, "/api/admin/mail/settings", invalid, token)
	productsAdminWantStatus(t, put, "PUT mail settings with an out-of-range port", http.StatusBadRequest)
	if value := s08bOptionValue(t, h, "mail_host"); value != "smtp.omitted-secret.example.test" {
		t.Errorf("invalid mail update partially changed mail_host to %q", value)
	}
	if value := s08bOptionValue(t, h, "mail_port"); value != "2527" {
		t.Errorf("invalid mail update partially changed mail_port to %q", value)
	}
}

func TestSystemCompletionCommentsOptionsRemainWritable(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "system-completion-comments-admin")
	token := h.token(admin)
	for _, tc := range []struct{ key, value string }{
		{"comments_enabled", "false"},
		{"comments_registered_only", "true"},
	} {
		r := h.request(http.MethodPost, "/api/admin/settings", s08bJSON(t, map[string]string{tc.key: tc.value}), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
		h.mustNoPanic(r, "POST /api/admin/settings for "+tc.key)
		productsAdminWantStatus(t, r, "write registered comment setting "+tc.key, http.StatusOK)
		if got := s08bOptionValue(t, h, tc.key); got != tc.value {
			t.Errorf("stored %s = %q, want %q", tc.key, got, tc.value)
		}
	}
}

func TestSystemCompletionCategoryAndAuthorFeedsFilterPublicPosts(t *testing.T) {
	h := newAccessHarness(t)
	target := h.user("author", "system-completion-feed-author")
	other := h.user("author", "system-completion-other-author")
	category := core.Category{Name: "Feed Scope", Slug: "system-completion-feed-scope"}
	if err := h.db.Create(&category).Error; err != nil {
		t.Fatalf("create feed category: %v", err)
	}
	now := time.Now().UTC()
	s08bSeedPost(t, h, target, s08bPost{title: "Feed Visible Target", slug: "feed-visible-target", categories: []core.Category{category}, publishedAt: now.Add(-time.Hour)})
	s08bSeedPost(t, h, target, s08bPost{title: "Feed Draft Hidden", slug: "feed-draft-hidden", categories: []core.Category{category}, status: "draft", publishedAt: now.Add(-time.Hour)})
	s08bSeedPost(t, h, target, s08bPost{title: "Feed Private Hidden", slug: "feed-private-hidden", categories: []core.Category{category}, visibility: "private", publishedAt: now.Add(-time.Hour)})
	s08bSeedPost(t, h, target, s08bPost{title: "Feed Future Hidden", slug: "feed-future-hidden", categories: []core.Category{category}, publishedAt: now.Add(24 * time.Hour)})
	s08bSeedPost(t, h, other, s08bPost{title: "Feed Other Author Visible", slug: "feed-other-author-visible", publishedAt: now.Add(-time.Hour)})
	s08bSeedPost(t, h, target, s08bPost{title: "Feed Other Category Visible", slug: "feed-other-category-visible", publishedAt: now.Add(-time.Hour)})

	for _, tc := range []struct {
		path string
		want []string
		omit []string
	}{
		{
			path: "/c/" + category.Slug + "/feed.xml",
			want: []string{"Feed Visible Target"},
			omit: []string{"Feed Draft Hidden", "Feed Private Hidden", "Feed Future Hidden", "Feed Other Category Visible", "Feed Other Author Visible"},
		},
		{
			path: "/author/" + target.Username + "/feed.xml",
			want: []string{"Feed Visible Target", "Feed Other Category Visible"},
			omit: []string{"Feed Draft Hidden", "Feed Private Hidden", "Feed Future Hidden", "Feed Other Author Visible"},
		},
	} {
		r := s08bGet(h, tc.path, nil)
		s08bWant(t, "GET "+tc.path, r, http.StatusOK)
		for _, title := range tc.want {
			if !strings.Contains(r.body, title) {
				t.Errorf("%s feed omits visible entry %q; body=%q", tc.path, title, s08bSnippet(r.body))
			}
		}
		for _, title := range tc.omit {
			if strings.Contains(r.body, title) {
				t.Errorf("%s feed exposes out-of-scope entry %q", tc.path, title)
			}
		}
	}
	emptyCategory := core.Category{Name: "Empty Feed Scope", Slug: "system-completion-empty-feed-scope"}
	if err := h.db.Create(&emptyCategory).Error; err != nil {
		t.Fatalf("create empty feed category: %v", err)
	}
	emptyAuthor := h.user("author", "system-completion-empty-feed-author")
	for _, path := range []string{
		"/c/" + emptyCategory.Slug + "/feed.xml",
		"/author/" + emptyAuthor.Username + "/feed.xml",
	} {
		s08bWant(t, "GET valid empty feed "+path, s08bGet(h, path, nil), http.StatusOK)
	}
	for _, path := range []string{"/c/no-such-feed-category/feed.xml", "/author/no-such-feed-author/feed.xml"} {
		s08bWant(t, "GET unknown feed "+path, s08bGet(h, path, nil), http.StatusNotFound)
	}
}

func TestSystemCompletionAdminPagesUseLocalSystemRenderer(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "system-completion-pages-admin")
	token := h.token(admin)
	for _, tc := range []struct{ path, page string }{
		{"/admin/redirects", "redirects"},
		{"/admin/logs", "logs"},
		{"/admin/tools/backup", "backup"},
		{"/admin/tools/import", "import"},
		{"/admin/store", "store"},
		{"/admin/plugins/system-completion-plugin", "plugin"},
		{"/admin/media/policies", "media"},
	} {
		r := h.request(http.MethodGet, tc.path, "", token, "text/html", "")
		h.mustNoPanic(r, "GET "+tc.path)
		productsAdminWantStatus(t, r, "GET "+tc.path, http.StatusOK)
		if !strings.Contains(r.body, `id="systemRoot"`) || !strings.Contains(r.body, `data-page="`+tc.page+`"`) {
			t.Errorf("%s does not render the shared system page root for %q", tc.path, tc.page)
		}
		if !strings.Contains(r.body, `src="/admin/assets/system-admin.js"`) {
			t.Errorf("%s does not load the bundled system admin script", tc.path)
		}
		if strings.Contains(strings.ToLower(r.body), "placeholder.html") || strings.Contains(strings.ToLower(r.body), "not implemented") {
			t.Errorf("%s still renders placeholder content", tc.path)
		}
	}
}

func TestSystemCompletionProductSEOCreateUpdatePublicAndSitemap(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "system-completion-product-seo-admin")
	token := h.token(admin)
	control := core.Product{Title: "SEO Sitemap Control Product", Slug: "seo-sitemap-control-product", Status: "active", Price: 100, Stock: 1}
	if err := h.db.Create(&control).Error; err != nil {
		t.Fatalf("create indexable sitemap control product: %v", err)
	}
	created := productsAdminDo(t, h, http.MethodPost, "/api/admin/products", map[string]any{
		"title": "SEO Completion Product", "slug": "seo-completion-product", "description": "A product with explicit search metadata.",
		"status": "active", "price": "120", "seo_title": "SEO Product Title", "seo_description": "SEO product description.",
		"seo_index": "noindex", "seo_ai": "noai",
		"variants": []map[string]any{{"name": "Default", "price": "120", "stock": 4, "status": "active"}},
	}, token)
	productsAdminWantSuccess(t, created, "create product with SEO overrides")
	var createdProduct map[string]any
	productsAdminDecode(t, created, "created SEO product", &createdProduct)
	idValue, ok := s08bFindKey(createdProduct, "id")
	if !ok {
		t.Fatalf("created SEO product response has no id: %s", created.body)
	}
	idNumber, ok := idValue.(float64)
	if !ok || idNumber <= 0 {
		t.Fatalf("created SEO product id = %v, want a positive JSON number", idValue)
	}
	id := uint(idNumber)
	path := fmt.Sprintf("/api/admin/products/%d", id)
	read := productsAdminDo(t, h, http.MethodGet, path, nil, token)
	productsAdminWantStatus(t, read, "read created SEO product", http.StatusOK)
	var readProduct map[string]any
	productsAdminDecode(t, read, "read created SEO product", &readProduct)
	if value, _ := s08bFindKey(readProduct, "seo_index"); value != "noindex" {
		t.Errorf("created product seo_index = %v, want noindex", value)
	}
	if value, _ := s08bFindKey(readProduct, "seo_ai"); value != "noai" {
		t.Errorf("created product seo_ai = %v, want noai", value)
	}
	if value, _ := s08bFindKey(readProduct, "seo_title"); value != "SEO Product Title" {
		t.Errorf("created product seo_title = %v, want existing SEO title field", value)
	}
	if value, _ := s08bFindKey(readProduct, "seo_description"); value != "SEO product description." {
		t.Errorf("created product seo_description = %v, want existing SEO description field", value)
	}

	public := s08bGet(h, "/shop/p/seo-completion-product", nil)
	s08bWant(t, "GET SEO product", public, http.StatusOK)
	robotsHeader := strings.ToLower(public.header.Get("X-Robots-Tag"))
	if !strings.Contains(strings.ToLower(public.body), `<meta name="robots" content="noindex">`) || !strings.Contains(robotsHeader, "noindex") {
		t.Errorf("noindex product lacks public robots metadata/header: header=%q", public.header.Get("X-Robots-Tag"))
	}
	if !strings.Contains(robotsHeader, "noai") {
		t.Errorf("seo_ai=noai product lacks noai in X-Robots-Tag: %q", public.header.Get("X-Robots-Tag"))
	}
	part := s08bGet(h, "/sitemap-products-1.xml", nil)
	s08bWant(t, "GET product sitemap with noindex product", part, http.StatusOK)
	if !strings.Contains(part.body, "/shop/p/"+control.Slug) {
		t.Error("sitemap omits the indexable control product")
	}
	if strings.Contains(part.body, "seo-completion-product") {
		t.Error("sitemap includes product whose seo_index is noindex")
	}

	// Ordinary product edits that omit SEO fields must preserve both overrides.
	ordinaryUpdate := productsAdminDo(t, h, http.MethodPut, path, map[string]any{
		"title": "SEO Completion Product After Ordinary Edit", "slug": "seo-completion-product",
		"description": "An ordinary product edit.", "status": "active", "price": "120",
	}, token)
	productsAdminWantSuccess(t, ordinaryUpdate, "update product without SEO fields")
	read = productsAdminDo(t, h, http.MethodGet, path, nil, token)
	productsAdminWantStatus(t, read, "read product after ordinary update", http.StatusOK)
	productsAdminDecode(t, read, "read product after ordinary update", &readProduct)
	if value, _ := s08bFindKey(readProduct, "seo_index"); value != "noindex" {
		t.Errorf("ordinary update changed seo_index to %v, want noindex", value)
	}
	if value, _ := s08bFindKey(readProduct, "seo_ai"); value != "noai" {
		t.Errorf("ordinary update changed seo_ai to %v, want noai", value)
	}

	for _, tc := range []struct {
		name, key, value string
	}{
		{"invalid seo_index", "seo_index", "follow"},
		{"invalid seo_ai", "seo_ai", "noindex"},
	} {
		body := map[string]any{
			"title": "Invalid SEO Product Update", "slug": "seo-completion-product",
			"description": "Must not be saved.", "status": "active", "price": "120", tc.key: tc.value,
			"seo_index": "noindex", "seo_ai": "noai",
		}
		if tc.key == "seo_index" {
			body["seo_index"] = tc.value
		} else {
			body["seo_ai"] = tc.value
		}
		r := productsAdminDo(t, h, http.MethodPut, path, body, token)
		productsAdminWantStatus(t, r, tc.name, http.StatusBadRequest)
	}
	read = productsAdminDo(t, h, http.MethodGet, path, nil, token)
	productsAdminWantStatus(t, read, "read product after invalid SEO updates", http.StatusOK)
	productsAdminDecode(t, read, "read product after invalid SEO updates", &readProduct)
	if value, _ := s08bFindKey(readProduct, "seo_index"); value != "noindex" {
		t.Errorf("invalid SEO updates changed seo_index to %v, want noindex", value)
	}
	if value, _ := s08bFindKey(readProduct, "seo_ai"); value != "noai" {
		t.Errorf("invalid SEO updates changed seo_ai to %v, want noai", value)
	}

	updated := productsAdminDo(t, h, http.MethodPut, path, map[string]any{
		"title": "SEO Completion Product Updated", "slug": "seo-completion-product", "description": "Updated public description.",
		"status": "active", "price": "125", "seo_title": "Updated SEO Product Title", "seo_description": "Updated SEO product description.",
		"seo_index": "index", "seo_ai": "inherit",
		"variants": []map[string]any{{"name": "Default", "price": "125", "stock": 4, "status": "active"}},
	}, token)
	productsAdminWantSuccess(t, updated, "update product SEO overrides")
	read = productsAdminDo(t, h, http.MethodGet, path, nil, token)
	productsAdminWantStatus(t, read, "read updated SEO product", http.StatusOK)
	productsAdminDecode(t, read, "read updated SEO product", &readProduct)
	if value, _ := s08bFindKey(readProduct, "seo_index"); value != "index" {
		t.Errorf("updated product seo_index = %v, want index", value)
	}
	if value, _ := s08bFindKey(readProduct, "seo_ai"); value != "inherit" {
		t.Errorf("updated product seo_ai = %v, want inherit", value)
	}
	public = s08bGet(h, "/shop/p/seo-completion-product", nil)
	s08bWant(t, "GET updated SEO product", public, http.StatusOK)
	if strings.Contains(strings.ToLower(public.body), `<meta name="robots" content="noindex">`) || strings.Contains(strings.ToLower(public.header.Get("X-Robots-Tag")), "noindex") {
		t.Error("updated indexable product still advertises noindex")
	}
	if strings.Contains(strings.ToLower(public.header.Get("X-Robots-Tag")), "noai") {
		t.Errorf("seo_ai=inherit still advertises noai: %q", public.header.Get("X-Robots-Tag"))
	}
	part = s08bGet(h, "/sitemap-products-1.xml", nil)
	s08bWant(t, "GET product sitemap after index update", part, http.StatusOK)
	if !strings.Contains(part.body, "/shop/p/"+control.Slug) || !strings.Contains(part.body, "seo-completion-product") {
		t.Error("sitemap must include both the indexable control product and target after seo_index changed to index")
	}
}
