package drift

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	execwrap "github.com/theaichimera/beekeeper-go/internal/exec"
	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
)

// stubBdConfigGetEmpty makes `bd config get sync.branch` return rc=1
// so ReadGitState falls back to .beads/config.json. Mirrors the
// pattern other tests use to keep bd off /tmp synthetic repos.
func stubBdConfigGetEmpty(t *testing.T) {
	t.Helper()
	orig := bkproject.BdRunner
	bkproject.BdRunner = func(args []string, cwd string, timeout time.Duration) (int, string, string, error) {
		if len(args) >= 3 && args[0] == "bd" && args[1] == "config" && args[2] == "get" {
			return 1, "", "(forced jsonl fallback)", nil
		}
		return execwrap.Default(args, cwd, timeout)
	}
	t.Cleanup(func() { bkproject.BdRunner = orig })
}

// initDriftRepo builds a synthetic git repo with a .beads/ workspace.
// The caller controls how many commits the branch is "behind" base
// by adding extra commits to base AFTER cutting the branch.
func initDriftRepo(t *testing.T, withSyncBranch bool) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	must := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".beads", "issues.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if withSyncBranch {
		if err := os.WriteFile(
			filepath.Join(dir, ".beads", "config.json"),
			[]byte(`{"sync":{"branch":"beads-sync"}}`),
			0o644,
		); err != nil {
			t.Fatal(err)
		}
	}
	must("init", "-q", "-b", "main")
	must("config", "user.email", "t@t")
	must("config", "user.name", "t")
	must("add", ".beads")
	must("commit", "-q", "-m", "init")
	return dir
}

func gitDo(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// addCommit adds one new commit to the current branch. Used to make
// `main` advance after a feature branch was cut from it.
func addCommit(t *testing.T, dir, file, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitDo(t, dir, "add", file)
	gitDo(t, dir, "commit", "-q", "-m", "advance "+file)
}

// ----- behind-base tests -------------------------------------------------

func TestScanCleanRepoNoFindings(t *testing.T) {
	stubBdConfigGetEmpty(t)
	dir := initDriftRepo(t, true)
	r, err := Scan(Opts{Repo: dir})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, f := range r.Findings {
		if f.Severity != GREEN {
			t.Fatalf("clean repo got finding: %+v", f)
		}
	}
	if r.Worst() != GREEN {
		t.Fatalf("worst=%s want GREEN; findings=%+v", r.Worst(), r.Findings)
	}
}

func TestScanBranchBehindBaseTrips(t *testing.T) {
	stubBdConfigGetEmpty(t)
	dir := initDriftRepo(t, true)
	gitDo(t, dir, "checkout", "-q", "-b", "feature/stale")
	gitDo(t, dir, "checkout", "-q", "main")
	// Advance main with N commits so feature/stale is N behind.
	for i := 0; i < 3; i++ {
		addCommit(t, dir, "f"+string(rune('a'+i))+".txt", "x")
	}
	gitDo(t, dir, "checkout", "-q", "feature/stale")

	// Threshold 2 -> 3 behind trips it.
	r, _ := Scan(Opts{Repo: dir, Base: "main", BehindThreshold: 2})
	if r.BehindCount != 3 {
		t.Fatalf("BehindCount=%d want 3", r.BehindCount)
	}
	hit := false
	for _, f := range r.Findings {
		if f.Kind == KindBehindBase && f.BehindCount == 3 && f.Branch == "feature/stale" {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("expected behind-base finding; got %+v", r.Findings)
	}

	// Threshold 5 -> 3 behind passes.
	r2, _ := Scan(Opts{Repo: dir, Base: "main", BehindThreshold: 5})
	for _, f := range r2.Findings {
		if f.Kind == KindBehindBase {
			t.Fatalf("threshold 5 should not trip on 3-behind: %+v", f)
		}
	}
}

func TestScanMissingSyncBranchTrips(t *testing.T) {
	stubBdConfigGetEmpty(t)
	dir := initDriftRepo(t, false) // NO sync.branch
	r, _ := Scan(Opts{Repo: dir, Base: "main"})
	hit := false
	for _, f := range r.Findings {
		if f.Kind == KindMissingSyncBranch {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("expected missing-sync-branch finding; got %+v", r.Findings)
	}
}

func TestScanNeedsManualSyncIsRed(t *testing.T) {
	stubBdConfigGetEmpty(t)
	dir := initDriftRepo(t, true)
	// Plant a sync-state.json the doctor / drift code reads.
	state := map[string]any{"needs_manual_sync": true}
	b, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(dir, ".beads", "sync-state.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	r, _ := Scan(Opts{Repo: dir, Base: "main"})
	red := false
	for _, f := range r.Findings {
		if f.Kind == KindNeedsManualSync && f.Severity == RED {
			red = true
		}
	}
	if !red {
		t.Fatalf("expected RED needs-manual-sync finding; got %+v", r.Findings)
	}
	if r.Worst() != RED {
		t.Fatalf("worst=%s want RED", r.Worst())
	}
}

func TestExitCodeMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		worst  Severity
		refuse bool
		want   int
	}{
		{GREEN, false, 0},
		{GREEN, true, 0},
		{YELLOW, false, 1},
		{YELLOW, true, 2},
		{RED, false, 2},
		{RED, true, 2},
	}
	for _, tc := range cases {
		got := ExitCode(tc.worst, tc.refuse)
		if got != tc.want {
			t.Fatalf("ExitCode(%s, refuse=%v)=%d want %d", tc.worst, tc.refuse, got, tc.want)
		}
	}
}

// TestScanIsCheap is a smoke test that the scan does not invoke
// `bd list --json` (the heavy pr-beads / authoritative-status path).
// We snoop BdRunner: any `bd list` call would have the test fail.
func TestScanIsCheap(t *testing.T) {
	dir := initDriftRepo(t, true)
	calls := 0
	orig := bkproject.BdRunner
	bkproject.BdRunner = func(args []string, cwd string, timeout time.Duration) (int, string, string, error) {
		calls++
		if len(args) >= 2 && args[0] == "bd" && args[1] == "list" {
			t.Fatalf("drift Scan invoked `bd list` — heavy path; args=%v", args)
		}
		// Make `bd config get` cheap fall-through.
		if len(args) >= 3 && args[0] == "bd" && args[1] == "config" && args[2] == "get" {
			return 1, "", "(stub)", nil
		}
		return execwrap.Default(args, cwd, timeout)
	}
	t.Cleanup(func() { bkproject.BdRunner = orig })

	if _, err := Scan(Opts{Repo: dir, Base: "main"}); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	// Cap on bd shell-outs. ReadGitState is one (sync.branch). That's
	// the only one we expect; allow a small headroom for project init.
	if calls > 3 {
		t.Fatalf("scan made %d bd shell-outs; expected <=3", calls)
	}
}
