package review

import (
	"database/sql"
	"fmt"
	"slices"
)

// The carry-forward (standards/FORMAT.md, "Carry-forward"): what a new
// iteration copies from the previous one.

// storedFile is a file change of the previous iteration with its sections.
type storedFile struct {
	*FileChange
	Sections []*Section
}

// carryForward pairs the new entries with the previous iteration's and
// copies what carries. A commit pairs with the previous commit of the same
// change id (commits sharing one pair in order), a commit with none with the
// same revision. Two squashed iterations over the same base and paths pair
// their entries by topic instead, the new one split by the previous one's
// topics first. It returns the entries to store.
func (b *builder) carryForward(prev *Iteration, sp *span, entries []*newEntry) ([]*newEntry, error) {
	old, err := b.db.Commits(prev.Number)
	if err != nil {
		return nil, err
	}
	squash := sameSquash(prev, sp)
	if squash {
		if entries, err = b.topicEntries(old, entries[0]); err != nil {
			return nil, err
		}
	}
	pick := pairer(old, prev.Squashed || sp.Squashed)
	for _, e := range entries {
		var from *CommitEntry
		if squash {
			from = byTopic(old, e.Topic)
		} else {
			from = pick(e)
		}
		if from == nil {
			continue
		}
		if err := b.carryEntry(e, from, squash); err != nil {
			return nil, err
		}
	}
	if squash && len(entries) > 0 {
		paired := map[int64]bool{}
		for _, e := range entries {
			paired[e.carryFrom] = true
		}
		for _, o := range old {
			if !paired[o.ID] {
				entries[0].carryAlso = append(entries[0].carryAlso, o.ID)
			}
		}
	}
	return entries, nil
}

// pairer gives each new history entry its previous partner, once: by change
// id, first with first, or by revision for a commit with no change id
// (an empty diff), except where either iteration is squashed.
func pairer(old []*CommitEntry, squashed bool) func(*newEntry) *CommitEntry {
	byChange := map[string][]*CommitEntry{}
	byRevision := map[string][]*CommitEntry{}
	for _, o := range old {
		switch {
		case o.ChangeID != nil:
			byChange[*o.ChangeID] = append(byChange[*o.ChangeID], o)
		case !squashed:
			byRevision[o.Revision] = append(byRevision[o.Revision], o)
		}
	}
	take := func(m map[string][]*CommitEntry, key string) *CommitEntry {
		q := m[key]
		if len(q) == 0 {
			return nil
		}
		m[key] = q[1:]
		return q[0]
	}
	return func(e *newEntry) *CommitEntry {
		if e.ChangeID != nil {
			return take(byChange, *e.ChangeID)
		}
		return take(byRevision, e.Revision)
	}
}

func byTopic(old []*CommitEntry, topic *string) *CommitEntry {
	for _, o := range old {
		if (o.Topic == nil) == (topic == nil) && (topic == nil || *o.Topic == *topic) {
			return o
		}
	}
	return nil
}

// sameSquash reports whether both iterations are squashed over the same base
// and path specs.
func sameSquash(prev *Iteration, sp *span) bool {
	return prev.Squashed && sp.Squashed && prev.Base == sp.Base &&
		(prev.Paths == nil) == (sp.Paths == nil) && slices.Equal(prev.Paths, sp.Paths)
}

// topicEntries splits a squashed entry by the previous iteration's topics:
// each file goes to the topic that held its path, in the topics' order, and
// files in none to one entry with no topic, last. A topic left with no file
// is not made. With no topics before, the entry stays whole.
func (b *builder) topicEntries(old []*CommitEntry, entry *newEntry) ([]*newEntry, error) {
	var topics []*CommitEntry
	for _, o := range old {
		if o.Topic != nil {
			topics = append(topics, o)
		}
	}
	if len(topics) == 0 {
		return []*newEntry{entry}, nil
	}
	topicOf := map[string]int{}
	for i, t := range topics {
		files, err := b.db.Files(t.ID)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			topicOf[f.Path] = i
		}
	}
	groups := make([][]*newFile, len(topics)+1)
	for _, f := range entry.Files {
		i, ok := topicOf[f.Path]
		if !ok {
			i = len(topics)
		}
		groups[i] = append(groups[i], f)
	}
	var out []*newEntry
	for i, files := range groups {
		if len(files) == 0 {
			continue
		}
		e := *entry
		e.Files, e.Topic = files, nil
		if i < len(topics) {
			e.Topic = topics[i].Topic
		}
		out = append(out, &e)
	}
	return out, nil
}

// carryEntry copies what carries from the previous entry old to e and its
// files. In a squashed pairing, reading order carries only when both list
// the same paths, and the entry's text only when, besides, every file reads
// the same after both.
func (b *builder) carryEntry(e *newEntry, old *CommitEntry, squash bool) error {
	files, err := b.storedFiles(old.ID)
	if err != nil {
		return err
	}
	order := !squash || samePaths(files, e.Files)
	if !squash {
		e.What, e.Why, e.How = old.What, old.Why, old.How
	}
	if order {
		e.OrderSet = old.OrderSet
	}
	e.carryFrom = old.ID
	same := order
	for _, f := range e.Files {
		o, ok := files[f.Path]
		if !ok {
			same = false
			continue
		}
		readsSame, err := b.carryFile(f, o, old, &e.CommitEntry, squash, order)
		if err != nil {
			return err
		}
		same = same && readsSame
	}
	if squash && same {
		e.What, e.Why, e.How = old.What, old.Why, old.How
	}
	return nil
}

func samePaths(old map[string]*storedFile, files []*newFile) bool {
	if len(old) != len(files) {
		return false
	}
	for _, f := range files {
		if old[f.Path] == nil {
			return false
		}
	}
	return true
}

// carryFile copies a file's reading position (when order says the entries'
// files are the same), its text (in a history pairing; in a squashed one
// only when the file reads the same after both), and its split when the file
// reads the same before and after both commits: a split's ranges are line
// numbers, which a rebase can move. It reports whether the file reads the
// same after both commits (false in a history pairing of an unsplit file,
// where it is not looked at).
func (b *builder) carryFile(f *newFile, old *storedFile, oldEntry, entry *CommitEntry, squash, order bool) (bool, error) {
	if order {
		f.ReadPosition = old.ReadPosition
	}
	if !squash {
		f.What, f.ObjectSystem, f.Paradigms = old.What, old.ObjectSystem, old.Paradigms
	}
	split := old.SplitSource != "none"
	if !squash && !split {
		return false, nil
	}
	after, err := b.repo.FileID(oldEntry.Revision, f.Path)
	if err != nil {
		return false, err
	}
	now, err := b.repo.FileID(entry.Revision, f.Path)
	if err != nil {
		return false, err
	}
	same := after == now
	if squash && same {
		f.What, f.ObjectSystem, f.Paradigms = old.What, old.ObjectSystem, old.Paradigms
	}
	if !split || after == "" || !same {
		return same, nil
	}
	path := f.Path
	if f.OldPath != nil {
		path = *f.OldPath
	}
	from, err := b.parentFileID(oldEntry, path)
	if err != nil {
		return false, err
	}
	to, err := b.parentFileID(entry, path)
	if err != nil {
		return false, err
	}
	if from == to {
		f.SplitSource, f.Sections = old.SplitSource, old.Sections
	}
	return same, nil
}

// parentFileID is the id of path's content before the entry's commit, ""
// when the commit has no parent or the path is absent there.
func (b *builder) parentFileID(e *CommitEntry, path string) (string, error) {
	if e.Parent == nil {
		return "", nil
	}
	return b.repo.FileID(*e.Parent, path)
}

// storedFiles are an entry's file changes with their sections, by path.
func (b *builder) storedFiles(commitID int64) (map[string]*storedFile, error) {
	files, err := b.db.Files(commitID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*storedFile, len(files))
	for _, f := range files {
		sections, err := b.db.Sections(f.ID)
		if err != nil {
			return nil, err
		}
		out[f.Path] = &storedFile{FileChange: f, Sections: sections}
	}
	return out, nil
}

// flagsOf lists an entry's flags, oldest first.
func flagsOf(q querier, commitID int64) ([]*Flag, error) {
	return queryAll(q, scanFlag, "SELECT "+flagCols+", commit_entry.revision, commit_entry.topic"+flagJoin+" WHERE flag.commit_id = ? ORDER BY flag.id", commitID)
}

func insertFlag(tx *sql.Tx, commitID int64, f *Flag) (int64, error) {
	res, err := tx.Exec(`INSERT INTO flag (commit_id, note_id, path, line_start, line_end, color, color_reason, kind, sentence, detail, created, pending)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		commitID, f.NoteID, f.Path, f.LineStart, f.LineEnd, f.Color, f.ColorReason, f.Kind, f.Sentence, f.Detail, f.Created, f.Pending)
	if err != nil {
		return 0, fmt.Errorf("store flag %s: %w", f.NoteID, err)
	}
	return res.LastInsertId()
}

// lineMoves is how lines of a file move from one commit to another.
type lineMoves struct {
	path  string  // where the file is in the new commit
	hunks []*Hunk // the diff between the two; none when it reads the same
	ok    bool    // false: the file is on one side only, or binary and changed
}

// carryFlags copies the flags of the previous entry fromID onto the new
// entry to. A flag whose code is unchanged follows it, to the file's new
// path when the VCS finds it renamed, its lines moved, with its seen marks.
// One whose code changed is pending with no seen marks, on the span of its
// lines the diff kept, else on the whole file.
func (b *builder) carryFlags(tx *sql.Tx, to *CommitEntry, fromID int64) error {
	from, err := queryOne(tx, scanCommit, "SELECT "+commitCols+" FROM commit_entry WHERE id = ?", fromID)
	if err != nil {
		return err
	}
	flags, err := flagsOf(tx, fromID)
	if err != nil {
		return err
	}
	moves := map[string]*lineMoves{}
	for _, f := range flags {
		same := true
		if f.Path != nil {
			m, ok := moves[*f.Path]
			if !ok {
				if m, err = b.movesTo(from, to, *f.Path); err != nil {
					return err
				}
				moves[*f.Path] = m
			}
			same = m.ok && len(m.hunks) == 0
			f.Path = &m.path
			if f.LineStart != nil {
				same = moveFlagLines(f, m)
			}
		}
		if !same {
			f.Pending = true
		}
		id, err := insertFlag(tx, to.ID, f)
		if err != nil {
			return err
		}
		if same {
			if _, err := tx.Exec("INSERT INTO flag_seen (flag, reviewer, at) SELECT ?, reviewer, at FROM flag_seen WHERE flag = ?", id, f.ID); err != nil {
				return fmt.Errorf("copy the seen marks of flag %s: %w", f.NoteID, err)
			}
		}
	}
	return nil
}

// movesTo finds where path went from one commit to another and how its
// lines moved.
func (b *builder) movesTo(from, to *CommitEntry, path string) (*lineMoves, error) {
	newPath, err := b.renamedTo(from, to, path)
	if err != nil {
		return nil, err
	}
	if newPath == "" {
		newPath = path
	}
	hunks, ok, err := b.lineMoves(from, to, path, newPath)
	if err != nil {
		return nil, err
	}
	return &lineMoves{path: newPath, hunks: hunks, ok: ok}, nil
}

// moveFlagLines moves a flag's lines through m and reports whether every
// flagged line survived, in a run. Otherwise the flag takes the span of its
// lines the diff kept, or the whole file when it kept none or the file
// cannot be compared.
func moveFlagLines(f *Flag, m *lineMoves) bool {
	start, end := int(*f.LineStart), int(*f.LineStart)
	if f.LineEnd != nil {
		end = int(*f.LineEnd)
	}
	if !m.ok {
		f.LineStart, f.LineEnd = nil, nil
		return false
	}
	if s, e, ok := MapLines(m.hunks, start, end); ok {
		f.LineStart, f.LineEnd = i64(s), i64(e)
		return true
	}
	if s, e, ok := keptSpan(m.hunks, start, end); ok {
		f.LineStart, f.LineEnd = i64(s), i64(e)
	} else {
		f.LineStart, f.LineEnd = nil, nil
	}
	return false
}

func i64(n int) *int64 {
	v := int64(n)
	return &v
}

// keptSpan is from the first to the last of the lines start..end the diff
// kept, at their new numbers; ok false when it kept none.
func keptSpan(hunks []*Hunk, start, end int) (int, int, bool) {
	moved, _ := MoveRanges(hunks, [][2]int{{start, end}})
	if len(moved) == 0 {
		return 0, 0, false
	}
	return moved[0][0], moved[len(moved)-1][1], true
}

// renamedTo is the path a file at path in one commit has in another when
// the VCS finds it renamed between them; "" when it is not, or still has
// its path.
func (b *builder) renamedTo(from, to *CommitEntry, path string) (string, error) {
	if from.Revision == to.Revision {
		return "", nil
	}
	there, err := b.repo.FileID(to.Revision, path)
	if err != nil || there != "" {
		return "", err
	}
	here, err := b.repo.FileID(from.Revision, path)
	if err != nil || here == "" {
		return "", err
	}
	changes, err := b.repo.RangeChanges(from.Revision, to.Revision, nil)
	if err != nil {
		return "", err
	}
	for _, c := range changes {
		if c.Status == "R" && c.OldPath == path {
			return c.Path, nil
		}
	}
	return "", nil
}

// lineMoves is the diff from path in one commit to toPath in another, so
// line numbers in one can be moved to the other; no hunks when they read the
// same. A file in neither commit (deleted by both) is compared as it was
// before them. ok is false when the file is on one side only, or is a binary
// file that changed.
func (b *builder) lineMoves(from, to *CommitEntry, path, toPath string) ([]*Hunk, bool, error) {
	if from.Revision == to.Revision {
		return nil, true, nil
	}
	fromRev, toRev := from.Revision, to.Revision
	before, err := b.repo.FileID(fromRev, path)
	if err != nil {
		return nil, false, err
	}
	after, err := b.repo.FileID(toRev, toPath)
	if err != nil {
		return nil, false, err
	}
	if before == "" && after == "" {
		if from.Parent == nil || to.Parent == nil {
			return nil, false, nil
		}
		fromRev, toRev = *from.Parent, *to.Parent
		if before, err = b.repo.FileID(fromRev, path); err != nil {
			return nil, false, err
		}
		if after, err = b.repo.FileID(toRev, toPath); err != nil {
			return nil, false, err
		}
	}
	if before == "" || after == "" {
		return nil, false, nil
	}
	if before == after {
		return nil, true, nil
	}
	oldPath := ""
	if toPath != path {
		oldPath = path
	}
	raw, err := b.repo.RangeDiff(fromRev, toRev, toPath, oldPath)
	if err != nil {
		return nil, false, err
	}
	d, err := ParseDiff(string(raw))
	if err != nil {
		return nil, false, fmt.Errorf("diff of %s: %w", toPath, err)
	}
	if d.Binary {
		return nil, false, nil
	}
	return d.Hunks, true, nil
}

// carryUnpairedFlags copies, pending with no seen marks, the flags of every
// previous entry no new entry paired with (its commit was rewritten or
// dropped): each to the last new entry that changes its path, its lines
// moved where the diff keeps any; with none changing its path, or no path,
// to the last entry (by position), on the whole file.
func (b *builder) carryUnpairedFlags(tx *sql.Tx, number int64, entries []*newEntry) error {
	paired := map[int64]bool{}
	for _, e := range entries {
		paired[e.carryFrom] = true
	}
	old, err := queryAll(tx, scanCommit, "SELECT "+commitCols+" FROM commit_entry WHERE iteration = ? ORDER BY position", number-1)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	newest := entries[len(entries)-1]
	for _, from := range old {
		if paired[from.ID] {
			continue
		}
		flags, err := flagsOf(tx, from.ID)
		if err != nil {
			return err
		}
		for _, f := range flags {
			to := lastChanging(entries, f.Path)
			if to == nil {
				to, f.LineStart, f.LineEnd = newest, nil, nil
			} else if f.LineStart != nil {
				hunks, ok, err := b.lineMoves(from, &to.CommitEntry, *f.Path, *f.Path)
				if err != nil {
					return err
				}
				moveFlagLines(f, &lineMoves{path: *f.Path, hunks: hunks, ok: ok})
			}
			f.Pending = true
			if _, err := insertFlag(tx, to.ID, f); err != nil {
				return err
			}
		}
	}
	return nil
}

// lastChanging is the last entry that changes path, or nil.
func lastChanging(entries []*newEntry, path *string) *newEntry {
	if path == nil {
		return nil
	}
	for i := len(entries) - 1; i >= 0; i-- {
		for _, f := range entries[i].Files {
			if f.Path == *path {
				return entries[i]
			}
		}
	}
	return nil
}

// flagsOffChange makes pending, with no seen marks, each carried flag whose
// file left the change in this iteration: no entry of its commit changes its
// path now, while in the previous iteration an entry with that flag's
// commit's revision changed the path the flag had there. A flag kept on a
// file already outside the change carries settled.
func flagsOffChange(tx *sql.Tx, number int64) error {
	ids, err := queryAll(tx, func(s scanner) (*int64, error) {
		var id int64
		return &id, s.Scan(&id)
	}, `SELECT flag.id FROM flag JOIN commit_entry c ON c.id = flag.commit_id
		 WHERE c.iteration = ? AND flag.path IS NOT NULL AND flag.pending = 0
		   AND NOT EXISTS (SELECT 1 FROM file_change f JOIN commit_entry e ON e.id = f.commit_id
		                    WHERE e.iteration = c.iteration AND e.revision = c.revision AND f.path = flag.path)
		   AND EXISTS (SELECT 1 FROM flag p JOIN commit_entry pc ON pc.id = p.commit_id
		                 JOIN file_change pf ON pf.path = p.path
		                 JOIN commit_entry pe ON pe.id = pf.commit_id AND pe.iteration = pc.iteration AND pe.revision = pc.revision
		                WHERE p.note_id = flag.note_id AND pc.iteration = c.iteration - 1)`, number)
	if err != nil {
		return fmt.Errorf("find flags off the change: %w", err)
	}
	for _, id := range ids {
		if _, err := tx.Exec("UPDATE flag SET pending = 1 WHERE id = ?", *id); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM flag_seen WHERE flag = ?", *id); err != nil {
			return err
		}
	}
	return nil
}

// flagsToPaths moves each flag with a path in a squashed iteration to the
// entry holding its path, where its file may have gone.
func flagsToPaths(tx *sql.Tx, number int64) error {
	_, err := tx.Exec(`UPDATE flag SET commit_id = (
		    SELECT f.commit_id FROM file_change f JOIN commit_entry c ON c.id = f.commit_id WHERE c.iteration = ? AND f.path = flag.path)
		 WHERE path IS NOT NULL
		   AND commit_id IN (SELECT id FROM commit_entry WHERE iteration = ?)
		   AND EXISTS (SELECT 1 FROM file_change f JOIN commit_entry c ON c.id = f.commit_id WHERE c.iteration = ? AND f.path = flag.path)`,
		number, number, number)
	if err != nil {
		return fmt.Errorf("move flags to their paths: %w", err)
	}
	return nil
}
