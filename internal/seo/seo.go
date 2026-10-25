// Package seo builds the crawler-facing documents: robots.txt, sitemaps,
// feeds, llms.txt and the TDMRep declaration, plus the AI crawler catalog.
//
// The server owns permalink patterns, so the path of each public item comes
// from the URLs passed through the context (WithURLs). Every rendered URL is
// the site base plus that path.
package seo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
)

// ErrNotImplemented marks behaviour that the package does not provide.
var ErrNotImplemented = errors.New("seo: not implemented")

// ErrNotFound means the requested document is disabled or does not exist,
// for example a sitemap part past the last one or llms.txt while it is off.
var ErrNotFound = errors.New("seo: not found")

// ErrDatabase means the document needs the database, which is not ready yet.
var ErrDatabase = errors.New("seo: database is not ready")

// Option keys read by this package. They are registered in internal/settings.
const (
	keySiteTitle       = "site_title"
	keySiteDescription = "site_description"
	keySEOIndexSite    = "seo_index_site"
	keySEOIndexTypes   = "seo_index_types"
	keyAIPolicyPreset  = "ai_policy_preset"
	keyAIBots          = "ai_bots"
	keyAICustomRules   = "ai_custom_rules"
	keyTDMReservation  = "tdm_reservation"
	keyTDMPolicyURL    = "tdm_policy_url"
	keyLLMSEnabled     = "llms_txt_enabled"
	keyLLMSIntro       = "llms_txt_intro"
)

// URLs gives the public path of each item kind. A nil function takes the
// default path noted on each field.
type URLs struct {
	// Post returns the path of a post. Default: /post/<slug>.
	Post func(post core.Post) string
	// Page returns the path of a page. Default: /<slug>.
	Page func(post core.Post) string
	// Category returns the path of a category archive. Default: /c/<slug>.
	Category func(category core.Category) string
	// Product returns the path of a product. Default: /shop/p/<slug>.
	Product func(product core.Product) string
}

type urlsKey struct{}

// WithURLs returns a context that carries the URL builders.
func WithURLs(ctx context.Context, urls URLs) context.Context {
	return context.WithValue(ctx, urlsKey{}, urls)
}

// urlsFrom returns the URL builders from ctx with defaults filled in.
func urlsFrom(ctx context.Context) URLs {
	urls, _ := ctx.Value(urlsKey{}).(URLs)
	if urls.Post == nil {
		urls.Post = func(p core.Post) string { return "/post/" + p.Slug }
	}
	if urls.Page == nil {
		urls.Page = func(p core.Post) string { return "/" + p.Slug }
	}
	if urls.Category == nil {
		urls.Category = func(c core.Category) string { return "/c/" + c.Slug }
	}
	if urls.Product == nil {
		urls.Product = func(p core.Product) string { return "/shop/p/" + p.Slug }
	}
	return urls
}

// absolute joins the site base and a path. The base has no trailing slash.
func absolute(siteURL, path string) string {
	return strings.TrimRight(siteURL, "/") + path
}

// option reads one registered option through the settings package.
func option(ctx context.Context, key string) string {
	return settings.Get(ctx, key)
}

// readyDB reports ErrDatabase when the database is not ready.
func readyDB() error {
	if data.DB == nil {
		return ErrDatabase
	}
	return nil
}

// typeIndexable reports whether seo_index_types allows the named type. A type
// that is missing from the map is indexable; only an explicit false blocks it.
func typeIndexable(types map[string]bool, name string) bool {
	allowed, ok := types[name]
	return !ok || allowed
}

// indexTypes parses seo_index_types. A value that is not a JSON object is ignored.
func indexTypes(ctx context.Context) map[string]bool {
	types := map[string]bool{}
	if raw := option(ctx, keySEOIndexTypes); raw != "" {
		if err := json.Unmarshal([]byte(raw), &types); err != nil {
			types = map[string]bool{}
		}
	}
	return types
}
