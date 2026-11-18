package review

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/exodist/Review-Helper/go/internal/git"
	"github.com/exodist/Review-Helper/go/internal/share"
)

// Opts are a command's parsed options: each value list in order given; a
// flag holds "1".
type Opts map[string][]string

// Get is the last value of an option, or "".
func (o Opts) Get(name string) string {
	v := o[name]
	if len(v) == 0 {
		return ""
	}
	return v[len(v)-1]
}

// Has reports whether an option was given.
func (o Opts) Has(name string) bool { return len(o[name]) > 0 }

// optSpec is one option: whether it takes a value.
type optSpec struct {
	name  string
	value bool
}

type command struct {
	opts []optSpec
	run  func(o Opts, args []string, out io.Writer) error
}

var commands = map[string]command{}

func register(name string, opts []optSpec, run func(Opts, []string, io.Writer) error) {
	commands[name] = command{opts: opts, run: run}
}

func val(name string) optSpec  { return optSpec{name, true} }
func flag(name string) optSpec { return optSpec{name, false} }

// Run runs the command line and returns the exit status.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage())
		return 1
	}
	name := args[0]
	cmd, ok := commands[name]
	if ok {
		args = args[1:]
	} else {
		// Anything that is not a command is a review file to serve.
		name, cmd = "serve", commands["serve"]
	}
	if err := runCommand(cmd, args, stdout); err != nil {
		msg := err.Error()
		if !strings.HasSuffix(msg, "\n") {
			msg += "\n"
		}
		fmt.Fprintf(stderr, "review-helper %s: %s", name, msg)
		return 1
	}
	return 0
}

func usage() string {
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	return "Usage: review-helper <command> [options] [arguments]\n       review-helper <review-file>\nCommands: " + strings.Join(names, ", ") + "\n"
}

// runCommand parses options (anywhere among the arguments, until "--" or
// a lone "-", which stays an argument) and runs the command. Output is
// buffered, so a failing command prints nothing on standard output.
func runCommand(cmd command, args []string, stdout io.Writer) error {
	opts := Opts{}
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i+1:]...)
			break
		}
		if a == "-" {
			rest = append(rest, args[i:]...)
			break
		}
		if !strings.HasPrefix(a, "--") {
			rest = append(rest, a)
			continue
		}
		name, value, hasValue := strings.Cut(a[2:], "=")
		var spec *optSpec
		for j := range cmd.opts {
			if cmd.opts[j].name == name {
				spec = &cmd.opts[j]
			}
		}
		if spec == nil {
			return errorf("Unknown option '--%s'", name)
		}
		if !spec.value {
			if hasValue {
				return errorf("Option '--%s' takes no value", name)
			}
			opts[name] = append(opts[name], "1")
			continue
		}
		if !hasValue {
			if i+1 >= len(args) {
				return errorf("Option '--%s' needs a value", name)
			}
			i++
			value = args[i]
		}
		if !utf8.ValidString(value) && name != "file" && name != "out" && name != "port-command" {
			return errorf("The value of --%s is not valid UTF-8", name)
		}
		opts[name] = append(opts[name], value)
	}
	var buf strings.Builder
	if err := cmd.run(opts, rest, &buf); err != nil {
		return err
	}
	_, err := io.WriteString(stdout, buf.String())
	return err
}

// cwdRepo is the repository holding the current directory.
func cwdRepo() (*git.Repo, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return git.Find(dir)
}

var positiveInt = regexp.MustCompile(`^[1-9][0-9]*$`)

func init() {
	register("note", []optSpec{val("flag"), val("kind"), val("path"), val("unit"), val("snippet"), val("sentence"), val("detail")}, func(o Opts, args []string, out io.Writer) error {
		if len(args) > 0 {
			return errorf("Unexpected arguments: %s", strings.Join(args, " "))
		}
		repo, err := cwdRepo()
		if err != nil {
			return err
		}
		dir, err := repo.StateDir()
		if err != nil {
			return err
		}
		fields := map[string]any{}
		for _, f := range []string{"flag", "kind", "path", "unit", "snippet", "sentence", "detail"} {
			if o.Has(f) {
				fields[f] = o.Get(f)
			}
		}
		id, err := (&Journal{Path: filepath.Join(dir, "journal.jsonl")}).Add(fields)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, id)
		return nil
	})

	register("list", nil, func(o Opts, args []string, out io.Writer) error {
		if len(args) > 0 {
			return errorf("Usage: review-helper list")
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
	})

	register("prune", []optSpec{flag("dry-run")}, func(o Opts, args []string, out io.Writer) error {
		if len(args) > 0 {
			return errorf("Usage: review-helper prune [--dry-run]")
		}
		repo, err := cwdRepo()
		if err != nil {
			return err
		}
		removed, err := NewRegistry(repo).Prune(o.Has("dry-run"))
		if err != nil {
			return err
		}
		for _, p := range removed {
			fmt.Fprintln(out, p)
		}
		return nil
	})

	register("close", nil, func(o Opts, args []string, out io.Writer) error {
		if len(args) != 1 {
			return errorf("Usage: review-helper close FILE")
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
	})

	register("published", nil, func(o Opts, args []string, out io.Writer) error {
		if len(args) != 2 {
			return errorf("Usage: review-helper published FILE ID")
		}
		if !positiveInt.MatchString(args[1]) {
			return errorf("Feedback ID must be a positive integer, got '%s'", args[1])
		}
		d, err := OpenDB(args[0])
		if err != nil {
			return err
		}
		defer d.Close()
		return d.SetPublished(Int(args[1]))
	})

	register("skills", nil, func(o Opts, args []string, out io.Writer) error {
		if len(args) != 1 || args[0] != "path" {
			return errorf("Usage: review-helper skills path")
		}
		dir, err := SkillsDir()
		if err != nil {
			return err
		}
		fmt.Fprintln(out, dir)
		return nil
	})

	register("version", nil, func(o Opts, args []string, out io.Writer) error {
		if len(args) > 0 {
			return errorf("Usage: review-helper version")
		}
		fmt.Fprintf(out, "standard %d\nimplementation %s %s\n", StandardVersion, Name, Version)
		return nil
	})
}

// SkillsDir writes the embedded skills to a cache directory named by their
// content, once, and returns it: agents link skill directories from disk.
func SkillsDir() (string, error) {
	skills, err := fs.Sub(share.FS, "skills")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	var files []string
	err = fs.WalkDir(skills, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		files = append(files, p)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	for _, p := range files {
		b, err := fs.ReadFile(skills, p)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(b))
		h.Write(b)
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(base, "review-helper-go", Version+"-"+hex.EncodeToString(h.Sum(nil))[:16])
	dir := filepath.Join(root, "skills")
	if _, err := os.Stat(filepath.Join(root, "complete")); err == nil {
		return dir, nil
	}
	for _, p := range files {
		b, _ := fs.ReadFile(skills, p)
		dest := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(dest, b, 0o644); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(filepath.Join(root, "complete"), nil, 0o644); err != nil {
		return "", err
	}
	return dir, nil
}
