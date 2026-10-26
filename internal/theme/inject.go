package theme

import (
	"bytes"
	"strings"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// InjectBeforeHeadEnd inserts snippet into a rendered page (ticket 13). The
// snippet goes right before the last "</head>" (matched without regard to
// letter case); without one, right before the last "</body>"; without either,
// at the end of the page. The rest of the page is returned byte for byte, and
// the input slice is not modified.
func InjectBeforeHeadEnd(page []byte, snippet string) []byte {
	lower := asciiLower(page)
	position := bytes.LastIndex(lower, []byte("</head>"))
	if position < 0 {
		position = bytes.LastIndex(lower, []byte("</body>"))
	}
	if position < 0 {
		position = len(page)
	}
	out := make([]byte, 0, len(page)+len(snippet))
	out = append(out, page[:position]...)
	out = append(out, snippet...)
	out = append(out, page[position:]...)
	return out
}

func asciiLower(value []byte) []byte {
	lower := make([]byte, len(value))
	for i, b := range value {
		if b >= 'A' && b <= 'Z' {
			b += 'a' - 'A'
		}
		lower[i] = b
	}
	return lower
}

// PlainTextParagraphs returns the visible text of an HTML fragment for the
// WebMCP get_post tool (ticket 13): no tags, entities decoded, script and style
// contents omitted, whitespace inside a block collapsed, and consecutive block
// elements (paragraphs, headings, list items, ...) separated by a blank line.
func PlainTextParagraphs(source string) string {
	root := &xhtml.Node{Type: xhtml.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := xhtml.ParseFragment(strings.NewReader(source), root)
	if err != nil {
		return ""
	}
	var paragraphs []string
	var current strings.Builder
	flush := func() {
		text := strings.Join(strings.Fields(current.String()), " ")
		if text != "" {
			paragraphs = append(paragraphs, text)
		}
		current.Reset()
	}
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.CommentNode {
			return
		}
		if node.Type == xhtml.ElementNode {
			if strings.EqualFold(node.Data, "script") || strings.EqualFold(node.Data, "style") {
				return
			}
			if node.Data == "br" {
				flush()
				return
			}
			if paragraphBlock(node.Data) {
				flush()
			}
		}
		if node.Type == xhtml.TextNode {
			current.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if node.Type == xhtml.ElementNode && paragraphBlock(node.Data) {
			flush()
		}
	}
	for _, node := range nodes {
		walk(node)
	}
	flush()
	return strings.Join(paragraphs, "\n\n")
}

func paragraphBlock(name string) bool {
	switch name {
	case "address", "article", "aside", "blockquote", "dd", "div", "dl", "dt", "fieldset", "figcaption", "figure", "footer", "form", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "li", "main", "nav", "ol", "p", "pre", "section", "table", "tbody", "td", "tfoot", "th", "thead", "tr", "ul":
		return true
	default:
		return false
	}
}
