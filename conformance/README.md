# Conformance

Checks that any backend of the standard (`standards/`) can run, whatever
language it is written in: the CLI checks and the page drive.

## The CLI checks

`cli/` checks the command line the skills rely on
([CLI.md](../standards/CLI.md)) and the documents it reads and writes
([FORMAT.md](../standards/FORMAT.md)): every required command, its output,
exit status and errors, the fill sheet, feedback and the publish list, and
export and import. Each test builds scratch git repositories with plain `git`,
and runs only `bin/review-helper`, so the dispatcher picks the implementation;
feedback is written through the HTTP API, as the page writes it.

Put the implementation on `PATH` as `review-helper-<impl>`, then:

    REVIEW_HELPER_IMPL=<impl> node --test conformance/cli/

Needs node 18 or later and git 2.32 or later. `REVIEW_HELPER_IMPL` may be left
out when the implementation is the first the dispatcher finds. The checks run
git with a configuration of their own, in scratch directories outside any
repository, and clear the `REVIEW_HELPER_*` settings but `REVIEW_HELPER_IMPL`,
so the caller's setup does not leak in. The feedback checks serve a review on
loopback with `REVIEW_HELPER_REVIEWER` set, as the local access layer allows
([API.md](../standards/API.md), "The local access layer"); each server runs in
a process group of its own and is stopped with it. Each check has a time
limit. The checks test only what the standard states: when it leaves open
something a check needs, the standard is made precise, not the check fitted to
one backend.

## The page drive

`page-drive.mjs` opens the page (`ux/`) in headless Chrome against a running
server of the HTTP API ([API.md](../standards/API.md)) and checks its
interactions. It knows nothing about the backend: it takes the URL the server
printed.

    node conformance/page-drive.mjs [--topics] URL [screenshot-dir]

It prints TAP and exits non-zero when a check fails. Needs `node` and Chrome
or Chromium.

- `CHROME` names the browser binary (default `google-chrome-stable`).
- `REVIEW_HELPER_E2E_TIMEOUT` is the seconds the whole run may take before it
  fails itself (default 180).
- `AUTHOR_TESTING` set and not `0` adds a slow whole-page wheel-scroll check.
- `screenshot-dir`, when given, receives screenshots of the main states.

The drive writes to the review (it adds feedback and requests, marks flags
seen, and closes the review), so give it a fresh fixture each run.

## Fixtures

The drive expects one of two fixture reviews, each over its own throwaway
git repository:

- the history review (no option): two iterations over several commits, with
  sections, flags of both colors, journal entries, a guide, feedback from
  two reviewers, an image, a binary file and merges;
- the topics review (`--topics`): a squashed review of a small codebase split
  into the topics "Perl backend" and "Frontend".

The server must name its reviewer `Tess Tester <tess@example.com>`.

Today the fixtures are built, and served, only by the test setup of the
first backend; that backend's own instructions say how to run it, and its
setup is the authoritative description of the fixtures' content. Another
backend can run the drive against the same fixtures once they can be written
without being served.
