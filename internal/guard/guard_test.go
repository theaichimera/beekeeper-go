package guard

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
)

func mkProject(t *testing.T, base, name string) bkproject.Project {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(dir)
	return bkproject.Project{Root: resolved}
}

// Mirrors test_clean_project_has_no_findings (Python).
func TestCleanProjectHasNoFindings(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	fakeRoot := filepath.Join(base, "FakeDropbox")
	_ = os.MkdirAll(fakeRoot, 0o755)
	p := mkProject(t, base, "outside")
	// DB outside fake sync root.
	_ = os.WriteFile(filepath.Join(p.BeadsDir(), "beads.db"), nil, 0o644)
	_, r := ScanPaths([]string{base}, 4, []string{fakeRoot})
	if !r.IsClean() {
		t.Fatalf("expected clean; got %+v", r)
	}
	if r.Worst() != GREEN {
		t.Fatalf("worst=%v want GREEN", r.Worst())
	}
}

// Mirrors test_db_inside_filesync_is_red.
func TestDBInsideFilesyncIsRed(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	fake := filepath.Join(base, "FakeDropbox")
	_ = os.MkdirAll(fake, 0o755)
	proj := filepath.Join(fake, "inside-project")
	_ = os.MkdirAll(filepath.Join(proj, ".beads"), 0o755)
	_ = os.WriteFile(filepath.Join(proj, ".beads", "beads.db"), nil, 0o644)
	_ = os.WriteFile(filepath.Join(proj, ".beads", "beads.db-wal"), nil, 0o644)
	_ = os.WriteFile(filepath.Join(proj, ".beads", "beads.db-shm"), nil, 0o644)
	_, r := ScanPaths([]string{fake}, 4, []string{fake})
	if r.Worst() != RED {
		t.Fatalf("worst=%v want RED", r.Worst())
	}
	sevs := map[Severity]int{}
	for _, f := range r.Findings {
		sevs[f.Severity]++
	}
	if sevs[RED] < 1 || sevs[YELLOW] < 1 {
		t.Fatalf("expected RED .db + YELLOW sidecars; got %v", sevs)
	}
	// Message includes the "rebuildable cache" remediation.
	for _, f := range r.Findings {
		if !strings.Contains(strings.ToLower(f.Remediation), "rebuildable cache") {
			t.Fatalf("remediation missing 'rebuildable cache': %q", f.Remediation)
		}
	}
}

// Mirrors test_plan_fix_refuses_while_daemon_alive.
func TestPlanFixRefusesWhileDaemonAlive(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	fake := filepath.Join(base, "FakeDropbox")
	_ = os.MkdirAll(fake, 0o755)
	proj := filepath.Join(fake, "with-live-daemon")
	_ = os.MkdirAll(filepath.Join(proj, ".beads"), 0o755)
	_ = os.WriteFile(filepath.Join(proj, ".beads", "beads.db"), nil, 0o644)
	_ = os.WriteFile(filepath.Join(proj, ".beads", "daemon.pid"), []byte(strconv.Itoa(syscall.Getpid())), 0o644)

	p := bkproject.Project{Root: proj}
	plan := PlanFix(p, filepath.Join(base, "dest"))
	if !plan.DaemonBlocking {
		t.Fatalf("DaemonBlocking=false; want true")
	}
	_, err := ApplyFix(plan, false)
	var fe *FixError
	if !errors.As(err, &fe) {
		t.Fatalf("expected FixError; got %v", err)
	}
	if !strings.Contains(err.Error(), "daemon") {
		t.Fatalf("err=%v", err)
	}
}

// Mirrors test_plan_fix_dry_run_does_not_move.
func TestPlanFixDryRunDoesNotMove(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	fake := filepath.Join(base, "FakeDropbox")
	_ = os.MkdirAll(fake, 0o755)
	proj := filepath.Join(fake, "no-daemon")
	_ = os.MkdirAll(filepath.Join(proj, ".beads"), 0o755)
	for _, n := range []string{"beads.db", "beads.db-wal", "beads.db-shm"} {
		_ = os.WriteFile(filepath.Join(proj, ".beads", n), []byte("x"), 0o644)
	}
	p := bkproject.Project{Root: proj}
	plan := PlanFix(p, filepath.Join(base, "safe"))
	if plan.DaemonBlocking {
		t.Fatalf("DaemonBlocking=true unexpectedly")
	}
	lines, err := ApplyFix(plan, true)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(proj, ".beads", "beads.db")); err != nil {
		t.Fatalf(".db missing after dry-run: %v", err)
	}
	dryFound := false
	for _, l := range lines {
		if strings.Contains(l, "DRY-RUN") {
			dryFound = true
		}
	}
	if !dryFound {
		t.Fatalf("expected DRY-RUN lines; got %v", lines)
	}
}

// Mirrors test_plan_fix_apply_moves_files.
func TestPlanFixApplyMovesFiles(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	fake := filepath.Join(base, "FakeDropbox")
	_ = os.MkdirAll(fake, 0o755)
	proj := filepath.Join(fake, "to-relocate")
	_ = os.MkdirAll(filepath.Join(proj, ".beads"), 0o755)
	for _, n := range []string{"beads.db", "beads.db-wal", "beads.db-shm"} {
		_ = os.WriteFile(filepath.Join(proj, ".beads", n), []byte("x"), 0o644)
	}
	p := bkproject.Project{Root: proj}
	dest := filepath.Join(base, "safe")
	plan := PlanFix(p, dest)
	if _, err := ApplyFix(plan, false); err != nil {
		t.Fatalf("ApplyFix err=%v", err)
	}
	for _, n := range []string{"beads.db", "beads.db-wal", "beads.db-shm"} {
		if _, err := os.Stat(filepath.Join(proj, ".beads", n)); err == nil {
			t.Fatalf("%s still in place", n)
		}
		if _, err := os.Stat(filepath.Join(dest, n)); err != nil {
			t.Fatalf("%s missing at dest: %v", n, err)
		}
	}
}

// Mirrors test_scan_paths_skips_heavy_dirs.
func TestScanPathsSkipsHeavyDirs(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	fake := filepath.Join(base, "FakeDropbox")
	// node_modules/pkg/.beads/x.db — should be skipped.
	nm := filepath.Join(fake, "myrepo", "node_modules", "pkg")
	_ = os.MkdirAll(filepath.Join(nm, ".beads"), 0o755)
	_ = os.WriteFile(filepath.Join(nm, ".beads", "x.db"), nil, 0o644)
	projs, r := ScanPaths([]string{fake}, 4, []string{fake})
	for _, p := range projs {
		if strings.Contains(p.Root, "node_modules") {
			t.Fatalf("node_modules walked: %v", p.Root)
		}
	}
	if !r.IsClean() {
		t.Fatalf("expected clean (heavy dir skipped); got %+v", r)
	}
}
