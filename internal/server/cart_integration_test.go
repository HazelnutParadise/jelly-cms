package server

// Cart integration tests for ticket 07, section E4 (contract: /api/cart* in docs/tickets/07-shop.md). They call the
// public routes against the dedicated PostgreSQL database and are skipped unless JELLY_TEST_POSTGRES=1. They are
// written before the implementation and must fail until it exists. Do not edit them to match an implementation.
//
// This file also holds the helpers shared by the shop test files: a cookie-carrying browser, option and product
// fixtures, order and coupon seeds, and the assertion helpers. Other shop test files reuse them by name.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm/clause"
)

const (
	shopCartCookie    = "jelly_cart"
	shopSandboxSecret = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
)

// shopBrowserSeq gives every browser its own client address. The rate limiters are process-wide, so a shared address
// would let one test's traffic trip a limit in another test. Tests that measure a per-IP limit set remote themselves.
var shopBrowserSeq atomic.Int64

var shopHex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// shopResponse is one HTTP answer, with the cookies it set.
type shopResponse struct {
	code     int
	body     string
	header   http.Header
	cookies  []*http.Cookie
	panicked any
}

// shopBrowser is one visitor: a cookie jar and an optional bearer token. Its methods use no testing calls, so a
// goroutine may drive its own browser.
type shopBrowser struct {
	h      *accessHarness
	jar    map[string]*http.Cookie
	token  string
	remote string
}

func newShopBrowser(h *accessHarness) *shopBrowser {
	n := shopBrowserSeq.Add(1)
	remote := fmt.Sprintf("198.18.%d.%d:40000", (n/250)%250, n%250+1)
	return &shopBrowser{h: h, jar: map[string]*http.Cookie{}, remote: remote}
}

// do sends one request with the browser's cookies, then stores the cookies the response sets. Max-Age<0 or an
// empty value removes a cookie from the jar.
func (b *shopBrowser) do(method, path, body, contentType string, headers map[string]string) shopResponse {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.RemoteAddr = b.remote
	// A token for /api/admin or an interactive /api/account call travels as the
	// jwt_token cookie (ticket 23), so a jar cookie of that name must not compete with it.
	tokenAsCookie := b.token != "" && usesBrowserLogin(req)
	for _, cookie := range b.jar {
		if tokenAsCookie && cookie.Name == "jwt_token" {
			continue
		}
		req.AddCookie(cookie)
	}
	if contentType != "" {
		req.Header.Set(echo.HeaderContentType, contentType)
	}
	if b.token != "" {
		setAccessToken(req, b.token)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	var panicked any
	func() {
		defer func() { panicked = recover() }()
		b.h.echo.ServeHTTP(rec, req)
	}()
	resp := shopResponse{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone(), panicked: panicked}
	resp.cookies = (&http.Response{Header: rec.Header()}).Cookies()
	for _, cookie := range resp.cookies {
		if cookie.Value == "" || cookie.MaxAge < 0 {
			delete(b.jar, cookie.Name)
			continue
		}
		b.jar[cookie.Name] = cookie
	}
	return resp
}

// send writes a browser request. It carries the same-origin proof (X-Jelly-CSRF) that the harness uses for trusted
// in-process browser writes.
func (b *shopBrowser) send(method, path string, payload any) shopResponse {
	return b.sendWith(method, path, payload, nil)
}

func (b *shopBrowser) sendWith(method, path string, payload any, headers map[string]string) shopResponse {
	body, contentType := shopEncode(payload)
	merged := map[string]string{"X-Jelly-CSRF": "1"}
	for key, value := range headers {
		merged[key] = value
	}
	return b.do(method, path, body, contentType, merged)
}

func (b *shopBrowser) get(path string) shopResponse {
	return b.do(http.MethodGet, path, "", "", nil)
}

func shopEncode(payload any) (string, string) {
	switch value := payload.(type) {
	case nil:
		return "", ""
	case url.Values:
		return value.Encode(), "application/x-www-form-urlencoded"
	case string:
		return value, echo.MIMEApplicationJSON
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return string(raw), echo.MIMEApplicationJSON
}

func shopWantCode(t *testing.T, r shopResponse, where string, want int) {
	t.Helper()
	if r.panicked != nil {
		t.Fatalf("%s panicked: %v", where, r.panicked)
	}
	if r.code != want {
		t.Fatalf("%s returned %d, want %d; body=%q", where, r.code, want, r.body)
	}
}

// shopWantOK accepts 200, 201 and 204 for successful writes.
func shopWantOK(t *testing.T, r shopResponse, where string) {
	t.Helper()
	if r.panicked != nil {
		t.Fatalf("%s panicked: %v", where, r.panicked)
	}
	if r.code != http.StatusOK && r.code != http.StatusCreated && r.code != http.StatusNoContent {
		t.Fatalf("%s returned %d, want 2xx; body=%q", where, r.code, r.body)
	}
}

func shopDecode(t *testing.T, r shopResponse, where string, into any) {
	t.Helper()
	if err := json.Unmarshal([]byte(r.body), into); err != nil {
		t.Fatalf("%s: decode JSON (status %d): %v; body=%q", where, r.code, err, r.body)
	}
}

// shopJSONID reads the numeric "id" of a JSON object response.
func shopJSONID(t *testing.T, r shopResponse, where string) uint {
	t.Helper()
	var object map[string]any
	shopDecode(t, r, where, &object)
	id, ok := object["id"].(float64)
	if !ok || id <= 0 {
		t.Fatalf("%s: response has no numeric id; body=%q", where, r.body)
	}
	return uint(id)
}

// shopSetOptions upserts core.Option rows.
func shopSetOptions(t *testing.T, h *accessHarness, values map[string]string) {
	t.Helper()
	for key, value := range values {
		row := core.Option{Key: key, Value: value}
		if err := h.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, UpdateAll: true}).Create(&row).Error; err != nil {
			t.Fatalf("save option %s: %v", key, err)
		}
	}
}

// shopSetup turns on the shop for a test: the sandbox gateway (APP_ENV=test, payment_sandbox_enabled=true,
// payment_sandbox_secret holds the hex HMAC key), the manual transfer method and two shipping methods. Money is in
// minor units: home delivery costs 8000 (NT$80) and free shipping is never automatic (free_over 0).
func shopSetup(t *testing.T, h *accessHarness) {
	t.Helper()
	t.Setenv("APP_ENV", "test")
	shopSetOptions(t, h, map[string]string{
		"shop_currency":           "TWD",
		"shop_tax_mode":           "included",
		"shop_tax_rate":           "5",
		"shop_free_shipping_over": "0",
		"payment_sandbox_enabled": "true",
		"payment_sandbox_secret":  shopSandboxSecret,
		"payment_methods":         `{"credit_card":"sandbox","atm":"sandbox","cvs":"sandbox","bank_transfer":"manual_transfer"}`,
		"shop_shipping_methods": `[` +
			`{"id":"home","label":"宅配","fee":8000,"free_over":0,"eta_days":3,"requires_address":true,"enabled":true},` +
			`{"id":"pickup","label":"自取","fee":0,"free_over":0,"eta_days":1,"requires_address":false,"enabled":true},` +
			`{"id":"cvs","label":"超商取貨","fee":6000,"free_over":0,"eta_days":3,"requires_address":false,"enabled":false}]`,
		"shop_manual_transfer_note": "測試銀行 000-1234567，請於 72 小時內匯款",
	})
}

// shopSeedProduct inserts an active product with one default variant (see shopCatalogSeedProduct).
func shopSeedProduct(t *testing.T, h *accessHarness, slug string, price int64, stock int) core.Product {
	t.Helper()
	return shopCatalogSeedProduct(t, h, shopCatalogProduct{title: slug, slug: slug, status: "active", price: price, stock: stock})
}

// shopVariantRows returns the variants of a product in position order.
func shopVariantRows(t *testing.T, h *accessHarness, productID uint) []core.ProductVariant {
	t.Helper()
	var rows []core.ProductVariant
	if err := h.db.Where("product_id = ?", productID).Order("position, id").Find(&rows).Error; err != nil {
		t.Fatalf("list variants of product %d: %v", productID, err)
	}
	return rows
}

// shopSeedCoupon inserts a coupon row directly. Status defaults to active and Scope to all.
func shopSeedCoupon(t *testing.T, h *accessHarness, coupon core.Coupon) core.Coupon {
	t.Helper()
	if coupon.Status == "" {
		coupon.Status = "active"
	}
	if coupon.Scope == "" {
		coupon.Scope = "all"
	}
	if err := h.db.Omit(clause.Associations).Create(&coupon).Error; err != nil {
		t.Fatalf("seed coupon %q: %v", coupon.Code, err)
	}
	return coupon
}

type shopItemSeed struct {
	variantID *uint
	productID *uint
	title     string
	qty       int
	unitPrice int64
}

// shopOrderSeed describes an order row inserted directly, for tests that need an order in a given state.
type shopOrderSeed struct {
	number     string
	email      string
	status     string
	provider   string
	method     string
	userID     *uint
	total      int64
	discount   int64
	created    time.Time
	expiresAt  *time.Time
	paidAt     *time.Time
	shipping   string
	couponID   *uint
	couponCode string
	items      []shopItemSeed
}

// shopSeedOrder inserts an order and its items without going through the checkout code.
func shopSeedOrder(t *testing.T, h *accessHarness, spec shopOrderSeed) core.Order {
	t.Helper()
	created := spec.created
	if created.IsZero() {
		created = time.Now()
	}
	order := core.Order{
		Number: spec.number, Email: spec.email, UserID: spec.userID, Status: spec.status,
		PaymentProvider: spec.provider, PaymentMethod: spec.method, Currency: "TWD",
		TotalAmount: spec.total, Subtotal: spec.total + spec.discount, DiscountTotal: spec.discount,
		CouponID: spec.couponID, CouponCode: spec.couponCode, ExpiresAt: spec.expiresAt, PaidAt: spec.paidAt,
		CreatedAt: created, UpdatedAt: created,
	}
	if spec.shipping != "" {
		order.Shipping = json.RawMessage(spec.shipping)
	}
	if err := h.db.Omit(clause.Associations).Create(&order).Error; err != nil {
		t.Fatalf("seed order %s: %v", spec.number, err)
	}
	for _, item := range spec.items {
		row := core.OrderItem{
			OrderID: order.ID, ProductID: item.productID, VariantID: item.variantID, Title: item.title,
			Quantity: item.qty, Price: item.unitPrice, UnitPrice: item.unitPrice, LineTotal: item.unitPrice * int64(item.qty),
		}
		if err := h.db.Omit(clause.Associations).Create(&row).Error; err != nil {
			t.Fatalf("seed item of order %s: %v", spec.number, err)
		}
	}
	return order
}

// shopOrderRow loads an order by its public number JL-YYYY-NNNN.
func shopOrderRow(t *testing.T, h *accessHarness, number string) core.Order {
	t.Helper()
	var order core.Order
	if err := h.db.Where("number = ?", number).First(&order).Error; err != nil {
		t.Fatalf("load order %s: %v", number, err)
	}
	return order
}

// shopCartItem, shopCartIssue and shopCartView mirror the GET /api/cart response of the contract.
type shopCartItem struct {
	VariantID uint   `json:"variant_id"`
	ProductID uint   `json:"product_id"`
	Qty       int    `json:"qty"`
	UnitPrice int64  `json:"unit_price"`
	LineTotal int64  `json:"line_total"`
	Available bool   `json:"available"`
	MaxQty    int    `json:"max_qty"`
	Title     string `json:"title"`
}

type shopCartIssue struct {
	VariantID uint   `json:"variant_id"`
	Code      string `json:"code"`
}

type shopCartView struct {
	Items             []shopCartItem  `json:"items"`
	Issues            []shopCartIssue `json:"issues"`
	Subtotal          int64           `json:"subtotal"`
	Discount          int64           `json:"discount"`
	Shipping          int64           `json:"shipping"`
	ShippingGapToFree int64           `json:"shipping_gap_to_free"`
	Total             int64           `json:"total"`
	Currency          string          `json:"currency"`
	Coupon            json.RawMessage `json:"coupon"`
}

func (c shopCartView) line(variantID uint) (shopCartItem, bool) {
	for _, item := range c.Items {
		if item.VariantID == variantID {
			return item, true
		}
	}
	return shopCartItem{}, false
}

func (c shopCartView) issueCodes(variantID uint) []string {
	var codes []string
	for _, issue := range c.Issues {
		if issue.VariantID == variantID {
			codes = append(codes, issue.Code)
		}
	}
	return codes
}

func shopCartGet(t *testing.T, b *shopBrowser) shopCartView {
	t.Helper()
	r := b.get("/api/cart")
	shopWantCode(t, r, "GET /api/cart", http.StatusOK)
	var cart shopCartView
	shopDecode(t, r, "GET /api/cart", &cart)
	return cart
}

func shopAddToCart(t *testing.T, b *shopBrowser, variantID uint, qty int) {
	t.Helper()
	shopWantOK(t, b.send(http.MethodPost, "/api/cart/items", map[string]any{"variant_id": variantID, "qty": qty}), "POST /api/cart/items")
}

// shopCartCookieValue returns the browser's cart token, or "" when it has none.
func shopCartCookieValue(b *shopBrowser) string {
	if cookie := b.jar[shopCartCookie]; cookie != nil {
		return cookie.Value
	}
	return ""
}

// shopSHA256Hex is the storage form of every token in the schema (carts, order_access_tokens, orders).
func shopSHA256Hex(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func TestCartCookieTokenHashedAndFlags(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "cookie-tee", 12000, 10)
	v := shopVariantRows(t, h, p.ID)[0]

	b := newShopBrowser(h)
	r := b.send(http.MethodPost, "/api/cart/items", map[string]any{"variant_id": v.ID, "qty": 1})
	shopWantOK(t, r, "POST /api/cart/items")

	var cookie *http.Cookie
	for _, c := range r.cookies {
		if c.Name == shopCartCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatalf("first cart write set no %s cookie; Set-Cookie=%v", shopCartCookie, r.header.Values(echo.HeaderSetCookie))
	}
	if !cookie.HttpOnly {
		t.Errorf("%s cookie is not HttpOnly", shopCartCookie)
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("%s cookie SameSite = %v, want Lax", shopCartCookie, cookie.SameSite)
	}

	var row core.Cart
	if err := h.db.Order("id desc").First(&row).Error; err != nil {
		t.Fatalf("load cart row: %v", err)
	}
	if !shopHex64.MatchString(row.TokenHash) {
		t.Errorf("carts.token_hash %q is not 64 lowercase hex characters", row.TokenHash)
	}
	if row.TokenHash == cookie.Value {
		t.Error("carts.token_hash equals the raw cookie value; only the hash may be stored")
	}
	if row.TokenHash != shopSHA256Hex(cookie.Value) {
		t.Error("carts.token_hash is not the SHA-256 of the cookie value")
	}
}

func TestCartAddUpdateRemoveAndTotals(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopCatalogSeedProduct(t, h, shopCatalogProduct{
		title: "Tea", slug: "tea", status: "active", price: 12000, stock: 20,
		variants: []shopCatalogVariant{{name: "紅", price: 12000, stock: 20}, {name: "綠", price: 5000, stock: 20}},
	})
	vs := shopVariantRows(t, h, p.ID)
	b := newShopBrowser(h)
	shopAddToCart(t, b, vs[0].ID, 2)
	shopAddToCart(t, b, vs[1].ID, 1)

	cart := shopCartGet(t, b)
	if cart.Subtotal != 29000 {
		t.Errorf("subtotal = %d, want 29000 (2 x 12000 + 1 x 5000)", cart.Subtotal)
	}
	if cart.Total != cart.Subtotal+cart.Shipping-cart.Discount {
		t.Errorf("total = %d, want subtotal %d + shipping %d - discount %d", cart.Total, cart.Subtotal, cart.Shipping, cart.Discount)
	}
	if cart.Currency != "TWD" {
		t.Errorf("currency = %q, want TWD", cart.Currency)
	}

	shopWantOK(t, b.send(http.MethodPatch, "/api/cart/items/"+shopIDText(vs[0].ID), map[string]any{"qty": 3}), "PATCH cart line")
	cart = shopCartGet(t, b)
	if line, ok := cart.line(vs[0].ID); !ok || line.Qty != 3 || line.LineTotal != 36000 {
		t.Errorf("after PATCH qty 3 the red line is %+v (found %v), want qty 3 and line_total 36000", line, ok)
	}
	if cart.Subtotal != 41000 {
		t.Errorf("subtotal after PATCH = %d, want 41000", cart.Subtotal)
	}

	shopWantOK(t, b.do(http.MethodDelete, "/api/cart/items/"+shopIDText(vs[1].ID), "", "", map[string]string{"X-Jelly-CSRF": "1"}), "DELETE cart line")
	cart = shopCartGet(t, b)
	if _, ok := cart.line(vs[1].ID); ok {
		t.Error("green line still in the cart after DELETE")
	}
	if cart.Subtotal != 36000 {
		t.Errorf("subtotal after DELETE = %d, want 36000", cart.Subtotal)
	}
}

func TestCartQtyClampedToStockReportsIssue(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "clamp-tee", 12000, 3)
	v := shopVariantRows(t, h, p.ID)[0]

	b := newShopBrowser(h)
	r := b.send(http.MethodPost, "/api/cart/items", map[string]any{"variant_id": v.ID, "qty": 5})
	shopWantOK(t, r, "POST /api/cart/items qty 5 with stock 3")
	var returned shopCartView
	shopDecode(t, r, "POST /api/cart/items", &returned)
	cart := shopCartGet(t, b)
	for label, view := range map[string]shopCartView{"POST response": returned, "GET /api/cart": cart} {
		line, ok := view.line(v.ID)
		if !ok {
			t.Errorf("%s: line missing", label)
			continue
		}
		if line.Qty != 3 {
			t.Errorf("%s: qty = %d, want 3 (clamped to stock)", label, line.Qty)
		}
		if !slices.Contains(view.issueCodes(v.ID), "qty_reduced") {
			t.Errorf("%s: issues %v do not report qty_reduced", label, view.Issues)
		}
	}
}

func TestCartSoldOutLineKeptButExcludedFromTotal(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopCatalogSeedProduct(t, h, shopCatalogProduct{
		title: "Sold out tee", slug: "sold-out-tee", status: "active", price: 12000, stock: 2,
		variants: []shopCatalogVariant{{name: "A", price: 12000, stock: 2}, {name: "B", price: 5000, stock: 5}},
	})
	vs := shopVariantRows(t, h, p.ID)
	b := newShopBrowser(h)
	shopAddToCart(t, b, vs[0].ID, 2)
	shopAddToCart(t, b, vs[1].ID, 1)
	if err := h.db.Model(&core.ProductVariant{}).Where("id = ?", vs[0].ID).Update("stock", 0).Error; err != nil {
		t.Fatalf("sell out variant A: %v", err)
	}

	cart := shopCartGet(t, b)
	line, ok := cart.line(vs[0].ID)
	if !ok {
		t.Fatal("sold-out line was removed from the cart; it must stay in the list")
	}
	if line.Available {
		t.Error("sold-out line reports available=true")
	}
	if cart.Subtotal != 5000 {
		t.Errorf("subtotal = %d, want 5000 (sold-out line excluded)", cart.Subtotal)
	}
	if !slices.Contains(cart.issueCodes(vs[0].ID), "sold_out") {
		t.Errorf("issues %v do not report sold_out for the sold-out line", cart.Issues)
	}
}

func TestCartIgnoresClientPriceFields(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "price-tee", 12000, 10)
	v := shopVariantRows(t, h, p.ID)[0]

	b := newShopBrowser(h)
	shopWantOK(t, b.send(http.MethodPost, "/api/cart/items", map[string]any{
		"variant_id": v.ID, "qty": 1, "price": 1, "unit_price": 1, "line_total": 1,
	}), "POST /api/cart/items with client prices")
	cart := shopCartGet(t, b)
	line, ok := cart.line(v.ID)
	if !ok {
		t.Fatal("line missing after add")
	}
	if line.UnitPrice != 12000 || line.LineTotal != 12000 {
		t.Errorf("client price fields were honoured: unit_price=%d line_total=%d, want 12000 and 12000", line.UnitPrice, line.LineTotal)
	}
	if cart.Subtotal != 12000 {
		t.Errorf("subtotal = %d, want 12000", cart.Subtotal)
	}
}

func TestCartIsolationBetweenBrowsers(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "isolated-tee", 12000, 10)
	v := shopVariantRows(t, h, p.ID)[0]

	a := newShopBrowser(h)
	shopAddToCart(t, a, v.ID, 1)
	other := newShopBrowser(h)
	cart := shopCartGet(t, other)
	if len(cart.Items) != 0 {
		t.Errorf("a second browser sees %d lines of the first browser's cart", len(cart.Items))
	}
	if shopCartCookieValue(a) == "" {
		t.Fatal("first browser has no cart cookie")
	}
	if shopCartCookieValue(other) == shopCartCookieValue(a) {
		t.Error("two browsers share one cart token")
	}
	if got := shopCartGet(t, a); len(got.Items) != 1 {
		t.Errorf("first browser lost its line: %d lines", len(got.Items))
	}
}

func TestCartMergeOnLogin(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopCatalogSeedProduct(t, h, shopCatalogProduct{
		title: "Merge tee", slug: "merge-tee", status: "active", price: 1000, stock: 50,
		variants: []shopCatalogVariant{{name: "X", price: 1000, stock: 50}, {name: "Y", price: 2000, stock: 50}},
	})
	vs := shopVariantRows(t, h, p.ID)

	hash, err := bcrypt.GenerateFromPassword([]byte("member-pass-1"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash member password: %v", err)
	}
	member := core.User{Username: "merger", Email: "merger@example.test", Role: "customer", Provider: "local", Password: string(hash)}
	if err := h.db.Create(&member).Error; err != nil {
		t.Fatalf("create member: %v", err)
	}
	// The member already has a cart from another device: X x5.
	memberID := member.ID
	memberCart := core.Cart{TokenHash: shopSHA256Hex("member-cart-token-0001"), UserID: &memberID, ExpiresAt: time.Now().Add(72 * time.Hour)}
	if err := h.db.Create(&memberCart).Error; err != nil {
		t.Fatalf("seed member cart: %v", err)
	}
	if err := h.db.Create(&core.CartItem{CartID: memberCart.ID, VariantID: vs[0].ID, Qty: 5}).Error; err != nil {
		t.Fatalf("seed member cart item: %v", err)
	}

	// The guest has X x2 and Y x1, then logs in.
	guest := newShopBrowser(h)
	shopAddToCart(t, guest, vs[0].ID, 2)
	shopAddToCart(t, guest, vs[1].ID, 1)
	login := guest.sendWith(http.MethodPost, "/auth/login", map[string]string{"username": member.Username, "password": "member-pass-1"}, map[string]string{"Accept": "application/json"})
	shopWantCode(t, login, "POST /auth/login", http.StatusOK)

	cart := shopCartGet(t, guest)
	if line, ok := cart.line(vs[0].ID); !ok || line.Qty != 5 {
		t.Errorf("after login X qty = %d (found %v), want 5 (the larger of 2 and 5)", line.Qty, ok)
	}
	if line, ok := cart.line(vs[1].ID); !ok || line.Qty != 1 {
		t.Errorf("after login Y qty = %d (found %v), want 1 (guest line kept)", line.Qty, ok)
	}
}

func TestCartQtyAndLineCaps(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "cap-tee", 1000, 200)
	v := shopVariantRows(t, h, p.ID)[0]

	b := newShopBrowser(h)
	for _, qty := range []int{0, 100} {
		r := b.send(http.MethodPost, "/api/cart/items", map[string]any{"variant_id": v.ID, "qty": qty})
		shopWantCode(t, r, "POST /api/cart/items qty "+shopIDText(uint(qty)), http.StatusBadRequest)
	}
	shopAddToCart(t, b, v.ID, 1)
	for _, qty := range []int{1, 50, 99} {
		shopWantOK(t, b.send(http.MethodPatch, "/api/cart/items/"+shopIDText(v.ID), map[string]any{"qty": qty}), "PATCH qty "+shopIDText(uint(qty)))
	}
	shopWantCode(t, b.send(http.MethodPatch, "/api/cart/items/"+shopIDText(v.ID), map[string]any{"qty": 100}), "PATCH qty 100", http.StatusBadRequest)
	cart := shopCartGet(t, b)
	if line, ok := cart.line(v.ID); !ok || line.Qty != 99 {
		t.Errorf("after the rejected qty 100 the line is %+v (found %v), want the last accepted qty 99", line, ok)
	}
}

// shopIDText formats an id for a path or a message.
func shopIDText(v uint) string {
	return strconv.FormatUint(uint64(v), 10)
}

// shopCheckoutBody is a valid POST /api/checkout body: a home delivery in Taipei, the given payment method, terms
// accepted and the given idempotency key. Tests override single fields on top of it.
func shopCheckoutBody(email, method, key string) map[string]any {
	return map[string]any{
		"email":           email,
		"phone":           "0912345678",
		"create_account":  false,
		"shipping":        map[string]any{"method": "home", "name": "測試買家", "postcode": "100", "city": "臺北市", "address": "中正區測試路 1 號"},
		"payment":         map[string]any{"method": method},
		"invoice":         map[string]any{"kind": "none"},
		"accept_terms":    true,
		"idempotency_key": key,
	}
}

// shopCheckoutResult mirrors the 201 response of POST /api/checkout in the contract.
type shopCheckoutResult struct {
	Order struct {
		Number   string `json:"number"`
		Status   string `json:"status"`
		Total    int64  `json:"total"`
		Currency string `json:"currency"`
	} `json:"order"`
	RedirectURL string `json:"redirect_url"`
	Payment     struct {
		Provider     string `json:"provider"`
		Method       string `json:"method"`
		Kind         string `json:"kind"`
		Instructions string `json:"instructions"`
	} `json:"payment"`
	AccessToken string `json:"access_token"`
}
