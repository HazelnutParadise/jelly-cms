package seo

import (
	"context"
	"encoding/xml"
	"errors"
	"html"
	"regexp"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"gorm.io/gorm"
)

// Feed modes (ticket 08 「Feed 模式」): full puts the whole body in each item,
// summary puts a short excerpt, off removes the site feed.
const (
	FeedFull    = "full"
	FeedSummary = "summary"
	FeedOff     = "off"
)

// maxFeedItems caps feed_items.
const maxFeedItems = 100

// FeedPost is the post data a feed item needs. The server owns permalink
// patterns, so it turns a FeedPost into a URL through FeedOptions.PostURL.
type FeedPost struct {
	ID      uint
	Title   string
	Slug    string
	Content string
	Excerpt string
	Date    time.Time
	// CategorySlugs lists the visible categories, primary first.
	CategorySlugs []string
}

// FeedOptions is the feed configuration the server passes through the context.
// Zero values take the defaults noted on each field.
type FeedOptions struct {
	// Mode is FeedFull or FeedSummary (default summary). FeedOff is handled by the caller.
	Mode string
	// Items is the number of entries (default 20, at most 100).
	Items int
	// Excerpt is the summary length in runes (default 110).
	Excerpt int
	// SiteTitle and SiteDescription fill the channel.
	SiteTitle       string
	SiteDescription string
	// PostURL returns the public URL of a post. Default: siteURL + "/post/" + slug.
	PostURL func(FeedPost) string
	// Plain turns HTML into plain text. Default: strips tags.
	Plain func(html string) string
}

type feedOptionsKey struct{}

// WithFeedOptions returns a context that carries opts for Feed.
func WithFeedOptions(ctx context.Context, opts FeedOptions) context.Context {
	return context.WithValue(ctx, feedOptionsKey{}, opts)
}

func feedOptionsFrom(ctx context.Context) FeedOptions {
	opts, _ := ctx.Value(feedOptionsKey{}).(FeedOptions)
	return opts
}

// ErrFeedDatabase means the feed cannot read posts because the database is not ready.
var ErrFeedDatabase = errors.New("seo: feed needs a database")

type rssDocument struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	Items       []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
	Description string `xml:"description"`
}

var feedTagRE = regexp.MustCompile(`<[^>]*>`)
var feedSpaceRE = regexp.MustCompile(`\s+`)

// stripTags is the default plain-text conversion for summaries.
func stripTags(source string) string {
	text := html.UnescapeString(feedTagRE.ReplaceAllString(source, " "))
	return strings.TrimSpace(feedSpaceRE.ReplaceAllString(text, " "))
}

// truncateRunes keeps at most n runes and marks a cut with an ellipsis.
func truncateRunes(text string, n int) string {
	runes := []rune(text)
	if n <= 0 || len(runes) <= n {
		if n <= 0 {
			return ""
		}
		return text
	}
	return strings.TrimSpace(string(runes[:n])) + "…"
}

// Feed renders the RSS 2.0 site feed. Only scope "" (the whole site) is
// supported; category and author feeds are not implemented yet.
func Feed(ctx context.Context, siteURL, scope string) ([]byte, error) {
	if scope != "" {
		return nil, ErrNotImplemented
	}
	if data.DB == nil {
		return nil, ErrFeedDatabase
	}
	opts := feedOptionsFrom(ctx)
	if opts.Items <= 0 {
		opts.Items = 20
	}
	if opts.Items > maxFeedItems {
		opts.Items = maxFeedItems
	}
	if opts.Excerpt == 0 {
		opts.Excerpt = 110
	}
	if opts.Plain == nil {
		opts.Plain = stripTags
	}
	if opts.PostURL == nil {
		opts.PostURL = func(p FeedPost) string { return strings.TrimRight(siteURL, "/") + "/post/" + p.Slug }
	}

	var rows []core.Post
	err := data.DB.WithContext(ctx).Model(&core.Post{}).
		Where("posts.type = ? AND posts.status = ?", "post", "published").
		Where("(posts.visibility = ? OR posts.visibility = '' OR posts.visibility IS NULL)", "public").
		Where("COALESCE(posts.published_at, posts.created_at) <= ?", time.Now()).
		Preload("Categories", func(tx *gorm.DB) *gorm.DB {
			return tx.Where("categories.hide_from_list = ?", false).Order("categories.position ASC, categories.name ASC")
		}).
		Order("COALESCE(posts.published_at, posts.created_at) DESC, posts.id DESC").
		Limit(opts.Items).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}

	channel := rssChannel{
		Title:       opts.SiteTitle,
		Link:        siteURL,
		Description: opts.SiteDescription,
		Items:       make([]rssItem, 0, len(rows)),
	}
	for _, row := range rows {
		post := FeedPost{ID: row.ID, Title: row.Title, Slug: row.Slug, Content: row.Content, Excerpt: row.Excerpt,
			Date: row.CreatedAt}
		if row.PublishedAt != nil {
			post.Date = *row.PublishedAt
		}
		for _, category := range row.Categories {
			post.CategorySlugs = append(post.CategorySlugs, category.Slug)
		}
		link := opts.PostURL(post)
		channel.Items = append(channel.Items, rssItem{
			Title:       post.Title,
			Link:        link,
			GUID:        link,
			PubDate:     post.Date.UTC().Format(time.RFC1123Z),
			Description: feedDescription(opts, post),
		})
	}

	doc := rssDocument{Version: "2.0", Channel: channel}
	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), body...), nil
}

// feedDescription is the item body: the whole content in full mode, or the
// manual excerpt or a cut of the content in summary mode.
func feedDescription(opts FeedOptions, post FeedPost) string {
	if opts.Mode == FeedFull {
		return post.Content
	}
	if strings.TrimSpace(post.Excerpt) != "" {
		return truncateRunes(post.Excerpt, opts.Excerpt)
	}
	return truncateRunes(opts.Plain(post.Content), opts.Excerpt)
}
