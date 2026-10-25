package server

import (
	"net/http"
	"strconv"

	"github.com/HazelnutParadise/jelly-cms/internal/seo"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

// registerFeedRoutes mounts the public site feed. The feed follows feed_mode
// (full, summary or off), feed_items, excerpt_auto_length and the permalink
// pattern. Category and author feeds are not implemented yet.
func registerFeedRoutes(e *echo.Echo) {
	e.GET("/feed.xml", handleSiteFeed)
}

// handleSiteFeed answers GET /feed.xml. feed_mode off answers 404.
func handleSiteFeed(c echo.Context) error {
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
	body, err := seo.Feed(seo.WithFeedOptions(ctx, opts), siteURL, "")
	if err != nil {
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
