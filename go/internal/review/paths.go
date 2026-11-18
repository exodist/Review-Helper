package review

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/exodist/Review-Helper/go/internal/git"
)

// Where review files may go: inside the worktree or the VCS's own
// directories (standards/FORMAT.md, "Repository conventions").

// absReviewPath is path made absolute, its directory's symbolic links
// resolved when the directory exists.
func absReviewPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	dir, base := filepath.Split(abs)
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(real, base), nil
		}
	}
	return abs, nil
}

// checkReviewLocation refuses a review file outside both the worktree and
// the VCS's own directories.
func checkReviewLocation(repo *git.Repo, path string) error {
	if repo.IsInternalPath(path) || git.Within(repo.Root, path) {
		return nil
	}
	return fmt.Errorf("review file '%s' is not inside the worktree '%s'", path, repo.Root)
}
