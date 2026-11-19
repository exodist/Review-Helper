package review

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The refusal cases of a split's sections, and how they are laid out.

func TestCheckUnit(t *testing.T) {
	size := func(n int64) *int64 { return &n }
	f := &sheetFile{FileChange: &FileChange{Path: "a.pl", SizeUnit: "lines", SizeBefore: size(3), SizeAfter: size(4)}}
	texts := &fileTexts{before: []string{"sub old {", "}", ""}, after: []string{"sub a {", "  1;", "}", "1;"}}
	for _, c := range []struct {
		name     string
		units    []string
		problems []string
	}{
		{"valid, names trimmed, first line whitespace ignored",
			[]string{`{"name": " sub a ", "package": " A ", "lines": [[1, 3]], "first_line": "  sub a {  ", "old_lines": [[1, 2]], "old_first_line": "sub old {"}`}, nil},
		{"a number names a unit",
			[]string{`{"name": 7, "lines": [[4, 4]], "first_line": "1;"}`}, nil},
		{"unknown field and bad package",
			[]string{`{"name": "x", "package": "  ", "lines": [[4, 4]], "first_line": "1;", "what": "no"}`},
			[]string{"'package' must be a non-empty string or null", "unknown field 'what'"}},
		{"ranges that are not arrays of arrays",
			[]string{`{"name": "x", "lines": [1, 2], "doc_lines": "1-2"}`},
			[]string{"'lines' must be an array of [start, end] ranges", "'doc_lines' must be an array of [start, end] ranges"}},
		{"no range at all",
			[]string{`{"name": "x", "lines": []}`}, []string{"'lines' or 'old_lines' must hold a range"}},
		{"first lines need their side",
			[]string{`{"name": "x", "old_lines": [[1, 1]], "old_first_line": "sub old {", "first_line": "sub a {"}`},
			[]string{"'first_line' needs 'lines'"}},
		{"a missing first line",
			[]string{`{"name": "x", "lines": [[2, 2]]}`}, []string{"'first_line' is required: the text of its first line"}},
		{"a wrong first line names the lowest line",
			[]string{`{"name": "x", "lines": [[3, 3], [1, 1]], "first_line": "}"}`},
			[]string{`'first_line' does not match line 1, which reads "sub a {"`}},
		{"a range overlapping itself",
			[]string{`{"name": "x", "lines": [[1, 2]], "doc_lines": [[2, 3]], "first_line": "sub a {"}`},
			[]string{"new range [2,3]: overlaps itself"}},
		{"a range overlapping another section",
			[]string{`{"name": "x", "lines": [[1, 2]], "first_line": "sub a {"}`, `{"name": "y", "lines": [[2, 4]], "first_line": "  1;"}`},
			[]string{"new range [2,4]: overlaps section 1"}},
		{"ranges outside the file, each side",
			[]string{`{"name": "x", "lines": [[4, 5]], "old_lines": [[3, 4]], "first_line": "1;"}`},
			[]string{"new range [4,5]: beyond the end of 'a.pl' (4 lines)", "old range [3,4]: beyond the end of 'a.pl' (3 lines)"}},
	} {
		claims := splitClaims{}
		var problems []string
		for n, raw := range c.units {
			_, errs := checkUnit(f, texts, json.RawMessage(raw), claims, n+1)
			problems = append(problems, errs...)
		}
		if !reflect.DeepEqual(problems, c.problems) {
			t.Errorf("%s:\n got  %q\n want %q", c.name, problems, c.problems)
		}
	}
}

func TestCheckUnitTrimsAndSorts(t *testing.T) {
	size := int64(6)
	f := &sheetFile{FileChange: &FileChange{Path: "a", SizeUnit: "lines", SizeAfter: &size}}
	texts := &fileTexts{after: []string{"# doc", "a", "b", "", "c", "d"}}
	u, errs := checkUnit(f, texts, json.RawMessage(`{"name": " u ", "lines": [[5, 6], [2, 3]], "doc_lines": [[1, 1]], "first_line": "a"}`), splitClaims{}, 1)
	if errs != nil {
		t.Fatal(errs)
	}
	if u.Name != "u" || !reflect.DeepEqual(u.Ranges, Ranges{{2, 3}, {5, 6}}) || !reflect.DeepEqual(u.DocRanges, Ranges{{1, 1}}) {
		t.Fatalf("unit %+v", u)
	}
}

func TestUncovered(t *testing.T) {
	lines := []string{"a", "", "b", "c", "  ", "d"}
	if got := uncovered(lines, map[int]int{1: 1, 2: 1, 3: 1, 4: 1, 6: 1}); got != "" {
		t.Errorf("blank lines may be left out: %q", got)
	}
	got := uncovered(lines, map[int]int{1: 1})
	if !strings.HasPrefix(got, "lines 3-4, 6 are in no section") {
		t.Errorf("got %q", got)
	}
}

func TestResplit(t *testing.T) {
	d, err := ParseDiff("@@ -1,3 +1,4 @@\n a\n-b\n+B\n+x\n \n")
	if err != nil {
		t.Fatal(err)
	}
	texts := &fileTexts{after: []string{"a", "B", "x", ""}, hunks: d.Hunks}
	sections, err := resplit([]*Unit{
		{Name: "gone", OldRanges: Ranges{{2, 2}}},
		{Name: "top", Ranges: Ranges{{1, 1}}},
		{Name: "rest", Ranges: Ranges{{2, 3}}},
	}, texts)
	if err != nil {
		t.Fatal(err)
	}
	type got struct {
		name    string
		ranges  Ranges
		deleted bool
		counts  [3]int64
	}
	var out []got
	for _, s := range sections {
		out = append(out, got{s.Name, s.Ranges, s.Deleted, [3]int64{s.Added, s.Removed, s.Changed}})
	}
	// Live sections in file order, the blank last line joined to the one
	// before it, then deleted ones; the - line goes to the section whose old
	// lines hold it.
	want := []got{
		{"top", Ranges{{1, 1}}, false, [3]int64{}},
		{"rest", Ranges{{2, 4}}, false, [3]int64{2, 0, 0}},
		{"gone", Ranges{}, true, [3]int64{0, 1, 0}},
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("got %+v\nwant %+v", out, want)
	}
}
