# Changelog

All user-visible changes to `bk`. `docs:`-prefixed commits are excluded
from the auto-generated GitHub release notes per `.goreleaser.yaml` —
this file captures user-facing additions explicitly.

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
