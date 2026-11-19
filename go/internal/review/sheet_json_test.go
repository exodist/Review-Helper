package review

import (
	"encoding/json"
	"testing"
)

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
