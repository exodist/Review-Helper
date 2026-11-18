package review

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// The rows of a review file (standards/review.sql). A nullable column is a
// pointer; a JSON column is decoded. Output layers (the API, the fill sheet,
// exports) shape their own JSON from these.

// Range is an inclusive [start, end] line range; Ranges is a sorted,
// non-overlapping list of them.
type (
	Range  = [2]int
	Ranges = []Range
)

// Review is the review row.
type Review struct {
	ID      string
	Title   *string
	Source  *string
	Created int64
	State   string
}

// Iteration is one iteration.
type Iteration struct {
	Number   int64
	Base     string
	Head     string
	Squashed bool
	Paths    []string // nil: all paths
	Pin      string
	Created  int64
	Note     *string
	What     *string
	Why      *string
	How      *string
}

// CommitEntry is an iteration's commit, or a topic of a squashed one.
type CommitEntry struct {
	ID        int64
	Iteration int64
	Position  int64
	MergeID   *int64
	Revision  string
	Parent    *string
	Subject   string
	Message   string
	Author    *string
	Authored  *int64
	ChangeID  *string
	Topic     *string
	What      *string
	Why       *string
	How       *string
	OrderSet  bool
}

// FileChange is a file an entry changes.
type FileChange struct {
	ID           int64
	CommitID     int64
	Path         string
	OldPath      *string
	Status       string
	Kind         string
	SizeUnit     string
	SizeBefore   *int64
	SizeAfter    *int64
	Added        int64
	Removed      int64
	Changed      int64
	ReadPosition *int64
	SplitSource  string
	What         *string
	ObjectSystem *string
	Paradigms    *string
}

// Section is one of a file change's sections.
type Section struct {
	ID        int64
	FileID    int64
	Position  int64
	Name      string
	Package   *string
	Ranges    Ranges
	DocRanges Ranges
	OldRanges Ranges
	Deleted   bool
	Added     int64
	Removed   int64
	Changed   int64
	What      *string
}

// Touched reports whether the commit changed a line of the section: only
// those are explained and shown.
func (s *Section) Touched() bool {
	return s.Deleted || s.Added != 0 || s.Removed != 0 || s.Changed != 0
}

// Span is the section as attribution sees it.
func (s *Section) Span() *Span {
	return &Span{Ranges: s.Ranges, DocRanges: s.DocRanges, OldRanges: s.OldRanges, Deleted: s.Deleted}
}

// Flag is a flag on an entry. Revision and Topic are its entry's, when the
// query joins them.
type Flag struct {
	ID          int64
	NoteID      string
	CommitID    int64
	Path        *string
	LineStart   *int64
	LineEnd     *int64
	Color       string
	ColorReason *string
	Kind        string
	Sentence    string
	Detail      *string
	Created     int64
	Pending     bool

	Revision string
	Topic    *string
}

// JournalEntry is a journal note an iteration took as an entry.
type JournalEntry struct {
	ID        string
	Iteration int64
	Kind      string
	Path      string
	Unit      *string
	Sentence  string
	Detail    *string
	Created   int64
}

// GuideStep is one step of an iteration's guide.
type GuideStep struct {
	Iteration  int64
	Position   int64
	Target     json.RawMessage
	Highlight  json.RawMessage
	Commentary string
}

// Feedback is a reviewer's comment.
type Feedback struct {
	ID             int64
	Iteration      int64
	Target         string
	Revision       *string
	Path           *string
	SectionName    *string
	SectionPackage *string
	Topic          *string
	Flag           *string
	FlagSentence   *string
	Line           *int64
	Side           *string
	Body           string
	Created        int64
	Updated        int64
	Status         string
	Response       *string
	ResolvedIn     *int64
	Published      *int64
	Reviewer       *string
}

// Request is a reviewer's request for a debate or a split.
type Request struct {
	ID             int64
	Iteration      int64
	Kind           string
	Target         string
	Revision       *string
	Path           *string
	SectionName    *string
	SectionPackage *string
	Topic          *string
	Note           *string
	Status         string
	Created        int64
	CaseFor        *string
	CaseAgainst    *string
	Reviewer       *string
}

// FlagSeen is a reviewer's seen mark on a flag.
type FlagSeen struct {
	Flag     int64
	Reviewer string
	At       int64
}

// The column lists the scanners read, in table order.
const (
	iterationCols = "number, base, head, squashed, paths, pin, created, note, what, why, how"
	commitCols    = "id, iteration, position, merge_id, revision, parent, subject, message, author, authored, change_id, topic, what, why, how, order_set"
	fileCols      = "id, commit_id, path, old_path, status, kind, size_unit, size_before, size_after, added, removed, changed, read_position, split_source, what, object_system, paradigms"
	sectionCols   = "id, file_id, position, name, package, ranges, doc_ranges, old_ranges, deleted, added, removed, changed, what"
	flagCols      = "flag.id, flag.note_id, flag.commit_id, flag.path, flag.line_start, flag.line_end, flag.color, flag.color_reason, flag.kind, flag.sentence, flag.detail, flag.created, flag.pending"
	journalCols   = "id, iteration, kind, path, unit, sentence, detail, created"
	guideCols     = "iteration, position, target, highlight, commentary"
	feedbackCols  = "id, iteration, target, revision, path, section_name, section_package, topic, flag, flag_sentence, line, side, body, created, updated, status, response, resolved_in, published, reviewer"
	requestCols   = "id, iteration, kind, target, revision, path, section_name, section_package, topic, note, status, created, case_for, case_against, reviewer"
)

// scanner is a *sql.Row or *sql.Rows.
type scanner interface{ Scan(dest ...any) error }

// jsonCol decodes a JSON text column on scan.
type jsonCol struct{ into any }

func (j jsonCol) Scan(src any) error {
	var b []byte
	switch v := src.(type) {
	case nil:
		return nil
	case string:
		b = []byte(v)
	case []byte:
		b = v
	default:
		return fmt.Errorf("JSON column holds %T", src)
	}
	return json.Unmarshal(b, j.into)
}

// rawCol keeps a JSON text column as it is.
type rawCol struct{ into *json.RawMessage }

func (r rawCol) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*r.into = nil
	case string:
		*r.into = json.RawMessage(v)
	case []byte:
		*r.into = append(json.RawMessage{}, v...)
	default:
		return fmt.Errorf("JSON column holds %T", src)
	}
	return nil
}

func scanIteration(s scanner) (*Iteration, error) {
	var it Iteration
	err := s.Scan(&it.Number, &it.Base, &it.Head, &it.Squashed, jsonCol{&it.Paths}, &it.Pin, &it.Created, &it.Note, &it.What, &it.Why, &it.How)
	return &it, err
}

func scanCommit(s scanner) (*CommitEntry, error) {
	var c CommitEntry
	err := s.Scan(&c.ID, &c.Iteration, &c.Position, &c.MergeID, &c.Revision, &c.Parent, &c.Subject, &c.Message, &c.Author, &c.Authored, &c.ChangeID, &c.Topic, &c.What, &c.Why, &c.How, &c.OrderSet)
	return &c, err
}

func scanFile(s scanner) (*FileChange, error) {
	var f FileChange
	err := s.Scan(&f.ID, &f.CommitID, &f.Path, &f.OldPath, &f.Status, &f.Kind, &f.SizeUnit, &f.SizeBefore, &f.SizeAfter, &f.Added, &f.Removed, &f.Changed, &f.ReadPosition, &f.SplitSource, &f.What, &f.ObjectSystem, &f.Paradigms)
	return &f, err
}

func scanSection(s scanner) (*Section, error) {
	var x Section
	err := s.Scan(&x.ID, &x.FileID, &x.Position, &x.Name, &x.Package, jsonCol{&x.Ranges}, jsonCol{&x.DocRanges}, jsonCol{&x.OldRanges}, &x.Deleted, &x.Added, &x.Removed, &x.Changed, &x.What)
	return &x, err
}

// scanFlag reads flagCols, then the entry's revision and topic.
func scanFlag(s scanner) (*Flag, error) {
	var f Flag
	err := s.Scan(&f.ID, &f.NoteID, &f.CommitID, &f.Path, &f.LineStart, &f.LineEnd, &f.Color, &f.ColorReason, &f.Kind, &f.Sentence, &f.Detail, &f.Created, &f.Pending, &f.Revision, &f.Topic)
	return &f, err
}

func scanJournal(s scanner) (*JournalEntry, error) {
	var j JournalEntry
	err := s.Scan(&j.ID, &j.Iteration, &j.Kind, &j.Path, &j.Unit, &j.Sentence, &j.Detail, &j.Created)
	return &j, err
}

func scanGuide(s scanner) (*GuideStep, error) {
	var g GuideStep
	err := s.Scan(&g.Iteration, &g.Position, rawCol{&g.Target}, rawCol{&g.Highlight}, &g.Commentary)
	return &g, err
}

func scanFeedback(s scanner) (*Feedback, error) {
	var f Feedback
	err := s.Scan(&f.ID, &f.Iteration, &f.Target, &f.Revision, &f.Path, &f.SectionName, &f.SectionPackage, &f.Topic, &f.Flag, &f.FlagSentence, &f.Line, &f.Side, &f.Body, &f.Created, &f.Updated, &f.Status, &f.Response, &f.ResolvedIn, &f.Published, &f.Reviewer)
	return &f, err
}

func scanRequest(s scanner) (*Request, error) {
	var r Request
	err := s.Scan(&r.ID, &r.Iteration, &r.Kind, &r.Target, &r.Revision, &r.Path, &r.SectionName, &r.SectionPackage, &r.Topic, &r.Note, &r.Status, &r.Created, &r.CaseFor, &r.CaseAgainst, &r.Reviewer)
	return &r, err
}

// queryAll runs a query and scans every row with scan.
func queryAll[T any](x querier, scan func(scanner) (*T, error), query string, args ...any) ([]*T, error) {
	rows, err := x.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// queryOne scans the first row, or returns nil when there is none.
func queryOne[T any](x querier, scan func(scanner) (*T, error), query string, args ...any) (*T, error) {
	v, err := scan(x.QueryRow(query, args...))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return v, err
}

// querier is what a DB and a transaction both offer.
type querier interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// rangesJSON stores a range list as JSON text, [] when empty.
func rangesJSON(r Ranges) string {
	if len(r) == 0 {
		return "[]"
	}
	return MustJSON(r)
}
