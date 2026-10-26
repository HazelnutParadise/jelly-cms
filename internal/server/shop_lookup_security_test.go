package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

func TestCheckoutLookupReplayPreservesAccessProof(t *testing.T) {
	t.Run("sandbox", func(t *testing.T) {
		h := archivesHarness(t)
		shopSetup(t, h)
		p := shopSeedProduct(t, h, "lookup-replay-sandbox", 12000, 5)
		variant := shopVariantRows(t, h, p.ID)[0]
		browser := newShopBrowser(h)
		shopAddToCart(t, browser, variant.ID, 1)
		body := shopCheckoutBody("replay-sandbox@example.test", "credit_card", "lookup-replay-sandbox-key")

		first := browser.send(http.MethodPost, "/api/checkout", body)
		shopWantCode(t, first, "initial sandbox checkout", http.StatusCreated)
		var firstCheckout shopCheckoutResult
		if err := json.Unmarshal([]byte(first.body), &firstCheckout); err != nil {
			t.Fatalf("decode initial checkout response: %v", err)
		}

		// Repeating the same request models a lost checkout response after the server committed the order.
		replay := browser.send(http.MethodPost, "/api/checkout", body)
		if replay.panicked != nil {
			t.Fatalf("replayed sandbox checkout panicked: %v", replay.panicked)
		}
		if replay.code != http.StatusOK && replay.code != http.StatusCreated {
			t.Fatalf("replayed sandbox checkout returned %d, want the existing order (200 or 201)", replay.code)
		}
		var replayCheckout shopCheckoutResult
		if err := json.Unmarshal([]byte(replay.body), &replayCheckout); err != nil {
			t.Fatalf("decode replayed checkout response: %v", err)
		}
		if replayCheckout.Order.Number != firstCheckout.Order.Number {
			t.Fatalf("replayed checkout order does not match the committed order")
		}
		if replayCheckout.AccessToken == "" || replayCheckout.AccessToken != firstCheckout.AccessToken {
			t.Fatalf("replayed checkout did not preserve the committed access proof")
		}

		redirect, err := url.Parse(replayCheckout.RedirectURL)
		if err != nil {
			t.Fatalf("parse replayed sandbox redirect: %v", err)
		}
		if redirect.Query().Get("t") != replayCheckout.AccessToken {
			t.Fatalf("replayed sandbox redirect omitted the checkout access proof")
		}
		if got := browser.get(redirect.RequestURI()); got.code != http.StatusOK {
			t.Fatalf("GET replayed sandbox redirect returned %d, want 200", got.code)
		}
	})

	t.Run("ecpay-local-handoff", func(t *testing.T) {
		h := archivesHarness(t)
		shopSetup(t, h)
		t.Setenv("APP_URL", "http://jelly.test")
		shopSetOptions(t, h, map[string]string{
			"payment_methods":           `{"credit_card":"ecpay"}`,
			"payment_ecpay_enabled":     "true",
			"payment_ecpay_merchant_id": "LOCAL-TEST-MERCHANT",
			"payment_ecpay_hash_key":    "local-test-hash-key-only",
			"payment_ecpay_hash_iv":     "local-test-hash-iv-only",
			"payment_ecpay_test_mode":   "true",
		})
		p := shopSeedProduct(t, h, "lookup-replay-ecpay", 12000, 5)
		variant := shopVariantRows(t, h, p.ID)[0]
		browser := newShopBrowser(h)
		shopAddToCart(t, browser, variant.ID, 1)
		body := shopCheckoutBody("replay-ecpay@example.test", "credit_card", "lookup-replay-ecpay-key")

		first := browser.send(http.MethodPost, "/api/checkout", body)
		shopWantCode(t, first, "initial ECPay checkout", http.StatusCreated)
		var firstCheckout shopCheckoutResult
		if err := json.Unmarshal([]byte(first.body), &firstCheckout); err != nil {
			t.Fatalf("decode initial ECPay checkout response: %v", err)
		}

		// Checkout and the local ECPay form builder only assemble data; this test makes no network request.
		replay := browser.send(http.MethodPost, "/api/checkout", body)
		if replay.panicked != nil {
			t.Fatalf("replayed ECPay checkout panicked: %v", replay.panicked)
		}
		if replay.code != http.StatusOK && replay.code != http.StatusCreated {
			t.Fatalf("replayed ECPay checkout returned %d, want the existing order (200 or 201)", replay.code)
		}
		var replayCheckout shopCheckoutResult
		if err := json.Unmarshal([]byte(replay.body), &replayCheckout); err != nil {
			t.Fatalf("decode replayed ECPay checkout response: %v", err)
		}
		if replayCheckout.Order.Number != firstCheckout.Order.Number {
			t.Fatalf("replayed ECPay checkout order does not match the committed order")
		}
		if replayCheckout.AccessToken == "" || replayCheckout.AccessToken != firstCheckout.AccessToken {
			t.Fatalf("replayed ECPay checkout did not preserve the committed access proof")
		}

		redirect, err := url.Parse(replayCheckout.RedirectURL)
		if err != nil {
			t.Fatalf("parse replayed ECPay redirect: %v", err)
		}
		if redirect.Query().Get("t") != replayCheckout.AccessToken {
			t.Fatalf("replayed ECPay redirect omitted the checkout access proof")
		}
		if got := browser.get(redirect.RequestURI()); got.code != http.StatusOK {
			t.Fatalf("GET replayed local ECPay handoff returned %d, want 200", got.code)
		}
	})
}

func TestCheckoutLookupTokensAreShortLivedAndSeparateFromOrderProof(t *testing.T) {
	h := newAccessHarness(t)
	shopSetup(t, h)
	p := shopSeedProduct(t, h, "lookup-token-contract", 12000, 5)
	variant := shopVariantRows(t, h, p.ID)[0]
	browser, checkout := shopPlaceCheckout(t, h, variant.ID, 1, "lookup-contract@example.test", "bank_transfer", "lookup-token-contract-key")
	order := shopOrderRow(t, h, checkout.Order.Number)
	if checkout.AccessToken == "" || order.LookupTokenHash != shopSHA256Hex(checkout.AccessToken) {
		t.Fatal("checkout access proof does not match the order's stored proof hash")
	}

	var tokens []core.OrderAccessToken
	if err := h.db.Where("order_id = ?", order.ID).Order("id").Find(&tokens).Error; err != nil {
		t.Fatalf("read checkout order access tokens: %v", err)
	}
	if len(tokens) == 0 {
		t.Fatal("bank-transfer checkout created no emailed lookup token")
	}
	for _, token := range tokens {
		if token.Kind != "lookup" {
			t.Errorf("checkout created an order access token of kind %q, want lookup", token.Kind)
		}
		if token.TokenHash == order.LookupTokenHash {
			t.Errorf("checkout proof was also persisted as an order access token")
		}
		if remaining := time.Until(token.ExpiresAt); remaining < 14*time.Minute || remaining > 16*time.Minute {
			t.Errorf("checkout lookup token lifetime is %v, want approximately 15 minutes", remaining)
		}
	}

	var deliveries []core.NotificationDelivery
	if err := h.db.Where("kind = ? AND to_address = ?", "order.lookup_link", "lookup-contract@example.test").Find(&deliveries).Error; err != nil {
		t.Fatalf("read checkout lookup deliveries: %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("checkout queued %d lookup-link deliveries, want 1", len(deliveries))
	}
	var params map[string]string
	if err := json.Unmarshal(deliveries[0].Params, &params); err != nil {
		t.Fatalf("decode lookup delivery parameters: %v", err)
	}
	lookupURL, err := url.Parse(params["url"])
	if err != nil || lookupURL.Path != "/orders/view" || lookupURL.Query().Get("t") == "" {
		t.Fatal("lookup delivery has no usable order-view link")
	}
	if lookupURL.Query().Get("t") == checkout.AccessToken {
		t.Fatal("emailed lookup link reused the long-lived checkout proof")
	}
	checkoutProof := browser.get("/orders/view?t=" + url.QueryEscape(checkout.AccessToken))
	shopWantCode(t, checkoutProof, "GET /orders/view with checkout proof instead of lookup token", http.StatusNotFound)
	freshLookup := browser.get(lookupURL.RequestURI())
	shopWantCode(t, freshLookup, "GET /orders/view with emailed lookup token", http.StatusFound)
	if location := freshLookup.header.Get("Location"); location != "/orders/view/"+order.Number {
		t.Errorf("fresh lookup token redirected to %q, want its bound order", location)
	}
}

func TestLookupExchangeIsOneTimeAndViewCookieIsOrderBound(t *testing.T) {
	h := archivesHarness(t)
	shopSetup(t, h)
	first := shopSeedOrder(t, h, shopOrderSeed{number: fmt.Sprintf("JL-%d-1001", time.Now().Year()), email: "lookup-first@example.test", status: "pending", total: 12000})
	second := shopSeedOrder(t, h, shopOrderSeed{number: fmt.Sprintf("JL-%d-1002", time.Now().Year()), email: "lookup-second@example.test", status: "pending", total: 12000})
	lookupToken := "parallel-lookup-exchange-fixture-token-0001"
	shopSeedLookupToken(t, h, first.ID, lookupToken, "lookup", time.Now().Add(15*time.Minute))

	browsers := []*shopBrowser{newShopBrowser(h), newShopBrowser(h)}
	start := make(chan struct{})
	type result struct {
		browser  *shopBrowser
		response shopResponse
	}
	results := make(chan result, len(browsers))
	var wg sync.WaitGroup
	for _, b := range browsers {
		wg.Add(1)
		go func(browser *shopBrowser) {
			defer wg.Done()
			<-start
			results <- result{browser: browser, response: browser.get("/orders/view?t=" + url.QueryEscape(lookupToken))}
		}(b)
	}
	close(start)
	wg.Wait()
	close(results)

	var winner *shopBrowser
	redirects := 0
	for got := range results {
		if got.response.panicked != nil {
			t.Fatalf("parallel lookup exchange panicked: %v", got.response.panicked)
		}
		if got.response.code == http.StatusFound {
			redirects++
			winner = got.browser
			if loc := got.response.header.Get("Location"); loc != "/orders/view/"+first.Number {
				t.Errorf("lookup token redirected to %q, want its bound order", loc)
			}
		} else if got.response.code >= http.StatusOK && got.response.code < http.StatusMultipleChoices {
			t.Errorf("second lookup exchange returned success status %d", got.response.code)
		}
	}
	if redirects != 1 || winner == nil {
		t.Fatalf("parallel lookup exchange produced %d successful redirects, want exactly 1", redirects)
	}
	if r := winner.get("/orders/view/" + first.Number); r.code != http.StatusOK {
		t.Fatalf("view cookie did not open its bound order: status %d", r.code)
	}
	if r := winner.get("/orders/view/" + second.Number); r.code == http.StatusOK {
		t.Fatal("view cookie opened another customer's order")
	}

	var view core.OrderAccessToken
	if err := h.db.Where("order_id = ? AND kind = ?", first.ID, "view").First(&view).Error; err != nil {
		t.Fatalf("read exchanged view token: %v", err)
	}
	if err := h.db.Model(&core.OrderAccessToken{}).Where("id = ?", view.ID).Update("expires_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatalf("expire exchanged view token: %v", err)
	}
	if r := winner.get("/orders/view/" + first.Number); r.code == http.StatusOK {
		t.Fatal("expired view cookie continued to open its order")
	}
}
