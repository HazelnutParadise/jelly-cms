package theme

import (
	"html"
	"html/template"
)

// Template functions for the public theme (ticket 05, B2 and B3). The manager registers them in its base
// FuncMap under the names readingTime, highlight, snippet, headingIDs and flavor (manager.go). Each Go
// function here is a contract stub until its slice is implemented.

// TOCEntry is one row of the post table of contents: a heading with its generated id.
type TOCEntry struct {
	ID    string
	Text  string
	Level int
}

// ReadingTime estimates reading minutes for html by character count (CJK 400 characters per minute, Latin
// 200 words per minute), at least 1. Not implemented yet: returns 1.
func ReadingTime(html string) int {
	return 1
}

// Highlight escapes text and wraps case-insensitive matches of query in <mark>. Not implemented yet: it
// returns the escaped text without marks.
func Highlight(text, query string) template.HTML {
	return template.HTML(html.EscapeString(text))
}

// Snippet returns the text around the first match of query in html, n characters either side, with
// "……" added where the text was cut. Not implemented yet: returns an empty string.
func Snippet(html, query string, n int) template.HTML {
	return ""
}

// HeadingIDs adds id attributes to the h2 and h3 headings of html, generated from their text (duplicates
// get -2, -3, ...), and returns the new html with the table of contents entries. Not implemented yet: it
// returns the input unchanged and no entries.
func HeadingIDs(html string) (string, []TOCEntry) {
	return html, nil
}

// headingIDsForTemplate adapts HeadingIDs for templates, which accept only one result or a result plus an
// error. The template receives a map with HTML and TOC keys.
func headingIDsForTemplate(s string) map[string]interface{} {
	out, toc := HeadingIDs(s)
	return map[string]interface{}{"HTML": template.HTML(out), "TOC": toc}
}

// Flavor maps a category colour name (primary, secondary, accent, warning, ink) to the CSS variable name
// used for it. Not implemented yet: returns an empty string.
func Flavor(name string) string {
	return ""
}
