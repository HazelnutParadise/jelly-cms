package payment

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/notify"
)

var (
	ErrCallbackNotFound = errors.New("payment: callback attempt not found")
	ErrCallbackMismatch = errors.New("payment: callback does not match the payment attempt")
)

// VerifiedCallback is a provider notification after its signature and amount
// check. Only a gateway's VerifyCallback may build one; Finalize trusts it.
type VerifiedCallback struct {
	// Provider is the gateway id (ecpay, sandbox, manual_transfer).
	Provider string
	// MerchantTradeNo is the 20-character number that identifies the payment.
	MerchantTradeNo string
	// ProviderTradeNo is the provider's own transaction number.
	ProviderTradeNo string
	// Method is the payment method the customer used.
	Method string
	// Status is the status the provider reports.
	Status PaymentStatus
	// Amount and Currency are checked against the payment row before anything changes.
	Amount   int64
	Currency string
	PaidAt   *time.Time
	// DedupeKey identifies one notification. A repeat with the same key is
	// recorded in payment_events once and applied once.
	DedupeKey string
	// Payload is the raw callback data, stored for audit.
	Payload map[string]interface{}
}

// Finalize applies one verified notification inside the caller's transaction.
// Lock order is always order first, then payment, so concurrent callbacks for
// one order cannot deadlock by acquiring these rows in opposite directions.
func Finalize(tx *gorm.DB, provider string, cb VerifiedCallback) error {
	if tx == nil {
		return errors.New("payment: nil transaction")
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" || cb.Provider != provider || cb.MerchantTradeNo == "" || cb.DedupeKey == "" || len(cb.DedupeKey) > 200 {
		return errors.New("payment: callback identity is invalid")
	}
	if cb.Status != PaymentStatusPending && cb.Status != PaymentStatusPaid && cb.Status != PaymentStatusFailed {
		return errors.New("payment: callback status is invalid")
	}
	if cb.Amount < 0 || cb.Currency == "" {
		return errors.New("payment: callback amount or currency is invalid")
	}

	var reference core.Payment
	if err := tx.Select("id", "order_id").Where("merchant_trade_no = ? AND provider = ?", cb.MerchantTradeNo, provider).First(&reference).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrCallbackNotFound
		}
		return fmt.Errorf("payment: find payment attempt: %w", err)
	}
	var order core.Order
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, reference.OrderID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrCallbackNotFound
		}
		return fmt.Errorf("payment: find order: %w", err)
	}
	var attempt core.Payment
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&attempt, reference.ID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrCallbackNotFound
		}
		return fmt.Errorf("payment: reload payment attempt: %w", err)
	}
	if attempt.Provider != provider || attempt.MerchantTradeNo != cb.MerchantTradeNo || order.ID != attempt.OrderID {
		return ErrCallbackMismatch
	}
	if attempt.Amount != cb.Amount || attempt.Currency != cb.Currency || order.TotalAmount != attempt.Amount || order.Currency != attempt.Currency {
		return fmt.Errorf("%w: amount or currency mismatch", ErrCallbackMismatch)
	}
	if cb.Method != "" && cb.Method != attempt.Method {
		return fmt.Errorf("%w: method mismatch", ErrCallbackMismatch)
	}
	if !attemptSupportsMethod(provider, attempt.Method) {
		return fmt.Errorf("%w: provider does not support the payment method", ErrCallbackMismatch)
	}

	payload, err := safePaymentPayload(cb.Payload)
	if err != nil {
		return fmt.Errorf("payment: sanitize callback payload: %w", err)
	}
	event := core.PaymentEvent{Provider: provider, DedupeKey: cb.DedupeKey, Verified: true, Payload: payload}
	result := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "provider"}, {Name: "dedupe_key"}},
		DoNothing: true,
	}).Create(&event)
	if result.Error != nil {
		return fmt.Errorf("payment: record verified event: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		var existing core.PaymentEvent
		if err := tx.Where("provider = ? AND dedupe_key = ?", provider, cb.DedupeKey).First(&existing).Error; err != nil {
			return fmt.Errorf("payment: verify duplicate event identity: %w", err)
		}
		if !existing.Verified || !samePaymentPayload(existing.Payload, payload) {
			return fmt.Errorf("%w: dedupe key was reused for a different callback", ErrCallbackMismatch)
		}
		return nil
	}

	previousStatus := attempt.Status
	previouslyPaid := previousStatus == string(PaymentStatusPaid) || previousStatus == "refunded"
	updates := map[string]any{}
	if offlineInfo := ecpayOfflinePaymentInfo(provider, attempt.Method, cb.Payload); len(offlineInfo) > 0 {
		updates["masked_info"] = offlineInfo
	}
	if cb.ProviderTradeNo != "" && attempt.ProviderTradeNo == "" {
		updates["provider_trade_no"] = cb.ProviderTradeNo
	}
	switch cb.Status {
	case PaymentStatusPaid:
		if previousStatus != "refunded" {
			updates["status"] = string(PaymentStatusPaid)
			if previousStatus != string(PaymentStatusPaid) {
				paidAt := time.Now().UTC()
				if cb.PaidAt != nil && !cb.PaidAt.IsZero() {
					paidAt = cb.PaidAt.UTC()
				}
				updates["paid_at"] = paidAt
			}
		}
	case PaymentStatusFailed:
		if !previouslyPaid {
			updates["status"] = string(PaymentStatusFailed)
		}
	case PaymentStatusPending:
		// A delayed pending notice cannot undo a failure or a successful payment.
		if previousStatus == "" || previousStatus == string(PaymentStatusPending) {
			updates["status"] = string(PaymentStatusPending)
		}
	}
	if len(updates) > 0 {
		if err := tx.Model(&core.Payment{}).Where("id = ?", attempt.ID).Updates(updates).Error; err != nil {
			return fmt.Errorf("payment: update attempt: %w", err)
		}
	}

	ctx := tx.Statement.Context
	if ctx == nil {
		ctx = context.Background()
	}
	switch cb.Status {
	case PaymentStatusPaid:
		if previousStatus == "refunded" {
			break
		}
		if order.Status == core.OrderStatusPending {
			if err := core.NewOrderService(tx).Transition(tx, order.ID, core.OrderStatusPending, core.OrderStatusPaid, nil, "Payment confirmed"); err != nil {
				return fmt.Errorf("payment: transition order to paid: %w", err)
			}
			if err := incrementSoldCount(tx, order.ID); err != nil {
				return fmt.Errorf("payment: update product sales: %w", err)
			}
			if err := emitShopEvent(ctx, tx, notify.KindOrderNew, order, "payment-confirmed", "Payment confirmed"); err != nil {
				return err
			}
			accessURL, err := newPaidOrderLookupURL(tx, order)
			if err != nil {
				return err
			}
			if err := notify.Emit(ctx, tx, notify.Event{
				Kind: notify.KindOrderConfirmation, Category: notify.CategoryOrders,
				Params:     map[string]any{"number": order.Number, "total": order.TotalAmount, "currency": order.Currency, "payment_status": "paid", "access_url": accessURL},
				GroupKey:   "order:" + order.Number + ":paid-confirmation",
				Recipients: notify.Recipients{Emails: []string{order.Email}},
			}); err != nil {
				return fmt.Errorf("payment: queue paid order confirmation: %w", err)
			}
			return nil
		}
		// A captured payment arriving after cancellation/expiry or after a
		// different attempt paid must be surfaced for a manual refund. It never
		// revives a terminal order or changes its stock reservation.
		if !previouslyPaid {
			message := "Late payment captured; refund review required"
			if err := tx.Create(&core.OrderEvent{
				OrderID: order.ID, At: time.Now().UTC(), Kind: "payment.needs_refund",
				FromStatus: order.Status, ToStatus: order.Status, Message: message,
			}).Error; err != nil {
				return fmt.Errorf("payment: record refund review: %w", err)
			}
			if err := emitShopEvent(ctx, tx, notify.KindOrderPaymentFailed, order, "late-paid-needs-refund", message); err != nil {
				return err
			}
		}
	case PaymentStatusFailed:
		if !previouslyPaid && previousStatus != string(PaymentStatusFailed) && order.Status == core.OrderStatusPending {
			var earlierFailures int64
			if err := tx.Model(&core.Payment{}).Where("order_id = ? AND id <> ? AND status = ?", order.ID, attempt.ID, string(PaymentStatusFailed)).Count(&earlierFailures).Error; err != nil {
				return fmt.Errorf("payment: check prior failed attempts: %w", err)
			}
			if earlierFailures == 0 {
				hold, err := failedOrderHoldDuration(tx)
				if err != nil {
					return err
				}
				if err := tx.Model(&core.Order{}).Where("id = ? AND status = ?", order.ID, core.OrderStatusPending).Update("expires_at", time.Now().UTC().Add(hold)).Error; err != nil {
					return fmt.Errorf("payment: extend failed order hold: %w", err)
				}
			}
			if err := emitShopEvent(ctx, tx, notify.KindOrderPaymentFailed, order, "payment-failed:"+fmt.Sprint(attempt.ID), "Payment attempt failed; order remains retryable"); err != nil {
				return err
			}
		}
	}
	return nil
}

func samePaymentPayload(existing, incoming json.RawMessage) bool {
	var existingValue, incomingValue any
	if json.Unmarshal(existing, &existingValue) != nil || json.Unmarshal(incoming, &incomingValue) != nil {
		return false
	}
	existingJSON, err := json.Marshal(existingValue)
	if err != nil {
		return false
	}
	incomingJSON, err := json.Marshal(incomingValue)
	return err == nil && bytes.Equal(existingJSON, incomingJSON)
}

// ecpayOfflinePaymentInfo keeps only the fields needed to complete a pending
// ATM/CVS payment. It deliberately avoids persisting the entire provider
// callback as customer-facing payment instructions.
func ecpayOfflinePaymentInfo(provider, method string, payload map[string]interface{}) json.RawMessage {
	if provider != "ecpay" || (method != "atm" && method != "cvs") || len(payload) == 0 {
		return nil
	}
	read := func(name string) string {
		for key, value := range payload {
			if strings.EqualFold(key, name) {
				text, ok := value.(string)
				if !ok {
					return ""
				}
				return strings.TrimSpace(text)
			}
		}
		return ""
	}
	info := map[string]string{}
	if method == "atm" {
		for output, input := range map[string]string{"bank_code": "BankCode", "account_number": "vAccount", "expires_at": "ExpireDate"} {
			if value := read(input); value != "" {
				info[output] = value
			}
		}
	} else {
		for output, input := range map[string]string{"payment_number": "PaymentNo", "barcode_1": "Barcode1", "barcode_2": "Barcode2", "barcode_3": "Barcode3", "expires_at": "ExpireDate"} {
			if value := read(input); value != "" {
				info[output] = value
			}
		}
	}
	if len(info) == 0 {
		return nil
	}
	encoded, err := json.Marshal(info)
	if err != nil {
		return nil
	}
	return encoded
}

func newPaidOrderLookupURL(tx *gorm.DB, order core.Order) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("payment: generate paid order link: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	hash := sha256.Sum256([]byte(token))
	now := time.Now().UTC()
	if err := tx.Create(&core.OrderAccessToken{
		OrderID: order.ID, TokenHash: hex.EncodeToString(hash[:]), Kind: "lookup",
		ExpiresAt: now.Add(15 * time.Minute), CreatedAt: now,
	}).Error; err != nil {
		return "", fmt.Errorf("payment: save paid order link: %w", err)
	}
	return "/orders/view?t=" + url.QueryEscape(token), nil
}

func failedOrderHoldDuration(tx *gorm.DB) (time.Duration, error) {
	const defaultHours int64 = 24
	var option core.Option
	err := tx.Where("key = ?", "shop_failed_order_hold_hours").First(&option).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, fmt.Errorf("payment: read failed order hold option: %w", err)
	}
	hours := defaultHours
	if err == nil && strings.TrimSpace(option.Value) != "" {
		parsed, parseErr := strconv.ParseInt(strings.TrimSpace(option.Value), 10, 64)
		if parseErr != nil || parsed < 1 || parsed > math.MaxInt64/int64(time.Hour) {
			return 0, errors.New("payment: shop_failed_order_hold_hours must be a positive integer")
		}
		hours = parsed
	}
	return time.Duration(hours) * time.Hour, nil
}

func emitShopEvent(ctx context.Context, tx *gorm.DB, kind string, order core.Order, groupKey, reason string) error {
	return notify.Emit(ctx, tx, notify.Event{
		Kind: kind, Category: notify.CategoryOrders,
		Params: map[string]any{"order": order.Number, "reason": reason}, GroupKey: groupKey,
		Recipients: notify.Recipients{Roles: []string{"admin"}},
	})
}

func incrementSoldCount(tx *gorm.DB, orderID uint) error {
	var items []core.OrderItem
	if err := tx.Select("product_id", "quantity").Where("order_id = ?", orderID).Find(&items).Error; err != nil {
		return err
	}
	for _, item := range items {
		if item.ProductID == nil || item.Quantity <= 0 {
			continue
		}
		if err := tx.Model(&core.Product{}).Where("id = ?", *item.ProductID).Update("sold_count", gorm.Expr("sold_count + ?", item.Quantity)).Error; err != nil {
			return err
		}
	}
	return nil
}

func safePaymentPayload(input map[string]interface{}) (json.RawMessage, error) {
	cleaned := scrubPaymentPayload(input)
	if cleaned == nil {
		cleaned = map[string]any{}
	}
	encoded, err := json.Marshal(cleaned)
	if err != nil {
		return nil, err
	}
	if len(encoded) > 64*1024 {
		return nil, errors.New("callback payload is too large")
	}
	return encoded, nil
}

func scrubPaymentPayload(value any) any {
	switch typed := value.(type) {
	case map[string]interface{}:
		cleaned := make(map[string]interface{}, len(typed))
		for key, child := range typed {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "password") || strings.Contains(lower, "authorization") || strings.Contains(lower, "signature") || lower == "checkmacvalue" || lower == "tradesha" {
				continue
			}
			cleaned[key] = scrubPaymentPayload(child)
		}
		return cleaned
	case []interface{}:
		cleaned := make([]interface{}, len(typed))
		for index, child := range typed {
			cleaned[index] = scrubPaymentPayload(child)
		}
		return cleaned
	default:
		return value
	}
}

func stablePaymentDedupeKey(provider, tradeNo string, status PaymentStatus, providerTradeNo string) string {
	value := provider + "|" + tradeNo + "|" + string(status) + "|" + providerTradeNo
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}
