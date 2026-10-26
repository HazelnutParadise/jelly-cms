package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

const shopOrderViewCookieName = "jelly_order_view"

// shopAuthorizedOrder returns an order only when the caller owns it or has a
// valid checkout token or order-bound view cookie. Errors for missing or
// unauthorized orders are intentionally indistinguishable.
func shopAuthorizedOrder(c echo.Context, number, access string) (*core.Order, error) {
	c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	number = strings.TrimSpace(number)
	if number == "" {
		return nil, echo.NewHTTPError(http.StatusNotFound)
	}

	var order core.Order
	if err := data.DB.Where("number = ?", number).First(&order).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, echo.NewHTTPError(http.StatusNotFound)
		}
		return nil, err
	}
	if user := shopPrincipalUser(c); user != nil && user.ID != 0 && order.UserID != nil && *order.UserID == user.ID {
		return &order, nil
	}
	if access != "" && order.LookupTokenHash != "" && order.CreatedAt.Add(90*24*time.Hour).After(time.Now().UTC()) {
		hash := shopTokenHash(strings.TrimSpace(access))
		if len(hash) == len(order.LookupTokenHash) && subtle.ConstantTimeCompare([]byte(hash), []byte(order.LookupTokenHash)) == 1 {
			return &order, nil
		}
	}

	for _, cookie := range c.Request().Cookies() {
		if cookie.Name != shopOrderViewCookieName || cookie.Value == "" {
			continue
		}
		var token core.OrderAccessToken
		hash := shopTokenHash(cookie.Value)
		err := data.DB.Where("token_hash = ? AND kind = ? AND order_id = ? AND used_at IS NOT NULL AND expires_at > ?", hash, "view", order.ID, time.Now().UTC()).First(&token).Error
		if err == nil {
			return &order, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	return nil, echo.NewHTTPError(http.StatusNotFound)
}

// shopOrderTemplateView maps the persistence model into the front-contract
// shape without serializing the associated user or payment-provider secrets.
func shopOrderTemplateView(db *gorm.DB, order *core.Order) (map[string]any, error) {
	if db == nil || order == nil {
		return nil, errors.New("order view requires a database and order")
	}
	items := []map[string]any{}
	var rows []core.OrderItem
	if err := db.Where("order_id = ?", order.ID).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	productIDs := make([]uint, 0, len(rows))
	for _, row := range rows {
		if row.ProductID != nil {
			productIDs = append(productIDs, *row.ProductID)
		}
	}
	products := map[uint]core.Product{}
	if len(productIDs) > 0 {
		var productRows []core.Product
		if err := db.Where("id IN ?", productIDs).Find(&productRows).Error; err != nil {
			return nil, err
		}
		for _, product := range productRows {
			products[product.ID] = product
		}
	}
	for _, row := range rows {
		qty := row.Quantity
		unitPrice := row.UnitPrice
		lineTotal := row.LineTotal
		if unitPrice == 0 {
			unitPrice = row.Price
		}
		if lineTotal == 0 && qty > 0 {
			lineTotal = unitPrice * int64(qty)
		}
		image, slug := row.Image, ""
		if row.ProductID != nil {
			if product, ok := products[*row.ProductID]; ok {
				if image == "" {
					image = shopProductImage(product.Images)
				}
				slug = product.Slug
			}
		}
		items = append(items, map[string]any{
			"Title": row.Title, "VariantName": row.VariantName, "SKU": row.SKU,
			"Image": image, "ProductSlug": slug, "UnitPrice": unitPrice,
			"LineTotal": lineTotal, "Qty": qty,
		})
	}

	shipping := map[string]any{}
	if len(order.Shipping) > 0 && string(order.Shipping) != "null" {
		if err := json.Unmarshal(order.Shipping, &shipping); err != nil {
			return nil, err
		}
	}
	if shipping == nil {
		shipping = map[string]any{}
	}
	shipping = shopOrderShippingView(shipping)
	invoice := map[string]any{}
	if len(order.Invoice) > 0 && string(order.Invoice) != "null" {
		if err := json.Unmarshal(order.Invoice, &invoice); err != nil {
			return nil, err
		}
	}
	if invoice == nil {
		invoice = map[string]any{}
	}
	invoice = shopOrderInvoiceView(invoice)
	shippingLabel, etaDays := "", 0
	if order.ShippingMethod != "" {
		methods, err := shopShippingMethods()
		if err != nil {
			return nil, err
		}
		if method := shopFindShipping(methods, order.ShippingMethod); method != nil {
			shippingLabel, etaDays = method.Label, method.ETADays
		}
	}
	var payment core.Payment
	paymentErr := db.Where("order_id = ?", order.ID).Order("created_at DESC, id DESC").First(&payment).Error
	if paymentErr != nil && !errors.Is(paymentErr, gorm.ErrRecordNotFound) {
		return nil, paymentErr
	}
	paymentStatus, paymentInfo := order.Status, ""
	if paymentErr == nil {
		paymentStatus = payment.Status
		var err error
		paymentInfo, err = shopMaskedPaymentInfo(payment.MaskedInfo)
		if err != nil {
			return nil, err
		}
	}
	instructions, err := shopReadOption("shop_manual_transfer_note")
	if err != nil {
		return nil, err
	}
	canCancel := order.Status == core.OrderStatusPending
	refundRequested := false
	if order.Status == core.OrderStatusPaid {
		var count int64
		if err := db.Model(&core.OrderEvent{}).Where("order_id = ? AND kind = ?", order.ID, "refund_requested").Count(&count).Error; err != nil {
			return nil, err
		}
		refundRequested = count > 0
	}
	zeroPaymentOrder := order.TotalAmount == 0 && order.PaymentProvider == "none" && order.PaymentMethod == "none"
	if order.Status == core.OrderStatusPaid && zeroPaymentOrder && order.ShippedAt == nil {
		canCancel = !refundRequested
	} else if order.Status == core.OrderStatusPaid && (order.PaymentProvider == "" || order.PaymentProvider == "sandbox" || order.PaymentProvider == "manual_transfer") {
		canCancel = !refundRequested
	}
	canRetry := order.Status == core.OrderStatusPending && order.ExpiresAt != nil && order.ExpiresAt.After(time.Now().UTC()) && paymentErr == nil && (payment.Status == "failed" || payment.Status == "expired")
	paymentKind := ""
	if order.TotalAmount == 0 && order.PaymentProvider == "none" && order.PaymentMethod == "none" {
		paymentKind, paymentStatus = "none", "none"
		paymentInfo = ""
	}
	if order.PaymentProvider != "manual_transfer" || order.PaymentMethod != "bank_transfer" {
		instructions = ""
	}

	return map[string]any{
		"Number": order.Number, "Status": order.Status, "Currency": order.Currency,
		"Email": order.Email, "Phone": order.Phone, "CreatedAt": shopOrderLocalTime(order.CreatedAt),
		"Subtotal": order.Subtotal, "DiscountTotal": order.DiscountTotal,
		"ShippingTotal": order.ShippingTotal, "TaxTotal": order.TaxTotal,
		"RoundingAdjustment": order.RoundingAdjustment,
		"Total":              order.TotalAmount, "RefundedTotal": order.RefundedTotal,
		"CouponCode": order.CouponCode, "ShippingMethod": order.ShippingMethod,
		"ShippingLabel": shippingLabel, "ETADays": etaDays,
		"Shipping": shipping, "PaymentMethod": order.PaymentMethod,
		"PaymentProvider": order.PaymentProvider, "PaymentStatus": paymentStatus,
		"PaymentKind": paymentKind,
		"PaymentInfo": paymentInfo, "Instructions": instructions,
		"ExpiresAt": shopOrderLocalTimePtr(order.ExpiresAt), "PaidAt": shopOrderLocalTimePtr(order.PaidAt),
		"ShippedAt": shopOrderLocalTimePtr(order.ShippedAt), "CompletedAt": shopOrderLocalTimePtr(order.CompletedAt),
		"CancelledAt": shopOrderLocalTimePtr(order.CancelledAt), "Carrier": order.Carrier,
		"TrackingNo": order.TrackingNo, "Invoice": invoice, "Items": items,
		"CanCancel": canCancel, "CanRetry": canRetry, "RefundRequested": refundRequested,
	}, nil
}

func shopMaskedPaymentInfo(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil {
		return "", err
	}
	get := func(key string) string { value, _ := values[key].(string); return strings.TrimSpace(value) }
	parts := []string{}
	if last4 := get("card_last4"); last4 != "" {
		if len(last4) > 4 {
			last4 = last4[len(last4)-4:]
		}
		parts = append(parts, "卡號末四碼："+last4)
	}
	if bank := get("bank_code"); bank != "" {
		parts = append(parts, "銀行代碼："+bank)
	}
	if account := get("account_number"); account != "" {
		parts = append(parts, "ATM 繳費帳號："+account)
	}
	if number := get("payment_number"); number != "" {
		parts = append(parts, "超商繳費代碼："+number)
	}
	for _, key := range []string{"barcode_1", "barcode_2", "barcode_3"} {
		if value := get(key); value != "" {
			parts = append(parts, "條碼："+value)
		}
	}
	if expiry := get("expires_at"); expiry != "" {
		parts = append(parts, "繳費期限："+shopPaymentExpiryText(expiry))
	}
	return strings.Join(parts, "\n"), nil
}

func shopPaymentExpiryText(value string) string {
	for _, layout := range []string{"2006/01/02 15:04:05", "2006/01/02", "2006-01-02 15:04:05", "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return parsed.Format("2006-01-02 15:04")
		}
	}
	return value
}

func shopOrderShippingView(source map[string]any) map[string]any {
	pick := func(keys ...string) string {
		for _, key := range keys {
			if value, ok := source[key].(string); ok && value != "" {
				return value
			}
		}
		return ""
	}
	return map[string]any{"Name": pick("name", "recipient", "Name", "Recipient"), "Recipient": pick("recipient", "name", "Recipient", "Name"), "Phone": pick("phone", "Phone"), "Postcode": pick("postcode", "Postcode"), "City": pick("city", "City"), "Address": pick("address", "Address"), "StoreID": pick("store_id", "StoreID"), "StoreName": pick("store_name", "StoreName")}
}

func shopOrderInvoiceView(source map[string]any) map[string]any {
	pick := func(key, snake string) string {
		if value, ok := source[key].(string); ok {
			return value
		}
		if value, ok := source[snake].(string); ok {
			return value
		}
		return ""
	}
	return map[string]any{"Kind": pick("Kind", "kind"), "CarrierCode": pick("CarrierCode", "carrier_code"), "TaxID": pick("TaxID", "tax_id"), "CompanyName": pick("CompanyName", "company_name"), "DonationCode": pick("DonationCode", "donation_code")}
}

func shopOrderLocalTime(value time.Time) time.Time {
	if config.GlobalConfig != nil && config.GlobalConfig.Database.Timezone != "" {
		if location, err := time.LoadLocation(config.GlobalConfig.Database.Timezone); err == nil {
			return value.In(location)
		}
	}
	return value
}

func shopOrderLocalTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	local := shopOrderLocalTime(*value)
	return &local
}
