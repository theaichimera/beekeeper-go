# Pre-public history scrub — plan + scripts

> **STATUS: prepared but NOT executed.** History rewrite + force-push is
> irreversible and must be run by the orchestrator with explicit sign-off.

## What this does

Beekeeper Go was developed in a private repo where some early commits
landed before the bd actor identity was scrubbed. The current `main`
tip is clean — `grep` over the in-tree files returns zero — but a
handful of commits in the history (and a few stale feature branches)
still carry the vendor-stamped strings inside `.beads/issues.jsonl`
deltas and commit messages.

This directory packages the scrub as three small scripts so a reviewer
can:

1. See what's there (`dry-run.sh` — read-only).
2. Run the rewrite under a tightly-scoped guard env var (`scrub.sh`).
3. Verify the result (`verify.sh`).

`scrub.sh` will refuse to touch the repo unless
`BEEKEEPER_GO_SCRUB_REWRITE_HISTORY=YES` is set. It will refuse a
second time unless the working tree is clean. It will refuse a third
time if there's no backup branch.

## Inventory at the time of writing

```
$ tools/scrub/dry-run.sh
--- commits referencing vendor strings (-G regex over diffs, all refs) ---
5372f12 chore: re-scrub vendor identity from bead metadata (post-M1 merge)
9633549 chore(beads): close bkg-9cs.2 (M1 shipped)
d536076 feat(m1): shared core — exec/filesync/git/config/proc/project (bkg-9cs.2)
8b05267 chore: scrub vendor identity from bead metadata; lock neutral actor
3b779d0 chore(beads): close bkg-9cs.1 (M0 shipped)
3b07f09 feat(m0): scaffold Go module + cobra skeleton + CI matrix (bkg-9cs.1)
2ff1c8c Seed Beekeeper Go epic: spec, orchestrator prompt, and beads (bkg-9cs + 6 milestones)

--- branch tips with vendor strings in tracked files ---
refs/heads/feature/m0-scaffolding        .beads/issues.jsonl
refs/heads/feature/m1-shared-core        .beads/issues.jsonl
refs/remotes/origin/feature/m0-scaffolding .beads/issues.jsonl
refs/remotes/origin/feature/m1-shared-core .beads/issues.jsonl

--- author/email scan ---
(empty — author metadata was already scrubbed in earlier commits)

--- summary ---
commits with vendor diffs: 6
```

The 7 historical commits are all on `main`'s ancestry: M0 + M1 work
plus the in-line scrub commits. The 4 branch tips are stale (feature
branches the orchestrator already merged) and can be deleted before
/ instead of being rewritten.

The vendor regex used by these scripts is *word-bounded* on short
tokens (`csod`, `esw`, `jive`) using POSIX `[[:<:]]`/`[[:>:]]` so they
don't collide with English (`Refuses While...` contains `esW`). Long
tokens (`devfactory`, `dschwartzi`, etc.) are unbounded — those don't
collide with normal text.

## Required tool

[`git-filter-repo`](https://github.com/newren/git-filter-repo) — a
modern replacement for `git filter-branch`. The scrub script invokes
it via `python3 -m git_filter_repo` (the `pip install git-filter-repo`
form). Install:

```bash
pipx install git-filter-repo   # or: pip install --user git-filter-repo
```

`scrub.sh` checks for it and refuses to run otherwise.

## The plan

```
1. PRE-FLIGHT
     - Confirm working tree clean (`git status --porcelain` empty).
     - Confirm we are NOT on a public-facing branch.
     - Tag a backup ref:  refs/backup/pre-scrub-$(date +%s)
     - Push the backup ref to origin so the rewrite is recoverable.

2. PRUNE STALE BRANCHES
     git branch    -D feature/m0-scaffolding feature/m1-shared-core
     git push origin --delete feature/m0-scaffolding
     git push origin --delete feature/m1-shared-core

3. REWRITE COMMIT MESSAGES + JSONL CONTENT
     Run filter-repo with --replace-text on every blob + commit
     message:
        devfactory==>vendor.example
        dschwartzi==>contributor
     This catches both .beads/issues.jsonl deltas AND any commit
     messages that quoted the strings during scrub-commits.

4. PRUNE EMPTY COMMITS
     filter-repo's --prune-empty=auto drops commits whose payload
     was entirely the scrub change (e.g. the in-line "scrub vendor
     identity" commits become empty after filtering and should
     drop out).

5. RECOMPUTE REFS
     filter-repo rewrites refs in place. After it runs, force-push:
        git push --force-with-lease origin main
        git push origin --tags --force
     ALL contributors must reclone — the SHAs will change.

6. VERIFY
     tools/scrub/verify.sh asserts:
        - 0 commits in `git log --all -G <vendor-strings>`
        - 0 blobs in `git rev-list --all --objects --filter=...`
          containing the strings
        - 0 author/committer email matches
     Exit non-zero if any of the above are non-zero.
```

## Why this isn't a release blocker

- The CURRENT `main` tip is clean: every released file, the README,
  every doc, every Go source has zero vendor refs. `bk` itself
  doesn't contain a single vendor string in any built artifact.
- The historical leak is only visible to anyone who clones the repo
  and runs `git log -p`. It's not in the binary, not in the
  release archives, not in the Homebrew formula.
- A scrub is therefore a **pre-public** action — required before
  flipping the repo public, optional otherwise.

## What awaits sign-off

The scrub itself is a force-push to `main` and a delete of stale
remote branches. Both require explicit orchestrator approval; the
script refuses to run without the guard env var set.

## Running it

```bash
# Step 1: see what's there (no writes).
tools/scrub/dry-run.sh

# Step 2: backup + rewrite (requires guard env var).
BEEKEEPER_GO_SCRUB_REWRITE_HISTORY=YES tools/scrub/scrub.sh

# Step 3: verify.
tools/scrub/verify.sh
```
