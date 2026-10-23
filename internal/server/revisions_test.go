package server

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Unit tests for LineDiff (handlers_revisions.go). No database is needed.

func TestLineDiffReportsRemovedAndAddedLines(t *testing.T) {
	got := LineDiff("a\nb\nc", "a\nc\nd")
	want := []DiffOp{{Op: "=", Text: "a"}, {Op: "-", Text: "b"}, {Op: "=", Text: "c"}, {Op: "+", Text: "d"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LineDiff = %+v, want %+v", got, want)
	}
}

func TestLineDiffIdenticalInputsAreAllEqual(t *testing.T) {
	got := LineDiff("a\nb\nc", "a\nb\nc")
	want := []DiffOp{{Op: "=", Text: "a"}, {Op: "=", Text: "b"}, {Op: "=", Text: "c"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LineDiff = %+v, want %+v", got, want)
	}
}

func TestLineDiffEmptyOldSideIsAllAdded(t *testing.T) {
	got := LineDiff("", "x\ny")
	want := []DiffOp{{Op: "+", Text: "x"}, {Op: "+", Text: "y"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LineDiff = %+v, want %+v", got, want)
	}
}

func TestLineDiffEmptyNewSideIsAllRemoved(t *testing.T) {
	got := LineDiff("x\ny", "")
	want := []DiffOp{{Op: "-", Text: "x"}, {Op: "-", Text: "y"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LineDiff = %+v, want %+v", got, want)
	}
}

// revTestLines splits s the way a diff sees it: an empty string has no lines.
func revTestLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// revTestCheckReconstructs verifies that the ops, read back, reproduce both inputs exactly.
func revTestCheckReconstructs(t *testing.T, a, b string, ops []DiffOp) {
	t.Helper()
	var oldSide, newSide []string
	for _, op := range ops {
		switch op.Op {
		case "=":
			oldSide = append(oldSide, op.Text)
			newSide = append(newSide, op.Text)
		case "-":
			oldSide = append(oldSide, op.Text)
		case "+":
			newSide = append(newSide, op.Text)
		default:
			t.Fatalf("LineDiff produced unknown op %q", op.Op)
		}
	}
	if !reflect.DeepEqual(oldSide, revTestLines(a)) {
		t.Errorf("old side of diff does not reproduce input a")
	}
	if !reflect.DeepEqual(newSide, revTestLines(b)) {
		t.Errorf("new side of diff does not reproduce input b")
	}
}

func TestLineDiffOpsReproduceBothInputs(t *testing.T) {
	cases := []struct{ a, b string }{
		{"x\ny\nz\nw", "y\nq\nz\nr\ns"},
		{"same\nlines", "same\nlines"},
		{"", ""},
		{"only old", ""},
		{"", "only new"},
	}
	for _, c := range cases {
		revTestCheckReconstructs(t, c.a, c.b, LineDiff(c.a, c.b))
	}
}

func TestLineDiffLargeInputsStillReproduceBothSides(t *testing.T) {
	// Above the 20k-line bound the handler documents a fallback, so only the
	// reconstruction property is asserted here, not the exact op sequence.
	const lines = 20001
	var a, b strings.Builder
	for i := 0; i < lines; i++ {
		if i > 0 {
			a.WriteByte('\n')
			b.WriteByte('\n')
		}
		fmt.Fprintf(&a, "line %d", i)
		if i%7 == 3 {
			fmt.Fprintf(&b, "edited %d", i)
		} else {
			fmt.Fprintf(&b, "line %d", i)
		}
	}
	revTestCheckReconstructs(t, a.String(), b.String(), LineDiff(a.String(), b.String()))
}
