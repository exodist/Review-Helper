# AGENTS.md

## MANDATORY: read the universal agent rules first

This project opts into shared agent guidance while keeping its own documents
authoritative for project-specific rules and design.

- Repository: `git@github.com:exodist/Agents.git`
- Default location: `~/projects/Agents`
- This project's location: as declared in `AGENTS_OVERRIDE.md` under "Agents
  repository location", when that section is present.
- Last synced: 40ad772e16af86e82ab175503572da62b4ff77e8 (2026-11-09)

Use the declared location when there is one, otherwise the default. If no
checkout is there, **stop and ask the user** whether to clone it and where.
Never clone it for them, and never guess a location.

Shared documents spell their paths against the default location. When this
project declares another, read every such path against the declared one.

Check for pending syncs once, at the start of the session:

    git -C <location> log --oneline <last-synced-sha>..HEAD -- \
        AI_AND_LLM_POLICY.md templates/ agent_scripts/ SYNC.md

No output means nothing to apply. Otherwise follow "Staying in sync" in the
shared `AGENTS.md`: show the user what is pending and let them sync now or
skip it and carry on with what they came here to do. Never sync unasked.

Then read `AGENTS.md` in that checkout and follow the shared guidance this
project has adopted. It points at task-specific guides and procedures.

All documents in THIS repository take priority over the shared repository.
Read the project documents named below; `AGENTS_OVERRIDE.md` records
declarations and explicit shared-rule overrides when present.

---

## What this project is

Review Helper helps a human review code written by an AI/LLM agent. An agent
that finishes a change (commit, branch, PR) produces a review data file and
starts a local web server for it (`review-helper serve <data_file>`), giving
the human its link. The page presents the change top-down — overall summary,
then each commit, file, and sub — with agent-written explanations, diffs, and
line-level feedback the human can export back to an agent.

The project ships the page, the agent skills that build the review file and
act on exported feedback, and backends that serve the page and run the
commands. Initial language targets for reviewed code: Perl, JavaScript, CSS,
HTML, and agent `.md` files; every file in a change is still listed.

**Primary use cases:** reviewing a change an agent just made (with its
journal of notes), and reviewing a whole project or part of one as it
finally lands — points of interest deduced from history and context, toured
with a guide rather than commit by commit (`review-project`). The second also
serves for learning a codebase.

**A goal beyond this app: a standard.** The review file, the journal, the
fill sheet, the command line the skills use and the HTTP API the page uses
(`standards/`) are meant to be a shared, documented base for AI/LLM-assisted
code review. Other tools, skills, and processes — in any language — should be
able to build on and extend them, and a backend in any language should be
able to serve the same page and run the same skills. Design decisions weigh
that: keep the standard language-neutral, keep implementation-specific
behavior out of it, and change it only deliberately (it is versioned).

---

## How the parts fit

| Directory | Holds | Its own instructions |
|---|---|---|
| `standards/` | The standard: `FORMAT.md` (review file, journal, fill sheet, exports), `review.sql` (the schema), `CLI.md` (the commands the skills use, and the dispatcher), `API.md` (the HTTP API the page uses) | — |
| `ux/` | The page every backend serves, and its unit tests | `ux/AGENTS.md` |
| `skills/` | The agent skills; they use only the commands in `standards/CLI.md` | — |
| `conformance/` | Checks any backend runs: the CLI checks and the page drive | `conformance/README.md` |
| `bin/` | `review-helper`, the POSIX `sh` dispatcher that runs `review-helper-<impl>` from `PATH` | — |
| `perl/` | The Perl backend, the first implementation; it builds the CPAN distribution `Review-Helper` | `perl/AGENTS.md` |
| `go/` | The Go backend: one binary embedding `ux/`, `skills/` and `standards/` | `go/AGENTS.md` |

A backend implements `standards/` and ships `ux/`, `skills/` and
`standards/` in its distribution; the Perl one does it through symlinks in
`perl/share/`. Read the instructions of each part you change.

---

## Canonical sources of truth

1. **`ARCHITECTURE.md`** — the authoritative design.
2. **`standards/`** — authoritative for what it specifies; `ARCHITECTURE.md`
   does not repeat it.
3. **`RULINGS.md`** — owner decisions and their reasons. Read it when a
   decision is in front of you. It holds only current rulings: a new ruling
   replaces the one it changes, and an amendment is folded into its ruling;
   git keeps the history.
4. **`AGENTS_OVERRIDE.md`** — this project's declarations and overrides.
5. **This file and each part's instructions** — project context and
   conventions.

`PLANNED_TODO.md` lists work the owner wants but has deferred, and when. Add
an entry when the owner defers something; remove it once it is built. It is
not a source of truth for the current design.

`project` is the owner's original brief; `ARCHITECTURE.md` supersedes it. If
you are about to implement something that seems to conflict with
`ARCHITECTURE.md`, stop and verify.

`AI_DOCS/` holds only working documents an agent needs for upcoming
development (see `AI_DOCS/README.md`). Review transcripts, review-cycle
records and other history do not go there: git keeps history, and
`RULINGS.md` and `PLANNED_TODO.md` hold what is still current.

---

## Testing and gates

The Perl suite runs everything, the page's unit tests and the conformance
checks included; `perl/AGENTS.md` gives the command and the Perl gates.
The page's unit tests also run alone: `node --test ux/t/`; the conformance
checks as `conformance/README.md` says.

For every change, beyond the passes in `~/projects/Agents/AGENTS.md`:

```
git diff --check
node --check <each touched .js or .mjs file>
```

---

## Conventions for every part

- Skills script the mechanical parts of the review file; an agent writes only
  the what/why/how explanations.
- The standard and the page know no backend: nothing in `standards/`, `ux/`,
  `skills/` or `conformance/` names a backend's language, paths or commands
  other than `review-helper`.
