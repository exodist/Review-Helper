package review

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"unicode/utf8"
)

// The guide item (standards/FORMAT.md, "Guide JSON"): its steps' targets and
// highlights must exist in the iteration, and its commentary must read
// aloud.

// targetKeys are the keys each target type takes besides its type.
var targetKeys = map[string][]string{
	"summary": {},
	"commit":  {"commit", "topic"},
	"file":    {"commit", "path"},
	"section": {"commit", "path", "section"},
	"flag":    {"flag"},
	"diff":    {"commit", "topic", "path", "section"},
	"view":    {"commit", "path", "lines"},
}

var codeToken = regexp.MustCompile("`|->|::|\\(\\)|[$@%][\\p{L}\\p{M}\\p{N}\\p{Pc}]")

func (v *validator) checkGuide(it *sheetItem, fields map[string]json.RawMessage) (fillWrite, []string) {
	steps, ok := jsonArray(fields["steps"])
	if !ok || len(steps) == 0 {
		return nil, []string{"'steps' must be a non-empty array"}
	}
	var problems []string
	stored := make([]*GuideStep, len(steps))
	for n, raw := range steps {
		step, errs := v.checkStep(raw)
		for _, e := range errs {
			problems = append(problems, fmt.Sprintf("step %d: %s", n+1, e))
		}
		stored[n] = step
	}
	if len(problems) > 0 {
		return nil, problems
	}
	number := v.iteration.Number
	return func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM guide_step WHERE iteration = ?", number); err != nil {
			return err
		}
		for n, s := range stored {
			_, err := tx.Exec("INSERT INTO guide_step (iteration, position, target, highlight, commentary) VALUES (?, ?, ?, ?, ?)",
				number, n+1, string(s.Target), string(s.Highlight), s.Commentary)
			if err != nil {
				return err
			}
		}
		return nil
	}, nil
}

// checkStep checks one step and returns it as stored: its target and
// highlight in canonical JSON.
func (v *validator) checkStep(raw json.RawMessage) (*GuideStep, []string) {
	obj, ok := jsonObject(raw)
	if !ok {
		return nil, []string{"must be an object"}
	}
	var problems []string
	var unknown []string
	for k := range obj {
		if k != "target" && k != "highlight" && k != "commentary" {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		problems = append(problems, fmt.Sprintf("unknown field '%s'", k))
	}
	commentary, ok := isText(obj["commentary"])
	switch {
	case !ok:
		problems = append(problems, "'commentary' must be a non-empty string")
	case utf8.RuneCountInString(commentary) > 400:
		problems = append(problems, "'commentary' is over 400 characters")
	case codeToken.MatchString(commentary):
		problems = append(problems, "'commentary' must be read aloud: no backticks, '->', '::', '()', or a sigil before a word")
	}

	e, view, targetErrs := v.checkTarget(obj["target"])
	for _, p := range targetErrs {
		problems = append(problems, "target: "+p)
	}

	highlight := obj["highlight"]
	if isNull(highlight) {
		highlight = json.RawMessage("[]")
	}
	marks, ok := jsonArray(highlight)
	switch {
	case !ok:
		problems = append(problems, "'highlight' must be an array")
	case len(marks) > 0 && e == nil:
		if len(targetErrs) == 0 {
			problems = append(problems, "'highlight' needs a target in a commit")
		}
	case e != nil:
		for _, m := range marks {
			if p := v.checkHighlight(e, m, view); p != "" {
				problems = append(problems, "highlight: "+p)
			}
		}
	}
	if len(problems) > 0 {
		return nil, problems
	}
	target, err := canonicalJSON(obj["target"])
	if err != nil {
		return nil, []string{err.Error()}
	}
	marked, err := canonicalJSON(highlight)
	if err != nil {
		return nil, []string{err.Error()}
	}
	return &GuideStep{Target: target, Highlight: marked, Commentary: commentary}, nil
}

// checkTarget returns the entry a target is in (nil for the summary),
// whether it is a view, and its problems.
func (v *validator) checkTarget(raw json.RawMessage) (*sheetCommit, bool, []string) {
	t, ok := jsonObject(raw)
	typ, typeOK := jsonText(t["type"])
	if !ok || !typeOK {
		return nil, false, []string{"must be an object with a type"}
	}
	keys, known := targetKeys[typ]
	if !known {
		return nil, false, []string{fmt.Sprintf("unknown type '%s'", typ)}
	}
	var unknown []string
	for k := range t {
		if k != "type" && !slices.Contains(keys, k) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		var problems []string
		for _, k := range unknown {
			problems = append(problems, fmt.Sprintf("unknown field '%s'", k))
		}
		return nil, false, problems
	}
	switch typ {
	case "summary":
		return nil, false, nil
	case "flag":
		e, p := v.checkFlagTarget(t["flag"])
		return e, false, problemList(p)
	}

	e, p := v.findCommit(t["commit"], t["topic"], t["path"])
	if p != "" {
		return nil, false, []string{"'commit': " + p}
	}
	switch {
	case typ == "commit" || (typ == "diff" && isNull(t["path"])):
		return e, false, nil
	case typ == "view":
		return e, true, problemList(v.checkView(e, "new", t["path"], t["lines"]))
	}
	path, _ := isText(t["path"])
	f := e.byPath[path]
	if f == nil {
		return e, false, []string{fmt.Sprintf("'path' must be a path changed in commit %s", e.Revision)}
	}
	if typ == "file" || (typ == "diff" && isNull(t["section"])) {
		return e, false, nil
	}
	return e, false, problemList(checkSectionTarget(v.plan, f, t["section"]))
}

func problemList(p string) []string {
	if p == "" {
		return nil
	}
	return []string{p}
}

// checkView takes a file as of any commit of the iteration, changed there
// or not, on the new side. Its lines are checked for shape only: a display
// marks the lines that exist.
func (v *validator) checkView(e *sheetCommit, side string, pathRaw, lines json.RawMessage) string {
	if side != "new" {
		return "'side' must be 'new' on a view step"
	}
	path, ok := isText(pathRaw)
	if ok {
		id, err := v.repo.FileID(e.Revision, path)
		if err != nil {
			v.fail(err)
		}
		ok = id != ""
	}
	if !ok {
		return fmt.Sprintf("'path' must be a path in commit %s", e.Revision)
	}
	if _, p := lineRange(lines); p != "" {
		return "'lines': " + p
	}
	return ""
}

// checkFlagTarget finds the entry of a flag, by note id: one placed in this
// sheet, or the iteration's, unless this sheet retires it.
func (v *validator) checkFlagTarget(raw json.RawMessage) (*sheetCommit, string) {
	id, ok := isText(raw)
	if !ok {
		return nil, "'flag' must be a note id"
	}
	if e := v.plan.notes[id]; e != nil {
		return e, ""
	}
	if v.plan.retired[id] {
		return nil, fmt.Sprintf("flag '%s' is retired in this sheet", id)
	}
	entryID, ok := v.flagEntry[id]
	if !ok {
		return nil, fmt.Sprintf("no flag '%s' in this iteration; 'flag' takes a note id", id)
	}
	return v.entry(entryID), ""
}

// checkSectionTarget takes a touched section of the file, as this sheet
// splits it when it does (plan may be nil).
func checkSectionTarget(plan *fillPlan, f *sheetFile, raw json.RawMessage) string {
	obj, ok := jsonObject(raw)
	name, nameOK := isText(obj["name"])
	if !ok || !nameOK {
		return "'section' must be an object with a 'name'"
	}
	pkg, _ := jsonText(obj["package"])
	sections := f.sections
	if plan != nil {
		if split, ok := plan.splits[f.ID]; ok {
			sections = split
		}
	}
	for _, s := range sections {
		if s.Name == name && orBlank(s.Package) == pkg {
			if s.Touched() {
				return ""
			}
			return fmt.Sprintf("section '%s' in '%s' has no change to show", name, f.Path)
		}
	}
	return fmt.Sprintf("no section '%s' in '%s'", name, f.Path)
}

// checkHighlight takes lines of a file the target's commit changed, on
// either side, or, on a view, of the file viewed.
func (v *validator) checkHighlight(e *sheetCommit, raw json.RawMessage, view bool) string {
	m, ok := jsonObject(raw)
	if !ok {
		return "must be an object with path, side and lines"
	}
	side, _ := jsonText(m["side"])
	if view {
		return v.checkView(e, side, m["path"], m["lines"])
	}
	path, _ := isText(m["path"])
	if e.byPath[path] == nil {
		return fmt.Sprintf("'path' must be a path changed in commit %s", e.Revision)
	}
	if side != "old" && side != "new" {
		return "'side' must be 'old' or 'new'"
	}
	if _, p := lineRange(m["lines"]); p != "" {
		return "'lines': " + p
	}
	return ""
}

// staleStep reports whether a stored step names what the iteration no
// longer shows: a flag it no longer has (one retired), a commit, topic or
// path a topics fill took away, or a section a re-split removed or left
// untouched. The guide is then asked for again.
func (c *sheetContext) staleStep(target json.RawMessage) bool {
	t, ok := jsonObject(target)
	if !ok {
		return false
	}
	if typ, _ := jsonText(t["type"]); typ == "flag" {
		id, _ := jsonText(t["flag"])
		_, held := c.flagEntry[id]
		return !held
	}
	if isNull(t["commit"]) {
		return false
	}
	e, p := c.findCommit(t["commit"], t["topic"], t["path"])
	if p != "" {
		return true
	}
	typ, _ := jsonText(t["type"])
	if isNull(t["path"]) || typ == "view" {
		return false
	}
	path, _ := jsonText(t["path"])
	f := e.byPath[path]
	if f == nil {
		return true
	}
	if isNull(t["section"]) {
		return false
	}
	return checkSectionTarget(nil, f, t["section"]) != ""
}
