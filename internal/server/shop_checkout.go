package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/notify"
	"github.com/HazelnutParadise/jelly-cms/internal/payment"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type shopCheckoutAddressInput struct {
	Method    string `json:"method"`
	Name      string `json:"name"`
	Recipient string `json:"recipient"`
	Phone     string `json:"phone"`
	Postcode  string `json:"postcode"`
	City      string `json:"city"`
	Address   string `json:"address"`
	StoreID   string `json:"store_id"`
	StoreName string `json:"store_name"`
}

type shopPaymentInput struct {
	Method string `json:"method"`
}
type shopInvoiceInput struct {
	Kind         string `json:"kind"`
	CarrierCode  string `json:"carrier_code"`
	TaxID        string `json:"tax_id"`
	CompanyName  string `json:"company_name"`
	DonationCode string `json:"donation_code"`
}

type shopCheckoutInput struct {
	Email          string                   `json:"email"`
	Phone          string                   `json:"phone"`
	CreateAccount  bool                     `json:"create_account"`
	Shipping       shopCheckoutAddressInput `json:"shipping"`
	Payment        shopPaymentInput         `json:"payment"`
	Invoice        shopInvoiceInput         `json:"invoice"`
	CouponCode     string                   `json:"coupon_code"`
	AcceptTerms    bool                     `json:"accept_terms"`
	IdempotencyKey string                   `json:"idempotency_key"`
	Website        string                   `json:"website"`
}

type shopCheckoutOrderResponse struct {
	Number             string `json:"number"`
	Status             string `json:"status"`
	Total              int64  `json:"total"`
	TaxTotal           int64  `json:"tax_total"`
	RoundingAdjustment int64  `json:"rounding_adjustment"`
	Currency           string `json:"currency"`
}
type shopCheckoutPaymentResponse struct {
	Provider     string `json:"provider"`
	Method       string `json:"method"`
	Kind         string `json:"kind"`
	Instructions string `json:"instructions"`
}
type shopCheckoutResponse struct {
	Order       shopCheckoutOrderResponse   `json:"order"`
	RedirectURL string                      `json:"redirect_url"`
	Payment     shopCheckoutPaymentResponse `json:"payment"`
	AccessToken string                      `json:"access_token"`
}

var merchantTradePattern = regexp.MustCompile(`^[A-Za-z0-9]{20}$`)
var shopTaxRatePattern = regexp.MustCompile(`^\d+(\.\d+)?$`)
var shopIdempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func shopCheckoutPage(tm *theme.Manager) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		cart, _, err := shopCartSession(c, false)
		if err != nil {
			if errors.Is(err, errShopCartUnavailable) {
				return c.Redirect(http.StatusFound, "/cart")
			}
			return echo.NewHTTPError(500, "could not load cart")
		}
		if cart.ID == 0 {
			return c.Redirect(http.StatusFound, "/cart")
		}
		var count int64
		user := shopPrincipalUser(c)
		err = data.DB.Transaction(func(tx *gorm.DB) error {
			locked, err := shopLockOwnedCart(tx, cart, user)
			if err != nil {
				return err
			}
			return tx.Model(&core.CartItem{}).Where("cart_id = ?", locked.ID).Count(&count).Error
		})
		if errors.Is(err, errShopCartUnavailable) {
			return c.Redirect(http.StatusFound, "/cart")
		}
		if err != nil {
			return echo.NewHTTPError(500, "could not load cart")
		}
		if count == 0 {
			return c.Redirect(http.StatusFound, "/cart")
		}
		title := "結帳"
		if shopLang(c) == "en" {
			title = "Checkout"
		}
		var customer interface{}
		if user := shopPrincipalUser(c); user != nil && user.Status == "active" {
			customer = map[string]string{"Email": user.Email, "Phone": "", "Name": ""}
		}
		return shopPage(tm, "checkout.html", title, map[string]interface{}{"Customer": customer})(c)
	}
}

func shopCheckoutHandler(c echo.Context) error {
	if !shopValidOrigin(c) {
		return shopJSONError(c, http.StatusForbidden, "origin", "request origin is not allowed")
	}
	if shopRateLimit(c, "checkout", 8, time.Minute) {
		return shopJSONError(c, http.StatusTooManyRequests, "rate_limit", "請稍後再試")
	}
	var input shopCheckoutInput
	if err := shopDecodeJSON(c, &input); err != nil {
		return shopJSONError(c, http.StatusBadRequest, "input", "invalid JSON request")
	}
	if strings.TrimSpace(input.Website) != "" {
		return shopJSONError(c, http.StatusBadRequest, "website", "invalid request")
	}
	if !input.AcceptTerms {
		return shopJSONError(c, http.StatusBadRequest, "accept_terms", "請先同意購物條款")
	}
	input.Email = strings.TrimSpace(input.Email)
	input.Phone = strings.TrimSpace(input.Phone)
	if !shopValidEmail(input.Email) {
		return shopJSONError(c, http.StatusBadRequest, "email", "請輸入有效電子郵件")
	}
	if !shopValidPhone(input.Phone) {
		return shopJSONError(c, http.StatusBadRequest, "phone", "請輸入有效手機號碼")
	}
	if field := shopValidInvoice(input.Invoice); field != "" {
		return shopJSONError(c, http.StatusBadRequest, field, "發票資料格式錯誤")
	}
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if !shopIdempotencyKeyPattern.MatchString(input.IdempotencyKey) {
		return shopJSONError(c, http.StatusBadRequest, "idempotency_key", "idempotency_key must be 1-64 letters, numbers, hyphens, or underscores")
	}
	shippingMethods, err := shopShippingMethods()
	if err != nil {
		return echo.NewHTTPError(500, "could not load shipping options")
	}
	shipping := shopFindShipping(shippingMethods, strings.TrimSpace(input.Shipping.Method))
	if input.Shipping.Method != "" && shipping == nil {
		return shopJSONError(c, http.StatusBadRequest, "shipping.method", "不支援的配送方式")
	}
	var selected *shopPaymentMethod
	paymentMethods, err := shopPaymentMethods()
	if err != nil {
		return echo.NewHTTPError(500, "could not load payment options")
	}
	for _, method := range paymentMethods {
		if method.ID == input.Payment.Method {
			selected = &method
			break
		}
	}
	cart, rawCartToken, err := shopCartSession(c, true)
	if err != nil {
		if errors.Is(err, errShopCartUnavailable) {
			return shopJSONError(c, http.StatusConflict, "cart_unavailable", "購物車已失效，請重新操作")
		}
		return echo.NewHTTPError(500, "could not load cart")
	}
	if replaced, _ := c.Get("shop_cart_replaced").(bool); replaced {
		return shopJSONError(c, http.StatusConflict, "cart_unavailable", "購物車已更新，請重新操作")
	}
	if cart.ID == 0 {
		return shopJSONError(c, http.StatusBadRequest, "cart", "購物車是空的")
	}
	if rawCartToken == "" {
		return shopJSONError(c, http.StatusBadRequest, "cart", "購物車是空的")
	}

	principal := shopPrincipalUser(c)
	var authUser *core.User
	if principal != nil && principal.Role == "customer" && principal.Status == "active" {
		authUser = principal
	}
	baseURL := strings.TrimRight(os.Getenv("APP_URL"), "/")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	var result shopCheckoutResponse
	replayed := false
	err = data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		lockedCart, err := shopLockOwnedCart(tx, cart, principal)
		if err != nil {
			return err
		}
		var concurrentReplay core.Order
		lookupErr := tx.Where("idempotency_key = ?", input.IdempotencyKey).First(&concurrentReplay).Error
		if lookupErr == nil {
			var remaining int64
			if err := tx.Model(&core.CartItem{}).Where("cart_id = ?", lockedCart.ID).Count(&remaining).Error; err != nil {
				return err
			}
			access := shopCheckoutAccess(rawCartToken, input.IdempotencyKey, concurrentReplay.Number)
			if remaining != 0 || concurrentReplay.LookupTokenHash != shopTokenHash(access) {
				return errShopIdempotencyConflict
			}
			var replayErr error
			result, replayErr = shopReplayResponseWithDB(tx, concurrentReplay, access)
			if replayErr != nil {
				return replayErr
			}
			replayed = true
			return nil
		} else if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			return lookupErr
		}
		lines, priced, subtotal, err := shopCheckoutLines(tx, lockedCart.ID)
		if err != nil {
			return err
		}
		if len(lines) == 0 {
			return errShopEmptyCart
		}
		requiresShipping := false
		for _, line := range lines {
			if shopLineRequiresShipping(line.Product) {
				requiresShipping = true
				break
			}
		}
		if requiresShipping && (shipping == nil || !shopShippingInputValid(*shipping, input.Shipping, input.Phone)) {
			return errShopInvalidShipping
		}
		method := shopPaymentMethod{Provider: "none", ID: "none", Kind: "none"}
		if selected != nil {
			method = *selected
		}
		shippingFee := int64(0)
		shippingID := "digital"
		if requiresShipping {
			shippingFee = shipping.Fee
			shippingID = shipping.ID
		}
		freeOverRaw, err := shopReadOptionWithDB(tx, "shop_free_shipping_over")
		if err != nil {
			return err
		}
		freeOver, _ := strconv.ParseInt(freeOverRaw, 10, 64)
		if requiresShipping && shipping.FreeOver > 0 && subtotal >= shipping.FreeOver {
			shippingFee = 0
		}
		if requiresShipping && freeOver > 0 && subtotal >= freeOver {
			shippingFee = 0
		}
		couponCode := strings.TrimSpace(input.CouponCode)
		if couponCode == "" {
			couponCode = lockedCart.CouponCode
		}
		var coupon *core.Coupon
		couponUserID := (*uint)(nil)
		if authUser != nil {
			id := authUser.ID
			couponUserID = &id
		}
		var discount core.Discount
		if couponCode != "" {
			var row core.Coupon
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("lower(code) = ?", strings.ToLower(couponCode)).First(&row).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return errShopInvalidCoupon
				}
				return err
			}
			targets, err := core.CouponTargets(tx, row.ID)
			if err != nil {
				return err
			}
			used, err := core.CouponPerUserUsed(tx, row.ID, couponUserID, input.Email)
			if err != nil {
				return err
			}
			if row.PerUserLimit != nil && used >= int64(*row.PerUserLimit) {
				return errShopInvalidCoupon
			}
			discount, err = core.EvaluateWithTargets(core.CartQuote{Lines: priced, Subtotal: subtotal, ShippingFee: shippingFee, Email: input.Email}, row, authUser, targets)
			if err != nil {
				return errShopInvalidCoupon
			}
			coupon = &row
			if discount.FreeShipping {
				shippingFee = 0
			}
		}
		total, roundingAdjustment, totalErr := shopCalculateTotal(subtotal, shippingFee, discount.Amount)
		if totalErr != nil {
			return errShopAmountOverflow
		}
		if total > 0 && selected == nil {
			return errShopInvalidPaymentMethod
		}
		taxMode, err := shopReadOptionWithDB(tx, "shop_tax_mode")
		if err != nil {
			return err
		}
		taxRateRaw, err := shopReadOptionWithDB(tx, "shop_tax_rate")
		if err != nil {
			return err
		}
		if taxMode == "" {
			taxMode = "included"
		}
		if taxMode != "included" {
			return errors.New("unsupported shop tax mode")
		}
		if taxRateRaw == "" {
			taxRateRaw = "5"
		}
		taxTotal, err := shopIncludedTax(subtotal-discount.Amount, taxRateRaw)
		if err != nil {
			return err
		}
		customer, err := core.EnsureCustomer(tx, input.Email, input.Phone)
		if err != nil {
			return err
		}
		var orderUserID *uint
		if customer.Role == "customer" {
			id := customer.ID
			orderUserID = &id
		}
		if authUser != nil {
			id := authUser.ID
			orderUserID = &id
		}
		var inviteLink string
		if input.CreateAccount && customer.Role == "customer" && customer.Status == "invited" && customer.Password == "" {
			var invites int64
			if err := tx.Model(&core.UserInvite{}).Where("user_id = ? AND kind = 'invite' AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > ?", customer.ID, time.Now().UTC()).Count(&invites).Error; err != nil {
				return err
			}
			if invites == 0 {
				raw, tokenHash, err := newUserLinkToken()
				if err != nil {
					return err
				}
				expires := time.Now().UTC().Add(7 * 24 * time.Hour)
				invite := core.UserInvite{UserID: customer.ID, Kind: "invite", TokenHash: tokenHash, Role: "customer", Note: "", ExpiresAt: expires}
				if err := tx.Create(&invite).Error; err != nil {
					return err
				}
				inviteLink = baseURL + "/invite/" + raw
			}
		}
		sequenceTime := time.Now()
		if config.GlobalConfig != nil && config.GlobalConfig.Database.Timezone != "" {
			loc, err := time.LoadLocation(config.GlobalConfig.Database.Timezone)
			if err != nil {
				return fmt.Errorf("load site timezone: %w", err)
			}
			sequenceTime = sequenceTime.In(loc)
		}
		year := sequenceTime.Year()
		var seq int64
		if err := tx.Raw(`INSERT INTO order_counters(year,seq) VALUES(?,1) ON CONFLICT(year) DO UPDATE SET seq=order_counters.seq+1 RETURNING seq`, year).Row().Scan(&seq); err != nil {
			return err
		}
		if seq < 1 || seq > 9999 {
			return errors.New("yearly order sequence exhausted")
		}
		prefix, err := shopReadOptionWithDB(tx, "shop_order_prefix")
		if err != nil {
			return err
		}
		if prefix == "" {
			prefix = "JL"
		}
		prefix = strings.TrimSpace(prefix)
		if len(prefix) > 12 || !regexp.MustCompile(`^[A-Za-z0-9]+$`).MatchString(prefix) {
			return errors.New("invalid shop order prefix")
		}
		number := fmt.Sprintf("%s-%04d-%04d", prefix, year, seq)
		currency, err := shopReadOptionWithDB(tx, "shop_currency")
		if err != nil {
			return err
		}
		if currency == "" {
			currency = "TWD"
		}
		holdKey, holdUnit, defaultHold := "shop_stock_hold_minutes_card", time.Minute, int64(30)
		if input.Payment.Method == "atm" || input.Payment.Method == "cvs" || input.Payment.Method == "bank_transfer" {
			holdKey, holdUnit, defaultHold = "shop_stock_hold_hours_offline", time.Hour, 72
		}
		hold := defaultHold
		if total > 0 {
			holdRaw, err := shopReadOptionWithDB(tx, holdKey)
			if err != nil {
				return err
			}
			if holdRaw != "" {
				parsed, parseErr := strconv.ParseInt(holdRaw, 10, 64)
				if parseErr != nil || parsed < 1 || parsed > 24*365 {
					return fmt.Errorf("invalid %s option", holdKey)
				}
				hold = parsed
			}
		}
		expiry := time.Now().UTC().Add(time.Duration(hold) * holdUnit)
		var expiryPointer *time.Time = &expiry
		paymentMethod := input.Payment.Method
		if total == 0 {
			expiryPointer = nil
			paymentMethod = "none"
			method = shopPaymentMethod{Provider: "none", ID: "none", Kind: "none"}
		}
		couponID := (*uint)(nil)
		if coupon != nil {
			id := coupon.ID
			couponID = &id
		}
		shippingJSON, err := json.Marshal(input.Shipping)
		if err != nil {
			return err
		}
		invoiceJSON, err := json.Marshal(input.Invoice)
		if err != nil {
			return err
		}
		key := input.IdempotencyKey
		accessToken := shopCheckoutAccess(rawCartToken, key, number)
		accessHash := shopTokenHash(accessToken)
		order := core.Order{Number: number, UserID: orderUserID, Email: input.Email, Phone: input.Phone, Currency: currency, Subtotal: subtotal, DiscountTotal: discount.Amount, ShippingTotal: shippingFee, TaxTotal: taxTotal, RoundingAdjustment: roundingAdjustment, TotalAmount: total, Status: core.OrderStatusPending, CouponCode: couponCode, CouponID: couponID, ShippingMethod: shippingID, Shipping: shippingJSON, Invoice: invoiceJSON, PaymentProvider: method.Provider, PaymentMethod: paymentMethod, IdempotencyKey: &key, LookupTokenHash: accessHash, ExpiresAt: expiryPointer, Locale: shopRequestLanguage(c), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		if err := tx.Create(&order).Error; err != nil {
			return err
		}
		for _, line := range lines {
			productID, variantID := line.Product.ID, line.Variant.ID
			item := core.OrderItem{OrderID: order.ID, ProductID: &productID, VariantID: &variantID, Quantity: line.Qty, Price: line.UnitPrice, UnitPrice: line.UnitPrice, LineTotal: line.LineTotal, Title: line.Product.Title, VariantName: line.Variant.Name, SKU: line.Variant.SKU, Image: shopProductImage(line.Product.Images)}
			if err := tx.Create(&item).Error; err != nil {
				return err
			}
		}
		for _, line := range lines {
			if err := core.Reserve(tx, line.Variant.ID, line.Qty, order.ID); err != nil {
				return err
			}
		}
		if coupon != nil {
			if err := core.Redeem(tx, coupon.ID, order.ID, couponUserID, input.Email); err != nil {
				return err
			}
		}
		redirectURL, instructions := "/checkout/done/"+order.Number+"?t="+url.QueryEscape(accessToken), ""
		if total > 0 {
			payRow, rurl, instr, err := payment.CreateAttempt(tx, &order, paymentMethod, method.Provider, baseURL, accessToken)
			if err != nil {
				return err
			}
			if payRow.OrderID != order.ID || !merchantTradePattern.MatchString(payRow.MerchantTradeNo) {
				return errors.New("payment helper returned invalid attempt")
			}
			redirectURL, instructions = rurl, instr
			if err := tx.Model(&core.Order{}).Where("id = ?", order.ID).Update("expires_at", expiry).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Create(&core.OrderEvent{OrderID: order.ID, At: time.Now().UTC(), Kind: "created", FromStatus: "", ToStatus: core.OrderStatusPending, Message: "checkout"}).Error; err != nil {
				return err
			}
			if err := core.NewOrderService(tx).Transition(tx, order.ID, core.OrderStatusPending, core.OrderStatusPaid, nil, "zero-total checkout"); err != nil {
				return err
			}
			paidAt := time.Now().UTC()
			order.Status, order.PaidAt, order.ExpiresAt = core.OrderStatusPaid, &paidAt, nil
			if err := tx.Model(&core.Order{}).Where("id = ?", order.ID).Updates(map[string]any{"status": core.OrderStatusPaid, "paid_at": paidAt, "expires_at": nil}).Error; err != nil {
				return err
			}
			if err := shopIncrementSoldCount(tx, order.ID); err != nil {
				return err
			}
		}
		if total > 0 {
			if err := tx.Create(&core.OrderEvent{OrderID: order.ID, At: time.Now().UTC(), Kind: "created", FromStatus: "", ToStatus: core.OrderStatusPending, Message: "checkout"}).Error; err != nil {
				return err
			}
		}
		if err := shopEmitOrderCreated(c.Request().Context(), tx, order, c.RealIP(), inviteLink, instructions); err != nil {
			return err
		}
		if err := tx.Where("cart_id = ?", lockedCart.ID).Delete(&core.CartItem{}).Error; err != nil {
			return err
		}
		if err := tx.Model(&core.Cart{}).Where("id = ?", lockedCart.ID).Updates(map[string]any{"coupon_code": "", "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		result = shopCheckoutResponse{Order: shopCheckoutOrderResponse{Number: order.Number, Status: order.Status, Total: order.TotalAmount, TaxTotal: taxTotal, RoundingAdjustment: roundingAdjustment, Currency: order.Currency}, RedirectURL: redirectURL, Payment: shopCheckoutPaymentResponse{Provider: method.Provider, Method: paymentMethod, Kind: method.Kind, Instructions: instructions}, AccessToken: accessToken}
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errShopCartUnavailable):
			return shopJSONError(c, http.StatusConflict, "cart_unavailable", "購物車已失效，請重新操作")
		case errors.Is(err, errShopEmptyCart):
			return shopJSONError(c, http.StatusBadRequest, "cart", "購物車是空的")
		case errors.Is(err, errShopInvalidShipping):
			return shopJSONError(c, http.StatusBadRequest, "shipping", "請完整填寫收件資料並選擇有效配送方式")
		case errors.Is(err, errShopInvalidPaymentMethod):
			return shopJSONError(c, http.StatusBadRequest, "payment.method", "不支援的付款方式")
		case errors.Is(err, core.ErrInsufficientStock):
			return shopJSONError(c, http.StatusConflict, "stock", "商品庫存不足")
		case errors.Is(err, errShopInvalidCoupon):
			return shopJSONError(c, http.StatusUnprocessableEntity, "coupon", "折扣碼無效")
		case errors.Is(err, errShopAmountOverflow):
			return shopJSONError(c, http.StatusBadRequest, "amount", "訂單金額超出範圍")
		case errors.Is(err, errShopIdempotencyConflict):
			return shopJSONError(c, http.StatusConflict, "idempotency_conflict", "checkout key already used")
		default:
			message := strings.ToLower(err.Error())
			if strings.Contains(message, "idempotency") || strings.Contains(message, "idx_orders_idempotency_key") {
				return shopJSONError(c, http.StatusConflict, "idempotency_conflict", "checkout key already used")
			}
			return echo.NewHTTPError(http.StatusInternalServerError, "could not create order")
		}
	}
	if replayed {
		return c.JSON(http.StatusOK, result)
	}
	return c.JSON(http.StatusCreated, result)
}

var (
	errShopEmptyCart            = errors.New("shop: empty cart")
	errShopInvalidCoupon        = errors.New("shop: invalid coupon")
	errShopAmountOverflow       = errors.New("shop: amount overflow")
	errShopIdempotencyConflict  = errors.New("shop: idempotency conflict")
	errShopInvalidShipping      = errors.New("shop: invalid shipping")
	errShopInvalidPaymentMethod = errors.New("shop: invalid payment method")
)

func shopShippingInputValid(method shopShippingMethod, input shopCheckoutAddressInput, phone string) bool {
	if !method.RequiresAddress {
		return true
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = strings.TrimSpace(input.Recipient)
	}
	return name != "" && len(name) <= 120 && strings.TrimSpace(input.Postcode) != "" && len(input.Postcode) <= 10 && strings.TrimSpace(input.City) != "" && len(input.City) <= 60 && strings.TrimSpace(input.Address) != "" && len(input.Address) <= 255 && (input.Phone == "" || shopValidPhone(input.Phone)) && phone != ""
}

func shopLineRequiresShipping(product core.Product) bool {
	return product.RequiresShipping && product.Kind != "digital"
}

func shopCheckoutLines(tx *gorm.DB, cartID uint) ([]shopCartLine, []core.PricedLine, int64, error) {
	var items []core.CartItem
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("cart_id = ?", cartID).Order("variant_id").Find(&items).Error; err != nil {
		return nil, nil, 0, err
	}
	lines := make([]shopCartLine, 0, len(items))
	priced := make([]core.PricedLine, 0, len(items))
	var subtotal int64
	for _, item := range items {
		var variant core.ProductVariant
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&variant, item.VariantID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, nil, 0, errShopEmptyCart
			}
			return nil, nil, 0, err
		}
		var product core.Product
		if err := tx.Where("id = ? AND status = 'active' AND deleted_at IS NULL", variant.ProductID).First(&product).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, nil, 0, errShopEmptyCart
			}
			return nil, nil, 0, err
		}
		if variant.Status != "active" {
			return nil, nil, 0, errShopEmptyCart
		}
		qty := item.Qty
		if qty < 1 {
			return nil, nil, 0, errShopEmptyCart
		}
		if qty > 99 {
			qty = 99
		}
		if variant.Stock <= 0 {
			return nil, nil, 0, core.ErrInsufficientStock
		}
		if qty > variant.Stock {
			qty = variant.Stock
			if err := tx.Model(&core.CartItem{}).Where("id = ?", item.ID).Update("qty", qty).Error; err != nil {
				return nil, nil, 0, err
			}
		}
		if variant.Price < 0 || int64(qty) > math.MaxInt64/max64(variant.Price, 1) {
			return nil, nil, 0, errShopAmountOverflow
		}
		lineTotal := variant.Price * int64(qty)
		if subtotal > math.MaxInt64-lineTotal {
			return nil, nil, 0, errShopAmountOverflow
		}
		subtotal += lineTotal
		var categoryIDs []uint
		if err := tx.Raw("SELECT category_id FROM product_category_links WHERE product_id = ? ORDER BY category_id", product.ID).Scan(&categoryIDs).Error; err != nil {
			return nil, nil, 0, err
		}
		line := shopCartLine{Item: item, Variant: variant, Product: product, Qty: qty, UnitPrice: variant.Price, LineTotal: lineTotal, Available: true, CategoryIDs: categoryIDs}
		lines = append(lines, line)
		priced = append(priced, core.PricedLine{VariantID: variant.ID, ProductID: product.ID, CategoryIDs: categoryIDs, Qty: qty, UnitPrice: variant.Price, LineTotal: lineTotal})
	}
	return lines, priced, subtotal, nil
}

func shopCheckoutAccess(rawCookie, key, number string) string {
	mac := hmac.New(sha256.New, []byte(rawCookie))
	_, _ = mac.Write([]byte(key + "\x00" + number))
	return hex.EncodeToString(mac.Sum(nil))
}

func shopReplayResponseWithDB(db *gorm.DB, order core.Order, access string) (shopCheckoutResponse, error) {
	if order.TotalAmount == 0 && order.PaymentProvider == "none" && order.PaymentMethod == "none" {
		return shopCheckoutResponse{Order: shopCheckoutOrderResponse{Number: order.Number, Status: order.Status, Total: order.TotalAmount, TaxTotal: order.TaxTotal, RoundingAdjustment: order.RoundingAdjustment, Currency: order.Currency}, RedirectURL: "/checkout/done/" + order.Number + "?t=" + url.QueryEscape(access), Payment: shopCheckoutPaymentResponse{Provider: "none", Method: "none", Kind: "none"}, AccessToken: access}, nil
	}
	var p core.Payment
	if err := db.Where("order_id = ?", order.ID).Order("id desc").First(&p).Error; err != nil {
		return shopCheckoutResponse{}, err
	}
	kind := "redirect"
	if p.Method == "bank_transfer" {
		kind = "offline"
	}
	redirect := "/checkout/pay/" + p.MerchantTradeNo
	if p.Provider == "sandbox" {
		redirect = "/checkout/sandbox/" + p.MerchantTradeNo
	}
	if p.Method == "bank_transfer" {
		redirect = "/checkout/done/" + order.Number + "?t=" + url.QueryEscape(access)
	} else if access != "" {
		redirect += "?t=" + url.QueryEscape(access)
	}
	instructions := ""
	if p.Method == "bank_transfer" && p.Provider == "manual_transfer" {
		var delivery core.NotificationDelivery
		err := db.Where("kind = ? AND lower(to_address) = lower(?) AND params ->> 'number' = ?", notify.KindOrderOfflineInstructions, order.Email, order.Number).Order("id desc").First(&delivery).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return shopCheckoutResponse{}, err
		}
		if err == nil {
			var params struct {
				Instructions string `json:"instructions"`
			}
			if err := json.Unmarshal(delivery.Params, &params); err != nil {
				return shopCheckoutResponse{}, err
			}
			instructions = params.Instructions
		}
	}
	return shopCheckoutResponse{Order: shopCheckoutOrderResponse{Number: order.Number, Status: order.Status, Total: order.TotalAmount, TaxTotal: order.TaxTotal, RoundingAdjustment: order.RoundingAdjustment, Currency: order.Currency}, RedirectURL: redirect, Payment: shopCheckoutPaymentResponse{Provider: p.Provider, Method: p.Method, Kind: kind, Instructions: instructions}, AccessToken: access}, nil
}

func shopIncrementSoldCount(tx *gorm.DB, orderID uint) error {
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

func shopIncludedTax(gross int64, rateRaw string) (int64, error) {
	rateRaw = strings.TrimSpace(rateRaw)
	rate, ok := new(big.Rat).SetString(rateRaw)
	if !shopTaxRatePattern.MatchString(rateRaw) || !ok || rate.Sign() < 0 || rate.Cmp(big.NewRat(100, 1)) > 0 {
		return 0, errors.New("invalid shop tax rate")
	}
	if gross <= 0 || rate.Sign() == 0 {
		return 0, nil
	}
	// For a tax-inclusive total, the tax fraction is rate/(100+rate).
	numerator := new(big.Int).Mul(big.NewInt(gross), rate.Num())
	denominator := new(big.Int).Mul(big.NewInt(100), rate.Denom())
	denominator.Add(denominator, rate.Num())
	// Round to the nearest minor unit, with exact halves rounded up.
	numerator.Add(numerator, new(big.Int).Rsh(new(big.Int).Set(denominator), 1))
	numerator.Quo(numerator, denominator)
	if !numerator.IsInt64() {
		return math.MaxInt64, nil
	}
	return numerator.Int64(), nil
}

func shopEmitOrderCreated(ctx context.Context, tx *gorm.DB, order core.Order, realIP, inviteLink, instructions string) error {
	zeroTotalPaid := order.TotalAmount == 0 && order.PaymentProvider == "none" && order.PaymentMethod == "none"
	if order.PaymentMethod == "bank_transfer" || zeroTotalPaid {
		confirmationToken, err := shopCreateCheckoutLookupToken(tx, order, realIP)
		if err != nil {
			return err
		}
		confirmationParams := map[string]any{
			"number": order.Number, "total": order.TotalAmount, "currency": order.Currency,
			"access_url": "/orders/view?t=" + url.QueryEscape(confirmationToken),
		}
		if err := notify.Emit(ctx, tx, notify.Event{Kind: "order.confirmation", Category: notify.CategoryOrders, Params: confirmationParams, GroupKey: "order:" + order.Number + ":confirmation", Recipients: notify.Recipients{Emails: []string{order.Email}}}); err != nil {
			return err
		}
	}
	lookupToken, err := shopCreateCheckoutLookupToken(tx, order, realIP)
	if err != nil {
		return err
	}
	if err := notify.Emit(ctx, tx, notify.Event{Kind: "order.lookup_link", Category: notify.CategoryOrders, Params: map[string]any{"number": order.Number, "url": "/orders/view?t=" + url.QueryEscape(lookupToken)}, Recipients: notify.Recipients{Emails: []string{order.Email}}}); err != nil {
		return err
	}
	if order.PaymentMethod == "bank_transfer" && instructions != "" {
		if err := notify.Emit(ctx, tx, notify.Event{Kind: "order.offline_instructions", Category: notify.CategoryOrders, Params: map[string]any{"number": order.Number, "instructions": instructions}, Recipients: notify.Recipients{Emails: []string{order.Email}}}); err != nil {
			return err
		}
	}
	if inviteLink != "" {
		if err := notify.Emit(ctx, tx, notify.Event{Kind: "customer.set_password", Category: notify.CategorySecurity, Params: map[string]any{"url": inviteLink}, Recipients: notify.Recipients{Emails: []string{order.Email}}}); err != nil {
			return err
		}
	}
	if order.PaymentMethod == "bank_transfer" || zeroTotalPaid {
		return notify.Emit(ctx, tx, notify.Event{Kind: notify.KindOrderNew, Category: notify.CategoryOrders, Params: map[string]any{"number": order.Number, "total": order.TotalAmount}, GroupKey: "order:" + order.Number + ":new", Recipients: notify.Recipients{Roles: []string{"admin"}}})
	}
	return nil
}

func shopCreateCheckoutLookupToken(tx *gorm.DB, order core.Order, realIP string) (string, error) {
	raw, hash, err := shopNewLookupRaw()
	if err != nil {
		return "", err
	}
	if err := tx.Create(&core.OrderAccessToken{
		OrderID: order.ID, TokenHash: hash, Kind: "lookup",
		ExpiresAt: time.Now().UTC().Add(15 * time.Minute), CreatedIPMasked: shopMaskedIP(realIP),
	}).Error; err != nil {
		return "", err
	}
	return raw, nil
}

func shopProductImage(raw json.RawMessage) string {
	var images []string
	if json.Unmarshal(raw, &images) == nil {
		for _, image := range images {
			if strings.HasPrefix(image, "/") && !strings.HasPrefix(image, "//") {
				return image
			}
		}
	}
	return ""
}

func shopMaskedIP(ip string) string {
	parsed, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	if parsed.Is4() {
		return netip.PrefixFrom(parsed, 24).Masked().Addr().String()
	}
	return netip.PrefixFrom(parsed, 64).Masked().Addr().String()
}
