package server

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/money"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

const shopCatalogPageSize = 12

type shopCatalogPageData struct {
	SiteTitle        string
	Title            string
	CurrentPath      string
	Products         []core.CatalogProductView
	Categories       []shopCategoryCount
	Term             *core.ProductCategory
	Children         []shopCategoryCount
	Breadcrumbs      []core.ProductCategory
	Filters          shopFilters
	Page             int
	TotalPages       int
	Total            int64
	Lang             string
	Currency         string
	ContactEmail     string
	FreeShippingOver int64
	Facets           []shopFacet
}

type shopFacet struct {
	Value, Label string
	Count        int64
}
type shopTrustLine struct{ Icon, Title, Note string }

func configureShopMoney(tm *theme.Manager) {
	if tm == nil {
		return
	}
	tm.RegisterFunc("money", func(minor int64) string { return money.Format(minor, "TWD") })
}

func shopCatalogPage(tm *theme.Manager) echo.HandlerFunc {
	configureShopMoney(tm)
	return func(c echo.Context) error { return renderShopCatalog(tm, c, nil, "shop.html", "商店") }
}

func shopCategoryPage(tm *theme.Manager) echo.HandlerFunc {
	configureShopMoney(tm)
	return func(c echo.Context) error {
		var category core.ProductCategory
		if err := data.DB.Where("slug = ?", c.Param("slug")).First(&category).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return echo.NewHTTPError(http.StatusNotFound)
			}
			return echo.NewHTTPError(500, "could not load category")
		}
		return renderShopCatalog(tm, c, &category, "shop_category.html", category.Name)
	}
}

func renderShopCatalog(tm *theme.Manager, c echo.Context, term *core.ProductCategory, templateName, title string) error {
	page := 1
	if raw := c.QueryParam("page"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			if n > 1000000 {
				return echo.NewHTTPError(http.StatusBadRequest, "invalid page")
			}
			page = n
		}
	}
	filters := shopFilters{Sort: c.QueryParam("sort"), Price: c.QueryParam("price"), InStock: c.QueryParam("instock") == "1", Attr: c.QueryParam("attr")}
	if filters.Sort != "new" && filters.Sort != "popular" && filters.Sort != "price_asc" && filters.Sort != "price_desc" {
		filters.Sort = "new"
	}
	if filters.Price != "lt300" && filters.Price != "300-800" && filters.Price != "gt800" {
		filters.Price = ""
	}
	query := data.DB.Model(&core.Product{}).Where("products.status = ? AND products.deleted_at IS NULL", "active")
	if term != nil {
		catIDs, err := shopCategorySubtreeIDs(term.ID)
		if err != nil {
			return echo.NewHTTPError(500, "could not load category tree")
		}
		query = query.Where("EXISTS (SELECT 1 FROM product_category_links pcl WHERE pcl.product_id = products.id AND pcl.category_id IN ?)", catIDs)
	}
	switch filters.Price {
	case "lt300":
		query = query.Where("COALESCE((SELECT MIN(pv.price) FROM product_variants pv WHERE pv.product_id = products.id AND pv.status = 'active'), products.price) < ?", 30000)
	case "300-800":
		query = query.Where("COALESCE((SELECT MIN(pv.price) FROM product_variants pv WHERE pv.product_id = products.id AND pv.status = 'active'), products.price) >= ? AND COALESCE((SELECT MIN(pv.price) FROM product_variants pv WHERE pv.product_id = products.id AND pv.status = 'active'), products.price) <= ?", 30000, 80000)
	case "gt800":
		query = query.Where("COALESCE((SELECT MIN(pv.price) FROM product_variants pv WHERE pv.product_id = products.id AND pv.status = 'active'), products.price) > ?", 80000)
	}
	if filters.InStock {
		query = query.Where("EXISTS (SELECT 1 FROM product_variants pv WHERE pv.product_id = products.id AND pv.status = 'active' AND pv.stock > 0)")
	}
	if filters.Attr != "" {
		key, value, ok := strings.Cut(filters.Attr, "=")
		if !ok {
			key, value, ok = strings.Cut(filters.Attr, ":")
		}
		if !ok || key == "" {
			filters.Attr = ""
		} else {
			query = query.Where("products.attributes ->> ? = ?", key, value)
		}
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return echo.NewHTTPError(500, "could not count products")
	}
	totalPages := int((total + shopCatalogPageSize - 1) / shopCatalogPageSize)
	if page > totalPages && totalPages > 0 {
		return echo.NewHTTPError(http.StatusNotFound)
	}
	if totalPages == 0 && page > 1 {
		return echo.NewHTTPError(http.StatusNotFound)
	}
	order := "products.created_at DESC, products.id DESC"
	switch filters.Sort {
	case "popular":
		order = "products.sold_count DESC, products.created_at DESC, products.id DESC"
	case "price_asc":
		order = "COALESCE((SELECT MIN(pv.price) FROM product_variants pv WHERE pv.product_id = products.id AND pv.status = 'active'), products.price) ASC, products.id ASC"
	case "price_desc":
		order = "COALESCE((SELECT MIN(pv.price) FROM product_variants pv WHERE pv.product_id = products.id AND pv.status = 'active'), products.price) DESC, products.id DESC"
	}
	var rows []core.Product
	if err := query.Order(order).Limit(shopCatalogPageSize).Offset((page - 1) * shopCatalogPageSize).Find(&rows).Error; err != nil {
		return echo.NewHTTPError(500, "could not load products")
	}
	products, err := shopCatalogProductViews(rows)
	if err != nil {
		return echo.NewHTTPError(500, "could not load product variants")
	}
	categories, err := shopCatalogCategories()
	if err != nil {
		return echo.NewHTTPError(500, "could not load categories")
	}
	children := []shopCategoryCount{}
	breadcrumbs := []core.ProductCategory{}
	if term != nil {
		var childRows []core.ProductCategory
		if err := data.DB.Where("parent_id = ? AND hidden = false", term.ID).Order("position,id").Find(&childRows).Error; err != nil {
			return echo.NewHTTPError(500, "could not load subcategories")
		}
		childCounts, err := shopCategoryCounts(childRows)
		if err != nil {
			return echo.NewHTTPError(500, "could not count subcategories")
		}
		children = childCounts
		breadcrumbs, err = shopCategoryBreadcrumbs(term)
		if err != nil {
			return echo.NewHTTPError(500, "could not load category path")
		}
	}
	currency, contact, freeShipping, err := shopCatalogSettings()
	if err != nil {
		return echo.NewHTTPError(500, "could not load shop settings")
	}
	facets := []shopFacet{}
	if term != nil {
		facets, err = shopCatalogFacets(term, filters)
		if err != nil {
			return echo.NewHTTPError(500, "could not load category facets")
		}
	}
	pageData := shopCatalogPageData{SiteTitle: getSiteTitle(), Title: title, CurrentPath: c.Path(), Products: products, Categories: categories, Term: term, Children: children, Breadcrumbs: breadcrumbs, Filters: filters, Page: page, TotalPages: totalPages, Total: total, Lang: shopLang(c), Currency: currency, ContactEmail: contact, FreeShippingOver: freeShipping, Facets: facets}
	dataMap := map[string]interface{}{
		"SiteTitle": pageData.SiteTitle, "Title": pageData.Title, "CurrentPath": pageData.CurrentPath,
		"Products": pageData.Products, "Categories": pageData.Categories, "Term": pageData.Term,
		"Children": pageData.Children, "Breadcrumbs": pageData.Breadcrumbs, "Filters": pageData.Filters,
		"Page": pageData.Page, "TotalPages": pageData.TotalPages, "Total": pageData.Total,
		"Lang": pageData.Lang, "Currency": pageData.Currency, "ContactEmail": pageData.ContactEmail,
		"FreeShippingOver": pageData.FreeShippingOver, "Facets": pageData.Facets,
	}
	applyPublicSEO(c, data.DB, templateName, dataMap)
	html, err := tm.RenderWithDB(templateName, dataMap, data.DB)
	if err != nil {
		return echo.NewHTTPError(500, "could not render shop page")
	}
	return renderedPublicHTML(c, http.StatusOK, html)
}

func shopCategorySubtreeIDs(root uint) ([]uint, error) {
	var cats []core.ProductCategory
	if err := data.DB.Select("id", "parent_id").Find(&cats).Error; err != nil {
		return nil, err
	}
	children := map[uint][]uint{}
	for _, cat := range cats {
		if cat.ParentID != nil {
			children[*cat.ParentID] = append(children[*cat.ParentID], cat.ID)
		}
	}
	seen := map[uint]bool{}
	ids := []uint{}
	queue := []uint{root}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
		queue = append(queue, children[id]...)
	}
	return ids, nil
}

func shopCatalogFacets(term *core.ProductCategory, filters shopFilters) ([]shopFacet, error) {
	categoryIDs, err := shopCategorySubtreeIDs(term.ID)
	if err != nil {
		return nil, err
	}
	effectivePrice := "COALESCE((SELECT MIN(pv.price) FROM product_variants pv WHERE pv.product_id = p.id AND pv.status = 'active'), p.price)"
	query := data.DB.Table("products p").Select("entry.key || '=' || entry.value AS value, entry.value AS label, COUNT(DISTINCT p.id) AS count").Joins("CROSS JOIN LATERAL jsonb_each_text(COALESCE(p.attributes, '{}'::jsonb)) AS entry(key, value)").Where("p.status = 'active' AND p.deleted_at IS NULL").Where("EXISTS (SELECT 1 FROM product_category_links pcl WHERE pcl.product_id = p.id AND pcl.category_id IN ?)", categoryIDs)
	if filters.Price != "" {
		switch filters.Price {
		case "lt300":
			query = query.Where(effectivePrice+" < ?", 30000)
		case "300-800":
			query = query.Where(effectivePrice+" >= ? AND "+effectivePrice+" <= ?", 30000, 80000)
		case "gt800":
			query = query.Where(effectivePrice+" > ?", 80000)
		}
	}
	if filters.InStock {
		query = query.Where("EXISTS (SELECT 1 FROM product_variants pv WHERE pv.product_id=p.id AND pv.status='active' AND pv.stock>0)")
	}
	var facets []shopFacet
	if err := query.Group("entry.key, entry.value").Order("count DESC, entry.key, entry.value").Limit(100).Scan(&facets).Error; err != nil {
		return nil, err
	}
	if facets == nil {
		facets = []shopFacet{}
	}
	return facets, nil
}

func shopCatalogCategories() ([]shopCategoryCount, error) {
	var rows []core.ProductCategory
	if err := data.DB.Where("hidden = false").Order("position,id").Find(&rows).Error; err != nil {
		return nil, err
	}
	return shopCategoryCounts(rows)
}

func shopCategoryCounts(rows []core.ProductCategory) ([]shopCategoryCount, error) {
	result := make([]shopCategoryCount, 0, len(rows))
	if len(rows) == 0 {
		return result, nil
	}
	ids := make([]uint, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	var counts []struct {
		CategoryID uint
		Count      int64
	}
	err := data.DB.Raw(`WITH RECURSIVE category_tree(ancestor_id, descendant_id) AS (
		SELECT id, id FROM product_categories
		UNION
		SELECT tree.ancestor_id, child.id FROM category_tree tree
		JOIN product_categories child ON child.parent_id = tree.descendant_id
	)
	SELECT tree.ancestor_id AS category_id, COUNT(DISTINCT links.product_id) AS count
	FROM category_tree tree
	JOIN product_category_links links ON links.category_id = tree.descendant_id
	JOIN products p ON p.id = links.product_id AND p.status = 'active' AND p.deleted_at IS NULL
	WHERE tree.ancestor_id IN ? GROUP BY tree.ancestor_id`, ids).Scan(&counts).Error
	if err != nil {
		return nil, err
	}
	byID := map[uint]int64{}
	for _, c := range counts {
		byID[c.CategoryID] = c.Count
	}
	for _, row := range rows {
		result = append(result, shopCategoryCount{ProductCategory: row, Count: byID[row.ID]})
	}
	return result, nil
}

func shopCategoryBreadcrumbs(term *core.ProductCategory) ([]core.ProductCategory, error) {
	all := []core.ProductCategory{}
	if err := data.DB.Find(&all).Error; err != nil {
		return nil, err
	}
	byID := map[uint]core.ProductCategory{}
	for _, cat := range all {
		byID[cat.ID] = cat
	}
	var reverse []core.ProductCategory
	seen := map[uint]bool{}
	cur := term
	for cur != nil && !seen[cur.ID] {
		seen[cur.ID] = true
		reverse = append(reverse, *cur)
		if cur.ParentID == nil {
			break
		}
		parent, ok := byID[*cur.ParentID]
		if !ok {
			break
		}
		cur = &parent
	}
	out := make([]core.ProductCategory, 0, len(reverse)-1)
	for i := len(reverse) - 1; i > 0; i-- {
		out = append(out, reverse[i])
	}
	return out, nil
}

func firstLocalProductImage(raw json.RawMessage) string {
	images := localProductImages(raw)
	if len(images) > 0 {
		return images[0]
	}
	return ""
}

func localProductImages(raw json.RawMessage) []string {
	var images []string
	if json.Unmarshal(raw, &images) != nil {
		return []string{}
	}
	result := make([]string, 0, len(images))
	for _, image := range images {
		if validLocalProductURL(image) && strings.TrimSpace(image) != "" {
			result = append(result, image)
		}
	}
	return result
}

func shopCatalogProductViews(rows []core.Product) ([]core.CatalogProductView, error) {
	views := make([]core.CatalogProductView, 0, len(rows))
	if len(rows) == 0 {
		return views, nil
	}
	ids := make([]uint, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	var variants []core.ProductVariant
	if err := data.DB.Where("product_id IN ? AND status = ?", ids, "active").Order("position ASC, id ASC").Find(&variants).Error; err != nil {
		return nil, err
	}
	byProduct := make(map[uint][]core.ProductVariant, len(rows))
	for _, variant := range variants {
		byProduct[variant.ProductID] = append(byProduct[variant.ProductID], variant)
	}
	maxInt := int(^uint(0) >> 1)
	for _, product := range rows {
		active := byProduct[product.ID]
		stock := 0
		var lowest *core.ProductVariant
		for i := range active {
			variant := &active[i]
			if variant.Stock > maxInt-stock {
				return nil, fmt.Errorf("product %d stock total overflows int", product.ID)
			}
			stock += variant.Stock
			if lowest == nil || variant.Price < lowest.Price {
				lowest = variant
			}
		}
		view := core.CatalogProductView{Product: product, Image: firstLocalProductImage(product.Images), Images: localProductImages(product.Images), Stock: stock, VariantCount: len(active)}
		if lowest != nil {
			view.Price = lowest.Price
			view.CompareAtPrice = lowest.CompareAtPrice
		}
		if len(active) == 1 && active[0].Stock > 0 {
			view.DefaultVariantID = active[0].ID
		}
		view.Summary = product.SEODescription
		if view.Summary == "" {
			view.Summary = theme.Excerpt(renderCommentMarkdown(product.Description), 180)
		}
		view.DescriptionHTML = template.HTML(renderCommentMarkdown(product.Description))
		view.Attributes = catalogAttributes(product.Attributes)
		views = append(views, view)
	}
	return views, nil
}

func catalogAttributes(raw json.RawMessage) []core.CatalogProductAttribute {
	var values map[string]string
	if json.Unmarshal(raw, &values) != nil {
		return []core.CatalogProductAttribute{}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]core.CatalogProductAttribute, 0, len(keys))
	for _, key := range keys {
		result = append(result, core.CatalogProductAttribute{Label: key, Value: values[key]})
	}
	return result
}

func shopLang(c echo.Context) string {
	if lang, ok := c.Get("lang").(string); ok && (lang == "en" || strings.HasPrefix(lang, "en-")) {
		return "en"
	}
	return "zh-TW"
}

func shopCatalogSettings() (string, string, int64, error) {
	currency, err := publicOption(data.DB, "shop_currency")
	if err != nil {
		return "", "", 0, err
	}
	if currency == "" {
		currency = "TWD"
	}
	contact, err := publicOption(data.DB, "shop_contact_email")
	if err != nil {
		return "", "", 0, err
	}
	freeRaw, err := publicOption(data.DB, "shop_free_shipping_over")
	if err != nil {
		return "", "", 0, err
	}
	free, _ := strconv.ParseInt(freeRaw, 10, 64)
	return currency, contact, free, nil
}

func shopTrustLines(c echo.Context) []shopTrustLine {
	lang := shopLang(c)
	lines := []shopTrustLine{}
	shippingRaw, err := publicOption(data.DB, "shop_shipping_methods")
	if err == nil && shippingRaw != "" {
		var methods []struct {
			Label   string `json:"label"`
			ETADays int    `json:"eta_days"`
			Enabled bool   `json:"enabled"`
		}
		if json.Unmarshal([]byte(shippingRaw), &methods) == nil {
			for _, method := range methods {
				if method.Enabled && method.Label != "" {
					note := ""
					if method.ETADays > 0 {
						if lang == "en" {
							note = fmt.Sprintf("%d day delivery", method.ETADays)
						} else {
							note = fmt.Sprintf("約 %d 天送達", method.ETADays)
						}
					}
					lines = append(lines, shopTrustLine{Icon: "truck", Title: method.Label, Note: note})
					break
				}
			}
		}
	}
	methods, paymentErr := shopPaymentMethods()
	if paymentErr == nil && len(methods) > 0 {
		labels := map[string]string{"credit_card": "信用卡", "atm": "ATM 轉帳", "cvs": "超商代碼", "bank_transfer": "銀行轉帳"}
		english := map[string]string{"credit_card": "Credit card", "atm": "ATM transfer", "cvs": "CVS code", "bank_transfer": "Bank transfer"}
		items := make([]string, 0, len(methods))
		for _, method := range methods {
			label := labels[method.ID]
			if lang == "en" {
				label = english[method.ID]
			}
			if label == "" {
				label = method.ID
			}
			items = append(items, label)
		}
		sep, title := "、", "可用付款方式"
		if lang == "en" {
			sep, title = ", ", "Payment options"
		}
		lines = append(lines, shopTrustLine{Icon: "card", Title: title, Note: strings.Join(items, sep)})
	}
	refundRaw, err := publicOption(data.DB, "shop_refund_window_days")
	if err == nil {
		if days, e := strconv.Atoi(refundRaw); e == nil && days > 0 {
			title, note := "退貨期限", fmt.Sprintf("%d 天內可申請", days)
			if lang == "en" {
				title, note = "Returns", fmt.Sprintf("Request within %d days", days)
			}
			lines = append(lines, shopTrustLine{Icon: "undo", Title: title, Note: note})
		}
	}
	return lines
}

func shopProductPage(tm *theme.Manager) echo.HandlerFunc {
	configureShopMoney(tm)
	return func(c echo.Context) error {
		var product core.Product
		if err := data.DB.Where("slug = ? AND status = ?", c.Param("slug"), "active").First(&product).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return echo.NewHTTPError(404)
			}
			return echo.NewHTTPError(500, "could not load product")
		}
		var variants []core.ProductVariant
		if err := data.DB.Where("product_id = ? AND status = ?", product.ID, "active").Order("position,id").Find(&variants).Error; err != nil {
			return echo.NewHTTPError(500, "could not load product variants")
		}
		if variants == nil {
			variants = []core.ProductVariant{}
		}
		variantViews := make([]core.CatalogVariantView, 0, len(variants))
		for _, v := range variants {
			variantViews = append(variantViews, core.CatalogVariantView{ProductVariant: v, Available: v.Stock > 0})
		}
		var category *core.ProductCategory
		var link struct{ CategoryID uint }
		if err := data.DB.Table("product_category_links").Select("category_id").Where("product_id = ?", product.ID).Order("category_id").Limit(1).Scan(&link).Error; err != nil {
			return echo.NewHTTPError(500, "could not load product category")
		}
		if link.CategoryID != 0 {
			var row core.ProductCategory
			if err := data.DB.Where("hidden = false").First(&row, link.CategoryID).Error; err == nil {
				category = &row
			} else if err != gorm.ErrRecordNotFound {
				return echo.NewHTTPError(500, "could not load product category")
			}
		}
		var relatedRows []core.Product
		if err := data.DB.Model(&core.Product{}).Where("status = ? AND deleted_at IS NULL AND id <> ? AND id IN ?", "active", product.ID, relatedIDs(product.RelatedIDs)).Limit(8).Find(&relatedRows).Error; err != nil {
			return echo.NewHTTPError(500, "could not load related products")
		}
		related, err := shopCatalogProductViews(relatedRows)
		if err != nil {
			return echo.NewHTTPError(500, "could not load related product variants")
		}
		productView := core.CatalogProductView{Product: product, Image: firstLocalProductImage(product.Images), Images: localProductImages(product.Images)}
		maxInt := int(^uint(0) >> 1)
		var lowestVariant *core.ProductVariant
		for i := range variants {
			if variants[i].Stock > maxInt-productView.Stock {
				return echo.NewHTTPError(500, "product stock total overflows int")
			}
			productView.Stock += variants[i].Stock
			if lowestVariant == nil || variants[i].Price < lowestVariant.Price {
				lowestVariant = &variants[i]
			}
		}
		if lowestVariant != nil {
			productView.Price = lowestVariant.Price
			productView.CompareAtPrice = lowestVariant.CompareAtPrice
		}
		if len(variants) == 1 && variants[0].Stock > 0 {
			productView.DefaultVariantID = variants[0].ID
		}
		productView.VariantCount = len(variants)
		productView.Summary = product.SEODescription
		if productView.Summary == "" {
			productView.Summary = theme.Excerpt(renderCommentMarkdown(product.Description), 180)
		}
		productView.DescriptionHTML = template.HTML(renderCommentMarkdown(product.Description))
		productView.Attributes = catalogAttributes(product.Attributes)
		availability := "https://schema.org/OutOfStock"
		for _, v := range variants {
			if v.Stock > 0 {
				availability = "https://schema.org/InStock"
				break
			}
		}
		currency, contact, freeShipping, err := shopCatalogSettings()
		if err != nil {
			return echo.NewHTTPError(500, "could not load shop settings")
		}
		jsonLD := map[string]interface{}{"@context": "https://schema.org", "@type": "Product", "name": product.Title, "description": product.Description, "sku": product.SKU, "offers": map[string]interface{}{"@type": "Offer", "price": shopJSONLDPrice(productView.Price), "priceCurrency": currency, "availability": availability, "url": "/shop/p/" + url.PathEscape(product.Slug)}}
		jsonLDBytes, err := json.Marshal(jsonLD)
		if err != nil {
			return echo.NewHTTPError(500, "could not encode product data")
		}
		jsonLDString := strings.ReplaceAll(string(jsonLDBytes), "<", "\\u003c")
		jsonLDString = strings.ReplaceAll(jsonLDString, ">", "\\u003e")
		jsonLDString = strings.ReplaceAll(jsonLDString, "&", "\\u0026")
		trust := shopTrustLines(c)
		dataMap := map[string]interface{}{"SiteTitle": getSiteTitle(), "Title": product.Title, "CurrentPath": c.Path(), "Lang": shopLang(c), "Currency": currency, "ContactEmail": contact, "FreeShippingOver": freeShipping, "Product": productView, "Variants": variantViews, "Category": category, "Related": related, "Trust": trust, "JSONLD": jsonLD, "JSONLDHTML": template.HTML(jsonLDString), "DescriptionHTML": productView.DescriptionHTML}
		applyPublicSEO(c, data.DB, "product.html", dataMap)
		html, err := tm.RenderWithDB("product.html", dataMap, data.DB)
		if err != nil {
			return echo.NewHTTPError(500, fmt.Sprintf("could not render product page: %v", err))
		}
		return renderedPublicHTML(c, http.StatusOK, html)
	}
}

func relatedIDs(raw json.RawMessage) []uint {
	var ids []uint
	if json.Unmarshal(raw, &ids) != nil {
		return []uint{}
	}
	return ids
}

func shopJSONLDPrice(minor int64) string {
	whole, fraction := minor/100, minor%100
	if fraction == 0 {
		return strconv.FormatInt(whole, 10)
	}
	return strings.TrimRight(fmt.Sprintf("%d.%02d", whole, fraction), "0")
}
