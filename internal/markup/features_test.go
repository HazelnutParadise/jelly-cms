package markup

// Motion, component and embed contract (docs/tickets/09-editor.md, G4 動效與互動元件, 語法表, 錯誤與邊界).
// Motion attributes become data-* only and are styled by content.css and jelly-motion.js. Built-in
// components must still show their content without JavaScript. Embeds are limited to an allow-list. They
// are written before the renderer exists and are expected to fail against the skeleton. Do not edit them
// to match an implementation.
//
// Assumption: the link card for a non-allow-listed embed carries rel="noopener nofollow" in any order.
// The committed golden embed-other.html expects the anchor without a rel attribute, so that golden and
// this test cannot both pass. Resolve the conflict in the golden, not here.

import (
	"regexp"
	"strings"
	"testing"
)

// ftrFeatureMarkers holds one minimal fragment per feature, as the renderer emits it.
var ftrFeatureMarkers = map[string]string{
	"math":     `<span class="jc-math" data-tex="x^2">x^2</span>`,
	"motion":   `<h2 id="a" data-reveal="fade-up">A</h2>`,
	"tabs":     `<div class="jc-tabs" data-jc="tabs"><section class="jc-tab" data-title="A"><h3 class="jc-tab-title">A</h3></section></div>`,
	"carousel": `<div data-jc="carousel"><img src="/uploads/a.png" alt=""></div>`,
	"lightbox": `<a href="/uploads/a.png" data-jc="lightbox"><img src="/uploads/a.png" alt=""></a>`,
	"widget":   `<div class="jc-widget" data-widget="countdown" data-v="1"><iframe sandbox="allow-scripts" src="/widgets/countdown/1/frame?to=2027-01-01" loading="lazy" title="countdown"></iframe></div>`,
}

var (
	ftrOpenH2  = regexp.MustCompile(`<h2\b([^>]*)>`)
	ftrAttr    = regexp.MustCompile(`([A-Za-z_:][-A-Za-z0-9_:.]*)(?:="([^"]*)")?`)
	ftrSection = regexp.MustCompile(`<section\b[^>]*class="jc-tab"[^>]*>`)
	ftrHidden  = regexp.MustCompile(`(?i)\bhidden\b|aria-hidden|\binert\b|display\s*:\s*none`)
	ftrCounter = regexp.MustCompile(`<span\b[^>]*data-jc="counter"[^>]*>([^<]*)</span>`)
	ftrIframe  = regexp.MustCompile(`<iframe\b[^>]*\ssrc="([^"]*)"`)
	ftrRel     = regexp.MustCompile(`\srel="([^"]*)"`)
)

func TestFeaturesDetectsMathMotionAndComponents(t *testing.T) {
	names := []string{"math", "motion", "tabs", "carousel", "lightbox", "widget"}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			if got := Features(ftrFeatureMarkers[name]); !mtContains(got, name) {
				t.Errorf("Features(%s marker) = %v, want it to contain %q", name, got, name)
			}
		})
	}

	var all strings.Builder
	for _, name := range names {
		all.WriteString(ftrFeatureMarkers[name])
	}
	got := Features(all.String())
	for _, want := range names {
		if !mtContains(got, want) {
			t.Errorf("Features(all markers) = %v, want it to contain %q", got, want)
		}
	}

	plain := Features("<p>純文字，沒有任何互動元件。</p>")
	for _, name := range names {
		if mtContains(plain, name) {
			t.Errorf("Features(plain text) = %v must not contain %q", plain, name)
		}
	}
}

func TestMotionAttributesBecomeDataOnly(t *testing.T) {
	source := "## T{reveal=fade-up hover=lift scroll=parallax delay=.2 dur=slow stagger=80 once}\n"
	r := mtRender(t, source, TrustRich)

	open := ftrOpenH2.FindStringSubmatch(r.HTML)
	if open == nil {
		t.Fatalf("no h2 element in %s", r.HTML)
	}
	attrs := map[string]string{}
	for _, m := range ftrAttr.FindAllStringSubmatch(open[1], -1) {
		attrs[m[1]] = m[2]
	}

	want := map[string]string{
		"data-reveal":  "fade-up",
		"data-hover":   "lift",
		"data-scroll":  "parallax",
		"data-delay":   ".2",
		"data-dur":     "slow",
		"data-stagger": "80",
		"data-once":    "",
	}
	for name, value := range want {
		got, ok := attrs[name]
		if !ok {
			t.Errorf("h2 lacks %s: %s", name, open[0])
			continue
		}
		if got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
	for name := range attrs {
		if name == "id" {
			continue
		}
		if _, ok := want[name]; !ok {
			t.Errorf("h2 carries %s, which is not a motion data attribute: %s", name, open[0])
		}
	}
	if strings.Contains(r.HTML, "style=") {
		t.Errorf("motion attributes must not emit a style attribute: %s", r.HTML)
	}
}

func TestComponentsRenderWithoutJSFallback(t *testing.T) {
	t.Run("tabs stack without script", func(t *testing.T) {
		r := mtRender(t, ":::tabs\n::: tab {title=\"甲\"}\n內容甲\n:::\n::: tab {title=\"乙\"}\n內容乙\n:::\n:::\n", TrustRich)
		sections := ftrSection.FindAllString(r.HTML, -1)
		if len(sections) != 2 {
			t.Fatalf("tabs must render two jc-tab sections, found %d: %s", len(sections), r.HTML)
		}
		for _, s := range sections {
			if ftrHidden.MatchString(s) || strings.Contains(s, "style=") {
				t.Errorf("tab section must not be hidden without JavaScript: %s", s)
			}
		}
		for _, text := range []string{"內容甲", "內容乙"} {
			if !strings.Contains(r.HTML, text) {
				t.Errorf("tab content %q must be in the HTML: %s", text, r.HTML)
			}
		}
	})

	t.Run("faq uses details and summary", func(t *testing.T) {
		r := mtRender(t, ":::faq\n::: item {title=\"Q1\"}\nA1\n:::\n::: item {title=\"Q2\"}\nA2\n:::\n:::\n", TrustRich)
		if n := strings.Count(r.HTML, "<details"); n != 2 {
			t.Errorf("faq must render two details elements, found %d: %s", n, r.HTML)
		}
		for _, q := range []string{"<summary>Q1</summary>", "<summary>Q2</summary>"} {
			if !strings.Contains(r.HTML, q) {
				t.Errorf("faq must render %s: %s", q, r.HTML)
			}
		}
	})

	t.Run("counter renders its final number as text", func(t *testing.T) {
		r := mtRender(t, "::counter{to=1200}\n", TrustRich)
		m := ftrCounter.FindStringSubmatch(r.HTML)
		if m == nil {
			t.Fatalf("counter must render a span with data-jc=\"counter\": %s", r.HTML)
		}
		if m[1] != "1200" {
			t.Errorf("counter text = %q, want the final number 1200", m[1])
		}
	})
}

func TestEmbedAllowlist(t *testing.T) {
	allowed := []struct{ name, url, want string }{
		{"youtube-nocookie", "https://www.youtube-nocookie.com/embed/abc123", "https://www.youtube-nocookie.com/embed/abc123"},
		{"vimeo", "https://player.vimeo.com/video/76979871", "https://player.vimeo.com/video/76979871"},
		{"bilibili", "https://player.bilibili.com/player.html?bvid=BV1xx411c7mD", "https://player.bilibili.com/player.html?bvid=BV1xx411c7mD"},
		{"youtube.com is normalised", "https://www.youtube.com/embed/abc123", "https://www.youtube-nocookie.com/embed/abc123"},
	}
	for _, c := range allowed {
		t.Run(c.name, func(t *testing.T) {
			r := mtRender(t, "::embed{url=\""+c.url+"\"}\n", TrustRich)
			m := ftrIframe.FindStringSubmatch(r.HTML)
			if m == nil {
				t.Fatalf("an allow-listed embed must render an iframe: %s", r.HTML)
			}
			if m[1] != c.want {
				t.Errorf("iframe src = %q, want %q", m[1], c.want)
			}
			if !strings.Contains(r.HTML, `sandbox="allow-scripts allow-same-origin allow-presentation"`) {
				t.Errorf("embed iframe must carry the embed sandbox list: %s", r.HTML)
			}
		})
	}

	t.Run("other hosts become a link card", func(t *testing.T) {
		r := mtRender(t, "::embed{url=\"https://evil.example/video\"}\n", TrustRich)
		if strings.Contains(r.HTML, "<iframe") {
			t.Errorf("a host outside the allow-list must not render an iframe: %s", r.HTML)
		}
		anchor := regexp.MustCompile(`<a\b[^>]*href="https://evil\.example/video"[^>]*>`).FindString(r.HTML)
		if anchor == "" {
			t.Fatalf("a host outside the allow-list must render a link to it: %s", r.HTML)
		}
		rel := ftrRel.FindStringSubmatch(anchor)
		if rel == nil {
			t.Fatalf("link card lacks a rel attribute: %s", anchor)
		}
		tokens := strings.Fields(rel[1])
		if !mtContains(tokens, "noopener") || !mtContains(tokens, "nofollow") {
			t.Errorf("link card rel = %q, want noopener and nofollow", rel[1])
		}
	})
}
