package review

import (
	"path/filepath"
	"testing"
)

func TestPublishPrefix(t *testing.T) {
	rev := new("abc1234def5678")
	path := new("lib/Foo.pm")
	lines := func(a, b int) *Range { return &Range{a, b} }
	cases := []struct {
		name  string
		f     Feedback
		lines *Range
		want  string // "" for no prefix
	}{
		{"line new", Feedback{Target: "line", Revision: rev, Path: path, Line: new(int64(42)), Side: new("new")}, nil, "`lib/Foo.pm:42` (commit abc1234)"},
		{"line old", Feedback{Target: "line", Revision: rev, Path: path, Line: new(int64(7)), Side: new("old")}, nil, "`lib/Foo.pm:7` (old side, commit abc1234)"},
		{"section", Feedback{Target: "section", Revision: rev, Path: path, SectionName: new("sub bar")}, nil, "`lib/Foo.pm`, section `sub bar`"},
		{"file", Feedback{Target: "file", Revision: rev, Path: path}, nil, "`lib/Foo.pm`"},
		{"file was on section", Feedback{Target: "file", Revision: rev, Path: path, SectionName: new("sub bar")}, nil, "`lib/Foo.pm`, was on section `sub bar`"},
		{"file was on flag", Feedback{Target: "file", Revision: rev, Path: path, Flag: new("n1")}, nil, "`lib/Foo.pm`, was on a flag"},
		{"commit", Feedback{Target: "commit", Revision: rev}, nil, "commit abc1234"},
		{"commit topic", Feedback{Target: "commit", Revision: rev, Topic: new("Frontend")}, nil, "topic `Frontend` (commit abc1234)"},
		{"commit was on flag", Feedback{Target: "commit", Revision: rev, Flag: new("n1")}, nil, "commit abc1234, was on a flag"},
		{"review", Feedback{Target: "review"}, nil, ""},
		{"review topic", Feedback{Target: "review", Revision: rev, Topic: new("Frontend")}, nil, "was on topic `Frontend`"},
		{"review no topic yet", Feedback{Target: "review", Revision: rev, Topic: new("")}, nil, "was on the files not in a topic yet"},
		{"flag lines", Feedback{Target: "flag", Revision: rev, Path: path, Flag: new("n1")}, lines(12, 14), "`lib/Foo.pm:12-14` (commit abc1234), on a flag"},
		{"flag one line", Feedback{Target: "flag", Revision: rev, Path: path, Flag: new("n1")}, lines(12, 12), "`lib/Foo.pm:12` (commit abc1234), on a flag"},
		{"flag whole file", Feedback{Target: "flag", Revision: rev, Path: path, Flag: new("n1")}, nil, "`lib/Foo.pm`, on a flag"},
		{"flag topic", Feedback{Target: "flag", Revision: rev, Topic: new("Frontend"), Flag: new("n1")}, nil, "topic `Frontend` (commit abc1234), on a flag"},
		{"flag commit", Feedback{Target: "flag", Revision: rev, Flag: new("n1")}, nil, "commit abc1234, on a flag"},
	}
	for _, c := range cases {
		got, ok := publishPrefix(&c.f, c.lines)
		if ok != (c.want != "") || got != c.want {
			t.Errorf("%s: got %q (%v), want %q", c.name, got, ok, c.want)
		}
	}
}

// fitDB is a review file whose latest iteration's entries are given as
// revision and the paths each changes ("old>new" for a rename); merged
// marks the second entry as merged in.
func fitDB(t *testing.T, squashed bool, merged bool, entries ...[]string) *DB {
	t.Helper()
	d, err := CreateDB(filepath.Join(t.TempDir(), "r.sqlite"), NewReview{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := d.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO iteration (number, base, head, squashed, pin, created) VALUES (1, 'b0', 'h1', ?, 'p1', 1)", squashed)
	exec("INSERT INTO iteration (number, base, head, squashed, pin, created) VALUES (2, 'b0', 'h2', 0, 'p2', 2)")
	for i, e := range entries {
		var mergeID any
		if merged && i == 1 {
			mergeID = 1
		}
		exec("INSERT INTO commit_entry (id, iteration, position, merge_id, revision, subject, message) VALUES (?, 2, ?, ?, ?, 's', 'm')", i+1, i+1, mergeID, e[0])
		for _, p := range e[1:] {
			old, path := "", p
			for j := range len(p) {
				if p[j] == '>' {
					old, path = p[:j], p[j+1:]
				}
			}
			exec("INSERT INTO file_change (commit_id, path, old_path, status, kind, size_unit, split_source) VALUES (?, ?, ?, 'M', 'text', 'lines', 'none')", i+1, path, nullable(old))
		}
	}
	return d
}

func TestLineFits(t *testing.T) {
	cases := []struct {
		name     string
		squashed bool
		merged   bool
		entries  [][]string
		iter     int64
		rev      string
		path     string
		side     string
		want     bool
	}{
		{"new, no later change", false, false, [][]string{{"c1", "a"}, {"c2", "b"}}, 2, "c1", "a", "new", true},
		{"new, a later entry changes it", false, false, [][]string{{"c1", "a"}, {"c2", "a"}}, 2, "c1", "a", "new", false},
		{"old, a later entry changes it", false, false, [][]string{{"c1", "a"}, {"c2", "a"}}, 2, "c1", "a", "old", true},
		{"old, an earlier entry changes it", false, false, [][]string{{"c1", "a"}, {"c2", "a"}}, 2, "c2", "a", "old", false},
		{"old, an earlier entry renames to it", false, false, [][]string{{"c1", "z>a"}, {"c2", "a"}}, 2, "c2", "a", "old", false},
		{"old, an earlier entry changes the path it was renamed from", false, false, [][]string{{"c1", "z"}, {"c2", "z>a"}}, 2, "c2", "a", "old", false},
		{"revision not in the latest iteration", false, false, [][]string{{"c1", "a"}}, 2, "gone", "a", "new", false},
		{"merged in: an earlier entry counts for new", false, true, [][]string{{"c1", "a"}, {"c2", "a"}}, 2, "c2", "a", "new", false},
		{"squashed old side, same base", true, false, [][]string{{"c1", "a"}}, 1, "h1", "a", "old", true},
	}
	for _, c := range cases {
		d := fitDB(t, c.squashed, c.merged, c.entries...)
		latest, err := d.LatestIteration()
		if err != nil {
			t.Fatal(err)
		}
		f := &Feedback{Iteration: c.iter, Target: "line", Revision: &c.rev, Path: &c.path}
		got, err := d.lineFits(latest, f, c.side)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
