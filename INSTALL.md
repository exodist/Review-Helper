# Installing Review Helper

Review Helper is a standard (`standards/`) with more than one backend. Pick
one; each has its own install guide:

| Backend | Guide | Needs |
|---|---|---|
| Perl, the reference implementation | [`perl/INSTALL.md`](perl/INSTALL.md) | Perl 5.36 or later, on a perl that can fork |
| Go | [`go/INSTALL.md`](go/INSTALL.md) | Go 1.26 or later to build; nothing to run |

Every backend also needs git (2.36 or later to see how a merge resolved its
conflicts) and a web browser.

## The `review-helper` command

Skills and people run `review-helper`, a short POSIX `sh` script
(`bin/review-helper`) that runs whichever backend is on your `PATH`
(`review-helper-perl`, `review-helper-go`, ...). Every backend installs it.
With several installed, the first on `PATH` runs, or the one
`REVIEW_HELPER_IMPL` names; `review-helper --which` lists them. It needs a
POSIX shell: on Windows, Cygwin, WSL or Git Bash.

## The agent skills

Each backend ships the nine agent skills. Link each into your agent's skills
directory (`~/.claude/skills/` for Claude Code, `~/.agents/skills/` for
Codex), or ask your agent to do it:

```
ln -s "$(review-helper skills path)/review-build" ~/.claude/skills/
```

## Building from a checkout with make

The root `Makefile` gives every backend the same targets, each calling that
backend's own tools (the backend guides give the commands they run):

| Target | Does |
|---|---|
| `make <lang>` | Prepare the checkout to build (`make go` copies the shared files Go embeds) |
| `make <lang>-build` | Build |
| `make <lang>-test` | Run its tests (for Go, with the conformance checks) |
| `make <lang>-install` | Install it, with the `review-helper` command |

`<lang>` is `perl` or `go`. Without make, use the commands in the backend's
guide.
