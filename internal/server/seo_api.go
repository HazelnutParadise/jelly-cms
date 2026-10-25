package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/seo"
	"github.com/HazelnutParadise/jelly-cms/internal/syslog"
	"github.com/labstack/echo/v4"
)

// crawlerStatRow is one bot and action with its request count.
type crawlerStatRow struct {
	Bot    string `json:"bot" gorm:"column:bot"`
	Action string `json:"action" gorm:"column:action"`
	Count  int64  `json:"count" gorm:"column:count"`
}

// seoOverrideRow is one post or product that overrides seo_index or seo_ai.
type seoOverrideRow struct {
	Kind     string `json:"kind" gorm:"column:kind"`
	ID       uint   `json:"id" gorm:"column:id"`
	Title    string `json:"title" gorm:"column:title"`
	Slug     string `json:"slug" gorm:"column:slug"`
	SEOIndex string `json:"seo_index" gorm:"column:seo_index"`
	SEOAI    string `json:"seo_ai" gorm:"column:seo_ai"`
}

// handleRobotsPreview answers the robots.txt text the current policy produces.
func handleRobotsPreview(c echo.Context) error {
	ctx := c.Request().Context()
	body, err := seo.Robots(seo.WithURLs(ctx, seoURLs(ctx)), accountBaseURL(c))
	if err != nil {
		return seoError(err)
	}
	return c.JSON(http.StatusOK, map[string]string{"robots": string(body)})
}

// handleCrawlerStats counts catalog crawler requests over the last days (1 to
// 90, default 7), grouped by bot and action. The counts come from the crawler
// rows that crawlerGuard writes to system_logs.
func handleCrawlerStats(c echo.Context) error {
	if data.DB == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	days := 7
	if raw := c.QueryParam("days"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 90 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "days must be between 1 and 90"})
		}
		days = n
	}
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	rows := []crawlerStatRow{}
	err := data.DB.WithContext(c.Request().Context()).Raw(`
		SELECT fields->>'bot' AS bot, fields->>'action' AS action, COUNT(*) AS count
		FROM system_logs
		WHERE source = ? AND at >= ? AND fields->>'bot' IS NOT NULL
		GROUP BY fields->>'bot', fields->>'action'
		ORDER BY count DESC, bot ASC`, syslog.SourceCrawler, since).Scan(&rows).Error
	if err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	return c.JSON(http.StatusOK, map[string]any{"days": days, "since": since, "items": rows})
}

// handleSEOOverrides lists the posts and products whose seo_index or seo_ai is
// not inherit.
func handleSEOOverrides(c echo.Context) error {
	if data.DB == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	rows := []seoOverrideRow{}
	err := data.DB.WithContext(c.Request().Context()).Raw(`
		SELECT 'post' AS kind, id, title, slug, seo_index, seo_ai FROM posts
		WHERE deleted_at IS NULL AND (seo_index <> 'inherit' OR seo_ai <> 'inherit')
		UNION ALL
		SELECT 'product' AS kind, id, title, slug, seo_index, seo_ai FROM products
		WHERE deleted_by_id IS NULL AND (seo_index <> 'inherit' OR seo_ai <> 'inherit')
		ORDER BY kind, id`).Scan(&rows).Error
	if err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
	}
	return c.JSON(http.StatusOK, map[string]any{"items": rows})
}
