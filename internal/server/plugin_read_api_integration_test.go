package server

// Plugin read API integration tests (docs/tickets/29-plugin-read-api-and-extensions.md, PR A, acceptance rows for
// content.read and shop.read). They run real host calls through the plugin runtime: a fixture plugin registers the
// action:read hook, each call invokes it with an operation name and an argument, and the callback logs the JSON
// result (read:<op>:<json>) or the thrown host error (read-error:<op>:<message>). The tests are skipped unless
// JELLY_TEST_POSTGRES=1. They must fail until plugin_host_read.go is implemented. Do not edit them to match an
// implementation.

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
)

const pluginReadFixtureID = "read-fixture"

// pluginReadScript routes one operation to the matching JellyCMS method. A host error is caught and logged so the
// test can read its message.
const pluginReadScript = `
JellyCMS.registerHook("action:read", function (data) {
  var name = data && data.op;
  try {
    var arg = data.arg;
    var result;
    if (name === "content.get") result = JellyCMS.content.get(arg);
    else if (name === "content.list") result = JellyCMS.content.list(arg);
    else if (name === "shop.getProduct") result = JellyCMS.shop.getProduct(arg);
    else if (name === "shop.listProducts") result = JellyCMS.shop.listProducts(arg);
    else if (name === "shop.getOrder") result = JellyCMS.shop.getOrder(arg);
    else if (name === "shop.listOrders") result = JellyCMS.shop.listOrders(arg);
    else throw new Error("unknown op " + name);
    JellyCMS.log.info("read:" + name + ":" + (result === undefined ? "undefined" : JSON.stringify(result)));
  } catch (e) {
    JellyCMS.log.info("read-error:" + name + ":" + String(e && e.message ? e.message : e));
  }
});
`

// pluginReadReply is the outcome of one action:read call: the JSON the callback logged, or the host error it logged.
type pluginReadReply struct {
	json string
	err  string
}

// pluginReadHarness returns the event harness with the read fixture installed with the given permission names. When
// granted is empty the enabled row carries the hash of the manifest permissions (a current approval); otherwise it
// carries granted verbatim.
func pluginReadHarness(t *testing.T, permissions []string, granted string) *pluginEventsHarness {
	t.Helper()
	h := newPluginEventsHarness(t)
	pluginReadInstall(t, h, permissions, granted)
	return h
}

func pluginReadInstall(t *testing.T, h *pluginEventsHarness, permissions []string, granted string) {
	t.Helper()
	meta := plugin.PluginMetadata{
		ID:         pluginReadFixtureID,
		Name:       "Read fixture",
		Version:    "1.0.0",
		Entrypoint: "index.js",
		Actions:    []plugin.ActionSpec{{ID: "read"}},
	}
	for _, name := range permissions {
		meta.Permissions = append(meta.Permissions, plugin.PermissionSpec{Name: name})
	}
	dir := filepath.Join("data", "plugins", pluginReadFixtureID)
	writeReloadPlugin(t, dir, meta, pluginReadScript)
	hash := granted
	if hash == "" {
		hash = plugin.PermissionNamesHash(plugin.ManifestPermissionNames(meta))
	}
	if err := h.db.Exec(`INSERT INTO plugins (id, kind, version, source, enabled, signature, permissions_granted_hash) VALUES (?, 'plugin', '1.0.0', 'upload', TRUE, 'unsigned', ?) ON CONFLICT (id) DO UPDATE SET enabled = TRUE, permissions_granted_hash = EXCLUDED.permissions_granted_hash`, pluginReadFixtureID, hash).Error; err != nil {
		t.Fatalf("insert plugins row for %s: %v", pluginReadFixtureID, err)
	}
	if err := h.runtime.Load(context.Background(), dir); err != nil {
		t.Fatalf("load read fixture: %v", err)
	}
}

// pluginReadCall invokes the fixture with one operation and waits for the callback's log line. The sink is searched
// only from the position it had before the call, so the line belongs to this call.
func pluginReadCall(t *testing.T, h *pluginEventsHarness, op string, arg any) pluginReadReply {
	t.Helper()
	before := len(h.sink.snapshot())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.runtime.Invoke(ctx, pluginReadFixtureID, plugin.Hook("action:read"), map[string]any{"op": op, "arg": arg}); err != nil {
		t.Fatalf("invoke action:read for %s: %v", op, err)
	}
	okPrefix := "read:" + op + ":"
	errPrefix := "read-error:" + op + ":"
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, line := range h.sink.snapshot()[before:] {
			if line.pluginID != pluginReadFixtureID {
				continue
			}
			if strings.HasPrefix(line.message, okPrefix) {
				return pluginReadReply{json: strings.TrimPrefix(line.message, okPrefix)}
			}
			if strings.HasPrefix(line.message, errPrefix) {
				return pluginReadReply{err: strings.TrimPrefix(line.message, errPrefix)}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no read result logged for %s; captured log:\n%s", op, h.sink.dump())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// pluginReadWantResult fails when the call raised a host error and returns the decoded JSON value.
func pluginReadWantResult(t *testing.T, reply pluginReadReply, where string) any {
	t.Helper()
	if reply.err != "" {
		t.Fatalf("%s: host error %q, want a result", where, reply.err)
	}
	if reply.json == "undefined" {
		t.Fatalf("%s returned undefined, want a JSON value (the host read method returned nothing)", where)
	}
	var value any
	if err := json.Unmarshal([]byte(reply.json), &value); err != nil {
		t.Fatalf("%s: decode result %q: %v", where, reply.json, err)
	}
	return value
}

// pluginReadWantObject returns the object result of a get call, failing on null or a host error.
func pluginReadWantObject(t *testing.T, reply pluginReadReply, where string) map[string]any {
	t.Helper()
	value := pluginReadWantResult(t, reply, where)
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s = %s, want an object", where, reply.json)
	}
	return object
}

// pluginReadWantNull fails unless the call returned JSON null without a host error.
func pluginReadWantNull(t *testing.T, reply pluginReadReply, where string) {
	t.Helper()
	if reply.err != "" {
		t.Fatalf("%s: host error %q, want null", where, reply.err)
	}
	if reply.json != "null" {
		t.Fatalf("%s = %s, want null", where, reply.json)
	}
}

// pluginReadWantError fails unless the call raised a host error whose message contains want.
func pluginReadWantError(t *testing.T, reply pluginReadReply, where, want string) {
	t.Helper()
	if reply.err == "" {
		t.Fatalf("%s returned %s, want a host error containing %q", where, reply.json, want)
	}
	if !strings.Contains(reply.err, want) {
		t.Fatalf("%s error = %q, want it to contain %q", where, reply.err, want)
	}
}

// pluginReadWantList decodes a list result into its envelope and items, failing on a host error or a bad shape.
func pluginReadWantList(t *testing.T, reply pluginReadReply, where string) (map[string]any, []map[string]any) {
	t.Helper()
	envelope := pluginReadWantObject(t, reply, where)
	raw, ok := envelope["items"].([]any)
	if !ok {
		t.Fatalf("%s has no items array: %s", where, reply.json)
	}
	items := make([]map[string]any, 0, len(raw))
	for _, value := range raw {
		item, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("%s item %v is not an object", where, value)
		}
		items = append(items, item)
	}
	return envelope, items
}

// pluginReadSeedPost inserts a post or page with optional categories. The published time is set only for published
// rows.
func pluginReadSeedPost(t *testing.T, h *pluginEventsHarness, author core.User, title, slug, status, postType, content string, published time.Time, categories ...core.Category) core.Post {
	t.Helper()
	post := core.Post{
		Title:      title,
		Slug:       slug,
		Content:    content,
		Type:       postType,
		Status:     status,
		AuthorID:   author.ID,
		Visibility: "public",
		Format:     "html",
	}
	if status == "published" {
		at := published
		post.PublishedAt = &at
	}
	if err := h.db.Create(&post).Error; err != nil {
		t.Fatalf("seed post %q: %v", slug, err)
	}
	for i := range categories {
		if err := h.db.Model(&post).Association("Categories").Append(&categories[i]); err != nil {
			t.Fatalf("assign category %q to %q: %v", categories[i].Slug, slug, err)
		}
	}
	return post
}

// pluginReadSeedCategory inserts a category row.
func pluginReadSeedCategory(t *testing.T, h *pluginEventsHarness, name, slug string) core.Category {
	t.Helper()
	row := core.Category{Name: name, Slug: slug}
	if err := h.db.Create(&row).Error; err != nil {
		t.Fatalf("seed category %q: %v", slug, err)
	}
	return row
}

// pluginReadCount returns the number of rows matching query on model; soft-deleted rows are excluded by GORM.
func pluginReadCount(t *testing.T, h *pluginEventsHarness, model any, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := h.db.Model(model).Where(query, args...).Count(&n).Error; err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func TestPluginReadAPIContentGetMatchesDatabase(t *testing.T) {
	h := pluginReadHarness(t, []string{"content.read"}, "")
	author := h.user("admin", "read-get-author")
	tech := pluginReadSeedCategory(t, h, "科技讀取", "tech-read")
	alpha := pluginReadSeedPost(t, h, author, "讀取關鍵字 Alpha", "read-alpha", "published", "post", "<p>alpha body</p>", time.Date(2026, time.March, 2, 9, 0, 0, 0, time.UTC), tech)

	var stored core.Post
	if err := h.db.Preload("Categories").First(&stored, alpha.ID).Error; err != nil {
		t.Fatalf("read seeded post: %v", err)
	}

	byID := pluginReadWantObject(t, pluginReadCall(t, h, "content.get", alpha.ID), "content.get(id)")
	if number(t, byID, "id") != float64(stored.ID) {
		t.Errorf("content.get(id).id = %v, want %d", byID["id"], stored.ID)
	}
	if byID["title"] != stored.Title || byID["slug"] != stored.Slug || byID["status"] != stored.Status || byID["type"] != stored.Type {
		t.Errorf("content.get(id) title/slug/status/type = %v/%v/%v/%v, want %q/%q/%q/%q", byID["title"], byID["slug"], byID["status"], byID["type"], stored.Title, stored.Slug, stored.Status, stored.Type)
	}
	if byID["content"] != stored.Content {
		t.Errorf("content.get(id).content = %v, want the stored body %q", byID["content"], stored.Content)
	}
	categories, ok := byID["categories"].([]any)
	if !ok || len(categories) != 1 {
		t.Fatalf("content.get(id).categories = %v, want one category", byID["categories"])
	}
	if first, _ := categories[0].(map[string]any); first["slug"] != "tech-read" {
		t.Errorf("content.get(id).categories[0].slug = %v, want tech-read", first["slug"])
	}

	bySlug := pluginReadWantObject(t, pluginReadCall(t, h, "content.get", stored.Slug), "content.get(slug)")
	if number(t, bySlug, "id") != float64(stored.ID) || bySlug["title"] != stored.Title {
		t.Errorf("content.get(slug) = %v, want the same post as id %d", bySlug, stored.ID)
	}

	draft := pluginReadSeedPost(t, h, author, "草稿可讀 Draft", "read-draft-get", "draft", "post", "<p>draft</p>", time.Time{})
	draftObject := pluginReadWantObject(t, pluginReadCall(t, h, "content.get", draft.ID), "content.get(draft id)")
	if draftObject["status"] != "draft" {
		t.Errorf("content.get(draft id).status = %v, want draft (drafts are readable)", draftObject["status"])
	}

	pluginReadWantNull(t, pluginReadCall(t, h, "content.get", 999999), "content.get(missing id)")
	pluginReadWantNull(t, pluginReadCall(t, h, "content.get", "no-such-post"), "content.get(missing slug)")

	trashed := pluginReadSeedPost(t, h, author, "回收桶 Trashed", "read-trashed", "published", "post", "<p>gone</p>", time.Date(2026, time.March, 3, 9, 0, 0, 0, time.UTC))
	if err := h.db.Delete(&trashed).Error; err != nil {
		t.Fatalf("move post to trash: %v", err)
	}
	pluginReadWantNull(t, pluginReadCall(t, h, "content.get", trashed.ID), "content.get(trashed id)")
	pluginReadWantNull(t, pluginReadCall(t, h, "content.get", trashed.Slug), "content.get(trashed slug)")
}

func TestPluginReadAPIContentListFiltersPagingAndClamp(t *testing.T) {
	h := pluginReadHarness(t, []string{"content.read"}, "")
	author := h.user("admin", "read-list-author")
	tech := pluginReadSeedCategory(t, h, "列表分類", "tech-read")
	base := time.Date(2026, time.April, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 22; i++ {
		slug := fmt.Sprintf("bulk-%02d", i)
		pluginReadSeedPost(t, h, author, "Bulk "+slug, slug, "published", "post", "<p>bulk</p>", base.Add(time.Duration(i)*time.Hour))
	}
	pluginReadSeedPost(t, h, author, "讀取關鍵字 Alpha", "read-alpha", "published", "post", "<p>alpha</p>", base.Add(30*time.Hour), tech)
	pluginReadSeedPost(t, h, author, "草稿 Draft 草稿", "read-draft", "draft", "post", "<p>draft</p>", time.Time{})
	pluginReadSeedPost(t, h, author, "頁面 Page", "read-page", "published", "page", "<p>page</p>", base)
	trashed := pluginReadSeedPost(t, h, author, "回收 Trashed", "read-trashed-list", "published", "post", "<p>x</p>", base)
	if err := h.db.Delete(&trashed).Error; err != nil {
		t.Fatalf("move post to trash: %v", err)
	}

	wantPosts := pluginReadCount(t, h, &core.Post{}, "type = ?", "post")

	envelope, items := pluginReadWantList(t, pluginReadCall(t, h, "content.list", map[string]any{"type": "post"}), "content.list({type:post})")
	if number(t, envelope, "total") != float64(wantPosts) {
		t.Errorf("content.list total = %v, want %d non-trashed posts", envelope["total"], wantPosts)
	}
	if number(t, envelope, "page") != 1 || number(t, envelope, "per_page") != 20 {
		t.Errorf("content.list page/per_page = %v/%v, want 1/20", envelope["page"], envelope["per_page"])
	}
	if len(items) != 20 {
		t.Errorf("content.list default page has %d items, want 20", len(items))
	}
	for _, item := range items {
		if _, ok := item["content"]; ok {
			t.Errorf("content.list item %v carries content; list items must omit it", item["slug"])
			break
		}
		if item["content_omitted"] != true {
			t.Errorf("content.list item %v content_omitted = %v, want true", item["slug"], item["content_omitted"])
			break
		}
	}

	page2, items2 := pluginReadWantList(t, pluginReadCall(t, h, "content.list", map[string]any{"type": "post", "page": 2, "per_page": 20}), "content.list(page 2)")
	if number(t, page2, "page") != 2 {
		t.Errorf("content.list page 2 reports page %v", page2["page"])
	}
	if want := int(wantPosts) - 20; len(items2) != want {
		t.Errorf("content.list page 2 has %d items, want %d", len(items2), want)
	}

	clamped, _ := pluginReadWantList(t, pluginReadCall(t, h, "content.list", map[string]any{"type": "post", "per_page": 500}), "content.list(per_page 500)")
	if number(t, clamped, "per_page") != 100 {
		t.Errorf("content.list per_page 500 reports %v, want 100", clamped["per_page"])
	}

	wantDrafts := pluginReadCount(t, h, &core.Post{}, "status = ?", "draft")
	drafts, draftItems := pluginReadWantList(t, pluginReadCall(t, h, "content.list", map[string]any{"status": "draft"}), "content.list({status:draft})")
	if number(t, drafts, "total") != float64(wantDrafts) || len(draftItems) != int(wantDrafts) {
		t.Errorf("content.list draft total/items = %v/%d, want %d", drafts["total"], len(draftItems), wantDrafts)
	}
	for _, item := range draftItems {
		if item["status"] != "draft" {
			t.Errorf("content.list({status:draft}) returned status %v", item["status"])
		}
	}

	searched, searchedItems := pluginReadWantList(t, pluginReadCall(t, h, "content.list", map[string]any{"search": "讀取關鍵字"}), "content.list({search})")
	if number(t, searched, "total") != 1 || len(searchedItems) != 1 || searchedItems[0]["slug"] != "read-alpha" {
		t.Errorf("content.list({search:讀取關鍵字}) = %v, want only read-alpha", searched)
	}

	byCategory, categoryItems := pluginReadWantList(t, pluginReadCall(t, h, "content.list", map[string]any{"category": "tech-read"}), "content.list({category})")
	if number(t, byCategory, "total") != 1 || len(categoryItems) != 1 || categoryItems[0]["slug"] != "read-alpha" {
		t.Errorf("content.list({category:tech-read}) = %v, want only read-alpha", byCategory)
	}
}

func TestPluginReadAPIShopProductsGetAndList(t *testing.T) {
	h := pluginReadHarness(t, []string{"shop.read"}, "")
	active := shopSeedProduct(t, h.accessHarness, "read-tee", 12000, 5)
	shopCatalogSeedProduct(t, h.accessHarness, shopCatalogProduct{title: "Read draft tee", slug: "read-draft-tee", status: "draft", price: 9900, stock: 2})
	trashed := shopSeedProduct(t, h.accessHarness, "read-trashed-tee", 5000, 1)
	if err := h.db.Delete(&trashed).Error; err != nil {
		t.Fatalf("move product to trash: %v", err)
	}

	byID := pluginReadWantObject(t, pluginReadCall(t, h, "shop.getProduct", active.ID), "shop.getProduct(id)")
	if number(t, byID, "id") != float64(active.ID) || byID["slug"] != "read-tee" || byID["title"] != "read-tee" || byID["status"] != "active" {
		t.Errorf("shop.getProduct(id) = %v, want the active read-tee product", byID)
	}
	if number(t, byID, "price") != 12000 {
		t.Errorf("shop.getProduct(id).price = %v, want 12000", byID["price"])
	}
	bySlug := pluginReadWantObject(t, pluginReadCall(t, h, "shop.getProduct", "read-tee"), "shop.getProduct(slug)")
	if number(t, bySlug, "id") != float64(active.ID) {
		t.Errorf("shop.getProduct(slug) id = %v, want %d", bySlug["id"], active.ID)
	}
	pluginReadWantNull(t, pluginReadCall(t, h, "shop.getProduct", 999999), "shop.getProduct(missing)")
	pluginReadWantNull(t, pluginReadCall(t, h, "shop.getProduct", trashed.ID), "shop.getProduct(trashed id)")
	pluginReadWantNull(t, pluginReadCall(t, h, "shop.getProduct", trashed.Slug), "shop.getProduct(trashed slug)")

	wantActive := pluginReadCount(t, h, &core.Product{}, "status = ?", "active")
	envelope, items := pluginReadWantList(t, pluginReadCall(t, h, "shop.listProducts", map[string]any{"status": "active"}), "shop.listProducts({status:active})")
	if number(t, envelope, "total") != float64(wantActive) || len(items) != int(wantActive) {
		t.Errorf("shop.listProducts({status:active}) total/items = %v/%d, want %d", envelope["total"], len(items), wantActive)
	}
	if number(t, envelope, "page") != 1 || number(t, envelope, "per_page") != 20 {
		t.Errorf("shop.listProducts page/per_page = %v/%v, want 1/20", envelope["page"], envelope["per_page"])
	}
	for _, item := range items {
		if item["status"] != "active" {
			t.Errorf("shop.listProducts({status:active}) returned status %v", item["status"])
		}
	}

	wantAll := pluginReadCount(t, h, &core.Product{}, "TRUE")
	all, allItems := pluginReadWantList(t, pluginReadCall(t, h, "shop.listProducts", map[string]any{}), "shop.listProducts({})")
	if number(t, all, "total") != float64(wantAll) || len(allItems) != int(wantAll) {
		t.Errorf("shop.listProducts({}) total/items = %v/%d, want %d non-trashed products", all["total"], len(allItems), wantAll)
	}
}

// pluginReadSeedOrders places three orders through the real checkout: two for reader@example.test and one for
// other@example.test. It returns the order id of the first checkout and the variant used.
func pluginReadSeedOrders(t *testing.T, h *pluginEventsHarness) (uint, string) {
	t.Helper()
	shopSetup(t, h.accessHarness)
	product := shopSeedProduct(t, h.accessHarness, "read-order-tee", 12000, 20)
	variants := shopVariantRows(t, h.accessHarness, product.ID)
	if len(variants) == 0 {
		t.Fatalf("product %d has no variants", product.ID)
	}
	_, first := shopPlaceCheckout(t, h.accessHarness, variants[0].ID, 2, "reader@example.test", "credit_card", "read-order-1")
	shopPlaceCheckout(t, h.accessHarness, variants[0].ID, 1, "reader@example.test", "credit_card", "read-order-2")
	shopPlaceCheckout(t, h.accessHarness, variants[0].ID, 1, "other@example.test", "credit_card", "read-order-3")
	var order core.Order
	if err := h.db.Where("number = ?", first.Order.Number).First(&order).Error; err != nil {
		t.Fatalf("read first checkout order %s: %v", first.Order.Number, err)
	}
	return order.ID, first.Order.Number
}

func TestPluginReadAPIShopOrdersGetAndList(t *testing.T) {
	h := pluginReadHarness(t, []string{"shop.read"}, "")
	orderID, orderNumber := pluginReadSeedOrders(t, h)

	var stored core.Order
	if err := h.db.First(&stored, orderID).Error; err != nil {
		t.Fatalf("read seeded order: %v", err)
	}
	wantItems := pluginReadCount(t, h, &core.OrderItem{}, "order_id = ?", orderID)
	if wantItems == 0 {
		t.Fatalf("seeded order %d has no items", orderID)
	}

	byID := pluginReadWantObject(t, pluginReadCall(t, h, "shop.getOrder", orderID), "shop.getOrder(id)")
	if number(t, byID, "id") != float64(orderID) || byID["number"] != orderNumber || byID["email"] != "reader@example.test" {
		t.Errorf("shop.getOrder(id) = %v, want order %s for reader@example.test", byID, orderNumber)
	}
	if number(t, byID, "total_amount") != float64(stored.TotalAmount) {
		t.Errorf("shop.getOrder(id).total_amount = %v, want %d", byID["total_amount"], stored.TotalAmount)
	}
	items, ok := byID["items"].([]any)
	if !ok || int64(len(items)) != wantItems {
		t.Fatalf("shop.getOrder(id).items = %v, want %d items", byID["items"], wantItems)
	}
	if first, _ := items[0].(map[string]any); number(t, first, "quantity") != 2 {
		t.Errorf("shop.getOrder(id).items[0].quantity = %v, want 2", first["quantity"])
	}

	byNumber := pluginReadWantObject(t, pluginReadCall(t, h, "shop.getOrder", orderNumber), "shop.getOrder(number)")
	if number(t, byNumber, "id") != float64(orderID) {
		t.Errorf("shop.getOrder(number) id = %v, want %d", byNumber["id"], orderID)
	}
	pluginReadWantNull(t, pluginReadCall(t, h, "shop.getOrder", 999999), "shop.getOrder(missing id)")
	pluginReadWantNull(t, pluginReadCall(t, h, "shop.getOrder", "JL-0000-0000"), "shop.getOrder(missing number)")

	wantReader := pluginReadCount(t, h, &core.Order{}, "email = ?", "reader@example.test")
	envelope, orders := pluginReadWantList(t, pluginReadCall(t, h, "shop.listOrders", map[string]any{"email": "reader@example.test"}), "shop.listOrders({email})")
	if number(t, envelope, "total") != float64(wantReader) || int64(len(orders)) != wantReader {
		t.Errorf("shop.listOrders({email}) total/items = %v/%d, want %d", envelope["total"], len(orders), wantReader)
	}
	for _, order := range orders {
		if _, ok := order["items"]; ok {
			t.Errorf("shop.listOrders item %v carries items; list items must omit them", order["number"])
		}
		if order["email"] != "reader@example.test" {
			t.Errorf("shop.listOrders({email}) returned email %v", order["email"])
		}
	}

	wantAll := pluginReadCount(t, h, &core.Order{}, "TRUE")
	all, _ := pluginReadWantList(t, pluginReadCall(t, h, "shop.listOrders", map[string]any{}), "shop.listOrders({})")
	if number(t, all, "total") != float64(wantAll) {
		t.Errorf("shop.listOrders({}) total = %v, want %d", all["total"], wantAll)
	}
}

func TestPluginReadAPIPermissionDenied(t *testing.T) {
	// Positive controls: with the permission declared and approved, the read succeeds. The denial subtests below only
	// pin the error wording.
	t.Run("content approved reads", func(t *testing.T) {
		h := pluginReadHarness(t, []string{"content.read"}, "")
		pluginReadWantList(t, pluginReadCall(t, h, "content.list", map[string]any{}), "content.list with content.read")
	})
	t.Run("shop approved reads", func(t *testing.T) {
		h := pluginReadHarness(t, []string{"shop.read"}, "")
		pluginReadWantList(t, pluginReadCall(t, h, "shop.listProducts", map[string]any{}), "shop.listProducts with shop.read")
	})
	t.Run("content without content.read", func(t *testing.T) {
		h := pluginReadHarness(t, []string{"shop.read"}, "")
		pluginReadWantError(t, pluginReadCall(t, h, "content.get", 1), "content.get", "permission denied")
		pluginReadWantError(t, pluginReadCall(t, h, "content.list", map[string]any{}), "content.list", "permission denied")
	})
	t.Run("shop without shop.read", func(t *testing.T) {
		h := pluginReadHarness(t, []string{"content.read"}, "")
		pluginReadWantError(t, pluginReadCall(t, h, "shop.getProduct", 1), "shop.getProduct", "permission denied")
		pluginReadWantError(t, pluginReadCall(t, h, "shop.listOrders", map[string]any{}), "shop.listOrders", "permission denied")
	})
	t.Run("content with stale approval", func(t *testing.T) {
		stale := plugin.PermissionNamesHash([]string{"events"})
		h := pluginReadHarness(t, []string{"content.read"}, stale)
		pluginReadWantError(t, pluginReadCall(t, h, "content.get", 1), "content.get", "stale")
	})
	t.Run("shop with stale approval", func(t *testing.T) {
		stale := plugin.PermissionNamesHash([]string{"events"})
		h := pluginReadHarness(t, []string{"shop.read"}, stale)
		pluginReadWantError(t, pluginReadCall(t, h, "shop.listProducts", map[string]any{}), "shop.listProducts", "stale")
	})
}

func TestPluginReadAPIArgumentErrorsAndDefaults(t *testing.T) {
	h := pluginReadHarness(t, []string{"content.read", "shop.read"}, "")
	t.Run("content.get without a ref", func(t *testing.T) {
		pluginReadWantError(t, pluginReadCall(t, h, "content.get", map[string]any{}), "content.get({})", "")
	})
	t.Run("content.list with a string", func(t *testing.T) {
		pluginReadWantError(t, pluginReadCall(t, h, "content.list", "x"), "content.list(\"x\")", "")
	})
	t.Run("shop.getProduct with a boolean", func(t *testing.T) {
		pluginReadWantError(t, pluginReadCall(t, h, "shop.getProduct", true), "shop.getProduct(true)", "")
	})
	t.Run("content.list page 0 uses the defaults", func(t *testing.T) {
		envelope, _ := pluginReadWantList(t, pluginReadCall(t, h, "content.list", map[string]any{"page": 0}), "content.list({page:0})")
		if number(t, envelope, "page") != 1 || number(t, envelope, "per_page") != 20 {
			t.Errorf("content.list({page:0}) page/per_page = %v/%v, want the defaults 1/20", envelope["page"], envelope["per_page"])
		}
	})
}

func TestPluginReadAPIObjectsCarryNoPII(t *testing.T) {
	h := pluginReadHarness(t, []string{"content.read", "shop.read"}, "")
	author := h.user("admin", "read-pii-author")
	post := pluginReadSeedPost(t, h, author, "PII Post", "read-pii-post", "published", "post", "<p>pii</p>", time.Date(2026, time.May, 1, 9, 0, 0, 0, time.UTC))

	postObject := pluginReadWantObject(t, pluginReadCall(t, h, "content.get", post.ID), "content.get(id)")
	if _, ok := postObject["author"]; ok {
		t.Errorf("content.get object carries an author key: %v", postObject["author"])
	}
	if number(t, postObject, "author_id") != float64(author.ID) {
		t.Errorf("content.get author_id = %v, want %d", postObject["author_id"], author.ID)
	}
	_, postItems := pluginReadWantList(t, pluginReadCall(t, h, "content.list", map[string]any{"type": "post"}), "content.list({type:post})")
	for _, item := range postItems {
		if _, ok := item["author"]; ok {
			t.Errorf("content.list item %v carries an author key", item["slug"])
		}
	}

	orderID, _ := pluginReadSeedOrders(t, h)
	forbidden := []string{"shipping", "invoice", "lookup_token_hash", "idempotency_key"}
	order := pluginReadWantObject(t, pluginReadCall(t, h, "shop.getOrder", orderID), "shop.getOrder(id)")
	for _, key := range forbidden {
		if _, ok := order[key]; ok {
			t.Errorf("shop.getOrder object carries %q", key)
		}
	}
	_, orders := pluginReadWantList(t, pluginReadCall(t, h, "shop.listOrders", map[string]any{}), "shop.listOrders({})")
	for _, listed := range orders {
		for _, key := range forbidden {
			if _, ok := listed[key]; ok {
				t.Errorf("shop.listOrders item %v carries %q", listed["number"], key)
			}
		}
	}
}
