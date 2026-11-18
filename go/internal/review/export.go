package review

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// The review export (standards/FORMAT.md, "Export"): a review shared as
// JSON text. Its rows are generic, every column of every table, so a table
// is a column list and rows of plain values.

const (
	ExportFormat        = "review-export"
	ExportFormatVersion = 1
)

// exportExcluded are the tables each reviewer keeps their own of.
var exportExcluded = map[string]bool{"feedback": true, "request": true, "flag_seen": true}

// ReviewExport is a review export.
type ReviewExport struct {
	Format        string                  `json:"format"`
	FormatVersion int                     `json:"format_version"`
	Generator     string                  `json:"generator"`
	Tables        map[string]*ExportTable `json:"tables"`
}

// ExportTable is one table: its columns in table order and its rows, each
// value a string, a number (int64 or float64) or nil.
type ExportTable struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// exportedTables lists the schema's tables an export holds, in schema order.
func exportedTables() ([]string, error) {
	names, err := SchemaTables()
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(names, func(n string) bool { return exportExcluded[n] }), nil
}

// Export builds the review's export.
func (d *DB) Export() (*ReviewExport, error) {
	names, err := exportedTables()
	if err != nil {
		return nil, err
	}
	x := &ReviewExport{Format: ExportFormat, FormatVersion: ExportFormatVersion, Generator: Generator, Tables: map[string]*ExportTable{}}
	for _, name := range names {
		t, err := exportTable(d, name)
		if err != nil {
			return nil, err
		}
		x.Tables[name] = t
	}
	return x, nil
}

// tableColumn is a column as PRAGMA table_info describes it.
type tableColumn struct {
	Name string
	PK   int // its place in the primary key, from 1; 0 when not in it
}

func tableColumns(q querier, table string) ([]tableColumn, error) {
	rows, err := q.Query("SELECT name, pk FROM pragma_table_info(?) ORDER BY cid", table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []tableColumn
	for rows.Next() {
		var c tableColumn
		if err := rows.Scan(&c.Name, &c.PK); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

func isExtension(name string) bool { return strings.HasPrefix(name, "x_") }

// exportTable reads every row of a table but its extension columns, by
// primary key, or by rowid when it has none.
func exportTable(q querier, name string) (*ExportTable, error) {
	info, err := tableColumns(q, name)
	if err != nil {
		return nil, err
	}
	t := &ExportTable{Columns: []string{}, Rows: [][]any{}}
	var pk []tableColumn
	for _, c := range info {
		if !isExtension(c.Name) {
			t.Columns = append(t.Columns, c.Name)
		}
		if c.PK > 0 {
			pk = append(pk, c)
		}
	}
	sort.Slice(pk, func(i, j int) bool { return pk[i].PK < pk[j].PK })
	order := "rowid"
	if len(pk) > 0 {
		keys := make([]string, len(pk))
		for i, c := range pk {
			keys[i] = quoteIdent(c.Name)
		}
		order = strings.Join(keys, ", ")
	}
	cols := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		cols[i] = quoteIdent(c)
	}
	rows, err := q.Query("SELECT " + strings.Join(cols, ", ") + " FROM " + quoteIdent(name) + " ORDER BY " + order)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		row := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range row {
			ptrs[i] = &row[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		for i, v := range row {
			if b, ok := v.([]byte); ok {
				row[i] = string(b)
			}
		}
		t.Rows = append(t.Rows, row)
	}
	return t, rows.Err()
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// Encode writes the export in its layout: keys sorted, one row per line,
// so exports diff well.
func (x *ReviewExport) Encode() ([]byte, error) {
	var b bytes.Buffer
	enc := func(v any) error {
		j, err := MarshalJSON(v)
		b.Write(j)
		return err
	}
	// The top-level keys, in sorted order.
	b.WriteString("{\n  \"format\": ")
	if err := enc(x.Format); err != nil {
		return nil, err
	}
	b.WriteString(",\n  \"format_version\": ")
	if err := enc(x.FormatVersion); err != nil {
		return nil, err
	}
	b.WriteString(",\n  \"generator\": ")
	if err := enc(x.Generator); err != nil {
		return nil, err
	}
	b.WriteString(",\n  \"tables\": ")
	if len(x.Tables) == 0 {
		b.WriteString("{}\n}\n")
		return b.Bytes(), nil
	}
	names := make([]string, 0, len(x.Tables))
	for n := range x.Tables {
		names = append(names, n)
	}
	sort.Strings(names)
	b.WriteString("{\n")
	for i, n := range names {
		t := x.Tables[n]
		if i > 0 {
			b.WriteString(",\n")
		}
		b.WriteString("    ")
		if err := enc(n); err != nil {
			return nil, err
		}
		b.WriteString(": {\n      \"columns\": ")
		if err := enc(t.Columns); err != nil {
			return nil, err
		}
		b.WriteString(",\n      \"rows\": ")
		if len(t.Rows) == 0 {
			b.WriteString("[]")
		} else {
			b.WriteString("[\n")
			for j, row := range t.Rows {
				if j > 0 {
					b.WriteString(",\n")
				}
				b.WriteString("        ")
				if err := enc(row); err != nil {
					return nil, err
				}
			}
			b.WriteString("\n      ]")
		}
		b.WriteString("\n    }")
	}
	b.WriteString("\n  }\n}\n")
	return b.Bytes(), nil
}

// otherVersion explains an export another version of the tool built.
func otherVersion(why string, args ...any) error {
	return fmt.Errorf("this review export was built by a different version of review-helper; export it again with this version, or import it with the version that built it (%s)", fmt.Sprintf(why, args...))
}

func runExport(args []string, out, warn io.Writer) error {
	var outPath string
	o := newOptions()
	o.path("out", &outPath)
	rest, err := o.parse(args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errors.New("usage: review-helper export FILE [--out P]")
	}
	d, err := OpenDB(rest[0])
	if err != nil {
		return err
	}
	defer d.Close()
	x, err := d.Export()
	if err != nil {
		return err
	}
	text, err := x.Encode()
	if err != nil {
		return err
	}
	if !o.given["out"] {
		_, err = out.Write(text)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o777); err != nil {
		return err
	}
	return os.WriteFile(outPath, text, 0o666)
}
