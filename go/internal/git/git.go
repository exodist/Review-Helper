// Package git reads a review's repository: every git command, ref name and
// option the backend uses is here. Runs isolate git from the user's and the
// system's configuration (except User) and from pathspec-mode variables, and
// diffs follow the format's diff convention (standards/FORMAT.md).
package git

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// DiffOptions are the options of every diff anchors count from.
var DiffOptions = []string{
	"--no-ext-diff", "--no-color", "--no-textconv", "--indent-heuristic",
	"--diff-algorithm=histogram", "--find-renames", "-U3",
	"--src-prefix=a/", "--dst-prefix=b/",
}

var statusMap = map[byte]string{'A': "A", 'D': "D", 'M': "M", 'R': "R", 'T': "M", 'C': "A"}

// Repo is a worktree of a git repository.
type Repo struct {
	Root      string // the worktree's top directory
	GitDir    string
	CommonDir string

	emptyOnce sync.Once
	empty     string
	remerge   *bool
}

// Change is one file that differs between two revisions.
type Change struct {
	Path      string
	OldPath   string // renames only
	Status    string // A, D, M or R
	Added     int
	Removed   int
	Binary    bool
	Submodule bool // either side is another repository's entry
}

// RevisionInfo describes a commit.
type RevisionInfo struct {
	Revision string
	Parent   string // first parent; "" for a root commit
	Subject  string
	Message  string
	Author   string
	Authored int64
}

// Rev is a revision with its parents.
type Rev struct {
	Revision string
	Parents  []string
}

// Find returns the repository holding path, a file (which need not exist) or
// a directory.
func Find(path string) (*Repo, error) {
	if path == "" {
		return nil, errors.New("no path given")
	}
	dir := path
	if st, err := os.Stat(path); err != nil || !st.IsDir() {
		dir = filepath.Dir(path)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("no such directory: %s", dir)
	}
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	if dir, err = filepath.Abs(dir); err != nil {
		return nil, err
	}
	out, err := gitIn(dir, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir", "--is-inside-git-dir")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 3 {
		return nil, fmt.Errorf("not inside a git worktree: %s", dir)
	}
	gitDir, commonDir := lines[0], lines[1]
	var root string
	if lines[2] == "true" {
		if root, err = rootFromGitDir(gitDir, commonDir); err != nil {
			return nil, err
		}
	} else {
		top, err := gitIn(dir, "rev-parse", "--show-toplevel")
		if err != nil {
			return nil, err
		}
		root = strings.SplitN(top, "\n", 2)[0]
	}
	return &Repo{Root: root, GitDir: gitDir, CommonDir: commonDir}, nil
}

// StateDir is the backend's directory for this worktree.
func (r *Repo) StateDir() (string, error) { return r.gitPath("review-helper") }

// SharedStateDir is the backend's directory shared by every worktree.
func (r *Repo) SharedStateDir() string { return filepath.Join(r.CommonDir, "review-helper") }

// IsInternalPath reports whether the absolute path is inside git's own
// directories, strictly.
func (r *Repo) IsInternalPath(path string) bool {
	for _, dir := range []string{r.GitDir, r.CommonDir} {
		if Within(dir, path) {
			return true
		}
	}
	return false
}

// Within reports whether path is strictly inside dir.
func Within(dir, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Resolve returns the full id of the commit name names.
func (r *Repo) Resolve(name string) (string, error) {
	out, code, errText, err := r.run(nil, "rev-parse", "--verify", "--quiet", "--end-of-options", name+"^{commit}")
	if err != nil {
		return "", err
	}
	if code == 1 {
		return "", fmt.Errorf("unknown revision '%s'", name)
	}
	if code != 0 {
		return "", fmt.Errorf("git rev-parse failed: %s", errText)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// EmptyRevision is the empty tree's id.
func (r *Repo) EmptyRevision() (string, error) {
	var err error
	r.emptyOnce.Do(func() {
		var out string
		out, err = r.git("hash-object", "-t", "tree", "/dev/null")
		r.empty = strings.TrimRight(out, "\n")
	})
	return r.empty, err
}

// HasRevision reports whether the commit is in the repository.
func (r *Repo) HasRevision(sha string) (bool, error) {
	return r.predicate("rev-parse", "--verify", "--quiet", "--end-of-options", sha+"^{commit}")
}

// Revisions lists the commits in head but not base, oldest first, each after
// its parents.
func (r *Repo) Revisions(base, head string) ([]Rev, error) {
	out, err := r.git("rev-list", "--reverse", "--topo-order", "--parents", "--end-of-options", base+".."+head)
	if err != nil {
		return nil, err
	}
	var revs []Rev
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, " ")
		revs = append(revs, Rev{Revision: f[0], Parents: f[1:]})
	}
	return revs, nil
}

// RevisionName is the branch or tag name rev stands for, or "".
func (r *Repo) RevisionName(rev string) (string, error) {
	if strings.HasPrefix(rev, "-") {
		return "", nil
	}
	out, err := r.git("rev-parse", "--abbrev-ref", rev)
	if err != nil {
		return "", err
	}
	name := strings.SplitN(out, "\n", 2)[0]
	if name == "HEAD" {
		return "", nil
	}
	return name, nil
}

// Info describes a commit.
func (r *Repo) Info(sha string) (*RevisionInfo, error) {
	out, err := r.git("log", "-1", "--no-show-signature", "--format=%H%x00%P%x00%an%x20<%ae>%x00%at%x00%s%x00%B", "--end-of-options", sha)
	if err != nil {
		return nil, err
	}
	f := strings.SplitN(out, "\x00", 6)
	if len(f) < 6 {
		return nil, fmt.Errorf("unexpected git log output for %s", sha)
	}
	authored, _ := strconv.ParseInt(f[3], 10, 64)
	msg := strings.TrimRight(f[5], "\n") + "\n"
	return &RevisionInfo{
		Revision: f[0],
		Parent:   strings.SplitN(f[1], " ", 2)[0],
		Author:   f[2],
		Authored: authored,
		Subject:  f[4],
		Message:  msg,
	}, nil
}

// Parents lists the commit's parents, first parent first.
func (r *Repo) Parents(sha string) ([]string, error) {
	out, err := r.git("rev-list", "--parents", "-n", "1", "--end-of-options", sha)
	if err != nil {
		return nil, err
	}
	f := strings.Fields(out)
	if len(f) == 0 {
		return nil, nil
	}
	return f[1:], nil
}

func (r *Repo) parentOrEmpty(sha string) (string, error) {
	p, err := r.Parents(sha)
	if err != nil {
		return "", err
	}
	if len(p) > 0 {
		return p[0], nil
	}
	return r.EmptyRevision()
}

// ChangeID is a stable id of the commit's change, "" when it changes nothing.
func (r *Repo) ChangeID(sha string) (string, error) {
	parent, err := r.parentOrEmpty(sha)
	if err != nil {
		return "", err
	}
	diff, err := r.gitBytes(append(append([]string{"diff"}, DiffOptions...), parent, sha)...)
	if err != nil {
		return "", err
	}
	if len(diff) == 0 {
		return "", nil
	}
	out, code, errText, err := r.run(diff, "patch-id", "--stable")
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("git patch-id failed: %s", errText)
	}
	return strings.SplitN(string(out), " ", 2)[0], nil
}

// RangeChanges lists the files that differ between from and to, limited to
// the path specs.
func (r *Repo) RangeChanges(from, to string, paths []string) ([]*Change, error) {
	rng := append([]string{from, to, "--"}, paths...)
	raw, err := r.gitBytes(append([]string{"diff-tree", "-r", "-z", "-M", "--raw"}, rng...)...)
	if err != nil {
		return nil, err
	}
	changes, err := parseRaw(raw)
	if err != nil {
		return nil, err
	}
	numstat, err := r.gitBytes(append([]string{"diff-tree", "-r", "-z", "-M", "--numstat"}, rng...)...)
	if err != nil {
		return nil, err
	}
	counts := parseNumstat(numstat)
	for _, c := range changes {
		n, ok := counts[c.Path]
		if !ok {
			return nil, fmt.Errorf("no line counts for '%s'", c.Path)
		}
		c.Added, c.Removed, c.Binary = n.Added, n.Removed, n.Binary
	}
	return changes, nil
}

// RangeDiff is one file's diff between two revisions, its path literal.
func (r *Repo) RangeDiff(from, to, path, oldPath string) ([]byte, error) {
	args := append(append([]string{"diff"}, DiffOptions...), from, to, "--")
	return r.gitBytes(append(args, filePathspecs(path, oldPath)...)...)
}

// ResolutionChanges lists a merge's own changes (its conflict resolutions),
// empty for a clean merge, a non-merge, or a git without --remerge-diff.
func (r *Repo) ResolutionChanges(sha string, paths []string) ([]*Change, error) {
	ok, err := r.hasRemergeDiff()
	if err != nil || !ok {
		return nil, err
	}
	parents, err := r.Parents(sha)
	if err != nil || len(parents) < 2 {
		return nil, err
	}
	args := append([]string{"show", "--no-show-signature", "--no-abbrev", "--format=", "--remerge-diff", "-r", "-z", "-M", "--raw", "--end-of-options", sha, "--"}, paths...)
	raw, err := r.gitBytes(args...)
	if err != nil {
		return nil, err
	}
	return parseRaw(raw)
}

// ResolutionDiff is one file's diff of a merge's own changes.
func (r *Repo) ResolutionDiff(sha, path, oldPath string) ([]byte, error) {
	args := append([]string{"show", "--no-show-signature", "--format=", "--remerge-diff", "-r", "-p"}, DiffOptions...)
	args = append(append(args, "--end-of-options", sha, "--"), filePathspecs(path, oldPath)...)
	return r.gitBytes(args...)
}

// File is the file's content at sha, or nil when no file is there.
func (r *Repo) File(sha, path string) ([]byte, error) {
	out, code, errText, err := r.run(nil, "cat-file", "blob", sha+":"+path)
	if err != nil {
		return nil, err
	}
	if code == 0 {
		if out == nil {
			out = []byte{}
		}
		return out, nil
	}
	blob, err := r.blob(sha, path)
	if err != nil {
		return nil, err
	}
	if blob == "" {
		return nil, nil
	}
	return nil, fmt.Errorf("git cat-file failed: %s", errText)
}

// FileID is an id of the content at path in sha, "" when nothing is there.
func (r *Repo) FileID(sha, path string) (string, error) {
	out, code, errText, err := r.run(nil, "rev-parse", "--verify", "--quiet", "--end-of-options", sha+":"+path)
	if err != nil {
		return "", err
	}
	if code == 1 {
		return "", nil
	}
	if code != 0 {
		return "", fmt.Errorf("git rev-parse failed: %s", errText)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// PinName is the ref keeping iteration number of a review.
func PinName(reviewID string, number int) string {
	return fmt.Sprintf("refs/review-helper/%s/%d", reviewID, number)
}

// Pin creates or moves the ref.
func (r *Repo) Pin(ref, sha string) error {
	_, err := r.git("update-ref", ref, sha)
	return err
}

// Unpin deletes the ref.
func (r *Repo) Unpin(ref string) error {
	_, err := r.git("update-ref", "-d", ref)
	return err
}

// Pins lists every ref of a review.
func (r *Repo) Pins(reviewID string) ([]string, error) {
	prefix := "refs/review-helper/" + reviewID + "/"
	out, err := r.git("for-each-ref", "--format=%(refname)", prefix)
	if err != nil {
		return nil, err
	}
	var refs []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, prefix) {
			refs = append(refs, l)
		}
	}
	return refs, nil
}

// User is the user's identity in this repository, "Name <email>", either
// part alone, or "". Unlike every other run it reads the user's own
// configuration, where their name and email live.
func (r *Repo) User() string {
	var parts []string
	for _, key := range []string{"name", "email"} {
		cmd := exec.Command("git", "-C", r.Root, "config", "--get", "user."+key)
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		v := strings.TrimSpace(string(out))
		if v == "" {
			continue
		}
		if key == "email" {
			v = "<" + v + ">"
		}
		parts = append(parts, v)
	}
	return strings.Join(parts, " ")
}

func (r *Repo) git(args ...string) (string, error) {
	out, err := r.gitBytes(args...)
	return string(out), err
}

func (r *Repo) gitBytes(args ...string) ([]byte, error) {
	out, code, errText, err := r.run(nil, args...)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("git %s failed: %s", strings.Join(args, " "), errText)
	}
	return out, nil
}

func (r *Repo) gitPath(name string) (string, error) {
	out, err := r.git("rev-parse", "--path-format=absolute", "--git-path", name)
	return strings.TrimRight(out, "\n"), err
}

func filePathspecs(path, oldPath string) []string {
	if oldPath != "" {
		return []string{":(literal)" + oldPath, ":(literal)" + path}
	}
	return []string{":(literal)" + path}
}

var versionRE = regexp.MustCompile(`(\d+)\.(\d+)`)

// git 2.36 added --remerge-diff; show, not diff-tree, which dies with a BUG
// on it before 2.47.
func (r *Repo) hasRemergeDiff() (bool, error) {
	if r.remerge != nil {
		return *r.remerge, nil
	}
	out, err := r.git("version")
	if err != nil {
		return false, err
	}
	ok := false
	if m := versionRE.FindStringSubmatch(out); m != nil {
		major, _ := strconv.Atoi(m[1])
		minor, _ := strconv.Atoi(m[2])
		ok = major > 2 || (major == 2 && minor >= 36)
	}
	r.remerge = &ok
	return ok, nil
}

// blob is the blob id at path in sha, "" when absent or not a file (a
// directory, or a submodule's commit).
func (r *Repo) blob(sha, path string) (string, error) {
	id, err := r.FileID(sha, path)
	if err != nil || id == "" {
		return "", err
	}
	out, code, _, err := r.run(nil, "cat-file", "-t", id)
	if err != nil {
		return "", err
	}
	if code != 0 || string(out) != "blob\n" {
		return "", nil
	}
	return id, nil
}

func gitIn(dir string, args ...string) (string, error) {
	out, code, errText, err := run(dir, nil, args...)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("not inside a git worktree: %s\n%s", dir, errText)
	}
	return string(out), nil
}

func rootFromGitDir(gitDir, commonDir string) (string, error) {
	// A git directory that names its worktree (core.worktree, as a
	// submodule's does) answers this; a repository's own .git and a linked
	// worktree's git directory do not.
	out, code, _, err := run(gitDir, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	root := strings.TrimRight(string(out), "\n")
	if code != 0 || root == "" {
		if gitDir == commonDir {
			root = filepath.Dir(gitDir)
		} else {
			data, err := os.ReadFile(filepath.Join(gitDir, "gitdir"))
			if err != nil {
				return "", fmt.Errorf("cannot find the worktree of %s: %v", gitDir, err)
			}
			dotGit := strings.SplitN(string(data), "\n", 2)[0]
			// Relative when the worktree was added with --relative-paths.
			if !filepath.IsAbs(dotGit) {
				dotGit = filepath.Join(gitDir, dotGit)
			}
			root = filepath.Dir(filepath.Clean(dotGit))
			if st, err := os.Stat(root); err == nil && st.IsDir() {
				if real, err := filepath.EvalSymlinks(root); err == nil {
					root = real
				}
			}
		}
	}
	check, code := "", 1
	if st, err := os.Stat(root); err == nil && st.IsDir() {
		out, c, _, err := run(root, nil, "rev-parse", "--path-format=absolute", "--git-dir")
		if err != nil {
			return "", err
		}
		check, code = strings.TrimRight(string(out), "\n"), c
	}
	if code != 0 || check != gitDir {
		return "", fmt.Errorf("cannot find the worktree of %s", gitDir)
	}
	return root, nil
}

func parseRaw(raw []byte) ([]*Change, error) {
	tokens := strings.Split(string(raw), "\x00")
	if len(tokens) > 0 && tokens[len(tokens)-1] == "" {
		tokens = tokens[:len(tokens)-1]
	}
	var changes []*Change
	for i := 0; i < len(tokens); {
		head := tokens[i]
		i++
		if !strings.HasPrefix(head, ":") {
			return nil, fmt.Errorf("unexpected diff line '%s'", head)
		}
		f := strings.Split(head[1:], " ")
		if len(f) < 5 || f[4] == "" {
			return nil, fmt.Errorf("unexpected diff line '%s'", head)
		}
		code := f[4][0]
		status, ok := statusMap[code]
		if !ok {
			return nil, fmt.Errorf("unexpected diff status '%c'", code)
		}
		c := &Change{Status: status, Submodule: f[0] == "160000" || f[1] == "160000"}
		if code == 'R' || code == 'C' {
			c.OldPath = tokens[i]
			i++
		}
		if i >= len(tokens) {
			return nil, fmt.Errorf("truncated diff output")
		}
		c.Path = tokens[i]
		i++
		if c.Status != "R" {
			c.OldPath = ""
		}
		changes = append(changes, c)
	}
	return changes, nil
}

type count struct {
	Added, Removed int
	Binary         bool
}

func parseNumstat(numstat []byte) map[string]count {
	counts := map[string]count{}
	tokens := strings.Split(string(numstat), "\x00")
	for i := 0; i < len(tokens); {
		t := tokens[i]
		i++
		if t == "" {
			continue
		}
		f := strings.SplitN(t, "\t", 3)
		if len(f) < 3 {
			continue
		}
		path := f[2]
		if path == "" {
			i++ // old path of a rename
			if i < len(tokens) {
				path = tokens[i]
			}
			i++
		}
		var n count
		if f[0] == "-" {
			n.Binary = true
		} else {
			n.Added, _ = strconv.Atoi(f[0])
			n.Removed, _ = strconv.Atoi(f[1])
		}
		counts[path] = n
	}
	return counts
}

func (r *Repo) predicate(args ...string) (bool, error) {
	_, code, errText, err := r.run(nil, args...)
	if err != nil {
		return false, err
	}
	switch code {
	case 0:
		return true, nil
	case 1:
		return false, nil
	}
	return false, fmt.Errorf("git %s failed: %s", strings.Join(args, " "), errText)
}

func (r *Repo) run(input []byte, args ...string) ([]byte, int, string, error) {
	return run(r.Root, input, args...)
}

// run runs git in dir, isolated from the user's and system's configuration,
// and returns its output, exit status and error output.
func run(dir string, input []byte, args ...string) ([]byte, int, string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	env := []string{}
	for _, e := range os.Environ() {
		k := strings.SplitN(e, "=", 2)[0]
		switch k {
		case "GIT_CONFIG_NOSYSTEM", "GIT_CONFIG_GLOBAL", "GIT_LITERAL_PATHSPECS", "GIT_GLOB_PATHSPECS", "GIT_NOGLOB_PATHSPECS", "GIT_ICASE_PATHSPECS":
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return nil, 0, "", fmt.Errorf("exec git failed: %v", err)
		}
		code = ee.ExitCode()
		if code < 0 {
			code = 255
		}
	}
	return out.Bytes(), code, errBuf.String(), nil
}
