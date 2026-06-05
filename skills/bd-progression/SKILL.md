---
name: bd-progression
description: Use when asked to record, update, or summarize the evolving understanding of a topic — create a progression, append a dated log entry, or refresh the "current understanding". Also use when a topic spans many epics/PRs and needs its own living narrative bead.
---

# Create & maintain progression beads (the topic "why" / living narrative)

A progression is a topic-scoped, living bead: where a topic started, how it
pivoted, and the current understanding. It is NOT owned by a single epic and is
kept OFF the work board. `bk` scaffolds the structure; YOU write the content.
Requires `bk` (>= v0.5.0) + `bd`.

Schema (`progression`): the `progression` label, a non-empty
`## Current understanding` section, and a `## Log` section.

## When to make a NEW progression vs. append

- **Append** to the per-repo `Project understanding: <repo>` progression for
  general "where the project stands now" updates (see `bd-onboard`).
- **Create a new** topic progression when an arc spans many epics/PRs and
  deserves its own narrative. Always check first: `bk progression list --repo .`.

## Create a new progression

```bash
bk progression new "<Topic>"          # scaffolds structure + progression label
bd update <id> -d "$(cat <<'EOF'
# <Topic>

## Current understanding
<concise summary of where this topic stands today>

## Log
- <YYYY-MM-DD> baseline: progression created for "<Topic>".
EOF
)"
```

## Append a dated log entry

Pick the entry type that fits what changed:

- `baseline` — initial state of understanding
- `deepening` — learned more, same direction
- `pivot` — direction changed
- `correction` — a prior belief was wrong

```bash
bk progression add <id> "<what changed in understanding>" --type deepening
```

After a `pivot` or `correction`, also refresh `## Current understanding` so the
top of the bead always reflects the latest truth:

```bash
bd update <id> -d "<full body with updated ## Current understanding>"
```

## List progressions

```bash
bk progression list --repo .   # shows each progression + its current-understanding one-liner
```

## Notes

- The `## Log` is append-only narrative; `## Current understanding` is the
  self-refreshing summary. Keep both honest.
- A log entry should reflect a real change in understanding, ideally citing a
  PR/commit/doc. Don't fabricate.
