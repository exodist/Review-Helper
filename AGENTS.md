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

Review::Helper helps a human review code written by an AI/LLM agent. An agent
that finishes a change (commit, branch, PR) produces a review data file; the
human runs `review-helper <data_file>`, which starts a local Plack web app and
reports its port. The app presents the change top-down — overall summary,
then each commit, file, and sub — with agent-written explanations, diffs, and
line-level feedback the human can export back to an agent.

The project ships the web app and the agent skills that build the review file
and act on exported feedback. Initial language targets: Perl, JavaScript,
CSS, HTML, and agent `.md` files; every file in a change is still listed.

CPAN distribution name: `Review-Helper`. Main module: `Review::Helper`
(`lib/Review/Helper.pm`).

---

## Canonical sources of truth

1. **`ARCHITECTURE.md`** — the authoritative design.
2. **`RULINGS.md`** — owner decisions and their reasons. Read it when a
   decision is in front of you.
3. **`AGENTS_OVERRIDE.md`** — this project's declarations and overrides.
4. **This file** — project context and conventions.

`project` is the owner's original brief; `ARCHITECTURE.md` supersedes it. If
you are about to implement something that seems to conflict with
`ARCHITECTURE.md`, stop and verify.

---

## Testing

```
~/projects/Agents/bin/agent-test-lock -- prove --timer -Ilib -j16 -r t/
```

---

## Project-specific pre-review gates

Beyond the three universal passes in `~/projects/Agents/AGENTS.md`, this
project also requires:

```
perl agent_scripts/audit-test-layout --mode strict --namespace Review::Helper
```

---

## Architecture quick-reference

Foundational rules an agent must internalise before writing any code here:

- `use v5.36;` in every module and test; signatures everywhere they fit.
- `Object::HashBase` for objects; `Role::Tiny` for roles. They compose.
- `parent` for inheritance, not `base`.
- Web layer is Plack with `Router::Simple`. HTML, CSS, and JS theming follows
  Test2-Harness-UI (`~/projects/Test2/Test2-Harness-UI/share/`); read its
  assets before writing new ones.
- Tests use `Test2::V0`.
- Skills script the mechanical parts of the review file; an agent writes only
  the what/why/how explanations.
