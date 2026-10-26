package core

import (
	"strconv"
	"strings"
	"time"
)

// Slug strategies (ticket 08b 「別名策略」). The strategy applies when a post is
// saved without a slug. Pages always take their slug from the title.
const (
	// SlugStrategyTitle derives the slug from the title (the default).
	SlugStrategyTitle = "title"
	// SlugStrategyTimestamp uses a numeric slug, the Unix time in milliseconds.
	SlugStrategyTimestamp = "timestamp"
)

// reservedSlugs are first path segments owned by the application, its public
// endpoints or the default content prefixes. A page may not use them as slug.
var reservedSlugs = map[string]bool{
	"admin": true, "api": true, "auth": true, "login": true, "install": true,
	"uploads": true, "themes": true, "robots.txt": true, "llms.txt": true,
	"feed.xml": true, "healthz": true, "shop": true, "cart": true,
	"checkout": true, "orders": true, "account": true,
	// Default content prefixes and the fixed public listing paths.
	"posts": true, "post": true, "search": true, "c": true, "t": true,
	"author": true, "archive": true,
}

// IsReservedSlug reports whether slug is reserved, ignoring case. sitemap
// files (sitemap*.xml) are always reserved. extra lists the content prefixes
// that are in effect right now, without their leading slash.
func IsReservedSlug(slug string, extra ...string) bool {
	s := strings.ToLower(strings.TrimSpace(slug))
	if reservedSlugs[s] {
		return true
	}
	if strings.HasPrefix(s, "sitemap") && strings.HasSuffix(s, ".xml") {
		return true
	}
	for _, e := range extra {
		if s != "" && strings.EqualFold(s, e) {
			return true
		}
	}
	return false
}

// TimestampSlug returns the numeric slug for the timestamp strategy.
func TimestampSlug(at time.Time) string {
	return strconv.FormatInt(at.UnixMilli(), 10)
}
