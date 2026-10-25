// Package seo builds the crawler-facing documents: robots.txt, sitemaps,
// feeds, llms.txt and the TDMRep declaration, plus the AI crawler catalog.
//
// The rendering functions are stubs until ticket 08b; their signatures are
// the contract the public routes in server/system_routes.go rely on.
package seo

import (
	"context"
	"errors"
)

// CatalogVersion identifies the bundled AI crawler catalog. The catalog is
// data, not code, so later versions can be fetched without a release.
const CatalogVersion = "2026-10-01"

// MaxURLsPerSitemap is the limit per sitemap file.
const MaxURLsPerSitemap = 50000

// ErrNotImplemented marks behaviour that the skeleton does not provide yet.
var ErrNotImplemented = errors.New("seo: not implemented")

// Bot is one AI crawler in the catalog.
type Bot struct {
	Name string
	// Operator is the company that runs the crawler.
	Operator string
	// Purpose is train (collects for model training), search (answers search
	// queries with citations) or user (fetches on a user's request).
	Purpose string
	// IgnoresRobots marks crawlers that do not honour robots.txt. It is set from
	// observed crawler statistics, never asserted by the catalog itself.
	IgnoresRobots bool
}

// Catalog returns the bundled AI crawler list in a stable order.
func Catalog() []Bot {
	return []Bot{
		{Name: "GPTBot", Operator: "OpenAI", Purpose: "train"},
		{Name: "OAI-SearchBot", Operator: "OpenAI", Purpose: "search"},
		{Name: "ChatGPT-User", Operator: "OpenAI", Purpose: "user"},
		{Name: "ClaudeBot", Operator: "Anthropic", Purpose: "train"},
		{Name: "Claude-SearchBot", Operator: "Anthropic", Purpose: "search"},
		{Name: "Claude-User", Operator: "Anthropic", Purpose: "user"},
		{Name: "Google-Extended", Operator: "Google", Purpose: "train"},
		{Name: "Applebot-Extended", Operator: "Apple", Purpose: "train"},
		{Name: "CCBot", Operator: "Common Crawl", Purpose: "train"},
		{Name: "Bytespider", Operator: "ByteDance", Purpose: "train"},
		{Name: "PerplexityBot", Operator: "Perplexity", Purpose: "search"},
		{Name: "Perplexity-User", Operator: "Perplexity", Purpose: "user"},
	}
}

// Robots renders /robots.txt for the current policy.
func Robots(ctx context.Context, siteURL string) ([]byte, error) {
	return nil, ErrNotImplemented
}

// SitemapIndex renders /sitemap.xml listing the part files.
func SitemapIndex(ctx context.Context, siteURL string) ([]byte, error) {
	return nil, ErrNotImplemented
}

// SitemapPart renders one part file, such as kind "posts" and number 1.
func SitemapPart(ctx context.Context, siteURL, kind string, n int) ([]byte, error) {
	return nil, ErrNotImplemented
}

// LLMSTxt renders /llms.txt.
func LLMSTxt(ctx context.Context, siteURL string) ([]byte, error) {
	return nil, ErrNotImplemented
}

// TDMRep renders /.well-known/tdmrep.json.
func TDMRep(ctx context.Context, siteURL string) ([]byte, error) {
	return nil, ErrNotImplemented
}
