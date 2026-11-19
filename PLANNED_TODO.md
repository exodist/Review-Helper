# Planned work

Features and changes the owner has asked for but deferred. Each entry says
when it is wanted. Remove an entry once it is built; git keeps the history.

## Keep the standard from blocking a hosted variant

Owner, 2026-11-11: the schema (`standards/review.sql`) in SQL that PostgreSQL
and MySQL also accept. Access as a layer and content fetches that need no
checkout are already in `standards/API.md`.

## Clean up

- review-publish: before posting line comments, compare the pull request's
  current head with the `head` that `feedback --publish` prints, and stop
  and ask to iterate the review if they differ.
- Smaller items found while reviewing phase 2: a friendlier error when an
  import has a column this version lacks, stale test names in
  `perl/t/unit/VCS/Git.t`, `DB->open` connecting twice, a bad journal line
  warning twice, the `repo` attribute name holding a VCS object, and whether
  to bring back a test of the spec against the schema.

## Conformance for every backend's shared rules

Owner, 2026-11-19 (option A): on a branch of its own, move into
`conformance/` the checks of behavior every backend must have that only the
Go backend's unit tests cover today, then drop those unit tests:

- the publish list's prefixes and line-fit rules (renames, merged-in
  entries, a squashed iteration's old side);
- the export layout (keys sorted, one row per line);
- the access layer: host check spellings, static path safety, POST
  `Content-Type` and `Origin` checks;
- merge nesting and pairing commits across iterations (by change id,
  first with first; by revision only when not squashed);
- fill's writes: a topics rewrite moving or renaming commit feedback and
  requests, a retired flag's feedback becoming file or commit feedback, a
  re-split moving section feedback and requests, debates turning `done`,
  addressed feedback's `resolved_in`, and guide refusals.

## Agent interaction in the browser

Owner, 2026-11-11: wanted before the first release (it missed 0.001). In
the agent-hosted setup this is the listening agent below: the agent that
launched the review answers questions asked on the page. Talking to an AI
from the page with no local agent (an AI API account) belongs to the
centrally hosted variant.

### Discuss with a listening agent

Owner, 2026-11-10, asked whether the agent that launched the review can
watch the file and answer in real time, with a "discuss" option next to
"request" so the human can ask about the section they are in and the agent
replies on the page. Owner: "I think we should do it, but only when an agent
launches the review helper so it can indicate it is listening, when a human
launches it with no agent the widget should not be present."

Evaluated shape: threaded messages anchored like feedback (new table, format
change); `review-helper watch FILE` blocks until a new human message and
prints it with its context (commit, path, section, code, explanation); the
agent replies through `fill` and watches again; the page polls (the server is
single-threaded, so no long-held connections). Debate requests could fold into
it. Open before building: how "an agent is listening" is signalled,
prompt-injection limits (loopback needs no token, so any local user reaching
the port could post), one listening agent per review, and when the loop stops.

Owner, 2026-11-10: hold this until the current style and features are
finalized; walk its decisions then. Prepared first decision: how the page
knows an agent is listening (a launch flag, a heartbeat written by `watch`
and shown while fresh, or both; recommended: the heartbeat, with a thread
note when it goes stale). The rest: prompt-injection limits, one listening
agent per review, when the loop stops.

## A centrally hosted variant

Owner, 2026-11-11: a company or group deploys the app on a web server with
PostgreSQL or MySQL, real user accounts and security. People post their
reviews there, review them, and ask others to review. Reviews then need repo
and revision metadata so collaborators can find the code. Agent features on
the page need an AI API account there, since no local agent is attached. The
CLI and the skills can then support both the agent-hosted and the centrally
hosted setups.

## More backends

Owner, 2026-11-18: add Go and Node backends, each a full implementation of
the standard checked by `conformance/` (its fixture builder runs with any
backend). Go is built (`go/`); Node is next.

- **Node** (`review-helper-node`): runs where agent tools already have node,
  via `npx`; SQLite and HTTP built in (`node:sqlite`, node 22.5 or later), so
  it can ship with no dependencies, and can share code with the page.

A Python backend once someone wants one. A `.cmd` dispatcher if native
Windows users appear, with, for the Go backend, a Windows file lock in
place of `flock` and `.exe` names accepted: until then Go supports Windows
only through WSL.

## Extension points in the review file format

Owner, 2026-11-10: "after the intiial work we are doing, we should look for
good extention points in the file, what other tables we may want to define,
columsn we may want/need to add, how extentions should have prefixed table
names, would it make sense for allowing other tools to write with custom
columns added to tables with a prefix? That should probably also mean moving
the table schema from the .pm file to share/schema.sql. Things like that."

Starting points:

- `standards/FORMAT.md` ("The review database") currently says
  only: readers ignore unknown tables and columns; the standard never
  defines an `x_` name, and an implementation's own start `x_<impl>_`, as
  in `API.md` and `CLI.md`. How extension data travels in an export is
  still open.
- Candidate questions: an extension registry in `meta`; prefixed columns on
  core tables vs. side tables keyed by core ids; who may write what; how
  extensions version themselves; what tables other tools would want (review
  runs by several agents, per-flag authorship, CI results, coverage, links
  to tickets).
- Whether and how extension data travels in exports
  (`standards/FORMAT.md`, "Export", leaves `x_` tables and
  columns out in version 1; an earlier draft carried their
  `CREATE TABLE` text and ran it on import, which means executing SQL from a
  file that arrives with a PR).
- Done (D2): the DDL is `standards/review.sql`, one canonical file other
  implementations read.

## Test-run evidence per review phase

Owner, 2026-11-10: "Future extension to track: test result urls or filenames
attached at point in time, proof of test runs at eaxh review phase."

Likely shape: per iteration, a list of test runs (URL or file name, when,
which head it ran against, pass/fail), shown in the page beside the
iteration and settable through a CLI subcommand. Candidate for the first
extension table under the extension design above.

## Rebases between reviews (important)

Owner, 2026-11-10: "Important feature, should be able to handle rebases that
occur between reviews and changes on a single review file."

Already in place: each iteration pins its head; explanations, reading order
and flags carry forward by patch-id; feedback anchors to commits that stay
pinned. Since closed: split requests orphaned by a rebase are closed
(RULINGS.md "Sections cover the whole file; the agent-sections walk"), and
carried flags move with their lines. Squashed iterations carry forward too
(RULINGS.md). Known gap to close: feedback on a rebased-away commit not
following its code to the new commit. Section text no longer carries by
content at all (RULINGS.md "Whole splits are stored; explanations never
carry by content").

## Exports that carry feedback

Owner, 2026-11-10: "I will eventually want B, but it can be a follow-up" —
B being: JSON exports carry feedback, requests and seen marks, each
tagged with its reviewer, and import merges them by reviewer. Ruling C
(feedback stays local) holds until then; the reviewer column added with the
flag walker already serves people passing the `.sqlite` file itself.

Needs a design pass before work: stable ids for feedback so re-import
deduplicates, whose status wins when two reviewers touch one item, and
whether one reviewer may edit another's feedback.

## Collaborators (next, after `serve --listen` with its token)

Owner, 2026-11-10: a **Collaborate** widget at the top, shown to whoever is
on loopback (no token needed) or holds the launch token, opens a modal: name,
optional email, and "may add collaborators" (checked by default). It stores a
token for that person in the review file and gives a shareable link; the
collaborator opens it and works on the review with their own settings, cache
and reviewed marks. Owner agreed the evaluation's defaults:

- Table in the review file: name, email, token hash (never the token),
  `can_invite`, invited_by, created, revoked; excluded from exports.
- Identity per request: loopback or launch token = the owner; a
  collaborator's cookie = that collaborator (feedback author, marks).
- Link flow as the launch token: token once in the URL, swapped for an
  HttpOnly SameSite=Lax cookie (Lax, as the launch token is, so a link
  clicked in a web app works), redirect to a clean URL.
- Collaborators can do everything except close the review, prune, and
  (unless `can_invite`) add collaborators. No expiry; revoke instead. The
  owner revokes anyone; an inviter revokes those they invited.
- Needs a fixed port (`--port` or the port command) for links to survive a
  restart. Plain HTTP: tokens are sniffable on the LAN; use HTTPS via a
  proxy or an SSH tunnel beyond a trusted network. Loopback is always the
  owner (local users are trusted, per the earlier ruling).

## Mechanical change groups (phase not decided)

Owner, 2026-11-12: a mechanical change (a rename, an import or version line)
often makes the same one or two line edit in many files. Stepping through a
hundred files of two lines each is a pain when they all hold the same
change. Agreed design:

- The backend finds the groups. When it builds or iterates, it reduces each
  hunk to its `-` and `+` lines (no context, no line numbers); hunks the
  same in enough files form a group. A group holds only hunks the backend
  checked, so a change that is not mechanical cannot hide in one. Owner,
  2026-11-12: backend only to start; agents may propose groups (still
  checked by the backend, as substitution groups below) only if real
  reviews show the backend misses too much.
- A `mechanical` sheet item per group: the agent writes its name, what and
  why, or declines it, which returns its hunks to normal review.
- The page shows a group first, at commit (or topic) level before the
  files: the explanation, one diff that is right for every file apart from
  line numbers, and the list of files. Its `diff` tab shows every file's
  real hunks.
- Grouped hunks leave the other diffs and sections. A file changed only by
  grouped hunks gets no row, sections or split of its own; it is listed
  under its group. A file with other changes keeps its row, with a note
  naming the group.
- Flags, feedback and guide steps can target a group, or one member's line.
  Membership is found again each iteration; the text carries forward while
  the reduced change stays the same.
- Later, if needed: substitution groups, where the agent states a rule
  (`from` to `to` on each line) and the backend checks every `-` line turns
  into its `+` line; hunks that do not fit go to normal review.

Open: how many files make a group (deferred) and when to build it. Touches
FORMAT.md (a table, the sheet item, split rules), API.md (groups in the
commit JSON; diffs leaving grouped hunks out), the Perl backend, the page,
the review-build skill and the conformance checks.
