# perl/AGENTS.md

The Perl backend: the first implementation of the standard in `standards/`,
and the CPAN distribution `Review-Helper` (main module `Review::Helper`,
`lib/Review/Helper.pm`). Read the top-level `AGENTS.md` first. The
declarations in the top-level `AGENTS_OVERRIDE.md` (Perl version,
signatures, POD, test layout, perltidy) govern the code here.

Run every command below from `perl/`.

---

## Layout

- `lib/`, `t/`, `scripts/`, `dist.ini`, `Changes`, `.perltidyrc`,
  `TEMPLATE.pod`, `agent_scripts/`: as in any distribution.
- `scripts/review-helper-perl` is this implementation's command.
  `scripts/review-helper` is a symlink to the dispatcher, `bin/review-helper`
  at the top of the repository, so the distribution installs it too.
- `share/` holds committed symlinks to the top-level `ux/`, `skills/` and
  `standards/`. A checkout runs with no extra lookup logic: `Util::share_dir`
  finds `share/` beside `lib/`. `dist.ini` gathers with `follow_symlinks`,
  so the distribution ships the real files and an installed copy finds them
  through `File::ShareDir`.
- `AI_AND_LLM_POLICY.md` is a symlink to the top-level copy, gathered back
  into the distribution by name.

---

## Testing

```
~/projects/Agents/bin/agent-test-lock -- prove --timer -Ilib -j16 -r t/
```

The tests need git 2.32 or later: they isolate git from the user's
configuration with `GIT_CONFIG_GLOBAL` and create repositories with
`git init -b`.

- `t/integration/js-logic.t` runs the page's unit tests (`share/ux/t/`)
  with `node --test`; it skips without `node`.
- `t/integration/page-e2e.t` drives the page in headless Chrome against a
  real server over a generated review, with `conformance/page-drive.mjs`; it
  skips without `node` 22.13 or later and `google-chrome-stable` or
  `chromium`, and outside a checkout. It builds each conformance fixture
  with this backend (`conformance/README.md`, "Fixtures") and serves it with
  `scripts/review-helper-perl serve`; see that section to run the drive by
  hand (the checks add feedback and close the review). The drive fails itself after `REVIEW_HELPER_E2E_TIMEOUT` seconds
  (default 180); the test notes how long it took. Its whole-page wheel check
  runs only when `AUTHOR_TESTING` is set and not 0 (`agent-test-lock` sets
  it).
- `t/integration/cli-conformance.t` runs the CLI conformance checks
  (`conformance/cli/`) against this checkout's `review-helper-perl`, through
  the dispatcher; it skips without `node`, and outside a checkout.
- `t/integration/dispatcher.t` tests `scripts/review-helper` with fake
  implementations on `PATH`.

---

## Project-specific pre-review gates

Beyond the passes in `~/projects/Agents/AGENTS.md`:

```
perl agent_scripts/audit-test-layout --mode strict --namespace Review::Helper \
    --manual-namespace Review::Helper::Manual
```

---

## Changes

`Changes` holds a single "Initial release" entry until the first release.
Add entries only after it: `Changes` tells people upgrading between
releases what changed, and nothing before the first release is meaningful
to them.

---

## Architecture quick-reference

Foundational rules an agent must internalise before writing any code here:

- `use v5.36;` in every module and test; signatures everywhere they fit.
- `Object::HashBase` for objects; `Role::Tiny` for roles. They compose.
  `Review::Helper::VCS` is the one role: every repository access goes
  through it, and only `Review::Helper::VCS::Git` names git.
- `parent` for inheritance, not `base`.
- Web layer is Plack with `Router::Simple`, serving the page from
  `share/ux/` and the HTTP API in `standards/API.md`.
- Tests use `Test2::V0`.
