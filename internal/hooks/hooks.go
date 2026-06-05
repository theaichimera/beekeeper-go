// Package hooks installs / removes beadkeeper's git pre-push hook and
// renders the shell-prompt indicator snippet.
//
// Port of Python beadkeeper.hooks. The Marker constant is what
// distinguishes our hook from any pre-existing one; install refuses
// to clobber a foreign hook unless force is true. Uninstall removes
// only marker-bearing hooks.
//
// The hook calls `bk doctor` (Python uses `beadkeeper`); both binaries
// share an exit-code contract so the hook semantics carry across.
package hooks

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Marker identifies a beadkeeper-managed pre-push hook. Mirrors the
// Python string verbatim so a Python-installed hook is detected here
// (and vice versa).
const Marker = "# beadkeeper-managed: pre-push v1"

// PreCommitMarker identifies the bk-managed pre-commit drift-guard
// hook (bkg-59b). Distinct from `Marker` so doctor's hook-presence
// check (bkg-6h7) can tell whether bk's drift guard is installed —
// bd's flush-only pre-commit shares the file but lacks this line.
const PreCommitMarker = "# bk-managed: pre-commit drift-guard v1"

// PreCommitChainedSuffix names the file `InstallPreCommit` moves an
// existing non-bk pre-commit to before installing the bk wrapper. The
// wrapper exec's it after the drift gate so bd's flush-only hook keeps
// firing on every commit.
const PreCommitChainedSuffix = "pre-commit.bk-chained"

// InstallResult mirrors Python's InstallResult.
type InstallResult struct {
	Path          string
	Written       bool
	SkippedReason string
}

// preferredBinary controls which CLI the rendered hook invokes. `bk`
// is the Go binary; for parity with the Python tool, the hook checks
// `command -v` and skips gracefully when the binary isn't on PATH.
var preferredBinary = "bk"

const prePushTemplate = `#!/usr/bin/env bash
%s
# This hook runs ` + "`bk doctor`" + ` against the current repo and, optionally,
# ` + "`bk guard pr-beads`" + ` against the configured upstream sync branch. It
# either warns or blocks the push when either report is RED.
#
# Override behavior:
#   - Set BEADKEEPER_BLOCK_ON_RED=1 to make warnings blocking.
#   - Set BEADKEEPER_SKIP_HOOK=1 to bypass (e.g. for emergency pushes).
#   - Set BEADKEEPER_PRBEADS_POLICY to one of:
#       off       — skip the pr-beads check entirely (default).
#       regression — warn/block on backlog regressions only.
#       no-beads  — warn/block on ANY .beads/issues.jsonl modification.
#
# Uninstall: ` + "`bk uninstall-hooks`" + `.

set -u

if [ "${BEADKEEPER_SKIP_HOOK:-0}" = "1" ]; then
  exit 0
fi

BK=%s

REPO_DIR="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"

if ! command -v %s >/dev/null 2>&1; then
  printf "%s not on PATH; skipping pre-push doctor.\n" >&2
  exit 0
fi

$BK doctor "$REPO_DIR" --no-color
rc=$?

PR_RC=0
PRBEADS_POLICY="${BEADKEEPER_PRBEADS_POLICY:-off}"
if [ "$PRBEADS_POLICY" != "off" ]; then
  ($BK guard pr-beads --repo "$REPO_DIR" --policy "$PRBEADS_POLICY")
  PR_RC=$?
fi

# Bead-schema hard gate (bkg-4zi.6): epics must carry rationale +
# a well-formed ## Decisions block. Always blocking (not warn-only),
# bypass with BEADKEEPER_SKIP_HOOK=1.
$BK guard beadspec "$REPO_DIR"
if [ "$?" -eq 2 ]; then
  printf "\nbeadkeeper: BLOCKING push — epic bead-schema violation(s) above. Override with BEADKEEPER_SKIP_HOOK=1.\n" >&2
  exit 1
fi

if [ "$rc" -ne 0 ] || [ "$PR_RC" -eq 2 ]; then
  if [ "${BEADKEEPER_BLOCK_ON_RED:-%s}" = "1" ]; then
    printf "\nbeadkeeper: BLOCKING push — doctor or pr-beads reported RED. Override with BEADKEEPER_SKIP_HOOK=1.\n" >&2
    exit 1
  fi
  printf "\nbeadkeeper: warning — doctor or pr-beads reported RED. Push allowed; set BEADKEEPER_BLOCK_ON_RED=1 to block.\n" >&2
fi

exit 0
`

const promptIndicatorTemplate = `# beadkeeper prompt indicator — source from .bashrc / .zshrc
%s
#
# Adds a function ` + "`beadkeeper_prompt`" + ` that prints a single character when
# the current directory's nearest .beads/ project is in non-GREEN health.
# Wire it into your PS1 / PROMPT; e.g.:
#
#   bash:  PS1='\u@\h \w $(beadkeeper_prompt)$ '
#   zsh:   PROMPT='%%n@%%m %%~ $(beadkeeper_prompt)%%# '
#
# Override behavior:
#   - Set BEADKEEPER_PROMPT_TTL_SECONDS to override the cache TTL (default 60).
#   - Set BEADKEEPER_PROMPT_DISABLE=1 to silence the indicator.

beadkeeper_prompt() {
  if [ "${BEADKEEPER_PROMPT_DISABLE:-0}" = "1" ]; then return 0; fi

  local dir
  dir="$(pwd -P 2>/dev/null)"
  [ -z "$dir" ] && return 0

  local cur="$dir"
  local project=""
  while [ "$cur" != "/" ] && [ -n "$cur" ]; do
    if [ -d "$cur/.beads" ]; then
      project="$cur"
      break
    fi
    cur="$(dirname "$cur")"
  done
  [ -z "$project" ] && return 0

  local ttl="${BEADKEEPER_PROMPT_TTL_SECONDS:-60}"
  local cache_dir="${TMPDIR:-/tmp}/beadkeeper-prompt"
  mkdir -p "$cache_dir" 2>/dev/null

  local hash
  if command -v shasum >/dev/null 2>&1; then
    hash="$(printf "%%s" "$project" | shasum | awk '{print $1}')"
  else
    hash="$(printf "%%s" "$project" | cksum | awk '{print $1}')"
  fi
  local cache="$cache_dir/$hash"

  local now
  now=$(date +%%s)
  local age=999999
  if [ -f "$cache" ]; then
    local mtime
    mtime=$(stat -f %%m "$cache" 2>/dev/null || stat -c %%Y "$cache" 2>/dev/null || echo 0)
    age=$(( now - mtime ))
  fi

  if [ "$age" -gt "$ttl" ]; then
    %s doctor "$project" --json >"$cache.tmp" 2>/dev/null
    mv -f "$cache.tmp" "$cache" 2>/dev/null
  fi

  if [ ! -s "$cache" ]; then return 0; fi

  if grep -q '"worst": "red"' "$cache" 2>/dev/null; then
    printf "\033[31m●\033[0m"
  elif grep -q '"worst": "yellow"' "$cache" 2>/dev/null; then
    printf "\033[33m●\033[0m"
  fi
}
`

// RenderPrePush builds the pre-push hook script with the Marker line
// + the optional default-blocking behavior.
func RenderPrePush(defaultBlock bool) string {
	flag := "0"
	if defaultBlock {
		flag = "1"
	}
	return fmt.Sprintf(prePushTemplate, Marker, preferredBinary, preferredBinary, preferredBinary, flag)
}

// preCommitTemplate is the pre-commit drift-guard hook template
// (bkg-59b). It calls `bk doctor --gate`, which performs the cheap
// drift scan and exits per the bk contract:
//
//	0 GREEN, 1 YELLOW (warn), 2 RED (or YELLOW if BK_DRIFT_BLOCK=1)
//
// If a non-bk pre-commit existed when InstallPreCommit ran, it was
// moved to `pre-commit.bk-chained`; this wrapper exec's that next so
// bd's flush-only hook (or any other) keeps firing on every commit.
//
// Override knobs:
//
//	BK_DRIFT_SKIP=1   — bypass the drift gate entirely (emergency commits)
//	BK_DRIFT_BLOCK=1  — make the gate block the commit on YELLOW (default: warn)
const preCommitTemplate = `#!/usr/bin/env bash
%s
# bk pre-commit drift-guard. Cheap (no pr-beads diff) — runs:
#   - rev-list --count to detect "branch behind base"
#   - reads bd sync.branch + sync-state for "needs_manual_sync"
# Then exec's the chained hook (.git/hooks/pre-commit.bk-chained) if any.
#
# Override:
#   BK_DRIFT_SKIP=1   bypass entirely (emergency commits).
#   BK_DRIFT_BLOCK=1  make YELLOW findings block instead of warn.
#
# Uninstall: ` + "`bk uninstall-hooks`" + `.

set -u

if [ "${BK_DRIFT_SKIP:-0}" = "1" ]; then
  exit 0
fi

BK=%s
REPO_DIR="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"

if command -v $BK >/dev/null 2>&1; then
  GATE_FLAGS="--gate"
  if [ "${BK_DRIFT_BLOCK:-0}" = "1" ]; then
    GATE_FLAGS="--gate --strict"
  fi
  $BK doctor "$REPO_DIR" $GATE_FLAGS
  rc=$?
  # Exit codes from drift gate:
  #   0 GREEN (silent)
  #   1 YELLOW warn (allow commit, message already on stderr)
  #   2 RED or --strict YELLOW -> block
  if [ "$rc" -eq 2 ]; then
    printf "\nbk: BLOCKING commit — drift gate reported RED. Override with BK_DRIFT_SKIP=1.\n" >&2
    exit 1
  fi
fi

# Chain to any pre-existing pre-commit (e.g. bd's flush-only hook).
CHAINED="$(dirname "$0")/pre-commit.bk-chained"
if [ -x "$CHAINED" ]; then
  exec "$CHAINED" "$@"
fi
exit 0
`

// RenderPreCommit builds the pre-commit drift-guard hook script. The
// rendered body always includes both PreCommitMarker (so doctor /
// idempotent re-install can detect it) and the chain-exec line at
// the bottom.
func RenderPreCommit() string {
	return fmt.Sprintf(preCommitTemplate, PreCommitMarker, preferredBinary)
}

// RenderPromptIndicator builds the sourceable prompt indicator script.
func RenderPromptIndicator() string {
	return fmt.Sprintf(promptIndicatorTemplate, Marker, preferredBinary)
}

// InstallPrePush writes `.git/hooks/pre-push` for `repo`. Refuses to
// overwrite a foreign hook unless `force` is true. Re-installing our
// own hook is always allowed.
func InstallPrePush(repo string, defaultBlock, force bool) InstallResult {
	gitDir := resolveGitDir(repo)
	if gitDir == "" {
		return InstallResult{
			Path:          filepath.Join(repo, ".git", "hooks", "pre-push"),
			SkippedReason: "not a git working tree",
		}
	}
	hooksDir := filepath.Join(gitDir, "hooks")
	_ = os.MkdirAll(hooksDir, 0o755)
	target := filepath.Join(hooksDir, "pre-push")

	if data, err := os.ReadFile(target); err == nil {
		body := string(data)
		if !strings.Contains(body, Marker) && !force {
			return InstallResult{
				Path: target,
				SkippedReason: "a non-beadkeeper pre-push hook is already present; " +
					"re-run with --force to overwrite, or merge by hand",
			}
		}
	}

	if err := os.WriteFile(target, []byte(RenderPrePush(defaultBlock)), 0o755); err != nil {
		return InstallResult{
			Path:          target,
			SkippedReason: err.Error(),
		}
	}
	return InstallResult{Path: target, Written: true}
}

// InstallPreCommit installs `.git/hooks/pre-commit` for `repo` with
// the bk drift-guard wrapper. If a non-bk hook already lives there
// (typical: bd's `bd sync --flush-only` hook), it's preserved by
// being moved to `pre-commit.bk-chained`; the wrapper exec's it after
// the drift gate so bd's flush still fires on every commit.
//
// Idempotent: re-installing detects PreCommitMarker and rewrites the
// wrapper without touching the chained file. Refuses to overwrite a
// foreign pre-commit (i.e. one without our marker AND without bd's
// expected shape) unless `force` is true.
//
// Returns the install path; SkippedReason populated when the install
// could not proceed (no .git, write error, foreign hook without
// --force, etc.).
func InstallPreCommit(repo string, force bool) InstallResult {
	gitDir := resolveGitDir(repo)
	if gitDir == "" {
		return InstallResult{
			Path:          filepath.Join(repo, ".git", "hooks", "pre-commit"),
			SkippedReason: "not a git working tree",
		}
	}
	hooksDir := filepath.Join(gitDir, "hooks")
	_ = os.MkdirAll(hooksDir, 0o755)
	target := filepath.Join(hooksDir, "pre-commit")
	chained := filepath.Join(hooksDir, PreCommitChainedSuffix)

	existing, readErr := os.ReadFile(target)
	if readErr == nil {
		body := string(existing)
		if strings.Contains(body, PreCommitMarker) {
			// Idempotent re-install: just rewrite the wrapper. Leave
			// any chained file untouched — re-installing should NOT
			// re-chain whatever is currently at `pre-commit`, because
			// that file IS the bk wrapper.
			if err := os.WriteFile(target, []byte(RenderPreCommit()), 0o755); err != nil {
				return InstallResult{Path: target, SkippedReason: err.Error()}
			}
			return InstallResult{Path: target, Written: true}
		}
		// Foreign hook present. Without --force, only allow chaining
		// when there's no existing chained file (don't clobber a
		// previous chain). With --force, always chain (overwrite the
		// chained file).
		if _, statErr := os.Stat(chained); statErr == nil && !force {
			return InstallResult{
				Path: target,
				SkippedReason: "a non-bk pre-commit hook is present and `pre-commit.bk-chained` " +
					"already exists; re-run with --force to overwrite, or merge by hand",
			}
		}
		// Move existing -> chained, preserving exec bit.
		if err := os.WriteFile(chained, existing, 0o755); err != nil {
			return InstallResult{Path: target, SkippedReason: err.Error()}
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return InstallResult{Path: target, SkippedReason: readErr.Error()}
	}

	if err := os.WriteFile(target, []byte(RenderPreCommit()), 0o755); err != nil {
		return InstallResult{Path: target, SkippedReason: err.Error()}
	}
	return InstallResult{Path: target, Written: true}
}

// UninstallPreCommit removes only marker-bearing hooks AND restores
// any chained predecessor in place.
func UninstallPreCommit(repo string) InstallResult {
	gitDir := resolveGitDir(repo)
	if gitDir == "" {
		return InstallResult{
			Path:          filepath.Join(repo, ".git", "hooks", "pre-commit"),
			SkippedReason: "not a git working tree",
		}
	}
	target := filepath.Join(gitDir, "hooks", "pre-commit")
	chained := filepath.Join(gitDir, "hooks", PreCommitChainedSuffix)

	data, err := os.ReadFile(target)
	if errors.Is(err, os.ErrNotExist) {
		return InstallResult{Path: target, SkippedReason: "no hook installed"}
	}
	if err != nil {
		return InstallResult{Path: target, SkippedReason: err.Error()}
	}
	if !strings.Contains(string(data), PreCommitMarker) {
		return InstallResult{Path: target, SkippedReason: "hook is not bk-managed (no marker)"}
	}
	if err := os.Remove(target); err != nil {
		return InstallResult{Path: target, SkippedReason: err.Error()}
	}
	// Restore chained predecessor, if any.
	if chainedData, err := os.ReadFile(chained); err == nil {
		if err := os.WriteFile(target, chainedData, 0o755); err == nil {
			_ = os.Remove(chained)
		}
	}
	return InstallResult{Path: target, Written: true}
}

// UninstallPrePush removes only marker-bearing hooks.
func UninstallPrePush(repo string) InstallResult {
	gitDir := resolveGitDir(repo)
	if gitDir == "" {
		return InstallResult{
			Path:          filepath.Join(repo, ".git", "hooks", "pre-push"),
			SkippedReason: "not a git working tree",
		}
	}
	target := filepath.Join(gitDir, "hooks", "pre-push")
	data, err := os.ReadFile(target)
	if errors.Is(err, os.ErrNotExist) {
		return InstallResult{Path: target, SkippedReason: "no hook installed"}
	}
	if err != nil {
		return InstallResult{Path: target, SkippedReason: err.Error()}
	}
	if !strings.Contains(string(data), Marker) {
		return InstallResult{Path: target, SkippedReason: "hook is not beadkeeper-managed (no marker)"}
	}
	if err := os.Remove(target); err != nil {
		return InstallResult{Path: target, SkippedReason: err.Error()}
	}
	return InstallResult{Path: target, Written: true}
}

// resolveGitDir mirrors Python `_resolve_git_dir`: handles a normal
// `.git/` dir AND a worktree's `.git` text file (`gitdir: ...`).
func resolveGitDir(repo string) string {
	repo, _ = filepath.Abs(repo)
	if resolved, err := filepath.EvalSymlinks(repo); err == nil {
		repo = resolved
	}
	gitPath := filepath.Join(repo, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return gitPath
	}
	data, err := os.ReadFile(gitPath)
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))
	const prefix = "gitdir:"
	if !strings.HasPrefix(content, prefix) {
		return ""
	}
	target := strings.TrimSpace(content[len(prefix):])
	if !filepath.IsAbs(target) {
		target = filepath.Join(repo, target)
	}
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		return target
	}
	return ""
}
