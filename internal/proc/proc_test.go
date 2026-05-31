package proc

import (
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"testing"
)

func readFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(data)
}

// withCwdLookup installs a deterministic cwd lookup for the duration
// of one test.
func withCwdLookup(t *testing.T, m map[int]string) {
	t.Helper()
	orig := CwdLookup
	CwdLookup = func(pid int) string {
		return m[pid]
	}
	t.Cleanup(func() { CwdLookup = orig })
}

func TestParsePsDarwinFixture(t *testing.T) {
	// Tests in this file mutate the package-level CwdLookup; do not
	// run them in parallel.
	withCwdLookup(t, map[int]string{
		12345: "/Users/x/work/proj-fallback",
	})
	got := ParsePs(readFixture(t, "ps_darwin.txt"))
	pids := pidsOf(got)
	sort.Ints(pids)
	want := []int{12345, 54321, 67890}
	if !equalInts(pids, want) {
		t.Fatalf("daemon pids=%v want %v", pids, want)
	}

	byPID := map[int]DaemonProcess{}
	for _, p := range got {
		byPID[p.PID] = p
	}
	if w := byPID[67890].Workspace; w != "/Users/x/work/proj-a" {
		t.Errorf("--workspace= flag: ws=%q want /Users/x/work/proj-a", w)
	}
	if w := byPID[54321].Workspace; w != "/Users/x/work/proj-b" {
		t.Errorf("--workspace <space> flag: ws=%q want /Users/x/work/proj-b", w)
	}
	if w := byPID[12345].Workspace; w != "/Users/x/work/proj-fallback" {
		t.Errorf("cwd fallback: ws=%q want /Users/x/work/proj-fallback", w)
	}
}

func TestParsePsLinuxFixture(t *testing.T) {
	// Tests in this file mutate the package-level CwdLookup; do not
	// run them in parallel.
	withCwdLookup(t, map[int]string{
		12345: "/home/x/work/proj-cwd",
	})
	got := ParsePs(readFixture(t, "ps_linux.txt"))
	pids := pidsOf(got)
	sort.Ints(pids)
	want := []int{12345, 54321, 67890}
	if !equalInts(pids, want) {
		t.Fatalf("daemon pids=%v want %v", pids, want)
	}
	byPID := map[int]DaemonProcess{}
	for _, p := range got {
		byPID[p.PID] = p
	}
	if byPID[67890].Workspace != "/home/x/work/proj-a" {
		t.Errorf("--workspace= linux: %q", byPID[67890].Workspace)
	}
	if byPID[54321].Workspace != "/home/x/work/proj-b" {
		t.Errorf("--workspace <space> linux: %q", byPID[54321].Workspace)
	}
	if byPID[12345].Workspace != "/home/x/work/proj-cwd" {
		t.Errorf("cwd lookup linux: %q", byPID[12345].Workspace)
	}
}

func TestParsePsSkipsNonBdLines(t *testing.T) {
	// Tests in this file mutate the package-level CwdLookup; do not
	// run them in parallel.
	withCwdLookup(t, map[int]string{})
	// Lines that match neither the (^|/)bd(\s|$) nor \bdaemon\b
	// portion of the contract: kernel processes, bd binary
	// without a subcommand, embedded "bd_helper" (no \s after bd).
	in := `  10 /sbin/launchd
  11 /usr/bin/python3 -m bd_helper daemon
  12 /usr/local/bin/bd
`
	got := ParsePs(in)
	if len(got) != 0 {
		t.Fatalf("expected zero daemons, got %v", got)
	}
}

func TestParsePsHandlesEmptyAndMalformed(t *testing.T) {
	// Tests in this file mutate the package-level CwdLookup; do not
	// run them in parallel.
	withCwdLookup(t, map[int]string{})
	if got := ParsePs(""); len(got) != 0 {
		t.Fatalf("empty input: %v", got)
	}
	in := `
not-a-pid /usr/local/bin/bd daemon
NaN bd daemon
12345 bd daemon
`
	got := ParsePs(in)
	if len(got) != 1 || got[0].PID != 12345 {
		t.Fatalf("expected only pid=12345; got %v", got)
	}
}

func TestPidAlive(t *testing.T) {
	mypid := syscall.Getpid()
	if !PidAlive(mypid) {
		t.Fatalf("self pid %d should be alive", mypid)
	}
	if PidAlive(-1) {
		t.Fatal("PidAlive(-1) should be false")
	}
	// PID 999999 is almost certainly unused on any test runner.
	if PidAlive(999999) {
		t.Skip("PID 999999 happened to exist on this host; cannot test stale-pid path")
	}
}

func TestExtractWorkspaceLastResortStripsBdBinary(t *testing.T) {
	// Tests in this file mutate the package-level CwdLookup; do not
	// run them in parallel.
	withCwdLookup(t, map[int]string{})
	// no --workspace, no cwd lookup, no path-like arg besides the bd binary
	got := ParsePs(`1 /usr/local/bin/bd daemon`)
	if len(got) != 1 || got[0].Workspace != "" {
		t.Fatalf("expected empty workspace (only /usr/local/bin/bd path token); got %+v", got)
	}
	// Now add a real workspace token after daemon:
	got = ParsePs(`1 /usr/local/bin/bd daemon /Users/x/work/abc`)
	if len(got) != 1 || got[0].Workspace != "/Users/x/work/abc" {
		t.Fatalf("expected /Users/x/work/abc; got %+v", got)
	}
}

// ---- helpers ----

func pidsOf(ps []DaemonProcess) []int {
	out := make([]int, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.PID)
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
