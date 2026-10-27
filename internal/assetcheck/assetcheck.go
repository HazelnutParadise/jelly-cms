// Package assetcheck finds references that make a page load a script, style, font, image or frame from
// an external origin (ticket 12). It only reports real resource loads: <a href>, text inside <textarea>
// or <code>, and string literals that merely contain a URL are not resource loads and never count.
// The scanner is used by the zero-CDN test in this package; it needs no database.
package assetcheck

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// Finding is one external resource reference.
type Finding struct {
	Line int    // 1-based line of the tag, declaration or statement
	Kind string // what loads it, for example "script src" or "css url()"
	URL  string // the reference as written, trimmed
}

// IsExternal reports whether ref points at another origin: it starts with http:, https: or // (any case,
// leading whitespace ignored).
func IsExternal(ref string) bool {
	ref = strings.ToLower(strings.TrimSpace(ref))
	return strings.HasPrefix(ref, "http:") || strings.HasPrefix(ref, "https:") || strings.HasPrefix(ref, "//")
}

// linkRelsThatDoNotLoad lists <link rel> values that name a relationship and never fetch the target.
var linkRelsThatDoNotLoad = map[string]bool{
	"canonical": true, "alternate": true, "next": true, "prev": true, "author": true, "license": true,
	"me": true, "help": true, "search": true, "bookmark": true, "nofollow": true, "noopener": true,
	"noreferrer": true, "webmention": true, "pingback": true, "shortlink": true,
}

// ScanHTML reports every external resource load in an HTML document or Go template: script, link, img,
// iframe, source, video, audio, embed, object and input[type=image] references, srcset lists, <style>
// blocks and style attributes (scanned as CSS).
func ScanHTML(src string) []Finding {
	var out []Finding
	z := html.NewTokenizer(strings.NewReader(src))
	line := 1
	inStyle := false
	inScript := false
	for {
		tt := z.Next()
		raw := z.Raw()
		startLine := line
		line += strings.Count(string(raw), "\n")
		if tt == html.ErrorToken {
			// End of input, or a malformed tail: keep what was found before it.
			return out
		}
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			tag := string(name)
			attrs := map[string]string{}
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				attrs[strings.ToLower(string(k))] = string(v)
			}
			if tag == "style" && tt == html.StartTagToken {
				inStyle = true
			}
			if tag == "script" && tt == html.StartTagToken {
				inScript = true
			}
			out = append(out, scanTag(tag, attrs, startLine)...)
		case html.EndTagToken:
			if name, _ := z.TagName(); string(name) == "style" {
				inStyle = false
			} else if string(name) == "script" {
				inScript = false
			}
		case html.TextToken:
			if inStyle {
				for _, f := range ScanCSS(string(raw)) {
					f.Line += startLine - 1
					out = append(out, f)
				}
			}
			if inScript {
				for _, f := range ScanJS(string(raw)) {
					f.Line += startLine - 1
					out = append(out, f)
				}
			}
		}
	}
}

func scanTag(tag string, attrs map[string]string, line int) []Finding {
	var out []Finding
	add := func(kind, ref string) {
		if IsExternal(ref) {
			out = append(out, Finding{Line: line, Kind: kind, URL: strings.TrimSpace(ref)})
		}
	}
	switch tag {
	case "script":
		add("script src", attrs["src"])
	case "link":
		if !linkLoads(attrs["rel"]) {
			break
		}
		add("link href", attrs["href"])
		for _, ref := range srcsetURLs(attrs["imagesrcset"]) {
			add("link imagesrcset", ref)
		}
	case "img":
		add("img src", attrs["src"])
		for _, ref := range srcsetURLs(attrs["srcset"]) {
			add("img srcset", ref)
		}
	case "source":
		add("source src", attrs["src"])
		for _, ref := range srcsetURLs(attrs["srcset"]) {
			add("source srcset", ref)
		}
	case "iframe", "video", "audio", "embed", "track":
		add(tag+" src", attrs["src"])
		if tag == "video" {
			add("video poster", attrs["poster"])
		}
	case "object":
		add("object data", attrs["data"])
	case "input":
		if strings.EqualFold(attrs["type"], "image") {
			add("input src", attrs["src"])
		}
	}
	if style, ok := attrs["style"]; ok {
		for _, f := range ScanCSS(style) {
			f.Line = line
			f.Kind = "style attribute " + f.Kind
			out = append(out, f)
		}
	}
	return out
}

// linkLoads reports whether a <link rel> value names a relationship that fetches its target. An empty rel
// counts as loading, so a link cannot dodge the check by omitting it.
func linkLoads(rel string) bool {
	tokens := strings.Fields(strings.ToLower(rel))
	if len(tokens) == 0 {
		return true
	}
	for _, token := range tokens {
		if !linkRelsThatDoNotLoad[token] {
			return true
		}
	}
	return false
}

// srcsetURLs returns the URL of every candidate in a srcset list.
func srcsetURLs(srcset string) []string {
	var out []string
	for _, candidate := range strings.Split(srcset, ",") {
		if fields := strings.Fields(candidate); len(fields) > 0 {
			out = append(out, fields[0])
		}
	}
	return out
}

var (
	cssComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	cssURL     = regexp.MustCompile(`(?i)url\(\s*(?:"([^"]*)"|'([^']*)'|([^)\s"']*))\s*\)`)
	cssImport  = regexp.MustCompile(`(?i)@import\s+(?:"([^"]*)"|'([^']*)')`)
)

// ScanCSS reports every external @import and url(...) in a stylesheet. Comments are ignored.
func ScanCSS(src string) []Finding {
	// Blank out comments but keep their newlines so line numbers stay right.
	src = cssComment.ReplaceAllStringFunc(src, func(c string) string {
		return strings.Repeat("\n", strings.Count(c, "\n"))
	})
	var out []Finding
	lineAt := func(offset int) int { return 1 + strings.Count(src[:offset], "\n") }
	for _, m := range cssURL.FindAllStringSubmatchIndex(src, -1) {
		ref := firstGroup(src, m)
		if IsExternal(ref) {
			out = append(out, Finding{Line: lineAt(m[0]), Kind: "css url()", URL: strings.TrimSpace(ref)})
		}
	}
	for _, m := range cssImport.FindAllStringSubmatchIndex(src, -1) {
		ref := firstGroup(src, m)
		if IsExternal(ref) {
			out = append(out, Finding{Line: lineAt(m[0]), Kind: "css @import", URL: strings.TrimSpace(ref)})
		}
	}
	return out
}

func firstGroup(src string, m []int) string {
	for i := 2; i+1 < len(m); i += 2 {
		if m[i] >= 0 {
			return src[m[i]:m[i+1]]
		}
	}
	return ""
}

var jsLoaders = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"js import", regexp.MustCompile(`\bimport\s*(?:[\w*${},\s]+\s+from\s*)?\(?\s*["']((?:(?i:https?):)?//[^"']*)["']`)},
	{"js export from", regexp.MustCompile(`\bexport\s*(?:\*|\{[^}]*\})\s*(?:as\s+\w+\s*)?from\s*["']((?:(?i:https?):)?//[^"']*)["']`)},
	{"js importScripts", regexp.MustCompile(`\bimportScripts\(\s*["']((?:(?i:https?):)?//[^"']*)["']`)},
	{"js .src =", regexp.MustCompile(`\.src\s*=\s*["']((?:(?i:https?):)?//[^"']*)["']`)},
	{"js new Worker", regexp.MustCompile(`\bnew\s+(?:Shared)?Worker\(\s*["']((?:(?i:https?):)?//[^"']*)["']`)},
}

// ScanJS reports external module imports, importScripts, Worker constructors and assignments to an
// element's src. A string that merely contains a URL (error messages, documentation links) is not
// reported.
func ScanJS(src string) []Finding {
	var out []Finding
	for _, loader := range jsLoaders {
		for _, m := range loader.re.FindAllStringSubmatchIndex(src, -1) {
			out = append(out, Finding{Line: 1 + strings.Count(src[:m[0]], "\n"), Kind: loader.kind, URL: src[m[2]:m[3]]})
		}
	}
	return out
}

// ScanFile picks the scanner by file name: .html and .tmpl as HTML, .css as CSS, .js and .mjs as
// JavaScript. Any other name returns nil.
func ScanFile(name string, src []byte) []Finding {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".html"), strings.HasSuffix(lower, ".tmpl"):
		return ScanHTML(string(src))
	case strings.HasSuffix(lower, ".css"):
		return ScanCSS(string(src))
	case strings.HasSuffix(lower, ".js"), strings.HasSuffix(lower, ".mjs"):
		return ScanJS(string(src))
	}
	return nil
}
