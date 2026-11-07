package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"gorm.io/gorm"
)

// Plugin read API (docs/tickets/29-plugin-read-api-and-extensions.md, PR A).
//
// Plugins with the content.read permission may read posts and pages; plugins
// with shop.read may read products and orders. The objects reuse the event
// payload builders (postEventPayload, productEventPayload, orderEventPayload)
// so a plugin learns one shape. Rows in the trash are never returned.

const (
	pluginReadDefaultPerPage = 20
	pluginReadMaxPerPage     = 100
	// pluginReadResultMax is the host API result bound; a list that encodes
	// larger than this is refused with "result exceeds 1 MiB".
	pluginReadResultMax = pluginHostJSONMax
	// pluginReadFilterMax is the longest accepted string filter, in characters.
	pluginReadFilterMax = 160
	// pluginReadRefMax is the longest accepted slug or order number, in bytes.
	pluginReadRefMax = 255
)

// pluginReadQuery is the decoded query object of a list call. Zero values mean
// "no filter"; Page and PerPage are normalised by decodePluginReadQuery.
type pluginReadQuery struct {
	Type       string
	Status     string
	Visibility string
	Category   string
	Tag        string
	Search     string
	Email      string
	Page       int
	PerPage    int
}

// pluginReadWireQuery is the JSON form of a list query. Unknown keys are ignored.
type pluginReadWireQuery struct {
	Type       string `json:"type"`
	Status     string `json:"status"`
	Visibility string `json:"visibility"`
	Category   string `json:"category"`
	Tag        string `json:"tag"`
	Search     string `json:"search"`
	Email      string `json:"email"`
	Page       int    `json:"page"`
	PerPage    int    `json:"per_page"`
}

// decodePluginReadQuery decodes args[0] (optional) into a query. Wanted results:
//   - no argument, null or {} → defaults (page 1, per_page 20)
//   - page < 1 or per_page < 1 → defaults; per_page > 100 → 100
//   - unknown keys are ignored; non-object argument → error
//   - string filters longer than 160 characters → error
func decodePluginReadQuery(args []json.RawMessage) (pluginReadQuery, error) {
	query := pluginReadQuery{Page: 1, PerPage: pluginReadDefaultPerPage}
	if len(args) == 0 {
		return query, nil
	}
	var wire pluginReadWireQuery
	if err := json.Unmarshal(args[0], &wire); err != nil {
		return pluginReadQuery{}, errors.New("list query must be an object")
	}
	filters := []string{wire.Type, wire.Status, wire.Visibility, wire.Category, wire.Tag, wire.Search, wire.Email}
	for _, filter := range filters {
		if utf8.RuneCountInString(filter) > pluginReadFilterMax {
			return pluginReadQuery{}, errors.New("list filter is longer than 160 characters")
		}
	}
	query.Type = strings.TrimSpace(wire.Type)
	query.Status = strings.TrimSpace(wire.Status)
	query.Visibility = strings.TrimSpace(wire.Visibility)
	query.Category = strings.TrimSpace(wire.Category)
	query.Tag = strings.TrimSpace(wire.Tag)
	query.Search = strings.TrimSpace(wire.Search)
	query.Email = strings.TrimSpace(wire.Email)
	if wire.Page >= 1 {
		query.Page = wire.Page
	}
	if wire.PerPage >= 1 {
		query.PerPage = wire.PerPage
	}
	if query.PerPage > pluginReadMaxPerPage {
		query.PerPage = pluginReadMaxPerPage
	}
	return query, nil
}

// pluginReadRef decodes args[0] as a row reference: a positive JSON number is
// an id, a non-empty string (≤ 255 bytes) is a slug or order number. Anything
// else is an error.
func pluginReadRef(args []json.RawMessage) (id uint, key string, err error) {
	if len(args) == 0 {
		return 0, "", errors.New("read reference is missing")
	}
	decoder := json.NewDecoder(bytes.NewReader(args[0]))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return 0, "", errors.New("read reference is invalid")
	}
	switch v := value.(type) {
	case json.Number:
		parsed, ok := pluginReadPositiveInt(v.String())
		if !ok {
			return 0, "", errors.New("read reference must be a positive id")
		}
		return uint(parsed), "", nil
	case string:
		if v == "" || len(v) > pluginReadRefMax {
			return 0, "", errors.New("read reference string is invalid")
		}
		return 0, v, nil
	}
	return 0, "", errors.New("read reference must be an id or a string")
}

// pluginReadPositiveInt parses a JSON number text that is a positive integer,
// either written as an integer or as a whole float such as 3.0 or 1e2.
func pluginReadPositiveInt(text string) (uint64, bool) {
	if n, err := strconv.ParseUint(text, 10, 63); err == nil {
		return n, n >= 1
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil || f != math.Trunc(f) || f < 1 || f >= 9.223372036854775807e18 {
		return 0, false
	}
	return uint64(f), true
}

// handlePluginHostRead serves the read methods after the permission check made
// by handlePluginHostCall. method is one of content.get, content.list,
// shop.product.get, shop.product.list, shop.order.get, shop.order.list.
// Wanted results:
//   - get methods return the object or JSON null when the row is missing or trashed
//   - list methods return {"items":[…],"total":n,"page":p,"per_page":s}
//   - content.list items omit "content" and carry content_omitted: true;
//     shop.order.list items omit "items"
//   - every read uses db bound to the host call ctx with a 2 second timeout (pluginReadDB)
//   - a result larger than pluginReadResultMax is refused with an error
func handlePluginHostRead(ctx context.Context, db *gorm.DB, meta plugin.PluginMetadata, method string, args []json.RawMessage) (json.RawMessage, error) {
	switch method {
	case "content.get":
		id, key, err := pluginReadRef(args)
		if err != nil {
			return nil, err
		}
		post, err := pluginReadPost(ctx, db, id, key)
		if err != nil {
			return nil, err
		}
		return pluginReadEncodeContent(post)
	case "content.list":
		query, err := decodePluginReadQuery(args)
		if err != nil {
			return nil, err
		}
		items, total, err := pluginReadPosts(ctx, db, query)
		if err != nil {
			return nil, err
		}
		return pluginReadEncodeList(items, total, query)
	case "shop.product.get":
		id, key, err := pluginReadRef(args)
		if err != nil {
			return nil, err
		}
		product, err := pluginReadProduct(ctx, db, id, key)
		if err != nil {
			return nil, err
		}
		return pluginReadEncode(product)
	case "shop.product.list":
		query, err := decodePluginReadQuery(args)
		if err != nil {
			return nil, err
		}
		items, total, err := pluginReadProducts(ctx, db, query)
		if err != nil {
			return nil, err
		}
		return pluginReadEncodeList(items, total, query)
	case "shop.order.get":
		id, number, err := pluginReadRef(args)
		if err != nil {
			return nil, err
		}
		order, err := pluginReadOrder(ctx, db, id, number)
		if err != nil {
			return nil, err
		}
		return pluginReadEncode(order)
	case "shop.order.list":
		query, err := decodePluginReadQuery(args)
		if err != nil {
			return nil, err
		}
		items, total, err := pluginReadOrders(ctx, db, query)
		if err != nil {
			return nil, err
		}
		return pluginReadEncodeList(items, total, query)
	default:
		return nil, errors.New("unknown plugin read method")
	}
}

// pluginReadMarshal encodes value as JSON without HTML escaping, so <, > and &
// keep their one-byte form. The encoder's trailing newline is dropped.
func pluginReadMarshal(value any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// pluginReadEncode marshals one read result and enforces the result size bound.
// A nil map encodes as JSON null.
func pluginReadEncode(value any) (json.RawMessage, error) {
	raw, err := pluginReadMarshal(value)
	if err != nil {
		return nil, errors.New("plugin read result could not be encoded")
	}
	if len(raw) > pluginReadResultMax {
		return nil, errors.New("result exceeds 1 MiB")
	}
	return raw, nil
}

// pluginReadEncodeContent encodes a content.get result. When the encoded object
// exceeds the bound and carries a "content" key, the content is dropped, the
// object is marked content_omitted and encoded once more. A second overflow, or
// an object without "content", is refused with "result exceeds 1 MiB".
func pluginReadEncodeContent(post map[string]any) (json.RawMessage, error) {
	raw, err := pluginReadMarshal(post)
	if err != nil {
		return nil, errors.New("plugin read result could not be encoded")
	}
	if len(raw) <= pluginReadResultMax {
		return raw, nil
	}
	if _, has := post["content"]; !has {
		return nil, errors.New("result exceeds 1 MiB")
	}
	delete(post, "content")
	post["content_omitted"] = true
	return pluginReadEncode(post)
}

// pluginReadEncodeList wraps one page of items in the list envelope.
func pluginReadEncodeList(items []map[string]any, total int64, query pluginReadQuery) (json.RawMessage, error) {
	if items == nil {
		items = []map[string]any{}
	}
	return pluginReadEncode(map[string]any{
		"items":    items,
		"total":    total,
		"page":     query.Page,
		"per_page": query.PerPage,
	})
}

// pluginReadOffset returns the row offset of the requested page. A page so far
// out that its offset would not fit an int32 is refused.
func pluginReadOffset(query pluginReadQuery) (int, error) {
	perPage := query.PerPage
	if perPage < 1 {
		perPage = pluginReadDefaultPerPage
	}
	if query.Page-1 > math.MaxInt32/perPage {
		return 0, errors.New("list page is too large")
	}
	return (query.Page - 1) * perPage, nil
}

// pluginReadLikePattern builds an ILIKE pattern that matches text anywhere in
// the value, with LIKE wildcards in text taken literally.
func pluginReadLikePattern(text string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(text)
	return "%" + escaped + "%"
}

// pluginReadPost loads one post or page by id or slug, excluding the trash,
// with categories and tags, and returns postEventPayload or nil.
func pluginReadPost(ctx context.Context, db *gorm.DB, id uint, slug string) (map[string]any, error) {
	db, cancel := pluginReadDB(ctx, db)
	defer cancel()
	query := db.Model(&core.Post{}).Preload("Categories").Preload("Tags")
	if id > 0 {
		query = query.Where("id = ?", id)
	} else {
		query = query.Where("slug = ?", slug)
	}
	var post core.Post
	if err := query.Take(&post).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, errors.New("content read failed")
	}
	return postEventPayload(post), nil
}

// pluginReadPostColumns are the columns a content.list row loads. content is
// left out; id stays for the category and tag preloads.
const pluginReadPostColumns = "id, type, slug, title, status, visibility, excerpt, format, author_id, published_at, created_at, updated_at, pinned"

// pluginReadPosts lists posts and pages. Filters: Type (post|page), Status,
// Visibility, Category (slug), Tag (slug), Search (ILIKE on title). Order:
// published_at DESC NULLS LAST, id DESC. Items carry no content.
func pluginReadPosts(ctx context.Context, db *gorm.DB, q pluginReadQuery) (items []map[string]any, total int64, err error) {
	db, cancel := pluginReadDB(ctx, db)
	defer cancel()
	filtered := func() *gorm.DB {
		query := db.Model(&core.Post{})
		if q.Type != "" {
			query = query.Where("type = ?", q.Type)
		}
		if q.Status != "" {
			query = query.Where("status = ?", q.Status)
		}
		if q.Visibility != "" {
			query = query.Where("visibility = ?", q.Visibility)
		}
		if q.Search != "" {
			query = query.Where("title ILIKE ?", pluginReadLikePattern(q.Search))
		}
		if q.Category != "" {
			query = query.Where("id IN (SELECT pc.post_id FROM post_categories pc JOIN categories c ON c.id = pc.category_id WHERE c.slug = ?)", q.Category)
		}
		if q.Tag != "" {
			query = query.Where("id IN (SELECT pt.post_id FROM post_tags pt JOIN tags t ON t.id = pt.tag_id WHERE t.slug = ?)", q.Tag)
		}
		return query
	}
	if err := filtered().Count(&total).Error; err != nil {
		return nil, 0, errors.New("content list failed")
	}
	offset, err := pluginReadOffset(q)
	if err != nil {
		return nil, 0, err
	}
	var posts []core.Post
	if err := filtered().Select(pluginReadPostColumns).
		Order("published_at DESC NULLS LAST, id DESC").
		Offset(offset).Limit(q.PerPage).
		Preload("Categories").Preload("Tags").
		Find(&posts).Error; err != nil {
		return nil, 0, errors.New("content list failed")
	}
	items = make([]map[string]any, 0, len(posts))
	for _, post := range posts {
		item := postEventPayload(post)
		delete(item, "content")
		item["content_omitted"] = true
		items = append(items, item)
	}
	return items, total, nil
}

// pluginReadProduct loads one product by id or slug, excluding the trash.
func pluginReadProduct(ctx context.Context, db *gorm.DB, id uint, slug string) (map[string]any, error) {
	db, cancel := pluginReadDB(ctx, db)
	defer cancel()
	query := db.Model(&core.Product{})
	if id > 0 {
		query = query.Where("id = ?", id)
	} else {
		query = query.Where("slug = ?", slug)
	}
	var product core.Product
	if err := query.Take(&product).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, errors.New("product read failed")
	}
	return productEventPayload(product), nil
}

// pluginReadProducts lists products (filter Status), order id DESC.
func pluginReadProducts(ctx context.Context, db *gorm.DB, q pluginReadQuery) (items []map[string]any, total int64, err error) {
	db, cancel := pluginReadDB(ctx, db)
	defer cancel()
	filtered := func() *gorm.DB {
		query := db.Model(&core.Product{})
		if q.Status != "" {
			query = query.Where("status = ?", q.Status)
		}
		return query
	}
	if err := filtered().Count(&total).Error; err != nil {
		return nil, 0, errors.New("product list failed")
	}
	offset, err := pluginReadOffset(q)
	if err != nil {
		return nil, 0, err
	}
	var products []core.Product
	if err := filtered().Order("id DESC").Offset(offset).Limit(q.PerPage).Find(&products).Error; err != nil {
		return nil, 0, errors.New("product list failed")
	}
	items = make([]map[string]any, 0, len(products))
	for _, product := range products {
		items = append(items, productEventPayload(product))
	}
	return items, total, nil
}

// pluginReadOrder loads one order by id or number with its items.
func pluginReadOrder(ctx context.Context, db *gorm.DB, id uint, number string) (map[string]any, error) {
	db, cancel := pluginReadDB(ctx, db)
	defer cancel()
	query := db.Model(&core.Order{})
	if id > 0 {
		query = query.Where("id = ?", id)
	} else {
		query = query.Where("number = ?", number)
	}
	var order core.Order
	if err := query.Take(&order).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, errors.New("order read failed")
	}
	items := []core.OrderItem{}
	if err := db.Model(&core.OrderItem{}).Where("order_id = ?", order.ID).Order("id").Find(&items).Error; err != nil {
		return nil, errors.New("order read failed")
	}
	if items == nil {
		items = []core.OrderItem{}
	}
	return orderEventPayload(order, items), nil
}

// pluginReadOrders lists orders (filters Status, Email), order id DESC, no items.
func pluginReadOrders(ctx context.Context, db *gorm.DB, q pluginReadQuery) (items []map[string]any, total int64, err error) {
	db, cancel := pluginReadDB(ctx, db)
	defer cancel()
	filtered := func() *gorm.DB {
		query := db.Model(&core.Order{})
		if q.Status != "" {
			query = query.Where("status = ?", q.Status)
		}
		if q.Email != "" {
			query = query.Where("LOWER(email) = LOWER(?)", q.Email)
		}
		return query
	}
	if err := filtered().Count(&total).Error; err != nil {
		return nil, 0, errors.New("order list failed")
	}
	offset, err := pluginReadOffset(q)
	if err != nil {
		return nil, 0, err
	}
	var orders []core.Order
	if err := filtered().Order("id DESC").Offset(offset).Limit(q.PerPage).Find(&orders).Error; err != nil {
		return nil, 0, errors.New("order list failed")
	}
	items = make([]map[string]any, 0, len(orders))
	for _, order := range orders {
		items = append(items, orderEventPayload(order, nil))
	}
	return items, total, nil
}
