package markup

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	htmlnode "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

func renderMarkdown(source string, sourceMap bool) (string, []TOCEntry, error) {
	return renderMarkdownAt(source, sourceMap, 0)
}

func renderMarkdownAt(source string, sourceMap bool, lineOffset int) (string, []TOCEntry, error) {
	return renderMarkdownWithPipeline(source, sourceMap, lineOffset, nil)
}

func renderMarkdownWithPipeline(source string, sourceMap bool, lineOffset int, p *pipeline) (string, []TOCEntry, error) {
	md := goldmark.New(goldmark.WithExtensions(extension.GFM, extension.Footnote, extension.TaskList), goldmark.WithParserOptions(parser.WithHeadingAttribute(), parser.WithAutoHeadingID()), goldmark.WithRendererOptions(gmhtml.WithUnsafe()))
	var b bytes.Buffer
	sourceBytes := []byte(source)
	if p != nil && p.opt.Format == "jmd" {
		doc := md.Parser().Parse(text.NewReader(sourceBytes))
		if err := p.applyLinkInlineAttrs(doc, sourceBytes, lineOffset); err != nil {
			return "", nil, err
		}
		if err := md.Renderer().Render(&b, sourceBytes, doc); err != nil {
			return "", nil, err
		}
	} else if err := md.Convert(sourceBytes, &b); err != nil {
		return "", nil, err
	}
	out := b.String()
	if sourceMap {
		out = applyASTSourceMap(md, sourceBytes, out, lineOffset)
	}
	out = normalizeTaskLists(out)
	out = normalizeFootnotes(out)
	var toc []TOCEntry
	used := map[string]bool{}
	out = headingRE.ReplaceAllStringFunc(out, func(tag string) string {
		m := headingRE.FindStringSubmatch(tag)
		level, _ := strconv.Atoi(m[1])
		id := ""
		if got := headingIDAttr.FindStringSubmatch(m[2]); got != nil {
			id = got[1]
		}
		text := plainHTML(m[3])
		if id == "" {
			id = slug(text)
			if id == "" {
				id = "section"
			}
		}
		baseID := id
		for suffix := 2; used[id]; suffix++ {
			id = fmt.Sprintf("%s-%d", baseID, suffix)
		}
		used[id] = true
		toc = append(toc, TOCEntry{ID: id, Text: text, Level: level})
		open := "<h" + m[1] + m[2] + ">"
		if headingIDAttr.MatchString(m[2]) {
			open = headingIDAttr.ReplaceAllString(open, `id="`+html.EscapeString(id)+`"`)
		} else {
			open = headingOpenEnd.ReplaceAllString(open, ` id="`+html.EscapeString(id)+`">`)
		}
		return open + m[3] + "</h" + m[1] + ">"
	})
	return strings.TrimSpace(out), toc, nil
}

// multilineCodeSpanLines returns source lines occupied by valid multiline
// inline code spans. The block preprocessor must leave these lines intact so
// JMD directives inside the spans stay literal.
// lineStarts returns the byte offset of every line start in source.
func lineStarts(source []byte) []int {
	starts := []int{0}
	for i, c := range source {
		if c == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// lineAt returns the zero-based line containing offset.
func lineAt(starts []int, offset int) int {
	return sort.Search(len(starts), func(i int) bool { return starts[i] > offset }) - 1
}

// multilineCodeSpanRanges maps zero-based source lines to the byte column
// ranges, delimiters included, covered by inline code spans crossing lines.
func multilineCodeSpanRanges(ctx context.Context, source []byte) (map[int][][2]int, error) {
	md := goldmark.New(goldmark.WithExtensions(extension.GFM, extension.Footnote, extension.TaskList), goldmark.WithParserOptions(parser.WithHeadingAttribute(), parser.WithAutoHeadingID()))
	doc := md.Parser().Parse(text.NewReader(source))
	protected := make(map[int][][2]int)
	var starts []int
	visited := 0
	err := ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		visited++
		if visited&0xff == 0 && ctx.Err() != nil {
			return ast.WalkStop, ErrRenderTimeout
		}
		span, ok := node.(*ast.CodeSpan)
		if !ok || !entering {
			return ast.WalkContinue, nil
		}
		start, stop := len(source), -1
		for child := span.FirstChild(); child != nil; child = child.NextSibling() {
			textNode, ok := child.(*ast.Text)
			if !ok || textNode.Segment.Padding != 0 {
				continue
			}
			if textNode.Segment.Start < start {
				start = textNode.Segment.Start
			}
			if textNode.Segment.Stop > stop {
				stop = textNode.Segment.Stop
			}
		}
		if stop <= start || !bytes.Contains(source[start:stop], []byte{'\n'}) {
			return ast.WalkContinue, nil
		}
		// Extend over at most one stripped padding space, then this span's own
		// backtick run, so adjacent spans never share delimiter bytes.
		if start > 0 && source[start-1] == ' ' {
			start--
		}
		for start > 0 && source[start-1] == '`' {
			start--
		}
		if stop < len(source) && source[stop] == ' ' {
			stop++
		}
		for stop < len(source) && source[stop] == '`' {
			stop++
		}
		if starts == nil {
			starts = lineStarts(source)
		}
		first, last := lineAt(starts, start), lineAt(starts, stop-1)
		for line := first; line <= last; line++ {
			lineStart := starts[line]
			lineStop := len(source)
			if line+1 < len(starts) {
				lineStop = starts[line+1] - 1
			}
			from, to := max(start, lineStart)-lineStart, min(stop, lineStop)-lineStart
			if to < from {
				continue
			}
			// Spans are visited in source order; merge any residual overlap.
			if rs := protected[line]; len(rs) > 0 && rs[len(rs)-1][1] >= from {
				rs[len(rs)-1][1] = max(rs[len(rs)-1][1], to)
				continue
			}
			protected[line] = append(protected[line], [2]int{from, to})
		}
		return ast.WalkSkipChildren, nil
	})
	return protected, err
}

type sourceMappedBlock struct {
	tag  string
	line int
}

// applyASTSourceMap derives each output block's source line from Goldmark's
// parsed source segments, then annotates matching rendered block starts.
func applyASTSourceMap(md goldmark.Markdown, source []byte, rendered string, lineOffset int) string {
	doc := md.Parser().Parse(text.NewReader(source))
	var blocks []sourceMappedBlock
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		tag := ""
		switch n.Kind() {
		case ast.KindParagraph:
			tag = "p"
		case ast.KindHeading:
			if h, ok := n.(*ast.Heading); ok {
				tag = fmt.Sprintf("h%d", h.Level)
			}
		case ast.KindFencedCodeBlock, ast.KindCodeBlock:
			tag = "pre"
		case ast.KindBlockquote:
			tag = "blockquote"
		case ast.KindList:
			if l, ok := n.(*ast.List); ok {
				if l.IsOrdered() {
					tag = "ol"
				} else {
					tag = "ul"
				}
			}
		case ast.KindListItem:
			tag = "li"
		case ast.KindThematicBreak:
			tag = "hr"
		default:
			if n.Kind().String() == "Table" {
				tag = "table"
			}
			if n.Kind().String() == "TableHeader" {
				tag = "thead"
			}
			if n.Kind().String() == "TableRow" {
				tag = "tr"
			}
			if n.Kind().String() == "TableCell" {
				tag = "th"
			}
		}
		if tag == "" {
			return ast.WalkContinue, nil
		}
		block, ok := n.(interface{ Lines() *text.Segments })
		if !ok || block.Lines() == nil || block.Lines().Len() == 0 {
			return ast.WalkContinue, nil
		}
		start := block.Lines().At(0).Start
		if start < 0 || start > len(source) {
			return ast.WalkContinue, nil
		}
		line := 1 + bytes.Count(source[:start], []byte{'\n'})
		blocks = append(blocks, sourceMappedBlock{tag: tag, line: line + lineOffset})
		return ast.WalkContinue, nil
	})
	startRE := regexp.MustCompile(`<(p|h[1-6]|pre|blockquote|ul|ol|li|hr|table|thead|tr|th|td)\b[^>]*>`)
	index := 0
	return startRE.ReplaceAllStringFunc(rendered, func(open string) string {
		if index >= len(blocks) {
			return open
		}
		tag := startRE.FindStringSubmatch(open)[1]
		if blocks[index].tag != tag {
			return open
		}
		line := blocks[index].line
		index++
		if strings.Contains(open, " data-line=") {
			return open
		}
		return strings.TrimSuffix(open, ">") + ` data-line="` + strconv.Itoa(line) + `">`
	})
}

var headingRE = regexp.MustCompile(`(?s)<h([1-6])([^>]*)>(.*?)</h[1-6]>`)
var headingIDAttr = regexp.MustCompile(`\bid="([^"]*)"`)
var headingOpenEnd = regexp.MustCompile(`>$`)

func plainHTML(s string) string {
	re := regexp.MustCompile(`<[^>]*>`)
	return strings.TrimSpace(html.UnescapeString(re.ReplaceAllString(s, "")))
}
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if r == ' ' || r == '\t' || r == '-' || r == '_' {
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
			continue
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r > 127 {
			b.WriteRune(r)
			dash = false
		}
	}
	return strings.Trim(b.String(), "-")
}
func countStats(source, rendered string) Stats {
	s := Stats{}
	for _, r := range source {
		if isCJK(r) {
			s.Chars++
		}
	}
	s.Minutes = (s.Chars + 399) / 400
	if s.Minutes == 0 && strings.TrimSpace(source) != "" {
		s.Minutes = 1
	}
	s.Words = len(strings.Fields(source))
	s.Images = len(imageRE.FindAllString(rendered, -1))
	s.Links = len(linkRE.FindAllString(rendered, -1))
	s.OpenTasks = len(openTaskRE.FindAllString(source, -1))
	s.Todos = len(todoRE.FindAllString(source, -1))
	return s
}
func isCJK(r rune) bool {
	return r >= 0x3400 && r <= 0x9fff || r >= 0xf900 && r <= 0xfaff || r >= 0x20000 && r <= 0x2fa1f
}

var imageRE = regexp.MustCompile(`<img\b`)
var linkRE = regexp.MustCompile(`<a\b`)
var openTaskRE = regexp.MustCompile(`(?m)^\s*[-*+]\s+\[ \]`)
var todoRE = regexp.MustCompile(`(?i)\bTODO\b`)

func expandFragment(ctx context.Context, s string, data DynamicData) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	return dynamicRE.ReplaceAllStringFunc(s, func(m string) string {
		parts := dynamicRE.FindStringSubmatch(m)
		attrs := parseDynamicArgs(html.UnescapeString(parts[2]))
		var items []DynamicItem
		if data.ItemsByKey != nil {
			items = data.ItemsByKey[DynamicItemsKey(parts[1], attrs)]
		} else {
			items = data.Products
			if parts[1] == "posts" {
				items = data.Posts
			}
		}
		var b strings.Builder
		b.WriteString(`<ul class="jc-` + parts[1] + `">`)
		for _, it := range items {
			if safeLinkURL(it.URL) {
				b.WriteString(`<li><a href="` + html.EscapeString(it.URL) + `">` + html.EscapeString(it.Title) + `</a></li>`)
			} else {
				b.WriteString(`<li>` + html.EscapeString(it.Title) + `</li>`)
			}
		}
		b.WriteString(`</ul>`)
		return b.String()
	}), nil
}

var dynamicRE = regexp.MustCompile(`<div class="jc-dyn(?: jc-[A-Za-z0-9_-]+)*"[^>]*data-kind="(products|posts)"[^>]*data-args="([^"]*)"[^>]*></div>`)

func parseDynamicArgs(raw string) map[string]string {
	attrs := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "=", 2)
		if len(parts) == 2 {
			attrs[parts[0]] = parts[1]
		}
	}
	return attrs
}

var taskULRE = regexp.MustCompile(`(?s)<ul>(.*?)(</ul>)`)
var taskLIRE = regexp.MustCompile(`(?s)<li>(.*?)</li>`)
var taskInputRE = regexp.MustCompile(`<input([^>]*)>`)

func normalizeTaskLists(s string) string {
	return taskULRE.ReplaceAllStringFunc(s, func(ul string) string {
		if !strings.Contains(ul, "<input") {
			return ul
		}
		m := taskULRE.FindStringSubmatch(ul)
		body := taskLIRE.ReplaceAllStringFunc(m[1], func(li string) string {
			part := taskLIRE.FindStringSubmatch(li)
			input := taskInputRE.FindString(part[1])
			if input == "" {
				return li
			}
			checked := strings.Contains(input, "checked")
			state := "disabled"
			if checked {
				state += " checked"
			}
			content := taskInputRE.ReplaceAllString(part[1], `<input type="checkbox" `+state+`>`)
			return `<li class="task-list-item">` + content + `</li>`
		})
		return `<ul class="contains-task-list">` + body + m[2]
	})
}

var footnoteRefRE = regexp.MustCompile(`<sup><a href="#fn:([^" ]+)"`)

func normalizeFootnotes(s string) string {
	s = footnoteRefRE.ReplaceAllString(s, `<sup id="fnref:$1"><a href="#fn:$1"`)
	s = regexp.MustCompile(`(class="footnote-ref" role="doc-noteref") rel="nofollow"`).ReplaceAllString(s, `$1`)
	return s
}

func normalizeIDs(s string) (string, []TOCEntry) {
	seen := map[string]bool{}
	var toc []TOCEntry
	s = headingRE.ReplaceAllStringFunc(s, func(tag string) string {
		m := headingRE.FindStringSubmatch(tag)
		if strings.Contains(m[2], `class="jc-tab-title"`) {
			return tag
		}
		level, _ := strconv.Atoi(m[1])
		text := plainHTML(m[3])
		old := ""
		if a := headingIDAttr.FindStringSubmatch(m[2]); a != nil {
			old = a[1]
		}
		id := old
		explicit := strings.Contains(m[2], `data-jc-heading-explicit=""`)
		if id == "" || (!explicit && (id == "heading" || strings.HasPrefix(id, "heading-"))) {
			id = slug(text)
		}
		if id == "" {
			id = "section"
		}
		baseID := id
		for suffix := 2; seen[id]; suffix++ {
			id = fmt.Sprintf("%s-%d", baseID, suffix)
		}
		seen[id] = true
		open := "<h" + m[1] + m[2] + ">"
		if headingIDAttr.MatchString(m[2]) {
			open = headingIDAttr.ReplaceAllString(open, `id="`+html.EscapeString(id)+`"`)
		} else {
			open = headingOpenEnd.ReplaceAllString(open, ` id="`+html.EscapeString(id)+`">`)
		}
		toc = append(toc, TOCEntry{ID: id, Text: text, Level: level})
		return open + m[3] + "</h" + m[1] + ">"
	})
	s = strings.ReplaceAll(s, ` data-jc-heading-explicit=""`, "")
	return s, toc
}

func detectFeatures(s string) []string {
	root := &htmlnode.Node{Type: htmlnode.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := htmlnode.ParseFragment(strings.NewReader(s), root)
	if err != nil {
		return nil
	}
	features := make(map[string]bool)
	var walk func(*htmlnode.Node)
	walk = func(n *htmlnode.Node) {
		if n.Type == htmlnode.ElementNode {
			classes := make(map[string]bool)
			attrs := make(map[string]string, len(n.Attr))
			present := make(map[string]bool, len(n.Attr))
			for _, attr := range n.Attr {
				attrs[attr.Key] = attr.Val
				present[attr.Key] = true
				if attr.Key == "class" {
					for _, class := range strings.Fields(attr.Val) {
						classes[class] = true
					}
				}
			}
			if classes["jc-accordion"] {
				features["accordion"] = true
			}
			if classes["jc-math"] {
				features["math"] = true
			}
			if classes["jc-widget"] {
				features["widget"] = true
			}
			if n.Data == "code" && classes["language-mermaid"] {
				features["mermaid"] = true
			}
			if present["data-reveal"] || present["data-hover"] || present["data-scroll"] || present["data-delay"] || present["data-dur"] || present["data-stagger"] || present["data-once"] {
				features["motion"] = true
			}
			if kind := attrs["data-jc"]; kind == "tabs" || kind == "carousel" || kind == "lightbox" || kind == "counter" || kind == "countdown" || kind == "compare" {
				features[kind] = true
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	var out []string
	for name := range features {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
