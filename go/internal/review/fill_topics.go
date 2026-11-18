package review

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// The topics item of a squashed iteration: its files grouped into named
// topics, each an entry of its own.

// sameTopics reports whether the topics given are the ones stored: the same
// names in the same order, each with the same paths in any order.
func sameTopics(raw json.RawMessage, stored []Topic) bool {
	given, ok := jsonArray(raw)
	if !ok || stored == nil || len(given) != len(stored) {
		return false
	}
	for n, t := range given {
		obj, ok := jsonObject(t)
		if !ok || len(obj) != 2 {
			return false
		}
		name, ok := jsonString(obj["name"])
		if !ok || name != stored[n].Name {
			return false
		}
		a, ok := jsonArray(obj["paths"])
		if !ok {
			return false
		}
		paths := make([]string, len(a))
		for i, p := range a {
			if paths[i], ok = jsonString(p); !ok {
				return false
			}
		}
		sort.Strings(paths)
		if !slices.Equal(paths, stored[n].Paths) { // stored paths are sorted
			return false
		}
	}
	return true
}

// checkTopics takes every path of the iteration in exactly one topic, each
// topic named once.
func (v *validator) checkTopics(it *sheetItem, fields map[string]json.RawMessage) (fillWrite, []string) {
	list, ok := jsonArray(fields["topics"])
	if !ok || len(list) == 0 {
		return nil, []string{"'topics' must be a non-empty array of { name, paths }"}
	}
	all := it.About.(TopicsAbout).Paths
	var problems []string
	names := map[string]bool{}
	in := map[string]int{}
	topics := make([]Topic, 0, len(list))
	for i, raw := range list {
		n := i + 1
		obj, ok := jsonObject(raw)
		if !ok {
			problems = append(problems, fmt.Sprintf("topic %d: must be an object with 'name' and 'paths'", n))
			continue
		}
		var unknown []string
		for k := range obj {
			if k != "name" && k != "paths" {
				unknown = append(unknown, k)
			}
		}
		sort.Strings(unknown)
		for _, k := range unknown {
			problems = append(problems, fmt.Sprintf("topic %d: unknown field '%s'", n, k))
		}

		name, ok := jsonText(obj["name"])
		name = strings.TrimSpace(name)
		switch {
		case !ok || name == "" || strings.IndexFunc(name, unicode.IsControl) >= 0:
			problems = append(problems, fmt.Sprintf("topic %d: 'name' must be a non-empty string with no control character", n))
		case names[name]:
			problems = append(problems, fmt.Sprintf("topic %d: name '%s' repeats", n, name))
		}
		names[name] = true

		paths, ok := texts(obj["paths"])
		if !ok || len(paths) == 0 {
			problems = append(problems, fmt.Sprintf("topic %d: 'paths' must be a non-empty array of paths", n))
			continue
		}
		for _, p := range paths {
			switch {
			case !slices.Contains(all, p):
				problems = append(problems, fmt.Sprintf("topic %d: '%s' is not a path of this iteration", n, p))
			case in[p] != 0:
				problems = append(problems, fmt.Sprintf("topic %d: '%s' is already in topic %d", n, p, in[p]))
			default:
				in[p] = n
			}
		}
		topics = append(topics, Topic{Name: name, Paths: paths})
	}
	var missing []string
	for _, p := range all {
		if in[p] == 0 {
			missing = append(missing, "'"+p+"'")
		}
	}
	if len(missing) > 0 {
		problems = append(problems, "in no topic: "+strings.Join(missing, ", "))
	}
	if len(problems) > 0 {
		return nil, problems
	}
	v.plan.topics = true
	return func(tx *sql.Tx) error { return v.writeTopics(tx, topics) }, nil
}

func pathKey(paths []string) string {
	s := slices.Clone(paths)
	sort.Strings(s)
	return strings.Join(s, "\x00")
}

// writeTopics makes the iteration's entries the topics, in order. A topic
// keeps the entry of its name, else an entry with exactly its paths, else
// gets a new one; a kept entry whose paths change loses its text and
// reading order. Files and flags follow their paths; a flag with no path
// stays on its entry, or goes to the first topic when its entry goes.
// Commit feedback and requests of this iteration on an entry that goes
// become review ones, keeping the commit and topic as context.
func (v *validator) writeTopics(tx *sql.Tx, topics []Topic) error {
	number := v.iteration.Number
	entries := v.commits
	files := map[int64]string{}
	fileID := map[string]int64{}
	named := map[string]*sheetCommit{}
	for _, e := range entries {
		files[e.ID] = pathKey(entryPaths(e))
		for _, f := range e.files {
			fileID[f.Path] = f.ID
		}
		if e.Topic != nil {
			named[*e.Topic] = e
		}
	}

	pick := make([]*sheetCommit, len(topics))
	kept := map[int64]bool{}
	for n, t := range topics {
		if e := named[t.Name]; e != nil {
			pick[n], kept[e.ID] = e, true
		}
	}
	for n, t := range topics {
		if pick[n] != nil {
			continue
		}
		for _, e := range entries {
			if !kept[e.ID] && files[e.ID] == pathKey(t.Paths) {
				pick[n], kept[e.ID] = e, true
				break
			}
		}
	}

	// Out of the way of the positions the topics take.
	if _, err := tx.Exec("UPDATE commit_entry SET position = -id WHERE iteration = ?", number); err != nil {
		return err
	}
	entryOf := map[string]int64{}
	ids := make([]int64, len(topics))
	for n, t := range topics {
		var err error
		if e := pick[n]; e != nil {
			err = keepEntry(tx, number, e, t.Name, n+1, files[e.ID] == pathKey(t.Paths))
			ids[n] = e.ID
		} else {
			ids[n], err = insertTopicEntry(tx, entries[0].CommitEntry, t.Name, n+1)
		}
		if err != nil {
			return err
		}
		for _, p := range t.Paths {
			if _, err := tx.Exec("UPDATE file_change SET commit_id = ? WHERE id = ?", ids[n], fileID[p]); err != nil {
				return err
			}
			entryOf[p] = ids[n]
		}
	}

	for _, f := range v.flags {
		var to int64
		if f.Path != nil {
			to = entryOf[*f.Path]
		}
		if to == 0 && !kept[f.CommitID] {
			to = ids[0]
		}
		if to != 0 {
			if _, err := tx.Exec("UPDATE flag SET commit_id = ? WHERE id = ?", to, f.ID); err != nil {
				return err
			}
		}
	}

	// Feedback on the entry with no topic keeps a topic of '' when the
	// iteration had topics: it was on the files not in a topic yet.
	hadTopics := len(named) > 0
	for _, e := range entries {
		if kept[e.ID] {
			continue
		}
		was := e.Topic
		if was == nil && hadTopics {
			was = new(string)
		}
		for _, table := range []string{"feedback", "request"} {
			if _, err := tx.Exec("UPDATE "+table+" SET target = 'review', topic = ? WHERE iteration = ? AND target = 'commit' AND revision = ? AND topic IS ?",
				was, number, e.Revision, e.Topic); err != nil {
				return err
			}
		}
		if _, err := tx.Exec("DELETE FROM commit_entry WHERE id = ?", e.ID); err != nil {
			return err
		}
	}
	return nil
}

// keepEntry renames and places an entry kept for a topic, its commit
// feedback and requests taking the new name; its text and reading order are
// cleared unless its paths stay the same.
func keepEntry(tx *sql.Tx, number int64, e *sheetCommit, name string, position int, same bool) error {
	if e.Topic == nil || *e.Topic != name {
		for _, table := range []string{"feedback", "request"} {
			if _, err := tx.Exec("UPDATE "+table+" SET topic = ? WHERE iteration = ? AND target = 'commit' AND revision = ? AND topic IS ?",
				name, number, e.Revision, e.Topic); err != nil {
				return err
			}
		}
	}
	q := "UPDATE commit_entry SET topic = ?, position = ?"
	if !same {
		q += ", what = NULL, why = NULL, how = NULL, order_set = 0"
	}
	_, err := tx.Exec(q+" WHERE id = ?", name, position, e.ID)
	return err
}

// insertTopicEntry stores a topic's new entry: the squashed entry's commit facts, no
// text.
func insertTopicEntry(tx *sql.Tx, from *CommitEntry, name string, position int) (int64, error) {
	res, err := tx.Exec(`INSERT INTO commit_entry (position, topic, iteration, revision, parent, subject, message, author, authored)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, position, name, from.Iteration, from.Revision, from.Parent, from.Subject, from.Message, from.Author, from.Authored)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
