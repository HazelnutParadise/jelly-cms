package payment

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/money"
)

// ECPayGateway implements PaymentGateway for ECPay (綠界科技)
type ECPayGateway struct {
	config *ECPayConfig
}

// NewECPayGateway creates a new ECPay gateway instance
func NewECPayGateway(config *ECPayConfig) *ECPayGateway {
	return &ECPayGateway{config: config}
}

func (g *ECPayGateway) GetName() string {
	return "ECPay"
}

func (g *ECPayGateway) CreateOrder(ctx context.Context, req CreateOrderRequest) (*CreateOrderResponse, error) {
	if g == nil || g.config == nil {
		return nil, fmt.Errorf("ecpay: gateway is not configured")
	}
	if req.Currency != "TWD" {
		return nil, fmt.Errorf("ecpay: only TWD is supported")
	}
	amount, err := money.ToGateway(req.Amount)
	if err != nil {
		return nil, fmt.Errorf("ecpay: invalid amount: %w", err)
	}
	method := req.Method
	if method == "" {
		method = "credit_card" // Preserve compatibility for callers predating Method.
	}
	choosePayment, err := ecpayChoosePayment(method)
	if err != nil {
		return nil, err
	}
	clientBackURL, err := ecpayClientBackURL(g.config.ClientBackURL, req.OrderID)
	if err != nil {
		return nil, err
	}
	baseURL := "https://payment.ecpay.com.tw/Cashier/AioCheckOut/V5"
	if g.config.IsTestMode {
		baseURL = "https://payment-stage.ecpay.com.tw/Cashier/AioCheckOut/V5"
	}

	// Prepare form data
	formData := map[string]string{
		"MerchantID":        g.config.MerchantID,
		"MerchantTradeNo":   req.OrderID,
		"MerchantTradeDate": time.Now().In(ecpayTaipeiLocation()).Format("2006/01/02 15:04:05"),
		"PaymentType":       "aio",
		"TotalAmount":       strconv.FormatInt(amount, 10),
		"TradeDesc":         req.Description,
		"ItemName":          g.formatItemName(req.Items),
		"ReturnURL":         firstNonEmpty(g.config.ReturnURL, g.config.NotifyURL),
		"ClientBackURL":     clientBackURL,
		"ChoosePayment":     choosePayment,
		"EncryptType":       "1",
	}
	if formData["ReturnURL"] == "" || formData["ClientBackURL"] == "" {
		return nil, fmt.Errorf("ecpay: notification and client return URLs are required")
	}
	if method == "atm" {
		days, err := ecpayATMExpiryDays(req.ExpireTime)
		if err != nil {
			return nil, err
		}
		formData["ExpireDate"] = strconv.Itoa(days)
		formData["PaymentInfoURL"] = firstNonEmpty(g.config.NotifyURL, g.config.ReturnURL)
	}
	if method == "cvs" {
		minutes, err := ecpayCVSExpiryMinutes(req.ExpireTime)
		if err != nil {
			return nil, err
		}
		formData["StoreExpireDate"] = strconv.Itoa(minutes)
		formData["PaymentInfoURL"] = firstNonEmpty(g.config.NotifyURL, g.config.ReturnURL)
	}

	// Generate CheckMacValue
	checkMacValue := g.generateCheckMacValue(formData)
	formData["CheckMacValue"] = checkMacValue

	return &CreateOrderResponse{
		PaymentURL: baseURL,
		OrderID:    req.OrderID,
		ExtraData:  formData,
	}, nil
}

func ecpayClientBackURL(rawURL, merchantTradeNo string) (string, error) {
	backURL, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !backURL.IsAbs() || backURL.Host == "" || backURL.User != nil || backURL.Opaque != "" || backURL.Fragment != "" || (backURL.Scheme != "https" && backURL.Scheme != "http") {
		return "", fmt.Errorf("ecpay: invalid client back URL")
	}
	query, err := url.ParseQuery(backURL.RawQuery)
	if err != nil {
		return "", fmt.Errorf("ecpay: invalid client back URL query")
	}
	query.Set("MerchantTradeNo", merchantTradeNo)
	backURL.RawQuery = query.Encode()
	result := backURL.String()
	if len(result) > 200 {
		return "", fmt.Errorf("ecpay: client back URL exceeds 200 characters")
	}
	return result, nil
}

func (g *ECPayGateway) VerifyCallback(ctx context.Context, data map[string]interface{}) (*CallbackData, error) {
	if g == nil || g.config == nil {
		return nil, fmt.Errorf("ecpay: gateway is not configured")
	}
	strData, err := strictStringMap(data)
	if err != nil {
		return nil, fmt.Errorf("ecpay: invalid callback fields: %w", err)
	}

	// Verify CheckMacValue
	receivedCheckMac := strData["CheckMacValue"]
	if receivedCheckMac == "" {
		return nil, fmt.Errorf("ecpay: missing CheckMacValue")
	}
	delete(strData, "CheckMacValue")

	expectedCheckMac := g.generateCheckMacValue(strData)
	if len(receivedCheckMac) != len(expectedCheckMac) || subtle.ConstantTimeCompare([]byte(strings.ToUpper(receivedCheckMac)), []byte(expectedCheckMac)) != 1 {
		return nil, fmt.Errorf("invalid CheckMacValue")
	}
	if g.config.MerchantID != "" && strData["MerchantID"] != g.config.MerchantID {
		return nil, fmt.Errorf("ecpay: merchant id does not match")
	}
	tradeNo := strData["MerchantTradeNo"]
	if tradeNo == "" {
		return nil, fmt.Errorf("ecpay: missing MerchantTradeNo")
	}

	method, err := ecpayCallbackMethod(strData["PaymentType"])
	if err != nil {
		return nil, err
	}
	// The PaymentInfoURL notification can also report RtnCode=1. When it
	// carries an ATM/CVS payment number, that is issuance, not a capture.
	rtnCode := strData["RtnCode"]
	status := PaymentStatusPending
	simulatePaid := strData["SimulatePaid"]
	if simulatePaid != "" && simulatePaid != "0" && simulatePaid != "1" {
		return nil, fmt.Errorf("ecpay: invalid SimulatePaid value")
	}
	switch rtnCode {
	case "1":
		if simulatePaid != "1" && !ecpayHasOfflineInstructions(method, strData) {
			status = PaymentStatusPaid
		}
	case "0", "2", "10100073":
		status = PaymentStatusPending
	default:
		status = PaymentStatusFailed
	}

	wholeAmount, err := parsePositiveDecimal(strData["TradeAmt"])
	if err != nil || wholeAmount > math.MaxInt64/100 {
		return nil, fmt.Errorf("ecpay: invalid TradeAmt")
	}
	amount := wholeAmount * 100
	// Parse paid time
	var paidAt time.Time
	if strData["PaymentDate"] != "" {
		paidAt, _ = time.ParseInLocation("2006/01/02 15:04:05", strData["PaymentDate"], ecpayTaipeiLocation())
	}

	return &CallbackData{
		OrderID:         tradeNo,
		Provider:        "ecpay",
		MerchantTradeNo: tradeNo,
		Method:          method,
		DedupeKey:       ecpayDedupeKey(strData),
		GatewayOrderID:  strData["TradeNo"],
		Status:          status,
		Amount:          amount,
		Currency:        "TWD",
		TransactionID:   strData["TradeNo"],
		PaidAt:          paidAt,
		RawData:         data,
	}, nil
}

// Capabilities reports the card, ATM and convenience-store methods of ECPay.
func (g *ECPayGateway) Capabilities() Capabilities {
	return Capabilities{Methods: []string{"credit_card", "atm", "cvs"}, Offline: false}
}

// QueryOrder is not implemented for ECPay yet; it returns ErrNotSupported instead
// of a made-up status.
func (g *ECPayGateway) QueryOrder(ctx context.Context, orderID string) (*OrderStatus, error) {
	return nil, ErrNotSupported
}

// Refund is not implemented for ECPay yet; it returns ErrNotSupported instead of
// a made-up success.
func (g *ECPayGateway) Refund(ctx context.Context, req RefundRequest) (*RefundResponse, error) {
	return nil, ErrNotSupported
}

// generateCheckMacValue generates the CheckMacValue for ECPay
func (g *ECPayGateway) generateCheckMacValue(data map[string]string) string {
	// ECPay EncryptType=1 uses case-insensitive ordering and SHA-256 over the
	// lower-cased, .NET-compatible encoded form. CheckMacValue is never signed.
	keys := make([]string, 0, len(data))
	for k := range data {
		if !strings.EqualFold(k, "CheckMacValue") {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		li, lj := strings.ToLower(keys[i]), strings.ToLower(keys[j])
		if li == lj {
			return keys[i] < keys[j]
		}
		return li < lj
	})

	// Build query string
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, data[k]))
	}
	queryString := strings.Join(parts, "&")

	// Add HashKey and HashIV
	fullString := fmt.Sprintf("HashKey=%s&%s&HashIV=%s", g.config.HashKey, queryString, g.config.HashIV)

	// URL encode
	encoded := strings.ToLower(url.QueryEscape(fullString))
	encoded = strings.ReplaceAll(encoded, "~", "%7e")
	encoded = strings.ReplaceAll(encoded, "%20", "+")
	for escaped, literal := range map[string]string{
		"%2d": "-", "%5f": "_", "%2e": ".", "%21": "!",
		"%2a": "*", "%28": "(", "%29": ")",
	} {
		encoded = strings.ReplaceAll(encoded, escaped, literal)
	}
	hash := sha256.Sum256([]byte(encoded))
	return strings.ToUpper(hex.EncodeToString(hash[:]))
}

func ecpayChoosePayment(method string) (string, error) {
	switch method {
	case "credit_card":
		return "Credit", nil
	case "atm":
		return "ATM", nil
	case "cvs":
		return "CVS", nil
	default:
		return "", fmt.Errorf("ecpay: unsupported payment method")
	}
}

func ecpayATMExpiryDays(expires time.Time) (int, error) {
	if expires.IsZero() {
		return 3, nil
	}
	remaining := time.Until(expires)
	if remaining <= 0 {
		return 0, fmt.Errorf("ecpay: ATM expiration must be in the future")
	}
	days := int((remaining + 24*time.Hour - 1) / (24 * time.Hour))
	if days < 1 || days > 60 {
		return 0, fmt.Errorf("ecpay: ATM expiration must be between 1 and 60 days")
	}
	return days, nil
}

func ecpayCVSExpiryMinutes(expires time.Time) (int, error) {
	if expires.IsZero() {
		return 4320, nil
	}
	remaining := time.Until(expires)
	if remaining <= 0 {
		return 0, fmt.Errorf("ecpay: CVS expiration must be in the future")
	}
	minutes := int((remaining + time.Minute - 1) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	if minutes > 43200 {
		minutes = 43200
	}
	return minutes, nil
}

func ecpayTaipeiLocation() *time.Location {
	location, err := time.LoadLocation("Asia/Taipei")
	if err == nil {
		return location
	}
	// Taiwan has no daylight-saving transitions; keep the provider's UTC+8
	// contract even in minimal containers without the IANA timezone database.
	return time.FixedZone("Asia/Taipei", 8*60*60)
}

func ecpayCallbackMethod(paymentType string) (string, error) {
	switch {
	case strings.EqualFold(paymentType, "Credit_CreditCard"), strings.EqualFold(paymentType, "Credit"):
		return "credit_card", nil
	case strings.EqualFold(paymentType, "ATM") || strings.HasPrefix(strings.ToUpper(paymentType), "ATM_"):
		return "atm", nil
	case strings.EqualFold(paymentType, "CVS") || strings.HasPrefix(strings.ToUpper(paymentType), "CVS_"):
		return "cvs", nil
	case paymentType == "":
		return "", nil
	default:
		return "", fmt.Errorf("ecpay: unsupported callback payment type")
	}
}

func ecpayHasOfflineInstructions(method string, data map[string]string) bool {
	switch method {
	case "atm":
		return data["BankCode"] != "" || data["vAccount"] != ""
	case "cvs":
		return data["PaymentNo"] != "" || data["Barcode1"] != "" || data["Barcode2"] != "" || data["Barcode3"] != ""
	default:
		return false
	}
}

func parsePositiveDecimal(value string) (int64, error) {
	if value == "" {
		return 0, fmt.Errorf("empty number")
	}
	for i := range value {
		if value[i] < '0' || value[i] > '9' {
			return 0, fmt.Errorf("not an unsigned decimal")
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("not a positive int64")
	}
	return parsed, nil
}

func strictStringMap(data map[string]interface{}) (map[string]string, error) {
	result := make(map[string]string, len(data))
	for key, value := range data {
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("field %q must be a string", key)
		}
		result[key] = text
	}
	return result, nil
}

func ecpayDedupeKey(data map[string]string) string {
	keys := make([]string, 0, len(data))
	for key := range data {
		if !strings.EqualFold(key, "CheckMacValue") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var joined strings.Builder
	for _, key := range keys {
		joined.WriteString(key)
		joined.WriteByte('=')
		joined.WriteString(data[key])
		joined.WriteByte('&')
	}
	hash := sha256.Sum256([]byte(joined.String()))
	return "ecpay:" + hex.EncodeToString(hash[:])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// formatItemName formats items for ECPay ItemName field
func (g *ECPayGateway) formatItemName(items []OrderItem) string {
	if len(items) == 0 {
		return "商品"
	}

	var names []string
	for _, item := range items {
		names = append(names, fmt.Sprintf("%s x%d", item.Name, item.Quantity))
	}

	result := strings.Join(names, "#")
	// ECPay has a limit on ItemName length
	if len(result) > 400 {
		return result[:397] + "..."
	}
	return result
}
