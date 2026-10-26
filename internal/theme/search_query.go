package theme

import (
	"html"
	"html/template"
	"strings"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/searchtext"
)

// SearchQuery holds the folded query so matching, highlighting and snippets
// share one query normalization per request.
type SearchQuery struct{ folded string }

func PrepareSearchQuery(query string) SearchQuery { return SearchQuery{folded: searchtext.Fold(query)} }

func (q SearchQuery) Folded() string { return q.folded }

func (q SearchQuery) Contains(text string) bool {
	if q.folded == "" {
		return false
	}
	return strings.Contains(searchtext.Fold(text), q.folded)
}

func (q SearchQuery) Highlight(text string) template.HTML {
	text = strings.ToValidUTF8(text, "\uFFFD")
	start, end, ok := foldMatchPrepared(text, q.folded)
	escaped := html.EscapeString(text)
	if !ok {
		return template.HTML(escaped)
	}
	return template.HTML(html.EscapeString(text[:start]) + "<mark>" + html.EscapeString(text[start:end]) + "</mark>" + html.EscapeString(text[end:]))
}

func (q SearchQuery) Snippet(source string, n int) template.HTML {
	text := strings.ToValidUTF8(PlainText(source), "\uFFFD")
	runes := []rune(text)
	if n < 0 {
		n = 0
	}
	start, end, matched := foldMatchPrepared(text, q.folded)
	if matched {
		startRune, endRune := utf8.RuneCountInString(text[:start]), utf8.RuneCountInString(text[:end])
		windowStart, windowEnd := startRune-n, endRune+n
		if windowStart < 0 {
			windowStart = 0
		}
		if windowEnd > len(runes) {
			windowEnd = len(runes)
		}
		prefix, suffix := "", ""
		if windowStart > 0 {
			prefix = "……"
		}
		if windowEnd < len(runes) {
			suffix = "……"
		}
		return template.HTML(prefix + string(q.Highlight(string(runes[windowStart:windowEnd]))) + suffix)
	}
	windowEnd := len(runes)
	if n <= len(runes)/2 {
		windowEnd = n * 2
	}
	suffix := ""
	if windowEnd < len(runes) {
		suffix = "……"
	}
	return template.HTML(html.EscapeString(string(runes[:windowEnd])) + suffix)
}
