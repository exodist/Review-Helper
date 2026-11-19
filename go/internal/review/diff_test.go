package review

import (
	"reflect"
	"testing"
)

const sampleDiff = `diff --git a/f b/f
index 1..2 100644
--- a/f
+++ b/f
@@ -1,4 +1,5 @@
 one
-two
+TWO
+extra
 three

@@ -10,2 +11,0 @@
-ten
-eleven
\ No newline at end of file
`

func TestParseAndCount(t *testing.T) {
	d, err := ParseDiff(sampleDiff)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Hunks) != 2 || d.Binary {
		t.Fatalf("hunks %d binary %v", len(d.Hunks), d.Binary)
	}
	// The blank line inside the first hunk is a context line.
	if got := d.Hunks[0].Lines[5]; got.Type != " " || got.Text != "" || *got.Old != 4 || *got.New != 5 {
		t.Fatalf("blank context line: %+v", got)
	}
	if c := CountLines(d.Hunks); c != (Counts{Added: 1, Removed: 2, Changed: 1}) {
		t.Fatalf("counts %+v", c)
	}
	bin, _ := ParseDiff("Binary files a/x and b/x differ\n")
	if !bin.Binary || len(bin.Hunks) != 0 {
		t.Fatalf("binary %+v", bin)
	}
}

// Under diff.suppressBlankEmpty git prints a blank context line as an
// empty line, which at the end of the text must still be read.
func TestParseTrailingBlankContext(t *testing.T) {
	d, err := ParseDiff("@@ -1,4 +1,4 @@\n a\n b\n-c\n+C\n\n")
	if err != nil {
		t.Fatal(err)
	}
	lines := d.Hunks[0].Lines
	if len(lines) != 5 {
		t.Fatalf("%d lines, want 5", len(lines))
	}
	if last := lines[4]; last.Type != " " || last.Text != "" || *last.Old != 4 || *last.New != 4 {
		t.Fatalf("last line %+v", last)
	}
}

func TestAttributeAndOwners(t *testing.T) {
	d, _ := ParseDiff(sampleDiff)
	a := &Span{Ranges: [][2]int{{1, 3}}}
	b := &Span{Ranges: [][2]int{{4, 12}}}
	gone := &Span{OldRanges: [][2]int{{10, 11}}, Deleted: true}
	Attribute(d.Hunks, []*Span{a, b, gone})
	if a.Counts != (Counts{Added: 1, Changed: 1}) {
		t.Fatalf("a %+v", a)
	}
	if b.Counts != (Counts{}) {
		t.Fatalf("b %+v", b)
	}
	if gone.Counts != (Counts{Removed: 2}) {
		t.Fatalf("gone %+v", gone)
	}
}

func TestMapAndMove(t *testing.T) {
	d, _ := ParseDiff(sampleDiff)
	if s, e, ok := MapLines(d.Hunks, 3, 4); !ok || s != 4 || e != 5 {
		t.Fatalf("map 3-4: %d %d %v", s, e, ok)
	}
	if _, _, ok := MapLines(d.Hunks, 2, 3); ok {
		t.Fatal("a removed line maps")
	}
	moved, changed := MoveRanges(d.Hunks, [][2]int{{1, 3}})
	if !changed || !reflect.DeepEqual(moved, [][2]int{{1, 1}, {4, 4}}) {
		t.Fatalf("move %v %v", moved, changed)
	}
}

func TestSectionHunks(t *testing.T) {
	d, _ := ParseDiff(sampleDiff)
	newLines := []string{"one", "TWO", "extra", "three", ""}
	// Code and documentation runs with nothing between them read as one hunk.
	a := &Span{Ranges: [][2]int{{1, 3}}, DocRanges: [][2]int{{4, 4}}}
	hs := SectionHunks(d.Hunks, newLines, []*Span{a}, 0)
	if len(hs) != 1 || hs[0].Part != "code" || len(hs[0].Lines) != 5 {
		t.Fatalf("joined %+v", hs)
	}
	if hs[0].OldStart != 1 || hs[0].OldCount != 3 || hs[0].NewStart != 1 || hs[0].NewCount != 4 {
		t.Fatalf("hunk %+v", hs[0])
	}
	// Apart by a line with text, they stay two hunks.
	b := &Span{Ranges: [][2]int{{1, 2}}, DocRanges: [][2]int{{4, 4}}}
	hs = SectionHunks(d.Hunks, newLines, []*Span{b}, 0)
	if len(hs) != 2 || hs[0].Part != "code" || hs[1].Part != "docs" {
		t.Fatalf("parts %+v", hs)
	}
}
