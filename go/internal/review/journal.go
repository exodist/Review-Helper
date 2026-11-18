package review

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
)

// The journal (standards/FORMAT.md, "The journal"): JSON Lines of notes,
// shared by concurrent processes under an flock.

var (
	noteKinds  = []string{"deviation", "assumption", "self-review", "test-revealed", "note", "other"}
	noteColors = []string{"yellow", "red"}
)

// Text is a note field: absent or null (Set false), or text. A reader also
// takes a JSON number as text.
type Text struct {
	Value string
	Set   bool
	bad   bool // neither a string, a number nor null
}

// UnmarshalJSON accepts a string, a number or null.
func (t *Text) UnmarshalJSON(b []byte) error {
	*t = Text{}
	if isNull(b) {
		return nil
	}
	t.Value, t.Set = jsonText(b)
	t.bad = !t.Set
	return nil
}

// MarshalJSON writes the text, or null.
func (t Text) MarshalJSON() ([]byte, error) {
	if !t.Set {
		return []byte("null"), nil
	}
	return json.Marshal(t.Value)
}

// Ptr is the text, or nil.
func (t Text) Ptr() *string {
	if !t.Set {
		return nil
	}
	return &t.Value
}

// Epoch is a note's time in whole epoch seconds. A reader takes the digits
// as a JSON number or, as the Perl backend does, a string; anything else
// leaves it unset.
type Epoch struct {
	Seconds int64
	Set     bool
}

// UnmarshalJSON reads digits, as a number or a string.
func (e *Epoch) UnmarshalJSON(b []byte) error {
	*e = Epoch{}
	if s, ok := jsonText(b); ok {
		e.Seconds, e.Set = parseDigits(s)
	}
	return nil
}

// MarshalJSON writes the time as a number, or null.
func (e Epoch) MarshalJSON() ([]byte, error) {
	if !e.Set {
		return []byte("null"), nil
	}
	return strconv.AppendInt(nil, e.Seconds, 10), nil
}

// Note is one journal note. A note with Flag set becomes a flag when placed;
// one without is a journal entry.
type Note struct {
	ID       Text  `json:"id"`
	Created  Epoch `json:"created"`
	Flag     Text  `json:"flag"`
	Kind     Text  `json:"kind"`
	Path     Text  `json:"path"`
	Unit     Text  `json:"unit"`
	Snippet  Text  `json:"snippet"`
	Sentence Text  `json:"sentence"`
	Detail   Text  `json:"detail"`
}

// Problem says what makes the note one the journal could not have written,
// or is nil when it is fine.
func (n *Note) Problem() error {
	if !n.ID.Set || n.ID.Value == "" {
		return errors.New("'id' must be a non-empty string")
	}
	if !n.Created.Set {
		return errors.New("'created' must be a time in epoch seconds")
	}
	return n.validate()
}

// validate checks the note's fields: kind, path and sentence required, the
// rest optional, none empty, flag and kind from their lists.
func (n *Note) validate() error {
	fields := []struct {
		name     string
		text     Text
		required bool
	}{
		{"kind", n.Kind, true}, {"path", n.Path, true}, {"sentence", n.Sentence, true},
		{"flag", n.Flag, false}, {"unit", n.Unit, false}, {"snippet", n.Snippet, false}, {"detail", n.Detail, false},
	}
	for _, f := range fields {
		switch {
		case f.text.bad:
			return fmt.Errorf("note field %s must be a string", f.name)
		case !f.text.Set && f.required, f.text.Set && f.text.Value == "" && f.required:
			return fmt.Errorf("note field %s is required", f.name)
		case f.text.Set && f.text.Value == "":
			return fmt.Errorf("note field %s must not be empty", f.name)
		}
	}
	if n.Flag.Set && !slices.Contains(noteColors, n.Flag.Value) {
		return fmt.Errorf("note flag must be one of: %v", noteColors)
	}
	if !slices.Contains(noteKinds, n.Kind.Value) {
		return fmt.Errorf("note kind must be one of: %v", noteKinds)
	}
	return nil
}

// Journal is a worktree's journal file. Warn, when set, gets a line for each
// line of the file skipped as not a note.
type Journal struct {
	Path string
	Warn io.Writer
}

// lock holds an flock on f until it is closed.
func lock(f *os.File, exclusive bool) error {
	how := syscall.LOCK_SH
	if exclusive {
		how = syscall.LOCK_EX
	}
	return syscall.Flock(int(f.Fd()), how)
}

// Add appends a note, defaulting its kind to other, and returns its id.
func (j *Journal) Add(n Note) (string, error) {
	if !n.Kind.Set {
		n.Kind = Text{Value: "other", Set: true}
	}
	if err := n.validate(); err != nil {
		return "", err
	}
	id := UUID7()
	n.ID = Text{Value: id, Set: true}
	n.Created = Epoch{Seconds: now(), Set: true}
	line, err := MarshalJSON(n)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(j.Path), 0o777); err != nil {
		return "", err
	}
	f, err := os.OpenFile(j.Path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o666)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := lock(f, true); err != nil {
		return "", fmt.Errorf("lock %s: %w", j.Path, err)
	}
	// A write cut short leaves a last line with no newline; appending to it
	// would make the new note part of that bad line.
	if st, err := f.Stat(); err == nil && st.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, st.Size()-1); err == nil && last[0] != '\n' {
			line = append([]byte("\n"), line...)
		}
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return "", err
	}
	return id, f.Close()
}

// Notes reads every note, skipping (with a warning) lines that are not one.
func (j *Journal) Notes() ([]*Note, error) {
	f, err := os.Open(j.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := lock(f, false); err != nil {
		return nil, fmt.Errorf("lock %s: %w", j.Path, err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	var notes []*Note
	for i, line := range bytes.SplitAfter(data, []byte("\n")) {
		if n := j.decode(line, i+1); n != nil {
			notes = append(notes, n)
		}
	}
	return notes, nil
}

// decode reads one line: nil for a blank line, or (with a warning) for one
// that is not a JSON object with an id.
func (j *Journal) decode(line []byte, number int) *Note {
	if len(bytes.TrimSpace(line)) == 0 {
		return nil
	}
	var n Note
	var probe map[string]json.RawMessage
	if json.Unmarshal(line, &probe) == nil {
		if id, ok := probe["id"]; ok && string(id) != "null" && json.Unmarshal(line, &n) == nil {
			return &n
		}
	}
	if j.Warn != nil {
		fmt.Fprintf(j.Warn, "skipping line %d of journal %s: not a note\n", number, j.Path)
	}
	return nil
}

// Remove deletes the notes with these ids, rewriting the file in place
// under the lock; lines that are not notes are kept as they are.
func (j *Journal) Remove(ids ...string) error {
	f, err := os.OpenFile(j.Path, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	if err := lock(f, true); err != nil {
		return fmt.Errorf("lock %s: %w", j.Path, err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	var keep bytes.Buffer
	for i, line := range bytes.SplitAfter(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if n := j.decode(line, i+1); n != nil && slices.Contains(ids, n.ID.Value) {
			continue
		}
		keep.Write(line)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Write(keep.Bytes()); err != nil {
		return err
	}
	return f.Close()
}
