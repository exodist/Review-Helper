package review

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"github.com/exodist/Review-Helper/go/internal/git"
)

// Filling a sheet (standards/FORMAT.md, "The fill sheet"): every item is
// checked before anything is written, then the whole sheet is written in
// one transaction.

// FillResult is what a fill wrote: the items written and the required
// items still to do.
type FillResult struct{ Filled, Remaining int }

// RejectedSheet lists every problem of a sheet fill refused.
type RejectedSheet struct{ Problems []string }

func (r *RejectedSheet) Error() string {
	return "the sheet was rejected:\n" + strings.Join(r.Problems, "\n")
}

// fillWrite writes one checked item.
type fillWrite func(tx *sql.Tx) error

// fillPlan is what the checked items add up to. Checks record what later
// checks need: the topics, the new splits, the notes placed and the flags
// retired.
type fillPlan struct {
	topics  bool
	splits  map[int64][]*Section // file id to its new sections
	notes   map[string]*sheetCommit
	retired map[string]bool
	writes  []fillWrite
}

// validator checks one sheet against the iteration. err is the first
// failure to read the repository, which fails the fill rather than
// rejecting the sheet.
type validator struct {
	*sheetContext
	plan *fillPlan
	err  error
}

func (v *validator) fail(err error) {
	if v.err == nil {
		v.err = err
	}
}

// Validation order: topics first, so the items they would move can be
// refused; then splits and note placements, which section items and guide
// steps see; the guide last.
var checkPhase = map[string]int{"topics": -1, "split": 0, "note": 0, "guide": 2}

// namesEntry are the items naming an entry, which a topics fill moves.
var namesEntry = map[string]bool{"commit": true, "order": true, "note": true, "guide": true}

// Fill validates a filled sheet (JSON text) and writes it.
func Fill(d *DB, repo *git.Repo, j *Journal, data []byte) (*FillResult, error) {
	c, err := loadSheetContext(d, repo, j, j.Warn)
	if err != nil {
		return nil, err
	}
	items, err := c.items()
	if err != nil {
		return nil, err
	}
	v := &validator{sheetContext: c, plan: &fillPlan{splits: map[int64][]*Section{}, notes: map[string]*sheetCommit{}, retired: map[string]bool{}}}
	problems := v.validate(data, items)
	if v.err != nil {
		return nil, v.err
	}
	if len(problems) > 0 {
		return nil, &RejectedSheet{Problems: problems}
	}
	err = d.Txn(func(tx *sql.Tx) error {
		for _, w := range v.plan.writes {
			if err := w(tx); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// The notes placed now leave the journal, and so do any an earlier fill
	// placed but failed to remove.
	var placed []string
	for id := range v.plan.notes {
		placed = append(placed, id)
	}
	for _, n := range c.notes {
		if _, ok := c.flagEntry[n.ID.Value]; ok {
			placed = append(placed, n.ID.Value)
		}
	}
	if len(placed) > 0 {
		if err := j.Remove(placed...); err != nil {
			return nil, err
		}
	}

	// The notes were warned about above.
	after, err := loadSheetContext(d, repo, j, nil)
	if err != nil {
		return nil, err
	}
	all, err := after.items()
	if err != nil {
		return nil, err
	}
	res := &FillResult{Filled: len(v.plan.writes)}
	for _, it := range all {
		if !it.done && !it.Optional {
			res.Remaining++
		}
	}
	return res, nil
}

// filledItem is one sheet entry matched to its item, with its non-null
// fields.
type filledItem struct {
	item   *sheetItem
	fields map[string]json.RawMessage
}

func (v *validator) validate(data []byte, items []*sheetItem) []string {
	top, ok := jsonObject(data)
	if !ok {
		return []string{"the sheet must be a JSON object"}
	}
	var problems []string
	is := func(key, want string) bool { s, ok := jsonText(top[key]); return ok && s == want }
	if !is("format", SheetFormat) {
		problems = append(problems, fmt.Sprintf("'format' must be '%s'", SheetFormat))
	}
	if !is("format_version", fmt.Sprint(SheetFormatVersion)) {
		problems = append(problems, fmt.Sprintf("'format_version' must be %d", SheetFormatVersion))
	}
	if !is("review", v.review.ID) {
		problems = append(problems, "the sheet is for another review")
	}
	if !is("iteration", fmt.Sprint(v.iteration.Number)) {
		problems = append(problems, fmt.Sprintf("the sheet is not for the latest iteration, %d", v.iteration.Number))
	}
	entries, ok := jsonArray(top["items"])
	if !ok {
		problems = append(problems, "'items' must be an array")
	}
	if len(problems) > 0 {
		return problems
	}

	byID := map[string]*sheetItem{}
	for _, it := range items {
		byID[it.ID] = it
	}
	seen := map[string]bool{}
	var todo []filledItem
	for n, raw := range entries {
		fi, errs := v.match(n, raw, byID)
		problems = append(problems, errs...)
		if fi == nil {
			continue
		}
		if seen[fi.item.ID] {
			problems = append(problems, fi.item.ID+": listed more than once")
		}
		seen[fi.item.ID] = true
		todo = append(todo, *fi)
	}

	sort.SliceStable(todo, func(a, b int) bool { return orderOf(todo[a].item.Type) < orderOf(todo[b].item.Type) })
	for _, fi := range todo {
		var w fillWrite
		var errs []string
		if v.plan.topics && namesEntry[fi.item.Type] {
			errs = []string{"the topics change in this sheet; fill this item from the next sheet"}
		} else {
			w, errs = v.check(fi.item, fi.fields)
		}
		for _, e := range errs {
			problems = append(problems, fi.item.ID+": "+e)
		}
		if len(errs) == 0 && w != nil {
			v.plan.writes = append(v.plan.writes, w)
		}
	}
	return problems
}

func orderOf(typ string) int {
	if o, ok := checkPhase[typ]; ok {
		return o
	}
	return 1
}

// match finds the item an entry fills and checks its id, type, rev and
// field names. It is nil, with no problem, for an entry that is skipped: no
// field set, or the topics stored sent back.
func (v *validator) match(n int, raw json.RawMessage, byID map[string]*sheetItem) (*filledItem, []string) {
	in, ok := jsonObject(raw)
	id, idOK := jsonText(in["id"])
	if !ok || !idOK {
		return nil, []string{fmt.Sprintf("item %d must be an object with an 'id'", n+1)}
	}
	given, ok := jsonObject(in["fields"])
	if !ok {
		return nil, []string{id + ": 'fields' must be an object"}
	}
	fields := map[string]json.RawMessage{}
	for k, val := range given {
		if !isNull(val) {
			fields[k] = val
		}
	}
	if len(fields) == 0 {
		return nil, nil
	}
	item := byID[id]
	if item == nil {
		return nil, []string{id + ": unknown item"}
	}
	var problems []string
	if t, _ := jsonText(in["type"]); t != item.Type {
		problems = append(problems, fmt.Sprintf("%s: type must be '%s'", id, item.Type))
	}
	if r, _ := jsonText(in["rev"]); r != item.Rev {
		problems = append(problems, id+": stale revision; get a new sheet")
	}
	problems = append(problems, unknownFields(fields, id+": ", item.fields...)...)
	if len(problems) > 0 {
		return nil, problems
	}
	if item.Type == "topics" && sameTopics(fields["topics"], item.Fields.(TopicsFields).Topics) {
		return nil, nil
	}
	return &filledItem{item, fields}, nil
}

// check checks one item's fields and returns its write; a valid item with
// nothing to write has none.
func (v *validator) check(it *sheetItem, fields map[string]json.RawMessage) (fillWrite, []string) {
	switch it.Type {
	case "summary":
		return v.checkSummary(it, fields)
	case "commit":
		return checkText(fields, "commit_entry", it.commit.ID, "what", "why", "how")
	case "file":
		// A file is done once it has what: one without it, given or stored,
		// is skipped and stays to do.
		w, errs := checkText(fields, "file_change", it.file.ID, "what", "object_system", "paradigms")
		if fields["what"] == nil && it.file.What == nil {
			w = nil
		}
		return w, errs
	case "section":
		w, errs := checkText(fields, "section", it.section.ID, "what")
		if _, resplit := v.plan.splits[it.file.ID]; resplit {
			errs = append(errs, "its file is re-split in this sheet; explain its sections from the next sheet")
		}
		return w, errs
	case "debate":
		return checkDebate(it, fields)
	case "order":
		return checkOrder(it, fields)
	case "feedback":
		return v.checkFeedback(it, fields)
	case "note":
		return v.checkNote(it, fields)
	case "flag":
		return v.checkFlag(it, fields)
	case "split":
		return v.checkSplit(it, fields)
	case "topics":
		return v.checkTopics(it, fields)
	case "guide":
		return v.checkGuide(it, fields)
	}
	return nil, []string{"cannot be filled"}
}

// colValue is a column and the value to store in it.
type colValue struct {
	col string
	val any
}

// update sets columns of the rows matching where.
func update(tx *sql.Tx, table string, set []colValue, where string, args ...any) error {
	if len(set) == 0 {
		return nil
	}
	cols := make([]string, len(set))
	vals := make([]any, 0, len(set)+len(args))
	for i, cv := range set {
		cols[i] = cv.col + " = ?"
		vals = append(vals, cv.val)
	}
	_, err := tx.Exec("UPDATE "+table+" SET "+strings.Join(cols, ", ")+" WHERE "+where, append(vals, args...)...)
	return err
}

// textValues checks text fields: each given one must be a non-empty string.
func textValues(fields map[string]json.RawMessage, names ...string) ([]colValue, []string) {
	var set []colValue
	var problems []string
	for _, name := range names {
		raw, given := fields[name]
		if !given {
			continue
		}
		s, ok := isText(raw)
		if !ok {
			problems = append(problems, fmt.Sprintf("'%s' must be a non-empty string or null", name))
			continue
		}
		set = append(set, colValue{name, s})
	}
	return set, problems
}

// checkText checks text fields and writes them to the row with id.
func checkText(fields map[string]json.RawMessage, table string, id int64, names ...string) (fillWrite, []string) {
	set, problems := textValues(fields, names...)
	return func(tx *sql.Tx) error { return update(tx, table, set, "id = ?", id) }, problems
}

func (v *validator) checkSummary(it *sheetItem, fields map[string]json.RawMessage) (fillWrite, []string) {
	set, problems := textValues(fields, it.fields...)
	n := v.iteration.Number
	return func(tx *sql.Tx) error { return update(tx, "iteration", set, "number = ?", n) }, problems
}

// checkDebate writes the cases; a debate is done once both are set.
func checkDebate(it *sheetItem, fields map[string]json.RawMessage) (fillWrite, []string) {
	set, problems := textValues(fields, "case_for", "case_against")
	r := it.request
	has := map[string]bool{"case_for": r.CaseFor != nil, "case_against": r.CaseAgainst != nil}
	for _, cv := range set {
		has[cv.col] = true
	}
	if has["case_for"] && has["case_against"] {
		set = append(set, colValue{"status", "done"})
	}
	return func(tx *sql.Tx) error { return update(tx, "request", set, "id = ?", r.ID) }, problems
}

// checkOrder takes every path of the commit, once each.
func checkOrder(it *sheetItem, fields map[string]json.RawMessage) (fillWrite, []string) {
	paths, ok := texts(fields["paths"])
	if !ok {
		return nil, []string{"'paths' must be an array of paths"}
	}
	want := slices.Clone(it.About.(OrderAbout).Paths)
	got := slices.Clone(paths)
	sort.Strings(want)
	sort.Strings(got)
	if !slices.Equal(want, got) {
		return nil, []string{"'paths' must list every path of the commit exactly once"}
	}
	id := it.commit.ID
	return func(tx *sql.Tx) error {
		for n, p := range paths {
			if _, err := tx.Exec("UPDATE file_change SET read_position = ? WHERE commit_id = ? AND path = ?", n+1, id, p); err != nil {
				return err
			}
		}
		_, err := tx.Exec("UPDATE commit_entry SET order_set = 1 WHERE id = ?", id)
		return err
	}, nil
}

// checkFeedback answers feedback: a status needs a response, given or
// stored. Addressed feedback records the iteration that resolved it.
func (v *validator) checkFeedback(it *sheetItem, fields map[string]json.RawMessage) (fillWrite, []string) {
	set, problems := textValues(fields, "response")
	fb := it.feedback
	if raw, given := fields["status"]; given {
		status, _ := jsonString(raw)
		if status != "addressed" && status != "declined" {
			problems = append(problems, "'status' must be 'addressed' or 'declined'")
		}
		if fields["response"] == nil && fb.Response == nil {
			problems = append(problems, "'status' needs a 'response'")
		}
		set = append(set, colValue{"status", status})
		if status == "addressed" {
			set = append(set, colValue{"resolved_in", v.iteration.Number})
		}
	}
	set = append(set, colValue{"updated", now()})
	return func(tx *sql.Tx) error { return update(tx, "feedback", set, "id = ?", fb.ID) }, problems
}

// checkNote places a journal note as a flag on a commit, a file of it, or
// lines of that file.
func (v *validator) checkNote(it *sheetItem, fields map[string]json.RawMessage) (fillWrite, []string) {
	if fields["commit"] == nil {
		return nil, []string{"'commit' is required to place a note"}
	}
	e, problem := v.findCommit(fields["commit"], fields["topic"], fields["path"])
	if problem != "" {
		return nil, []string{"'commit': " + problem}
	}
	if fields["lines"] != nil && fields["path"] == nil {
		return nil, []string{"'lines' needs a 'path'"}
	}
	var path *string
	var f *sheetFile
	if raw := fields["path"]; raw != nil {
		p, ok := jsonText(raw)
		if !ok {
			return nil, []string{"'path' must be a string"}
		}
		if f = e.byPath[p]; f == nil {
			return nil, []string{fmt.Sprintf("'path': '%s' is not changed in %s", p, entryName(e))}
		}
		path = &p
	}
	var start, end *int
	if raw := fields["lines"]; raw != nil {
		side := "new"
		if f.Status == "D" {
			side = "old"
		}
		r, problem := fileRange(f, side, raw)
		if problem != "" {
			return nil, []string{"'lines': " + problem}
		}
		start, end = &r[0], &r[1]
	}
	n := it.note
	v.plan.notes[n.ID.Value] = e
	return func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO flag (note_id, commit_id, path, line_start, line_end, color, kind, sentence, detail, created)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, n.ID.Value, e.ID, path, start, end, n.Flag.Value, n.Kind.Value, n.Sentence.Value, n.Detail.Ptr(), n.Created.Seconds)
		return err
	}, nil
}

func entryName(e *sheetCommit) string {
	if e.Topic != nil {
		return fmt.Sprintf("topic '%s' of commit %s", *e.Topic, e.Revision)
	}
	return "commit " + e.Revision
}

// checkFlag decides on a flag: keep it, recolor it (the other color and
// why), or retire it. Keep and retire ignore color and reason.
func (v *validator) checkFlag(it *sheetItem, fields map[string]json.RawMessage) (fillWrite, []string) {
	decision, _ := jsonString(fields["decision"])
	if decision != "keep" && decision != "recolor" && decision != "retire" {
		return nil, []string{"'decision' must be 'keep', 'recolor' or 'retire'"}
	}
	f := it.flag
	switch decision {
	case "retire":
		v.plan.retired[f.NoteID] = true
		return func(tx *sql.Tx) error { return retireFlag(tx, f) }, nil
	case "recolor":
		var problems []string
		color, _ := jsonString(fields["color"])
		if color != "yellow" && color != "red" {
			problems = append(problems, "'color' must be 'yellow' or 'red'")
		}
		reason, ok := isText(fields["reason"])
		if !ok {
			problems = append(problems, "'reason' must be a non-empty string: why the color changed")
		}
		if len(problems) == 0 && color == f.Color {
			problems = append(problems, "'color' is the flag's color already; use 'keep'")
		}
		if len(problems) > 0 {
			return nil, problems
		}
		return func(tx *sql.Tx) error {
			// A flag turned red is unseen by everyone.
			if color == "red" {
				if _, err := tx.Exec("DELETE FROM flag_seen WHERE flag = ?", f.ID); err != nil {
					return err
				}
			}
			_, err := tx.Exec("UPDATE flag SET pending = 0, color = ?, color_reason = ? WHERE id = ?", color, reason, f.ID)
			return err
		}, nil
	}
	return func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE flag SET pending = 0 WHERE id = ?", f.ID)
		return err
	}, nil
}

// retireFlag deletes the flag from the iteration with its seen marks; its
// feedback, from any iteration, becomes feedback on its file (its commit,
// for a whole-commit flag), naming the flag as context.
func retireFlag(tx *sql.Tx, f *Flag) error {
	target := "file"
	if f.Path == nil {
		target = "commit"
	}
	if _, err := tx.Exec("UPDATE feedback SET target = ? WHERE target = 'flag' AND flag = ?", target, f.NoteID); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM flag_seen WHERE flag = ?", f.ID); err != nil {
		return err
	}
	_, err := tx.Exec("DELETE FROM flag WHERE id = ?", f.ID)
	return err
}

// findCommit is the entry a full revision id names. In a squashed iteration
// split into topics every entry has the head's revision: the topic picks
// one, else the entry holding the path, else, for a path in none, the
// first; with neither it is a problem.
func (c *sheetContext) findCommit(revision, topic, path json.RawMessage) (*sheetCommit, string) {
	rev, ok := isText(revision)
	if !ok {
		return nil, "must be a commit's revision id"
	}
	var found []*sheetCommit
	for _, e := range c.commits {
		if e.Revision == rev {
			found = append(found, e)
		}
	}
	if len(found) == 0 {
		return nil, fmt.Sprintf("'%s' is not a commit of this iteration; give its full revision id", rev)
	}
	if !isNull(topic) {
		name, ok := isText(topic)
		if !ok {
			return nil, "'topic' must be a topic name"
		}
		for _, e := range found {
			if e.Topic != nil && *e.Topic == name {
				return e, ""
			}
		}
		return nil, fmt.Sprintf("commit %s has no topic '%s'", rev, name)
	}
	if len(found) == 1 {
		return found[0], ""
	}
	p, ok := isText(path)
	if !ok {
		return nil, fmt.Sprintf("commit %s is split into topics; give 'topic'", rev)
	}
	for _, e := range found {
		if e.byPath[p] != nil {
			return e, ""
		}
	}
	return found[0], ""
}

// fileRange reads [start, end] within one side of a file.
func fileRange(f *sheetFile, side string, raw json.RawMessage) (Range, string) {
	r, problem := lineRange(raw)
	if problem != "" {
		return r, problem
	}
	if f.SizeUnit != "lines" {
		return r, fmt.Sprintf("'%s' has no lines", f.Path)
	}
	size := f.SizeAfter
	if side == "old" {
		size = f.SizeBefore
	}
	if size == nil {
		return r, fmt.Sprintf("'%s' has no %s side", f.Path, side)
	}
	if int64(r[1]) > *size {
		return r, fmt.Sprintf("beyond the end of '%s' (%d lines)", f.Path, *size)
	}
	return r, ""
}

func runFill(args []string, in io.Reader, out, warn io.Writer) error {
	o := newOptions()
	rest, err := o.parse(args)
	if err != nil {
		return err
	}
	if len(rest) != 2 {
		return errors.New("usage: review-helper fill FILE SHEET")
	}
	file, sheetFile := rest[0], rest[1]
	data, err := readInput(sheetFile, in)
	if err != nil {
		return fmt.Errorf("cannot read '%s': %w", sheetFile, err)
	}
	if !json.Valid(data) {
		var probe any
		return fmt.Errorf("'%s' is not valid JSON: %v", sheetFile, json.Unmarshal(data, &probe))
	}
	d, repo, j, err := openReview(file, warn)
	if err != nil {
		return err
	}
	defer d.Close()
	res, err := Fill(d, repo, j, data)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "filled %d, remaining %d\n", res.Filled, res.Remaining)
	return nil
}
