package markup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"
)

const maxSourceBytes = 2 << 20
const maxRenderTime = 2 * time.Second

var ErrSourceTooLarge = errors.New("markup: source exceeds 2 MB")
var ErrRenderTimeout = errors.New("markup: render timed out")
var markdownRenderSlots = make(chan struct{}, 2)

func Render(ctx context.Context, source string, opt Options) (Result, error) {
	if len(source) > maxSourceBytes {
		return Result{}, ErrSourceTooLarge
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, maxRenderTime)
	defer cancel()
	if opt.Trust != TrustRich {
		opt.Trust = TrustStrict
	}
	if opt.Format == "" {
		opt.Format = "jmd"
	}
	if opt.Format != "jmd" && opt.Format != "md" {
		opt.Format = "jmd"
	}
	var nonceBytes [16]byte
	if _, err := rand.Read(nonceBytes[:]); err != nil {
		return Result{}, fmt.Errorf("markup nonce: %w", err)
	}
	nonce := "jc-trusted-" + hex.EncodeToString(nonceBytes[:])
	p := &pipeline{ctx: ctx, source: source, opt: opt, nonce: nonce, trusted: make(map[string]string)}
	select {
	case markdownRenderSlots <- struct{}{}:
	case <-ctx.Done():
		return Result{}, ErrRenderTimeout
	}
	type renderResult struct {
		result Result
		err    error
	}
	done := make(chan renderResult, 1)
	go func() {
		defer func() { <-markdownRenderSlots }()
		prepared, err := p.prepare()
		if err == nil && ctx.Err() != nil {
			err = ErrRenderTimeout
		}
		var raw string
		var toc []TOCEntry
		if err == nil && ctx.Err() == nil {
			raw, toc, err = renderMarkdownWithPipeline(prepared, opt.SourceMap, 0, p)
		}
		if err == nil && ctx.Err() != nil {
			err = ErrRenderTimeout
		}
		if err != nil {
			done <- renderResult{err: err}
			return
		}
		clean := Policy(opt.Trust).Sanitize(raw)
		if ctx.Err() != nil {
			done <- renderResult{err: ErrRenderTimeout}
			return
		}
		clean = regexp.MustCompile(`(disabled|checked|allowfullscreen)=""`).ReplaceAllString(clean, "$1")
		clean = unwrapPlaceholderParagraphs(clean)
		clean, err = restoreTrustedMarkers(ctx, clean, p.trusted, p.nonce)
		if err != nil {
			done <- renderResult{err: err}
			return
		}
		clean = unwrapPlaceholderParagraphs(clean)
		if ctx.Err() != nil {
			done <- renderResult{err: ErrRenderTimeout}
			return
		}
		clean = normalizeFootnotes(clean)
		clean, toc = normalizeIDs(clean)
		if opt.SourceMap {
			clean = applyHeadingSourceLines(clean, p.headingLines)
		}
		clean = strings.TrimSpace(clean)
		result := Result{HTML: clean, TOC: toc, Diagnostics: p.diagnostics, Blocks: p.blocks}
		result.Stats = countStats(source, clean)
		result.Features = Features(clean)
		if ctx.Err() != nil {
			done <- renderResult{err: ErrRenderTimeout}
			return
		}
		done <- renderResult{result: result}
	}()
	select {
	case <-ctx.Done():
		return Result{}, ErrRenderTimeout
	case rendered := <-done:
		if ctx.Err() != nil {
			return Result{}, ErrRenderTimeout
		}
		return rendered.result, rendered.err
	}
}

func restoreTrustedMarkers(ctx context.Context, input string, trusted map[string]string, nonce string) (string, error) {
	prefix := "JELLYSLOT" + strings.TrimPrefix(nonce, "jc-trusted-") + "X"
	var expand func(string, int) (string, error)
	expand = func(s string, depth int) (string, error) {
		if depth > 16 {
			return "", fmt.Errorf("markup: trusted fragment nesting exceeded")
		}
		var out strings.Builder
		for pos := 0; pos < len(s); {
			select {
			case <-ctx.Done():
				return "", ErrRenderTimeout
			default:
			}
			i := strings.Index(s[pos:], prefix)
			if i < 0 {
				out.WriteString(s[pos:])
				break
			}
			i += pos
			out.WriteString(s[pos:i])
			end := strings.Index(s[i+len(prefix):], "END")
			if end < 0 {
				out.WriteString(s[i : i+len(prefix)])
				pos = i + len(prefix)
				continue
			}
			end += i + len(prefix) + len("END")
			marker := s[i:end]
			fragment, ok := trusted[marker]
			if !ok {
				out.WriteString(marker)
			} else {
				restored, err := expand(fragment, depth+1)
				if err != nil {
					return "", err
				}
				out.WriteString(restored)
			}
			pos = end
		}
		return out.String(), nil
	}
	return expand(input, 0)
}

func Flatten(source string) (string, error) { return flattenSource(source) }
func Expand(ctx context.Context, fragment string, data DynamicData) (string, error) {
	return expandFragment(ctx, fragment, data)
}
func Features(fragment string) []string { return detectFeatures(fragment) }
func Schema() SchemaDoc                 { return schemaDocument() }

func (p *pipeline) placeholder(markup string) string {
	return p.placeholderAt(markup, p.currentLine)
}

func (p *pipeline) placeholderAt(markup string, line int) string {
	if p.opt.SourceMap && line > 0 {
		markup = sourceLineTag.ReplaceAllStringFunc(markup, func(tag string) string {
			return strings.TrimSuffix(tag, ">") + ` data-line="` + fmt.Sprint(line) + `">`
		})
	}
	marker := "JELLYSLOT" + strings.TrimPrefix(p.nonce, "jc-trusted-") + "X" + fmt.Sprint(len(p.trusted)) + "END"
	p.trusted[marker] = markup
	return marker
}

func esc(s string) string { return html.EscapeString(s) }

var placeholderParagraphRE = regexp.MustCompile(`(?s)<p>((?:JELLYSLOT[0-9a-f]+X[0-9]+END\s*)+)</p>`)
var sourceLineTag = regexp.MustCompile(`^<([A-Za-z][A-Za-z0-9-]*)([^>]*)>`)

var headingTagRE = regexp.MustCompile(`<h[1-6]\b([^>]*)>`)

func applyHeadingSourceLines(s string, lines []int) string {
	index := 0
	return headingTagRE.ReplaceAllStringFunc(s, func(tag string) string {
		if index >= len(lines) {
			return tag
		}
		line := lines[index]
		index++
		if strings.Contains(tag, " data-line=") {
			return tag
		}
		return strings.TrimSuffix(tag, ">") + ` data-line="` + fmt.Sprint(line) + `">`
	})
}

func unwrapPlaceholderParagraphs(s string) string {
	return placeholderParagraphRE.ReplaceAllString(s, "$1")
}

var (
	customAttrValue = regexp.MustCompile(`^[A-Za-z0-9_.:+/-]+$`)
	containerStart  = regexp.MustCompile(`^\s*:::\s*([A-Za-z][A-Za-z0-9_-]*)(?:\s+(\{(?:"(?:\\[^\r\n]|[^"\\\r\n])*"|'(?:\\[^\r\n]|[^'\\\r\n])*'|[^}'"\r\n])*\}))?\s*(?:<!--.*-->)?\s*$`)
	lineDirective   = regexp.MustCompile(`::([A-Za-z][A-Za-z0-9_-]*)\{((?:"(?:\\[^\r\n]|[^"\\\r\n])*"|'(?:\\[^\r\n]|[^'\\\r\n])*'|[^}'"\r\n])*)\}`)
	attrToken       = regexp.MustCompile(`(?:#([\pL\pN_-]+)|\.([A-Za-z0-9_-]+)|([A-Za-z][A-Za-z0-9_-]*)=(?:"((?:\\[^\r\n]|[^"\\\r\n])*)"|'((?:\\[^\r\n]|[^'\\\r\n])*)'|([^\s}]+))|([A-Za-z][A-Za-z0-9_-]*))`)
)

func replaceDirectivesOutsideCode(source string, replace func(string) string) string {
	indices := lineDirective.FindAllStringIndex(source, -1)
	if len(indices) == 0 {
		return source
	}
	var out strings.Builder
	last := 0
	for _, span := range indices {
		out.WriteString(source[last:span[0]])
		prefix := source[:span[0]]
		inHTMLTag := strings.LastIndex(prefix, "<") > strings.LastIndex(prefix, ">")
		if insideInlineCode(prefix) || inHTMLTag || isEscapedAt(source, span[0]) {
			out.WriteString(source[span[0]:span[1]])
		} else {
			out.WriteString(replace(source[span[0]:span[1]]))
		}
		last = span[1]
	}
	out.WriteString(source[last:])
	return out.String()
}

func isEscapedAt(source string, index int) bool {
	slashes := 0
	for i := index - 1; i >= 0 && source[i] == '\\'; i-- {
		slashes++
	}
	return slashes%2 == 1
}

func parseAttrs(raw string) map[string]string {
	attrs := map[string]string{}
	raw = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(raw), "{"), "}"))
	for _, m := range attrToken.FindAllStringSubmatch(raw, -1) {
		switch {
		case m[1] != "":
			attrs["id"] = m[1]
		case m[2] != "":
			attrs["class"] = strings.TrimSpace(attrs["class"] + " " + m[2])
		case m[3] != "":
			v := m[6]
			if m[4] != "" || strings.Contains(m[0], `=""`) {
				v = decodeAttrValue(m[4], '"')
			}
			if m[5] != "" || strings.Contains(m[0], `=''`) {
				v = decodeAttrValue(m[5], '\'')
			}
			attrs[m[3]] = v
		case m[7] != "":
			attrs[m[7]] = "true"
		}
	}
	return attrs
}
