# Orchestrator prompt — Beekeeper Go

Paste the block below to the implementing agent. It is self-contained.

---

You are the implementing agent for **Beekeeper Go**, a single-binary Go rewrite of the
Python tool `beadkeeper` (V2). Your job is to drive the epic to completion, milestone by
milestone, with behavioral parity as the bar.

## Repos
- **Target (you write here):** `theaichimera/beekeeper-go` — cloned at `~/cc/beekeeper-go`.
- **Reference (read-only contract):** the Python `beadkeeper` repo at `~/cc/beadkeeper`.
  Its source under `src/beadkeeper/` and its 133 tests under `tests/` define the exact
  behavior you must reproduce (output, exit codes, refusal conditions). When Go behavior
  and Python behavior disagree, **Python wins** unless you can prove Python is wrong.

## Authoritative spec
Read `docs/handoff/EPIC.md` in full before writing code. Do not deviate from its hard
constraints:
- Stay a wrapper around `git` / `bd` / `ps`. Do **not** reimplement them. **No SQLite
  driver** — the tool reads JSONL + git only.
- Single static binary, `CGO_ENABLED=0`, macOS + Linux.
- Behavioral parity with Python is the bar.

## Work tracking (beads)
Work is tracked with `bd` in this repo's `.beads/`. The epic is `bkg-9cs`; milestones are
`bkg-9cs.1`–`.6`, chained M0→M5 (each blocks the next).
- Before starting: `BEADS_AUTO_START_DAEMON=0 bd ready` — work only what is ready.
- When you start a milestone: `bd update <id> --status in_progress --assignee <you>`.
- When done (all acceptance criteria met, CI green): `bd close <id>`.
- Run `bd export` after bead mutations and commit `.beads/issues.jsonl` with your code.
- Do **not** commit `.beads/*.db*` (already gitignored).

## Milestone order (do not skip ahead — later depends on earlier)
1. **M0 — Scaffolding & CI** (`bkg-9cs.1`): Go module, cobra skeleton, goreleaser,
   GitHub Actions matrix {ubuntu, macos} × {go 1.22, 1.23}, MIT, README. `bk version` works.
2. **M1 — Shared core** (`bkg-9cs.2`): `internal/{git,project,config,filesync,proc}`.
   `internal/proc` (cross-platform `ps`/PID liveness) is the highest-risk piece — isolate
   per-OS files, unit-test against recorded `ps` fixtures.
3. **M2 — Read-only commands** (`bkg-9cs.3`): `doctor`, `board`, and detection-only
   `sync-branch` / `daemon` / `identity`. No mutation.
4. **M3 — Mutation + coordination** (`bkg-9cs.4`): `lease`, `merge-slot`, `trunk-sync`,
   `identity normalize`, `install-hooks`. Replicate daemon-live refusal, dirty-tree
   refusal, idempotency, and **id-based** divergence/stranding detection exactly.
5. **M4 — Test parity & diff harness** (`bkg-9cs.5`): port all 133 Python tests; build a
   harness that runs both binaries on one fixture corpus and asserts zero divergence.
6. **M5 — Distribution & cutover** (`bkg-9cs.6`): goreleaser release, Homebrew tap,
   README, Python↔Go interop note.

## Per-milestone loop
For each ready milestone:
1. Read the bead (`bd show <id>`) for its description + acceptance criteria.
2. Read the corresponding Python module(s) and their tests in `~/cc/beadkeeper`.
3. TDD: write Go tests that mirror the Python tests first, then implement until green.
4. Enumerate exact subcommands/flags and the exact argv of every `git`/`bd`/`ps` call
   from the Python source — it is the authority, do not guess.
5. Ensure `go vet`, `golangci-lint`, and `go test ./...` pass; CI green on both OSes.
6. `bd close <id>`, `bd export`, commit, open a PR, merge when green.

## Definition of done (epic `bkg-9cs`)
Every `cli.py` subcommand has a behavior-equivalent Go implementation; 133-equivalent
tests green; the diff harness shows zero divergence vs Python; `brew install` works;
`bk doctor` is GREEN on a real repo with no host runtime installed; README documents
every command; no secrets or vendor names in code or history.

## Reporting
After each milestone, report: what shipped, test counts, any behavioral deltas you found
vs Python (and how you resolved them), and the next ready bead. Do not mark the epic done
until the diff harness is clean.
