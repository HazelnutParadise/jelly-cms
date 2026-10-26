package server

import (
	"context"
	"errors"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	datapkg "github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/markup"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

const publicPostsPageSize = 10

// publicSiteStats holds the home page statistics.
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

func getSiteTitle() string {
	if datapkg.DB == nil {
		return "Jelly CMS"
	}
	var option core.Option
	if err := datapkg.DB.Where("key = ?", "site_title").First(&option).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("load site title: %v", err)
		}
		return "Jelly CMS"
	}
	if option.Value == "" {
		return "Jelly CMS"
	}
	return option.Value
}

func publicSiteTitle(db *gorm.DB) (string, error) {
	if db == nil {
		return "Jelly CMS", nil
	}
	var option core.Option
	err := db.Where("key = ?", "site_title").First(&option).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "Jelly CMS", nil
	}
	if err != nil {
		return "", err
	}
	if option.Value == "" {
		return "Jelly CMS", nil
	}
	return option.Value, nil
}

func publicOption(db *gorm.DB, key string) (string, error) {
	var option core.Option
	err := db.Where("key = ?", key).First(&option).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return option.Value, nil
}

func publicRender(c echo.Context, tm *theme.Manager, db *gorm.DB, file, title string, values map[string]interface{}) error {
	siteTitle, err := publicSiteTitle(db)
	if err != nil {
		return err
	}
	values["SiteTitle"] = siteTitle
	values["Title"] = title
	values["CurrentPath"] = c.Request().URL.Path
	applyPublicSEO(c, db, file, values)

	body, err := tm.RenderWithDB(file, values, db)
	if err != nil {
		return err
	}
	return c.HTMLBlob(http.StatusOK, body)
}

func publicPostPreloads(db *gorm.DB) *gorm.DB {
	return db.Preload("Author").
		Preload("Categories", func(tx *gorm.DB) *gorm.DB {
			return tx.Where("categories.hide_from_list = ?", false).Order("categories.position ASC, categories.name ASC")
		}).
		Preload("Tags", func(tx *gorm.DB) *gorm.DB {
			return tx.Order("tags.name ASC")
		})
}

func RegisterPublicRoutes(e *echo.Echo, tm *theme.Manager) {
	e.GET("/", func(c echo.Context) error {
		db := datapkg.DB
		if db == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "Service unavailable")
		}
		ctx := c.Request().Context()
		db = db.WithContext(ctx)

		// home_show=page answers with the chosen page; the latest posts are the default.
		landing, ok, err := homeLandingPage(ctx, db)
		if err != nil {
			return err
		}
		if ok {
			landing.Content, err = publicPostContent(ctx, db, landing)
			if err != nil {
				return err
			}
			return publicRender(c, tm, db, "page.html", landing.Title, map[string]interface{}{
				"IsHome":  true,
				"IsPage":  true,
				"Page":    landing,
				"Content": template.HTML(landing.Content),
			})
		}

		stats, err := publicStats(db)
		if err != nil {
			return err
		}
		intro, err := publicOption(db, "site_description")
		if err != nil {
			return err
		}

		// per_page_home counts every post on the home page, the featured one included.
		size := perPage(ctx, keyPerPageHome)
		posts := make([]core.Post, 0, size)
		var latest []core.Post
		err = publicPostPreloads(publicPostVisibility(db).Where("posts.type = ?", "post")).
			Order("posts.pinned DESC, " + archivePostDate + " DESC, posts.id DESC").
			Limit(size).Find(&latest).Error
		if err != nil {
			return err
		}
		var featured *core.Post
		if len(latest) > 0 {
			featured = &latest[0]
			posts = append(posts, latest[1:]...)
		}

		return publicRender(c, tm, db, "index.html", "首頁", map[string]interface{}{
			"IsHome":   true,
			"Posts":    posts,
			"Featured": featured,
			"Stats":    stats,
			"Intro":    intro,
		})
	})

	e.GET("/posts", func(c echo.Context) error {
		db := datapkg.DB
		if db == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "Service unavailable")
		}
		db = db.WithContext(c.Request().Context())
		page := publicPageParam(c)
		size := perPage(c.Request().Context(), keyPerPageHome)

		var total int64
		if err := publicPostVisibility(db).Where("posts.type = ?", "post").Count(&total).Error; err != nil {
			return err
		}
		totalPages := publicTotalPages(total, size)
		if page > 1 && page > totalPages {
			return echo.ErrNotFound
		}
		if page-1 > int(^uint(0)>>1)/size {
			return echo.ErrNotFound
		}

		posts := make([]core.Post, 0, size)
		err := publicPostPreloads(publicPostVisibility(db).Where("posts.type = ?", "post")).
			Order("posts.pinned DESC, " + archivePostDate + " DESC, posts.id DESC").
			Limit(size).Offset((page - 1) * size).
			Find(&posts).Error
		if err != nil {
			return err
		}

		var categories []core.Category
		if err := db.Order("position ASC, name ASC, id ASC").Find(&categories).Error; err != nil {
			return err
		}
		counts, err := archiveCategoryCounts(categories)
		if err != nil {
			return err
		}
		visibleCategories := make([]core.Category, 0, 6)
		for _, category := range categories {
			if category.ParentID != nil || category.HideFromList {
				continue
			}
			category.PostCount = counts[category.ID]
			visibleCategories = append(visibleCategories, category)
			if len(visibleCategories) == 6 {
				break
			}
		}

		return publicRender(c, tm, db, "posts.html", "文章總覽", map[string]interface{}{
			"IsPosts":    true,
			"Posts":      posts,
			"Page":       page,
			"TotalPages": totalPages,
			"Total":      total,
			"PageSize":   size,
			"Categories": visibleCategories,
		})
	})

	e.GET("/post/:slug", func(c echo.Context) error {
		// Only a URL that matched the current permalink pattern reaches this
		// route as a post; a bare /post/ URL is not served once the pattern changed.
		if canonical, _ := c.Get(permalinkCanonicalKey).(bool); !canonical {
			return echo.ErrNotFound
		}
		db := datapkg.DB
		if db == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "Service unavailable")
		}
		ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
		defer cancel()
		db = db.WithContext(ctx)
		post, err := publicPostBySlug(db, c.Param("slug"))
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return echo.ErrNotFound
		}
		if err != nil {
			return err
		}
		post.Content, err = publicPostContent(ctx, db, post)
		if err != nil {
			return err
		}
		content, toc := theme.HeadingIDs(post.Content)
		post.Content = content
		prev, next, err := prevNextPosts(db, post)
		if err != nil {
			return err
		}
		comments, commentCount, commentsOpen, err := publicCommentsForPost(db, c, post)
		if err != nil {
			return err
		}
		return publicRender(c, tm, db, "post.html", post.Title, map[string]interface{}{
			"IsPost":         true,
			"Post":           post,
			"ReadingMinutes": theme.ReadingTime(post.Content),
			"TOC":            toc,
			"Prev":           prev,
			"Next":           next,
			"AuthorCard":     authorCardFor(db, post.Author),
			"Comments":       comments,
			"CommentCount":   commentCount,
			"CommentsOpen":   commentsOpen,
		})
	})

	e.GET("/search", func(c echo.Context) error {
		db := datapkg.DB
		if db == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "Service unavailable")
		}
		db = db.WithContext(c.Request().Context())
		query := c.QueryParam("q")
		page := publicPageParam(c)
		size := perPage(c.Request().Context(), keyPerPageSearch)
		results := []searchResult{}
		var total int64
		if query != "" {
			var err error
			results, total, err = searchPublicPosts(db, query, page, size)
			if err != nil {
				return err
			}
			if page > 1 && page > publicTotalPages(total, size) {
				return echo.ErrNotFound
			}
		}
		totalPages := publicTotalPages(total, size)
		return publicRender(c, tm, db, "search.html", "搜尋結果", map[string]interface{}{
			"IsSearch":   true,
			"Query":      query,
			"Results":    results,
			"Total":      total,
			"Page":       page,
			"TotalPages": totalPages,
		})
	})

	e.GET("/api/search", func(c echo.Context) error {
		query := c.QueryParam("q")
		if query == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Query parameter 'q' is required"})
		}
		if datapkg.DB == nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Service unavailable"})
		}

		results, _, err := searchPublicPosts(datapkg.DB.WithContext(c.Request().Context()), query, 1, 20)
		if err != nil {
			return err
		}
		type postSummary struct {
			ID        uint      `json:"id"`
			Title     string    `json:"title"`
			Slug      string    `json:"slug"`
			Excerpt   string    `json:"excerpt"`
			CreatedAt time.Time `json:"created_at"`
		}
		summaries := make([]postSummary, 0, len(results))
		for _, result := range results {
			text := plainText(result.Post.Content)
			runes := []rune(text)
			if len(runes) > 200 {
				text = string(runes[:200]) + "..."
			}
			summaries = append(summaries, postSummary{
				ID:        result.Post.ID,
				Title:     result.Post.Title,
				Slug:      result.Post.Slug,
				Excerpt:   text,
				CreatedAt: result.Post.CreatedAt,
			})
		}
		return c.JSON(http.StatusOK, map[string]interface{}{
			"query": query,
			"posts": summaries,
			"count": len(summaries),
		})
	})

	// Generic page route must remain last so specific public routes take precedence.
	e.GET("/:slug", func(c echo.Context) error {
		slug := c.Param("slug")
		switch slug {
		case "posts", "search", "post", "c", "t", "author", "archive", "api", "admin", "auth", "login", "install", "uploads", "themes",
			"robots.txt", "sitemap.xml", "llms.txt", "feed.xml", "healthz":
			return echo.ErrNotFound
		}
		db := datapkg.DB
		if db == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "Service unavailable")
		}
		ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
		defer cancel()
		db = db.WithContext(ctx)
		var page core.Post
		err := publicPostVisibility(db).Where("posts.type = ? AND posts.slug = ?", "page", slug).
			Preload("Author").
			First(&page).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return echo.ErrNotFound
		}
		if err != nil {
			return err
		}
		page.Content, err = publicPostContent(ctx, db, page)
		if err != nil {
			return err
		}
		return publicRender(c, tm, db, "page.html", page.Title, map[string]interface{}{
			"IsPage":  true,
			"Page":    page,
			"Content": template.HTML(page.Content),
		})
	})
}

// homeLandingPage returns the page that / shows when home_show is page. ok is
// false when the latest posts should show: the mode is latest, or the page
// does not exist or is not public.
func homeLandingPage(ctx context.Context, db *gorm.DB) (page core.Post, ok bool, err error) {
	values := loadOptionValues(ctx, keyHomeShow, keyHomePageID)
	if valueOr(values, keyHomeShow) != "page" {
		return core.Post{}, false, nil
	}
	id, err := strconv.ParseUint(valueOr(values, keyHomePageID), 10, 64)
	if err != nil || id == 0 {
		return core.Post{}, false, nil
	}
	err = publicPostVisibility(db).Where("posts.type = ? AND posts.id = ?", "page", id).
		Preload("Author").First(&page).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return core.Post{}, false, nil
	}
	if err != nil {
		return core.Post{}, false, err
	}
	return page, true, nil
}

func publicPostContent(ctx context.Context, db *gorm.DB, post core.Post) (string, error) {
	content := post.Content
	if post.RenderVersion < 1 {
		content = markup.Policy(markup.TrustStrict).Sanitize(content)
	}
	return expandPublicBlocks(ctx, db, content)
}

var publicDynamicRE = regexp.MustCompile(`<div class="jc-dyn(?: jc-[A-Za-z0-9_-]+)*"[^>]*data-kind="(products|posts)"[^>]*data-args="([^"]*)"[^>]*></div>`)

func dynamicAttrs(raw string) map[string]string {
	attrs := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "=", 2)
		if len(parts) == 2 {
			attrs[parts[0]] = parts[1]
		}
	}
	return attrs
}

func queryDynamicItems(db *gorm.DB, kind string, attrs map[string]string) ([]markup.DynamicItem, error) {
	category := strings.TrimSpace(attrs["category"])
	limit := 20
	if raw := attrs["limit"]; raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 1 && n <= 100 {
			limit = n
		}
	}
	items := []markup.DynamicItem{}
	switch kind {
	case "products":
		q := db.Model(&core.Product{}).Where("products.status = ? AND products.deleted_at IS NULL", "active")
		if category != "" {
			q = q.Joins("JOIN product_category_links pcl ON pcl.product_id = products.id").Joins("JOIN product_categories pc ON pc.id = pcl.category_id").Where("pc.slug = ? AND pc.hidden = false", category)
		}
		var rows []core.Product
		if err := q.Order("products.created_at DESC").Limit(limit).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, p := range rows {
			items = append(items, markup.DynamicItem{Title: p.Title, URL: "/shop/p/" + p.Slug})
		}
	case "posts":
		q := publicPostVisibility(db).Where("posts.type = ?", "post")
		if category != "" {
			q = q.Joins("JOIN post_categories pc ON pc.post_id=posts.id").Joins("JOIN categories c ON c.id=pc.category_id").Where("c.slug = ? AND c.hide_from_list = false", category)
		}
		var rows []core.Post
		err := q.Preload("Categories", func(tx *gorm.DB) *gorm.DB {
			return tx.Where("categories.hide_from_list = ?", false).Order("categories.position ASC, categories.name ASC")
		}).Order("posts.published_at DESC NULLS LAST, posts.id DESC").Limit(limit).Find(&rows).Error
		if err != nil {
			return nil, err
		}
		cfg := loadPermalinkConfig(dbContext(db))
		for _, p := range rows {
			items = append(items, markup.DynamicItem{Title: p.Title, URL: permalinkPathFor(db, cfg, p)})
		}
	}
	return items, nil
}

func dynamicDataForContent(db *gorm.DB, content string) (markup.DynamicData, error) {
	data := markup.DynamicData{ItemsByKey: map[string][]markup.DynamicItem{}}
	for _, m := range publicDynamicRE.FindAllStringSubmatch(content, -1) {
		kind, attrs := m[1], dynamicAttrs(m[2])
		key := markup.DynamicItemsKey(kind, attrs)
		if _, ok := data.ItemsByKey[key]; ok {
			continue
		}
		items, err := queryDynamicItems(db, kind, attrs)
		if err != nil {
			return data, err
		}
		data.ItemsByKey[key] = items
	}
	return data, nil
}

func expandPublicBlocks(ctx context.Context, db *gorm.DB, content string) (string, error) {
	if !strings.Contains(content, "jc-dyn") {
		return content, nil
	}
	data, err := dynamicDataForContent(db, content)
	if err != nil {
		return "", err
	}
	return markup.Expand(ctx, content, data)
}

func publicPageParam(c echo.Context) int {
	value, err := strconv.ParseInt(c.QueryParam("page"), 10, 64)
	if err != nil {
		if numberError, ok := err.(*strconv.NumError); ok && errors.Is(numberError.Err, strconv.ErrRange) {
			return int(^uint(0) >> 1)
		}
		return 1
	}
	if value < 1 {
		return 1
	}
	maxInt := int64(^uint(0) >> 1)
	if value > maxInt {
		return int(maxInt)
	}
	return int(value)
}

func publicTotalPages(total int64, size int) int {
	if total <= 0 || size <= 0 {
		return 0
	}
	pages := (total-1)/int64(size) + 1
	maxInt := int64(^uint(0) >> 1)
	if pages > maxInt {
		return int(maxInt)
	}
	return int(pages)
}

func publicStats(db *gorm.DB) (publicSiteStats, error) {
	if db == nil {
		return publicSiteStats{}, gorm.ErrInvalidDB
	}
	stats := publicSiteStats{SinceYear: time.Now().Year()}
	if err := publicPostVisibility(db).Where("posts.type = ?", "post").Count(&stats.PostCount).Error; err != nil {
		return publicSiteStats{}, err
	}
	if err := publicPostVisibility(db).Where("posts.type = ?", "post").
		Distinct("posts.author_id").Count(&stats.AuthorCount).Error; err != nil {
		return publicSiteStats{}, err
	}
	var earliest core.Post
	err := publicPostVisibility(db).Where("posts.type = ?", "post").
		Select("posts.id, posts.created_at, posts.published_at").
		Order(archivePostDate + " ASC, posts.id ASC").First(&earliest).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return stats, nil
	}
	if err != nil {
		return publicSiteStats{}, err
	}
	if earliest.PublishedAt != nil {
		stats.SinceYear = earliest.PublishedAt.Year()
	} else {
		stats.SinceYear = earliest.CreatedAt.Year()
	}
	return stats, nil
}

func publicPostBySlug(db *gorm.DB, slug string) (core.Post, error) {
	if db == nil {
		return core.Post{}, gorm.ErrInvalidDB
	}
	var post core.Post
	err := publicPostVisibility(db).Where("posts.type = ? AND posts.slug = ?", "post", slug).
		Preload("Author").
		Preload("Categories", func(tx *gorm.DB) *gorm.DB {
			return tx.Where("categories.hide_from_list = ?", false).Order("categories.position ASC, categories.name ASC")
		}).
		Preload("Tags", func(tx *gorm.DB) *gorm.DB { return tx.Order("tags.name ASC") }).
		First(&post).Error
	return post, err
}

// prevNextPosts returns the nearest older post as prev and newer post as next, with IDs as stable tie-breakers.
func prevNextPosts(db *gorm.DB, post core.Post) (prev, next *core.Post, err error) {
	if db == nil {
		return nil, nil, gorm.ErrInvalidDB
	}
	date := post.CreatedAt
	if post.PublishedAt != nil {
		date = *post.PublishedAt
	}
	scope := func() *gorm.DB {
		return publicPostVisibility(db).Where("posts.type = ?", "post").Where("posts.id <> ?", post.ID)
	}
	load := func(query *gorm.DB, order string) (*core.Post, error) {
		var adjacent core.Post
		err := publicPostPreloads(query).Order(order).First(&adjacent).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return &adjacent, nil
	}

	prev, err = load(scope().Where("("+archivePostDate+" < ?) OR ("+archivePostDate+" = ? AND posts.id < ?)", date, date, post.ID),
		archivePostDate+" DESC, posts.id DESC")
	if err != nil {
		return nil, nil, err
	}
	next, err = load(scope().Where("("+archivePostDate+" > ?) OR ("+archivePostDate+" = ? AND posts.id > ?)", date, date, post.ID),
		archivePostDate+" ASC, posts.id ASC")
	if err != nil {
		return nil, nil, err
	}
	return prev, next, nil
}

func authorCardFor(db *gorm.DB, user core.User) authorCard {
	name := user.DisplayName
	if name == "" {
		name = user.Username
	}
	card := authorCard{Name: name, Bio: user.Bio, AvatarURL: user.AvatarURL, Username: user.Username}
	if db == nil {
		log.Printf("count public posts for author %d: database is not initialized", user.ID)
		return card
	}
	if err := publicPostVisibility(db).Where("posts.type = ? AND posts.author_id = ?", "post", user.ID).
		Count(&card.PostCount).Error; err != nil {
		log.Printf("count public posts for author %d: %v", user.ID, err)
	}
	return card
}

func publicCommentsForPost(db *gorm.DB, c echo.Context, post core.Post) ([]core.Comment, int, bool, error) {
	var comments []core.Comment
	err := db.WithContext(c.Request().Context()).
		Preload("Author", commentPreviewAuthor).
		Where("post_id = ? AND status = ?", post.ID, "approved").
		Order("created_at ASC, id ASC").Find(&comments).Error
	if err != nil {
		return nil, 0, false, err
	}

	values := map[string]string{"comments_enabled": "true", "comments_registered_only": "false"}
	var options []core.Option
	if err := db.Where("key IN ?", []string{"comments_enabled", "comments_registered_only"}).Find(&options).Error; err != nil {
		return nil, 0, false, err
	}
	for _, option := range options {
		values[option.Key] = option.Value
	}
	_, signedInErr := auth.GetCurrentUser(c)
	open := post.CommentsAllowed() && values["comments_enabled"] != "false" &&
		(signedInErr == nil || values["comments_registered_only"] != "true")
	return buildCommentTree(comments), len(comments), open, nil
}
