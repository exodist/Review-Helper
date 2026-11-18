package review

// The read routes of the HTTP API (standards/API.md, "Routes").

import (
	"bytes"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
)

// The JSON of /api/review and its parts. Field names and values are those
// of the review file; booleans and numbers are as the API says.

type reviewJSON struct {
	StandardVersion int                `json:"standard_version"`
	Review          reviewRowJSON      `json:"review"`
	Identity        Identity           `json:"identity"`
	Iteration       int64              `json:"iteration"`
	Iterations      []iterationJSON    `json:"iterations"`
	Summary         summaryJSON        `json:"summary"`
	Commits         []commitJSON       `json:"commits"`
	Flags           []flagJSON         `json:"flags"`
	Guide           []guideJSON        `json:"guide"`
	Journal         []journalJSON      `json:"journal"`
	ContentKinds    map[string]*string `json:"content_kinds"`
	Feedback        []*FeedbackEntry   `json:"feedback"`
	Requests        []*RequestEntry    `json:"requests"`
}

type reviewRowJSON struct {
	ID     string  `json:"id"`
	Title  *string `json:"title"`
	Source *string `json:"source"`
	State  string  `json:"state"`
}

type iterationJSON struct {
	Number    int64   `json:"number"`
	Base      string  `json:"base"`
	Head      string  `json:"head"`
	Created   int64   `json:"created"`
	Note      *string `json:"note"`
	Available bool    `json:"available"`
}

type summaryJSON struct {
	What *string `json:"what"`
	Why  *string `json:"why"`
	How  *string `json:"how"`
}

type commitJSON struct {
	ID         int64            `json:"id"`
	Revision   string           `json:"revision"`
	Parent     *string          `json:"parent"`
	Position   int64            `json:"position"`
	MergeID    *int64           `json:"merge_id"`
	Topic      *string          `json:"topic"`
	Subject    string           `json:"subject"`
	Message    string           `json:"message"`
	Author     *string          `json:"author"`
	Authored   *int64           `json:"authored"`
	What       *string          `json:"what"`
	Why        *string          `json:"why"`
	How        *string          `json:"how"`
	OrderSet   int              `json:"order_set"`
	Resolution bool             `json:"resolution"`
	Files      []reviewFileJSON `json:"files"`
}

type fileJSON struct {
	ID           int64   `json:"id"`
	Path         string  `json:"path"`
	OldPath      *string `json:"old_path"`
	Status       string  `json:"status"`
	Kind         string  `json:"kind"`
	SizeUnit     string  `json:"size_unit"`
	SizeBefore   *int64  `json:"size_before"`
	SizeAfter    *int64  `json:"size_after"`
	Added        int64   `json:"added"`
	Removed      int64   `json:"removed"`
	Changed      int64   `json:"changed"`
	ReadPosition *int64  `json:"read_position"`
	SplitSource  string  `json:"split_source"`
	What         *string `json:"what"`
	ObjectSystem *string `json:"object_system"`
	Paradigms    *string `json:"paradigms"`
}

// reviewFileJSON is a file as /api/review lists it: with the names of the
// sections the page shows, so it can tell section feedback it has no card
// for.
type reviewFileJSON struct {
	fileJSON
	SectionNames []SectionRef `json:"section_names"`
}

type sectionJSON struct {
	ID        int64   `json:"id"`
	Position  int64   `json:"position"`
	Name      string  `json:"name"`
	Package   *string `json:"package"`
	Deleted   int     `json:"deleted"`
	Added     int64   `json:"added"`
	Removed   int64   `json:"removed"`
	Changed   int64   `json:"changed"`
	What      *string `json:"what"`
	Ranges    Ranges  `json:"ranges"`
	DocRanges Ranges  `json:"doc_ranges"`
	OldRanges Ranges  `json:"old_ranges"`
}

type flagJSON struct {
	ID          int64      `json:"id"`
	NoteID      string     `json:"note_id"`
	Commit      string     `json:"commit"`
	Entry       int64      `json:"entry"`
	Path        *string    `json:"path"`
	LineStart   *int64     `json:"line_start"`
	LineEnd     *int64     `json:"line_end"`
	Color       string     `json:"color"`
	ColorReason *string    `json:"color_reason"`
	Kind        string     `json:"kind"`
	Sentence    string     `json:"sentence"`
	Detail      *string    `json:"detail"`
	Pending     bool       `json:"pending"`
	Seen        []seenJSON `json:"seen"`
}

type seenJSON struct {
	Reviewer string `json:"reviewer"`
	At       int64  `json:"at"`
}

type guideJSON struct {
	Position   int64           `json:"position"`
	Target     json.RawMessage `json:"target"`
	Highlight  json.RawMessage `json:"highlight"`
	Commentary string          `json:"commentary"`
}

type journalJSON struct {
	ID       string  `json:"id"`
	Kind     string  `json:"kind"`
	Path     string  `json:"path"`
	Unit     *string `json:"unit"`
	Sentence string  `json:"sentence"`
	Detail   *string `json:"detail"`
	Created  int64   `json:"created"`
}

func newFileJSON(f *FileChange) fileJSON {
	return fileJSON{
		ID: f.ID, Path: f.Path, OldPath: f.OldPath, Status: f.Status, Kind: f.Kind, SizeUnit: f.SizeUnit,
		SizeBefore: f.SizeBefore, SizeAfter: f.SizeAfter, Added: f.Added, Removed: f.Removed, Changed: f.Changed,
		ReadPosition: f.ReadPosition, SplitSource: f.SplitSource, What: f.What, ObjectSystem: f.ObjectSystem, Paradigms: f.Paradigms,
	}
}

func newSectionJSON(x *Section) sectionJSON {
	return sectionJSON{
		ID: x.ID, Position: x.Position, Name: x.Name, Package: x.Package, Deleted: b2i(x.Deleted),
		Added: x.Added, Removed: x.Removed, Changed: x.Changed, What: x.What,
		Ranges: orEmpty(x.Ranges), DocRanges: orEmpty(x.DocRanges), OldRanges: orEmpty(x.OldRanges),
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func orEmpty(r Ranges) Ranges {
	if r == nil {
		return Ranges{}
	}
	return r
}

// touchedSections are the file's sections its commit changed, the only
// ones the page shows. A split stores the untouched ones too, for the next
// split to start from.
func (s *Server) touchedSections(fileID int64) ([]*Section, error) {
	all, err := s.db.Sections(fileID)
	if err != nil {
		return nil, err
	}
	var out []*Section
	for _, x := range all {
		if x.Touched() {
			out = append(out, x)
		}
	}
	return out, nil
}

// iteration is the iteration a request names, else the latest.
func (s *Server) iteration(number *int64) (*Iteration, error) {
	if number == nil {
		it, err := s.db.LatestIteration()
		if err == nil && it == nil {
			return nil, notFound("The review has no iterations")
		}
		return it, err
	}
	if *number < 1 {
		return nil, badRequest("'iteration' must be a positive integer")
	}
	it, err := s.db.Iteration(*number)
	if err == nil && it == nil {
		return nil, notFound("No iteration %d", *number)
	}
	return it, err
}

// queryIteration is the iteration ?iteration=N names, else the latest.
func (s *Server) queryIteration(r *http.Request) (*Iteration, error) {
	q := r.URL.Query()
	if !q.Has("iteration") {
		return s.iteration(nil)
	}
	v := q.Get("iteration")
	n, err := strconv.ParseInt(v, 10, 64)
	if !positiveInt.MatchString(v) || err != nil {
		return nil, badRequest("'iteration' must be a positive integer")
	}
	return s.iteration(&n)
}

func (s *Server) getReview(w http.ResponseWriter, r *http.Request) error {
	it, err := s.queryIteration(r)
	if err != nil {
		return err
	}
	rv, err := s.db.Review()
	if err != nil {
		return err
	}
	out := &reviewJSON{
		StandardVersion: StandardVersion,
		Review:          reviewRowJSON{ID: rv.ID, Title: rv.Title, Source: rv.Source, State: rv.State},
		Identity:        s.identity,
		Iteration:       it.Number,
		Summary:         summaryJSON{What: it.What, Why: it.Why, How: it.How},
	}
	if out.Iterations, err = s.iterationsJSON(); err != nil {
		return err
	}
	if out.Commits, err = s.commitsJSON(it); err != nil {
		return err
	}
	flags, err := s.db.Flags(it.Number)
	if err != nil {
		return err
	}
	if out.Flags, err = s.flagsJSON(it, flags); err != nil {
		return err
	}
	steps, err := s.db.GuideSteps(it.Number)
	if err != nil {
		return err
	}
	out.Guide = make([]guideJSON, 0, len(steps))
	for _, g := range steps {
		out.Guide = append(out.Guide, guideJSON{Position: g.Position, Target: g.Target, Highlight: g.Highlight, Commentary: g.Commentary})
	}
	journal, err := s.db.JournalEntries(it.Number)
	if err != nil {
		return err
	}
	out.Journal = make([]journalJSON, 0, len(journal))
	for _, j := range journal {
		out.Journal = append(out.Journal, journalJSON{ID: j.ID, Kind: j.Kind, Path: j.Path, Unit: j.Unit, Sentence: j.Sentence, Detail: j.Detail, Created: j.Created})
	}
	out.ContentKinds = s.contentKindsOf(out.Commits, flags, steps)
	if out.Feedback, out.Requests, err = s.db.FeedbackEntries(false); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) iterationsJSON() ([]iterationJSON, error) {
	its, err := s.db.Iterations()
	if err != nil {
		return nil, err
	}
	out := make([]iterationJSON, 0, len(its))
	for _, x := range its {
		ok, err := s.repo.HasRevision(x.Head)
		if err != nil {
			return nil, err
		}
		out = append(out, iterationJSON{Number: x.Number, Base: x.Base, Head: x.Head, Created: x.Created, Note: x.Note, Available: ok})
	}
	return out, nil
}

func (s *Server) commitsJSON(it *Iteration) ([]commitJSON, error) {
	commits, err := s.db.Commits(it.Number)
	if err != nil {
		return nil, err
	}
	out := make([]commitJSON, 0, len(commits))
	for _, c := range commits {
		files, err := s.db.Files(c.ID)
		if err != nil {
			return nil, err
		}
		cj := commitJSON{
			ID: c.ID, Revision: c.Revision, Parent: c.Parent, Position: c.Position, MergeID: c.MergeID, Topic: c.Topic,
			Subject: c.Subject, Message: c.Message, Author: c.Author, Authored: c.Authored,
			What: c.What, Why: c.Why, How: c.How, OrderSet: b2i(c.OrderSet),
			Files: make([]reviewFileJSON, 0, len(files)),
		}
		for _, f := range files {
			touched, err := s.touchedSections(f.ID)
			if err != nil {
				return nil, err
			}
			names := make([]SectionRef, 0, len(touched))
			for _, x := range touched {
				names = append(names, SectionRef{Name: x.Name, Package: x.Package})
			}
			cj.Files = append(cj.Files, reviewFileJSON{fileJSON: newFileJSON(f), SectionNames: names})
		}
		// Only merges have resolutions, and a merge entry lists no files.
		if len(files) == 0 {
			res, err := s.resolutionsOf(c.Revision, it)
			if err != nil {
				return nil, err
			}
			cj.Resolution = len(res) > 0
		}
		out = append(out, cj)
	}
	return out, nil
}

func (s *Server) flagsJSON(it *Iteration, flags []*Flag) ([]flagJSON, error) {
	marks, err := s.db.FlagSeen(it.Number)
	if err != nil {
		return nil, err
	}
	seen := map[int64][]seenJSON{}
	for _, m := range marks {
		seen[m.Flag] = append(seen[m.Flag], seenJSON{Reviewer: m.Reviewer, At: m.At})
	}
	out := make([]flagJSON, 0, len(flags))
	for _, f := range flags {
		marks := seen[f.ID]
		if marks == nil {
			marks = []seenJSON{}
		}
		out = append(out, flagJSON{
			ID: f.ID, NoteID: f.NoteID, Commit: f.Revision, Entry: f.CommitID, Path: f.Path,
			LineStart: f.LineStart, LineEnd: f.LineEnd, Color: f.Color, ColorReason: f.ColorReason,
			Kind: f.Kind, Sentence: f.Sentence, Detail: f.Detail, Pending: f.Pending, Seen: marks,
		})
	}
	return out, nil
}

// contentKindsOf gives the kind of each file the page may show whole at a
// commit that did not change it (a flag's, a view guide step's), keyed
// "REV:PATH". Content at a revision never changes, so a kind found is kept
// for later loads; a file that cannot be read is plain text this time
// rather than failing the whole review.
func (s *Server) contentKindsOf(commits []commitJSON, flags []*Flag, steps []*GuideStep) map[string]*string {
	changed := map[string]bool{}
	for _, c := range commits {
		for _, f := range c.Files {
			changed[c.Revision+":"+f.Path] = true
		}
	}
	type at struct{ rev, path string }
	var shown []at
	for _, f := range flags {
		if f.Path != nil {
			shown = append(shown, at{f.Revision, *f.Path})
		}
	}
	for _, g := range steps {
		var t struct{ Type, Commit, Path string }
		if json.Unmarshal(g.Target, &t) == nil && t.Type == "view" {
			shown = append(shown, at{t.Commit, t.Path})
		}
	}
	kinds := map[string]*string{}
	for _, x := range shown {
		key := x.rev + ":" + x.path
		if _, done := kinds[key]; done || changed[key] {
			continue
		}
		kinds[key] = s.contentKind(x.rev, x.path)
	}
	return kinds
}

func (s *Server) contentKind(rev, path string) *string {
	key := rev + ":" + path
	s.mu.Lock()
	kind, ok := s.contentKinds[key]
	s.mu.Unlock()
	if ok {
		return &kind
	}
	b, err := s.repo.File(rev, path)
	if err != nil {
		text := "text"
		return &text
	}
	if b == nil {
		return nil
	}
	kind = kindOfContent(path, b)
	s.mu.Lock()
	s.contentKinds[key] = kind
	s.mu.Unlock()
	return &kind
}

// kindOfContent is the kind a file change would get: by name or #! line,
// binary for content with a NUL in its first 8000 bytes (git's test).
func kindOfContent(path string, b []byte) string {
	first, _, _ := bytes.Cut(b, []byte("\n"))
	kind := KindFor(path, string(first))
	if kind != "image" && hasNUL(b) {
		return "binary"
	}
	return kind
}

func hasNUL(b []byte) bool {
	return bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0
}

// fileAndCommit is a file change and its entry.
func (s *Server) fileAndCommit(r *http.Request) (*FileChange, *CommitEntry, error) {
	id, err := idValue(r, "id")
	if err != nil {
		return nil, nil, err
	}
	f, err := s.db.File(id)
	if err != nil {
		return nil, nil, err
	}
	if f == nil {
		return nil, nil, notFound("No file with id %d", id)
	}
	c, err := s.db.Commit(f.CommitID)
	if err != nil {
		return nil, nil, err
	}
	return f, c, nil
}

func (s *Server) getFile(w http.ResponseWriter, r *http.Request) error {
	f, c, err := s.fileAndCommit(r)
	if err != nil {
		return err
	}
	touched, err := s.touchedSections(f.ID)
	if err != nil {
		return err
	}
	sections := make([]sectionJSON, 0, len(touched))
	for _, x := range touched {
		sections = append(sections, newSectionJSON(x))
	}
	return writeJSON(w, http.StatusOK, struct {
		File     fileJSON      `json:"file"`
		Commit   string        `json:"commit"`
		Parent   *string       `json:"parent"`
		Sections []sectionJSON `json:"sections"`
	}{newFileJSON(f), c.Revision, c.Parent, sections})
}

// diffOf is the file change's diff, parsed.
func (s *Server) diffOf(f *FileChange, c *CommitEntry) (*Diff, error) {
	from := ""
	if c.Parent != nil {
		from = *c.Parent
	} else {
		empty, err := s.repo.EmptyRevision()
		if err != nil {
			return nil, err
		}
		from = empty
	}
	raw, err := s.repo.RangeDiff(from, c.Revision, f.Path, deref(f.OldPath))
	if err != nil {
		return nil, err
	}
	return ParseDiff(string(raw))
}

func (s *Server) getDiff(w http.ResponseWriter, r *http.Request) error {
	f, c, err := s.fileAndCommit(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	var sectionID int64
	if q.Has("section") {
		v := q.Get("section")
		id, err := strconv.ParseInt(v, 10, 64)
		if !allDigits(v) || err != nil {
			return badRequest("Invalid section id")
		}
		sectionID = id
	}
	d, err := s.diffOf(f, c)
	if err != nil {
		return err
	}
	if !q.Has("section") {
		return writeJSON(w, http.StatusOK, d)
	}
	sections, err := s.db.Sections(f.ID)
	if err != nil {
		return err
	}
	index := -1
	spans := make([]*Span, len(sections))
	for i, x := range sections {
		spans[i] = x.Span()
		if x.ID == sectionID {
			index = i
		}
	}
	if index < 0 {
		return notFound("File %d has no section %d", f.ID, sectionID)
	}
	lines, err := s.newLines(f, c)
	if err != nil {
		return err
	}
	hunks := SectionHunks(d.Hunks, lines, spans, index)
	if hunks == nil {
		hunks = []*Hunk{}
	}
	return writeJSON(w, http.StatusOK, &Diff{Binary: d.Binary, Hunks: hunks})
}

// newLines are the file's lines after the commit; none when it is deleted.
func (s *Server) newLines(f *FileChange, c *CommitEntry) ([]string, error) {
	if f.Status == "D" {
		return []string{}, nil
	}
	b, err := s.repo.File(c.Revision, f.Path)
	if err != nil {
		return nil, err
	}
	return SplitLines(string(b)), nil
}

// ResolutionFile is one file of a merge's conflict resolutions and other
// changes made in the merge.
type ResolutionFile struct {
	Path    string  `json:"path"`
	OldPath *string `json:"old_path"`
	Status  string  `json:"status"`
	Binary  bool    `json:"binary"`
	Hunks   []*Hunk `json:"hunks"`
}

type resolutionKey struct{ revision, paths string }

// resolutionsOf is a merge's resolutions within the iteration's path
// specs; empty for a clean merge, a commit that is not a merge or not in
// the repository, and when git cannot show them. A revision never changes,
// so each is computed once per server; an absent commit is not kept, as it
// may be fetched while the server runs.
func (s *Server) resolutionsOf(revision string, it *Iteration) ([]*ResolutionFile, error) {
	key := resolutionKey{revision, MustJSON(it.Paths)}
	// Held throughout: git access caches what it probes, unguarded.
	s.mu.Lock()
	defer s.mu.Unlock()
	if files, ok := s.resolutions[key]; ok {
		return files, nil
	}
	ok, err := s.repo.HasRevision(revision)
	if err != nil || !ok {
		return []*ResolutionFile{}, err
	}
	changes, err := s.repo.ResolutionChanges(revision, it.Paths)
	if err != nil {
		return nil, err
	}
	files := make([]*ResolutionFile, 0, len(changes))
	for _, ch := range changes {
		raw, err := s.repo.ResolutionDiff(revision, ch.Path, ch.OldPath)
		if err != nil {
			return nil, err
		}
		d, err := ParseDiff(string(raw))
		if err != nil {
			return nil, err
		}
		rf := &ResolutionFile{Path: ch.Path, Status: ch.Status, Binary: d.Binary, Hunks: d.Hunks}
		if ch.OldPath != "" {
			rf.OldPath = &ch.OldPath
		}
		files = append(files, rf)
	}
	s.resolutions[key] = files
	return files, nil
}

func (s *Server) getResolution(w http.ResponseWriter, r *http.Request) error {
	id, err := idValue(r, "id")
	if err != nil {
		return err
	}
	c, err := s.db.Commit(id)
	if err != nil {
		return err
	}
	if c == nil {
		return notFound("No commit with id %d", id)
	}
	if ok, err := s.repo.HasRevision(c.Revision); err != nil || !ok {
		if err != nil {
			return err
		}
		return notFound("Commit %d is not in this repository", id)
	}
	parents, err := s.repo.Parents(c.Revision)
	if err != nil {
		return err
	}
	if len(parents) < 2 {
		return notFound("Commit %d is not a merge", id)
	}
	it, err := s.db.Iteration(c.Iteration)
	if err != nil {
		return err
	}
	files, err := s.resolutionsOf(c.Revision, it)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, struct {
		Commit string            `json:"commit"`
		Files  []*ResolutionFile `json:"files"`
	}{c.Revision, files})
}

var revisionRE = regexp.MustCompile(`^[0-9A-Za-z]{4,64}$`)

// getContent serves a file's bytes at a revision. Reviewed content is
// untrusted: a browser must never run it on this origin.
func (s *Server) getContent(w http.ResponseWriter, r *http.Request) error {
	rev, rel := r.PathValue("rev"), r.PathValue("path")
	if !revisionRE.MatchString(rev) || !safePath(rel) {
		return notFound("Not found")
	}
	b, err := s.repo.File(rev, rel)
	if err != nil {
		return err
	}
	if b == nil {
		return notFound("No such file at that commit")
	}
	ct := "text/plain; charset=utf-8"
	if ext := extension(rel); imageTypes[ext] {
		ct = contentTypes[ext]
	} else if hasNUL(b) {
		ct = "application/octet-stream"
	}
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox")
	_, err = w.Write(b)
	return err
}

func (s *Server) getFeedback(w http.ResponseWriter, r *http.Request) error {
	x, err := s.db.FeedbackExport(false)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, x)
}
