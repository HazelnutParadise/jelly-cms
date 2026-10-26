package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/labstack/echo/v4"
)

// Admin shop routes (docs/tickets/07-shop.md, 管理 API and 頁面). Product list,
// create, and update routes are registered by handlers_ecommerce.go; this file
// registers the additional shop administration APIs and pages below.
//
// APIs require the admin role unless their API documentation states otherwise.
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
// ignored. A top-level price string is in major units; a non-negative JSON
// integer is accepted as legacy minor units. Compare-at and variant prices
// remain major-unit strings. Top-level SKU and stock are optional legacy fields.
type productInput struct {
	Title             string                `json:"title"`
	Slug              string                `json:"slug"`
	Description       string                `json:"description"`
	Price             string                `json:"price"`
	SKU               *string               `json:"sku"`
	Stock             *int                  `json:"stock"`
	Kind              string                `json:"kind"`   // physical, digital
	Status            string                `json:"status"` // active, draft, archived
	CompareAtPrice    string                `json:"compare_at_price"`
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
	SEOIndex          *string               `json:"seo_index"`
	SEOAI             *string               `json:"seo_ai"`
}

// UnmarshalJSON accepts legacy top-level integer prices as minor units while
// keeping the parsed representation as the decimal string used by money.FromInput.
func (in *productInput) UnmarshalJSON(data []byte) error {
	type productInputAlias productInput
	var decoded struct {
		*productInputAlias
		Price json.RawMessage `json:"price"`
	}
	decoded.productInputAlias = (*productInputAlias)(in)
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if len(decoded.Price) == 0 || string(decoded.Price) == "null" {
		in.Price = ""
		return nil
	}
	if decoded.Price[0] == '"' {
		return json.Unmarshal(decoded.Price, &in.Price)
	}
	// JSON numeric prices are deliberately limited to non-negative base-10
	// integers. In particular, decimal and exponent notation is not accepted.
	raw := string(decoded.Price)
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return errors.New("price number must be a non-negative decimal integer")
		}
	}
	minor, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return errors.New("price number is out of range")
	}
	in.Price = fmt.Sprintf("%d.%02d", minor/100, minor%100)
	return nil
}

// productVariantInput is one variant of productInput.
type productVariantInput struct {
	ID             uint   `json:"id"`
	Name           string `json:"name"`
	SKU            string `json:"sku"`
	Price          string `json:"price"`
	CompareAtPrice string `json:"compare_at_price"`
	Stock          int    `json:"stock"`
	Position       int    `json:"position"`
	Status         string `json:"status"`
}

// registerShopAdminRoutes registers the admin shop APIs and pages.
func registerShopAdminRoutes(e *echo.Echo, renderAdmin func(c echo.Context, file, active string) error) {
	api := e.Group("/api/admin", auth.RequireRole(adminRoles...))

	api.POST("/products/:id/duplicate", adminProductsDuplicate)

	api.GET("/product-categories", adminProductCategoriesList)
	api.POST("/product-categories", adminProductCategoriesCreate)
	api.PUT("/product-categories/:id", adminProductCategoriesUpdate)
	api.DELETE("/product-categories/:id", adminProductCategoriesDelete)

	api.GET("/orders/:id", adminOrderGet)
	api.PUT("/orders/:id", adminOrderUpdateStatus)
	api.POST("/orders/:id/notes", adminOrderAddNote)
	api.PATCH("/orders/:id/address", adminOrderUpdateAddress)
	api.POST("/orders/:id/refund", adminOrderRefund)
	api.POST("/orders/:id/mark-paid", adminOrderMarkPaid)

	api.GET("/coupons", adminCouponsList)
	api.POST("/coupons", adminCouponCreate)
	api.GET("/coupons/:id", adminCouponGet)
	api.PATCH("/coupons/:id", adminCouponPatch)
	api.DELETE("/coupons/:id", adminCouponDelete)
	api.POST("/coupons/generate", adminCouponGenerate)
	api.POST("/coupons/preview", adminCouponPreview)

	api.GET("/shop/settings", adminShopSettingsGet)
	api.PUT("/shop/settings", adminShopSettingsPut)
	api.POST("/shop/payment/:gateway/test", adminShopPaymentTest)

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
