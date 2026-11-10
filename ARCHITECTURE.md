# ARCHITECTURE.md

The authoritative design of Review::Helper. Code follows this document. When
code and this document disagree, stop and flag it; a deliberate change lands
here in the same commit as the code.

`project` is the owner's original brief; this file supersedes it wherever they
differ. The reasons behind contested choices are in `RULINGS.md`.

---

## 1. What it is

A local web app that helps a human review code an AI/LLM agent wrote. After an
agent finishes a change (commit, branch, PR), it builds a **review file**
describing the change and fills in explanations. The human runs
`review-helper <review-file>`, reads the change top-down in a browser, leaves
feedback, and flags spots for deeper agent work. Agents read the feedback back
out, act on it, and add a new **iteration** to the same file.

While writing the code, the agent records notes — wrong assumptions,
deviations, surprises — that become red and yellow **flags** on the lines they
concern. It also orders each commit's files for reading and prepares a
**guide**: a short, step-by-step presentation of the change.

The human should be able to judge design and concepts from summaries, and open
a diff or the code only when details matter or a summary looks wrong.

Principles:

- **Git is the source of content.** The review file holds only review data;
  diffs, file contents and images are read live from the repository under
  review (`RULINGS.md`).
- **Agents never touch the database or refs directly.** Everything goes
  through `review-helper` subcommands, so one narrow permission rule covers
  every skill.
- **Scripts do the mechanical work; agents write only explanations** (what,
  why, how) and the splits a parser cannot do.
- **Local and single-user.** One human, one browser, one machine.
- **KISS.** New options, layers and edge-case support need a real use first.

Scope: committed changes only. Initial language targets are Perl,
JavaScript, CSS, HTML and Markdown (including agent `.md` files). Every file in
a commit is listed whatever its type; images are rendered.

---

## 2. The review file

### Location

One SQLite file per review, inside the repository under review.

- Default: `$(git rev-parse --git-path review-helper)/<name>.sqlite`. That
  path is per-worktree (`.git/review-helper/` in the main checkout,
  `.git/worktrees/<wt>/review-helper/` in a worktree), so a review belongs to
  the worktree it was built in and never dirties a working tree.
- Any path inside a worktree is also accepted. If the file is tracked or not
  ignored (`git check-ignore`), the CLI warns; it never edits ignore files.
- The repository is found from the file's location (`git -C <dir>
  rev-parse`). No repository path is stored, so the file works in any clone
  it sits in.

### Pinned refs

Iterations usually follow rebases and amends, which leave earlier commits
unreachable and eventually garbage-collected. Each iteration pins its head:

```
refs/review-helper/<review-id>/<iteration-number>
```

These live in the shared ref store, so gc in any worktree honors them.

### Registry

`$(git rev-parse --git-common-dir)/review-helper/registry` (JSON) maps each
review id to its file path and open/closed state. `build` registers; `close`
marks closed. It exists so `prune` can find orphans — reviews whose file is
gone, e.g. with a removed worktree — without scanning every worktree.

### Journal

`$(git rev-parse --git-path review-helper)/journal.jsonl`, per-worktree like
review files. `review-helper note` appends one JSON line per note while the
agent works; no review file is needed yet. Notes leave the journal when `fill`
places them into a review (§7).

### Cleanup

`review-helper prune [--dry-run]` deletes the refs of closed reviews and of
registered reviews whose file no longer exists, and drops those registry
entries. It takes no ref or path arguments, never prompts, is idempotent, and
never deletes review files. That makes it safe to allow outright. The
`review-build` skill runs it before every build; `review-cleanup` runs it on
demand.

### Fixed diff options

Explanations and feedback anchor to line numbers, so every diff is produced
the same way regardless of configuration. Git runs with
`GIT_CONFIG_NOSYSTEM=1` and `GIT_CONFIG_GLOBAL=/dev/null`, and with explicit
options that override repository config:

```
git diff --no-ext-diff --no-color --no-textconv --indent-heuristic \
    --diff-algorithm=histogram --find-renames -U3 \
    --src-prefix=a/ --dst-prefix=b/ ...
```

Changing these options is a schema-level change: it moves every anchor.

### Contents

Tables, in outline (exact DDL lives in `Review::Helper::DB`, versioned by a
`schema_version` row with forward-only migrations):

| Table | Holds |
|---|---|
| `review` | id (UUID), title, created, state (`open`/`closed`). |
| `iteration` | number, base sha, head sha, pinned ref, created, note (what this iteration changed). |
| `summary` | Per iteration: the top-level agent summary — what, why, how. |
| `commit_entry` | Per iteration: sha, position, subject, message, patch-id, agent summary. |
| `file_change` | Per commit: path, old path, reading position, status (A/D/M/R), kind (perl/js/css/html/markdown/text/image/binary), lines or bytes before/after, added/removed/changed counts, split source (`script`/`agent`/`agent-checked`), agent file summary, object system, paradigms. |
| `section` | Per file change: kind (`unit`/`top-level`/`end`), name, package, code line ranges, doc line ranges, old-side ranges for deleted units, counts, agent summary. |
| `feedback` | Target (review/commit/file/section/line), iteration, commit sha, path, line, side (old/new), body, created, status (`open`/`addressed`/`declined`), agent response, iteration that resolved it. |
| `request` | Kind (`debate`/`split`/`check-split`), target, human note, status, results (debate: the case for, the case against). |
| `flag` | Color (`red`/`yellow`), kind (`assumption`/`deviation`/`test-revealed`/`self-review`/`interest`), anchor (commit sha + path + line range, or a whole commit or file), sentence, brief, detail. |
| `guide_step` | Per iteration: position, target, optional highlight ranges (path, side, lines), commentary. |

Feedback and flags anchor to commit sha + path + line in that commit's blob,
never to a section id. Blobs are immutable, so anchors survive re-splits and iterations.

---

## 3. Building a review

### Inputs

```
review-helper build --base <rev> [--head <rev>] [--title <text>] [--file <path>]
```

`--base` is required; `--head` defaults to `HEAD`. The commits reviewed are
`git rev-list --reverse --topo-order base..head`, first commit first. A
branch or PR is just a range.

### Per commit

1. Record sha, subject, message, `git patch-id --stable`.
2. For each changed path: status, kind (by extension, then shebang for Perl),
   before/after sizes. Binary and image detection follows `git diff
   --numstat` (`-` counts).
3. Parse the diff into hunks; count lines (below).
4. Split the after-file (and the before-file, for deleted units) into
   sections (§4); attribute hunk lines to sections; keep only sections the
   commit touched.

Line counts, per hunk: each run of `-` lines followed by `+` lines counts
`min(-, +)` as changed and the remainder as removed or added. A `-` line is
attributed to the section holding the next new-side line.

### Iterations

`review-helper iterate <file> [--base <rev>] [--head <rev>]` adds iteration
N+1 (base defaults to the previous base, head to `HEAD`) and pins it.
Explanations, reading order and flags carry forward for any commit whose
patch-id matches one in the previous iteration, and section explanations for
sections whose content is unchanged. Everything else is left empty for the
sheet. Each iteration gets its own guide; none carries forward. Earlier iterations stay viewable for as long
as their pinned refs exist.

---

## 4. Sections

A file change splits into sections. The tree and the page list only sections
the commit touched; "view file" shows the whole file (§5).

### Kinds

- **Named units** — subs, methods, functions; found by a parser.
- **`[top-level]`** — every line not in a named unit and not in `[__END__]`:
  header, code between units, footer, loose statements anywhere. Usually
  several disjoint ranges. Every language has it.
- **`[__END__]`** — Perl only: everything after `__END__` or `__DATA__`,
  minus documentation grouped into units.

Other languages gain specials only when a real need appears.

### Documentation grouping

Documentation goes with a unit only when it unambiguously documents that unit;
otherwise it stays where it is. Expanding a unit shows its code and its
documentation together, and doc changes count toward the unit.

- **Perl:** a POD heading (`=head2`–`=head4`) or `=item` whose text names the
  unit as a declaration or call — `foo`, `foo(...)`, `$obj->foo(...)`,
  `Class->foo(...)`, `$val = $obj->foo(...)` — through the next heading of the
  same or higher level, or `=back`. Applies under `__END__` and before it.
  Method lists, examples, and passing mentions are not grouped.
- **JavaScript:** a `/** ... */` block directly above the unit, separated only
  by blank lines.

### Splitters

| Kind | Splitter | Named units |
|---|---|---|
| Perl | `Split::Perl` over PPI | `sub` and `method` declarations. |
| JavaScript | `Split::JavaScript`, a brace-tracking tokenizer | `function name(`; `class Name {` members (`static`/`async`/`get`/`set`/`#private` prefixes); `const`/`let`/`var name =` function or arrow; `export` forms of these. |
| Markdown | `Split::Markdown` | Headings. |
| CSS, HTML, other text | none | `[top-level]` only, unless an agent splits it. |
| Image, binary | none | No sections; images render, binaries list only. |

`Split::Perl` must not rely on `PPI::Statement::Sub`: PPI does not understand
`method`, and an unterminated `method` statement swallows the following
`sub`. It matches `sub|method NAME [signature/prototype] [attributes] BLOCK`
inside every statement (`RULINGS.md`).

`Split::JavaScript` skips strings, template literals, comments and regex
literals, deciding regex-versus-division from the preceding token. A misread
lands code in `[top-level]` or gives a bad range; the agent re-split covers
that.

### Agent splits

- Files with no parser may be split by the agent through the sheet without
  being asked.
- For any file, the human can request `split` (agent replaces the split) or
  `check-split` (agent confirms or replaces it). The agent never overrides a
  parser split unasked.
- `fill` validates agent splits: ranges inside the file, no overlaps;
  uncovered lines are `[top-level]`.
- The UI labels each file's split as script, agent-checked, or agent.
- A re-split keeps feedback (line-anchored) but drops the explanation of any
  section whose ranges changed; the sheet lists them again.

---

## 5. The page

One static HTML shell plus vanilla JavaScript over a JSON API. No template
engine, no jQuery, no JS framework or build step (`RULINGS.md`). Look and
theme follow Test2-Harness-UI (`~/projects/Test2/Test2-Harness-UI/share/css/`).

### Layout

- **Main column** — one long page. First the review summary: short paragraphs
  and bullet lists, little redundancy. Then a page break and one section per
  commit, in order. Each section is meant to fit a tablet or monitor without
  scrolling; that is guidance for the agent writing it, not an enforced limit.
- **Left tree** — `summary`, then each commit. Resizable, collapsible, and
  restorable. As the page scrolls (`IntersectionObserver`), the current
  section's node expands to its files and the tree scrolls to keep it in
  view. Clicking a node jumps to its section.

### Drilling right

Each commit section is a horizontal stack: left is higher level, right is
deeper.

- Clicking a file replaces the commit's view with that file's summary and
  expands the tree to the file's sections. Opening a second file stacks the
  two vertically within the commit's section.
- Clicking a section (unit or special) shows its agent summary, with its
  documentation alongside.
- Clicking a parent — in the section's breadcrumb or the tree — collapses
  back left. The browser back button does the same.
- Each section remembers its own open path. The full view state lives in the
  URL fragment, so back, forward and reload restore it.

### Per-item widgets

Every tree node and every open section carries:

- **diff** — the diff at that level (commit, file, or section ranges);
- **view file** — the whole file after the commit, syntax highlighted;
- **feedback** — add a comment at that level;
- **request** — flag a debate (and, for files, a split or split check).

Unified and side-by-side diff are a global toggle, remembered per browser in
`localStorage`.

### Colors

New files green, deleted red, files that shrank blue, files that grew purple.
Size is line count (bytes for binaries); unchanged size is uncolored.

### Highlighting and diffs

- highlight.js (BSD-3), vendored in `share/js/` with its license, languages
  limited to Perl, JavaScript, CSS, HTML/XML and Markdown.
- Whole files are highlighted once, then split into lines, so heredocs, POD
  and template literals highlight correctly. Diff rows pick lines by number.
- Diff rendering is ours: two table layouts (unified, side-by-side) over the
  server's hunk JSON. Each line is its own row, so line feedback can attach to
  any of them.

### Reading order

The agent orders each commit's files so a file comes after the files needed to
understand it. A global **Reading order | A–Z** toggle (default reading order,
remembered in `localStorage`) orders tree nodes and stacked file views. Until
the agent fills it, reading order falls back to A–Z and the toggle shows it as
pending. Commits stay in commit order; sections stay in file order.

### Flags

- Diff and file views show a flag icon in the gutter of flagged lines, in the
  flag's color. Hovering shows the one-sentence explanation; clicking opens a
  popup with the brief summary and an expander for the detail.
- Tree nodes (commit, file, section) carry a flag count badge in the worst
  color they contain.
- The review summary lists every flag, linking to each.

### Guide

The agent's presentation of the iteration: an ordered list of steps.

- **Step target:** the review summary, a commit, a file, a section, a flag, a
  diff (commit, file or section), or "view file" at a line range. Optional
  **highlight** ranges mark the exact lines the step talks about.
- **Commentary:** one or two short sentences, at most 300 characters, written
  to be read aloud — it says "the highlighted loop" or "the Builder module",
  never code tokens. The guide may skip commit overviews it already covers and
  goes into diffs and source only at points of interest.
- **Playing drives the real page.** A step sets the same view state a click
  would (tree, sideways sections, diff), then scrolls to the target and marks
  highlighted lines with a tinted background and gutter bar. Steps change the
  URL, so the browser's back button follows them too.
- **Guide bar** pinned at the bottom of the main column: commentary, step
  `n / N`, back, play, next, speech toggle, exit. Play enters guide mode at
  step 1; from then on only next and back move. Nothing advances on its own.
  Exploring the page by hand does not leave guide mode; next resumes from the
  current step. Reading order does not affect the guide.
- Entry: a play button in the header and a **Guide** button in the review
  summary; hidden when the iteration has no guide.

### Speech

Optional narration of guide commentary, generated by the browser's Web Speech
API (`speechSynthesis`). No server work, no stored audio, no agent tokens.

- Toggle in the guide bar, off by default; hidden when the browser offers no
  voices.
- When on, each step's commentary is read when the step is shown; changing
  step cancels the current reading.
- Voice picker lists on-device voices (`localService`) first and defaults to
  the first one matching the page language. Online voices are labeled
  "(online: text leaves this machine)".
- Speed 0.75–1.5x. Toggle, voice and speed are remembered in `localStorage`.

### Summaries shown

- Review, commit: what, why, how.
- File: what changed, object system (Moose, Object::HashBase, …), paradigms
  (OOP, exporter, functional, …), added/removed/changed lines.
- Section: what changed, plus the same facts where they differ from the file,
  and its counts.

### Iterations, feedback, requests

- An iteration selector at the top; the latest is the default.
- Feedback shows inline where it anchors, with status and the agent's
  response. Feedback from earlier iterations shows its status and, when
  addressed, the iteration that resolved it.
- Debate results show under the item they target: the case for and the case
  against, side by side.
- **Download feedback** returns the same JSON as `review-helper feedback`.
- **Close review** sets the review closed and notes that old iterations may
  lose their diffs after `prune`.

---

## 6. Server

- Plack app with `Router::Simple`, run under Plack's `HTTP::Server::PSGI`.
- Binds `127.0.0.1` on an ephemeral port and prints the URL. `--port` fixes
  the port; `--open` launches the browser (`xdg-open` on Linux, `open` on
  macOS; elsewhere the URL is only printed).
- A random per-run token is part of the printed URL; the first request trades
  it for a cookie, and every API request needs it. Requests whose `Host` is
  not `127.0.0.1` or `localhost` are refused. This keeps other local users and
  rebinding web pages out of the repository and the feedback.
- Git reads run through list-form pipes, never a shell.

### Routes

```
GET  /                              index.html
GET  /static/*                      share/ assets
GET  /api/review                    review, iterations, summary, commit list, flags, guide
GET  /api/commit/:sha               commit detail, file changes
GET  /api/file/:sha/*path           file change detail, sections
GET  /api/diff/:sha/*path           hunks JSON; ?section=<id> limits to its ranges
GET  /api/blob/:sha/*path           raw file content (text, image)
GET  /api/feedback                  feedback export JSON
POST /api/feedback                  add feedback
POST /api/feedback/:id              edit or change status
POST /api/request                   add a debate/split request
POST /api/close                     close the review
```

---

## 7. CLI

One command, `review-helper`. Options parsed with Getopt::Long.

```
review-helper <file> [--port N] [--open]   serve (shorthand for `serve`)
review-helper serve <file> ...
review-helper build --base <rev> [--head <rev>] [--title <t>] [--file <path>]
review-helper iterate <file> [--base <rev>] [--head <rev>]
review-helper note --flag red|yellow --kind <kind> --path <p> [--unit <name>]
    [--snippet <text>] --summary <sentence> [--brief <text>] [--detail <text>]
                                           append a note to the journal
review-helper sheet <file>                 print the fill sheet
review-helper fill <file> <sheet.json>     validate and import a filled sheet
review-helper feedback <file> [--open]     print feedback JSON
review-helper close <file>
review-helper list                         registered reviews in this repository
review-helper prune [--dry-run]
review-helper skills install [--copy|--link]
review-helper skills update
review-helper skills path
review-helper skills check <skill-name>
```

A first argument that is a known subcommand is a subcommand; anything else is
a review file.

### The fill sheet

`sheet` prints JSON listing every item still needing agent text:

- review summary; commit summaries; file summaries with object system and
  paradigms; section summaries;
- splits for files with no parser, or with a `split`/`check-split` request;
- reading order for each commit;
- placement of each journal note (commit + path + line range, or a whole
  commit or file), using its path, unit and snippet hints;
- the iteration's guide;
- responses (status and text) for open feedback;
- results for open requests.

Each item carries an id, a revision hash of the data it describes, and
pointers (commit, path, section name, line ranges). It carries no code; the
agent reads code through git.

`fill` validates the whole sheet before writing anything and rejects, with a
message naming the item:

- unknown ids, wrong field types, stale revisions;
- split or flag ranges outside the file, overlapping split ranges;
- a reading order that is not exactly the commit's paths, once each;
- guide steps whose target or highlight does not exist in the iteration;
- guide commentary over 300 characters or containing code tokens: backticks,
  `->`, `::`, `()`, or a sigil (`$`, `@`, `%`) followed by a word character.
 A valid sheet is written in one transaction. Partial sheets
are fine; `fill` reports what remains empty, so agents can work in batches.

---

## 8. Skills

Five skills ship in `share/skills/<name>/SKILL.md`:

| Skill | Does |
|---|---|
| `review-notes` | Read before writing any code. While working, record a note with `review-helper note` whenever an assumption proves wrong, the work deviates from a plan or spec, a test exposes a misunderstanding, self-review corrects something, or a point deserves the reviewer's attention. Red for "check this", yellow for "worth knowing". |
| `review-build` | `prune`, `build`, then fill the sheet until nothing remains: explanations, reading order, note placement, and the guide. Covers writing commentary for speech: refer to highlighted code, never quote it. |
| `review-feedback` | Read `feedback`, change the code as new commits, `iterate`, then fill responses and new explanations. |
| `review-requests` | Handle open requests: dispatch two sub-agents for each debate (one defending the target, one arguing against it or for an alternative) and record their cases; perform `split` and `check-split`. |
| `review-cleanup` | `prune`, and report what it removed. |

Skills run only `review-helper` subcommands and read-only `git` commands.
Where the harness supports it, each skill's frontmatter grants exactly that.

Every skill starts with `review-helper skills check <name>`. On a version
mismatch, the agent tells the user and carries on.

### Install

- `skills install` — default `--copy`; `--link` symlinks instead. Targets
  `~/.claude/skills` and `~/.agents/skills`.
- Each installed skill directory gets a `.review-helper` marker: mode,
  installing version, and per-file checksums for copies. Install and update
  touch only directories carrying the marker, and refuse to overwrite a
  locally edited copy without `--force`.
- Copies get `review-helper-version: <version>` written into their
  frontmatter. Links are always current and carry none.
- `skills update` refreshes copies and re-points broken links.
- `skills check <name>` compares the skill's stamp to the installed version and
  prints one line when they differ, in either direction. It always exits 0.
- `skills path` prints the shipped directory.

---

## 9. Code layout

```
lib/Review/Helper.pm                 version, overview POD
lib/Review/Helper/CLI.pm             subcommand dispatch
lib/Review/Helper/Repo.pm            git: locate repo, fixed-option diffs, blobs, refs
lib/Review/Helper/DB.pm              connection, schema, migrations, txn helper
lib/Review/Helper/Registry.pm        common-dir registry; prune
lib/Review/Helper/Journal.pm         note recording; unplaced notes for the sheet
lib/Review/Helper/Builder.pm         build and iterate
lib/Review/Helper/Diff.pm            diff text -> hunks; line counts
lib/Review/Helper/Split.pm           pick a splitter by kind; section attribution
lib/Review/Helper/Split/Perl.pm
lib/Review/Helper/Split/JavaScript.pm
lib/Review/Helper/Split/Markdown.pm
lib/Review/Helper/Sheet.pm           sheet export; fill validation and import
lib/Review/Helper/Skills.pm          install, update, check
lib/Review/Helper/Server.pm          PSGI app, routes, token

scripts/review-helper
share/html/index.html
share/css/  share/js/  share/img/
share/skills/{review-notes,review-build,review-feedback,review-requests,review-cleanup}/
```

Dependencies: Plack, Router::Simple, DBI, DBD::SQLite, PPI, File::ShareDir,
Object::HashBase, Role::Tiny; core JSON::PP and Getopt::Long. External:
`git`. Vendored: highlight.js.

---

## 10. Not in the first version

Reviewing uncommitted changes. Viewing a review without its repository.
Multi-user or remote serving. Editing code from the UI. Splitters for CSS,
HTML or languages beyond the list. A JavaScript parser beyond the scanner.
Opening a browser on platforms other than Linux and macOS.
