package review

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
)

var (
	noteKinds    = []string{"deviation", "assumption", "self-review", "test-revealed", "note", "other"}
	noteColors   = []string{"yellow", "red"}
	noteRequired = []string{"kind", "path", "sentence"}
	noteOptional = []string{"flag", "unit", "snippet", "detail"}
)

// Journal is a worktree's journal file (standards/FORMAT.md, "The journal").
type Journal struct{ Path string }

// Note is one journal line, decoded.
type Note map[string]any

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// validateNote checks the fields of a note to add.
func validateNote(n map[string]any) error {
	var unknown []string
	for k := range n {
		if !contains(noteRequired, k) && !contains(noteOptional, k) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return errorf("Unknown note fields: %s", strings.Join(unknown, " "))
	}
	for _, f := range append(append([]string{}, noteRequired...), noteOptional...) {
		v, ok := n[f]
		if !ok || v == nil {
			if contains(noteRequired, f) {
				return errorf("Note field '%s' is required", f)
			}
			continue
		}
		s, isStr := noteText(v)
		if !isStr {
			return errorf("Note field '%s' must be a string", f)
		}
		if s == "" {
			if contains(noteRequired, f) {
				return errorf("Note field '%s' is required", f)
			}
			return errorf("Note field '%s' must not be empty", f)
		}
	}
	if v, ok := n["flag"]; ok && v != nil {
		if s, _ := noteText(v); !contains(noteColors, s) {
			return errorf("Note flag must be one of: %s", strings.Join(noteColors, " "))
		}
	}
	if s, _ := noteText(n["kind"]); !contains(noteKinds, s) {
		return errorf("Note kind must be one of: %s", strings.Join(noteKinds, " "))
	}
	return nil
}

// noteText reads a note field: a string, or a JSON number read as text.
func noteText(v any) (string, bool) {
	switch s := v.(type) {
	case string:
		return s, true
	case json.Number:
		return s.String(), true
	}
	return "", false
}

// lockFile holds an flock on f.
func lockFile(f *os.File, exclusive bool) error {
	how := syscall.LOCK_SH
	if exclusive {
		how = syscall.LOCK_EX
	}
	return syscall.Flock(int(f.Fd()), how)
}

// Add appends a note and returns its id.
func (j *Journal) Add(fields map[string]any) (string, error) {
	if v, ok := fields["kind"]; !ok || v == nil {
		fields["kind"] = "other"
	}
	if err := validateNote(fields); err != nil {
		return "", err
	}
	id := UUID7()
	note := map[string]any{"id": id, "created": Now()}
	for _, f := range append(append([]string{}, noteRequired...), noteOptional...) {
		note[f] = fields[f]
	}
	line, err := JSONEncode(note)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(j.Path), 0o777); err != nil {
		return "", err
	}
	f, err := os.OpenFile(j.Path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o666)
	if err != nil {
		return "", fmt.Errorf("Cannot open '%s': %v", j.Path, err)
	}
	defer f.Close()
	if err := lockFile(f, true); err != nil {
		return "", fmt.Errorf("Cannot lock '%s': %v", j.Path, err)
	}
	// A write cut short leaves a last line with no newline; appending to it
	// would make the new note part of that bad line.
	start := ""
	if st, err := f.Stat(); err == nil && st.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, st.Size()-1); err == nil && last[0] != '\n' {
			start = "\n"
		}
	}
	if _, err := f.WriteString(start + string(line) + "\n"); err != nil {
		return "", fmt.Errorf("Cannot write '%s': %v", j.Path, err)
	}
	return id, f.Close()
}

// Notes reads every note, skipping (with a warning) lines that are not one.
func (j *Journal) Notes() ([]Note, error) {
	f, err := os.Open(j.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("Cannot open '%s': %v", j.Path, err)
	}
	defer f.Close()
	if err := lockFile(f, false); err != nil {
		return nil, fmt.Errorf("Cannot lock '%s': %v", j.Path, err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	var notes []Note
	for i, line := range splitLines(data) {
		if n := j.decode(line, i+1); n != nil {
			notes = append(notes, n)
		}
	}
	return notes, nil
}

// splitLines splits keeping no line ends.
func splitLines(data []byte) []string {
	s := string(data)
	lines := strings.SplitAfter(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

var nonSpace = regexp.MustCompile(`\S`)

func (j *Journal) decode(line string, number int) Note {
	if !nonSpace.MatchString(line) {
		return nil
	}
	v, err := JSONDecode([]byte(line))
	if err == nil {
		if m, ok := v.(map[string]any); ok && m["id"] != nil {
			return Note(m)
		}
	}
	fmt.Fprintf(os.Stderr, "Skipping line %d of journal '%s': not a note\n", number, j.Path)
	return nil
}

// Remove deletes the notes with these ids, rewriting the file in place.
func (j *Journal) Remove(ids ...string) error {
	f, err := os.OpenFile(j.Path, os.O_RDWR, 0)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("Cannot open '%s': %v", j.Path, err)
	}
	defer f.Close()
	if err := lockFile(f, true); err != nil {
		return fmt.Errorf("Cannot lock '%s': %v", j.Path, err)
	}
	drop := map[string]bool{}
	for _, id := range ids {
		drop[id] = true
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	var keep bytes.Buffer
	for i, line := range splitLines(data) {
		if !nonSpace.MatchString(line) {
			continue
		}
		n := j.decode(line, i+1)
		if n != nil && drop[Str(noteID(n))] {
			continue
		}
		keep.WriteString(line)
	}
	if _, err := f.Seek(0, 0); err != nil {
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

func noteID(n Note) any {
	if s, ok := noteText(n["id"]); ok {
		return s
	}
	return n["id"]
}

// EntryError says what makes a journal line one the journal could not have
// written, or "" when it is fine.
func EntryError(n Note) string {
	id, ok := noteText(n["id"])
	if !ok || id == "" {
		return "'id' must be a non-empty string"
	}
	c, ok := n["created"]
	created := ""
	if num, isNum := c.(json.Number); ok && isNum {
		created = num.String()
	}
	if !regexp.MustCompile(`\A\d+\z`).MatchString(created) {
		return "'created' must be a time in epoch seconds"
	}
	fields := map[string]any{}
	for _, f := range []string{"flag", "kind", "path", "unit", "snippet", "sentence", "detail"} {
		if v, ok := n[f]; ok {
			fields[f] = v
		}
	}
	if err := validateNote(fields); err != nil {
		return err.Error()
	}
	return ""
}

// NoteString reads a note field as text ("" when absent or null).
func NoteString(n Note, field string) string {
	s, _ := noteText(n[field])
	return s
}
