# Installing the Go backend

A single binary, `review-helper-go`, with the review page, the agent skills
and the standards built in. Shared steps (the `review-helper` command,
linking the skills) are in the root [`INSTALL.md`](../INSTALL.md).

It runs on Linux, macOS and the BSDs, and on Windows through WSL (where it
is a Linux program). Windows itself is not supported yet: the binary does
not build there (its file locking uses `flock`), Go has no Cygwin target,
so under Cygwin or Git Bash it would be a Windows program too, and the
`review-helper` command would not run one named `review-helper-go.exe`.

## From a checkout

You need Go 1.26 or later. Go's `go:embed` cannot read files outside the
module or through symlinks, so a build first copies `ux/`, `skills/` and
`standards/` into `go/internal/share/files/` (gitignored, never committed).

With make, from the repository root:

    make go-install

installs `review-helper-go` where `go install` puts commands (`go env
GOBIN`, else `$(go env GOPATH)/bin`), with the `review-helper` command
beside it. Make sure that directory is on your `PATH`.

Without make, from `go/`:

    go generate ./...
    go build -o bin/review-helper-go ./cmd/review-helper-go

or `go install ./cmd/review-helper-go` in place of the build, then copy
`bin/review-helper` (at the repository root) onto your `PATH` beside it.

## `go install ...@latest` does not work

`go install github.com/exodist/Review-Helper/go/cmd/review-helper-go@latest`
builds from the module as it is in git, which holds no copies of the shared
files, so the build has nothing to embed and fails. Build from a checkout
as above instead.

## Tests

    make go-test

runs `go vet`, the unit tests, and the CLI conformance checks against the
built binary. `go/AGENTS.md` has the details.
