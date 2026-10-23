package server

// Storefront catalog integration tests for ticket 07, section E3 (contract: 公開路由 in
// docs/tickets/07-shop.md). They render the public theme through archivesHarness and run against the
// dedicated PostgreSQL database; they are skipped unless JELLY_TEST_POSTGRES=1. They are written before the
// implementation and must fail until it exists (/shop and /shop/c/:slug currently fall through to the
// catch-all and answer 404). Do not edit them to match an implementation.
//
// Money: fixtures store minor units (12000 = NT$120). Pages show whole NT$ such as "NT$120". Assertions
// check rendered text and status codes; product links are read from href="/shop/p/<slug>".

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

var (
	shopCatalogProductHref = regexp.MustCompile(`href="/shop/p/([^"?#]+)"`)
	shopCatalogTag         = regexp.MustCompile(`<[^>]+>`)
	shopCatalogJSONLD      = regexp.MustCompile(`(?s)<script[^>]*type="application/ld\+json"[^>]*>(.*?)</script>`)
)

type shopCatalogVariant struct {
	name      string
	price     int64
	compareAt int64
	stock     int
}

type shopCatalogProduct struct {
	title      string
	slug       string
	status     string
	price      int64
	compareAt  int64
	stock      int
	soldCount  int64
	createdAt  time.Time
	desc       string
	variants   []shopCatalogVariant
	categories []core.ProductCategory
}

func shopCatalogSeedCategory(t *testing.T, h *accessHarness, name, slug string, parentID *uint, hidden bool) core.ProductCategory {
	t.Helper()
	row := core.ProductCategory{Name: name, Slug: slug, ParentID: parentID, Hidden: hidden}
	if err := h.db.Create(&row).Error; err != nil {
		t.Fatalf("seed product category %q: %v", slug, err)
	}
	return row
}

// shopCatalogSeedProduct inserts a product. Without variants it gets one default variant that mirrors the
// product's price and stock.
func shopCatalogSeedProduct(t *testing.T, h *accessHarness, spec shopCatalogProduct) core.Product {
	t.Helper()
	status := spec.status
	if status == "" {
		status = "active"
	}
	created := spec.createdAt
	if created.IsZero() {
		created = time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	}
	product := core.Product{
		Title:             spec.title,
		Slug:              spec.slug,
		Description:       spec.desc,
		Status:            status,
		Price:             spec.price,
		CompareAtPrice:    spec.compareAt,
		Stock:             spec.stock,
		LowStockThreshold: 5,
		SoldCount:         spec.soldCount,
		CreatedAt:         created,
		UpdatedAt:         created,
	}
	if err := h.db.Create(&product).Error; err != nil {
		t.Fatalf("seed product %q: %v", spec.slug, err)
	}
	variants := spec.variants
	if len(variants) == 0 {
		variants = []shopCatalogVariant{{price: spec.price, compareAt: spec.compareAt, stock: spec.stock}}
	}
	for i, v := range variants {
		row := core.ProductVariant{
			ProductID: product.ID, Name: v.name, Price: v.price, CompareAtPrice: v.compareAt,
			Stock: v.stock, Position: i, Status: "active",
		}
		if err := h.db.Create(&row).Error; err != nil {
			t.Fatalf("seed variant %q of %q: %v", v.name, spec.slug, err)
		}
	}
	// Product categories are linked through product_category_links; Product has no relation field.
	for _, category := range spec.categories {
		if err := h.db.Exec("INSERT INTO product_category_links (product_id, category_id) VALUES (?, ?)", product.ID, category.ID).Error; err != nil {
			t.Fatalf("assign category %q to %q: %v", category.Slug, spec.slug, err)
		}
	}
	return product
}

func shopCatalogGet(t *testing.T, h *accessHarness, path string) accessResult {
	t.Helper()
	r := h.request(http.MethodGet, path, "", "", "text/html", "")
	h.mustNoPanic(r, "GET "+path)
	return r
}

func shopCatalogWantStatus(t *testing.T, r accessResult, path string, want int) {
	t.Helper()
	if r.code != want {
		t.Fatalf("GET %s returned %d, want %d", path, r.code, want)
	}
}

// shopCatalogSlugs returns the product slugs linked from a page, in order of first appearance.
func shopCatalogSlugs(body string) []string {
	seen := map[string]bool{}
	var slugs []string
	for _, match := range shopCatalogProductHref.FindAllStringSubmatch(body, -1) {
		if !seen[match[1]] {
			seen[match[1]] = true
			slugs = append(slugs, match[1])
		}
	}
	return slugs
}

// shopCatalogOrder returns the given slugs in the order the page lists them.
func shopCatalogOrder(body string, slugs []string) []string {
	want := make(map[string]bool, len(slugs))
	for _, slug := range slugs {
		want[slug] = true
	}
	var ordered []string
	for _, slug := range shopCatalogSlugs(body) {
		if want[slug] {
			ordered = append(ordered, slug)
		}
	}
	return ordered
}

func shopCatalogStripTags(s string) string {
	return strings.Join(strings.Fields(shopCatalogTag.ReplaceAllString(s, " ")), " ")
}

// shopCatalogAnchorText returns the visible text of the first link to href.
func shopCatalogAnchorText(body, href string) (string, bool) {
	re := regexp.MustCompile(`(?s)<a\b[^>]*href="` + regexp.QuoteMeta(href) + `"[^>]*>(.*?)</a>`)
	match := re.FindStringSubmatch(body)
	if match == nil {
		return "", false
	}
	return shopCatalogStripTags(match[1]), true
}

func TestShopListsActiveProductsOnly(t *testing.T) {
	h := archivesHarness(t)
	shopCatalogSeedProduct(t, h, shopCatalogProduct{title: "Visible Teapot", slug: "visible-teapot", price: 12000, stock: 5})
	draft := shopCatalogSeedProduct(t, h, shopCatalogProduct{title: "Draft Sketch Pot", slug: "draft-sketch-pot", status: "draft", price: 9000, stock: 5})
	archived := shopCatalogSeedProduct(t, h, shopCatalogProduct{title: "Archived Pot Relic", slug: "archived-pot-relic", status: "archived", price: 9000, stock: 5})
	trashed := shopCatalogSeedProduct(t, h, shopCatalogProduct{title: "Trashed Pot Relic", slug: "trashed-pot-relic", price: 9000, stock: 5})
	if err := h.db.Delete(&trashed).Error; err != nil {
		t.Fatalf("trash product: %v", err)
	}

	list := shopCatalogGet(t, h, "/shop")
	shopCatalogWantStatus(t, list, "/shop", http.StatusOK)
	if !strings.Contains(list.body, "Visible Teapot") {
		t.Error("/shop does not list the active product Visible Teapot")
	}
	if !strings.Contains(list.body, "加入") {
		t.Error("/shop product card does not show the add-to-cart action 加入")
	}
	for _, title := range []string{"Draft Sketch Pot", "Archived Pot Relic", "Trashed Pot Relic"} {
		if strings.Contains(list.body, title) {
			t.Errorf("/shop lists %q, which must not be listed", title)
		}
	}

	for _, p := range []core.Product{draft, archived, trashed} {
		path := "/shop/p/" + p.Slug
		shopCatalogWantStatus(t, shopCatalogGet(t, h, path), path, http.StatusNotFound)
	}
	shopCatalogWantStatus(t, shopCatalogGet(t, h, "/shop/p/visible-teapot"), "/shop/p/visible-teapot", http.StatusOK)
}

func TestShopPaginationTwelvePerPage(t *testing.T) {
	h := archivesHarness(t)
	var all []string
	for i := 1; i <= 13; i++ {
		slug := fmt.Sprintf("pager-item-%02d", i)
		shopCatalogSeedProduct(t, h, shopCatalogProduct{
			title: fmt.Sprintf("Pager Item %02d", i), slug: slug, price: 10000, stock: 3,
			createdAt: time.Date(2026, time.January, i, 12, 0, 0, 0, time.UTC),
		})
		all = append(all, slug)
	}

	first := shopCatalogGet(t, h, "/shop?page=1")
	shopCatalogWantStatus(t, first, "/shop?page=1", http.StatusOK)
	firstSlugs := shopCatalogSlugs(first.body)
	if len(firstSlugs) != 12 {
		t.Errorf("page 1 lists %d products, want 12: %v", len(firstSlugs), firstSlugs)
	}

	second := shopCatalogGet(t, h, "/shop?page=2")
	shopCatalogWantStatus(t, second, "/shop?page=2", http.StatusOK)
	secondSlugs := shopCatalogSlugs(second.body)
	if len(secondSlugs) != 1 {
		t.Errorf("page 2 lists %d products, want 1: %v", len(secondSlugs), secondSlugs)
	}

	union := map[string]bool{}
	for _, slug := range append(append([]string{}, firstSlugs...), secondSlugs...) {
		union[slug] = true
	}
	for _, slug := range all {
		if !union[slug] {
			t.Errorf("product %s is on neither page 1 nor page 2", slug)
		}
	}

	shopCatalogWantStatus(t, shopCatalogGet(t, h, "/shop?page=3"), "/shop?page=3", http.StatusNotFound)

	for _, path := range []string{"/shop?page=0", "/shop?page=abc"} {
		r := shopCatalogGet(t, h, path)
		shopCatalogWantStatus(t, r, path, http.StatusOK)
		if strings.Join(shopCatalogSlugs(r.body), ",") != strings.Join(firstSlugs, ",") {
			t.Errorf("%s did not show page 1; got %v want %v", path, shopCatalogSlugs(r.body), firstSlugs)
		}
	}
}

func TestShopFiltersPriceStockAndSort(t *testing.T) {
	h := archivesHarness(t)
	shopCatalogSeedProduct(t, h, shopCatalogProduct{title: "Filter Mug Cheap", slug: "filter-mug-cheap",
		price: 15000, stock: 5, soldCount: 3, createdAt: time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)})
	shopCatalogSeedProduct(t, h, shopCatalogProduct{title: "Filter Cup Sold Out", slug: "filter-cup-sold-out",
		price: 16000, stock: 0, soldCount: 7, createdAt: time.Date(2026, time.April, 1, 12, 0, 0, 0, time.UTC)})
	shopCatalogSeedProduct(t, h, shopCatalogProduct{title: "Filter Vase Middle", slug: "filter-vase-middle",
		price: 50000, stock: 8, soldCount: 10, createdAt: time.Date(2026, time.February, 1, 12, 0, 0, 0, time.UTC)})
	shopCatalogSeedProduct(t, h, shopCatalogProduct{title: "Filter Lamp Premium", slug: "filter-lamp-premium",
		price: 120000, stock: 2, soldCount: 1, createdAt: time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)})

	ours := []string{"filter-mug-cheap", "filter-cup-sold-out", "filter-vase-middle", "filter-lamp-premium"}
	members := func(path string) map[string]bool {
		t.Helper()
		r := shopCatalogGet(t, h, path)
		shopCatalogWantStatus(t, r, path, http.StatusOK)
		found := map[string]bool{}
		for _, slug := range shopCatalogOrder(r.body, ours) {
			found[slug] = true
		}
		return found
	}
	wantMembers := func(path string, want ...string) {
		t.Helper()
		got := members(path)
		if len(got) != len(want) {
			t.Errorf("%s shows %v, want exactly %v", path, got, want)
		}
		for _, slug := range want {
			if !got[slug] {
				t.Errorf("%s is missing %s", path, slug)
			}
		}
	}

	wantMembers("/shop?price=lt300", "filter-mug-cheap", "filter-cup-sold-out")
	wantMembers("/shop?price=300-800", "filter-vase-middle")
	wantMembers("/shop?price=gt800", "filter-lamp-premium")
	wantMembers("/shop?instock=1", "filter-mug-cheap", "filter-vase-middle", "filter-lamp-premium")
	wantMembers("/shop?instock=1&price=lt300", "filter-mug-cheap")

	order := func(path string) string {
		t.Helper()
		r := shopCatalogGet(t, h, path)
		shopCatalogWantStatus(t, r, path, http.StatusOK)
		return strings.Join(shopCatalogOrder(r.body, ours), ",")
	}
	checks := []struct {
		path string
		want []string
	}{
		{"/shop?sort=price_asc", []string{"filter-mug-cheap", "filter-cup-sold-out", "filter-vase-middle", "filter-lamp-premium"}},
		{"/shop?sort=price_desc", []string{"filter-lamp-premium", "filter-vase-middle", "filter-cup-sold-out", "filter-mug-cheap"}},
		{"/shop?sort=new", []string{"filter-cup-sold-out", "filter-lamp-premium", "filter-vase-middle", "filter-mug-cheap"}},
		{"/shop?sort=popular", []string{"filter-vase-middle", "filter-cup-sold-out", "filter-mug-cheap", "filter-lamp-premium"}},
	}
	for _, tc := range checks {
		if got, want := order(tc.path), strings.Join(tc.want, ","); got != want {
			t.Errorf("%s order = %s, want %s", tc.path, got, want)
		}
	}
}

func TestShopCategoryPageBreadcrumbsChildrenAndCascade(t *testing.T) {
	h := archivesHarness(t)
	parent := shopCatalogSeedCategory(t, h, "Kitchen Ware", "kitchen-ware", nil, false)
	child := shopCatalogSeedCategory(t, h, "Tea Cups", "tea-cups", &parent.ID, false)
	shopCatalogSeedProduct(t, h, shopCatalogProduct{title: "Parent Only Kettle", slug: "parent-only-kettle",
		price: 20000, stock: 4, categories: []core.ProductCategory{parent}})
	shopCatalogSeedProduct(t, h, shopCatalogProduct{title: "Child Green Cup", slug: "child-green-cup",
		price: 6000, stock: 4, categories: []core.ProductCategory{child}})
	shopCatalogSeedProduct(t, h, shopCatalogProduct{title: "Child Black Cup", slug: "child-black-cup",
		price: 7000, stock: 4, categories: []core.ProductCategory{child}})

	parentPage := shopCatalogGet(t, h, "/shop/c/kitchen-ware")
	shopCatalogWantStatus(t, parentPage, "/shop/c/kitchen-ware", http.StatusOK)
	for _, title := range []string{"Parent Only Kettle", "Child Green Cup", "Child Black Cup"} {
		if !strings.Contains(parentPage.body, title) {
			t.Errorf("parent category page does not cascade to include %q", title)
		}
	}
	chip, ok := shopCatalogAnchorText(parentPage.body, "/shop/c/tea-cups")
	if !ok {
		t.Error("parent category page has no child chip linking to /shop/c/tea-cups")
	} else {
		if !strings.Contains(chip, "Tea Cups") {
			t.Errorf("child chip text = %q, want it to name Tea Cups", chip)
		}
		if !strings.Contains(chip, "2") {
			t.Errorf("child chip text = %q, want the count 2 for the two child products", chip)
		}
	}

	childPage := shopCatalogGet(t, h, "/shop/c/tea-cups")
	shopCatalogWantStatus(t, childPage, "/shop/c/tea-cups", http.StatusOK)
	if !strings.Contains(childPage.body, "Kitchen Ware") || !strings.Contains(childPage.body, "Tea Cups") {
		t.Error("child category page breadcrumb must show Kitchen Ware and Tea Cups")
	}
	if !strings.Contains(childPage.body, `href="/shop/c/kitchen-ware"`) {
		t.Error("child category breadcrumb does not link back to /shop/c/kitchen-ware")
	}
	if strings.Contains(childPage.body, "Parent Only Kettle") {
		t.Error("child category page lists a product that is only in the parent category")
	}

	shopCatalogWantStatus(t, shopCatalogGet(t, h, "/shop/c/no-such-category"), "/shop/c/no-such-category", http.StatusNotFound)
}

func TestShopCategoryHiddenExcludedFromListsButReachable(t *testing.T) {
	h := archivesHarness(t)
	hidden := shopCatalogSeedCategory(t, h, "Hidden Gear", "hidden-gear", nil, true)
	shopCatalogSeedProduct(t, h, shopCatalogProduct{title: "Hidden Gear Item", slug: "hidden-gear-item",
		price: 9900, stock: 2, categories: []core.ProductCategory{hidden}})

	list := shopCatalogGet(t, h, "/shop")
	shopCatalogWantStatus(t, list, "/shop", http.StatusOK)
	if strings.Contains(list.body, `href="/shop/c/hidden-gear"`) {
		t.Error("/shop links to the hidden category /shop/c/hidden-gear")
	}

	page := shopCatalogGet(t, h, "/shop/c/hidden-gear")
	shopCatalogWantStatus(t, page, "/shop/c/hidden-gear", http.StatusOK)
	if !strings.Contains(page.body, "Hidden Gear Item") {
		t.Error("hidden category page is reachable but does not list its product")
	}
}

func TestShopProductPageShowsVariantsPriceCompareAtAndStockStates(t *testing.T) {
	h := archivesHarness(t)
	shopCatalogSeedProduct(t, h, shopCatalogProduct{
		title: "Variant Teapot", slug: "variant-teapot", price: 12000, compareAt: 15000, stock: 57,
		variants: []shopCatalogVariant{
			{name: "Red 250ml", price: 12000, compareAt: 15000, stock: 50},
			{name: "Blue 350ml", price: 13000, stock: 2},
			{name: "Green 500ml", price: 14000, stock: 0},
		},
	})

	r := shopCatalogGet(t, h, "/shop/p/variant-teapot")
	shopCatalogWantStatus(t, r, "/shop/p/variant-teapot", http.StatusOK)
	for _, name := range []string{"Red 250ml", "Blue 350ml", "Green 500ml"} {
		if !strings.Contains(r.body, name) {
			t.Errorf("product page does not offer variant %q", name)
		}
	}
	for _, text := range []string{"NT$120", "有貨", "剩 2", "售完"} {
		if !strings.Contains(r.body, text) {
			t.Errorf("product page is missing %q", text)
		}
	}
	strikethrough := regexp.MustCompile(`(?s)<(s|del)\b[^>]*>[^<]*NT\$150`)
	if !strikethrough.MatchString(r.body) {
		t.Error("product page does not show the original price NT$150 struck through (<s> or <del>)")
	}
}

func TestShopProductPageRendersMarkdownDescriptionSanitized(t *testing.T) {
	h := archivesHarness(t)
	shopCatalogSeedProduct(t, h, shopCatalogProduct{
		title: "Markdown Kettle", slug: "markdown-kettle", price: 12000, stock: 5,
		desc: "Intro line.\n\n- first item\n- second item\n\n<script>alert(1)</script>\n\n[bad link](javascript:alert(2))",
	})

	r := shopCatalogGet(t, h, "/shop/p/markdown-kettle")
	shopCatalogWantStatus(t, r, "/shop/p/markdown-kettle", http.StatusOK)
	if strings.Contains(r.body, "<script>alert") {
		t.Error("product description rendered the injected <script> element")
	}
	if strings.Contains(r.body, `href="javascript:`) {
		t.Error("product description rendered a javascript: link")
	}
	for _, item := range []string{"first item", "second item"} {
		pattern := regexp.MustCompile(`(?s)<li[^>]*>\s*(<p>\s*)?` + regexp.QuoteMeta(item))
		if !pattern.MatchString(r.body) {
			t.Errorf("Markdown bullet %q was not rendered as a list item", item)
		}
	}
}

func TestShopProductPageJSONLD(t *testing.T) {
	h := archivesHarness(t)
	shopCatalogSeedProduct(t, h, shopCatalogProduct{title: "Ld Kettle", slug: "ld-kettle", price: 12000, stock: 5})

	r := shopCatalogGet(t, h, "/shop/p/ld-kettle")
	shopCatalogWantStatus(t, r, "/shop/p/ld-kettle", http.StatusOK)

	var product map[string]any
	for _, match := range shopCatalogJSONLD.FindAllStringSubmatch(r.body, -1) {
		var node any
		if err := json.Unmarshal([]byte(match[1]), &node); err != nil {
			t.Fatalf("JSON-LD block is not valid JSON: %v; block=%s", err, match[1])
		}
		if object, ok := node.(map[string]any); ok && object["@type"] == "Product" {
			product = object
		}
	}
	if product == nil {
		t.Fatalf("product page has no application/ld+json block with @type Product")
	}
	offers, ok := product["offers"].(map[string]any)
	if !ok {
		t.Fatalf("JSON-LD offers = %v, want an object", product["offers"])
	}
	if price, ok := offers["price"].(string); !ok || price != "120" {
		t.Errorf("JSON-LD offers.price = %v (%T), want the string \"120\"", offers["price"], offers["price"])
	}
}

func TestShopPriceDisplayRegression(t *testing.T) {
	h := archivesHarness(t)
	shopCatalogSeedProduct(t, h, shopCatalogProduct{title: "Price Regress Lamp", slug: "price-regress-lamp", price: 12000, stock: 5})

	for _, path := range []string{"/shop", "/shop/p/price-regress-lamp"} {
		r := shopCatalogGet(t, h, path)
		shopCatalogWantStatus(t, r, path, http.StatusOK)
		if !strings.Contains(r.body, "NT$120") {
			t.Errorf("%s does not show the price NT$120", path)
		}
		if strings.Contains(r.body, "12,000") {
			t.Errorf("%s shows the minor-unit amount as 12,000", path)
		}
	}
}

func TestShopMenuItemResolves(t *testing.T) {
	h := archivesHarness(t)
	shopCatalogSeedProduct(t, h, shopCatalogProduct{title: "Menu Probe Kettle", slug: "menu-probe-kettle", price: 12000, stock: 5})
	menu := core.Menu{Name: "Main Menu", Slug: "main-menu", Location: "header"}
	if err := h.db.Create(&menu).Error; err != nil {
		t.Fatalf("seed header menu: %v", err)
	}
	item := core.MenuItem{MenuID: menu.ID, Position: 1, Label: "Shop Front Link", Type: "shop"}
	if err := h.db.Create(&item).Error; err != nil {
		t.Fatalf("seed shop menu item: %v", err)
	}

	r := shopCatalogGet(t, h, "/shop")
	shopCatalogWantStatus(t, r, "/shop", http.StatusOK)
	link := regexp.MustCompile(`(?s)<a\b[^>]*href="/shop"[^>]*>\s*(<[^>]+>\s*)*Shop Front Link`)
	if !link.MatchString(r.body) {
		t.Error("header does not render the shop menu item as a link to /shop")
	}
}

// TestShopPagesLoadNoExternalAssets checks the whole rendered page, which includes the head, the masthead
// and the footer, for scripts, stylesheets, fonts, images or CSS imports loaded from http(s) addresses.
func TestShopPagesLoadNoExternalAssets(t *testing.T) {
	h := archivesHarness(t)
	category := shopCatalogSeedCategory(t, h, "Asset Check", "asset-check", nil, false)
	shopCatalogSeedProduct(t, h, shopCatalogProduct{title: "Asset Probe Lamp", slug: "asset-probe-lamp",
		price: 12000, stock: 5, categories: []core.ProductCategory{category}})

	external := []*regexp.Regexp{
		regexp.MustCompile(`(?i)<script[^>]*\bsrc\s*=\s*["']?https?://`),
		regexp.MustCompile(`(?i)<link[^>]*\bhref\s*=\s*["']?https?://`),
		regexp.MustCompile(`(?i)<img[^>]*\bsrc\s*=\s*["']?https?://`),
		regexp.MustCompile(`(?i)@import\s+(url\(\s*)?["']?https?://`),
		regexp.MustCompile(`(?i)url\(\s*["']?https?://`),
	}
	for _, path := range []string{"/shop", "/shop/c/asset-check", "/shop/p/asset-probe-lamp"} {
		r := shopCatalogGet(t, h, path)
		shopCatalogWantStatus(t, r, path, http.StatusOK)
		for _, pattern := range external {
			if loc := pattern.FindStringIndex(r.body); loc != nil {
				t.Errorf("%s loads an external asset: %s", path, r.body[loc[0]:min(loc[1]+60, len(r.body))])
			}
		}
	}
}
