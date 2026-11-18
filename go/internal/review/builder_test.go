package review

import (
	"reflect"
	"testing"
)

func TestMergeOwners(t *testing.T) {
	tests := []struct {
		name    string
		parents map[string][]string // a commit's parents; base is outside the range
		order   []string
		head    string
		want    map[string]string
	}{
		{
			name:    "linear history has no merges",
			parents: map[string][]string{"a": {"base"}, "b": {"a"}},
			order:   []string{"a", "b"}, head: "b",
			want: map[string]string{},
		},
		{
			name: "a merge takes its branch's commits",
			// base - a - m
			//    \- b1 - b2 /
			parents: map[string][]string{"a": {"base"}, "b1": {"base"}, "b2": {"b1"}, "m": {"a", "b2"}},
			order:   []string{"a", "b1", "b2", "m"}, head: "m",
			want: map[string]string{"b1": "m", "b2": "m"},
		},
		{
			name: "a merge inside a branch keeps its own commits",
			// topic: t1 - t2 - mi(t2, i1), merged into the mainline by mo
			parents: map[string][]string{
				"a": {"base"}, "t1": {"base"}, "i1": {"t1"}, "t2": {"t1"}, "mi": {"t2", "i1"}, "mo": {"a", "mi"},
			},
			order: []string{"a", "t1", "i1", "t2", "mi", "mo"}, head: "mo",
			want: map[string]string{"t1": "mo", "t2": "mo", "mi": "mo", "i1": "mi"},
		},
		{
			name: "a back-merge of the mainline into a branch takes nothing",
			// a on the mainline is merged into the branch (bm), which mo merges back
			parents: map[string][]string{
				"a": {"base"}, "b1": {"base"}, "bm": {"b1", "a"}, "mo": {"a", "bm"},
			},
			order: []string{"a", "b1", "bm", "mo"}, head: "mo",
			want: map[string]string{"b1": "mo", "bm": "mo"},
		},
		{
			name: "a merge on the mainline, then a plain commit",
			// m merges c, a commit off a; h follows m on the mainline
			parents: map[string][]string{"a": {"base"}, "c": {"a"}, "m": {"a", "c"}, "h": {"m"}},
			order:   []string{"a", "c", "m", "h"}, head: "h",
			want: map[string]string{"c": "m"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeOwners(tt.parents, tt.order, tt.head)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("mergeOwners = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDropUntouched(t *testing.T) {
	file := []*newFile{{FileChange: FileChange{Path: "x"}}}
	entry := func(rev, merge string, files []*newFile) *newEntry {
		return &newEntry{CommitEntry: CommitEntry{Revision: rev}, mergeRevision: merge, Files: files}
	}
	entries := []*newEntry{
		entry("a", "", file),    // kept: changes a matching file
		entry("b", "", nil),     // dropped: changes none
		entry("i1", "mi", file), // kept, so its merge mi is kept
		entry("mi", "mo", nil),  // kept: a merge with a commit, so mo is kept
		entry("mo", "", nil),    // kept
		entry("e1", "me", nil),  // dropped
		entry("me", "", nil),    // dropped: a merge left with no commits
		entry("last", "", file), // kept
	}
	var got []string
	for _, e := range dropUntouched(entries) {
		got = append(got, e.Revision)
	}
	want := []string{"a", "i1", "mi", "mo", "last"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dropUntouched kept %v, want %v", got, want)
	}
}

func TestReviewFileName(t *testing.T) {
	const rev = "0123456789abcdef0123456789abcdef01234567"
	tests := []struct{ name, ref, want string }{
		{"no name", "", "0123456789ab"},
		{"a branch", "main", "main"},
		{"slashes become dashes", "feature/x", "feature-x"},
		{"a name with other characters", "we ird", "0123456789ab"},
		{"an abbreviated id", "0123456", "0123456789ab"},
	}
	for _, tt := range tests {
		if got := reviewFileName(rev, tt.ref); got != tt.want {
			t.Errorf("%s: reviewFileName(%q) = %q, want %q", tt.name, tt.ref, got, tt.want)
		}
	}
}

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
