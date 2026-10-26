package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const shopCartCookieName = "jelly_cart"

type shopCartItemJSON struct {
	VariantID   uint   `json:"variant_id"`
	ProductID   uint   `json:"product_id"`
	ProductSlug string `json:"product_slug"`
	Qty         int    `json:"qty"`
	UnitPrice   int64  `json:"unit_price"`
	LineTotal   int64  `json:"line_total"`
	Available   bool   `json:"available"`
	MaxQty      int    `json:"max_qty"`
	Title       string `json:"title"`
	VariantName string `json:"variant_name"`
	Image       string `json:"image"`
}

type shopCartIssueJSON struct {
	VariantID uint   `json:"variant_id"`
	Code      string `json:"code"`
}

type shopCartJSON struct {
	Items              []shopCartItemJSON  `json:"items"`
	Issues             []shopCartIssueJSON `json:"issues"`
	Subtotal           int64               `json:"subtotal"`
	Discount           int64               `json:"discount"`
	Shipping           int64               `json:"shipping"`
	ShippingGapToFree  int64               `json:"shipping_gap_to_free"`
	FreeShippingOver   int64               `json:"free_shipping_over"`
	RequiresShipping   bool                `json:"requires_shipping"`
	Total              int64               `json:"total"`
	TaxTotal           int64               `json:"tax_total"`
	RoundingAdjustment int64               `json:"rounding_adjustment"`
	Currency           string              `json:"currency"`
	Coupon             any                 `json:"coupon"`
	Count              int                 `json:"count"`
}

type shopCartLine struct {
	Item        core.CartItem
	Variant     core.ProductVariant
	Product     core.Product
	CategoryIDs []uint
	UnitPrice   int64
	LineTotal   int64
	Available   bool
	Qty         int
}

type shopCartQuote struct {
	View        shopCartJSON
	Lines       []shopCartLine
	PricedLines []core.PricedLine
	Coupon      *core.Coupon
	Discount    core.Discount
}

func shopTokenHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

var errShopCartUnavailable = errors.New("shop: cart unavailable")

func shopLockOwnedCart(tx *gorm.DB, expected core.Cart, principal *core.User) (core.Cart, error) {
	if tx == nil || expected.ID == 0 {
		return core.Cart{}, errShopCartUnavailable
	}
	var current core.Cart
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, expected.ID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return core.Cart{}, errShopCartUnavailable
		}
		return core.Cart{}, err
	}
	if current.TokenHash != expected.TokenHash || current.TokenHash == "" || !current.ExpiresAt.After(time.Now().UTC()) {
		return core.Cart{}, errShopCartUnavailable
	}
	if principal == nil && current.UserID != nil || principal != nil && current.UserID != nil && *current.UserID != principal.ID {
		return core.Cart{}, errShopCartUnavailable
	}
	return current, nil
}

func shopCartSession(c echo.Context, create bool) (core.Cart, string, error) {
	c.Set("shop_cart_replaced", false)
	raw := ""
	if cookie, err := c.Cookie(shopCartCookieName); err == nil {
		raw = cookie.Value
	}
	if len(raw) >= 40 && len(raw) <= 64 {
		var cart core.Cart
		err := data.DB.Where("token_hash = ? AND expires_at > ?", shopTokenHash(raw), time.Now().UTC()).First(&cart).Error
		if err == nil {
			principal := shopPrincipalUser(c)
			if principal == nil && cart.UserID != nil || principal != nil && cart.UserID != nil && *cart.UserID != principal.ID {
				if !create {
					return core.Cart{}, "", errShopCartUnavailable
				}
				c.Set("shop_cart_replaced", true)
				err = gorm.ErrRecordNotFound
			} else {
				cart, err = shopMergeCartForPrincipal(c, cart, raw)
				if errors.Is(err, errShopCartUnavailable) && create {
					c.Set("shop_cart_replaced", true)
					err = gorm.ErrRecordNotFound
				} else {
					return cart, raw, err
				}
			}
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return core.Cart{}, "", err
		}
	}
	if !create {
		return core.Cart{}, "", nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return core.Cart{}, "", err
	}
	raw = hex.EncodeToString(buf)
	cart := core.Cart{TokenHash: shopTokenHash(raw), ExpiresAt: time.Now().UTC().Add(30 * 24 * time.Hour)}
	if err := data.DB.Create(&cart).Error; err != nil {
		return core.Cart{}, "", err
	}
	cart, err := shopMergeCartForPrincipal(c, cart, raw)
	if err != nil {
		return core.Cart{}, "", err
	}
	c.SetCookie(&http.Cookie{Name: shopCartCookieName, Value: raw, Path: "/", HttpOnly: true, Secure: c.IsTLS(), SameSite: http.SameSiteLaxMode, Expires: time.Now().UTC().Add(30 * 24 * time.Hour), MaxAge: 30 * 24 * 60 * 60})
	return cart, raw, nil
}

func shopMergeCartForPrincipal(c echo.Context, cart core.Cart, raw string) (core.Cart, error) {
	principal, err := auth.GetCurrentPrincipal(c)
	if err != nil || principal == nil || principal.User == nil || principal.User.ID == 0 {
		return cart, nil
	}
	userID := principal.User.ID
	if cart.UserID != nil && *cart.UserID == userID {
		return cart, nil
	}
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", int64(userID)).Error; err != nil {
			return err
		}
		var current core.Cart
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, cart.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errShopCartUnavailable
			}
			return err
		}
		if current.TokenHash != shopTokenHash(raw) || !current.ExpiresAt.After(time.Now().UTC()) || current.UserID != nil && *current.UserID != userID {
			return errShopCartUnavailable
		}
		var memberCarts []core.Cart
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ? AND id <> ? AND expires_at > ?", userID, current.ID, time.Now().UTC()).Order("id ASC").Find(&memberCarts).Error; err != nil {
			return err
		}
		var currentItems []core.CartItem
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("cart_id = ?", current.ID).Order("variant_id").Find(&currentItems).Error; err != nil {
			return err
		}
		qtys := make(map[uint]int, len(currentItems))
		for _, item := range currentItems {
			qtys[item.VariantID] = item.Qty
		}
		for _, memberCart := range memberCarts {
			var items []core.CartItem
			if err := tx.Where("cart_id = ?", memberCart.ID).Find(&items).Error; err != nil {
				return err
			}
			for _, item := range items {
				if qtys[item.VariantID] < item.Qty {
					qtys[item.VariantID] = item.Qty
				}
			}
		}
		if len(qtys) > 50 {
			return errors.New("cart line limit exceeded during merge")
		}
		for variantID, qty := range qtys {
			item := core.CartItem{CartID: current.ID, VariantID: variantID, Qty: qty}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "cart_id"}, {Name: "variant_id"}}, DoUpdates: clause.Assignments(map[string]any{"qty": qty, "updated_at": time.Now().UTC()})}).Create(&item).Error; err != nil {
				return err
			}
		}
		for _, memberCart := range memberCarts {
			if err := tx.Where("cart_id = ?", memberCart.ID).Delete(&core.CartItem{}).Error; err != nil {
				return err
			}
			if err := tx.Model(&core.Cart{}).Where("id = ?", memberCart.ID).Update("expires_at", time.Now().UTC()).Error; err != nil {
				return err
			}
		}
		return tx.Model(&core.Cart{}).Where("id = ?", current.ID).Updates(map[string]any{"user_id": userID, "updated_at": time.Now().UTC(), "expires_at": time.Now().UTC().Add(30 * 24 * time.Hour)}).Error
	})
	if err != nil {
		return core.Cart{}, err
	}
	cart.UserID = &userID
	return cart, nil
}

func shopLoadCartQuote(cart core.Cart, user *core.User, email, shippingID string) (shopCartQuote, error) {
	var quote shopCartQuote
	err := data.DB.Transaction(func(tx *gorm.DB) error {
		current := cart
		if cart.ID != 0 {
			var err error
			current, err = shopLockOwnedCart(tx, cart, user)
			if err != nil {
				return err
			}
		}
		var err error
		quote, err = shopLoadCartQuoteWithDB(tx, current, user, email, shippingID)
		return err
	})
	return quote, err
}

func shopLoadCartQuoteWithDB(db *gorm.DB, cart core.Cart, user *core.User, email, shippingID string) (shopCartQuote, error) {
	currency, err := shopReadOptionWithDB(db, "shop_currency")
	if err != nil {
		return shopCartQuote{}, err
	}
	quote := shopCartQuote{View: shopCartJSON{Items: []shopCartItemJSON{}, Issues: []shopCartIssueJSON{}, Currency: currency, Coupon: nil}}
	if quote.View.Currency == "" {
		quote.View.Currency = "TWD"
	}
	if cart.ID == 0 {
		return quote, nil
	}
	var items []core.CartItem
	if err := db.Where("cart_id = ?", cart.ID).Order("variant_id").Find(&items).Error; err != nil {
		return quote, err
	}
	variantIDs := make([]uint, 0, len(items))
	for _, item := range items {
		variantIDs = append(variantIDs, item.VariantID)
	}
	variants := map[uint]core.ProductVariant{}
	if len(variantIDs) != 0 {
		var rows []core.ProductVariant
		if err := db.Where("id IN ?", variantIDs).Find(&rows).Error; err != nil {
			return quote, err
		}
		for _, row := range rows {
			variants[row.ID] = row
		}
	}
	productIDs := make([]uint, 0, len(variants))
	for _, variant := range variants {
		productIDs = append(productIDs, variant.ProductID)
	}
	products := map[uint]core.Product{}
	if len(productIDs) != 0 {
		var rows []core.Product
		if err := db.Where("id IN ? AND status = ?", productIDs, "active").Find(&rows).Error; err != nil {
			return quote, err
		}
		for _, row := range rows {
			products[row.ID] = row
		}
	}
	type productCategoryRef struct {
		ProductID  uint `gorm:"column:product_id"`
		CategoryID uint `gorm:"column:category_id"`
	}
	categoryIDsByProduct := make(map[uint][]uint, len(products))
	if len(productIDs) != 0 {
		var refs []productCategoryRef
		if err := db.Raw("SELECT product_id,category_id FROM product_category_links WHERE product_id IN ? ORDER BY product_id,category_id", productIDs).Scan(&refs).Error; err != nil {
			return quote, err
		}
		for _, ref := range refs {
			categoryIDsByProduct[ref.ProductID] = append(categoryIDsByProduct[ref.ProductID], ref.CategoryID)
		}
	}
	for _, item := range items {
		line := shopCartLine{Item: item, Qty: item.Qty}
		if line.Qty < 1 {
			line.Qty = 1
		}
		if line.Qty > 99 {
			line.Qty = 99
		}
		var found bool
		line.Variant, found = variants[item.VariantID]
		if !found {
			quote.View.Issues = append(quote.View.Issues, shopCartIssueJSON{VariantID: item.VariantID, Code: "unavailable"})
			continue
		}
		line.Product, found = products[line.Variant.ProductID]
		if !found || line.Variant.Status != "active" {
			quote.View.Issues = append(quote.View.Issues, shopCartIssueJSON{VariantID: item.VariantID, Code: "unavailable"})
			continue
		}
		line.UnitPrice = line.Variant.Price
		if line.UnitPrice < 0 || line.Qty < 1 || int64(line.Qty) > math.MaxInt64/max64(line.UnitPrice, 1) {
			return quote, errors.New("cart amount overflow")
		}
		line.Available = line.Variant.Stock > 0
		if line.Variant.Stock <= 0 {
			line.Qty = item.Qty
			quote.View.Issues = append(quote.View.Issues, shopCartIssueJSON{VariantID: item.VariantID, Code: "sold_out"})
		} else if line.Qty > line.Variant.Stock {
			line.Qty = line.Variant.Stock
			quote.View.Issues = append(quote.View.Issues, shopCartIssueJSON{VariantID: item.VariantID, Code: "qty_reduced"})
		}
		if line.Available {
			line.LineTotal = int64(line.Qty) * line.UnitPrice
			if quote.View.Subtotal > math.MaxInt64-line.LineTotal {
				return quote, errors.New("cart subtotal overflow")
			}
			quote.View.Subtotal += line.LineTotal
			categoryIDs := categoryIDsByProduct[line.Product.ID]
			line.CategoryIDs = categoryIDs
			quote.PricedLines = append(quote.PricedLines, core.PricedLine{VariantID: line.Variant.ID, ProductID: line.Product.ID, CategoryIDs: categoryIDs, Qty: line.Qty, UnitPrice: line.UnitPrice, LineTotal: line.LineTotal})
		}
		quote.Lines = append(quote.Lines, line)
		quote.View.Items = append(quote.View.Items, shopCartItemJSON{VariantID: line.Variant.ID, ProductID: line.Product.ID, ProductSlug: line.Product.Slug, Qty: line.Qty, UnitPrice: line.UnitPrice, LineTotal: line.LineTotal, Available: line.Available, MaxQty: max(line.Variant.Stock, 0), Title: line.Product.Title, VariantName: line.Variant.Name, Image: shopProductImage(line.Product.Images)})
		quote.View.Count += line.Qty
	}
	requiresShipping := false
	for _, line := range quote.Lines {
		if line.Available && shopLineRequiresShipping(line.Product) {
			requiresShipping = true
			break
		}
	}
	quote.View.RequiresShipping = requiresShipping
	methods, err := shopShippingMethodsWithDB(db)
	if err != nil {
		return quote, err
	}
	var selectedFreeOver int64
	if requiresShipping {
		selected := shopFindShipping(methods, shippingID)
		if selected == nil {
			selected = shopFindShipping(methods, "home")
		}
		if selected != nil {
			quote.View.Shipping = selected.Fee
			selectedFreeOver = selected.FreeOver
			if selectedFreeOver > 0 && quote.View.Subtotal >= selectedFreeOver {
				quote.View.Shipping = 0
			}
		}
	} else {
		quote.View.Shipping = 0
	}
	freeOverRaw, err := shopReadOptionWithDB(db, "shop_free_shipping_over")
	if err != nil {
		return quote, err
	}
	freeOver, _ := strconv.ParseInt(freeOverRaw, 10, 64)
	quote.View.FreeShippingOver = freeOver
	if requiresShipping && freeOver > 0 && quote.View.Subtotal >= freeOver {
		quote.View.Shipping = 0
	}
	quote.View.ShippingGapToFree = 0
	if requiresShipping {
		threshold := freeOver
		if selectedFreeOver > 0 && (threshold == 0 || selectedFreeOver < threshold) {
			threshold = selectedFreeOver
		}
		if threshold > 0 && quote.View.Subtotal < threshold {
			quote.View.ShippingGapToFree = threshold - quote.View.Subtotal
		}
	}
	if cart.CouponCode != "" {
		var coupon core.Coupon
		couponErr := db.Where("lower(code) = ?", strings.ToLower(strings.TrimSpace(cart.CouponCode))).First(&coupon).Error
		if couponErr == nil {
			targets, err := core.CouponTargets(db, coupon.ID)
			if err != nil {
				return quote, err
			}
			couponQuote := core.CartQuote{Lines: quote.PricedLines, Subtotal: quote.View.Subtotal, ShippingFee: quote.View.Shipping, Email: email}
			if user != nil {
				if used, err := core.CouponPerUserUsed(db, coupon.ID, &user.ID, email); err != nil {
					return quote, err
				} else if coupon.PerUserLimit != nil && used >= int64(*coupon.PerUserLimit) {
					quote.View.Issues = append(quote.View.Issues, shopCartIssueJSON{Code: "coupon_invalid"})
					goto finishCoupon
				}
			} else if used, err := core.CouponPerUserUsed(db, coupon.ID, nil, email); err != nil {
				return quote, err
			} else if coupon.PerUserLimit != nil && used >= int64(*coupon.PerUserLimit) {
				quote.View.Issues = append(quote.View.Issues, shopCartIssueJSON{Code: "coupon_invalid"})
				goto finishCoupon
			}
			if discount, err := core.EvaluateWithTargets(couponQuote, coupon, user, targets); err == nil {
				quote.Coupon = &coupon
				quote.Discount = discount
				quote.View.Discount = discount.Amount
				if discount.FreeShipping {
					quote.View.Shipping = 0
					quote.View.ShippingGapToFree = 0
				}
				quote.View.Coupon = map[string]any{"code": coupon.Code, "label": discount.Label, "free_shipping": discount.FreeShipping}
			} else {
				quote.View.Issues = append(quote.View.Issues, shopCartIssueJSON{Code: "coupon_invalid"})
			}
		} else if !errors.Is(couponErr, gorm.ErrRecordNotFound) {
			return quote, couponErr
		}
	}
finishCoupon:
	quote.View.Total, quote.View.RoundingAdjustment, err = shopCalculateTotal(quote.View.Subtotal, quote.View.Shipping, quote.View.Discount)
	if err != nil {
		return quote, err
	}
	taxMode, err := shopReadOptionWithDB(db, "shop_tax_mode")
	if err != nil {
		return quote, err
	}
	taxRateRaw, err := shopReadOptionWithDB(db, "shop_tax_rate")
	if err != nil {
		return quote, err
	}
	if taxMode == "" {
		taxMode = "included"
	}
	if taxMode != "included" {
		return quote, errors.New("unsupported shop tax mode")
	}
	if taxRateRaw == "" {
		taxRateRaw = "5"
	}
	quote.View.TaxTotal, err = shopIncludedTax(quote.View.Subtotal-quote.View.Discount, taxRateRaw)
	if err != nil {
		return quote, err
	}
	return quote, nil
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func shopCartGetHandler(c echo.Context) error {
	cart, _, err := shopCartSession(c, false)
	if errors.Is(err, errShopCartUnavailable) {
		cart = core.Cart{}
	} else if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not load cart")
	}
	user := shopPrincipalUser(c)
	quote, err := shopLoadCartQuote(cart, user, "", "home")
	if errors.Is(err, errShopCartUnavailable) {
		quote, err = shopLoadCartQuote(core.Cart{}, user, "", "home")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not price cart")
	}
	return c.JSON(http.StatusOK, quote.View)
}

func shopCartAddHandler(c echo.Context) error {
	var body struct {
		VariantID uint `json:"variant_id"`
		Qty       int  `json:"qty"`
	}
	if err := shopDecodeJSON(c, &body); err != nil || body.VariantID == 0 || body.Qty < 1 || body.Qty > 99 {
		return shopJSONError(c, http.StatusBadRequest, "input", "variant_id and qty 1-99 are required")
	}
	cart, _, err := shopCartSession(c, true)
	if err != nil {
		if errors.Is(err, errShopCartUnavailable) {
			return shopJSONError(c, http.StatusConflict, "cart_unavailable", "購物車已失效，請重新操作")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not load cart")
	}
	if cart.ID == 0 {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not create cart")
	}
	user := shopPrincipalUser(c)
	var quote shopCartQuote
	if err := data.DB.Transaction(func(tx *gorm.DB) error {
		locked, err := shopLockOwnedCart(tx, cart, user)
		if err != nil {
			return err
		}
		var variant core.ProductVariant
		if err := tx.Joins("JOIN products ON products.id = product_variants.product_id").Where("product_variants.id = ? AND product_variants.status = 'active' AND products.status = 'active' AND products.deleted_at IS NULL", body.VariantID).First(&variant).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errShopCartVariantUnavailable
			}
			return err
		}
		var existing core.CartItem
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("cart_id = ? AND variant_id = ?", cart.ID, body.VariantID).First(&existing).Error
		if err == nil {
			qty := existing.Qty + body.Qty
			if qty > 99 {
				qty = 99
			}
			err = tx.Model(&existing).Update("qty", qty).Error
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		} else {
			var count int64
			if err := tx.Model(&core.CartItem{}).Where("cart_id = ?", cart.ID).Count(&count).Error; err != nil {
				return err
			}
			if count >= 50 {
				return errors.New("cart line limit exceeded")
			}
			err = tx.Create(&core.CartItem{CartID: cart.ID, VariantID: body.VariantID, Qty: body.Qty}).Error
		}
		if err != nil {
			return err
		}
		quote, err = shopLoadCartQuoteWithDB(tx, locked, user, "", "home")
		return err
	}); err != nil {
		if errors.Is(err, errShopCartUnavailable) {
			return shopJSONError(c, http.StatusConflict, "cart_unavailable", "購物車已失效，請重新操作")
		}
		if errors.Is(err, errShopCartVariantUnavailable) {
			return shopJSONError(c, http.StatusNotFound, "variant", "商品不存在")
		}
		if strings.Contains(err.Error(), "limit exceeded") {
			return shopJSONError(c, http.StatusBadRequest, "line_limit", "最多 50 項商品")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not update cart")
	}
	return c.JSON(http.StatusOK, quote.View)
}

var errShopCartVariantUnavailable = errors.New("shop: cart variant unavailable")

func shopCartUpdateHandler(c echo.Context) error {
	variantID, err := strconv.ParseUint(c.Param("variant_id"), 10, 64)
	if err != nil || variantID == 0 {
		return shopJSONError(c, 400, "input", "invalid variant_id")
	}
	var body struct {
		Qty int `json:"qty"`
	}
	if err := shopDecodeJSON(c, &body); err != nil || body.Qty < 1 || body.Qty > 99 {
		return shopJSONError(c, 400, "input", "qty must be 1-99")
	}
	cart, _, err := shopCartSession(c, false)
	if err != nil {
		if errors.Is(err, errShopCartUnavailable) {
			return echo.NewHTTPError(http.StatusNotFound)
		}
		return echo.NewHTTPError(500, "could not load cart")
	}
	if cart.ID == 0 {
		return echo.NewHTTPError(http.StatusNotFound)
	}
	user := shopPrincipalUser(c)
	var quote shopCartQuote
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		locked, err := shopLockOwnedCart(tx, cart, user)
		if err != nil {
			return err
		}
		var item core.CartItem
		if err := tx.Where("cart_id = ? AND variant_id = ?", cart.ID, uint(variantID)).First(&item).Error; err != nil {
			return err
		}
		if err := tx.Model(&item).Update("qty", body.Qty).Error; err != nil {
			return err
		}
		quote, err = shopLoadCartQuoteWithDB(tx, locked, user, "", "home")
		return err
	})
	if errors.Is(err, errShopCartUnavailable) || errors.Is(err, gorm.ErrRecordNotFound) {
		return echo.NewHTTPError(http.StatusNotFound)
	}
	if err != nil {
		return echo.NewHTTPError(500, "could not update cart")
	}
	return c.JSON(200, quote.View)
}

func shopCartRemoveHandler(c echo.Context) error {
	variantID, err := strconv.ParseUint(c.Param("variant_id"), 10, 64)
	if err != nil || variantID == 0 {
		return shopJSONError(c, 400, "input", "invalid variant_id")
	}
	cart, _, err := shopCartSession(c, false)
	if err != nil {
		if errors.Is(err, errShopCartUnavailable) {
			return c.NoContent(http.StatusNoContent)
		}
		return echo.NewHTTPError(500, "could not load cart")
	}
	if cart.ID != 0 {
		user := shopPrincipalUser(c)
		err := data.DB.Transaction(func(tx *gorm.DB) error {
			if _, err := shopLockOwnedCart(tx, cart, user); err != nil {
				return err
			}
			return tx.Where("cart_id = ? AND variant_id = ?", cart.ID, uint(variantID)).Delete(&core.CartItem{}).Error
		})
		if errors.Is(err, errShopCartUnavailable) {
			return c.NoContent(http.StatusNoContent)
		}
		if err != nil {
			return echo.NewHTTPError(500, "could not update cart")
		}
	}
	return c.NoContent(http.StatusNoContent)
}

func shopCartCouponApplyHandler(c echo.Context) error {
	if limited := shopRateLimit(c, "coupon", 8, time.Minute); limited {
		return shopJSONError(c, http.StatusTooManyRequests, "rate_limit", "請稍後再試")
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := shopDecodeJSON(c, &body); err != nil {
		return shopJSONError(c, 400, "input", "coupon code is required")
	}
	code := strings.TrimSpace(body.Code)
	if code == "" || len(code) > 64 {
		return shopJSONError(c, 422, "coupon", "折扣碼無效")
	}
	cart, _, err := shopCartSession(c, false)
	if err != nil {
		if errors.Is(err, errShopCartUnavailable) {
			cart = core.Cart{}
		} else {
			return echo.NewHTTPError(500, "could not load cart")
		}
	}
	if cart.ID == 0 {
		preflightErr := data.DB.Transaction(func(tx *gorm.DB) error {
			var coupon core.Coupon
			if err := tx.Where("lower(code) = ?", strings.ToLower(code)).First(&coupon).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return errShopInvalidCartCoupon
				}
				return err
			}
			now := time.Now()
			if coupon.Status != "" && coupon.Status != "active" ||
				coupon.StartsAt != nil && now.Before(*coupon.StartsAt) ||
				coupon.EndsAt != nil && !now.Before(*coupon.EndsAt) {
				return errShopInvalidCartCoupon
			}
			return nil
		})
		if errors.Is(preflightErr, errShopInvalidCartCoupon) {
			return shopJSONError(c, 422, "coupon", "折扣碼無效")
		}
		if preflightErr != nil {
			return echo.NewHTTPError(500, "could not validate coupon")
		}
	}
	if cart.ID == 0 {
		cart, _, err = shopCartSession(c, true)
		if err != nil {
			if errors.Is(err, errShopCartUnavailable) {
				return shopJSONError(c, http.StatusConflict, "cart_unavailable", "購物車已失效，請重新操作")
			}
			return echo.NewHTTPError(500, "could not create cart")
		}
	}
	user := shopPrincipalUser(c)
	email := ""
	if user != nil {
		email = user.Email
	}
	var quote shopCartQuote
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		locked, err := shopLockOwnedCart(tx, cart, user)
		if err != nil {
			return err
		}
		quote, err = shopLoadCartQuoteWithDB(tx, locked, user, email, "home")
		if err != nil {
			return err
		}
		var coupon core.Coupon
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("lower(code) = ?", strings.ToLower(code)).First(&coupon).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errShopInvalidCartCoupon
			}
			return err
		}
		targets, err := core.CouponTargets(tx, coupon.ID)
		if err != nil {
			return err
		}
		userID := (*uint)(nil)
		if user != nil {
			id := user.ID
			userID = &id
		}
		used, err := core.CouponPerUserUsed(tx, coupon.ID, userID, email)
		if err != nil {
			return err
		}
		if coupon.PerUserLimit != nil && used >= int64(*coupon.PerUserLimit) {
			return errShopInvalidCartCoupon
		}
		input := core.CartQuote{Lines: quote.PricedLines, Subtotal: quote.View.Subtotal, ShippingFee: quote.View.Shipping, Email: email}
		if _, err := core.EvaluateWithTargets(input, coupon, user, targets); err != nil {
			return errShopInvalidCartCoupon
		}
		if err := tx.Model(&core.Cart{}).Where("id = ?", locked.ID).Update("coupon_code", coupon.Code).Error; err != nil {
			return err
		}
		locked.CouponCode = coupon.Code
		quote, err = shopLoadCartQuoteWithDB(tx, locked, user, email, "home")
		return err
	})
	if errors.Is(err, errShopCartUnavailable) {
		return shopJSONError(c, http.StatusConflict, "cart_unavailable", "購物車已失效，請重新操作")
	}
	if errors.Is(err, errShopInvalidCartCoupon) {
		return shopJSONError(c, 422, "coupon", "折扣碼無效")
	}
	if err != nil {
		return echo.NewHTTPError(500, "could not apply coupon")
	}
	return c.JSON(200, quote.View)
}

var errShopInvalidCartCoupon = errors.New("shop: invalid cart coupon")

func shopCartCouponRemoveHandler(c echo.Context) error {
	cart, _, err := shopCartSession(c, false)
	if err != nil {
		if errors.Is(err, errShopCartUnavailable) {
			return c.NoContent(http.StatusNoContent)
		}
		return echo.NewHTTPError(500, "could not load cart")
	}
	if cart.ID != 0 {
		user := shopPrincipalUser(c)
		err := data.DB.Transaction(func(tx *gorm.DB) error {
			if _, err := shopLockOwnedCart(tx, cart, user); err != nil {
				return err
			}
			return tx.Model(&core.Cart{}).Where("id = ?", cart.ID).Update("coupon_code", "").Error
		})
		if errors.Is(err, errShopCartUnavailable) {
			return c.NoContent(http.StatusNoContent)
		}
		if err != nil {
			return echo.NewHTTPError(500, "could not clear coupon")
		}
	}
	return c.NoContent(http.StatusNoContent)
}

func shopPrincipalUser(c echo.Context) *core.User {
	p, err := auth.GetCurrentPrincipal(c)
	if err != nil || p == nil || p.User == nil {
		return nil
	}
	return p.User
}

func shopJSONError(c echo.Context, status int, code, message string) error {
	return c.JSON(status, map[string]string{"code": code, "error": message})
}

func shopReadOption(key string) (string, error) {
	return shopReadOptionWithDB(data.DB, key)
}

func shopReadOptionWithDB(db *gorm.DB, key string) (string, error) {
	var option core.Option
	err := db.Where("key = ?", key).First(&option).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return option.Value, nil
}

func shopErrorf(format string, args ...any) error { return fmt.Errorf(format, args...) }

var _ = json.Valid
