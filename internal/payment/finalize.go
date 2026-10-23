package payment

import (
	"time"

	"gorm.io/gorm"
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

// Finalize applies a verified notification inside tx. It locks the payment row
// by merchant_trade_no, compares amount and currency, records the notification
// in payment_events (deduplicated on provider and dedupe key), and then moves
// the order through the state machine. A mismatch changes nothing.
//
// Skeleton: not implemented yet (ticket 07, E6).
func Finalize(tx *gorm.DB, provider string, cb VerifiedCallback) error {
	return ErrNotImplemented
}
