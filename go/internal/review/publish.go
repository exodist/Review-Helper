package review

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// The publish list (standards/FORMAT.md, "Publish list"): the feedback to
// post to the pull or merge request under review, each comment's text final.

const PublishFormat = "review-publish"

// PublishList is what `feedback FILE --publish` prints.
type PublishList struct {
	Format        string         `json:"format"`
	FormatVersion int            `json:"format_version"`
	Review        string         `json:"review"`
	Source        *string        `json:"source"`
	Head          *string        `json:"head"`
	Items         []*PublishItem `json:"items"`
}

// PublishItem is one comment to post: a line comment at Anchor with
// Fallback as the general comment to post if the forge refuses it, or a
// general comment (Anchor and Fallback nil).
type PublishItem struct {
	ID       int64       `json:"id"`
	Text     string      `json:"text"`
	Anchor   *LineAnchor `json:"anchor"`
	Fallback *string     `json:"fallback"`
}

// LineAnchor is where a line comment goes.
type LineAnchor struct {
	Path string `json:"path"`
	Line int64  `json:"line"`
	Side string `json:"side"`
}

// PublishList lists the feedback to post: with no ids, each open item not
// posted yet, oldest first; otherwise the items with those ids, in that
// order, each once.
func (d *DB) PublishList(ids []int64) (*PublishList, error) {
	rows, err := d.publishRows(ids)
	if err != nil {
		return nil, err
	}
	rv, err := d.Review()
	if err != nil {
		return nil, err
	}
	latest, err := d.LatestIteration()
	if err != nil {
		return nil, err
	}
	lines, err := d.flagLines()
	if err != nil {
		return nil, err
	}
	list := &PublishList{Format: PublishFormat, FormatVersion: FeedbackFormatVersion, Review: rv.ID, Source: rv.Source, Items: make([]*PublishItem, 0, len(rows))}
	if latest != nil {
		list.Head = &latest.Head
	}
	for _, f := range rows {
		item, err := d.publishItem(f, lines, latest)
		if err != nil {
			return nil, err
		}
		list.Items = append(list.Items, item)
	}
	return list, nil
}

func (d *DB) publishRows(ids []int64) ([]*Feedback, error) {
	if len(ids) == 0 {
		return queryAll(d, scanFeedback, "SELECT "+feedbackCols+" FROM feedback WHERE status = 'open' AND published IS NULL ORDER BY id")
	}
	var rows []*Feedback
	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		f, err := d.FeedbackByID(id)
		if err != nil {
			return nil, err
		}
		if f == nil {
			return nil, fmt.Errorf("no such feedback item: %d", id)
		}
		rows = append(rows, f)
	}
	return rows, nil
}

func (d *DB) publishItem(f *Feedback, lines flagLines, latest *Iteration) (*PublishItem, error) {
	var flagRange *Range
	if f.Target == "flag" && f.Path != nil && f.Flag != nil {
		if r, ok := lines[flagKey{f.Iteration, *f.Flag}]; ok {
			flagRange = &r
		}
	}
	general := f.Body
	if prefix, ok := publishPrefix(f, flagRange); ok {
		general = prefix + "\n\n" + f.Body
	}
	item := &PublishItem{ID: f.ID, Text: general}

	anchor := lineAnchorOf(f, flagRange)
	if anchor == nil || latest == nil {
		return item, nil
	}
	fits, err := d.lineFits(latest, f, anchor.Side)
	if err != nil || !fits {
		return item, err
	}
	item.Text, item.Anchor, item.Fallback = f.Body, anchor, &general
	return item, nil
}

// lineAnchorOf is where a line comment for f would go: a line item's line,
// or a flag item's flag's first line on the new side; nil for any other.
func lineAnchorOf(f *Feedback, flagRange *Range) *LineAnchor {
	if f.Path == nil {
		return nil
	}
	switch {
	case f.Target == "line" && f.Line != nil && f.Side != nil:
		return &LineAnchor{Path: *f.Path, Line: *f.Line, Side: *f.Side}
	case f.Target == "flag" && flagRange != nil:
		return &LineAnchor{Path: *f.Path, Line: int64(flagRange[0]), Side: "new"}
	}
	return nil
}

// publishPrefix is the location prefix naming where f was given, as the
// format's prefix table has it; false for feedback on the whole change.
// flagRange holds a flag item's flag's lines, when it has them.
func publishPrefix(f *Feedback, flagRange *Range) (string, bool) {
	path, topic := deref(f.Path), deref(f.Topic)
	commit := deref(f.Revision)
	if len(commit) > 7 {
		commit = commit[:7]
	}
	var prefix string
	switch f.Target {
	case "line":
		old := ""
		if deref(f.Side) == "old" {
			old = "old side, "
		}
		return fmt.Sprintf("`%s:%d` (%scommit %s)", path, deref(f.Line), old, commit), true
	case "flag":
		switch {
		case f.Path != nil && flagRange != nil:
			lines := strconv.Itoa(flagRange[0])
			if flagRange[1] != flagRange[0] {
				lines += "-" + strconv.Itoa(flagRange[1])
			}
			return fmt.Sprintf("`%s:%s` (commit %s), on a flag", path, lines, commit), true
		case f.Path != nil:
			return fmt.Sprintf("`%s`, on a flag", path), true
		case f.Topic != nil:
			return fmt.Sprintf("topic `%s` (commit %s), on a flag", topic, commit), true
		}
		return fmt.Sprintf("commit %s, on a flag", commit), true
	case "section":
		return fmt.Sprintf("`%s`, section `%s`", path, deref(f.SectionName)), true
	case "review":
		switch {
		case f.Topic == nil:
			return "", false
		case topic == "":
			return "was on the files not in a topic yet", true
		}
		return fmt.Sprintf("was on topic `%s`", topic), true
	case "file":
		prefix = fmt.Sprintf("`%s`", path)
		if f.SectionName != nil {
			prefix += fmt.Sprintf(", was on section `%s`", *f.SectionName)
		}
	default: // commit
		prefix = "commit " + commit
		if f.Topic != nil {
			prefix = fmt.Sprintf("topic `%s` (commit %s)", topic, commit)
		}
	}
	if f.Flag != nil {
		prefix += ", was on a flag"
	}
	return prefix, true
}

// lineFits reports whether a line comment on f's path, on that side, counts
// in the same file as that side of the request's diff, as the review knows
// the request: its latest iteration.
func (d *DB) lineFits(latest *Iteration, f *Feedback, side string) (bool, error) {
	if side == "old" {
		it, err := d.Iteration(f.Iteration)
		if err != nil {
			return false, err
		}
		if it != nil && it.Squashed {
			return it.Base == latest.Base, nil
		}
	}
	revision, path := deref(f.Revision), deref(f.Path)

	var position sql.NullInt64
	if err := d.QueryRow("SELECT MIN(position) FROM commit_entry WHERE iteration = ? AND revision = ?", latest.Number, revision).Scan(&position); err != nil {
		return false, err
	}
	if !position.Valid {
		return false, nil
	}

	// Merged-in entries do not follow one another in position order: with
	// any, every other entry changing the path counts.
	var merges int
	if err := d.QueryRow("SELECT COUNT(*) FROM commit_entry WHERE iteration = ? AND merge_id IS NOT NULL", latest.Number).Scan(&merges); err != nil {
		return false, err
	}
	compare := ">"
	switch {
	case merges > 0:
		compare = "!="
	case side == "old":
		compare = "<"
	}

	// A file the item's entry renamed is at its old path before it.
	paths := []any{path}
	var renamed sql.NullString
	err := d.QueryRow(`SELECT file_change.old_path FROM commit_entry JOIN file_change ON file_change.commit_id = commit_entry.id
	 WHERE commit_entry.iteration = ? AND commit_entry.revision = ? AND file_change.path = ? AND file_change.old_path IS NOT NULL`,
		latest.Number, revision, path).Scan(&renamed)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if renamed.Valid {
		paths = append(paths, renamed.String)
	}
	in := strings.TrimSuffix(strings.Repeat("?, ", len(paths)), ", ")

	var changed int
	args := append(append([]any{latest.Number, revision, position.Int64}, paths...), paths...)
	err = d.QueryRow(`SELECT COUNT(*) FROM commit_entry JOIN file_change ON file_change.commit_id = commit_entry.id
	 WHERE commit_entry.iteration = ? AND commit_entry.revision != ? AND commit_entry.position `+compare+` ?
	   AND (file_change.path IN (`+in+`) OR file_change.old_path IN (`+in+`))`, args...).Scan(&changed)
	if err != nil {
		return false, err
	}
	return changed == 0, nil
}

// deref is the value p points at, or the zero value for nil.
func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
