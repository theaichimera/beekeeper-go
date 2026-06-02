# Why Beekeeper (`bk`) — positioning

## TL;DR

`bk` is an **add-on** for the [beads](https://github.com/steveyegge/beads) issue
tracker (`bd`) — not a replacement, and not a database. beads already gives you a
good git-native issue model (issues + dependencies + a JSONL log that lives in your
repo). What hurts at scale is the *operational* layer around it: concurrent writers
on one append-heavy file, sync daemons that silently die for days, SQLite databases
accidentally dropped into Dropbox/iCloud, bead writes stranded on feature branches,
and no lease discipline so two agents grab the same work.

`bk` is the thin layer that watches for and coordinates exactly those failure modes.
It **owns operational health — never issue data.**

## The layers

```
your git repo            history, branches, the actual source of truth
   └─ beads (bd)         issue model: .beads/issues.jsonl + SQLite cache + sync daemon + merge driver
        └─ bk            operations layer: health, drift detection, leases, merge-slots, hooks
```

- **git** stores everything and is already in every repo you have.
- **beads (`bd`)** is the engine and the data model. It owns the `.beads/` directory,
  the JSONL log, the SQLite cache, the sync daemon, and a registered git `merge` driver
  that does field-level 3-way merges of the JSONL.
- **`bk`** sits on top. It reads what `bd` and `git` produce, reports RED/YELLOW/GREEN
  health across all your repos, catches drift, and coordinates multi-agent work. It
  shells out to `bd` / `git` rather than reimplementing them, and introduces **no new
  storage and no new source of truth for issues.**

## What `bk` is

- An **operations / observability layer** for beads.
- A single statically-linked binary — drop it on `PATH`, point it at your repos, done.
  No interpreter, no venv, no server, no migration.
- **Cross-project by default** — one `doctor` / `board` view across every hive.
- A coordinator that leans on **bead-native primitives** (`merge` driver, `merge-slot`,
  `daemon`, `hooks`) wherever they exist, adding new code only where beads has a genuine
  gap: health / observability and drift.

## What `bk` is *not*

- **Not an issue tracker.** beads is. `bk` never invents an issue model.
- **Not a database or storage engine.** It persists nothing of its own except a tiny
  on-disk health cache.
- **Not a server.** No daemon to run, no endpoint to operate.
- **Not a history rewriter.** `trunk-sync` replays as a single forward commit; it refuses
  on a dirty tree and never force-pushes.

## Why not just use Dolt / cr-sqlite / git-bug?

These come up because beads' data is versioned-in-git, which sounds adjacent to "Git for
data." They solve a different problem.

### Dolt ("Git for data")

Dolt is a **versioned SQL database** — you'd adopt it as your *storage layer*. That's the
opposite of what's needed here:

- Its headline feature, **cell/field-level 3-way merge**, is **already provided** by beads'
  registered `merge` git driver on the JSONL.
- Adopting it means **replacing beads' storage** (beads is SQLite + JSONL; there is no Dolt
  backend) — i.e. replacing beads, which defeats the point.
- It fixes **none** of the actual failure modes (daemon environment loss, DB-in-file-sync,
  branch stranding, leases) — and it *adds* a database server to operate.
- Dolt's superpower is **data branching**; `bk`'s direction is a *single* operational source
  of truth. The two philosophies are in tension.

### cr-sqlite (CRDT SQLite)

Interesting only if the storage layer is ever rebuilt. It's a storage-engine swap, not an
operational fix. Shelved.

### git-bug

A strong git-native, multi-writer issue model — but adopting it **forfeits beads' dependency
model**. Again a tracker replacement, not a coordinator.

### A centralized claim / health service

The eventual answer **if** git-sync topology genuinely can't keep up (atomic claims, events,
a health endpoint). Deliberately deferred behind evidence, and kept backend-agnostic — not
tied to any specific cloud or company. Gated behind "configuration + observability didn't
suffice."

## At a glance

| | What it is | Owns issue data? | New server? | Relationship to beads |
|---|---|---|---|---|
| **bk (Beekeeper)** | Ops / health / coordination layer | No | No | **Add-on** — wraps `bd` |
| Dolt | Versioned SQL database | Yes (would store it) | Yes | Replaces storage |
| cr-sqlite | CRDT storage engine | Yes | No | Replaces storage |
| git-bug | Git-native tracker | Yes | No | Replaces tracker |
| Central service | Claim / health backend | Health only | Yes | Future, gated alternative |

## Design tenets

1. **Wrap, don't replace.** `bd` stays the engine and the data model.
2. **Bead-native first.** Map every coordination feature to an existing `bd` primitive
   before writing custom code.
3. **One source of truth for *health* — never for *issue data*.** Issue data stays in
   beads / JSONL / git.
4. **Cross-project by default.** `bk` sees all your repos' hives at once.
5. **Dogfood.** `bk`'s own bead database lives outside any file-sync folder and uses a
   dedicated sync branch from commit #1.
