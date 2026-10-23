package server

import (
	"net/http"
	"strconv"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/payment"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

var paymentService *payment.Service

func InitPaymentService(db *gorm.DB) {
	paymentService = payment.NewService(db)
}

// RegisterPaymentRoutes registers payment-related routes
func RegisterPaymentRoutes(e *echo.Echo) {
	// Public payment routes (no auth required). Providers notify on POST
	// /notify (ticket 07). /callback is kept as a POST-only alias so existing
	// provider settings keep working.
	e.POST("/api/payment/:gateway/notify", HandlePaymentCallback)
	e.POST("/api/payment/:gateway/callback", HandlePaymentCallback)
	e.GET("/api/payment/:gateway/return", HandlePaymentReturn)

	// Protected payment routes
	api := e.Group("/api/payment", auth.RequireAuth)
	api.POST("/create", HandleCreatePayment)
	api.GET("/order/:id/status", HandleGetOrderStatus)
}

// HandleCreatePayment creates a payment order
func HandleCreatePayment(c echo.Context) error {
	var req struct {
		OrderID       string              `json:"order_id"`
		Gateway       string              `json:"gateway"`
		Amount        int64               `json:"amount"`
		Currency      string              `json:"currency"`
		Description   string              `json:"description"`
		CustomerName  string              `json:"customer_name"`
		CustomerEmail string              `json:"customer_email"`
		Items         []payment.OrderItem `json:"items"`
	}

	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}

	// Validate order exists
	var order core.Order
	if err := data.DB.Preload("Items").Preload("Items.Product").First(&order, "id = ?", req.OrderID).Error; err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Order not found"})
	}

	// Build payment request
	paymentReq := payment.CreateOrderRequest{
		OrderID:       req.OrderID,
		Amount:        req.Amount,
		Currency:      req.Currency,
		Description:   req.Description,
		CustomerName:  req.CustomerName,
		CustomerEmail: req.CustomerEmail,
		ReturnURL:     c.Scheme() + "://" + c.Request().Host + "/api/payment/" + req.Gateway + "/return",
		NotifyURL:     c.Scheme() + "://" + c.Request().Host + "/api/payment/" + req.Gateway + "/callback",
		Items:         req.Items,
	}

	// Create payment
	resp, err := paymentService.CreatePayment(c.Request().Context(), req.Gateway, paymentReq)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, resp)
}

// HandlePaymentCallback handles payment gateway notifications. Only POST is
// accepted: a notification that changes an order must not be triggered by a
// GET link or a browser prefetch.
func HandlePaymentCallback(c echo.Context) error {
	if c.Request().Method != http.MethodPost {
		return c.NoContent(http.StatusMethodNotAllowed)
	}
	gatewayName := c.Param("gateway")

	// Get callback data (form or JSON)
	var callbackData map[string]interface{}
	if err := c.Bind(&callbackData); err != nil {
		// Try form data
		form, _ := c.FormParams()
		callbackData = make(map[string]interface{})
		for k, v := range form {
			if len(v) > 0 {
				callbackData[k] = v[0]
			}
		}
	}

	// Verify callback
	if paymentService == nil {
		paymentService = payment.NewService(data.DB)
	}
	callback, err := paymentService.HandleCallback(c.Request().Context(), gatewayName, callbackData)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	// Update order status
	var order core.Order
	if err := data.DB.First(&order, "id = ?", callback.OrderID).Error; err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Order not found"})
	}

	// Update order status based on payment status
	if callback.Status == payment.PaymentStatusPaid {
		order.Status = "paid"
		order.PaymentMethod = gatewayName
		data.DB.Save(&order)
	} else if callback.Status == payment.PaymentStatusFailed {
		order.Status = "cancelled"
		data.DB.Save(&order)
	}

	// Return response based on gateway
	if gatewayName == "ecpay" {
		return c.String(http.StatusOK, "1|OK")
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "success"})
}

// HandlePaymentReturn handles payment return URL
func HandlePaymentReturn(c echo.Context) error {
	gatewayName := c.Param("gateway")

	// Get return data
	returnData := make(map[string]interface{})
	for k, v := range c.QueryParams() {
		if len(v) > 0 {
			returnData[k] = v[0]
		}
	}

	// Verify and process return
	if paymentService == nil {
		paymentService = payment.NewService(data.DB)
	}
	callback, err := paymentService.HandleCallback(c.Request().Context(), gatewayName, returnData)
	if err != nil {
		return c.Redirect(http.StatusFound, "/?payment=error")
	}

	// Redirect to order page or success page
	return c.Redirect(http.StatusFound, "/orders/"+callback.OrderID+"?payment=success")
}

// HandleGetOrderStatus gets the payment status of an order
func HandleGetOrderStatus(c echo.Context) error {
	orderID := c.Param("id")

	// Get order
	var order core.Order
	if err := data.DB.First(&order, "id = ?", orderID).Error; err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Order not found"})
	}

	// Query gateway for latest status
	gatewayName := order.PaymentMethod
	if gatewayName == "" {
		gatewayName = "ecpay" // Default
	}

	if paymentService == nil {
		paymentService = payment.NewService(data.DB)
	}
	gateway, err := paymentService.GetGateway(gatewayName)
	if err != nil {
		return c.JSON(http.StatusOK, map[string]interface{}{
			"order_id": orderID,
			"status":   order.Status,
		})
	}

	status, err := gateway.QueryOrder(c.Request().Context(), strconv.Itoa(int(order.ID)))
	if err != nil {
		return c.JSON(http.StatusOK, map[string]interface{}{
			"order_id": orderID,
			"status":   order.Status,
		})
	}

	return c.JSON(http.StatusOK, status)
}
