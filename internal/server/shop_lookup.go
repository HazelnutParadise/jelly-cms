package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/notify"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const shopLookupResponseMessage = "如果訂單存在，我們將寄送查詢連結至您的電子郵件。"

var shopLookupGlobalLimits = struct {
	sync.Mutex
	entries map[string]shopRateWindow
}{entries: map[string]shopRateWindow{}}

func registerShopLookupRoutes(e *echo.Echo, tm *theme.Manager) {
	e.GET("/orders/lookup", func(c echo.Context) error {
		title := "查詢訂單"
		if shopLang(c) == "en" {
			title = "Find your order"
		}
		return shopPage(tm, "order_lookup.html", title, nil)(c)
	})
	e.GET("/orders/view", shopOrderViewExchangeHandler(tm))
	e.GET("/orders/view/:number", shopOrderViewPageHandler(tm))
	e.POST("/api/orders/lookup", shopOrderLookupAPI)
	e.GET("/api/orders/:number/status", shopOrderStatusAPI)
	e.POST("/api/orders/:number/resend", shopOrderResendAPI)
}

func shopNewLookupRaw() (string, string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	raw := hex.EncodeToString(buf)
	return raw, shopTokenHash(raw), nil
}

func shopOrderLookupAPI(c echo.Context) error {
	if !shopValidOrigin(c) {
		return echo.NewHTTPError(http.StatusForbidden, "invalid origin")
	}
	if shopRateLimit(c, "order_lookup_ip", 29, time.Minute) {
		return shopRateLimited(c)
	}
	var input struct {
		Number  string `json:"number"`
		Email   string `json:"email"`
		Website string `json:"website"`
	}
	if err := shopDecodeJSON(c, &input); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request")
	}
	if strings.TrimSpace(input.Website) != "" {
		return c.JSON(http.StatusAccepted, map[string]string{"message": shopLookupResponseMessage})
	}
	number := strings.TrimSpace(input.Number)
	if number != "" && shopLookupGlobalRateLimit("lookup-number:"+shopLookupGlobalKey(number), 29, time.Minute) {
		return shopRateLimited(c)
	}
	response := map[string]string{"message": shopLookupResponseMessage}
	if number == "" || !shopValidEmail(input.Email) {
		return c.JSON(http.StatusAccepted, response)
	}
	var order core.Order
	err := data.DB.Where("number = ? AND lower(email) = ?", number, strings.ToLower(strings.TrimSpace(input.Email))).First(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.JSON(http.StatusAccepted, response)
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not process order lookup")
	}
	raw, hash, err := shopNewLookupRaw()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not create lookup token")
	}
	token := core.OrderAccessToken{OrderID: order.ID, TokenHash: hash, Kind: "lookup", ExpiresAt: time.Now().UTC().Add(15 * time.Minute), CreatedIPMasked: shopMaskedIP(c.RealIP())}
	if err := data.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&token).Error; err != nil {
			return err
		}
		return emitShopLookupLink(c, tx, order, raw)
	}); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not queue lookup email")
	}
	return c.JSON(http.StatusAccepted, response)
}

func emitShopLookupLink(c echo.Context, tx *gorm.DB, order core.Order, raw string) error {
	return notify.Emit(c.Request().Context(), tx, notify.Event{
		Kind: "order.lookup_link", Category: notify.CategoryOrders,
		Params:     map[string]any{"number": order.Number, "url": "/orders/view?t=" + url.QueryEscape(raw)},
		Recipients: notify.Recipients{Emails: []string{order.Email}},
	})
}

func shopLookupGlobalRateLimit(key string, limit int, window time.Duration) bool {
	now := time.Now()
	shopLookupGlobalLimits.Lock()
	defer shopLookupGlobalLimits.Unlock()
	for candidate, entry := range shopLookupGlobalLimits.entries {
		if now.Sub(entry.start) > 2*shopLookupWindowForKey(candidate) {
			delete(shopLookupGlobalLimits.entries, candidate)
		}
	}
	entry := shopLookupGlobalLimits.entries[key]
	if entry.start.IsZero() || now.Sub(entry.start) >= window {
		shopLookupGlobalLimits.entries[key] = shopRateWindow{start: now, count: 1}
		return false
	}
	entry.count++
	shopLookupGlobalLimits.entries[key] = entry
	return entry.count > limit
}

func shopLookupWindowForKey(key string) time.Duration {
	if strings.HasPrefix(key, "resend-order:") {
		return time.Hour
	}
	return time.Minute
}

func shopRateLimited(c echo.Context) error {
	return c.JSON(http.StatusTooManyRequests, map[string]string{"error": "rate_limit"})
}

func shopOrderViewExchangeHandler(tm *theme.Manager) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		raw := strings.TrimSpace(c.QueryParam("t"))
		if raw == "" {
			return echo.NewHTTPError(http.StatusNotFound)
		}
		hash := shopTokenHash(raw)
		now := time.Now().UTC()
		var order core.Order
		viewRaw := ""
		invalid := false
		err := data.DB.Transaction(func(tx *gorm.DB) error {
			var token core.OrderAccessToken
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("token_hash = ? AND kind = ?", hash, "lookup").First(&token).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					invalid = true
					return nil
				}
				return err
			}
			if token.UsedAt != nil || !token.ExpiresAt.After(now) {
				invalid = true
				return nil
			}
			if err := tx.First(&order, token.OrderID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					invalid = true
					return nil
				}
				return err
			}
			used := now
			result := tx.Model(&core.OrderAccessToken{}).Where("id = ? AND used_at IS NULL AND expires_at > ?", token.ID, now).Update("used_at", used)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				invalid = true
				return nil
			}
			var err error
			viewRaw, _, err = shopNewLookupRaw()
			if err != nil {
				return err
			}
			view := core.OrderAccessToken{OrderID: order.ID, TokenHash: shopTokenHash(viewRaw), Kind: "view", ExpiresAt: now.Add(30 * time.Minute), UsedAt: &used, CreatedIPMasked: shopMaskedIP(c.RealIP())}
			return tx.Create(&view).Error
		})
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "could not exchange lookup token")
		}
		if invalid || order.ID == 0 || viewRaw == "" {
			return echo.NewHTTPError(http.StatusNotFound)
		}
		cookie := &http.Cookie{Name: shopOrderViewCookieName, Value: viewRaw, Path: "/orders/view", HttpOnly: true, Secure: c.IsTLS() || strings.EqualFold(os.Getenv("APP_ENV"), "production"), SameSite: http.SameSiteLaxMode, MaxAge: 1800, Expires: now.Add(30 * time.Minute)}
		apiCookie := *cookie
		apiCookie.Path = "/api/orders"
		c.SetCookie(cookie)
		c.SetCookie(&apiCookie)
		return c.Redirect(http.StatusFound, "/orders/view/"+url.PathEscape(order.Number))
	}
}

func shopOrderViewPageHandler(tm *theme.Manager) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		order, err := shopAuthorizedOrder(c, c.Param("number"), "")
		if err != nil {
			if httpErr, ok := err.(*echo.HTTPError); ok && httpErr.Code == http.StatusNotFound {
				return echo.NewHTTPError(http.StatusNotFound)
			}
			return echo.NewHTTPError(http.StatusInternalServerError, "could not authorize order view")
		}
		view, err := shopOrderTemplateView(data.DB, order)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "could not load order view")
		}
		title := "訂單"
		if shopLang(c) == "en" {
			title = "Order"
		}
		return shopPage(tm, "order_view.html", title, map[string]any{"Order": view})(c)
	}
}

func shopOrderStatusAPI(c echo.Context) error {
	c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	access := c.QueryParam("access")
	if access == "" {
		access = c.QueryParam("t")
	}
	order, err := shopAuthorizedOrder(c, c.Param("number"), access)
	if err != nil {
		if httpErr, ok := err.(*echo.HTTPError); ok && httpErr.Code == http.StatusNotFound {
			return echo.NewHTTPError(http.StatusNotFound)
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not load order status")
	}
	var payment core.Payment
	paymentStatus := ""
	err = data.DB.Where("order_id = ?", order.ID).Order("id desc").First(&payment).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not load order status")
	}
	if err == nil {
		paymentStatus = payment.Status
	} else if order.TotalAmount == 0 && order.PaymentProvider == "none" && order.PaymentMethod == "none" {
		paymentStatus = "none"
	}
	return c.JSON(http.StatusOK, map[string]string{"status": order.Status, "number": order.Number, "payment_status": paymentStatus})
}

func shopOrderResendAPI(c echo.Context) error {
	c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	if !shopValidOrigin(c) {
		return echo.NewHTTPError(http.StatusForbidden, "invalid origin")
	}
	var input struct {
		Access string `json:"access"`
		Email  string `json:"email"`
	}
	if c.Request().Body != nil && c.Request().ContentLength != 0 {
		if err := shopDecodeJSON(c, &input); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid request")
		}
	}
	if input.Access == "" {
		input.Access = c.QueryParam("access")
	}
	order, err := shopAuthorizedOrder(c, c.Param("number"), input.Access)
	if err != nil {
		if httpErr, ok := err.(*echo.HTTPError); ok && httpErr.Code == http.StatusNotFound {
			return echo.NewHTTPError(http.StatusNotFound)
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not authorize order")
	}
	if shopRateLimit(c, "order_resend_ip", 3, time.Hour) || shopLookupGlobalRateLimit("resend-order:"+shopLookupGlobalKey(order.Number), 3, time.Hour) {
		return shopRateLimited(c)
	}
	raw, hash, err := shopNewLookupRaw()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not create lookup token")
	}
	token := core.OrderAccessToken{OrderID: order.ID, TokenHash: hash, Kind: "lookup", ExpiresAt: time.Now().UTC().Add(15 * time.Minute), CreatedIPMasked: shopMaskedIP(c.RealIP())}
	if err := data.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&token).Error; err != nil {
			return err
		}
		return emitShopLookupLink(c, tx, *order, raw)
	}); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not queue lookup email")
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func shopLookupGlobalKey(value string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(value))))
	return hex.EncodeToString(digest[:])
}
