package payment

import (
	"context"
	"time"
)

// PaymentGateway defines the interface for payment gateways
type PaymentGateway interface {
	// CreateOrder creates a payment order and returns payment URL
	CreateOrder(ctx context.Context, req CreateOrderRequest) (*CreateOrderResponse, error)

	// VerifyCallback verifies the payment callback from the gateway
	VerifyCallback(ctx context.Context, data map[string]interface{}) (*CallbackData, error)

	// QueryOrder queries the order status from the gateway
	QueryOrder(ctx context.Context, orderID string) (*OrderStatus, error)

	// Refund processes a refund
	Refund(ctx context.Context, req RefundRequest) (*RefundResponse, error)

	// GetName returns the gateway name
	GetName() string
}

// CreateOrderRequest contains information needed to create a payment order
type CreateOrderRequest struct {
	OrderID      string    // Internal order ID
	Amount       int64     // Amount in smallest currency unit (e.g., cents)
	Currency     string    // Currency code (e.g., "TWD", "USD")
	Description  string    // Order description
	CustomerName string    // Customer name
	CustomerEmail string   // Customer email
	ReturnURL    string    // Return URL after payment
	NotifyURL    string    // Callback URL for payment notification
	ExpireTime   time.Time // Order expiration time
	Items        []OrderItem
}

// OrderItem represents an item in the payment order
type OrderItem struct {
	Name     string
	Quantity int
	Price    int64
}

// CreateOrderResponse contains the response from creating an order
type CreateOrderResponse struct {
	PaymentURL string            // URL to redirect user for payment
	OrderID    string            // Gateway's order ID
	ExtraData  map[string]string // Additional data from gateway
}

// CallbackData contains data from payment gateway callback
type CallbackData struct {
	OrderID       string
	GatewayOrderID string
	Status        PaymentStatus
	Amount        int64
	Currency      string
	TransactionID string
	PaidAt        time.Time
	RawData       map[string]interface{}
}

// PaymentStatus represents the payment status
type PaymentStatus string

const (
	PaymentStatusPending   PaymentStatus = "pending"
	PaymentStatusPaid      PaymentStatus = "paid"
	PaymentStatusFailed    PaymentStatus = "failed"
	PaymentStatusCancelled PaymentStatus = "cancelled"
	PaymentStatusRefunded   PaymentStatus = "refunded"
)

// OrderStatus represents the current status of an order
type OrderStatus struct {
	OrderID       string
	Status        PaymentStatus
	Amount        int64
	PaidAt        *time.Time
	TransactionID string
}

// RefundRequest contains information for refund
type RefundRequest struct {
	OrderID       string
	Amount        int64  // If 0, refund full amount
	Reason        string // Refund reason
	TransactionID string
}

// RefundResponse contains the response from refund
type RefundResponse struct {
	RefundID      string
	Status        string
	RefundedAmount int64
	RefundedAt    time.Time
}

// GatewayConfig contains configuration for payment gateways
type GatewayConfig struct {
	ECPay   *ECPayConfig
	NewebPay *NewebPayConfig
}

// ECPayConfig contains ECPay (綠界) configuration
type ECPayConfig struct {
	MerchantID  string
	HashKey     string
	HashIV      string
	IsTestMode  bool
	ReturnURL   string
	NotifyURL   string
}

// NewebPayConfig contains NewebPay (藍新) configuration
type NewebPayConfig struct {
	MerchantID  string
	HashKey     string
	HashIV      string
	IsTestMode  bool
	ReturnURL   string
	NotifyURL   string
}

