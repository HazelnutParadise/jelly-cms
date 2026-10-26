package server

// Public URL settings (ticket 08b 「閱讀與網址」).
//
// Post URLs follow the permalink_post pattern, which is read on every request,
// so a change applies at once. Routing is fixed in Echo, so permalinks are
// handled before routing: permalinkRouting rewrites a URL that matches the
// current pattern to the internal /post/:slug route, answers an old pattern
// from permalink_legacy with a 301, and maps the category, tag, author and
// archive prefixes onto the internal /c, /t, /author and /archive routes.
// Old prefixes answer 301 to the configured ones.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// permalinkCanonicalKey is the echo context key set when a request was rewritten
// to /post/:slug because it used the current permalink pattern. The post route
// answers 404 without it, so the old /post/ form stops working after a change.
const permalinkCanonicalKey = "permalink.canonical"

// Option keys read by the URL settings.
const (
	keyPermalinkPost     = "permalink_post"
	keyPermalinkLegacy   = "permalink_legacy"
	keySlugStrategy      = "slug_strategy"
	keySiteTimezone      = "site_timezone"
	keyHomeShow          = "home_show"
	keyHomePageID        = "home_page_id"
	keyPerPageHome       = "per_page_home"
	keyPerPageTaxonomy   = "per_page_taxonomy"
	keyPerPageAuthor     = "per_page_author"
	keyPerPageSearch     = "per_page_search"
	keyExcerptAutoLength = "excerpt_auto_length"
	keyFeedMode          = "feed_mode"
	keyFeedItems         = "feed_items"
	keySiteTitle         = "site_title"
	keySiteDescription   = "site_description"
)

// Prefix kinds. route is the internal path the router serves.
type permalinkPrefix struct {
	key   string
	route string
}

var permalinkPrefixes = []permalinkPrefix{
	{key: "permalink_category_prefix", route: "/c"},
	{key: "permalink_tag_prefix", route: "/t"},
	{key: "permalink_author_prefix", route: "/author"},
	{key: "permalink_archive_prefix", route: "/archive"},
}

// permalinkNoCategory stands in for {category} on a post without a visible category.
const permalinkNoCategory = "uncategorized"

// permalinkTokenGroups maps each pattern token to the regular expression that
// matches its text in a URL.
var permalinkTokenGroups = map[string]string{
	"slug":     `([^/]+)`,
	"category": `([^/]+)`,
	"year":     `([0-9]{4})`,
	"month":    `([0-9]{2})`,
	"day":      `([0-9]{2})`,
	"id":       `([0-9]+)`,
}

var (
	permalinkTokenRefRE = regexp.MustCompile(`\{([a-z]+)\}`)
	permalinkPrefixRE   = regexp.MustCompile(`^/[\p{L}\p{N}_-]+(/[\p{L}\p{N}_-]+)*$`)
)

// permalinkPattern is a compiled permalink pattern such as /{year}/{month}/{slug}.
type permalinkPattern struct {
	raw           string
	re            *regexp.Regexp
	tokens        []string // token name of each capture group, in order
	needsCategory bool
}

// parsePermalinkPattern checks a pattern and compiles it. A pattern must start
// with /, contain {slug} or {id}, use only the known tokens once each, and must
// not start with a reserved name (except the default segment post).
func parsePermalinkPattern(raw string) (*permalinkPattern, error) {
	if raw == "" || raw[0] != '/' || len(raw) > 200 {
		return nil, errors.New("permalink must start with / and be at most 200 characters")
	}
	if strings.Contains(raw, "//") || (len(raw) > 1 && strings.HasSuffix(raw, "/")) {
		return nil, errors.New("permalink must not contain // or end with /")
	}
	var expr strings.Builder
	expr.WriteString("^")
	var tokens []string
	seen := map[string]bool{}
	for i := 0; i < len(raw); {
		switch raw[i] {
		case '{':
			end := strings.IndexByte(raw[i:], '}')
			if end < 0 {
				return nil, errors.New("permalink has an unclosed {")
			}
			name := raw[i+1 : i+end]
			group, ok := permalinkTokenGroups[name]
			if !ok {
				return nil, fmt.Errorf("permalink token {%s} is not supported", name)
			}
			if seen[name] {
				return nil, fmt.Errorf("permalink token {%s} appears more than once", name)
			}
			seen[name] = true
			tokens = append(tokens, name)
			expr.WriteString(group)
			i += end + 1
		case '}':
			return nil, errors.New("permalink has an unopened }")
		default:
			r, size := utf8.DecodeRuneInString(raw[i:])
			if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' || r == '/') {
				return nil, fmt.Errorf("permalink character %q is not allowed", r)
			}
			expr.WriteString(regexp.QuoteMeta(string(r)))
			i += size
		}
	}
	if !seen["slug"] && !seen["id"] {
		return nil, errors.New("permalink needs {slug} or {id}")
	}
	if first := strings.SplitN(raw[1:], "/", 2)[0]; !strings.Contains(first, "{") && first != "post" && core.IsReservedSlug(first) {
		return nil, fmt.Errorf("permalink cannot start with the reserved name %q", first)
	}
	expr.WriteString("$")
	re, err := regexp.Compile(expr.String())
	if err != nil {
		return nil, fmt.Errorf("permalink compile: %w", err)
	}
	return &permalinkPattern{raw: raw, re: re, tokens: tokens, needsCategory: seen["category"]}, nil
}

// match returns the token values of path when it matches the pattern.
func (p *permalinkPattern) match(path string) (map[string]string, bool) {
	groups := p.re.FindStringSubmatch(path)
	if groups == nil {
		return nil, false
	}
	values := make(map[string]string, len(p.tokens))
	for i, name := range p.tokens {
		values[name] = groups[i+1]
	}
	return values, true
}

// build returns the URL path of post under this pattern.
func (p *permalinkPattern) build(post permalinkPost, loc *time.Location) string {
	local := post.Date.In(loc)
	values := map[string]string{
		"slug":     post.Slug,
		"category": post.Category,
		"year":     fmt.Sprintf("%04d", local.Year()),
		"month":    fmt.Sprintf("%02d", int(local.Month())),
		"day":      fmt.Sprintf("%02d", local.Day()),
		"id":       strconv.FormatUint(uint64(post.ID), 10),
	}
	return permalinkTokenRefRE.ReplaceAllStringFunc(p.raw, func(ref string) string {
		return values[ref[1:len(ref)-1]]
	})
}

// permalinkPost is the data a pattern needs from a post.
type permalinkPost struct {
	ID       uint
	Slug     string
	Date     time.Time
	Category string
}

// postDate is the date the archives sort by: the published date, else the creation date.
func postDate(post core.Post) time.Time {
	if post.PublishedAt != nil {
		return *post.PublishedAt
	}
	return post.CreatedAt
}

// primaryCategorySlug picks the category the URL names: the first visible
// category in position order.
func primaryCategorySlug(categories []core.Category) string {
	visible := make([]core.Category, 0, len(categories))
	for _, c := range categories {
		if !c.HideFromList {
			visible = append(visible, c)
		}
	}
	if len(visible) == 0 {
		return permalinkNoCategory
	}
	sort.SliceStable(visible, func(i, j int) bool {
		if visible[i].Position != visible[j].Position {
			return visible[i].Position < visible[j].Position
		}
		return visible[i].Name < visible[j].Name
	})
	return visible[0].Slug
}

// permalinkConfig is the URL configuration in effect for one request.
type permalinkConfig struct {
	post      *permalinkPattern
	legacy    []*permalinkPattern
	prefixes  map[string]string // internal route → configured prefix
	loc       *time.Location
	slugStrat string
}

// loadPermalinkConfig reads the URL options in one query. A value that does
// not parse falls back to the registered default and is logged.
func loadPermalinkConfig(ctx context.Context) permalinkConfig {
	keys := []string{keyPermalinkPost, keyPermalinkLegacy, keySiteTimezone, keySlugStrategy}
	for _, p := range permalinkPrefixes {
		keys = append(keys, p.key)
	}
	values := loadOptionValues(ctx, keys...)

	cfg := permalinkConfig{prefixes: map[string]string{}, slugStrat: valueOr(values, keySlugStrategy)}
	post, err := parsePermalinkPattern(valueOr(values, keyPermalinkPost))
	if err != nil {
		log.Printf("permalink_post: %v; using the default", err)
		post, _ = parsePermalinkPattern(defaultOption(keyPermalinkPost))
	}
	cfg.post = post

	var legacy []string
	if raw := valueOr(values, keyPermalinkLegacy); raw != "" {
		if err := json.Unmarshal([]byte(raw), &legacy); err != nil {
			log.Printf("permalink_legacy: %v; ignored", err)
			legacy = nil
		}
	}
	for _, raw := range legacy {
		pattern, err := parsePermalinkPattern(raw)
		if err != nil {
			log.Printf("permalink_legacy %q: %v; ignored", raw, err)
			continue
		}
		cfg.legacy = append(cfg.legacy, pattern)
	}

	cfg.loc = time.UTC
	if name := valueOr(values, keySiteTimezone); name != "" {
		if loc, err := time.LoadLocation(name); err == nil {
			cfg.loc = loc
		} else {
			log.Printf("site_timezone %q: %v; using UTC", name, err)
		}
	}
	for _, p := range permalinkPrefixes {
		cfg.prefixes[p.route] = valueOr(values, p.key)
	}
	return cfg
}

// currentPrefixNames lists the configured prefixes without their slash. The
// page slug check uses them as reserved names.
func (cfg permalinkConfig) currentPrefixNames() []string {
	names := make([]string, 0, len(cfg.prefixes))
	for _, prefix := range cfg.prefixes {
		names = append(names, strings.SplitN(strings.TrimPrefix(prefix, "/"), "/", 2)[0])
	}
	return names
}

// loadOptionValues returns the stored values of keys, falling back to the
// registered default for keys without a row.
func loadOptionValues(ctx context.Context, keys ...string) map[string]string {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		values[key] = defaultOption(key)
	}
	if data.DB == nil {
		return values
	}
	var rows []core.Option
	if err := data.DB.WithContext(ctx).Where("key IN ?", keys).Find(&rows).Error; err != nil {
		log.Printf("load url settings: %v", err)
		return values
	}
	for _, row := range rows {
		values[row.Key] = row.Value
	}
	return values
}

// valueOr returns values[key], or the registered default when the stored value is empty.
func valueOr(values map[string]string, key string) string {
	if value := values[key]; value != "" {
		return value
	}
	return defaultOption(key)
}

// defaultOption is the registered default of key.
func defaultOption(key string) string {
	spec, _ := settings.Lookup(key)
	return spec.Default
}

// optionInt reads one integer option. A missing or invalid value gives the default.
func optionInt(ctx context.Context, key string) int {
	n, err := strconv.Atoi(valueOr(loadOptionValues(ctx, key), key))
	if err != nil {
		n, _ = strconv.Atoi(defaultOption(key))
	}
	return n
}

// perPage returns the page size of a listing, between 1 and 100.
func perPage(ctx context.Context, key string) int {
	n := optionInt(ctx, key)
	if n < 1 {
		return 1
	}
	if n > 100 {
		return 100
	}
	return n
}

// permalinkPostOf builds the pattern data of a post whose categories are loaded.
func permalinkPostOf(post core.Post) permalinkPost {
	return permalinkPost{ID: post.ID, Slug: post.Slug, Date: postDate(post), Category: primaryCategorySlug(post.Categories)}
}

// loadPermalinkPost returns the public post with slug (or id when byID), with its
// visible categories loaded.
func loadPermalinkPost(db *gorm.DB, slug string, id uint, byID bool) (core.Post, bool, error) {
	q := publicPostVisibility(db).Where("posts.type = ?", "post")
	if byID {
		q = q.Where("posts.id = ?", id)
	} else {
		q = q.Where("posts.slug = ?", slug)
	}
	var post core.Post
	err := q.Preload("Categories", func(tx *gorm.DB) *gorm.DB {
		return tx.Where("categories.hide_from_list = ?", false).Order("categories.position ASC, categories.name ASC")
	}).First(&post).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return core.Post{}, false, nil
	}
	if err != nil {
		return core.Post{}, false, err
	}
	return post, true, nil
}

// resolvePermalinkPost finds the post named by the captured tokens, by slug when
// the pattern has one and by id otherwise.
func resolvePermalinkPost(db *gorm.DB, tokens map[string]string) (core.Post, bool, error) {
	if slug, ok := tokens["slug"]; ok {
		return loadPermalinkPost(db, slug, 0, false)
	}
	id, err := strconv.ParseUint(tokens["id"], 10, 64)
	if err != nil {
		return core.Post{}, false, nil
	}
	return loadPermalinkPost(db, "", uint(id), true)
}

// dbContext returns the context a query was started with, or Background.
func dbContext(db *gorm.DB) context.Context {
	if db.Statement != nil && db.Statement.Context != nil {
		return db.Statement.Context
	}
	return context.Background()
}

// permalinkPathFor returns the public path of post under the current pattern.
// Pages live at /{slug}. Posts load their categories when the pattern needs one.
func permalinkPathFor(db *gorm.DB, cfg permalinkConfig, post core.Post) string {
	if post.Type == "page" {
		return "/" + post.Slug
	}
	if cfg.post.needsCategory && len(post.Categories) == 0 {
		var categories []core.Category
		if err := db.Model(&core.Post{ID: post.ID}).Association("Categories").Find(&categories); err != nil {
			log.Printf("permalink categories for post %d: %v", post.ID, err)
		}
		post.Categories = categories
	}
	return cfg.post.build(permalinkPostOf(post), cfg.loc)
}

// permalinkAppOwned reports whether the first path segment belongs to the
// application or to a public system file. Those paths skip permalink handling.
func permalinkAppOwned(path string) bool {
	segment := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)[0]
	switch segment {
	case "admin", "api", "auth", "login", "install", "uploads", "themes", "healthz",
		"shop", "cart", "checkout", "orders", "account", "robots.txt", "llms.txt", "feed.xml", ".well-known":
		return true
	}
	return strings.HasPrefix(segment, "sitemap") && strings.HasSuffix(segment, ".xml")
}

// trimPathPrefix returns the rest of path after prefix when prefix names a whole
// leading segment run of path.
func trimPathPrefix(path, prefix string) (string, bool) {
	if path == prefix {
		return "", true
	}
	if strings.HasPrefix(path, prefix+"/") {
		return path[len(prefix):], true
	}
	return "", false
}

// permalinkRouting runs before routing and handles the configurable URLs.
func permalinkRouting(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		req := c.Request()
		if data.DB == nil || (req.Method != http.MethodGet && req.Method != http.MethodHead) {
			return next(c)
		}
		path := req.URL.Path
		if permalinkAppOwned(path) {
			return next(c)
		}
		ctx := req.Context()
		cfg := loadPermalinkConfig(ctx)
		db := data.DB.WithContext(ctx)

		// 1. A URL in the current post pattern serves the post at its canonical path.
		if tokens, ok := cfg.post.match(path); ok {
			post, found, err := resolvePermalinkPost(db, tokens)
			switch {
			case err != nil:
				log.Printf("permalink lookup %q: %v", path, err)
			case found && cfg.post.build(permalinkPostOf(post), cfg.loc) == path:
				req.URL.Path, req.URL.RawPath = "/post/"+post.Slug, ""
				c.Set(permalinkCanonicalKey, true)
				return next(c)
			}
		}

		// 2. A pattern listed in permalink_legacy answers 301 to the current URL.
		for _, legacy := range cfg.legacy {
			tokens, ok := legacy.match(path)
			if !ok {
				continue
			}
			post, found, err := resolvePermalinkPost(db, tokens)
			if err != nil {
				log.Printf("permalink legacy lookup %q: %v", path, err)
				continue
			}
			if found {
				target := permalinkPathFor(db, cfg, post)
				if raw := req.URL.RawQuery; raw != "" {
					target += "?" + raw
				}
				return c.Redirect(http.StatusMovedPermanently, target)
			}
		}

		// 3. Prefixes: the configured prefix serves the listing, the old one redirects.
		for _, p := range permalinkPrefixes {
			configured := cfg.prefixes[p.route]
			if configured == p.route {
				continue
			}
			if rest, ok := trimPathPrefix(path, configured); ok {
				req.URL.Path, req.URL.RawPath = p.route+rest, ""
				return next(c)
			}
			if rest, ok := trimPathPrefix(path, p.route); ok {
				target := configured + rest
				if raw := req.URL.RawQuery; raw != "" {
					target += "?" + raw
				}
				return c.Redirect(http.StatusMovedPermanently, target)
			}
		}
		return next(c)
	}
}

// reservedPageSlug reports whether a page may not use slug under the current prefixes.
func reservedPageSlug(ctx context.Context, slug string) bool {
	return core.IsReservedSlug(slug, loadPermalinkConfig(ctx).currentPrefixNames()...)
}

// nextTimestampSlug returns the first free numeric slug, counting up from now
// one millisecond at a time.
func nextTimestampSlug(db *gorm.DB, now time.Time) (string, error) {
	for i := 0; i < 1000; i++ {
		candidate := core.TimestampSlug(now.Add(time.Duration(i) * time.Millisecond))
		var taken int64
		if err := db.Model(&core.Post{}).Where("slug = ?", candidate).Count(&taken).Error; err != nil {
			return "", err
		}
		if taken == 0 {
			return candidate, nil
		}
	}
	return "", errors.New("no free timestamp slug")
}

// urlSettingKeys are the options this file validates.
var urlSettingKeys = func() []string {
	keys := []string{
		keyPermalinkPost, keyPermalinkLegacy, keySlugStrategy, keyHomeShow, keyHomePageID,
		keyPerPageHome, keyPerPageTaxonomy, keyPerPageAuthor, keyPerPageSearch,
		keyExcerptAutoLength, keyFeedMode, keyFeedItems,
	}
	for _, p := range permalinkPrefixes {
		keys = append(keys, p.key)
	}
	return keys
}()

func isURLSettingKey(key string) bool {
	for _, k := range urlSettingKeys {
		if k == key {
			return true
		}
	}
	return false
}

// settingsURLGuard rejects invalid URL settings in POST /api/admin/settings before
// the handler stores anything, so a bad value never reaches the database.
func settingsURLGuard(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		req := c.Request()
		if req.Method != http.MethodPost && req.Method != http.MethodPut || c.Path() != "/api/admin/settings" {
			return next(c)
		}
		raw, err := io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(raw))
		if err != nil {
			return next(c)
		}
		var kv map[string]string
		if json.Unmarshal(raw, &kv) != nil {
			return next(c)
		}
		if err := checkURLSettings(req.Context(), kv); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return next(c)
	}
}

// checkURLSettings validates the URL options in one write. The prefixes are
// checked together with the stored values of the others.
func checkURLSettings(ctx context.Context, incoming map[string]string) error {
	touched := false
	for key, value := range incoming {
		if !isURLSettingKey(key) {
			continue
		}
		touched = true
		if err := checkURLSettingValue(key, value); err != nil {
			return err
		}
	}
	if !touched {
		return nil
	}
	merged := loadOptionValues(ctx, urlSettingKeys...)
	for key, value := range incoming {
		if isURLSettingKey(key) {
			merged[key] = value // an empty value means the default, see valueOr
		}
	}
	seen := map[string]string{}
	for _, p := range permalinkPrefixes {
		value := valueOr(merged, p.key)
		if err := checkPermalinkPrefix(p, value); err != nil {
			return err
		}
		lower := strings.ToLower(value)
		if other, dup := seen[lower]; dup {
			return fmt.Errorf("%s and %s must differ", other, p.key)
		}
		seen[lower] = p.key
	}
	return nil
}

// checkURLSettingValue checks one option by key. An empty value means the default.
func checkURLSettingValue(key, value string) error {
	if value == "" {
		return nil
	}
	switch key {
	case keyPermalinkPost:
		_, err := parsePermalinkPattern(value)
		if err != nil {
			return fmt.Errorf("%s: %v", key, err)
		}
		return nil
	case keyPermalinkLegacy:
		var list []string
		if err := json.Unmarshal([]byte(value), &list); err != nil {
			return fmt.Errorf("%s must be a JSON array of strings", key)
		}
		if len(list) > 5 {
			return fmt.Errorf("%s allows at most 5 patterns", key)
		}
		for _, raw := range list {
			if _, err := parsePermalinkPattern(raw); err != nil {
				return fmt.Errorf("%s pattern %q: %v", key, raw, err)
			}
		}
		return nil
	case keySlugStrategy:
		return oneOf(key, value, core.SlugStrategyTitle, core.SlugStrategyTimestamp)
	case keyHomeShow:
		return oneOf(key, value, "latest", "page")
	case keyFeedMode:
		return oneOf(key, value, "full", "summary", "off")
	case keyHomePageID:
		return intInRange(key, value, 0, 1<<31-1)
	case keyPerPageHome, keyPerPageTaxonomy, keyPerPageAuthor, keyPerPageSearch, keyFeedItems:
		return intInRange(key, value, 1, 100)
	case keyExcerptAutoLength:
		return intInRange(key, value, 1, 1000)
	}
	return nil
}

// checkPermalinkPrefix requires /segment form, and a first segment that is not
// reserved unless it is the prefix's own internal name.
func checkPermalinkPrefix(p permalinkPrefix, value string) error {
	if len(value) > 64 || !permalinkPrefixRE.MatchString(value) {
		return fmt.Errorf("%s must look like /name", p.key)
	}
	first := strings.SplitN(value[1:], "/", 2)[0]
	if !strings.EqualFold("/"+first, p.route) && core.IsReservedSlug(first) {
		return fmt.Errorf("%s cannot use the reserved name %q", p.key, first)
	}
	return nil
}

func oneOf(key, value string, allowed ...string) error {
	for _, a := range allowed {
		if value == a {
			return nil
		}
	}
	return fmt.Errorf("%s must be one of %s", key, strings.Join(allowed, ", "))
}

func intInRange(key, value string, min, max int) error {
	n, err := strconv.Atoi(value)
	if err != nil || n < min || n > max {
		return fmt.Errorf("%s must be a whole number from %d to %d", key, min, max)
	}
	return nil
}

// newContentSlug returns the slug of a post saved without one. Posts follow
// slug_strategy; pages always take the title.
func newContentSlug(db *gorm.DB, cfg permalinkConfig, post core.Post) (string, error) {
	if post.Type != "page" && cfg.slugStrat == core.SlugStrategyTimestamp {
		return nextTimestampSlug(db, time.Now())
	}
	return core.NewPostService(db).GenerateSlug(post.Title), nil
}
