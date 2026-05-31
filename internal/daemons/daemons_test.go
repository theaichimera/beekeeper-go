package daemons

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/theaichimera/beekeeper-go/internal/proc"
	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
)

func mkProject(t *testing.T, name string) bkproject.Project {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(dir)
	return bkproject.Project{Root: resolved}
}

func withEnumerator(t *testing.T, ds []proc.DaemonProcess) {
	t.Helper()
	orig := Enumerator
	Enumerator = func() []proc.DaemonProcess { return ds }
	t.Cleanup(func() { Enumerator = orig })
}

func writeFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDuplicateDaemonsRed(t *testing.T) {
	p := mkProject(t, "dups")
	withEnumerator(t, []proc.DaemonProcess{
		{PID: 111, Workspace: p.Root},
		{PID: 222, Workspace: p.Root},
	})
	got := DiagnoseProject(p)
	if len(got) != 1 || got[0].Kind != "duplicate-daemons" || got[0].Severity != RED {
		t.Fatalf("findings=%+v", got)
	}
	if got[0].PIDs[0] != 111 || got[0].PIDs[1] != 222 {
		t.Fatalf("pids=%v want sorted [111,222]", got[0].PIDs)
	}
}

func TestSingleHealthyNoLog_IsClean(t *testing.T) {
	p := mkProject(t, "healthy")
	withEnumerator(t, []proc.DaemonProcess{{PID: 111, Workspace: p.Root}})
	got := DiagnoseProject(p)
	if len(got) != 0 {
		t.Fatalf("expected zero findings, got %+v", got)
	}
}

func TestRemoteHelperFailuresRed(t *testing.T) {
	p := mkProject(t, "helper-failures")
	writeFile(t, filepath.Join(p.BeadsDir(), "daemon.log"), []byte(
		"time=t1 ERROR remote: Repository not found.\n"+
			"time=t2 ERROR fatal: could not find git remote helper for https\n"+
			"time=t3 ERROR Authentication failed for 'https://github.com/...'\n"))
	withEnumerator(t, []proc.DaemonProcess{{PID: 222, Workspace: p.Root}})
	got := DiagnoseProject(p)
	if len(got) == 0 {
		t.Fatalf("expected at least one finding")
	}
	found := false
	for _, f := range got {
		if f.Kind == "remote-helper-failure" && f.Severity == RED {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected remote-helper-failure RED; got %+v", got)
	}
}

func TestRemoteHelperBelowThresholdIsSilent(t *testing.T) {
	p := mkProject(t, "helper-onefail")
	writeFile(t, filepath.Join(p.BeadsDir(), "daemon.log"), []byte(
		"time=t1 ERROR remote: Repository not found.\n",
	))
	withEnumerator(t, []proc.DaemonProcess{{PID: 333, Workspace: p.Root}})
	got := DiagnoseProject(p)
	for _, f := range got {
		if f.Kind == "remote-helper-failure" {
			t.Fatalf("1 failure < threshold(%d); should not flag", RemoteHelperThreshold)
		}
	}
}

func TestOrphanStateYellow(t *testing.T) {
	p := mkProject(t, "orphan")
	writeFile(t, filepath.Join(p.BeadsDir(), "sync-state.json"),
		[]byte(`{"needs_manual_sync":false}`))
	withEnumerator(t, []proc.DaemonProcess{})
	got := DiagnoseProject(p)
	if len(got) != 1 || got[0].Kind != "orphan-state" || got[0].Severity != YELLOW {
		t.Fatalf("findings=%+v", got)
	}
}

func TestNoOrphanWhenPidFilePresent(t *testing.T) {
	p := mkProject(t, "pidfile-present")
	writeFile(t, filepath.Join(p.BeadsDir(), "sync-state.json"),
		[]byte(`{"needs_manual_sync":false}`))
	writeFile(t, filepath.Join(p.BeadsDir(), "daemon.pid"), []byte("99999"))
	withEnumerator(t, []proc.DaemonProcess{})
	got := DiagnoseProject(p)
	for _, f := range got {
		if f.Kind == "orphan-state" {
			t.Fatalf("should not flag orphan when daemon.pid exists; got %+v", got)
		}
	}
}

func TestCountRemoteHelperFailures(t *testing.T) {
	p := mkProject(t, "count")
	log := filepath.Join(p.BeadsDir(), "daemon.log")
	writeFile(t, log, []byte(
		"INFO ok\n"+
			"ERROR remote: Repository not found.\n"+
			"INFO ok\n"+
			"ERROR Could not find git remote helper\n"+
			"ERROR Permission denied (publickey)\n"+
			"WARN something\n"))
	if got := CountRemoteHelperFailures(log); got != 3 {
		t.Fatalf("got=%d want 3", got)
	}
}

func TestCountRemoteHelperFailuresMissing(t *testing.T) {
	p := mkProject(t, "no-log")
	if got := CountRemoteHelperFailures(filepath.Join(p.BeadsDir(), "missing.log")); got != 0 {
		t.Fatalf("got=%d want 0", got)
	}
}

func TestScanAggregates(t *testing.T) {
	// Two projects: one with duplicates, one clean.
	root := t.TempDir()
	dup := filepath.Join(root, "dup")
	clean := filepath.Join(root, "clean")
	for _, d := range []string{dup, clean} {
		if err := os.MkdirAll(filepath.Join(d, ".beads"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dupResolved, _ := filepath.EvalSymlinks(dup)
	cleanResolved, _ := filepath.EvalSymlinks(clean)
	withEnumerator(t, []proc.DaemonProcess{
		{PID: 1, Workspace: dupResolved},
		{PID: 2, Workspace: dupResolved},
		{PID: 3, Workspace: cleanResolved},
	})
	r := Scan([]string{root}, 4)
	if r.Worst() != RED {
		t.Fatalf("worst=%v want RED", r.Worst())
	}
	kinds := map[string]int{}
	for _, f := range r.Findings {
		kinds[f.Kind]++
	}
	if kinds["duplicate-daemons"] != 1 {
		t.Fatalf("expected one duplicate-daemons finding; got %v", kinds)
	}
}
