package theme

// Unit tests for ticket 13 (WebMCP), slot W1: the injection step that puts the WebMCP script tag into a
// rendered theme page, and the paragraph-preserving plain text used by the get_post tool. The contract is
// docs/tickets/13-webmcp.md. They need no database. These tests are expected to fail now, because
// InjectBeforeHeadEnd and PlainTextParagraphs are skeletons; do not edit them to match an implementation.

import (
	"bytes"
	"strings"
	"testing"
)

const webmcpTestTag = `<script type="module" src="/_jelly/webmcp.js"></script>`

func TestWebMCPInjectBeforeHeadEndPositions(t *testing.T) {
	cases := []struct {
		name string
		page string
		want string
	}{
		{
			name: "before the head end",
			page: "<!doctype html><html><head><title>x</title></head><body><p>hi</p></body></html>",
			want: "<!doctype html><html><head><title>x</title>" + webmcpTestTag + "</head><body><p>hi</p></body></html>",
		},
		{
			name: "before the last head end",
			page: "<html><head><script>var s='</head>';</script></head><body></body></html>",
			want: "<html><head><script>var s='</head>';</script>" + webmcpTestTag + "</head><body></body></html>",
		},
		{
			name: "head end in any letter case",
			page: "<HTML><HEAD><TITLE>x</TITLE></HEAD><BODY></BODY></HTML>",
			want: "<HTML><HEAD><TITLE>x</TITLE>" + webmcpTestTag + "</HEAD><BODY></BODY></HTML>",
		},
		{
			name: "before the body end without a head end",
			page: "<html><body><p>no head</p></body></html>",
			want: "<html><body><p>no head</p>" + webmcpTestTag + "</body></html>",
		},
		{
			name: "before the last body end",
			page: "<body><pre>&lt;/body&gt;</pre><template></body></template></body>",
			want: "<body><pre>&lt;/body&gt;</pre><template></body></template>" + webmcpTestTag + "</body>",
		},
		{
			name: "body end in any letter case",
			page: "<P>x</P></Body>",
			want: "<P>x</P>" + webmcpTestTag + "</Body>",
		},
		{
			name: "appended without head or body end",
			page: "<p>fragment</p>",
			want: "<p>fragment</p>" + webmcpTestTag,
		},
		{
			name: "empty page",
			page: "",
			want: webmcpTestTag,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := []byte(tc.page)
			original := append([]byte(nil), input...)
			got := InjectBeforeHeadEnd(input, webmcpTestTag)
			if string(got) != tc.want {
				t.Errorf("InjectBeforeHeadEnd(%q) = %q, want %q", tc.page, got, tc.want)
			}
			if !bytes.Equal(input, original) {
				t.Errorf("InjectBeforeHeadEnd modified its input: now %q, was %q", input, original)
			}
		})
	}
}

func TestWebMCPInjectBeforeHeadEndPrefersHeadOverBody(t *testing.T) {
	page := "<html><head></head><body><p>a</p></body></html>"
	got := string(InjectBeforeHeadEnd([]byte(page), webmcpTestTag))
	if n := strings.Count(got, webmcpTestTag); n != 1 {
		t.Fatalf("tag inserted %d times, want 1; got %q", n, got)
	}
	if strings.Index(got, webmcpTestTag) > strings.Index(got, "<body>") {
		t.Errorf("tag placed inside the body although the page has a head end: %q", got)
	}
}

func TestWebMCPInjectBeforeHeadEndPreservesUnicodeAndFindsHead(t *testing.T) {
	page := []byte("<html><head><title>Kİ中文</title></HEAD><body></body></html>")
	original := append([]byte(nil), page...)
	got := InjectBeforeHeadEnd(page, webmcpTestTag)
	want := "<html><head><title>Kİ中文</title>" + webmcpTestTag + "</HEAD><body></body></html>"
	if string(got) != want {
		t.Errorf("injection = %q, want %q", got, want)
	}
	if !bytes.Equal(page, original) {
		t.Errorf("input changed: got %q, want original %q", page, original)
	}
}

func TestWebMCPInjectBeforeHeadEndDoesNotWriteIntoInputCapacity(t *testing.T) {
	input := make([]byte, len("<head></head>"), 256)
	copy(input, "<head></head>")
	backing := input[:cap(input)]
	original := append([]byte(nil), backing...)
	_ = InjectBeforeHeadEnd(input, webmcpTestTag)
	if !bytes.Equal(backing, original) {
		t.Errorf("injection changed bytes in input backing array beyond visible length")
	}
}

func TestWebMCPPlainTextParagraphs(t *testing.T) {
	source := `<h2>小標題</h2><p>第一段 <strong>粗體</strong>  與
<a href="/x">連結</a></p><script>alert("x")</script><style>p{color:red}</style>` +
		`<ul><li>項目一</li><li>項目二</li></ul><p>第二段 &amp; 符號 &quot;引號&quot;</p>`
	got := PlainTextParagraphs(source)

	for _, want := range []string{"小標題", "第一段 粗體 與 連結", "項目一", "項目二", `第二段 & 符號 "引號"`} {
		if !strings.Contains(got, want) {
			t.Errorf("PlainTextParagraphs result lacks %q; got %q", want, got)
		}
	}
	for _, unwanted := range []string{"<", ">", "alert", "color:red", "&amp;", "&quot;"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("PlainTextParagraphs result contains %q; got %q", unwanted, got)
		}
	}
	first, second := strings.Index(got, "第一段"), strings.Index(got, "第二段")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("paragraph order lost; got %q", got)
	}
	if between := got[first:second]; !strings.Contains(between, "\n\n") {
		t.Errorf("no blank line between the first and second paragraph; got %q", got)
	}
	heading, para := strings.Index(got, "小標題"), strings.Index(got, "第一段")
	if heading < 0 || para < 0 || !strings.Contains(got[heading:para], "\n\n") {
		t.Errorf("no blank line between the heading and the first paragraph; got %q", got)
	}
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("more than one blank line between blocks; got %q", got)
	}
	if got != strings.TrimSpace(got) {
		t.Errorf("result has leading or trailing whitespace; got %q", got)
	}
}

func TestWebMCPPlainTextParagraphsEmpty(t *testing.T) {
	for _, source := range []string{"", "   ", "<p></p><script>x()</script>"} {
		if got := PlainTextParagraphs(source); got != "" {
			t.Errorf("PlainTextParagraphs(%q) = %q, want empty", source, got)
		}
	}
}
