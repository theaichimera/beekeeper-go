# Beekeeper Go (`bk`)

**An operations layer for [beads](https://github.com/steveyegge/beads) — keep many bead-hives healthy across all your projects.**

`bk` is the Go port of [beadkeeper](https://github.com/theaichimera/beadkeeper) (Python).
Both versions read and write the same `.beads/` directory and shell out to the same `bd` /
`git` binaries, so they're interchangeable per-invocation. The Go binary is the recommended
on-disk form: one statically-linked file, no Python interpreter, no venv.

## What it does

- **Cross-project health (`bk doctor`)** — RED/YELLOW/GREEN per project across many repos.
- **Work board (`bk board`)** — ready / in-progress / blocked, lease-gap detection.
- **Guardrails (`bk guard db|sync-branch|daemon`)** — refuse a bead DB inside a sync folder,
  catch bead-data commits stranded off the sync branch, find duplicate / silently-failing
  daemons.
- **Coordination (`bk lease`, `bk merge-slot`, `bk trunk-sync`)** — per-issue lease
  discipline, serialized merge+deploy, out-of-band JSONL replay onto trunk.
- **Identity (`bk identity check|normalize`)** — canonical actor handles via
  `.beadkeeper/identity.toml`.
- **Hooks (`bk install-hooks`, `bk prompt-indicator`)** — sync-death alarm at push time, a
  colored dot in your shell prompt when health goes non-GREEN.

## Install

### Homebrew (macOS & Linux)

```bash
brew install theaichimera/tap/bk
bk version
```

Homebrew runs on both macOS and Linux, so this is the simplest route on either.
(Equivalently: `brew tap theaichimera/tap && brew install bk`.) The tap formula is
mirrored at [`pkg/homebrew/Formula/bk.rb`](pkg/homebrew/Formula/bk.rb) for review;
goreleaser writes the live formula into `theaichimera/homebrew-tap` on every stable tag.

### Direct download (macOS, Linux, Windows)

Each release ships statically-linked binaries for **macOS, Linux, and Windows** on
**amd64 + arm64**. Browse them at
<https://github.com/theaichimera/beekeeper-go/releases/latest>. Asset names follow
`beekeeper-go_<version>_<os>_<arch>` — `.tar.gz` for macOS/Linux, `.zip` for Windows.

**macOS / Linux:**

```bash
VERSION=0.1.1          # set to the latest release tag (without the leading v)
OS=linux               # darwin | linux
ARCH=x86_64            # x86_64 | arm64
curl -L -o bk.tar.gz "https://github.com/theaichimera/beekeeper-go/releases/download/v${VERSION}/beekeeper-go_${VERSION}_${OS}_${ARCH}.tar.gz"
tar xf bk.tar.gz
sudo install bk /usr/local/bin/bk     # or move ./bk anywhere on your PATH
bk version
```

**Windows (PowerShell):**

```powershell
$Version = "0.1.1"                     # set to the latest release tag (without the leading v)
$Arch    = "x86_64"                    # x86_64 | arm64
Invoke-WebRequest "https://github.com/theaichimera/beekeeper-go/releases/download/v$Version/beekeeper-go_${Version}_windows_$Arch.zip" -OutFile bk.zip
Expand-Archive bk.zip -DestinationPath .
.\bk.exe version
```

Move `bk.exe` into a directory on your `PATH` (or add its folder) to run `bk` from anywhere.
Windows needs `git` on `PATH` for any command that touches a repo.

### From source

Requires Go 1.22+. Builds a fully static binary (no host runtime, no libc dependency):

```bash
git clone https://github.com/theaichimera/beekeeper-go.git
cd beekeeper-go
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bk ./cmd/bk
./bk version
```

Runtime requires `git` on `PATH` for any command that touches a git repo. `bd` is needed
only at the projects `bk` inspects.

## Commands at a glance

| Command | What it does |
|---|---|
| `bk doctor` | Cross-project RED/YELLOW/GREEN health scan |
| `bk board` | Cross-project work board (ready / in_progress / blocked + lease gaps) |
| `bk guard db` | Catch a bead DB living inside a file-sync folder (`--fix --destination DIR`) |
| `bk guard sync-branch` | Catch bead commits stranded off the sync branch (`--set BRANCH`) |
| `bk guard daemon` | Catch duplicate daemons & silent remote-helper failures |
| `bk guard pr-beads` | Catch backlog regressions when merging head into base (`--policy regression\|no-beads`) |
| `bk guard stale-beads` | Catch open/in_progress beads whose work already shipped on the default branch |
| `bk identity check` / `normalize` | Detect & fix actor-handle drift in bead JSONL |
| `bk trunk-sync` | Reconcile bead JSONL drift between trunk and the sync branch (`--apply`) |
| `bk lease claim` / `release` / `list` | Per-issue lease discipline via canonical identity |
| `bk merge-slot acquire` / `release` / `status` | Serialize merge+deploy across agents |
| `bk install-hooks` / `prompt-indicator` | Surface health at push time and in your shell prompt |
| `bk version` | Print the binary version |

## Exit-code contract

Every subcommand uses the same exit-code language. Mirrors the Python tool exactly.

| Code | Meaning |
|---|---|
| **0** | Success / clean / no findings |
| **1** | Generic error OR `--strict` flag turned a YELLOW finding into a non-zero exit |
| **2** | Doctor / scan reported **RED** (blocking severity) |
| **3** | **REFUSE** — typed conflict (lease held by another, slot already taken, daemon alive on a mutation, divergent trunk edit, dirty working tree) |
| **64** | Missing-flag / non-workspace usage error (e.g. `--fix` without `--destination`) |
| **127** | Underlying tool not found (e.g. `git` missing) |

Use `--strict` on any subcommand that produces a YELLOW finding (`doctor`, `board`,
`guard *`, `trunk-sync`, `identity check`) to make YELLOW exit `1` for CI gating.

## Usage

### `bk doctor` — cross-project health scan

Walks each path looking for `.beads/` and reports per-project health. The check set:
`issues-jsonl`, `db-in-filesync`, `git`, `git-upstream`, `sync-branch`, `daemon`,
`sync-state`, `daemon-hygiene`, `trunk-sync`, `lease`, `identity`.

```bash
bk doctor                          # current dir
bk doctor ~/code/proj-a ~/code/proj-b
bk doctor . --json | jq .worst     # machine-readable
bk doctor . --strict               # YELLOW also exits non-zero
```

### `bk board` — cross-project work board

Reads `.beads/issues.jsonl` across projects. **Read-only** — no JSONL mutation, no bd
subprocess, no daemon touch. In-project dependency resolution: an `open` issue with a
`blocks` dep on a non-closed issue lands in `blocked`. Parent-child deps don't gate.

```bash
bk board                              # all buckets, all projects
bk board --status ready               # ready bucket only
bk board --lease-gaps                 # only in_progress with no assignee
bk board --json | jq .totals
bk board --strict                     # exit 1 if any lease gaps (CI)
```

### `bk guard db` — DB-in-file-sync guardrail

Detects bead `*.db` / `-wal` / `-shm` files inside known cloud-sync roots (Dropbox,
iCloud Drive, OneDrive, Google Drive, Insync, configurable via `BEADKEEPER_FILESYNC_ROOTS`).

```bash
bk guard db                           # report only
bk guard db . --fix --destination ~/.local/share/bk-relocated         # plan, dry-run
bk guard db . --fix --destination ~/.local/share/bk-relocated --yes   # apply
```

`--fix` refuses (rc=3) when the bd daemon is alive. The DB is a rebuildable cache of
`issues.jsonl`, so relocation is safe — `bk` still won't touch it while bd has the WAL
open.

### `bk guard sync-branch` — stranded-bead-commit guard

Detects an empty `bd config sync.branch` (YELLOW) and bead-data commits on branches OTHER
than the configured sync branch (RED). Detection is **id-based**: a branch whose JSONL
records are a strict subset of the sync branch's records is NOT flagged (the regression
test that prevents `trunk-sync` replay commits from false-flagging themselves).

```bash
bk guard sync-branch ~/code
bk guard sync-branch . --set beads-sync   # writes .beads/config.json (refuses on live daemon)
```

### `bk guard pr-beads` — backlog-regression gate for PRs

Diffs `.beads/issues.jsonl` between two refs and reports any change that would REWIND
backlog state on merge: status rewinds, dropped assignees, stale timestamps, dropped
records. The "stale-snapshot stomp" — a feature branch carrying an old JSONL whose
merge silently reverts work the daemon completed on `main` while the branch lived.

```bash
bk guard pr-beads --base main --head HEAD                    # local check
bk guard pr-beads --base origin/main --head feature/x        # CI form
bk guard pr-beads --policy no-beads                          # ANY .beads diff fails
bk guard pr-beads --json | jq '.findings[] | select(.kind=="status-rewind")'
```

Defaults: `--base = $GITHUB_BASE_REF` (CI) → remote default branch → `origin/main` → `main`;
`--head = HEAD`.

**Detection** (per id present in BOTH base and head):

| Signal | Trigger | Severity |
|---|---|---|
| Status rewind | rank `open=0`, `in_progress=1`, `blocked=1`, `closed=2`; head < base | RED |
| Assignee rewind | base set, head clears or changes | RED |
| Stale timestamp | both records carry parsable `updated_at` (or `modified`); head < base | RED |
| Dropped record | id in base, absent in head | RED |
| Forward-only | new ids on head, status advances | GREEN |

**Policies:**

| `--policy` | Use case |
|---|---|
| `regression` (default) | Repos that allow bead edits anywhere — only the four signals fail. |
| `no-beads` | Repos that confine bead writes to a sync branch — ANY commit in `base..head` touching the JSONL fails. |

#### GitHub Actions

Drop this job into `.github/workflows/pr-beads.yml` to gate every pull request:

```yaml
name: pr-beads
on:
  pull_request:

jobs:
  pr-beads:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0   # pr-beads needs both refs reachable
      - name: Install bk
        run: |
          curl -sL https://github.com/theaichimera/beekeeper-go/releases/latest/download/beekeeper-go_linux_x86_64.tar.gz | tar -xz
          sudo install -m 0755 bk /usr/local/bin/bk
      - name: Guard pr-beads
        env:
          GITHUB_BASE_REF: ${{ github.base_ref }}
        run: bk guard pr-beads --head HEAD --json
```

The `fetch-depth: 0` is required so both `$GITHUB_BASE_REF` and the PR head are
reachable from the runner's clone. Setting `--policy no-beads` instead of the default
makes ANY `.beads/issues.jsonl` modification fail the check — useful for repos that
keep bead writes on a dedicated sync branch.

#### Pre-push hook integration

`bk install-hooks` plumbs an opt-in `pr-beads` invocation behind
`BEADKEEPER_PRBEADS_POLICY`. Default is `off` (the check never fires until you opt in).
Set to `regression` or `no-beads` to enable; combine with `BEADKEEPER_BLOCK_ON_RED=1` to
make a finding blocking instead of a warning. `BEADKEEPER_SKIP_HOOK=1` is the same
escape hatch as for the doctor check.

### `bk guard stale-beads` — git↔backlog reconciliation

The post-merge sibling of `pr-beads`. Scans merged commit subjects on the default branch for
bead-id tokens; reports open / in_progress beads whose work already shipped. Catches the
"shipped but never closed" drift that accumulates when an agent forgets to run `bd close`.

```bash
bk guard stale-beads                                # current dir, default branch
bk guard stale-beads --branch origin/main --json    # CI form
bk guard stale-beads --prefix demo --lookback-days 30
```

**Match precision** (the make-or-break detail):

- The bead id must appear in the commit **subject**, not the body.
- The id must be flanked by characters NOT in the id alphabet (letters / digits / `_` / `.` / `-`).
- The subject must be in **one of two shapes**:
  - **Conventional-commit scope**: `feat(<id>): ...`, `<id>: ...`, `[<id>] ...`
  - **PR-merge marker present**: subject ends with `(#N)` AND the id is token-bounded anywhere
- The conventional-commit **type** must be in the implementation allowlist: `feat`, `fix`, `perf`, `refactor` (configurable via `--ship-types`).
- Type `spec` and scope `bd` / `beads` are **never** shipping (bead-management commits — filing, spec writing, status updates).

Tangential mentions like `chore: bump deps — see <id> for context`, bead-filing commits like `chore(bd): file <id>`, and spec commits like `spec(<id>): file epic` are deliberately NOT flagged.

**Status source** — `--source auto` (default) reads bead status from `bd list --json` first
(authoritative; reflects post-`bd close` state immediately) and merges with `.beads/issues.jsonl`
for ids bd hasn't ingested yet (e.g. fresh clones with no SQLite DB). `--source bd` requires bd;
`--source jsonl` reads only the on-disk JSONL (the legacy behavior, useful when bd is unreachable
from the runner).

**`--close` (apply mode)** — close the shipped-not-closed beads automatically:

```bash
bk guard stale-beads --close                       # dry-run plan
bk guard stale-beads --close --apply               # actually close them
bk guard stale-beads --close --apply --json        # agent-driveable summary
bk guard stale-beads --close --apply \
  --exclude demo-keep1,demo-keep2                  # skip these specific ids
bk guard stale-beads --close --apply --force       # close even if blocked by open child issues
```

Each finding becomes one of:

- **close**: bead has no open blockers; `bd close <id> --reason "shipped in #<PR>"` runs.
- **skip-blocked**: bead has at least one open `blocks` dependency; bd would refuse with `(use --force)`. Use `--force` to override (closes with `--force`).
- **skip-excluded**: id appears in `--exclude` — reserved for beads pending verification.

Re-runs are idempotent: bkg-td0.2's authoritative status read sees freshly-closed beads as `closed` so they never re-enter the planner. The `--json` summary's `counts` block is shaped so an agent loop can drive the workflow deterministically.

**JSON shape** (`--json`) — each finding includes a `bd_close_command` field shaped so an
agent can drive closure deterministically:

```json
{
  "id": "demo-xymh",
  "status": "in_progress",
  "landing_sha": "...",
  "landing_subject": "feat(demo-xymh): ship parser (#736)",
  "landing_pr": 736,
  "bd_close_command": "bd close demo-xymh --reason \"shipped in #736\""
}
```

Exit codes per bk's contract: `0` clean, `2` RED (one or more shipped-not-closed),
`64` missing flag, `127` git missing.

### `bk guard daemon` — daemon hygiene

Detects: duplicate `bd daemon` processes for one workspace (RED); a live daemon whose log
shows ≥2 recent `Repository not found` / `Authentication failed` / similar (RED — the
silent-sync-death pattern); orphan `sync-state.json` with no daemon (YELLOW).

```bash
bk guard daemon ~/code
```

### `bk identity` — canonical actor identity

Reads `.beadkeeper/identity.toml` (TOML; opt-in — absent config means the feature is off).
Bead JSONL accumulates the same person under several handles (`alice`, `alice@laptop`,
`Alice`); `identity check` reports drift, `identity normalize --yes` rewrites aliases to
the canonical handle. Refuses (rc=3) while the daemon is alive.

```bash
bk identity check .
bk identity normalize . --yes
```

`identity.toml` schema:

```toml
[identity]
canonical = ["alice", "bob"]

[identity.aliases]
"alice@example.com" = "alice"
"a.smith"           = "alice"
"robert"            = "bob"
```

### `bk trunk-sync` — reconcile trunk vs. sync branch

beads keeps issue state on a dedicated sync branch; trunk only sees it after replay.
`trunk-sync` reports drift; `--apply` fast-forwards the sync-branch JSONL onto trunk in a
single commit. Divergence detection is content-aware (id-based), so re-running after a
previous apply doesn't false-flag its own replay commit. Refuses (rc=3) on a divergent
trunk edit, a live daemon, or a dirty working tree.

```bash
bk trunk-sync ~/code            # report drift
bk trunk-sync . --apply         # plan the replay (dry-run)
bk trunk-sync . --apply --yes   # commit the replay onto trunk
```

### `bk lease` — per-issue lease discipline

Wraps `bd update --status in_progress --assignee <canonical>` so two agents never both
grab the same bead. Caller resolved through `.beadkeeper/identity.toml` (or `--as` to
override). Idempotent self-claim, conflict-typed errors (rc=3), refuses on live daemon.

```bash
bk lease claim x.7 --repo .
bk lease release x.7 --repo .
bk lease list ~/code            # active leases across projects (flags stale)
```

### `bk merge-slot` — serialize merge+deploy

Wraps bd's per-workspace merge slot. `acquire`/`release`/`status`. Idempotent self-acquire,
conflict-typed errors, refuses on live daemon.

```bash
bk merge-slot status --repo .
bk merge-slot acquire --repo . --holder alice
# ... do merge+deploy ...
bk merge-slot release --repo . --holder alice
```

### `bk install-hooks` — sync-death alarm

Installs a `pre-push` hook that runs `bk doctor` and either warns (default) or blocks on
RED. The hook marker is shared with the Python tool — see [Interop](#interop-with-python-beadkeeper)
below.

```bash
bk install-hooks                # warn-only
bk install-hooks --block        # block on RED
bk install-hooks --print-prompt-indicator  # also print the shell snippet
bk uninstall-hooks
```

Override at push time: `BEADKEEPER_BLOCK_ON_RED=1` (escalate) / `BEADKEEPER_SKIP_HOOK=1`
(bypass).

### `bk prompt-indicator` — shell prompt dot

Sourceable shell snippet that prints a colored dot when the current dir's nearest
`.beads/` project is non-GREEN. 60 s on-disk cache so it never slows your prompt down.

```bash
bk prompt-indicator >> ~/.zshrc
# then in PROMPT/PS1: ... $(beadkeeper_prompt) ...
```

## Interop with Python beadkeeper

`bk` and `beadkeeper` are interchangeable per-invocation. They:

- Read and write the **same** `.beads/issues.jsonl` (and the same `.beads/config.json`
  fallback for `sync.branch`).
- Read the **same** `.beadkeeper/identity.toml`.
- Honor the **same** environment variables: `BEADKEEPER_FILESYNC_ROOTS`, `BD_ACTOR`,
  `BEADKEEPER_BLOCK_ON_RED`, `BEADKEEPER_SKIP_HOOK`.
- Install hooks with the **identical marker** (`# beadkeeper-managed: pre-push v1`), so a
  Python-installed hook is recognised by `bk uninstall-hooks` and vice versa.
- Use the same `bd` / `git` shell-out boundary — no in-process re-implementation of either.

That means in any given workspace you can:

```bash
beadkeeper doctor .   # Python; venv-driven
# ... and later, on the same repo ...
bk doctor .           # Go; statically linked
```

…and both report the same severity. Behavioral parity is enforced by the diff harness in
[`tools/diffharness/`](tools/diffharness/) — see [`docs/PARITY.md`](docs/PARITY.md) for
the test-by-test 133→Go map and the accepted-divergence ledger (8 cross-language
idiomatic deltas, all fully whitelisted).

When in doubt: prefer `bk` for one-shot CI invocations (single static binary, no setup);
prefer `beadkeeper` when you're already in a Python environment and want pip-style
extensibility.

## Status

- M0 — Scaffolding & CI: ✅
- M1 — Shared core: ✅
- M2 — Read-only commands: ✅
- M3 — Mutation + coordination: ✅
- M4 — Test parity & diff harness: ✅
- M5 — Distribution & cutover: ✅
- M6 — Coordination harness coverage: ✅

`v0.1.0` is released and public — install via Homebrew or a direct download above.

## License

MIT — see [`LICENSE`](LICENSE).
