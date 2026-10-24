package server

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/payment"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

var (
	paymentService   *payment.Service
	paymentServiceMu sync.Mutex
)

func InitPaymentService(db *gorm.DB) {
	paymentServiceMu.Lock()
	paymentService = payment.NewService(db)
	paymentServiceMu.Unlock()
}

// RegisterPaymentRoutes registers provider callbacks and the legacy payment
// routes. Callback state changes are POST-only and committed with Finalize.
func RegisterPaymentRoutes(e *echo.Echo) {
	e.POST("/api/payment/:gateway/notify", HandlePaymentCallback)
	e.POST("/api/payment/:gateway/callback", HandlePaymentCallback)
	e.GET("/api/payment/:gateway/return", HandlePaymentReturn)

	api := e.Group("/api/payment", auth.RequireAuth)
	api.POST("/create", auth.ProtectBrowserWrites(HandleCreatePayment))
	api.GET("/order/:id/status", HandleGetOrderStatus)
}

// HandleCreatePayment keeps the old route for compatibility, but ignores all
// client-supplied prices and snapshots. It is limited to the actual order owner.
func HandleCreatePayment(c echo.Context) error {
	var req struct {
		OrderID string `json:"order_id"`
		Gateway string `json:"gateway"`
		Method  string `json:"method"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}
	if data.DB == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	user, ok := auth.GetUserFromContext(c)
	if !ok || user == nil {
		return c.NoContent(http.StatusNotFound)
	}
	var order core.Order
	query := data.DB.WithContext(c.Request().Context()).Preload("Items").Where("number = ?", req.OrderID).First(&order)
	if errors.Is(query.Error, gorm.ErrRecordNotFound) {
		if id, err := strconv.ParseUint(req.OrderID, 10, 64); err == nil && id > 0 {
			query = data.DB.WithContext(c.Request().Context()).Preload("Items").First(&order, uint(id))
		}
	}
	if query.Error != nil || order.UserID == nil || *order.UserID != user.ID {
		return c.NoContent(http.StatusNotFound)
	}
	if req.Gateway == "" || req.Method == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Payment method is required"})
	}
	baseURL, err := paymentRequestBaseURL(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid application URL"})
	}
	var attempt core.Payment
	var redirectURL, instructions string
	if err := data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		var createErr error
		attempt, redirectURL, instructions, createErr = payment.CreateAttempt(tx, &order, req.Method, req.Gateway, baseURL, "")
		return createErr
	}); err != nil {
		return c.JSON(http.StatusConflict, map[string]string{"error": "Payment attempt cannot be created"})
	}
	return c.JSON(http.StatusOK, map[string]interface{}{
		"payment":      map[string]interface{}{"provider": attempt.Provider, "method": attempt.Method, "status": attempt.Status},
		"redirect_url": redirectURL,
		"instructions": instructions,
	})
}

// HandlePaymentCallback verifies a provider POST and atomically records its
// event, payment status, order transition, and staff notification.
func HandlePaymentCallback(c echo.Context) error {
	if c.Request().Method != http.MethodPost {
		return c.NoContent(http.StatusMethodNotAllowed)
	}
	if data.DB == nil {
		return c.NoContent(http.StatusServiceUnavailable)
	}
	gatewayName := strings.ToLower(strings.TrimSpace(c.Param("gateway")))
	callbackData, err := paymentCallbackFields(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid notification"})
	}
	service := payment.NewService(data.DB.WithContext(c.Request().Context()))
	callback, err := service.HandleCallback(c.Request().Context(), gatewayName, callbackData)
	if err != nil || callback == nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid notification"})
	}
	tradeNo := callback.MerchantTradeNo
	if tradeNo == "" {
		tradeNo = callback.OrderID
	}
	if tradeNo == "" || callback.DedupeKey == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid notification"})
	}
	verified := payment.VerifiedCallback{
		Provider: gatewayName, MerchantTradeNo: tradeNo,
		ProviderTradeNo: callback.GatewayOrderID, Method: callback.Method,
		Status: callback.Status, Amount: callback.Amount, Currency: callback.Currency,
		DedupeKey: callback.DedupeKey, Payload: callback.RawData,
	}
	if !callback.PaidAt.IsZero() {
		paidAt := callback.PaidAt
		verified.PaidAt = &paidAt
	}
	err = data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		return payment.Finalize(tx, gatewayName, verified)
	})
	if err != nil {
		switch {
		case errors.Is(err, payment.ErrCallbackNotFound):
			return c.NoContent(http.StatusNotFound)
		case errors.Is(err, payment.ErrCallbackMismatch):
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid notification"})
		default:
			return c.NoContent(http.StatusInternalServerError)
		}
	}
	if gatewayName == "ecpay" {
		return c.String(http.StatusOK, "1|OK")
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "success"})
}

// HandlePaymentReturn only looks up the order to redirect the browser. It does
// not validate a payment notification or change any database state.
func HandlePaymentReturn(c echo.Context) error {
	if data.DB == nil {
		return c.NoContent(http.StatusServiceUnavailable)
	}
	tradeNo := firstQuery(c, "MerchantTradeNo", "merchant_trade_no", "trade_no")
	if tradeNo == "" {
		return c.NoContent(http.StatusNotFound)
	}
	var attempt core.Payment
	if err := data.DB.WithContext(c.Request().Context()).Where("merchant_trade_no = ?", tradeNo).First(&attempt).Error; err != nil {
		return c.NoContent(http.StatusNotFound)
	}
	var order core.Order
	if err := data.DB.WithContext(c.Request().Context()).Select("id", "number").First(&order, attempt.OrderID).Error; err != nil || order.Number == "" {
		return c.NoContent(http.StatusNotFound)
	}
	return c.Redirect(http.StatusFound, "/checkout/done/"+url.PathEscape(order.Number))
}

// HandleGetOrderStatus is a legacy owner-only read route. Current order state
// comes from Jelly; the provider is never queried from a browser request.
func HandleGetOrderStatus(c echo.Context) error {
	if data.DB == nil {
		return c.NoContent(http.StatusServiceUnavailable)
	}
	user, ok := auth.GetUserFromContext(c)
	if !ok || user == nil {
		return c.NoContent(http.StatusNotFound)
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		return c.NoContent(http.StatusNotFound)
	}
	var order core.Order
	if err := data.DB.WithContext(c.Request().Context()).Select("id", "user_id", "status", "total_amount", "currency").First(&order, uint(id)).Error; err != nil || order.UserID == nil || *order.UserID != user.ID {
		return c.NoContent(http.StatusNotFound)
	}
	return c.JSON(http.StatusOK, map[string]interface{}{
		"order_id": order.ID, "status": order.Status, "amount": order.TotalAmount, "currency": order.Currency,
	})
}

func paymentCallbackFields(c echo.Context) (map[string]interface{}, error) {
	values := make(map[string]interface{})
	if strings.Contains(strings.ToLower(c.Request().Header.Get(echo.HeaderContentType)), echo.MIMEApplicationJSON) {
		if err := c.Bind(&values); err != nil {
			return nil, err
		}
		return values, nil
	}
	form, err := c.FormParams()
	if err != nil {
		return nil, err
	}
	for key, items := range form {
		if len(items) > 0 {
			values[key] = items[0]
		}
	}
	return values, nil
}

func paymentRequestBaseURL(c echo.Context) (string, error) {
	if configured := strings.TrimSpace(os.Getenv("APP_URL")); configured != "" {
		parsed, err := url.Parse(configured)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", errors.New("invalid APP_URL")
		}
		return parsed.Scheme + "://" + parsed.Host, nil
	}
	scheme := c.Scheme()
	if scheme != "http" && scheme != "https" {
		return "", errors.New("invalid request scheme")
	}
	if strings.TrimSpace(c.Request().Host) != c.Request().Host || strings.ContainsAny(c.Request().Host, "/?#@\\%") {
		return "", errors.New("invalid request host")
	}
	return scheme + "://" + c.Request().Host, nil
}

func firstQuery(c echo.Context, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(c.QueryParam(key)); value != "" {
			return value
		}
	}
	return ""
}
