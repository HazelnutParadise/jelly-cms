package theme

import (
	"html/template"
	"reflect"
	"strings"
	"testing"
)

func TestPublicFuncEdgeUnicodeMatchOffsets(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
	}{
		{"Kelvin sign prefix", "Kabc", "K<mark>abc</mark>"},
		{"dotted capital I prefix", "İabc", "İ<mark>abc</mark>"},
		{"sharp s prefix does not damage later match", "ßabc", "ß<mark>abc</mark>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := string(Highlight(tc.text, "abc"))
			if got != tc.want {
				t.Errorf("Highlight(%q, %q) = %q, want %q", tc.text, "abc", got, tc.want)
			}
			snippet := string(Snippet("<p>"+tc.text+"</p>", "abc", 8))
			if snippet != tc.want {
				t.Errorf("Snippet(%q, %q) = %q, want %q", tc.text, "abc", snippet, tc.want)
			}
		})
	}
}

func TestPublicFuncEdgeSnippetEntitiesBlocksAndAdversarialQuery(t *testing.T) {
	escapedEntity := Snippet("<p>&lt;script&gt;needle&lt;/script&gt;</p>", "needle", 20)
	const wantEntity template.HTML = "&lt;script&gt;<mark>needle</mark>&lt;/script&gt;"
	if escapedEntity != wantEntity {
		t.Errorf("Snippet of encoded markup = %q, want safely escaped %q", escapedEntity, wantEntity)
	}

	blocks := Snippet("<p>alpha</p><p>beta</p>", "alpha beta", 10)
	const wantBlocks template.HTML = "<mark>alpha beta</mark>"
	if blocks != wantBlocks {
		t.Errorf("Snippet across paragraph boundaries = %q, want consistent word spacing %q", blocks, wantBlocks)
	}

	const hostile = `<img src=x onerror=alert(1)>`
	got := Highlight("match "+hostile+" end", hostile)
	const wantHostile template.HTML = "match <mark>&lt;img src=x onerror=alert(1)&gt;</mark> end"
	if got != wantHostile {
		t.Errorf("Highlight of markup-shaped query = %q, want %q", got, wantHostile)
	}
	if strings.Contains(string(got), "<img") {
		t.Errorf("Highlight emitted executable query markup: %q", got)
	}
}

func TestPublicFuncEdgeHeadingIDsReserveExplicitIDsAndRemainStableOnReentry(t *testing.T) {
	input := `<h2>Setup Guide</h2><h2 id="setup-guide">Explicit ID</h2><h2>Setup Guide</h2>`
	html, toc := HeadingIDs(input)
	wantIDs := []string{"setup-guide-2", "setup-guide", "setup-guide-3"}
	if len(toc) != len(wantIDs) {
		t.Fatalf("HeadingIDs returned %d entries, want %d: %+v", len(toc), len(wantIDs), toc)
	}
	for i, want := range wantIDs {
		if toc[i].ID != want {
			t.Errorf("heading %d ID = %q, want %q", i, toc[i].ID, want)
		}
		if !strings.Contains(html, `id="`+want+`"`) {
			t.Errorf("rendered HTML has no id=%q: %s", want, html)
		}
	}

	htmlAgain, tocAgain := HeadingIDs(html)
	if htmlAgain != html || !reflect.DeepEqual(tocAgain, toc) {
		t.Errorf("HeadingIDs changed IDs when re-entered:\nfirst  %q %+v\nsecond %q %+v", html, toc, htmlAgain, tocAgain)
	}
}

func TestPublicFuncEdgeHeadingIDsAvoidNonHeadingIDCollision(t *testing.T) {
	input := `<div id="same"></div><h2 id="same">Heading</h2>`
	html, toc := HeadingIDs(input)
	if len(toc) != 1 {
		t.Fatalf("HeadingIDs returned %d entries, want 1: %+v", len(toc), toc)
	}
	if toc[0].ID == "same" {
		t.Errorf("heading reused non-heading element id %q", toc[0].ID)
	}
	if !strings.Contains(html, `id="same"`) ||
		!strings.Contains(html, `<h2 id="`+toc[0].ID+`">Heading</h2>`) {
		t.Errorf("HTML does not preserve the div ID and give the heading its TOC ID %q: %s", toc[0].ID, html)
	}

	htmlAgain, tocAgain := HeadingIDs(html)
	if htmlAgain != html || !reflect.DeepEqual(tocAgain, toc) {
		t.Errorf("HeadingIDs changed collision-resolved ID on re-entry:\nfirst  %q %+v\nsecond %q %+v", html, toc, htmlAgain, tocAgain)
	}
}

func TestPublicFuncEdgeReadingTimeCountsVisibleHTMLWords(t *testing.T) {
	t.Run("450 paragraphs", func(t *testing.T) {
		if got := ReadingTime(strings.Repeat("<p>word</p>", 450)); got != 3 {
			t.Errorf("ReadingTime(450 paragraph words) = %d, want 3", got)
		}
	})

	t.Run("inline markup does not split a word", func(t *testing.T) {
		source := strings.Repeat("<p>word</p>", 199) + "<p>w<strong>or</strong>d</p>"
		if got := ReadingTime(source); got != 1 {
			t.Errorf("ReadingTime(199 words plus inline-split word) = %d, want 1", got)
		}
	})

	t.Run("script and style contents are ignored", func(t *testing.T) {
		source := strings.Repeat("<p>word</p>", 199) +
			"<script>" + strings.Repeat("scriptword ", 200) + "</script>" +
			"<style>" + strings.Repeat("styleword ", 200) + "</style>"
		if got := ReadingTime(source); got != 1 {
			t.Errorf("ReadingTime(199 visible words plus script/style text) = %d, want 1", got)
		}
	})
}
