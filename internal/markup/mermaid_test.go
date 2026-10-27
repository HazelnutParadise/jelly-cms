package markup

import (
	"context"
	"html"
	"strings"
	"testing"
)

func TestRenderMermaidFencePreservesEscapedSourceWithoutHighlighting(t *testing.T) {
	source := "flowchart TD\n  A[\"a & <b>\"] --> B\n"
	for _, format := range []string{"md", "jmd"} {
		for _, trust := range []string{TrustStrict, TrustRich} {
			t.Run(format+"/"+trust, func(t *testing.T) {
				r, err := Render(context.Background(), "```mermaid\n"+source+"```\n", Options{Format: format, Trust: trust})
				if err != nil {
					t.Fatalf("Render: %v", err)
				}
				if strings.Contains(r.HTML, "<svg") || strings.Contains(r.HTML, "<span") || strings.Contains(r.HTML, "chroma") {
					t.Fatalf("Mermaid source was rendered or highlighted on the server: %s", r.HTML)
				}
				if !strings.Contains(r.HTML, `<code class="language-mermaid">`) {
					t.Fatalf("Mermaid block lost its language class: %s", r.HTML)
				}
				if !strings.Contains(r.HTML, html.EscapeString(source)) {
					t.Errorf("source must remain escaped inside code: %s", r.HTML)
				}
			})
		}
	}
}

func TestFeaturesDetectsMermaidCodeClassToken(t *testing.T) {
	for _, tc := range []struct {
		name, fragment string
		want           bool
	}{
		{"code token", `<pre><code class="language-mermaid">flowchart TD</code></pre>`, true},
		{"extra code classes", `<pre><code class="chroma language-mermaid">flowchart TD</code></pre>`, true},
		{"substring is not token", `<pre><code class="language-mermaidx">x</code></pre>`, false},
		{"text is not code", `<p>language-mermaid</p>`, false},
		{"other element is not code", `<div class="language-mermaid">x</div>`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mtContains(Features(tc.fragment), "mermaid")
			if got != tc.want {
				t.Errorf("Features(%q) has mermaid=%t, want %t", tc.fragment, got, tc.want)
			}
		})
	}
}

func TestFeaturesRetainsMathMotionAndWidgetWithAdditionalClasses(t *testing.T) {
	for _, tc := range []struct{ name, fragment, feature string }{
		{"math", `<span class="jc-math user-note" data-tex="x">x</span>`, "math"},
		{"motion", `<h2 class="jc-heading" data-reveal="fade-up">Title</h2>`, "motion"},
		{"widget", `<div class="jc-widget extra" data-widget="clock" data-v="1"></div>`, "widget"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Features(tc.fragment); !mtContains(got, tc.feature) {
				t.Errorf("Features(%q) = %v, want %q", tc.fragment, got, tc.feature)
			}
		})
	}
}
