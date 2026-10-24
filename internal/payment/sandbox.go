package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// SandboxGateway is the test provider for non-production environments (ticket 07).
// It is only registered when the option payment_sandbox_enabled is true and
// APP_ENV is not production. Its callbacks are signed with a per-site key using
// HMAC-SHA256, so a sandbox notification never passes as a real one.
type SandboxGateway struct {
	key string
}

var _ PaymentGateway = (*SandboxGateway)(nil)

// NewSandboxGateway returns the sandbox gateway with the per-site signing key.
func NewSandboxGateway(key string) *SandboxGateway {
	return &SandboxGateway{key: key}
}

// GetName returns "sandbox".
func (g *SandboxGateway) GetName() string { return "sandbox" }

// Capabilities reports card, ATM and convenience-store payments, all online.
func (g *SandboxGateway) Capabilities() Capabilities {
	return Capabilities{Methods: []string{"credit_card", "atm", "cvs"}, Offline: false}
}

// CreateOrder returns the sandbox payment page URL (/checkout/sandbox/<trade_no>).
func (g *SandboxGateway) CreateOrder(ctx context.Context, req CreateOrderRequest) (*CreateOrderResponse, error) {
	if g == nil || !validSandboxKey(g.key) {
		return nil, fmt.Errorf("sandbox: signing key is invalid")
	}
	if req.OrderID == "" || req.Amount < 0 || req.Currency != "TWD" {
		return nil, fmt.Errorf("sandbox: invalid payment order")
	}
	method := req.Method
	if method == "" {
		method = "credit_card"
	}
	if _, err := ecpayChoosePayment(method); err != nil {
		return nil, fmt.Errorf("sandbox: unsupported payment method")
	}
	return &CreateOrderResponse{
		PaymentURL: "/checkout/sandbox/" + url.PathEscape(req.OrderID) + "?t=" + url.QueryEscape(req.AccessToken),
		OrderID:    req.OrderID,
		ExtraData:  map[string]string{"method": method},
	}, nil
}

// VerifyCallback checks the HMAC-SHA256 signature and returns the verified notification.
func (g *SandboxGateway) VerifyCallback(ctx context.Context, data map[string]interface{}) (*CallbackData, error) {
	if g == nil || !validSandboxKey(g.key) {
		return nil, fmt.Errorf("sandbox: signing key is invalid")
	}
	values, err := strictStringMap(data)
	if err != nil {
		return nil, fmt.Errorf("sandbox: callback fields must be strings")
	}
	tradeNo := values["merchant_trade_no"]
	statusText := values["status"]
	amountText := values["amount"]
	currency := values["currency"]
	nonce := values["nonce"]
	signature := values["signature"]
	if tradeNo == "" || nonce == "" || len(nonce) > 200 || signature == "" {
		return nil, fmt.Errorf("sandbox: missing callback fields")
	}
	if currency != "TWD" {
		return nil, fmt.Errorf("sandbox: unsupported currency")
	}
	amount, err := parseUnsignedMinorUnits(amountText)
	if err != nil {
		return nil, fmt.Errorf("sandbox: invalid amount")
	}
	status := PaymentStatusPending
	switch statusText {
	case "paid":
		status = PaymentStatusPaid
	case "failed":
		status = PaymentStatusFailed
	case "pending":
		status = PaymentStatusPending
	default:
		return nil, fmt.Errorf("sandbox: invalid status")
	}
	provided, err := hex.DecodeString(signature)
	if err != nil || len(provided) != sha256.Size {
		return nil, fmt.Errorf("sandbox: invalid signature")
	}
	key, _ := hex.DecodeString(g.key)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(sandboxCanonical(values)))
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return nil, fmt.Errorf("sandbox: invalid signature")
	}
	return &CallbackData{
		OrderID: tradeNo, Provider: "sandbox", MerchantTradeNo: tradeNo,
		Status: status, Amount: amount, Currency: currency, DedupeKey: nonce,
		RawData: data,
	}, nil
}

// SignSimulation builds a signed callback for the non-production sandbox UI.
// It is for server-side simulation only; browser clients never receive g.key.
func (g *SandboxGateway) SignSimulation(tradeNo, status string, amount int64, currency, nonce string) (map[string]interface{}, error) {
	if g == nil || !validSandboxKey(g.key) {
		return nil, fmt.Errorf("sandbox: signing key is invalid")
	}
	if tradeNo == "" || nonce == "" || len(nonce) > 200 || amount < 0 || currency != "TWD" {
		return nil, fmt.Errorf("sandbox: invalid simulation")
	}
	if status != "paid" && status != "failed" && status != "pending" {
		return nil, fmt.Errorf("sandbox: invalid simulation status")
	}
	values := map[string]string{
		"merchant_trade_no": tradeNo,
		"status":            status,
		"amount":            strconv.FormatInt(amount, 10),
		"currency":          currency,
		"nonce":             nonce,
	}
	key, _ := hex.DecodeString(g.key)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(sandboxCanonical(values)))
	values["signature"] = hex.EncodeToString(mac.Sum(nil))
	result := make(map[string]interface{}, len(values))
	for name, value := range values {
		result[name] = value
	}
	return result, nil
}

// QueryOrder is not supported by the sandbox.
func (g *SandboxGateway) QueryOrder(ctx context.Context, orderID string) (*OrderStatus, error) {
	return nil, ErrNotSupported
}

// Refund is not supported by the sandbox.
func (g *SandboxGateway) Refund(ctx context.Context, req RefundRequest) (*RefundResponse, error) {
	return nil, ErrNotSupported
}

func validSandboxKey(encoded string) bool {
	key, err := hex.DecodeString(encoded)
	return err == nil && len(key) == 32
}

func sandboxCanonical(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if key != "signature" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+values[key])
	}
	return strings.Join(parts, "&")
}

func parseUnsignedMinorUnits(value string) (int64, error) {
	if value == "" {
		return 0, fmt.Errorf("empty amount")
	}
	for i := range value {
		if value[i] < '0' || value[i] > '9' {
			return 0, fmt.Errorf("invalid amount")
		}
	}
	return strconv.ParseInt(value, 10, 64)
}
