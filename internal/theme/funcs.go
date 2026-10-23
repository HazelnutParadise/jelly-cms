package theme

import (
	"bytes"
	"html"
	"html/template"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/text/cases"
)

type TOCEntry struct {
	ID    string
	Text  string
	Level int
}

func ReadingTime(source string) int {
	var cjkChars, otherWords int
	inWord, hasNonCJK := false, false
	for _, r := range PlainText(source) {
		if isCJK(r) {
			if inWord && hasNonCJK {
				otherWords++
			}
			inWord, hasNonCJK = false, false
			cjkChars++
			continue
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) || (inWord && unicode.IsMark(r)) {
			inWord = true
			hasNonCJK = true
			continue
		}
		if inWord && hasNonCJK {
			otherWords++
		}
		inWord, hasNonCJK = false, false
	}
	if inWord && hasNonCJK {
		otherWords++
	}
	minutes := (float64(cjkChars)/400 + float64(otherWords)/200)
	if minutes < 1 {
		return 1
	}
	return int(minutes) + boolInt(minutes > float64(int(minutes)))
}

func Highlight(text, query string) template.HTML {
	text = strings.ToValidUTF8(text, "\uFFFD")
	query = strings.ToValidUTF8(query, "\uFFFD")
	start, end, ok := foldMatch(text, query)
	escaped := html.EscapeString(text)
	if !ok {
		return template.HTML(escaped)
	}
	return template.HTML(
		html.EscapeString(text[:start]) +
			"<mark>" + html.EscapeString(text[start:end]) + "</mark>" +
			html.EscapeString(text[end:]),
	)
}

func Snippet(source, query string, n int) template.HTML {
	text := strings.ToValidUTF8(PlainText(source), "\uFFFD")
	runes := []rune(text)
	if n < 0 {
		n = 0
	}
	start, end, matched := foldMatch(text, query)
	if matched {
		startRune := utf8.RuneCountInString(text[:start])
		endRune := utf8.RuneCountInString(text[:end])
		windowStart := startRune - n
		if windowStart < 0 {
			windowStart = 0
		}
		windowEnd := endRune + n
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
		return template.HTML(prefix + string(Highlight(string(runes[windowStart:windowEnd]), query)) + suffix)
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

func HeadingIDs(source string) (string, []TOCEntry) {
	nodes, err := parseHTMLFragment(source)
	if err != nil {
		return source, nil
	}
	reserved := make(map[string]int)
	owners := make(map[string]*xhtml.Node)
	var headings []*xhtml.Node
	var collect func(*xhtml.Node)
	collect = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode {
			if strings.EqualFold(node.Data, "script") || strings.EqualFold(node.Data, "style") {
				return
			}
			for _, attr := range node.Attr {
				if attr.Key == "id" && attr.Val != "" {
					reserved[attr.Val]++
					owners[attr.Val] = node
				}
			}
			if node.Data == "h2" || node.Data == "h3" {
				headings = append(headings, node)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			collect(child)
		}
	}
	for _, node := range nodes {
		collect(node)
	}

	used := make(map[string]bool)
	toc := make([]TOCEntry, 0, len(headings))
	for _, heading := range headings {
		text := strings.Join(strings.Fields(nodeText(heading)), " ")
		existing := nodeID(heading)
		id := existing
		if id == "" || reserved[id] != 1 || owners[id] != heading || used[id] {
			base := headingSlug(text)
			id = base
			for suffix := 2; reserved[id] > 0 || used[id]; suffix++ {
				id = base + "-" + strconv.Itoa(suffix)
			}
			setNodeID(heading, id)
		}
		used[id] = true
		reserved[id]++
		level := 2
		if heading.Data == "h3" {
			level = 3
		}
		toc = append(toc, TOCEntry{ID: id, Text: text, Level: level})
	}

	var out bytes.Buffer
	for _, node := range nodes {
		if err := xhtml.Render(&out, node); err != nil {
			return source, nil
		}
	}
	return out.String(), toc
}

func headingIDsForTemplate(source string) map[string]interface{} {
	html, toc := HeadingIDs(source)
	return map[string]interface{}{"HTML": template.HTML(html), "TOC": toc}
}

func Flavor(name string) string {
	switch name {
	case "primary", "secondary", "accent", "warning", "ink":
		return "--" + name
	default:
		return ""
	}
}

func parseHTMLFragment(source string) ([]*xhtml.Node, error) {
	return xhtml.ParseFragment(strings.NewReader(source), &xhtml.Node{
		Type:     xhtml.ElementNode,
		Data:     "div",
		DataAtom: atom.Div,
	})
}

// PlainText returns visible text with whitespace between block elements and none added between inline
// elements. Script and style contents are omitted.
func PlainText(source string) string {
	nodes, err := parseHTMLFragment(source)
	if err != nil {
		return ""
	}
	var text strings.Builder
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode &&
			(strings.EqualFold(node.Data, "script") || strings.EqualFold(node.Data, "style")) {
			return
		}
		block := node.Type == xhtml.ElementNode && isBlockElement(node.Data)
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

func isBlockElement(name string) bool {
	switch strings.ToLower(name) {
	case "address", "article", "aside", "blockquote", "br", "dd", "div", "dl", "dt", "fieldset",
		"figcaption", "figure", "footer", "form", "h1", "h2", "h3", "h4", "h5", "h6", "header",
		"hr", "li", "main", "nav", "ol", "p", "pre", "section", "table", "td", "th", "tr", "ul":
		return true
	default:
		return false
	}
}

func nodeText(node *xhtml.Node) string {
	var text strings.Builder
	var walk func(*xhtml.Node)
	walk = func(current *xhtml.Node) {
		if current.Type == xhtml.ElementNode &&
			(strings.EqualFold(current.Data, "script") || strings.EqualFold(current.Data, "style")) {
			return
		}
		if current.Type == xhtml.TextNode {
			text.WriteString(current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return text.String()
}

func nodeID(node *xhtml.Node) string {
	for _, attr := range node.Attr {
		if attr.Key == "id" {
			return attr.Val
		}
	}
	return ""
}

func setNodeID(node *xhtml.Node, id string) {
	attrs := node.Attr[:0]
	found := false
	for _, attr := range node.Attr {
		if attr.Key != "id" {
			attrs = append(attrs, attr)
			continue
		}
		if !found {
			attr.Val = id
			attrs = append(attrs, attr)
			found = true
		}
	}
	if !found {
		attrs = append(attrs, xhtml.Attribute{Key: "id", Val: id})
	}
	node.Attr = attrs
}

func headingSlug(text string) string {
	var out strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(text) {
		switch {
		case unicode.IsLetter(r), unicode.IsNumber(r), unicode.IsMark(r):
			if hyphen && out.Len() > 0 {
				out.WriteByte('-')
			}
			out.WriteRune(r)
			hyphen = false
		default:
			hyphen = true
		}
	}
	if out.Len() == 0 {
		return "section"
	}
	return out.String()
}

func foldMatch(text, query string) (start, end int, ok bool) {
	if query == "" || text == "" {
		return 0, 0, false
	}
	query = cases.Fold().String(query)
	if query == "" {
		return 0, 0, false
	}
	var folded strings.Builder
	var foldedStarts, foldedEnds, sourceStarts, sourceEnds []int
	fold := cases.Fold()
	for byteStart, r := range text {
		byteEnd := byteStart + utf8.RuneLen(r)
		for _, foldedRune := range fold.String(string(r)) {
			foldedStarts = append(foldedStarts, folded.Len())
			folded.WriteRune(foldedRune)
			foldedEnds = append(foldedEnds, folded.Len())
			sourceStarts = append(sourceStarts, byteStart)
			sourceEnds = append(sourceEnds, byteEnd)
		}
	}
	foldedText := folded.String()
	matchStart := strings.Index(foldedText, query)
	if matchStart < 0 {
		return 0, 0, false
	}
	matchEnd := matchStart + len(query)
	startIndex := sort.Search(len(foldedStarts), func(i int) bool { return foldedStarts[i] >= matchStart })
	endIndex := sort.Search(len(foldedEnds), func(i int) bool { return foldedEnds[i] >= matchEnd })
	if startIndex >= len(sourceStarts) || endIndex >= len(sourceEnds) {
		return 0, 0, false
	}
	return sourceStarts[startIndex], sourceEnds[endIndex], true
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
