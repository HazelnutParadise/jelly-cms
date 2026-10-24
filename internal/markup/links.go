package markup

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

var linkMotionAttrs = []string{"reveal", "hover", "scroll", "delay", "dur", "stagger", "once"}

// stripJMDLinkAttributes removes valid trailing JMD attributes from actual
// Markdown links while preserving the original link source byte for byte.
func stripJMDLinkAttributes(source string) string {
	input := []byte(source)
	md := goldmark.New(goldmark.WithExtensions(extension.GFM, extension.Footnote, extension.TaskList), goldmark.WithParserOptions(parser.WithHeadingAttribute(), parser.WithAutoHeadingID()))
	doc := md.Parser().Parse(text.NewReader(input))
	type span struct{ start, end int }
	var removals []span
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		link, ok := node.(*ast.Link)
		if !ok {
			return ast.WalkContinue, nil
		}
		first, ok := link.NextSibling().(*ast.Text)
		if !ok || first.Segment.Padding != 0 {
			return ast.WalkContinue, nil
		}
		textNodes := []*ast.Text{first}
		value := append([]byte(nil), first.Value(input)...)
		last := ast.Node(first)
		for {
			next, ok := last.NextSibling().(*ast.Text)
			if !ok || next.Segment.Padding != 0 || textNodes[len(textNodes)-1].Segment.Stop != next.Segment.Start {
				break
			}
			piece := next.Value(input)
			if bytes.ContainsAny(piece, "\r\n") {
				break
			}
			textNodes = append(textNodes, next)
			value = append(value, piece...)
			last = next
		}
		end, ok := linkAttrTokenEnd(value)
		if !ok || !validAttrSyntax(string(value[:end])) {
			return ast.WalkContinue, nil
		}
		removals = append(removals, span{first.Segment.Start, first.Segment.Start + end})
		return ast.WalkContinue, nil
	})
	if len(removals) == 0 {
		return source
	}
	sort.Slice(removals, func(i, j int) bool { return removals[i].start < removals[j].start })
	var output strings.Builder
	output.Grow(len(source))
	last := 0
	for _, span := range removals {
		if span.start < last || span.end > len(source) {
			continue
		}
		output.WriteString(source[last:span.start])
		last = span.end
	}
	output.WriteString(source[last:])
	return output.String()
}

func (p *pipeline) applyLinkInlineAttrs(root ast.Node, source []byte, lineOffset int) error {
	var starts []int
	return ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		select {
		case <-p.ctx.Done():
			return ast.WalkStop, ErrRenderTimeout
		default:
		}
		link, ok := node.(*ast.Link)
		if !ok {
			return ast.WalkContinue, nil
		}
		first, ok := link.NextSibling().(*ast.Text)
		if !ok || first.Segment.Padding != 0 {
			return ast.WalkContinue, nil
		}
		textNodes := []*ast.Text{first}
		value := append([]byte(nil), first.Value(source)...)
		last := ast.Node(first)
		for {
			next, ok := last.NextSibling().(*ast.Text)
			if !ok || next.Segment.Padding != 0 || textNodes[len(textNodes)-1].Segment.Stop != next.Segment.Start {
				break
			}
			piece := next.Value(source)
			if bytes.ContainsAny(piece, "\r\n") {
				break
			}
			textNodes = append(textNodes, next)
			value = append(value, piece...)
			last = next
		}
		tokenEnd, ok := linkAttrTokenEnd(value)
		if !ok {
			return ast.WalkContinue, nil
		}
		raw := string(value[:tokenEnd])
		if !validAttrSyntax(raw) {
			return ast.WalkContinue, nil
		}
		attrs := parseAttrs(raw)
		if starts == nil {
			starts = lineStarts(source)
		}
		line := lineOffset + 1 + lineAt(starts, first.Segment.Start)
		classValues := strings.Fields(attrs["class"])
		classes := make([]string, 0, len(classValues))
		seen := make(map[string]bool, len(classValues))
		for _, className := range classValues {
			switch className {
			case "btn", "t", "lead", "center", "muted":
				if !seen[className] {
					classes = append(classes, "jc-"+className)
					seen[className] = true
				}
			default:
				p.diag(line, 1, "unknown_attr", "Unknown link class: "+className)
			}
		}
		motion := make([]string, 0, len(linkMotionAttrs))
		for _, key := range linkMotionAttrs {
			value, present := attrs[key]
			if !present {
				continue
			}
			if vals, enum := motionValues[key]; enum {
				if vals[value] {
					motion = append(motion, `data-`+key+`="`+esc(value)+`"`)
				} else {
					p.diag(line, 1, "attr_value", "Invalid value for link attribute: "+key)
				}
				continue
			}
			if key == "once" {
				if value == "true" {
					motion = append(motion, `data-once="true"`)
				} else {
					p.diag(line, 1, "attr_value", "Invalid value for link attribute: once")
				}
				continue
			}
			if customAttrValue.MatchString(value) {
				motion = append(motion, `data-`+key+`="`+esc(value)+`"`)
			} else {
				p.diag(line, 1, "attr_value", "Invalid value for link attribute: "+key)
			}
		}
		allowed := map[string]bool{"class": true}
		for _, key := range linkMotionAttrs {
			allowed[key] = true
		}
		keys := make([]string, 0, len(attrs))
		for key := range attrs {
			if !allowed[key] {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			p.diag(line, 1, "unknown_attr", "Unknown link attribute: "+key)
		}
		if len(classes) > 0 || len(motion) > 0 {
			marker := "JELLYSLOT" + strings.TrimPrefix(p.nonce, "jc-trusted-") + "X" + fmt.Sprint(len(p.trusted)) + "END"
			fragment := strings.Join(classes, " ")
			if len(motion) > 0 {
				fragment += `"`
				for i, attr := range motion {
					if i == len(motion)-1 {
						attr = strings.TrimSuffix(attr, `"`)
					}
					fragment += ` ` + attr
				}
			}
			p.trusted[marker] = fragment
			link.SetAttributeString("class", marker)
		}
		remaining := tokenEnd
		for _, suffix := range textNodes {
			segment := suffix.Segment
			consumed := min(remaining, segment.Stop-segment.Start)
			remaining -= consumed
			suffix.Segment = segment.WithStart(segment.Start + consumed)
			if suffix.Segment.Start == suffix.Segment.Stop && !suffix.SoftLineBreak() {
				suffix.Parent().RemoveChild(suffix.Parent(), suffix)
			}
			if remaining == 0 {
				break
			}
		}
		return ast.WalkContinue, nil
	})
}

func linkAttrTokenEnd(source []byte) (int, bool) {
	if len(source) == 0 || source[0] != '{' {
		return 0, false
	}
	var quote byte
	for i := 1; i < len(source); i++ {
		c := source[i]
		if c == '\n' || c == '\r' {
			return 0, false
		}
		if quote != 0 {
			if c == '\\' {
				if i+1 >= len(source) || source[i+1] == '\n' || source[i+1] == '\r' {
					return 0, false
				}
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == '}' {
			return i + 1, true
		}
	}
	return 0, false
}
