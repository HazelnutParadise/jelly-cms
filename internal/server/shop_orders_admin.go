package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/notify"
	"github.com/HazelnutParadise/jelly-cms/internal/payment"
	"github.com/goccy/go-json"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	errInvalidOrderRefund        = errors.New("invalid refund request")
	errRefundIdempotencyConflict = errors.New("idempotency key was already used for a different refund")
)

func adminOrdersList(c echo.Context) error {
	if data.DB == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "service unavailable"})
	}
	db := data.DB.WithContext(c.Request().Context()).Model(&core.Order{})
	status, q := strings.TrimSpace(c.QueryParam("status")), strings.TrimSpace(c.QueryParam("q"))
	email := strings.TrimSpace(c.QueryParam("email"))
	from, to := strings.TrimSpace(c.QueryParam("from")), strings.TrimSpace(c.QueryParam("to"))
	if date := strings.TrimSpace(c.QueryParam("date")); date != "" && from == "" && to == "" {
		from, to = date, date
	}
	if status != "" && status != "all" {
		if !isValidShopOrderStatus(status) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid status"})
		}
		db = db.Where("status = ?", status)
	}
	if email != "" {
		db = db.Where("lower(email) LIKE ?", "%"+strings.ToLower(email)+"%")
	}
	if q != "" {
		pat := "%" + q + "%"
		db = db.Where("number ILIKE ? OR email ILIKE ? OR phone ILIKE ? OR COALESCE(shipping::text,'') ILIKE ?", pat, pat, pat, pat)
	}
	if from != "" {
		start, err := time.Parse("2006-01-02", from)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid from date"})
		}
		db = db.Where("created_at >= ?", start)
	}
	if to != "" {
		end, err := time.Parse("2006-01-02", to)
		if err != nil || (from != "" && to < from) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid to date"})
		}
		db = db.Where("created_at < ?", end.AddDate(0, 0, 1))
	}
	page, err := parsePositiveQueryInt(c.QueryParam("page"), 1, 1_000_000)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid page"})
	}
	perPage, err := parsePositiveQueryInt(c.QueryParam("per_page"), 20, 100)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid per_page"})
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not list orders"})
	}
	var orders []core.Order
	if err := db.Preload("Items").Order("created_at DESC, id DESC").Offset((page - 1) * perPage).Limit(perPage).Find(&orders).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not list orders"})
	}
	items := make([]map[string]interface{}, 0, len(orders))
	for _, order := range orders {
		orderItems := make([]map[string]interface{}, 0, len(order.Items))
		for _, item := range order.Items {
			orderItems = append(orderItems, map[string]interface{}{"title": item.Title, "variant_name": item.VariantName, "qty": item.Quantity, "quantity": item.Quantity})
		}
		items = append(items, map[string]interface{}{
			"id": order.ID, "number": order.Number, "email": order.Email, "status": order.Status,
			"total_amount": order.TotalAmount, "total": order.TotalAmount, "currency": order.Currency,
			"payment_provider": order.PaymentProvider, "payment_method": order.PaymentMethod,
			"shipping": order.Shipping, "items": orderItems, "created_at": order.CreatedAt,
		})
	}
	counts := make(map[string]int, len(shopOrderStatusesForAdmin()))
	for _, value := range shopOrderStatusesForAdmin() {
		var count int64
		if err := data.DB.WithContext(c.Request().Context()).Model(&core.Order{}).Where("status = ?", value).Count(&count).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not count orders"})
		}
		counts[value] = int(count)
	}
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	var revenue30d, orders30d, pending int64
	if err := data.DB.WithContext(c.Request().Context()).Model(&core.Order{}).Where("status IN ? AND created_at >= ?", []string{core.OrderStatusPaid, core.OrderStatusShipped, core.OrderStatusCompleted}, cutoff).Select("COALESCE(SUM(total_amount),0)").Scan(&revenue30d).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not calculate order revenue"})
	}
	if err := data.DB.WithContext(c.Request().Context()).Model(&core.Order{}).Where("created_at >= ?", cutoff).Count(&orders30d).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not count recent orders"})
	}
	if err := data.DB.WithContext(c.Request().Context()).Model(&core.Order{}).Where("status = ?", core.OrderStatusPending).Count(&pending).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not count pending orders"})
	}
	var avgOrder int64
	if orders30d > 0 {
		avgOrder = revenue30d / orders30d
	}
	return c.JSON(http.StatusOK, map[string]interface{}{
		"items": items, "total": total, "counts": counts,
		"kpi": map[string]interface{}{"revenue_30d": revenue30d, "orders_30d": int(orders30d), "pending": int(pending), "avg_order": avgOrder},
	})
}

func shopOrderStatusesForAdmin() []string {
	return []string{core.OrderStatusPending, core.OrderStatusPaid, core.OrderStatusShipped, core.OrderStatusCompleted, core.OrderStatusCancelled, core.OrderStatusExpired, core.OrderStatusRefunded}
}

func parsePositiveQueryInt(raw string, fallback, max int) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > max {
		return 0, errors.New("invalid positive integer")
	}
	return value, nil
}

func isValidShopOrderStatus(s string) bool {
	switch s {
	case core.OrderStatusPending, core.OrderStatusPaid, core.OrderStatusShipped, core.OrderStatusCompleted,
		core.OrderStatusCancelled, core.OrderStatusExpired, core.OrderStatusRefunded:
		return true
	}
	return false
}

func adminOrderGet(c echo.Context) error {
	if data.DB == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "service unavailable"})
	}
	id, parseErr := parseOrderID(c.Param("id"))
	if parseErr != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
	}
	var order core.Order
	db := data.DB.WithContext(c.Request().Context())
	if err := db.Preload("Items").Preload("User").First(&order, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not load order"})
	}

	var events []core.OrderEvent
	if err := db.Where("order_id = ?", order.ID).Order("at ASC, id ASC").Find(&events).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not load order history"})
	}
	timeline := make([]map[string]interface{}, 0, len(events))
	refundRequested := false
	for _, ev := range events {
		if ev.Kind == "refund_requested" {
			refundRequested = true
		}
		timeline = append(timeline, map[string]interface{}{
			"id":            ev.ID,
			"at":            ev.At,
			"kind":          ev.Kind,
			"from":          ev.FromStatus,
			"to":            ev.ToStatus,
			"status":        ev.ToStatus,
			"from_status":   ev.FromStatus,
			"to_status":     ev.ToStatus,
			"message":       ev.Message,
			"actor_user_id": ev.ActorUserID,
			"meta":          ev.Meta,
		})
	}

	// build items
	items := make([]map[string]interface{}, 0, len(order.Items))
	for _, it := range order.Items {
		items = append(items, map[string]interface{}{
			"id":           it.ID,
			"variant_id":   it.VariantID,
			"product_id":   it.ProductID,
			"title":        it.Title,
			"variant_name": it.VariantName,
			"sku":          it.SKU,
			"image":        it.Image,
			"quantity":     it.Quantity,
			"qty":          it.Quantity,
			"unit_price":   it.UnitPrice,
			"price":        it.Price,
			"line_total":   it.LineTotal,
		})
	}

	var payment core.Payment
	var paymentResp interface{} = nil
	if err := db.Where("order_id = ?", order.ID).Order("id DESC").First(&payment).Error; err == nil {
		paymentResp = map[string]interface{}{
			"id":                payment.ID,
			"provider":          payment.Provider,
			"method":            payment.Method,
			"merchant_trade_no": payment.MerchantTradeNo,
			"amount":            payment.Amount,
			"currency":          payment.Currency,
			"status":            payment.Status,
			"provider_trade_no": payment.ProviderTradeNo,
			"paid_at":           payment.PaidAt,
			"masked_info":       payment.MaskedInfo,
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not load payment"})
	}
	if order.TotalAmount == 0 && order.PaymentProvider == "none" && order.PaymentMethod == "none" {
		paymentResp = map[string]interface{}{"provider": "none", "method": "none", "kind": "none", "amount": int64(0), "currency": order.Currency, "status": "none"}
	}

	customer := map[string]interface{}{
		"email": order.Email,
		"phone": order.Phone,
	}
	var shipping map[string]interface{}
	if len(order.Shipping) > 0 && json.Unmarshal(order.Shipping, &shipping) == nil {
		for _, key := range []string{"recipient", "name"} {
			if name, ok := shipping[key].(string); ok && strings.TrimSpace(name) != "" {
				customer["name"] = name
				break
			}
		}
	}
	if order.UserID != nil {
		customer["user_id"] = *order.UserID
	}
	var historyCount int64
	query := db.Model(&core.Order{})
	if order.UserID != nil {
		query = query.Where("user_id = ?", *order.UserID)
	} else {
		query = query.Where("lower(email) = ?", strings.ToLower(strings.TrimSpace(order.Email)))
	}
	if err := query.Count(&historyCount).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not load customer history"})
	}
	customer["history_count"] = historyCount

	resp := map[string]interface{}{
		"id":                  order.ID,
		"number":              order.Number,
		"status":              order.Status,
		"refund_requested":    order.Status == core.OrderStatusPaid && refundRequested,
		"email":               order.Email,
		"phone":               order.Phone,
		"currency":            order.Currency,
		"subtotal":            order.Subtotal,
		"discount_total":      order.DiscountTotal,
		"shipping_total":      order.ShippingTotal,
		"tax_total":           order.TaxTotal,
		"rounding_adjustment": order.RoundingAdjustment,
		"total_amount":        order.TotalAmount,
		"total":               order.TotalAmount,
		"coupon_code":         order.CouponCode,
		"coupon_id":           order.CouponID,
		"shipping_method":     order.ShippingMethod,
		"shipping":            order.Shipping,
		"invoice":             order.Invoice,
		"payment_provider":    order.PaymentProvider,
		"payment_method":      order.PaymentMethod,
		"carrier":             order.Carrier,
		"tracking_no":         order.TrackingNo,
		"refunded_total":      order.RefundedTotal,
		"internal_note":       order.InternalNote,
		"created_at":          order.CreatedAt,
		"updated_at":          order.UpdatedAt,
		"paid_at":             order.PaidAt,
		"shipped_at":          order.ShippedAt,
		"completed_at":        order.CompletedAt,
		"cancelled_at":        order.CancelledAt,
		"items":               items,
		"timeline":            timeline,
		"payment":             paymentResp,
		"customer":            customer,
		"snapshot":            map[string]interface{}{"shipping": order.Shipping, "invoice": order.Invoice},
		"allowed":             core.AllowedTransitions(order.Status),
	}
	return c.JSON(http.StatusOK, resp)
}

func adminOrderUpdateStatus(c echo.Context) error {
	if data.DB == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "service unavailable"})
	}
	id, err := parseOrderID(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
	}
	var req struct {
		Status     string `json:"status"`
		Carrier    string `json:"carrier"`
		TrackingNo string `json:"tracking_no"`
		Note       string `json:"note"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
	}
	req.Status = strings.TrimSpace(req.Status)
	if req.Status == "" || !isValidShopOrderStatus(req.Status) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "status required"})
	}
	actorID := getCurrentUserID(c)

	var order core.Order
	err = data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, id).Error; err != nil {
			return err
		}
		from := order.Status
		to := req.Status
		if to == core.OrderStatusShipped && from == core.OrderStatusPaid {
			var count int64
			if err := tx.Model(&core.OrderEvent{}).Where("order_id = ? AND kind = ?", order.ID, "refund_requested").Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return errOrderRefundRequestPending
			}
		}
		if to == core.OrderStatusShipped && (strings.TrimSpace(req.Carrier) == "" || strings.TrimSpace(req.TrackingNo) == "") {
			return errors.New("shipping details are required")
		}
		zeroPaymentOrder := order.TotalAmount == 0 && order.PaymentProvider == "none" && order.PaymentMethod == "none"
		if (to == core.OrderStatusCancelled && from == core.OrderStatusPaid && !zeroPaymentOrder) ||
			(to == core.OrderStatusRefunded && (from == core.OrderStatusShipped || from == core.OrderStatusCompleted)) {
			if order.RefundedTotal < order.TotalAmount {
				reason := strings.TrimSpace(req.Note)
				if reason == "" {
					reason = fmt.Sprintf("order status changed from %s to %s", from, to)
				}
				return applyOrderRefund(tx, c, &order, actorID, order.TotalAmount-order.RefundedTotal, false, reason, false, "")
			}
			return errors.New("order has no refundable balance")
		}
		svc := core.NewOrderService(tx)
		if err := svc.Transition(tx, order.ID, from, to, actorID, req.Note); err != nil {
			return err
		}
		if to == core.OrderStatusShipped {
			if err := tx.Model(&core.Order{}).Where("id = ?", order.ID).Updates(map[string]interface{}{
				"carrier": strings.TrimSpace(req.Carrier), "tracking_no": strings.TrimSpace(req.TrackingNo),
			}).Error; err != nil {
				return err
			}
		}
		if to == core.OrderStatusCancelled && from == core.OrderStatusPaid && zeroPaymentOrder {
			if err := core.Restock(tx, order.ID); err != nil {
				return err
			}
			if err := core.ReleaseRedemption(tx, order.ID); err != nil {
				return err
			}
		}
		if (to == core.OrderStatusCancelled || to == core.OrderStatusExpired) && from == core.OrderStatusPending {
			if err := core.Release(tx, order.ID); err != nil {
				return err
			}
			if err := core.ReleaseRedemption(tx, order.ID); err != nil {
				return err
			}
		}
		if to == core.OrderStatusShipped {
			if err := notify.Emit(c.Request().Context(), tx, notify.Event{
				Kind: notify.KindOrderShipped, Category: notify.CategoryOrders,
				Params:     map[string]any{"order": order.Number, "carrier": strings.TrimSpace(req.Carrier), "tracking_no": strings.TrimSpace(req.TrackingNo)},
				Recipients: notify.Recipients{Emails: []string{order.Email}},
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		var te *core.TransitionError
		if errors.As(err, &te) {
			return c.JSON(http.StatusConflict, map[string]interface{}{"allowed": te.Allowed})
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
		}
		if errors.Is(err, payment.ErrNotSupported) {
			return c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": "provider refund is not supported"})
		}
		if err.Error() == "shipping details are required" || err.Error() == "order has no refundable balance" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if errors.Is(err, errOrderRefundRequestPending) {
			return c.JSON(http.StatusConflict, map[string]string{"error": "refund request must be resolved before shipping"})
		}
		c.Logger().Errorf("admin order %d status update failed: %v", id, err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not update order"})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

var errOrderRefundRequestPending = errors.New("refund request must be resolved before shipping")

func parseOrderID(raw string) (uint, error) {
	value, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 32)
	if err != nil || value == 0 {
		return 0, errors.New("invalid order id")
	}
	return uint(value), nil
}

func getCurrentUserID(c echo.Context) *uint {
	if u := c.Get("user"); u != nil {
		if user, ok := u.(*core.User); ok {
			id := user.ID
			return &id
		}
		if user, ok := u.(core.User); ok {
			id := user.ID
			return &id
		}
	}
	if principal, err := auth.GetCurrentPrincipal(c); err == nil && principal != nil && principal.User != nil && principal.User.ID != 0 {
		id := principal.User.ID
		return &id
	}
	return nil
}

func adminOrderAddNote(c echo.Context) error {
	if data.DB == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "service unavailable"})
	}
	id, err := parseOrderID(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
	}
	var req map[string]interface{}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
	}
	body := ""
	if v, ok := req["body"].(string); ok {
		body = v
	}
	if v, ok := req["note"].(string); ok && body == "" {
		body = v
	}
	actorID := getCurrentUserID(c)
	err = data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		var order core.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, id).Error; err != nil {
			return err
		}
		ev := core.OrderEvent{
			OrderID: order.ID, At: time.Now(), ActorUserID: actorID, Kind: "note", FromStatus: order.Status, ToStatus: order.Status, Message: body,
		}
		if err := tx.Create(&ev).Error; err != nil {
			return err
		}
		if err := tx.Model(&core.Order{}).Where("id = ?", order.ID).Update("internal_note", body).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
		}
		c.Logger().Errorf("admin order %d note update failed: %v", id, err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not add order note"})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func adminOrderUpdateAddress(c echo.Context) error {
	if data.DB == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "service unavailable"})
	}
	id, err := parseOrderID(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
	}
	var req struct {
		Recipient *string `json:"recipient"`
		Name      *string `json:"name"`
		Phone     *string `json:"phone"`
		Postcode  *string `json:"postcode"`
		City      *string `json:"city"`
		Address   *string `json:"address"`
	}
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, 1<<20)
	decoder := json.NewDecoder(c.Request().Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
	}
	if req.Recipient == nil && req.Name == nil && req.Phone == nil && req.Postcode == nil && req.City == nil && req.Address == nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "address fields required"})
	}
	err = data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		var order core.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, id).Error; err != nil {
			return err
		}
		if order.Status != core.OrderStatusPending && order.Status != core.OrderStatusPaid {
			return errors.New("invalid status")
		}
		shipping := map[string]interface{}{}
		if len(order.Shipping) > 0 {
			if err := json.Unmarshal(order.Shipping, &shipping); err != nil {
				return errors.New("invalid stored address")
			}
		}
		if req.Name != nil {
			name := strings.TrimSpace(*req.Name)
			shipping["name"] = name
			if req.Recipient == nil {
				shipping["recipient"] = name
			}
		}
		if req.Recipient != nil {
			shipping["recipient"] = strings.TrimSpace(*req.Recipient)
		}
		for key, value := range map[string]*string{
			"phone": req.Phone, "postcode": req.Postcode, "city": req.City, "address": req.Address,
		} {
			if value != nil {
				shipping[key] = strings.TrimSpace(*value)
			}
		}
		raw, err := json.Marshal(shipping)
		if err != nil {
			return err
		}
		return tx.Model(&core.Order{}).Where("id = ?", order.ID).Update("shipping", raw).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
		}
		if err.Error() == "invalid status" || err.Error() == "invalid stored address" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		c.Logger().Errorf("admin order %d address update failed: %v", id, err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not update address"})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func adminOrderRefund(c echo.Context) error {
	if data.DB == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "service unavailable"})
	}
	id, err := parseOrderID(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
	}
	var req struct {
		Amount         json.RawMessage `json:"amount"`
		Reason         string          `json:"reason"`
		Restock        bool            `json:"restock"`
		IdempotencyKey string          `json:"idempotency_key"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
	}
	key := strings.TrimSpace(req.IdempotencyKey)
	headerKey := strings.TrimSpace(c.Request().Header.Get("Idempotency-Key"))
	if key != "" && headerKey != "" && key != headerKey {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "idempotency key mismatch"})
	}
	if key == "" {
		key = headerKey
	}
	req.Reason = strings.TrimSpace(req.Reason)
	amount, amountOmitted := int64(0), len(req.Amount) == 0
	if !amountOmitted && (strings.TrimSpace(string(req.Amount)) == "null" || json.Unmarshal(req.Amount, &amount) != nil || amount <= 0) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid refund amount or reason"})
	}
	if req.Reason == "" || len(req.Reason) > 1000 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid refund amount or reason"})
	}
	amountText := "remaining"
	if !amountOmitted {
		amountText = strconv.FormatInt(amount, 10)
	}
	if key == "" {
		fingerprint := sha256.Sum256([]byte(fmt.Sprintf("order:%d/refund/%s/%s/%t", id, amountText, req.Reason, req.Restock)))
		key = "auto-" + hex.EncodeToString(fingerprint[:16])
	}
	if !validRefundIdempotencyKey(key) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid idempotency key"})
	}
	actorID := getCurrentUserID(c)
	err = data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		var order core.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, id).Error; err != nil {
			return err
		}
		return applyOrderRefund(tx, c, &order, actorID, amount, amountOmitted, req.Reason, req.Restock, key)
	})
	if err != nil {
		if errors.Is(err, payment.ErrNotSupported) {
			return c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": "provider refund is not supported"})
		}
		if errors.Is(err, errInvalidOrderRefund) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if errors.Is(err, errRefundIdempotencyConflict) {
			return c.JSON(http.StatusConflict, map[string]string{"error": err.Error()})
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not process refund"})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func validRefundIdempotencyKey(key string) bool {
	if len(key) == 0 || len(key) > 128 {
		return false
	}
	for _, r := range key {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

type adminRefundPayload struct {
	OrderID       uint   `json:"order_id"`
	Amount        int64  `json:"amount"`
	AmountOmitted bool   `json:"amount_omitted,omitempty"`
	Reason        string `json:"reason"`
	Restock       bool   `json:"restock"`
}

func applyOrderRefund(tx *gorm.DB, c echo.Context, order *core.Order, actorID *uint, amount int64, amountOmitted bool, reason string, restock bool, key string) error {
	if order == nil || strings.TrimSpace(reason) == "" {
		return errInvalidOrderRefund
	}
	switch order.PaymentProvider {
	case "sandbox", "manual_transfer":
	default:
		return payment.ErrNotSupported
	}
	key = strings.TrimSpace(key)
	if key == "" {
		amountText := strconv.FormatInt(amount, 10)
		if amountOmitted {
			amountText = "remaining"
		}
		fingerprint := sha256.Sum256([]byte(fmt.Sprintf("order:%d/refund/%s/%s/%t", order.ID, amountText, reason, restock)))
		key = "auto-" + hex.EncodeToString(fingerprint[:16])
	}
	if !validRefundIdempotencyKey(key) {
		return errInvalidOrderRefund
	}
	var existing core.PaymentEvent
	err := tx.Where("provider = ? AND dedupe_key = ?", "admin-refund", key).First(&existing).Error
	if err == nil {
		var previous adminRefundPayload
		if !existing.Verified || json.Unmarshal(existing.Payload, &previous) != nil || previous.OrderID != order.ID || previous.Reason != reason || previous.Restock != restock || previous.Amount <= 0 || (amountOmitted && !previous.AmountOmitted) || (!amountOmitted && (previous.AmountOmitted || previous.Amount != amount)) {
			return errRefundIdempotencyConflict
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if order.RefundedTotal < 0 || order.RefundedTotal > order.TotalAmount {
		return errInvalidOrderRefund
	}
	if amountOmitted {
		amount = order.TotalAmount - order.RefundedTotal
	}
	if amount <= 0 || amount > order.TotalAmount-order.RefundedTotal {
		return errInvalidOrderRefund
	}
	payload := adminRefundPayload{OrderID: order.ID, Amount: amount, AmountOmitted: amountOmitted, Reason: reason, Restock: restock}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if order.Status != core.OrderStatusPaid && order.Status != core.OrderStatusShipped && order.Status != core.OrderStatusCompleted {
		return errInvalidOrderRefund
	}
	from := order.Status
	newRefunded := order.RefundedTotal + amount
	if restock {
		if err := core.Restock(tx, order.ID); err != nil {
			return fmt.Errorf("restock order: %w", err)
		}
	}
	if newRefunded == order.TotalAmount {
		to := core.OrderStatusRefunded
		if from == core.OrderStatusPaid {
			to = core.OrderStatusCancelled
		}
		if err := core.NewOrderService(tx).Transition(tx, order.ID, from, to, actorID, reason); err != nil {
			return fmt.Errorf("transition refunded order: %w", err)
		}
		order.Status = to
	}
	if err := tx.Model(&core.Order{}).Where("id = ?", order.ID).Update("refunded_total", newRefunded).Error; err != nil {
		return fmt.Errorf("update refunded total: %w", err)
	}
	order.RefundedTotal = newRefunded
	if err := tx.Create(&core.OrderEvent{
		OrderID: order.ID, At: time.Now().UTC(), ActorUserID: actorID,
		Kind: "refund", FromStatus: from, ToStatus: order.Status, Message: reason,
		Meta: json.RawMessage(fmt.Sprintf(`{"amount":%d,"restock":%t}`, amount, restock)),
	}).Error; err != nil {
		return fmt.Errorf("record refund order event: %w", err)
	}
	if err := tx.Create(&core.PaymentEvent{Provider: "admin-refund", DedupeKey: key, Verified: true, Payload: payloadBytes}).Error; err != nil {
		return fmt.Errorf("record refund payment event: %w", err)
	}
	if err := notify.Emit(c.Request().Context(), tx, notify.Event{
		Kind: notify.KindOrderRefunded, Category: notify.CategoryOrders,
		Params:     map[string]any{"order": order.Number, "amount": amount, "reason": reason},
		GroupKey:   fmt.Sprintf("order:%s:refund:%s", order.Number, key),
		Recipients: notify.Recipients{Roles: []string{"admin"}, Emails: []string{order.Email}},
	}); err != nil {
		return fmt.Errorf("notify refund: %w", err)
	}
	return nil
}

func adminOrderMarkPaid(c echo.Context) error {
	if data.DB == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "service unavailable"})
	}
	id, parseErr := parseOrderID(c.Param("id"))
	if parseErr != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
	}
	actorID := getCurrentUserID(c)
	err := data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		var order core.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, id).Error; err != nil {
			return err
		}
		// only manual_transfer
		if order.PaymentProvider != "manual_transfer" || order.PaymentMethod != "bank_transfer" {
			return errors.New("not allowed")
		}
		if order.Status != core.OrderStatusPending {
			return errors.New("invalid status")
		}
		svc := core.NewOrderService(tx)
		if err := svc.Transition(tx, order.ID, core.OrderStatusPending, core.OrderStatusPaid, actorID, "marked paid"); err != nil {
			return err
		}
		var latestPayment core.Payment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("order_id = ?", order.ID).Order("id DESC").First(&latestPayment).Error; err == nil {
			paidAt := time.Now().UTC()
			if err := tx.Model(&core.Payment{}).Where("id = ?", latestPayment.ID).Updates(map[string]interface{}{"status": string(payment.PaymentStatusPaid), "paid_at": paidAt}).Error; err != nil {
				return err
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := notify.Emit(c.Request().Context(), tx, notify.Event{
			Kind: notify.KindOrderNew, Category: notify.CategoryOrders,
			Params: map[string]any{"order": order.Number}, Recipients: notify.Recipients{Roles: []string{"admin"}},
		}); err != nil {
			return err
		}
		if err := notify.Emit(c.Request().Context(), tx, notify.Event{
			Kind: notify.KindOrderConfirmation, Category: notify.CategoryOrders,
			Params: map[string]any{"order": order.Number}, Recipients: notify.Recipients{Emails: []string{order.Email}},
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if err.Error() == "not allowed" || err.Error() == "invalid status" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
		}
		c.Logger().Errorf("admin order %d mark-paid failed: %v", id, err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not mark order paid"})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}
