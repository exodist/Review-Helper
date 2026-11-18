package review

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"

	"github.com/exodist/Review-Helper/go/internal/git"
)

// The fill sheet (standards/FORMAT.md, "The fill sheet"): `sheet` lists the
// items of the latest iteration that need the agent; `fill` (fill.go)
// validates a filled sheet and writes it.

const (
	SheetFormat        = "review-sheet"
	SheetFormatVersion = 1
)

// Sheet is the fill sheet document.
type Sheet struct {
	Format        string       `json:"format"`
	FormatVersion int          `json:"format_version"`
	Review        string       `json:"review"`
	Iteration     int64        `json:"iteration"`
	Items         []*SheetItem `json:"items"`
}

// SheetItem is one item as the sheet gives it. About and Fields hold the
// item type's own structs (the *About and *Fields types below).
type SheetItem struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Rev      string `json:"rev"`
	About    any    `json:"about"`
	Fields   any    `json:"fields"`
	Optional bool   `json:"optional,omitempty"`
}

// The about and fields of each item type. Nullable values are pointers; a
// nil slice is null.
type (
	SummaryAbout struct {
		Title    *string  `json:"title"`
		Base     string   `json:"base"`
		Head     string   `json:"head"`
		Squashed bool     `json:"squashed"`
		Paths    []string `json:"paths"`
	}
	TextFields struct {
		What *string `json:"what"`
		Why  *string `json:"why"`
		How  *string `json:"how"`
	}
	// SummaryNoteFields are a summary's fields from iteration 2.
	SummaryNoteFields struct {
		TextFields
		Note *string `json:"note"`
	}

	TopicsAbout struct {
		Paths      []string `json:"paths"`
		Unassigned []string `json:"unassigned"`
	}
	TopicsFields struct {
		Topics []Topic `json:"topics"`
	}
	Topic struct {
		Name  string   `json:"name"`
		Paths []string `json:"paths"`
	}

	CommitAbout struct {
		Revision string  `json:"revision"`
		Subject  string  `json:"subject"`
		Position int64   `json:"position"`
		Merge    *string `json:"merge"`
		Topic    *string `json:"topic"`
	}

	OrderAbout struct {
		Commit string   `json:"commit"`
		Paths  []string `json:"paths"`
	}
	OrderFields struct {
		Paths []string `json:"paths"`
	}

	FileAbout struct {
		Commit      string  `json:"commit"`
		Path        string  `json:"path"`
		OldPath     *string `json:"old_path"`
		Status      string  `json:"status"`
		Kind        string  `json:"kind"`
		SizeUnit    string  `json:"size_unit"`
		SizeBefore  *int64  `json:"size_before"`
		SizeAfter   *int64  `json:"size_after"`
		Added       int64   `json:"added"`
		Removed     int64   `json:"removed"`
		Changed     int64   `json:"changed"`
		SplitSource string  `json:"split_source"`
	}
	FileFields struct {
		What         *string `json:"what"`
		ObjectSystem *string `json:"object_system"`
		Paradigms    *string `json:"paradigms"`
	}

	SectionAbout struct {
		Commit    string  `json:"commit"`
		Path      string  `json:"path"`
		Name      string  `json:"name"`
		Package   *string `json:"package"`
		Ranges    Ranges  `json:"ranges"`
		DocRanges Ranges  `json:"doc_ranges"`
		OldRanges Ranges  `json:"old_ranges"`
		Deleted   int     `json:"deleted"` // 0 or 1, as stored
		Added     int64   `json:"added"`
		Removed   int64   `json:"removed"`
		Changed   int64   `json:"changed"`
	}
	SectionFields struct {
		What *string `json:"what"`
	}

	SplitAbout struct {
		Commit     string         `json:"commit"`
		Path       string         `json:"path"`
		OldPath    *string        `json:"old_path"`
		Status     string         `json:"status"`
		Kind       string         `json:"kind"`
		SizeBefore *int64         `json:"size_before"`
		SizeAfter  *int64         `json:"size_after"`
		Request    *SplitRequest  `json:"request"`
		Sections   []SplitUnit    `json:"sections"`
		Previous   *PreviousSplit `json:"previous"`
	}
	SplitRequest struct {
		ID   int64   `json:"id"`
		Note *string `json:"note"`
	}
	// SplitUnit is a stored section in the split field's shape: a deleted
	// one has no lines or doc_lines, and old_lines only when it has some.
	SplitUnit struct {
		Name     string  `json:"name"`
		Package  *string `json:"package"`
		Lines    *Ranges `json:"lines,omitempty"`
		DocLines *Ranges `json:"doc_lines,omitempty"`
		OldLines Ranges  `json:"old_lines,omitempty"`
	}
	PreviousSplit struct {
		Commit    string         `json:"commit"`
		Iteration int64          `json:"iteration"`
		Path      string         `json:"path"`
		Sections  []PreviousUnit `json:"sections"`
	}
	PreviousUnit struct {
		Name     string  `json:"name"`
		Package  *string `json:"package"`
		Lines    Ranges  `json:"lines"`
		DocLines Ranges  `json:"doc_lines"`
	}
	SplitFields struct {
		Sections []SplitUnit `json:"sections"` // always null in a sheet
	}

	NoteAbout struct {
		Flag     Text `json:"flag"`
		Kind     Text `json:"kind"`
		Path     Text `json:"path"`
		Unit     Text `json:"unit"`
		Snippet  Text `json:"snippet"`
		Sentence Text `json:"sentence"`
		Detail   Text `json:"detail"`
	}
	NoteFields struct {
		Commit *string `json:"commit"`
		Topic  *string `json:"topic"`
		Path   *string `json:"path"`
		Lines  *Range  `json:"lines"`
	}

	FlagAbout struct {
		Note        string     `json:"note"`
		Commit      string     `json:"commit"`
		Topic       *string    `json:"topic"`
		Path        *string    `json:"path"`
		Lines       *Range     `json:"lines"`
		Color       string     `json:"color"`
		ColorReason *string    `json:"color_reason"`
		Kind        string     `json:"kind"`
		Sentence    string     `json:"sentence"`
		Detail      *string    `json:"detail"`
		Pending     bool       `json:"pending"`
		LeftChange  bool       `json:"left_change"`
		Rewritten   bool       `json:"rewritten"`
		Previous    *FlagPlace `json:"previous"`
	}
	FlagPlace struct {
		Commit string  `json:"commit"`
		Path   *string `json:"path"`
		Lines  *Range  `json:"lines"`
	}
	FlagFields struct {
		Decision *string `json:"decision"`
		Color    *string `json:"color"`
		Reason   *string `json:"reason"`
	}

	// Anchor is where feedback or a request was given.
	Anchor struct {
		Target  string       `json:"target"`
		Commit  *string      `json:"commit"`
		Topic   *string      `json:"topic"`
		Path    *string      `json:"path"`
		Section *SectionName `json:"section"`
	}
	SectionName struct {
		Name    string  `json:"name"`
		Package *string `json:"package"`
	}
	FeedbackAbout struct {
		Anchor
		Flag      *FeedbackFlag `json:"flag"`
		Iteration int64         `json:"iteration"`
		Line      *int64        `json:"line"`
		Side      *string       `json:"side"`
		Body      string        `json:"body"`
	}
	FeedbackFlag struct {
		Note     string  `json:"note"`
		Sentence *string `json:"sentence"`
		Path     *string `json:"path"`
		Lines    *Range  `json:"lines"`
	}
	FeedbackFields struct {
		Status   *string `json:"status"` // always null in a sheet
		Response *string `json:"response"`
	}

	DebateAbout struct {
		Anchor
		Iteration int64   `json:"iteration"`
		Note      *string `json:"note"`
	}
	DebateFields struct {
		CaseFor     *string `json:"case_for"`
		CaseAgainst *string `json:"case_against"`
	}

	GuideAbout struct {
		Commits []string `json:"commits"`
	}
	GuideFields struct {
		Steps []GuideStepJSON `json:"steps"`
	}
	GuideStepJSON struct {
		Target     json.RawMessage `json:"target"`
		Highlight  json.RawMessage `json:"highlight"`
		Commentary string          `json:"commentary"`
	}
)

// sheetItem is an item with what fill needs to check and write it.
type sheetItem struct {
	SheetItem
	done   bool
	fields []string // the names in Fields

	commit   *sheetCommit
	file     *sheetFile
	section  *Section
	flag     *Flag
	note     *Note
	feedback *Feedback
	request  *Request // a debate's request, or a split's open request
}

// sheetContext is the latest iteration as the sheet and fill read it.
type sheetContext struct {
	db        *DB
	repo      *git.Repo
	journal   *Journal
	review    *Review
	iteration *Iteration
	commits   []*sheetCommit
	feedback  []*Feedback // open, from any iteration
	requests  []*Request
	steps     []*GuideStep
	notes     []*Note          // the journal's notes with a flag
	flags     []*Flag          // the iteration's, by id
	flagEntry map[string]int64 // note id to the entry holding its flag
	texts     map[int64]*fileTexts
}

type sheetCommit struct {
	*CommitEntry
	files  []*sheetFile // by path
	byPath map[string]*sheetFile
}

type sheetFile struct {
	*FileChange
	sections []*Section
}

func loadSheetContext(d *DB, repo *git.Repo, j *Journal) (*sheetContext, error) {
	it, err := d.LatestIteration()
	if err != nil {
		return nil, err
	}
	if it == nil {
		return nil, errors.New("the review has no iteration")
	}
	c := &sheetContext{db: d, repo: repo, journal: j, iteration: it, flagEntry: map[string]int64{}, texts: map[int64]*fileTexts{}}
	if c.review, err = d.Review(); err != nil {
		return nil, err
	}
	entries, err := d.Commits(it.Number)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		sc := &sheetCommit{CommitEntry: e, byPath: map[string]*sheetFile{}}
		files, err := d.Files(e.ID)
		if err != nil {
			return nil, err
		}
		sort.Slice(files, func(a, b int) bool { return files[a].Path < files[b].Path })
		for _, f := range files {
			sf := &sheetFile{FileChange: f}
			if sf.sections, err = d.Sections(f.ID); err != nil {
				return nil, err
			}
			sc.files = append(sc.files, sf)
			sc.byPath[f.Path] = sf
		}
		c.commits = append(c.commits, sc)
	}
	if c.feedback, err = queryAll(d, scanFeedback, "SELECT "+feedbackCols+" FROM feedback WHERE status = 'open' ORDER BY id"); err != nil {
		return nil, err
	}
	if c.requests, err = d.AllRequests(); err != nil {
		return nil, err
	}
	if c.steps, err = d.GuideSteps(it.Number); err != nil {
		return nil, err
	}
	if c.flags, err = queryAll(d, scanFlag, "SELECT "+flagCols+", commit_entry.revision, commit_entry.topic"+flagJoin+
		" WHERE commit_entry.iteration = ? ORDER BY flag.id", it.Number); err != nil {
		return nil, err
	}
	for _, f := range c.flags {
		c.flagEntry[f.NoteID] = f.CommitID
	}
	notes, err := j.Notes()
	if err != nil {
		return nil, err
	}
	for _, n := range notes {
		// Only a note the journal could have written can become a flag.
		if n.Flag.Set && n.Problem() == "" {
			c.notes = append(c.notes, n)
		}
	}
	return c, nil
}

// entry is the iteration's entry with id, or nil.
func (c *sheetContext) entry(id int64) *sheetCommit {
	for _, e := range c.commits {
		if e.ID == id {
			return e
		}
	}
	return nil
}

// items builds every item of the iteration, done or not, in sheet order.
func (c *sheetContext) items() ([]*sheetItem, error) {
	squashed := c.iteration.Squashed
	items := []*sheetItem{c.summaryItem()}
	if squashed {
		items = append(items, c.topicsItem())
	}
	for _, e := range c.commits {
		items = append(items, c.commitItem(e))
		if len(e.files) > 0 {
			items = append(items, orderItem(e, squashed))
		}
		for _, f := range e.files {
			items = append(items, fileItem(e, f))
			split, err := c.splitItem(e, f)
			if err != nil {
				return nil, err
			}
			if split != nil {
				items = append(items, split)
			}
			for _, s := range f.sections {
				if s.Touched() {
					items = append(items, sectionItem(e, f, s, squashed))
				}
			}
		}
	}
	for _, n := range c.notes {
		if _, placed := c.flagEntry[n.ID.Value]; !placed {
			items = append(items, noteItem(n))
		}
	}
	for _, f := range c.flags {
		item, err := c.flagItem(f)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	lines, err := c.flagLines()
	if err != nil {
		return nil, err
	}
	for _, fb := range c.feedback {
		items = append(items, feedbackItem(fb, lines))
	}
	for _, r := range c.requests {
		if r.Kind == "debate" {
			items = append(items, debateItem(r))
		}
	}
	items = append(items, c.guideItem())
	for _, it := range items {
		if err := it.seal(); err != nil {
			return nil, err
		}
	}
	return items, nil
}

// seal computes the item's rev and records its field names.
func (it *sheetItem) seal() error {
	var err error
	if it.Rev, err = rev(it.About, it.Fields); err != nil {
		return err
	}
	it.fields, err = jsonKeys(it.Fields)
	return err
}

// rev is the SHA-256 of the canonical JSON (keys sorted, compact) of
// [about, fields], as the Perl backend computes it, so a sheet one backend
// gives another can fill.
func rev(about, fields any) (string, error) {
	b, err := canonicalJSON([]any{about, fields})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func (c *sheetContext) summaryItem() *sheetItem {
	it := c.iteration
	about := SummaryAbout{Title: c.review.Title, Base: it.Base, Head: it.Head, Squashed: it.Squashed, Paths: it.Paths}
	text := TextFields{What: it.What, Why: it.Why, How: it.How}
	var fields any = text
	if it.Number > 1 {
		fields = SummaryNoteFields{TextFields: text, Note: it.Note}
	}
	return &sheetItem{
		SheetItem: SheetItem{ID: fmt.Sprintf("summary:%d", it.Number), Type: "summary", About: about, Fields: fields},
		done:      text.complete(),
	}
}

// complete reports whether every text field is set.
func (t TextFields) complete() bool { return t.What != nil && t.Why != nil && t.How != nil }

func entryPaths(e *sheetCommit) []string {
	paths := make([]string, 0, len(e.files))
	for _, f := range e.files {
		paths = append(paths, f.Path)
	}
	return paths // files are sorted by path
}

// topicsItem is done once every file is in a topic; until then the files in
// none share the one entry with no topic.
func (c *sheetContext) topicsItem() *sheetItem {
	about := TopicsAbout{Paths: []string{}, Unassigned: []string{}}
	var fields TopicsFields
	var unassigned *sheetCommit
	for _, e := range c.commits {
		about.Paths = append(about.Paths, entryPaths(e)...)
		if e.Topic == nil {
			if unassigned == nil {
				unassigned = e
			}
			continue
		}
		fields.Topics = append(fields.Topics, Topic{Name: *e.Topic, Paths: entryPaths(e)})
	}
	sort.Strings(about.Paths)
	if unassigned != nil {
		about.Unassigned = entryPaths(unassigned)
	}
	return &sheetItem{
		SheetItem: SheetItem{ID: fmt.Sprintf("topics:%d", c.iteration.Number), Type: "topics", About: about, Fields: fields},
		done:      unassigned == nil,
	}
}

func (c *sheetContext) commitItem(e *sheetCommit) *sheetItem {
	about := CommitAbout{Revision: e.Revision, Subject: e.Subject, Position: e.Position, Topic: e.Topic}
	if e.MergeID != nil {
		if m := c.entry(*e.MergeID); m != nil {
			about.Merge = &m.Revision
		}
	}
	fields := TextFields{What: e.What, Why: e.Why, How: e.How}
	return &sheetItem{
		SheetItem: SheetItem{ID: fmt.Sprintf("commit:%d", e.ID), Type: "commit", About: about, Fields: fields},
		done:      fields.complete(),
		commit:    e,
	}
}

func orderItem(e *sheetCommit, squashed bool) *sheetItem {
	var fields OrderFields
	if e.OrderSet {
		files := slices.Clone(e.files)
		sort.SliceStable(files, func(a, b int) bool { return orZero(files[a].ReadPosition) < orZero(files[b].ReadPosition) })
		for _, f := range files {
			fields.Paths = append(fields.Paths, f.Path)
		}
	}
	return &sheetItem{
		SheetItem: SheetItem{ID: fmt.Sprintf("order:%d", e.ID), Type: "order", About: OrderAbout{Commit: e.Revision, Paths: entryPaths(e)}, Fields: fields, Optional: squashed},
		done:      e.OrderSet,
		commit:    e,
	}
}

// orZero is the number, or 0 for none.
func orZero(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func fileItem(e *sheetCommit, f *sheetFile) *sheetItem {
	about := FileAbout{
		Commit: e.Revision, Path: f.Path, OldPath: f.OldPath, Status: f.Status, Kind: f.Kind, SizeUnit: f.SizeUnit,
		SizeBefore: f.SizeBefore, SizeAfter: f.SizeAfter, Added: f.Added, Removed: f.Removed, Changed: f.Changed, SplitSource: f.SplitSource,
	}
	return &sheetItem{
		SheetItem: SheetItem{ID: fmt.Sprintf("file:%d", f.ID), Type: "file", About: about, Fields: FileFields{What: f.What, ObjectSystem: f.ObjectSystem, Paradigms: f.Paradigms}},
		done:      f.What != nil,
		commit:    e,
		file:      f,
	}
}

func sectionItem(e *sheetCommit, f *sheetFile, s *Section, squashed bool) *sheetItem {
	about := SectionAbout{
		Commit: e.Revision, Path: f.Path, Name: s.Name, Package: s.Package,
		Ranges: orEmpty(s.Ranges), DocRanges: orEmpty(s.DocRanges), OldRanges: orEmpty(s.OldRanges),
		Added: s.Added, Removed: s.Removed, Changed: s.Changed,
	}
	if s.Deleted {
		about.Deleted = 1
	}
	return &sheetItem{
		SheetItem: SheetItem{ID: fmt.Sprintf("section:%d", s.ID), Type: "section", About: about, Fields: SectionFields{What: s.What}, Optional: squashed},
		done:      s.What != nil,
		commit:    e,
		file:      f,
		section:   s,
	}
}

func orEmpty(r Ranges) Ranges {
	if r == nil {
		return Ranges{}
	}
	return r
}

// splitItem is nil for a file with nothing to split: deleted, image or
// binary, or a rename or mode change alone. It is required while the file
// has no split (optional in a squashed iteration) and while a split
// request on it is open.
func (c *sheetContext) splitItem(e *sheetCommit, f *sheetFile) (*sheetItem, error) {
	if f.SizeUnit != "lines" || f.Status == "D" {
		return nil, nil
	}
	if f.Status != "A" && f.Added == 0 && f.Removed == 0 && f.Changed == 0 {
		return nil, nil
	}
	request := c.splitRequest(e, f)
	unsplit := f.SplitSource == "none"
	about := SplitAbout{
		Commit: e.Revision, Path: f.Path, OldPath: f.OldPath, Status: f.Status, Kind: f.Kind,
		SizeBefore: f.SizeBefore, SizeAfter: f.SizeAfter, Sections: []SplitUnit{},
	}
	if request != nil {
		about.Request = &SplitRequest{ID: request.ID, Note: request.Note}
	}
	for _, s := range f.sections {
		about.Sections = append(about.Sections, splitUnit(s))
	}
	if unsplit {
		var err error
		if about.Previous, err = c.previousSplit(e, f); err != nil {
			return nil, err
		}
	}
	return &sheetItem{
		SheetItem: SheetItem{ID: fmt.Sprintf("split:%d", f.ID), Type: "split", About: about, Fields: SplitFields{}, Optional: request == nil && c.iteration.Squashed},
		done:      request == nil && !unsplit,
		commit:    e,
		file:      f,
		request:   request,
	}, nil
}

// splitRequest is the first open split request on the file, or nil.
func (c *sheetContext) splitRequest(e *sheetCommit, f *sheetFile) *Request {
	for _, r := range c.requests {
		if r.isSplitOf(e, f) {
			return r
		}
	}
	return nil
}

func (r *Request) isSplitOf(e *sheetCommit, f *sheetFile) bool {
	return r.Status == "open" && r.Kind == "split" && r.Target == "file" &&
		r.Revision != nil && *r.Revision == e.Revision && r.Path != nil && *r.Path == f.Path
}

// splitUnit is a stored section as the split field gives it.
func splitUnit(s *Section) SplitUnit {
	u := SplitUnit{Name: s.Name, Package: s.Package}
	if !s.Deleted {
		lines, docs := orEmpty(s.Ranges), orEmpty(s.DocRanges)
		u.Lines, u.DocLines = &lines, &docs
	}
	if len(s.OldRanges) > 0 {
		u.OldLines = s.OldRanges
	}
	return u
}

// previousSplit is the newest split an earlier iteration stored for the
// file's path (or its old path), preferring the commit with the same change
// id, else the last; its lines are as stored, at that commit.
func (c *sheetContext) previousSplit(e *sheetCommit, f *sheetFile) (*PreviousSplit, error) {
	oldPath := f.Path
	if f.OldPath != nil {
		oldPath = *f.OldPath
	}
	var fileID int64
	var p PreviousSplit
	err := c.db.QueryRow(`SELECT f.id, f.path, c.revision, c.iteration
	  FROM file_change f JOIN commit_entry c ON c.id = f.commit_id
	 WHERE f.path IN (?, ?) AND c.iteration < ? AND f.split_source != 'none'
	 ORDER BY c.iteration DESC, c.change_id IS ? DESC, c.position DESC
	 LIMIT 1`, f.Path, oldPath, c.iteration.Number, e.ChangeID).Scan(&fileID, &p.Path, &p.Commit, &p.Iteration)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sections, err := c.db.Sections(fileID)
	if err != nil {
		return nil, err
	}
	p.Sections = []PreviousUnit{}
	for _, s := range sections {
		if !s.Deleted {
			p.Sections = append(p.Sections, PreviousUnit{Name: s.Name, Package: s.Package, Lines: orEmpty(s.Ranges), DocLines: orEmpty(s.DocRanges)})
		}
	}
	return &p, nil
}

// noteItem places a journal note as a flag; always optional, since the
// journal may hold notes of unrelated work.
func noteItem(n *Note) *sheetItem {
	about := NoteAbout{Flag: n.Flag, Kind: n.Kind, Path: n.Path, Unit: n.Unit, Snippet: n.Snippet, Sentence: n.Sentence, Detail: n.Detail}
	return &sheetItem{
		SheetItem: SheetItem{ID: "note:" + n.ID.Value, Type: "note", About: about, Fields: NoteFields{}, Optional: true},
		note:      n,
	}
}

func (f *Flag) lines() *Range {
	if f.LineStart == nil || f.LineEnd == nil {
		return nil
	}
	return &Range{int(*f.LineStart), int(*f.LineEnd)}
}

// flagItem needs a decision only while the flag is pending; its fields are
// actions, so they always start null.
func (c *sheetContext) flagItem(f *Flag) (*sheetItem, error) {
	about := FlagAbout{
		Note: f.NoteID, Commit: f.Revision, Topic: f.Topic, Path: f.Path, Lines: f.lines(), Color: f.Color, ColorReason: f.ColorReason,
		Kind: f.Kind, Sentence: f.Sentence, Detail: f.Detail, Pending: f.Pending,
	}
	if f.Pending {
		old, err := c.db.FlagByNote(c.iteration.Number-1, f.NoteID)
		if err != nil {
			return nil, err
		}
		if old != nil {
			about.Previous = &FlagPlace{Commit: old.Revision, Path: old.Path, Lines: old.lines()}
			if !c.iteration.Squashed {
				if about.Rewritten, err = c.rewritten(old, f); err != nil {
					return nil, err
				}
			}
		}
	}
	if f.Path != nil {
		about.LeftChange = true
		for _, e := range c.commits {
			if e.Revision == f.Revision && e.byPath[*f.Path] != nil {
				about.LeftChange = false
				break
			}
		}
	}
	return &sheetItem{
		SheetItem: SheetItem{ID: fmt.Sprintf("flag:%d", f.ID), Type: "flag", About: about, Fields: FlagFields{}},
		done:      !f.Pending,
		flag:      f,
	}, nil
}

// rewritten reports whether the flag's commit does not pair with the one it
// was on: their change ids differ, or, the old one having none, their
// revisions do.
func (c *sheetContext) rewritten(old, f *Flag) (bool, error) {
	was, err := c.db.Commit(old.CommitID)
	if err != nil || was == nil {
		return false, err
	}
	is, err := c.db.Commit(f.CommitID)
	if err != nil || is == nil {
		return false, err
	}
	if was.ChangeID != nil {
		return is.ChangeID == nil || *is.ChangeID != *was.ChangeID, nil
	}
	return is.ChangeID != nil || old.Revision != f.Revision, nil
}

// flagLines gives the lines of every flag with lines, by iteration and note
// id, for the flags feedback names.
func (c *sheetContext) flagLines() (map[flagKey]Range, error) {
	rows, err := c.db.Query(`SELECT c.iteration, f.note_id, f.line_start, f.line_end
	  FROM flag f JOIN commit_entry c ON c.id = f.commit_id
	 WHERE f.line_start IS NOT NULL AND f.line_end IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lines := map[flagKey]Range{}
	for rows.Next() {
		var k flagKey
		var r Range
		if err := rows.Scan(&k.iteration, &k.note, &r[0], &r[1]); err != nil {
			return nil, err
		}
		lines[k] = r
	}
	return lines, rows.Err()
}

type flagKey struct {
	iteration int64
	note      string
}

func anchorOf(target string, revision, topic, path, sectionName, sectionPackage *string) Anchor {
	a := Anchor{Target: target, Commit: revision, Topic: topic, Path: path}
	if sectionName != nil {
		a.Section = &SectionName{Name: *sectionName, Package: sectionPackage}
	}
	return a
}

func feedbackItem(fb *Feedback, lines map[flagKey]Range) *sheetItem {
	about := FeedbackAbout{
		Anchor:    anchorOf(fb.Target, fb.Revision, fb.Topic, fb.Path, fb.SectionName, fb.SectionPackage),
		Iteration: fb.Iteration, Line: fb.Line, Side: fb.Side, Body: fb.Body,
	}
	if fb.Flag != nil {
		about.Flag = &FeedbackFlag{Note: *fb.Flag, Sentence: fb.FlagSentence, Path: fb.Path}
		if r, ok := lines[flagKey{fb.Iteration, *fb.Flag}]; ok {
			about.Flag.Lines = &r
		}
	}
	return &sheetItem{
		SheetItem: SheetItem{ID: fmt.Sprintf("feedback:%d", fb.ID), Type: "feedback", About: about, Fields: FeedbackFields{Response: fb.Response}},
		feedback:  fb,
	}
}

func debateItem(r *Request) *sheetItem {
	about := DebateAbout{Anchor: anchorOf(r.Target, r.Revision, r.Topic, r.Path, r.SectionName, r.SectionPackage), Iteration: r.Iteration, Note: r.Note}
	return &sheetItem{
		SheetItem: SheetItem{ID: fmt.Sprintf("request:%d", r.ID), Type: "debate", About: about, Fields: DebateFields{CaseFor: r.CaseFor, CaseAgainst: r.CaseAgainst}},
		done:      r.Status == "done",
		request:   r,
	}
}

// guideItem is asked for again when a stored step names something the
// iteration no longer shows (see staleStep).
func (c *sheetContext) guideItem() *sheetItem {
	about := GuideAbout{Commits: []string{}}
	for _, e := range c.commits {
		if !slices.Contains(about.Commits, e.Revision) {
			about.Commits = append(about.Commits, e.Revision)
		}
	}
	var fields GuideFields
	stale := false
	for _, s := range c.steps {
		fields.Steps = append(fields.Steps, GuideStepJSON{Target: s.Target, Highlight: s.Highlight, Commentary: s.Commentary})
		stale = stale || c.staleStep(s.Target)
	}
	return &sheetItem{
		SheetItem: SheetItem{ID: fmt.Sprintf("guide:%d", c.iteration.Number), Type: "guide", About: about, Fields: fields},
		done:      len(c.steps) > 0 && !stale,
	}
}

// BuildSheet is the latest iteration's sheet: the items still to do, or,
// with all, every item.
func BuildSheet(d *DB, repo *git.Repo, j *Journal, all bool) (*Sheet, error) {
	c, err := loadSheetContext(d, repo, j)
	if err != nil {
		return nil, err
	}
	items, err := c.items()
	if err != nil {
		return nil, err
	}
	s := &Sheet{Format: SheetFormat, FormatVersion: SheetFormatVersion, Review: c.review.ID, Iteration: c.iteration.Number, Items: []*SheetItem{}}
	for _, it := range items {
		if all || !it.done {
			s.Items = append(s.Items, &it.SheetItem)
		}
	}
	return s, nil
}

// openReview opens a review file with its repository and journal.
func openReview(file string) (*DB, *git.Repo, *Journal, error) {
	repo, err := git.Find(file)
	if err != nil {
		return nil, nil, nil, err
	}
	j, err := journalOf(repo)
	if err != nil {
		return nil, nil, nil, err
	}
	d, err := OpenDB(file)
	if err != nil {
		return nil, nil, nil, err
	}
	return d, repo, j, nil
}

func runSheet(args []string, out io.Writer) error {
	var all bool
	o := newOptions()
	o.flag("all", &all)
	rest, err := o.parse(args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errors.New("usage: review-helper sheet [--all] FILE")
	}
	d, repo, j, err := openReview(rest[0])
	if err != nil {
		return err
	}
	defer d.Close()
	s, err := BuildSheet(d, repo, j, all)
	if err != nil {
		return err
	}
	b, err := MarshalJSONIndent(s)
	if err != nil {
		return err
	}
	_, err = out.Write(b)
	return err
}
