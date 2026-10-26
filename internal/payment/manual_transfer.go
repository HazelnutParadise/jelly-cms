package payment

import "context"

// ManualTransferGateway is the offline bank-transfer provider (ticket 07). The
// customer pays by bank transfer and an administrator confirms the money with
// mark-paid. Instructions come from the option shop_manual_transfer_note.
type ManualTransferGateway struct {
	instructions string
}

var _ PaymentGateway = (*ManualTransferGateway)(nil)

// NewManualTransferGateway returns the gateway that shows instructions as the
// payment text.
func NewManualTransferGateway(instructions string) *ManualTransferGateway {
	return &ManualTransferGateway{instructions: instructions}
}

// GetName returns "manual_transfer".
func (g *ManualTransferGateway) GetName() string { return "manual_transfer" }

// Capabilities reports bank transfer as an offline method.
func (g *ManualTransferGateway) Capabilities() Capabilities {
	return Capabilities{Methods: []string{"bank_transfer"}, Offline: true}
}

// CreateOrder returns the configured bank-transfer instructions in ExtraData.
func (g *ManualTransferGateway) CreateOrder(ctx context.Context, req CreateOrderRequest) (*CreateOrderResponse, error) {
	if g == nil {
		return nil, ErrNotSupported
	}
	return &CreateOrderResponse{
		OrderID:   req.OrderID,
		ExtraData: map[string]string{"instructions": g.instructions},
	}, nil
}

// VerifyCallback is not applicable: there is no provider notification. The
// administrator confirms the payment with mark-paid.
func (g *ManualTransferGateway) VerifyCallback(ctx context.Context, data map[string]interface{}) (*CallbackData, error) {
	return nil, ErrNotSupported
}

// QueryOrder is not supported for offline payments.
func (g *ManualTransferGateway) QueryOrder(ctx context.Context, orderID string) (*OrderStatus, error) {
	return nil, ErrNotSupported
}

// Refund is not supported for offline payments; refunds are recorded by hand.
func (g *ManualTransferGateway) Refund(ctx context.Context, req RefundRequest) (*RefundResponse, error) {
	return nil, ErrNotSupported
}
