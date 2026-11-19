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

var kindByExt = map[string]string{
	"pm": "perl", "pl": "perl", "t": "perl", "psgi": "perl", "pod": "perl",
	"js": "javascript", "mjs": "javascript", "cjs": "javascript", "jsx": "javascript",
	"ts": "typescript", "mts": "typescript", "cts": "typescript", "tsx": "typescript",
	"html": "html", "htm": "html",
	"xml": "xml", "xsd": "xml", "xsl": "xml", "xslt": "xml",
	"md": "markdown", "markdown": "markdown",
	"sh": "shell", "bash": "shell", "zsh": "shell", "ksh": "shell", "bashrc": "shell", "zshrc": "shell", "bash_profile": "shell",
	"yml": "yaml", "yaml": "yaml",
	"ini": "ini", "cfg": "ini",
	"py": "python", "pyi": "python",
	"rb": "ruby", "rake": "ruby", "gemspec": "ruby",
	"c": "c", "h": "c",
	"cpp": "cpp", "cc": "cpp", "cxx": "cpp", "hpp": "cpp", "hh": "cpp", "hxx": "cpp",
	"diff": "diff", "patch": "diff",
	"png": "image", "jpg": "image", "jpeg": "image", "gif": "image", "webp": "image", "bmp": "image", "ico": "image",
	"css": "css", "sql": "sql", "json": "json", "toml": "toml", "go": "go", "java": "java",
	"rs": "rust",
	"mk": "makefile",
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

// Assemble lays out an agent's live units (each with code lines) as the
// file's sections, in file order. A run of lines in no unit must be all
// blank: it joins the section holding the line before it (the line after
// it at the top of the file), in code or documentation like that line.
// Units must not share lines, and their ranges must lie inside the file.
// No units: the file is left whole.
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
		for _, l := range append(rangeLines(s.Ranges), rangeLines(s.DocRanges)...) {
			if l < 1 || l > len(lines) {
				return nil, fmt.Errorf("line %d is outside the file's %d lines", l, len(lines))
			}
		}
		for _, l := range rangeLines(s.Ranges) {
			owner[l], code[l] = s, true
		}
		for _, l := range rangeLines(s.DocRanges) {
			owner[l] = s
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
		host := owner[next]
		if host == nil {
			continue
		}
		part := &host.DocRanges
		if code[next] {
			part = &host.Ranges
		}
		*part = ToRanges(append(rangeLines(*part), rangeLines([][2]int{run})...))
	}
	sort.SliceStable(sections, func(i, j int) bool { return firstLine(sections[i]) < firstLine(sections[j]) })
	return sections, nil
}

// firstLine is where a live section starts in the file.
func firstLine(s *Unit) int {
	first := s.Ranges[0][0]
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
