---
name: bd-onboard
description: Use when starting work in an unfamiliar repo, when asked to "get up to speed", or when there is no progression bead yet capturing what the project is. Studies the repo and creates the first "Project understanding" progression bead.
---

# Get up to speed & seed the project-understanding progression

Use this when you land in a repo and want a durable, living summary of what the
project is and where it stands — captured as a `progression`-labeled bead so the
next session starts informed. Requires `bk` (>= v0.5.0) and `bd`.

## Steps

1. **Check whether a project-understanding progression already exists.**
   ```bash
   bk progression list --repo .
   ```
   If one titled like `Project understanding: <repo>` already exists, do NOT
   create a second — switch to `bd-progression` and append to it.

2. **Study the repo.** Read the README, any `docs/` (architecture/decisions),
   the top-level layout, and recent history (`git log --oneline -20`). Skim open
   beads (`bd ready`, `bd list`). Form a concise model: what the project is, its
   architecture, and current state.

3. **Scaffold the progression bead.**
   ```bash
   bk progression new "Project understanding: <repo>"
   # prints: created progression <id>: "Project understanding: <repo>"
   ```

4. **Replace the placeholder with the real summary.** The scaffold leaves a
   `_TODO_` in `## Current understanding`; fill it in, preserving `## Log`.
   ```bash
   bd update <id> -d "$(cat <<'EOF'
   # Project understanding: <repo>

   ## Current understanding
   <one or two tight paragraphs: what the project is, key architecture,
   how the pieces fit, and where things currently stand>

   ## Log
   - <YYYY-MM-DD> baseline: initial understanding captured from README,
     docs, and recent history.
   EOF
   )"
   ```

5. **Verify it conforms.**
   ```bash
   bk progression list --repo .   # your bead should show with its one-liner
   ```

## Notes

- Don't pre-seed blank progressions in repos with no activity — only create one
  once you've actually formed an understanding worth recording.
- Progression beads are kept OFF the work board automatically.
