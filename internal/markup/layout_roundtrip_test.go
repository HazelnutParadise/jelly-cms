package markup

// Layout mode contract (docs/tickets/09-editor.md, G3 版面模式, 語法表). The editor reads Block line
// ranges from Render, moves a block by cutting its source lines and pasting them after another block,
// and rewrites only the opener line of a container. These tests check those three guarantees. They are
// written before the renderer exists and are expected to fail against the skeleton. Do not edit them to
// match an implementation.
//
// Assumption: Result.Blocks lists nested containers too, in document order with a parent before its
// children (pre-order). The types.go comment says "top-level"; the ticket's layout mode needs the
// nested col and item blocks as well, so the comment must change with the implementation.

import (
	"regexp"
	"strings"
	"testing"
)

// lrtDoc is the layout fixture. The comments give the 1-based line numbers that the assertions use.
const lrtDoc = `:::hero {bg=mint}
歡迎來到網站
:::

:::columns {ratio=2:1}
::: col
左欄
:::
::: col
右欄
:::
:::

一般段落。

:::faq
::: item {title="Q1"}
A1
:::
:::
`

// lrtOpenerDoc is a hero whose opener carries an unknown attribute and a trailing comment.
const lrtOpenerDoc = ":::hero {bg=mint colour=blue} <!-- note -->\n歡迎\n:::\n"

// lrtBlockWant is one block that the coverage test expects, matched by kind, name and line range. An
// empty name matches any name.
type lrtBlockWant struct {
	kind, name string
	start, end int
}

func lrtMatches(b Block, w lrtBlockWant) bool {
	return b.Kind == w.kind && (w.name == "" || b.Name == w.name) && b.StartLine == w.start && b.EndLine == w.end
}

// lrtIndex returns the position of the first block that matches w, or -1.
func lrtIndex(blocks []Block, w lrtBlockWant) int {
	for i, b := range blocks {
		if lrtMatches(b, w) {
			return i
		}
	}
	return -1
}

// lrtParent returns the index of the innermost block that strictly contains blocks[i], or -1 when none
// does. A block with exactly the same range is a sibling, not a parent.
func lrtParent(blocks []Block, i int) int {
	child := blocks[i]
	parent := -1
	for j, b := range blocks {
		if j == i || b.StartLine > child.StartLine || b.EndLine < child.EndLine {
			continue
		}
		if b.StartLine == child.StartLine && b.EndLine == child.EndLine {
			continue
		}
		if parent == -1 || b.EndLine-b.StartLine < blocks[parent].EndLine-blocks[parent].StartLine {
			parent = j
		}
	}
	return parent
}

// lrtMoveBlock cuts the lines of moved and pastes them after the lines of target. It uses only the
// StartLine and EndLine values from Render, which is all the layout mode has.
func lrtMoveBlock(source string, moved, target Block) string {
	lines := strings.Split(source, "\n")
	cut := append([]string(nil), lines[moved.StartLine-1:moved.EndLine]...)
	rest := append(append([]string(nil), lines[:moved.StartLine-1]...), lines[moved.EndLine:]...)
	insertAfter := target.EndLine
	if target.StartLine > moved.EndLine {
		insertAfter -= len(cut)
	}
	out := append([]string(nil), rest[:insertAfter]...)
	out = append(out, cut...)
	out = append(out, rest[insertAfter:]...)
	return strings.Join(out, "\n")
}

// lrtOutside returns the lines of source that lie outside every skipped block, joined by newlines.
func lrtOutside(source string, skip ...Block) string {
	var keep []string
	for i, line := range strings.Split(source, "\n") {
		n := i + 1
		dropped := false
		for _, b := range skip {
			if n >= b.StartLine && n <= b.EndLine {
				dropped = true
				break
			}
		}
		if !dropped {
			keep = append(keep, line)
		}
	}
	return strings.Join(keep, "\n")
}

// lrtDiagnosticWithCode returns the first diagnostic with the given code.
func lrtDiagnosticWithCode(diags []Diagnostic, code string) (Diagnostic, bool) {
	for _, d := range diags {
		if d.Code == code {
			return d, true
		}
	}
	return Diagnostic{}, false
}

func TestBlocksLineRangesCoverDocument(t *testing.T) {
	r := mtRender(t, lrtDoc, TrustRich)

	top := []lrtBlockWant{
		{"container", "hero", 1, 3},
		{"container", "columns", 5, 12},
		{"paragraph", "", 14, 14},
		{"container", "faq", 16, 20},
	}
	nested := []lrtBlockWant{
		{"container", "col", 6, 8},
		{"container", "col", 9, 11},
		{"container", "item", 17, 19},
	}
	parentName := map[string]string{"col": "columns", "item": "faq"}

	for _, w := range append(append([]lrtBlockWant(nil), top...), nested...) {
		if lrtIndex(r.Blocks, w) < 0 {
			t.Errorf("no block kind=%s name=%q at lines %d-%d in %+v", w.kind, w.name, w.start, w.end, r.Blocks)
		}
	}

	var roots []Block
	for i, b := range r.Blocks {
		if lrtParent(r.Blocks, i) == -1 {
			roots = append(roots, b)
		}
	}
	if len(roots) != len(top) {
		t.Errorf("top-level blocks = %d, want %d: %+v", len(roots), len(top), roots)
	}
	for _, w := range top {
		if lrtIndex(roots, w) < 0 {
			t.Errorf("top-level block kind=%s name=%q lines %d-%d is missing: %+v", w.kind, w.name, w.start, w.end, roots)
		}
	}

	lines := strings.Split(lrtDoc, "\n")
	for n, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		covered := false
		for _, b := range roots {
			if b.StartLine <= n+1 && n+1 <= b.EndLine {
				covered = true
				break
			}
		}
		if !covered {
			t.Errorf("line %d (%q) is not covered by a top-level block", n+1, line)
		}
	}

	for _, w := range nested {
		i := lrtIndex(r.Blocks, w)
		if i < 0 {
			continue
		}
		p := lrtParent(r.Blocks, i)
		if p < 0 {
			t.Errorf("nested block %q at %d-%d has no parent block", w.name, w.start, w.end)
			continue
		}
		if p > i {
			t.Errorf("parent %+v is listed after its child %+v", r.Blocks[p], r.Blocks[i])
		}
		if got := r.Blocks[p].Name; got != parentName[w.name] {
			t.Errorf("parent of %q block is %q, want %q", w.name, got, parentName[w.name])
		}
	}

	for i := 1; i < len(r.Blocks); i++ {
		if r.Blocks[i].StartLine < r.Blocks[i-1].StartLine {
			t.Errorf("blocks are not in document order: %+v comes after %+v", r.Blocks[i], r.Blocks[i-1])
		}
	}
}

func TestSpliceMoveBlockIsLossless(t *testing.T) {
	cases := []struct{ name, moved, target string }{
		{"hero pasted after faq", "hero", "faq"},
		{"faq pasted after hero", "faq", "hero"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := mtRender(t, lrtDoc, TrustRich)
			moved, ok := mtFindBlock(before.Blocks, c.moved)
			if !ok {
				t.Fatalf("no %s block in %+v", c.moved, before.Blocks)
			}
			target, ok := mtFindBlock(before.Blocks, c.target)
			if !ok {
				t.Fatalf("no %s block in %+v", c.target, before.Blocks)
			}
			cutText := strings.Join(strings.Split(lrtDoc, "\n")[moved.StartLine-1:moved.EndLine], "\n")

			edited := lrtMoveBlock(lrtDoc, moved, target)

			after := mtRender(t, edited, TrustRich)
			newMoved, ok := mtFindBlock(after.Blocks, c.moved)
			if !ok {
				t.Fatalf("after the move there is no %s block in %+v", c.moved, after.Blocks)
			}
			newTarget, ok := mtFindBlock(after.Blocks, c.target)
			if !ok {
				t.Fatalf("after the move there is no %s block in %+v", c.target, after.Blocks)
			}

			wantOutside := lrtOutside(lrtDoc, moved, target)
			gotOutside := lrtOutside(edited, newMoved, newTarget)
			if gotOutside != wantOutside {
				t.Errorf("bytes outside the two touched ranges changed:\n got: %q\nwant: %q", gotOutside, wantOutside)
			}

			gotText := strings.Join(strings.Split(edited, "\n")[newMoved.StartLine-1:newMoved.EndLine], "\n")
			if gotText != cutText {
				t.Errorf("moved block text changed:\n got: %q\nwant: %q", gotText, cutText)
			}
		})
	}
}

// lrtSetOpenerAttr is the inspector edit. It rewrites one key inside the {…} group of the block's
// opener line and changes nothing else on that line or on any other line.
func lrtSetOpenerAttr(source string, b Block, key, value string) string {
	braces := regexp.MustCompile(`\{[^}]*\}`)
	attr := regexp.MustCompile(`(^|\s)` + regexp.QuoteMeta(key) + `=[^\s}]*`)
	lines := strings.Split(source, "\n")
	lines[b.StartLine-1] = braces.ReplaceAllStringFunc(lines[b.StartLine-1], func(group string) string {
		return attr.ReplaceAllString(group, "${1}"+key+"="+value)
	})
	return strings.Join(lines, "\n")
}

func TestInspectorEditsOnlyOpenerLine(t *testing.T) {
	before := mtRender(t, lrtOpenerDoc, TrustRich)
	hero, ok := mtFindBlock(before.Blocks, "hero")
	if !ok {
		t.Fatalf("no hero block in %+v", before.Blocks)
	}

	edited := lrtSetOpenerAttr(lrtOpenerDoc, hero, "bg", "accent")

	oldLines := strings.Split(lrtOpenerDoc, "\n")
	newLines := strings.Split(edited, "\n")
	if len(oldLines) != len(newLines) {
		t.Fatalf("the inspector edit changed the line count: %d lines became %d", len(oldLines), len(newLines))
	}
	var changed []int
	for i := range oldLines {
		if oldLines[i] != newLines[i] {
			changed = append(changed, i+1)
		}
	}
	if len(changed) != 1 || changed[0] != hero.StartLine {
		t.Fatalf("lines changed = %v, want only the opener line %d", changed, hero.StartLine)
	}

	opener := newLines[hero.StartLine-1]
	for _, want := range []string{"bg=accent", "colour=blue", "<!-- note -->"} {
		if !strings.Contains(opener, want) {
			t.Errorf("opener line after the edit lacks %q: %q", want, opener)
		}
	}
	if strings.Contains(opener, "bg=mint") {
		t.Errorf("opener line still has bg=mint: %q", opener)
	}

	after := mtRender(t, edited, TrustRich)
	again, ok := mtFindBlock(after.Blocks, "hero")
	if !ok {
		t.Fatalf("no hero block after the edit in %+v", after.Blocks)
	}
	if again.Attrs["bg"] != "accent" {
		t.Errorf("hero bg after the edit = %q, want accent", again.Attrs["bg"])
	}
	if !strings.Contains(after.HTML, `data-bg="accent"`) {
		t.Errorf("rendered hero must carry data-bg=\"accent\": %s", after.HTML)
	}
}

func TestUnknownAttributeValuesDroppedWithDiagnostic(t *testing.T) {
	r := mtRender(t, ":::hero {height=huge}\n內容\n:::\n", TrustRich)

	if strings.Contains(r.HTML, "data-height") || strings.Contains(r.HTML, "huge") {
		t.Errorf("an unknown height value must be dropped from the output: %s", r.HTML)
	}
	d, ok := lrtDiagnosticWithCode(r.Diagnostics, "attr_value")
	if !ok {
		t.Fatalf("an unknown height value must produce an attr_value diagnostic: %+v", r.Diagnostics)
	}
	if d.Line != 1 {
		t.Errorf("attr_value diagnostic line = %d, want 1 (the opener line)", d.Line)
	}
}

func TestFlavorAliasesAccepted(t *testing.T) {
	t.Run("raspberry alias is kept as given", func(t *testing.T) {
		r := mtRender(t, ":::hero {bg=raspberry}\n內容\n:::\n", TrustRich)
		if !strings.Contains(r.HTML, `data-bg="raspberry"`) {
			t.Errorf("alias raspberry must be kept as data-bg=\"raspberry\": %s", r.HTML)
		}
		if _, ok := lrtDiagnosticWithCode(r.Diagnostics, "attr_value"); ok {
			t.Errorf("an accepted alias must not produce an attr_value diagnostic: %+v", r.Diagnostics)
		}
	})

	t.Run("primary is kept", func(t *testing.T) {
		r := mtRender(t, ":::hero {bg=primary}\n內容\n:::\n", TrustRich)
		if !strings.Contains(r.HTML, `data-bg="primary"`) {
			t.Errorf("flavor primary must be kept as data-bg=\"primary\": %s", r.HTML)
		}
		if _, ok := lrtDiagnosticWithCode(r.Diagnostics, "attr_value"); ok {
			t.Errorf("an accepted flavor must not produce an attr_value diagnostic: %+v", r.Diagnostics)
		}
	})

	t.Run("hex colour is dropped with a diagnostic", func(t *testing.T) {
		r := mtRender(t, ":::hero {bg=#fff}\n內容\n:::\n", TrustRich)
		if strings.Contains(r.HTML, "data-bg") {
			t.Errorf("a hex colour is not a flavor and must be dropped: %s", r.HTML)
		}
		if _, ok := lrtDiagnosticWithCode(r.Diagnostics, "attr_value"); !ok {
			t.Errorf("a dropped hex colour must produce an attr_value diagnostic: %+v", r.Diagnostics)
		}
	})
}
