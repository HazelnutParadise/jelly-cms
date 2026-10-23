package server

import (
	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/labstack/echo/v4"
)

// Admin shop routes (docs/tickets/07-shop.md, 管理 API and 頁面). Skeleton: every
// new API answers 501 through notImplemented. The existing GET and POST
// handlers in handlers_ecommerce.go (/api/admin/products, /api/admin/orders)
// stay as they are until the ticket-07 versions replace them, so this file
// does not register those paths.
//
// APIs (role admin; GET may later use 06's orders_read token scope):
//
//	POST   /api/admin/products/:id/duplicate
//	GET    /api/admin/product-categories
//	POST   /api/admin/product-categories
//	PUT    /api/admin/product-categories/:id
//	DELETE /api/admin/product-categories/:id
//	GET    /api/admin/orders/:id
//	PUT    /api/admin/orders/:id                  {status, carrier, tracking_no, note}
//	POST   /api/admin/orders/:id/notes
//	PATCH  /api/admin/orders/:id/address
//	POST   /api/admin/orders/:id/refund           {amount?, reason, restock}
//	POST   /api/admin/orders/:id/mark-paid        bank transfer only
//	GET    /api/admin/coupons                     ?status=active|scheduled|ended&q=
//	POST   /api/admin/coupons
//	GET    /api/admin/coupons/:id
//	PATCH  /api/admin/coupons/:id
//	DELETE /api/admin/coupons/:id
//	POST   /api/admin/coupons/generate            {prefix, size, template}
//	POST   /api/admin/coupons/preview             {code, cart}
//	GET    /api/admin/shop/settings               masked secrets, never written
//	PUT    /api/admin/shop/settings
//	POST   /api/admin/shop/payment/:gateway/test
//
// Pages (theme-free, shared admin layout):
//
//	GET /admin/orders/:id    order_detail.html
//	GET /admin/coupons       coupons.html
//
// /admin/orders itself stays in routes.go (orders.html is rewritten later).

// productInput is the typed body of POST and PUT /api/admin/products (ticket 07,
// E2). It has no id or deleted_at field, so those values in a request are
// ignored. Prices and stock are never negative; the database checks stock too.
// Skeleton: the handlers that use it are not written yet.
type productInput struct {
	Title             string                `json:"title"`
	Slug              string                `json:"slug"`
	Description       string                `json:"description"`
	Kind              string                `json:"kind"`   // physical, digital
	Status            string                `json:"status"` // active, draft, archived
	CompareAtPrice    int64                 `json:"compare_at_price"`
	LowStockThreshold *int                  `json:"low_stock_threshold"`
	RequiresShipping  *bool                 `json:"requires_shipping"`
	ColdChain         bool                  `json:"cold_chain"`
	Images            []string              `json:"images"`
	CategoryIDs       []uint                `json:"categories"`
	Variants          []productVariantInput `json:"variants"`
	Attributes        map[string]string     `json:"attributes"`
	RelatedIDs        []uint                `json:"related_ids"`
	SEOTitle          string                `json:"seo_title"`
	SEODescription    string                `json:"seo_description"`
}

// productVariantInput is one variant of productInput.
type productVariantInput struct {
	Name           string `json:"name"`
	SKU            string `json:"sku"`
	Price          int64  `json:"price"`
	CompareAtPrice int64  `json:"compare_at_price"`
	Stock          int    `json:"stock"`
	Position       int    `json:"position"`
	Status         string `json:"status"`
}

// registerShopAdminRoutes registers the admin shop APIs and pages.
func registerShopAdminRoutes(e *echo.Echo, renderAdmin func(c echo.Context, file, active string) error) {
	api := e.Group("/api/admin", auth.RequireRole("admin"))

	api.POST("/products/:id/duplicate", notImplemented)

	api.GET("/product-categories", notImplemented)
	api.POST("/product-categories", notImplemented)
	api.PUT("/product-categories/:id", notImplemented)
	api.DELETE("/product-categories/:id", notImplemented)

	api.GET("/orders/:id", notImplemented)
	api.PUT("/orders/:id", notImplemented)
	api.POST("/orders/:id/notes", notImplemented)
	api.PATCH("/orders/:id/address", notImplemented)
	api.POST("/orders/:id/refund", notImplemented)
	api.POST("/orders/:id/mark-paid", notImplemented)

	api.GET("/coupons", notImplemented)
	api.POST("/coupons", notImplemented)
	api.GET("/coupons/:id", notImplemented)
	api.PATCH("/coupons/:id", notImplemented)
	api.DELETE("/coupons/:id", notImplemented)
	api.POST("/coupons/generate", notImplemented)
	api.POST("/coupons/preview", notImplemented)

	api.GET("/shop/settings", notImplemented)
	api.PUT("/shop/settings", notImplemented)
	api.POST("/shop/payment/:gateway/test", notImplemented)

	pages := e.Group("/admin", auth.RequireAuth)
	pages.GET("/orders/:id", func(c echo.Context) error {
		return renderAdmin(c, "order_detail.html", "orders")
	})
	pages.GET("/coupons", func(c echo.Context) error {
		return renderAdmin(c, "coupons.html", "coupons")
	})
}

func init() {
	adminOnly := []string{"admin"}
	RegisterNav(
		NavItem{Key: "coupons", Section: "shop", Label: "coupons", Icon: "i-tag", Href: "/admin/coupons", Order: 20, Roles: adminOnly},
	)
	RegisterPaletteCommands(
		PaletteCommand{Key: "new-coupon", Label: "newCoupon", Keywords: []string{"coupon", "discount", "折扣碼", "優惠碼"}, Icon: "i-tag", Href: "/admin/coupons#new", Roles: adminOnly},
	)
}
