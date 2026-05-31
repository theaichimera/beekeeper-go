#!/usr/bin/env bash
# tools/scrub/dry-run.sh — read-only inventory of vendor leaks across
# this repo's history. Safe to run any time. Touches nothing.
#
# Usage: tools/scrub/dry-run.sh
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

# Vendor-string allowlist. Long tokens are matched anywhere; short
# tokens (`csod`, `esw`, `jive`) require word boundaries so they
# don't collide with English (`Refuses While...`). git log -G uses
# POSIX ERE which doesn't honor `\b`; we use `[[:<:]]`/`[[:>:]]`.
LONG_RE='trilogy|khoros|vendor.example|toolgate|agentflow|cloudfix|crossover|aurea|spigit|cornerstone|nomio|contributor'
SHORT_RE='[[:<:]](csod|esw|jive)[[:>:]]'
VENDOR_RE="(${LONG_RE})|${SHORT_RE}"

echo "--- commits referencing vendor strings (-G regex over diffs, all refs) ---"
git log --all --no-merges --pretty=format:"%h %s" -i -G "$VENDOR_RE" || true
echo
echo

echo "--- branch tips with vendor strings in tracked files ---"
for ref in $(git for-each-ref --format='%(refname)' refs/heads refs/remotes); do
  hits=$(git grep -l -i -E "$VENDOR_RE" "$ref" -- 2>/dev/null || true)
  if [ -n "$hits" ]; then
    echo "$ref"
    while IFS= read -r line; do
      echo "  $line"
    done <<< "$hits"
  fi
done
echo

echo "--- author / committer scan ---"
git log --all --pretty=format:"%H %an <%ae> // committer: %cn <%ce>" \
  | grep -iE "$VENDOR_RE" || echo "(none)"
echo

echo "--- summary ---"
n_commits=$(git log --all --no-merges --pretty=format:"%h" -G "$VENDOR_RE" 2>/dev/null | wc -l | tr -d ' ')
echo "commits with vendor diffs: $n_commits"
echo "(this is the number filter-repo will rewrite if scrub.sh runs)"
