package review

import "testing"

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
