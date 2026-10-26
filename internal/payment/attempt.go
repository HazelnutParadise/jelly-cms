package payment

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const tradeAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// CreateAttempt inserts one pending payment attempt in the caller's checkout
// transaction and returns a same-origin handoff URL. Provider APIs are never
// called from checkout; the ECPay form is prepared by the local handoff page.
func CreateAttempt(tx *gorm.DB, order *core.Order, method, provider, baseURL, accessToken string) (core.Payment, string, string, error) {
	var zero core.Payment
	if tx == nil || order == nil || order.ID == 0 {
		return zero, "", "", errors.New("payment: checkout transaction and persisted order are required")
	}
	method = strings.TrimSpace(method)
	provider = strings.TrimSpace(strings.ToLower(provider))
	if baseURL == "" {
		return zero, "", "", errors.New("payment: base URL is required")
	}
	parsedBase, err := url.Parse(baseURL)
	if err != nil || parsedBase.Scheme == "" || parsedBase.Host == "" || parsedBase.User != nil || parsedBase.RawQuery != "" || parsedBase.Fragment != "" {
		return zero, "", "", errors.New("payment: invalid base URL")
	}
	if parsedBase.Scheme != "http" && parsedBase.Scheme != "https" {
		return zero, "", "", errors.New("payment: invalid base URL scheme")
	}
	baseURL = strings.TrimRight(baseURL, "/")

	var stored core.Order
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&stored, order.ID).Error; err != nil {
		return zero, "", "", err
	}
	if stored.Status != core.OrderStatusPending {
		return zero, "", "", errors.New("payment: order is not payable")
	}
	if stored.TotalAmount < 0 || stored.Currency == "" {
		return zero, "", "", errors.New("payment: order amount or currency is invalid")
	}
	var previous core.Payment
	err = tx.Where("order_id = ?", stored.ID).Order("id DESC").First(&previous).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return zero, "", "", err
	}
	if err == nil && previous.Status != "failed" && previous.Status != "expired" {
		return zero, "", "", errors.New("payment: an active or completed payment attempt already exists")
	}
	if !attemptSupportsMethod(provider, method) {
		return zero, "", "", errors.New("payment: provider does not support the selected method")
	}

	service := NewService(tx)
	service.appURL = baseURL
	gateway, err := service.GetGateway(provider)
	if err != nil {
		return zero, "", "", err
	}
	if !gatewaySupportsMethod(gateway, method) {
		return zero, "", "", errors.New("payment: provider does not support the selected method")
	}

	expiresAt := time.Time{}
	if stored.ExpiresAt != nil {
		expiresAt = stored.ExpiresAt.UTC()
	} else {
		duration, err := holdDuration(method)
		if err != nil {
			return zero, "", "", err
		}
		expiresAt = time.Now().UTC().Add(duration)
	}
	tradeNo, err := newMerchantTradeNo()
	if err != nil {
		return zero, "", "", err
	}
	var items []core.OrderItem
	if err := tx.Where("order_id = ?", stored.ID).Order("id").Find(&items).Error; err != nil {
		return zero, "", "", err
	}
	providerItems := make([]OrderItem, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item.Title)
		if item.VariantName != "" {
			name += " " + strings.TrimSpace(item.VariantName)
		}
		providerItems = append(providerItems, OrderItem{Name: name, Quantity: item.Quantity, Price: item.UnitPrice})
	}
	request := CreateOrderRequest{
		OrderID: tradeNo, Amount: stored.TotalAmount, Currency: stored.Currency,
		Method: method, AccessToken: accessToken,
		Description: "Order " + stored.Number, CustomerEmail: stored.Email,
		NotifyURL:  baseURL + "/api/payment/" + url.PathEscape(provider) + "/notify",
		ReturnURL:  baseURL + "/api/payment/" + url.PathEscape(provider) + "/return",
		ExpireTime: expiresAt, Items: providerItems,
	}
	response, err := gateway.CreateOrder(tx.Statement.Context, request)
	if err != nil {
		return zero, "", "", err
	}
	if response == nil {
		return zero, "", "", errors.New("payment: provider returned no handoff")
	}

	payment := core.Payment{
		OrderID: stored.ID, Provider: provider, Method: method,
		MerchantTradeNo: tradeNo, Amount: stored.TotalAmount,
		Currency: stored.Currency, Status: "pending",
	}
	if err := tx.Create(&payment).Error; err != nil {
		return zero, "", "", err
	}
	if stored.ExpiresAt == nil {
		if err := tx.Model(&core.Order{}).Where("id = ? AND status = ?", stored.ID, core.OrderStatusPending).Update("expires_at", expiresAt).Error; err != nil {
			return zero, "", "", err
		}
	}

	redirectURL := ""
	instructions := ""
	switch provider {
	case "ecpay":
		redirectURL = baseURL + "/checkout/pay/" + tradeNo
		if accessToken != "" {
			redirectURL += "?t=" + url.QueryEscape(accessToken)
		}
	case "sandbox":
		redirectURL = response.PaymentURL
	case "manual_transfer":
		redirectURL = baseURL + "/checkout/done/" + url.PathEscape(stored.Number)
		if accessToken != "" {
			redirectURL += "?t=" + url.QueryEscape(accessToken)
		}
		instructions = response.ExtraData["instructions"]
	default:
		return zero, "", "", errors.New("payment: unsupported checkout provider")
	}
	return payment, redirectURL, instructions, nil
}

func attemptSupportsMethod(provider, method string) bool {
	switch provider {
	case "ecpay", "sandbox":
		return method == "credit_card" || method == "atm" || method == "cvs"
	case "manual_transfer":
		return method == "bank_transfer"
	default:
		return false
	}
}

func gatewaySupportsMethod(gateway PaymentGateway, method string) bool {
	for _, supported := range gateway.Capabilities().Methods {
		if supported == method {
			return true
		}
	}
	return false
}

func holdDuration(method string) (time.Duration, error) {
	switch method {
	case "credit_card":
		return 30 * time.Minute, nil
	case "atm", "cvs", "bank_transfer":
		return 72 * time.Hour, nil
	default:
		return 0, errors.New("payment: unsupported method")
	}
}

func newMerchantTradeNo() (string, error) {
	result := make([]byte, 20)
	limit := byte(256 - (256 % len(tradeAlphabet)))
	for i := range result {
		for {
			var sample [1]byte
			if _, err := rand.Read(sample[:]); err != nil {
				return "", fmt.Errorf("payment: generate trade number: %w", err)
			}
			if sample[0] < limit {
				result[i] = tradeAlphabet[int(sample[0])%len(tradeAlphabet)]
				break
			}
		}
	}
	return string(result), nil
}
