package server

import (
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"

	datapkg "github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// Public theme pages (ticket 05). Every public render goes through tm.RenderWithDB and carries
// SiteTitle, Title and CurrentPath. Public scope is archivePublicPosts (handlers_archives.go).
//
//	GET /                 index.html   data: IsHome bool, Posts []core.Post (6, Author/Categories preloaded),
//	                                   Featured *core.Post (pinned first, else newest, never repeated in Posts),
//	                                   Stats publicSiteStats{PostCount, AuthorCount int64; SinceYear int},
//	                                   Intro string (option site_description)
//	GET /posts            posts.html   data: Posts, Page, TotalPages, Total int64, PageSize int (10),
//	                                   Categories []core.Category (visible top level, PostCount, max 6)
//	GET /post/:slug       post.html    data: Post (Author/Categories/Tags preloaded, content with heading ids),
//	                                   ReadingMinutes int, TOC []theme.TOCEntry{ID, Text, Level},
//	                                   Prev, Next *core.Post, AuthorCard authorCard{Name, Bio, AvatarURL, Username; PostCount int64},
//	                                   Comments, CommentCount, CommentsOpen
//	GET /:slug            page.html    data: Page core.Post, IsPage bool, Content template.HTML
//	                                   (only type=page, published, public, not scheduled, not trashed)
//	GET /search?q=&page=  search.html  data: Query string, Results []searchResult{Post core.Post; Title, Snippet template.HTML},
//	                                   Total int64, Page, TotalPages (see search.go)
//	unknown path         error.html   data: Error ErrorPage (existing; rendered through RenderWithDB)
//	maintenance          maintenance.html  data: Message, StartedAt time.Time, ElapsedMinutes, RetryAfter, SiteTitle (see maintenance.go)
//
// Stubs below are contract placeholders; the live handlers in RegisterPublicRoutes are unchanged.

func getSiteTitle() string {
	var option core.Option
	if err := datapkg.DB.Where("key = ?", "site_title").First(&option).Error; err == nil {
		return option.Value
	}
	return "Jelly CMS"
}

func RegisterPublicRoutes(e *echo.Echo, tm *theme.Manager) {
	// Home page - show latest posts
	e.GET("/", func(c echo.Context) error {
		var posts []core.Post
		datapkg.DB.Where("type = ? AND status = ?", "post", "published").
			Preload("Author").
			Order("created_at DESC").
			Limit(10).
			Find(&posts)

		data := map[string]interface{}{
			"SiteTitle": getSiteTitle(),
			"Title":     "首頁",
			"Posts":     posts,
			"IsHome":    true,
		}

		html, err := tm.RenderWithDB("index.html", data, datapkg.DB)
		if err != nil {
			return c.String(http.StatusInternalServerError, err.Error())
		}
		return c.HTMLBlob(http.StatusOK, html)
	})

	// Posts listing page
	e.GET("/posts", func(c echo.Context) error {
		page, _ := strconv.Atoi(c.QueryParam("page"))
		if page < 1 {
			page = 1
		}
		pageSize := 10
		offset := (page - 1) * pageSize

		var posts []core.Post
		var total int64
		datapkg.DB.Where("type = ? AND status = ?", "post", "published").
			Preload("Author").
			Order("created_at DESC").
			Limit(pageSize).
			Offset(offset).
			Find(&posts)
		datapkg.DB.Model(&core.Post{}).Where("type = ? AND status = ?", "post", "published").Count(&total)

		totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))

		data := map[string]interface{}{
			"SiteTitle":  getSiteTitle(),
			"Title":      "文章總覽",
			"Posts":      posts,
			"Page":       page,
			"TotalPages": totalPages,
			"IsPosts":    true,
		}

		html, err := tm.RenderWithDB("posts.html", data, datapkg.DB)
		if err != nil {
			// Fallback to index if posts.html doesn't exist
			html, err = tm.RenderWithDB("index.html", data, datapkg.DB)
			if err != nil {
				return c.String(http.StatusInternalServerError, err.Error())
			}
		}
		return c.HTMLBlob(http.StatusOK, html)
	})

	// Single post page
	e.GET("/post/:slug", func(c echo.Context) error {
		slug := c.Param("slug")
		var post core.Post
		if err := datapkg.DB.Where("slug = ? AND type = ? AND status = ?", slug, "post", "published").
			Preload("Author").
			First(&post).Error; err != nil {
			return echo.ErrNotFound
		}

		comments, commentCount, commentsOpen := commentsForPost(post, commentViewerSignedIn(c))
		data := map[string]interface{}{
			"SiteTitle":    getSiteTitle(),
			"Post":         post,
			"Title":        post.Title,
			"IsPost":       true,
			"Comments":     comments,
			"CommentCount": commentCount,
			"CommentsOpen": commentsOpen,
		}

		html, err := tm.RenderWithDB("post.html", data, datapkg.DB)
		if err != nil {
			// Fallback to index if post.html doesn't exist
			data["Content"] = template.HTML(post.Content)
			html, err = tm.RenderWithDB("index.html", data, datapkg.DB)
			if err != nil {
				return c.String(http.StatusInternalServerError, err.Error())
			}
		}
		return c.HTMLBlob(http.StatusOK, html)
	})

	// About page
	e.GET("/about", func(c echo.Context) error {
		var page core.Post
		if err := datapkg.DB.Where("slug = ? AND type = ? AND status = ?", "about", "page", "published").First(&page).Error; err != nil {
			// Create default about page if not exists
			page = core.Post{
				Title:   "關於我們",
				Slug:    "about",
				Content: "<p>歡迎來到 Jelly CMS！這是一個現代化的內容管理系統。</p>",
				Type:    "page",
				Status:  "published",
			}
		}

		data := map[string]interface{}{
			"SiteTitle": getSiteTitle(),
			"Title":     page.Title,
			"Content":   template.HTML(page.Content),
			"IsPage":    true,
		}

		// Try to render page.html first
		html, err := tm.RenderWithDB("page.html", data, datapkg.DB)
		if err != nil {
			// If page.html doesn't exist or fails, try index.html
			html, err = tm.RenderWithDB("index.html", data, datapkg.DB)
			if err != nil {
				// If both fail, return error with details
				return c.String(http.StatusInternalServerError, "Template render error: "+err.Error())
			}
		}
		return c.HTMLBlob(http.StatusOK, html)
	})

	// Search page
	e.GET("/search", func(c echo.Context) error {
		query := c.QueryParam("q")
		var posts []core.Post

		if query != "" {
			searchQuery := "%" + strings.ToLower(query) + "%"
			data.DB.Where("type = ? AND status = ? AND (LOWER(title) LIKE ? OR LOWER(content) LIKE ?)", "post", "published", searchQuery, searchQuery).
				Preload("Author").
				Order("created_at DESC").
				Find(&posts)
		}

		data := map[string]interface{}{
			"SiteTitle": getSiteTitle(),
			"Title":     "搜尋結果",
			"Query":     query,
			"Posts":     posts,
			"IsSearch":  true,
		}

		html, err := tm.RenderWithDB("search.html", data, datapkg.DB)
		if err != nil {
			html, err = tm.RenderWithDB("index.html", data, datapkg.DB)
			if err != nil {
				return c.String(http.StatusInternalServerError, err.Error())
			}
		}
		return c.HTMLBlob(http.StatusOK, html)
	})

	// Search API
	e.GET("/api/search", func(c echo.Context) error {
		query := c.QueryParam("q")
		if query == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Query parameter 'q' is required"})
		}

		var posts []core.Post
		searchQuery := "%" + strings.ToLower(query) + "%"
		data.DB.Where("type = ? AND status = ? AND (LOWER(title) LIKE ? OR LOWER(content) LIKE ?)", "post", "published", searchQuery, searchQuery).
			Order("created_at DESC").
			Limit(20).
			Find(&posts)

		// Return simplified post data
		type PostSummary struct {
			ID        uint      `json:"id"`
			Title     string    `json:"title"`
			Slug      string    `json:"slug"`
			Excerpt   string    `json:"excerpt"`
			CreatedAt time.Time `json:"created_at"`
		}

		results := make([]PostSummary, len(posts))
		for i, post := range posts {
			excerpt := post.Content
			if len(excerpt) > 200 {
				excerpt = excerpt[:200] + "..."
			}
			results[i] = PostSummary{
				ID:        post.ID,
				Title:     post.Title,
				Slug:      post.Slug,
				Excerpt:   excerpt,
				CreatedAt: post.CreatedAt,
			}
		}

		return c.JSON(http.StatusOK, map[string]interface{}{
			"query": query,
			"posts": results,
			"count": len(results),
		})
	})

	// Generic page route (must be last to avoid conflicts)
	e.GET("/:slug", func(c echo.Context) error {
		slug := c.Param("slug")

		// Skip known routes
		if slug == "posts" || slug == "search" || slug == "about" || slug == "api" || slug == "admin" || slug == "auth" || slug == "login" || slug == "install" {
			return echo.ErrNotFound
		}

		var post core.Post
		if err := datapkg.DB.Where("slug = ? AND status = ?", slug, "published").First(&post).Error; err != nil {
			return echo.ErrNotFound
		}

		data := map[string]interface{}{
			"SiteTitle": getSiteTitle(),
			"Title":     post.Title,
			"Content":   template.HTML(post.Content),
			"IsPage":    true,
		}

		html, err := tm.RenderWithDB("page.html", data, datapkg.DB)
		if err != nil {
			html, err = tm.RenderWithDB("index.html", data, datapkg.DB)
			if err != nil {
				return c.String(http.StatusInternalServerError, err.Error())
			}
		}
		return c.HTMLBlob(http.StatusOK, html)
	})
}

// publicSiteStats holds the home page statistics. The contract's name for this type is publicStats; the
// function publicStats below keeps that name, so the struct is named publicSiteStats (Go forbids both).
type publicSiteStats struct {
	PostCount   int64
	AuthorCount int64
	SinceYear   int
}

// authorCard is the author box on a post page.
type authorCard struct {
	Name      string
	Bio       string
	AvatarURL string
	Username  string
	PostCount int64
}

// publicStats counts public posts and authors and finds the site's opening year (year of the earliest
// public post, else the current year). Not implemented yet: returns zero values.
func publicStats(db *gorm.DB) (publicSiteStats, error) {
	return publicSiteStats{}, nil
}

// publicPostBySlug loads one post by slug within the public scope. It returns gorm.ErrRecordNotFound when
// the post is not public (draft, private, scheduled, trashed, or not a post). Not implemented yet.
func publicPostBySlug(db *gorm.DB, slug string) (core.Post, error) {
	return core.Post{}, gorm.ErrRecordNotFound
}

// prevNextPosts returns the public posts adjacent to post in the list order (previous = newer neighbour,
// next = older). Either pointer is nil at the ends. Not implemented yet.
func prevNextPosts(db *gorm.DB, post core.Post) (prev, next *core.Post, err error) {
	return nil, nil, nil
}

// authorCardFor builds the author box for user, with the author's public post count. The display name
// falls back to the username and an empty bio is kept empty. Not implemented yet: returns zero values.
func authorCardFor(db *gorm.DB, user core.User) authorCard {
	return authorCard{}
}
