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
