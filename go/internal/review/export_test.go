package review

import "testing"

func TestExportEncodeLayout(t *testing.T) {
	x := &ReviewExport{
		Format: ExportFormat, FormatVersion: 1, Generator: "T 1",
		Tables: map[string]*ExportTable{
			"review": {Columns: []string{"id", "title"}, Rows: [][]any{{"r1", nil}}},
			"meta":   {Columns: []string{"key", "value"}, Rows: [][]any{{"a", "<b>"}, {"c", int64(2)}}},
			"empty":  {Columns: []string{"id"}, Rows: [][]any{}},
		},
	}
	got, err := x.Encode()
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "format": "review-export",
  "format_version": 1,
  "generator": "T 1",
  "tables": {
    "empty": {
      "columns": ["id"],
      "rows": []
    },
    "meta": {
      "columns": ["key","value"],
      "rows": [
        ["a","<b>"],
        ["c",2]
      ]
    },
    "review": {
      "columns": ["id","title"],
      "rows": [
        ["r1",null]
      ]
    }
  }
}
`
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}

	none, err := (&ReviewExport{Format: ExportFormat, FormatVersion: 1, Generator: "T 1"}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"format\": \"review-export\",\n  \"format_version\": 1,\n  \"generator\": \"T 1\",\n  \"tables\": {}\n}\n"; string(none) != want {
		t.Errorf("no tables: got %q", none)
	}
}

func TestImportName(t *testing.T) {
	review := func(title any) *ReviewExport {
		return &ReviewExport{Tables: map[string]*ExportTable{"review": {Columns: []string{"id", "title"}, Rows: [][]any{{"0190-id", title}}}}}
	}
	for _, c := range []struct {
		x          *ReviewExport
		name, want string
	}{
		{review("My review: part 2"), "", "My-review-part-2"},
		{review(nil), "", "0190-id"},
		{review("x"), "shared.v1", "shared.v1"},
		{review("..-ok--"), "", "ok"},
		{review("!!!"), "", "review"},
		{review("Größe"), "", "Größe"},
	} {
		if got := importName(c.x, c.name); got != c.want {
			t.Errorf("importName(%v, %q) = %q, want %q", c.x.Tables["review"].Rows[0][1], c.name, got, c.want)
		}
	}
}
