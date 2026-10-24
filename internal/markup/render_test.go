package markup

// Render contract tests (docs/tickets/09-editor.md, 語法表, 錯誤與邊界). They are written before the
// renderer exists and are expected to fail against the skeleton. Do not edit them to match an
// implementation.
//
// Golden convention. Each testdata/render/<name>.jmd is rendered with Options{Trust: "rich",
// Format: "jmd"} and compared with <name>.html after normalisation: whitespace between tags is
// removed and every other run of whitespace becomes one space. A golden may contain the marker
// <!-- any --> which matches any text, lazily. Use it only where the spec leaves the inner markup
// open (KaTeX, chroma, footnote bodies, link-card internals). The wrapper elements around the marker
// are still compared exactly.

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	xhtml "golang.org/x/net/html"
)

// mtGoldenNames is the explicit list of golden pairs. The list is not globbed, so a missing .html
// fails the test instead of silently shrinking the set. TestRenderGoldenFilesComplete checks that
// the directory holds exactly these pairs.
var mtGoldenNames = []string{
	"heading-attrs",
	"task-list",
	"footnote",
	"callout",
	"more",
	"table-yaml",
	"math-inline-block",
	"latex-fragment",
	"html-rich",
	"code-chroma",
	"hero",
	"columns-nested",
	"faq",
	"tabs",
	"counter",
	"embed-allowed",
	"embed-other",
	"widget-unknown",
	"products-dyn",
	"heading-id-cjk-and-dup",
	"attrs-unknown-dropped",
	"not-math",
}

const mtAnyMarker = "<!-- any -->"

var (
	mtTagGap   = regexp.MustCompile(`>\s+<`)
	mtSpaceRun = regexp.MustCompile(`\s+`)
)

func mtReadFile(t testing.TB, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// mtNormalize applies the golden normalisation to one side of a comparison.
func mtNormalize(s string) string {
	s = mtTagGap.ReplaceAllString(s, "><")
	s = mtSpaceRun.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// mtGoldenMatches reports whether actual matches golden, where golden may hold mtAnyMarker.
func mtGoldenMatches(golden, actual string) bool {
	parts := strings.Split(mtNormalize(golden), mtAnyMarker)
	quoted := make([]string, len(parts))
	for i, part := range parts {
		quoted[i] = regexp.QuoteMeta(part)
	}
	pattern := "^" + strings.Join(quoted, "(?s:.*?)") + "$"
	return regexp.MustCompile(pattern).MatchString(mtNormalize(actual))
}

func mtRender(t *testing.T, source, trust string) Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r, err := Render(ctx, source, Options{Trust: trust, Format: "jmd"})
	if err != nil {
		t.Fatalf("Render(trust=%s): %v", trust, err)
	}
	return r
}

func mtFindBlock(blocks []Block, name string) (Block, bool) {
	for _, b := range blocks {
		if b.Name == name {
			return b, true
		}
	}
	return Block{}, false
}

func mtAssertSafe(t *testing.T, trust, out string) {
	t.Helper()
	if reason, err := mtUnsafeHTMLReason(out); err != nil {
		t.Errorf("trust=%s: inspect rendered HTML: %v", trust, err)
	} else if reason != "" {
		t.Errorf("trust=%s: unsafe rendered HTML (%s): %q", trust, reason, out)
	}
}

var mtURLAttrs = map[string]struct{}{
	"action": {}, "background": {}, "cite": {}, "codebase": {}, "data": {},
	"formaction": {}, "href": {}, "longdesc": {}, "manifest": {}, "poster": {},
	"profile": {}, "src": {}, "usemap": {}, "xlink:href": {},
}

// mtUnsafeHTMLReason classifies actual HTML tags and attributes. Attribute values are already
// entity-decoded by x/net/html, so only the browser-relevant ASCII tab/newline/carriage-return
// obfuscation is removed inside URL values. Other internal whitespace and percent escapes stay
// intact because they do not form a javascript: scheme.
func mtUnsafeHTMLReason(out string) (string, error) {
	tokenizer := xhtml.NewTokenizer(strings.NewReader(out))
	for {
		tokenType := tokenizer.Next()
		if tokenType == xhtml.ErrorToken {
			if err := tokenizer.Err(); err != io.EOF {
				return "", err
			}
			return "", nil
		}
		if tokenType != xhtml.StartTagToken && tokenType != xhtml.SelfClosingTagToken {
			continue
		}
		tagName, hasAttr := tokenizer.TagName()
		if strings.EqualFold(string(tagName), "script") {
			return "script element", nil
		}
		for hasAttr {
			key, value, moreAttr := tokenizer.TagAttr()
			name := strings.ToLower(string(key))
			if len(name) > 2 && strings.HasPrefix(name, "on") {
				return "event attribute " + name, nil
			}
			if _, isURL := mtURLAttrs[name]; isURL && mtHasJavaScriptScheme(string(value)) {
				return "javascript URL in " + name, nil
			}
			hasAttr = moreAttr
		}
	}
}

func mtHasJavaScriptScheme(value string) bool {
	value = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, value)
	start, end := 0, len(value)
	for start < end && mtASCIIControlOrSpace(value[start]) {
		start++
	}
	for end > start && mtASCIIControlOrSpace(value[end-1]) {
		end--
	}
	return strings.HasPrefix(strings.ToLower(value[start:end]), "javascript:")
}

func mtASCIIControlOrSpace(b byte) bool { return b <= 0x20 || b == 0x7f }

func TestMarkupSafetyOracleClassifiesHTMLTokens(t *testing.T) {
	tests := []struct {
		name string
		html string
		want bool
	}{
		{name: "script start tag", html: `<script>alert(1)</script>`, want: true},
		{name: "self-closing script tag", html: `<script />`, want: true},
		{name: "quoted event attribute", html: `<img onerror="alert(1)">`, want: true},
		{name: "unquoted event attribute", html: `<svg onload=alert(1)>`, want: true},
		{name: "case-insensitive javascript URL", html: `<a href="JaVaScRiPt:alert(1)">x</a>`, want: true},
		{name: "entity-decoded javascript URL", html: `<a href="jav&#x61;script:alert(1)">x</a>`, want: true},
		{name: "tab-obfuscated javascript URL", html: `<a href="java&#x09;script:alert(1)">x</a>`, want: true},
		{name: "newline-obfuscated javascript URL", html: "<a href=\"java&#x0a;script:alert(1)\">x</a>", want: true},
		{name: "ASCII controls around javascript URL", html: `<a href="&#x01; javascript:alert(1)&#x7f;">x</a>`, want: true},
		{name: "quoted class containing event-like text", html: `<pre class="chroma"><code class="language-0 onA=">code</code></pre>`, want: false},
		{name: "quoted title containing javascript text", html: `<span title="javascript: is only text">safe</span>`, want: false},
		{name: "escaped script and attributes in code", html: `<pre><code>&lt;script&gt; onerror=alert(1) javascript:alert(1)</code></pre>`, want: false},
		{name: "ordinary URL path containing javascript text", html: `<a href="https://example.test/docs/javascript:guide">safe</a>`, want: false},
		{name: "internal ordinary space is preserved", html: `<a href="java script:alert(1)">safe</a>`, want: false},
		{name: "percent-encoded scheme is not decoded", html: `<a href="java%73cript:alert(1)">safe</a>`, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reason, err := mtUnsafeHTMLReason(tc.html)
			if err != nil {
				t.Fatalf("mtUnsafeHTMLReason: %v", err)
			}
			if got := reason != ""; got != tc.want {
				t.Errorf("unsafe=%t, want %t (reason=%q)", got, tc.want, reason)
			}
		})
	}
}

func TestRenderGoldenFilesComplete(t *testing.T) {
	jmd, err := filepath.Glob(filepath.Join("testdata", "render", "*.jmd"))
	if err != nil {
		t.Fatalf("glob golden sources: %v", err)
	}
	found := make(map[string]bool, len(jmd))
	for _, path := range jmd {
		found[strings.TrimSuffix(filepath.Base(path), ".jmd")] = true
	}
	listed := make(map[string]bool, len(mtGoldenNames))
	for _, name := range mtGoldenNames {
		listed[name] = true
		if !found[name] {
			t.Errorf("golden %s.jmd is listed but missing", name)
		}
		if _, err := os.Stat(filepath.Join("testdata", "render", name+".html")); err != nil {
			t.Errorf("golden %s.html is missing: %v", name, err)
		}
	}
	for name := range found {
		if !listed[name] {
			t.Errorf("golden %s.jmd exists but is not listed in mtGoldenNames", name)
		}
	}
}

// mtGoldenExtra adds assertions that a golden cannot express: the presence or absence of a
// token, or a diagnostic. Clean goldens must render with no error diagnostic.
var mtGoldenExtra = map[string]func(t *testing.T, r Result){
	"code-chroma": func(t *testing.T, r Result) {
		if !strings.Contains(r.HTML, `<pre class="chroma">`) || !strings.Contains(r.HTML, `class="language-go"`) {
			t.Errorf("code block must carry <pre class=\"chroma\"> and class=\"language-go\": %s", r.HTML)
		}
	},
	"embed-other": func(t *testing.T, r Result) {
		if strings.Contains(r.HTML, "<iframe") {
			t.Errorf("a non-allow-listed embed must become a link card, not an iframe: %s", r.HTML)
		}
	},
	"widget-unknown": func(t *testing.T, r Result) {
		if len(r.Diagnostics) == 0 {
			t.Errorf("an unknown widget must produce a diagnostic")
		}
		if strings.Contains(r.HTML, "<iframe") {
			t.Errorf("an unknown widget must not render an iframe: %s", r.HTML)
		}
	},
	"products-dyn": func(t *testing.T, r Result) {
		if !strings.Contains(r.HTML, "gift") {
			t.Errorf("products placeholder must carry the category in data-args: %s", r.HTML)
		}
	},
	"attrs-unknown-dropped": func(t *testing.T, r Result) {
		if len(r.Diagnostics) == 0 {
			t.Errorf("an unknown attribute value must produce a diagnostic")
		}
		if strings.Contains(r.HTML, "data-bg") {
			t.Errorf("an unknown bg value must be dropped: %s", r.HTML)
		}
	},
	"not-math": func(t *testing.T, r Result) {
		if strings.Contains(r.HTML, "jc-math") {
			t.Errorf("\"$5 and $10\" must not open math: %s", r.HTML)
		}
	},
	"heading-id-cjk-and-dup": func(t *testing.T, r Result) {
		want := []string{"中文標題", "中文標題-2", "中文標題-3"}
		if len(r.TOC) != len(want) {
			t.Fatalf("TOC has %d entries, want %d", len(r.TOC), len(want))
		}
		for i, id := range want {
			if r.TOC[i].ID != id {
				t.Errorf("TOC[%d].ID = %q, want %q", i, r.TOC[i].ID, id)
			}
		}
	},
}

// mtGoldenWithDiagnostics lists goldens that are allowed to carry diagnostics.
var mtGoldenWithDiagnostics = map[string]bool{
	"widget-unknown":        true,
	"attrs-unknown-dropped": true,
}

func TestRenderGolden(t *testing.T) {
	for _, name := range mtGoldenNames {
		t.Run(name, func(t *testing.T) {
			source := mtReadFile(t, filepath.Join("testdata", "render", name+".jmd"))
			want := mtReadFile(t, filepath.Join("testdata", "render", name+".html"))
			r := mtRender(t, source, TrustRich)
			if !mtGoldenMatches(want, r.HTML) {
				t.Errorf("render of %s.jmd does not match %s.html\n got: %s\nwant: %s",
					name, name, mtNormalize(r.HTML), mtNormalize(want))
			}
			if !mtGoldenWithDiagnostics[name] {
				for _, d := range r.Diagnostics {
					if d.Severity == "error" {
						t.Errorf("clean golden %s produced an error diagnostic: %+v", name, d)
					}
				}
			}
			if extra, ok := mtGoldenExtra[name]; ok {
				extra(t, r)
			}
		})
	}
}

func TestRenderStrictPolicy(t *testing.T) {
	source := mtReadFile(t, filepath.Join("testdata", "render", "html-rich.jmd"))
	r := mtRender(t, source, TrustStrict)
	if !strings.Contains(r.HTML, `<pre><code class="language-html">`) {
		t.Errorf("strict policy must render an html fence as code: %s", r.HTML)
	}
	if strings.Contains(r.HTML, `class="jc-html"`) {
		t.Errorf("strict policy must not emit the rich jc-html wrapper: %s", r.HTML)
	}
	if len(r.Diagnostics) == 0 {
		t.Errorf("strict policy must emit a diagnostic for an html fence")
	}
}

func TestRenderLimits(t *testing.T) {
	ctx := context.Background()
	opt := Options{Trust: TrustRich, Format: "jmd"}

	t.Run("source over 2 MB is an error", func(t *testing.T) {
		source := strings.Repeat("a", 2*1024*1024+1)
		if _, err := Render(ctx, source, opt); err == nil {
			t.Fatalf("a 2 MB + 1 byte source must return an error")
		}
	})

	t.Run("container depth 9 gives a diagnostic and no panic", func(t *testing.T) {
		var b strings.Builder
		for i := 0; i < 9; i++ {
			b.WriteString(":::section\n")
		}
		b.WriteString("內容\n")
		for i := 0; i < 9; i++ {
			b.WriteString(":::\n")
		}
		r, err := Render(ctx, b.String(), opt)
		if err != nil {
			t.Fatalf("depth 9 must not be a hard error: %v", err)
		}
		if len(r.Diagnostics) == 0 {
			t.Errorf("depth 9 must produce a diagnostic")
		}
	})

	t.Run("10k dollar signs finish within 2 s", func(t *testing.T) {
		source := strings.Repeat("$", 10000)
		start := time.Now()
		if _, err := Render(ctx, source, opt); err != nil {
			t.Fatalf("10k dollar signs must render as text: %v", err)
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Errorf("10k dollar signs took %s, want under 2 s", elapsed)
		}
	})

	t.Run("anchor-heavy yaml table gives a diagnostic", func(t *testing.T) {
		source := "```table\n" +
			"a: &a [x, x, x, x, x, x, x, x, x, x]\n" +
			"b: &b [*a, *a, *a, *a, *a, *a, *a, *a, *a, *a]\n" +
			"caption: *b\n" +
			"```\n"
		r, err := Render(ctx, source, opt)
		if err != nil {
			t.Fatalf("a bad yaml table must not be a hard error: %v", err)
		}
		if len(r.Diagnostics) == 0 {
			t.Errorf("an anchor-heavy yaml table must produce a diagnostic")
		}
	})
}

// mtStatsFixture holds 800 CJK characters, three images, two links, two open tasks and one TODO.
// Chars counts CJK runes only and Minutes is ceil(CJK / 400), so the ASCII text here does not
// change the expected numbers.
func mtStatsFixture() string {
	return strings.Repeat("中", 800) + "\n\n" +
		"![](/uploads/a.png)\n\n![](/uploads/b.png)\n\n![](/uploads/c.png)\n\n" +
		"[x](https://a.test) and [y](https://b.test)\n\n" +
		"- [ ] one\n- [ ] two\n\n" +
		"TODO\n"
}

func TestRenderStats(t *testing.T) {
	r := mtRender(t, mtStatsFixture(), TrustRich)
	s := r.Stats
	if s.Chars != 800 {
		t.Errorf("Chars = %d, want 800", s.Chars)
	}
	if s.Images != 3 {
		t.Errorf("Images = %d, want 3", s.Images)
	}
	if s.Links != 2 {
		t.Errorf("Links = %d, want 2", s.Links)
	}
	if s.OpenTasks != 2 {
		t.Errorf("OpenTasks = %d, want 2", s.OpenTasks)
	}
	if s.Todos != 1 {
		t.Errorf("Todos = %d, want 1", s.Todos)
	}
	if s.Minutes != 2 {
		t.Errorf("Minutes = %d, want 2", s.Minutes)
	}
}

// mtLayoutDoc has a hero on lines 1 to 3, a blank line, a columns block on lines 5 to 12, and a
// tabs block that uses math elsewhere in the document.
const mtLayoutDoc = `:::hero {bg=mint}
歡迎
:::

:::columns
::: col
左
:::
::: col
右
:::
:::

數學 $x^2$

:::tabs
::: tab {title="甲"}
內容
:::
:::
`

func TestRenderBlocksAndFeatures(t *testing.T) {
	r := mtRender(t, mtLayoutDoc, TrustRich)

	hero, ok := mtFindBlock(r.Blocks, "hero")
	if !ok {
		t.Fatalf("no hero block in %+v", r.Blocks)
	}
	if hero.StartLine != 1 || hero.EndLine != 3 {
		t.Errorf("hero lines = %d-%d, want 1-3", hero.StartLine, hero.EndLine)
	}
	if hero.Attrs["bg"] != "mint" {
		t.Errorf("hero attrs bg = %q, want mint", hero.Attrs["bg"])
	}

	columns, ok := mtFindBlock(r.Blocks, "columns")
	if !ok {
		t.Fatalf("no columns block in %+v", r.Blocks)
	}
	if columns.StartLine != 5 || columns.EndLine != 12 {
		t.Errorf("columns lines = %d-%d, want 5-12", columns.StartLine, columns.EndLine)
	}

	for _, want := range []string{"math", "tabs"} {
		if !mtContains(r.Features, want) {
			t.Errorf("Result.Features = %v, want it to contain %q", r.Features, want)
		}
		if !mtContains(Features(r.HTML), want) {
			t.Errorf("Features(HTML) = %v, want it to contain %q", Features(r.HTML), want)
		}
	}
}

func mtContains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func TestHeadingIDsIdempotent(t *testing.T) {
	source := "## 自訂{#custom}\n\n## 自訂\n"
	first := mtRender(t, source, TrustRich)
	second := mtRender(t, source, TrustRich)

	if first.HTML != second.HTML {
		t.Errorf("rendering twice changed the output:\n first: %s\nsecond: %s", first.HTML, second.HTML)
	}
	if !strings.Contains(first.HTML, `<h2 id="custom">`) {
		t.Errorf("an explicit {#custom} id must be kept: %s", first.HTML)
	}
	if len(first.TOC) != 2 || first.TOC[0].ID != "custom" || first.TOC[1].ID != "自訂" {
		t.Errorf("TOC ids = %+v, want custom then 自訂", first.TOC)
	}
}

func FuzzRender(f *testing.F) {
	paths, err := filepath.Glob(filepath.Join("testdata", "render", "*.jmd"))
	if err != nil {
		f.Fatalf("glob golden sources: %v", err)
	}
	for _, path := range paths {
		f.Add(mtReadFile(f, path))
	}
	f.Add("<img src=x onerror=alert(1)>")
	f.Add("[x](javascript:alert(1))")
	f.Add(":::hero {bg=mint}\n```html\n<script>x</script>\n```\n:::")

	f.Fuzz(func(t *testing.T, source string) {
		for _, trust := range []string{TrustStrict, TrustRich} {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			r, err := Render(ctx, source, Options{Trust: trust, Format: "jmd"})
			cancel()
			if err != nil {
				continue
			}
			mtAssertSafe(t, trust, r.HTML)
		}
	})
}
