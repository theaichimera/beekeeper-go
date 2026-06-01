package project

import (
	"time"

	execwrap "github.com/theaichimera/beekeeper-go/internal/exec"
)

// BdRunner is the injectable subprocess runner used for `bd ...`
// calls. Tests replace this to keep bd out of synthetic workspaces.
// Mirrors the role of project._run in Python tests (monkey-patched
// via `_force_config_json_fallback` fixtures).
var BdRunner execwrap.Runner = execwrap.Default

// bdRun is the internal helper that all bd shell-outs route through.
// 10 s timeout mirrors Python's `bd config get sync.branch` call site.
func bdRun(args []string, cwd string) (int, string, string, error) {
	return BdRunner(args, cwd, 10*time.Second)
}

// BdRun is the exported variant for callers in other packages. The
// timeout is bumped to 30 s — `bd list --json` on a large backlog
// (627 records observed) is heavier than `bd config get`.
func BdRun(args []string, cwd string, timeout time.Duration) (int, string, string, error) {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return BdRunner(args, cwd, timeout)
}
