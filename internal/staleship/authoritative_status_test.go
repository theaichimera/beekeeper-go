package staleship

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	execwrap "github.com/theaichimera/beekeeper-go/internal/exec"
	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
)

// stubBd installs a BdRunner that returns the supplied stdout for
// `bd list --json` calls and falls through to the real runner for
// every other invocation. Tests use it to simulate the bd SQLite DB
// ahead of the on-disk JSONL.
func stubBd(t *testing.T, stdout string) {
	t.Helper()
	orig := bkproject.BdRunner
	bkproject.BdRunner = func(args []string, cwd string, timeout time.Duration) (int, string, string, error) {
		if len(args) >= 2 && args[0] == "bd" && args[1] == "list" {
			return 0, stdout, "", nil
		}
		return execwrap.Default(args, cwd, timeout)
	}
	t.Cleanup(func() { bkproject.BdRunner = orig })
}

// stubBdFail installs a BdRunner that returns rc=1 for `bd list
// --json` calls — simulates bd unavailable / not initialized so the
// auto-merge falls through to JSONL only.
func stubBdFail(t *testing.T) {
	t.Helper()
	orig := bkproject.BdRunner
	bkproject.BdRunner = func(args []string, cwd string, timeout time.Duration) (int, string, string, error) {
		if len(args) >= 2 && args[0] == "bd" && args[1] == "list" {
			return 1, "", "(stubbed bd error)", nil
		}
		return execwrap.Default(args, cwd, timeout)
	}
	t.Cleanup(func() { bkproject.BdRunner = orig })
}

// TestAuthoritativeStatusBdAheadOfJSONL is the bkg-td0.2 regression
// fixture. Reproduces the dogfood incident: `bd close demo-x` flushed
// the SQLite DB to status=closed, but `.beads/issues.jsonl` still
// said status=open (async flush lag). The guard MUST trust bd and
// NOT re-flag demo-x as shipped-not-closed.
func TestAuthoritativeStatusBdAheadOfJSONL(t *testing.T) {
	// JSONL says open (the lag) — bd says closed (the truth).
	stubBd(t, `[{"id":"demo-x","status":"closed"}]`)
	recs := []map[string]any{
		{"id": "demo-x", "status": "open"}, // stale JSONL
	}
	subjects := []string{
		"feat(demo-x): land it (#42)",
	}
	dir := initRepoWithJSONL(t, recs, subjects)
	r, err := Diagnose(dir, "main", "demo", 0)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if len(r.Findings) != 0 {
		t.Fatalf("FALSE POSITIVE: bd says closed but guard re-flagged demo-x: %+v", r.Findings)
	}
}

// Confirms the merge respects bd's "in_progress" view too — if bd
// says in_progress and JSONL is stale (closed), the bead IS still
// open from the guard's perspective. Symmetric proof of bd
// authoritativeness in the other direction.
func TestAuthoritativeStatusBdShowsInProgressJSONLClosed(t *testing.T) {
	stubBd(t, `[{"id":"demo-x","status":"in_progress"}]`)
	recs := []map[string]any{
		{"id": "demo-x", "status": "closed"}, // JSONL stale-closed
	}
	subjects := []string{
		"feat(demo-x): land it (#42)",
	}
	dir := initRepoWithJSONL(t, recs, subjects)
	r, _ := Diagnose(dir, "main", "demo", 0)
	if len(r.Findings) != 1 || r.Findings[0].BeadID != "demo-x" {
		t.Fatalf("bd in_progress should override JSONL closed; got %+v", r.Findings)
	}
}

// JSONL covers ids bd hasn't ingested (e.g. fresh clone, no DB).
// When bd reports [], the guard MUST still see ids only present in
// the JSONL.
func TestAuthoritativeStatusBdEmptyFallsThroughToJSONL(t *testing.T) {
	stubBd(t, `[]`)
	recs := []map[string]any{
		{"id": "demo-x", "status": "in_progress"},
	}
	subjects := []string{
		"feat(demo-x): land it (#42)",
	}
	dir := initRepoWithJSONL(t, recs, subjects)
	r, _ := Diagnose(dir, "main", "demo", 0)
	if len(r.Findings) != 1 {
		t.Fatalf("bd-empty should fall through to JSONL; got %+v", r.Findings)
	}
}

// StatusSourceJSONL preserves legacy bkg-bqa.3 behavior verbatim:
// bd is bypassed, only the on-disk JSONL is read. This is the
// escape-hatch for repos where bd isn't reachable from the runner
// (or for the bk board parity context noted in bkg-td0.2).
func TestStatusSourceJSONLBypassesBd(t *testing.T) {
	stubBd(t, `[{"id":"demo-x","status":"closed"}]`) // would override under auto
	recs := []map[string]any{
		{"id": "demo-x", "status": "in_progress"},
	}
	subjects := []string{
		"feat(demo-x): land it (#42)",
	}
	dir := initRepoWithJSONL(t, recs, subjects)
	r, _ := DiagnoseWithOpts(dir, "main", "demo", Opts{
		StatusSource: StatusSourceJSONL,
	})
	if len(r.Findings) != 1 {
		t.Fatalf("StatusSourceJSONL should ignore bd; got %+v", r.Findings)
	}
}

// StatusSourceBd is the strict mode: errors when bd is unavailable.
// Useful for CI environments that want to hard-fail rather than
// silently fall through to a stale JSONL.
func TestStatusSourceBdErrorsWhenBdUnavailable(t *testing.T) {
	stubBdFail(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".beads", "issues.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "init", "-q", "-b", "main")
	mustGit(t, dir, "config", "user.email", "t@t")
	mustGit(t, dir, "config", "user.name", "t")
	mustGit(t, dir, "add", ".beads/issues.jsonl")
	mustGit(t, dir, "commit", "-q", "-m", "init")

	_, err := DiagnoseWithOpts(dir, "main", "demo", Opts{
		StatusSource: StatusSourceBd,
	})
	if err == nil || !strings.Contains(err.Error(), "bd") {
		t.Fatalf("expected bd-unavailable error; got %v", err)
	}
}
