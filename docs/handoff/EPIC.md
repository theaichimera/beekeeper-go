# Epic: Beekeeper Go (beadkeeper V2, Go rewrite)

> Handoff spec for an autonomous agent. Self-contained. Read top to bottom before
> writing code. The Python `beadkeeper` repo is the **behavioral contract** — when in
> doubt, match its observable behavior (output, exit codes, refusal conditions) exactly.

---

## 0. Goal & framing

Reimplement `beadkeeper` (Python, ~5k LOC, zero runtime deps) as a single statically
linked Go binary. The motivation is **distribution**: today the tool needs a Python
interpreter + venv + `pip install`. The Go version ships as one binary (`brew install`
or download-and-run), which is the entire payoff.

This is **V2 as a separate repo**, living alongside the Python version. Both versions
read/write the same `.beads` state and shell out to the same `bd` and `git` binaries,
so they are interchangeable on a per-invocation basis. Neither replaces the other until
the Go version reaches test parity.

**Naming:** the requested working name is **"Beekeeper Go"** (repo `beekeeper-go`,
binary `bk`). Note this is a pun-spelling of "beadkeeper" — confirm with the owner
whether you want `beekeeper-go` (pun) or `beadkeeper-go` (literal) before creating the
repo. Everything below uses `bk` as the binary name regardless.

---

## 1. Hard constraints (do not violate)

- **Stay a wrapper.** Shell out to `git`, `bd`, and `ps` exactly as the Python version
  does. Do **not** reimplement git, bd, or a SQLite reader. The Python tool never opens
  the SQLite DB directly (it only detects the file path and reads JSONL + git) — the Go
  version must keep that property, so **no SQLite driver dependency**.
- **Single static binary.** `CGO_ENABLED=0`. No runtime dependencies on the host.
- **Cross-platform** for macOS + Linux (the Python tool's `ps`/PID logic is the only
  platform-sensitive surface — see M1).
- **Behavioral parity is the bar**, not internal-structure parity. Match observable
  output, classification (GREEN/YELLOW/RED), exit codes, and refusal conditions.
- **Preserve the safety semantics** the Python version hardened: daemon-live refusal,
  dirty-working-tree refusal, idempotent mutations, and **id-based** divergence
  detection in `trunksync`/`syncbranch`.

### External surface (the whole integration boundary)
```
git  — 8 call sites
bd   — 5 call sites
ps   — 1 call site (process enumeration / daemon liveness)
```
The Python source is the source of truth for the exact argv of each call. Grep it.

---

## 2. Source inventory (Python → Go package map)

Port the Python modules to Go packages. Suggested layout under `internal/`:

| Python module (LOC)   | Go package        | Risk  | Notes |
|-----------------------|-------------------|-------|-------|
| `project.py` (276)    | `internal/project`| med   | discovery, daemon/git state read |
| `filesync.py` (147)   | `internal/filesync`| low  | known cloud roots, prefix match |
| `git` calls (in many) | `internal/git`    | low   | `os/exec` wrapper, ref/tree helpers |
| `daemons.py` (327)    | `internal/daemons`| **high** | `ps` parsing, PID liveness, dup detection |
| `identity.py` (255)   | `internal/identity`| med  | TOML config, alias→canonical, JSONL scan |
| `guard.py` (202)      | `internal/guard`  | low   | guard-db detection |
| `syncbranch.py` (336) | `internal/syncbranch`| **high** | id-based stranding detection |
| `trunksync.py` (552)  | `internal/trunksync`| **high** | id-based divergence, ff apply |
| `lease.py` (386)      | `internal/lease`  | med   | claim/release/list, identity resolve |
| `mergeslot.py` (281)  | `internal/mergeslot`| med | acquire/release, critical_section |
| `board.py` (291)      | `internal/board`  | low   | cross-project read-only aggregation |
| `hooks.py` (286)      | `internal/hooks`  | low   | install/uninstall git hooks |
| `doctor.py` (599)     | `internal/doctor` | med   | composes all checks, severity rollup |
| `cli.py` (1033)       | `cmd/bk` (cobra)  | med   | subcommand wiring — enumerate from cli.py |

Total: ~5k LOC Python → expect ~6–8k LOC Go.

**Enumerate the exact subcommand set and flags from `cli.py`** — it is the authority.
Known subcommands include: `doctor`, `board`, `sync-branch`, `daemon`, `identity`,
`install-hooks`, `lease`, `merge-slot`, `trunk-sync`. Verify against `cli.py` before
building the cobra tree.

---

## 3. Tech choices

- **CLI:** `spf13/cobra` (matches the multi-subcommand shape of `cli.py`).
- **TOML:** `BurntSushi/toml` (for `identity.toml` / config).
- **JSON/JSONL:** stdlib `encoding/json`, line-split manually.
- **Process info:** stdlib `os/exec` + parse `ps`; abstract behind `internal/proc` with
  per-OS files (`proc_darwin.go`, `proc_linux.go`) so the platform-sensitive bit is
  isolated and unit-testable with recorded fixtures.
- **Release:** `goreleaser` → GitHub Releases + a Homebrew tap.
- **CI:** GitHub Actions, matrix `{ubuntu-latest, macos-latest} × {go 1.22, 1.23}`,
  `go vet` + `golangci-lint` + `go test ./...`.

---

## 4. Milestones

Each milestone is independently shippable and ends GREEN in CI. Build in order — later
milestones depend on the shared core from M1.

### M0 — Scaffolding & CI
- Create repo, `go.mod`, cobra skeleton, MIT license, README stub, goreleaser config,
  CI matrix. `bk version` works.
- **DoD:** `CGO_ENABLED=0 go build` yields a runnable binary; CI green on both OSes.

### M1 — Shared core (foundation)
- Implement `internal/git`, `internal/project`, `internal/config`, `internal/filesync`,
  `internal/proc` (the cross-platform `ps`/PID-liveness layer — **highest risk**).
- **DoD:** unit tests for project discovery, git ref/tree helpers, filesync root
  detection, and proc liveness (stale vs alive PID), validated against fixtures derived
  from the Python tests.

### M2 — Read-only commands (low risk first)
- `doctor`, `board`, `sync-branch` (detection only), `daemon` (detection only),
  `identity` (scan / dry-run only). No mutation paths yet.
- **DoD:** output and exit codes match the Python tool on a shared fixture corpus;
  ported tests green.

### M3 — Mutation + coordination (the hard semantics)
- `lease` (claim/release/list), `merge-slot` (acquire/release/critical_section),
  `trunk-sync` (scan/apply), `identity normalize`, `install-hooks`.
- Must replicate: **daemon-live refusal**, **dirty-tree refusal**, **idempotency**, and
  **id-based divergence/stranding detection**.
- **DoD:** faithful ports of `test_trunksync`, `test_syncbranch`, `test_lease`,
  `test_mergeslot` (real temp-git-repo fixtures) green.

### M4 — Test parity & behavioral diff harness
- Port all **133** Python tests to Go table-driven tests.
- Build a **diff harness**: a script that runs `beadkeeper` (py) and `bk` (go) against
  an identical fixture repo corpus and asserts identical classification, output shape,
  and exit codes. This is the objective parity gate.
- **DoD:** 133-equivalent tests green; diff harness reports **zero divergence** across
  the corpus.

### M5 — Distribution & cutover note
- `goreleaser` release pipeline; Homebrew tap; README with install + usage; a short
  "Python ↔ Go interop" note documenting that both operate on the same `.beads` dir and
  are interchangeable per-invocation.
- **DoD:** `brew install` produces a working `bk`; `bk doctor` GREEN on a real repo with
  no host runtime installed.

---

## 5. Test-parity strategy (this is half the work — budget for it)

- The 133 Python tests are the **only** guarantee the hardened divergence logic survives
  the port. Treat porting them as a first-class deliverable, not an afterthought.
- Most tests build **real temporary git repos** and run the CLI/module — this ports
  cleanly to Go (`t.TempDir()` + `os/exec git`). Keep the same fixture construction.
- Preserve the **exit-code contract** (GREEN/YELLOW/RED → process exit codes). Read the
  Python exit codes as the spec; do not invent new ones.
- The diff harness in M4 is the strongest parity signal — prioritize it.

---

## 6. Risks & landmines

- **Process inspection (`internal/proc`/`daemons`)** is the most likely bug nest:
  macOS vs Linux `ps` flags and output columns differ. Isolate per-OS, record real `ps`
  output as fixtures, and unit-test the parser hard.
- **id-based divergence detection** (`trunksync`/`syncbranch`) is subtle — these are the
  recently hardened modules. Port the logic *and its regression tests together*; do not
  ship one without the other.
- **Exit-code / output drift** is easy to introduce silently. The M4 diff harness exists
  specifically to catch this.
- **Scope creep:** resist reimplementing `git`/`bd`. The binary stays a coordinator.

---

## 7. Definition of done (epic)

- `bk` is a single static binary, no host runtime, macOS + Linux.
- Every subcommand in `cli.py` has a behavior-equivalent Go implementation.
- 133-equivalent tests green; diff harness shows zero divergence vs the Python tool.
- `brew install` path works; `bk doctor` GREEN on a real repo.
- README documents install + every command with examples.
- No secrets, no vendor names anywhere in code or history.
