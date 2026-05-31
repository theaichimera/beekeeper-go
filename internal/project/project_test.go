package project

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"
	"testing"
	"time"

	execwrap "github.com/theaichimera/beekeeper-go/internal/exec"
)

func mkBeads(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(dir, BeadsDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// forceConfigJSONFallback installs a bd runner that always returns
// rc=1 — mirrors Python's `_force_config_json_fallback` fixture so
// `bd config get` doesn't pollute synthetic workspaces.
func forceConfigJSONFallback(t *testing.T) {
	t.Helper()
	orig := BdRunner
	BdRunner = func(args []string, cwd string, _ time.Duration) (int, string, string, error) {
		if len(args) >= 3 && args[0] == "bd" && args[1] == "config" && args[2] == "get" {
			return 1, "", "(forced fallback in tests)", nil
		}
		return execwrap.Default(args, cwd, 10*time.Second)
	}
	t.Cleanup(func() { BdRunner = orig })
}

func writeJSON(t *testing.T, path string, m any) {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindProjectsDiscoversNested(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	for _, name := range []string{"a", "b", "node_modules/c"} {
		if err := os.MkdirAll(filepath.Join(tmp, name, BeadsDirName), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := FindProjects([]string{tmp}, 4)
	roots := make([]string, 0, len(got))
	for _, p := range got {
		roots = append(roots, p.Root)
	}
	sort.Strings(roots)

	resolved := func(rel string) string {
		r, _ := filepath.EvalSymlinks(filepath.Join(tmp, rel))
		return r
	}
	want := []string{resolved("a"), resolved("b")}
	// `node_modules/c` should be skipped.
	if len(roots) != len(want) {
		t.Fatalf("roots=%v want %v", roots, want)
	}
	for i := range roots {
		if roots[i] != want[i] {
			t.Fatalf("roots=%v want %v", roots, want)
		}
	}
}

func TestFindProjectsHonorsMaxDepth(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	deep := filepath.Join(tmp, "x/y/z/q")
	if err := os.MkdirAll(filepath.Join(deep, BeadsDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindProjects([]string{tmp}, 2); len(got) != 0 {
		t.Fatalf("depth=2 should miss .../z/q/.beads: %v", got)
	}
	if got := FindProjects([]string{tmp}, 5); len(got) != 1 {
		t.Fatalf("depth=5 should find one project: %v", got)
	}
}

func TestDiscoverDBPaths(t *testing.T) {
	t.Parallel()
	dir := mkBeads(t, "dbs")
	beads := filepath.Join(dir, BeadsDirName)
	for _, n := range []string{"beads.db", "beads.db-wal", "beads.db-shm", "issues.jsonl", "ignore.txt"} {
		if err := os.WriteFile(filepath.Join(beads, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := DiscoverDBPaths(Project{Root: dir})
	want := []string{
		filepath.Join(beads, "beads.db"),
		filepath.Join(beads, "beads.db-shm"),
		filepath.Join(beads, "beads.db-wal"),
	}
	if len(got) != len(want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got[%d]=%q want %q", i, got[i], want[i])
		}
	}
}

func TestReadDaemonStateNoPidNoState(t *testing.T) {
	t.Parallel()
	dir := mkBeads(t, "no-pid")
	got := ReadDaemonState(Project{Root: dir})
	if got.PIDPresent {
		t.Fatalf("PIDPresent=%v want false", got.PIDPresent)
	}
	if got.PIDAlive {
		t.Fatalf("PIDAlive=%v want false", got.PIDAlive)
	}
	if got.StatePath != "" {
		t.Fatalf("StatePath=%q want empty", got.StatePath)
	}
}

func TestReadDaemonStateStalePID(t *testing.T) {
	t.Parallel()
	dir := mkBeads(t, "stale")
	if err := os.WriteFile(filepath.Join(dir, BeadsDirName, "daemon.pid"), []byte("999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ReadDaemonState(Project{Root: dir})
	if !got.PIDPresent || got.PID != 999999 {
		t.Fatalf("PIDPresent=%v PID=%d", got.PIDPresent, got.PID)
	}
	if got.PIDAlive {
		t.Skip("PID 999999 happened to exist on host; cannot test stale-pid")
	}
}

func TestReadDaemonStateAlivePID(t *testing.T) {
	t.Parallel()
	dir := mkBeads(t, "alive")
	mypid := syscall.Getpid()
	if err := os.WriteFile(filepath.Join(dir, BeadsDirName, "daemon.pid"), []byte(strconv.Itoa(mypid)), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ReadDaemonState(Project{Root: dir})
	if !got.PIDPresent || !got.PIDAlive {
		t.Fatalf("PIDPresent=%v PIDAlive=%v want both true", got.PIDPresent, got.PIDAlive)
	}
}

func TestReadDaemonStateLoadsSyncStateJSON(t *testing.T) {
	t.Parallel()
	dir := mkBeads(t, "needs-manual")
	writeJSON(t, filepath.Join(dir, BeadsDirName, "sync-state.json"), map[string]any{
		"needs_manual_sync":    true,
		"consecutive_failures": 3,
	})
	got := ReadDaemonState(Project{Root: dir})
	if got.StatePath == "" {
		t.Fatal("StatePath empty")
	}
	if got.LastSyncState["needs_manual_sync"] != true {
		t.Fatalf("needs_manual_sync=%v", got.LastSyncState)
	}
}

func TestReadGitStateFallsBackToConfigJSON(t *testing.T) {
	forceConfigJSONFallback(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := mkBeads(t, "git-state")
	cmd := exec.Command("git", "init", "-q", "-b", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	// Set local identity so any future commits don't need host config.
	for _, args := range [][]string{
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// An empty-init repo has no HEAD yet; git rev-parse --abbrev-ref
	// HEAD returns rc=128. Make one commit so the branch is real.
	commit := exec.Command("git", "commit", "--allow-empty", "-q", "-m", "init")
	commit.Dir = dir
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	// Drop a config.json for sync.branch.
	writeJSON(t, filepath.Join(dir, BeadsDirName, "config.json"), map[string]any{
		"sync": map[string]any{"branch": "beads-sync"},
	})
	got := ReadGitState(Project{Root: dir})
	if got.Branch != "main" {
		t.Fatalf("Branch=%q want main", got.Branch)
	}
	if got.SyncBranch != "beads-sync" {
		t.Fatalf("SyncBranch=%q want beads-sync", got.SyncBranch)
	}
	// The untracked config.json we just wrote leaves the working
	// tree non-clean, which matches the Python tool's behavior.
	// What matters is that IsClean was POPULATED (not nil), which
	// proves `git status --porcelain` ran cleanly.
	if got.IsClean == nil {
		t.Fatal("IsClean=nil; expected a populated bool")
	}
}
