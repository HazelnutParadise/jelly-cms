package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/seo"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

// registerFeedRoutes mounts the public site feed. The feed follows feed_mode
// (full, summary or off), feed_items, excerpt_auto_length and the permalink
// pattern. Scoped feeds use configured category and author prefixes.
func registerFeedRoutes(e *echo.Echo) {
	e.GET("/feed.xml", handleSiteFeed)
	// Register the defaults explicitly so the feed routes win over archive
	// routes that share their prefixes. The parameter route below handles
	// custom prefixes.
	e.GET("/c/:slug/feed.xml", handleCategoryFeed)
	e.GET("/author/:slug/feed.xml", handleAuthorFeed)
	e.GET("/:prefix/:slug/feed.xml", handleScopedFeed)
}

// handleSiteFeed answers GET /feed.xml. feed_mode off answers 404.
func handleSiteFeed(c echo.Context) error {
	return serveFeed(c, "")
}

func handleCategoryFeed(c echo.Context) error {
	return serveFeed(c, "category:"+c.Param("slug"))
}

func handleAuthorFeed(c echo.Context) error {
	return serveFeed(c, "author:"+c.Param("slug"))
}

func handleScopedFeed(c echo.Context) error {
	cfg := loadPermalinkConfig(c.Request().Context())
	prefix := strings.Trim(c.Param("prefix"), "/")
	slug := c.Param("slug")
	if prefix == strings.Trim(cfg.prefixes["/c"], "/") {
		return serveFeed(c, "category:"+slug)
	}
	if prefix == strings.Trim(cfg.prefixes["/author"], "/") {
		return serveFeed(c, "author:"+slug)
	}
	return echo.ErrNotFound
}

func serveFeed(c echo.Context, scope string) error {
	ctx := c.Request().Context()
	values := loadOptionValues(ctx,
		keyFeedMode, keyFeedItems, keyExcerptAutoLength, keySiteTitle, keySiteDescription)
	mode := valueOr(values, keyFeedMode)
	if mode == "off" {
		return echo.ErrNotFound
	}
	if mode != "full" {
		mode = "summary"
	}
	items := optionIntFrom(values, keyFeedItems)
	excerpt := optionIntFrom(values, keyExcerptAutoLength)
	cfg := loadPermalinkConfig(ctx)
	siteURL := accountBaseURL(c)

	opts := seo.FeedOptions{
		Mode:            mode,
		Items:           items,
		Excerpt:         excerpt,
		SiteTitle:       getSiteTitleFrom(values),
		SiteDescription: values[keySiteDescription],
		PostURL: func(p seo.FeedPost) string {
			return siteURL + cfg.post.build(permalinkPost{ID: p.ID, Slug: p.Slug, Date: p.Date,
				Category: primaryCategorySlugNames(p.CategorySlugs)}, cfg.loc)
		},
		Plain: theme.PlainText,
	}
	body, err := seo.Feed(seo.WithFeedOptions(ctx, opts), siteURL, scope)
	if err != nil {
		if errors.Is(err, seo.ErrFeedNotFound) {
			return echo.ErrNotFound
		}
		return err
	}
	return c.Blob(http.StatusOK, "application/rss+xml; charset=utf-8", body)
}

// optionIntFrom reads an integer option from loaded values, falling back to the default.
func optionIntFrom(values map[string]string, key string) int {
	n, err := strconv.Atoi(valueOr(values, key))
	if err != nil {
		n, _ = strconv.Atoi(defaultOption(key))
	}
	return n
}

// getSiteTitleFrom returns the site title, or "Jelly CMS" when it is empty.
func getSiteTitleFrom(values map[string]string) string {
	if title := values[keySiteTitle]; title != "" {
		return title
	}
	return defaultOption(keySiteTitle)
}

// primaryCategorySlugNames returns the first category slug, or the no-category stand-in.
func primaryCategorySlugNames(slugs []string) string {
	if len(slugs) == 0 {
		return permalinkNoCategory
	}
	return slugs[0]
}
