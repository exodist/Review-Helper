package review

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/exodist/Review-Helper/go/internal/git"
)

// Building a review and adding iterations (standards/CLI.md, build and
// iterate; standards/FORMAT.md, "Meaning"). The carry-forward from one
// iteration to the next is in carryforward.go.

// span is what an iteration covers: base..head, squashed or not, limited to
// path specs (nil: every path).
type span struct {
	Base, Head string
	Squashed   bool
	Paths      []string
}

// newEntry is a commit entry not stored yet, with its file changes and what
// the carry-forward gave it.
type newEntry struct {
	CommitEntry
	Files []*newFile

	mergeRevision string  // the merge that brought it in; "" on the mainline
	carryFrom     int64   // the previous iteration's entry it pairs with; 0: none
	carryAlso     []int64 // entries no new entry pairs with whose flags it takes
}

// newFile is a file change not stored yet, with its sections (carried only).
type newFile struct {
	FileChange
	Sections []*Section
}

// builder builds the iterations of one review file.
type builder struct {
	repo    *git.Repo
	db      *DB
	journal *Journal
	warn    io.Writer
}

func newBuilder(repo *git.Repo, warn io.Writer) (*builder, error) {
	j, err := journalOf(repo, warn)
	if err != nil {
		return nil, err
	}
	return &builder{repo: repo, journal: j, warn: warn}, nil
}

func (b *builder) warnf(format string, args ...any) {
	fmt.Fprintf(b.warn, format+"\n", args...)
}

func runBuild(args []string, out, warn io.Writer) error {
	var base, head, title, source, file string
	var squash bool
	var paths []string
	o := newOptions()
	o.str("base", &base)
	o.str("head", &head)
	o.flag("squash", &squash)
	o.list("path", &paths)
	o.str("title", &title)
	o.str("source", &source)
	o.path("file", &file)
	rest, err := o.parse(args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || (!o.given["base"] && !squash) {
		return errors.New("usage: review-helper build [--base R] [--head R] [--squash] [--path P]... [--title T] [--source URL] [--file P]\n--base is required unless --squash")
	}
	if !o.given["head"] {
		head = "HEAD"
	}
	repo, err := cwdRepo()
	if err != nil {
		return err
	}
	b, err := newBuilder(repo, warn)
	if err != nil {
		return err
	}
	path, err := b.build(buildRequest{
		base: base, baseGiven: o.given["base"], head: head, squash: squash, paths: paths,
		title: title, source: source, file: file,
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(out, path)
	return nil
}

func runIterate(args []string, out, warn io.Writer) error {
	var base, head string
	o := newOptions()
	o.str("base", &base)
	o.str("head", &head)
	rest, err := o.parse(args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errors.New("usage: review-helper iterate FILE [--base R] [--head R]")
	}
	if !o.given["head"] {
		head = "HEAD"
	}
	file := rest[0]
	d, err := OpenDB(file)
	if err != nil {
		return err
	}
	defer d.Close()
	repo, err := git.Find(file)
	if err != nil {
		return err
	}
	b, err := newBuilder(repo, warn)
	if err != nil {
		return err
	}
	b.db = d
	number, err := b.iterate(base, o.given["base"], head)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, number)
	return nil
}

// buildRequest is what build was asked for.
type buildRequest struct {
	base      string
	baseGiven bool
	head      string
	squash    bool
	paths     []string
	title     string
	source    string
	file      string // "" for the default
}

// build creates the review file with iteration 1, pins and registers it, and
// returns the file's absolute path.
func (b *builder) build(req buildRequest) (string, error) {
	sp, err := b.buildSpan(req)
	if err != nil {
		return "", err
	}
	entries, err := b.collect(sp)
	if err != nil {
		return "", err
	}
	file := req.file
	if file == "" {
		if file, err = b.defaultFile(req.head, sp.Head); err != nil {
			return "", err
		}
	}
	if file, err = absReviewPath(file); err != nil {
		return "", err
	}
	if err := checkReviewLocation(b.repo, file); err != nil {
		return "", err
	}
	if !utf8.ValidString(file) {
		return "", fmt.Errorf("review file path %q is not valid UTF-8", file)
	}
	d, err := CreateDB(file, NewReview{Title: req.title, Source: req.source})
	if err != nil {
		return "", err
	}
	b.db = d
	defer d.Close()
	if err := b.store(1, sp, entries); err != nil {
		d.Close()
		os.Remove(file)
		return "", err
	}
	rv, err := d.Review()
	if err != nil {
		return "", err
	}
	if err := NewRegistry(b.repo).Register(rv.ID, file, "open"); err != nil {
		return "", err
	}
	return file, nil
}

func (b *builder) buildSpan(req buildRequest) (*span, error) {
	sp := &span{Squashed: req.squash}
	for _, p := range req.paths {
		if p == "" {
			return nil, errors.New("a path spec may not be empty")
		}
	}
	if len(req.paths) > 0 {
		sp.Paths = req.paths
	}
	var err error
	if req.baseGiven {
		sp.Base, err = b.repo.Resolve(req.base)
	} else {
		sp.Base, err = b.repo.EmptyRevision()
	}
	if err != nil {
		return nil, err
	}
	if sp.Head, err = b.repo.Resolve(req.head); err != nil {
		return nil, err
	}
	return sp, nil
}

// iterate adds the next iteration, keeping the previous one's squash mode
// and path specs, and returns its number.
func (b *builder) iterate(base string, baseGiven bool, head string) (int64, error) {
	prev, err := b.db.LatestIteration()
	if err != nil {
		return 0, err
	}
	if prev == nil {
		return 0, errors.New("the review has no iteration to follow")
	}
	if !baseGiven {
		base = prev.Base
	}
	sp := &span{Squashed: prev.Squashed, Paths: prev.Paths}
	empty, err := b.repo.EmptyRevision()
	if err != nil {
		return 0, err
	}
	sp.Base = base
	if base != empty {
		if sp.Base, err = b.repo.Resolve(base); err != nil {
			return 0, err
		}
	}
	if sp.Head, err = b.repo.Resolve(head); err != nil {
		return 0, err
	}
	entries, err := b.collect(sp)
	if err != nil {
		return 0, err
	}
	if entries, err = b.carryForward(prev, sp, entries); err != nil {
		return 0, err
	}
	number := prev.Number + 1
	return number, b.store(number, sp, entries)
}

// collect lists the entries of a span, before any carry-forward.
func (b *builder) collect(sp *span) ([]*newEntry, error) {
	if sp.Squashed {
		e, err := b.collectSquashed(sp)
		if err != nil {
			return nil, err
		}
		return []*newEntry{e}, nil
	}
	return b.collectHistory(sp)
}

// collectHistory lists base..head oldest first, each commit a merge brought
// in under that merge, less those path specs leave out.
func (b *builder) collectHistory(sp *span) ([]*newEntry, error) {
	revs, err := b.repo.Revisions(sp.Base, sp.Head)
	if err != nil {
		return nil, err
	}
	if len(revs) == 0 {
		return nil, fmt.Errorf("no commits in %s..%s", sp.Base, sp.Head)
	}
	parents := make(map[string][]string, len(revs))
	order := make([]string, len(revs))
	for i, r := range revs {
		parents[r.Revision], order[i] = r.Parents, r.Revision
	}
	owners := mergeOwners(parents, order, sp.Head)
	entries := make([]*newEntry, 0, len(revs))
	for _, r := range revs {
		e, err := b.collectCommit(r.Revision, len(r.Parents) > 1, sp.Paths)
		if err != nil {
			return nil, err
		}
		e.mergeRevision = owners[r.Revision]
		entries = append(entries, e)
	}
	if sp.Paths == nil {
		return entries, nil
	}
	kept := dropUntouched(entries)
	if len(kept) == 0 {
		return nil, fmt.Errorf("no commits in %s..%s change the given paths", sp.Base, sp.Head)
	}
	return kept, nil
}

// collectSquashed is the one combined entry for base..head: the head's commit
// text, the base as its parent (none for the empty revision), no change id.
func (b *builder) collectSquashed(sp *span) (*newEntry, error) {
	changes, err := b.repo.RangeChanges(sp.Base, sp.Head, sp.Paths)
	if err != nil {
		return nil, err
	}
	if len(changes) == 0 {
		under := ""
		if sp.Paths != nil {
			under = " under the given paths"
		}
		return nil, fmt.Errorf("no changes in %s..%s%s", sp.Base, sp.Head, under)
	}
	info, err := b.repo.Info(sp.Head)
	if err != nil {
		return nil, err
	}
	empty, err := b.repo.EmptyRevision()
	if err != nil {
		return nil, err
	}
	info.Parent = sp.Base
	if sp.Base == empty {
		info.Parent = ""
	}
	e := entryFrom(info, "")
	for _, c := range changes {
		f, err := b.collectFile(info, c)
		if err != nil {
			return nil, err
		}
		e.Files = append(e.Files, f)
	}
	return e, nil
}

// collectCommit is one commit's entry. A merge lists no file changes: the
// commits it brought in hold them.
func (b *builder) collectCommit(revision string, merge bool, paths []string) (*newEntry, error) {
	info, err := b.repo.Info(revision)
	if err != nil {
		return nil, err
	}
	changeID, err := b.repo.ChangeID(revision)
	if err != nil {
		return nil, err
	}
	e := entryFrom(info, changeID)
	if merge {
		return e, nil
	}
	from := info.Parent
	if from == "" {
		if from, err = b.repo.EmptyRevision(); err != nil {
			return nil, err
		}
	}
	changes, err := b.repo.RangeChanges(from, revision, paths)
	if err != nil {
		return nil, err
	}
	for _, c := range changes {
		f, err := b.collectFile(info, c)
		if err != nil {
			return nil, err
		}
		e.Files = append(e.Files, f)
	}
	return e, nil
}

func entryFrom(info *git.RevisionInfo, changeID string) *newEntry {
	e := &newEntry{CommitEntry: CommitEntry{
		Revision: info.Revision,
		Parent:   optional(info.Parent),
		Subject:  info.Subject,
		Message:  info.Message,
		Author:   optional(info.Author),
		Authored: &info.Authored,
		ChangeID: optional(changeID),
	}}
	return e
}

// optional is nil for "".
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// collectFile describes one file a commit (or a squashed range) changes:
// its kind, sizes and line counts. It starts unsplit.
func (b *builder) collectFile(info *git.RevisionInfo, c *git.Change) (*newFile, error) {
	f := &newFile{FileChange: FileChange{
		Path: c.Path, OldPath: optional(c.OldPath), Status: c.Status,
		Kind: "binary", SizeUnit: "bytes", SplitSource: "none",
	}}
	// A submodule entry has no content in this repository.
	if c.Submodule {
		return f, nil
	}
	var before, after []byte
	var err error
	if info.Parent != "" && c.Status != "A" {
		old := c.Path
		if c.OldPath != "" {
			old = c.OldPath
		}
		if before, err = b.repo.File(info.Parent, old); err != nil {
			return nil, err
		}
	}
	if c.Status != "D" {
		if after, err = b.repo.File(info.Revision, c.Path); err != nil {
			return nil, err
		}
	}
	first := ""
	if !c.Binary {
		text := after
		if text == nil {
			text = before
		}
		first, _, _ = strings.Cut(string(text), "\n")
	}
	f.Kind = KindFor(c.Path, first)
	if c.Binary && f.Kind != "image" {
		f.Kind = "binary"
	}
	if f.Kind == "image" || f.Kind == "binary" {
		f.SizeBefore, f.SizeAfter = byteSize(before), byteSize(after)
		return f, nil
	}
	f.SizeUnit = "lines"
	f.SizeBefore, f.SizeAfter = lineSize(before), lineSize(after)
	from := info.Parent
	if from == "" {
		if from, err = b.repo.EmptyRevision(); err != nil {
			return nil, err
		}
	}
	raw, err := b.repo.RangeDiff(from, info.Revision, c.Path, c.OldPath)
	if err != nil {
		return nil, err
	}
	d, err := ParseDiff(string(raw))
	if err != nil {
		return nil, fmt.Errorf("diff of %s: %w", c.Path, err)
	}
	n := CountLines(d.Hunks)
	f.Added, f.Removed, f.Changed = int64(n.Added), int64(n.Removed), int64(n.Changed)
	return f, nil
}

func byteSize(content []byte) *int64 {
	if content == nil {
		return nil
	}
	n := int64(len(content))
	return &n
}

func lineSize(content []byte) *int64 {
	if content == nil {
		return nil
	}
	n := int64(len(SplitLines(string(content))))
	return &n
}

// mergeOwners maps each commit a merge brought in to that merge: the commits
// reachable from its second and later parents but not from its first, nor
// on the mainline (the first-parent chain from head). Outer merges go first,
// and a merge takes only commits its own owner holds, so a nested merge
// keeps its commits and a back-merge of the mainline takes none. parents
// covers the range; order is oldest first.
func mergeOwners(parents map[string][]string, order []string, head string) map[string]string {
	mainline := map[string]bool{}
	for rev := head; ; {
		ps, ok := parents[rev]
		if !ok {
			break
		}
		mainline[rev] = true
		if len(ps) == 0 {
			break
		}
		rev = ps[0]
	}
	owner := map[string]string{}
	for i := len(order) - 1; i >= 0; i-- {
		merge := order[i]
		ps := parents[merge]
		if len(ps) < 2 {
			continue
		}
		outer := owner[merge]
		own := ancestors(parents, ps[:1])
		for rev := range ancestors(parents, ps[1:]) {
			if !own[rev] && !mainline[rev] && owner[rev] == outer {
				owner[rev] = merge
			}
		}
	}
	return owner
}

// ancestors is the given commits and their ancestors, within the range
// parents covers.
func ancestors(parents map[string][]string, start []string) map[string]bool {
	seen := map[string]bool{}
	todo := append([]string{}, start...)
	for len(todo) > 0 {
		rev := todo[0]
		todo = todo[1:]
		ps, ok := parents[rev]
		if seen[rev] || !ok {
			continue
		}
		seen[rev] = true
		todo = append(todo, ps...)
	}
	return seen
}

// dropUntouched leaves out commits that change no file under the path
// specs, and merges left with no commits. A merge's commits come before it,
// so one pass finds them.
func dropUntouched(entries []*newEntry) []*newEntry {
	hasCommits := map[string]bool{}
	var kept []*newEntry
	for _, e := range entries {
		if len(e.Files) == 0 && !hasCommits[e.Revision] {
			continue
		}
		kept = append(kept, e)
		if e.mergeRevision != "" {
			hasCommits[e.mergeRevision] = true
		}
	}
	return kept
}

// store writes iteration number with its entries in one transaction, takes
// the journal's entries into it, then pins its head.
func (b *builder) store(number int64, sp *span, entries []*newEntry) error {
	rv, err := b.db.Review()
	if err != nil {
		return err
	}
	pin := git.PinName(rv.ID, int(number))
	notes, err := b.journalEntries()
	if err != nil {
		return err
	}
	err = b.db.Txn(func(tx *sql.Tx) error {
		var paths any
		if sp.Paths != nil {
			paths = MustJSON(sp.Paths)
		}
		if _, err := tx.Exec("INSERT INTO iteration (number, base, head, squashed, paths, pin, created) VALUES (?, ?, ?, ?, ?, ?, ?)",
			number, sp.Base, sp.Head, sp.Squashed, paths, pin, now()); err != nil {
			return fmt.Errorf("store iteration %d: %w", number, err)
		}
		if err := b.storeEntries(tx, number, entries); err != nil {
			return err
		}
		if number > 1 {
			if !sp.Squashed {
				if err := b.carryUnpairedFlags(tx, number, entries); err != nil {
					return err
				}
			}
			if err := closeOrphanedRequests(tx, number); err != nil {
				return err
			}
			if err := flagsOffChange(tx, number); err != nil {
				return err
			}
		}
		if sp.Squashed {
			if err := flagsToPaths(tx, number); err != nil {
				return err
			}
		}
		return storeJournal(tx, number, notes)
	})
	if err != nil {
		return err
	}
	// Only a committed iteration is pinned: a failed one leaves no pin.
	if err := b.repo.Pin(pin, sp.Head); err != nil {
		return err
	}
	// The iteration is in; a failed removal must not undo it. An entry left
	// behind is ignored by the next build or iteration (INSERT OR IGNORE).
	if len(notes) > 0 {
		ids := make([]string, len(notes))
		for i, n := range notes {
			ids[i] = n.ID.Value
		}
		if err := b.journal.Remove(ids...); err != nil {
			b.warnf("could not remove the stored journal entries from the journal: %v", err)
		}
	}
	return nil
}

// journalEntries are the journal's notes without a flag. A bad one is
// skipped with a warning and left in the journal, so it blocks no build.
func (b *builder) journalEntries() ([]*Note, error) {
	notes, err := b.journal.Notes()
	if err != nil {
		return nil, err
	}
	var entries []*Note
	for _, n := range notes {
		if n.Flag.Set {
			continue
		}
		if problem := n.Problem(); problem != "" {
			b.warnf("skipping journal entry %s: %s", n.ID.Value, problem)
			continue
		}
		entries = append(entries, n)
	}
	return entries, nil
}

// storeJournal stores the entries. One already in the file was taken by an
// earlier build whose removal from the journal failed; it stays there.
func storeJournal(tx *sql.Tx, number int64, notes []*Note) error {
	for _, n := range notes {
		if _, err := tx.Exec("INSERT OR IGNORE INTO journal_entry (id, iteration, kind, path, unit, sentence, detail, created) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			n.ID.Value, number, n.Kind.Value, n.Path.Value, n.Unit.Ptr(), n.Sentence.Value, n.Detail.Ptr(), n.CreatedAt()); err != nil {
			return fmt.Errorf("store journal entry %s: %w", n.ID.Value, err)
		}
	}
	return nil
}

// storeEntries inserts the entries in order with their flags and files,
// then links each merged-in commit to its merge.
func (b *builder) storeEntries(tx *sql.Tx, number int64, entries []*newEntry) error {
	ids := map[string]int64{}
	for i, e := range entries {
		e.Iteration, e.Position = number, int64(i+1)
		id, err := insertEntry(tx, &e.CommitEntry)
		if err != nil {
			return err
		}
		e.ID = id
		ids[e.Revision] = id
		from := e.carryAlso
		if e.carryFrom != 0 {
			from = append([]int64{e.carryFrom}, from...)
		}
		for _, old := range from {
			if err := b.carryFlags(tx, &e.CommitEntry, old); err != nil {
				return err
			}
		}
		for _, f := range e.Files {
			if err := insertFile(tx, id, f); err != nil {
				return err
			}
		}
	}
	for _, e := range entries {
		if e.mergeRevision == "" {
			continue
		}
		merge, ok := ids[e.mergeRevision]
		if !ok {
			return fmt.Errorf("merge %s of %s is not in the iteration", e.mergeRevision, e.Revision)
		}
		if _, err := tx.Exec("UPDATE commit_entry SET merge_id = ? WHERE id = ?", merge, e.ID); err != nil {
			return err
		}
	}
	return nil
}

func insertEntry(tx *sql.Tx, c *CommitEntry) (int64, error) {
	res, err := tx.Exec(`INSERT INTO commit_entry (iteration, position, revision, parent, subject, message, author, authored, change_id, topic, what, why, how, order_set)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Iteration, c.Position, c.Revision, c.Parent, c.Subject, c.Message, c.Author, c.Authored, c.ChangeID, c.Topic, c.What, c.Why, c.How, c.OrderSet)
	if err != nil {
		return 0, fmt.Errorf("store commit %s: %w", c.Revision, err)
	}
	return res.LastInsertId()
}

func insertFile(tx *sql.Tx, commitID int64, f *newFile) error {
	res, err := tx.Exec(`INSERT INTO file_change (commit_id, path, old_path, status, kind, size_unit, size_before, size_after, added, removed, changed, read_position, split_source, what, object_system, paradigms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		commitID, f.Path, f.OldPath, f.Status, f.Kind, f.SizeUnit, f.SizeBefore, f.SizeAfter, f.Added, f.Removed, f.Changed, f.ReadPosition, f.SplitSource, f.What, f.ObjectSystem, f.Paradigms)
	if err != nil {
		return fmt.Errorf("store file %s: %w", f.Path, err)
	}
	fileID, err := res.LastInsertId()
	if err != nil {
		return err
	}
	for i, s := range f.Sections {
		if _, err := tx.Exec(`INSERT INTO section (file_id, position, name, package, ranges, doc_ranges, old_ranges, deleted, added, removed, changed, what)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			fileID, i+1, s.Name, s.Package, rangesJSON(s.Ranges), rangesJSON(s.DocRanges), rangesJSON(s.OldRanges), s.Deleted, s.Added, s.Removed, s.Changed, s.What); err != nil {
			return fmt.Errorf("store section %q of %s: %w", s.Name, f.Path, err)
		}
	}
	return nil
}

// closedNote is appended to an orphaned split request's note.
const closedNote = "(closed: its commit was rewritten)"

// closeOrphanedRequests closes the open split requests whose commit the new
// iteration no longer has.
func closeOrphanedRequests(tx *sql.Tx, number int64) error {
	_, err := tx.Exec(`UPDATE request SET status = 'done', note = COALESCE(note || ' ', '') || ?
		 WHERE status = 'open' AND kind = 'split' AND target = 'file'
		   AND revision NOT IN (SELECT revision FROM commit_entry WHERE iteration = ?)`, closedNote, number)
	if err != nil {
		return fmt.Errorf("close orphaned requests: %w", err)
	}
	return nil
}

// defaultFile is <state dir>/<name>.sqlite, or <name>-N.sqlite for the first
// N from 2 not taken.
func (b *builder) defaultFile(rev, revision string) (string, error) {
	dir, err := b.repo.StateDir()
	if err != nil {
		return "", err
	}
	name, err := b.repo.RevisionName(rev)
	if err != nil {
		return "", err
	}
	base := reviewFileName(revision, name)
	file := filepath.Join(dir, base+".sqlite")
	for n := 2; ; n++ {
		if _, err := os.Stat(file); errors.Is(err, os.ErrNotExist) {
			return file, nil
		}
		file = filepath.Join(dir, fmt.Sprintf("%s-%d.sqlite", base, n))
	}
}

var refNameRE = regexp.MustCompile(`\A[\w.\-/]+\z`)

// reviewFileName is the head's branch or tag name, with / as -, when it has
// a plain one, else its short id.
func reviewFileName(revision, name string) string {
	short := revision
	if len(short) > 12 {
		short = short[:12]
	}
	if !refNameRE.MatchString(name) || strings.HasPrefix(revision, name) {
		return short
	}
	return strings.ReplaceAll(name, "/", "-")
}
