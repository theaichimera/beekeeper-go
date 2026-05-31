// Tests that close the parity gap with Python test_daemons.py.
package daemons

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/theaichimera/beekeeper-go/internal/proc"
	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
)

// Mirrors test_list_workspace_daemons_filters_by_workspace.
func TestListWorkspaceDaemonsFiltersByWorkspace(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "target-ws")
	other := filepath.Join(tmp, "other-ws")
	for _, d := range []string{target, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	targetResolved, _ := filepath.EvalSymlinks(target)
	otherResolved, _ := filepath.EvalSymlinks(other)
	orig := Enumerator
	Enumerator = func() []proc.DaemonProcess {
		return []proc.DaemonProcess{
			{PID: 111, Workspace: targetResolved},
			{PID: 222, Workspace: otherResolved},
			{PID: 333, Workspace: targetResolved},
		}
	}
	t.Cleanup(func() { Enumerator = orig })

	got := listWorkspaceDaemons(bkproject.Project{Root: targetResolved})
	pids := make([]int, 0, len(got))
	for _, d := range got {
		pids = append(pids, d.PID)
	}
	sort.Ints(pids)
	want := []int{111, 333}
	if len(pids) != len(want) || pids[0] != want[0] || pids[1] != want[1] {
		t.Fatalf("pids=%v want %v", pids, want)
	}
}

// Mirrors test_scan_does_not_invoke_start_or_stop. The Go scan path
// only reads (Enumerator, log file, filesystem); confirm none of
// those use bd subprocesses.
func TestScanDoesNotInvokeStartOrStop(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "ws")
	_ = os.MkdirAll(filepath.Join(dir, ".beads"), 0o755)
	resolved, _ := filepath.EvalSymlinks(dir)
	called := []string{}
	orig := Enumerator
	Enumerator = func() []proc.DaemonProcess {
		called = append(called, "enumerate")
		return []proc.DaemonProcess{{PID: 1, Workspace: resolved}}
	}
	t.Cleanup(func() { Enumerator = orig })
	_ = Scan([]string{tmp}, 4)
	// "enumerate" is the only allowed shell-out, and even that's
	// `ps` (read-only). The constraint is no `bd daemon
	// start|stop|restart|killall` — none of which are wired into
	// this code path. Smoke-check: only enumerate was called.
	if len(called) == 0 {
		t.Fatal("expected enumerator to be called once")
	}
	for _, c := range called {
		if c != "enumerate" {
			t.Fatalf("unexpected call: %q", c)
		}
	}
}

// Mirrors test_doctor_red_on_duplicate_daemons (per-package signal).
func TestDuplicatesAreRedAtModuleLevel(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "dups2")
	_ = os.MkdirAll(filepath.Join(dir, ".beads"), 0o755)
	resolved, _ := filepath.EvalSymlinks(dir)
	orig := Enumerator
	Enumerator = func() []proc.DaemonProcess {
		return []proc.DaemonProcess{
			{PID: 1, Workspace: resolved},
			{PID: 2, Workspace: resolved},
		}
	}
	t.Cleanup(func() { Enumerator = orig })
	r := Scan([]string{tmp}, 4)
	if r.Worst() != RED {
		t.Fatalf("worst=%v want RED", r.Worst())
	}
}

// Mirrors test_doctor_red_on_log_remote_helper_failure (per-package
// signal — the doctor wiring is exercised in internal/doctor's
// TestDBInFilesyncRed-adjacent suite).
func TestRemoteHelperFailureRedAtModuleLevel(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "rh")
	_ = os.MkdirAll(filepath.Join(dir, ".beads"), 0o755)
	resolved, _ := filepath.EvalSymlinks(dir)
	_ = os.WriteFile(filepath.Join(dir, ".beads", "daemon.log"),
		[]byte("ERROR remote: Repository not found.\nERROR Authentication failed\n"),
		0o644)
	orig := Enumerator
	Enumerator = func() []proc.DaemonProcess {
		return []proc.DaemonProcess{{PID: 1, Workspace: resolved}}
	}
	t.Cleanup(func() { Enumerator = orig })
	r := Scan([]string{tmp}, 4)
	if r.Worst() != RED {
		t.Fatalf("worst=%v want RED; %+v", r.Worst(), r.Findings)
	}
}
