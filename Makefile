# Build, test and install each backend with its own tools; see INSTALL.md.
# Every backend has the same targets: <lang> prepares a checkout to build
# (nothing, for Perl), <lang>-build, <lang>-test and <lang>-install.

.POSIX:

GO   = go
NODE = node

all: go-build

# Go embeds copies of ux/, skills/ and standards/, made by go generate.
go:
	cd go && $(GO) generate ./...

go-build: go
	cd go && $(GO) build -o bin/review-helper-go ./cmd/review-helper-go

go-test: go-build
	cd go && $(GO) vet ./... && $(GO) test ./...
	PATH="$$PWD/go/bin:$$PATH" REVIEW_HELPER_IMPL=go $(NODE) --test conformance/cli/

# The Go backend, and the review-helper dispatcher that runs it, go where
# go install puts commands.
go-install: go
	cd go && $(GO) install ./cmd/review-helper-go
	bin="$$($(GO) env GOBIN)"; [ -n "$$bin" ] || bin="$$($(GO) env GOPATH)/bin"; \
	cp bin/review-helper "$$bin/review-helper"

perl:

perl-build:
	cd perl && dzil build

perl-test:
	cd perl && prove -lr t

perl-install:
	cd perl && dzil install

.PHONY: all go go-build go-test go-install perl perl-build perl-test perl-install
