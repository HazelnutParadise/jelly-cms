package seo

import (
	"context"
	"encoding/xml"
	"fmt"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
	"gorm.io/gorm"
)

// Sitemap part kinds, used in /sitemap-<kind>-<n>.xml.
const (
	KindPosts      = "posts"
	KindPages      = "pages"
	KindCategories = "categories"
	KindProducts   = "products"
)

// sitemapKinds lists the kinds in index order.
var sitemapKinds = []string{KindPosts, KindPages, KindCategories, KindProducts}

// sitemapTypeName maps each kind to the seo_index_types key that governs it.
var sitemapTypeName = map[string]string{
	KindPosts:      "post",
	KindPages:      "page",
	KindCategories: "category",
	KindProducts:   "product",
}

type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

type sitemapSet struct {
	XMLName xml.Name     `xml:"urlset"`
	Xmlns   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapRef struct {
	Loc string `xml:"loc"`
}

type sitemapIndexDoc struct {
	XMLName  xml.Name     `xml:"sitemapindex"`
	Xmlns    string       `xml:"xmlns,attr"`
	Sitemaps []sitemapRef `xml:"sitemap"`
}

const sitemapNamespace = "http://www.sitemaps.org/schemas/sitemap/0.9"

// publicPosts scopes posts or pages to the public, published and indexable ones.
func publicPosts(db *gorm.DB, typ string) *gorm.DB {
	return db.Model(&core.Post{}).
		Where("type = ? AND status = ? AND (visibility = ? OR visibility = '' OR visibility IS NULL) AND deleted_at IS NULL AND COALESCE(published_at, created_at) <= ?",
			typ, "published", "public", time.Now().UTC()).
		Where("seo_index <> ?", "noindex")
}

// publicProducts scopes products to the active, indexable and not trashed ones.
func publicProducts(db *gorm.DB) *gorm.DB {
	return db.Model(&core.Product{}).
		Where("status = ? AND deleted_by_id IS NULL AND deleted_at IS NULL AND seo_index <> ?", "active", "noindex")
}

// kindScope returns the query of the items a kind lists, or nil for an
// unknown kind.
func kindScope(db *gorm.DB, kind string) *gorm.DB {
	switch kind {
	case KindPosts:
		return publicPosts(db, "post")
	case KindPages:
		return publicPosts(db, "page")
	case KindCategories:
		return db.Model(&core.Category{})
	case KindProducts:
		return publicProducts(db)
	}
	return nil
}

// kindCount counts the URLs of one kind. A kind that seo_index_types turns
// off counts as zero.
func kindCount(ctx context.Context, db *gorm.DB, types map[string]bool, kind string) (int64, error) {
	if settings.Get(ctx, "seo_index_site") == "false" {
		return 0, nil
	}
	if !typeIndexable(types, sitemapTypeName[kind]) {
		return 0, nil
	}
	var n int64
	if err := kindScope(db, kind).WithContext(ctx).Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// partCount returns how many sitemap files a kind needs.
func partCount(n int64) int {
	return int((n + MaxURLsPerSitemap - 1) / MaxURLsPerSitemap)
}

// SitemapIndex renders /sitemap.xml listing the part files. Each kind is
// split into parts of at most MaxURLsPerSitemap URLs.
func SitemapIndex(ctx context.Context, siteURL string) ([]byte, error) {
	if err := readyDB(); err != nil {
		return nil, err
	}
	db := data.DB.WithContext(ctx)
	types := indexTypes(ctx)
	doc := sitemapIndexDoc{Xmlns: sitemapNamespace}
	for _, kind := range sitemapKinds {
		n, err := kindCount(ctx, db, types, kind)
		if err != nil {
			return nil, err
		}
		for part := 1; part <= partCount(n); part++ {
			doc.Sitemaps = append(doc.Sitemaps, sitemapRef{Loc: absolute(siteURL, fmt.Sprintf("/sitemap-%s-%d.xml", kind, part))})
		}
	}
	return marshalXML(doc)
}

// SitemapPart renders one part file, such as kind "posts" and number 1. A
// part that does not exist answers ErrNotFound.
func SitemapPart(ctx context.Context, siteURL, kind string, n int) ([]byte, error) {
	if err := readyDB(); err != nil {
		return nil, err
	}
	if kindScope(data.DB, kind) == nil {
		return nil, ErrNotFound
	}
	db := data.DB.WithContext(ctx)
	types := indexTypes(ctx)
	total, err := kindCount(ctx, db, types, kind)
	if err != nil {
		return nil, err
	}
	if n < 1 || n > partCount(total) {
		return nil, ErrNotFound
	}
	offset := (n - 1) * MaxURLsPerSitemap
	urls := urlsFrom(ctx)
	doc := sitemapSet{Xmlns: sitemapNamespace}
	switch kind {
	case KindPosts, KindPages:
		typ := "post"
		if kind == KindPages {
			typ = "page"
		}
		var rows []core.Post
		err = publicPosts(db, typ).Select("id, slug, type, title, published_at, created_at, updated_at").
			Order("id ASC").Offset(offset).Limit(MaxURLsPerSitemap).Find(&rows).Error
		for _, row := range rows {
			path := urls.Post(row)
			if typ == "page" {
				path = urls.Page(row)
			}
			doc.URLs = append(doc.URLs, sitemapURL{Loc: absolute(siteURL, path), LastMod: lastMod(row.UpdatedAt)})
		}
	case KindCategories:
		var rows []core.Category
		err = db.Model(&core.Category{}).Select("id, slug, updated_at").
			Order("id ASC").Offset(offset).Limit(MaxURLsPerSitemap).Find(&rows).Error
		for _, row := range rows {
			doc.URLs = append(doc.URLs, sitemapURL{Loc: absolute(siteURL, urls.Category(row)), LastMod: lastMod(row.UpdatedAt)})
		}
	case KindProducts:
		var rows []core.Product
		err = publicProducts(db).Select("id, slug, updated_at").
			Order("id ASC").Offset(offset).Limit(MaxURLsPerSitemap).Find(&rows).Error
		for _, row := range rows {
			doc.URLs = append(doc.URLs, sitemapURL{Loc: absolute(siteURL, urls.Product(row)), LastMod: lastMod(row.UpdatedAt)})
		}
	}
	if err != nil {
		return nil, err
	}
	return marshalXML(doc)
}

// lastMod formats a timestamp for the sitemap, or returns "" for a zero time.
func lastMod(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// marshalXML encodes a document with the XML declaration.
func marshalXML(v any) ([]byte, error) {
	body, err := xml.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), body...), nil
}
