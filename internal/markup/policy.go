package markup

import (
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

func Policy(trust string) *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("p", "br", "hr", "strong", "b", "em", "i", "del", "s", "code", "pre", "blockquote", "ul", "ol", "li", "input", "a", "img", "h1", "h2", "h3", "h4", "h5", "h6", "table", "thead", "tbody", "tfoot", "tr", "th", "td", "caption", "figure", "div", "section", "span", "details", "summary", "sup", "sub")
	p.AllowAttrs("href", "title", "rel").OnElements("a")
	p.AllowAttrs("src", "alt", "title", "width", "height").OnElements("img")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^[A-Za-z0-9 _-]+$`)).Globally()
	p.AllowAttrs("id").Matching(regexp.MustCompile(`^[\pL\pN_:-]+$`)).OnElements("h1", "h2", "h3", "h4", "h5", "h6", "span", "div", "section", "details")
	// `data-line` is the only author-independent metadata on rendered source.
	// Jelly component hooks are restored from opaque renderer-owned placeholders
	// after sanitization, so source HTML cannot forge a widget or dynamic block.
	p.AllowAttrs("role", "scope", "data-line").OnElements("a", "div", "section", "span", "h1", "h2", "h3", "h4", "h5", "h6", "th", "td", "figure", "pre", "code", "p", "blockquote", "ul", "ol", "li", "hr", "table", "thead", "tbody", "tr", "details", "summary")
	p.AllowAttrs("disabled", "checked", "type").OnElements("input")
	p.AllowAttrs("loading", "sandbox", "referrerpolicy", "allowfullscreen").OnElements("iframe") // iframe is stripped below for untrusted content
	p.AllowAttrs("src").Matching(regexp.MustCompile(`^/(uploads|widgets)/[A-Za-z0-9_./?=&%-]*$`)).OnElements("img", "iframe")
	p.AllowAttrs("href", "src").Matching(regexp.MustCompile(`(?i)^(https?://|mailto:|/|#)`)).OnElements("a", "img")
	p.AllowRelativeURLs(true)
	p.AllowURLSchemes("http", "https", "mailto")
	p.RequireNoFollowOnLinks(true)
	if trust == TrustRich {
		p.AllowElements("figure", "section", "details", "summary", "table", "thead", "tbody", "tfoot", "tr", "th", "td", "caption", "img", "a", "div", "span", "h1", "h2", "h3", "h4", "h5", "h6")
	}
	return p
}

// sanitizeHTMLBlock uses a deliberately narrower policy and strips author supplied id/name
// attributes. Server generated component placeholders never pass through this function.
func sanitizeHTMLBlock(s string) string {
	p := bluemonday.NewPolicy()
	p.AllowElements("p", "br", "strong", "b", "em", "i", "del", "s", "blockquote", "ul", "ol", "li", "a", "img", "figure", "figcaption", "div", "span", "h1", "h2", "h3", "h4", "h5", "h6", "table", "thead", "tbody", "tr", "th", "td", "caption")
	p.AllowAttrs("href", "title", "rel").OnElements("a")
	p.AllowAttrs("src", "alt", "title").OnElements("img")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^[A-Za-z0-9 _-]+$`)).Globally()
	p.AllowAttrs("scope").OnElements("th")
	p.AllowRelativeURLs(true)
	p.AllowURLSchemes("http", "https", "mailto")
	out := p.Sanitize(s)
	out = strings.ReplaceAll(out, " id=", " ")
	out = strings.ReplaceAll(out, " name=", " ")
	return out
}
