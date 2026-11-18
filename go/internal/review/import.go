package review

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/exodist/Review-Helper/go/internal/git"
)

// Importing a review export (standards/FORMAT.md, "Export"): into a new
// review file, or merged into the file already holding that review.

// ImportResult is where an import went, and the iterations whose head
// commits are not in the repository, so not pinned.
type ImportResult struct {
	Path    string
	Missing []*Iteration
}

// ImportOptions choose the review file: File, when set, is its path; else
// Name (when set) or the review's title or id names it in the default
// location.
type ImportOptions struct {
	File, Name string
}

// ParseExport decodes and checks a review export, returning it and its
// review's id.
func ParseExport(data []byte) (*ReviewExport, string, error) {
	var head struct {
		Format        any             `json:"format"`
		FormatVersion json.RawMessage `json:"format_version"`
		Tables        json.RawMessage `json:"tables"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, "", fmt.Errorf("not valid JSON: %w", err)
	}
	if head.Format != ExportFormat {
		return nil, "", errors.New("not a review export")
	}
	if v := string(bytes.TrimSpace(head.FormatVersion)); v != "1" && v != `"1"` {
		return nil, "", fmt.Errorf("unsupported review export format version %s", v)
	}
	if firstByte(head.Tables) != '{' {
		return nil, "", errors.New("the export has no 'tables' object")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(head.Tables, &raw); err != nil {
		return nil, "", fmt.Errorf("the export's tables: %w", err)
	}
	var excluded []string
	for _, n := range []string{"feedback", "flag_seen", "request"} {
		if _, ok := raw[n]; ok {
			excluded = append(excluded, n)
		}
	}
	if len(excluded) > 0 {
		return nil, "", fmt.Errorf("an export must not include these tables: %s", strings.Join(excluded, " "))
	}

	x := &ReviewExport{Format: ExportFormat, FormatVersion: ExportFormatVersion, Tables: map[string]*ExportTable{}}
	for name, b := range raw {
		t, err := decodeTable(name, b)
		if err != nil {
			return nil, "", err
		}
		x.Tables[name] = t
	}
	review := x.Tables["review"]
	if review == nil || len(review.Rows) != 1 {
		return nil, "", errors.New("the export must hold exactly one review row")
	}
	id, ok := review.value(0, "id").(string)
	if !ok || id == "" {
		return nil, "", errors.New("the export's review has no id")
	}
	return x, id, nil
}

// decodeTable reads one exported table: columns and rows whose values are
// strings, numbers or null, each row as long as the column list.
func decodeTable(name string, b json.RawMessage) (*ExportTable, error) {
	var t struct {
		Columns []string          `json:"columns"`
		Rows    []json.RawMessage `json:"rows"`
	}
	if err := json.Unmarshal(b, &t); err != nil || t.Columns == nil || t.Rows == nil {
		return nil, fmt.Errorf("table '%s' needs 'columns' and 'rows' arrays", name)
	}
	if len(t.Columns) == 0 {
		return nil, fmt.Errorf("table '%s' needs 'columns' and 'rows' arrays", name)
	}
	out := &ExportTable{Columns: t.Columns, Rows: make([][]any, 0, len(t.Rows))}
	for _, rb := range t.Rows {
		dec := json.NewDecoder(bytes.NewReader(rb))
		dec.UseNumber()
		var row []any
		if err := dec.Decode(&row); err != nil || len(row) != len(t.Columns) {
			return nil, fmt.Errorf("table '%s' has a row that does not match its columns", name)
		}
		for i, v := range row {
			switch v := v.(type) {
			case nil, string:
			case json.Number:
				if n, err := v.Int64(); err == nil {
					row[i] = n
				} else if f, err := v.Float64(); err == nil {
					row[i] = f
				} else {
					return nil, fmt.Errorf("table '%s' has a number out of range: %s", name, v)
				}
			default:
				return nil, fmt.Errorf("table '%s' has a value that is not a string, number or null", name)
			}
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

// value is row i's value of column col, or nil.
func (t *ExportTable) value(i int, col string) any {
	for c, name := range t.Columns {
		if name == col && i < len(t.Rows) {
			return t.Rows[i][c]
		}
	}
	return nil
}

// Import creates a review file from an export, or merges it into the file
// holding that review; registers the review as open and pins every
// iteration whose head is present.
func Import(repo *git.Repo, x *ReviewExport, id string, opt ImportOptions) (*ImportResult, error) {
	reg := NewRegistry(repo)
	reviews, err := reg.Reviews()
	if err != nil {
		return nil, err
	}
	known, isKnown := reviews[id]
	path, err := importTarget(repo, x, id, opt, known, isKnown)
	if err != nil {
		return nil, err
	}

	_, statErr := os.Stat(path)
	isNew := errors.Is(statErr, os.ErrNotExist)
	var d *DB
	if isNew {
		d, err = CreateDB(path, NewReview{ID: id})
	} else {
		d, err = openSameReview(path, id)
	}
	if err != nil {
		return nil, err
	}
	defer d.Close()

	// Registering is the transaction's last step, so a failure to register
	// rolls the import back rather than leave it unregistered.
	err = d.Txn(func(tx *sql.Tx) error {
		if err := loadExport(tx, x); err != nil {
			return err
		}
		if err := checkPins(tx, id); err != nil {
			return err
		}
		if err := checkReferences(tx); err != nil {
			return err
		}
		return reg.Register(id, path, "open")
	})
	if err != nil {
		if isNew {
			d.Close()
			os.Remove(path)
		}
		return nil, err
	}

	res := &ImportResult{Path: path}
	iterations, err := d.Iterations()
	if err != nil {
		return nil, err
	}
	for _, it := range iterations {
		ok, err := repo.HasRevision(it.Head)
		if err != nil {
			return nil, err
		}
		if !ok {
			res.Missing = append(res.Missing, it)
			continue
		}
		if err := repo.Pin(it.Pin, it.Head); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// importTarget is the registered file when it exists, else the file asked
// for or the default one.
func importTarget(repo *git.Repo, x *ReviewExport, id string, opt ImportOptions, known RegEntry, isKnown bool) (string, error) {
	if isKnown {
		if _, err := os.Stat(known.Path); err == nil {
			if opt.File != "" {
				asked, err := absReviewPath(opt.File)
				if err != nil {
					return "", err
				}
				have, err := absReviewPath(known.Path)
				if err != nil {
					return "", err
				}
				if asked != have {
					return "", fmt.Errorf("review %s is already in this repository: %s", id, known.Path)
				}
			}
			return known.Path, nil
		}
	}
	file := opt.File
	if file == "" {
		dir, err := repo.StateDir()
		if err != nil {
			return "", err
		}
		file = filepath.Join(dir, importName(x, opt.Name)+".sqlite")
	}
	path, err := absReviewPath(file)
	if err != nil {
		return "", err
	}
	return path, checkReviewLocation(repo, path)
}

// importName is the default file's name: the one given, else the review's
// title or id, made safe for a file name.
func importName(x *ReviewExport, name string) string {
	if name == "" {
		review := x.Tables["review"]
		if title, ok := review.value(0, "title").(string); ok {
			name = title
		} else {
			name, _ = review.value(0, "id").(string)
		}
	}
	var b strings.Builder
	dash := false
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '_' || r == '.' || r == '-' {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.TrimRight(strings.TrimLeft(b.String(), "-."), "-")
	if s == "" {
		return "review"
	}
	return s
}

// openSameReview opens an existing file to merge into, which must hold the
// review.
func openSameReview(path, id string) (*DB, error) {
	if !IsReviewFile(path) {
		return nil, fmt.Errorf("review file '%s' already exists and does not hold review %s", path, id)
	}
	d, err := OpenDB(path)
	if err != nil {
		return nil, err
	}
	rv, err := d.Review()
	if err != nil || rv.ID != id {
		d.Close()
		return nil, fmt.Errorf("review file '%s' already exists and does not hold review %s", path, id)
	}
	return d, nil
}

// loadExport writes the export into the file: its review row replaces the
// file's, each exported iteration replaces the file's of that number with
// everything under it, and other iterations, feedback and requests stay.
// The review is then open, state being each reviewer's own. Foreign keys
// are checked once every row is in.
func loadExport(tx *sql.Tx, x *ReviewExport) error {
	if _, err := tx.Exec("PRAGMA defer_foreign_keys = ON"); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM review"); err != nil {
		return err
	}
	numbers := exportedIterations(x)
	if err := saveMarks(tx, numbers); err != nil {
		return err
	}
	if err := clearIterations(tx, numbers); err != nil {
		return err
	}
	names, err := exportedTables()
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := loadTable(tx, name, x.Tables[name]); err != nil {
			return err
		}
	}
	if err := restoreMarks(tx); err != nil {
		return err
	}
	_, err = tx.Exec("UPDATE review SET state = 'open'")
	return err
}

// exportedIterations lists the numbers of the export's iterations.
func exportedIterations(x *ReviewExport) []any {
	t := x.Tables["iteration"]
	if t == nil {
		return nil
	}
	numbers := make([]any, 0, len(t.Rows))
	for i := range t.Rows {
		numbers = append(numbers, t.value(i, "number"))
	}
	return numbers
}

func placeholders(n int) string {
	return "(" + strings.TrimSuffix(strings.Repeat("?, ", n), ", ") + ")"
}

// saveMarks keeps the seen marks of the flags about to be replaced, with
// what each flag covers and its color.
func saveMarks(tx *sql.Tx, numbers []any) error {
	if _, err := tx.Exec("DROP TABLE IF EXISTS temp.saved_marks"); err != nil {
		return err
	}
	_, err := tx.Exec(`CREATE TEMP TABLE saved_marks AS
	SELECT flag.note_id, commit_entry.iteration, commit_entry.revision, flag.path, flag.line_start, flag.line_end,
	       flag.color, flag_seen.reviewer, flag_seen.at
	  FROM flag_seen
	  JOIN flag ON flag.id = flag_seen.flag
	  JOIN commit_entry ON commit_entry.id = flag.commit_id
	 WHERE commit_entry.iteration IN `+placeholders(len(numbers)), numbers...)
	return err
}

// restoreMarks moves each saved mark to the flag with its note id in its
// iteration that covers the same commit, path and lines, unless the export
// turned that flag from yellow to red: a flag turned red is unseen by
// everyone.
func restoreMarks(tx *sql.Tx) error {
	for _, q := range []string{
		"DELETE FROM flag_seen WHERE flag NOT IN (SELECT id FROM flag)",
		`INSERT OR IGNORE INTO flag_seen (flag, reviewer, at)
		SELECT flag.id, saved_marks.reviewer, saved_marks.at
		  FROM temp.saved_marks AS saved_marks
		  JOIN flag ON flag.note_id = saved_marks.note_id
		       AND flag.path IS saved_marks.path
		       AND flag.line_start IS saved_marks.line_start
		       AND flag.line_end IS saved_marks.line_end
		       AND NOT (saved_marks.color = 'yellow' AND flag.color = 'red')
		  JOIN commit_entry ON commit_entry.id = flag.commit_id
		       AND commit_entry.iteration = saved_marks.iteration
		       AND commit_entry.revision = saved_marks.revision`,
		"DROP TABLE temp.saved_marks",
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

// clearIterations deletes iterations with every row under them, children
// first.
func clearIterations(tx *sql.Tx, numbers []any) error {
	if len(numbers) == 0 {
		return nil
	}
	in := placeholders(len(numbers))
	commits := "SELECT id FROM commit_entry WHERE iteration IN " + in
	files := "SELECT id FROM file_change WHERE commit_id IN (" + commits + ")"
	for _, q := range []string{
		"DELETE FROM guide_step WHERE iteration IN " + in,
		"DELETE FROM journal_entry WHERE iteration IN " + in,
		"DELETE FROM flag_seen WHERE flag IN (SELECT id FROM flag WHERE commit_id IN (" + commits + "))",
		"DELETE FROM flag WHERE commit_id IN (" + commits + ")",
		"DELETE FROM section WHERE file_id IN (" + files + ")",
		"DELETE FROM file_change WHERE commit_id IN (" + commits + ")",
		"DELETE FROM commit_entry WHERE iteration IN " + in,
		"DELETE FROM iteration WHERE number IN " + in,
	} {
		if _, err := tx.Exec(q, numbers...); err != nil {
			return err
		}
	}
	return nil
}

// loadTable inserts a table's exported rows, leaving out extension columns.
// A column the export lacks would load as null without a word, so that is
// refused; a column the file lacks fails the insert.
func loadTable(tx *sql.Tx, name string, t *ExportTable) error {
	if t == nil {
		return otherVersion("it has no table '%s'", name)
	}
	var keep []int
	has := map[string]bool{}
	for i, c := range t.Columns {
		if !isExtension(c) {
			keep = append(keep, i)
			has[c] = true
		}
	}
	info, err := tableColumns(tx, name)
	if err != nil {
		return err
	}
	var lacks []string
	for _, c := range info {
		if !has[c.Name] && !isExtension(c.Name) {
			lacks = append(lacks, c.Name)
		}
	}
	if len(lacks) > 0 {
		return otherVersion("table '%s' lacks columns: %s", name, strings.Join(lacks, " "))
	}

	cols := make([]string, len(keep))
	for i, k := range keep {
		cols[i] = quoteIdent(t.Columns[k])
	}
	// The file's own meta rows win.
	insert := "INSERT"
	if name == "meta" {
		insert = "INSERT OR IGNORE"
	}
	stmt, err := tx.Prepare(insert + " INTO " + quoteIdent(name) + " (" + strings.Join(cols, ", ") + ") VALUES " + placeholders(len(cols)))
	if err != nil {
		return fmt.Errorf("table '%s': %w", name, err)
	}
	defer stmt.Close()
	args := make([]any, len(keep))
	for _, row := range t.Rows {
		for i, k := range keep {
			args[i] = row[k]
		}
		if _, err := stmt.Exec(args...); err != nil {
			return fmt.Errorf("table '%s': %w", name, err)
		}
	}
	return nil
}

// checkPins requires each iteration's pin to be the name the VCS gives it.
func checkPins(tx *sql.Tx, id string) error {
	rows, err := tx.Query("SELECT number, pin FROM iteration ORDER BY number")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var n int
		var pin string
		if err := rows.Scan(&n, &pin); err != nil {
			return err
		}
		if want := git.PinName(id, n); pin != want {
			return fmt.Errorf("iteration %d must have pin '%s'", n, want)
		}
	}
	return rows.Err()
}

// checkReferences checks the deferred foreign keys before the commit, so a
// step run before it (registering) is not undone by a bad reference.
func checkReferences(tx *sql.Tx) error {
	rows, err := tx.Query("PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		return rows.Err()
	}
	var table, parent string
	var rowid sql.NullInt64
	var fkid int
	if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
		return err
	}
	return fmt.Errorf("table '%s' has a row that refers to a missing '%s' row", table, parent)
}

func runImport(args []string, out, warn io.Writer) error {
	var file string
	o := newOptions()
	o.path("file", &file)
	rest, err := o.parse(args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errors.New("usage: review-helper import JSON [--file P]")
	}
	src, name := rest[0], ""
	var data []byte
	if src == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(src)
		name = strings.TrimSuffix(filepath.Base(src), ".json")
	}
	if err != nil {
		return err
	}
	x, id, err := ParseExport(data)
	if err != nil {
		return fmt.Errorf("%s: %w", src, err)
	}
	repo, err := cwdRepo()
	if err != nil {
		return err
	}
	res, err := Import(repo, x, id, ImportOptions{File: file, Name: name})
	if err != nil {
		return err
	}
	fmt.Fprintln(out, res.Path)
	for _, it := range res.Missing {
		fmt.Fprintf(warn, "Iteration %d: commit %s is not in this repository; fetch it to view that iteration.\n", it.Number, it.Head)
	}
	return nil
}
