package review

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"

	"github.com/exodist/Review-Helper/go/internal/share"
	_ "modernc.org/sqlite"
)

const (
	FileFormat        = "review-file"
	FileFormatVersion = "1"
)

// DB is an open review file.
type DB struct {
	Path string
	*sql.DB
}

// Schema is standards/review.sql, as shipped.
func Schema() (string, error) {
	b, err := fs.ReadFile(share.FS, "standards/review.sql")
	return string(b), err
}

var tableRE = regexp.MustCompile(`(?m)^CREATE TABLE (\w+) \(`)

// SchemaTables lists the schema's tables in schema order.
func SchemaTables() ([]string, error) {
	s, err := Schema()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, m := range tableRE.FindAllStringSubmatch(s, -1) {
		names = append(names, m[1])
	}
	return names, nil
}

func connect(path string) (*sql.DB, error) {
	// SQLite reads the name as a URI, so the path is escaped: a # or ? in it
	// would otherwise end the path, and %HH be decoded. Transactions take the
	// write lock when they begin, so one that reads first still waits for
	// another writer rather than failing.
	uri := url.URL{Scheme: "file", OmitHost: true, Path: path, RawQuery: "_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_txlock=immediate"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	// One connection, so a transaction and its last-insert ids stay on it.
	db.SetMaxOpenConns(1)
	return db, nil
}

// NewReview is a new review's row; zero values take defaults.
type NewReview struct {
	ID, Title, Source string
	Created           int64
}

// CreateDB makes a new review file from the schema. The file is created
// exclusively first, so of two builds racing for one path, one fails and
// neither removes the other's file.
func CreateDB(path string, r NewReview) (*DB, error) {
	schema, err := Schema()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o666)
	if errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("review file %s already exists", path)
	}
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return nil, err
	}
	sqlDB, err := connect(path)
	if err != nil {
		os.Remove(path)
		return nil, err
	}
	d := &DB{Path: path, DB: sqlDB}
	if r.ID == "" {
		r.ID = UUID7()
	}
	if r.Created == 0 {
		r.Created = now()
	}
	err = d.Txn(func(tx *sql.Tx) error {
		if _, err := tx.Exec(schema); err != nil {
			return err
		}
		for _, kv := range [][2]string{{"format", FileFormat}, {"format_version", FileFormatVersion}, {"generator", Generator}} {
			if _, err := tx.Exec("INSERT INTO meta (key, value) VALUES (?, ?)", kv[0], kv[1]); err != nil {
				return err
			}
		}
		_, err := tx.Exec("INSERT INTO review (id, title, source, created, state) VALUES (?, ?, ?, ?, 'open')", r.ID, nullable(r.Title), nullable(r.Source), r.Created)
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
		return nil, fmt.Errorf("no such review file: %s", path)
	}
	if !IsReviewFile(path) {
		return nil, fmt.Errorf("not a review file: %s", path)
	}
	sqlDB, err := connect(path)
	if err != nil {
		return nil, err
	}
	var version string
	if err := sqlDB.QueryRow("SELECT value FROM meta WHERE key = 'format_version'").Scan(&version); err != nil && !errors.Is(err, sql.ErrNoRows) {
		sqlDB.Close()
		return nil, err
	}
	if version != FileFormatVersion {
		sqlDB.Close()
		return nil, fmt.Errorf("unsupported review file format version %q: %s", version, path)
	}
	return &DB{Path: path, DB: sqlDB}, nil
}

// IsReviewFile reports whether path is an SQLite file whose meta says it is
// a review file, whatever its format version.
func IsReviewFile(path string) bool {
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		return false
	}
	sqlDB, err := connect(path)
	if err != nil {
		return false
	}
	defer sqlDB.Close()
	var format string
	return sqlDB.QueryRow("SELECT value FROM meta WHERE key = 'format'").Scan(&format) == nil && format == FileFormat
}

// Txn runs fn in a transaction, committing when it returns nil.
func (d *DB) Txn(fn func(tx *sql.Tx) error) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Review is the review row.
func (d *DB) Review() (*Review, error) {
	var r Review
	err := d.QueryRow("SELECT id, title, source, created, state FROM review").Scan(&r.ID, &r.Title, &r.Source, &r.Created, &r.State)
	return &r, err
}

// SetState sets review.state.
func (d *DB) SetState(state string) error {
	if state != "open" && state != "closed" {
		return fmt.Errorf("review state must be open or closed, not %q", state)
	}
	_, err := d.Exec("UPDATE review SET state = ?", state)
	return err
}

// SetPublished marks feedback as posted.
func (d *DB) SetPublished(id int64) error {
	res, err := d.Exec("UPDATE feedback SET published = ? WHERE id = ?", now(), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("mark feedback %d published: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("no such feedback item: %d", id)
	}
	return nil
}

// Iterations lists every iteration, oldest first.
func (d *DB) Iterations() ([]*Iteration, error) {
	return queryAll(d, scanIteration, "SELECT "+iterationCols+" FROM iteration ORDER BY number")
}

// LatestIteration is the newest iteration, or nil.
func (d *DB) LatestIteration() (*Iteration, error) {
	return queryOne(d, scanIteration, "SELECT "+iterationCols+" FROM iteration ORDER BY number DESC LIMIT 1")
}

// Iteration is iteration number n, or nil.
func (d *DB) Iteration(n int64) (*Iteration, error) {
	return queryOne(d, scanIteration, "SELECT "+iterationCols+" FROM iteration WHERE number = ?", n)
}

// Commits lists an iteration's entries in order.
func (d *DB) Commits(iteration int64) ([]*CommitEntry, error) {
	return queryAll(d, scanCommit, "SELECT "+commitCols+" FROM commit_entry WHERE iteration = ? ORDER BY position", iteration)
}

// Commit is the entry with id, or nil.
func (d *DB) Commit(id int64) (*CommitEntry, error) {
	return queryOne(d, scanCommit, "SELECT "+commitCols+" FROM commit_entry WHERE id = ?", id)
}

// Files lists an entry's file changes.
func (d *DB) Files(commitID int64) ([]*FileChange, error) {
	return queryAll(d, scanFile, "SELECT "+fileCols+" FROM file_change WHERE commit_id = ? ORDER BY id", commitID)
}

// CommitsByRevision lists an iteration's entries with a revision: one, or
// in a squashed iteration split into topics, one per topic.
func (d *DB) CommitsByRevision(iteration int64, revision string) ([]*CommitEntry, error) {
	return queryAll(d, scanCommit, "SELECT "+commitCols+" FROM commit_entry WHERE iteration = ? AND revision = ? ORDER BY position", iteration, revision)
}

// FileByPath is the entry's file change at path, or nil.
func (d *DB) FileByPath(commitID int64, path string) (*FileChange, error) {
	return queryOne(d, scanFile, "SELECT "+fileCols+" FROM file_change WHERE commit_id = ? AND path = ?", commitID, path)
}

// File is the file change with id, or nil.
func (d *DB) File(id int64) (*FileChange, error) {
	return queryOne(d, scanFile, "SELECT "+fileCols+" FROM file_change WHERE id = ?", id)
}

// Sections lists a file change's sections in file order.
func (d *DB) Sections(fileID int64) ([]*Section, error) {
	return queryAll(d, scanSection, "SELECT "+sectionCols+" FROM section WHERE file_id = ? ORDER BY position", fileID)
}

const flagJoin = " FROM flag JOIN commit_entry ON commit_entry.id = flag.commit_id"

// Flags lists an iteration's flags with their entries' revisions and topics.
func (d *DB) Flags(iteration int64) ([]*Flag, error) {
	return queryAll(d, scanFlag, "SELECT "+flagCols+", commit_entry.revision, commit_entry.topic"+flagJoin+
		" WHERE commit_entry.iteration = ? ORDER BY commit_entry.position, flag.path, flag.line_start, flag.id", iteration)
}

// FlagByNote is the iteration's flag with a note id, or nil.
func (d *DB) FlagByNote(iteration int64, note string) (*Flag, error) {
	return queryOne(d, scanFlag, "SELECT "+flagCols+", commit_entry.revision, commit_entry.topic"+flagJoin+
		" WHERE commit_entry.iteration = ? AND flag.note_id = ?", iteration, note)
}

// FlagByID is the flag with id, or nil.
func (d *DB) FlagByID(id int64) (*Flag, error) {
	return queryOne(d, scanFlag, "SELECT "+flagCols+", commit_entry.revision, commit_entry.topic"+flagJoin+" WHERE flag.id = ?", id)
}

// FlagSeen lists the seen marks of an iteration's flags, oldest first.
func (d *DB) FlagSeen(iteration int64) ([]*FlagSeen, error) {
	return queryAll(d, func(s scanner) (*FlagSeen, error) {
		var f FlagSeen
		return &f, s.Scan(&f.Flag, &f.Reviewer, &f.At)
	}, `SELECT flag_seen.flag, flag_seen.reviewer, flag_seen.at
	  FROM flag_seen
	  JOIN flag ON flag.id = flag_seen.flag
	  JOIN commit_entry ON commit_entry.id = flag.commit_id
	 WHERE commit_entry.iteration = ?
	 ORDER BY flag_seen.at, flag_seen.reviewer`, iteration)
}

// SetFlagSeen sets or removes a reviewer's mark; a mark already there keeps
// its time.
func (d *DB) SetFlagSeen(flag int64, reviewer string, seen bool) error {
	return setFlagSeen(d, flag, reviewer, seen)
}

func setFlagSeen(q querier, flag int64, reviewer string, seen bool) error {
	var err error
	if seen {
		_, err = q.Exec("INSERT OR IGNORE INTO flag_seen (flag, reviewer, at) VALUES (?, ?, ?)", flag, reviewer, now())
	} else {
		_, err = q.Exec("DELETE FROM flag_seen WHERE flag = ? AND reviewer = ?", flag, reviewer)
	}
	return err
}

// JournalEntries lists an iteration's journal entries, oldest first.
func (d *DB) JournalEntries(iteration int64) ([]*JournalEntry, error) {
	return queryAll(d, scanJournal, "SELECT "+journalCols+" FROM journal_entry WHERE iteration = ? ORDER BY created, id", iteration)
}

// GuideSteps lists an iteration's guide.
func (d *DB) GuideSteps(iteration int64) ([]*GuideStep, error) {
	return queryAll(d, scanGuide, "SELECT "+guideCols+" FROM guide_step WHERE iteration = ? ORDER BY position", iteration)
}

// AllFeedback lists every feedback row, oldest first.
func (d *DB) AllFeedback() ([]*Feedback, error) {
	return queryAll(d, scanFeedback, "SELECT "+feedbackCols+" FROM feedback ORDER BY id")
}

// FeedbackByID is the feedback row with id, or nil.
func (d *DB) FeedbackByID(id int64) (*Feedback, error) {
	return queryOne(d, scanFeedback, "SELECT "+feedbackCols+" FROM feedback WHERE id = ?", id)
}

// AllRequests lists every request, oldest first.
func (d *DB) AllRequests() ([]*Request, error) {
	return queryAll(d, scanRequest, "SELECT "+requestCols+" FROM request ORDER BY id")
}

// nullable stores "" as NULL.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
