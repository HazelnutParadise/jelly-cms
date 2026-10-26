package server

// Public read-only JSON API used by the WebMCP tools (ticket 13).
//
// GET /api/public/posts?page=&per_page=  list public posts
// GET /api/public/content/:slug          read one public post or page
// GET /api/public/site                   read public site information

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	datapkg "github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/syslog"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

type publicAPIErrorBody struct {
	Code          string `json:"code"`
	Message       string `json:"message"`
	CorrelationID string `json:"correlation_id"`
}

func publicAPIError(c echo.Context, status int, code, message string) error {
	requestID, _ := c.Get(syslog.RequestIDContextKey).(string)
	if requestID == "" {
		requestID = c.Response().Header().Get(syslog.RequestIDHeader)
	}
	return c.JSON(status, publicAPIErrorBody{Code: code, Message: message, CorrelationID: requestID})
}

func registerPublicAPIRoutes(e *echo.Echo) {
	e.GET("/api/public", publicAPIOverview)
	e.GET("/api/public/openapi.json", publicAPIOpenAPI)
	e.GET("/api/public/posts", listPublicAPIPosts)
	e.GET("/api/public/content/:slug", getPublicAPIContent)
	e.GET("/api/public/site", getPublicAPISite)
}

func listPublicAPIPosts(c echo.Context) error {
	db := datapkg.DB
	if db == nil {
		return publicAPIError(c, http.StatusServiceUnavailable, "service_unavailable", "The service is temporarily unavailable.")
	}
	db = db.WithContext(c.Request().Context())
	page := publicAPIPage(c.QueryParam("page"))
	perPage := publicAPIPerPage(c.QueryParam("per_page"))

	// Pin one cutoff for the count and row query so a post scheduled during this
	// request cannot make the two values describe different public collections.
	cutoff := time.Now()
	scope := func() *gorm.DB {
		return publicPostVisibility(db).
			Where("posts.type = ?", "post").
			Where(archivePostDate+" <= ?", cutoff)
	}
	var total int64
	if err := scope().Count(&total).Error; err != nil {
		log.Printf("public API count posts request_id=%s: %v", publicAPIRequestID(c), err)
		return publicAPIError(c, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
	}

	items := make([]map[string]any, 0, perPage)
	if page-1 <= int(^uint(0)>>1)/perPage {
		cfg := loadPermalinkConfig(c.Request().Context())
		var posts []core.Post
		query := scope().Order("posts.pinned DESC, " + archivePostDate + " DESC, posts.id DESC").
			Limit(perPage).Offset((page - 1) * perPage)
		if cfg.post.needsCategory {
			query = query.Preload("Categories", func(tx *gorm.DB) *gorm.DB {
				return tx.Where("categories.hide_from_list = ?", false).Order("categories.position ASC, categories.name ASC")
			})
		}
		if err := query.Find(&posts).Error; err != nil {
			log.Printf("public API list posts request_id=%s: %v", publicAPIRequestID(c), err)
			return publicAPIError(c, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
		}
		for _, post := range posts {
			items = append(items, map[string]any{
				"title":        post.Title,
				"excerpt":      publicAPIExcerpt(post.Excerpt, post.Content),
				"url":          permalinkPathFor(db, cfg, post),
				"published_at": effectivePublishedAt(post).Format(time.RFC3339),
			})
		}
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items, "page": page, "per_page": perPage, "total": total})
}

func getPublicAPIContent(c echo.Context) error {
	db := datapkg.DB
	if db == nil {
		return publicAPIError(c, http.StatusServiceUnavailable, "service_unavailable", "The service is temporarily unavailable.")
	}
	db = db.WithContext(c.Request().Context())
	var post core.Post
	err := publicPostVisibility(db).
		Where("posts.type IN ? AND posts.slug = ?", []string{"post", "page"}, c.Param("slug")).
		First(&post).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return publicAPIError(c, http.StatusNotFound, "not_found", "No published content has this slug.")
	}
	if err != nil {
		log.Printf("public API content request_id=%s: %v", publicAPIRequestID(c), err)
		return publicAPIError(c, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
	}
	content, err := publicPostContent(c.Request().Context(), db, post)
	if err != nil {
		log.Printf("public API render content request_id=%s: %v", publicAPIRequestID(c), err)
		return publicAPIError(c, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
	}
	post.Content = content
	cfg := loadPermalinkConfig(c.Request().Context())
	return c.JSON(http.StatusOK, map[string]any{
		"type":         post.Type,
		"title":        post.Title,
		"url":          permalinkPathFor(db, cfg, post),
		"published_at": effectivePublishedAt(post).Format(time.RFC3339),
		"excerpt":      publicAPIExcerpt(post.Excerpt, content),
		"text":         theme.PlainTextParagraphs(content),
	})
}

func getPublicAPISite(c echo.Context) error {
	db := datapkg.DB
	if db == nil {
		return publicAPIError(c, http.StatusServiceUnavailable, "service_unavailable", "The service is temporarily unavailable.")
	}
	db = db.WithContext(c.Request().Context())
	values := map[string]string{}
	var options []core.Option
	if err := db.Where("key IN ?", []string{"site_title", "site_description", "site_language"}).Find(&options).Error; err != nil {
		log.Printf("public API site request_id=%s: %v", publicAPIRequestID(c), err)
		return publicAPIError(c, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
	}
	for _, option := range options {
		values[option.Key] = option.Value
	}
	name := values["site_title"]
	if name == "" {
		name = "Jelly CMS"
	}
	language := values["site_language"]
	if language == "" {
		language = "zh-TW"
	}
	return c.JSON(http.StatusOK, map[string]any{
		"name":        name,
		"description": values["site_description"],
		"language":    language,
		"url":         requestBaseURL(c) + "/",
	})
}

func publicAPIPage(raw string) int {
	page, err := strconv.Atoi(raw)
	if err != nil {
		if publicAPIPositiveInteger(raw) {
			return int(^uint(0) >> 1)
		}
		return 1
	}
	if page < 1 {
		return 1
	}
	return page
}

func publicAPIPerPage(raw string) int {
	perPage, err := strconv.Atoi(raw)
	if err != nil {
		if publicAPIPositiveInteger(raw) {
			return 50
		}
		return 10
	}
	if perPage < 1 {
		return 10
	}
	if perPage > 50 {
		return 50
	}
	return perPage
}

func publicAPIPositiveInteger(raw string) bool {
	if strings.HasPrefix(raw, "+") {
		raw = raw[1:]
	}
	if raw == "" {
		return false
	}
	positive := false
	for _, digit := range raw {
		if digit < '0' || digit > '9' {
			return false
		}
		if digit != '0' {
			positive = true
		}
	}
	return positive
}

func effectivePublishedAt(post core.Post) time.Time {
	if post.PublishedAt != nil {
		return *post.PublishedAt
	}
	return post.CreatedAt
}

func publicAPIExcerpt(manual, content string) string {
	if excerpt := strings.TrimSpace(plainText(manual)); excerpt != "" {
		return excerpt
	}
	text := theme.PlainTextParagraphs(content)
	runes := []rune(text)
	if len(runes) > 200 {
		return string(runes[:200]) + "…"
	}
	return text
}

func publicAPIRequestID(c echo.Context) string {
	requestID, _ := c.Get(syslog.RequestIDContextKey).(string)
	if requestID == "" {
		return c.Response().Header().Get(syslog.RequestIDHeader)
	}
	return requestID
}

func requestBaseURL(c echo.Context) string {
	scheme := c.Request().URL.Scheme
	if scheme == "" {
		if c.Request().TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	return scheme + "://" + c.Request().Host
}
