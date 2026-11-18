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

func TestAttributeAndOwners(t *testing.T) {
	d, _ := ParseDiff(sampleDiff)
	a := &Span{Ranges: [][2]int{{1, 3}}}
	b := &Span{Ranges: [][2]int{{4, 12}}}
	gone := &Span{OldRanges: [][2]int{{10, 11}}, Deleted: true}
	Attribute(d.Hunks, []*Span{a, b, gone})
	if a.Counts != (Counts{Added: 1, Changed: 1}) || !a.Touched {
		t.Fatalf("a %+v", a)
	}
	if b.Touched || b.Counts != (Counts{}) {
		t.Fatalf("b %+v", b)
	}
	if gone.Counts != (Counts{Removed: 2}) || !gone.Touched {
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

func TestAssembleAndKinds(t *testing.T) {
	lines := []string{"package Foo;", "", "sub a {", "}", "", "1;"}
	pkg := "Foo"
	units := []*Unit{
		{Name: "sub a", Package: &pkg, Ranges: [][2]int{{3, 4}}},
		{Name: "header", Package: &pkg, Ranges: [][2]int{{1, 1}}},
		{Name: "end", Package: &pkg, Ranges: [][2]int{{6, 6}}},
	}
	out, err := Assemble(units, lines)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Name != "header" || !reflect.DeepEqual(out[0].Ranges, [][2]int{{1, 2}}) || !reflect.DeepEqual(out[1].Ranges, [][2]int{{3, 5}}) {
		t.Fatalf("assembled %+v %+v", out[0], out[1])
	}
	if _, err := Assemble(units[:1], lines); err == nil {
		t.Fatal("a line with text in no section passes")
	}
	for p, want := range map[string]string{"lib/A.pm": "perl", "Makefile": "makefile", "x.PNG": "image", "bin/tool": "text"} {
		if got := KindFor(p, ""); got != want {
			t.Errorf("%s: %s", p, got)
		}
	}
	if got := KindFor("bin/tool", "#!/usr/bin/env python3"); got != "python" {
		t.Errorf("shebang: %s", got)
	}
}
