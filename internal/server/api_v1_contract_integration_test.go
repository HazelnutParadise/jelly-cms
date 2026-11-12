package server

// Acceptance tests for ticket 27 (ticket 27): the machine API owns its list
// envelopes, the duplicate endpoints are gone, every integration is rate limited and GET /api is the
// index of the APIs. Each TestAPIV1Contract... function names the contract item it locks. The expected
// shapes are written from the ticket, not read back from the implementation. They need
// JELLY_TEST_POSTGRES=1 and the dedicated PostgreSQL container, like the other access-key tests.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"gorm.io/gorm/clause"
)

// apiv1ContractSeq makes slugs and order numbers unique across seeds.
var apiv1ContractSeq atomic.Int64

// apiv1ContractBadKey is a well-formed key that no integration holds, so the lookup misses.
var apiv1ContractBadKey = "jly_" + strings.Repeat("A", 43)

// Ticket 27 名詞 「內容摘要」 (field order does not matter; the set is exact).
var apiv1ContractContentSummaryKeys = []string{
	"id", "title", "slug", "type", "status", "visibility", "author_id", "excerpt", "cover", "pinned", "format",
	"template", "published_at", "created_at", "updated_at", "allow_comments", "seo_index", "seo_ai", "meta",
	"categories", "tags",
}

// Ticket 27 名詞 「訂單摘要」 and its items.
var apiv1ContractOrderSummaryKeys = []string{
	"id", "number", "email", "status", "total_amount", "currency", "payment_provider", "payment_method",
	"shipping", "items", "created_at",
}
var apiv1ContractOrderItemKeys = []string{"title", "variant_name", "quantity"}

// Ticket 27 名詞 「分頁信封」 and 「集合信封」.
var apiv1ContractPagedKeys = []string{"items", "total", "page", "per_page"}
var apiv1ContractCollectionKeys = []string{"items", "total"}

// apiv1ContractRateHeaders are the four headers a 429 declares (ticket 27 R4).
var apiv1ContractRateHeaders = []string{"Retry-After", "RateLimit-Limit", "RateLimit-Remaining", "RateLimit-Reset"}

// apiv1ContractInstallClock pins the rate limiter clock to 30 seconds into the current minute, so
// the window ends 30 seconds later. Moving the returned value forward by a minute moves the window.
// time.Now is restored when the test ends.
func apiv1ContractInstallClock(t *testing.T) *time.Time {
	t.Helper()
	start := time.Now().UTC().Truncate(time.Minute).Add(30 * time.Second)
	clock := &start
	integrationRateLimitNow = func() time.Time { return *clock }
	t.Cleanup(func() { integrationRateLimitNow = time.Now })
	return clock
}

// apiv1ContractObject decodes a JSON object body.
func apiv1ContractObject(t *testing.T, r agtResult, where string) map[string]any {
	t.Helper()
	var m map[string]any
	agtDecode(t, r, &m, where)
	return m
}

// apiv1ContractItems returns the items array of an envelope, failing when it is not an array (null included).
func apiv1ContractItems(t *testing.T, obj map[string]any, where string) []any {
	t.Helper()
	items, ok := obj["items"].([]any)
	if !ok {
		t.Fatalf("%s: items=%v, want a JSON array (an empty list is [], never null)", where, obj["items"])
	}
	return items
}

// apiv1ContractExpectKeys checks that obj has exactly the wanted keys.
func apiv1ContractExpectKeys(t *testing.T, obj map[string]any, want []string, where string) {
	t.Helper()
	got := make([]string, 0, len(obj))
	for k := range obj {
		got = append(got, k)
	}
	sort.Strings(got)
	wantSorted := slices.Clone(want)
	sort.Strings(wantSorted)
	if !reflect.DeepEqual(got, wantSorted) {
		t.Errorf("%s keys=%v, want exactly %v", where, got, wantSorted)
	}
}

// apiv1ContractExpectPaged checks the ticket 27 paged envelope and its numbers.
func apiv1ContractExpectPaged(t *testing.T, r agtResult, where string, total, page, perPage int) map[string]any {
	t.Helper()
	agtExpectOK(t, r, where)
	obj := apiv1ContractObject(t, r, where)
	apiv1ContractExpectKeys(t, obj, apiv1ContractPagedKeys, where+" envelope")
	apiv1ContractItems(t, obj, where)
	if obj["total"] != float64(total) || obj["page"] != float64(page) || obj["per_page"] != float64(perPage) {
		t.Errorf("%s total=%v page=%v per_page=%v, want %d %d %d", where, obj["total"], obj["page"], obj["per_page"], total, page, perPage)
	}
	return obj
}

// apiv1ContractExpectCollection checks the ticket 27 collection envelope: total equals the item count.
func apiv1ContractExpectCollection(t *testing.T, r agtResult, where string) ([]any, map[string]any) {
	t.Helper()
	agtExpectOK(t, r, where)
	obj := apiv1ContractObject(t, r, where)
	apiv1ContractExpectKeys(t, obj, apiv1ContractCollectionKeys, where+" envelope")
	items := apiv1ContractItems(t, obj, where)
	if obj["total"] != float64(len(items)) {
		t.Errorf("%s total=%v, want the item count %d", where, obj["total"], len(items))
	}
	return items, obj
}

// apiv1ContractHeader checks one response header against the expected value.
func apiv1ContractHeader(t *testing.T, r agtResult, name, want, where string) {
	t.Helper()
	if got := r.header.Get(name); got != want {
		t.Errorf("%s: %s=%q, want %q", where, name, got, want)
	}
}

// apiv1ContractSeconds reads an integer header that must sit inside the 1 to 60 second window.
func apiv1ContractSeconds(t *testing.T, r agtResult, name, where string) int {
	t.Helper()
	n, err := strconv.Atoi(r.header.Get(name))
	if err != nil || n < 1 || n > 60 {
		t.Errorf("%s: %s=%q, want an integer from 1 to 60", where, name, r.header.Get(name))
	}
	return n
}

// apiv1ContractSeedPost inserts a post or page (kind) without going through the handlers.
func apiv1ContractSeedPost(t *testing.T, h *accessHarness, author core.User, title, status, kind string) core.Post {
	t.Helper()
	now := time.Now().UTC()
	post := core.Post{
		Title: title, Slug: fmt.Sprintf("contract-%d", apiv1ContractSeq.Add(1)), Content: "<p>" + title + " 全文</p>",
		Type: kind, Status: status, AuthorID: author.ID, Visibility: "public",
		PublishedAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	if err := h.db.Omit(clause.Associations).Create(&post).Error; err != nil {
		t.Fatalf("seed %s %q: %v", kind, title, err)
	}
	return post
}

// apiv1ContractSeedOrder inserts one paid order with one item of quantity 2.
func apiv1ContractSeedOrder(t *testing.T, h *accessHarness) core.Order {
	t.Helper()
	order := core.Order{
		Number: fmt.Sprintf("JL-2026-%04d", 9000+apiv1ContractSeq.Add(1)), Email: "buyer@example.test", Status: "paid",
		Currency: "TWD", PaymentProvider: "ecpay", PaymentMethod: "credit_card", ShippingAddress: "台北市信義區",
		TotalAmount: 300, Subtotal: 300, Shipping: json.RawMessage(`{"method":"home"}`), Invoice: json.RawMessage(`{}`),
	}
	if err := h.db.Omit(clause.Associations).Create(&order).Error; err != nil {
		t.Fatalf("seed order: %v", err)
	}
	item := core.OrderItem{OrderID: order.ID, Title: "杯子", VariantName: "白色", SKU: "CUP-W", Quantity: 2, Price: 150, UnitPrice: 150, LineTotal: 300}
	if err := h.db.Omit(clause.Associations).Create(&item).Error; err != nil {
		t.Fatalf("seed order item: %v", err)
	}
	return order
}

// apiv1ContractAuthor creates an author and an integration owned by that author (role_cap empty).
func apiv1ContractAuthor(t *testing.T, h *accessHarness, name string, permissions ...string) (core.User, string) {
	t.Helper()
	author := h.user("author", name)
	key := apiKeysSeedIntegration(t, h, author, name+" bot", permissions, "", nil, nil)
	return author, key
}

// apiv1ContractDoc fetches the v1 OpenAPI document without a key.
func apiv1ContractDoc(t *testing.T, h *accessHarness) map[string]any {
	t.Helper()
	r := agtDo(h, http.MethodGet, "/api/v1/openapi.json", "", "", "")
	agtExpectOK(t, r, "GET /api/v1/openapi.json")
	return apiv1ContractObject(t, r, "openapi.json")
}

// apiv1ContractChild follows $ref and returns node[key] as an object (nil when absent).
func apiv1ContractChild(doc map[string]any, node map[string]any, key string) map[string]any {
	child, _ := agtDeref(doc, node[key]).(map[string]any)
	return child
}

// apiv1ContractOp returns the operation object at path and method (nil when absent).
func apiv1ContractOp(doc map[string]any, path, method string) map[string]any {
	paths, _ := doc["paths"].(map[string]any)
	item, _ := paths[path].(map[string]any)
	op, _ := item[method].(map[string]any)
	return op
}

// apiv1ContractResponseSchema returns the application/json schema of a response, following $ref.
func apiv1ContractResponseSchema(doc map[string]any, op map[string]any, status string) map[string]any {
	responses, _ := op["responses"].(map[string]any)
	resp, _ := agtDeref(doc, responses[status]).(map[string]any)
	content, _ := resp["content"].(map[string]any)
	media, _ := content["application/json"].(map[string]any)
	schema, _ := agtDeref(doc, media["schema"]).(map[string]any)
	return schema
}

// apiv1ContractLastUsed reads last_used_at of the key whose prefix matches apiKey.
func apiv1ContractLastUsed(t *testing.T, h *accessHarness, token string, integrationID uint, apiKey string) *string {
	t.Helper()
	detail := agtDetail(t, h, token, integrationID)
	key, ok := agtKeyByPrefix(detail.Keys, apiKey[:12])
	if !ok {
		t.Fatalf("integration %d has no key with prefix %s", integrationID, apiKey[:12])
	}
	return key.LastUsedAt
}

// ---- R3: removed endpoints ----

func TestAPIV1ContractRemovedEndpointsAnswer404And405WithKey(t *testing.T) {
	h := newAccessHarness(t)
	_, _, key := agtSetup(t, h, "Contract Removed", "media.read", "media.write", "settings.write", "logs.read")
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/logs", ""},
		{http.MethodGet, "/api/v1/media", ""},
		{http.MethodPost, "/api/v1/media", `{"filename":"x.png"}`},
		{http.MethodPut, "/api/v1/media/x.png", `{"filename":"y.png"}`},
		{http.MethodDelete, "/api/v1/media/x.png", ""},
		{http.MethodPost, "/api/v1/media/rename", `{"from":"x.png","to":"y.png"}`},
	} {
		agtExpectError(t, agtDo(h, c.method, c.path, c.body, key.APIKey, ""), http.StatusNotFound, "not_found", c.method+" "+c.path)
	}
	agtExpectError(t, agtDo(h, http.MethodPost, "/api/v1/settings", `{"site_title":"x"}`, key.APIKey, ""), http.StatusMethodNotAllowed, "method_not_allowed", "POST /api/v1/settings")
}

func TestAPIV1ContractRemovedEndpointsNeedKeyWithoutKey(t *testing.T) {
	h := newAccessHarness(t)
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/logs", ""},
		{http.MethodGet, "/api/v1/media", ""},
		{http.MethodPost, "/api/v1/media", `{}`},
		{http.MethodPut, "/api/v1/media/x.png", `{}`},
		{http.MethodDelete, "/api/v1/media/x.png", ""},
		{http.MethodPost, "/api/v1/media/rename", `{}`},
		{http.MethodPost, "/api/v1/settings", `{}`},
	} {
		agtExpectError(t, agtDo(h, c.method, c.path, c.body, "", ""), http.StatusUnauthorized, "key_missing", "no key "+c.method+" "+c.path)
	}
}

func TestAPIV1ContractIndexAndOpenAPIOmitRemovedEndpoints(t *testing.T) {
	h := newAccessHarness(t)
	removed := func(path string) bool {
		return path == "/api/v1/logs" || path == "/api/v1/media" ||
			(strings.HasPrefix(path, "/api/v1/media/") && !strings.HasPrefix(path, "/api/v1/media/files"))
	}

	index := apiv1ContractObject(t, agtDo(h, http.MethodGet, "/api/v1", "", "", ""), "discovery index")
	endpoints, _ := index["endpoints"].([]any)
	settingsMethods := map[string]bool{}
	for _, raw := range endpoints {
		ep, _ := raw.(map[string]any)
		path, _ := ep["path"].(string)
		method, _ := ep["method"].(string)
		if removed(path) {
			t.Errorf("GET /api/v1 endpoints still lists %s %s", method, path)
		}
		if path == "/api/v1/settings" {
			settingsMethods[method] = true
		}
	}
	if !reflect.DeepEqual(settingsMethods, map[string]bool{"GET": true, "PUT": true}) {
		t.Errorf("GET /api/v1 lists /api/v1/settings methods %v, want only GET and PUT", settingsMethods)
	}
	notExposed, _ := json.Marshal(index["not_exposed"])
	var admin, logs bool
	notExposedList, _ := index["not_exposed"].([]any)
	for _, raw := range notExposedList {
		entry, _ := raw.(map[string]any)
		text, _ := json.Marshal(entry)
		if entry["path"] == "/api/admin/media" && strings.Contains(string(text), "/api/v1/media/files") {
			admin = true
		}
		if p, _ := entry["path"].(string); strings.HasPrefix(p, "/api/admin/logs") && strings.Contains(string(text), "/me/events") {
			logs = true
		}
	}
	if !admin || !logs {
		t.Errorf("not_exposed=%s, want /api/admin/media pointing to /api/v1/media/files and /api/admin/logs* pointing to /me/events", notExposed)
	}

	doc := apiv1ContractDoc(t, h)
	paths, _ := doc["paths"].(map[string]any)
	for path := range paths {
		if removed(path) {
			t.Errorf("OpenAPI paths still contain %s", path)
		}
	}
	settingsItem, _ := paths["/api/v1/settings"].(map[string]any)
	methods := map[string]bool{}
	for m := range settingsItem {
		methods[m] = true
	}
	if !reflect.DeepEqual(methods, map[string]bool{"get": true, "put": true}) {
		t.Errorf("OpenAPI /api/v1/settings operations=%v, want only get and put", methods)
	}
}

// ---- R1, R2: posts and pages ----

func TestAPIV1ContractPostsListEnvelopeAndSummary(t *testing.T) {
	h := newAccessHarness(t)
	admin, token, _ := agtSetup(t, h, "Contract Posts", "posts.read")
	apiv1ContractSeedPost(t, h, admin, "Contract Alpha", "published", "post")
	apiv1ContractSeedPost(t, h, admin, "Contract Beta", "published", "post")
	apiv1ContractSeedPost(t, h, admin, "Contract Gamma", "draft", "post")
	trash := apiv1ContractSeedPost(t, h, admin, "Contract Trashed", "published", "post")
	if err := h.db.Delete(&trash).Error; err != nil {
		t.Fatalf("soft delete post: %v", err)
	}
	apiv1ContractSeedPost(t, h, admin, "Contract About", "published", "page")
	reader := agtCreate(t, h, token, "Contract Posts Reader", "posts.read")

	obj := apiv1ContractExpectPaged(t, agtDo(h, http.MethodGet, "/api/v1/posts?per_page=2", "", reader.APIKey, ""), "GET /posts?per_page=2", 3, 1, 2)
	items := apiv1ContractItems(t, obj, "GET /posts?per_page=2")
	if len(items) != 2 {
		t.Fatalf("GET /posts?per_page=2 items=%d, want 2", len(items))
	}
	for _, raw := range items {
		apiv1ContractExpectKeys(t, raw.(map[string]any), apiv1ContractContentSummaryKeys, "content summary")
	}

	page2 := apiv1ContractExpectPaged(t, agtDo(h, http.MethodGet, "/api/v1/posts?page=2&per_page=2", "", reader.APIKey, ""), "GET /posts?page=2&per_page=2", 3, 2, 2)
	if got := apiv1ContractItems(t, page2, "page 2"); len(got) != 1 {
		t.Errorf("page 2 items=%d, want 1", len(got))
	}
	page3 := apiv1ContractExpectPaged(t, agtDo(h, http.MethodGet, "/api/v1/posts?page=3&per_page=2", "", reader.APIKey, ""), "GET /posts?page=3&per_page=2", 3, 3, 2)
	if got := apiv1ContractItems(t, page3, "page 3"); len(got) != 0 {
		t.Errorf("page 3 items=%d, want []", len(got))
	}

	all := agtDo(h, http.MethodGet, "/api/v1/posts", "", reader.APIKey, "")
	apiv1ContractExpectPaged(t, all, "GET /posts without parameters", 3, 1, 20)
	for _, forbidden := range []string{"Contract Trashed", "Contract About"} {
		if strings.Contains(all.body, forbidden) {
			t.Errorf("GET /posts lists %q; the trash and pages must not appear", forbidden)
		}
	}
	for _, raw := range apiv1ContractItems(t, apiv1ContractObject(t, all, "all posts"), "all posts") {
		item := raw.(map[string]any)
		for _, key := range []string{"content", "source", "author"} {
			if _, ok := item[key]; ok {
				t.Errorf("list item %v carries %q", item["title"], key)
			}
		}
	}

	one := agtDo(h, http.MethodGet, fmt.Sprintf("/api/v1/posts/%d", apiv1ContractPostID(t, h, "Contract Alpha")), "", reader.APIKey, "")
	agtExpectOK(t, one, "GET /posts/:id")
	single := apiv1ContractObject(t, one, "GET /posts/:id")
	if _, ok := single["content"]; !ok {
		t.Error("GET /posts/:id omits content")
	}
	if _, ok := single["source"]; !ok {
		t.Error("GET /posts/:id omits source")
	}
}

// apiv1ContractPostID looks up a seeded post by title.
func apiv1ContractPostID(t *testing.T, h *accessHarness, title string) uint {
	t.Helper()
	var post core.Post
	if err := h.db.Where("title = ?", title).First(&post).Error; err != nil {
		t.Fatalf("load post %q: %v", title, err)
	}
	return post.ID
}

func TestAPIV1ContractPagedParamsRejectBadValuesAndAcceptEmpty(t *testing.T) {
	h := newAccessHarness(t)
	_, _, key := agtSetup(t, h, "Contract Paging", "posts.read", "pages.read")
	bad := []struct{ query, param string }{
		{"page=0", "page"}, {"page=-1", "page"}, {"page=abc", "page"}, {"page=1000001", "page"},
		{"per_page=0", "per_page"}, {"per_page=101", "per_page"}, {"per_page=abc", "per_page"},
	}
	for _, path := range []string{"/api/v1/posts", "/api/v1/pages"} {
		for _, c := range bad {
			where := path + "?" + c.query
			env := agtExpectError(t, agtDo(h, http.MethodGet, path+"?"+c.query, "", key.APIKey, ""), http.StatusBadRequest, "bad_request", where)
			if env.Message != "Invalid "+c.param || env.Details["param"] != c.param {
				t.Errorf("%s message=%q details=%v, want Invalid %s with param %s", where, env.Message, env.Details, c.param, c.param)
			}
		}
		apiv1ContractExpectPaged(t, agtDo(h, http.MethodGet, path+"?page=&per_page=", "", key.APIKey, ""), path+" with empty page and per_page", 0, 1, 20)
		apiv1ContractExpectPaged(t, agtDo(h, http.MethodGet, path+"?page=1000000&per_page=100", "", key.APIKey, ""), path+" at the upper bounds", 0, 1000000, 100)
		apiv1ContractExpectPaged(t, agtDo(h, http.MethodGet, path+"?foo=1", "", key.APIKey, ""), path+" with an unknown parameter", 0, 1, 20)
	}
}

func TestAPIV1ContractPostsStatusFilterAndUnknownParameters(t *testing.T) {
	h := newAccessHarness(t)
	admin, token, _ := agtSetup(t, h, "Contract Status", "posts.read")
	apiv1ContractSeedPost(t, h, admin, "Contract Status Alpha", "published", "post")
	apiv1ContractSeedPost(t, h, admin, "Contract Status Beta", "published", "post")
	apiv1ContractSeedPost(t, h, admin, "Contract Status Gamma", "draft", "post")
	reader := agtCreate(t, h, token, "Contract Status Reader", "posts.read")

	drafts := apiv1ContractExpectPaged(t, agtDo(h, http.MethodGet, "/api/v1/posts?status=draft", "", reader.APIKey, ""), "GET /posts?status=draft", 1, 1, 20)
	for _, raw := range apiv1ContractItems(t, drafts, "status=draft") {
		if raw.(map[string]any)["status"] != "draft" {
			t.Errorf("status=draft returned item with status %v", raw.(map[string]any)["status"])
		}
	}
	apiv1ContractExpectPaged(t, agtDo(h, http.MethodGet, "/api/v1/posts?status=", "", reader.APIKey, ""), "GET /posts?status= (empty)", 3, 1, 20)
	apiv1ContractExpectPaged(t, agtDo(h, http.MethodGet, "/api/v1/posts?status=archived", "", reader.APIKey, ""), "GET /posts?status=archived", 0, 1, 20)
}

func TestAPIV1ContractPostsTitleSearchIsLiteralAndCaseInsensitive(t *testing.T) {
	h := newAccessHarness(t)
	admin, token, _ := agtSetup(t, h, "Contract Search", "posts.read")
	apiv1ContractSeedPost(t, h, admin, "Sale 50% off", "published", "post")
	apiv1ContractSeedPost(t, h, admin, "Sale 500 off", "published", "post")
	apiv1ContractSeedPost(t, h, admin, "Plain note", "published", "post")
	reader := agtCreate(t, h, token, "Contract Search Reader", "posts.read")

	// A percent sign in q is a literal character: "50%" must not match "500".
	literal := apiv1ContractExpectPaged(t, agtDo(h, http.MethodGet, "/api/v1/posts?q="+url.QueryEscape("50%"), "", reader.APIKey, ""), "GET /posts?q=50%", 1, 1, 20)
	if items := apiv1ContractItems(t, literal, "q=50%"); len(items) == 1 && items[0].(map[string]any)["title"] != "Sale 50% off" {
		t.Errorf("q=50%% returned %v, want Sale 50%% off", items[0].(map[string]any)["title"])
	}
	apiv1ContractExpectPaged(t, agtDo(h, http.MethodGet, "/api/v1/posts?q=%25", "", reader.APIKey, ""), "GET /posts?q=%", 1, 1, 20)
	apiv1ContractExpectPaged(t, agtDo(h, http.MethodGet, "/api/v1/posts?q=SALE", "", reader.APIKey, ""), "GET /posts?q=SALE (case-insensitive)", 2, 1, 20)
}

func TestAPIV1ContractPagesListEnvelopeOnlyPages(t *testing.T) {
	h := newAccessHarness(t)
	admin, token, _ := agtSetup(t, h, "Contract Pages", "pages.read")
	apiv1ContractSeedPost(t, h, admin, "Contract Pages Article", "published", "post")
	apiv1ContractSeedPost(t, h, admin, "Contract Pages About", "published", "page")
	reader := agtCreate(t, h, token, "Contract Pages Reader", "pages.read")

	r := agtDo(h, http.MethodGet, "/api/v1/pages", "", reader.APIKey, "")
	apiv1ContractExpectPaged(t, r, "GET /pages", 1, 1, 20)
	if !strings.Contains(r.body, "Contract Pages About") || strings.Contains(r.body, "Contract Pages Article") {
		t.Errorf("GET /pages must list pages only; body=%q", r.body)
	}
	for _, raw := range apiv1ContractItems(t, apiv1ContractObject(t, r, "pages"), "pages") {
		apiv1ContractExpectKeys(t, raw.(map[string]any), apiv1ContractContentSummaryKeys, "page summary")
	}
}

func TestAPIV1ContractAuthorScopeOnPostsAndPages(t *testing.T) {
	h := newAccessHarness(t)
	admin, _, _ := agtSetup(t, h, "Contract Scope", "posts.read")
	author, authorKey := apiv1ContractAuthor(t, h, "contract-scope-author", "posts.read", "pages.read")
	apiv1ContractSeedPost(t, h, admin, "Scope Admin Post", "published", "post")
	apiv1ContractSeedPost(t, h, admin, "Scope Admin Page", "published", "page")
	apiv1ContractSeedPost(t, h, author, "Scope Author One", "published", "post")
	apiv1ContractSeedPost(t, h, author, "Scope Author Two", "draft", "post")

	// A key owned by an author sees only its own posts.
	list := apiv1ContractExpectPaged(t, agtDo(h, http.MethodGet, "/api/v1/posts", "", authorKey, ""), "author key GET /posts", 2, 1, 20)
	for _, raw := range apiv1ContractItems(t, list, "author key GET /posts") {
		if raw.(map[string]any)["author_id"] != float64(author.ID) {
			t.Errorf("author key sees a post by author_id %v, want %d", raw.(map[string]any)["author_id"], author.ID)
		}
	}
	apiv1ContractExpectPaged(t, agtDo(h, http.MethodGet, "/api/v1/pages", "", authorKey, ""), "author key GET /pages", 0, 1, 20)

	// An admin key whose role_cap is author is limited the same way.
	capped := apiKeysSeedIntegration(t, h, admin, "Contract capped", []string{"posts.read", "pages.read"}, "author", nil, nil)
	cappedList := apiv1ContractExpectPaged(t, agtDo(h, http.MethodGet, "/api/v1/posts", "", capped, ""), "role_cap author GET /posts", 1, 1, 20)
	for _, raw := range apiv1ContractItems(t, cappedList, "role_cap author GET /posts") {
		if raw.(map[string]any)["author_id"] != float64(admin.ID) {
			t.Errorf("role_cap author key sees a post by author_id %v, want its owner %d", raw.(map[string]any)["author_id"], admin.ID)
		}
	}
	apiv1ContractExpectPaged(t, agtDo(h, http.MethodGet, "/api/v1/pages", "", capped, ""), "role_cap author GET /pages", 0, 1, 20)

	// Control: an uncapped admin key still sees everything.
	uncapped := apiKeysSeedIntegration(t, h, admin, "Contract uncapped", []string{"posts.read"}, "", nil, nil)
	apiv1ContractExpectPaged(t, agtDo(h, http.MethodGet, "/api/v1/posts", "", uncapped, ""), "uncapped admin GET /posts", 3, 1, 20)
}

// ---- R1, R2: products and orders ----

func TestAPIV1ContractProductsEnvelopeWithoutCounts(t *testing.T) {
	h := newAccessHarness(t)
	_, token, _ := agtSetup(t, h, "Contract Products", "products.read")
	product := core.Product{Title: "Contract Mug", Slug: "contract-mug", Price: 150, Status: "active"}
	if err := h.db.Create(&product).Error; err != nil {
		t.Fatalf("seed product: %v", err)
	}
	reader := agtCreate(t, h, token, "Contract Products Reader", "products.read")
	apiv1ContractExpectPaged(t, agtDo(h, http.MethodGet, "/api/v1/products", "", reader.APIKey, ""), "GET /products", 1, 1, 20)
	agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/products?per_page=101", "", reader.APIKey, ""), http.StatusBadRequest, "bad_request", "GET /products?per_page=101")
}

func TestAPIV1ContractOrdersEnvelopeAndSummary(t *testing.T) {
	h := newAccessHarness(t)
	_, token, _ := agtSetup(t, h, "Contract Orders", "orders.read")
	apiv1ContractSeedOrder(t, h)
	reader := agtCreate(t, h, token, "Contract Orders Reader", "orders.read")

	r := agtDo(h, http.MethodGet, "/api/v1/orders", "", reader.APIKey, "")
	obj := apiv1ContractExpectPaged(t, r, "GET /orders", 1, 1, 20)
	items := apiv1ContractItems(t, obj, "GET /orders")
	if len(items) != 1 {
		t.Fatalf("GET /orders items=%d, want 1", len(items))
	}
	order := items[0].(map[string]any)
	apiv1ContractExpectKeys(t, order, apiv1ContractOrderSummaryKeys, "order summary")
	orderItems, _ := order["items"].([]any)
	if len(orderItems) != 1 {
		t.Fatalf("order items=%d, want 1", len(orderItems))
	}
	apiv1ContractExpectKeys(t, orderItems[0].(map[string]any), apiv1ContractOrderItemKeys, "order summary item")
	agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/orders?per_page=101", "", reader.APIKey, ""), http.StatusBadRequest, "bad_request", "GET /orders?per_page=101")
}

// TestAPIV1ContractAdminListsKeepTheirScreenShape guards the admin API: the v1 changes must not
// move the admin products, orders or media lists.
func TestAPIV1ContractAdminListsKeepTheirScreenShape(t *testing.T) {
	h := newAccessHarness(t)
	_, token, _ := agtSetup(t, h, "Contract Admin Lists", "orders.read")
	product := core.Product{Title: "Admin Mug", Slug: "contract-admin-mug", Price: 150, Status: "active"}
	if err := h.db.Create(&product).Error; err != nil {
		t.Fatalf("seed product: %v", err)
	}
	apiv1ContractSeedOrder(t, h)

	products := apiv1ContractObject(t, agtAdmin(h, token, http.MethodGet, "/api/admin/products", ""), "admin products")
	if _, ok := products["counts"]; !ok {
		t.Error("GET /api/admin/products lost counts")
	}
	clamped := agtAdmin(h, token, http.MethodGet, "/api/admin/products?per_page=500", "")
	agtExpectOK(t, clamped, "GET /api/admin/products?per_page=500")
	if got := apiv1ContractObject(t, clamped, "admin products per_page=500"); got["per_page"] != float64(100) {
		t.Errorf("GET /api/admin/products?per_page=500 per_page=%v, want the clamp to 100", got["per_page"])
	}

	orders := agtAdmin(h, token, http.MethodGet, "/api/admin/orders", "")
	agtExpectOK(t, orders, "GET /api/admin/orders")
	adminOrders := apiv1ContractObject(t, orders, "admin orders")
	for _, key := range []string{"counts", "kpi"} {
		if _, ok := adminOrders[key]; !ok {
			t.Errorf("GET /api/admin/orders lost %s", key)
		}
	}
	if items := apiv1ContractItems(t, adminOrders, "admin orders"); len(items) == 1 {
		order := items[0].(map[string]any)
		if _, ok := order["total"]; !ok {
			t.Error("admin order lost its total duplicate key")
		}
		if lines, _ := order["items"].([]any); len(lines) != 1 {
			t.Errorf("admin order items=%v, want 1 line", order["items"])
		} else if _, ok := lines[0].(map[string]any)["qty"]; !ok {
			t.Error("admin order line lost its qty duplicate key")
		}
	}
	bad := agtAdmin(h, token, http.MethodGet, "/api/admin/orders?per_page=500", "")
	if bad.code != http.StatusBadRequest {
		t.Errorf("GET /api/admin/orders?per_page=500 returned %d, want 400", bad.code)
	}
	var legacy map[string]any
	if err := json.Unmarshal([]byte(bad.body), &legacy); err != nil || legacy["error"] == nil {
		t.Errorf("GET /api/admin/orders?per_page=500 body=%q, want {\"error\": ...}", bad.body)
	}
}

// ---- R1, R2: media files ----

func TestAPIV1ContractMediaFilesStrictParamsOnV1OnlyClampOnAdmin(t *testing.T) {
	h := newAccessHarness(t)
	_, token, reader := agtSetup(t, h, "Contract Media", "media.read")
	agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/media/files?per_page=101", "", reader.APIKey, ""), http.StatusBadRequest, "bad_request", "v1 GET /media/files?per_page=101")
	env := agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/media/files?page=0", "", reader.APIKey, ""), http.StatusBadRequest, "bad_request", "v1 GET /media/files?page=0")
	if env.Message != "Invalid page" || env.Details["param"] != "page" {
		t.Errorf("v1 page=0 message=%q details=%v, want Invalid page with param page", env.Message, env.Details)
	}
	apiv1ContractExpectPaged(t, agtDo(h, http.MethodGet, "/api/v1/media/files", "", reader.APIKey, ""), "v1 GET /media/files", 0, 1, 20)
	admin := agtAdmin(h, token, http.MethodGet, "/api/admin/media/files?per_page=101", "")
	agtExpectOK(t, admin, "admin GET /media/files?per_page=101")
}

// ---- R1: collection envelopes ----

func TestAPIV1ContractTaxonomyAndSettingsCollectionEnvelopes(t *testing.T) {
	h := newAccessHarness(t)
	_, token, _ := agtSetup(t, h, "Contract Taxonomy", "posts.read", "settings.read")
	if err := h.db.Create(&core.Category{Name: "Contract Cat", Slug: "contract-cat"}).Error; err != nil {
		t.Fatalf("seed category: %v", err)
	}
	if err := h.db.Create(&core.Tag{Name: "contract-tag", Slug: "contract-tag"}).Error; err != nil {
		t.Fatalf("seed tag: %v", err)
	}
	maintenanceSetOption(t, h, "site_title", "Contract 測試站")
	maintenanceSetOption(t, h, "payment_ecpay_hash_key", "hk-live-0123456789")
	reader := agtCreate(t, h, token, "Contract Taxonomy Reader", "posts.read", "settings.read")

	for _, path := range []string{"/api/v1/categories", "/api/v1/tags"} {
		items, _ := apiv1ContractExpectCollection(t, agtDo(h, http.MethodGet, path, "", reader.APIKey, ""), "GET "+path)
		if len(items) == 0 {
			t.Errorf("GET %s returned no items; the seed should be listed", path)
		}
	}
	settings := agtDo(h, http.MethodGet, "/api/v1/settings", "", reader.APIKey, "")
	items, _ := apiv1ContractExpectCollection(t, settings, "GET /api/v1/settings")
	values := map[string]string{}
	for _, raw := range items {
		o, _ := raw.(map[string]any)
		key, _ := o["key"].(string)
		value, _ := o["value"].(string)
		values[key] = value
	}
	if values["site_title"] != "Contract 測試站" {
		t.Errorf("settings site_title=%q, want Contract 測試站", values["site_title"])
	}
	if values["payment_ecpay_hash_key"] != "****6789" || strings.Contains(settings.body, "hk-live-0123456789") {
		t.Errorf("settings must mask the ECPay hash key; value=%q", values["payment_ecpay_hash_key"])
	}
}

func TestAPIV1ContractThemesAndPluginsEmptySiteAnswerEmptyArray(t *testing.T) {
	h := newAccessHarness(t)
	_, token, _ := agtSetup(t, h, "Contract Empty Site", "themes.read", "plugins.read")
	// The harness copies the theme tree into a temporary working directory; an empty site has no themes.
	if err := os.RemoveAll(filepath.Join("web", "themes")); err != nil {
		t.Fatalf("empty the theme directory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join("web", "themes"), 0o755); err != nil {
		t.Fatalf("recreate the theme directory: %v", err)
	}
	reader := agtCreate(t, h, token, "Contract Empty Reader", "themes.read", "plugins.read")
	for _, path := range []string{"/api/v1/themes", "/api/v1/plugins"} {
		items, _ := apiv1ContractExpectCollection(t, agtDo(h, http.MethodGet, path, "", reader.APIKey, ""), "GET "+path+" on an empty site")
		if items == nil || len(items) != 0 {
			t.Errorf("GET %s items=%v, want []", path, items)
		}
	}
}

// ---- OpenAPI ----

func TestAPIV1ContractOpenAPIListSchemasAreObjectEnvelopes(t *testing.T) {
	h := newAccessHarness(t)
	doc := apiv1ContractDoc(t, h)
	for _, path := range []string{"/api/v1/categories", "/api/v1/tags", "/api/v1/themes", "/api/v1/plugins", "/api/v1/settings"} {
		schema := apiv1ContractResponseSchema(doc, apiv1ContractOp(doc, path, "get"), "200")
		if schema["type"] != "object" || apiv1ContractChild(doc, schema, "properties")["items"] == nil {
			t.Errorf("GET %s 200 schema=%v, want an object with properties.items", path, schema)
		}
	}
	for _, path := range []string{"/api/v1/posts", "/api/v1/pages", "/api/v1/products", "/api/v1/orders", "/api/v1/media/files"} {
		schema := apiv1ContractResponseSchema(doc, apiv1ContractOp(doc, path, "get"), "200")
		if schema["type"] != "object" || apiv1ContractChild(doc, schema, "properties")["items"] == nil {
			t.Errorf("GET %s 200 schema=%v, want an object with properties.items", path, schema)
		}
	}
}

func TestAPIV1ContractOpenAPIPostsPagesOrdersItemSchemas(t *testing.T) {
	h := newAccessHarness(t)
	doc := apiv1ContractDoc(t, h)
	for _, path := range []string{"/api/v1/posts", "/api/v1/pages"} {
		op := apiv1ContractOp(doc, path, "get")
		names := map[string]bool{}
		params, _ := op["parameters"].([]any)
		for _, raw := range params {
			p, _ := agtDeref(doc, raw).(map[string]any)
			name, _ := p["name"].(string)
			names[name] = true
		}
		for _, want := range []string{"page", "per_page", "status", "q"} {
			if !names[want] {
				t.Errorf("GET %s has no %q query parameter", path, want)
			}
		}
		itemProps := apiv1ContractChild(doc, apiv1ContractChild(doc, apiv1ContractChild(doc, apiv1ContractResponseSchema(doc, op, "200"), "properties"), "items"), "items")
		props := apiv1ContractChild(doc, itemProps, "properties")
		got := make(map[string]any, len(props))
		for k, v := range props {
			got[k] = v
		}
		apiv1ContractExpectKeys(t, got, apiv1ContractContentSummaryKeys, "OpenAPI "+path+" items.items.properties")
	}
	post := apiv1ContractOp(doc, "/api/v1/posts/{id}", "get")
	single := apiv1ContractChild(doc, apiv1ContractResponseSchema(doc, post, "200"), "properties")
	if single["content"] == nil {
		t.Error("GET /posts/{id} 200 schema omits content")
	}
	orderProps := apiv1ContractChild(doc, apiv1ContractChild(doc, apiv1ContractChild(doc, apiv1ContractResponseSchema(doc, apiv1ContractOp(doc, "/api/v1/orders", "get"), "200"), "properties"), "items"), "items")
	got := map[string]any{}
	for k, v := range apiv1ContractChild(doc, orderProps, "properties") {
		got[k] = v
	}
	apiv1ContractExpectKeys(t, got, apiv1ContractOrderSummaryKeys, "OpenAPI /orders items.items.properties")
}

func TestAPIV1ContractOpenAPIEveryOperationDeclaresRateLimitedResponse(t *testing.T) {
	h := newAccessHarness(t)
	doc := apiv1ContractDoc(t, h)
	paths, _ := doc["paths"].(map[string]any)
	count := 0
	for path, rawItem := range paths {
		item, _ := rawItem.(map[string]any)
		for method, rawOp := range item {
			if !slices.Contains([]string{"get", "put", "post", "delete", "patch"}, method) {
				continue
			}
			count++
			op, _ := rawOp.(map[string]any)
			responses, _ := op["responses"].(map[string]any)
			resp, _ := agtDeref(doc, responses["429"]).(map[string]any)
			if resp == nil {
				t.Errorf("%s %s has no 429 response", method, path)
				continue
			}
			headers, _ := agtDeref(doc, resp["headers"]).(map[string]any)
			for _, name := range apiv1ContractRateHeaders {
				if _, ok := headers[name]; !ok {
					t.Errorf("%s %s 429 does not declare %s", method, path, name)
				}
			}
		}
	}
	if count == 0 {
		t.Fatal("OpenAPI document has no operations")
	}
}

func TestAPIV1ContractIndexDeclaresPaginationAndRateLimitedCode(t *testing.T) {
	h := newAccessHarness(t)
	index := apiv1ContractObject(t, agtDo(h, http.MethodGet, "/api/v1", "", "", ""), "discovery index")
	pagination, ok := index["pagination"].(map[string]any)
	if !ok {
		t.Fatalf("GET /api/v1 has no pagination object; keys=%v", index)
	}
	text, _ := json.Marshal(pagination)
	for _, want := range []string{"per_page", "cursor", "/api/v1/me/events"} {
		if !strings.Contains(string(text), want) {
			t.Errorf("pagination block %s does not describe %q", text, want)
		}
	}
	errors, _ := index["errors"].(map[string]any)
	codes, _ := errors["codes"].([]any)
	found := false
	for _, raw := range codes {
		c, _ := raw.(map[string]any)
		if c["code"] == "rate_limited" && c["status"] == float64(429) {
			found = true
		}
	}
	if !found {
		t.Errorf("errors.codes=%v, want rate_limited with status 429", codes)
	}
}

// ---- R5: GET /api ----

func TestAPIV1ContractAPIIndexGETAndHEAD(t *testing.T) {
	h := newAccessHarness(t)
	head := agtDo(h, http.MethodHead, "/api", "", "", "")
	agtExpectOK(t, head, "HEAD /api")
	if head.header.Get("Cache-Control") != "no-store" {
		t.Errorf("HEAD /api Cache-Control=%q, want no-store", head.header.Get("Cache-Control"))
	}

	get := agtDo(h, http.MethodGet, "/api", "", "", "")
	agtExpectOK(t, get, "GET /api")
	if get.header.Get("Cache-Control") != "no-store" {
		t.Errorf("GET /api Cache-Control=%q, want no-store", get.header.Get("Cache-Control"))
	}
	doc := apiv1ContractObject(t, get, "GET /api")
	apiv1ContractExpectKeys(t, doc, []string{"name", "apis", "admin_api"}, "GET /api")
	if doc["name"] != "Jelly CMS" {
		t.Errorf("name=%v, want Jelly CMS", doc["name"])
	}
	want := []any{
		map[string]any{"name": "v1", "audience": "machine", "auth": "access_key", "base_path": "/api/v1", "index_url": "/api/v1", "openapi_url": "/api/v1/openapi.json"},
		map[string]any{"name": "public", "audience": "anonymous", "auth": "none", "base_path": "/api/public", "index_url": "/api/public", "openapi_url": "/api/public/openapi.json"},
		map[string]any{"name": "shop", "audience": "anonymous", "auth": "none", "base_path": "/api/shop", "index_url": "/api/shop", "openapi_url": "/api/shop/openapi.json"},
		map[string]any{"name": "auth", "audience": "browser", "auth": "browser_session", "base_path": "/api/auth", "index_url": "/api/auth", "openapi_url": "/api/auth/openapi.json"},
	}
	if !reflect.DeepEqual(doc["apis"], want) {
		t.Errorf("apis=%v, want exactly %v", doc["apis"], want)
	}
	wantAdmin := map[string]any{"base_path": "/api/admin", "audience": "admin_ui", "auth": "browser_session", "note": "Private API of the admin screens; use /api/v1 with an access key instead."}
	if !reflect.DeepEqual(doc["admin_api"], wantAdmin) {
		t.Errorf("admin_api=%v, want %v", doc["admin_api"], wantAdmin)
	}
	apis, _ := doc["apis"].([]any)
	for _, raw := range apis {
		entry, _ := raw.(map[string]any)
		for _, key := range []string{"index_url", "openapi_url"} {
			path, _ := entry[key].(string)
			agtExpectOK(t, agtDo(h, http.MethodGet, path, "", "", ""), "GET "+path+" from the index")
		}
	}
}

func TestAPIV1ContractAPIIndexOpenDuringMaintenance(t *testing.T) {
	h := newAccessHarness(t)
	maintenanceEnable(t, h, 1)
	agtExpectOK(t, agtDo(h, http.MethodGet, "/api", "", "", ""), "GET /api during maintenance")
}

// ---- R4: rate limits ----

func TestAPIV1ContractIntegrationLimitWindowResetsNextMinute(t *testing.T) {
	t.Setenv("JELLY_API_RATE_LIMIT_PER_INTEGRATION", "5")
	t.Setenv("JELLY_TRUSTED_PROXIES", "")
	clock := apiv1ContractInstallClock(t)
	h := newAccessHarness(t)
	_, token, a := agtSetup(t, h, "Contract Limit", "posts.read")
	const ip = "192.0.2.10:1000"
	for i := 1; i <= 5; i++ {
		where := fmt.Sprintf("request %d within the limit", i)
		r := agtDo(h, http.MethodGet, "/api/v1/me", "", a.APIKey, ip)
		agtExpectOK(t, r, where)
		apiv1ContractHeader(t, r, "RateLimit-Limit", "5", where)
		apiv1ContractHeader(t, r, "RateLimit-Remaining", strconv.Itoa(5-i), where)
		apiv1ContractSeconds(t, r, "RateLimit-Reset", where)
	}
	before := apiv1ContractLastUsed(t, h, token, a.Agent.ID, a.APIKey)

	r := agtDo(h, http.MethodGet, "/api/v1/me", "", a.APIKey, ip)
	env := agtExpectError(t, r, http.StatusTooManyRequests, "rate_limited", "sixth request")
	if env.Details["scope"] != "integration" || env.Details["limit"] != float64(5) || env.Details["window_seconds"] != float64(60) {
		t.Errorf("429 details=%v, want scope integration, limit 5, window_seconds 60", env.Details)
	}
	retryAfter := apiv1ContractSeconds(t, r, "Retry-After", "sixth request")
	reset := apiv1ContractSeconds(t, r, "RateLimit-Reset", "sixth request")
	if retryAfter != reset || env.Details["retry_after"] != float64(retryAfter) {
		t.Errorf("Retry-After=%d RateLimit-Reset=%d details.retry_after=%v, want all equal", retryAfter, reset, env.Details["retry_after"])
	}
	apiv1ContractHeader(t, r, "RateLimit-Remaining", "0", "sixth request")
	apiv1ContractHeader(t, r, "RateLimit-Limit", "5", "sixth request")

	after := apiv1ContractLastUsed(t, h, token, a.Agent.ID, a.APIKey)
	if (before == nil) != (after == nil) || (before != nil && *before != *after) {
		t.Errorf("last_used_at changed on a 429: before=%v after=%v, want no update", before, after)
	}

	*clock = clock.Add(time.Minute)
	next := agtDo(h, http.MethodGet, "/api/v1/me", "", a.APIKey, ip)
	agtExpectOK(t, next, "first request in the next minute")
	apiv1ContractHeader(t, next, "RateLimit-Remaining", "4", "first request in the next minute")
}

func TestAPIV1ContractIntegrationLimitSharedAcrossGraceKeys(t *testing.T) {
	t.Setenv("JELLY_API_RATE_LIMIT_PER_INTEGRATION", "5")
	t.Setenv("JELLY_TRUSTED_PROXIES", "")
	apiv1ContractInstallClock(t)
	h := newAccessHarness(t)
	_, token, a := agtSetup(t, h, "Contract Grace", "posts.read")
	const ip = "192.0.2.20:1000"
	for i := 0; i < 5; i++ {
		agtExpectOK(t, agtDo(h, http.MethodGet, "/api/v1/me", "", a.APIKey, ip), "old key within the limit")
	}
	rotated := agtRotate(t, h, token, a.Agent.ID, `{"mode":"grace","grace_minutes":30}`)
	agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/me", "", rotated.APIKey, ip), http.StatusTooManyRequests, "rate_limited", "new key right after rotation")
	agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/me", "", a.APIKey, ip), http.StatusTooManyRequests, "rate_limited", "old key during the grace period")
}

func TestAPIV1ContractIntegrationLimitIsPerIntegration(t *testing.T) {
	t.Setenv("JELLY_API_RATE_LIMIT_PER_INTEGRATION", "5")
	t.Setenv("JELLY_TRUSTED_PROXIES", "")
	apiv1ContractInstallClock(t)
	h := newAccessHarness(t)
	_, token, a := agtSetup(t, h, "Contract Limit A", "posts.read")
	b := agtCreate(t, h, token, "Contract Limit B", "posts.read")
	const ipA, ipB = "192.0.2.30:1000", "192.0.2.31:1000"
	for i := 0; i < 5; i++ {
		agtExpectOK(t, agtDo(h, http.MethodGet, "/api/v1/me", "", a.APIKey, ipA), "A within the limit")
	}
	agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/me", "", a.APIKey, ipA), http.StatusTooManyRequests, "rate_limited", "A over the limit")
	for i := 0; i < 5; i++ {
		agtExpectOK(t, agtDo(h, http.MethodGet, "/api/v1/me", "", b.APIKey, ipB), "B while A is over the limit")
	}
}

func TestAPIV1ContractIntegrationLimitCountsPermissionDenials(t *testing.T) {
	t.Setenv("JELLY_API_RATE_LIMIT_PER_INTEGRATION", "5")
	t.Setenv("JELLY_TRUSTED_PROXIES", "")
	apiv1ContractInstallClock(t)
	h := newAccessHarness(t)
	_, _, c := agtSetup(t, h, "Contract Pages Only", "pages.read")
	const ip = "192.0.2.40:1000"
	for i := 1; i <= 5; i++ {
		where := fmt.Sprintf("denied request %d", i)
		r := agtDo(h, http.MethodGet, "/api/v1/posts", "", c.APIKey, ip)
		agtExpectError(t, r, http.StatusForbidden, "permission_missing", where)
		apiv1ContractHeader(t, r, "RateLimit-Limit", "5", where)
		apiv1ContractHeader(t, r, "RateLimit-Remaining", strconv.Itoa(5-i), where)
		if r.header.Get("RateLimit-Reset") == "" || r.header.Get("Retry-After") != "" {
			t.Errorf("%s: RateLimit-Reset=%q Retry-After=%q, want Reset and no Retry-After", where, r.header.Get("RateLimit-Reset"), r.header.Get("Retry-After"))
		}
	}
	env := agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/posts", "", c.APIKey, ip), http.StatusTooManyRequests, "rate_limited", "sixth denied request")
	if env.Details["scope"] != "integration" {
		t.Errorf("429 details=%v, want scope integration rather than 403", env.Details)
	}
}

func TestAPIV1ContractIntegrationLimitCountsCatchAllRoutes(t *testing.T) {
	t.Setenv("JELLY_API_RATE_LIMIT_PER_INTEGRATION", "5")
	t.Setenv("JELLY_TRUSTED_PROXIES", "")
	apiv1ContractInstallClock(t)
	h := newAccessHarness(t)
	_, _, a := agtSetup(t, h, "Contract Catch All", "posts.read")
	const ip = "192.0.2.50:1000"
	unknown := agtDo(h, http.MethodGet, "/api/v1/no-such-endpoint", "", a.APIKey, ip)
	agtExpectError(t, unknown, http.StatusNotFound, "not_found", "unknown path with a key")
	apiv1ContractHeader(t, unknown, "RateLimit-Remaining", "4", "unknown path with a key")
	for i := 1; i <= 4; i++ {
		agtExpectOK(t, agtDo(h, http.MethodGet, "/api/v1/me", "", a.APIKey, ip), "request after the unknown path")
	}
	agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/me", "", a.APIKey, ip), http.StatusTooManyRequests, "rate_limited", "sixth counted request")
}

func TestAPIV1ContractIntegrationLimitZeroDisablesCounter(t *testing.T) {
	t.Setenv("JELLY_API_RATE_LIMIT_PER_INTEGRATION", "0")
	t.Setenv("JELLY_TRUSTED_PROXIES", "")
	apiv1ContractInstallClock(t)
	h := newAccessHarness(t)
	_, _, a := agtSetup(t, h, "Contract Zero", "posts.read")
	for i := 1; i <= 20; i++ {
		r := agtDo(h, http.MethodGet, "/api/v1/me", "", a.APIKey, "192.0.2.60:1000")
		agtExpectOK(t, r, fmt.Sprintf("request %d with the counter off", i))
		for _, name := range []string{"RateLimit-Limit", "RateLimit-Remaining", "RateLimit-Reset"} {
			if r.header.Get(name) != "" {
				t.Errorf("request %d with the counter off sends %s=%q", i, name, r.header.Get(name))
			}
		}
	}
}

func TestAPIV1ContractIntegrationLimitInvalidEnvUsesDefault(t *testing.T) {
	for _, value := range []string{"abc", "-3"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("JELLY_API_RATE_LIMIT_PER_INTEGRATION", value)
			t.Setenv("JELLY_TRUSTED_PROXIES", "")
			apiv1ContractInstallClock(t)
			h := newAccessHarness(t)
			_, _, a := agtSetup(t, h, "Contract Default", "posts.read")
			r := agtDo(h, http.MethodGet, "/api/v1/me", "", a.APIKey, "192.0.2.70:1000")
			agtExpectOK(t, r, "request with an invalid limit")
			apiv1ContractHeader(t, r, "RateLimit-Limit", "600", "invalid limit "+value)
		})
	}
}

func TestAPIV1ContractAuthFailuresLimitPerIPAndKeepValidKeys(t *testing.T) {
	t.Setenv("JELLY_API_RATE_LIMIT_AUTH_FAILURES_PER_IP", "3")
	t.Setenv("JELLY_TRUSTED_PROXIES", "")
	apiv1ContractInstallClock(t)
	h := newAccessHarness(t)
	_, _, a := agtSetup(t, h, "Contract Auth Failures", "posts.read")
	const ip = "203.0.113.7:4000"
	for i := 1; i <= 3; i++ {
		agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/me", "", apiv1ContractBadKey, ip), http.StatusUnauthorized, "key_invalid", fmt.Sprintf("bad key %d", i))
	}
	limited := agtDo(h, http.MethodGet, "/api/v1/me", "", "not-a-key", ip)
	env := agtExpectError(t, limited, http.StatusTooManyRequests, "rate_limited", "fourth bad key from the same IP")
	if env.Details["scope"] != "auth_failures" || env.Details["limit"] != float64(3) {
		t.Errorf("429 details=%v, want scope auth_failures and limit 3", env.Details)
	}
	apiv1ContractSeconds(t, limited, "Retry-After", "fourth bad key")
	agtExpectOK(t, agtDo(h, http.MethodGet, "/api/v1/me", "", a.APIKey, ip), "valid key from the limited IP")
	agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/me", "", apiv1ContractBadKey, "203.0.113.8:4000"), http.StatusUnauthorized, "key_invalid", "bad key from another IPv4 address")
}

func TestAPIV1ContractAuthFailuresSkipRevokedDisabledAndMissingKeys(t *testing.T) {
	t.Setenv("JELLY_API_RATE_LIMIT_AUTH_FAILURES_PER_IP", "3")
	t.Setenv("JELLY_TRUSTED_PROXIES", "")
	apiv1ContractInstallClock(t)
	h := newAccessHarness(t)
	_, token, a := agtSetup(t, h, "Contract Revoked", "posts.read")
	rotated := agtRotate(t, h, token, a.Agent.ID, `{"mode":"immediate"}`)
	b := agtCreate(t, h, token, "Contract Disabled", "posts.read")
	agtExpectOK(t, agtAdmin(h, token, http.MethodPost, fmt.Sprintf("/api/admin/integrations/%d/disable", b.Agent.ID), ""), "disable integration B")

	// Over the limit from one IP: three bad keys answer 401, the fourth answers 429.
	const ip = "203.0.113.9:4000"
	for i := 1; i <= 3; i++ {
		agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/me", "", apiv1ContractBadKey, ip), http.StatusUnauthorized, "key_invalid", fmt.Sprintf("bad key %d", i))
	}
	agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/me", "", apiv1ContractBadKey, ip), http.StatusTooManyRequests, "rate_limited", "fourth bad key")

	// Over the limit, revoked, disabled and missing keys still get their own 401, never 429.
	for i := 1; i <= 10; i++ {
		agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/me", "", a.APIKey, ip), http.StatusUnauthorized, "key_revoked", fmt.Sprintf("revoked key %d over the limit", i))
		agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/me", "", b.APIKey, ip), http.StatusUnauthorized, "integration_disabled", fmt.Sprintf("disabled key %d over the limit", i))
		agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/me", "", "", ip), http.StatusUnauthorized, "key_missing", fmt.Sprintf("no key %d over the limit", i))
	}
	agtExpectOK(t, agtDo(h, http.MethodGet, "/api/v1/me", "", rotated.APIKey, ip), "valid key from the limited IP")

	// Those answers were not counted: from a fresh IP the same ten of each leave the three bad-key budget intact.
	const other = "203.0.113.10:4000"
	for i := 1; i <= 10; i++ {
		agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/me", "", a.APIKey, other), http.StatusUnauthorized, "key_revoked", fmt.Sprintf("revoked key %d from a fresh IP", i))
		agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/me", "", b.APIKey, other), http.StatusUnauthorized, "integration_disabled", fmt.Sprintf("disabled key %d from a fresh IP", i))
		agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/me", "", "", other), http.StatusUnauthorized, "key_missing", fmt.Sprintf("no key %d from a fresh IP", i))
	}
	for i := 1; i <= 3; i++ {
		agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/me", "", apiv1ContractBadKey, other), http.StatusUnauthorized, "key_invalid", fmt.Sprintf("bad key %d from the fresh IP", i))
	}
	agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/me", "", apiv1ContractBadKey, other), http.StatusTooManyRequests, "rate_limited", "fourth bad key from the fresh IP")
}

func TestAPIV1ContractAuthFailuresShareIPv6Prefix64(t *testing.T) {
	t.Setenv("JELLY_API_RATE_LIMIT_AUTH_FAILURES_PER_IP", "3")
	t.Setenv("JELLY_TRUSTED_PROXIES", "")
	apiv1ContractInstallClock(t)
	h := newAccessHarness(t)
	for i := 1; i <= 3; i++ {
		agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/me", "", apiv1ContractBadKey, "[2001:db8:abcd:12::5]:5555"), http.StatusUnauthorized, "key_invalid", fmt.Sprintf("bad key %d from the first address", i))
	}
	env := agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/me", "", apiv1ContractBadKey, "[2001:db8:abcd:12:ffff::1]:5555"), http.StatusTooManyRequests, "rate_limited", "same /64 another address")
	if env.Details["scope"] != "auth_failures" {
		t.Errorf("details=%v, want scope auth_failures", env.Details)
	}
	agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/me", "", apiv1ContractBadKey, "[2001:db8:abcd:13::5]:5555"), http.StatusUnauthorized, "key_invalid", "another /64")
	agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/me", "", apiv1ContractBadKey, "198.51.100.9:1"), http.StatusUnauthorized, "key_invalid", "another IPv4 address")
}

func TestAPIV1ContractAuthFailuresIgnoreForwardedForWithoutTrustedProxy(t *testing.T) {
	t.Setenv("JELLY_API_RATE_LIMIT_AUTH_FAILURES_PER_IP", "3")
	t.Setenv("JELLY_TRUSTED_PROXIES", "")
	apiv1ContractInstallClock(t)
	h := newAccessHarness(t)
	const remote = "203.0.113.50:1"
	for i := 1; i <= 3; i++ {
		r := apiv1ContractForwarded(h, apiv1ContractBadKey, remote, fmt.Sprintf("198.51.100.%d", i))
		agtExpectError(t, r, http.StatusUnauthorized, "key_invalid", fmt.Sprintf("bad key %d", i))
	}
	r := apiv1ContractForwarded(h, apiv1ContractBadKey, remote, "198.51.100.99")
	agtExpectError(t, r, http.StatusTooManyRequests, "rate_limited", "fourth attempt with a new forwarded address")
}

// apiv1ContractForwarded sends GET /api/v1/me from remote with an X-Forwarded-For header.
func apiv1ContractForwarded(h *accessHarness, bearer, remote, forwarded string) agtResult {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("X-Forwarded-For", forwarded)
	req.RemoteAddr = remote
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	return agtResult{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone()}
}

func TestAPIV1ContractAuthFailuresZeroDisablesCounter(t *testing.T) {
	t.Setenv("JELLY_API_RATE_LIMIT_AUTH_FAILURES_PER_IP", "0")
	t.Setenv("JELLY_TRUSTED_PROXIES", "")
	apiv1ContractInstallClock(t)
	h := newAccessHarness(t)
	for i := 1; i <= 10; i++ {
		agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/me", "", apiv1ContractBadKey, "203.0.113.80:1"), http.StatusUnauthorized, "key_invalid", fmt.Sprintf("bad key %d with the counter off", i))
	}
}

func TestAPIV1ContractPublicDiscoveryLimitSharedWithOpenAPI(t *testing.T) {
	t.Setenv("JELLY_API_RATE_LIMIT_PUBLIC_PER_IP", "2")
	t.Setenv("JELLY_TRUSTED_PROXIES", "")
	apiv1ContractInstallClock(t)
	h := newAccessHarness(t)
	const ip = "198.51.100.20:1000"
	agtExpectOK(t, agtDo(h, http.MethodGet, "/api/v1", "", "", ip), "first GET /api/v1")
	agtExpectOK(t, agtDo(h, http.MethodGet, "/api/v1/openapi.json", "", "", ip), "first GET /api/v1/openapi.json")
	env := agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1", "", "", ip), http.StatusTooManyRequests, "rate_limited", "third GET /api/v1")
	if env.Details["scope"] != "public" {
		t.Errorf("429 details=%v, want scope public", env.Details)
	}
	agtExpectError(t, agtDo(h, http.MethodGet, "/api/v1/openapi.json", "", "", ip), http.StatusTooManyRequests, "rate_limited", "GET /api/v1/openapi.json shares the count")
	agtExpectOK(t, agtDo(h, http.MethodGet, "/api/v1", "", "", "198.51.100.21:1000"), "another IP is not affected")
}
