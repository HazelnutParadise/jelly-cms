package markup

// Flatten contract (docs/tickets/09-editor.md, 匯出規則: md export). Flatten turns Jelly MD into
// plain Markdown by following the AST line ranges. Cases with an exact want compare after line
// normalisation (trailing spaces removed, runs of blank lines collapsed to one). Cases without an
// exact want check that the required pieces are present and the Jelly syntax is gone. Written before
// the flattener exists; do not edit to match an implementation.

import (
	"strings"
	"testing"
)

type mtFlattenCase struct {
	name     string
	source   string
	want     string   // exact expected output after mtFlatNorm; empty when contains/absent apply
	contains []string // substrings that must appear
	absent   []string // substrings that must not appear
}

const mtFence = "```"

var mtFlattenCases = []mtFlattenCase{
	{
		name:   "columns become consecutive paragraphs",
		source: ":::columns\n::: col\nA\n:::\n::: col\nB\n:::\n:::\n",
		want:   "A\n\nB",
	},
	{
		name:   "callout becomes a Note quote",
		source: "> [!NOTE]\n> x\n",
		want:   "> **Note**\n>\n> x",
	},
	{
		name: "yaml table becomes a pipe table",
		source: mtFence + "table\n" +
			"align: [null, right]\n" +
			"columns: [品項, 價格]\n" +
			"rows:\n" +
			"  - [咖啡, \"120\"]\n" +
			"  - [茶, \"60\"]\n" +
			"header_col: false\n" +
			mtFence + "\n",
		want: "| 品項 | 價格 |\n| --- | ---: |\n| 咖啡 | 120 |\n| 茶 | 60 |",
	},
	{
		name:   "tabs become level-3 headings",
		source: ":::tabs\n::: tab {title=\"甲\"}\n內容甲\n:::\n::: tab {title=\"乙\"}\n內容乙\n:::\n:::\n",
		want:   "### 甲\n\n內容甲\n\n### 乙\n\n內容乙",
	},
	{
		name:   "attributes and motion are stripped",
		source: "## 標題{reveal=fade-up #x .y key=value}\n",
		want:   "## 標題",
	},
	{
		name:   "html fence is kept",
		source: mtFence + "html\n<div>x</div>\n" + mtFence + "\n",
		want:   mtFence + "html\n<div>x</div>\n" + mtFence,
	},
	{
		name:   "latex fence is kept",
		source: mtFence + "latex\n\\frac{a}{b}\n" + mtFence + "\n",
		want:   mtFence + "latex\n\\frac{a}{b}\n" + mtFence,
	},
	{
		name:   "inline and display math are kept",
		source: "行內 $a_b$\n\n$$x^2$$\n",
		want:   "行內 $a_b$\n\n$$x^2$$",
	},
	{
		name:     "widget becomes a link",
		source:   "::widget{name=countdown to=\"2030-01-01\"}\n",
		contains: []string{"](", "countdown"},
		absent:   []string{"::widget"},
	},
	{
		name:     "embed becomes a link",
		source:   "::embed{url=https://www.youtube-nocookie.com/embed/abc123}\n",
		contains: []string{"](https://www.youtube-nocookie.com/embed/abc123)"},
		absent:   []string{"::embed", "iframe"},
	},
}

// mtFlatNorm trims trailing spaces on every line, collapses runs of blank lines to one, and trims the
// whole text. It keeps the layout otherwise, so paragraph breaks still count.
func mtFlatNorm(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if line == "" {
			if blank {
				continue
			}
			blank = true
		} else {
			blank = false
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func TestFlattenGolden(t *testing.T) {
	for _, c := range mtFlattenCases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Flatten(c.source)
			if err != nil {
				t.Fatalf("Flatten: %v", err)
			}
			if c.want != "" {
				if mtFlatNorm(got) != mtFlatNorm(c.want) {
					t.Errorf("Flatten output mismatch\n got: %q\nwant: %q", mtFlatNorm(got), mtFlatNorm(c.want))
				}
				return
			}
			for _, s := range c.contains {
				if !strings.Contains(got, s) {
					t.Errorf("Flatten output must contain %q: %q", s, got)
				}
			}
			for _, s := range c.absent {
				if strings.Contains(got, s) {
					t.Errorf("Flatten output must not contain %q: %q", s, got)
				}
			}
		})
	}
}
