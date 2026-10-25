package server

// Crawler-facing public documents and their wiring (ticket 08b 「搜尋引擎與 AI 爬蟲」).
//
// robots.txt, sitemaps, llms.txt and the TDMRep file are rendered by package
// seo from the options and the permalink settings. The IndexNow key file is
// served before routing, so a key that changes never collides with a page slug.
// Public pages get the robots meta tag, X-Robots-Tag and TDM-Reservation through
// applyPublicSEO. A crawler named in the AI catalog is logged to system_logs
// (source crawler), and refused with 403 when it ignores robots.txt and
// ai_block_server_side is on.

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/seo"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
	"github.com/HazelnutParadise/jelly-cms/internal/syslog"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// Option keys of the crawler policy read by the public pages.
const (
	keySEOIndexSite   = "seo_index_site"
	keySEOIndexTypes  = "seo_index_types"
	keyTDMReservation = "tdm_reservation"
	keyTDMPolicyURL   = "tdm_policy_url"
)

// publicSEOKeys are the options a public page reads on each render.
var publicSEOKeys = []string{
	keySEOIndexSite, keySEOIndexTypes, keyTDMReservation, keyTDMPolicyURL,
	keyInjectHeadAll, keyInjectHeadContent, keyInjectFooter, keyInjectOnCommerce,
}

// sitemapPartRE matches the part file names /sitemap-<kind>-<n>.xml.
var sitemapPartRE = regexp.MustCompile(`^(posts|pages|categories|products)-([1-9][0-9]*)\.xml$`)

// indexNowPathRE matches a candidate IndexNow key file name.
var indexNowPathRE = regexp.MustCompile(`^/([A-Za-z0-9-]{8,128})\.txt$`)

// registerSEORoutes mounts the crawler documents and the SEO admin API. It is
// called from registerSystemRoutes, before the /:slug catch-all.
func registerSEORoutes(e *echo.Echo) {
	e.GET("/robots.txt", handleRobots)
	e.GET("/sitemap.xml", handleSitemapIndex)
	e.GET("/sitemap-*", handleSitemapPart)
	e.GET("/llms.txt", handleLLMSTxt)
	e.GET("/.well-known/tdmrep.json", handleTDMRep)

	adminOnly := auth.RequireRole("admin")
	e.GET("/api/admin/seo/robots-preview", handleRobotsPreview, adminOnly)
	e.GET("/api/admin/seo/crawler-stats", handleCrawlerStats, adminOnly)
	e.GET("/api/admin/seo/overrides", handleSEOOverrides, adminOnly)
}

// seoURLs returns the path builders the sitemap and llms.txt use. Posts follow
// the permalink pattern, and categories follow the category prefix.
func seoURLs(ctx context.Context) seo.URLs {
	cfg := loadPermalinkConfig(ctx)
	var db *gorm.DB
	if data.DB != nil {
		db = data.DB.WithContext(ctx)
	}
	categoryPrefix := cfg.prefixes["/c"]
	return seo.URLs{
		Post: func(post core.Post) string {
			if db == nil {
				return "/post/" + post.Slug
			}
			return permalinkPathFor(db, cfg, post)
		},
		Category: func(category core.Category) string {
			return categoryPrefix + "/" + category.Slug
		},
	}
}

// seoError maps the seo package errors to HTTP errors.
func seoError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, seo.ErrNotFound):
		return echo.ErrNotFound
	case errors.Is(err, seo.ErrDatabase):
		return echo.NewHTTPError(http.StatusServiceUnavailable, "Service unavailable")
	}
	return err
}

func handleRobots(c echo.Context) error {
	ctx := c.Request().Context()
	body, err := seo.Robots(seo.WithURLs(ctx, seoURLs(ctx)), accountBaseURL(c))
	if err != nil {
		return seoError(err)
	}
	return c.Blob(http.StatusOK, "text/plain; charset=utf-8", body)
}

func handleSitemapIndex(c echo.Context) error {
	ctx := c.Request().Context()
	body, err := seo.SitemapIndex(seo.WithURLs(ctx, seoURLs(ctx)), accountBaseURL(c))
	if err != nil {
		return seoError(err)
	}
	return c.Blob(http.StatusOK, "application/xml; charset=utf-8", body)
}

func handleSitemapPart(c echo.Context) error {
	m := sitemapPartRE.FindStringSubmatch(c.Param("*"))
	if m == nil {
		return echo.ErrNotFound
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return echo.ErrNotFound
	}
	ctx := c.Request().Context()
	body, err := seo.SitemapPart(seo.WithURLs(ctx, seoURLs(ctx)), accountBaseURL(c), m[1], n)
	if err != nil {
		return seoError(err)
	}
	return c.Blob(http.StatusOK, "application/xml; charset=utf-8", body)
}

func handleLLMSTxt(c echo.Context) error {
	ctx := c.Request().Context()
	body, err := seo.LLMSTxt(seo.WithURLs(ctx, seoURLs(ctx)), accountBaseURL(c))
	if err != nil {
		return seoError(err)
	}
	return c.Blob(http.StatusOK, "text/plain; charset=utf-8", body)
}

func handleTDMRep(c echo.Context) error {
	body, err := seo.TDMRep(c.Request().Context(), accountBaseURL(c))
	if err != nil {
		return seoError(err)
	}
	return c.Blob(http.StatusOK, echo.MIMEApplicationJSONCharsetUTF8, body)
}

// crawlerGuard logs each request from a catalog crawler and refuses the ones
// that ignore robots.txt while ai_block_server_side is on. robots.txt itself
// stays open so a refused crawler can still read the policy.
func crawlerGuard(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		req := c.Request()
		bot, ok := seo.MatchBot(req.UserAgent())
		if !ok || req.URL.Path == "/robots.txt" {
			return next(c)
		}
		ctx := req.Context()
		action := "allowed"
		if bot.IgnoresRobots && settings.Get(ctx, "ai_block_server_side") == "true" {
			action = "blocked"
		}
		syslog.Emit(ctx, syslog.Entry{
			Level:   syslog.LevelInfo,
			Source:  syslog.SourceCrawler,
			Message: "crawler request",
			Fields:  map[string]any{"bot": bot.Name, "purpose": bot.Purpose, "action": action},
		})
		if action == "blocked" {
			return c.String(http.StatusForbidden, "Forbidden")
		}
		return next(c)
	}
}

// indexNowKeyRouting answers GET /<key>.txt with the key while IndexNow is on.
// It runs before routing, so the key is not mistaken for a page slug.
func indexNowKeyRouting(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		req := c.Request()
		m := indexNowPathRE.FindStringSubmatch(req.URL.Path)
		if m == nil || data.DB == nil || (req.Method != http.MethodGet && req.Method != http.MethodHead) {
			return next(c)
		}
		ctx := req.Context()
		if settings.Get(ctx, "seo_indexnow_enabled") != "true" {
			return next(c)
		}
		key := settings.Get(ctx, "seo_indexnow_key")
		if key == "" || subtle.ConstantTimeCompare([]byte(m[1]), []byte(key)) != 1 {
			return next(c)
		}
		return c.String(http.StatusOK, key)
	}
}

// publicInjection holds the three raw HTML slots a public page outputs. The
// theme prints them as {{.Inject.Head}}, {{.Inject.ContentHead}} and {{.Inject.Footer}}.
type publicInjection struct {
	Head        template.HTML
	ContentHead template.HTML
	Footer      template.HTML
}

// publicInjectionFor picks the slots for one page. Commerce paths output
// nothing unless inject_on_commerce is on. head_content is output on content
// pages only.
func publicInjectionFor(path string, opts map[string]string, contentPage bool) publicInjection {
	if isCommercePath(path) && opts[keyInjectOnCommerce] != "true" {
		return publicInjection{}
	}
	out := publicInjection{
		Head:   template.HTML(opts[keyInjectHeadAll]),
		Footer: template.HTML(opts[keyInjectFooter]),
	}
	if contentPage {
		out.ContentHead = template.HTML(opts[keyInjectHeadContent])
	}
	return out
}

// isCommercePath reports whether path is under /checkout or /account.
func isCommercePath(path string) bool {
	return strings.HasPrefix(path, "/checkout") || strings.HasPrefix(path, "/account")
}

// publicOptionValues reads keys in one query. A key without a row takes its
// registered default, the same as settings.Get.
func publicOptionValues(db *gorm.DB, keys ...string) map[string]string {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		values[key] = defaultOption(key)
	}
	if db == nil {
		return values
	}
	var rows []core.Option
	if err := db.Where("key IN ?", keys).Find(&rows).Error; err != nil {
		log.Printf("load public options: %v", err)
		return values
	}
	for _, row := range rows {
		values[row.Key] = row.Value
	}
	return values
}

// applyPublicSEO adds the injection slots, the robots meta value and the
// crawler headers to a public page's template data before it renders. file
// is the theme template name.
func applyPublicSEO(c echo.Context, db *gorm.DB, file string, values map[string]interface{}) {
	opts := publicOptionValues(db, publicSEOKeys...)
	home, _ := values["IsHome"].(bool)
	contentPage := file == "post.html" || (file == "page.html" && !home)
	values["Inject"] = publicInjectionFor(c.Request().URL.Path, opts, contentPage)

	if publicNoindex(db, opts, publicIndexKind(file, home), values) {
		values["RobotsMeta"] = "noindex"
		c.Response().Header().Set("X-Robots-Tag", "noindex")
	}
	if opts[keyTDMReservation] == "true" {
		c.Response().Header().Set("TDM-Reservation", "1")
	}
}

// publicIndexKind names the seo_index_types key that governs a template.
func publicIndexKind(file string, home bool) string {
	switch file {
	case "post.html":
		return "post"
	case "page.html":
		if home {
			return "home"
		}
		return "page"
	case "index.html":
		return "home"
	case "search.html":
		return "search"
	case "posts.html":
		return "archive"
	}
	return ""
}

// publicNoindex reports whether a page must carry noindex. The site switch
// and the type switch apply to every page, and a post or page with
// seo_index noindex is held back on its own.
func publicNoindex(db *gorm.DB, opts map[string]string, kind string, values map[string]interface{}) bool {
	if opts[keySEOIndexSite] == "false" {
		return true
	}
	types := map[string]bool{}
	if raw := opts[keySEOIndexTypes]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &types); err != nil {
			types = map[string]bool{}
		}
	}
	if allowed, set := types[kind]; kind != "" && set && !allowed {
		return true
	}
	post, ok := values["Post"].(core.Post)
	if !ok {
		post, ok = values["Page"].(core.Post)
	}
	if !ok || post.ID == 0 || db == nil {
		return false
	}
	var index string
	if err := db.Raw("SELECT seo_index FROM posts WHERE id = ?", post.ID).Scan(&index).Error; err != nil {
		log.Printf("read seo_index of post %d: %v", post.ID, err)
		return false
	}
	return index == "noindex"
}
