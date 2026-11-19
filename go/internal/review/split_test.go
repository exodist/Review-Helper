package review

import (
	"reflect"
	"testing"
)

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
