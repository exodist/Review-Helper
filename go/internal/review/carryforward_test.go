package review

import (
	"reflect"
	"testing"
)

func TestMoveFlagLines(t *testing.T) {
	// one two three four five six -> zero one two THREE four five six
	d, err := ParseDiff("@@ -0,0 +1 @@\n+zero\n@@ -3 +4 @@\n-three\n+THREE\n")
	if err != nil {
		t.Fatal(err)
	}
	moves := &lineMoves{path: "f", hunks: d.Hunks, ok: true}
	tests := []struct {
		name       string
		start, end int64
		m          *lineMoves
		want       []int64 // nil: whole file
		same       bool
	}{
		{"an unchanged line moves", 1, 2, moves, []int64{2, 3}, true},
		{"a changed line is gone: whole file", 3, 3, moves, nil, false},
		{"a span keeps the lines that survived", 2, 4, moves, []int64{3, 5}, false},
		{"lines after the change move", 5, 6, moves, []int64{6, 7}, true},
		{"a file that cannot be compared", 1, 1, &lineMoves{path: "f"}, nil, false},
		{"no diff: the same lines", 2, 3, &lineMoves{path: "f", ok: true}, []int64{2, 3}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &Flag{LineStart: &tt.start, LineEnd: &tt.end}
			same := moveFlagLines(f, tt.m)
			var got []int64
			if f.LineStart != nil {
				got = []int64{*f.LineStart, *f.LineEnd}
			}
			if same != tt.same || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("moveFlagLines = %v %v, want %v %v", got, same, tt.want, tt.same)
			}
		})
	}
}

func TestPairer(t *testing.T) {
	id := func(s string) *string { return &s }
	old := []*CommitEntry{
		{ID: 1, Revision: "r1", ChangeID: id("p")},
		{ID: 2, Revision: "r2", ChangeID: id("p")},
		{ID: 3, Revision: "r3"},
	}
	pick := pairer(old, false)
	var got []int64
	for _, e := range []*newEntry{
		{CommitEntry: CommitEntry{Revision: "n1", ChangeID: id("p")}},
		{CommitEntry: CommitEntry{Revision: "n2", ChangeID: id("p")}},
		{CommitEntry: CommitEntry{Revision: "n3", ChangeID: id("p")}},
		{CommitEntry: CommitEntry{Revision: "r3"}},
		{CommitEntry: CommitEntry{Revision: "r1"}},
	} {
		var n int64
		if o := pick(e); o != nil {
			n = o.ID
		}
		got = append(got, n)
	}
	if want := []int64{1, 2, 0, 3, 0}; !reflect.DeepEqual(got, want) {
		t.Errorf("pairs %v, want %v", got, want)
	}
	if o := pairer(old, true)(&newEntry{CommitEntry: CommitEntry{Revision: "r3"}}); o != nil {
		t.Errorf("a squashed iteration pairs no commit by revision, got %d", o.ID)
	}
}

func TestSameSquash(t *testing.T) {
	prev := &Iteration{Squashed: true, Base: "b", Paths: []string{"x"}}
	tests := []struct {
		name string
		sp   *span
		want bool
	}{
		{"same base and paths", &span{Squashed: true, Base: "b", Paths: []string{"x"}}, true},
		{"other paths", &span{Squashed: true, Base: "b", Paths: []string{"y"}}, false},
		{"all paths", &span{Squashed: true, Base: "b"}, false},
		{"other base", &span{Squashed: true, Base: "c", Paths: []string{"x"}}, false},
		{"not squashed", &span{Base: "b", Paths: []string{"x"}}, false},
	}
	for _, tt := range tests {
		if got := sameSquash(prev, tt.sp); got != tt.want {
			t.Errorf("%s: sameSquash = %v, want %v", tt.name, got, tt.want)
		}
	}
}
