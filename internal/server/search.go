package server

import (
	"html/template"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"gorm.io/gorm"
)

// Public search (ticket 05, B2). GET /search?q=&page=  renders search.html.
//
// Contract:
//   - Data: Query string, Results []searchResult{Post core.Post; Title, Snippet template.HTML}, Total int64,
//     Page, TotalPages. An empty query renders the empty state with status 200 (no query runs).
//   - Scope: archivePublicPosts (published, public, not scheduled, not trashed), newest first, 10 per page.
//   - Matching: case-insensitive against plain text (HTML tags removed by plainText), title and content.
//     LIKE wildcards in the query are escaped with escapeLike (handlers_trash.go).
//   - Title and Snippet: keywords wrapped in <mark> after escaping (theme function highlight); the snippet
//     is taken with theme function snippet (n characters either side, "……" at cut points). Raw HTML from
//     content must never reach the page, so <script> is shown as text.
//   - Template: a yellow search band, "找到 N 篇文章", and when nothing matches the card
//     "沒有找到「關鍵字」" with a link to /posts.

// searchResult is one hit on the search page.
type searchResult struct {
	Post    core.Post
	Title   template.HTML
	Snippet template.HTML
}

// searchPublicPosts returns one page of public posts matching query, plus the total match count. Not
// implemented yet: returns zero values.
func searchPublicPosts(db *gorm.DB, query string, page, size int) ([]searchResult, int64, error) {
	return nil, 0, nil
}

// plainText removes HTML tags from html and returns the text used for matching and excerpts. Not
// implemented yet: returns its input unchanged.
func plainText(html string) string {
	return html
}
