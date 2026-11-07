package server

import (
	"context"
	"encoding/json"

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

// decodePluginReadQuery decodes args[0] (optional) into a query. Wanted results:
//   - no argument, null or {} → defaults (page 1, per_page 20)
//   - page < 1 or per_page < 1 → defaults; per_page > 100 → 100
//   - unknown keys are ignored; non-object argument → error
//   - string filters longer than 160 characters → error
func decodePluginReadQuery(args []json.RawMessage) (pluginReadQuery, error) {
	return pluginReadQuery{Page: 1, PerPage: pluginReadDefaultPerPage}, nil
}

// pluginReadRef decodes args[0] as a row reference: a positive JSON number is
// an id, a non-empty string (≤ 255 bytes) is a slug or order number. Anything
// else is an error.
func pluginReadRef(args []json.RawMessage) (id uint, key string, err error) {
	return 0, "", nil
}

// handlePluginHostRead serves the read methods after the permission check made
// by handlePluginHostCall. method is one of content.get, content.list,
// shop.product.get, shop.product.list, shop.order.get, shop.order.list.
// Wanted results:
//   - get methods return the object or JSON null when the row is missing or trashed
//   - list methods return {"items":[…],"total":n,"page":p,"per_page":s}
//   - content.list items omit "content" and carry content_omitted: true;
//     shop.order.list items omit "items"
//   - every read uses db with a 2 second timeout (pluginEventReadDB)
//   - a result larger than pluginReadResultMax is refused with an error
func handlePluginHostRead(ctx context.Context, db *gorm.DB, meta plugin.PluginMetadata, method string, args []json.RawMessage) (json.RawMessage, error) {
	return nil, nil
}

// pluginReadPost loads one post or page by id or slug, excluding the trash,
// with categories and tags, and returns postEventPayload or nil.
func pluginReadPost(ctx context.Context, db *gorm.DB, id uint, slug string) (map[string]any, error) {
	return nil, nil
}

// pluginReadPosts lists posts and pages. Filters: Type (post|page), Status,
// Visibility, Category (slug), Tag (slug), Search (ILIKE on title). Order:
// published_at DESC NULLS LAST, id DESC. Items carry no content.
func pluginReadPosts(ctx context.Context, db *gorm.DB, q pluginReadQuery) (items []map[string]any, total int64, err error) {
	return nil, 0, nil
}

// pluginReadProduct loads one product by id or slug, excluding the trash.
func pluginReadProduct(ctx context.Context, db *gorm.DB, id uint, slug string) (map[string]any, error) {
	return nil, nil
}

// pluginReadProducts lists products (filter Status), order id DESC.
func pluginReadProducts(ctx context.Context, db *gorm.DB, q pluginReadQuery) (items []map[string]any, total int64, err error) {
	return nil, 0, nil
}

// pluginReadOrder loads one order by id or number with its items.
func pluginReadOrder(ctx context.Context, db *gorm.DB, id uint, number string) (map[string]any, error) {
	return nil, nil
}

// pluginReadOrders lists orders (filters Status, Email), order id DESC, no items.
func pluginReadOrders(ctx context.Context, db *gorm.DB, q pluginReadQuery) (items []map[string]any, total int64, err error) {
	return nil, 0, nil
}
