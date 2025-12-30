package payment

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// NewebPayGateway implements PaymentGateway for NewebPay (藍新金流)
type NewebPayGateway struct {
	config *NewebPayConfig
}

// NewNewebPayGateway creates a new NewebPay gateway instance
func NewNewebPayGateway(config *NewebPayConfig) *NewebPayGateway {
	return &NewebPayGateway{config: config}
}

func (g *NewebPayGateway) GetName() string {
	return "NewebPay"
}

func (g *NewebPayGateway) CreateOrder(ctx context.Context, req CreateOrderRequest) (*CreateOrderResponse, error) {
	baseURL := "https://core.newebpay.com/MPG/mpg_gateway"
	if g.config.IsTestMode {
		baseURL = "https://ccore.newebpay.com/MPG/mpg_gateway"
	}

	// Prepare trade info
	tradeInfo := map[string]interface{}{
		"MerchantID":      g.config.MerchantID,
		"RespondType":     "JSON",
		"TimeStamp":       time.Now().Unix(),
		"Version":         "2.0",
		"MerchantOrderNo": req.OrderID,
		"Amt":             req.Amount,
		"ItemDesc":        req.Description,
		"Email":           req.CustomerEmail,
		"ReturnURL":       g.config.ReturnURL,
		"NotifyURL":       g.config.NotifyURL,
	}

	// Convert trade info to JSON string and encrypt
	tradeInfoJSON := g.mapToJSONString(tradeInfo)
	encryptedTradeInfo := g.aesEncrypt(tradeInfoJSON, g.config.HashKey, g.config.HashIV)

	// Generate SHA256 hash
	hash := g.generateSHA256(encryptedTradeInfo, g.config.HashKey, g.config.HashIV)

	// Build form data
	formData := map[string]string{
		"MerchantID": g.config.MerchantID,
		"TradeInfo":  encryptedTradeInfo,
		"TradeSha":   hash,
		"Version":    "2.0",
	}

	return &CreateOrderResponse{
		PaymentURL: baseURL,
		OrderID:    req.OrderID,
		ExtraData:  formData,
	}, nil
}

func (g *NewebPayGateway) VerifyCallback(ctx context.Context, data map[string]interface{}) (*CallbackData, error) {
	// NewebPay sends encrypted TradeInfo
	tradeInfoEncrypted, ok := data["TradeInfo"].(string)
	if !ok {
		return nil, fmt.Errorf("missing TradeInfo")
	}

	// Decrypt TradeInfo
	tradeInfoJSON := g.aesDecrypt(tradeInfoEncrypted, g.config.HashKey, g.config.HashIV)
	tradeInfo := g.jsonStringToMap(tradeInfoJSON)

	// Verify TradeSha
	receivedTradeSha := data["TradeSha"].(string)
	expectedTradeSha := g.generateSHA256(tradeInfoEncrypted, g.config.HashKey, g.config.HashIV)
	if receivedTradeSha != expectedTradeSha {
		return nil, fmt.Errorf("invalid TradeSha")
	}

	// Parse payment status
	status := PaymentStatusPending
	resultCode := tradeInfo["Status"].(string)
	if resultCode == "SUCCESS" {
		status = PaymentStatusPaid
	} else if resultCode == "FAIL" {
		status = PaymentStatusFailed
	}

	// Parse amount
	var amount int64
	if amt, ok := tradeInfo["Amt"].(float64); ok {
		amount = int64(amt)
	}

	// Parse paid time
	var paidAt time.Time
	if payTime, ok := tradeInfo["PayTime"].(string); ok && payTime != "" {
		paidAt, _ = time.Parse("2006-01-02 15:04:05", payTime)
	}

	return &CallbackData{
		OrderID:        tradeInfo["MerchantOrderNo"].(string),
		GatewayOrderID: tradeInfo["TradeNo"].(string),
		Status:          status,
		Amount:          amount,
		Currency:       "TWD",
		TransactionID:  tradeInfo["TradeNo"].(string),
		PaidAt:          paidAt,
		RawData:         data,
	}, nil
}

func (g *NewebPayGateway) QueryOrder(ctx context.Context, orderID string) (*OrderStatus, error) {
	// NewebPay query order implementation
	// This would typically make an API call to NewebPay
	return &OrderStatus{
		OrderID: orderID,
		Status:  PaymentStatusPending,
	}, nil
}

func (g *NewebPayGateway) Refund(ctx context.Context, req RefundRequest) (*RefundResponse, error) {
	// NewebPay refund implementation
	// This would typically make an API call to NewebPay
	return &RefundResponse{
		RefundID:      fmt.Sprintf("REFUND_%s_%d", req.OrderID, time.Now().Unix()),
		Status:        "success",
		RefundedAmount: req.Amount,
		RefundedAt:     time.Now(),
	}, nil
}

// aesEncrypt encrypts data using AES-256-CBC
func (g *NewebPayGateway) aesEncrypt(data, key, iv string) string {
	block, _ := aes.NewCipher([]byte(key))
	mode := cipher.NewCBCEncrypter(block, []byte(iv))

	// Pad data to block size
	padded := g.pkcs7Pad([]byte(data), block.BlockSize())

	encrypted := make([]byte, len(padded))
	mode.CryptBlocks(encrypted, padded)

	return hex.EncodeToString(encrypted)
}

// aesDecrypt decrypts data using AES-256-CBC
func (g *NewebPayGateway) aesDecrypt(encrypted, key, iv string) string {
	data, _ := hex.DecodeString(encrypted)

	block, _ := aes.NewCipher([]byte(key))
	mode := cipher.NewCBCDecrypter(block, []byte(iv))

	decrypted := make([]byte, len(data))
	mode.CryptBlocks(decrypted, data)

	// Remove padding
	unpadded := g.pkcs7Unpad(decrypted)

	return string(unpadded)
}

// generateSHA256 generates SHA256 hash
func (g *NewebPayGateway) generateSHA256(tradeInfo, key, iv string) string {
	hash := sha256.New()
	hash.Write([]byte(fmt.Sprintf("HashKey=%s&%s&HashIV=%s", key, tradeInfo, iv)))
	return strings.ToUpper(hex.EncodeToString(hash.Sum(nil)))
}

// pkcs7Pad adds PKCS7 padding
func (g *NewebPayGateway) pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	padtext := make([]byte, padding)
	for i := range padtext {
		padtext[i] = byte(padding)
	}
	return append(data, padtext...)
}

// pkcs7Unpad removes PKCS7 padding
func (g *NewebPayGateway) pkcs7Unpad(data []byte) []byte {
	length := len(data)
	unpadding := int(data[length-1])
	return data[:(length - unpadding)]
}

// Helper functions for JSON conversion
func (g *NewebPayGateway) mapToJSONString(m map[string]interface{}) string {
	data, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func (g *NewebPayGateway) jsonStringToMap(jsonStr string) map[string]interface{} {
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return make(map[string]interface{})
	}
	return result
}

