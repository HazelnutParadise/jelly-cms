package payment

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
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
	baseURL := "https://payment.ecpay.com.tw/Cashier/AioCheckOut/V5"
	if g.config.IsTestMode {
		baseURL = "https://payment-stage.ecpay.com.tw/Cashier/AioCheckOut/V5"
	}

	// Prepare form data
	formData := map[string]string{
		"MerchantID":        g.config.MerchantID,
		"MerchantTradeNo":   req.OrderID,
		"MerchantTradeDate": time.Now().Format("2006/01/02 15:04:05"),
		"PaymentType":       "aio",
		"TotalAmount":       fmt.Sprintf("%d", req.Amount),
		"TradeDesc":         req.Description,
		"ItemName":          g.formatItemName(req.Items),
		"ReturnURL":         g.config.ReturnURL,
		"OrderResultURL":    g.config.NotifyURL,
		"ChoosePayment":     "ALL",
		"EncryptType":       "1",
	}

	// Add expiration time if provided
	if !req.ExpireTime.IsZero() {
		formData["ExpireDate"] = req.ExpireTime.Format("2006/01/02 15:04:05")
	}

	// Generate CheckMacValue
	checkMacValue := g.generateCheckMacValue(formData)
	formData["CheckMacValue"] = checkMacValue

	// Build payment URL with form data
	values := url.Values{}
	for k, v := range formData {
		values.Set(k, v)
	}

	return &CreateOrderResponse{
		PaymentURL: fmt.Sprintf("%s?%s", baseURL, values.Encode()),
		OrderID:    req.OrderID,
		ExtraData:  formData,
	}, nil
}

func (g *ECPayGateway) VerifyCallback(ctx context.Context, data map[string]interface{}) (*CallbackData, error) {
	// Convert map to string map for processing
	strData := make(map[string]string)
	for k, v := range data {
		strData[k] = fmt.Sprintf("%v", v)
	}

	// Verify CheckMacValue
	receivedCheckMac := strData["CheckMacValue"]
	delete(strData, "CheckMacValue")

	expectedCheckMac := g.generateCheckMacValue(strData)
	if receivedCheckMac != expectedCheckMac {
		return nil, fmt.Errorf("invalid CheckMacValue")
	}

	// Parse payment status
	rtnCode := strData["RtnCode"]
	status := PaymentStatusPending
	if rtnCode == "1" {
		status = PaymentStatusPaid
	} else if rtnCode != "0" {
		status = PaymentStatusFailed
	}

	// Parse amount
	var amount int64
	fmt.Sscanf(strData["TradeAmt"], "%d", &amount)

	// Parse paid time
	var paidAt time.Time
	if strData["PaymentDate"] != "" {
		paidAt, _ = time.Parse("2006/01/02 15:04:05", strData["PaymentDate"])
	}

	return &CallbackData{
		OrderID:        strData["MerchantTradeNo"],
		GatewayOrderID: strData["TradeNo"],
		Status:          status,
		Amount:          amount,
		Currency:       "TWD",
		TransactionID:  strData["TradeNo"],
		PaidAt:          paidAt,
		RawData:         data,
	}, nil
}

func (g *ECPayGateway) QueryOrder(ctx context.Context, orderID string) (*OrderStatus, error) {
	// ECPay query order implementation
	// This would typically make an API call to ECPay
	// For now, return a placeholder
	return &OrderStatus{
		OrderID: orderID,
		Status:  PaymentStatusPending,
	}, nil
}

func (g *ECPayGateway) Refund(ctx context.Context, req RefundRequest) (*RefundResponse, error) {
	// ECPay refund implementation
	// This would typically make an API call to ECPay
	return &RefundResponse{
		RefundID:      fmt.Sprintf("REFUND_%s_%d", req.OrderID, time.Now().Unix()),
		Status:        "success",
		RefundedAmount: req.Amount,
		RefundedAt:     time.Now(),
	}, nil
}

// generateCheckMacValue generates the CheckMacValue for ECPay
func (g *ECPayGateway) generateCheckMacValue(data map[string]string) string {
	// Sort keys
	keys := make([]string, 0, len(data))
	for k := range data {
		if k != "CheckMacValue" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	// Build query string
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, data[k]))
	}
	queryString := strings.Join(parts, "&")

	// Add HashKey and HashIV
	fullString := fmt.Sprintf("HashKey=%s&%s&HashIV=%s", g.config.HashKey, queryString, g.config.HashIV)

	// URL encode
	encoded := url.QueryEscape(fullString)
	encoded = strings.ToLower(encoded)
	encoded = strings.ReplaceAll(encoded, "%20", "+")
	encoded = strings.ReplaceAll(encoded, "%2d", "-")
	encoded = strings.ReplaceAll(encoded, "%5f", "_")
	encoded = strings.ReplaceAll(encoded, "%2e", ".")
	encoded = strings.ReplaceAll(encoded, "%21", "!")
	encoded = strings.ReplaceAll(encoded, "%2a", "*")
	encoded = strings.ReplaceAll(encoded, "%28", "(")
	encoded = strings.ReplaceAll(encoded, "%29", ")")

	// MD5 hash
	hash := md5.Sum([]byte(encoded))
	return strings.ToUpper(hex.EncodeToString(hash[:]))
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

