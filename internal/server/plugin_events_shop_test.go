package server

// Shop plugin event tests for docs/tickets/27-plugin-events.md (product, public product page, order, payment and
// refund hooks). They drive real HTTP routes through the plugin runtime with the fixture plugin from
// plugin_events_harness_test.go and are skipped unless JELLY_TEST_POSTGRES=1. They must fail until the event bridge
// and the store wiring exist. Do not edit them to match an implementation.
//
// Every assertion waits for the delivery with waitEvents (or checks absence with requireNoEvent) before it reads a
// payload. Payload snapshots are read by the bridge after commit, so an OnOrderCreate for a zero-total order shows the
// status the order has when the transaction has committed.

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
)

// pluginEventsShopHarness returns the event harness with the shop enabled (sandbox gateway, manual transfer, shipping).
func pluginEventsShopHarness(t *testing.T) *pluginEventsHarness {
	t.Helper()
	h := newPluginEventsHarness(t)
	shopSetup(t, h.accessHarness)
	return h
}

// pluginEventsShopUseTheme activates the default theme and rebuilds the routes on the harness runtime, so public
// storefront pages render while plugin events still reach the fixture plugin.
func pluginEventsShopUseTheme(t *testing.T, h *pluginEventsHarness) {
	t.Helper()
	tm := theme.NewManager("web/themes")
	if err := tm.Activate("default"); err != nil {
		t.Fatalf("activate default theme: %v", err)
	}
	h.rebuildRoutes(t, tm)
}

// pluginEventsShopAdmin returns a browser signed in as a new administrator.
func pluginEventsShopAdmin(t *testing.T, h *pluginEventsHarness, name string) (*shopBrowser, core.User) {
	t.Helper()
	admin := h.user("admin", name)
	b := newShopBrowser(h.accessHarness)
	b.token = h.token(admin)
	return b, admin
}

// pluginEventsShopExactly waits for hook and fails unless exactly n deliveries were made.
func pluginEventsShopExactly(t *testing.T, h *pluginEventsHarness, hook plugin.Hook, n int) []pluginEvent {
	t.Helper()
	got := h.waitEvents(t, hook, n)
	if len(got) != n {
		t.Fatalf("%s deliveries = %d, want exactly %d; captured log:\n%s", hook, len(got), n, h.sink.dump())
	}
	return got
}

// pluginEventsShopWantNullActor fails unless payload has an actor key set to null.
func pluginEventsShopWantNullActor(t *testing.T, payload map[string]any) {
	t.Helper()
	if value, ok := payload["actor"]; !ok || value != nil {
		t.Fatalf("actor = %v (present %v), want null for a system or anonymous action", value, ok)
	}
}

// pluginEventsShopWantActor fails unless payload.actor is the given administrator.
func pluginEventsShopWantActor(t *testing.T, payload map[string]any, admin core.User) {
	t.Helper()
	actor := nested(t, payload, "actor")
	if id := number(t, actor, "id"); id != float64(admin.ID) {
		t.Errorf("actor.id = %v, want administrator %d", id, admin.ID)
	}
	if role, _ := actor["role"].(string); role != "admin" {
		t.Errorf("actor.role = %q, want admin", role)
	}
}

// pluginEventsShopTrade returns the newest merchant_trade_no of orderID.
func pluginEventsShopTrade(t *testing.T, h *pluginEventsHarness, orderID uint) string {
	t.Helper()
	var trade string
	if err := h.db.Raw("SELECT merchant_trade_no FROM payments WHERE order_id = ? ORDER BY id DESC LIMIT 1", orderID).Scan(&trade).Error; err != nil || trade == "" {
		t.Fatalf("order %d has no payment attempt (err %v)", orderID, err)
	}
	return trade
}

// pluginEventsShopSimulate answers a sandbox payment attempt through the simulate route with status.
func pluginEventsShopSimulate(t *testing.T, b *shopBrowser, trade, access, status string) {
	t.Helper()
	path := "/api/checkout/sandbox/" + trade
	if access != "" {
		path += "?t=" + url.QueryEscape(access)
	}
	shopWantCode(t, b.send(http.MethodPost, path, map[string]any{"status": status}), "simulate sandbox "+status, http.StatusOK)
}

// pluginEventsShopProductBody is a product form body with one variant, matching what the admin form sends.
func pluginEventsShopProductBody(title, slug, price, status string, stock int) map[string]any {
	return map[string]any{
		"title": title, "slug": slug, "description": "", "price": price, "compare_at_price": "",
		"status": status, "images": []string{}, "categories": []uint{},
		"variants":  []map[string]any{{"name": "", "sku": "", "price": price, "compare_at_price": "", "stock": stock, "position": 0, "status": "active"}},
		"seo_title": "", "seo_description": "", "seo_index": "inherit", "seo_ai": "inherit",
	}
}

func TestPluginEventsShopProductSaveDeleteAndPurge(t *testing.T) {
	h := pluginEventsShopHarness(t)
	adm, admin := pluginEventsShopAdmin(t, h, "events-product-admin")

	// create
	r := adm.send(http.MethodPost, "/api/admin/products", pluginEventsShopProductBody("Events Tee", "events-tee", "120", "active", 7))
	shopWantOK(t, r, "POST /api/admin/products")
	productID := shopJSONID(t, r, "create product")
	var stored core.Product
	if err := h.db.First(&stored, productID).Error; err != nil {
		t.Fatalf("read created product: %v", err)
	}
	created := pluginEventsShopExactly(t, h, plugin.HookOnProductSave, 1)[0].Payload
	if created["action"] != "create" {
		t.Errorf("create action = %v, want create", created["action"])
	}
	product := nested(t, created, "product")
	if number(t, product, "id") != float64(productID) || product["slug"] != "events-tee" || product["title"] != "Events Tee" || product["status"] != "active" {
		t.Errorf("create product = %v, want id %d slug events-tee title Events Tee status active", product, productID)
	}
	if price := number(t, product, "price"); price != float64(stored.Price) || price != 12000 {
		t.Errorf("create product price = %v, want 12000 minor units (stored %d)", price, stored.Price)
	}
	if stock := number(t, product, "stock"); stock != float64(stored.Stock) {
		t.Errorf("create product stock = %v, want stored %d", stock, stored.Stock)
	}
	pluginEventsShopWantActor(t, created, admin)
	h.reset(t)

	// update
	r = adm.send(http.MethodPut, fmt.Sprintf("/api/admin/products/%d", productID), pluginEventsShopProductBody("Events Tee Renamed", "events-tee", "150", "active", 7))
	shopWantOK(t, r, "PUT /api/admin/products/:id")
	updated := pluginEventsShopExactly(t, h, plugin.HookOnProductSave, 1)[0].Payload
	if updated["action"] != "update" {
		t.Errorf("update action = %v, want update", updated["action"])
	}
	if p := nested(t, updated, "product"); number(t, p, "id") != float64(productID) || p["title"] != "Events Tee Renamed" {
		t.Errorf("update product = %v, want id %d renamed", p, productID)
	}
	pluginEventsShopWantActor(t, updated, admin)
	h.reset(t)

	// duplicate
	r = adm.send(http.MethodPost, fmt.Sprintf("/api/admin/products/%d/duplicate", productID), nil)
	shopWantOK(t, r, "POST /api/admin/products/:id/duplicate")
	copyID := shopJSONID(t, r, "duplicate product")
	if copyID == productID {
		t.Fatalf("duplicate returned the source id %d", productID)
	}
	duplicated := pluginEventsShopExactly(t, h, plugin.HookOnProductSave, 1)[0].Payload
	if duplicated["action"] != "duplicate" {
		t.Errorf("duplicate action = %v, want duplicate", duplicated["action"])
	}
	if p := nested(t, duplicated, "product"); number(t, p, "id") != float64(copyID) {
		t.Errorf("duplicate product id = %v, want the new product %d", p["id"], copyID)
	}
	pluginEventsShopWantActor(t, duplicated, admin)
	h.reset(t)

	// trash the copy
	shopWantOK(t, adm.send(http.MethodDelete, fmt.Sprintf("/api/admin/products/%d", copyID), nil), "DELETE /api/admin/products/:id")
	trashed := pluginEventsShopExactly(t, h, plugin.HookOnProductDelete, 1)[0].Payload
	if trashed["action"] != "trash" {
		t.Errorf("trash action = %v, want trash", trashed["action"])
	}
	if p := nested(t, trashed, "product"); number(t, p, "id") != float64(copyID) {
		t.Errorf("trash product id = %v, want %d", p["id"], copyID)
	}
	pluginEventsShopWantActor(t, trashed, admin)
	h.reset(t)

	// restore the trashed copy from the recycle bin
	shopWantOK(t, adm.send(http.MethodPost, fmt.Sprintf("/api/admin/trash/product/%d/restore", copyID), nil), "POST /api/admin/trash/product/:id/restore")
	restored := pluginEventsShopExactly(t, h, plugin.HookOnProductSave, 1)[0].Payload
	if restored["action"] != "restore" {
		t.Errorf("restore action = %v, want restore", restored["action"])
	}
	if p := nested(t, restored, "product"); number(t, p, "id") != float64(copyID) {
		t.Errorf("restore product id = %v, want %d", p["id"], copyID)
	}
	pluginEventsShopWantActor(t, restored, admin)
	h.reset(t)

	// trash again and purge: the purge payload carries only the summary fields
	shopWantOK(t, adm.send(http.MethodDelete, fmt.Sprintf("/api/admin/products/%d", copyID), nil), "DELETE /api/admin/products/:id (again)")
	pluginEventsShopExactly(t, h, plugin.HookOnProductDelete, 1)
	h.reset(t)
	shopWantOK(t, adm.send(http.MethodDelete, fmt.Sprintf("/api/admin/trash/product/%d", copyID), nil), "DELETE /api/admin/trash/product/:id")
	purged := pluginEventsShopExactly(t, h, plugin.HookOnProductDelete, 1)[0].Payload
	if purged["action"] != "purge" {
		t.Errorf("purge action = %v, want purge", purged["action"])
	}
	summary := nested(t, purged, "product")
	keys := make([]string, 0, len(summary))
	for key := range summary {
		keys = append(keys, key)
	}
	if len(keys) != 5 {
		t.Errorf("purge product keys = %v, want exactly id, slug, title, price, currency", keys)
	}
	for _, key := range []string{"id", "slug", "title", "price", "currency"} {
		if _, ok := summary[key]; !ok {
			t.Errorf("purge product has no %q key: %v", key, summary)
		}
	}
	if number(t, summary, "id") != float64(copyID) {
		t.Errorf("purge product id = %v, want %d", summary["id"], copyID)
	}
	pluginEventsShopWantActor(t, purged, admin)
}

func TestPluginEventsShopProductViewOnGETOnly(t *testing.T) {
	h := pluginEventsShopHarness(t)
	pluginEventsShopUseTheme(t, h)
	p := shopSeedProduct(t, h.accessHarness, "events-view-tee", 12000, 10)
	path := "/shop/p/events-view-tee"

	anon := newShopBrowser(h.accessHarness)
	shopWantCode(t, anon.get(path), "anonymous GET "+path, http.StatusOK)
	view := pluginEventsShopExactly(t, h, plugin.HookOnProductView, 1)[0].Payload
	product := nested(t, view, "product")
	if number(t, product, "id") != float64(p.ID) || product["slug"] != "events-view-tee" || product["title"] != "events-view-tee" {
		t.Errorf("view product = %v, want id %d slug events-view-tee", product, p.ID)
	}
	if price := number(t, product, "price"); price != 12000 {
		t.Errorf("view product price = %v, want 12000 minor units", price)
	}
	if product["currency"] != "TWD" {
		t.Errorf("view product currency = %v, want TWD", product["currency"])
	}
	if view["path"] != path {
		t.Errorf("view path = %v, want %s", view["path"], path)
	}
	if view["signed_in"] != false {
		t.Errorf("view signed_in = %v, want false for an anonymous visitor", view["signed_in"])
	}
	if _, ok := view["ip"]; ok {
		t.Errorf("view carries an IP field: %v", view)
	}

	// HEAD answers the page but is not a view.
	h.reset(t)
	anon.do(http.MethodHead, path, "", "", nil)
	h.requireNoEvent(t, plugin.HookOnProductView)

	// A missing slug is not a product view.
	missing := anon.get("/shop/p/events-no-such-product")
	if missing.code == http.StatusOK {
		t.Fatalf("GET of a missing product answered 200")
	}
	h.requireNoEvent(t, plugin.HookOnProductView)

	// A signed-in customer is reported with signed_in true.
	customer := h.user("customer", "events-view-customer")
	signedIn := newShopBrowser(h.accessHarness)
	signedIn.token = h.token(customer)
	h.reset(t)
	shopWantCode(t, signedIn.get(path), "signed-in GET "+path, http.StatusOK)
	signedView := pluginEventsShopExactly(t, h, plugin.HookOnProductView, 1)[0].Payload
	if signedView["signed_in"] != true {
		t.Errorf("signed-in view signed_in = %v, want true", signedView["signed_in"])
	}
}

func TestPluginEventsShopZeroTotalCheckout(t *testing.T) {
	h := pluginEventsShopHarness(t)
	p := shopSeedProduct(t, h.accessHarness, "events-free-tee", 12000, 10)
	v := shopVariantRows(t, h.accessHarness, p.ID)[0]
	coupon := shopSeedCoupon(t, h.accessHarness, core.Coupon{Code: "EVENTSFREE", Kind: "percent", Value: 100})

	b := newShopBrowser(h.accessHarness)
	shopAddToCart(t, b, v.ID, 2)
	shopWantOK(t, b.send(http.MethodPost, "/api/cart/coupon", map[string]any{"code": coupon.Code}), "apply free coupon")
	body := shopCheckoutBody("events-free@example.test", "credit_card", "events-free-1")
	body["coupon_code"] = coupon.Code
	body["shipping"] = map[string]any{"method": "pickup", "name": "測試買家"}
	r := b.send(http.MethodPost, "/api/checkout", body)
	shopWantCode(t, r, "zero-total checkout", http.StatusCreated)
	var result shopCheckoutResult
	shopDecode(t, r, "zero-total checkout", &result)
	order := shopOrderRow(t, h.accessHarness, result.Order.Number)

	created := pluginEventsShopExactly(t, h, plugin.HookOnOrderCreate, 1)[0].Payload
	snapshot := nested(t, created, "order")
	if snapshot["number"] != result.Order.Number {
		t.Errorf("OnOrderCreate order.number = %v, want %s", snapshot["number"], result.Order.Number)
	}
	// The order is announced as created (pending); the zero-total payment then
	// arrives as OnOrderUpdate pending → paid and OnOrderPaid.
	if snapshot["status"] != core.OrderStatusPending {
		t.Errorf("OnOrderCreate order.status = %v, want pending (committed status is %q)", snapshot["status"], order.Status)
	}
	if snapshot["email"] != "events-free@example.test" {
		t.Errorf("OnOrderCreate order.email = %v, want events-free@example.test", snapshot["email"])
	}
	if total := number(t, snapshot, "total_amount"); total != 0 {
		t.Errorf("OnOrderCreate order.total_amount = %v, want 0", total)
	}
	if snapshot["currency"] != "TWD" {
		t.Errorf("OnOrderCreate order.currency = %v, want TWD", snapshot["currency"])
	}
	items, ok := snapshot["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("OnOrderCreate order.items = %v, want one line", snapshot["items"])
	}
	line, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("OnOrderCreate items[0] = %v, want an object", items[0])
	}
	if number(t, line, "product_id") != float64(p.ID) {
		t.Errorf("items[0].product_id = %v, want %d", line["product_id"], p.ID)
	}
	if number(t, line, "quantity") != 2 {
		t.Errorf("items[0].quantity = %v, want 2", line["quantity"])
	}
	for _, key := range []string{"shipping", "invoice", "lookup_token_hash", "idempotency_key"} {
		if _, present := snapshot[key]; present {
			t.Errorf("OnOrderCreate order carries %q, which must never be in the payload", key)
		}
	}
	pluginEventsShopWantNullActor(t, created)

	update := pluginEventsShopExactly(t, h, plugin.HookOnOrderUpdate, 1)[0].Payload
	if update["from"] != "pending" || update["to"] != "paid" {
		t.Errorf("OnOrderUpdate from=%v to=%v, want pending to paid", update["from"], update["to"])
	}
	if nested(t, update, "order")["number"] != result.Order.Number {
		t.Errorf("OnOrderUpdate order.number mismatch, want %s", result.Order.Number)
	}
	pluginEventsShopWantNullActor(t, update)

	paid := pluginEventsShopExactly(t, h, plugin.HookOnOrderPaid, 1)[0].Payload
	if nested(t, paid, "order")["number"] != result.Order.Number {
		t.Errorf("OnOrderPaid order.number mismatch, want %s", result.Order.Number)
	}

	h.flush(t)
	var createAt, updateAt = -1, -1
	for i, event := range h.allEvents(t, pluginEventsFixtureID) {
		switch event.Hook {
		case string(plugin.HookOnOrderCreate):
			if createAt < 0 {
				createAt = i
			}
		case string(plugin.HookOnOrderUpdate):
			if updateAt < 0 {
				updateAt = i
			}
		}
	}
	if createAt < 0 || updateAt < 0 || createAt > updateAt {
		t.Errorf("OnOrderCreate at %d and OnOrderUpdate at %d, want create before update", createAt, updateAt)
	}
}

func TestPluginEventsShopSandboxPaymentSuccess(t *testing.T) {
	h := pluginEventsShopHarness(t)
	p := shopSeedProduct(t, h.accessHarness, "events-pay-tee", 12000, 10)
	v := shopVariantRows(t, h.accessHarness, p.ID)[0]
	email := "events-pay@example.test"

	b, result := shopPlaceCheckout(t, h.accessHarness, v.ID, 1, email, "credit_card", "events-pay-1")
	order := shopOrderRow(t, h.accessHarness, result.Order.Number)
	created := pluginEventsShopExactly(t, h, plugin.HookOnOrderCreate, 1)[0].Payload
	if nested(t, created, "order")["status"] != "pending" {
		t.Errorf("OnOrderCreate status = %v, want pending for a card order", nested(t, created, "order")["status"])
	}
	h.requireNoEvent(t, plugin.HookOnOrderPaid)
	h.reset(t)

	trade := pluginEventsShopTrade(t, h, order.ID)
	pluginEventsShopSimulate(t, b, trade, result.AccessToken, "paid")

	success := pluginEventsShopExactly(t, h, plugin.HookOnPaymentSuccess, 1)[0].Payload
	if nested(t, success, "order")["number"] != result.Order.Number {
		t.Errorf("OnPaymentSuccess order.number = %v, want %s", nested(t, success, "order")["number"], result.Order.Number)
	}
	payment := nested(t, success, "payment")
	if payment["provider"] != "sandbox" {
		t.Errorf("OnPaymentSuccess payment.provider = %v, want sandbox", payment["provider"])
	}
	if amount := number(t, payment, "amount"); amount != float64(order.TotalAmount) {
		t.Errorf("OnPaymentSuccess payment.amount = %v, want %d", amount, order.TotalAmount)
	}
	if payment["currency"] != "TWD" {
		t.Errorf("OnPaymentSuccess payment.currency = %v, want TWD", payment["currency"])
	}
	if payment["merchant_trade_no"] != trade {
		t.Errorf("OnPaymentSuccess payment.merchant_trade_no = %v, want %s", payment["merchant_trade_no"], trade)
	}
	for _, key := range []string{"payload", "raw"} {
		if _, present := payment[key]; present {
			t.Errorf("OnPaymentSuccess payment carries %q, which must never be in the payload", key)
		}
	}

	update := pluginEventsShopExactly(t, h, plugin.HookOnOrderUpdate, 1)[0].Payload
	if update["from"] != "pending" || update["to"] != "paid" {
		t.Errorf("OnOrderUpdate from=%v to=%v, want pending to paid", update["from"], update["to"])
	}
	paid := pluginEventsShopExactly(t, h, plugin.HookOnOrderPaid, 1)[0].Payload
	if nested(t, paid, "order")["number"] != result.Order.Number {
		t.Errorf("OnOrderPaid order.number = %v, want %s", nested(t, paid, "order")["number"], result.Order.Number)
	}
}

func TestPluginEventsShopSandboxPaymentFailed(t *testing.T) {
	h := pluginEventsShopHarness(t)
	p := shopSeedProduct(t, h.accessHarness, "events-fail-tee", 12000, 10)
	v := shopVariantRows(t, h.accessHarness, p.ID)[0]

	b, result := shopPlaceCheckout(t, h.accessHarness, v.ID, 1, "events-fail@example.test", "credit_card", "events-fail-1")
	order := shopOrderRow(t, h.accessHarness, result.Order.Number)
	pluginEventsShopExactly(t, h, plugin.HookOnOrderCreate, 1)
	h.reset(t)

	trade := pluginEventsShopTrade(t, h, order.ID)
	pluginEventsShopSimulate(t, b, trade, result.AccessToken, "failed")

	failed := pluginEventsShopExactly(t, h, plugin.HookOnPaymentFailed, 1)[0].Payload
	if nested(t, failed, "order")["number"] != result.Order.Number {
		t.Errorf("OnPaymentFailed order.number = %v, want %s", nested(t, failed, "order")["number"], result.Order.Number)
	}
	payment := nested(t, failed, "payment")
	if payment["provider"] != "sandbox" {
		t.Errorf("OnPaymentFailed payment.provider = %v, want sandbox", payment["provider"])
	}
	if reason, _ := failed["reason"].(string); strings.TrimSpace(reason) == "" {
		t.Errorf("OnPaymentFailed reason = %v, want a non-empty reason", failed["reason"])
	}
	h.requireNoEvent(t, plugin.HookOnOrderPaid)
	h.requireNoEvent(t, plugin.HookOnPaymentSuccess)
}

func TestPluginEventsShopManualTransferMarkPaidActor(t *testing.T) {
	h := pluginEventsShopHarness(t)
	p := shopSeedProduct(t, h.accessHarness, "events-wire-tee", 12000, 10)
	v := shopVariantRows(t, h.accessHarness, p.ID)[0]
	_, result := shopPlaceCheckout(t, h.accessHarness, v.ID, 1, "events-wire@example.test", "bank_transfer", "events-wire-1")
	order := shopOrderRow(t, h.accessHarness, result.Order.Number)
	h.reset(t)

	adm, admin := pluginEventsShopAdmin(t, h, "events-markpaid-admin")
	shopWantOK(t, adm.send(http.MethodPost, fmt.Sprintf("/api/admin/orders/%d/mark-paid", order.ID), nil), "POST mark-paid")

	paid := pluginEventsShopExactly(t, h, plugin.HookOnOrderPaid, 1)[0].Payload
	if nested(t, paid, "order")["number"] != result.Order.Number {
		t.Errorf("OnOrderPaid order.number = %v, want %s", nested(t, paid, "order")["number"], result.Order.Number)
	}
	pluginEventsShopWantActor(t, paid, admin)

	update := pluginEventsShopExactly(t, h, plugin.HookOnOrderUpdate, 1)[0].Payload
	if update["to"] != "paid" {
		t.Errorf("OnOrderUpdate to = %v, want paid", update["to"])
	}
	pluginEventsShopWantActor(t, update, admin)
}

func TestPluginEventsShopAdminStatusChangeAndIllegalMove(t *testing.T) {
	h := pluginEventsShopHarness(t)
	adm, admin := pluginEventsShopAdmin(t, h, "events-status-admin")
	year := time.Now().Year()
	paidAt := time.Now().Add(-time.Hour)
	shipping := shopSeedOrder(t, h.accessHarness, shopOrderSeed{
		number: fmt.Sprintf("JL-%d-0301", year), email: "events-ship@example.test", status: "paid",
		provider: "manual_transfer", method: "bank_transfer", total: 12000, paidAt: &paidAt,
	})
	illegal := shopSeedOrder(t, h.accessHarness, shopOrderSeed{
		number: fmt.Sprintf("JL-%d-0302", year), email: "events-illegal@example.test", status: "paid",
		provider: "manual_transfer", method: "bank_transfer", total: 12000, paidAt: &paidAt,
	})
	h.reset(t)

	shopWantOK(t, adm.send(http.MethodPut, fmt.Sprintf("/api/admin/orders/%d", shipping.ID), map[string]any{
		"status": "shipped", "carrier": "TEST-CARRIER", "tracking_no": "TRACK-EVENTS-1", "note": "出貨備註",
	}), "PUT paid to shipped")
	update := pluginEventsShopExactly(t, h, plugin.HookOnOrderUpdate, 1)[0].Payload
	if update["from"] != "paid" || update["to"] != "shipped" {
		t.Errorf("OnOrderUpdate from=%v to=%v, want paid to shipped", update["from"], update["to"])
	}
	if update["note"] != "出貨備註" {
		t.Errorf("OnOrderUpdate note = %v, want the submitted note", update["note"])
	}
	pluginEventsShopWantActor(t, update, admin)
	h.requireNoEvent(t, plugin.HookOnOrderPaid)
	h.reset(t)

	// paid to completed is not in the state machine: 409 and no event.
	shopWantCode(t, adm.send(http.MethodPut, fmt.Sprintf("/api/admin/orders/%d", illegal.ID), map[string]any{"status": "completed"}), "illegal paid to completed", http.StatusConflict)
	h.requireNoEvent(t, plugin.HookOnOrderUpdate)
}

func TestPluginEventsShopFullRefund(t *testing.T) {
	h := pluginEventsShopHarness(t)
	adm, admin := pluginEventsShopAdmin(t, h, "events-refund-admin")
	p := shopSeedProduct(t, h.accessHarness, "events-refund-tee", 12000, 10)
	v := shopVariantRows(t, h.accessHarness, p.ID)[0]

	b, result := shopPlaceCheckout(t, h.accessHarness, v.ID, 1, "events-refund@example.test", "credit_card", "events-refund-1")
	order := shopOrderRow(t, h.accessHarness, result.Order.Number)
	trade := pluginEventsShopTrade(t, h, order.ID)
	pluginEventsShopSimulate(t, b, trade, result.AccessToken, "paid")
	paidOrder := shopOrderRow(t, h.accessHarness, result.Order.Number)
	h.reset(t)

	shopWantOK(t, adm.send(http.MethodPost, fmt.Sprintf("/api/admin/orders/%d/refund", order.ID), map[string]any{
		"amount": paidOrder.TotalAmount, "reason": "客戶取消", "restock": true,
	}), "full refund")

	refund := pluginEventsShopExactly(t, h, plugin.HookOnOrderRefund, 1)[0].Payload
	if nested(t, refund, "order")["number"] != result.Order.Number {
		t.Errorf("OnOrderRefund order.number = %v, want %s", nested(t, refund, "order")["number"], result.Order.Number)
	}
	if amount := number(t, refund, "amount"); amount != float64(paidOrder.TotalAmount) {
		t.Errorf("OnOrderRefund amount = %v, want %d", amount, paidOrder.TotalAmount)
	}
	if refund["reason"] != "客戶取消" {
		t.Errorf("OnOrderRefund reason = %v, want 客戶取消", refund["reason"])
	}
	if refund["restock"] != true {
		t.Errorf("OnOrderRefund restock = %v, want true", refund["restock"])
	}
	pluginEventsShopWantActor(t, refund, admin)
	if refunded := number(t, nested(t, refund, "order"), "refunded_total"); refunded != float64(paidOrder.TotalAmount) {
		t.Errorf("OnOrderRefund order.refunded_total = %v, want the final total %d (snapshot after the refund update)", refunded, paidOrder.TotalAmount)
	}

	update := pluginEventsShopExactly(t, h, plugin.HookOnOrderUpdate, 1)[0].Payload
	if update["from"] != "paid" || update["to"] != "cancelled" {
		t.Errorf("OnOrderUpdate from=%v to=%v, want paid to cancelled for a full refund of a paid order", update["from"], update["to"])
	}
	pluginEventsShopWantActor(t, update, admin)
}

func TestPluginEventsShopExpiredPendingOrder(t *testing.T) {
	h := pluginEventsShopHarness(t)
	p := shopSeedProduct(t, h.accessHarness, "events-expire-tee", 12000, 10)
	v := shopVariantRows(t, h.accessHarness, p.ID)[0]

	_, result := shopPlaceCheckout(t, h.accessHarness, v.ID, 1, "events-expire@example.test", "credit_card", "events-expire-1")
	pluginEventsShopExactly(t, h, plugin.HookOnOrderCreate, 1)
	h.reset(t)

	expired, err := core.ExpireUnpaidOrders(h.db, time.Now().Add(72*time.Hour))
	if err != nil {
		t.Fatalf("expire unpaid orders: %v", err)
	}
	if expired < 1 {
		t.Fatalf("ExpireUnpaidOrders expired %d orders, want at least the pending checkout", expired)
	}
	update := pluginEventsShopExactly(t, h, plugin.HookOnOrderUpdate, 1)[0].Payload
	if update["from"] != "pending" || update["to"] != "expired" {
		t.Errorf("OnOrderUpdate from=%v to=%v, want pending to expired", update["from"], update["to"])
	}
	if nested(t, update, "order")["number"] != result.Order.Number {
		t.Errorf("OnOrderUpdate order.number = %v, want %s", nested(t, update, "order")["number"], result.Order.Number)
	}
	pluginEventsShopWantNullActor(t, update)
}
