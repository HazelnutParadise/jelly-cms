package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/money"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type adminProductView struct {
	core.Product
	Variants   []core.ProductVariant `json:"variants"`
	Categories []uint                `json:"categories"`
}

func productError(c echo.Context, status int, message string) error {
	return c.JSON(status, map[string]string{"error": message})
}

func parseMoneyInput(s string) (int64, error) {
	if strings.TrimSpace(s) == "" {
		return 0, nil
	}
	return money.FromInput(s)
}

func validProductStatus(s string) bool { return s == "active" || s == "draft" || s == "archived" }

func parseProductInput(in productInput) (int64, int64, []parsedProductVariant, error) {
	price, err := parseMoneyInput(in.Price)
	if err != nil {
		return 0, 0, nil, err
	}
	compare, err := parseMoneyInput(in.CompareAtPrice)
	if err != nil {
		return 0, 0, nil, err
	}
	if in.LowStockThreshold != nil && *in.LowStockThreshold < 0 {
		return 0, 0, nil, errors.New("negative low stock threshold")
	}
	if in.Stock != nil && (*in.Stock < 0 || *in.Stock > math.MaxInt/2) {
		return 0, 0, nil, errors.New("invalid product stock")
	}
	if in.Kind != "" && in.Kind != "physical" && in.Kind != "digital" {
		return 0, 0, nil, errors.New("invalid product kind")
	}
	if in.Status != "" && !validProductStatus(in.Status) {
		return 0, 0, nil, errors.New("invalid product status")
	}
	for _, image := range in.Images {
		if !validLocalProductURL(image) {
			return 0, 0, nil, errors.New("invalid image URL")
		}
	}
	variants := make([]parsedProductVariant, 0, len(in.Variants))
	seen := map[uint]bool{}
	for _, v := range in.Variants {
		vp, e := parseMoneyInput(v.Price)
		if e != nil {
			return 0, 0, nil, e
		}
		vc, e := parseMoneyInput(v.CompareAtPrice)
		if e != nil {
			return 0, 0, nil, e
		}
		if v.Stock < 0 || v.Position < 0 || v.Stock > math.MaxInt/2 || v.ID != 0 && seen[v.ID] {
			return 0, 0, nil, errors.New("invalid variant")
		}
		if v.ID != 0 {
			seen[v.ID] = true
		}
		status := v.Status
		if status == "" {
			status = "active"
		}
		if status != "active" && status != "draft" && status != "archived" {
			return 0, 0, nil, errors.New("invalid variant status")
		}
		variants = append(variants, parsedProductVariant{ID: v.ID, Name: v.Name, SKU: v.SKU, Price: vp, CompareAtPrice: vc, Stock: v.Stock, Position: v.Position, Status: status})
	}
	return price, compare, variants, nil
}

type parsedProductVariant struct {
	ID                    uint
	Name, SKU             string
	Price, CompareAtPrice int64
	Stock, Position       int
	Status                string
}

func validLocalProductURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.ContainsAny(raw, "\\\r\n") {
		return false
	}
	decodedPath, err := url.PathUnescape(u.Path)
	if err != nil {
		return false
	}
	for _, segment := range strings.Split(decodedPath, "/") {
		if segment == ".." {
			return false
		}
	}
	return !strings.HasPrefix(strings.ToLower(u.Path), "javascript:") && !strings.HasPrefix(strings.ToLower(u.Path), "data:")
}

func validateCategoryIDs(tx *gorm.DB, ids []uint) error {
	seen := map[uint]bool{}
	for _, id := range ids {
		if id == 0 || seen[id] {
			return errors.New("invalid category IDs")
		}
		seen[id] = true
	}
	if len(ids) == 0 {
		return nil
	}
	var count int64
	if err := tx.Model(&core.ProductCategory{}).Where("id IN ?", ids).Count(&count).Error; err != nil {
		return err
	}
	if count != int64(len(ids)) {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func loadAdminProduct(tx *gorm.DB, p *core.Product) (adminProductView, error) {
	var variants []core.ProductVariant
	if err := tx.Where("product_id = ?", p.ID).Order("position ASC, id ASC").Find(&variants).Error; err != nil {
		return adminProductView{}, err
	}
	if variants == nil {
		variants = []core.ProductVariant{}
	}
	var ids []uint
	if err := tx.Raw("SELECT category_id FROM product_category_links WHERE product_id = ? ORDER BY category_id", p.ID).Scan(&ids).Error; err != nil {
		return adminProductView{}, err
	}
	if ids == nil {
		ids = []uint{}
	}
	return adminProductView{Product: *p, Variants: variants, Categories: ids}, nil
}

func adminProductsList(c echo.Context) error {
	db := data.DB.Model(&core.Product{})
	status, q := c.QueryParam("status"), strings.TrimSpace(c.QueryParam("q"))
	if status != "" && status != "all" && status != "active" && status != "draft" && status != "archived" && status != "out_of_stock" {
		return productError(c, http.StatusBadRequest, "invalid status")
	}
	page, err := strconv.Atoi(c.QueryParam("page"))
	if err != nil && c.QueryParam("page") != "" {
		return productError(c, 400, "invalid page")
	}
	if page < 1 {
		page = 1
	}
	if page > 1000000 {
		return productError(c, 400, "invalid page")
	}
	perPage, err := strconv.Atoi(c.QueryParam("per_page"))
	if err != nil && c.QueryParam("per_page") != "" {
		return productError(c, 400, "invalid per_page")
	}
	if perPage < 1 {
		perPage = 20
	}
	if perPage > 100 {
		perPage = 100
	}
	if q != "" {
		pat := "%" + q + "%"
		db = db.Where("title ILIKE ? OR slug ILIKE ? OR sku ILIKE ? OR EXISTS (SELECT 1 FROM product_variants pv WHERE pv.product_id = products.id AND pv.sku ILIKE ?)", pat, pat, pat, pat)
	}
	if status == "out_of_stock" {
		db = db.Where("status = ? AND stock <= 0", "active")
	} else if status != "" && status != "all" {
		db = db.Where("status = ?", status)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return productError(c, 500, "could not count products")
	}
	var products []core.Product
	if err := db.Order("created_at DESC, id DESC").Limit(perPage).Offset((page - 1) * perPage).Find(&products).Error; err != nil {
		return productError(c, 500, "could not load products")
	}
	counts := map[string]int64{}
	for key, filter := range map[string]string{"all": "", "active": "status = 'active'", "draft": "status = 'draft'", "archived": "status = 'archived'", "out_of_stock": "status = 'active' AND stock <= 0"} {
		qcount := data.DB.Model(&core.Product{})
		if filter != "" {
			qcount = qcount.Where(filter)
		}
		var count int64
		if err := qcount.Count(&count).Error; err != nil {
			return productError(c, 500, "could not count product statuses")
		}
		counts[key] = count
	}
	items := make([]adminProductView, 0, len(products))
	if len(products) > 0 {
		ids := make([]uint, len(products))
		for i := range products {
			ids[i] = products[i].ID
		}
		var variants []core.ProductVariant
		if err := data.DB.Where("product_id IN ?", ids).Order("position ASC, id ASC").Find(&variants).Error; err != nil {
			return productError(c, 500, "could not load variants")
		}
		byProduct := map[uint][]core.ProductVariant{}
		for _, v := range variants {
			byProduct[v.ProductID] = append(byProduct[v.ProductID], v)
		}
		var links []struct {
			ProductID  uint
			CategoryID uint
		}
		if err := data.DB.Table("product_category_links").Select("product_id, category_id").Where("product_id IN ?", ids).Order("category_id").Scan(&links).Error; err != nil {
			return productError(c, 500, "could not load product categories")
		}
		cats := map[uint][]uint{}
		for _, l := range links {
			cats[l.ProductID] = append(cats[l.ProductID], l.CategoryID)
		}
		for _, p := range products {
			vs := byProduct[p.ID]
			if vs == nil {
				vs = []core.ProductVariant{}
			}
			cs := cats[p.ID]
			if cs == nil {
				cs = []uint{}
			}
			items = append(items, adminProductView{Product: p, Variants: vs, Categories: cs})
		}
	}
	return c.JSON(200, map[string]interface{}{"items": items, "total": total, "counts": counts, "page": page, "per_page": perPage})
}

func adminProductsGet(c echo.Context) error {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		return productError(c, 404, "Product not found")
	}
	var p core.Product
	if err := data.DB.First(&p, uint(id)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return productError(c, 404, "Product not found")
		}
		return productError(c, 500, "could not load product")
	}
	view, err := loadAdminProduct(data.DB, &p)
	if err != nil {
		return productError(c, 500, "could not load product details")
	}
	return c.JSON(200, view)
}

func applyProductInput(tx *gorm.DB, p *core.Product, in productInput, price, compare int64, isCreate bool) error {
	if strings.TrimSpace(in.Title) == "" {
		return errors.New("title is required")
	}
	if in.Slug != "" {
		p.Slug = strings.TrimSpace(in.Slug)
	} else if isCreate || p.Slug == "" {
		p.Slug = core.Slugify(in.Title, "")
	}
	p.Title, p.Description = strings.TrimSpace(in.Title), in.Description
	if len(in.Variants) == 0 && in.SKU != nil {
		p.SKU = *in.SKU
	}
	if isCreate || in.Price != "" {
		p.Price = price
	}
	if isCreate || in.CompareAtPrice != "" {
		p.CompareAtPrice = compare
	}
	if in.Status != "" {
		p.Status = in.Status
	} else if isCreate {
		p.Status = "draft"
	}
	if p.Status == "" {
		p.Status = "draft"
	}
	if in.Kind != "" {
		p.Kind = in.Kind
	} else if isCreate {
		p.Kind = "physical"
	}
	if p.Kind == "" {
		p.Kind = "physical"
	}
	if in.LowStockThreshold != nil {
		p.LowStockThreshold = *in.LowStockThreshold
	} else if isCreate {
		p.LowStockThreshold = 5
	}
	if in.RequiresShipping != nil {
		p.RequiresShipping = *in.RequiresShipping
	} else if isCreate {
		p.RequiresShipping = true
	}
	p.ColdChain = in.ColdChain
	images, err := json.Marshal(in.Images)
	if err != nil {
		return err
	}
	if in.Images != nil {
		p.Images = images
	} else if isCreate {
		p.Images = []byte("[]")
	}
	attrs, err := json.Marshal(in.Attributes)
	if err != nil {
		return err
	}
	if in.Attributes != nil {
		p.Attributes = attrs
	} else if isCreate {
		p.Attributes = []byte("{}")
	}
	related, err := json.Marshal(in.RelatedIDs)
	if err != nil {
		return err
	}
	if in.RelatedIDs != nil {
		p.RelatedIDs = related
	} else if isCreate {
		p.RelatedIDs = []byte("[]")
	}
	p.SEOTitle, p.SEODescription = in.SEOTitle, in.SEODescription
	return nil
}

func saveVariantSet(tx *gorm.DB, productID uint, parsed []parsedProductVariant, provided bool) (int, error) {
	var existing []core.ProductVariant
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("product_id = ?", productID).Order("id").Find(&existing).Error; err != nil {
		return 0, err
	}
	if !provided {
		total := 0
		for _, v := range existing {
			if v.Stock > math.MaxInt-total {
				return 0, errors.New("stock overflow")
			}
			total += v.Stock
		}
		return total, nil
	}
	if len(parsed) == 0 {
		return 0, gorm.ErrInvalidData
	}
	byID := map[uint]core.ProductVariant{}
	for _, v := range existing {
		byID[v.ID] = v
	}
	keep := map[uint]bool{}
	total := 0
	for _, v := range parsed {
		if v.Stock > math.MaxInt-total {
			return 0, errors.New("stock overflow")
		}
		total += v.Stock
		row := core.ProductVariant{ProductID: productID, Name: v.Name, SKU: v.SKU, Price: v.Price, CompareAtPrice: v.CompareAtPrice, Stock: v.Stock, Position: v.Position, Status: v.Status}
		if v.ID != 0 {
			old, ok := byID[v.ID]
			if !ok {
				return 0, gorm.ErrRecordNotFound
			}
			row.ID = old.ID
			row.CreatedAt = old.CreatedAt
			if err := tx.Model(&row).Select("name", "sku", "price", "compare_at_price", "stock", "position", "status").Updates(&row).Error; err != nil {
				return 0, err
			}
			if delta := row.Stock - old.Stock; delta != 0 {
				movement := core.StockMovement{VariantID: row.ID, Delta: delta, Reason: "adjust"}
				if err := tx.Create(&movement).Error; err != nil {
					return 0, err
				}
			}
			keep[row.ID] = true
		} else {
			if err := tx.Create(&row).Error; err != nil {
				return 0, err
			}
			if row.Stock > 0 {
				movement := core.StockMovement{VariantID: row.ID, Delta: row.Stock, Reason: "adjust"}
				if err := tx.Create(&movement).Error; err != nil {
					return 0, err
				}
			}
			keep[row.ID] = true
		}
	}
	for _, old := range existing {
		if keep[old.ID] {
			continue
		}
		var refs int64
		if err := tx.Table("order_items").Where("variant_id = ?", old.ID).Count(&refs).Error; err != nil {
			return 0, err
		}
		var movements int64
		if err := tx.Model(&core.StockMovement{}).Where("variant_id = ?", old.ID).Count(&movements).Error; err != nil {
			return 0, err
		}
		if refs > 0 || movements > 0 {
			if old.Stock > math.MaxInt-total {
				return 0, errors.New("stock overflow")
			}
			total += old.Stock
			continue
		}
		if err := tx.Delete(&old).Error; err != nil {
			return 0, err
		}
	}
	return total, nil
}

var errLegacyVariantConflict = errors.New("legacy SKU or stock requires one default variant")

// applyLegacyDefaultVariant updates the existing empty-name variant in place.
// It preserves omitted fields and records inventory changes through the same
// append-only adjustment ledger used by saveVariantSet.
func applyLegacyDefaultVariant(tx *gorm.DB, productID uint, in productInput, price int64) (bool, error) {
	legacySKUStock := in.SKU != nil || in.Stock != nil
	var variants []core.ProductVariant
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("product_id = ?", productID).Order("id").Find(&variants).Error; err != nil {
		return false, err
	}
	if len(variants) != 1 || variants[0].Name != "" {
		if legacySKUStock {
			return false, errLegacyVariantConflict
		}
		return false, nil
	}
	row := variants[0]
	columns := make([]string, 0, 3)
	if in.Price != "" {
		row.Price = price
		columns = append(columns, "price")
	}
	if in.SKU != nil {
		row.SKU = *in.SKU
		columns = append(columns, "sku")
	}
	if in.Stock != nil {
		row.Stock = *in.Stock
		columns = append(columns, "stock")
	}
	if len(columns) == 0 {
		return true, nil
	}
	if err := tx.Model(&row).Select(columns).Updates(&row).Error; err != nil {
		return false, err
	}
	if in.Stock != nil && row.Stock != variants[0].Stock {
		movement := core.StockMovement{VariantID: row.ID, Delta: row.Stock - variants[0].Stock, Reason: "adjust"}
		if err := tx.Create(&movement).Error; err != nil {
			return false, err
		}
	}
	return true, nil
}

func adminProductsCreate(c echo.Context) error {
	var in productInput
	if err := c.Bind(&in); err != nil {
		return productError(c, 400, "Invalid request")
	}
	price, compare, variants, err := parseProductInput(in)
	if err != nil {
		return productError(c, 400, "Invalid product values")
	}
	if len(variants) == 0 {
		sku, stock := "", 0
		if in.SKU != nil {
			sku = *in.SKU
		}
		if in.Stock != nil {
			stock = *in.Stock
		}
		variants = []parsedProductVariant{{Name: "", SKU: sku, Price: price, CompareAtPrice: compare, Stock: stock, Status: "active"}}
	}
	tx := data.DB.Begin()
	if tx.Error != nil {
		return productError(c, 500, "could not start product transaction")
	}
	defer tx.Rollback()
	if err := validateCategoryIDs(tx, in.CategoryIDs); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return productError(c, 400, "unknown category")
		}
		return productError(c, 500, "could not validate categories")
	}
	p := core.Product{}
	if err := applyProductInput(tx, &p, in, price, compare, true); err != nil {
		return productError(c, 400, "Invalid product values")
	}
	if err := tx.Select("title", "slug", "description", "price", "sku", "stock", "images", "status", "compare_at_price", "low_stock_threshold", "requires_shipping", "cold_chain", "attributes", "related_ids", "seo_title", "seo_description", "kind").Create(&p).Error; err != nil {
		return productDBError(c, err)
	}
	stock, err := saveVariantSet(tx, p.ID, variants, true)
	if err != nil {
		return productDBError(c, err)
	}
	p.Stock = stock
	if err := tx.Model(&p).Select("sku", "stock").Updates(&p).Error; err != nil {
		return productDBError(c, err)
	}
	if err := replaceProductCategories(tx, p.ID, in.CategoryIDs); err != nil {
		return productDBError(c, err)
	}
	if err := tx.Commit().Error; err != nil {
		return productDBError(c, err)
	}
	view, err := loadAdminProduct(data.DB, &p)
	if err != nil {
		return productError(c, 500, "product saved but could not load response")
	}
	return c.JSON(200, view)
}

func adminProductsUpdate(c echo.Context) error {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		return productError(c, 404, "Product not found")
	}
	var in productInput
	if err := c.Bind(&in); err != nil {
		return productError(c, 400, "Invalid request")
	}
	price, compare, variants, err := parseProductInput(in)
	if err != nil {
		return productError(c, 400, "Invalid product values")
	}
	tx := data.DB.Begin()
	if tx.Error != nil {
		return productError(c, 500, "could not start product transaction")
	}
	defer tx.Rollback()
	var p core.Product
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&p, uint(id)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return productError(c, 404, "Product not found")
		}
		return productError(c, 500, "could not load product")
	}
	if err := validateCategoryIDs(tx, in.CategoryIDs); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return productError(c, 400, "unknown category")
		}
		return productError(c, 500, "could not validate categories")
	}
	if err := applyProductInput(tx, &p, in, price, compare, false); err != nil {
		return productError(c, 400, "Invalid product values")
	}
	stock, err := saveVariantSet(tx, p.ID, variants, in.Variants != nil)
	if err != nil {
		if errors.Is(err, gorm.ErrInvalidData) {
			return productError(c, 409, "at least one variant is required")
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return productError(c, 400, "variant does not belong to product")
		}
		return productDBError(c, err)
	}
	p.Stock = stock
	if in.Variants == nil {
		defaultVariant, variantErr := applyLegacyDefaultVariant(tx, p.ID, in, price)
		if errors.Is(variantErr, errLegacyVariantConflict) {
			return productError(c, 409, "legacy SKU or stock requires a single default variant")
		}
		if variantErr != nil {
			return productDBError(c, variantErr)
		}
		if defaultVariant && in.Stock != nil {
			p.Stock = *in.Stock
		}
	}
	if err := tx.Select("title", "slug", "description", "price", "sku", "status", "compare_at_price", "low_stock_threshold", "requires_shipping", "cold_chain", "kind", "seo_title", "seo_description", "images", "attributes", "related_ids", "stock").Save(&p).Error; err != nil {
		return productDBError(c, err)
	}
	if in.CategoryIDs != nil {
		if err := replaceProductCategories(tx, p.ID, in.CategoryIDs); err != nil {
			return productDBError(c, err)
		}
	}
	if err := tx.Commit().Error; err != nil {
		return productDBError(c, err)
	}
	view, err := loadAdminProduct(data.DB, &p)
	if err != nil {
		return productError(c, 500, "product saved but could not load response")
	}
	return c.JSON(200, view)
}

func replaceProductCategories(tx *gorm.DB, productID uint, ids []uint) error {
	if err := tx.Exec("DELETE FROM product_category_links WHERE product_id = ?", productID).Error; err != nil {
		return err
	}
	for _, id := range ids {
		if err := tx.Exec("INSERT INTO product_category_links (product_id, category_id) VALUES (?, ?)", productID, id).Error; err != nil {
			return err
		}
	}
	return nil
}

func productDBError(c echo.Context, err error) error {
	if errors.Is(err, gorm.ErrDuplicatedKey) || strings.Contains(strings.ToLower(err.Error()), "duplicate key") {
		return productError(c, 409, "product or variant value already exists")
	}
	return productError(c, 500, "could not save product")
}

func adminProductsDelete(c echo.Context) error {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		return productError(c, 404, "Product not found")
	}
	res := data.DB.Delete(&core.Product{}, uint(id))
	if res.Error != nil {
		return productError(c, 500, "could not delete product")
	}
	if res.RowsAffected == 0 {
		return productError(c, 404, "Product not found")
	}
	return c.JSON(200, map[string]string{"message": "deleted"})
}

func adminProductsDuplicate(c echo.Context) error {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		return productError(c, 404, "Product not found")
	}
	tx := data.DB.Begin()
	if tx.Error != nil {
		return productError(c, 500, "could not start product transaction")
	}
	defer tx.Rollback()
	var src core.Product
	if err := tx.First(&src, uint(id)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return productError(c, 404, "Product not found")
		}
		return productError(c, 500, "could not load product")
	}
	var vars []core.ProductVariant
	if err := tx.Where("product_id = ?", src.ID).Order("position,id").Find(&vars).Error; err != nil {
		return productError(c, 500, "could not load variants")
	}
	var links []struct{ CategoryID uint }
	if err := tx.Raw("SELECT category_id FROM product_category_links WHERE product_id = ?", src.ID).Scan(&links).Error; err != nil {
		return productError(c, 500, "could not load categories")
	}
	dup := src
	dup.ID = 0
	dup.DeletedAt.Valid = false
	dup.DeletedByID = nil
	dup.Status = "draft"
	base := src.Slug + "-2"
	dup.Slug = base
	for n := 3; ; n++ {
		var count int64
		if err := tx.Model(&core.Product{}).Where("slug = ?", dup.Slug).Count(&count).Error; err != nil {
			return productError(c, 500, "could not check duplicate slug")
		}
		if count == 0 {
			break
		}
		dup.Slug = fmt.Sprintf("%s-%d", src.Slug, n)
	}
	if err := tx.Omit("ID", "CreatedAt", "UpdatedAt", "DeletedAt").Create(&dup).Error; err != nil {
		return productDBError(c, err)
	}
	for _, v := range vars {
		v.ID = 0
		v.ProductID = dup.ID
		if err := tx.Omit("ID", "CreatedAt", "UpdatedAt").Create(&v).Error; err != nil {
			return productDBError(c, err)
		}
	}
	for _, l := range links {
		if err := tx.Exec("INSERT INTO product_category_links (product_id,category_id) VALUES (?,?)", dup.ID, l.CategoryID).Error; err != nil {
			return productDBError(c, err)
		}
	}
	if err := tx.Commit().Error; err != nil {
		return productDBError(c, err)
	}
	view, err := loadAdminProduct(data.DB, &dup)
	if err != nil {
		return productError(c, 500, "duplicate saved but could not load response")
	}
	return c.JSON(200, view)
}

type productCategoryInput struct {
	Name        *string         `json:"name"`
	Slug        *string         `json:"slug"`
	ParentID    json.RawMessage `json:"parent_id"`
	Description *string         `json:"description"`
	Cover       *string         `json:"cover"`
	Position    *int            `json:"position"`
	Hidden      *bool           `json:"hidden"`
}

func adminProductCategoriesList(c echo.Context) error {
	var cats []core.ProductCategory
	if err := data.DB.Order("position ASC,id ASC").Find(&cats).Error; err != nil {
		return productError(c, 500, "could not load categories")
	}
	children := map[uint][]core.ProductCategory{}
	var roots []core.ProductCategory
	for _, cat := range cats {
		if cat.ParentID == nil {
			roots = append(roots, cat)
		} else {
			children[*cat.ParentID] = append(children[*cat.ParentID], cat)
		}
	}
	var build func([]core.ProductCategory) []map[string]interface{}
	build = func(rows []core.ProductCategory) []map[string]interface{} {
		out := make([]map[string]interface{}, 0, len(rows))
		for _, cat := range rows {
			node := map[string]interface{}{"id": cat.ID, "name": cat.Name, "slug": cat.Slug, "parent_id": cat.ParentID, "description": cat.Description, "cover": cat.Cover, "position": cat.Position, "hidden": cat.Hidden}
			node["children"] = build(children[cat.ID])
			out = append(out, node)
		}
		return out
	}
	return c.JSON(200, build(roots))
}

func bindProductCategory(c echo.Context) (productCategoryInput, error) {
	var in productCategoryInput
	err := c.Bind(&in)
	return in, err
}

func parseProductCategoryParent(raw json.RawMessage) (bool, *uint, error) {
	if raw == nil {
		return false, nil, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return true, nil, nil
	}
	var id uint
	if err := json.Unmarshal(raw, &id); err != nil || id == 0 {
		return true, nil, errors.New("invalid parent")
	}
	return true, &id, nil
}

func parentExists(tx *gorm.DB, id *uint) error {
	if id == nil {
		return nil
	}
	if *id == 0 {
		return errors.New("invalid parent")
	}
	var cat core.ProductCategory
	if err := tx.First(&cat, *id).Error; err != nil {
		return err
	}
	return nil
}

func adminProductCategoriesCreate(c echo.Context) error {
	in, err := bindProductCategory(c)
	if err != nil || in.Name == nil || strings.TrimSpace(*in.Name) == "" {
		return productError(c, 400, "name is required")
	}
	name := strings.TrimSpace(*in.Name)
	slug := core.Slugify(name, "")
	if in.Slug != nil && strings.TrimSpace(*in.Slug) != "" {
		slug = strings.TrimSpace(*in.Slug)
	}
	_, parent, err := parseProductCategoryParent(in.ParentID)
	if err != nil {
		return productError(c, 400, "invalid parent category")
	}
	tx := data.DB.Begin()
	if tx.Error != nil {
		return productError(c, 500, "could not start category transaction")
	}
	defer tx.Rollback()
	if err := lockProductCategoryTree(tx); err != nil {
		return productError(c, 500, "could not lock category tree")
	}
	if err := parentExists(tx, parent); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return productError(c, 400, "parent category not found")
		}
		return productError(c, 400, "invalid parent category")
	}
	cat := core.ProductCategory{Name: name, Slug: slug, ParentID: parent}
	if in.Description != nil {
		cat.Description = *in.Description
	}
	if in.Cover != nil {
		if !validLocalProductURL(*in.Cover) {
			return productError(c, 400, "invalid cover URL")
		}
		cat.Cover = *in.Cover
	}
	if in.Position != nil {
		if *in.Position < 0 {
			return productError(c, 400, "invalid position")
		}
		cat.Position = *in.Position
	}
	if in.Hidden != nil {
		cat.Hidden = *in.Hidden
	}
	if err := tx.Create(&cat).Error; err != nil {
		return productDBError(c, err)
	}
	if err := tx.Commit().Error; err != nil {
		return productDBError(c, err)
	}
	return c.JSON(201, cat)
}

func adminProductCategoriesUpdate(c echo.Context) error {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		return productError(c, 404, "category not found")
	}
	in, err := bindProductCategory(c)
	if err != nil {
		return productError(c, 400, "invalid request")
	}
	tx := data.DB.Begin()
	if tx.Error != nil {
		return productError(c, 500, "could not start category transaction")
	}
	defer tx.Rollback()
	if err := lockProductCategoryTree(tx); err != nil {
		return productError(c, 500, "could not lock category tree")
	}
	var cat core.ProductCategory
	if err := tx.First(&cat, uint(id)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return productError(c, 404, "category not found")
		}
		return productError(c, 500, "could not load category")
	}
	if in.Name != nil {
		if strings.TrimSpace(*in.Name) == "" {
			return productError(c, 400, "name is required")
		}
		cat.Name = strings.TrimSpace(*in.Name)
	}
	if in.Slug != nil {
		if strings.TrimSpace(*in.Slug) == "" {
			return productError(c, 400, "slug is required")
		}
		cat.Slug = strings.TrimSpace(*in.Slug)
	}
	parentProvided, parent, err := parseProductCategoryParent(in.ParentID)
	if err != nil {
		return productError(c, 400, "invalid parent category")
	}
	if parentProvided {
		cat.ParentID = parent
		if cat.ParentID != nil {
			if *cat.ParentID == cat.ID {
				return productError(c, 409, "category cycle")
			}
			if err := parentExists(tx, cat.ParentID); err != nil {
				return productError(c, 400, "parent category not found")
			}
			seen := map[uint]bool{cat.ID: true}
			parent := cat.ParentID
			for parent != nil {
				if seen[*parent] {
					return productError(c, 409, "category cycle")
				}
				seen[*parent] = true
				var ancestor core.ProductCategory
				if err := tx.First(&ancestor, *parent).Error; err != nil {
					return productError(c, 400, "invalid parent category")
				}
				parent = ancestor.ParentID
			}
		}
	}
	if in.Description != nil {
		cat.Description = *in.Description
	}
	if in.Cover != nil {
		if !validLocalProductURL(*in.Cover) {
			return productError(c, 400, "invalid cover URL")
		}
		cat.Cover = *in.Cover
	}
	if in.Position != nil {
		if *in.Position < 0 {
			return productError(c, 400, "invalid position")
		}
		cat.Position = *in.Position
	}
	if in.Hidden != nil {
		cat.Hidden = *in.Hidden
	}
	if err := tx.Select("name", "slug", "parent_id", "description", "cover", "position", "hidden").Save(&cat).Error; err != nil {
		return productDBError(c, err)
	}
	if err := tx.Commit().Error; err != nil {
		return productDBError(c, err)
	}
	return c.JSON(200, cat)
}

func lockProductCategoryTree(tx *gorm.DB) error {
	return tx.Exec("SELECT pg_advisory_xact_lock(?)", int64(707025)).Error
}

func adminProductCategoriesDelete(c echo.Context) error {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		return productError(c, 404, "category not found")
	}
	tx := data.DB.Begin()
	if tx.Error != nil {
		return productError(c, 500, "could not start category transaction")
	}
	defer tx.Rollback()
	var cat core.ProductCategory
	if err := tx.First(&cat, uint(id)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return productError(c, 404, "category not found")
		}
		return productError(c, 500, "could not load category")
	}
	var childCount, linkCount int64
	if err := tx.Model(&core.ProductCategory{}).Where("parent_id = ?", cat.ID).Count(&childCount).Error; err != nil {
		return productError(c, 500, "could not inspect category")
	}
	if err := tx.Table("product_category_links").Where("category_id = ?", cat.ID).Count(&linkCount).Error; err != nil {
		return productError(c, 500, "could not inspect category")
	}
	if childCount > 0 || linkCount > 0 {
		return productError(c, 409, "category is in use")
	}
	if err := tx.Delete(&cat).Error; err != nil {
		return productDBError(c, err)
	}
	if err := tx.Commit().Error; err != nil {
		return productDBError(c, err)
	}
	return c.JSON(200, map[string]string{"message": "deleted"})
}
