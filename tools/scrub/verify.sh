#!/usr/bin/env bash
# tools/scrub/verify.sh — assert ZERO vendor refs across all history.
# Use after tools/scrub/scrub.sh and before force-pushing.
#
# Exits 0 if clean, non-zero with a diagnosis if any leak remains.

set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

# NOTE: vendor tokens are written with a bracketed letter (e.g. dev[f]actory,
# tool[g]ate, agent[f]low, dschwart[z]i). The regex still matches the real
# tokens, but the file no longer contains the contiguous literal — so when
# scrub.sh runs git-filter-repo --replace-text over the whole tree, it CANNOT
# rewrite this denylist into a self-referential check (e.g. turning toolgate
# into its replacement term). Every token that scrub.sh actually replaces MUST
# be bracketed here. Do not "fix" the brackets. tools/scrub/* is also excluded
# from the scans below for the same reason.
LONG_RE='trilogy|khoros|dev[f]actory|tool[g]ate|agent[f]low|cloudfix|crossover|aurea|spigit|cornerstone|nomio|dschwart[z]i'
SHORT_RE='(^|[^[:alnum:]_])(csod|esw|jive)([^[:alnum:]_]|$)'
VENDOR_RE="(${LONG_RE})|${SHORT_RE}"

fail=0

echo "--- 1/4: commit messages ---"
hits=$(git log --all --pretty=format:"%H %s" | grep -iE "$VENDOR_RE" || true)
if [ -n "$hits" ]; then
  echo "LEAK in commit messages:"
  echo "$hits"
  fail=1
else
  echo "  (clean)"
fi

echo "--- 2/4: commit diffs (-G regex) ---"
hits=$(git log --all --no-merges --pretty=format:"%h %s" -G "$VENDOR_RE" -- . ':(exclude)tools/scrub/*' || true)
if [ -n "$hits" ]; then
  echo "LEAK in commit diffs:"
  echo "$hits"
  fail=1
else
  echo "  (clean)"
fi

echo "--- 3/4: tracked file contents at every ref ---"
for ref in $(git for-each-ref --format='%(refname)' refs/heads refs/tags refs/remotes refs/backup 2>/dev/null); do
  hits=$(git grep -l -i -E "$VENDOR_RE" "$ref" -- . ':(exclude)tools/scrub/*' 2>/dev/null || true)
  if [ -n "$hits" ]; then
    # `refs/backup/...` is exempt (the backup ref intentionally
    # carries the pre-scrub state until we delete it).
    if [[ "$ref" == refs/backup/* ]]; then
      echo "  (backup ref carries the pre-scrub state — expected: $ref)"
      continue
    fi
    echo "LEAK in $ref:"
    echo "$hits" | sed 's/^/    /'
    fail=1
  fi
done

echo "--- 4/4: author / committer metadata ---"
hits=$(git log --all --pretty=format:"%H %ae %ce" | grep -iE "$VENDOR_RE" || true)
if [ -n "$hits" ]; then
  echo "LEAK in author/committer metadata:"
  echo "$hits"
  fail=1
else
  echo "  (clean)"
fi

echo
if [ "$fail" -eq 0 ]; then
  echo "ALL CLEAN — safe to force-push."
  exit 0
fi
echo "VERIFY FAILED — re-run tools/scrub/scrub.sh or investigate."
exit 1
