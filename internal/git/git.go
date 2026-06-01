// Package git is a thin typed wrapper over the `git` CLI.
//
// Each call site that the Python tool uses lives here as a single
// named function. The argv passed to `git` MUST match the Python
// source exactly (see ~/cc/beadkeeper/src/beadkeeper/{project,
// syncbranch,trunksync}.py).
//
// Helpers never raise — they return (value, ok) for "ran cleanly with
// a known shape" or (value, rc, stdout, stderr, err) for low-level
// access. The shape mirrors the Python project._run helper.
package git

import (
	"os/exec"
	"strings"
	"time"

	execwrap "github.com/theaichimera/beekeeper-go/internal/exec"
)

// DefaultTimeout matches the Python project._run default (5 s) for
// short, fast queries. trunksync uses 15 s for heavier `git log` calls;
// the caller may override via Run.
const DefaultTimeout = 5 * time.Second

// Runner is the injectable subprocess runner. Tests can replace it.
type Runner = execwrap.Runner

// Bin is the binary name. Always "git"; exposed as a constant so that
// every call site uses the same literal.
const Bin = "git"

// Run invokes `git <args>` under the given cwd. Convenience over
// execwrap.Default; mirrors Python's `_run(["git", *args], cwd=...)`.
func Run(args []string, cwd string, timeout time.Duration) (rc int, stdout, stderr string, err error) {
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	full := make([]string, 0, len(args)+1)
	full = append(full, Bin)
	full = append(full, args...)
	return execwrap.Default(full, cwd, timeout)
}

// CurrentBranch returns the abbreviated name of the current branch
// (`git rev-parse --abbrev-ref HEAD`). Empty when git fails or the
// repo is in a detached state that prints just "HEAD".
func CurrentBranch(cwd string) (string, bool) {
	rc, out, _, _ := Run([]string{"rev-parse", "--abbrev-ref", "HEAD"}, cwd, 0)
	if rc != 0 {
		return "", false
	}
	s := strings.TrimSpace(out)
	if s == "" {
		return "", false
	}
	return s, true
}

// Upstream returns the upstream ref (`git rev-parse --abbrev-ref
// --symbolic-full-name @{u}`). False when no upstream is configured.
func Upstream(cwd string) (string, bool) {
	rc, out, _, _ := Run(
		[]string{"rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"},
		cwd, 0,
	)
	if rc != 0 {
		return "", false
	}
	s := strings.TrimSpace(out)
	if s == "" {
		return "", false
	}
	return s, true
}

// StatusPorcelain returns the raw `git status --porcelain` output.
// Mirrors Python's check for working-tree cleanness.
func StatusPorcelain(cwd string) (string, bool) {
	rc, out, _, _ := Run([]string{"status", "--porcelain"}, cwd, 0)
	if rc != 0 {
		return "", false
	}
	return out, true
}

// IsClean reports whether StatusPorcelain returned empty.
func IsClean(cwd string) (clean, ok bool) {
	out, ok := StatusPorcelain(cwd)
	if !ok {
		return false, false
	}
	return strings.TrimSpace(out) == "", true
}

// BranchExists is `git rev-parse --verify --quiet refs/heads/<branch>`.
func BranchExists(cwd, branch string) bool {
	rc, _, _, _ := Run([]string{"rev-parse", "--verify", "--quiet", "refs/heads/" + branch}, cwd, 0)
	return rc == 0
}

// RevParse returns the resolved sha of `ref` (or whatever `git
// rev-parse <ref>` prints, e.g. `branch:path` for a blob hash).
func RevParse(cwd, ref string) (string, bool) {
	rc, out, _, _ := Run([]string{"rev-parse", ref}, cwd, 0)
	if rc != 0 {
		return "", false
	}
	return strings.TrimSpace(out), true
}

// JSONLCommitsBetween returns commit SHAs in `rangeSpec` that touch
// the file at `relPath`. Mirrors `_jsonl_commits_between` in
// trunksync.py: `git log --no-merges --pretty=%H <range> -- <path>`.
// trunksync's 15 s timeout is the default here.
func JSONLCommitsBetween(cwd, rangeSpec, relPath string) []string {
	rc, out, _, _ := Run(
		[]string{"log", "--no-merges", "--pretty=%H", rangeSpec, "--", relPath},
		cwd, 15*time.Second,
	)
	if rc != 0 {
		return nil
	}
	var out2 []string
	for _, line := range strings.Split(out, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			out2 = append(out2, s)
		}
	}
	return out2
}

// Show returns the raw contents of `<ref>:<relPath>` via
// `git show <ref>:<path>`. Used by trunksync to read JSONL at a ref
// without checking out.
func Show(cwd, ref, relPath string) (string, bool) {
	rc, out, _, _ := Run([]string{"show", ref + ":" + relPath}, cwd, 15*time.Second)
	if rc != 0 {
		return "", false
	}
	return out, true
}

// MergeBase returns the merge-base sha of two refs (`git merge-base
// <a> <b>`). False on git failure or unrelated histories.
func MergeBase(cwd, a, b string) (string, bool) {
	rc, out, _, _ := Run([]string{"merge-base", a, b}, cwd, 0)
	if rc != 0 {
		return "", false
	}
	s := strings.TrimSpace(out)
	if s == "" {
		return "", false
	}
	return s, true
}

// DefaultRemoteBranch resolves the remote's default branch via
// `git symbolic-ref refs/remotes/<remote>/HEAD`, returning the
// short branch name (e.g. "main"). False when no such symbolic-ref
// is configured (common on minimally-configured fresh clones).
func DefaultRemoteBranch(cwd, remote string) (string, bool) {
	rc, out, _, _ := Run(
		[]string{"symbolic-ref", "--short", "refs/remotes/" + remote + "/HEAD"},
		cwd, 0,
	)
	if rc != 0 {
		return "", false
	}
	s := strings.TrimSpace(out)
	prefix := remote + "/"
	if strings.HasPrefix(s, prefix) {
		s = s[len(prefix):]
	}
	if s == "" {
		return "", false
	}
	return s, true
}

// RefExists is `git rev-parse --verify --quiet <ref>` — a generic
// check that works for any ref form (branch, remote-tracking, tag,
// sha). BranchExists is the local-branch-only variant.
func RefExists(cwd, ref string) bool {
	rc, _, _, _ := Run([]string{"rev-parse", "--verify", "--quiet", ref}, cwd, 0)
	return rc == 0
}

// Available reports whether the `git` binary is on PATH. Used by
// callers that want to surface a structured "git not installed"
// error instead of letting subprocess invocations fail opaquely.
func Available() bool {
	_, err := exec.LookPath(Bin)
	return err == nil
}
