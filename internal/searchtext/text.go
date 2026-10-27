// Package searchtext defines the canonical visible-text and Unicode folding
// semantics used by public search and its persisted derived fields.
package searchtext

import (
	"strings"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/text/cases"
)

// PlainText returns visible text nodes, excluding script/style contents and
// separating block elements while joining inline elements.
func PlainText(source string) string {
	nodes, err := xhtml.ParseFragment(strings.NewReader(source), &xhtml.Node{Type: xhtml.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return ""
	}
	var text strings.Builder
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && (strings.EqualFold(node.Data, "script") || strings.EqualFold(node.Data, "style")) {
			return
		}
		block := node.Type == xhtml.ElementNode && isBlock(node.Data)
		if block {
			text.WriteByte(' ')
		}
		if node.Type == xhtml.TextNode {
			text.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if block {
			text.WriteByte(' ')
		}
	}
	for _, node := range nodes {
		walk(node)
	}
	return strings.Join(strings.Fields(text.String()), " ")
}

func isBlock(name string) bool {
	switch strings.ToLower(name) {
	case "address", "article", "aside", "blockquote", "br", "dd", "div", "dl", "dt", "fieldset", "figcaption", "figure", "footer", "form", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "li", "main", "nav", "ol", "p", "pre", "section", "table", "td", "th", "tr", "ul":
		return true
	default:
		return false
	}
}

// Fold applies Unicode default case folding.
func Fold(s string) string { return cases.Fold().String(s) }
