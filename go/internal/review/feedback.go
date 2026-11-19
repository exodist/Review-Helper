package review

// The feedback export (standards/FORMAT.md, "Feedback export"): the
// reviewer's feedback and requests, as `feedback FILE` prints them and the
// HTTP API serves them.

import (
	"errors"
	"fmt"
	"io"
)

const (
	FeedbackFormat        = "review-feedback"
	FeedbackFormatVersion = 1
)

// FeedbackExport is the feedback export.
type FeedbackExport struct {
	Format        string           `json:"format"`
	FormatVersion int              `json:"format_version"`
	Review        string           `json:"review"`
	Source        *string          `json:"source"`
	Iteration     *int64           `json:"iteration"`
	Base          *string          `json:"base"`
	Head          *string          `json:"head"`
	Feedback      []*FeedbackEntry `json:"feedback"`
	Requests      []*RequestEntry  `json:"requests"`
}

// SectionRef names a section by name and package.
type SectionRef struct {
	Name    string  `json:"name"`
	Package *string `json:"package"`
}

// FeedbackFlag is the flag a feedback entry is on, or was on before the
// agent retired it. Lines are those of the flag with that note id in the
// entry's iteration; nil for a whole-file or whole-commit flag, or one that
// iteration no longer has.
type FeedbackFlag struct {
	Note     string  `json:"note"`
	Sentence *string `json:"sentence"`
	Path     *string `json:"path"`
	Lines    *Range  `json:"lines"`
}

// FeedbackEntry is one feedback item of the export.
type FeedbackEntry struct {
	ID         int64         `json:"id"`
	Iteration  int64         `json:"iteration"`
	Target     string        `json:"target"`
	Commit     *string       `json:"commit"`
	Topic      *string       `json:"topic"`
	Path       *string       `json:"path"`
	Section    *SectionRef   `json:"section"`
	Flag       *FeedbackFlag `json:"flag"`
	Line       *int64        `json:"line"`
	Side       *string       `json:"side"`
	Body       string        `json:"body"`
	Status     string        `json:"status"`
	Response   *string       `json:"response"`
	ResolvedIn *int64        `json:"resolved_in"`
	Published  *int64        `json:"published"`
	Reviewer   *string       `json:"reviewer"`
}

// RequestEntry is one request of the export.
type RequestEntry struct {
	ID          int64       `json:"id"`
	Iteration   int64       `json:"iteration"`
	Kind        string      `json:"kind"`
	Target      string      `json:"target"`
	Commit      *string     `json:"commit"`
	Topic       *string     `json:"topic"`
	Path        *string     `json:"path"`
	Section     *SectionRef `json:"section"`
	Note        *string     `json:"note"`
	Status      string      `json:"status"`
	CaseFor     *string     `json:"case_for"`
	CaseAgainst *string     `json:"case_against"`
	Reviewer    *string     `json:"reviewer"`
}

// FeedbackExport builds the feedback export; openOnly keeps only open
// feedback and requests.
func (d *DB) FeedbackExport(openOnly bool) (*FeedbackExport, error) {
	rv, err := d.Review()
	if err != nil {
		return nil, err
	}
	latest, err := d.LatestIteration()
	if err != nil {
		return nil, err
	}
	feedback, requests, err := d.FeedbackEntries(openOnly)
	if err != nil {
		return nil, err
	}
	x := &FeedbackExport{
		Format:        FeedbackFormat,
		FormatVersion: FeedbackFormatVersion,
		Review:        rv.ID,
		Source:        rv.Source,
		Feedback:      feedback,
		Requests:      requests,
	}
	if latest != nil {
		x.Iteration, x.Base, x.Head = &latest.Number, &latest.Base, &latest.Head
	}
	return x, nil
}

// FeedbackEntries lists the feedback and request entries of every
// iteration, oldest first, as the feedback export holds them; openOnly
// keeps only open ones. Neither list is nil.
func (d *DB) FeedbackEntries(openOnly bool) ([]*FeedbackEntry, []*RequestEntry, error) {
	rows, err := d.AllFeedback()
	if err != nil {
		return nil, nil, err
	}
	reqs, err := d.AllRequests()
	if err != nil {
		return nil, nil, err
	}
	lines, err := d.flagLines()
	if err != nil {
		return nil, nil, err
	}
	feedback := make([]*FeedbackEntry, 0, len(rows))
	for _, f := range rows {
		if !openOnly || f.Status == "open" {
			feedback = append(feedback, newFeedbackEntry(f, lines))
		}
	}
	requests := make([]*RequestEntry, 0, len(reqs))
	for _, r := range reqs {
		if !openOnly || r.Status == "open" {
			requests = append(requests, newRequestEntry(r))
		}
	}
	return feedback, requests, nil
}

func newFeedbackEntry(f *Feedback, lines flagLines) *FeedbackEntry {
	return &FeedbackEntry{
		ID:         f.ID,
		Iteration:  f.Iteration,
		Target:     f.Target,
		Commit:     f.Revision,
		Topic:      f.Topic,
		Path:       f.Path,
		Section:    sectionRef(f.SectionName, f.SectionPackage),
		Flag:       feedbackFlag(f, lines),
		Line:       f.Line,
		Side:       f.Side,
		Body:       f.Body,
		Status:     f.Status,
		Response:   f.Response,
		ResolvedIn: f.ResolvedIn,
		Published:  f.Published,
		Reviewer:   f.Reviewer,
	}
}

func newRequestEntry(r *Request) *RequestEntry {
	return &RequestEntry{
		ID:          r.ID,
		Iteration:   r.Iteration,
		Kind:        r.Kind,
		Target:      r.Target,
		Commit:      r.Revision,
		Topic:       r.Topic,
		Path:        r.Path,
		Section:     sectionRef(r.SectionName, r.SectionPackage),
		Note:        r.Note,
		Status:      r.Status,
		CaseFor:     r.CaseFor,
		CaseAgainst: r.CaseAgainst,
		Reviewer:    r.Reviewer,
	}
}

func sectionRef(name, pkg *string) *SectionRef {
	if name == nil {
		return nil
	}
	return &SectionRef{Name: *name, Package: pkg}
}

// feedbackFlag is the flag of feedback that names one, else nil.
func feedbackFlag(f *Feedback, lines flagLines) *FeedbackFlag {
	if f.Flag == nil {
		return nil
	}
	ff := &FeedbackFlag{Note: *f.Flag, Sentence: f.FlagSentence, Path: f.Path}
	if r, ok := lines[flagKey{f.Iteration, *f.Flag}]; ok {
		ff.Lines = &r
	}
	return ff
}

type flagKey struct {
	iteration int64
	note      string
}

// flagLines holds the lines of every flag that has them, by iteration and
// note id.
type flagLines map[flagKey]Range

func (d *DB) flagLines() (flagLines, error) {
	rows, err := d.Query(`SELECT commit_entry.iteration, flag.note_id, flag.line_start, COALESCE(flag.line_end, flag.line_start)
	  FROM flag JOIN commit_entry ON commit_entry.id = flag.commit_id
	 WHERE flag.line_start IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lines := flagLines{}
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

func runFeedback(args []string, in io.Reader, out, warn io.Writer) error {
	var open, publish bool
	o := newOptions()
	o.flag("open", &open)
	o.flag("publish", &publish)
	rest, err := o.parse(args)
	if err != nil {
		return err
	}
	if len(rest) == 0 || (len(rest) > 1 && !publish) {
		return errors.New("usage: review-helper feedback FILE [--open | --publish [ID...]]")
	}
	if open && publish {
		return errors.New("--open and --publish do not combine")
	}
	var ids []int64
	for _, a := range rest[1:] {
		id, ok := parsePositive(a)
		if !ok {
			return fmt.Errorf("feedback ID must be a positive integer, got %q", a)
		}
		ids = append(ids, id)
	}
	d, err := OpenDB(rest[0])
	if err != nil {
		return err
	}
	defer d.Close()
	var v any
	if publish {
		v, err = d.PublishList(ids)
	} else {
		v, err = d.FeedbackExport(open)
	}
	if err != nil {
		return err
	}
	b, err := MarshalJSONIndent(v)
	if err != nil {
		return err
	}
	_, err = out.Write(b)
	return err
}

// addFeedback stores new, open feedback: its anchor, body and reviewer.
func addFeedback(q querier, f *Feedback) (int64, error) {
	t := now()
	res, err := q.Exec(`INSERT INTO feedback (iteration, target, revision, path, section_name, section_package, topic, flag, flag_sentence, line, side, body, reviewer, created, updated, status)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'open')`,
		f.Iteration, f.Target, f.Revision, f.Path, f.SectionName, f.SectionPackage, f.Topic, f.Flag, f.FlagSentence, f.Line, f.Side, f.Body, f.Reviewer, t, t)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateFeedback sets a feedback's body, status or both; nil leaves one as
// it is.
func (d *DB) UpdateFeedback(id int64, body, status *string) error {
	_, err := d.Exec("UPDATE feedback SET body = COALESCE(?, body), status = COALESCE(?, status), updated = ? WHERE id = ?", body, status, now(), id)
	return err
}

// AddRequest stores a new, open request: its kind, anchor, note and
// reviewer.
func (d *DB) AddRequest(r *Request) (int64, error) {
	res, err := d.Exec(`INSERT INTO request (iteration, kind, target, revision, path, section_name, section_package, topic, note, reviewer, created, status)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'open')`,
		r.Iteration, r.Kind, r.Target, r.Revision, r.Path, r.SectionName, r.SectionPackage, r.Topic, r.Note, r.Reviewer, now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
