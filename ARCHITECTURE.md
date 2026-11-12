# ARCHITECTURE.md

The authoritative design of Review::Helper. Code follows this document. When
code and this document disagree, stop and flag it; a deliberate change lands
here in the same commit as the code.

`project` is the owner's original brief; this file supersedes it wherever they
differ. The reasons behind contested choices are in `RULINGS.md`.

The review file, journal and fill sheet (`standards/FORMAT.md`), the command
line the skills use (`standards/CLI.md`) and the HTTP API the page uses
(`standards/API.md`) are specified separately, as a standard other tools may
implement. Those files are authoritative for what they cover; this file does
not repeat them. Each backend ships them in its distribution.

---

## 1. What it is

A local web app that helps a human review code an AI/LLM agent wrote. After an
agent finishes a change (commit, branch, PR), it builds a **review file**
describing the change and fills in explanations, then starts the review's
server (`review-helper serve <review-file>`, in the background) and gives the
human its link. The human reads the change top-down in a browser, leaves
feedback, and flags spots for deeper agent work. Agents read the feedback back
out, act on it, and add a new **iteration** to the same file.

While writing the code, the agent records notes — wrong assumptions,
deviations, surprises. Those that need the reviewer's judgment become
**flags** on the lines they concern, yellow (a reviewer may object) or red
(probably a problem); the rest are the iteration's **journal**, listed for
the reviewer's information. It also orders each
commit's files for reading and prepares a **guide**: a short, step-by-step
presentation of the change.

The human should be able to judge design and concepts from summaries, and open
a diff or the code only when details matter or a summary looks wrong.

Principles:

- **The repository is the source of content.** The review file holds only review data;
  diffs, file contents and images are read live from the repository under
  review (`RULINGS.md`).
- **One VCS interface.** Every repository access goes through the
  `Review::Helper::VCS` role (revisions in a range, changes, diffs, files at
  a revision, a stable change id, pins, private state directories, the
  user). `Review::Helper::VCS::Git` is the only implementation and the only
  code that names git commands, refs or pathspec magic;
  `Review::Helper::VCS->detect` picks it, and is where another VCS would be
  added. The git details below describe that implementation.
- **Agents never touch the database or refs directly.** Everything goes
  through `review-helper` subcommands, so one narrow permission rule covers
  every skill.
- **Scripts do the mechanical work; agents write the explanations** (what,
  why, how) and choose each file's sections, which they read anyway.
- **Local first.** One review file in the repository, served by a small
  local server; loopback by default, and `serve --listen` (with a token)
  opens it to other machines on the network.
- **The format is a standard.** A goal of the project is a sharable base for
  AI/LLM-assisted code review that other tools, skills, and processes build on
  and extend. Nothing Perl-specific is stored, and the skills depend only on
  the command line in `standards/CLI.md`, so another tool can reuse both.
  The page is the same for every backend: it needs only the HTTP API in
  `standards/API.md`.
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
- Any path inside a worktree is also accepted, with no check of whether git
  tracks or ignores it; keeping it out of commits is the user's call.
- The repository is found from the file's location (`git -C <dir>
  rev-parse`). No repository path is stored, so the file works in any clone
  it sits in.

### Pins

Iterations usually follow rebases and amends, which leave earlier commits
unreachable and eventually garbage-collected. Each iteration pins its head
(`VCS->pin`), storing the pin's name in `iteration.pin`; for git, a ref:

```
refs/review-helper/<review-id>/<iteration-number>
```

These live in the shared ref store, so gc in any worktree honors them.

### Registry

`$(git rev-parse --git-common-dir)/review-helper/registry.json` maps each
review id to its file path and open/closed state. `build` registers; `close`
marks closed. It exists so `prune` can find orphans — reviews whose file is
gone, e.g. with a removed worktree — without scanning every worktree.

### Journal

`$(git rev-parse --git-path review-helper)/journal.jsonl`, per-worktree like
review files. `review-helper note` appends one JSON line per note while the
agent works; no review file is needed yet. A note with a flag color leaves
the journal when `fill` places it into a review (§7); one without is a
journal entry, which the next `build` or `iterate` takes into its new
iteration as it is (§5, Journal).

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

The schema is `standards/review.sql`, which `Review::Helper::DB` runs to
create a file; meanings and anchoring rules are in `standards/FORMAT.md`,
"The review database". The schema version lives in its `meta` table.
`Review::Helper::DB` refuses any other format version, and never upgrades a
file; nothing else of the schema is checked.
The format is unreleased, so version 1 still changes: a file an earlier
build wrote may fail with SQLite errors, and is rebuilt. Once released,
every schema change bumps `format_version`. An import refuses an export that
lacks a table or column this version has, since that column would load as
null without a word; other tables are ignored.

---

## 3. Building a review

### Inputs

```
review-helper build [--base <rev>] [--head <rev>] [--squash] [--path <spec>]...
    [--title <text>] [--source <url>] [--file <path>]
```

`--base` is required unless `--squash`; `--head` defaults to `HEAD`. The
commits reviewed are `git rev-list --reverse --topo-order base..head`, first
commit first. A branch or PR is just a range.

`--squash` records one combined entry for `git diff base head` instead (head's
subject and message, no change id); its base defaults to the empty revision,
the whole codebase. Each `--path` is a path spec (git: a pathspec) passed to
every VCS call that lists or diffs files; without `--squash`, commits that change no matching
file are dropped, and merges left with no commits.

### Topics

A squashed review avoids reviewing churn: the code only as it lands. For a
large one that single entry is too much to read at once, so the agent splits
its files into a handful of named **topics** (the skills, the Perl backend,
the frontend) through the sheet's `topics` item; a small review keeps one.
Each topic is a commit entry like any other: the head's revision and parent,
`topic` set to its name, only its files, its own what/why/how and reading
order. No parallel model: the page shows topics where commits are ("1. Perl
backend"), each with its file list, and every per-commit feature (flags,
feedback, sections, guide steps) works on a topic as on a commit.

Since every topic shares the head's revision, revision + path still names one
entry:
flags, notes and line, file and section feedback anchor as before and land
in the topic holding the path. Only anchors with no path name the topic too:
`commit` feedback and requests (`topic` column), guide `commit` and `diff`
steps without a path (`topic` in the target), and whole-change notes (the
note item's `topic`). `commit_entry` is unique per (iteration, revision,
topic) instead of per (iteration, revision).

Before the agent fills the topics, the iteration has its one entry with no
topic, holding every file, exactly as without topics. Filling them replaces
the entries: a topic keeps the entry of its name, else one with exactly its
files (so naming the one entry, or renaming a topic, keeps its text); a kept
entry whose files change loses its text; flags follow their paths; commit
feedback on an entry that goes moves to the review, keeping the topic as
context ("was on: Frontend"). The same sheet may not also fill commit,
order, note or guide items, which name entries the fill moves.

### Per commit

1. Record the revision, subject, message, and change id (`git patch-id
   --stable`).
2. For each changed path: status, kind (by extension, then file name
   (`Makefile`, `Dockerfile`), then the `#!` interpreter), before/after
   sizes. Binary and image detection follows `git diff --numstat` (`-`
   counts).
3. Parse the diff into hunks; count lines (below).
4. Store no sections: the file starts unsplit (`split_source` `none`) until
   the agent splits it through the sheet (§4).

Line counts, per hunk: each run of `-` lines followed by `+` lines counts
`min(-, +)` as changed and the remainder as removed or added. Once a file is
split, a `-` line is attributed on the old side first, to the section whose
`old_ranges` (the agent's `old_lines`) hold it: a deleted unit, or a live
one that claimed lines removed at its edge. Any other `-` line goes to the
section holding the next new-side line of its hunk (the previous one at the
end of the file).

### Iterations

`review-helper iterate <file> [--base <rev>] [--head <rev>]` adds iteration N+1
(base defaults to the previous base, head to `HEAD`) and pins it. Explanations,
reading order and flags carry forward for any commit whose change id matches
one in the previous iteration (commits sharing a change id pair in order; in a
review that is not squashed, a commit with no change id, an empty diff, pairs
with the same revision), and so does a file's split, sections and their
explanations included, when the file reads the same before and after both
commits. Any other file starts unsplit, and the sheet asks for its split again;
once split, its sections are explained afresh. Explanations never carry by
content: carry-forward matters for the human's feedback on a review, and an
agent re-explaining a changed file is cheap (`RULINGS.md`). A carried flag
whose code is unchanged follows it, with its seen marks and comments, its lines
shifted by the diff between the two versions of its file; a file the VCS finds
renamed between them (`-M`) takes its flags to its new path, and in a squashed
iteration a flag goes to the entry holding its path. When the code under it
changed (a flagged line, a whole-file flag's file, or a file that cannot be
compared: present on one side only, or a binary file that changed) it is
carried **pending**: on the span of its lines the diff kept (the whole file
when none), with its comments but no seen marks, and the sheet asks the agent
to keep, recolor or retire it (§5, Flags). A flag whose file left the change in
the new iteration (a squashed change to it reverted, say) is pending too, even
when its lines read the same; its sheet item says the file left the change, and
it stays on its path if kept, carrying settled from then on. The flags of a
commit that no new commit pairs with (by change id, or by revision for an empty
diff: amended or rebased with changes, or dropped) are not lost: in a review
that is not squashed each is carried **pending** onto the last new commit that
changes its path, its lines moved through the diff from the old commit where it
keeps any (else the whole file), or, with no commit changing its path, onto the
iteration's last commit; its sheet item says its commit was rewritten
(`RULINGS.md`). An open split request whose commit left the new iteration is
closed with a note ("its commit was rewritten"): the file is split afresh
anyway. A squashed entry has no change id: when both iterations are squashed
with the same base and paths, entries are matched by topic name instead (the
one with no topic with the one with none). The new iteration is split by the
previous topics first: each file keeps the topic that held its path, and new
files wait in an entry with no topic, so the sheet asks for the topics again.
Flags, their seen marks and splits carry by the same rules, and the flags of a
topic left with no file go to the first entry, as a topics fill moves them; a
file's explanations only when the file reads the same; reading order only when
the entry lists the same paths; a topic's own text only when, besides, every
file in it reads the same (the principle: carry-forward matters for the human's
feedback; the agent's text only where it is exact and cheap). The human's
feedback on a topic is not copied: the page shows it under the topic of the
same name in any iteration (a topics fill renames only the latest iteration's
feedback, so an earlier one keeps its name), and feedback no entry of the
iteration shown holds is listed in the summary, never hidden. Everything else
is left empty for the sheet. Each iteration gets its own guide; none carries
forward. Earlier iterations stay viewable for as long as their pinned refs
exist.

---

## 4. Sections

A file change splits into sections. Every section of the agent's split is
stored, so the next split can start from all of it; the tree, the page and
the sheet's section items take only the sections the commit touched (deleted,
or owning a `+` or `-` line); "view file" shows the whole file (§5).

The agent filling the review chooses the sections (`RULINGS.md`, "Sections
are the agent's"); the tool has no splitter. The agent already reads every
changed file to explain it, and a file's units (what a reviewer comments on
one at a time) are a judgement a parser approximates with many rules and
edge cases. `build` stores none; the sheet's `split` item asks for each text
file's sections, and `fill` checks them strictly. The exact data and checks
are "Sections" in `standards/FORMAT.md`.

### Units

- **Named units** — what the agent names: subs, methods,
  functions and other named block statements (`subtest "x" => sub {...}`,
  `has x => sub {...}`, `BEGIN`), CSS rules, Markdown or POD headings, each
  with its comments and documentation; never the file's main package or
  class, module wrappers or control flow. A short secondary package or class
  in another package's file may be one unit, `package Foo::Bar`, its
  `package` `Foo::Bar`. The agent may add a unit for a
  part of a long body that needs its own explanation (`sub fetch: retry
  loop`). A unit with only old lines is a **deleted** unit: code the commit
  removed.

A split either covers every line of the file or is empty. Covering, every
line with text is in a named section; a run of blank lines in none joins the
section before it (after it, at the top of the file). There is no catch-all
section: the agent names the header, code between subs and a closing `1;`
too (`header`, `imports`, `use constant LIMIT`, `end`), with the context to
name them usefully. Empty (`[]`), the file is one unit: the page shows the
file and its diff with no section rows (vendored, generated, trivial files).

A unit's `doc_ranges` are its documentation wherever it is: Perl POD naming
it (often after `__END__`), a JavaScript `/** */` block. In a Perl file,
documentation that is POD (each range's first line starts with `=` and a
letter) shows rendered beside the unit's summary. Comment lines directly
above a unit are part of its code `ranges`.

### The split item

- Every text file its commit adds, or changes a line of without deleting
  it, has a `split` item; a rename or mode change alone has none (the file
  is one unit with no sections). It is
  required while the file is unsplit (optional in a squashed iteration,
  where a whole codebase would be thousands of files) and while a `split`
  request on it is open. Its `about` gives the old path of a renamed file,
  the units stored now, and, while the file is
  unsplit, the previous split: the newest split an earlier iteration stored
  for the path (the commit with the same change id first), as stored: that
  commit and each section's name, package and lines at that commit, not
  moved through the diff (the agent maps them itself if it wants). It
  is context for the agent, who keeps what still fits, so sections stay
  steady, and changes or drops the rest freely.
- The agent fills `sections`: per section a `name`, `package`, `lines`
  (ranges after the commit) with `first_line` (the text of its first line,
  to catch an offset), optional `doc_lines`, and optional `old_lines` with
  `old_first_line` (before the commit). `[]` leaves the file whole.
- `fill` rejects, per section: ranges outside the file or overlapping
  another section's (naming it), a first
  line that does not match, a repeated name in a package, an empty name
  (names and packages are trimmed of whitespace first); and a split that
  leaves a line with text in no section. The agent fixes and fills again.
- Writing it replaces the file's sections: every unit, touched or not, with
  counts from the diff and no explanation. Section items, for the touched
  sections, appear in the next sheet.
- The human can ask for a `split` (the agent splits the file again, minding
  the note) of a file that has a split item; the server refuses one on any
  other file, and the page does not offer it. A re-split keeps line feedback; section feedback and
  section requests follow their section by name and package. With no match
  among the touched sections (the page shows no other) they move to the
  file, keeping the section's name and package as context, which the page
  shows ("was on: sub foo"); nothing else about them changes (`RULINGS.md`).
  The server refuses new ones on an untouched section.
- The page says when a file has no sections yet because the agent has not
  split it; it shows no other split label (every split is the agent's).

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
- **Left tree** — `summary`, then the mainline: plain commits and merges. A
  merge expands to the commits it brought in (nested merges nest), and each
  commit to its files. Resizable, collapsible, and restorable. Closed by
  default: a tall `›` tab on the page's left edge, beside the main column
  and centred between the header and the guide bar, opens it, and a `‹` tab
  on its right edge closes it
  (buttons labelled "Show navigation" / "Hide navigation", with
  `aria-controls` and `aria-expanded`). Whether it is open is remembered per
  browser in `localStorage`; at phone width it is an overlay that always
  starts closed and closes when a node is clicked. As the page scrolls
  (`IntersectionObserver`), the current section's node expands and the tree
  scrolls to keep it in view, and opening it shows that node. Clicking a node
  jumps to its section.
- **Merges** — the main column has one section per mainline entry. A merge's
  section shows its summary and its commits; each level (merge, commit,
  file, section) expands in place under the one above. Merges have no diff
  tab (they carry no file changes). A merge whose `/api/review` entry has
  `resolution: true` carries a **resolved conflicts** badge on its card and
  tree node and a **resolution** tab: a panel showing `GET
  /api/resolution/<commit id>` (the merge's resolution hunks per file; git:
  `git show --remerge-diff`) in the usual diff tables. A guide step or flag on a file its commit did not
  change opens a whole-file card inside that commit's section.

### Drilling right

Each open level hangs under its parent's head like a mirrored L: the
parent's head reads across the top, a guide line runs down the left, and
nothing frames the right or bottom. Each deeper level indents by one more
guide line.

Every level (a commit or topic, a merge, a file, a section, a whole-file
view) is one level node (`levelNode` in `app.js`): closed, a one-line row
outlined in its level's strip; open, a card whose head carries the same
title and parts, then its tabs and widgets, with the feedback form slot and
the level's body under it. A level supplies only its kind (its color), its
title, the parts its row shows (counts, badges, a commit's summary line),
its tabs and its body. One path opens and closes every level, holds the
clicked head where it was, and sticks the heads (`RULINGS.md`). Each kind
has its own color (merge, commit, file and whole-file view, section), on
its row, its card, its stuck head and, for merges and commits, its tree
node; every title, row or head, has one size.

- An open commit keeps its own summary and lists all its files under it, in
  the current file order, as compact rows like a file's section rows: path
  (its color tells the status; its tooltip gives path and status), counts
  and flag badge. A renamed file shows `old/path → new/path` with a badge,
  `renamed` when its content is the same and `renamed + changed` otherwise,
  in its row, its tree node and the commit's diff, and a commit that renamed
  files says how many among its facts ("Renamed: 3 files (2 changed)"). A
  collapsed row is the file level's strip outlined in its line with every
  corner rounded; expanded, the file returns to the strip and left guide
  line. Clicking a row (or a tree file node, a guide step, a flag link)
  expands that file in place into its summary, facts and section rows, and
  expands the tree to its sections; the other files stay listed.
  Any number of files can be expanded at once. The fragment keeps its
  shape: `cN=f138` means file 138 is expanded in commit N.
- A merge lists the commits it brought in instead of files, as rows the
  same way: a collapsed row is its level's strip (commit or merge),
  outlined, with the commit's label, its badges and the first line of its
  summary. Clicking a row (or a tree node, a guide step, a flag link)
  expands that commit in place into its card; the merge keeps its summary,
  its other commits stay listed, and the clicked row's head stays where the
  row was. Any number can be expanded at once, nested merges alike (`cN=c5` means commit 5 is
  expanded in merge N).
- An open file lists the sections its commit changed under its summary as
  compact rows: name, counts, and flag badge, outlined in the section
  level's strip like a file's row. Clicking a row expands that
  section in place into its agent summary, with its documentation
  alongside and its diff open below; clicking its head collapses it. Any
  number of sections can be expanded at once, so several methods of a file
  can be read side by side without the tree. A tree section node, a guide
  step or a flag link expands its section and scrolls to it; a `section`
  guide step opens its diff as a click does, so the step's highlight shows
  there.
- An expanded file's, section's, merged-in commit's or whole-file view's
  head collapses it when clicked anywhere but its buttons (tabs, a
  section's diff and file, feedback, request), unless a mouse click ends a drag
  selecting text in it (a section row's counts; the path is
  the toggle button's label, which browsers do not drag-select); its toggle
  button carries keyboard focus, kept across the redraw, and
  `aria-expanded`. Nothing has a separate close button. A click on a head
  (its toggle, tabs or buttons) keeps that head where it is on screen,
  under the mouse, however much what it shows grows or shrinks, and again
  once panels still filling above it land, unless the reader scrolls
  meanwhile (the browser's scroll anchoring is off while the page waits,
  so a scroll then is the reader's; the page's own scroll to keep a head
  for a later click is not, and that head is the one kept). A head stuck
  over its card scrolled above stays where it is stuck, its card's content
  (collapsed, its row) starting under it; one its card's end pushes up
  from there holds where it sticks if its card then reaches past it, and
  where it is if not (a collapsed row, or a card left short, its end placed
  so the head, which stops short of it by the card's bottom padding and
  border and its own bottom margin, is where it was). Closing a whole-file
  view puts the commit's card where the clicked head was. Near the end of
  the page, room grows under it so the head need not drop; a later hold
  gives back what it no longer needs, it shrinks to what still shows as the
  reader scrolls back up, and it goes when the page next scrolls to a target. Near the top
  of the page nothing is added: a replacement that sits above the clicked
  head (closing a whole-file view barely scrolled) can
  land higher than the head was, by up to the scroll left.
- Each commit's head carries tabs styled like the header's Unified | Side
  by side switch: `list | diff`, plus `resolution` on a merge with
  `resolution: true`. A merge has no `diff` tab; its tabs are `list` (its
  commits) and `resolution` when it has one, and a merge with no resolution
  shows no tabs, as `list` would be its only choice; a commit or topic
  with no files has nothing to list or diff, so no tabs. Switching is a mode,
  not open and close: the tabs are a radio group, the tab shown checked
  (`active`, `aria-checked`, the one in the tab order; arrow keys move it,
  and the focus stays on the tab across the redraw), and pressing it again
  does nothing. A panel the commit has no tab for (a hand-made URL) shows
  `list`, and a `diff` guide step on a merge shows its resolution, or its
  list when it has none. The header's Unified | Side by side switch is a
  radio group the same way. `list`, the default, shows the files (or
  a merge's commits) as rows; `diff` or `resolution` shows that panel in
  their place, right under the summary and commit message. The tab is the
  commit's panel slot in the URL (`list` writes nothing, `cN=d`, `cN=r`);
  the files' and sections' state stays there the whole time
  (`cN=d,f138,s40~d`), so back on `list` they show exactly as they were. A
  `diff` guide step on the commit switches it to `diff`, keeping that state;
  anything that targets a file, section or line in the commit (a row, a
  tree node, a guide step, a flag link or the flag walker) switches it to
  `list` to show it. Leaving the step puts the reader's tab back.
- An expanded file's head carries tabs the same way, by the same code:
  `list | diff | file`, `list` the default. `list` shows the file's sections
  as rows; `diff` (the file's diff) or `file` (the whole file after the
  commit) shows in their place, under the file's facts and summary, the
  sections' state kept. A file with no sections to list (split into none,
  or none its commit changed) has no `list` tab and opens on `diff`. A
  deleted file has no `file` tab, and a binary file no tabs at all. The tab
  is the file's panel slot in the URL (`cN=f138~d`, `cN=f138~v`); the
  file's first tab, its default, writes nothing, so an empty slot (a guide
  or flag plan's `''` too) always shows the default, and a panel the file
  has no tab for shows the default as well. A `diff` guide step on the file
  switches it to `diff`, a `view` step to `file`; a section step, or a tree
  section node, switches it to `list`. A line flag lands on `file` (`diff`
  for a deleted file, on its old lines); on a file with no such tab it
  marks the flag's line on the card (Flags). Leaving a step puts the
  reader's tab back.
- Each level's header (merge heads, then the commit's head, then the
  file's head, then an expanded section's head) sticks under the page header
  and the levels above it while its level is on screen, and leaves with it,
  so the reader always sees where they are. Commit, merge and file heads
  are one line of fixed height, and anything scrolled to lands below
  them. A section's head, the last level, keeps its own height; a target
  inside an expanded section allows the fixed head height for it, so it can
  land a few pixels under a taller section head.
- Clicking a parent's tree node collapses back to it.
  The browser back button undoes the last expand or collapse.
- Each commit section remembers its own open path and expanded sections. The
  full view state lives in the URL fragment, so back, forward and reload
  restore it.

### Per-item widgets

Every tree node and every open section carries:

- **diff** — a section's diff (its ranges); a commit or file shows its diff
  on its `diff` tab instead (Drilling right);
- **view file** — for a section, the whole file after the commit, syntax
  highlighted, its ranges marked; a file shows it on its `file` tab;
- **feedback** — add a comment at that level;
- **request** — flag a debate (and, for files, a new split).

Their forms open right under the head whose button was clicked, above the
card's content, with no scrolling; the head stays where it is, stuck or not,
as the form opens, is sent, or is cancelled. Posted feedback and requests
show in the card's notes, after its content. From the index, the page goes to
the item instead, as a click on it there does, the form under its head and
below the heads stuck over it; it does not move when both already show.

An SVG file (by its `.svg` extension) is text: diffed and taking line
feedback like any other. Its `file` tab also shows the picture above the
source, before (from the parent commit) and after, side by side when
modified. An image's `diff` tab shows it before and after, its `file` tab the
picture after the commit.

Unified and side-by-side diff are a global toggle, remembered per browser in
`localStorage`.

### Colors

New files green, deleted red, and an existing file whose content changed
blue, whatever its size did; a rename alone is uncolored. The `~` changed
line counts stay purple, apart from the file blue. Flags have a scale of
their own (Flags).

### Highlighting and diffs

- highlight.js (BSD-3), vendored in `ux/js/` with its license, languages
  limited to Perl, JavaScript, CSS, HTML/XML and Markdown.
- Whole files are highlighted once, then split into lines, so heredocs, POD
  and template literals highlight correctly. Diff rows pick lines by number.
- Diff rendering is ours: two table layouts (unified, side-by-side) over the
  server's hunk JSON. Each line is its own row, so line feedback can attach to
  any of them.
- A named unit's section diff is the whole unit and its whole documentation,
  changes marked, as two parts (`code`, `docs`) with a separator row
  ("documentation — lines A–B") and nothing from between them, even when the
  docs sit far away (under `__END__`). Diffs always show documentation raw.
- The documentation beside a section's summary is Perl POD rendered to HTML
  by the server, when it is POD; other documentation shows as highlighted
  lines.
- Diffs and documentation grow with the page and scroll only sideways, so
  the mouse wheel always scrolls the page. Only a whole-file view is a window
  with its own scroll, opened at the lines it marks.

### Reading order

The agent orders each commit's files so a file comes after the files needed to
understand it. A global **Reading order | A–Z** toggle (default reading order,
remembered in `localStorage`) orders tree nodes and stacked file views. Until
the agent fills it, reading order falls back to A–Z and the toggle shows it as
pending. Commits stay in commit order; sections stay in file order.

### Flags

A flag is the agent asking for the reviewer's judgment. Only agents create
flags (journal notes with a color, placed through the sheet, §7). The data
and its rules are in `standards/FORMAT.md`; the page shows them as follows.

- **Color** is **yellow** (a reviewer may object: a decision, a deviation,
  a problem solved in a way the reviewer may not accept) or **red**
  (probably a problem, still unsolved; nothing solved is red). It drives a
  flag's icon, line bars, badges and walk order. Only the agent sets it,
  and recolors it with a short reason; humans comment instead. No history
  is kept: the reason for the last change is all. There is no unflagging: a
  red flag needs action, and a yellow one is settled by seeing it, or
  commented on.
- **Kind** is a label from a fixed list: `deviation`, `assumption`,
  `self-review`, `test-revealed`, `note`, `other` (the default).
- A flag has a one-line **sentence** and an optional **detail**.
- **Seen** is each reviewer's own (`flag_seen`, not exported): set when the
  walker moves off the flag (the last one when the walk is left), when the
  reader clicks the flag (its icon or card line, opening its popup, or a
  link to it), and when they comment on it; the popup's toggle clears it to
  revisit the flag later. A yellow flag the agent turns red is unseen for
  everyone again.
- **Icons.** The two colors are CSS tokens. A gutter icon opens its popup
  with Enter or Space, the focus moves into the popup and stays there while
  its controls redraw it, and Escape returns it to the icon that opened it.
  No number is shown on or beside a flag icon (it would read as a count). A
  small eye on the icon marks a flag you have seen; a speech mark, a flag
  with comments.
- Diff and file views show the icon in the gutter of a flag's first shown
  line and a bar of its color on the rest. Hovering gives what the color
  means and the sentence; clicking opens a popup with the color, kind,
  sentence, detail, the reason for the last color change, a note while the
  flag is pending (its code changed and the agent has not decided on it
  yet), a **Mark unseen** / **Mark seen** toggle, the flag's comments with
  a box to add one, and buttons to read the flag aloud (Speech).
- **Nothing typed in the popup is lost.** Its comment box and any comment
  edit left open are kept per flag (per comment) in the page while it
  lives: redraws (marking seen, a comment added) and closing and reopening
  the popup put them back; only sending them or clearing them by hand drops
  them.
- **Comments.** A flag is a feedback target. Its feedback has a status, is
  edited only by its author, gets the agent's response through the sheet, is
  exported, and belongs to the flag's note id, so it shows on the flag in
  every iteration that has it. When the agent retires a flag, its comments
  move to its file (its commit, for a whole-commit flag), shown as "was on
  flag: <sentence>". Flag feedback whose flag the shown iteration lacks is
  listed in the summary, never hidden.
- **Badges.** Tree nodes and rows (commit, file, section) carry a count of
  their flags, red when any of them is, else yellow, and a small dot says
  some of them are unseen by you. Seen flags are counted and shown like any
  other; there are no view settings that hide flags.
- Every flag a badge counts can be seen on its card without the walker. A
  file card lists, in its info block under its summary and above whatever
  tab is open, the flags no section row carries: its whole-file flags and
  line flags in none of the sections it lists, the latter with their lines
  (`:12–14`), which jump to them. A commit or topic card lists its
  whole-commit flags and flags on files it did not change (with path and
  lines) the same way. One line per flag: the gutter's icon, kind and
  sentence, and its read-aloud buttons; clicking it opens the gutter popup.
- The review summary lists the flags, linking to each, grouped by color,
  red first, each group in commit, path and line order.
  Groups start collapsed to their name and their own **Walk new (N)** and
  **Walk all (M)** buttons (Flag walker), whose counts say how many flags
  are unseen and in all; whether each is expanded is remembered per browser
  in `localStorage`. Each flag says who has seen it ("seen by you, Bob").
- **The flag jumped to.** A flag link or a flag walker step marks its flag
  where it lands (`flags.js`): a line flag's lines, in the panel the jump
  opened, get a tint and a thick bar in the flag's color, stronger than a
  guide highlight, and a callout with the flag's kind and sentence sits
  right above them. A flag listed on its card (a
  whole-file or commit flag, or lines its panel does not show) gets the tint
  and bar on that line instead; one on a whole-file view with no line to
  show gets them on the card's head, with the callout under it. On arrival
  the page scrolls it into the upper part of the screen below the stuck
  heads (a whole-file view scrolls its own box too) and the callout or line
  pulses, unless the reader prefers reduced motion.
  The mark lives only in the page, not the URL: it lasts while the URL is the
  one the jump made, so any other navigation, the walker's next step
  included, removes it; a reload drops it.

### Flag walker

Steps through the flags, marking each seen as the reader moves on. It is not
a second step engine: the guide bar plays a list of generated `flag` steps
instead of the guide (`logic.js` `walkSteps`), with the same navigation,
highlights, panel closing and `g` step in the URL; `w` in the URL selects the
list. While walking, `g` is the flag's id rather than its place in the list,
so a reload stays on the flag even when the list has changed; a walk whose
flag is not in its list is left.

- **Order:** red first, then commit, path and line. Each
  step's commentary is the flag's sentence.
- **Which flags:** `w` is `f` (walk new: the flags you have not seen) or
  `a` (walk all: seen ones too), followed by `r` or `y` for one color
  group's walk (`w=fr`). Seen and unseen is the only difference between
  the two.
- **Seen.** Moving off a flag (next, back, exit, the browser's Back,
  another walk) marks it seen for the current reviewer, unless the reader
  set its mark by hand (**Mark unseen** / **Mark seen** in the bar or the
  popup) while the walk showed it: that choice holds for the rest of the
  walk, however often the walk passes the flag again. Switching the
  iteration mid-walk leaves the walk without marking its flag, which is
  another iteration's.
  Other reviewers' marks show as "Seen by …". The list is made when the walk starts or the
  page loads, so a flag just marked stays in place until then; a list made
  on load keeps the flag `g` names, seen or not.
- **Seen marks carry with unchanged flags.** When the builder carries a flag
  into a new iteration, every reviewer's mark (with its time) comes along
  unless the flag is carried pending (its code changed).
- Entry: **Walk new (N)** and **Walk all (M)** buttons in the header
  and above the summary's flag list walk every flag; each color group's
  pair walks that group. N and M are the lengths of the two lists. Each
  walk starts at its list's first flag: walk new at the first unseen one,
  so starting it again picks up where the last walk stopped; walk all at
  the first, red first. Walk new is disabled while nothing is unseen; the
  header's pair hides when the iteration has no flags.

### Journal

The iteration's journal entries (`journal_entry`): what the agent recorded
for the reviewer's information that probably needs no attention, anything
worth knowing that is not a flag. `build` and `iterate` take every journal
note without a color into the new iteration as it is and remove it from
the worktree's journal: no sheet item, no placement, no lines. An entry
belongs to that iteration; it is not carried, takes no comments, has no
seen marks or badge, and is not walked.

The page lists them in a **Journal** section at the very end, said to be
for information that probably needs no attention: an entry per line, its
sentence behind a disclosure arrow (no list bullet); clicking one opens its kind, its path (a link to the file in the
last entry of the iteration that changed it, plain text when none did) and
unit, read-aloud buttons and its detail. A closed entry drops its details,
so a reading of it pauses as a collapsed card's does.

### Guide

The agent's presentation of the iteration: an ordered list of steps.

- **Step target:** the review summary, a commit, a file, a section, a flag, a
  diff (commit, file or section), or "view file" at a line range. Optional
  **highlight** ranges mark the exact lines the step talks about.
- **Commentary:** up to three short sentences, at most 400 characters, written
  to be read aloud — it says "the highlighted loop" or "the Builder module",
  never code tokens. The guide may skip commit overviews it already covers and
  goes into diffs and source only at points of interest. It need not follow
  the history at all: for a long or merge-heavy range it can tour the code as
  it finally lands, with `view` steps at the head commit, which the page opens
  as a standalone file view with its highlights; a flag lives on a file its
  commit changed, so such a view shows none, and flags are reached with
  `flag` steps.
- A step may name only a section the page shows. When a later re-split
  removes such a section or leaves it untouched, the sheet asks for the guide
  again (`RULINGS.md`); until then the page plays that step as one whose
  target is missing, showing its commentary only.
- **Playing drives the real page.** A step sets the same view state a click
  would (tree, expanded levels and sections, diff), then scrolls to the
  target and marks highlighted lines with a strong warm tint, laid over the
  line's own diff color so added and removed lines stay distinct, and a thick
  gutter bar.
  Steps change the URL, so the browser's back button follows them too.
- **Leaving a step undoes exactly what entering it changed.** Entering step n
  records in the URL (`u=`) each piece of view state it changed and its value
  before: the panels it opened, the files and sections it expanded, the
  merged-in commits it expanded, its whole-file card, and the commit tab it
  switched. Moving to another step, or exiting guide mode, puts those back
  (a file the step expanded collapses, a commit it switched to another tab
  goes back to the reader's tab, a merged-in commit it opened closes with
  everything under it); what the reader had open before the step stays as
  it was, and so does anything the reader opened during it elsewhere.
  Exploring by hand keeps the step and its record. A step that changed
  nothing undoes nothing.
- **Guide bar** pinned at the bottom of the main column: commentary, its
  read-aloud buttons, step `n / N`, previous topic, back, replay, next,
  next topic, speech toggle, exit. It is large, with an accent
  border, tint and shadow, so it is not missed. The controls stack in a
  compact block right of the commentary, which takes the rest of the
  width: the read-aloud buttons and the count, then previous and next
  topic, then back and next, then replay, speech and exit (in the flag
  walker, its seen mark in the topics' row). A bar too narrow for the block
  beside the text (a phone, a folded phone) flows the controls in wrapped
  rows under it. Play enters guide mode at
  step 1; from then on only next and back move. Nothing advances on its own.
  Exploring the page by hand does not leave guide mode; next resumes from the
  current step. Reading order does not affect the guide.
- **Next topic / previous topic** (named "commit" in a review not split
  into topics) skip a run of steps on one topic or commit, for guides that
  dwell or jump around (`logic.js` `topicJumps`). A step's group is the
  entry its target is on: a file, section, diff or view step's commit and
  its path's topic, a flag step's flag's; summary steps have none and are
  passed over. Next goes to the first later step of another group;
  previous to the first step of the run of steps before the current
  step's run. Each hides when there is nowhere to go, and both hide in the
  flag walker. At phone width they show only their arrows (« »). A flag step passed over is not marked seen.
- Entry: a **Play guide** button in the header, the only one; hidden when
  the iteration has no guide.

### Speech

Agent prose read aloud by the browser's Web Speech API (`speechSynthesis`,
`speech.js`). No server work, no stored audio, no agent tokens.

- **Read-aloud buttons** on every block of agent prose: the summary's and
  each commit's or topic's what, why and how, the iteration note, a file's
  and a section's what changed, a flag (sentence, then detail) in its popup
  and on its card line, and the guide bar's step. Short fields (status,
  kind, object system, paradigms, sizes) have none. Each block has a
  play/pause toggle (▶ stopped or paused, two bars playing) and a stop, a
  black square; the bars and the square are drawn in CSS (the pause and
  stop characters show as colored emoji on phones). Play
  starts at the beginning, or resumes where the block was paused; pause
  keeps the place; stop, or reaching the end, goes back to the beginning.
  One reading plays at a time: playing another block pauses the one
  playing, which keeps its place. A reading whose buttons leave the page
  (its popup closed, its card collapsed) pauses at once, keeping its
  place, so nothing reads on with no way to stop it; a click in the speech
  menu does not close a flag's popup, so its reading goes on: a new voice
  restarts the sentence playing, a new speed applies from the next one.
  The buttons sit beside a block's heading, not in it.
- **Per sentence.** A reading is spoken one sentence per utterance, keeping
  the index of the sentence it is on, so a pause and a resume restart that
  sentence; the API's own pause and resume are unreliable (Chrome cuts long
  utterances off, and cancel loses the place).
- **Text cleanup** (`logic.js` `speechSentences`) for every reading:
  paragraphs and bullets lose Markdown's markers (backticks, emphasis,
  headings, quotes, a link's target), and `::` and `->` read as a pause
  (a comma: `Foo::Bar->new` is "Foo, Bar, new"; none next to punctuation
  already there, and the author's own commas stay). A sentence longer than
  140 characters is cut at `;`, `:` or `,`, else between words: Chrome's
  online voices stop an utterance at about 15 seconds, and 140 characters
  take about 13 at the slowest speed offered. Guide commentary still may not
  hold code (the fill checks); other prose may.
- **Auto-play** only in the guide bar, each mode with its own toggle, off
  by default: with the guide's on, each step's commentary is read when the
  step shows; with the walker's on, each flag's sentence and detail. Moving
  off a step stops its reading. Nothing else plays by itself.
- **Speech menu** in the header (a speaker icon), one place for every
  reading: the voice picker and the speed. Its voices list on-device voices
  (`localService`) first. The default is an on-device voice, the first
  matching the page language if any. An online voice is never the default:
  with only online voices, the picker starts at "Pick a voice", and a play
  (or turning a toggle on) opens the menu and waits; picking a voice starts
  it. A play pressed before the browser has listed its voices starts when
  they arrive. Either way a wait ends when its block leaves the page.
  Speech only ever uses the picked or default voice, never the browser's
  own; a picked voice the browser drops falls back to the default, and
  with none the reading pauses. Online voices are labeled "(online: text
  leaves this machine)". When the browser reports no voices, the menu says
  why (on Linux the browser needs speech-dispatcher), a play button opens
  it, and the bar's toggle stays in view, disabled, with the same hint.
- Speed 0.75–1.5x. Toggles, voice and speed are remembered in
  `localStorage`.

### Summaries shown

- Review, commit: what, why, how.
- File: what changed, object system (Moose, Object::HashBase, …), paradigms
  (OOP, exporter, functional, …), added/removed/changed lines.
- Section: what changed, plus the same facts where they differ from the file,
  and its counts.

### Iterations, feedback, requests

- The header shows `user@host · repo · title` (`identity {user, host, repo}`
  from `/api/review`; the identity part is left out when absent) and, when
  the review has a `source`, a link to that pull/merge request. Hovering the
  identity also shows who is reviewing (`identity.reviewer`). The controls
  follow on the same line when everything fits, else on a second line, so
  the identity and title keep their width; the page under the header is
  offset by its real height (`--header-h`). They are grouped by purpose, a
  line between groups: going through the review (Play guide, the walks),
  then how it shows (iteration, speech menu, file order, diff layout), and
  **Close review** last, at the far end. The line shows only between two
  groups on one row: a group with nothing to show is hidden, and the second
  group wrapped to a row of its own has none. Every control has one height
  and outline; the guide and walk buttons are tinted to stand out without
  shouting, and Close review is a plain button. Both switches are radio
  groups.
- **Stale tab check.** The tab keeps the id of the review it loaded in
  `sessionStorage`. When a later load (reload, iteration change) returns
  another review id, a banner says "This tab was showing review X; this
  server is now serving review Y" with a button that accepts it and starts
  the new review from the top.
- An iteration selector at the top; the latest is the default.
- Feedback shows inline where it anchors, with status, who wrote it, and the
  agent's response; requests show who asked. Line feedback shows only at
  its line, under the row of every line table showing it, never under a
  card. Feedback on a flag shows in the flag's popup (Flags).
- **Comments out of sight.** Where the page stops short of a comment (a
  closed commit, file or section row over it, or a tab that does not show
  it), a badge, a speech mark and a count, sits on that row or on the head
  of the open card whose tab leaves it out. Clicking it opens the page down to the first comment it counts:
  a line comment on the tab that shows its line (its section's diff when
  that has it, else the file's diff, or the whole file for a new line the
  diff leaves out), any other with its card. Feedback the iteration has no
  entry for is listed in the summary, so every comment is findable.
- A line number click opens a feedback box under its row. A box with text
  stays open when another line is clicked, so a draft is never dropped; an
  empty one gives way to the new box. Feedback from earlier iterations shows its
  status and, when addressed, the iteration that resolved it.
- Debate results show under the item they target: the case for and the case
  against, side by side.
- **Download feedback** returns the same JSON as `review-helper feedback`.
- **Close review** sets the review closed and notes that old iterations may
  lose their diffs after `prune`.

---

## 6. Server

- Plack app with `Router::Simple`, run under Plack's `HTTP::Server::PSGI`.
- Binds `127.0.0.1` and prints the plain URL (`http://127.0.0.1:PORT/`),
  unless `--listen ADDR` (or `$REVIEW_HELPER_LISTEN`) names another address:
  IPv4, IPv6 or a host name, `0.0.0.0`/`::` for every interface (owner
  ruling, `RULINGS.md`: reviewing on a shared dev server from a laptop).
  Beyond loopback it prints a URL for other machines (the FQDN for a
  wildcard, else `ADDR` as given), then the loopback URL when the socket
  serves loopback, and warns on stderr that anyone with the token URL, and
  anyone on this machine, can read the review and post feedback. The
  port is `--port`, else the integer printed by `--port-command CMD`, else by
  `$REVIEW_HELPER_PORT_COMMAND`, else ephemeral. `CMD` is split on whitespace
  and run in list form, never through a shell. `--open` launches the browser
  (`xdg-open` on Linux, `open` on macOS; elsewhere the URL is only printed).
- Access (`RULINGS.md`): other local users are trusted, so loopback clients
  need no token. Beyond loopback, `serve` makes a launch token (32 bytes of
  `/dev/urandom`, hex) and prints the network URL with `?token=`; a request
  from a non-loopback address must carry it, once in the URL (answered 303
  to `/`, whatever the path and query, setting the `HttpOnly`,
  `SameSite=Lax` cookie `rh_token_PORT`) and then in the cookie; else 401
  and a text page. `Lax`, so a token link clicked in a web app works: GETs
  are read-only, and POSTs still need JSON and the Origin check. Tokens are
  compared in constant time. A reverse proxy on the same machine connects
  from loopback, so everyone behind it gets in without the token; the docs
  say so and suggest no proxy. `Server::_requester` resolves who is
  asking (open server, loopback, URL token, cookie) and is where
  per-reviewer tokens stored in the review file would plug in.
- Requests whose `Host` is not `127.0.0.1`, `localhost`, `[::1]` or one of
  the server's `allow_hosts` are refused. Beyond loopback `allow_hosts` holds
  the address listened on (unless a wildcard), the hostname, FQDN and the
  addresses they resolve to, and the outgoing address (a UDP socket's route,
  no packet sent), plus every `--allow-host NAME` and
  `$REVIEW_HELPER_ALLOW_HOSTS` entry; other interfaces' addresses need
  `--allow-host`. POSTs must send a JSON body, and an `Origin` header, when
  present, must match the Host.
  This keeps rebinding and cross-site web pages out of the repository and the
  feedback. `/api/review` carries `identity` (`user`, `host`, `repo`: login
  name, hostname, repository root) so the page can show whose review a port
  serves.
- **Reviewer.** `identity.reviewer` names who is reviewing: `--reviewer`,
  else `$REVIEW_HELPER_REVIEWER`, else `Name <email>` from git's `user.name`
  and `user.email` for the repository (the one git call that reads the
  user's own configuration), else the login name, else `unknown`. The server
  records it on the feedback, requests and seen marks it writes, so a review
  file passed between people says who left what. Seen marks are each
  reviewer's own and, like feedback, are not exported.
- **Only the author edits feedback.** `POST /api/feedback/:id` (body or
  status) answers 403 unless the row's `reviewer` is the server's, or the
  row has none (written before reviewers were recorded); the page shows the
  edit control only on feedback it would accept. Others reply with feedback
  of their own. Agents' writes through `fill` (responses, status) are not
  edits of the human's text and are not limited. There is no delete, and
  the page does not edit requests.
- Merges with conflict resolutions: a merge's resolutions
  (`VCS->resolution_changes`; git: `git show --remerge-diff`, git 2.36+, older
  git reports none) are computed live, once per server per revision
  (revisions are immutable), limited to the iteration's path specs. Each
  commit in `/api/review` has `resolution`, true for a merge with any. Nothing is stored in the review file.
- A section's diff is built by one function from the file after the commit
  and the fixed-option hunks: every line of the section's `ranges` and
  `doc_ranges` (unchanged ones as context), the `-` lines Diff's attribution
  gives it, in file order, as hunks labelled `part` `code` or `docs`. A
  section with no added or removed line has none.
- Perl documentation is rendered with Pod::Simple::XHTML: no raw `html`
  targets, links reduced to their text, no element ids.
- Git reads run through list-form pipes, never a shell.

### Routes

```
GET  /                    index.html
GET  /static/*            ux/css, ux/js, ux/html assets
GET  /api/review          review, iterations, summary, commits with files
                          (each with the names of its shown sections),
                          flags with their seen marks, guide, feedback, requests,
                          and the kind of each file shown whole at a commit
                          that did not change it (content_kinds, kept
                          per revision:path; plain text when it cannot be read); the page
                          keeps no kind table of its own
GET  /api/file/:id        file change detail and the sections its commit
                          touched; a Perl unit's documented sections carry
                          doc_html
GET  /api/diff/:id        hunks JSON; ?section=<id> limits to its lines, or
                          for a unit gives its code and docs parts
GET  /api/resolution/:id  a merge's resolutions, hunks JSON per file
GET  /api/content/:rev/*path raw file content (text, image)
GET  /api/feedback        feedback export JSON
POST /api/feedback        add feedback
POST /api/feedback/:id    edit or change status (the author's, or no one's)
POST /api/request         add a debate/split request
POST /api/flag/:id        mark a flag seen by this reviewer, or not
POST /api/close           close the review
```

GET routes take `?iteration=N`; the default is the latest. `standards/API.md`
specifies every route, its JSON, and the access rules.

---

## 7. CLI

One command, `review-helper`: a POSIX `sh` dispatcher (`bin/review-helper`)
that runs an implementation, `review-helper-<impl>` on `PATH`, chosen by
`REVIEW_HELPER_IMPL` or else the first found walking `PATH`
(`standards/CLI.md`, "The dispatcher"). The Perl backend's command is
`review-helper-perl`; the Perl distribution installs both. Options are parsed
with Getopt::Yath.

```
review-helper <file> [--port N | --port-command CMD] [--listen ADDR] [--allow-host NAME]... [--open]
                                           serve (shorthand for `serve`)
review-helper serve <file> ...
review-helper build [--base <rev>] [--head <rev>] [--squash] [--path <spec>]...
    [--title <t>] [--source <url>] [--file <path>]
review-helper iterate <file> [--base <rev>] [--head <rev>]
review-helper note [--flag yellow|red] [--kind <kind>] --path <p> [--unit <name>]
    [--snippet <text>] --sentence <text> [--detail <text>]
                                           append a note to the journal: a
                                           journal entry, or with --flag a flag
review-helper sheet [--all] <file>         print the fill sheet (--all: filled
                                           items too, to replace their text)
review-helper fill <file> <sheet.json>     validate and import a filled sheet
review-helper feedback <file> [--open]     print feedback JSON
review-helper feedback <file> --publish [<id>...]
                                           print each comment to post: its
                                           exact text and line anchor
review-helper export <file> [--out <path>]  print the review as shareable JSON
review-helper import <json> [--file <path>] create a review file from an export, or merge one already here
review-helper close <file>
review-helper published <file> <feedback-id>   mark feedback as posted to the PR
review-helper list                         registered reviews in this repository
review-helper prune [--dry-run]
review-helper skills path                  print the shipped skills directory
review-helper version                      print the standard and implementation versions
review-helper --which                      (the dispatcher) name the implementation it runs
```

A first argument that is a known subcommand is a subcommand; anything else is
a review file.

### The fill sheet

`sheet` prints JSON listing every item still needing agent text:

- review summary; commit summaries; file summaries with object system and
  paradigms; section summaries;
- each changed text file's split into sections (§4), and again for a file
  with a `split` request;
- reading order for each commit;
- placement of each journal note with a flag color (commit + path + line
  range, or a whole commit or file), using its path, unit and snippet
  hints;
- each flag carried pending (its code changed): keep, recolor (a new color
  and the reason) or retire it;
- the iteration's guide;
- responses (status and text) for open feedback;
- results for open requests.

Each item carries an id, a revision hash of the data it describes, and
pointers (commit, path, section name, line ranges). It carries no code; the
agent reads code through git.

`sheet --all` also lists the items already filled — summaries, reading
orders, splits, every flag (to recolor or retire it), finished debates, the
guide — with their current values and revisions, so `fill` can replace them;
the revision check still rejects overwriting a change made since. Placed notes
are flags by then and do not come back, and feedback is listed only while
open. No other command rewrites review text.

`fill` validates the whole sheet before writing anything and rejects, with a
message naming the item:

- unknown ids, wrong field types, stale revisions;
- split or flag ranges outside the file; a split that overlaps, repeats a
  name, or whose first lines do not match the file (§4);
- a reading order that is not exactly the commit's paths, once each;
- a flag decision that does not fit: a recolor without a color (`yellow` or
  `red`) and a reason (a keep or retire ignores both);
- guide steps whose target or highlight does not exist in the iteration
  (a view's or highlight's lines are checked for shape, not against the
  file's length: the page marks only lines that exist);
- guide commentary over 400 characters or containing code tokens: backticks,
  `->`, `::`, `()`, or a sigil (`$`, `@`, `%`) followed by a word character.

A valid sheet is written in one transaction. Partial sheets
are fine; `fill` reports what remains empty, so agents can work in batches.

---

## 8. Skills

Nine skills ship in `skills/<name>/SKILL.md`:

| Skill | Does |
|---|---|
| `review-notes` | Read before writing any code. While working, record a note with `review-helper note` whenever an assumption proves wrong, the work deviates from a plan or spec, a test exposes a misunderstanding, self-review corrects something, or a point deserves the reviewer's attention: a yellow flag when a reviewer may object, a red flag for a probable unsolved problem, a journal entry for the rest; and a kind. |
| `review-build` | `prune`, `build`, then fill the sheet until nothing remains: explanations, reading order, note placement, and the guide. Covers writing commentary for speech: refer to highlighted code, never quote it. |
| `review-feedback` | Read `feedback`, change the code as new commits, `iterate`, then fill responses and new explanations, and keep, recolor or retire each flag whose code changed. Fix-round findings are journal entries unless the fix changes behavior a reviewer should check. |
| `review-requests` | Handle open requests: dispatch two sub-agents for each debate (one defending the target, one arguing against it or for an alternative) and record their cases; split files again for `split` requests. |
| `review-cleanup` | `prune`, and report what it removed. |
| `review-project` | Review a codebase, or part of one, as it finally lands — a primary use case. No author journal exists, so the agent deduces points of interest from the commit history (reverts, fixes, churn, the reasons in messages), docs, tests, TODOs, and issues, records them as notes, builds with `--squash` (and `--path` to split a large codebase into several reviews, e.g. Perl and JavaScript), fills the sheet, and writes a guide touring the final code. Also serves for learning a codebase. |
| `review-add-notes` | A second agent reviews an existing review: records its own flags with `note` (red for a suspected problem, yellow for a departure the author may defend; no journal entries, as it adds no iteration) and places them through the sheet. Changes nothing else. |
| `review-publish` | Post the human's feedback to the pull/merge request **verbatim**, through the human's own account and the agent's tools for that forge, after showing the human exactly what will be posted and getting confirmation. It posts what `feedback --publish` prints: each item's exact text, its location prefix included, and for line feedback whose line still counts in the same file in the request's diff a line comment anchor, with the general comment to post if the forge refuses it. Never edits, summarizes, or adds text. Marks each posted item with `published`. |
| `review-pr` | Review someone else's pull/merge request: with the agent's own tools for the forge (its CLI, an MCP server, or similar; with none it says so and stops), read the description, comments, reviews, and linked issues and their discussion; get the head and target locally; `build --source <url>`; fill the sheet, comparing the change against the issue and recording its notes, before the build so it takes their journal entries (red for a suspected problem, yellow for what the author may defend), by judgment, since author notes are unavailable. Read-only toward the forge; the human posts any review. |

The skills that build or change a review (`review-build`, `review-project`,
`review-pr`, `review-feedback`, `review-requests`) start its server
themselves with `review-helper serve FILE` in the background and give the
user the printed link; they reuse the server they started in the session
while it runs, serve with `--listen 0.0.0.0` (the token link) when the user
wants to share with others on the network, and stop it when the review is
closed or on request (`RULINGS.md`).

Skills run only `review-helper` subcommands and read-only `git` commands
(`review-pr` also `git fetch`). `review-pr`, `review-project`, and
`review-publish` reach the forge with whatever tools the agent has for it;
the tool itself has no forge code. Every forge write is approved by the
human call by call.
Where the harness supports it, each skill's frontmatter grants exactly that.

### Install

`skills path` prints the shipped directory. The user symlinks each skill's
directory into `~/.claude/skills` (Claude Code) or `~/.agents/skills`
(Codex), both of which load symlinked skill directories, or asks the agent
to install them. A link always matches the installed tool, so there is no
install command, version stamp, or version check.

---

## 9. Code layout

The repository:

```
standards/   the standard: FORMAT.md, review.sql, API.md, CLI.md
ux/          the page (html/, css/, js/) and its unit tests (t/)
skills/      the agent skills, one directory each
conformance/ checks any backend runs: the CLI checks and the page drive
bin/         review-helper, the dispatcher
perl/        the Perl backend and its distribution (dist.ini, Changes)
AI_DOCS/     working documents for upcoming development only
```

Each part keeps what is specific to it in its own `AGENTS.md`. A backend
ships the shared parts in its distribution: `perl/share/` holds committed
symlinks to `ux/`, `skills/` and `standards/`, so a checkout runs as is, and
`dist.ini` follows them so the distribution carries the real files.
`perl/scripts/review-helper` is a symlink to `bin/review-helper`.

The Perl backend, under `perl/`:

```
lib/Review/Helper.pm                 version, standard version, overview POD
lib/Review/Helper/CLI.pm             subcommand dispatch
lib/Review/Helper/CLI/*.pm           one class per subcommand
lib/Review/Helper/VCS.pm             the VCS role: the interface every repository access uses
lib/Review/Helper/VCS/Git.pm         git: locate repo, fixed-option diffs, files, pins (refs)
lib/Review/Helper/DB.pm              connection, schema, txn helper
lib/Review/Helper/Registry.pm        common-dir registry; prune
lib/Review/Helper/Journal.pm         note recording; flag notes for the sheet, entries for build/iterate
lib/Review/Helper/Builder.pm         build and iterate
lib/Review/Helper/Diff.pm            diff text -> hunks; line counts; section diffs
lib/Review/Helper/Split.pm           file kind; lays out an agent split
lib/Review/Helper/Sheet.pm           sheet export; fill validation and import
lib/Review/Helper/Export.pm          review export and import for sharing
lib/Review/Helper/Feedback.pm        feedback and request export
lib/Review/Helper/Util.pm            JSON, UUID, time, share dir helpers
lib/Review/Helper/Server.pm          PSGI app, routes
lib/Review/Helper/Manual.pm          user manual (POD)

scripts/review-helper-perl           the Perl implementation's command
scripts/review-helper                symlink to the dispatcher
share/{ux,skills,standards}          symlinks to the shared parts
```

Dependencies: Plack, Router::Simple, DBI, DBD::SQLite, File::ShareDir, UUID,
Cpanel::JSON::XS, Object::HashBase, Role::Tiny, Getopt::Yath, Capture::Tiny
(the port command's output in `serve`). External: `git`. Vendored:
highlight.js, in `ux/js/highlight/`.

---

## 10. Not in the first version

Reviewing uncommitted changes. Viewing a review without its repository.
Collaborators with their own links (planned next; see
`PLANNED_TODO.md`). Editing code from the UI. A tool splitter or
parser: the agent chooses sections, by design.
Opening a browser on platforms other than Linux and macOS.
