package seo

import "strings"

// CatalogVersion identifies the bundled AI crawler catalog. The catalog is
// data, not code, so later versions can be fetched without a release.
const CatalogVersion = "2026-10-01"

// MaxURLsPerSitemap is the limit per sitemap file.
const MaxURLsPerSitemap = 50000

// Bot is one AI crawler in the catalog.
type Bot struct {
	Name string
	// Operator is the company that runs the crawler.
	Operator string
	// Purpose is train (collects for model training), search (answers search
	// queries with citations) or user (fetches on a user's request).
	Purpose string
	// IgnoresRobots marks crawlers that do not honour robots.txt. Only these
	// are refused with 403 when ai_block_server_side is on.
	IgnoresRobots bool
}

// Catalog returns the bundled AI crawler list in a stable order.
//
// IgnoresRobots on Bytespider is a catalog entry based on public reports that
// the crawler does not follow robots.txt. It was not checked against this
// site's own logs, so the admin can judge it from the crawler statistics.
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
		{Name: "Bytespider", Operator: "ByteDance", Purpose: "train", IgnoresRobots: true},
		{Name: "PerplexityBot", Operator: "Perplexity", Purpose: "search"},
		{Name: "Perplexity-User", Operator: "Perplexity", Purpose: "user"},
	}
}

// MatchBot returns the catalog crawler named in a User-Agent header. The name
// match ignores case and the first catalog entry that matches wins.
func MatchBot(userAgent string) (Bot, bool) {
	lower := strings.ToLower(userAgent)
	if lower == "" {
		return Bot{}, false
	}
	for _, bot := range Catalog() {
		if strings.Contains(lower, strings.ToLower(bot.Name)) {
			return bot, true
		}
	}
	return Bot{}, false
}
