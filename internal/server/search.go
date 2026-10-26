package server

import (
	"context"
	"html/template"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"golang.org/x/text/cases"
	"gorm.io/gorm"
)

const searchScanBatchSize = 200

// Public search (ticket 05, B2). GET /search?q=&page= renders search.html.
type searchResult struct {
	Post    core.Post
	Title   template.HTML
	Snippet template.HTML
}

// searchPublicPosts scans public posts in bounded keyset batches, matching visible text rather than HTML
// source. The stable newest-first order lets the scan retain only the requested page.
func searchPublicPosts(db *gorm.DB, query string, page, size int) ([]searchResult, int64, error) {
	if db == nil {
		return nil, 0, gorm.ErrInvalidDB
	}
	if query == "" {
		return []searchResult{}, 0, nil
	}
	ctx := context.Background()
	if db.Statement != nil && db.Statement.Context != nil {
		ctx = db.Statement.Context
	}
	if results, total, used := extensionSearchPosts(ctx, db, query, page, size); used {
		return results, total, nil
	}
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = publicPostsPageSize
	}

	maxInt64 := int64(^uint64(0) >> 1)
	skip := maxInt64
	if int64(page-1) <= maxInt64/int64(size) {
		skip = int64(page-1) * int64(size)
	}
	results := make([]searchResult, 0, size)
	var total int64
	var lastDate time.Time
	var lastID uint
	firstBatch := true
	for {
		var batch []core.Post
		queryDB := archivePublicPosts(db).Select(
			"posts.id, posts.title, posts.slug, posts.content, posts.excerpt, posts.author_id, posts.cover, posts.published_at, posts.created_at, posts.updated_at",
		).Preload("Author")
		if !firstBatch {
			queryDB = queryDB.Where(
				"("+archivePostDate+" < ?) OR ("+archivePostDate+" = ? AND posts.id < ?)",
				lastDate, lastDate, lastID,
			)
		}
		if err := queryDB.Order(archivePostDate + " DESC, posts.id DESC").
			Limit(searchScanBatchSize).Find(&batch).Error; err != nil {
			return nil, 0, err
		}
		if len(batch) == 0 {
			break
		}

		for _, post := range batch {
			content := plainText(post.Content)
			excerpt := plainText(post.Excerpt)
			if !containsFold(post.Title, query) && !containsFold(content, query) && !containsFold(excerpt, query) {
				continue
			}
			total++
			if total <= skip || len(results) >= size {
				continue
			}
			source := post.Excerpt
			if excerpt == "" || !containsFold(excerpt, query) {
				source = post.Content
			}
			results = append(results, searchResult{
				Post:    post,
				Title:   theme.Highlight(post.Title, query),
				Snippet: theme.Snippet(source, query, 100),
			})
		}
		last := batch[len(batch)-1]
		lastDate = last.CreatedAt
		if last.PublishedAt != nil {
			lastDate = *last.PublishedAt
		}
		lastID = last.ID
		firstBatch = false
	}
	return results, total, nil
}

// plainText extracts visible text nodes, excluding tag attributes and script/style contents.
func plainText(source string) string {
	return theme.PlainText(source)
}

func containsFold(text, query string) bool {
	fold := cases.Fold()
	return strings.Contains(fold.String(text), fold.String(query))
}
