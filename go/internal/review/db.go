package review

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/exodist/Review-Helper/go/internal/share"
	_ "modernc.org/sqlite"
)

const (
	FileFormat        = "review-file"
	FileFormatVersion = "1"
)

// Row is one row of a query: column name to value (int64, float64, string,
// []byte or nil).
type Row map[string]any

// DB is an open review file.
type DB struct {
	Path string
	SQL  *sql.DB
}

// q is what both a DB and a transaction can query.
type q interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
}

// Schema is standards/review.sql, as shipped.
func Schema() (string, error) {
	b, err := fs.ReadFile(share.FS, "standards/review.sql")
	return string(b), err
}

var tableRE = regexp.MustCompile(`(?m)^CREATE TABLE (\w+) \(`)

// SchemaTables lists the schema's tables in schema order.
func SchemaTables() []string {
	s, _ := Schema()
	var names []string
	for _, m := range tableRE.FindAllStringSubmatch(s, -1) {
		names = append(names, m[1])
	}
	return names
}

func connect(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)")
	if err != nil {
		return nil, err
	}
	// One connection: transactions and last-insert ids stay on it.
	db.SetMaxOpenConns(1)
	return db, nil
}

// ReviewFields are a new review's row.
type ReviewFields struct {
	ID, Title, Source string
	Created           int64
}

// CreateDB makes a new review file from the schema.
func CreateDB(path string, f ReviewFields) (*DB, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, errorf("Review file '%s' already exists", path)
	}
	if strings.Contains(path, ";") {
		return nil, errorf("Review file paths may not contain ';': %s", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return nil, err
	}
	sqlDB, err := connect(path)
	if err != nil {
		return nil, err
	}
	d := &DB{Path: path, SQL: sqlDB}
	err = d.Txn(func(tx *sql.Tx) error {
		schema, err := Schema()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(schema); err != nil {
			return err
		}
		for _, kv := range [][2]string{{"format", FileFormat}, {"format_version", FileFormatVersion}, {"generator", Generator()}} {
			if _, err := tx.Exec("INSERT INTO meta (key, value) VALUES (?, ?)", kv[0], kv[1]); err != nil {
				return err
			}
		}
		id := f.ID
		if id == "" {
			id = UUID7()
		}
		created := f.Created
		if created == 0 {
			created = Now()
		}
		_, err = tx.Exec("INSERT INTO review (id, title, source, created, state) VALUES (?, ?, ?, ?, 'open')", id, nullStr(f.Title), nullStr(f.Source), created)
		return err
	})
	if err != nil {
		sqlDB.Close()
		os.Remove(path)
		return nil, err
	}
	return d, nil
}

// OpenDB opens an existing review file of this format version.
func OpenDB(path string) (*DB, error) {
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		return nil, errorf("No such review file: %s", path)
	}
	if !IsReviewFile(path) {
		return nil, errorf("Not a review file: %s", path)
	}
	sqlDB, err := connect(path)
	if err != nil {
		return nil, err
	}
	d := &DB{Path: path, SQL: sqlDB}
	v, _ := d.Value("SELECT value FROM meta WHERE key = 'format_version'")
	if s, _ := v.(string); s != FileFormatVersion {
		sqlDB.Close()
		return nil, errorf("Unsupported review file format version '%v': %s", nz(v), path)
	}
	return d, nil
}

func nz(v any) any {
	if v == nil {
		return ""
	}
	return v
}

// IsReviewFile reports whether path is an SQLite file whose meta says it is
// a review file, whatever its format version.
func IsReviewFile(path string) bool {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return false
	}
	sqlDB, err := connect(path)
	if err != nil {
		return false
	}
	defer sqlDB.Close()
	var format string
	if err := sqlDB.QueryRow("SELECT value FROM meta WHERE key = 'format'").Scan(&format); err != nil {
		return false
	}
	return format == FileFormat
}

// Close closes the file.
func (d *DB) Close() error { return d.SQL.Close() }

// Txn runs fn in a transaction, committing when it returns nil.
func (d *DB) Txn(fn func(tx *sql.Tx) error) error {
	tx, err := d.SQL.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Rows runs a query and returns every row.
func Rows(x q, query string, args ...any) ([]Row, error) {
	rs, err := x.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	cols, err := rs.Columns()
	if err != nil {
		return nil, err
	}
	var out []Row
	for rs.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			return nil, err
		}
		r := Row{}
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				vals[i] = string(b)
			}
			r[c] = vals[i]
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

// One runs a query and returns its first row, or nil.
func One(x q, query string, args ...any) (Row, error) {
	rows, err := Rows(x, query, args...)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

// Rows on the file.
func (d *DB) Rows(query string, args ...any) ([]Row, error) { return Rows(d.SQL, query, args...) }

// One on the file.
func (d *DB) One(query string, args ...any) (Row, error) { return One(d.SQL, query, args...) }

// Value is the first column of the first row, or nil.
func (d *DB) Value(query string, args ...any) (any, error) {
	var v any
	err := d.SQL.QueryRow(query, args...).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if b, ok := v.([]byte); ok {
		v = string(b)
	}
	return v, err
}

// Exec on the file.
func (d *DB) Exec(query string, args ...any) (sql.Result, error) { return d.SQL.Exec(query, args...) }

// Review is the review row.
func (d *DB) Review() (Row, error) { return d.One("SELECT * FROM review") }

// SetState sets review.state.
func (d *DB) SetState(state string) error {
	if state != "open" && state != "closed" {
		return errorf("Review state must be 'open' or 'closed'")
	}
	_, err := d.Exec("UPDATE review SET state = ?", state)
	return err
}

// SetPublished marks feedback as posted.
func (d *DB) SetPublished(id int64) error {
	res, err := d.Exec("UPDATE feedback SET published = ? WHERE id = ?", Now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errorf("No such feedback item: %d", id)
	}
	return nil
}

// Iterations lists every iteration, oldest first.
func (d *DB) Iterations() ([]Row, error) { return d.Rows("SELECT * FROM iteration ORDER BY number") }

// LatestIteration is the newest iteration, or nil.
func (d *DB) LatestIteration() (Row, error) {
	return d.One("SELECT * FROM iteration ORDER BY number DESC LIMIT 1")
}

// Iteration is iteration number n, or nil.
func (d *DB) Iteration(n int64) (Row, error) {
	return d.One("SELECT * FROM iteration WHERE number = ?", n)
}

// Commits lists an iteration's entries in order.
func (d *DB) Commits(iteration int64) ([]Row, error) {
	return d.Rows("SELECT * FROM commit_entry WHERE iteration = ? ORDER BY position", iteration)
}

// Files lists an entry's file changes.
func (d *DB) Files(commitID int64) ([]Row, error) {
	return d.Rows("SELECT * FROM file_change WHERE commit_id = ? ORDER BY id", commitID)
}

// Sections lists a file change's sections in file order.
func (d *DB) Sections(fileID int64) ([]Row, error) {
	return d.Rows("SELECT * FROM section WHERE file_id = ? ORDER BY position", fileID)
}

// Touched reports whether the commit changed a line of the section.
func Touched(s Row) bool {
	return Int(s["deleted"]) != 0 || Int(s["added"]) != 0 || Int(s["removed"]) != 0 || Int(s["changed"]) != 0
}

// Flags lists an iteration's flags with their entry's revision.
func (d *DB) Flags(iteration int64) ([]Row, error) {
	return d.Rows(`SELECT flag.*, commit_entry.revision
	  FROM flag JOIN commit_entry ON commit_entry.id = flag.commit_id
	 WHERE commit_entry.iteration = ?
	 ORDER BY commit_entry.position, flag.path, flag.line_start, flag.id`, iteration)
}

// FlagByNote is the iteration's flag with a note id, with its entry's
// revision and topic, or nil.
func (d *DB) FlagByNote(iteration int64, note string) (Row, error) {
	return d.One(`SELECT flag.*, commit_entry.revision, commit_entry.topic AS topic
	  FROM flag JOIN commit_entry ON commit_entry.id = flag.commit_id
	 WHERE commit_entry.iteration = ? AND flag.note_id = ?`, iteration, note)
}

// FlagSeen lists the seen marks of an iteration's flags.
func (d *DB) FlagSeen(iteration int64) ([]Row, error) {
	return d.Rows(`SELECT flag_seen.*
	  FROM flag_seen
	  JOIN flag ON flag.id = flag_seen.flag
	  JOIN commit_entry ON commit_entry.id = flag.commit_id
	 WHERE commit_entry.iteration = ?
	 ORDER BY flag_seen.at, flag_seen.reviewer`, iteration)
}

// SetFlagSeen sets or removes a reviewer's mark.
func (d *DB) SetFlagSeen(flag int64, reviewer string, seen bool) error {
	var err error
	if seen {
		_, err = d.Exec("INSERT OR IGNORE INTO flag_seen (flag, reviewer, at) VALUES (?, ?, ?)", flag, reviewer, Now())
	} else {
		_, err = d.Exec("DELETE FROM flag_seen WHERE flag = ? AND reviewer = ?", flag, reviewer)
	}
	return err
}

// Int reads an integer column value (0 for NULL).
func Int(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case bool:
		if n {
			return 1
		}
	case string:
		var i int64
		fmt.Sscan(n, &i)
		return i
	}
	return 0
}

// Str reads a text column value ("" for NULL).
func Str(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

// IsNull reports whether a column value is NULL.
func IsNull(v any) bool { return v == nil }

// nullStr stores "" as NULL.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
