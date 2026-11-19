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
