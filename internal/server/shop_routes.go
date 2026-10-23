package server

import (
	"net/http"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	datapkg "github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

// Public shop routes (docs/tickets/07-shop.md, 契約). Skeleton: pages render
// a placeholder template with stub data; JSON APIs answer 501 through
// notImplemented. Everything here is registered before the /:slug catch-all.
//
// Pages (theme templates under web/themes/default):
//
//	GET /shop                          shop.html            Products, Categories, Term, Children, Breadcrumbs, Filters, Page, TotalPages, Total
//	GET /shop/c/:slug                  shop_category.html   same keys plus Term (category)
//	GET /shop/p/:slug                  product.html         Product, Variants, Category, Related, Trust, JSONLD
//	GET /cart                          cart.html
//	GET /checkout                      checkout.html        (empty cart redirects to /cart later)
//	GET /checkout/done/:number         order_done.html      needs the buyer's session or ?t= token
//	GET /checkout/sandbox/:trade_no    sandbox_pay.html     sandbox only (not production)
//	GET /orders/lookup                 order_lookup.html
//	GET /orders/view                   order_view.html      ?t= token, then 302 to /orders/view/:number
//	GET /orders/view/:number           order_view.html
//	GET /account/orders                account_orders.html  member only
//	GET /account/orders/:number        account_order.html   member only
//	GET /account/addresses             account_addresses.html
//	GET /account/invoices              account_invoices.html
//	GET /account/security              account_security.html      account tabs reuse 06 /api/account/*
//	GET /account/notifications         account_notifications.html reuse 06 /api/account/*
//
// /account itself is owned by registerAccountPages (account_pages.go) and is
// not registered here; ticket 07 changes its customer redirect later.
//
// JSON APIs (501 for now):
//
//	GET    /api/cart
//	POST   /api/cart/items                      {variant_id, qty}
//	PATCH  /api/cart/items/:variant_id          {qty}
//	DELETE /api/cart/items/:variant_id
//	POST   /api/cart/coupon                     {code}
//	DELETE /api/cart/coupon
//	GET    /api/shop/shipping-methods
//	GET    /api/shop/payment-methods
//	POST   /api/checkout                        {email, phone, create_account, shipping, payment, invoice, coupon_code, accept_terms, idempotency_key, website}
//	POST   /api/checkout/retry                  {number, access, method}
//	GET    /api/orders/:number/status
//	POST   /api/orders/lookup                   {number, email, website}
//	POST   /api/orders/:number/resend
//	GET    /api/account/orders                  ?status=&page=
//	GET    /api/account/orders/:number
//	POST   /api/account/orders/:number/cancel
//	POST   /api/account/orders/:number/reorder
//	GET    /api/account/addresses
//	POST   /api/account/addresses
//	PUT    /api/account/addresses/:id
//	DELETE /api/account/addresses/:id
//	GET    /api/account/invoice-profiles
//	POST   /api/account/invoice-profiles
//	PUT    /api/account/invoice-profiles/:id
//	DELETE /api/account/invoice-profiles/:id
//
// Payment notification routes are not registered here: RegisterPaymentRoutes
// owns POST /api/payment/:gateway/notify (alias /callback) and GET
// /api/payment/:gateway/return, and it already runs before this file.

// shopCategoryCount is a category with its number of visible products, as the
// category listing shows it.
type shopCategoryCount struct {
	core.ProductCategory
	Count int64
}

// shopFilters is the active filter state of a listing page (?sort=, ?price=,
// ?instock=, ?attr=).
type shopFilters struct {
	Sort    string
	Price   string
	InStock bool
	Attr    string
}

// registerShopRoutes registers the public storefront, cart, checkout, order
// lookup and member pages, and the JSON APIs that belong to them.
func registerShopRoutes(e *echo.Echo, tm *theme.Manager) {
	// Storefront
	e.GET("/shop", shopPage(tm, "shop.html", "商店", map[string]interface{}{
		"Products":    []core.Product{},
		"Categories":  []shopCategoryCount{},
		"Children":    []core.ProductCategory{},
		"Breadcrumbs": []core.ProductCategory{},
		"Filters":     shopFilters{},
		"Page":        1,
		"TotalPages":  0,
		"Total":       int64(0),
	}))
	e.GET("/shop/c/:slug", shopPage(tm, "shop_category.html", "商店分類", map[string]interface{}{
		"Products":    []core.Product{},
		"Categories":  []shopCategoryCount{},
		"Term":        (*core.ProductCategory)(nil),
		"Children":    []core.ProductCategory{},
		"Breadcrumbs": []core.ProductCategory{},
		"Filters":     shopFilters{},
		"Page":        1,
		"TotalPages":  0,
		"Total":       int64(0),
	}))
	e.GET("/shop/p/:slug", shopPage(tm, "product.html", "商品", map[string]interface{}{
		"Product":  (*core.Product)(nil),
		"Variants": []core.ProductVariant{},
		"Category": (*core.ProductCategory)(nil),
		"Related":  []core.Product{},
		"Trust":    nil,
		"JSONLD":   "",
	}))

	// Cart and checkout
	e.GET("/cart", shopPage(tm, "cart.html", "購物車", nil))
	e.GET("/checkout", shopPage(tm, "checkout.html", "結帳", nil))
	e.GET("/checkout/done/:number", shopPage(tm, "order_done.html", "訂單完成", map[string]interface{}{
		"Order": (*core.Order)(nil),
		"Items": []core.OrderItem{},
	}))
	e.GET("/checkout/sandbox/:trade_no", shopPage(tm, "sandbox_pay.html", "沙盒付款", map[string]interface{}{
		"TradeNo": "",
	}))

	// Order lookup and view (token links)
	e.GET("/orders/lookup", shopPage(tm, "order_lookup.html", "查詢訂單", nil))
	e.GET("/orders/view", shopPage(tm, "order_view.html", "訂單", map[string]interface{}{
		"Order": (*core.Order)(nil),
	}))
	e.GET("/orders/view/:number", shopPage(tm, "order_view.html", "訂單", map[string]interface{}{
		"Order": (*core.Order)(nil),
	}))

	// Member pages (customer and staff without a shop role are sent elsewhere
	// once the account redirect lands)
	e.GET("/account/orders", auth.RequireAuth(shopPage(tm, "account_orders.html", "我的訂單", map[string]interface{}{
		"Orders":     []core.Order{},
		"Counts":     map[string]int64{},
		"Status":     "",
		"Page":       1,
		"TotalPages": 0,
	})))
	e.GET("/account/orders/:number", auth.RequireAuth(shopPage(tm, "account_order.html", "訂單詳情", map[string]interface{}{
		"Order": (*core.Order)(nil),
	})))
	e.GET("/account/addresses", auth.RequireAuth(shopPage(tm, "account_addresses.html", "地址", map[string]interface{}{
		"Addresses": []core.Address{},
	})))
	e.GET("/account/invoices", auth.RequireAuth(shopPage(tm, "account_invoices.html", "發票設定", map[string]interface{}{
		"Profiles": []core.InvoiceProfile{},
	})))
	e.GET("/account/security", auth.RequireAuth(shopPage(tm, "account_security.html", "帳號與密碼", nil)))
	e.GET("/account/notifications", auth.RequireAuth(shopPage(tm, "account_notifications.html", "通知", nil)))

	// Cart API
	e.GET("/api/cart", notImplemented)
	e.POST("/api/cart/items", notImplemented)
	e.PATCH("/api/cart/items/:variant_id", notImplemented)
	e.DELETE("/api/cart/items/:variant_id", notImplemented)
	e.POST("/api/cart/coupon", notImplemented)
	e.DELETE("/api/cart/coupon", notImplemented)

	// Shop options for the checkout form
	e.GET("/api/shop/shipping-methods", notImplemented)
	e.GET("/api/shop/payment-methods", notImplemented)

	// Checkout and order API
	e.POST("/api/checkout", notImplemented)
	e.POST("/api/checkout/retry", notImplemented)
	e.GET("/api/orders/:number/status", notImplemented)
	e.POST("/api/orders/lookup", notImplemented)
	e.POST("/api/orders/:number/resend", notImplemented)

	// Member API (only the member's own orders, addresses and invoice profiles)
	account := e.Group("/api/account", auth.RequireAuth)
	account.GET("/orders", notImplemented)
	account.GET("/orders/:number", notImplemented)
	account.POST("/orders/:number/cancel", notImplemented)
	account.POST("/orders/:number/reorder", notImplemented)
	account.GET("/addresses", notImplemented)
	account.POST("/addresses", notImplemented)
	account.PUT("/addresses/:id", notImplemented)
	account.DELETE("/addresses/:id", notImplemented)
	account.GET("/invoice-profiles", notImplemented)
	account.POST("/invoice-profiles", notImplemented)
	account.PUT("/invoice-profiles/:id", notImplemented)
	account.DELETE("/invoice-profiles/:id", notImplemented)
}

// shopPage renders a theme template with stub data. Keys from stub are merged
// into the page data; SiteTitle, Title and CurrentPath are always present.
// Skeleton: the real handlers (ticket 07, E3 to E7) replace the stub values.
func shopPage(tm *theme.Manager, template, title string, stub map[string]interface{}) echo.HandlerFunc {
	return func(c echo.Context) error {
		data := map[string]interface{}{
			"SiteTitle":   getSiteTitle(),
			"Title":       title,
			"CurrentPath": c.Path(),
		}
		for key, value := range stub {
			data[key] = value
		}
		html, err := tm.RenderWithDB(template, data, datapkg.DB)
		if err != nil {
			return c.String(http.StatusInternalServerError, "Template render error: "+err.Error())
		}
		return c.HTMLBlob(http.StatusOK, html)
	}
}
