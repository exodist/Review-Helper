package review

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Pure functions over a single-file unified diff in the format's diff
// convention (standards/FORMAT.md): parse it, count changed lines, and
// attribute them to sections. Nothing here runs git.
//
// Line counts follow one rule everywhere: each run of - lines followed by
// + lines counts min(removed, added) as changed and the rest as removed or
// added.

// DiffLine is one line of a hunk: Type is " ", "-" or "+"; Old and New are
// its line numbers, nil on the side it does not exist.
type DiffLine struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Old  *int   `json:"old"`
	New  *int   `json:"new"`
}

// Hunk is one hunk of a diff. Part (code or docs) is set only on a
// section's hunks.
type Hunk struct {
	OldStart int         `json:"old_start"`
	OldCount int         `json:"old_count"`
	NewStart int         `json:"new_start"`
	NewCount int         `json:"new_count"`
	Lines    []*DiffLine `json:"lines"`
	Part     string      `json:"part,omitempty"`
}

// Diff is a parsed diff.
type Diff struct {
	Binary bool    `json:"binary"`
	Hunks  []*Hunk `json:"hunks"`
}

// Counts are a diff's or a section's line counts.
type Counts struct{ Added, Removed, Changed int }

// Span is a section as attribution sees it: its ranges and whether it is
// deleted; attribution sets its counts.
type Span struct {
	Ranges, DocRanges, OldRanges [][2]int
	Deleted                      bool
	Counts
	Touched bool
}

func ip(n int) *int { return &n }

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// ParseDiff parses diff text. An empty diff (a rename or mode change alone)
// has no hunks; "\ No newline at end of file" markers are dropped.
func ParseDiff(text string) (*Diff, error) {
	lines := strings.Split(text, "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	d := &Diff{Hunks: []*Hunk{}}
	for i := 0; i < len(lines); {
		line := lines[i]
		i++
		if m := hunkHeader.FindStringSubmatch(line); m != nil {
			num := func(s string, def int) int {
				if s == "" {
					return def
				}
				n, _ := strconv.Atoi(s)
				return n
			}
			h := &Hunk{OldStart: num(m[1], 0), OldCount: num(m[2], 1), NewStart: num(m[3], 0), NewCount: num(m[4], 1), Lines: []*DiffLine{}}
			var err error
			if i, err = readHunkLines(h, lines, i); err != nil {
				return nil, err
			}
			d.Hunks = append(d.Hunks, h)
			continue
		}
		if line == "GIT binary patch" || (strings.HasPrefix(line, "Binary files ") && strings.HasSuffix(line, " differ")) {
			d.Binary = true
		}
	}
	return d, nil
}

func readHunkLines(h *Hunk, lines []string, i int) (int, error) {
	old, nw := h.OldStart, h.NewStart
	oldLeft, newLeft := h.OldCount, h.NewCount
	for i < len(lines) && (oldLeft > 0 || newLeft > 0) {
		line := lines[i]
		i++
		typ, text := "", ""
		if line != "" {
			typ, text = line[:1], line[1:]
		}
		if typ == "\\" { // "\ No newline at end of file"
			continue
		}
		// A blank line inside a hunk is a context line whose leading space
		// was stripped.
		if typ == "" {
			typ = " "
		}
		switch typ {
		case " ":
			h.Lines = append(h.Lines, &DiffLine{Type: " ", Text: text, Old: ip(old), New: ip(nw)})
			old++
			nw++
			oldLeft--
			newLeft--
		case "-":
			h.Lines = append(h.Lines, &DiffLine{Type: "-", Text: text, Old: ip(old)})
			old++
			oldLeft--
		case "+":
			h.Lines = append(h.Lines, &DiffLine{Type: "+", Text: text, New: ip(nw)})
			nw++
			newLeft--
		default:
			return i, fmt.Errorf("malformed diff: unexpected line %q inside a hunk", line)
		}
	}
	for i < len(lines) && strings.HasPrefix(lines[i], "\\") {
		i++
	}
	return i, nil
}

// CountLines counts a whole diff.
func CountLines(hunks []*Hunk) Counts {
	var c Counts
	for _, h := range hunks {
		for _, run := range changeRuns(h.Lines) {
			minus := 0
			for _, i := range run {
				if h.Lines[i].Type == "-" {
					minus++
				}
			}
			tally(&c, minus, len(run)-minus)
		}
	}
	return c
}

// Attribute sets each section's counts and Touched from the diff.
func Attribute(hunks []*Hunk, sections []*Span) {
	for _, s := range sections {
		s.Counts, s.Touched = Counts{}, false
	}
	owners := LineOwners(hunks, sections)
	for h, hunk := range hunks {
		for _, run := range changeRuns(hunk.Lines) {
			per := map[int]*[2]int{}
			var order []int
			for _, i := range run {
				o := owners[h][i]
				if o < 0 {
					continue
				}
				if per[o] == nil {
					per[o] = &[2]int{}
					order = append(order, o)
				}
				if hunk.Lines[i].Type == "-" {
					per[o][0]++
				} else {
					per[o][1]++
				}
			}
			for _, o := range order {
				tally(&sections[o].Counts, per[o][0], per[o][1])
				sections[o].Touched = true
			}
		}
	}
}

// LineOwners gives, per hunk, the index of the section owning each line, or
// -1. A context or + line belongs to the section holding its new line; a -
// line to the section whose old ranges hold it, else the one holding the
// nearest following new-side line of its hunk, or the nearest preceding one
// when none follows. Deleted sections own only through old ranges.
func LineOwners(hunks []*Hunk, sections []*Span) [][]int {
	newOwner, oldOwner := map[int]int{}, map[int]int{}
	for s, sec := range sections {
		for _, l := range rangeLines(sec.OldRanges) {
			oldOwner[l] = s
		}
		if sec.Deleted {
			continue
		}
		for _, l := range rangeLines(sec.Ranges) {
			newOwner[l] = s
		}
		for _, l := range rangeLines(sec.DocRanges) {
			newOwner[l] = s
		}
	}
	owners := make([][]int, len(hunks))
	for h, hunk := range hunks {
		near := nearestNew(hunk.Lines)
		owners[h] = make([]int, len(hunk.Lines))
		for i, l := range hunk.Lines {
			owner := -1
			if l.Type != "-" {
				if o, ok := newOwner[*l.New]; ok {
					owner = o
				}
			} else if o, ok := oldOwner[*l.Old]; ok {
				owner = o
			} else if near[i] != nil {
				if o, ok := newOwner[*near[i]]; ok {
					owner = o
				}
			}
			owners[h][i] = owner
		}
	}
	return owners
}

// seqEntry is one line of a section's whole-file sequence: the line (nil
// for one the unit does not take), its part ("", code, docs, or "-" for a
// removed line it owns), and the positions on each side before it.
type seqEntry struct {
	line     *DiffLine
	part     string
	old, new int
}

// SectionHunks is the diff of the section at index: every line of its
// ranges and doc ranges, unchanged ones as context (their text from
// newLines, the file after the change), with the + lines of the diff and
// the - lines it owns, in file order. Each unbroken run of one part is a
// hunk with a Part; runs apart only by blank lines join, holding those
// lines. A section with no added or removed line has no hunks.
func SectionHunks(hunks []*Hunk, newLines []string, sections []*Span, index int) []*Hunk {
	sec := sections[index]
	owners := LineOwners(hunks, sections)
	part := map[int]string{}
	if !sec.Deleted {
		for _, l := range rangeLines(sec.Ranges) {
			part[l] = "code"
		}
		for _, l := range rangeLines(sec.DocRanges) {
			part[l] = "docs"
		}
	}
	var seq []*seqEntry
	old, nw := 1, 1
	fill := func(to int) {
		for nw < to {
			p := part[nw]
			var line *DiffLine
			if p != "" {
				line = &DiffLine{Type: " ", Text: lineAt(newLines, nw), Old: ip(old), New: ip(nw)}
			}
			seq = append(seq, &seqEntry{line, p, old, nw})
			old++
			nw++
		}
	}
	startOf := func(start, count int) int {
		if count > 0 {
			return start
		}
		return start + 1
	}
	for h, hunk := range hunks {
		fill(startOf(hunk.NewStart, hunk.NewCount))
		old, nw = startOf(hunk.OldStart, hunk.OldCount), startOf(hunk.NewStart, hunk.NewCount)
		for i, l := range hunk.Lines {
			p := ""
			if l.Type == "-" {
				if owners[h][i] == index {
					p = "-"
				}
			} else {
				p = part[*l.New]
			}
			seq = append(seq, &seqEntry{l, p, old, nw})
			if l.Type != "+" {
				old++
			}
			if l.Type != "-" {
				nw++
			}
		}
	}
	fill(len(newLines) + 1)
	removedParts(seq)

	// Runs of one part, each with the lines the unit does not take between
	// it and the run before.
	var runs, gaps [][]*seqEntry
	var gap []*seqEntry
	prev := ""
	for _, e := range seq {
		if e.part == "" {
			if len(runs) > 0 {
				gap = append(gap, e)
			}
			prev = ""
			continue
		}
		if prev != e.part {
			runs = append(runs, nil)
			gaps = append(gaps, gap)
			gap = nil
		}
		runs[len(runs)-1] = append(runs[len(runs)-1], e)
		prev = e.part
	}
	changed := false
	for _, run := range runs {
		for _, e := range run {
			if e.line.Type != " " {
				changed = true
			}
		}
	}
	if !changed {
		return []*Hunk{}
	}

	// Runs next to each other, apart only by blank lines, read as one diff.
	type merged struct {
		part    string
		entries []*seqEntry
	}
	out := []*merged{{runs[0][0].part, append([]*seqEntry{}, runs[0]...)}}
	for r := 1; r < len(runs); r++ {
		run, g := runs[r], gaps[r]
		blank := make([]*DiffLine, len(g))
		allBlank := true
		for i, e := range g {
			l := e.line
			if l == nil {
				l = &DiffLine{Type: " ", Text: lineAt(newLines, e.new), Old: ip(e.old), New: ip(e.new)}
			}
			blank[i] = l
			if strings.TrimSpace(l.Text) != "" {
				allBlank = false
			}
		}
		if allBlank {
			m := out[len(out)-1]
			if run[0].part == "code" {
				m.part = "code"
			}
			for i, l := range blank {
				m.entries = append(m.entries, &seqEntry{l, "", g[i].old, g[i].new})
			}
			m.entries = append(m.entries, run...)
			continue
		}
		out = append(out, &merged{run[0].part, append([]*seqEntry{}, run...)})
	}
	result := make([]*Hunk, len(out))
	for i, m := range out {
		result[i] = runHunk(m.part, m.entries)
	}
	return result
}

func lineAt(lines []string, n int) string {
	if n >= 1 && n <= len(lines) {
		return lines[n-1]
	}
	return ""
}

// MapLines maps old lines start..end through the diff: the new start and
// end, ok false unless every line survives, consecutively.
func MapLines(hunks []*Hunk, start, end int) (int, int, bool) {
	var first int
	for i := start; i <= end; i++ {
		n, ok := mapLine(hunks, i)
		if !ok {
			return 0, 0, false
		}
		if i == start {
			first = n
		} else if n != first+(i-start) {
			return 0, 0, false
		}
	}
	return first, first + (end - start), true
}

// MoveRanges maps ranges through the diff: the surviving lines as ranges,
// and whether any line was removed or the lines no longer run on.
func MoveRanges(hunks []*Hunk, ranges [][2]int) ([][2]int, bool) {
	changed := false
	var lines []int
	for _, r := range ranges {
		prev, have := 0, false
		for l := r[0]; l <= r[1]; l++ {
			to, ok := mapLine(hunks, l)
			if !ok || (have && to != prev+1) {
				changed = true
			}
			if !ok {
				continue
			}
			lines = append(lines, to)
			prev, have = to, true
		}
	}
	var moved [][2]int
	for _, l := range lines {
		if len(moved) > 0 && moved[len(moved)-1][1]+1 == l {
			moved[len(moved)-1][1] = l
		} else {
			moved = append(moved, [2]int{l, l})
		}
	}
	return moved, changed
}

// mapLine is the new number of an old line, ok false when the diff removes it.
func mapLine(hunks []*Hunk, line int) (int, bool) {
	shift := 0
	for _, h := range hunks {
		for _, l := range h.Lines {
			if l.Old != nil && *l.Old == line {
				if l.Type == " " {
					return *l.New, true
				}
				return 0, false
			}
		}
		last := h.OldStart
		if h.OldCount > 0 {
			last = h.OldStart + h.OldCount - 1
		}
		if last < line {
			shift += h.NewCount - h.OldCount
		}
	}
	return line + shift, true
}

// changeRuns are the maximal runs of non-context line indexes.
func changeRuns(lines []*DiffLine) [][]int {
	var runs [][]int
	in := false
	for i, l := range lines {
		if l.Type == " " {
			in = false
			continue
		}
		if !in {
			runs = append(runs, nil)
			in = true
		}
		runs[len(runs)-1] = append(runs[len(runs)-1], i)
	}
	return runs
}

func tally(c *Counts, minus, plus int) {
	changed := minus
	if plus < changed {
		changed = plus
	}
	c.Changed += changed
	c.Removed += minus - changed
	c.Added += plus - changed
}

// nearestNew gives, per line, the new-side line a - line belongs with: the
// next new-side line in the hunk, or the last one before it.
func nearestNew(lines []*DiffLine) []*int {
	near := make([]*int, len(lines))
	var seen *int
	for i := len(lines) - 1; i >= 0; i-- {
		near[i] = seen
		if lines[i].New != nil {
			seen = lines[i].New
		}
	}
	seen = nil
	for i := range lines {
		if near[i] == nil {
			near[i] = seen
		}
		if lines[i].New != nil {
			seen = lines[i].New
		}
	}
	return near
}

// removedParts gives a removed line the unit owns the part of the unit's
// line after it, else the one before it, else code.
func removedParts(seq []*seqEntry) {
	for i, e := range seq {
		if e.part != "-" {
			continue
		}
		p := ""
		for j := i + 1; j < len(seq); j++ {
			q := seq[j].part
			if q == "" {
				break
			}
			if q == "-" {
				continue
			}
			p = q
			break
		}
		for j := i - 1; j >= 0 && p == ""; j-- {
			q := seq[j].part
			if q == "" {
				break
			}
			if q != "-" {
				p = q
			}
		}
		if p == "" {
			p = "code"
		}
		e.part = p
	}
}

// runHunk makes a hunk of consecutive entries. A side with no lines names
// the line before the hunk, as git does.
func runHunk(part string, run []*seqEntry) *Hunk {
	h := &Hunk{Part: part}
	for _, e := range run {
		h.Lines = append(h.Lines, e.line)
	}
	for _, l := range h.Lines {
		if l.Type != "+" {
			h.OldCount++
		}
		if l.Type != "-" {
			h.NewCount++
		}
	}
	h.OldStart, h.NewStart = run[0].old, run[0].new
	if h.OldCount == 0 {
		h.OldStart--
	}
	if h.NewCount == 0 {
		h.NewStart--
	}
	return h
}

func rangeLines(ranges [][2]int) []int {
	var out []int
	for _, r := range ranges {
		for l := r[0]; l <= r[1]; l++ {
			out = append(out, l)
		}
	}
	return out
}
