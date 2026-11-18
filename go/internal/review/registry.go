package review

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"unicode/utf8"

	"github.com/exodist/Review-Helper/go/internal/git"
)

// The registry (standards/FORMAT.md, "Registry"): the repository's reviews,
// shared by every worktree and by concurrent processes under an flock.

// RegEntry is one review's registry entry.
type RegEntry struct {
	Path  string `json:"path"`
	State string `json:"state"`
}

type registryFile struct {
	Reviews map[string]RegEntry `json:"reviews"`
}

// Registry is a repository's registry file.
type Registry struct {
	Repo *git.Repo
	Path string
}

// NewRegistry is the registry of repo.
func NewRegistry(repo *git.Repo) *Registry {
	return &Registry{Repo: repo, Path: filepath.Join(repo.SharedStateDir(), "registry.json")}
}

var reviewIDRE = regexp.MustCompile(`\A[0-9A-Za-z][0-9A-Za-z_-]*\z`)

// Close marks a review closed in its file and the registry.
func (r *Registry) Close(d *DB, path string) error {
	if err := d.SetState("closed"); err != nil {
		return err
	}
	rv, err := d.Review()
	if err != nil {
		return err
	}
	return r.Register(rv.ID, path, "closed")
}

// Register records a review's file, by absolute path, and state.
func (r *Registry) Register(id, path, state string) error {
	if !reviewIDRE.MatchString(id) {
		return fmt.Errorf("invalid review id %q", id)
	}
	if state != "open" && state != "closed" {
		return fmt.Errorf("review state must be open or closed, not %q", state)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	// The registry holds paths as JSON text, which cannot keep other bytes.
	if !utf8.ValidString(abs) {
		return fmt.Errorf("review file paths must be valid UTF-8: %q", abs)
	}
	return r.update(func(reviews map[string]RegEntry) error {
		reviews[id] = RegEntry{Path: abs, State: state}
		return nil
	})
}

// Reviews lists the registered reviews.
func (r *Registry) Reviews() (map[string]RegEntry, error) {
	f, err := os.Open(r.Path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]RegEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := lock(f, false); err != nil {
		return nil, fmt.Errorf("lock %s: %w", r.Path, err)
	}
	return r.decode(f)
}

// Prune removes the pins of closed reviews and of reviews whose file is gone,
// with their entries, and returns the pins removed; dryRun changes nothing.
func (r *Registry) Prune(dryRun bool) ([]string, error) {
	var removed []string
	scan := func(reviews map[string]RegEntry) error {
		ids := make([]string, 0, len(reviews))
		for id := range reviews {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			// An entry no review can have names no pins to delete; it goes.
			if !reviewIDRE.MatchString(id) {
				delete(reviews, id)
				continue
			}
			if e := reviews[id]; e.State == "open" {
				if _, err := os.Stat(e.Path); err == nil {
					continue
				}
			}
			pins, err := r.Repo.Pins(id)
			if err != nil {
				return err
			}
			for _, pin := range pins {
				if !dryRun {
					if err := r.Repo.Unpin(pin); err != nil {
						return err
					}
				}
				removed = append(removed, pin)
			}
			delete(reviews, id)
		}
		return nil
	}
	if dryRun {
		reviews, err := r.Reviews()
		if err != nil {
			return nil, err
		}
		return removed, scan(reviews)
	}
	return removed, r.update(scan)
}

func (r *Registry) decode(f *os.File) (map[string]RegEntry, error) {
	raw, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]RegEntry{}, nil
	}
	var probe map[string]json.RawMessage
	var data registryFile
	if json.Unmarshal(raw, &probe) != nil || firstByte(probe["reviews"]) != '{' || json.Unmarshal(raw, &data) != nil {
		return nil, fmt.Errorf("registry %s is not a review registry: expected a JSON object with a reviews object", r.Path)
	}
	return data.Reviews, nil
}

func firstByte(b []byte) byte {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return 0
	}
	return b[0]
}

// update reads, changes and rewrites the registry in place under an
// exclusive lock held throughout: never by replacing the file, which a
// process locked on the old one would go on writing.
func (r *Registry) update(fn func(reviews map[string]RegEntry) error) error {
	if err := os.MkdirAll(filepath.Dir(r.Path), 0o777); err != nil {
		return err
	}
	f, err := os.OpenFile(r.Path, os.O_RDWR|os.O_CREATE, 0o666)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := lock(f, true); err != nil {
		return fmt.Errorf("lock %s: %w", r.Path, err)
	}
	reviews, err := r.decode(f)
	if err != nil {
		return err
	}
	if err := fn(reviews); err != nil {
		return err
	}
	out, err := MarshalJSON(registryFile{Reviews: reviews})
	if err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Write(append(out, '\n')); err != nil {
		return err
	}
	return f.Close()
}
