package payment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/notify"
	"gorm.io/gorm"
)

func TestECPayClientBackURLParticipatesInCreateSignature(t *testing.T) {
	backURL := "https://shop.example.test/api/payment/ecpay/return?campaign=shop"
	tradeNo := "SBX00000000000007201"
	gateway := NewECPayGateway(&ECPayConfig{
		MerchantID: "LOCAL-TEST-MERCHANT", HashKey: "local-test-hash-key-only", HashIV: "local-test-hash-iv-only",
		IsTestMode: true, ReturnURL: "https://shop.example.test/api/payment/ecpay/notify", ClientBackURL: backURL,
	})
	created, err := gateway.CreateOrder(context.Background(), CreateOrderRequest{
		OrderID: tradeNo, Amount: 12000, Currency: "TWD", Method: "credit_card",
		Description: "Regression order",
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	gotBackURL := created.ExtraData["ClientBackURL"]
	parsedBackURL, err := url.Parse(gotBackURL)
	if err != nil {
		t.Fatalf("parse ClientBackURL: %v", err)
	}
	query := parsedBackURL.Query()
	if parsedBackURL.Path != "/api/payment/ecpay/return" || (query.Get("MerchantTradeNo") != tradeNo && query.Get("trade_no") != tradeNo) {
		t.Fatalf("ClientBackURL = %q, want the return route to include the merchant trade number", gotBackURL)
	}
	gotSignature := created.ExtraData["CheckMacValue"]
	unsigned := make(map[string]string, len(created.ExtraData)-1)
	for key, value := range created.ExtraData {
		if key != "CheckMacValue" {
			unsigned[key] = value
		}
	}
	if want := gateway.generateCheckMacValue(unsigned); gotSignature != want {
		t.Fatalf("CheckMacValue = %q, want signature over every handoff field %q", gotSignature, want)
	}
	unsigned["ClientBackURL"] += "&unexpected=1"
	if gateway.generateCheckMacValue(unsigned) == gotSignature {
		t.Fatal("changing ClientBackURL did not change CheckMacValue; return URL is not signed")
	}
}

func TestPaidOrderConfirmationUsesLookupExchangeURL(t *testing.T) {
	db := newPaymentShopTestDB(t)
	fx := paymentShopSeed(t, db, 7202, 12000, "TWD")
	paymentShopAddProductItem(t, db, fx)
	callback := paymentShopCallback(fx, PaymentStatusPaid, 12000, "TWD", "lookup-url-regression")
	if err := db.Transaction(func(tx *gorm.DB) error { return Finalize(tx, "sandbox", callback) }); err != nil {
		t.Fatalf("Finalize(paid): %v", err)
	}
	var delivery core.NotificationDelivery
	if err := db.Where("kind = ? AND to_address = ?", notify.KindOrderConfirmation, "pay-extra@example.test").First(&delivery).Error; err != nil {
		t.Fatalf("read paid confirmation outbox: %v", err)
	}
	var params struct {
		AccessURL string `json:"access_url"`
	}
	if err := json.Unmarshal(delivery.Params, &params); err != nil {
		t.Fatalf("decode paid confirmation params: %v", err)
	}
	parsed, err := url.Parse(params.AccessURL)
	if err != nil {
		t.Fatalf("parse paid confirmation access_url: %v", err)
	}
	if parsed.Path != "/orders/view" || parsed.Query().Get("t") == "" || strings.Contains(parsed.Path, fx.number) {
		t.Fatalf("paid confirmation access_url = %q, want /orders/view?t=<one-use token>", params.AccessURL)
	}
	token := parsed.Query().Get("t")
	digest := sha256.Sum256([]byte(token))
	var stored core.OrderAccessToken
	if err := db.Where("token_hash = ?", hex.EncodeToString(digest[:])).First(&stored).Error; err != nil {
		t.Fatalf("lookup token from outbox URL was not stored as a hash: %v", err)
	}
	if stored.Kind != "lookup" || stored.OrderID != fx.orderID || stored.UsedAt != nil {
		t.Errorf("paid confirmation token = kind %q, order %d, used %v; want a fresh lookup token for order %d", stored.Kind, stored.OrderID, stored.UsedAt, fx.orderID)
	}
	if left := time.Until(stored.ExpiresAt); left < 14*time.Minute || left > 16*time.Minute {
		t.Errorf("paid confirmation lookup token lives %v, want 15 minutes", left)
	}
}
