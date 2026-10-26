package server

import (
	"context"
	"database/sql"
	"html/template"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/searchtext"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"golang.org/x/text/cases"
	"gorm.io/gorm"
)

type searchResult struct {
	Post           core.Post
	Title, Snippet template.HTML
}

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
	prepared := theme.PrepareSearchQuery(query)
	if results, total, used := extensionSearchPosts(ctx, db, query, prepared, page, size); used {
		return results, total, nil
	}
	page, size = normalizeSearchPage(page, size)
	var results []searchResult
	var total int64
	err := db.WithContext(ctx).Transaction(func(snapshot *gorm.DB) error {
		scope := searchVisibleScope(snapshot, prepared.Folded())
		if err := scope.Session(&gorm.Session{}).Count(&total).Error; err != nil {
			return err
		}
		offset, ok := searchOffset(page, size)
		if !ok {
			results = []searchResult{}
			return nil
		}
		var posts []core.Post
		if err := scope.Session(&gorm.Session{}).
			Select("posts.id, posts.title, posts.slug, posts.content, posts.excerpt, posts.author_id, posts.cover, posts.published_at, posts.created_at, posts.updated_at").
			Preload("Author").Order(archivePostDate + " DESC, posts.id DESC").Limit(size).Offset(offset).Find(&posts).Error; err != nil {
			return err
		}
		results = make([]searchResult, 0, len(posts))
		for _, post := range posts {
			source := post.Excerpt
			if !prepared.Contains(searchtext.PlainText(source)) {
				source = post.Content
			}
			results = append(results, searchResult{Post: post, Title: prepared.Highlight(post.Title), Snippet: prepared.Snippet(source, 100)})
		}
		return nil
	}, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, 0, err
	}
	return results, total, nil
}

const searchTextPredicate = "(strpos(posts.search_title, ?) > 0 OR strpos(posts.search_content, ?) > 0 OR strpos(posts.search_excerpt, ?) > 0)"

func searchVisibleScope(db *gorm.DB, foldedQuery string) *gorm.DB {
	return archivePublicPosts(db).Where(searchTextPredicate, foldedQuery, foldedQuery, foldedQuery)
}

func normalizeSearchPage(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = publicPostsPageSize
	}
	if size > 100 {
		size = 100
	}
	return page, size
}

func searchOffset(page, size int) (int, bool) {
	max := int(^uint(0) >> 1)
	if page < 1 || size < 1 || page-1 > max/size {
		return 0, false
	}
	return (page - 1) * size, true
}

func plainText(source string) string { return searchtext.PlainText(source) }

func containsFold(text, query string) bool {
	// Compatibility wrapper: new request paths prepare the query once and use
	// theme.SearchQuery directly.
	return strings.Contains(cases.Fold().String(text), cases.Fold().String(query))
}
