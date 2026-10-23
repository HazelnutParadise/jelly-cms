package payment

import "context"

// SandboxGateway is the test provider for non-production environments (ticket 07).
// It is only registered when the option payment_sandbox_enabled is true and
// APP_ENV is not production. Its callbacks are signed with a per-site key using
// HMAC-SHA256, so a sandbox notification never passes as a real one.
//
// Skeleton: CreateOrder and VerifyCallback are not implemented yet.
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
// Not implemented yet.
func (g *SandboxGateway) CreateOrder(ctx context.Context, req CreateOrderRequest) (*CreateOrderResponse, error) {
	return nil, ErrNotImplemented
}

// VerifyCallback checks the HMAC-SHA256 signature and returns the verified
// notification. Not implemented yet.
func (g *SandboxGateway) VerifyCallback(ctx context.Context, data map[string]interface{}) (*CallbackData, error) {
	return nil, ErrNotImplemented
}

// QueryOrder is not supported by the sandbox.
func (g *SandboxGateway) QueryOrder(ctx context.Context, orderID string) (*OrderStatus, error) {
	return nil, ErrNotSupported
}

// Refund is not supported by the sandbox.
func (g *SandboxGateway) Refund(ctx context.Context, req RefundRequest) (*RefundResponse, error) {
	return nil, ErrNotSupported
}
