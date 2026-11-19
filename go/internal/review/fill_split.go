package review

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// The split item: checking an agent's sections of a file (standards/
// FORMAT.md, "Sections") and storing them with counts from the diff.

// fileTexts are both sides of a file change, as lines, and its diff.
type fileTexts struct {
	before, after []string
	hunks         []*Hunk
}

// fileTexts reads a file change from the repository, once per fill.
func (c *sheetContext) fileTexts(e *sheetCommit, f *sheetFile) (*fileTexts, error) {
	if t := c.texts[f.ID]; t != nil {
		return t, nil
	}
	t := &fileTexts{}
	parent := deref(e.Parent)
	if parent != "" && f.Status != "A" {
		oldPath := f.Path
		if f.OldPath != nil {
			oldPath = *f.OldPath
		}
		b, err := c.repo.File(parent, oldPath)
		if err != nil {
			return nil, err
		}
		t.before = SplitLines(decodeText(b))
	}
	if f.Status != "D" {
		b, err := c.repo.File(e.Revision, f.Path)
		if err != nil {
			return nil, err
		}
		t.after = SplitLines(decodeText(b))
	}
	d, err := commitDiff(c.repo, parent, e.Revision, f.Path, deref(f.OldPath))
	if err != nil {
		return nil, err
	}
	t.hunks = d.Hunks
	c.texts[f.ID] = t
	return t, nil
}

// decodeText reads bytes as UTF-8, replacing what is not.
func decodeText(b []byte) string { return strings.ToValidUTF8(string(b), "�") }

// splitClaims records, per side, the section (1-based) holding each line.
type splitClaims map[string]map[int]int

func (c splitClaims) side(s string) map[int]int {
	if c[s] == nil {
		c[s] = map[int]int{}
	}
	return c[s]
}

func (v *validator) checkSplit(it *sheetItem, fields map[string]json.RawMessage) (fillWrite, []string) {
	entries, ok := jsonArray(fields["sections"])
	if !ok {
		return nil, []string{"'sections' must be an array"}
	}
	t, err := v.fileTexts(it.commit, it.file)
	if err != nil {
		v.fail(err)
		return nil, nil
	}
	var problems []string
	claims := splitClaims{}
	names := map[[2]string]bool{}
	units := make([]*Unit, 0, len(entries))
	for n, raw := range entries {
		u, errs := checkUnit(it.file, t, raw, claims, n+1)
		if len(errs) == 0 {
			key := [2]string{deref(u.Package), u.Name}
			if names[key] {
				in := ""
				if u.Package != nil {
					in = fmt.Sprintf(" in package '%s'", *u.Package)
				}
				errs = append(errs, fmt.Sprintf("name '%s' repeats%s; make it unique, for example with ' (2)'", u.Name, in))
			}
			names[key] = true
		}
		for _, e := range errs {
			problems = append(problems, fmt.Sprintf("section %d: %s", n+1, e))
		}
		units = append(units, u)
	}
	if len(entries) > 0 && len(problems) == 0 {
		if p := uncovered(t.after, claims.side("new")); p != "" {
			problems = append(problems, p)
		}
	}
	if len(problems) > 0 {
		return nil, problems
	}
	sections, err := resplit(units, t)
	if err != nil {
		return nil, []string{err.Error()}
	}
	v.plan.splits[it.file.ID] = sections
	return v.writeSplit(it, sections), nil
}

var unitFields = []string{"name", "package", "lines", "first_line", "doc_lines", "old_lines", "old_first_line"}

// checkUnit checks one section of a split: a name, ranges inside the file
// that no other section holds, and the text of its first line on each side
// it has, to catch an offset. Its ranges are claimed even when its name is
// bad, so a later section overlapping it is caught in the same fill.
func checkUnit(f *sheetFile, t *fileTexts, raw json.RawMessage, claims splitClaims, n int) (*Unit, []string) {
	obj, ok := jsonObject(raw)
	if !ok {
		return nil, []string{"must be an object"}
	}
	var problems []string
	u := &Unit{}
	name, ok := jsonText(obj["name"])
	if u.Name = strings.TrimSpace(name); !ok || u.Name == "" {
		problems = append(problems, "'name' must be a non-empty string")
	}
	if !isNull(obj["package"]) {
		pkg, ok := jsonText(obj["package"])
		if pkg = strings.TrimSpace(pkg); !ok || pkg == "" {
			problems = append(problems, "'package' must be a non-empty string or null")
		}
		u.Package = &pkg
	}
	problems = append(problems, unknownFields(obj, "", unitFields...)...)

	lists := map[string][]json.RawMessage{}
	var shape []string
	for _, key := range []string{"lines", "doc_lines", "old_lines"} {
		if isNull(obj[key]) {
			continue
		}
		list, ok := jsonArray(obj[key])
		for _, r := range list {
			if _, isArray := jsonArray(r); !isArray {
				ok = false
			}
		}
		if !ok {
			shape = append(shape, fmt.Sprintf("'%s' must be an array of [start, end] ranges", key))
		}
		lists[key] = list
	}
	if len(shape) > 0 {
		return nil, append(problems, shape...)
	}
	code, docs, old := lists["lines"], lists["doc_lines"], lists["old_lines"]
	if len(code) == 0 && len(old) == 0 {
		shape = append(shape, "'lines' or 'old_lines' must hold a range")
	}
	if len(docs) > 0 && len(code) == 0 {
		shape = append(shape, "'doc_lines' needs 'lines'")
	}
	if !isNull(obj["first_line"]) && len(code) == 0 {
		shape = append(shape, "'first_line' needs 'lines'")
	}
	if !isNull(obj["old_first_line"]) && len(old) == 0 {
		shape = append(shape, "'old_first_line' needs 'old_lines'")
	}
	if len(shape) > 0 {
		return nil, append(problems, shape...)
	}

	newSide, errs := claimRanges(f, "new", append(append([]json.RawMessage{}, code...), docs...), claims, n)
	problems = append(problems, errs...)
	u.OldRanges, errs = claimRanges(f, "old", old, claims, n)
	problems = append(problems, errs...)
	if len(problems) > 0 {
		return nil, problems
	}
	u.Ranges = sortRanges(newSide[:len(code)])
	u.DocRanges = sortRanges(newSide[len(code):])
	u.OldRanges = sortRanges(u.OldRanges)
	if len(code) > 0 {
		problems = append(problems, firstLineProblem(obj["first_line"], "first_line", u.Ranges, t.after)...)
	}
	if len(old) > 0 {
		problems = append(problems, firstLineProblem(obj["old_first_line"], "old_first_line", u.OldRanges, t.before)...)
	}
	return u, problems
}

// claimRanges reads ranges on one side of the file, each line claimed by
// one section only.
func claimRanges(f *sheetFile, side string, list []json.RawMessage, claims splitClaims, n int) (Ranges, []string) {
	held := claims.side(side)
	var ranges Ranges
	var problems []string
	for _, raw := range list {
		r, problem := fileRange(f, side, raw)
		if problem == "" {
			for l := r[0]; l <= r[1]; l++ {
				if other, ok := held[l]; ok {
					if other == n {
						problem = "overlaps itself"
					} else {
						problem = fmt.Sprintf("overlaps section %d", other)
					}
					break
				}
			}
			for l := r[0]; l <= r[1]; l++ {
				if _, ok := held[l]; !ok {
					held[l] = n
				}
			}
		}
		if problem != "" {
			problems = append(problems, fmt.Sprintf("%s range %s: %s", side, compactJSON(raw), problem))
		}
		ranges = append(ranges, r)
	}
	return ranges, problems
}

func sortRanges(r Ranges) Ranges {
	out := append(Ranges{}, r...)
	sort.SliceStable(out, func(a, b int) bool { return out[a][0] < out[b][0] })
	return out
}

// firstLineProblem checks a first line given against the text of the
// lowest line of the ranges; whitespace at either end does not count.
func firstLineProblem(raw json.RawMessage, key string, ranges Ranges, lines []string) []string {
	want, ok := jsonText(raw)
	if !ok {
		return []string{fmt.Sprintf("'%s' is required: the text of its first line", key)}
	}
	first := ranges[0][0]
	got := strings.TrimSpace(lineAt(lines, first))
	if strings.TrimSpace(want) == got {
		return nil
	}
	return []string{fmt.Sprintf("'%s' does not match line %d, which reads %s", key, first, quoteJSON(got))}
}

// uncovered names the lines with text after the commit that no section
// holds, or is "".
func uncovered(lines []string, held map[int]int) string {
	var free []int
	for l := 1; l <= len(lines); l++ {
		if _, ok := held[l]; !ok && strings.TrimSpace(lines[l-1]) != "" {
			free = append(free, l)
		}
	}
	if len(free) == 0 {
		return ""
	}
	var parts []string
	for _, r := range ToRanges(free) {
		if r[0] == r[1] {
			parts = append(parts, fmt.Sprint(r[0]))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", r[0], r[1]))
		}
	}
	return fmt.Sprintf("lines %s are in no section; cover every line of the file, or give [] to leave it whole", strings.Join(parts, ", "))
}

// resplit lays out the units as the file's sections: the live ones with the
// blank lines between them joined, then the deleted ones, every one with
// its counts from the diff and no explanation.
func resplit(units []*Unit, t *fileTexts) ([]*Section, error) {
	var live, deleted []*Unit
	for _, u := range units {
		if len(u.Ranges) > 0 {
			live = append(live, u)
		} else {
			deleted = append(deleted, &Unit{Name: u.Name, Package: u.Package, Ranges: Ranges{}, DocRanges: Ranges{}, OldRanges: u.OldRanges, Deleted: true})
		}
	}
	placed, err := Assemble(live, t.after)
	if err != nil {
		return nil, err
	}
	all := append(placed, deleted...)
	spans := make([]*Span, len(all))
	for i, u := range all {
		spans[i] = &Span{Ranges: u.Ranges, DocRanges: u.DocRanges, OldRanges: u.OldRanges, Deleted: u.Deleted}
	}
	Attribute(t.hunks, spans)
	sections := make([]*Section, len(all))
	for i, u := range all {
		sections[i] = &Section{
			Position: int64(i + 1), Name: u.Name, Package: u.Package,
			Ranges: orEmpty(u.Ranges), DocRanges: orEmpty(u.DocRanges), OldRanges: orEmpty(u.OldRanges), Deleted: u.Deleted,
			Added: int64(spans[i].Added), Removed: int64(spans[i].Removed), Changed: int64(spans[i].Changed),
		}
	}
	return sections, nil
}

// writeSplit replaces the file's sections, moves section feedback and
// requests whose section is gone or untouched to the file, labels the file
// split by the agent, and marks the open split request its item shows done
// (another one open on the file is shown next).
func (v *validator) writeSplit(it *sheetItem, sections []*Section) fillWrite {
	e, f := it.commit, it.file
	return func(tx *sql.Tx) error {
		if err := moveSectionFeedback(tx, e.Revision, f.Path, sections); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM section WHERE file_id = ?", f.ID); err != nil {
			return err
		}
		for _, s := range sections {
			_, err := tx.Exec(`INSERT INTO section (file_id, position, name, package, ranges, doc_ranges, old_ranges, deleted, added, removed, changed, what)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`, f.ID, s.Position, s.Name, s.Package,
				rangesJSON(s.Ranges), rangesJSON(s.DocRanges), rangesJSON(s.OldRanges), s.Deleted, s.Added, s.Removed, s.Changed)
			if err != nil {
				return err
			}
		}
		if _, err := tx.Exec("UPDATE file_change SET split_source = 'agent' WHERE id = ?", f.ID); err != nil {
			return err
		}
		if it.request == nil {
			return nil
		}
		_, err := tx.Exec("UPDATE request SET status = 'done' WHERE id = ?", it.request.ID)
		return err
	}
}

// moveSectionFeedback keeps section feedback and requests on the new
// touched section of their name and package; with none they become file
// ones, keeping the section's name and package as context.
func moveSectionFeedback(tx *sql.Tx, revision, path string, sections []*Section) error {
	kept := map[[2]string]bool{}
	for _, s := range sections {
		if s.Touched() {
			kept[[2]string{s.Name, deref(s.Package)}] = true
		}
	}
	for _, table := range []string{"feedback", "request"} {
		rows, err := tx.Query("SELECT id, section_name, section_package FROM "+table+" WHERE target = 'section' AND revision = ? AND path = ?", revision, path)
		if err != nil {
			return err
		}
		var move []int64
		for rows.Next() {
			var id int64
			var name, pkg *string
			if err := rows.Scan(&id, &name, &pkg); err != nil {
				rows.Close()
				return err
			}
			if !kept[[2]string{deref(name), deref(pkg)}] {
				move = append(move, id)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, id := range move {
			if _, err := tx.Exec("UPDATE "+table+" SET target = 'file' WHERE id = ?", id); err != nil {
				return err
			}
		}
	}
	return nil
}
