---
name: bd-setup
description: Use when setting up a repo for the bk rationale/progression gate, when asked to install bk git hooks, or to check bead/repo health. Ensures the pre-push beadspec gate is live and runs a bk doctor health read.
---

# Make the bk gate live in a repo & check health

Ensures a repo enforces the rationale/progression contract on push, and gives a
quick health read. Requires `bk` (>= v0.5.0) and `bd` on PATH.

## 1. Confirm bk is current

```bash
bk version            # want >= 0.5.0 (the version that ships `guard beadspec`)
```

## 2. Ensure the pre-push gate is installed

The hard gate lives in `.git/hooks/pre-push` and must call `bk guard beadspec`.
Upgrading the `bk` binary does NOT rewrite an already-installed hook — reinstall
it once per existing repo:

```bash
grep -q "guard beadspec" .git/hooks/pre-push 2>/dev/null \
  && echo "gate already installed" \
  || bk install-hooks        # idempotent
```

(Optional, for fleets: a portable copy of this hook placed in a global git
template dir — `git config --global init.templateDir <dir>` — auto-installs the
gate on every future `git clone` / `git init`. Existing repos still need the
one-time `bk install-hooks`.)

## 3. Health read

```bash
bk doctor .           # per-repo health incl. schema findings + missing-hook nag
bk board              # work board (progression-labeled beads are excluded)
```

## What the gate does once live

- On `git push`, runs `bk guard beadspec`: any epic missing its `## Decisions`
  rationale **blocks the push** with `[BLOCK] <id>` lines.
- Emergency bypass (avoid — fix the bead instead): `BEADKEEPER_SKIP_HOOK=1 git push`.
- `bk doctor` findings are warn-only unless `BEADKEEPER_BLOCK_ON_RED=1`.
