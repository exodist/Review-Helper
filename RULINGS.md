# RULINGS.md

Decisions this project's owner has already made. A recorded ruling stands
until the owner changes it.

Read this file when a decision is in front of you, not for general context.
When new evidence — a bug report, a pull request, a user request, a changed
constraint — suggests a ruling needs revisiting, flag it for the owner rather
than acting against it. See `~/projects/Agents/AGENTS.md` under "Rulings".

Newest first. Each entry gives the date, what was ruled, and enough evidence
for the next reader to judge whether the situation has changed.

Only current rulings are kept. A new ruling replaces the one it changes, and
an amendment is folded into the ruling it amends; git keeps the history.

Only decisions that could be raised again belong here — contentious calls,
questions likely to recur, and rulings that may need revisiting when
circumstances change. Routine decisions nobody will ever question stay out;
see `~/projects/Agents/AGENTS.md` under "What earns a place in `RULINGS.md`".

---

## 2026-11-12 — `AI_DOCS` holds only documents for upcoming development

**Ruling (owner): `AI_DOCS/` keeps only working documents an agent needs for
development still to come. Review transcripts, review-cycle records, plans of
built work, decision walks and other history do not go there.** Its
`README.md` says so. Rules out keeping history in `AI_DOCS/` for context:
git keeps it, and `RULINGS.md` and `PLANNED_TODO.md` hold what is current.

Evidence: before sharing the repository, the owner found none of the
documents there were needed for new development. Revisit if agents keep
rediscovering context that a kept document would have given them.

## 2026-11-12 — The agent starts the review server

**Ruling (owner, option A): after building or changing a review
(`review-build`, `review-project`, `review-pr`, `review-feedback`,
`review-requests`), the agent runs `review-helper serve FILE` in the
background and gives the user the link it prints. When the user wants to
share with others on the network, it serves with `--listen 0.0.0.0` and gives
the token link. It reuses a server it started for that review in the
session while that still runs (it keeps the URL it printed), and stops the
server when the review is closed or when the user asks.** Rules out telling
the user to run the command, and tooling to find or manage running servers:
the agent keeps track of the process it started.

Evidence: the README review found the README promised the agent would start
the server while every skill told the user to run it. Revisit if users want
to start servers themselves by default, or agents lose track of the servers
they started.

## 2026-11-12 — Flags of a rewritten commit carry pending

**Ruling (owner, option A): in a history review, a flag whose commit gets no
partner in the new iteration (by change id, or by revision for a commit with
an empty diff: amended, rebased with changes, or dropped) carries as pending
onto the new iteration's last commit that changes the same path, its lines
moved through the diff from the old commit where the diff keeps any, else on
the whole file. With no commit changing the path, or no path, it goes pending
on the iteration's last commit (a flag with a path flags the whole file there,
and its sheet item reports the file as having left the change, as the squashed
rule does). The sheet's flag item asks keep, recolor or retire as usual and
says the commit was rewritten (`about.rewritten`); seen marks go, as for any
pending flag.** Feedback on the flag keeps working through its note id.

Rules out: dropping such flags silently (the behaviour before), and
reporting them only on standard error. Evidence: the CLI conformance work
found that amending a flagged commit and iterating lost every flag on it,
red ones included, with nothing said; the flags' journal notes were gone, so
the agent could not place them again. Revisit if the last commit changing
the path proves the wrong home often enough that agents keep moving the
flags by hand.

## 2026-11-12 — When the standard's version changes

**Ruling (owner): the standard's version stays 1 and may change freely until
the first release. After it, an optional addition (a route, a response
field, a command, an option) does not change it; removing, renaming or
changing anything required raises it; and raising the review file's
`format_version` raises it too.** `standards/CLI.md`, "The standard's
version", states it for implementers. Rules out raising the version for
every addition, and a version per part of the standard.

Evidence: the phase-3 review found the standard said where its version is
reported but not when it changes, so an implementer could not tell what
"implements standard 1" promises. Revisit if implementers need to tell
optional additions apart (a minor version).

## 2026-11-11 — The repository is a standard, a page and skills; Perl is one backend

**Ruling (owner): the page and the review database are the main value, and
some people will not want to run Perl. The top level is language-neutral:
`standards/` (`FORMAT.md`, `review.sql`, `API.md`, `CLI.md`), `ux/` (the
page and its unit tests), `skills/`, `conformance/` (checks any backend
runs) and `perl/` (the first backend and the CPAN distribution). Each part
has its own agent instructions.** Packaging: the Perl distribution builds
from `perl/`; `perl/share/` holds committed symlinks to `ux/`, `skills/` and
`standards/`, and `[GatherDir]` with `follow_symlinks = 1` ships the real
files, so a checkout runs with no extra lookup logic.

The standard has a required core and room to extend: `CLI.md` lists the
commands, options and output the skills rely on, `API.md` the routes and
JSON the page relies on, with access as a layer (a token is one kind) and
content fetches that need no checkout, so a hosted variant can follow.
Implementations may add their own; the standard never uses commands or
options starting `x-<impl>-` or routes under `/api/x-<impl>/`, and anything
else an implementation adds may collide with a later version. The standard
is versioned as a whole; `review-helper version` and `standard_version` in
`GET /api/review` report it.

Rules out: copying the shared files into each backend, and lookup logic to
find them from a checkout. Revisit if a platform the distribution must
install on cannot hold symlinks in a checkout, or the standard's parts need
versions of their own.

## 2026-11-11 — One `review-helper` command dispatches to implementations

**Ruling (owner): `review-helper` is a short POSIX `sh` dispatcher at
`bin/review-helper`, symlinked into `perl/scripts/` so the Perl distribution
installs it. Implementations are executables named `review-helper-<impl>` on
`PATH`; `REVIEW_HELPER_IMPL` picks one, else the first found walking
`PATH` in order runs. `review-helper --which` lists every implementation
found, marks the chosen one, and prints the versions it reports.** The Perl
command is `review-helper-perl`. A fixed order of implementation names
would have named implementations nobody has written; the order of `PATH` is
the user's own choice.

Rules out: a dispatcher in any one backend's language, a configuration
file for choosing, and a fixed preference order of implementations. Skills
keep one command name whatever is installed.
Revisit if Windows users appear (a `.cmd` dispatcher, `PLANNED_TODO.md`).

## 2026-11-11 — `Changes` starts after the first release

**Ruling (owner, phase-2 review): `Changes` holds a single "Initial release"
entry until the first release; entries are added only after it.** `Changes`
is for people upgrading between releases, and changes made before the first
one mean nothing to them. Revisit at the first release.

## 2026-11-11 — The page renders one recursive node

**Ruling (owner): every level of the page (topic or commit, merge, file,
section, whole-file view) renders through one node: closed, an outlined
row; open, a card with a title head and a left line; one path to open,
close, hold the clicked head and stick heads; each level plugs in only its
color and content.** A merge lists its commits as rows that expand in place,
as a commit lists its files. This rules out per-level renderers with their
own row, head or hold logic, and the drill-down that folded a merge into a
label to show one commit.

Evidence: the owner found a merge's commits did not look openable, a
commit's title was larger than its merge's, merges and commits shared a
color, collapsed section rows were not outlined, and opening a commit in a
merge made the page jump and hid the merge's other commits. Revisit if a
level needs a presentation the shared node cannot give it.

## 2026-11-11 — Flags are yellow or red; every other note is the journal

**Ruling (owner, flag rework after the phase-2 review): a flag is the agent
asking for the reviewer's judgment. It is yellow, "a reviewer may object"
(a decision, a deviation, a problem solved in a way that may not be
accepted), or red, "this is probably a problem" (unsolved; nothing solved is
red). Every other note is a journal entry. There is no unflagging.** It
rules out severity levels, a resolved state, and reviewers clearing flags
for each other. The owner's answers:

- **Colors.** Only the agent sets a color, and recolors a flag with a reason
  for the last change; no history. A yellow flag turned red is unseen for
  everyone again.
- **No unflag.** A red flag needs action; a yellow one is settled by
  seeing it, or commented on.
- **Journal entries.** `review-helper note` records one by default;
  `--flag yellow` or `--flag red` records a flag. `build` and `iterate` copy
  the entries into that iteration's journal with their path and unit: no
  sheet item, no placement, no lines. An entry belongs to the iteration it
  was taken into (not carried), takes no comments, has no badge and is not
  walked. The page lists them in a Journal section at the very end, a
  bullet each that opens its details, labelled as things the agent noted
  that probably need no attention; a click on its path goes to the file.
- **Badges** count yellow and red flags, colored by the worst. **Walk new**
  and **Walk all** differ only by seen and unseen; the summary groups flags
  by color, each group with its own pair.
- **Carry:** a flag whose code changed is kept, recolored (with a reason)
  or retired.
- **Kept:** seen per reviewer, comments on flags, the kind label.
- **The speech stop button** is a black square everywhere, not the stop
  character, which phones draw as a yellow or orange emoji.
- **The sweep** (owner, option C after the review): `build` and `iterate`
  take every journal entry in the worktree's journal, by design, even one
  recorded for other work; no filter by path and no switch to skip them.
  review-pr and review-project ask before building when the user may have
  recorded notes in the worktree for other work.
  Revisit if entries from unrelated work turn up in reviews.

Also ruled for flags (owner and review rounds):

- **Seen** is set when the walker moves past a flag (the last one when the
  walk is left), moving back included; on clicking a flag or a link to it
  (the walker's own jumps do not count); and on commenting, by the server
  as part of adding the feedback. The popup's toggle clears it. A mark the
  reader sets by hand while the walk shows a flag holds for the rest of
  the walk.
- **Comments:** a flag is a feedback target, by its note id. Flag feedback
  keeps the flag's commit and path (and topic for a whole-commit flag) and
  its sentence (`flag_sentence`), so a retire turns it into `file` or
  `commit` feedback that still reads "was on flag: <sentence>".
- **Carry:** unchanged code (moved lines, a renamed file) carries a flag
  with its seen marks and comments. Changed code makes it pending, without
  seen marks, on the span of its lines the diff kept (the whole file when
  none); the agent cannot place it again, so when it fits no lines it is
  retired and a new note recorded. `sheet --all` lists every flag, so an
  agent answering a comment can recolor or retire one whose code did not
  change.
- **Icons:** no number on or beside a flag icon (it would read as a count);
  what the color means is in the hover title and the popup. No dark tokens
  yet: the page has no dark theme.

Implementer's calls, pending the owner:

- **Storage: a `journal_entry` table**, not flag rows with a kind. Entries
  have no commit, lines, seen marks, comments or carry; in `flag` they would
  need a nullable `commit_id` and a kind filter in every flag query (badges,
  walks, seen marks, carry, export).
- **Names:** columns `color` and `color_reason`, the sheet decision
  `recolor`, and the journal note field `flag`, as the `--flag` option;
  American spelling, as the code and documents use.
- **Skills:** review-pr records its notes before building, as
  review-project does, so the build takes their entries; review-add-notes,
  which adds no iteration, records flags only. A fix round's findings are
  journal entries unless the fix changes behavior a reviewer should check.
- **An entry's link** goes to the file in the last entry of the iteration
  that changed its path, on its default tab; an entry on a file the
  iteration did not change names it without a link. A closed entry drops
  its details, so a reading of it pauses as a collapsed card's does. An
  entry's path stays required, as every note's.
- **The header has no flag menu**: with no minimum severity and no "show
  unflagged" it had nothing left. A color group's walk is `w=fr`, `w=ay`
  and so on in the URL.

Revisit if: reviewers ask for a level between yellow and red, or a way to
clear a flag for later reviewers.

## 2026-11-11 — Seen flags always show

**Ruling (owner, final phase-2 walk F5, option C): there is no "show seen"
setting. Seen flags always show, marked with an eye; Walk new skips them and
Walk all includes them. Badges count flags seen or not.** It rules out
hiding a flag because you have seen it.

Evidence: with Walk new and Walk all, the toggle was a third way to say
which flags to walk, and Walk all with it off was a walk of unseen flags
again. The eye already shows what you have seen. Revisit if reviewers of
large reviews ask to hide what they have seen.

## 2026-11-11 — The header wraps rather than cut the identity

**Ruling (owner, final phase-2 walk F4, option A): when the header does
not fit on one line, the controls move to a second line under the
identity and title; on a wide screen it stays one line.** The walk pair
is normal size, and the page under the header follows its real height.
This rules out cutting the identity short to keep the header one line, and
shrinking controls to make room.

Evidence: with Walk new and Walk all in it, a one-line header cut the
identity to a few characters at 1280 pixels. Revisit if the controls alone
need more than one line at desktop width.

## 2026-11-11 — No warning on where a review file is kept

**Ruling (owner, final phase-2 walk F1, option B): build and import do not
warn when a review file inside the worktree is tracked or not ignored, and
the VCS role has no `is_tracked` or `is_ignored`.** A file outside both the
worktree and the VCS's own directories is still refused. This rules out
checking the VCS's ignore rules for a review file.

Evidence: the phase-2 complexity audit; the two role methods served only
these two warnings, and an agent or human already sees an untracked review
file in `git status`. The default location never dirties a working tree.
Revisit if review files turn up in commits.

## 2026-11-11 — Flag walks start as Walk new or Walk all

**Ruling (owner, dogfood6 item 7, option A): a flag walk starts from one of
two buttons, "Walk new (N)" and "Walk all (M)", in the header and on each
color group in the summary.** Walk new walks the flags not seen yet, from
the first of them (so starting it again resumes); it is disabled when none
is unseen. Walk all walks every flag, seen ones too, from the first. The
mode stays in the URL (`w=f` new, `w=a` all). This rules out a mode switch
inside a running walk, such as a "skip seen" checkbox.

Evidence: dogfood6 review; a single Walk flags button with a "skip seen"
checkbox hid which walk was running and whether starting again would
resume.

## 2026-11-11 — The guide has next and previous topic

**Ruling (owner, dogfood6 item 1): the guide bar has "next topic" and
"previous topic" ("commit" in a review not split into topics).** Next goes
to the first later step whose group differs from the current step's;
previous to the first step of the run of steps before the current step's
run. A step's group is the entry its target is on (file, section, diff and
view steps: the commit and the path's topic; a flag step: the flag's);
summary steps have none and are passed over. Each hides when there is
nowhere to go. Flag steps passed over are not marked seen. This rules out
grouping by position (a guide may come back to a topic; that visit is a run
of its own).

Evidence: guides that dwell on one topic or jump around had no quick way
past it. Revisit if guides gain explicit chapters.

## 2026-11-11 — The flag popup keeps what is typed

**Ruling (owner, dogfood6 item 11, a bug): no popup action or redraw (the
seen toggle, Escape and reopening) discards an unsent comment; drafts stay
until sent or cleared by hand.** This rules out clearing the box on a
redraw. Drafts (and comment edits left open) live in the page, so a reload
still loses them.

Evidence: a popup redraw erased a typed comment.

## 2026-11-11 — `feedback --publish` builds each comment

**Ruling (owner, dogfood6 item 17): `review-helper feedback FILE --publish`
prints, per item to post, the exact final text (location prefix included),
the line comment anchor when one fits, else null, and the general comment to
fall back on. The prefix table lives in code; review-publish passes the text
verbatim.** The line-fit check is in code, decided from the review file alone
(its latest iteration stands for the request), so a line in a file a later
commit changed is never posted at stale line numbers. This is the one forge
concern the tool keeps (see "Forge access is the agent's").

Rules out: prefix tables and line-fit checks in the skill, and git or forge
queries in the check.

Evidence: owner item 17 of the dogfood6 audit walk; the phase-2 docs review
found that with the check in the skill gone a line in a file changed later
in the request would post at stale lines. Revisit if the review's latest
iteration often lags the request, so that anchors miss.

## 2026-11-11 — Repository access goes through a VCS role

**Ruling (owner, D4 and D4b): `Review::Helper::VCS` is a Role::Tiny role
(its `requires` is the interface) and `Review::Helper::VCS::Git` is the one
implementation, holding every git specific: commands and options, ref names
under `refs/review-helper/`, the git directory and `--git-path`, pathspec
magic, patch ids, remerge diffs and `user.name`/`user.email`. Every other
module asks the VCS object for concepts and builds no git string.
`Review::Helper::VCS->detect($path)` returns the implementation and is where
another VCS would be added.**

The neutral names the role and the format use (revision, empty revision,
path spec, pin, change id) are defined in `standards/FORMAT.md`. Keys named
`commit` stay: they name the commit, by its revision id. The skills keep
their git commands, since the agent drives its own VCS. Objects keep their
`repo` attribute.

Rules out: git commands, ref names or git paths outside
`Review::Helper::VCS::Git`, and a plugin or configuration mechanism for VCS
choice before a second implementation exists.

Evidence: dogfood6 review #5 (owner): "don't bake in git-only; make a
source-control role, implement Git as one implementation, obvious way to add
others later; rename/restructure now." Revisit when a second VCS is added
(detection order, and whether the interface fits it).

## 2026-11-11 — The schema is a file of the standard

**Ruling (owner, D2): the review file's schema is `standards/review.sql`,
not code in a backend.** `Review::Helper::DB` reads it from the share
directory (`standards/review.sql` there, as the page assets are found, from
a checkout or an install) and runs it as is. The format specification names
that file as the schema and describes the tables and columns in prose
without repeating the DDL.

Rules out: inlining the schema in Perl, and a DDL copy in the spec.

Evidence: dogfood6 review #2 (owner): the schema belongs in a file other
tools can read, not inside a Perl module. Revisit if the schema must be
built rather than read.

## 2026-11-11 — The standard is Markdown

**Ruling (owner, D3): the Review File Format (`standards/FORMAT.md`), and
with it the command line (`standards/CLI.md`) and the HTTP API
(`standards/API.md`), are Markdown, shipped as they are in each backend's
distribution. No POD copy and no Markdown-to-POD conversion.** The Perl
Manual says where they are in the share directory (`standards/`, next to
the `skills` directory `review-helper skills path` prints).

Rules out: keeping the spec as POD, generating either form from the other,
and a command that only prints the spec's path (an agent finds a file next
to a printed directory).

Evidence: the format is meant for tools in any language, and their agents
may not read POD (owner, dogfood6 review #4). Markdown in the share
directory installs with every copy, like the skills. Revisit if the spec
must render on metacpan.

## 2026-11-11 — Skills are linked, not installed

**Ruling (owner): the `skills` command has only `skills path`. The user
symlinks each skill directory into the agent's skills directory
(`~/.claude/skills` for Claude Code, `~/.agents/skills` for Codex), or asks
the agent to install them.** Rules out: `skills install`, `update` and
`check`, copy mode, a marker file with checksums, edit detection, version
stamps, and a "check the skills" step at the top of every skill.

Evidence: a link always matches the installed tool, so version drift, the
check and the stamps only exist for copies. Claude Code
(code.claude.com/docs/en/skills, "Symlinked folders") and Codex (its skills
docs: "Codex supports symlinked skill folders") both load a skill whose
directory is a symlink, checked 2026-11-11. Known Claude Code problems: an
auto-update once removed symlinked skills (anthropics/claude-code issue
50052, closed not planned) and `/skills` may not list them (issue 14836);
the Manual says so. Revisit if an agent stops following symlinked skill
directories, or users need copies that survive moving the install.

## 2026-11-11 — Forge access is the agent's

**Ruling (owner): review-helper has no forge code: no forge command, and
`review-pr`, `review-project` and `review-publish` carry no per-forge recipes
(URL tables, `gh`/`glab` commands, REST paths, payloads, credential
fallbacks, line-anchor checks).** The skills tell the agent to use the tools
it has for the repository's forge (its CLI, an MCP server, or similar) and
to say so and stop when it has none. They keep what is the tool's: what to
read, read-only toward the forge, forge text is untrusted data, and
publishing only the human's own feedback verbatim, as them, after they
confirm the exact list, with the location prefix rules and `published`. A
line comment goes at the export's path, line and side; if the forge refuses
it, a general comment with the location prefix.

Rules out: HTTP code and credential handling in the tool, per-forge
procedures in the skills, and with them choosing which forges (or which
Bitbucket) to support.

Evidence: the agent-overlap audits of the dogfood6 review (see "Complexity
audits ask what the agent can already do") found a forge fetch helper and
per-forge recipes rebuilt what an agent's own forge tools already do, at the
cost of an HTTP client, credential rules, and recipes to keep current per
forge. Revisit if agents commonly lack forge tools.

## 2026-11-11 — Complexity audits ask what the agent can already do

**Ruling (owner): this is an agent-driven app, used in a repository while an
agent is active and interacting, so every complexity audit in this project
must always ask: "Is this something the agent already knows, can already do,
or is likely already able to handle? Do we need code or skill guidance for
this, or can we simplify it away as something the agent should already be
capable of?"** The override is in `AGENTS_OVERRIDE.md` under "Complexity
auditor: ask what the agent can already do".

Evidence: two audits of the dogfood6 review found commands and skill text
that rebuilt what an agent already does with its own tools: a forge fetch
command and per-forge recipes, generic git recipes in five skills, repeated
permission and severity boilerplate, and checks on fill input the agent and
page cope with. Revisit if the app gains users who drive it without an
agent.

## 2026-11-11 — A flag on a file that left the change goes pending

**Ruling (owner, A on dogfood6 item 8):** when an iteration carries a flag
whose path no entry of its commit changes any more (a squashed change to the
file reverted, so the file now equals the base), the flag is pending even
if its lines are unchanged, without seen marks. The sheet's flag item says
so (`about.left_change`); the agent keeps, recolors or retires it (usually
retires). A kept flag stays on its path, shown on its topic or commit card.
It goes pending only in the iteration its file leaves the change; once
kept, later iterations carry it settled with its seen marks (re-pending it
every iteration lost the marks).

Rules out: carrying such a flag silently as settled, or dropping it.

Revisit if: flags on reverted files turn out to be worth keeping as a rule.

## 2026-11-11 — Fill checks trimmed where the agent or the page copes

**Rulings (owner, agent-overlap audit 2):** `fill` has no checks or sheet
context an agent already handles, or whose failure only makes the review
slightly worse:

- **A3.** Guide view and highlight lines are checked for shape only
  (`[start, end]`, side), not against the file's length; the page marks
  only lines that exist. A view step still needs side `new` and a file
  that exists at its commit.
- **A4.** Split items do not give the changed line ranges; the agent reads
  the diff anyway.
- **A7.** A deleted section's `old_lines` need not hold a removed line;
  such a section stores with zero counts and is simply not shown.
- **A5.** Section names may start with `[` (an INI block such as `[server]`
  is a fine name), and a name may be a JSON number. Whitespace around
  section, package and topic names is trimmed instead of rejected; the
  topic-name control-character ban stays.
- **A8.** A guide target's or note placement's `commit` is a full revision
  id, matched exactly: no hex pattern, no unique-prefix lookup, no ambiguity
  handling. Every revision the agent sees in `about` is full.
- **A9.** A flag `keep` or `retire` ignores `color` and `reason`, null or
  not, instead of rejecting them; only `recolor` uses them.

Rules out: re-adding these checks without a case where their absence lost
data or broke the page.

Revisit if: bad guide lines or deleted sections confuse reviewers in
practice.

## 2026-11-11 — Review files are never upgraded or schema-checked

**Ruling (owner, D1 and A11):** no code upgrades or converts review files,
and opening one checks only `format` and `format_version` in `meta`. The
owner, verbatim: "was code written to upgrade/convert old files? if so we
should not do that, the code is not needed and adds unnecessary
complexity"; and of a full schema comparison, for an unreleased tool, "just
added complexity".

The format is unreleased, so version 1 still changes and nothing needs
carrying forward: a file an earlier build wrote fails with SQLite errors and
is rebuilt. Once the format is released, every schema change bumps
`format_version`. Imports keep one small check (A11): an export whose table
lacks a column this version has, or lacks a table this version exports, is
refused with "This review export was built by a different version of
review-helper; ...", since that column would otherwise load as null without
a word. A column the file lacks fails the insert loudly; unknown tables are
ignored. Data only older files could hold is not tolerated either: every
section is a unit with no kind, `split_source` is `none` or `agent`, and
request kinds are `debate` and `split`, in the schema, sheet, builder,
server and page.

Rules out: upgrading or converting files, comparing a file's or an export's
schema with this version's beyond that missing-column check, and
read-only fallbacks for old files.

Revisit if: format version 1 is released (from then on an older file needs
a defined path: an upgrade or a clear refusal by format version), or stale
files cause confusing errors in practice.

## 2026-11-11 — Every flag a badge counts shows on its card; no empty list tab

**Ruling (owner, dogfooding a squashed review): every flag a card's badge
counts must be visible on that card without the flag walker.** A file card
lists its whole-file flags and its line flags in none of the sections it
lists (with their lines); a commit or topic card its whole-commit flags (and
flags on files it did not change), in the card's info block whatever tab is
open, one line each, opening the gutter popup. Before, a whole-file flag
showed only as the head's badge and the walker's temporary callout; 165 of
326 file flags in the dogfood review were whole-file. Same report: **a file
(or commit) with nothing to list has no `list` tab and opens on its first
other tab** ("If list is empty it should probably automatically go to
diff").

Revisit if: a card's flag lines crowd out its summary in large reviews.

## 2026-11-11 — Extensions use an `x_` prefix, naming left open

The format says extension tables and columns are prefixed `x_`, and that how
an extension names itself within that prefix is part of the extension design
still to come (`PLANNED_TODO.md`). Every reader accepts any `x_` name; export
and import leave `x_` tables and columns out.

Implementer's call, pending owner confirmation: the format states what the
code does rather than requiring `x_<name>_`, which would pick an extension
naming scheme before the extension design is done.

Revisit if: the extension design settles how extensions are named.

## 2026-11-11 — Squashed reviews split by topic

**Ruling F6 (owner):** squashed reviews split by topic. The owner,
verbatim: "The goal of a squashed review is to avoid reviewing churn, no
reason to review how we use module X in commit A when commit B replaces X
with Y and rewrites the things that previously used X. So a squashed review
means reviewing the entire project, or an entire PR or branch (from its
branch point) only at its final head, not all the intermediate steps. But
for large ones (like our dogfood where we have a LOT) that leads to a single
giant commit, so splitting that to commits by topic (skills, perl backend,
frontend, etc), may make it easier to review."

Agreed design: the agent groups the squashed `base..head` files into named
topics; each topic is an entry like a commit (same base and head, limited to
its files) with its own what/why/how; a sheet `topics` item lists each
topic's name and files; `fill` checks every file is in exactly one topic and
names are unique and not empty; a small review may keep one topic (before
the agent fills it, one entry holds all files); topics pair by name on
iterate (files keep their topic, new files are asked for); the page shows
topics where commits are ("1. Perl backend"), each with its files.

Details, as built:

- **Shape: a `commit_entry` per topic**, no topic table. Each has the head's
  revision and parent and a nullable `topic` column; files in no topic share
  one entry with a NULL topic (before the first fill, the only entry). A
  unique index on `(iteration, revision, IFNULL(topic, ''))`.
- **Addressing.** Revision + path still names one entry, so flags, notes and
  file, section and line feedback anchor as before and resolve to the topic
  holding the path. Anchors with no path name the topic: `commit` feedback
  and requests (a nullable `topic` column on `feedback` and `request`, in
  the feedback export), guide `commit` and `diff` targets without a path
  (`topic` key, allowed on those two types only), and the note item (a
  `topic` field, needed only for a whole-change note). Flags in
  `/api/review` carry `entry` (their commit entry id); commits carry `topic`.
- **The `topics` item** is required while any file is in no topic, so every
  squashed review asks for it once; `about` gives `paths` and `unassigned`,
  `fields.topics` the stored topics. A sheet that fills it may not fill
  `commit`, `order`, `note` or `guide` items (they name entries the fill
  moves); the agent fills topics alone first.
- **Writing topics.** A topic keeps the entry of its name, else an entry
  with exactly its files (so naming the one entry, or renaming a topic,
  keeps its text); a kept entry whose files change loses what/why/how and
  reading order; flags follow their paths (a whole-entry flag on an entry
  that goes moves to the first topic); `commit` feedback and requests on an
  entry that goes become `review` ones keeping their revision and `topic` as
  context, which the page shows ("was on: Frontend"); on a renamed entry
  they take the new name. A stored guide step whose commit, topic or path
  no longer resolves makes the guide item not done, so the sheet asks again.
- **What carries on iterate** (same base and paths, per "carry-forward is
  only important for human feedback"): each file keeps the topic that held
  its path; new files go to an entry with no topic, last; a topic left with
  no file is dropped. Entries pair by topic name (NULL with NULL). Flags,
  marks, splits and file text carry by the existing rules; reading order
  when the topic lists the same paths; the topic's what/why/how only when,
  besides, every file reads the same after both (with the same base the
  change is then the same). Human feedback is never copied: a `commit`
  feedback or request with a topic shows under the topic of that name in any
  iteration.
- **Order:** per topic, as per commit; topics are listed in the order the
  agent gives them.
- **Skill guidance:** group by area or layer, a handful of topics, each
  readable in one sitting; tests go with the code they test, and a test
  suite spanning every area may be its own topic; topics in reading order.

Revisit if: agents choose topics poorly enough that reviewers regroup them
by hand, or commit-level feedback moving to the review on a topics refill
loses context reviewers needed.

## 2026-11-11 — Whole splits are stored; explanations never carry by content

**Rulings (owner, follow-ups to "Sections cover the whole file; the
agent-sections walk", after two reviews of that work):**

- **F1.** Every section of a split is stored, touched or not; a section is
  untouched when no `+` or `-` line falls in it. The page shows touched
  sections only, and the previous split given to the agent is therefore
  complete. The skill treats the previous split as context: use it while it
  fits, change or drop it freely when it does not. The owner: "it was not so
  that it adheres to it strictly ... the new agent can decide the previous
  split was not adequate or no longer applies".
- **F3 (c).** Nothing carries by content: a re-split file gets fresh
  explanations from the agent. A file whose change is identical between
  iterations still keeps its whole split and its text. **Principle (owner):
  "carry-forward is only important for human feedback on a review."**
  Carrying the agent's own text is worth it only where it is cheap and exact
  (F4); otherwise the agent writes it again.
- **F4 (a).** Commit and file explanations carry by patch-id, and identical
  files carry in squashed pairs: cheap, and saves agent work. A pure rename
  keeps its file text and, when unchanged across iterations, its split, by
  patch-id as any file.
- **F5 (b).** A commit that renames files says so in its facts,
  "Renamed: 3 files (2 changed)"; nothing is filtered.

Details, as built:

- "Touched" is derived, not stored: a section is touched when it is
  deleted or one of its `added`/`removed`/`changed` counts is not 0, which
  is exactly when it owns a `+` or `-` line. No column.
- Only touched sections get a sheet `section` item, come back from
  `GET /api/file`, and may be named by a guide `section` or `diff` step
  ("has no change to show" otherwise); the split item's `about.sections` and
  `about.previous` list them all.
- A re-split moves section-targeted debate requests whose section is gone
  to the file, keeping the section as context, the same as feedback; the
  page shows "was on" for them too.
- A split request on a file with no split item (a rename or mode change
  alone, besides deleted, image and binary files) is refused by the server
  (400) and not offered by the page, the same rule as the split item.
- `previous_split` gives none when its commit is not in the repository (an
  imported review whose earlier iterations were never fetched) rather than
  failing the sheet.
- `content_kinds` keeps each kind found per `REV:PATH` for the server's
  life; a VCS failure gives `text` for that load and is not kept.
- The renames fact counts `(0 changed)` too, so its shape is always the
  same: "Renamed: 1 file (0 changed)".
- The binary rename badge reads counts and sizes, so a same-size edited
  binary reads `renamed`; a fix needs a stored "same content" bit.
- A re-split keeps section feedback and debate requests only on a section
  with the same name and package that is still touched; on one it left
  untouched they move to the file, as for a section that is gone, since the
  page would no longer show them. The server refuses new feedback or
  requests on an untouched section (400, "has no section ... with a change
  to show").
- A guide step naming a section that a later re-split removed or left
  untouched makes the guide item not done (derived, no column), so the
  sheet asks for the guide again with its steps as they are; resubmitting
  them gives the usual "has no change to show" or "no section" error. Until
  the agent refills it the page plays that step as one whose target is
  missing: it shows its commentary without moving, and the tour goes on.
  Chosen over a page fallback to the file: the agent, not the page, decides
  what the step should show.

Revisit if: re-explaining re-split files costs agents noticeably more than
carrying by content would save, or stored untouched sections grow review
files enough to matter.

## 2026-11-11 — Sections cover the whole file; the agent-sections walk

**Rulings (owner, walk of the follow-ups to "Sections are the agent's"):**

- **R0a.** File cards have no "Split: agent" fact: every split is the
  agent's. Only "No sections yet" shows, for a file not split yet.
- **D1 (a, refined).** A split is either empty (`[]`: the file is one unit;
  the page shows the file and its diff, no section rows) or covers every
  line of the file with sections the agent named; deleted sections cover
  old-side lines. `fill` rejects a split that covers only part of the file.
  No section is made automatically (no `[body]` runs): the agent has the
  context to name the header, imports or a constant between subs usefully.
- **D2 (a).** Section feedback whose section disappears on a re-split moves
  to the file and keeps its old section name as context, which the page
  shows ("was on: sub foo"). Nothing is appended to the agent's response.
- **D3 (a).** An orphaned split request (its commit gone after a rebase) is
  always closed with the note "(closed: its commit was rewritten)"; it is
  not re-anchored by patch-id or to a new squashed entry. This applies to
  squashed pairs too.
- **D4 (a).** The server sends the kind of each file the page shows whole
  at a commit that did not change it (`content_kinds` in `/api/review`); the
  page keeps no extension table of its own.
- **D5 (b + A2).** A changed file is re-split whole by the agent, as built.
  While a file needs a split, its item gives the previous split as stored,
  with its commit and its lines at that commit; nothing is moved through
  the diff or marked changed, and the agent may map lines itself. The skill
  tells the agent to start from it and keep names and boundaries unless the
  code changed.
- **D6+ (skill).** A short secondary package or class in another package's
  file (about 100 lines or less) is one section named `package Foo::Bar` or
  `class Foo::Bar`; a long one is split into its subs or methods; the
  file's main package is never a section.
- **D7 (a).** Naming conventions always win; the previous split guides
  boundaries, and its names are kept only when they follow the conventions.
- **D8 (owner's idea).** A POD block shared by several methods (a bare
  method list, or several `=item` lines sharing one description) is its own
  section, named descriptively (`POD: method list`; the prefix is a
  suggestion). Documentation lines still never overlap; no validation
  change.
- **D9 (a).** `package` is agent-decided free text; the skill states it is
  the namespace the code is declared in (`package Foo::Bar;`), or none.
- **D10 (a + addition).** No split item for a file whose content did not
  change (pure rename, mode-only change): it is one unit with no section
  rows. Renames show clearly: the file row, card and tree show
  `old/path → new/path` with a badge, `renamed` or `renamed + changed`.

The owner's principles for the walk: simple to maintain; human data
(feedback, requests, reviewed marks) deterministic; the page works without
an agent; the format is a standard, so `standards/FORMAT.md` describes the
data and its validation exactly.

Details (owner-confirmed, F2):

- A covering split may leave blank lines out; a run of blank lines in no
  section joins the section before it (after it at the top of the file).
  Only a line with text must be in a section; requiring blank lines too
  would reject most splits over the gaps between subs. The coverage error
  is reported only when the sections are otherwise valid.
- The "was on" context reuses `section_name`/`section_package` on the
  moved feedback's `file` target, which the format already allows as
  context; no column or export field of its own.
- `content_kinds` is keyed `REV:PATH` and covers guide `view` steps and
  flags whose commit did not change the file; the kind is computed as for
  a file change (extension, Perl shebang, `binary` for a NUL in the first
  8000 bytes, null when absent). The page falls back to plain text.
- The rename badge reads the line counts and sizes, so a binary renamed
  with different bytes of the same size shows as `renamed`.

Revisit if: agents leave text lines out often enough that covering splits
cost more fills than they save, previous splits mislead agents on files
whose sections were mostly untouched, or a reader needs the kind of files
other than the ones the page shows whole.

## 2026-11-10 — Sections are the agent's

**Ruling (owner, option a): the agent filling the review defines each file's
sections; there is no programmatic splitter. `build` splits nothing; the fill
sheet's `split` item asks for every changed text file's sections, and `fill`
validates them strictly.** The owner, verbatim: "I wonder if the programatic
approach to building sections may be the wrong one. It is still a lot of
parsing and edge cases. But identifying sections is the kind of thing an
agent is good at, and this is already about having an agent provide code
review guidance. It already needs to pull everything into context, and needs
to comment on each section. Maybe it is better to just have the agent denote
the sections with a skill guiding it that subs, methods, braced constructs,
but not containers or flow control, and docs/comments for a sub attach to it
as its guidelines. Then also maybe note it can define additional sections if
something specific needs to be highlighted, such as something in a body
should get extra explanation, or split a sub into multiple groups that need
different commentary. I think the programmatic splitter does little to save
tokens when the agent already has so much context anyway." Then: "No need
to prototype, I think it is fine to proceed with it."

Rules out: splitting files in the tool by parsing, braces or indentation.
Brace, PPI and Markdown splitters were built and dropped for this; their
corpus results are in git history.

As built (the design agreed with the owner, and what follows from it):

- A split section is `{name, package, lines, first_line, doc_lines,
  old_lines, old_first_line}`. `fill` rejects ranges outside the file,
  overlaps (code or docs, and on the old side), a `first_line` that is not
  the file's text there (whitespace at the ends aside), and empty or
  repeated names. A section with only `old_lines` is a deleted unit;
  `old_lines` on a live one claim lines removed at its edge. Every other `-`
  line goes to the section holding the next new-side line.
- Every split is the agent's: the page offers only a `split` request, and
  `split_source` is `none` or `agent`.
- The page says when a file has no sections yet, and renders a unit's
  documentation as POD only when it is POD.

Details (owner-confirmed, D6):

- A repeated name in a package is rejected, not numbered automatically:
  the agent adds ` (2)`.
- `lines`, `doc_lines` and `old_lines` are all range lists, so a sub with a
  part carved out as its own section keeps the lines around it.
- A changed file is split again whole: unchanged units are not kept at
  moved positions without the agent; the agent restates them, starting from
  the previous split, and explains them afresh.
- Splits are optional in a squashed iteration, like its sections and
  reading order; deleted files get no split and no sections.
- No CLI command for numbered lines: `git grep -a -h -n '' <sha> --
  ':(top,literal)<path>'`, already allowed to the skills, prints them (`-h`
  drops the `<sha>:<path>:` prefix from every line, `:(top,literal)` keeps
  glob characters in the path from matching other files from any
  directory, `-a` reads a file marked binary). The tool's own per-file
  diffs use `:(literal)` too, with git's pathspec mode variables cleared.
- Section feedback stays on its iteration's commit; "reviewed marks" exist
  only on flags.
- `fill` also rejects a first line given without its ranges, and an
  overlap error names the other section. A section with a bad name still
  claims its lines, so overlaps with it show in the same fill.
- `doc_html` renders only when every doc range starts as POD, so comments
  after a `=cut` are not dropped from the docs panel.

Revisit if: agents split badly or inconsistently enough that sections
drift between iterations, or the token cost of splitting large files shows
in practice.

## 2026-11-10 — A flag lives on a file its commit changed

**Ruling (owner): a note is placed only on a file its commit changed, as
`fill` enforces. A view of a file that commit did not change (a guide `view`
step at the head) therefore shows no flags; the skill and docs say so
instead of promising them.**

Found dogfooding: ARCHITECTURE and the review-build skill said a head view
showed the flags anchored at that commit, which placement could never
produce. The alternative, placing notes on any file present at the
commit, was declined: a note about existing code a commit affects belongs
on the changed file that causes it. Revisit if reviewers need flags on
untouched files.

## 2026-11-10 — Access: no token on loopback, a token beyond it

**Ruling (owner): the local server asks loopback clients for nothing: no
per-run token or cookie. It keeps the Host check, JSON-only POSTs and the
Origin check, shows whose review and which repository in the page header,
and warns when a tab's review id differs from the server's. `serve --listen
ADDR` (and `REVIEW_HELPER_LISTEN`) may bind another address, printing a URL
another machine can open; then clients other than loopback need a token: a
random launch token in the printed URL, traded for an `HttpOnly`,
`SameSite=Lax` cookie.**

The owner runs this on machines whose other users are trusted (work behind a
firewall, a personal laptop). The real local risk is opening someone else's
port by accident and being confused, which a token answers only with a 403;
the identity header and stale-tab check answer it directly. The web-page
threats (DNS rebinding, cross-site POSTs) stay blocked by the other checks.
That trust does not extend to everyone on the network. The owner, verbatim:
"add the addr so I can open it directly. My use case at work involves
running it on a shared dev server and accessing it from a browser on the
laptop, so we need to be able to open it to the local network". The Host
check also accepts the machine's own names and `--allow-host` names. The
check is shaped as "who is asking" (loopback, launch token, and later
perhaps per-collaborator tokens stored in the review file), so a hosted
variant can put another layer there (`standards/API.md`, "Access").

The cookie is `Lax`, not `Strict`: a token link clicked inside a web app
(Slack, Gmail, a ticket) is a cross-site navigation, so a `Strict` cookie
set by its redirect was not sent on the page load that followed, and the
page answered 401 on every click. `Lax` costs nothing here: every GET route
is read-only, POSTs need a JSON body and a matching `Origin`, and `Lax`
still withholds the cookie from cross-site background requests such as
POSTs.

Revisit if: the tool is run on machines with untrusted local users,
per-collaborator tokens are adopted, or the tool is served over a network
that is not trusted (the token travels over plain http).

## 2026-11-10 — Feedback and request forms open under the clicked header (trial)

**Ruling (owner, for now): a head's feedback and request forms open directly
under the head whose button was clicked, above that card's content, with no
scrolling. The clicked head stays put as the form opens, is sent, or is
cancelled. Posted feedback and requests stay where they were, in the card's
notes after its content. This covers commit, file and section heads and the
summary.**

Owner, choosing option a of the placements offered: "lets try a for now".

A form appended to the card's notes after its whole file or section list
and scrolled into view moved the clicked head, against the owner's rule that
a click in a section header never moves it. A trial: revisit if the form
under the head proves awkward in use (for example, pushing a long card's
content down while it is open).

## 2026-11-10 — A file's list, diff and file are tabs

**Ruling (owner): an expanded file's head carries three tabs, `list | diff
| file`, acting like a commit's tabs and kept consistent with them.** The
owner: "the file sections should have 3 tabs, 'list', 'diff', 'file', with
list being the default, it should act like the commit view with tabs, make
it consistent." `list` shows the file's sections; `diff` or `file` shows
that panel in their place, the sections' state kept. The default is the
first tab the file has (`list` unless it has nothing to list). The file head
has no `diff` or `file` widget buttons, on the card or in the tree; a
section keeps its own. Confirmed by the owner (2026-11-10): the facts and
summary stay above every tab, as a commit's summary does; a deleted file has
no `file` tab and a binary file no tabs (one choice); an image's `diff` is
its before and after pictures and its `file` the picture after; an SVG's
picture is on its `file` tab, above its source.

Revisit if: readers need a file's diff and its expanded sections on screen
together.

## 2026-11-10 — Changed files are blue, whatever their size did

**Ruling (owner): an existing file whose content changed is blue, whether it
grew, shrank, or kept its size; new files are green and deleted red. No
grew/shrank colors.** The owner: "I think thats too complicated, just blue
for files that were changed but already existed". A rename alone is
uncolored. The `~` changed line counts are purple, so they stay apart from
the changed-file blue they sit beside.

Revisit if: readers miss seeing at a glance which files grew.

## 2026-11-10 — A commit's list and diff are tabs

**Ruling (owner): each commit's head carries tabs like the Unified | Side by
side switch: `list | diff`, defaulting to `list`, plus `resolution` on a
merge that resolved conflicts; a merge has no `diff` tab. `list` shows the
files (or a merge's commits) as rows; `diff` (or `resolution`) shows only
the commit's whole diff (or the merge's resolution) in their place, right
under the commit's summary. Switching is a mode, not open and close; the
list's expansion state is kept in the URL while on another tab. There are
no commit-level `diff` or `resolution` widget buttons.** A target inside the
commit (tree, guide step, flag link, walker) switches it to `list`, a `diff`
guide step switches it to `diff`, and leaving the step restores the
reader's tab.

The owner wanted the two views to read as alternatives: a toggle button
read as open and close, and a panel opened below every file row was out of
sight in a squashed review of a hundred files.

Tabs show only when there is more than one choice, so a merge without a
resolution shows no tab group at all, not a lone `list`. A tree commit node
has no diff button: the tabs on the commit's card are the one place to
switch. A panel a commit has no tab for (a hand-made URL, or an agent's
`diff` step on a merge) shows the list, or the resolution for a merge `diff`
step when the merge has one. The tabs and the header Unified | Side by side
switch are radio groups (one checked, arrow keys move it).

Revisit if: readers need a commit's diff and its expanded files on screen
together.

## 2026-11-10 — Leaving a guide step undoes what the step changed

**Ruling (owner, option a): leaving a guide or flag-walker step, to another
step or out of guide mode, also collapses any file or section that step
expanded, and reopens a commit panel it closed to show its target; anything
the reader had open before the step stays open. The step records what it
changed (slot and value before) in the URL (`u=`) when entered, and leaving
undoes exactly that.** Recomputing what to close from the step alone, with
no record, closed panels the reader had opened too and left every file a
step expanded open, so a tour slowly expanded the whole page.

Revisit if: the record grows the URL past what browsers keep, or readers
want a step's layout kept after leaving it.

## 2026-11-10 — `sheet --all` reopens filled items for rewriting

**Ruling: `review-helper sheet --all` also lists the latest iteration's
filled items with their current field values and revisions, so `fill` can
replace them; the existing revision check still rejects overwriting a change
made since the sheet was taken. No new subcommand.** Summaries, commits,
reading orders, files, sections, splits, flags, finished debates and the
guide come back. Placed notes are flags by then and do not. Feedback stays
listed only while open, since its status may be the reviewer's; human text
(feedback bodies, request notes) is never a field. Skills use `--all` only
when the user asks for a rewrite or to correct text the agent itself got
wrong.

A filled item dropped out of the sheet and so had no revision, and `fill`
refused it: an agent could not sharpen its own text, and rewriting a review
meant clearing the database by hand.

Revisit if: agents start rewriting text unasked, or human-edited
explanations become possible (they would need protecting from `--all`).

## 2026-11-10 — Capture::Tiny for Perl-level output; git keeps fork and exec

**Ruling: in the Perl backend, Capture::Tiny captures output where it is
simpler — the port command's stdout in `serve`, and tests capturing Perl's
own output. Running git in `Review::Helper::VCS::Git::_run` keeps its own
fork and exec.** Capture::Tiny redirects Perl's STDOUT and STDERR handles,
but git inherits descriptors. When those handles are elsewhere (an in-memory
STDERR, as in the tests) git's errors escape the capture; the capture takes
STDOUT's layers and decodes git's bytes; and it cannot feed input, which
`patch-id` needs. Making it work took aliasing handles onto descriptors 1
and 2 and redirecting descriptor 0 by hand, more code than the fork it
replaced. `_run` instead sets descriptors 0 and 2 in the child and reads a
raw pipe. `VCS::Git::user` also stays a plain `open '-|'` pipe, as a capture
there would need the same aliasing. `serve` does alias STDOUT onto
descriptor 1 for the port command, which is still shorter than a fork and
close-on-exec errno pipe, and lets the command's errors through untouched.
Tests in `perl/t/unit/VCS/Git.t` (`running_git`) pin raw bytes under UTF-8
layers, an in-memory STDERR and STDIN, a closed descriptor 0, and a missing
git; and `perl/t/unit/CLI/Serve.t` (`port_command`) pins the port read past
an in-memory STDOUT.

Revisit if: Capture::Tiny gains descriptor-level capture without layers, and
input.

## 2026-11-10 — Squashed iterations carry as one entry

**Ruling: when the previous and the new iteration are both squashed with
the same base and pathspecs, their combined entries are matched for
carry-forward, by topic name when split into topics ("Squashed reviews split
by topic").** Flags carry by the flag rules ("Flags are yellow or red; every
other note is the journal"). A file's explanations carry only when its
content is identical. The entry's what/why/how carries only when every file
of the entry reads the same after both; otherwise it, and the review
summary, are left for the agent to rewrite. Reading order carries only when
the entry lists the same paths. An open split request on the old entry is
closed, not moved. With another base or pathspecs, or when either side is
not squashed, nothing is matched.

Carry-forward matched commits by patch-id, which a squashed entry lacks, so
every iterate of a squashed review lost everything: one real review went
from 167 flags to none, with its reviewed marks. The pairing reuses the
existing carry; only the matching step differs.

Revisit if: iterating onto a new base should still carry.

## 2026-11-10 — Only the author edits their feedback

**Ruling: a reviewer can edit (body or status) only the feedback they
wrote; others reply with feedback of their own. Feedback with no reviewer,
written before reviewers were recorded, stays editable by anyone.** The
server answers 403 with `{"error": ...}` for anyone else's, and the page
shows no edit control there. Agents' `fill` writes (responses, status) are
not edits of the human's text and are not limited. Nothing deletes feedback
and the page does not edit requests, so neither needs the rule yet; one that
is added follows it.

With reviewers recorded, an edit by someone else still showed the original
author, putting words in their name.

Revisit if: exports carry feedback between reviewers (`PLANNED_TODO.md`,
"Exports that carry feedback"), which must decide whose edits win on import.

## 2026-11-10 — review-publish may post the human's own feedback

**Ruling: posting the human's feedback verbatim, through the human's own
account, after the human confirms the exact list, is allowed under the shared
AI/LLM policy.** The agent only transports text the human wrote; the one
mechanical addition is a location prefix when a line comment cannot anchor.

Revisit if: the skill ever adds or rewrites text.

## 2026-11-10 — Share reviews through a JSON export, not the SQLite file

**Ruling: reviews are shared with `review-helper export` / `import`: a
standard text JSON form of the review file, committed on the PR branch at
`.review-helper/<name>.json` or attached to the PR.**

Chosen over sharing the `.sqlite` file plus an `adopt` subcommand (the
complexity auditor's recommendation: no second representation), over
committing the SQLite file, the journal alone, or git notes. The export is
diffable and readable by any implementation of the format. It leaves out
feedback and requests: each reviewer keeps their own.

Revisit if: keeping the export in step with the schema proves costly.

## 2026-11-09 — One static page, vanilla JS, JSON API

**Ruling: the UI is one static HTML shell plus vanilla JavaScript that talks
to a JSON API. No template engine, no jQuery, no JS framework or build
step.**

Theming (colors, fonts, panel styling) follows Test2-Harness-UI's CSS; its
stack (Text::Xslate shell, jQuery) does not carry over. The page is a single
view, so a template engine has nothing to fill, and modern DOM APIs cover what
jQuery provided. View state lives in the URL so back and reload work with no
server session. With no build step the same files serve from every backend.

Revisit if: the server needs to render dynamic page content.

## 2026-11-09 — Review content comes from the repository; pins keep iterations

**Ruling: the review file stores only review data. Diffs, file contents, and
images are read live from the repository under review, through the VCS
(git specifics in `Review::Helper::VCS::Git`).**

- One SQLite file per review, inside that repository. Default, for git,
  `$(git rev-parse --git-path review-helper)/<name>.sqlite`, which is
  per-worktree; any path inside the worktree also works. The repository is
  found from the file's location, never stored.
- Stored: revisions, metadata, agent explanations, feedback, debates,
  iterations. Diffs are generated with fixed options, ignoring user diff
  config, so line anchors stay stable.
- Agents never write the database directly: `review-helper sheet` emits a
  JSON fill sheet, `review-helper fill` validates and imports it.
- Each iteration's head is pinned (git: `refs/review-helper/<review-id>/<n>`,
  the shared ref store) so rebases and gc do not erase earlier iterations. A
  registry in the common git dir maps review ids to file paths.
- `review-helper prune` is the only cleanup path: it removes the pins of
  closed reviews and of reviews whose file is gone, takes no ref or path
  arguments, and never deletes review files. It is narrow enough to allow
  outright, and the build skill runs it automatically.

Rejected: a self-contained file (JSON or tarball) carrying all content —
larger, duplicated, and JSON cannot take incremental writes safely.

Revisit if: reviews must be shared or viewed without the repository.
