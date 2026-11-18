# go/AGENTS.md

The Go backend: a second implementation of the standard in `standards/`,
built as one binary, `review-helper-go`, that embeds the page (`ux/`), the
agent skills (`skills/`) and the standards. Read the top-level `AGENTS.md`
first. Run every command below from `go/` unless it says otherwise.

---

## Layout

- `cmd/review-helper-go/`: the command; it only calls `review.Run`.
- `internal/git/`: every git command, ref name and git path the backend
  uses, as concrete functions on a `Repo`. Nothing else runs git.
- `internal/review/`: everything else, one file per area: `db.go` and
  `models.go` (the review file), `journal.go`, `registry.go`, `diff.go` and
  `split.go`, the builder, the fill sheet, feedback and exports, the HTTP
  server, and `cli.go` (the command line).
- `internal/share/`: the embedded shared files. `go generate ./...` runs
  `gen`, which copies `ux/html`, `ux/css`, `ux/js`, `skills/` and
  `standards/` into `internal/share/files/` (gitignored): `go:embed` reads
  neither symlinks nor files outside the module (`RULINGS.md`, 2026-11-11
  layout ruling). Run it before building, after any change to those files.

Add a package only where a real boundary shows up, and an interface only
when a second implementation exists.

---

## Conventions

- **Go, not Perl in Go.** The standards are the contract; the Perl backend
  (`perl/lib/`) shows behavior where they are silent. Write native Go:
  typed structs for rows and JSON (a nullable column is a pointer), errors
  returned and wrapped with `%w` in lowercase, small functions, the
  standard library where it has the tool. Owner, 2026-11-18: "If the
  results is basically forcing a perl shape into a go source then we are
  doing it wrong."
- Dependencies: the standard library and `modernc.org/sqlite` (SQLite in
  pure Go, so no C compiler and easy cross-builds). Nothing else without
  the owner.
- No commands, options, routes or fields beyond the standard; an extension
  would be named `x-go-...` (`standards/CLI.md`, "Extensions").
- Windows is supported only through WSL: the binary does not build for
  Windows (file locking uses `flock`), and the `review-helper` command is a
  POSIX `sh` script that would not run a `.exe` (`PLANNED_TODO.md`, "More
  backends").
- `doc_html` (rendered documentation beside a section) is not implemented;
  it is optional (`standards/API.md`, `GET /api/file/ID`), and the page then
  shows the documentation's lines.

---

## Testing

    go generate ./... && go vet ./... && go test ./...
    go build -o bin/review-helper-go ./cmd/review-helper-go

then, from the repository root, the conformance checks against that binary:

    PATH="$PWD/go/bin:$PATH" REVIEW_HELPER_IMPL=go node --test conformance/cli/

and the page drive over both fixtures (`conformance/README.md`,
"Fixtures"), built and served by this backend. `make go-test` runs all but
the page drive.

The conformance checks are the behavioral contract. Unit tests cover what
they cannot reach or pin precisely: diff parsing, attribution and section
hunks (`diff_test.go`), the `rev` hash and line ranges (`sheet_test.go`),
a split's refusals and layout (`fill_split_test.go`), topics and guide
checks (`fill_topics_test.go`, `fill_guide_test.go`), merge nesting and
flag line moves (`builder_test.go`), the publish prefixes and line fit
(`publish_test.go`), the export layout (`export_test.go`), import names
(`import_test.go`), and the access layer's host check, token and static
paths (`server_test.go`). A test lives in the `_test.go` file of the code
it tests. A behavior every backend must
have, that conformance misses, is added to `conformance/`.

Gates for every change: `gofmt -l .` prints nothing, `go vet ./...` is
clean, and the tests above pass.
