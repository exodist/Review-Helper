package review

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/exodist/Review-Helper/go/internal/git"
	"github.com/exodist/Review-Helper/go/internal/share"
)

// The command line (standards/CLI.md).

// A command parses its own options and runs, reading standard input from
// in, writing what it prints to out and its warnings to warn.
type command func(args []string, in io.Reader, out, warn io.Writer) error

var commands = map[string]command{
	"build":     runBuild,
	"close":     runClose,
	"export":    runExport,
	"feedback":  runFeedback,
	"fill":      runFill,
	"import":    runImport,
	"iterate":   runIterate,
	"list":      runList,
	"note":      runNote,
	"prune":     runPrune,
	"published": runPublished,
	"serve":     runServe,
	"sheet":     runSheet,
	"skills":    runSkills,
	"version":   runVersion,
}

// Run runs a command line and returns the exit status. A first argument
// that names no command is a review file to serve. A command's output is
// held until it succeeds, so one that fails prints nothing on standard
// output; serve, which runs until stopped, prints as it goes.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage())
		return 1
	}
	name := args[0]
	run, ok := commands[name]
	if ok {
		args = args[1:]
	} else {
		name, run = "serve", runServe
	}
	out := stdout
	var held bytes.Buffer
	if name != "serve" {
		out = &held
	}
	if err := run(args, stdin, out, stderr); err != nil {
		fmt.Fprintf(stderr, "review-helper %s: %s\n", name, strings.TrimSuffix(err.Error(), "\n"))
		return 1
	}
	if _, err := stdout.Write(held.Bytes()); err != nil {
		return 1
	}
	return 0
}

// readInput reads the named file, or in when the name is "-".
func readInput(name string, in io.Reader) ([]byte, error) {
	if name == "-" {
		return io.ReadAll(in)
	}
	return os.ReadFile(name)
}

func usage() string {
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	return "Usage: review-helper <command> [options] [arguments]\n       review-helper <review-file>\nCommands: " + strings.Join(names, ", ") + "\n"
}

// options binds a command's options to variables. Options may come anywhere
// among the arguments, as --name value or --name=value; "--" ends them, and
// a lone "-" (standard input) is an argument that also ends them.
type options struct {
	values map[string]*string
	lists  map[string]*[]string
	flags  map[string]*bool
	paths  map[string]bool // take bytes as given, not checked as UTF-8
	given  map[string]bool // options the command line set
}

func newOptions() *options {
	return &options{values: map[string]*string{}, lists: map[string]*[]string{}, flags: map[string]*bool{}, paths: map[string]bool{}, given: map[string]bool{}}
}

func (o *options) str(name string, p *string)    { o.values[name] = p }
func (o *options) list(name string, p *[]string) { o.lists[name] = p }
func (o *options) flag(name string, p *bool)     { o.flags[name] = p }
func (o *options) path(name string, p *string)   { o.values[name], o.paths[name] = p, true }

// parse sets the bound variables and returns the other arguments.
func (o *options) parse(args []string) ([]string, error) {
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return append(rest, args[i+1:]...), nil
		case a == "-":
			return append(rest, args[i:]...), nil
		case !strings.HasPrefix(a, "--"):
			rest = append(rest, a)
			continue
		}
		name, value, inline := strings.Cut(a[2:], "=")
		if p, ok := o.flags[name]; ok {
			if inline {
				return nil, fmt.Errorf("option --%s takes no value", name)
			}
			*p = true
			continue
		}
		vp, isValue := o.values[name]
		lp, isList := o.lists[name]
		if !isValue && !isList {
			return nil, fmt.Errorf("unknown option --%s", name)
		}
		if !inline {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("option --%s needs a value", name)
			}
			i++
			value = args[i]
		}
		if !o.paths[name] && !utf8.ValidString(value) {
			return nil, fmt.Errorf("the value of --%s is not valid UTF-8", name)
		}
		o.given[name] = true
		if isValue {
			*vp = value
		} else {
			*lp = append(*lp, value)
		}
	}
	return rest, nil
}

// cwdRepo is the repository holding the current directory.
func cwdRepo() (*git.Repo, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return git.Find(dir)
}

// journalOf is the journal of repo's worktree, warning on warn.
func journalOf(repo *git.Repo, warn io.Writer) (*Journal, error) {
	dir, err := repo.StateDir()
	if err != nil {
		return nil, err
	}
	return &Journal{Path: filepath.Join(dir, "journal.jsonl"), Warn: warn}, nil
}

func runNote(args []string, in io.Reader, out, warn io.Writer) error {
	var flag, kind, path, unit, snippet, sentence, detail string
	o := newOptions()
	for name, p := range map[string]*string{"flag": &flag, "kind": &kind, "path": &path, "unit": &unit, "snippet": &snippet, "sentence": &sentence, "detail": &detail} {
		o.str(name, p)
	}
	rest, err := o.parse(args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(rest, " "))
	}
	repo, err := cwdRepo()
	if err != nil {
		return err
	}
	j, err := journalOf(repo, warn)
	if err != nil {
		return err
	}
	// An option given as "" is set, and rejected as empty.
	given := func(name, s string) Text { return Text{Value: s, Set: o.given[name]} }
	id, err := j.Add(Note{
		Flag: given("flag", flag), Kind: given("kind", kind), Path: given("path", path), Unit: given("unit", unit),
		Snippet: given("snippet", snippet), Sentence: given("sentence", sentence), Detail: given("detail", detail),
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(out, id)
	return nil
}

func runList(args []string, in io.Reader, out, warn io.Writer) error {
	if len(args) > 0 {
		return errors.New("usage: review-helper list")
	}
	repo, err := cwdRepo()
	if err != nil {
		return err
	}
	reviews, err := NewRegistry(repo).Reviews()
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(reviews))
	for id := range reviews {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fmt.Fprintf(out, "%s\t%s\t%s\n", id, reviews[id].State, reviews[id].Path)
	}
	return nil
}

func runPrune(args []string, in io.Reader, out, warn io.Writer) error {
	var dryRun bool
	o := newOptions()
	o.flag("dry-run", &dryRun)
	rest, err := o.parse(args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return errors.New("usage: review-helper prune [--dry-run]")
	}
	repo, err := cwdRepo()
	if err != nil {
		return err
	}
	removed, err := NewRegistry(repo).Prune(dryRun)
	if err != nil {
		return err
	}
	for _, p := range removed {
		fmt.Fprintln(out, p)
	}
	return nil
}

func runClose(args []string, in io.Reader, out, warn io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: review-helper close FILE")
	}
	repo, err := git.Find(args[0])
	if err != nil {
		return err
	}
	d, err := OpenDB(args[0])
	if err != nil {
		return err
	}
	defer d.Close()
	return NewRegistry(repo).Close(d, args[0])
}

func runPublished(args []string, in io.Reader, out, warn io.Writer) error {
	if len(args) != 2 {
		return errors.New("usage: review-helper published FILE ID")
	}
	id, ok := parsePositive(args[1])
	if !ok {
		return fmt.Errorf("feedback ID must be a positive integer, got %q", args[1])
	}
	d, err := OpenDB(args[0])
	if err != nil {
		return err
	}
	defer d.Close()
	return d.SetPublished(id)
}

func runSkills(args []string, in io.Reader, out, warn io.Writer) error {
	if len(args) != 1 || args[0] != "path" {
		return errors.New("usage: review-helper skills path")
	}
	dir, err := SkillsDir()
	if err != nil {
		return err
	}
	fmt.Fprintln(out, dir)
	return nil
}

func runVersion(args []string, in io.Reader, out, warn io.Writer) error {
	if len(args) > 0 {
		return errors.New("usage: review-helper version")
	}
	fmt.Fprintf(out, "standard %d\nimplementation %s %s\n", StandardVersion, Name, Version)
	return nil
}

// SkillsDir writes the embedded skills, once, to a cache directory named by
// their content and returns it: agents link skill directories from disk.
// The copy is written beside the cache directory and renamed into place,
// so a run never sees another's half-written copy.
func SkillsDir() (string, error) {
	skills, err := fs.Sub(share.FS, "skills")
	if err != nil {
		return "", err
	}
	var files []string
	err = fs.WalkDir(skills, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, p)
		}
		return err
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, p := range files {
		b, err := fs.ReadFile(skills, p)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(b))
		h.Write(b)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	parent := filepath.Join(cache, "review-helper-go")
	root := filepath.Join(parent, Version+"-"+hex.EncodeToString(h.Sum(nil))[:16])
	dir := filepath.Join(root, "skills")
	done := filepath.Join(root, "complete")
	if _, err := os.Stat(done); err == nil {
		return dir, nil
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(parent, ".skills-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	for _, p := range files {
		b, err := fs.ReadFile(skills, p)
		if err != nil {
			return "", err
		}
		dest := filepath.Join(tmp, "skills", filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(dest, b, 0o644); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, "complete"), nil, 0o644); err != nil {
		return "", err
	}
	// MkdirTemp makes the directory 0700; the copy is for agents to read.
	if err := os.Chmod(tmp, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, root); err != nil {
		if _, statErr := os.Stat(done); statErr == nil {
			return dir, nil // another run put its copy in place first
		}
		return "", err
	}
	return dir, nil
}
