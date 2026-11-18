package review

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/exodist/Review-Helper/go/internal/git"
)

// Registry is the repository's shared list of reviews (standards/FORMAT.md,
// "Registry").
type Registry struct {
	Repo *git.Repo
	Path string
}

// RegEntry is one review's registry entry.
type RegEntry struct {
	Path  string `json:"path"`
	State string `json:"state"`
}

// NewRegistry is the registry of repo.
func NewRegistry(repo *git.Repo) *Registry {
	return &Registry{Repo: repo, Path: filepath.Join(repo.SharedStateDir(), "registry.json")}
}

var reviewIDRE = regexp.MustCompile(`\A[0-9A-Za-z][0-9A-Za-z_-]*\z`)

func validID(id string) bool { return reviewIDRE.MatchString(id) }

// Close marks a review closed in its file and the registry.
func (r *Registry) Close(d *DB, path string) error {
	if err := d.SetState("closed"); err != nil {
		return err
	}
	rv, err := d.Review()
	if err != nil {
		return err
	}
	return r.Register(Str(rv["id"]), path, "closed")
}

// Register records a review's file and state.
func (r *Registry) Register(id, path, state string) error {
	if !validID(id) {
		return errorf("Invalid review id '%s'", id)
	}
	if state != "open" && state != "closed" {
		return errorf("Review state must be 'open' or 'closed'")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	return r.update(func(reviews map[string]any) error {
		reviews[id] = map[string]any{"path": abs, "state": state}
		return nil
	})
}

// Reviews lists the registered reviews.
func (r *Registry) Reviews() (map[string]RegEntry, error) {
	stored, err := r.stored()
	if err != nil {
		return nil, err
	}
	out := map[string]RegEntry{}
	for id, v := range stored {
		m, _ := v.(map[string]any)
		out[id] = RegEntry{Path: Str(m["path"]), State: Str(m["state"])}
	}
	return out, nil
}

// Prune removes the pins of closed reviews and of reviews whose file is gone,
// with their entries, and returns the pins removed.
func (r *Registry) Prune(dryRun bool) ([]string, error) {
	var removed []string
	scan := func(reviews map[string]any) error {
		ids := make([]string, 0, len(reviews))
		for id := range reviews {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			// An entry no review can have names no pins to delete; it goes.
			if !validID(id) {
				if !dryRun {
					delete(reviews, id)
				}
				continue
			}
			m, _ := reviews[id].(map[string]any)
			if Str(m["state"]) == "open" {
				if _, err := os.Stat(Str(m["path"])); err == nil {
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
			if !dryRun {
				delete(reviews, id)
			}
		}
		return nil
	}
	if dryRun {
		stored, err := r.stored()
		if err != nil {
			return nil, err
		}
		return removed, scan(stored)
	}
	return removed, r.update(scan)
}

func (r *Registry) stored() (map[string]any, error) {
	f, err := os.Open(r.Path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("Cannot open '%s': %v", r.Path, err)
	}
	defer f.Close()
	if err := lockFile(f, false); err != nil {
		return nil, fmt.Errorf("Cannot lock '%s': %v", r.Path, err)
	}
	data, err := r.decode(f)
	if err != nil {
		return nil, err
	}
	return data["reviews"].(map[string]any), nil
}

func (r *Registry) decode(f *os.File) (map[string]any, error) {
	raw, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	if !nonSpace.Match(raw) {
		return map[string]any{"reviews": map[string]any{}}, nil
	}
	v, err := JSONDecode(raw)
	if err == nil {
		if m, ok := v.(map[string]any); ok {
			if _, ok := m["reviews"].(map[string]any); ok {
				return m, nil
			}
		}
	}
	return nil, fmt.Errorf("Registry '%s' is not a review registry: expected a JSON object with a 'reviews' object", r.Path)
}

// update reads, changes and rewrites the registry in place under an
// exclusive lock held for the whole update.
func (r *Registry) update(fn func(reviews map[string]any) error) error {
	if err := os.MkdirAll(filepath.Dir(r.Path), 0o777); err != nil {
		return err
	}
	f, err := os.OpenFile(r.Path, os.O_RDWR|os.O_CREATE, 0o666)
	if err != nil {
		return fmt.Errorf("Cannot open '%s': %v", r.Path, err)
	}
	defer f.Close()
	if err := lockFile(f, true); err != nil {
		return fmt.Errorf("Cannot lock '%s': %v", r.Path, err)
	}
	data, err := r.decode(f)
	if err != nil {
		return err
	}
	if err := fn(data["reviews"].(map[string]any)); err != nil {
		return err
	}
	out, err := JSONEncode(data)
	if err != nil {
		return err
	}
	if _, err := f.Seek(0, 0); err != nil {
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
