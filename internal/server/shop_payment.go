package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/money"
	"github.com/HazelnutParadise/jelly-cms/internal/payment"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	sandboxAccessCookie      = "jelly_shop_sandbox_access"
	shopCheckoutAccessCookie = "jelly_shop_checkout_access"
)

// registerShopPaymentPages owns local payment handoffs and the sandbox-only
// simulation path. It is called by the commerce route registrar after checkout.
func registerShopPaymentPages(e *echo.Echo, tm *theme.Manager) {
	e.GET("/checkout/done/:number", shopOrderDonePage(tm))
	e.GET("/checkout/pay/:trade_no", shopECPayHandoff)
	e.GET("/checkout/sandbox/:trade_no", func(c echo.Context) error { return shopSandboxPage(c, tm) })
	e.POST("/api/checkout/retry", auth.ProtectBrowserWrites(shopCheckoutRetryAPI))
	e.POST("/api/checkout/sandbox/:trade_no", auth.ProtectBrowserWrites(shopSandboxSimulate))
}

func shopOrderDonePage(tm *theme.Manager) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		c.Response().Header().Set("Referrer-Policy", "no-referrer")
		if data.DB == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		viewData := map[string]any{"Order": nil, "Access": "", "ReceiptURL": ""}
		requestedAccess := strings.TrimSpace(c.QueryParam("t"))
		order, err := shopAuthorizedOrder(c, c.Param("number"), requestedAccess)
		access := ""
		if requestedAccess != "" {
			if err == nil && order != nil && shopCheckoutAccessTokenMatches(*order, requestedAccess) {
				access = requestedAccess
			}
		} else if err == nil || shopPaymentNotFound(err) {
			for _, cookie := range c.Request().Cookies() {
				if cookie.Name != shopCheckoutAccessCookie || strings.TrimSpace(cookie.Value) == "" {
					continue
				}
				candidate := strings.TrimSpace(cookie.Value)
				candidateOrder, candidateErr := shopAuthorizedOrder(c, c.Param("number"), candidate)
				if candidateErr != nil {
					if !shopPaymentNotFound(candidateErr) {
						err = candidateErr
						break
					}
					continue
				}
				if candidateOrder != nil && shopCheckoutAccessTokenMatches(*candidateOrder, candidate) {
					order, err, access = candidateOrder, nil, candidate
					break
				}
			}
		}
		if err == nil && order != nil {
			view, viewErr := shopOrderTemplateView(data.DB.WithContext(c.Request().Context()), order)
			if viewErr != nil {
				return c.NoContent(http.StatusServiceUnavailable)
			}
			// Transfer instructions are site configuration; they are never accepted
			// from the browser or copied from a payment callback.
			if order.PaymentMethod == "bank_transfer" {
				instructions, optionErr := shopReadOption("shop_manual_transfer_note")
				if optionErr != nil {
					return c.NoContent(http.StatusServiceUnavailable)
				}
				view["Instructions"] = instructions
			} else {
				view["Instructions"] = ""
			}
			paymentStatus, _ := view["PaymentStatus"].(string)
			canRetry := order.Status == core.OrderStatusPending && (paymentStatus == "failed" || paymentStatus == "expired") && order.ExpiresAt != nil && order.ExpiresAt.After(time.Now().UTC())
			view["CanRetry"] = canRetry
			viewData["Order"] = view
			viewData["Access"] = access
		} else if err != nil {
			if !shopPaymentNotFound(err) {
				return c.NoContent(http.StatusServiceUnavailable)
			}
		}
		doneTitle := "訂單"
		if shopRequestLanguage(c) == "en" {
			doneTitle = "Order"
		}
		return shopPage(tm, "order_done.html", doneTitle, viewData)(c)
	}
}

func shopCheckoutRetryAPI(c echo.Context) error {
	if data.DB == nil {
		return c.NoContent(http.StatusServiceUnavailable)
	}
	var input struct {
		Number string `json:"number"`
		Access string `json:"access"`
		Method string `json:"method"`
	}
	if err := shopDecodeJSON(c, &input); err != nil {
		return shopJSONError(c, http.StatusBadRequest, "request", "Invalid retry request")
	}
	input.Number = strings.TrimSpace(input.Number)
	input.Access = strings.TrimSpace(input.Access)
	input.Method = strings.TrimSpace(input.Method)
	if input.Number == "" || input.Method == "" {
		return shopJSONError(c, http.StatusBadRequest, "request", "Order number and payment method are required")
	}
	order, err := shopAuthorizedOrder(c, input.Number, input.Access)
	if err != nil || order == nil {
		return c.NoContent(http.StatusNotFound)
	}
	retryAccess := ""
	if input.Access != "" && order.LookupTokenHash != "" {
		hash := shopTokenHash(input.Access)
		if len(hash) == len(order.LookupTokenHash) && subtle.ConstantTimeCompare([]byte(hash), []byte(order.LookupTokenHash)) == 1 {
			retryAccess = input.Access
		}
	}
	if shopRateLimit(c, "checkout_retry:"+order.Number, 5, time.Hour) {
		return shopJSONError(c, http.StatusTooManyRequests, "rate_limit", "Too many payment retries")
	}

	methods, err := shopPaymentMethods()
	if err != nil {
		return c.NoContent(http.StatusServiceUnavailable)
	}
	var selected *shopPaymentMethod
	for i := range methods {
		if methods[i].Method == input.Method {
			selected = &methods[i]
			break
		}
	}
	if selected == nil {
		return shopJSONError(c, http.StatusConflict, "payment_method", "Payment method is not currently available")
	}

	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("APP_URL")), "/")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	var redirectURL, instructions string
	err = data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		var locked core.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", order.ID).First(&locked).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		if locked.Status != core.OrderStatusPending || locked.ExpiresAt == nil || !locked.ExpiresAt.After(now) {
			return errShopRetryUnavailable
		}
		if locked.TotalAmount <= 0 || strings.TrimSpace(locked.Currency) == "" {
			return errShopRetryInvalidAmount
		}
		var previous core.Payment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("order_id = ?", locked.ID).Order("id DESC").First(&previous).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errShopRetryUnavailable
			}
			return err
		}
		if previous.Status != string(payment.PaymentStatusFailed) && previous.Status != "expired" {
			return errShopRetryUnavailable
		}
		attempt, nextURL, note, err := payment.CreateAttempt(tx, &locked, selected.Method, selected.Provider, baseURL, retryAccess)
		if err != nil {
			return err
		}
		if attempt.OrderID != locked.ID || attempt.Amount != locked.TotalAmount || attempt.Currency != locked.Currency || !merchantTradePattern.MatchString(attempt.MerchantTradeNo) || nextURL == "" {
			return errors.New("payment retry returned an invalid attempt")
		}
		if err := tx.Model(&core.Order{}).Where("id = ? AND status = ?", locked.ID, core.OrderStatusPending).Updates(map[string]any{
			"payment_provider": selected.Provider,
			"payment_method":   selected.Method,
			"updated_at":       now,
		}).Error; err != nil {
			return err
		}
		redirectURL, instructions = nextURL, note
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errShopRetryUnavailable):
			return shopJSONError(c, http.StatusConflict, "retry_unavailable", "This order cannot be retried")
		case errors.Is(err, errShopRetryInvalidAmount):
			return shopJSONError(c, http.StatusConflict, "amount", "This order cannot be paid")
		default:
			return c.NoContent(http.StatusServiceUnavailable)
		}
	}
	return c.JSON(http.StatusOK, map[string]any{
		"redirect_url": redirectURL,
		"payment": map[string]string{
			"provider": selected.Provider, "method": selected.Method,
			"kind": selected.Kind, "instructions": instructions,
		},
	})
}

var (
	errShopRetryUnavailable   = errors.New("shop: retry unavailable")
	errShopRetryInvalidAmount = errors.New("shop: retry invalid amount")
)

func shopECPayHandoff(c echo.Context) error {
	if data.DB == nil || !merchantTradePattern.MatchString(c.Param("trade_no")) {
		return c.NoContent(http.StatusNotFound)
	}
	proof := strings.TrimSpace(c.QueryParam("t"))
	attempt, order, err := loadShopPaymentAttempt(c, c.Param("trade_no"), "ecpay")
	if err != nil || !shopPaymentAuthorized(c, order, proof) || order.Status != core.OrderStatusPending || attempt.Status != "pending" || (order.ExpiresAt != nil && !order.ExpiresAt.After(time.Now().UTC())) {
		return c.NoContent(http.StatusNotFound)
	}
	service := payment.NewService(data.DB.WithContext(c.Request().Context()))
	gateway, err := service.GetGateway("ecpay")
	if err != nil {
		return c.NoContent(http.StatusNotFound)
	}
	var items []core.OrderItem
	if err := data.DB.WithContext(c.Request().Context()).Where("order_id = ?", order.ID).Order("id").Find(&items).Error; err != nil {
		return c.NoContent(http.StatusServiceUnavailable)
	}
	providerItems := make([]payment.OrderItem, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item.Title)
		if item.VariantName != "" {
			name += " " + strings.TrimSpace(item.VariantName)
		}
		providerItems = append(providerItems, payment.OrderItem{Name: name, Quantity: item.Quantity, Price: item.UnitPrice})
	}
	var expireAt time.Time
	if order.ExpiresAt != nil {
		expireAt = *order.ExpiresAt
	}
	response, err := gateway.CreateOrder(c.Request().Context(), payment.CreateOrderRequest{
		OrderID: attempt.MerchantTradeNo, Amount: attempt.Amount, Currency: attempt.Currency,
		Method: attempt.Method, Description: "Order " + order.Number, CustomerEmail: order.Email,
		ExpireTime: expireAt, Items: providerItems,
	})
	if err != nil || response == nil || response.PaymentURL == "" || len(response.ExtraData) == 0 {
		return c.NoContent(http.StatusServiceUnavailable)
	}
	if !validECPayPaymentURL(response.PaymentURL) {
		return c.NoContent(http.StatusServiceUnavailable)
	}
	language := shopRequestLanguage(c)
	if order.Locale == "en" || order.Locale == "zh-TW" {
		language = order.Locale
	}
	method := map[string]string{"credit_card": "信用卡", "atm": "ATM 轉帳", "cvs": "超商代碼"}[attempt.Method]
	if language == "en" {
		method = map[string]string{"credit_card": "Credit card", "atm": "ATM transfer", "cvs": "CVS code"}[attempt.Method]
	}
	backURL := "/checkout/done/" + url.PathEscape(order.Number)
	if proof != "" && order.LookupTokenHash != "" {
		hash := shopTokenHash(proof)
		if len(hash) == len(order.LookupTokenHash) && subtle.ConstantTimeCompare([]byte(hash), []byte(order.LookupTokenHash)) == 1 {
			backURL += "?t=" + url.QueryEscape(proof)
		}
	}
	page, err := renderECPayPostForm(response.PaymentURL, response.ExtraData, ecpayHandoffView{
		Language: language, SiteTitle: getSiteTitle(), OrderNumber: order.Number,
		Amount: money.Format(attempt.Amount, attempt.Currency), Method: method, BackURL: backURL,
	})
	if err != nil {
		return c.NoContent(http.StatusInternalServerError)
	}
	if shopCheckoutAccessTokenMatches(order, proof) {
		now := time.Now().UTC()
		c.SetCookie(&http.Cookie{
			Name: shopCheckoutAccessCookie, Value: proof,
			Path:     "/checkout/done/" + url.PathEscape(order.Number),
			HttpOnly: true, Secure: c.IsTLS() || strings.EqualFold(strings.TrimSpace(os.Getenv("APP_ENV")), "production"),
			SameSite: http.SameSiteLaxMode, MaxAge: 1800, Expires: now.Add(30 * time.Minute),
		})
	}
	setPrivatePaymentHeaders(c, "form-action 'self' https://payment.ecpay.com.tw https://payment-stage.ecpay.com.tw; script-src 'unsafe-inline'; style-src 'unsafe-inline'")
	return c.HTMLBlob(http.StatusOK, page)
}

type ecpayHandoffView struct {
	Language    string
	SiteTitle   string
	OrderNumber string
	Amount      string
	Method      string
	BackURL     string
}

func validECPayPaymentURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "/Cashier/AioCheckOut/V5" {
		return false
	}
	return parsed.Host == "payment.ecpay.com.tw" || parsed.Host == "payment-stage.ecpay.com.tw"
}

func renderECPayPostForm(action string, fields map[string]string, view ecpayHandoffView) ([]byte, error) {
	if !validECPayPaymentURL(action) || (view.Language != "en" && view.Language != "zh-TW") || len(fields) == 0 {
		return nil, errors.New("ecpay: invalid handoff data")
	}
	en := view.Language == "en"
	lang, title, security, heading, progress, copy, continueText, backText, orderLabel, amountLabel, methodLabel := "zh-TW", "正在前往綠界付款頁", "安全付款", "正在前往綠界付款頁", "前往付款頁", "這一頁不會收集卡號。卡片資料只在綠界的付款頁輸入，付款完成後會回到本站顯示結果。", "沒有自動跳轉？繼續前往綠界", "回到訂單", "訂單", "金額", "付款方式"
	if en {
		lang, title, security, heading, progress, copy, continueText, backText, orderLabel, amountLabel, methodLabel = "en", "Taking you to ECPay", "Secure payment", "Taking you to ECPay", "Redirecting", "This page never asks for your card. Enter card details only on ECPay, then return here to see your payment result.", "Not redirected? Continue to ECPay", "Back to your order", "Order", "Amount", "Method"
	}
	const source = `<!doctype html><html lang="{{.Language}}"><head><meta charset="utf-8"><meta name="referrer" content="no-referrer"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}}</title><style>
:root{color-scheme:light dark;--ink:#15161A;--muted:#5F6470;--paper:#fff;--soft:#F5F5F7;--outline:#E2E3E8;--rose:#E8285B;--blue:#2F6BFF;--mint:#13C2A3}
*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;padding:24px;background:var(--paper);color:var(--ink);font:15px system-ui,-apple-system,"PingFang TC","Microsoft JhengHei",sans-serif;-webkit-font-smoothing:antialiased}.topline{position:fixed;inset:0 0 auto;height:3px;background:linear-gradient(90deg,var(--rose) 0 64%,var(--blue) 64% 86%,var(--mint) 86% 100%)}main{width:min(100%,440px);display:flex;flex-direction:column;gap:16px}.brand{display:flex;align-items:center;gap:10px;font-size:17px;font-weight:800}.mark{width:26px;height:30px;flex:none}.stripe{width:56px;height:3px;background:linear-gradient(90deg,var(--rose) 0 52%,transparent 52% 60%,var(--blue) 60% 82%,transparent 82% 88%,var(--mint) 88% 100%);transform:skewX(-32deg);transform-origin:left bottom}.label{color:var(--muted);font-size:11px;font-weight:700;letter-spacing:.08em;text-transform:uppercase}h1{margin:0;font-size:32px;line-height:1.1;letter-spacing:-.03em}.bar{height:4px;background:var(--soft);overflow:hidden}.bar i{display:block;width:45%;height:100%;background:var(--rose);animation:progress 1.6s ease-in-out infinite alternate}@keyframes progress{to{transform:translateX(120%)}}p{margin:0;color:var(--muted);line-height:1.7}.summary{display:grid;grid-template-columns:auto 1fr;gap:4px 16px;padding:14px 16px;background:var(--soft);border-radius:0 12px 0 12px}.summary b{text-align:right;overflow-wrap:anywhere}.mono{font-family:ui-monospace,monospace;font-weight:800}.actions{display:flex;flex-direction:column;gap:14px}.primary{display:inline-flex;align-items:center;justify-content:center;min-height:48px;padding:0 22px;border:0;border-radius:0 10px 0 10px;background:var(--rose);color:white;font:inherit;font-weight:700;cursor:pointer;width:100%}.back{display:inline-flex;align-items:center;gap:8px;color:var(--blue);font-size:14px;text-decoration:underline;text-underline-offset:3px}.back svg{width:14px;height:14px;stroke:currentColor;fill:none;stroke-width:2}@media(prefers-color-scheme:dark){:root{color-scheme:dark;--ink:#ECEDF1;--muted:#9A9FAB;--paper:#141519;--soft:#1E2026;--outline:#2A2B31}.back{color:#8FB0FF}}@media(prefers-reduced-motion:reduce){.bar i{animation:none}}
</style></head><body><div class="topline" aria-hidden="true"></div><main><div class="brand"><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 85 100" role="img" class="mark" aria-hidden="true"><path d="M10.80 9.20 H36.30 A6.5 6.5 0 0 1 42.80 15.70 V41.20 H17.30 A6.5 6.5 0 0 1 10.80 34.70 Z" fill="#2F6BFF" fill-opacity="0.8"/><path d="M26.70 30.50 H52.20 A6.5 6.5 0 0 1 58.70 37.00 V62.50 H33.20 A6.5 6.5 0 0 1 26.70 56.00 Z" fill="#13C2A3" fill-opacity="0.82"/><path d="M42.00 52.00 H67.50 A6.5 6.5 0 0 1 74.00 58.50 V84.00 H48.50 A6.5 6.5 0 0 1 42.00 77.50 Z" fill="#E8285B" fill-opacity="0.9" transform="rotate(-7 58.00 68.00)"/><path d="M12.00 77.00 H23.50 A3 3 0 0 1 26.50 80.00 V91.50 H15.00 A3 3 0 0 1 12.00 88.50 Z" fill="#F5B800" fill-opacity="0.95"/><path d="M62.50 16.50 H71.10 A2.2 2.2 0 0 1 73.30 18.70 V27.30 H64.70 A2.2 2.2 0 0 1 62.50 25.10 Z" fill="#15161A" fill-opacity="0.9"/></svg>{{.SiteTitle}}</div><span class="stripe" aria-hidden="true"></span><div class="label">{{.Security}}</div><h1>{{.Heading}}</h1><div class="bar" role="progressbar" aria-label="{{.Progress}}"><i></i></div><p>{{.Copy}}</p><div class="summary"><span>{{.OrderLabel}}</span><b class="mono">{{.OrderNumber}}</b><span>{{.AmountLabel}}</span><b class="mono">{{.Amount}}</b><span>{{.MethodLabel}}</span><b>{{.Method}}</b></div><form id="payment" method="post" action="{{.Action}}">{{range $key,$value:=.Fields}}<input type="hidden" name="{{$key}}" value="{{$value}}">{{end}}<div class="actions"><button class="primary" type="submit">{{.ContinueText}}</button><a class="back" href="{{.BackURL}}"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 12H4M10 6l-6 6 6 6"/></svg>{{.BackText}}</a></div></form><script>document.getElementById("payment").submit();</script></main></body></html>`
	tmpl, err := template.New("ecpay-handoff").Parse(source)
	if err != nil {
		return nil, err
	}
	var output strings.Builder
	if err := tmpl.Execute(&output, struct {
		Language, Title, SiteTitle, Security, Heading, Progress, Copy string
		OrderLabel, AmountLabel, MethodLabel, ContinueText, BackText  string
		Action, OrderNumber, Amount, Method, BackURL                  string
		Fields                                                        map[string]string
	}{Language: lang, Title: title, SiteTitle: view.SiteTitle, Security: security, Heading: heading, Progress: progress, Copy: copy, OrderLabel: orderLabel, AmountLabel: amountLabel, MethodLabel: methodLabel, ContinueText: continueText, BackText: backText, Action: action, OrderNumber: view.OrderNumber, Amount: view.Amount, Method: view.Method, BackURL: view.BackURL, Fields: fields}); err != nil {
		return nil, err
	}
	return []byte(output.String()), nil
}

func shopSandboxPage(c echo.Context, tm *theme.Manager) error {
	tradeNo := c.Param("trade_no")
	if data.DB == nil || !merchantTradePattern.MatchString(tradeNo) || !sandboxEnabled(c.Request().Context()) {
		return c.NoContent(http.StatusNotFound)
	}
	attempt, order, err := loadShopPaymentAttempt(c, tradeNo, "sandbox")
	proof := c.QueryParam("t")
	if err != nil || !shopPaymentAuthorized(c, order, proof) || order.Status != core.OrderStatusPending || attempt.Status != "pending" || (order.ExpiresAt != nil && !order.ExpiresAt.After(time.Now().UTC())) {
		return c.NoContent(http.StatusNotFound)
	}
	if proof != "" {
		c.SetCookie(&http.Cookie{
			Name: sandboxAccessCookie, Value: proof,
			Path:     "/api/checkout/sandbox/" + tradeNo,
			HttpOnly: true, Secure: c.Request().TLS != nil, SameSite: http.SameSiteStrictMode,
			MaxAge: 10 * 60,
		})
	}
	setPrivatePaymentHeaders(c, "script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; font-src 'self'")
	language := shopRequestLanguage(c)
	pageTitle := "沙盒測試付款"
	if language == "en" {
		pageTitle = "Sandbox payment"
	}
	page, err := renderPublicTheme(c, tm, data.DB, "sandbox_pay.html", map[string]interface{}{
		"SiteTitle": getSiteTitle(), "Title": pageTitle, "CurrentPath": c.Path(),
		"Lang": language, "TradeNo": tradeNo, "OrderNumber": order.Number,
		"Amount": attempt.Amount, "Currency": attempt.Currency,
	})
	if err != nil {
		return c.NoContent(http.StatusInternalServerError)
	}
	return renderedPublicHTML(c, http.StatusOK, page)
}

func shopSandboxSimulate(c echo.Context) error {
	tradeNo := c.Param("trade_no")
	if data.DB == nil || !merchantTradePattern.MatchString(tradeNo) || !sandboxEnabled(c.Request().Context()) {
		return c.NoContent(http.StatusNotFound)
	}
	attempt, order, err := loadShopPaymentAttempt(c, tradeNo, "sandbox")
	if err != nil || order.Status != core.OrderStatusPending || attempt.Status != "pending" || (order.ExpiresAt != nil && !order.ExpiresAt.After(time.Now().UTC())) {
		return c.NoContent(http.StatusNotFound)
	}
	proof := ""
	if cookie, err := c.Cookie(sandboxAccessCookie); err == nil {
		proof = cookie.Value
	}
	if proof == "" {
		proof = c.QueryParam("t")
	}
	if !shopPaymentAuthorized(c, order, proof) {
		return c.NoContent(http.StatusNotFound)
	}
	var input struct {
		Status string `json:"status"`
	}
	if err := c.Bind(&input); err != nil || (input.Status != "paid" && input.Status != "failed" && input.Status != "pending") {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid payment status"})
	}
	service := payment.NewService(data.DB.WithContext(c.Request().Context()))
	gatewayValue, err := service.GetGateway("sandbox")
	if err != nil {
		return c.NoContent(http.StatusNotFound)
	}
	gateway, ok := gatewayValue.(*payment.SandboxGateway)
	if !ok {
		return c.NoContent(http.StatusNotFound)
	}
	nonce, err := newSandboxNonce()
	if err != nil {
		return c.NoContent(http.StatusInternalServerError)
	}
	signed, err := gateway.SignSimulation(tradeNo, input.Status, attempt.Amount, attempt.Currency, nonce)
	if err != nil {
		return c.NoContent(http.StatusBadRequest)
	}
	verified, err := service.HandleCallback(c.Request().Context(), "sandbox", signed)
	if err != nil || verified == nil {
		return c.NoContent(http.StatusBadRequest)
	}
	cb := payment.VerifiedCallback{
		Provider: "sandbox", MerchantTradeNo: tradeNo,
		Method: attempt.Method, Status: verified.Status,
		Amount: verified.Amount, Currency: verified.Currency,
		DedupeKey: verified.DedupeKey, Payload: verified.RawData,
	}
	err = data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		return payment.Finalize(tx, "sandbox", cb)
	})
	if err != nil {
		if errors.Is(err, payment.ErrCallbackNotFound) || errors.Is(err, payment.ErrCallbackMismatch) {
			return c.NoContent(http.StatusNotFound)
		}
		return c.NoContent(http.StatusInternalServerError)
	}
	redirect := "/checkout/done/" + url.PathEscape(order.Number)
	if proof != "" {
		redirect += "?t=" + url.QueryEscape(proof)
	}
	return c.JSON(http.StatusOK, map[string]string{"redirect_url": redirect})
}

func loadShopPaymentAttempt(c echo.Context, tradeNo, provider string) (core.Payment, core.Order, error) {
	var attempt core.Payment
	var order core.Order
	if err := data.DB.WithContext(c.Request().Context()).Where("merchant_trade_no = ? AND provider = ?", tradeNo, provider).First(&attempt).Error; err != nil {
		return attempt, order, err
	}
	if err := data.DB.WithContext(c.Request().Context()).First(&order, attempt.OrderID).Error; err != nil {
		return attempt, order, err
	}
	return attempt, order, nil
}

func shopPaymentAuthorized(c echo.Context, order core.Order, rawToken string) bool {
	if rawToken != "" && order.LookupTokenHash != "" {
		actual := shopTokenHash(rawToken)
		if len(actual) == len(order.LookupTokenHash) && subtle.ConstantTimeCompare([]byte(actual), []byte(order.LookupTokenHash)) == 1 {
			return true
		}
	}
	principal, err := auth.GetCurrentPrincipal(c)
	return err == nil && principal != nil && principal.User != nil && order.UserID != nil && *order.UserID == principal.User.ID
}

func shopCheckoutAccessTokenMatches(order core.Order, rawToken string) bool {
	if rawToken == "" || order.LookupTokenHash == "" || !order.CreatedAt.Add(90*24*time.Hour).After(time.Now().UTC()) {
		return false
	}
	hash := shopTokenHash(strings.TrimSpace(rawToken))
	return len(hash) == len(order.LookupTokenHash) && subtle.ConstantTimeCompare([]byte(hash), []byte(order.LookupTokenHash)) == 1
}

func shopPaymentNotFound(err error) bool {
	var httpErr *echo.HTTPError
	return errors.As(err, &httpErr) && httpErr.Code == http.StatusNotFound
}

func sandboxEnabled(ctx context.Context) bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("APP_ENV")), "production") || data.DB == nil {
		return false
	}
	service := payment.NewService(data.DB.WithContext(ctx))
	_, err := service.GetGateway("sandbox")
	return err == nil
}

func newSandboxNonce() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func setPrivatePaymentHeaders(c echo.Context, csp string) {
	header := c.Response().Header()
	header.Set(echo.HeaderCacheControl, "no-store")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy", "default-src 'none'; "+csp+"; base-uri 'none'; frame-ancestors 'none'")
}

func shopRequestLanguage(c echo.Context) string {
	if value, ok := c.Get("lang").(string); ok && strings.HasPrefix(strings.ToLower(value), "en") {
		return "en"
	}
	return "zh-TW"
}
