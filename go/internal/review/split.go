package review

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// File kinds (standards/FORMAT.md, "File kind") and the layout of an
// agent's split of a file into sections.

var kindByExt = map[string]string{}

func init() {
	add := func(kind string, exts ...string) {
		for _, e := range exts {
			kindByExt[e] = kind
		}
	}
	add("perl", "pm", "pl", "t", "psgi", "pod")
	add("javascript", "js", "mjs", "cjs", "jsx")
	add("typescript", "ts", "mts", "cts", "tsx")
	add("html", "html", "htm")
	add("xml", "xml", "xsd", "xsl", "xslt")
	add("markdown", "md", "markdown")
	add("shell", "sh", "bash", "zsh", "ksh", "bashrc", "zshrc", "bash_profile")
	add("yaml", "yml", "yaml")
	add("ini", "ini", "cfg")
	add("python", "py", "pyi")
	add("ruby", "rb", "rake", "gemspec")
	add("c", "c", "h")
	add("cpp", "cpp", "cc", "cxx", "hpp", "hh", "hxx")
	add("diff", "diff", "patch")
	add("image", "png", "jpg", "jpeg", "gif", "webp", "bmp", "ico")
	for _, k := range []string{"css", "sql", "json", "toml", "go", "java"} {
		kindByExt[k] = k
	}
	kindByExt["rs"] = "rust"
	kindByExt["mk"] = "makefile"
}

var kindByName = map[string]string{"makefile": "makefile", "gnumakefile": "makefile", "dockerfile": "dockerfile"}

var kindByInterpreter = map[string]string{
	"perl": "perl", "sh": "shell", "bash": "shell", "zsh": "shell", "ksh": "shell", "dash": "shell", "ash": "shell",
	"node": "javascript", "nodejs": "javascript", "python": "python", "ruby": "ruby",
}

// KindFor is a file's kind from its path, or its #! line, else text.
func KindFor(p, firstLine string) string {
	name := path.Base(p)
	if i := strings.LastIndex(name, "."); i >= 0 && i < len(name)-1 {
		if k, ok := kindByExt[strings.ToLower(name[i+1:])]; ok {
			return k
		}
	}
	if k, ok := kindByName[strings.ToLower(name)]; ok {
		return k
	}
	if k := kindByInterpreterLine(firstLine); k != "" {
		return k
	}
	return "text"
}

var shebang = regexp.MustCompile(`^#!\s*(\S+)(.*)`)
var versionTail = regexp.MustCompile(`[\d.]+$`)

// kindByInterpreterLine is the kind a #! line names: its interpreter, or the
// command env runs, less any version number (python3, perl5.36).
func kindByInterpreterLine(line string) string {
	m := shebang.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	program, rest := m[1], m[2]
	if program == "env" || strings.HasSuffix(program, "/env") {
		program = ""
		for _, w := range strings.Fields(rest) {
			if !strings.HasPrefix(w, "-") && !strings.Contains(w, "=") {
				program = w
				break
			}
		}
		if program == "" {
			return ""
		}
	}
	program = program[strings.LastIndex(program, "/")+1:]
	program = versionTail.ReplaceAllString(program, "")
	return kindByInterpreter[program]
}

// SplitLines splits text into lines, without a last empty one.
func SplitLines(text string) []string {
	if text == "" {
		return []string{}
	}
	lines := strings.Split(text, "\n")
	if strings.HasSuffix(text, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// Unit is one section of an agent's split, as assembled.
type Unit struct {
	Name      string
	Package   *string
	Ranges    [][2]int
	DocRanges [][2]int
	OldRanges [][2]int
	Deleted   bool
}

// Assemble lays out an agent's units as the file's sections. A run of lines
// in no unit must be all blank: it joins the section holding the line
// before it (the line after it at the top of the file), in code or
// documentation like that line. Units must not share lines. Deleted units
// (no lines) are kept, after the live ones are placed; no units: the file
// is left whole.
func Assemble(units []*Unit, lines []string) ([]*Unit, error) {
	if len(units) == 0 {
		return []*Unit{}, nil
	}
	sections := make([]*Unit, len(units))
	for i, u := range units {
		c := *u
		sections[i] = &c
	}
	owner := make([]*Unit, len(lines)+2)
	code := make([]bool, len(lines)+2)
	for _, s := range sections {
		for _, l := range rangeLines(s.Ranges) {
			if l < len(owner) {
				owner[l], code[l] = s, true
			}
		}
		for _, l := range rangeLines(s.DocRanges) {
			if l < len(owner) {
				owner[l] = s
			}
		}
	}
	var free []int
	for l := 1; l <= len(lines); l++ {
		if owner[l] == nil {
			free = append(free, l)
		}
	}
	for _, run := range ToRanges(free) {
		first, final := run[0], run[1]
		for l := first; l <= final; l++ {
			if strings.TrimSpace(lines[l-1]) != "" {
				return nil, fmt.Errorf("line %d is in no section", l)
			}
		}
		next := final + 1
		if first > 1 {
			next = first - 1
		}
		if next >= len(owner) || owner[next] == nil {
			continue
		}
		host := owner[next]
		joined := make([]int, 0)
		for l := first; l <= final; l++ {
			joined = append(joined, l)
		}
		if code[next] {
			host.Ranges = ToRanges(append(rangeLines(host.Ranges), joined...))
		} else {
			host.DocRanges = ToRanges(append(rangeLines(host.DocRanges), joined...))
		}
	}
	sort.SliceStable(sections, func(i, j int) bool { return firstLine(sections[i]) < firstLine(sections[j]) })
	return sections, nil
}

// firstLine is where a section starts in the file; a deleted one, with no
// lines, sorts first as in the Perl backend (its first line is undefined,
// so numerically 0).
func firstLine(s *Unit) int {
	first := 0
	if len(s.Ranges) > 0 {
		first = s.Ranges[0][0]
	}
	if len(s.DocRanges) > 0 && s.DocRanges[0][0] < first {
		return s.DocRanges[0][0]
	}
	return first
}

// ToRanges makes sorted ranges of line numbers.
func ToRanges(lines []int) [][2]int {
	s := append([]int{}, lines...)
	sort.Ints(s)
	var ranges [][2]int
	for _, l := range s {
		if len(ranges) > 0 && ranges[len(ranges)-1][1]+1 == l {
			ranges[len(ranges)-1][1] = l
		} else if len(ranges) > 0 && ranges[len(ranges)-1][1] >= l {
			continue
		} else {
			ranges = append(ranges, [2]int{l, l})
		}
	}
	if ranges == nil {
		ranges = [][2]int{}
	}
	return ranges
}
