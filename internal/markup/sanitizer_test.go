package markup

// Sanitizer matrix (docs/tickets/09-editor.md, 淨化政策). Every case renders under both policies and
// checks that the dangerous token is absent from the output. Tokens are raw tag fragments such as
// "<img src=x onerror" rather than bare words such as "onerror". Under strict, an html fence becomes
// escaped code, so its text legitimately contains the bare words, but the escaped form never
// contains the raw tag. Written before the policy exists; do not edit to match an implementation.

import (
	"strings"
	"testing"
)

type mtSanitizerCase struct {
	name      string
	source    string
	forbidden []string
}

var mtSanitizerCases = []mtSanitizerCase{
	{"script in html fence", "```html\n<script>alert(1)</script>\n```", []string{"<script"}},
	{"script inline", "<script>alert(1)</script>", []string{"<script"}},
	{"img onerror inline", "<img src=x onerror=alert(1)>", []string{"<img src=x onerror"}},
	{"img onerror html fence", "```html\n<img src=\"x\" onerror=\"alert(1)\">\n```", []string{"<img src=\"x\" onerror"}},
	{"svg onload html fence", "```html\n<svg onload=\"alert(1)\"></svg>\n```", []string{"<svg"}},
	{"a javascript href html fence", "```html\n<a href=\"javascript:alert(1)\">x</a>\n```", []string{"href=\"javascript:"}},
	{"a javascript href inline", "<a href=\"javascript:alert(1)\">x</a>", []string{"href=\"javascript:"}},
	{"markdown link javascript", "[x](javascript:alert(1))", []string{"href=\"javascript:"}},
	{"data url link html fence", "```html\n<a href=\"data:text/html;base64,PHNjcmlwdD4=\">x</a>\n```", []string{"href=\"data:"}},
	{"markdown image data url", "![x](data:image/svg+xml;base64,PHN2Zz4=)", []string{"src=\"data:"}},
	{"style html fence", "```html\n<style>body{display:none}</style>\n```", []string{"<style"}},
	{"iframe html fence", "```html\n<iframe src=\"https://evil.test/\"></iframe>\n```", []string{"<iframe"}},
	{"iframe inline", "<iframe src=\"https://evil.test/\"></iframe>", []string{"<iframe"}},
	{"form html fence", "```html\n<form action=\"https://evil.test/\"><input name=\"x\"></form>\n```", []string{"<form"}},
	{"meta refresh html fence", "```html\n<meta http-equiv=\"refresh\" content=\"0;url=https://evil.test/\">\n```", []string{"<meta"}},
	{"base html fence", "```html\n<base href=\"https://evil.test/\">\n```", []string{"<base"}},
	{"math html fence", "```html\n<math><mi xlink:href=\"javascript:alert(1)\">x</mi></math>\n```", []string{"<math", "href=\"javascript:"}},
	{"noscript mxss html fence", "```html\n<noscript><p title=\"</noscript><img src=x onerror=alert(1)>\"></p></noscript>\n```", []string{"<noscript", "<img src=x onerror"}},
	{"object html fence", "```html\n<object data=\"https://evil.test/x.swf\"></object>\n```", []string{"<object"}},
	{"embed html fence", "```html\n<embed src=\"https://evil.test/x\">\n```", []string{"<embed"}},
	{"id __proto__ html fence", "```html\n<div id=\"__proto__\">x</div>\n```", []string{"id=\"__proto__\""}},
	{"forged slot html fence", "```html\n<span data-jc-slot=\"x\">y</span>\n```", []string{"data-jc-slot=\"x\""}},
	{"forged widget html fence", "```html\n<div class=\"jc-widget\" data-widget=\"evil\">z</div>\n```", []string{"data-widget=\"evil\""}},
	{"forged slot inline", "<span data-jc-slot=\"x\">y</span>", []string{"data-jc-slot=\"x\""}},
}

func TestSanitizerMatrix(t *testing.T) {
	for _, c := range mtSanitizerCases {
		for _, trust := range []string{TrustStrict, TrustRich} {
			t.Run(c.name+"/"+trust, func(t *testing.T) {
				r := mtRender(t, c.source, trust)
				for _, token := range c.forbidden {
					if strings.Contains(r.HTML, token) {
						t.Errorf("trust=%s: output contains %q\noutput: %s", trust, token, r.HTML)
					}
				}
			})
		}
	}
}

// TestSanitizerKeepsPlainFigure checks the other side of the policy: a plain figure with a local
// image survives in rich, and the same fence becomes code under strict.
func TestSanitizerKeepsPlainFigure(t *testing.T) {
	source := "```html\n<figure><img src=\"/uploads/a.png\"></figure>\n```"

	rich := mtRender(t, source, TrustRich)
	if !strings.Contains(rich.HTML, "<figure>") {
		t.Errorf("rich policy must keep <figure> from an html fence: %s", rich.HTML)
	}
	if !strings.Contains(rich.HTML, `<img src="/uploads/a.png"`) {
		t.Errorf("rich policy must keep a local img src: %s", rich.HTML)
	}

	strict := mtRender(t, source, TrustStrict)
	if !strings.Contains(strict.HTML, `<pre><code class="language-html">`) {
		t.Errorf("strict policy must render the html fence as code: %s", strict.HTML)
	}
	if strings.Contains(strict.HTML, "<figure>") {
		t.Errorf("strict policy must not emit a raw <figure>: %s", strict.HTML)
	}
}
