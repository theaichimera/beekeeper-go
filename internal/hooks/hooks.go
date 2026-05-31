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
# This hook runs ` + "`bk doctor`" + ` against the current repo and either
# warns or blocks the push when the report is RED.
#
# Override behavior:
#   - Set BEADKEEPER_BLOCK_ON_RED=1 to make warnings blocking.
#   - Set BEADKEEPER_SKIP_HOOK=1 to bypass (e.g. for emergency pushes).
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

if [ "$rc" -ne 0 ]; then
  if [ "${BEADKEEPER_BLOCK_ON_RED:-%s}" = "1" ]; then
    printf "\nbeadkeeper: BLOCKING push — doctor reported RED. Override with BEADKEEPER_SKIP_HOOK=1.\n" >&2
    exit 1
  fi
  printf "\nbeadkeeper: warning — doctor reported RED. Push allowed; set BEADKEEPER_BLOCK_ON_RED=1 to block.\n" >&2
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
