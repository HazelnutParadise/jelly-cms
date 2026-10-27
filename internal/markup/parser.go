package markup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type pipeline struct {
	ctx          context.Context
	currentLine  int
	headingLines []int
	source       string
	opt          Options
	nonce        string
	trusted      map[string]string
	diagnostics  []Diagnostic
	blocks       []Block
}

var knownContainers = map[string]bool{"hero": true, "section": true, "columns": true, "col": true, "sidebar": true, "cta": true, "gallery": true, "carousel": true, "faq": true, "item": true, "tabs": true, "tab": true, "products": true, "posts": true}
var headingSourceRE = regexp.MustCompile(`^\s{0,3}#{1,6}(?:\s|$)`)
var validIDRE = regexp.MustCompile(`^[\pL\pN_:-]{1,128}$`)
var headingAttrTokenRE = regexp.MustCompile(`(?:#[\pL\pN_-]+|\.[A-Za-z0-9_-]+|[A-Za-z][A-Za-z0-9_-]*(?:=(?:"(?:\\[^\r\n]|[^"\\\r\n])*"|'(?:\\[^\r\n]|[^'\\\r\n])*'|[^\s}]+))?)`)
var calloutOpenRE = regexp.MustCompile(`(?i)^>\s*\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\](?:\s*(.*))?$`)
var motionValues = map[string]map[string]bool{"reveal": {"fade-up": true, "fade": true, "slide-left": true, "slide-right": true, "zoom": true}, "hover": {"lift": true, "glow": true, "tilt": true}, "scroll": {"parallax": true}}
var enumValues = map[string]map[string]bool{"bg": {"primary": true, "secondary": true, "accent": true, "warning": true, "ink": true, "raspberry": true, "blue": true, "mint": true, "yellow": true}, "align": {"left": true, "center": true, "right": true}, "height": {"short": true, "normal": true, "tall": true, "full": true}, "ratio": {"1:1": true, "2:1": true, "1:2": true, "1:1:1": true}}

func motionValuesKey(key string) bool { _, ok := motionValues[key]; return ok }

func parseFenceOpen(line string) (byte, int, string, bool) {
	if !strings.HasPrefix(line, "```") && !strings.HasPrefix(line, "~~~") {
		return 0, 0, "", false
	}
	char := line[0]
	n := 0
	for n < len(line) && line[n] == char {
		n++
	}
	if n < 3 {
		return 0, 0, "", false
	}
	return char, n, strings.TrimSpace(line[n:]), true
}

func isFenceClose(line string, char byte, min int) bool {
	n := 0
	for n < len(line) && line[n] == char {
		n++
	}
	return n >= min && strings.TrimSpace(line[n:]) == ""
}

func (p *pipeline) diag(line, col int, code, msg string) {
	p.diagnostics = append(p.diagnostics, Diagnostic{Line: line, Col: col, Severity: "warning", Code: code, Message: msg})
}
func (p *pipeline) prepare() (string, error) {
	normalizedSource := strings.ReplaceAll(p.source, "\r\n", "\n")
	lines := strings.Split(normalizedSource, "\n")
	var protectedCode map[int][][2]int
	if p.opt.Format == "jmd" {
		var err error
		if protectedCode, err = multilineCodeSpanRanges(p.ctx, []byte(normalizedSource)); err != nil {
			return "", ErrRenderTimeout
		}
	}
	inlineJMD := func(segment string, line int) string {
		segment = replaceDirectivesOutsideCode(segment, func(tok string) string {
			m := lineDirective.FindStringSubmatch(tok)
			name := m[1]
			attrs := parseAttrs("{" + m[2] + "}")
			p.blocks = append(p.blocks, Block{Kind: "directive", Name: name, Attrs: attrs, StartLine: line, EndLine: line})
			return p.placeholder(p.directive(name, attrs, line))
		})
		return mathInline(segment, p)
	}
	coverParagraph := func(i int) {
		if n := len(p.blocks); n > 0 && p.blocks[n-1].Kind == "paragraph" && p.blocks[n-1].EndLine == i {
			p.blocks[n-1].EndLine = i + 1
		} else {
			p.blocks = append(p.blocks, Block{Kind: "paragraph", StartLine: i + 1, EndLine: i + 1})
		}
	}
	var walk func(int, int, int) (int, string)
	walk = func(i, end, depth int) (int, string) {
		var b strings.Builder
		inIndentedCode := false
		indentedBlock := -1
		for i < end {
			p.currentLine = i + 1
			if i%32 == 0 {
				select {
				case <-p.ctx.Done():
					return i, b.String()
				default:
				}
			}
			line := lines[i]
			trimmed := strings.TrimSpace(line)
			if inIndentedCode && (trimmed == "" || strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t")) {
				b.WriteString(line + "\n")
				if trimmed != "" && indentedBlock >= 0 {
					p.blocks[indentedBlock].EndLine = i + 1
				}
				i++
				continue
			}
			inIndentedCode = false
			indentedBlock = -1
			if ranges := protectedCode[i]; len(ranges) > 0 {
				// Only the code span bytes stay literal; surrounding text keeps JMD inline syntax.
				var out strings.Builder
				last := 0
				for _, r := range ranges {
					out.WriteString(inlineJMD(line[last:r[0]], i+1))
					out.WriteString(line[r[0]:r[1]])
					last = r[1]
				}
				out.WriteString(inlineJMD(line[last:], i+1))
				if trimmed != "" {
					coverParagraph(i)
				}
				b.WriteString(out.String() + "\n")
				i++
				continue
			}
			if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
				inIndentedCode = true
				p.blocks = append(p.blocks, Block{Kind: "code", StartLine: i + 1, EndLine: i + 1})
				indentedBlock = len(p.blocks) - 1
				b.WriteString(line + "\n")
				i++
				continue
			}
			if strings.HasPrefix(trimmed, "$$") && p.opt.Format == "jmd" {
				tex := strings.TrimSpace(strings.TrimPrefix(trimmed, "$$"))
				j := i
				if strings.HasSuffix(tex, "$$") {
					tex = strings.TrimSpace(strings.TrimSuffix(tex, "$$"))
					j = i + 1
				} else {
					tex = ""
					j = i + 1
					for j < end && !strings.Contains(lines[j], "$$") {
						if j%32 == 0 && p.ctx.Err() != nil {
							return end, b.String()
						}
						tex += lines[j] + "\n"
						j++
					}
					if j < end {
						tex += strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(lines[j]), "$$"))
						j++
					}
				}
				if strings.TrimSpace(tex) != "" {
					b.WriteString(mathHTML(strings.TrimSpace(tex), true, p) + "\n")
					i = j
					continue
				}
			}
			if trimmed == ":::" && p.opt.Format == "jmd" && depth > 0 {
				return i + 1, b.String()
			}
			if trimmed == ":::" && p.opt.Format == "jmd" {
				p.diag(i+1, 1, "unexpected_container_close", "Closing ::: has no open container")
				b.WriteString(line + "\n")
				i++
				continue
			}
			if m := containerStart.FindStringSubmatch(line); m != nil && p.opt.Format == "jmd" {
				name := m[1]
				attrs := parseAttrs(m[2])
				start := i + 1
				if !knownContainers[name] {
					p.diag(start, 1, "unknown_block", "Unknown container: "+name)
					b.WriteString(line + "\n")
					i++
					continue
				}
				if depth >= 8 {
					p.diagnostics = append(p.diagnostics, Diagnostic{Line: start, Col: 1, Severity: "error", Code: "container_depth", Message: "Container nesting exceeds 8"})
					b.WriteString(line + "\n")
					i++
					continue
				}
				childStart := i + 1
				j, inner := walk(childStart, end, depth+1)
				closed := j > childStart && j <= end && j-1 < len(lines) && strings.TrimSpace(lines[j-1]) == ":::"
				if !closed {
					p.diag(start, 1, "unclosed_container", "Container is missing a closing :::")
					inner = line + "\n" + inner
				}
				if closed {
					p.blocks = append(p.blocks, Block{Kind: "container", Name: name, Attrs: attrs, StartLine: start, EndLine: j})
					p.addContainer(name, attrs, inner, start, &b)
					// Preserve the original source-line offsets for blocks after this placeholder.
					b.WriteString(strings.Repeat("\n", j-start))
				} else {
					b.WriteString(inner)
				}
				i = j
				continue
			}
			if fenceChar, fenceLen, lang, ok := parseFenceOpen(trimmed); ok {
				start := i + 1
				codeLines := []string{}
				j := i + 1
				for j < end && !isFenceClose(strings.TrimSpace(lines[j]), fenceChar, fenceLen) {
					if j%32 == 0 {
						select {
						case <-p.ctx.Done():
							return end, b.String()
						default:
						}
					}
					codeLines = append(codeLines, lines[j])
					j++
				}
				stop := j
				if stop < end {
					stop++
				}
				code := strings.Join(codeLines, "\n")
				language := lang
				if fields := strings.Fields(lang); len(fields) > 0 {
					language = fields[0]
				}
				if language == "table" {
					h, ok, ds := renderTable(code, start)
					p.diagnostics = append(p.diagnostics, ds...)
					if ok {
						b.WriteString(p.placeholder(h) + "\n")
					} else {
						fence := strings.Repeat(string(fenceChar), fenceLen)
						b.WriteString(fence + lang + "\n" + code + "\n" + fence + "\n")
					}
					p.blocks = append(p.blocks, Block{Kind: "table", Name: "table", StartLine: start, EndLine: stop})
					i = stop
					continue
				}
				if strings.EqualFold(language, "latex") {
					expr := strings.TrimSpace(code)
					if strings.Contains(expr, "\\documentclass") {
						sum := sha256.Sum256([]byte(code))
						hash := hex.EncodeToString(sum[:])
						b.WriteString(p.placeholderAt(`<figure class="jc-latex" data-state="source" data-paper="a4" data-hash="sha256:`+hash+`"><pre><code>`+esc(code)+`</code></pre></figure>`, start) + "\n")
					} else {
						b.WriteString(mathHTML(expr, true, p) + "\n")
					}
					p.blocks = append(p.blocks, Block{Kind: "latex", Name: "latex", StartLine: start, EndLine: stop})
					i = stop
					continue
				}
				if strings.EqualFold(language, "html") {
					codeMode := strings.Contains(lang, "{code}")
					if p.opt.Format == "md" || p.opt.Trust == TrustStrict || codeMode {
						if p.opt.Format == "jmd" && p.opt.Trust == TrustStrict {
							p.diag(start, 1, "html_strict", "HTML blocks are shown as code for this trust level")
						}
						fence := strings.Repeat(string(fenceChar), fenceLen)
						b.WriteString(fence + lang + "\n" + code + "\n" + fence + "\n")
					} else {
						clean := sanitizeHTMLBlock(code)
						b.WriteString(p.placeholderAt(`<div class="jc-html">`+clean+`</div>`, start) + "\n")
					}
					p.blocks = append(p.blocks, Block{Kind: "html", Name: "html", StartLine: start, EndLine: stop})
					i = stop
					continue
				}
				if strings.EqualFold(language, "mermaid") {
					b.WriteString(p.placeholderAt(`<pre><code class="language-mermaid">`+esc(code)+"\n</code></pre>", start) + "\n")
					p.blocks = append(p.blocks, Block{Kind: "code", Name: lang, StartLine: start, EndLine: stop})
					i = stop
					continue
				}
				if lang != "" {
					if strings.EqualFold(language, "markdown") {
						b.WriteString(p.placeholderAt(`<pre><code class="language-markdown">`+esc(code)+`</code></pre>`, start) + "\n")
					} else {
						b.WriteString(p.placeholderAt(highlightCode(lang, code), start) + "\n")
					}
					p.blocks = append(p.blocks, Block{Kind: "code", Name: lang, StartLine: start, EndLine: stop})
					i = stop
					continue
				}
				fence := strings.Repeat(string(fenceChar), fenceLen)
				b.WriteString(fence + lang + "\n" + code + "\n" + fence + "\n")
				p.blocks = append(p.blocks, Block{Kind: "code", StartLine: start, EndLine: stop})
				i = stop
				continue
			}
			if strings.HasPrefix(trimmed, "> [!") {
				m := calloutOpenRE.FindStringSubmatch(trimmed)
				if m == nil {
					p.diag(i+1, 1, "unknown_callout", "Unknown callout kind; rendered as a blockquote")
				} else {
					kind := strings.ToLower(m[1])
					title := strings.ToUpper(kind[:1]) + kind[1:]
					var body []string
					if strings.TrimSpace(m[2]) != "" {
						body = append(body, m[2])
					}
					j := i + 1
					for j < end && strings.HasPrefix(strings.TrimSpace(lines[j]), ">") {
						if j%32 == 0 && p.ctx.Err() != nil {
							return end, b.String()
						}
						body = append(body, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[j]), ">")))
						j++
					}
					bodyHTML := ""
					if strings.TrimSpace(strings.Join(body, "\n")) != "" {
						lineOffset := i + 1
						if strings.TrimSpace(m[2]) != "" {
							lineOffset = i
						}
						bodyHTML, _, _ = renderMarkdownWithPipeline(strings.Join(body, "\n"), p.opt.SourceMap, lineOffset, p)
						bodyHTML = Policy(p.opt.Trust).Sanitize(bodyHTML)
					}
					fragment := `<div class="jc-callout" data-kind="` + kind + `" role="note"><p class="jc-callout-title">` + title + `</p>` + bodyHTML + `</div>`
					b.WriteString(p.placeholderAt(fragment, i+1) + "\n")
					i = j
					continue
				}
			}
			if strings.Contains(line, "<!--more-->") {
				parts := strings.Split(line, "<!--more-->")
				if strings.TrimSpace(parts[0]) != "" {
					b.WriteString(parts[0] + "\n")
				}
				b.WriteString(p.placeholder(`<span class="jc-more" id="more"></span>`) + "\n")
				if strings.TrimSpace(parts[len(parts)-1]) != "" {
					b.WriteString(parts[len(parts)-1] + "\n")
				}
				i++
				continue
			}
			if p.opt.Format == "jmd" {
				line = replaceDirectivesOutsideCode(line, func(tok string) string {
					m := lineDirective.FindStringSubmatch(tok)
					name := m[1]
					attrs := parseAttrs("{" + m[2] + "}")
					p.blocks = append(p.blocks, Block{Kind: "directive", Name: name, Attrs: attrs, StartLine: i + 1, EndLine: i + 1})
					return p.placeholder(p.directive(name, attrs, i+1))
				})
			}
			// Heading inline attributes are stripped before goldmark parses the heading.
			if p.opt.Format == "jmd" && strings.HasPrefix(trimmed, "#") {
				p.headingLines = append(p.headingLines, i+1)
				line = p.headingAttrs(line, i+1)
			} else if headingSourceRE.MatchString(line) {
				p.headingLines = append(p.headingLines, i+1)
			}
			if p.opt.Format == "jmd" {
				line = mathInline(line, p)
			}
			if strings.TrimSpace(line) != "" && !strings.HasPrefix(strings.TrimSpace(line), "<span data-jc-slot") {
				coverParagraph(i)
			}
			b.WriteString(line + "\n")
			i++
		}
		return i, b.String()
	}
	_, prepared := walk(0, len(lines), 0)
	if p.ctx.Err() != nil {
		return "", ErrRenderTimeout
	}
	if len(p.blocks) == 0 && strings.TrimSpace(p.source) != "" {
		p.blocks = []Block{{Kind: "paragraph", StartLine: 1, EndLine: len(lines)}}
	}
	// Sort stable by start line, with containers before children sharing a start.
	sort.SliceStable(p.blocks, func(i, j int) bool {
		if p.blocks[i].StartLine == p.blocks[j].StartLine {
			return p.blocks[i].EndLine > p.blocks[j].EndLine
		}
		return p.blocks[i].StartLine < p.blocks[j].StartLine
	})
	return prepared, nil
}

func (p *pipeline) headingAttrs(line string, n int) string {
	idx, end, found := trailingAttrBlock(line)
	if !found || strings.TrimSpace(line[end:]) != "" {
		return line
	}
	rawAttrs := strings.TrimSpace(line[idx:end])
	if !validAttrSyntax(rawAttrs) {
		return line
	}
	attrs := parseAttrs(rawAttrs)
	title := strings.TrimSpace(line[:idx])
	var a []string
	var classes []string
	for k, v := range attrs {
		if k == "id" {
			if !validIDRE.MatchString(v) {
				p.diag(n, 1, "attr_value", "Invalid heading id")
			}
			continue
		}
		if k == "class" {
			for _, c := range strings.Fields(v) {
				if c == "btn" || c == "t" || c == "lead" || c == "center" || c == "muted" {
					classes = append(classes, "jc-"+c)
				} else {
					p.diag(n, 1, "unknown_attr", "Unknown heading class: "+c)
				}
			}
			continue
		}
		if vals, ok := motionValues[k]; ok && vals[v] {
			a = append(a, "data-"+k+"=\""+esc(v)+"\"")
			continue
		}
		if k == "delay" || k == "dur" || k == "stagger" {
			if customAttrValue.MatchString(v) {
				a = append(a, "data-"+k+"=\""+esc(v)+"\"")
			}
			continue
		}
		if k == "once" && v == "true" {
			a = append(a, "data-once")
			continue
		}
		p.diag(n, 1, "unknown_attr", "Unknown heading attribute: "+k)
	}
	if len(classes) > 0 {
		a = append(a, `class="`+strings.Join(classes, " ")+`"`)
	}
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 {
		return line
	}
	tag := fmt.Sprintf("h%d", level)
	explicitID := validIDRE.MatchString(attrs["id"])
	if !explicitID {
		attrs["id"] = slug(strings.TrimSpace(strings.TrimLeft(title, "#")))
		if attrs["id"] == "" {
			attrs["id"] = "section"
		}
	}
	outputAttrs := []string{`id="` + esc(attrs["id"]) + `"`}
	if explicitID {
		outputAttrs = append(outputAttrs, `data-jc-heading-explicit=""`)
	}
	outputAttrs = append(outputAttrs, a...)
	text := strings.TrimSpace(strings.TrimLeft(title, "#"))
	inline := renderHeadingInline(text, p, n-1)
	return p.placeholder("<" + tag + " " + strings.Join(outputAttrs, " ") + ">" + inline + "</" + tag + ">")
}

func validAttrSyntax(raw string) bool {
	raw = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(raw, "{"), "}"))
	if raw == "" {
		return false
	}
	indices := headingAttrTokenRE.FindAllStringIndex(raw, -1)
	if len(indices) == 0 {
		return false
	}
	pos := 0
	for _, span := range indices {
		if strings.TrimSpace(raw[pos:span[0]]) != "" {
			return false
		}
		pos = span[1]
	}
	return strings.TrimSpace(raw[pos:]) == ""
}

func renderHeadingInline(source string, p *pipeline, lineOffset int) string {
	h, _, err := renderMarkdownWithPipeline("# "+source, false, lineOffset, p)
	if err != nil {
		return esc(source)
	}
	match := regexp.MustCompile(`(?s)<h1[^>]*>(.*?)</h1>`).FindStringSubmatch(h)
	if len(match) != 2 {
		return esc(source)
	}
	return Policy(TrustStrict).Sanitize(match[1])
}

func (p *pipeline) addContainer(name string, attrs map[string]string, inner string, line int, out *strings.Builder) {
	p.currentLine = line
	emitAttrs := attrs
	if name == "products" || name == "posts" {
		emitAttrs = make(map[string]string, len(attrs))
		for k, v := range attrs {
			emitAttrs[k] = v
		}
		delete(emitAttrs, "category")
		delete(emitAttrs, "limit")
	}
	a := p.emitAttrs(emitAttrs, line)
	switch name {
	case "hero":
		out.WriteString(p.placeholderAt(`<section class="`+p.componentClass(attrs, "jc-hero")+`"`+a+`><div class="jc-hero-inner">`+p.containerBody(inner, line)+`</div></section>`, line) + "\n")
	case "columns", "section", "sidebar", "cta":
		cl := map[string]string{"columns": "jc-columns", "section": "jc-section", "sidebar": "jc-sidebar", "cta": "jc-cta"}[name]
		out.WriteString(p.placeholderAt(`<div class="`+p.componentClass(attrs, cl)+`"`+a+`>`+p.containerBody(inner, line)+`</div>`, line) + "\n")
	case "gallery":
		out.WriteString(p.placeholderAt(`<div class="`+p.componentClass(attrs, "jc-gallery")+`" data-jc="lightbox"`+a+`>`+p.containerBody(inner, line)+`</div>`, line) + "\n")
	case "carousel":
		out.WriteString(p.placeholderAt(`<div class="`+p.componentClass(attrs, "jc-carousel")+`" data-jc="carousel"`+a+`>`+p.containerBody(inner, line)+`</div>`, line) + "\n")
	case "faq":
		out.WriteString(p.placeholderAt(`<div class="`+p.componentClass(attrs, "jc-faq")+`"`+a+`>`+p.containerBody(inner, line)+`</div>`, line) + "\n")
	case "tabs":
		out.WriteString(p.placeholderAt(`<div class="`+p.componentClass(attrs, "jc-tabs")+`" data-jc="tabs"`+a+`>`+p.containerBody(inner, line)+`</div>`, line) + "\n")
	case "col":
		out.WriteString(p.placeholderAt(`<div class="`+p.componentClass(attrs, "jc-col")+`"`+a+`>`+p.containerBody(inner, line)+`</div>`, line) + "\n")
	case "item":
		out.WriteString(p.placeholderAt(`<details class="`+p.componentClass(attrs, "jc-accordion")+`"`+a+`><summary>`+esc(attrs["title"])+`</summary>`+p.containerBody(inner, line)+`</details>`, line) + "\n")
	case "tab":
		title := attrs["title"]
		out.WriteString(p.placeholderAt(`<section class="`+p.componentClass(attrs, "jc-tab")+`" data-title="`+esc(title)+`"`+a+`><h3 class="jc-tab-title">`+esc(title)+`</h3>`+p.containerBody(inner, line)+`</section>`, line) + "\n")
	case "products", "posts":
		kind := name
		var keys []string
		validQuery := true
		for k := range attrs {
			if k == "category" {
				if validDynamicCategory(attrs[k]) {
					keys = append(keys, k)
				} else {
					p.diag(line, 1, "attr_value", "Invalid value for category")
					validQuery = false
				}
			} else if k == "limit" {
				n, err := strconv.Atoi(attrs[k])
				if err != nil || n < 1 || n > 100 {
					p.diag(line, 1, "attr_value", "Invalid value for limit")
					validQuery = false
				} else {
					keys = append(keys, k)
				}
			} else if k != "id" && k != "class" && k != "bg" && k != "align" && k != "cubes" && k != "height" && k != "ratio" && !motionValuesKey(k) && k != "delay" && k != "dur" && k != "stagger" && k != "once" {
				p.diag(line, 1, "unknown_attr", "Unknown attribute: "+k)
			}
		}
		sort.Strings(keys)
		var payload []string
		for _, k := range keys {
			payload = append(payload, k+"="+attrs[k])
		}
		if !validQuery {
			out.WriteString(p.placeholderAt(`<div class="jc-dyn-invalid"></div>`, line) + "\n")
			return
		}
		out.WriteString(p.placeholderAt(`<div class="`+p.componentClass(attrs, "jc-dyn")+`"`+a+` data-kind="`+kind+`" data-args="`+esc(strings.Join(payload, "\n"))+`"></div>`, line) + "\n")
	}
}
func (p *pipeline) containerBody(s string, line int) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	body, _, err := renderMarkdownWithPipeline(s, p.opt.SourceMap, line, p)
	if err != nil {
		return esc(s)
	}
	body = Policy(p.opt.Trust).Sanitize(body)
	body = unwrapPlaceholderParagraphs(body)
	return body
}
func (p *pipeline) emitAttrs(attrs map[string]string, line int) string {
	var b strings.Builder
	keys := []string{"id", "bg", "align", "height", "ratio", "cubes", "reveal", "hover", "scroll", "delay", "dur", "stagger", "once", "title"}
	known := map[string]bool{}
	for _, k := range keys {
		known[k] = true
		v, ok := attrs[k]
		if !ok {
			continue
		}
		if k == "id" {
			if validIDRE.MatchString(v) {
				b.WriteString(` id="` + esc(v) + `"`)
			} else {
				p.diag(line, 1, "attr_value", "Invalid id attribute")
			}
		} else if k == "title" {
			// title is consumed by item and tab wrappers.
		} else if enum, ok := enumValues[k]; ok {
			if enum[v] {
				b.WriteString(` data-` + k + `="` + esc(v) + `"`)
			} else {
				p.diag(line, 1, "attr_value", "Unknown value for "+k)
			}
		} else if enum, ok := motionValues[k]; ok {
			if enum[v] {
				b.WriteString(` data-` + k + `="` + esc(v) + `"`)
			} else {
				p.diag(line, 1, "attr_value", "Unknown value for "+k)
			}
		} else if k == "cubes" && v == "true" {
			b.WriteString(` data-cubes`)
		} else if (k == "delay" || k == "dur" || k == "stagger") && customAttrValue.MatchString(v) {
			b.WriteString(` data-` + k + `="` + esc(v) + `"`)
		} else if k == "once" && v == "true" {
			b.WriteString(` data-once`)
		} else {
			p.diag(line, 1, "attr_value", "Invalid value for "+k)
		}
	}
	var unknown []string
	for k := range attrs {
		if k != "class" && !known[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		p.diag(line, 1, "unknown_attr", "Unknown attribute: "+k)
	}
	return b.String()
}

func (p *pipeline) componentClass(attrs map[string]string, base string) string {
	classes := []string{base}
	for _, c := range strings.Fields(attrs["class"]) {
		if c == "btn" || c == "t" || c == "lead" || c == "center" || c == "muted" {
			classes = append(classes, "jc-"+c)
		} else {
			p.diag(p.currentLine, 1, "unknown_attr", "Unknown class: "+c)
		}
	}
	return strings.Join(classes, " ")
}
func (p *pipeline) directive(name string, a map[string]string, line int) string {
	switch name {
	case "counter":
		to := a["to"]
		if _, e := strconv.ParseInt(to, 10, 64); e != nil {
			return esc("::counter")
		}
		return `<span class="jc-counter" data-jc="counter" data-to="` + esc(to) + `">` + esc(to) + `</span>`
	case "countdown":
		return p.widgetMarkup(name, a, line)
	case "compare":
		return p.nativeCompareMarkup(a, line)
	case "embed":
		return embedMarkup(a["url"])
	case "widget":
		n := a["name"]
		if !widgetNameRE.MatchString(n) {
			p.diag(line, 1, "unknown_widget", "Unknown widget: "+n)
			return ""
		}
		params := make(map[string]string, len(a))
		for k, v := range a {
			if k != "name" {
				params[k] = v
			}
		}
		return p.widgetMarkup(n, params, line)
	}
	p.diag(line, 1, "unknown_directive", "Unknown directive: "+name)
	return ""
}

var widgetNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func (p *pipeline) widgetMarkup(name string, attrs map[string]string, line int) string {
	name = strings.ToLower(name)
	spec, ok := p.opt.Widgets[name]
	registered := ok
	if !ok {
		switch name {
		case "countdown":
			spec = WidgetSpec{Version: 1, Params: map[string]WidgetParamSpec{"to": {Type: "datetime", Required: true}}}
			ok = true
		case "compare":
			spec = WidgetSpec{Version: 1, Params: map[string]WidgetParamSpec{"before": {Type: "string", Required: true}, "after": {Type: "string", Required: true}}}
			ok = true
		case "counter":
			spec = WidgetSpec{Version: 1, Params: map[string]WidgetParamSpec{"to": {Type: "number", Required: true}}}
			ok = true
		}
	}
	if !ok {
		p.diag(line, 1, "unknown_widget", "Unknown widget: "+name)
		return ""
	}
	if name == "counter" && !registered {
		_, err := ValidateWidgetParams(spec, attrs)
		if err != nil {
			p.diag(line, 1, "widget_params", err.Error())
			return ""
		}
		to := attrs["to"]
		return `<span class="jc-counter" data-jc="counter" data-to="` + esc(to) + `">` + esc(to) + `</span>`
	}
	if name == "compare" && !registered {
		return p.nativeCompareMarkup(attrs, line)
	}
	frame, err := widgetFrameURL(name, spec, attrs)
	if err != nil {
		p.diag(line, 1, "widget_params", err.Error())
		return ""
	}
	title := name
	builtinMarker := ""
	if !registered && name == "countdown" {
		builtinMarker = ` data-builtin="countdown"`
	}
	return `<div class="jc-widget" data-jc="` + esc(name) + `" data-widget="` + esc(name) + `" data-v="` + strconv.Itoa(spec.Version) + `"` + builtinMarker + `><iframe sandbox="allow-scripts" src="` + esc(frame) + `" loading="lazy" title="` + esc(title) + `"></iframe></div>`
}

func (p *pipeline) nativeCompareMarkup(attrs map[string]string, line int) string {
	spec := WidgetSpec{Version: 1, Params: map[string]WidgetParamSpec{
		"before": {Type: "string", Required: true},
		"after":  {Type: "string", Required: true},
	}}
	values, err := ValidateWidgetParams(spec, attrs)
	if err != nil {
		p.diag(line, 1, "widget_params", err.Error())
		return ""
	}
	before, beforeOK := safeImageURL(fmt.Sprint(values["before"]))
	after, afterOK := safeImageURL(fmt.Sprint(values["after"]))
	if !beforeOK || !afterOK {
		p.diag(line, 1, "widget_params", "compare images must use HTTPS URLs or /uploads/ paths")
		return ""
	}
	return `<figure class="jc-compare" data-jc="compare" role="group" aria-label="Before and after image comparison"><div class="jc-compare-stage"><img class="jc-compare-before" src="` + esc(before) + `" alt="Before"><img class="jc-compare-after" src="` + esc(after) + `" alt="After"></div><label class="jc-compare-label">Comparison position<input class="jc-compare-range" type="range" min="0" max="100" value="50" aria-valuetext="50% showing after image"></label><figcaption><span>Before</span><span>After</span></figcaption></figure>`
}

func safeImageURL(raw string) (string, bool) {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\\\r\n\t\x00") {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" {
		return "", false
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		if u.Host == "" {
			return "", false
		}
		return u.String(), true
	case "":
		if !strings.HasPrefix(u.Path, "/uploads/") || strings.HasPrefix(raw, "//") || strings.Contains(u.Path, "\\") {
			return "", false
		}
		for _, segment := range strings.Split(u.Path, "/") {
			if segment == "." || segment == ".." {
				return "", false
			}
		}
		return u.String(), true
	default:
		return "", false
	}
}

func embedMarkup(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		if safeLinkURL(raw) {
			return `<a href="` + esc(raw) + `" rel="noopener nofollow">` + esc(raw) + `</a>`
		}
		return esc(raw)
	}
	host := strings.ToLower(u.Hostname())
	src := ""
	switch host {
	case "youtube.com", "www.youtube.com", "m.youtube.com":
		id := u.Query().Get("v")
		if id == "" && strings.HasPrefix(u.Path, "/embed/") {
			id = strings.TrimPrefix(u.Path, "/embed/")
		}
		if id != "" {
			src = "https://www.youtube-nocookie.com/embed/" + url.PathEscape(id)
		}
	case "www.youtube-nocookie.com":
		if strings.HasPrefix(u.Path, "/embed/") {
			src = raw
		}
	case "player.vimeo.com":
		if strings.HasPrefix(u.Path, "/video/") {
			src = raw
		}
	case "player.bilibili.com":
		if strings.HasPrefix(u.Path, "/player.html") {
			src = raw
		}
	}
	if src == "" {
		return `<a href="` + esc(raw) + `" rel="noopener nofollow">` + esc(raw) + `</a>`
	}
	return `<figure class="jc-embed"><iframe src="` + esc(src) + `" loading="lazy" sandbox="allow-scripts allow-same-origin allow-presentation" referrerpolicy="no-referrer" allowfullscreen></iframe></figure>`
}

func safeLinkURL(raw string) bool {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\r\n\t\\") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u.Host != ""
	case "mailto":
		return u.Opaque != "" && !strings.ContainsAny(u.Opaque, "<>\"'")
	case "":
		return !strings.HasPrefix(raw, "//") && !strings.HasPrefix(u.Path, "//")
	default:
		return false
	}
}
