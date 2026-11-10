# AGENTS_OVERRIDE.md

This project's answers to the choices the universal agent rules deliberately
leave open, plus every deliberate departure from them.

Universal rules live in `~/projects/Agents` (see `AGENTS.md` for the clone
URL). Every project-local document already takes priority over shared rules;
this file keeps declarations and explicit overrides easy to find.

---

## Declarations

Every one of these must be answered. An unanswered declaration is how two
files in one repository end up in different styles.

### Minimum Perl version

> **Minimum: 5.036000**

Reason: new distribution with no existing compatibility promise; `use v5.36`
gives strict, warnings, and signatures without experimental pragmas.

### Subroutine signatures

> **Policy: required where expressible**

> **Enabling pragma: `use v5.36;`**

Reason: the 5.36 floor supports signatures as a stable feature.

### POD placement

> **Layout: all at bottom (default)**

- **All at bottom (default).** One continuous POD document under `__END__`, in
  `TEMPLATE.pod` order. No POD at the top of the file, none between subs.
- **Split.** `NAME` / `DESCRIPTION` / `SYNOPSIS` at the top, per-method POD
  inline above each sub, tail sections under `__END__`. Sub order then follows
  the POD grouping.

Reason: shared default; nothing in a new project argues otherwise.

### Test layout and provenance

> **Scheme: categories plus origin headers**

- **Categories plus origin headers (default for new projects).** `t/unit`,
  `t/acceptance`, `t/regression`, `t/integration`, with `t/lib`, `t/fixtures`,
  `t/scripts`; every `.t` carries `# Test origin: human|AI|mixed`.
- **`t/AI/` mirror tree.** Human tests in `t/`, AI-generated tests in `t/AI/`
  mirroring the same subdirectory layout. No per-file origin header.

> **Layout audit:**

```text
perl agent_scripts/audit-test-layout --mode strict --namespace Review::Helper
```

Reason: shared default for new projects; consistent layout and explicit test
provenance from the first test.

### perltidy

> **Config: shared**

- **Shared.** `~/projects/Agents/templates/perltidyrc`, copied to
  `.perltidyrc` at the project root.
- **Project-local.** A `.perltidyrc` that differs — list what differs and why
  below.

Reason: `.perltidyrc` is an exact copy of
`~/projects/Agents/templates/perltidyrc`.

---

## Overrides

Every deliberate departure from a universal rule, with its reason. A rule
quietly ignored in the code is a bug; a rule overridden here is a decision.

None.

---

## Prior rulings

Recorded in `RULINGS.md`, not here. This file holds declarations and
shared-rule overrides; a ruling is neither.
