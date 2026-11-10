# RULINGS.md

Decisions this project's owner has already made. A recorded ruling stands
until the owner changes it.

Read this file when a decision is in front of you, not for general context.
When new evidence — a bug report, a pull request, a user request, a changed
constraint — suggests a ruling needs revisiting, flag it for the owner rather
than acting against it. See `~/projects/Agents/AGENTS.md` under "Rulings".

Newest first. Each entry gives the date, what was ruled, and enough evidence
for the next reader to judge whether the situation has changed.

Only decisions that could be raised again belong here — contentious calls,
questions likely to recur, and rulings that may need revisiting when
circumstances change. Routine decisions nobody will ever question stay out;
see `~/projects/Agents/AGENTS.md` under "What earns a place in `RULINGS.md`".

---

## 2026-11-09 — The Perl splitter walks PPI's tree itself

**Ruling: do not rely on `PPI::Statement::Sub` to find units. Match the token
pattern `sub|method NAME [signature/prototype] [attributes] BLOCK` inside every
statement.**

PPI 1.291 has no `method`/`class` support (`feature_mods` covers only
`signatures` and `try`). Measured: `class Foo { method bar ($y) {...} sub baz
{...} }` yields zero `PPI::Statement::Sub` with or without feature switches,
and `method bar {...}` with no trailing `;` swallows the following `sub baz`
into one statement. Simplifying back to `find('PPI::Statement::Sub')` loses
plain subs in any file using `method`.

Revisit if: PPI gains native `method` support.

## 2026-11-09 — One static page, vanilla JS, JSON API

**Ruling: the UI is one static HTML shell plus vanilla JavaScript that talks
to a JSON API. No template engine, no jQuery, no JS framework or build
step.**

Theming (colors, fonts, panel styling) follows Test2-Harness-UI's CSS; its
stack (Text::Xslate shell, jQuery) does not carry over. The page is a single
view, so a template engine has nothing to fill, and modern DOM APIs cover what
jQuery provided. View state lives in the URL so back and reload work with no
server session.

Revisit if: the server needs to render dynamic page content.

## 2026-11-09 — Review content comes from git; refs pin iterations

**Ruling: the review file stores only review data. Diffs, file contents, and
images are read live from the repository under review.**

- One SQLite file per review, inside that repository. Default
  `$(git rev-parse --git-path review-helper)/<name>.sqlite`, which is
  per-worktree; any path inside the worktree also works. The repository is
  found from the file's location, never stored.
- Stored: shas, metadata, agent explanations, feedback, debates, iterations.
  Diffs are generated with fixed options, ignoring user diff config, so line
  anchors stay stable.
- Agents never write the database directly: `review-helper sheet` emits a
  JSON fill sheet, `review-helper fill` validates and imports it.
- Each iteration's head is pinned at `refs/review-helper/<review-id>/<n>`
  (shared ref store) so rebases and gc do not erase earlier iterations. A
  registry in the common git dir maps review ids to file paths.
- `review-helper prune` is the only cleanup path: it removes refs of closed
  reviews and of reviews whose file is gone, takes no ref or path arguments,
  and never deletes review files. It is narrow enough to allow outright, and
  the build skill runs it automatically.

Rejected: a self-contained file (JSON or tarball) carrying all content —
larger, duplicated, and JSON cannot take incremental writes safely.

Revisit if: reviews must be shared or viewed without the repository.

