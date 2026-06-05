---
name: bd-rationale
description: Use when creating an epic, when asked to record the "why" behind a decision, or when an epic is blocked by a missing rationale / "## Decisions" section. Also use to backfill rationale on existing epics from PRs, commits, and design docs.
---

# Author epic rationale (the per-decision "why")

Every epic must carry its decision rationale in its body, authored at creation
time. `bk` hard-blocks pushing an epic that lacks it. `bk` enforces *presence*;
YOU supply the *content* (no auto-extraction). Requires `bk` (>= v0.5.0) + `bd`.

Schema (`epic`): a non-empty body AND a non-empty `## Decisions` section where
every bullet contains the token `Why:`.

## Canonical shape

```
## Decisions
- Decision: <what was decided> — Why: <reason>  (src: PR #123, commit abc1234, or doc path)
- Decision: <another> — Why: <reason>  (src: ...)
```

Add a `(src: ...)` citation when the rationale traces to real evidence. If no
evidence exists yet (pure design intent), say so honestly rather than inventing
one, e.g. `Why: (design intent — not yet validated by shipped code)`.

## Creating a new epic with rationale

```bash
bd create "<epic title>" -t epic -d "$(cat <<'EOF'
<one-paragraph description of the epic>

## Decisions
- Decision: <choice> — Why: <reason>
- Decision: <choice> — Why: <reason>
EOF
)"
```

Include only the decisions you've actually made; append more as the design
firms up via `bd update <id> -d "<new body>"`.

## Backfilling rationale on an existing epic

Reconstruct the "why" from real sources, in priority order:

1. The bead itself — `bd show <id> --json` (description + comments).
2. Commits referencing it — `git log --all --grep=<id> --oneline`.
3. PRs referencing it — your forge's PR search + the PR discussion.
4. Design/handoff docs — `grep -rl "<id>" docs/`.
5. Child beads + their close reasons.

Append a `## Decisions` block (preserving the existing body) and write it back
with `bd update <id> -d "..."`.

## Verify the gate is satisfied

```bash
bk guard beadspec --repo .     # exit non-zero + [BLOCK] lines if an epic still lacks rationale
bk doctor .                    # softer, repo-wide schema visibility
```

A clean run means the pre-push hook will allow the push.
