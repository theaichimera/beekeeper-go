# Changelog

All user-visible changes to `bk`. `docs:`-prefixed commits are excluded
from the auto-generated GitHub release notes per `.goreleaser.yaml` —
this file captures user-facing additions explicitly.

## v0.5.0

### Added

- **`pkg/beadspec`** — a declarative, schema-driven definition of bead
  structure (the "what a well-formed bead looks like" source of truth)
  plus a pure validator. Schemas are embedded TOML under
  `schemas/<type>.toml`; adding a bead type is a drop-in file. Leaf
  package: zero imports from `internal/*`/`cmd/*` (enforced by a guard
  test) so it can be extracted to its own repo later. Seed schemas:
  `epic` (requires rationale + a `## Decisions` block with a `Why:` per
  entry) and `progression` (requires the `progression` label +
  `## Current understanding` + `## Log`).
- **`bk doctor`** now runs a schema-driven `bead-schema` check (YELLOW),
  sourced from `pkg/beadspec` via the shared `internal/beadlint`
  plumbing — no hard-coded rules.
- **`bk guard beadspec`** — validates beads against their type schema
  and exits non-zero (2) on EPIC violations; wired into the pre-push
  hook as a hard gate (progression findings are advisory). Bypass with
  `BEADKEEPER_SKIP_HOOK=1`.
- **`bk progression new|add|list`** — scaffolds and maintains
  progression beads (the topic-arc "why"): `new` creates a
  schema-conforming bead with `## Current understanding` + `## Log`;
  `add --type baseline|deepening|pivot|correction` appends a dated log
  entry; `list` shows progressions with their current-understanding
  one-liner.
- **Board exclusion** — `progression`-labeled beads are omitted from
  `bk board` / ready / summary (living documents, not work).
  Configurable via `board.ExcludedLabels`.

## v0.4.0

### Added

- `bk export` — emit a vectorizable bead corpus (one document per bead,
  NDJSON by default; `--format json` for an array). Reads the
  authoritative bead state (sync-branch JSONL when configured, working
  tree fallback), normalizes actor identities through
  `.beadkeeper/identity.toml`, and pulls comments via `bd comments <id>
  --json`. Stable schema (id / repo / title / body / comments / text /
  status / issue_type / priority / labels / assignee / deps / timestamps
  / is_routine / is_decision / richness / superseded_by /
  schema_version). Tuning: `--format`, `--output`, `--status`, `--type`,
  `--labels` / `--exclude-labels`, `--since`, `--include-comments` /
  `--no-comments`, `--include-closed`, `--classify`, `--min-richness`,
  `--decisions-only`, `--text-template`, `--fields`, `--repo-filter`,
  `--redact`. Read-only — never mutates beads, never touches the daemon.
  `bk` does NOT embed or own a vector index; the corpus is meant for
  any downstream vector store. (bkg-3xb)

## v0.3.0

(See git history; `bk` did not maintain this file before v0.4.0.)
