package server

// Order lookup integration tests for ticket 07, section E7 (contract: /orders/lookup, /orders/view and
// POST /api/orders/lookup, /api/orders/:number/resend in docs/tickets/07-shop.md). They run against the dedicated
// PostgreSQL database and are skipped unless JELLY_TEST_POSTGRES=1. They must fail until the implementation exists.
// Do not edit them to match an implementation.
//
// Tokens: the emailed link carries a random token t. Only its SHA-256 is stored in order_access_tokens. The
// lookup kind lives 15 minutes; the view cookie that /orders/view?t= sets lives 30 minutes (Max-Age 1800).

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

var shopLongToken = regexp.MustCompile(`[A-Za-z0-9_-]{32,}`)

// shopSeedLookupToken inserts a lookup or view token row for orderID with a known raw value.
func shopSeedLookupToken(t *testing.T, h *accessHarness, orderID uint, raw, kind string, expiresAt time.Time) {
	t.Helper()
	row := core.OrderAccessToken{OrderID: orderID, TokenHash: shopSHA256Hex(raw), Kind: kind, ExpiresAt: expiresAt, CreatedIPMasked: "198.51.100.0/24"}
	if err := h.db.Create(&row).Error; err != nil {
		t.Fatalf("seed %s token: %v", kind, err)
	}
}

func TestLookupAlwaysSame202(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	number := fmt.Sprintf("JL-%d-0001", time.Now().Year())
	shopSeedOrder(t, h, shopOrderSeed{number: number, email: "guest@example.test", status: "pending", total: 12000})

	b := newShopBrowser(h)
	hit := b.send(http.MethodPost, "/api/orders/lookup", map[string]any{"number": number, "email": "guest@example.test", "website": ""})
	wrongEmail := b.send(http.MethodPost, "/api/orders/lookup", map[string]any{"number": number, "email": "nobody@example.test", "website": ""})
	unknown := b.send(http.MethodPost, "/api/orders/lookup", map[string]any{"number": "JL-2099-9999", "email": "guest@example.test", "website": ""})

	for label, r := range map[string]shopResponse{"matching order": hit, "wrong email": wrongEmail, "unknown order": unknown} {
		shopWantCode(t, r, "POST /api/orders/lookup ("+label+")", http.StatusAccepted)
	}
	if wrongEmail.body != hit.body || unknown.body != hit.body {
		t.Errorf("responses differ: hit=%q wrong-email=%q unknown=%q; they must be identical", hit.body, wrongEmail.body, unknown.body)
	}
}

func TestLookupTokenHashedExpiresAndBoundToOrder(t *testing.T) {
	h := archivesHarness(t)
	shopSetup(t, h)
	year := time.Now().Year()
	first := shopSeedOrder(t, h, shopOrderSeed{number: fmt.Sprintf("JL-%d-0001", year), email: "bound@example.test", status: "pending", total: 12000})
	second := shopSeedOrder(t, h, shopOrderSeed{number: fmt.Sprintf("JL-%d-0002", year), email: "other@example.test", status: "pending", total: 12000})

	b := newShopBrowser(h)
	shopWantCode(t, b.send(http.MethodPost, "/api/orders/lookup", map[string]any{"number": first.Number, "email": "bound@example.test", "website": ""}),
		"POST /api/orders/lookup", http.StatusAccepted)
	var rows []core.OrderAccessToken
	if err := h.db.Where("kind = ?", "lookup").Find(&rows).Error; err != nil {
		t.Fatalf("read lookup tokens: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("%d lookup token rows after one lookup, want 1", len(rows))
	}
	row := rows[0]
	if !shopHex64.MatchString(row.TokenHash) {
		t.Errorf("token_hash %q is not 64 lowercase hex characters", row.TokenHash)
	}
	if row.OrderID != first.ID {
		t.Errorf("token is bound to order %d, want %d", row.OrderID, first.ID)
	}
	if hold := time.Until(row.ExpiresAt); hold < 14*time.Minute || hold > 16*time.Minute {
		t.Errorf("lookup token lives %v, want 15 minutes", hold)
	}

	// An expired token is refused, and a live token opens only its own order.
	shopSeedLookupToken(t, h, second.ID, "expired-lookup-token-0002", "lookup", time.Now().Add(-time.Minute))
	expired := b.get("/orders/view?t=expired-lookup-token-0002")
	if expired.code == http.StatusFound {
		t.Errorf("expired lookup token was accepted (302 to %q)", expired.header.Get("Location"))
	}
	shopSeedLookupToken(t, h, first.ID, "valid-lookup-token-0001", "lookup", time.Now().Add(15*time.Minute))
	live := newShopBrowser(h).get("/orders/view?t=valid-lookup-token-0001")
	shopWantCode(t, live, "GET /orders/view with a live token", http.StatusFound)
	if loc := live.header.Get("Location"); loc != "/orders/view/"+first.Number {
		t.Errorf("live token redirects to %q, want /orders/view/%s", loc, first.Number)
	}
}

func TestLookupTokenNeverInAnonymousResponse(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	number := fmt.Sprintf("JL-%d-0001", time.Now().Year())
	shopSeedOrder(t, h, shopOrderSeed{number: number, email: "anon@example.test", status: "pending", total: 12000})

	r := newShopBrowser(h).send(http.MethodPost, "/api/orders/lookup", map[string]any{"number": number, "email": "anon@example.test", "website": ""})
	shopWantCode(t, r, "POST /api/orders/lookup", http.StatusAccepted)
	if len(r.cookies) != 0 {
		t.Errorf("anonymous lookup set %d cookies; it must not set any", len(r.cookies))
	}
	if strings.Contains(r.body, "/orders/view") || strings.Contains(r.body, "t=") {
		t.Errorf("anonymous response contains a link: %q", r.body)
	}
	if shopLongToken.MatchString(r.body) {
		t.Errorf("anonymous response contains a token-like string: %q", r.body)
	}
	var rows []core.OrderAccessToken
	if err := h.db.Find(&rows).Error; err != nil {
		t.Fatalf("read tokens: %v", err)
	}
	for _, row := range rows {
		if strings.Contains(r.body, row.TokenHash) {
			t.Errorf("anonymous response contains the stored token hash")
		}
	}
}

func TestLookupRateLimitedPerIPAndNumber(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)

	perIP := newShopBrowser(h)
	perIP.remote = "203.0.113.50:1234"
	limited := false
	for i := 0; i < 30 && !limited; i++ {
		r := perIP.send(http.MethodPost, "/api/orders/lookup", map[string]any{"number": fmt.Sprintf("JL-2099-%04d", i), "email": "x@example.test", "website": ""})
		limited = r.code == http.StatusTooManyRequests
	}
	if !limited {
		t.Error("30 lookups from one IP never got 429")
	}

	perNumber := false
	for i := 0; i < 30 && !perNumber; i++ {
		b := newShopBrowser(h)
		b.remote = fmt.Sprintf("203.0.113.%d:1234", 100+i)
		r := b.send(http.MethodPost, "/api/orders/lookup", map[string]any{"number": "JL-2099-0777", "email": "y@example.test", "website": ""})
		perNumber = r.code == http.StatusTooManyRequests
	}
	if !perNumber {
		t.Error("30 lookups for one order number from different IPs never got 429")
	}
}

func TestLookupViewExchangesTokenForCookieAndRedirects(t *testing.T) {
	h := archivesHarness(t)
	shopSetup(t, h)
	order := shopSeedOrder(t, h, shopOrderSeed{number: fmt.Sprintf("JL-%d-0001", time.Now().Year()), email: "view@example.test", status: "pending", total: 12000})
	shopSeedLookupToken(t, h, order.ID, "exchange-token-0001", "lookup", time.Now().Add(15*time.Minute))

	b := newShopBrowser(h)
	r := b.get("/orders/view?t=exchange-token-0001")
	shopWantCode(t, r, "GET /orders/view?t=", http.StatusFound)
	if loc := r.header.Get("Location"); loc != "/orders/view/"+order.Number {
		t.Errorf("Location = %q, want /orders/view/%s", loc, order.Number)
	}
	found := false
	for _, c := range r.cookies {
		if c.HttpOnly && c.MaxAge > 1700 && c.MaxAge <= 1800 {
			found = true
		}
	}
	if !found {
		t.Errorf("no HttpOnly cookie with a 30-minute Max-Age was set; Set-Cookie=%v", r.header.Values("Set-Cookie"))
	}

	if without := newShopBrowser(h).get("/orders/view/" + order.Number); without.code == http.StatusOK {
		t.Error("the order page opened without the view cookie")
	}
	if with := b.get("/orders/view/" + order.Number); with.code != http.StatusOK {
		t.Errorf("the order page with the view cookie = %d, want 200", with.code)
	}
}

func TestResendLimitedThreePerHour(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	order := shopSeedOrder(t, h, shopOrderSeed{number: fmt.Sprintf("JL-%d-0001", time.Now().Year()), email: "resend@example.test", status: "pending", total: 12000})
	checkoutProof := "fixture-checkout-proof-for-resend-test-0123456789"
	if err := h.db.Model(&core.Order{}).Where("id = ?", order.ID).Update("lookup_token_hash", shopSHA256Hex(checkoutProof)).Error; err != nil {
		t.Fatalf("seed checkout proof hash: %v", err)
	}

	b := newShopBrowser(h)
	path := "/api/orders/" + order.Number + "/resend"
	var deliveriesBefore int64
	if err := h.db.Model(&core.NotificationDelivery{}).Where("kind = ? AND to_address = ?", "order.lookup_link", "resend@example.test").Count(&deliveriesBefore).Error; err != nil {
		t.Fatalf("count lookup deliveries before unauthorized resend: %v", err)
	}
	unauthorized := b.send(http.MethodPost, path, map[string]any{"email": "resend@example.test"})
	shopWantCode(t, unauthorized, "resend with matching email but without order proof", http.StatusNotFound)
	var deliveriesAfterUnauthorized int64
	if err := h.db.Model(&core.NotificationDelivery{}).Where("kind = ? AND to_address = ?", "order.lookup_link", "resend@example.test").Count(&deliveriesAfterUnauthorized).Error; err != nil {
		t.Fatalf("count lookup deliveries after unauthorized resend: %v", err)
	}
	if deliveriesAfterUnauthorized != deliveriesBefore {
		t.Errorf("unauthorized resend queued %d lookup deliveries, want none", deliveriesAfterUnauthorized-deliveriesBefore)
	}
	for i := 1; i <= 3; i++ {
		shopWantOK(t, b.send(http.MethodPost, path, map[string]any{"email": "resend@example.test", "access": checkoutProof}), fmt.Sprintf("resend %d with checkout proof", i))
	}
	shopWantCode(t, b.send(http.MethodPost, path, map[string]any{"email": "resend@example.test", "access": checkoutProof}), "resend 4 with checkout proof", http.StatusTooManyRequests)
}
