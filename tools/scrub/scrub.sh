#!/usr/bin/env bash
# tools/scrub/scrub.sh — irreversible. REWRITES git history.
#
# Refuses to run unless:
#   - BEEKEEPER_GO_SCRUB_REWRITE_HISTORY=YES is set in the environment.
#   - working tree is clean (no staged or unstaged changes).
#   - we're on the main branch (force-push target).
#   - git-filter-repo is installed.
#
# After running, the script:
#   - tags a recoverable backup ref under refs/backup/ and pushes it.
#   - deletes the stale M0/M1 feature branches locally and remotely.
#   - filters every blob and commit message: replaces the vendor-
#     stamped strings with neutral placeholders.
#   - prunes commits that became empty after the substitution.
#
# AFTER the rewrite the script does NOT force-push automatically.
# The operator runs:
#   git push --force-with-lease origin main
#   git push origin --tags --force
#
# Usage:
#   BEEKEEPER_GO_SCRUB_REWRITE_HISTORY=YES tools/scrub/scrub.sh

set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

# --- guards -----------------------------------------------------------------

if [ "${BEEKEEPER_GO_SCRUB_REWRITE_HISTORY:-}" != "YES" ]; then
  echo "REFUSE: BEEKEEPER_GO_SCRUB_REWRITE_HISTORY must be set to YES." >&2
  echo "        This script rewrites git history and must be run with explicit" >&2
  echo "        operator sign-off. Run tools/scrub/dry-run.sh first." >&2
  exit 64
fi

if ! git diff --quiet || ! git diff --cached --quiet; then
  echo "REFUSE: working tree or index is dirty. Commit / stash / discard first." >&2
  exit 3
fi

cur_branch=$(git rev-parse --abbrev-ref HEAD)
if [ "$cur_branch" != "main" ]; then
  echo "REFUSE: must be on main; got $cur_branch." >&2
  exit 3
fi

if ! command -v git-filter-repo >/dev/null 2>&1 \
   && ! python3 -m git_filter_repo --help >/dev/null 2>&1; then
  echo "REFUSE: git-filter-repo not installed." >&2
  echo "        Install with:  pipx install git-filter-repo" >&2
  exit 127
fi

# --- backup -----------------------------------------------------------------

ts=$(date +%s)
backup_ref="refs/backup/pre-scrub-${ts}"
echo "==> tagging backup ref ${backup_ref}"
git update-ref "$backup_ref" HEAD
git push origin "$backup_ref"

# --- prune stale feature branches ------------------------------------------

for b in feature/m0-scaffolding feature/m1-shared-core; do
  if git show-ref --quiet "refs/heads/$b"; then
    echo "==> deleting local branch $b"
    git branch -D "$b" || true
  fi
  if git ls-remote --exit-code --heads origin "$b" >/dev/null 2>&1; then
    echo "==> deleting remote branch origin/$b"
    git push origin --delete "$b" || true
  fi
done

# --- rewrite ---------------------------------------------------------------

# Build the replacement table. Pin the substitutions in a temp file so
# git-filter-repo can read them from disk (it doesn't accept multiple
# --replace-text flags on the CLI).
repl=$(mktemp)
trap 'rm -f "$repl"' EXIT
cat > "$repl" <<'EOF'
literal:vendor.example==>vendor.example
literal:contributor==>contributor
EOF

echo "==> rewriting blobs + commit messages with git-filter-repo"
if command -v git-filter-repo >/dev/null 2>&1; then
  git filter-repo \
    --replace-text "$repl" \
    --replace-message "$repl" \
    --prune-empty auto \
    --force
else
  python3 -m git_filter_repo \
    --replace-text "$repl" \
    --replace-message "$repl" \
    --prune-empty auto \
    --force
fi

cat <<EOF

==> rewrite complete.

Verify:
  tools/scrub/verify.sh

Push (review the diff first):
  git push --force-with-lease origin main
  git push origin --tags --force

Recovery (if anything looks wrong):
  git update-ref refs/heads/main ${backup_ref}
  git push --force-with-lease origin main

EOF
