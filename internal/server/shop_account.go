package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// registerShopAccountRoutes registers member-only shop pages and APIs.
func registerShopAccountRoutes(e *echo.Echo, tm *theme.Manager) {
	page := func(templateName, title string) echo.HandlerFunc {
		return func(c echo.Context) error {
			u := shopPrincipalUser(c)
			if u == nil {
				return echo.NewHTTPError(http.StatusUnauthorized)
			}
			pageTitle := shopAccountPageTitle(c, templateName, title)
			return shopPage(tm, templateName, pageTitle, map[string]interface{}{"Member": map[string]string{"Name": accountMemberName(u), "Email": u.Email}})(c)
		}
	}
	member := func(next echo.HandlerFunc) echo.HandlerFunc {
		return auth.RequireAuth(func(c echo.Context) error {
			c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
			return next(c)
		})
	}
	e.GET("/account/orders", member(shopMemberPage(tm, "account_orders.html", "我的訂單")))
	e.GET("/account/orders/:number", member(shopMemberOrderPage(tm)))
	e.GET("/account/addresses", member(page("account_addresses.html", "常用地址")))
	e.GET("/account/invoices", member(page("account_invoices.html", "發票設定")))
	e.GET("/account/security", member(page("account_security.html", "帳號安全")))
	e.GET("/account/notifications", member(page("account_notifications.html", "通知設定")))
	e.GET("/api/account/orders", member(shopMemberOrdersAPI))
	e.GET("/api/account/orders/:number", member(shopMemberOrderAPI))
	e.POST("/api/account/orders/:number/cancel", member(shopMemberCancelAPI))
	e.POST("/api/account/orders/:number/reorder", member(shopMemberReorderAPI))
	e.GET("/api/account/addresses", member(shopMemberAddressesAPI))
	e.POST("/api/account/addresses", member(shopMemberAddressCreateAPI))
	e.PUT("/api/account/addresses/:id", member(shopMemberAddressUpdateAPI))
	e.DELETE("/api/account/addresses/:id", member(shopMemberAddressDeleteAPI))
	e.GET("/api/account/invoice-profiles", member(shopMemberInvoicesAPI))
	e.POST("/api/account/invoice-profiles", member(shopMemberInvoiceCreateAPI))
	e.PUT("/api/account/invoice-profiles/:id", member(shopMemberInvoiceUpdateAPI))
	e.DELETE("/api/account/invoice-profiles/:id", member(shopMemberInvoiceDeleteAPI))
}

func shopMemberPage(tm *theme.Manager, templateName, title string) echo.HandlerFunc {
	return func(c echo.Context) error {
		user := shopPrincipalUser(c)
		if user == nil {
			return echo.NewHTTPError(http.StatusUnauthorized)
		}
		dataPage := map[string]any{"Orders": []any{}, "Counts": map[string]int64{}, "Status": c.QueryParam("status"), "Page": 1, "TotalPages": 1, "Member": map[string]string{"Name": accountMemberName(user), "Email": user.Email}}
		rows, counts, pageNo, pages, err := shopMemberOrderRows(data.DB, user.ID, c.QueryParam("status"), c.QueryParam("page"))
		if err != nil {
			return echo.NewHTTPError(500, "could not load account orders")
		}
		dataPage["Orders"], dataPage["Counts"], dataPage["Page"], dataPage["TotalPages"] = rows, counts, pageNo, pages
		return shopPage(tm, templateName, shopAccountPageTitle(c, templateName, title), dataPage)(c)
	}
}

func shopMemberOrderRows(db *gorm.DB, userID uint, status, pageRaw string) ([]map[string]any, map[string]int64, int, int, error) {
	counts := map[string]int64{"all": 0}
	base := db.Model(&core.Order{}).Where("user_id = ?", userID)
	var all int64
	if err := base.Count(&all).Error; err != nil {
		return nil, nil, 0, 0, err
	}
	counts["all"] = all
	for _, s := range []string{core.OrderStatusPending, core.OrderStatusPaid, core.OrderStatusShipped, core.OrderStatusCompleted, core.OrderStatusCancelled, core.OrderStatusExpired, core.OrderStatusRefunded} {
		var n int64
		if err := db.Model(&core.Order{}).Where("user_id = ? AND status = ?", userID, s).Count(&n).Error; err != nil {
			return nil, nil, 0, 0, err
		}
		counts[s] = n
	}
	pageNo, _ := strconv.Atoi(pageRaw)
	if pageNo < 1 {
		pageNo = 1
	}
	if pageNo > 100000 {
		pageNo = 100000
	}
	query := db.Where("user_id = ?", userID)
	if status != "" && status != "all" {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Model(&core.Order{}).Count(&total).Error; err != nil {
		return nil, nil, 0, 0, err
	}
	pages := int((total + 19) / 20)
	if pages < 1 {
		pages = 1
	}
	if pageNo > pages {
		pageNo = pages
	}
	var orders []core.Order
	if err := query.Order("created_at DESC, id DESC").Offset((pageNo - 1) * 20).Limit(20).Find(&orders).Error; err != nil {
		return nil, nil, 0, 0, err
	}
	orderIDs := make([]uint, 0, len(orders))
	for _, order := range orders {
		orderIDs = append(orderIDs, order.ID)
	}
	grouped := map[uint][]core.OrderItem{}
	if len(orderIDs) > 0 {
		var allItems []core.OrderItem
		if err := db.Where("order_id IN ?", orderIDs).Order("order_id, id").Find(&allItems).Error; err != nil {
			return nil, nil, 0, 0, err
		}
		for _, item := range allItems {
			grouped[item.OrderID] = append(grouped[item.OrderID], item)
		}
	}
	views := make([]map[string]any, 0, len(orders))
	for _, order := range orders {
		items := grouped[order.ID]
		summary, images := "", []string{}
		for i, item := range items {
			title := item.Title
			if title == "" {
				title = item.SKU
			}
			if title == "" {
				title = "商品"
			}
			if i < 3 {
				if summary != "" {
					summary += "、"
				}
				summary += title
			}
			if item.Image != "" && len(images) < 3 {
				images = append(images, item.Image)
			}
		}
		views = append(views, map[string]any{"Number": order.Number, "CreatedAt": shopOrderLocalTime(order.CreatedAt), "Status": order.Status, "Total": order.TotalAmount, "ItemCount": len(items), "Summary": summary, "Images": images})
	}
	return views, counts, pageNo, pages, nil
}

func shopMemberOrdersAPI(c echo.Context) error {
	u := shopPrincipalUser(c)
	if u == nil {
		return echo.NewHTTPError(401)
	}
	items, counts, pageNo, pages, err := shopMemberOrderRows(data.DB, u.ID, c.QueryParam("status"), c.QueryParam("page"))
	if err != nil {
		return echo.NewHTTPError(500)
	}
	return c.JSON(200, map[string]any{"items": items, "counts": counts, "status": c.QueryParam("status"), "page": pageNo, "total_pages": pages})
}

func shopOwnOrder(c echo.Context) (*core.Order, error) {
	u := shopPrincipalUser(c)
	if u == nil {
		return nil, echo.NewHTTPError(401)
	}
	var order core.Order
	err := data.DB.Where("number = ? AND user_id = ?", strings.TrimSpace(c.Param("number")), u.ID).First(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, echo.NewHTTPError(404)
	}
	if err != nil {
		return nil, err
	}
	return &order, nil
}
func shopMemberOrderAPI(c echo.Context) error {
	order, err := shopOwnOrder(c)
	if err != nil {
		if he, ok := err.(*echo.HTTPError); ok {
			return he
		}
		return echo.NewHTTPError(500)
	}
	view, err := shopOrderTemplateView(data.DB, order)
	if err != nil {
		return echo.NewHTTPError(500)
	}
	return c.JSON(200, map[string]any{"order": view})
}
func shopMemberOrderPage(tm *theme.Manager) echo.HandlerFunc {
	return func(c echo.Context) error {
		order, err := shopOwnOrder(c)
		if err != nil {
			if he, ok := err.(*echo.HTTPError); ok {
				return he
			}
			return echo.NewHTTPError(500)
		}
		view, err := shopOrderTemplateView(data.DB, order)
		if err != nil {
			return echo.NewHTTPError(500)
		}
		u := shopPrincipalUser(c)
		return shopPage(tm, "account_order.html", shopAccountPageTitle(c, "account_order.html", "訂單"), map[string]any{"Order": view, "Member": map[string]string{"Name": accountMemberName(u), "Email": u.Email}})(c)
	}
}

func accountMemberName(user *core.User) string {
	if user == nil {
		return ""
	}
	if strings.TrimSpace(user.DisplayName) != "" {
		return user.DisplayName
	}
	return user.Username
}

func shopAccountPageTitle(c echo.Context, templateName, fallback string) string {
	if shopLang(c) != "en" {
		return fallback
	}
	switch templateName {
	case "account_orders.html":
		return "My orders"
	case "account_order.html":
		return "Order details"
	case "account_addresses.html":
		return "Saved addresses"
	case "account_invoices.html":
		return "Invoice settings"
	case "account_security.html":
		return "Account security"
	case "account_notifications.html":
		return "Notifications"
	default:
		return fallback
	}
}

func shopMemberCancelAPI(c echo.Context) error {
	if !shopValidOrigin(c) {
		return echo.NewHTTPError(403)
	}
	user := shopPrincipalUser(c)
	if user == nil {
		return echo.NewHTTPError(401)
	}
	result := ""
	err := data.DB.Transaction(func(tx *gorm.DB) error {
		var order core.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("number = ? AND user_id = ?", strings.TrimSpace(c.Param("number")), user.ID).First(&order).Error; err != nil {
			return err
		}
		zeroPaymentOrder := order.TotalAmount == 0 && order.PaymentProvider == "none" && order.PaymentMethod == "none"
		if order.Status == core.OrderStatusPaid && !zeroPaymentOrder && order.PaymentProvider != "" && order.PaymentProvider != "sandbox" && order.PaymentProvider != "manual_transfer" {
			return errShopUnsupportedRefund
		}
		if order.Status == core.OrderStatusPaid {
			if zeroPaymentOrder {
				if err := core.NewOrderService(tx).Transition(tx, order.ID, core.OrderStatusPaid, core.OrderStatusCancelled, &user.ID, "customer cancellation of no-payment order"); err != nil {
					return err
				}
				if err := core.Restock(tx, order.ID); err != nil {
					return err
				}
				if err := core.ReleaseRedemption(tx, order.ID); err != nil {
					return err
				}
				result = "cancelled"
				return nil
			}
			var count int64
			if err := tx.Model(&core.OrderEvent{}).Where("order_id = ? AND kind = ?", order.ID, "refund_requested").Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				result = "refund_requested"
				return nil
			}
			ev := core.OrderEvent{OrderID: order.ID, At: time.Now().UTC(), ActorUserID: &user.ID, Kind: "refund_requested", FromStatus: core.OrderStatusPaid, ToStatus: core.OrderStatusPaid, Message: "會員已提出退款申請，待商家處理"}
			if err := tx.Create(&ev).Error; err != nil {
				return err
			}
			result = "refund_requested"
			return nil
		}
		if order.Status != core.OrderStatusPending && order.Status != core.OrderStatusPaid {
			return errShopOrderCannotCancel
		}
		if err := core.NewOrderService(tx).Transition(tx, order.ID, order.Status, core.OrderStatusCancelled, &user.ID, "customer cancellation"); err != nil {
			return err
		}
		if order.Status == core.OrderStatusPending {
			if err := core.Release(tx, order.ID); err != nil {
				return err
			}
		}
		result = "cancelled"
		return core.ReleaseRedemption(tx, order.ID)
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return echo.NewHTTPError(404)
	}
	var transitionErr *core.TransitionError
	if errors.Is(err, errShopUnsupportedRefund) || errors.Is(err, errShopOrderCannotCancel) || errors.As(err, &transitionErr) {
		return echo.NewHTTPError(409, err.Error())
	}
	if err != nil {
		return echo.NewHTTPError(500, "could not cancel order")
	}
	return c.JSON(http.StatusOK, map[string]string{"status": result})
}

var errShopUnsupportedRefund = errors.New("payment provider does not support customer refund")
var errShopOrderCannotCancel = errors.New("order cannot be cancelled in its current state")

func shopMemberReorderAPI(c echo.Context) error {
	if !shopValidOrigin(c) {
		return echo.NewHTTPError(403)
	}
	user := shopPrincipalUser(c)
	if user == nil {
		return echo.NewHTTPError(401)
	}
	order, err := shopOwnOrder(c)
	if err != nil {
		if he, ok := err.(*echo.HTTPError); ok {
			return he
		}
		return echo.NewHTTPError(500)
	}
	var items []core.OrderItem
	if err := data.DB.Where("order_id = ?", order.ID).Order("id").Find(&items).Error; err != nil {
		return echo.NewHTTPError(500)
	}
	cart, _, err := shopCartSession(c, true)
	if err != nil {
		if errors.Is(err, errShopCartUnavailable) {
			return shopJSONError(c, http.StatusConflict, "cart_unavailable", "購物車已失效，請重新操作")
		}
		return echo.NewHTTPError(500, "could not load cart")
	}
	if replaced, _ := c.Get("shop_cart_replaced").(bool); replaced {
		return shopJSONError(c, http.StatusConflict, "cart_unavailable", "購物車已更新，請重新操作")
	}
	variantIDs := []uint{}
	for _, it := range items {
		if it.VariantID != nil {
			variantIDs = append(variantIDs, *it.VariantID)
		}
	}
	var variants []core.ProductVariant
	if len(variantIDs) > 0 {
		if err := data.DB.Where("id IN ? AND status = ?", variantIDs, "active").Find(&variants).Error; err != nil {
			return echo.NewHTTPError(500)
		}
	}
	productIDs := []uint{}
	for _, v := range variants {
		productIDs = append(productIDs, v.ProductID)
	}
	var products []core.Product
	if len(productIDs) > 0 {
		if err := data.DB.Where("id IN ? AND status = ?", productIDs, "active").Find(&products).Error; err != nil {
			return echo.NewHTTPError(500)
		}
	}
	vm := map[uint]core.ProductVariant{}
	for _, v := range variants {
		vm[v.ID] = v
	}
	pm := map[uint]bool{}
	for _, p := range products {
		pm[p.ID] = true
	}
	unavailable := []uint{}
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		current, err := shopLockOwnedCart(tx, cart, user)
		if err != nil {
			return err
		}
		for _, it := range items {
			if it.VariantID == nil {
				continue
			}
			v, ok := vm[*it.VariantID]
			if !ok || !pm[v.ProductID] || v.Stock < 1 {
				unavailable = append(unavailable, *it.VariantID)
				continue
			}
			qty := it.Quantity
			if qty < 1 {
				qty = 1
			}
			if qty > 99 {
				qty = 99
			}
			if qty > v.Stock {
				qty = v.Stock
			}
			line := core.CartItem{CartID: current.ID, VariantID: v.ID, Qty: qty}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "cart_id"}, {Name: "variant_id"}}, DoUpdates: clause.Assignments(map[string]any{"qty": gorm.Expr("LEAST(cart_items.qty + EXCLUDED.qty, ?, 99)", v.Stock), "updated_at": time.Now().UTC()})}).Create(&line).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errShopCartUnavailable) {
			return shopJSONError(c, http.StatusConflict, "cart_unavailable", "購物車已失效，請重新操作")
		}
		return echo.NewHTTPError(500, "could not reorder")
	}
	return c.JSON(200, map[string]any{"unavailable": unavailable})
}

type shopAddressInput struct {
	Label     string `json:"label"`
	Recipient string `json:"recipient"`
	Phone     string `json:"phone"`
	Postcode  string `json:"postcode"`
	City      string `json:"city"`
	Address   string `json:"address"`
	IsDefault bool   `json:"is_default"`
}

func (i *shopAddressInput) normalize() string {
	i.Label = strings.TrimSpace(i.Label)
	i.Recipient = strings.TrimSpace(i.Recipient)
	i.Phone = strings.TrimSpace(i.Phone)
	i.Postcode = strings.TrimSpace(i.Postcode)
	i.City = strings.TrimSpace(i.City)
	i.Address = strings.TrimSpace(i.Address)
	if len(i.Recipient) == 0 || len(i.Recipient) > 120 || !shopValidPhone(i.Phone) || len(i.Postcode) > 10 || len(i.City) > 60 || len(i.Address) == 0 || len(i.Address) > 255 || len(i.Label) > 60 {
		return "address"
	}
	return ""
}
func shopMemberAddressesAPI(c echo.Context) error {
	u := shopPrincipalUser(c)
	if u == nil {
		return echo.NewHTTPError(401)
	}
	var rows []core.Address
	if err := data.DB.Where("user_id = ?", u.ID).Order("is_default DESC,id DESC").Find(&rows).Error; err != nil {
		return echo.NewHTTPError(500)
	}
	return c.JSON(200, rows)
}
func shopMemberAddressCreateAPI(c echo.Context) error { return shopAddressWrite(c, false) }
func shopMemberAddressUpdateAPI(c echo.Context) error { return shopAddressWrite(c, true) }
func shopAddressWrite(c echo.Context, update bool) error {
	if !shopValidOrigin(c) {
		return echo.NewHTTPError(403)
	}
	u := shopPrincipalUser(c)
	if u == nil {
		return echo.NewHTTPError(401)
	}
	var id uint
	if update {
		n, err := strconv.ParseUint(c.Param("id"), 10, 32)
		if err != nil || n == 0 {
			return echo.NewHTTPError(404)
		}
		id = uint(n)
		var owned core.Address
		err = data.DB.Where("id = ? AND user_id = ?", id, u.ID).First(&owned).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return echo.NewHTTPError(404)
		}
		if err != nil {
			return echo.NewHTTPError(500)
		}
	}
	var in shopAddressInput
	if err := shopDecodeJSON(c, &in); err != nil {
		return echo.NewHTTPError(400)
	}
	if field := in.normalize(); field != "" {
		return echo.NewHTTPError(400, "invalid "+field)
	}
	var row core.Address
	err := data.DB.Transaction(func(tx *gorm.DB) error {
		var owner core.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&owner, u.ID).Error; err != nil {
			return err
		}
		if update {
			if err := tx.Where("id = ? AND user_id = ?", id, u.ID).First(&row).Error; err != nil {
				return err
			}
		}
		row.Label = in.Label
		row.Recipient = in.Recipient
		row.Phone = in.Phone
		row.Postcode = in.Postcode
		row.City = in.City
		row.Address = in.Address
		if in.IsDefault {
			if err := tx.Model(&core.Address{}).Where("user_id = ?", u.ID).Update("is_default", false).Error; err != nil {
				return err
			}
		}
		row.IsDefault = in.IsDefault
		row.UserID = u.ID
		if update {
			return tx.Save(&row).Error
		}
		return tx.Create(&row).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return echo.NewHTTPError(404)
	}
	if err != nil {
		return echo.NewHTTPError(500)
	}
	if update {
		return c.JSON(200, row)
	}
	return c.JSON(201, row)
}
func shopMemberAddressDeleteAPI(c echo.Context) error {
	if !shopValidOrigin(c) {
		return echo.NewHTTPError(http.StatusForbidden)
	}
	u := shopPrincipalUser(c)
	if u == nil {
		return echo.NewHTTPError(401)
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || id == 0 {
		return echo.NewHTTPError(404)
	}
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		var user core.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, u.ID).Error; err != nil {
			return err
		}
		var row core.Address
		if err := tx.Where("id = ? AND user_id = ?", id, u.ID).First(&row).Error; err != nil {
			return err
		}
		if err := tx.Delete(&row).Error; err != nil {
			return err
		}
		if row.IsDefault {
			var next core.Address
			err := tx.Where("user_id = ?", u.ID).Order("id ASC").First(&next).Error
			if err == nil {
				return tx.Model(&next).Update("is_default", true).Error
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return echo.NewHTTPError(404)
	}
	if err != nil {
		return echo.NewHTTPError(500)
	}
	return c.NoContent(204)
}

type shopInvoiceProfileInput struct {
	Kind         string `json:"kind"`
	CarrierCode  string `json:"carrier_code"`
	TaxID        string `json:"tax_id"`
	CompanyName  string `json:"company_name"`
	DonationCode string `json:"donation_code"`
	IsDefault    bool   `json:"is_default"`
}

func (i *shopInvoiceProfileInput) validate() string {
	i.Kind = strings.TrimSpace(i.Kind)
	i.CarrierCode = strings.TrimSpace(i.CarrierCode)
	i.TaxID = strings.TrimSpace(i.TaxID)
	i.CompanyName = strings.TrimSpace(i.CompanyName)
	i.DonationCode = strings.TrimSpace(i.DonationCode)
	field := shopValidInvoice(shopInvoiceInput{Kind: i.Kind, CarrierCode: i.CarrierCode, TaxID: i.TaxID, CompanyName: i.CompanyName, DonationCode: i.DonationCode})
	if field != "" {
		return field
	}
	switch i.Kind {
	case "none":
		i.CarrierCode, i.TaxID, i.CompanyName, i.DonationCode = "", "", "", ""
	case "personal_carrier":
		i.TaxID, i.CompanyName, i.DonationCode = "", "", ""
	case "tax_id":
		i.CarrierCode, i.DonationCode = "", ""
	case "donation":
		i.CarrierCode, i.TaxID, i.CompanyName = "", "", ""
	}
	return ""
}
func shopMemberInvoicesAPI(c echo.Context) error {
	u := shopPrincipalUser(c)
	if u == nil {
		return echo.NewHTTPError(401)
	}
	var rows []core.InvoiceProfile
	if err := data.DB.Where("user_id = ?", u.ID).Order("is_default DESC,id DESC").Find(&rows).Error; err != nil {
		return echo.NewHTTPError(500)
	}
	return c.JSON(200, rows)
}
func shopMemberInvoiceCreateAPI(c echo.Context) error { return shopInvoiceWrite(c, false) }
func shopMemberInvoiceUpdateAPI(c echo.Context) error { return shopInvoiceWrite(c, true) }
func shopInvoiceWrite(c echo.Context, update bool) error {
	if !shopValidOrigin(c) {
		return echo.NewHTTPError(403)
	}
	u := shopPrincipalUser(c)
	if u == nil {
		return echo.NewHTTPError(401)
	}
	var id uint
	if update {
		n, err := strconv.ParseUint(c.Param("id"), 10, 32)
		if err != nil || n == 0 {
			return echo.NewHTTPError(404)
		}
		id = uint(n)
		var owned core.InvoiceProfile
		err = data.DB.Where("id = ? AND user_id = ?", id, u.ID).First(&owned).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return echo.NewHTTPError(404)
		}
		if err != nil {
			return echo.NewHTTPError(500)
		}
	}
	var in shopInvoiceProfileInput
	if err := shopDecodeJSON(c, &in); err != nil {
		return echo.NewHTTPError(400)
	}
	if f := in.validate(); f != "" {
		return echo.NewHTTPError(400, "invalid "+f)
	}
	var row core.InvoiceProfile
	err := data.DB.Transaction(func(tx *gorm.DB) error {
		var user core.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, u.ID).Error; err != nil {
			return err
		}
		if update {
			if err := tx.Where("id = ? AND user_id = ?", id, u.ID).First(&row).Error; err != nil {
				return err
			}
		}
		row.Kind = in.Kind
		row.CarrierCode = in.CarrierCode
		row.TaxID = in.TaxID
		row.CompanyName = in.CompanyName
		row.DonationCode = in.DonationCode
		row.IsDefault = in.IsDefault
		row.UserID = u.ID
		if in.IsDefault {
			if err := tx.Model(&core.InvoiceProfile{}).Where("user_id = ?", u.ID).Update("is_default", false).Error; err != nil {
				return err
			}
		}
		if update {
			return tx.Save(&row).Error
		}
		return tx.Create(&row).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return echo.NewHTTPError(404)
	}
	if err != nil {
		return echo.NewHTTPError(500)
	}
	if update {
		return c.JSON(200, row)
	}
	return c.JSON(201, row)
}
func shopMemberInvoiceDeleteAPI(c echo.Context) error {
	if !shopValidOrigin(c) {
		return echo.NewHTTPError(http.StatusForbidden)
	}
	u := shopPrincipalUser(c)
	if u == nil {
		return echo.NewHTTPError(401)
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || id == 0 {
		return echo.NewHTTPError(404)
	}
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		var user core.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, u.ID).Error; err != nil {
			return err
		}
		var row core.InvoiceProfile
		if err := tx.Where("id = ? AND user_id = ?", id, u.ID).First(&row).Error; err != nil {
			return err
		}
		if err := tx.Delete(&row).Error; err != nil {
			return err
		}
		if row.IsDefault {
			var next core.InvoiceProfile
			err := tx.Where("user_id = ?", u.ID).Order("id ASC").First(&next).Error
			if err == nil {
				return tx.Model(&next).Update("is_default", true).Error
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return echo.NewHTTPError(404)
	}
	if err != nil {
		return echo.NewHTTPError(500)
	}
	return c.NoContent(204)
}
