package server

import (
	"net/http"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	datapkg "github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

// Public shop routes (docs/tickets/07-shop.md, 契約). Everything here is
// registered before the /:slug catch-all.
//
// Pages (theme templates under web/themes/default):
//
//	GET /shop                          shop.html            Products, Categories, Term, Children, Breadcrumbs, Filters, Page, TotalPages, Total
//	GET /shop/c/:slug                  shop_category.html   same keys plus Term (category)
//	GET /shop/p/:slug                  product.html         Product, Variants, Category, Related, Trust, JSONLD
//	GET /cart                          cart.html
//	GET /checkout                      checkout.html        empty cart redirects to /cart
//	GET /checkout/done/:number         order_done.html      payment/member lookup integration
//	GET /checkout/sandbox/:trade_no    sandbox_pay.html     non-production sandbox integration
//	GET /orders/lookup                 order_lookup.html    member/lookup integration
//	GET /orders/view/:number           order_view.html      member/lookup integration
//	GET /account/*                     member integration
//
// /account itself is owned by registerAccountPages (account_pages.go) and is
// not registered here; ticket 07 changes its customer redirect later.
//
// Cart, quote, options and checkout are implemented here; the remaining
// payment and order lookup routes are wired by their owning integrations.
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
	e.GET("/api/shop", shopAPIOverview)
	e.GET("/api/shop/openapi.json", shopAPIOpenAPI)

	// Storefront assets are served from the currently active theme.
	e.GET("/theme/assets/*", shopThemeAssetHandler(tm))

	// Storefront
	e.GET("/shop", shopCatalogPage(tm))
	e.GET("/shop/c/:slug", shopCategoryPage(tm))
	e.GET("/shop/p/:slug", shopProductPage(tm))

	// Cart and checkout
	e.GET("/cart", shopPage(tm, "cart.html", "", nil))
	e.GET("/checkout", shopCheckoutPage(tm))

	// Cart API
	e.GET("/api/cart", shopCartGetHandler)
	e.POST("/api/cart/items", shopCartAddHandler)
	e.PATCH("/api/cart/items/:variant_id", shopCartUpdateHandler)
	e.DELETE("/api/cart/items/:variant_id", shopCartRemoveHandler)
	e.POST("/api/cart/coupon", shopCartCouponApplyHandler)
	e.DELETE("/api/cart/coupon", shopCartCouponRemoveHandler)

	// Shop options for the checkout form
	e.GET("/api/shop/shipping-methods", shopShippingMethodsHandler)
	e.GET("/api/shop/payment-methods", shopPaymentMethodsHandler)

	// Checkout and order API
	e.POST("/api/checkout", shopCheckoutHandler)

	// These routes are owned by the payment and member/lookup integrations.
	registerShopPaymentPages(e, tm)
	registerShopLookupRoutes(e, tm)
	registerShopAccountRoutes(e, tm)
}

// shopPage renders a theme template with shared site data. Keys from stub are merged
// into the page data; site, language, currency and contact details are always present.
func shopPage(tm *theme.Manager, template, title string, stub map[string]interface{}) echo.HandlerFunc {
	return func(c echo.Context) error {
		currency, err := publicOption(datapkg.DB, "shop_currency")
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "could not load shop currency")
		}
		if currency == "" {
			currency = "TWD"
		}
		contact, err := publicOption(datapkg.DB, "shop_contact_email")
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "could not load shop contact")
		}
		lang := shopLang(c)
		pageTitle := title
		if pageTitle == "" {
			if lang == "en" {
				pageTitle = "Cart"
			} else {
				pageTitle = "購物車"
			}
		}
		data := map[string]interface{}{
			"SiteTitle":    getSiteTitle(),
			"Title":        pageTitle,
			"CurrentPath":  c.Path(),
			"Lang":         lang,
			"Currency":     currency,
			"ContactEmail": contact,
		}
		for key, value := range stub {
			data[key] = value
		}
		applyPublicSEO(c, datapkg.DB, template, data)
		html, err := tm.RenderWithDB(template, data, datapkg.DB)
		if err != nil {
			return c.String(http.StatusInternalServerError, "Template render error: "+err.Error())
		}
		return renderedPublicHTML(c, http.StatusOK, html)
	}
}
