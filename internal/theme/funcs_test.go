package theme

// Unit tests for the public theme template functions (ticket 05, slice B2 and the reading time and heading
// helpers). They are plain functions with no database and are expected to fail until funcs.go implements the
// contract. Do not edit them to match an implementation.

import (
	"html/template"
	"reflect"
	"strings"
	"testing"
)

// pubThemePlain removes <mark> tags so that a snippet can be compared with its plain text window.
func pubThemePlain(s string) string {
	return strings.NewReplacer("<mark>", "", "</mark>", "").Replace(s)
}

func TestReadingTime(t *testing.T) {
	cases := []struct {
		name string
		html string
		want int
	}{
		{"800 CJK characters read in 2 minutes", "<p>" + strings.Repeat("測", 800) + "</p>", 2},
		{"450 Latin words read in 3 minutes", strings.Repeat("word ", 450), 3},
		{"empty text is at least 1 minute", "", 1},
		{"HTML tags and attributes do not count", `<div data-x="` + strings.Repeat("tag ", 1000) + `"><span class="a b c">hi</span></div>`, 1},
	}
	for _, c := range cases {
		if got := ReadingTime(c.html); got != c.want {
			t.Errorf("%s: ReadingTime = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestHighlightEscapesAndMarks(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		query string
		want  template.HTML
	}{
		{"text is escaped before marking", "<b>bold</b>", "bold", "&lt;b&gt;<mark>bold</mark>&lt;/b&gt;"},
		{"match is case-insensitive and keeps its own case", "Go gopher", "GO", "<mark>Go</mark> gopher"},
		{"the query is escaped as well", "x <i>", "<i>", "x <mark>&lt;i&gt;</mark>"},
		{"no match returns the escaped text unchanged", "x < y & z", "nomatch", "x &lt; y &amp; z"},
	}
	for _, c := range cases {
		if got := Highlight(c.text, c.query); got != c.want {
			t.Errorf("%s: Highlight(%q, %q) = %q, want %q", c.name, c.text, c.query, got, c.want)
		}
	}
}

func TestSnippetAroundMatch(t *testing.T) {
	// Match in the middle: 10 characters either side, with the ellipsis marker on both cut sides.
	body := "<p>" + strings.Repeat("a", 50) + "NEEDLE" + strings.Repeat("b", 50) + "</p>"
	got := pubThemePlain(string(Snippet(body, "needle", 10)))
	want := "……" + strings.Repeat("a", 10) + "NEEDLE" + strings.Repeat("b", 10) + "……"
	if got != want {
		t.Errorf("Snippet with a match = %q, want %q", got, want)
	}

	// No match: the leading n*2 characters of the text.
	noMatch := "<p>" + strings.Repeat("x", 100) + "</p>"
	got = pubThemePlain(string(Snippet(noMatch, "zzz", 5)))
	if !strings.HasPrefix(got, strings.Repeat("x", 10)) {
		t.Errorf("Snippet without a match = %q, want it to start with 10 characters", got)
	}
	if n := strings.Count(got, "x"); n != 10 {
		t.Errorf("Snippet without a match has %d characters of text, want 10 (n*2)", n)
	}
}

func TestHeadingIDsStableAndUnique(t *testing.T) {
	in := `<h2>前言</h2><p>x</p><h3>Setup Guide</h3><h2>前言</h2><h2 id="keep-me">Kept Heading</h2><h2>前言</h2>`
	out, toc := HeadingIDs(in)

	wantTexts := []string{"前言", "Setup Guide", "前言", "Kept Heading", "前言"}
	wantLevels := []int{2, 3, 2, 2, 2}
	if len(toc) != len(wantTexts) {
		t.Fatalf("HeadingIDs returned %d entries, want %d: %+v", len(toc), len(wantTexts), toc)
	}
	for i, e := range toc {
		if e.Text != wantTexts[i] || e.Level != wantLevels[i] {
			t.Errorf("entry %d = {Text:%q Level:%d}, want {Text:%q Level:%d}", i, e.Text, e.Level, wantTexts[i], wantLevels[i])
		}
	}

	base := toc[0].ID
	if base == "" {
		t.Fatalf("first heading has an empty id: %+v", toc)
	}
	if toc[2].ID != base+"-2" {
		t.Errorf("second 前言 id = %q, want %q", toc[2].ID, base+"-2")
	}
	if toc[4].ID != base+"-3" {
		t.Errorf("third 前言 id = %q, want %q", toc[4].ID, base+"-3")
	}
	if toc[3].ID != "keep-me" {
		t.Errorf("existing id on Kept Heading = %q, want keep-me", toc[3].ID)
	}

	seen := map[string]bool{}
	for _, e := range toc {
		if seen[e.ID] {
			t.Errorf("duplicate heading id %q in %+v", e.ID, toc)
		}
		seen[e.ID] = true
		if !strings.Contains(out, `id="`+e.ID+`"`) {
			t.Errorf("returned html has no id=%q attribute", e.ID)
		}
	}

	out2, toc2 := HeadingIDs(in)
	if out2 != out || !reflect.DeepEqual(toc2, toc) {
		t.Errorf("HeadingIDs is not stable across calls:\nfirst  %q %+v\nsecond %q %+v", out, toc, out2, toc2)
	}
}

func TestFlavorMapsKnownColours(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"known colour maps to its CSS variable", "primary", "--primary"},
		{"unknown colour maps to nothing", "no-such-colour", ""},
		{"empty name maps to nothing", "", ""},
	}
	for _, c := range cases {
		if got := Flavor(c.in); got != c.want {
			t.Errorf("%s: Flavor(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
