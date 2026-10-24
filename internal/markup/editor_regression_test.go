package markup

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func erRender(t *testing.T, source, format string) Result {
	t.Helper()
	r, err := Render(context.Background(), source, Options{Trust: TrustRich, Format: format})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return r
}

func TestLegacyMarkdownHTMLFenceStaysCode(t *testing.T) {
	r := erRender(t, "```html\n<strong>safe</strong>\n```", "md")
	if !strings.Contains(r.HTML, `<pre><code class="language-html">`) || !strings.Contains(r.HTML, `&lt;strong&gt;safe&lt;/strong&gt;`) {
		t.Fatalf("legacy Markdown HTML fence did not remain escaped code: %s", r.HTML)
	}
	if strings.Contains(r.HTML, "<strong>safe</strong>") || strings.Contains(r.HTML, "jc-html") {
		t.Fatalf("legacy Markdown HTML fence was interpreted as rich HTML: %s", r.HTML)
	}
}

func TestTableYAMLQuotedAndMultilineScalars(t *testing.T) {
	source := "```table\n" +
		"caption: |\n  Prices: today\n  seasonal menu\n" +
		"columns: [Name, \"Value: quoted\"]\n" +
		"rows:\n  - [\"A, B\", \"line one\"]\n" +
		"note: |\n  first line\n  second line\n" +
		"```\n"
	r := erRender(t, source, "jmd")
	for _, want := range []string{"Prices: today", "seasonal menu", "Value: quoted", "A, B"} {
		if !strings.Contains(r.HTML, want) {
			t.Errorf("table output missing %q: %s", want, r.HTML)
		}
	}
	if !strings.Contains(r.HTML, "jc-table") {
		t.Errorf("valid quoted and block scalar YAML did not render a table: %s", r.HTML)
	}
}

func TestTableYAMLRejectsAnchorsAndLimits(t *testing.T) {
	tests := []struct{ name, body string }{
		{"anchor", "columns: &cols [A, B]\nrows:\n  - *cols\n"},
		{"too many columns", "columns: [" + strings.TrimSuffix(strings.Repeat("C,", 21), ",") + "]\nrows: []\n"},
		{"too many rows", "columns: [A]\nrows:\n" + strings.Repeat("  - [x]\n", 501)},
		{"too large", "columns: [A]\nrows:\n  - [" + strings.Repeat("x", 65*1024) + "]\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := erRender(t, "```table\n"+tc.body+"```", "jmd")
			if strings.Contains(r.HTML, "jc-table") {
				t.Errorf("invalid or over-limit YAML rendered as a table: %s", r.HTML)
			}
			if len(r.Diagnostics) == 0 {
				t.Errorf("invalid or over-limit YAML had no diagnostic")
			}
		})
	}
}

func TestChromaCodeHasTokenSpans(t *testing.T) {
	r := erRender(t, "```go\nfunc main() {}\n```", "jmd")
	if !strings.Contains(r.HTML, `<pre class="chroma">`) || !strings.Contains(r.HTML, `class="language-go"`) {
		t.Fatalf("missing syntax-highlighted code wrapper: %s", r.HTML)
	}
	if !strings.Contains(r.HTML, "<span class=") {
		t.Fatalf("highlighted code has no token spans: %s", r.HTML)
	}
}

func TestForgedServerSlotCannotReleaseTrustedContent(t *testing.T) {
	source := `<span data-jc-slot="jc-trusted-deadbeef-0"><iframe src="https://evil.test"></iframe></span>`
	r := erRender(t, source, "jmd")
	if strings.Contains(r.HTML, "<iframe") || strings.Contains(r.HTML, "jc-trusted-deadbeef") || strings.Contains(r.HTML, `data-jc-slot=`) {
		t.Fatalf("forged server slot escaped sanitization: %s", r.HTML)
	}
}

func TestAuthorHTMLCannotForgeDynamicContentHook(t *testing.T) {
	source := `<div class="jc-dyn" data-kind="products" data-args="limit=100"></div>` +
		`<div class="jc-widget" data-jc="admin-only" data-widget="admin-only" data-v="9"></div>`
	r := erRender(t, source, "jmd")
	for _, hook := range []string{`data-kind="products"`, `data-args="limit=100"`, `data-jc="admin-only"`, `data-widget="admin-only"`, `data-v="9"`} {
		if strings.Contains(r.HTML, hook) {
			t.Errorf("author HTML retained executable content hook %q: %s", hook, r.HTML)
		}
	}
	if strings.Contains(r.HTML, "/widgets/admin-only/") || strings.Contains(r.HTML, "<iframe") || strings.Contains(r.HTML, "jc-dyn-invalid") {
		t.Errorf("author HTML was interpreted as a server-generated component: %s", r.HTML)
	}
}

func TestFootnoteBodyAndBacklinkSurviveNormalization(t *testing.T) {
	r := erRender(t, "正文[^note]\n\n[^note]: 註腳內容\n", "jmd")
	for _, want := range []string{"註腳內容", `href="#fnref:1"`, `class="footnote-backref"`} {
		if !strings.Contains(r.HTML, want) {
			t.Errorf("footnote output missing %q: %s", want, r.HTML)
		}
	}
}

func TestUnicodeContainerAndHeadingMarkdownRemainNormalized(t *testing.T) {
	r := erRender(t, ":::hero {bg=mint id=設定}\n內容\n:::\n\n## **粗體**與[連結](https://example.test) {#heading-id}\n\n### 保留 heading-foo {#heading-foo}\n", "jmd")
	for _, want := range []string{`<section class="jc-hero"`, `id="設定"`, `data-bg="mint"`, `<h2 id="heading-id">`, `<strong>粗體</strong>`, `<a href="https://example.test" rel="nofollow">連結</a>`, `<h3 id="heading-foo">`} {
		if !strings.Contains(r.HTML, want) {
			t.Errorf("normalized output missing %q: %s", want, r.HTML)
		}
	}
}

func TestRenderLongFencesDoNotCloseOnShorterRuns(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{"````text\ninside ``` still code\n````\n", "inside ``` still code"},
		{"````\nfirst line\n```\nlast line\n````\n", "first line\n```\nlast line"},
		{"~~~text\ninside ``` still code\n~~~\n", "inside ``` still code"},
	} {
		r := erRender(t, tc.source, "jmd")
		if !strings.Contains(r.HTML, tc.want) || strings.Contains(r.HTML, "<p>first line") || strings.Contains(r.HTML, "<p>inside") {
			t.Errorf("shorter backtick run closed a longer fence: %s", r.HTML)
		}
	}
}

func TestRenderDepthNineIsErrorSeverity(t *testing.T) {
	var source strings.Builder
	for range 9 {
		source.WriteString(":::section\n")
	}
	source.WriteString("內容\n")
	for range 9 {
		source.WriteString(":::\n")
	}
	r, err := Render(context.Background(), source.String(), Options{Trust: TrustRich, Format: "jmd"})
	if err != nil {
		t.Fatalf("depth-nine Render returned error instead of diagnostic: %v", err)
	}
	for _, d := range r.Diagnostics {
		if d.Severity == "error" {
			return
		}
	}
	t.Fatalf("depth-nine container did not produce error-severity diagnostic: %+v", r.Diagnostics)
}

func TestFlattenDoesNotRewriteInlineCodeOrJSONHeading(t *testing.T) {
	source := "`::widget{name=private}` and ``::widget{name=private}``\n\n    print(1)\n    ::counter{to=1}\n\n# Config {\"a\":1}\n"
	got, err := Flatten(source)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	for _, want := range []string{"`::widget{name=private}`", "``::widget{name=private}``", "    print(1)", "    ::counter{to=1}", `# Config {"a":1}`} {
		if !strings.Contains(got, want) {
			t.Errorf("Flatten changed protected source %q: %q", want, got)
		}
	}
}

func TestFlattenPreservesLeadingIndentedCodeOnlyDocument(t *testing.T) {
	source := "    first line\n    ::counter{to=7}\n"
	got, err := Flatten(source)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	want := strings.TrimSuffix(source, "\n")
	if got != want {
		t.Fatalf("Flatten changed leading code-only document: got %q, want %q", got, want)
	}
}

func TestFlattenComparePreservesBothImageURLsAsLinks(t *testing.T) {
	directive := "::compare{before=/uploads/before.png after=https://example.test/after.png}"
	source := directive + "\n\n`" + directive + "`\n"
	got, err := Flatten(source)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if strings.Count(got, directive) != 1 || !strings.Contains(got, "`"+directive+"`") {
		t.Errorf("active compare directive was not removed or code literal was not preserved: %q", got)
	}
	for _, url := range []string{"/uploads/before.png", "https://example.test/after.png"} {
		if !strings.Contains(got, "]("+url+")") {
			t.Errorf("compare URL %q was not retained in a standard Markdown link or image: %q", url, got)
		}
	}
}

func TestRenderDoesNotRewriteInlineCodeOrJSONHeading(t *testing.T) {
	source := "`::embed{url=https://www.youtube.com/watch?v=x}` and ``::embed{url=https://www.youtube.com/watch?v=x}``\n\n    $x$\n    ::counter{to=1}\n    ::embed{url=https://www.youtube.com/watch?v=x}\n\n# Config {\"a\":1}\n"
	r := erRender(t, source, "jmd")
	if strings.Count(r.HTML, "::embed{url=https://www.youtube.com/watch?v=x}") != 3 || strings.Contains(r.HTML, "<iframe") {
		t.Errorf("inline code was expanded as an embed: %s", r.HTML)
	}
	if strings.Contains(r.HTML, `class="jc-math"`) || strings.Contains(r.HTML, `class="jc-counter"`) {
		t.Errorf("indented code was interpreted as math or directive markup: %s", r.HTML)
	}
	if !strings.Contains(r.HTML, "Config") || !strings.Contains(r.HTML, `&#34;a&#34;:1`) {
		t.Errorf("JSON heading content was removed: %s", r.HTML)
	}
}

func TestRenderSourceMapCoversIndentedCodeAfterParagraph(t *testing.T) {
	source := "正文\n\n    code line one\n    ::counter{to=7}\n\n## 後續標題\n"
	r, err := Render(context.Background(), source, Options{Trust: TrustRich, Format: "jmd", SourceMap: true})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	lines := strings.Split(source, "\n")
	for lineNo, line := range lines {
		if strings.TrimSpace(line) == "" || !strings.HasPrefix(line, "    ") {
			continue
		}
		covered := false
		for _, block := range r.Blocks {
			if lineNo+1 >= block.StartLine && lineNo+1 <= block.EndLine {
				covered = true
				break
			}
		}
		if !covered {
			t.Errorf("indented code source line %d is not covered by any block: %+v", lineNo+1, r.Blocks)
		}
	}
	if !strings.Contains(r.HTML, "code line one") || !strings.Contains(r.HTML, "::counter{to=7}") {
		t.Errorf("indented code was lost or treated as a directive: %s", r.HTML)
	}
	if strings.Contains(r.HTML, `class="jc-counter"`) {
		t.Errorf("indented code directive was executed: %s", r.HTML)
	}
	if !strings.Contains(r.HTML, "<h2") || !strings.Contains(r.HTML, "後續標題") {
		t.Errorf("heading after indented code was lost or rendered as plain text: %s", r.HTML)
	}
	if len(r.TOC) != 1 || r.TOC[0].Text != "後續標題" {
		t.Errorf("heading after indented code is missing from TOC: %+v", r.TOC)
	}
}

func TestUnknownHTMLishLanguagesRemainOrdinaryCode(t *testing.T) {
	for _, language := range []string{"htmlish", "latexyz"} {
		source := "```" + language + "\n<div>literal</div>\n```\n"
		r := erRender(t, source, "jmd")
		if !strings.Contains(r.HTML, "language-"+language) || strings.Contains(r.HTML, "<div>literal</div>") || strings.Contains(r.HTML, "jc-math") {
			t.Errorf("unknown language %q was interpreted as HTML or math: %s", language, r.HTML)
		}
	}
}

func TestTableYAMLMarkupCharactersStaySingleEscaped(t *testing.T) {
	source := "```table\ncolumns: [Name, Value]\nrows:\n  - [\"A & B\", \"<item>\"]\n```\n"
	r := erRender(t, source, "jmd")
	if !strings.Contains(r.HTML, "A &amp; B") || !strings.Contains(r.HTML, "&lt;item&gt;") {
		t.Errorf("table text was not escaped exactly once: %s", r.HTML)
	}
	if strings.Contains(r.HTML, "&amp;amp;") || strings.Contains(r.HTML, "&amp;lt;") {
		t.Errorf("table text was double-escaped: %s", r.HTML)
	}
}

func TestBuiltinCompareUsesSafeNativeMarkupAndCustomRegistryStillUsesFrame(t *testing.T) {
	r := erRender(t, "::compare{before=/uploads/before.png after=https://example.test/after.png}", "jmd")
	for _, want := range []string{`class="jc-compare"`, `class="jc-compare-before"`, `class="jc-compare-after"`, `type="range"`} {
		if !strings.Contains(r.HTML, want) {
			t.Errorf("native compare missing %q: %s", want, r.HTML)
		}
	}
	if !mtContains(r.Features, "compare") || strings.Contains(r.HTML, "<iframe") {
		t.Errorf("native compare feature/output mismatch: html=%s features=%v", r.HTML, r.Features)
	}
	for _, unsafe := range []string{
		"::compare{before=javascript:alert(1) after=/uploads/after.png}",
		"::compare{before=data:image/svg+xml,x after=/uploads/after.png}",
	} {
		bad := erRender(t, unsafe, "jmd")
		if strings.Contains(bad.HTML, "<img") || strings.Contains(bad.HTML, "javascript:") || strings.Contains(bad.HTML, "data:image") {
			t.Errorf("unsafe compare URL became active markup: %s", bad.HTML)
		}
	}
	// A published custom widget with the builtin name remains versioned and sandboxed.
	custom, err := Render(context.Background(), "::widget{name=compare before=/uploads/before.png after=/uploads/after.png}", Options{
		Trust: TrustRich, Format: "jmd", Widgets: map[string]WidgetSpec{"compare": {Version: 7, Params: map[string]WidgetParamSpec{"before": {Type: "string", Required: true}, "after": {Type: "string", Required: true}}}},
	})
	if err != nil {
		t.Fatalf("render registered compare widget: %v", err)
	}
	if !strings.Contains(custom.HTML, `data-v="7"`) || !strings.Contains(custom.HTML, "/widgets/compare/7/frame") || !strings.Contains(custom.HTML, "<iframe") {
		t.Errorf("published custom compare widget did not retain iframe behavior: %s", custom.HTML)
	}
	builtin, err := Render(context.Background(), "::compare{before=/uploads/before.png after=/uploads/after.png}", Options{
		Trust: TrustRich, Format: "jmd", Widgets: map[string]WidgetSpec{"compare": {Version: 7, Params: map[string]WidgetParamSpec{"before": {Type: "string", Required: true}, "after": {Type: "string", Required: true}}}},
	})
	if err != nil {
		t.Fatalf("render builtin compare with same-name custom registry: %v", err)
	}
	if !strings.Contains(builtin.HTML, `class="jc-compare"`) || !strings.Contains(builtin.HTML, `type="range"`) || strings.Count(builtin.HTML, "<img") != 2 || strings.Contains(builtin.HTML, "<iframe") {
		t.Errorf("::compare was overridden by same-name custom widget registry: %s", builtin.HTML)
	}
}

func TestWidgetDateValidationDefaultsRequiredAndMaxLength(t *testing.T) {
	spec := WidgetSpec{Version: 1, Params: map[string]WidgetParamSpec{
		"date":  {Type: "date", Required: true},
		"label": {Type: "string", Default: "ok", MaxLength: 4},
	}}
	for _, date := range []string{"2024-02-29"} {
		got, err := ValidateWidgetParams(spec, map[string]string{"date": date})
		if err != nil || got["date"] != date || got["label"] != "ok" {
			t.Errorf("valid leap date/default rejected or mistyped: got=%#v err=%v", got, err)
		}
	}
	for _, date := range []string{"2023-02-29", "2024-02-29T00:00:00Z"} {
		if _, err := ValidateWidgetParams(spec, map[string]string{"date": date}); err == nil {
			t.Errorf("invalid or RFC3339 date %q was accepted", date)
		}
	}
	if _, err := ValidateWidgetParams(spec, map[string]string{"label": "ok"}); err == nil {
		t.Error("missing required date was accepted")
	}
	if _, err := ValidateWidgetParams(spec, map[string]string{"date": "2024-02-29", "label": "longer"}); err == nil {
		t.Error("string value beyond MaxLength was accepted")
	}
}

func TestHeadingIDsRemainUniqueAndMatchTOC(t *testing.T) {
	r := erRender(t, "## One {#foo}\n\n## Two {#foo-2}\n\n## Three {#foo}\n", "jmd")
	ids := regexp.MustCompile(`<h[1-6] id="([^"]+)"`).FindAllStringSubmatch(r.HTML, -1)
	if len(ids) != 3 || len(r.TOC) != 3 {
		t.Fatalf("found %d heading ids and %d TOC entries, want 3: html=%s toc=%+v", len(ids), len(r.TOC), r.HTML, r.TOC)
	}
	seen := map[string]bool{}
	for i, match := range ids {
		id := match[1]
		if seen[id] {
			t.Errorf("duplicate final heading id %q: %s", id, r.HTML)
		}
		seen[id] = true
		if r.TOC[i].ID != id {
			t.Errorf("TOC id %q does not match heading id %q at position %s", r.TOC[i].ID, id, strconv.Itoa(i))
		}
	}
}

func TestRenderHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Render(ctx, "內容", Options{Trust: TrustRich, Format: "jmd"}); err == nil {
		t.Fatal("Render succeeded with an already-canceled context")
	}
}

func TestRenderPipelineTimeBound(t *testing.T) {
	const childEnv = "JELLY_RENDER_PIPELINE_BOUNDS_CHILD"
	if os.Getenv(childEnv) == "1" {
		source := strings.Repeat("$x$\n::counter{to=1}\n", 90_000)
		if len(source) >= 2<<20 {
			t.Fatalf("test source exceeds the render source limit: %d bytes", len(source))
		}
		started := time.Now()
		_, err := Render(context.Background(), source, Options{Trust: TrustRich, Format: "jmd"})
		elapsed := time.Since(started)
		if elapsed > 3*time.Second {
			t.Fatalf("whole render pipeline took %s, want it bounded to about 3 seconds", elapsed)
		}
		if err != nil && err != ErrRenderTimeout {
			t.Fatalf("large render returned unexpected error after %s: %v", elapsed, err)
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRenderPipelineTimeBound$")
	cmd.Env = append(os.Environ(), childEnv+"=1")
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatalf("start bounded render subprocess: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("bounded render subprocess failed: %v\n%s", err, output.String())
		}
	case <-time.After(4 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatalf("render subprocess exceeded 4 seconds and was terminated\n%s", output.String())
	}
}

func TestParallelRenderOutputIsolation(t *testing.T) {
	const count = 16
	results := make([]string, count)
	errs := make([]error, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			marker := fmt.Sprintf("parallel-marker-%02d", i)
			r, err := Render(context.Background(), marker, Options{Trust: TrustRich, Format: "jmd"})
			if err != nil {
				errs[i] = err
				return
			}
			results[i] = r.HTML
		}(i)
	}
	wg.Wait()
	for i, out := range results {
		if errs[i] != nil {
			t.Errorf("render %d: %v", i, errs[i])
			continue
		}
		own := fmt.Sprintf("parallel-marker-%02d", i)
		if !strings.Contains(out, own) {
			t.Errorf("render %d lost its own marker: %s", i, out)
		}
		for j := range count {
			if j != i && strings.Contains(out, fmt.Sprintf("parallel-marker-%02d", j)) {
				t.Errorf("render %d leaked render %d output: %s", i, j, out)
			}
		}
	}
}

func TestInlineMathDollarBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		wantMath     bool
	}{
		{"space after opener", "$ x$", false},
		{"digit before opener", "1$x$", false},
		{"valid inline math", "$x$", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := erRender(t, tc.source, "jmd")
			got := strings.Contains(r.HTML, `class="jc-math"`)
			if got != tc.wantMath {
				t.Errorf("jc-math present = %v, want %v: %s", got, tc.wantMath, r.HTML)
			}
		})
	}
}

func TestSourceMapUsesOriginalLines(t *testing.T) {
	source := "intro paragraph\n\n## Heading\n\n:::hero {bg=mint}\ninside paragraph\n:::\n"
	r, err := Render(context.Background(), source, Options{Trust: TrustRich, Format: "jmd", SourceMap: true})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{`<p data-line="1">`, `<h2 id="heading" data-line="3">`, `<section class="jc-hero" data-bg="mint" data-line="5">`, `<p data-line="6">`} {
		if !strings.Contains(r.HTML, want) {
			t.Errorf("source map missing %s: %s", want, r.HTML)
		}
	}
}

func TestSourceMapSkipsInlineTagsWithoutLosingLaterBlocks(t *testing.T) {
	source := "**bold**\n\n後段\n\n## 後標題\n"
	r, err := Render(context.Background(), source, Options{Trust: TrustRich, Format: "jmd", SourceMap: true})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{`<p data-line="1"><strong>bold</strong></p>`, `<p data-line="3">後段</p>`, `<h2 id="後標題" data-line="5">`} {
		if !strings.Contains(r.HTML, want) {
			t.Errorf("source map missing %s: %s", want, r.HTML)
		}
	}
}

func TestSourceMapMatchesDuplicateTextBySourcePosition(t *testing.T) {
	source := "repeat\n\n:::hero {bg=mint}\nrepeat\n:::\n\nrepeat\n\n## repeat\n"
	r, err := Render(context.Background(), source, Options{Trust: TrustRich, Format: "jmd", SourceMap: true})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	pLines := regexp.MustCompile(`<p\b[^>]*data-line="([0-9]+)">repeat</p>`).FindAllStringSubmatch(r.HTML, -1)
	if len(pLines) != 3 {
		t.Fatalf("found %d mapped paragraphs, want 3: %s", len(pLines), r.HTML)
	}
	for i, line := range []string{"1", "4", "7"} {
		if pLines[i][1] != line {
			t.Errorf("paragraph %d mapped to line %s, want %s: %s", i+1, pLines[i][1], line, r.HTML)
		}
	}
	if !strings.Contains(r.HTML, `<h2 id="repeat" data-line="9">`) {
		t.Errorf("repeated heading mapped to wrong source line: %s", r.HTML)
	}
}

func TestLiteralCodeSlotAttributeIsPreserved(t *testing.T) {
	source := "```text\ndata-jc-slot=\"jc-trusted-deadbeef-0\"\n```"
	r := erRender(t, source, "jmd")
	if !strings.Contains(r.HTML, `data-jc-slot=&#34;jc-trusted-deadbeef-0&#34;`) && !strings.Contains(r.HTML, `data-jc-slot=\"jc-trusted-deadbeef-0\"`) {
		t.Fatalf("code block changed literal data-jc-slot text: %s", r.HTML)
	}
	if strings.Contains(r.HTML, "data-jc-user-slot") {
		t.Fatalf("code block was rewritten as if literal text were a live slot: %s", r.HTML)
	}
}

func TestUnclosedContainerDiagnosesAndKeepsBody(t *testing.T) {
	r := erRender(t, ":::hero {bg=mint}\nimportant body text\n", "jmd")
	if len(r.Diagnostics) == 0 {
		t.Fatal("unclosed container produced no diagnostic")
	}
	found := false
	for _, d := range r.Diagnostics {
		if d.Code == "unclosed_container" {
			found = true
		}
	}
	if !found {
		t.Errorf("missing unclosed_container diagnostic: %+v", r.Diagnostics)
	}
	if !strings.Contains(r.HTML, "important body text") {
		t.Errorf("unclosed container discarded its body: %s", r.HTML)
	}
}

func TestSchemaBackedFeatureMetadataMatchesRenderedOutput(t *testing.T) {
	// The ticket names six Features values. Counter is also a schema-declared directive with a
	// data-jc hook, making it the seventh interactive output whose feature metadata needs a decision.
	source := "$x$\n\n## Motion {reveal=fade-up}\n\n:::tabs\n::: tab {title=One}\nTab body\n:::\n:::\n\n::counter{to=12}\n\n::widget{name=countdown to=2030-01-01}\n\n::widget{name=counter to=12}\n"
	r := erRender(t, source, "jmd")
	if strings.Join(r.Features, ",") != strings.Join(Features(r.HTML), ",") {
		t.Fatalf("Result.Features %v differs from Features(HTML) %v", r.Features, Features(r.HTML))
	}
	for _, expected := range []string{"counter", "math", "motion", "tabs", "widget"} {
		if !mtContains(r.Features, expected) {
			t.Errorf("schema-backed source omitted feature %q: html=%s features=%v", expected, r.HTML, r.Features)
		}
	}
	if !strings.Contains(r.HTML, `data-jc="counter"`) {
		t.Errorf("schema-declared counter did not render its hook: %s", r.HTML)
	}
}

func TestFlattenNoteCalloutWithoutBodyDoesNotPanic(t *testing.T) {
	var got string
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Errorf("Flatten panicked for a one-line callout: %v", recovered)
			}
		}()
		var err error
		got, err = Flatten("> [!NOTE]")
		if err != nil {
			t.Errorf("Flatten: %v", err)
		}
	}()
	if t.Failed() {
		return
	}
	if !strings.Contains(got, "> **Note**") {
		t.Fatalf("flattened callout lost its Note quote heading: %q", got)
	}
}

func TestFlattenPreservesInlineMathCodeAndJSONBraces(t *testing.T) {
	source := "行內 $x_{i}+y^{2}$\n\n`{value}`\n\n普通文字 JSON {\"a\":1}"
	got, err := Flatten(source)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	for _, want := range []string{"$x_{i}+y^{2}$", "`{value}`", `JSON {"a":1}`} {
		if !strings.Contains(got, want) {
			t.Errorf("flattening removed required source %q: %q", want, got)
		}
	}
}

func TestFlattenNestedSectionComponentsRemoveJellySyntaxInOrder(t *testing.T) {
	source := ":::section\nFirst\n:::tabs\n::: tab {title=Middle}\nSecond\n:::\n:::\n:::hero {bg=mint}\nThird\n:::\nFourth\n:::\n"
	got, err := Flatten(source)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	last := -1
	for _, text := range []string{"First", "Second", "Third", "Fourth"} {
		pos := strings.Index(got, text)
		if pos < 0 || pos <= last {
			t.Errorf("flattened text %q missing or out of order: %q", text, got)
		}
		last = pos
	}
	for _, syntax := range []string{":::section", ":::tabs", "::: tab", ":::hero"} {
		if strings.Contains(got, syntax) {
			t.Errorf("flattened output retained Jelly container syntax %q: %q", syntax, got)
		}
	}
}

func TestFlattenPreservesLongBacktickAndTildeFences(t *testing.T) {
	source := "````markdown\n```go\nfmt.Println(\"x\")\n```\n````\n\n~~~text\n:::hero\n$x$\n~~~\n"
	got, err := Flatten(source)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	for _, exact := range []string{"````markdown\n```go\nfmt.Println(\"x\")\n```\n````", "~~~text\n:::hero\n$x$\n~~~"} {
		if !strings.Contains(got, exact) {
			t.Errorf("flattening changed fenced content %q: %q", exact, got)
		}
	}
}

func TestRenderLongBacktickAndTildeFencesKeepJellySyntaxLiteral(t *testing.T) {
	cases := []string{
		"````markdown\n```go\n:::hero\n$x$\n```\n````",
		"~~~text\n:::hero\n$x$\n~~~",
	}
	for i, source := range cases {
		t.Run(fmt.Sprintf("fence_%d", i), func(t *testing.T) {
			r := erRender(t, source, "jmd")
			if strings.Contains(r.HTML, "jc-hero") || strings.Contains(r.HTML, "jc-math") {
				t.Fatalf("fenced Jelly syntax was interpreted: %s", r.HTML)
			}
			for _, literal := range []string{":::hero", "$x$"} {
				if !strings.Contains(r.HTML, literal) {
					t.Errorf("fenced source lost literal %q: %s", literal, r.HTML)
				}
			}
		})
	}
}

func TestMalformedEmptyCalloutDoesNotPanic(t *testing.T) {
	const childEnv = "JELLY_RENDER_EMPTY_CALLOUT_CHILD"
	if os.Getenv(childEnv) == "1" {
		_, _ = Render(context.Background(), "> [!]\n> body", Options{Trust: TrustRich, Format: "jmd"})
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMalformedEmptyCalloutDoesNotPanic$")
	cmd.Env = append(os.Environ(), childEnv+"=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("Render panicked or exited unsuccessfully for empty callout kind: %v\n%s", err, output)
	}
}

func TestTopLevelBareCloserDoesNotDiscardFollowingSource(t *testing.T) {
	for _, format := range []string{"jmd", "md"} {
		t.Run(format, func(t *testing.T) {
			r := erRender(t, "before\n\n:::\n\nafter", format)
			if !strings.Contains(r.HTML, "before") || !strings.Contains(r.HTML, "after") {
				t.Errorf("bare top-level ::: swallowed surrounding text: %s", r.HTML)
			}
		})
	}
}

var activeURLAttrRE = regexp.MustCompile(`(?is)<(?:a\b[^>]*\bhref|iframe\b[^>]*\bsrc)\s*=\s*["']([^"']*)["'][^>]*>`)

func assertNoDangerousActiveURL(t *testing.T, output string) {
	t.Helper()
	for _, match := range activeURLAttrRE.FindAllStringSubmatch(output, -1) {
		value := html.UnescapeString(match[1])
		var normalized strings.Builder
		for _, r := range value {
			if r > 0x20 && r != 0x7f {
				normalized.WriteRune(r)
			}
		}
		scheme := strings.ToLower(normalized.String())
		if strings.HasPrefix(scheme, "javascript:") || strings.HasPrefix(scheme, "data:") {
			t.Errorf("dangerous active URL in rendered element: %s", match[0])
		}
	}
}

func TestEmbedRejectsDangerousActiveURLSchemes(t *testing.T) {
	tests := []struct{ name, target string }{
		{"javascript", "javascript:alert(1)"},
		{"data", "data:text/html,evil"},
		{"mixed case", "JaVaScRiPt:alert(1)"},
		{"embedded control", "java\nscript:alert(1)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := erRender(t, "::embed{url=\""+tc.target+"\"}", "jmd")
			assertNoDangerousActiveURL(t, r.HTML)
		})
	}
}

func TestExpandRejectsDangerousProductURLs(t *testing.T) {
	r := erRender(t, ":::products {category=gift}\n:::", "jmd")
	out, err := Expand(context.Background(), r.HTML, DynamicData{Products: []DynamicItem{{Title: "x", URL: "javascript:alert(1)"}}})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	assertNoDangerousActiveURL(t, out)
}

func TestFlattenYAMLTableEscapesPipeAndKeepsEntitiesReadable(t *testing.T) {
	source := "```table\ncolumns: [Name, Value]\nrows:\n  - [Item, \"left|right & <ok>\"]\n```\n"
	got, err := Flatten(source)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	row := ""
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "Item") {
			row = line
		}
	}
	if row == "" {
		t.Fatalf("flattened table lost its data row: %q", got)
	}
	if !strings.Contains(row, `left\|right`) || !strings.Contains(row, "&amp;") || !strings.Contains(row, "&lt;ok&gt;") {
		t.Errorf("flattened cells are not safely readable: %q", row)
	}
	if strings.Count(strings.ReplaceAll(row, `\|`, ""), "|") != 3 {
		t.Errorf("pipe in a cell changed the two-column table structure: %q", row)
	}
}
