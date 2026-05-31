# Beekeeper Go

A single-binary Go rewrite of **beadkeeper** (V2). Beadkeeper is a coordination layer
around the `bd` issue tracker and `git`: cross-project health (`doctor`), a unified
board, sync-branch / daemon / identity guardrails, and Phase-3 coordination primitives
(lease, merge-slot, trunk-sync).

The Python original works but is **hard to run** — it needs an interpreter, a venv, and
`pip install`. This repo reimplements it as one statically linked binary (`bk`) you can
`brew install` or download and run. No runtime.

## Status

**M0 shipped (scaffolding + CI).** No real subcommands yet; only `bk version` works. The
remaining milestones implement the actual functionality.

- Full specification: [`docs/handoff/EPIC.md`](docs/handoff/EPIC.md)
- Orchestrator prompt for the implementing agent: [`docs/handoff/ORCHESTRATOR.md`](docs/handoff/ORCHESTRATOR.md)
- Work is tracked as beads in `.beads/issues.jsonl` (epic `bkg-9cs`, milestones `bkg-9cs.1`–`.6`).

## Build

```bash
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bk ./cmd/bk
./bk version
```

Requires Go 1.22+. The binary is fully static — no host runtime, no libc dependency at run
time. The release pipeline (`.goreleaser.yaml`) produces cross-built binaries for
`darwin`/`linux` × `amd64`/`arm64`.

## Design tenets

- **Stays a wrapper.** Shells out to `git`, `bd`, and `ps`. Does not reimplement them, and
  carries **no SQLite driver** (the tool reads JSONL + git, never opens the DB directly).
- **Single static binary**, `CGO_ENABLED=0`, macOS + Linux.
- **Behavioral parity** with the Python tool is the bar: identical output, exit codes, and
  refusal conditions. The Python repo's 133-test suite is the contract.

## Milestones

| Bead | Milestone |
|------|-----------|
| `bkg-9cs.1` | M0 — Scaffolding & CI |
| `bkg-9cs.2` | M1 — Shared core (git/project/config/filesync/proc) |
| `bkg-9cs.3` | M2 — Read-only commands |
| `bkg-9cs.4` | M3 — Mutation + coordination (hard semantics) |
| `bkg-9cs.5` | M4 — Test parity & behavioral diff harness |
| `bkg-9cs.6` | M5 — Distribution & cutover |

## License

MIT — see [`LICENSE`](LICENSE).
