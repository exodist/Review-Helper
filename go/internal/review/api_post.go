package review

// The write routes of the HTTP API (standards/API.md): feedback, requests,
// seen marks and closing the review. Anchors are checked as
// standards/FORMAT.md, "Feedback and request targets", says.

import (
	"database/sql"
	"net/http"
	"strings"
)

// anchorBody is the anchor of new feedback or a request, and the fields
// either adds. A field absent and a field null are alike.
type anchorBody struct {
	Iteration *int64      `json:"iteration"`
	Target    *string     `json:"target"`
	Commit    *string     `json:"commit"`
	Topic     *string     `json:"topic"`
	Path      *string     `json:"path"`
	Section   *sectionArg `json:"section"`
	Line      *int64      `json:"line"`
	Side      *string     `json:"side"`
	Flag      *string     `json:"flag"`
	Body      *string     `json:"body"`
	Kind      *string     `json:"kind"`
	Note      *string     `json:"note"`
}

type sectionArg struct {
	Name    *string `json:"name"`
	Package *string `json:"package"`
}

// anchor is what a target stores: the columns feedback and requests share.
type anchor struct {
	Target         string
	Revision       *string
	Path           *string
	SectionName    *string
	SectionPackage *string
	Topic          *string
}

var (
	feedbackTargets = []string{"review", "commit", "file", "section", "line", "flag"}
	requestTargets  = []string{"review", "commit", "file", "section"}
)

func oneOf(v *string, allowed ...string) bool {
	if v == nil {
		return false
	}
	for _, a := range allowed {
		if *v == a {
			return true
		}
	}
	return false
}

// text is a required, or when given an optional, non-blank string.
func text(v *string, name string, required bool) (*string, error) {
	if v == nil && !required {
		return nil, nil
	}
	if v == nil || strings.TrimSpace(*v) == "" {
		return nil, badRequest("'%s' must be a non-empty string", name)
	}
	return v, nil
}

// anchorOf checks a target other than flag against the iteration, and
// returns its anchor and, for a target with a path, its file change.
func (s *Server) anchorOf(in *anchorBody, it *Iteration, targets []string) (*anchor, *FileChange, error) {
	if !oneOf(in.Target, targets...) {
		return nil, nil, badRequest("'target' must be one of: %s", strings.Join(targets, " "))
	}
	a := &anchor{Target: *in.Target}
	if a.Target == "review" {
		return a, nil, nil
	}
	if in.Commit == nil {
		return nil, nil, badRequest("'commit' must be a commit's revision id")
	}
	rev := *in.Commit
	entries, err := s.db.CommitsByRevision(it.Number, rev)
	if err != nil {
		return nil, nil, err
	}
	if len(entries) == 0 {
		return nil, nil, badRequest("Commit '%s' is not in iteration %d", rev, it.Number)
	}
	a.Revision = &rev
	if a.Target == "commit" {
		a.Topic, err = entryTopic(in.Topic, rev, entries)
		return a, nil, err
	}

	if in.Path == nil {
		return nil, nil, badRequest("'path' must be a string")
	}
	// In a squashed iteration split into topics, the path is in the one
	// topic holding it.
	var file *FileChange
	for _, e := range entries {
		if file, err = s.db.FileByPath(e.ID, *in.Path); err != nil || file != nil {
			break
		}
	}
	if err != nil {
		return nil, nil, err
	}
	if file == nil {
		return nil, nil, badRequest("Commit '%s' does not change '%s'", rev, *in.Path)
	}
	a.Path = in.Path

	if in.Section == nil {
		if a.Target == "section" {
			return nil, nil, badRequest("A section target needs 'section'")
		}
		return a, file, nil
	}
	if in.Section.Name == nil {
		return nil, nil, badRequest("'section' must be an object with 'name' and 'package'")
	}
	name, pkg := *in.Section.Name, deref(in.Section.Package)
	touched, err := s.touchedSections(file.ID)
	if err != nil {
		return nil, nil, err
	}
	known := false
	for _, x := range touched {
		known = known || (x.Name == name && deref(x.Package) == pkg)
	}
	if !known {
		return nil, nil, badRequest("'%s' has no section '%s' with a change to show", *in.Path, name)
	}
	a.SectionName, a.SectionPackage = in.Section.Name, in.Section.Package
	return a, file, nil
}

// entryTopic is the topic a commit target names. A squashed iteration
// split into topics has one entry per topic, all with the head's revision,
// so the topic picks one; any other commit has one entry and no topic.
func entryTopic(topic *string, rev string, entries []*CommitEntry) (*string, error) {
	for _, e := range entries {
		if (e.Topic == nil && topic == nil) || (e.Topic != nil && topic != nil && *e.Topic == *topic) {
			return e.Topic, nil
		}
	}
	if topic != nil {
		return nil, badRequest("Commit '%s' has no topic '%s'", rev, *topic)
	}
	return nil, badRequest("Commit '%s' is split into topics; give 'topic'", rev)
}

// addFeedback adds feedback. Feedback on a flag also marks the flag seen by
// its writer.
func (s *Server) addFeedback(w http.ResponseWriter, r *http.Request) error {
	var in anchorBody
	if err := decodeBody(r, &in); err != nil {
		return err
	}
	it, err := s.iteration(in.Iteration)
	if err != nil {
		return err
	}
	fb := &Feedback{Iteration: it.Number, Reviewer: &s.access.Reviewer}
	var flag *Flag
	if in.Target != nil && *in.Target == "flag" {
		if in.Flag == nil {
			return badRequest("'flag' must be a flag's note id")
		}
		if flag, err = s.db.FlagByNote(it.Number, *in.Flag); err != nil {
			return err
		}
		if flag == nil {
			return badRequest("Iteration %d has no flag '%s'", it.Number, *in.Flag)
		}
		fb.Target, fb.Flag, fb.FlagSentence, fb.Revision, fb.Path = "flag", &flag.NoteID, &flag.Sentence, &flag.Revision, flag.Path
		if flag.Path == nil {
			fb.Topic = flag.Topic
		}
	} else {
		a, _, err := s.anchorOf(&in, it, feedbackTargets)
		if err != nil {
			return err
		}
		fb.Target, fb.Revision, fb.Path, fb.SectionName, fb.SectionPackage, fb.Topic = a.Target, a.Revision, a.Path, a.SectionName, a.SectionPackage, a.Topic
	}
	if fb.Target == "line" {
		if in.Line == nil || *in.Line < 1 {
			return badRequest("'line' must be a positive integer")
		}
		if !oneOf(in.Side, "old", "new") {
			return badRequest("'side' must be 'old' or 'new'")
		}
		fb.Line, fb.Side = in.Line, in.Side
	}
	body, err := text(in.Body, "body", true)
	if err != nil {
		return err
	}
	fb.Body = *body

	var id int64
	err = s.db.Txn(func(tx *sql.Tx) error {
		if flag != nil {
			if err := setFlagSeen(tx, flag.ID, s.access.Reviewer, true); err != nil {
				return err
			}
		}
		id, err = addFeedback(tx, fb)
		return err
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]int64{"id": id})
}

// editFeedback edits a feedback's body or status. Only its author edits it;
// feedback with no reviewer, written before reviewers were recorded, is
// anyone's.
func (s *Server) editFeedback(w http.ResponseWriter, r *http.Request) error {
	id, err := idValue(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		Body   *string `json:"body"`
		Status *string `json:"status"`
	}
	if err := decodeBody(r, &in); err != nil {
		return err
	}
	if in.Body != nil {
		if _, err := text(in.Body, "body", true); err != nil {
			return err
		}
	}
	if in.Status != nil && !oneOf(in.Status, "open", "addressed", "declined") {
		return badRequest("'status' must be one of: open addressed declined")
	}
	if in.Body == nil && in.Status == nil {
		return badRequest("Give 'body', 'status', or both")
	}
	fb, err := s.db.FeedbackByID(id)
	if err != nil {
		return err
	}
	if fb == nil {
		return notFound("No feedback with id %d", id)
	}
	if by := deref(fb.Reviewer); strings.TrimSpace(by) != "" && by != s.access.Reviewer {
		return forbidden("Only %s can edit this feedback", by)
	}
	if err := s.db.UpdateFeedback(id, in.Body, in.Status); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, struct{}{})
}

// addRequest adds a request for a debate or a split.
func (s *Server) addRequest(w http.ResponseWriter, r *http.Request) error {
	var in anchorBody
	if err := decodeBody(r, &in); err != nil {
		return err
	}
	it, err := s.iteration(in.Iteration)
	if err != nil {
		return err
	}
	if !oneOf(in.Kind, "debate", "split") {
		return badRequest("'kind' must be one of: debate split")
	}
	a, file, err := s.anchorOf(&in, it, requestTargets)
	if err != nil {
		return err
	}
	if *in.Kind == "split" {
		if a.Target != "file" {
			return badRequest("A 'split' request must target a file")
		}
		if err := splittable(file); err != nil {
			return err
		}
	}
	note, err := text(in.Note, "note", false)
	if err != nil {
		return err
	}
	id, err := s.db.AddRequest(&Request{
		Iteration: it.Number, Kind: *in.Kind, Target: a.Target, Revision: a.Revision, Path: a.Path,
		SectionName: a.SectionName, SectionPackage: a.SectionPackage, Topic: a.Topic, Note: note, Reviewer: &s.access.Reviewer,
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]int64{"id": id})
}

// splittable refuses a file the fill sheet gives no split item: one with no
// lines after the commit, or whose commit added or changed none of them.
func splittable(f *FileChange) error {
	if f.SizeUnit != "lines" || f.Status == "D" {
		return badRequest("'%s' has no lines to split: it is deleted, an image, or binary", f.Path)
	}
	if f.Status == "A" || f.Added != 0 || f.Removed != 0 || f.Changed != 0 {
		return nil
	}
	return badRequest("'%s' has nothing to split: a rename or mode change alone leaves its content as it was", f.Path)
}

// setFlagSeen sets or removes the reviewer's seen mark on a flag and
// answers who has seen it.
func (s *Server) setFlagSeen(w http.ResponseWriter, r *http.Request) error {
	id, err := idValue(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		Seen *bool `json:"seen"`
	}
	if err := decodeBody(r, &in); err != nil {
		return err
	}
	flag, err := s.db.FlagByID(id)
	if err != nil {
		return err
	}
	if flag == nil {
		return notFound("No flag with id %d", id)
	}
	if in.Seen == nil {
		return badRequest("'seen' must be true or false")
	}
	if err := s.db.SetFlagSeen(flag.ID, s.access.Reviewer, *in.Seen); err != nil {
		return err
	}
	c, err := s.db.Commit(flag.CommitID)
	if err != nil {
		return err
	}
	marks, err := s.db.FlagSeen(c.Iteration)
	if err != nil {
		return err
	}
	seen := []seenJSON{}
	for _, m := range marks {
		if m.Flag == flag.ID {
			seen = append(seen, seenJSON{Reviewer: m.Reviewer, At: m.At})
		}
	}
	return writeJSON(w, http.StatusOK, map[string][]seenJSON{"seen": seen})
}

// closeReview closes the review, as `close FILE` does.
func (s *Server) closeReview(w http.ResponseWriter, r *http.Request) error {
	if err := NewRegistry(s.repo).Close(s.db, s.path); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, struct{}{})
}
