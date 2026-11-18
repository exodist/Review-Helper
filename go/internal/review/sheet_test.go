package review

import (
	"encoding/json"
	"testing"
)

// The conformance checks drive sheet and fill end to end; these pin what
// they cannot: the rev both backends share and how a line range reads.

func TestRevMatchesPerlBackend(t *testing.T) {
	// The revs the Perl backend gave these items. It writes U+2028 and
	// U+2029 raw, where Go's encoder escapes them; an escaped backslash
	// before u2028 is not one.
	for title, want := range map[string]string{
		"T e s t é":               "fbc3e9e8b4da76461850b045d87817305ebd141707819bdef4123a9bae8c393d",
		"a\u2028b\u2029c \\u2028": "16c65edf201e5ae07d0234271dffb2f5fc85b642a1f9b14917d7cb5df828047a",
	} {
		about := SummaryAbout{Title: &title, Base: "5e69bad9e6397839d7a7e98f2a9b1a7c4d52f44b", Head: "f96a45930297beed80bd9a040ae91342f3887dbe"}
		got, err := rev(about, TextFields{})
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%q: rev %s, want %s", title, got, want)
		}
	}
}

func TestLineRange(t *testing.T) {
	for _, c := range []struct {
		in      string
		want    Range
		problem string
	}{
		{`[1, 3]`, Range{1, 3}, ""},
		{`["2", 2]`, Range{2, 2}, ""},
		{`[0, 3]`, Range{}, "must be [start, end]"},
		{`["01", 3]`, Range{}, "must be [start, end]"},
		{`[1.5, 3]`, Range{}, "must be [start, end]"},
		{`[1]`, Range{}, "must be [start, end]"},
		{`{"a": 1}`, Range{}, "must be [start, end]"},
		{`[4, 3]`, Range{}, "start is after end"},
	} {
		got, problem := lineRange(json.RawMessage(c.in))
		if got != c.want || problem != c.problem {
			t.Errorf("%s: got %v %q, want %v %q", c.in, got, problem, c.want, c.problem)
		}
	}
}
