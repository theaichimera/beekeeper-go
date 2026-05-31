package doctor

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	execwrap "github.com/theaichimera/beekeeper-go/internal/exec"
	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
)

// forceConfigJSONFallback keeps `bd config get` out of synthetic workspaces.
func forceConfigJSONFallback(t *testing.T) {
	t.Helper()
	orig := bkproject.BdRunner
	bkproject.BdRunner = func(args []string, cwd string, _ time.Duration) (int, string, string, error) {
		if len(args) >= 3 && args[0] == "bd" && args[1] == "config" && args[2] == "get" {
			return 1, "", "(forced fallback)", nil
		}
		return execwrap.Default(args, cwd, 10*time.Second)
	}
	t.Cleanup(func() { bkproject.BdRunner = orig })
}

func initGitRepo(t *testing.T, dir, syncBranch string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	must(t, os.MkdirAll(filepath.Join(dir, ".beads"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, ".beads", "issues.jsonl"), nil, 0o644))
	if syncBranch != "" {
		b, _ := json.Marshal(map[string]any{"sync": map[string]any{"branch": syncBranch}})
		must(t, os.WriteFile(filepath.Join(dir, ".beads", "config.json"), b, 0o644))
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
		{"add", ".beads/issues.jsonl"},
		{"commit", "-q", "-m", "init"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// --- exit code contract -------------------------------------------------

func TestExitCodeContract(t *testing.T) {
	t.Parallel()
	cases := []struct {
		worst  Severity
		strict bool
		want   int
	}{
		{RED, false, 2},
		{RED, true, 2},
		{YELLOW, false, 0},
		{YELLOW, true, 1},
		{GREEN, false, 0},
		{GREEN, true, 0},
	}
	for _, c := range cases {
		if got := ExitCode(c.worst, c.strict); got != c.want {
			t.Errorf("ExitCode(%v, %v)=%d want %d", c.worst, c.strict, got, c.want)
		}
	}
}

// --- diagnose: GREEN happy path ----------------------------------------

func TestHealthyProjectNotRed(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := t.TempDir()
	initGitRepo(t, dir, "beads-sync")
	r := Run([]string{dir}, 4)
	if len(r.Projects) != 1 {
		t.Fatalf("projects=%d", len(r.Projects))
	}
	if r.Projects[0].Severity == RED {
		t.Fatalf("healthy project flagged RED: %+v", r.Projects[0])
	}
}

// --- jsonl missing -----------------------------------------------------

func TestMissingJSONLYellow(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := t.TempDir()
	// `.beads/` exists but no issues.jsonl.
	must(t, os.MkdirAll(filepath.Join(dir, ".beads"), 0o755))
	r := Run([]string{dir}, 4)
	if len(r.Projects) != 1 {
		t.Fatalf("projects=%d", len(r.Projects))
	}
	if !hasCheck(r.Projects[0], "issues-jsonl", YELLOW) {
		t.Fatalf("expected issues-jsonl YELLOW; got %+v", r.Projects[0])
	}
}

// --- DB-in-filesync RED ------------------------------------------------

func TestDBInFilesyncRed(t *testing.T) {
	forceConfigJSONFallback(t)
	// Synthetic project living inside a fake "Dropbox" root.
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "Dropbox")
	must(t, os.MkdirAll(fake, 0o755))
	t.Setenv("BEADKEEPER_FILESYNC_ROOTS", fake)
	repo := filepath.Join(fake, "rotten")
	initGitRepo(t, repo, "beads-sync")
	must(t, os.WriteFile(filepath.Join(repo, ".beads", "beads.db"), []byte("x"), 0o644))

	r := Run([]string{repo}, 4)
	if len(r.Projects) != 1 {
		t.Fatalf("projects=%d", len(r.Projects))
	}
	if r.Projects[0].Severity != RED {
		t.Fatalf("severity=%v want RED", r.Projects[0].Severity)
	}
	if !hasCheck(r.Projects[0], "db-in-filesync", RED) {
		t.Fatalf("expected db-in-filesync RED; got %+v", r.Projects[0])
	}
}

// --- daemon: stale PID RED --------------------------------------------

func TestStaleDaemonPIDRed(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := t.TempDir()
	initGitRepo(t, dir, "beads-sync")
	must(t, os.WriteFile(filepath.Join(dir, ".beads", "daemon.pid"), []byte("999999\n"), 0o644))
	r := Run([]string{dir}, 4)
	if !hasCheck(r.Projects[0], "daemon", RED) {
		t.Fatalf("expected daemon RED; got %+v", r.Projects[0])
	}
}

func TestAliveDaemonPIDGreen(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := t.TempDir()
	initGitRepo(t, dir, "beads-sync")
	must(t, os.WriteFile(filepath.Join(dir, ".beads", "daemon.pid"), []byte(strconv.Itoa(syscall.Getpid())), 0o644))
	r := Run([]string{dir}, 4)
	if !hasCheck(r.Projects[0], "daemon", GREEN) {
		t.Fatalf("expected daemon GREEN; got %+v", r.Projects[0])
	}
}

// --- sync-state needs_manual_sync (regardless of pid) -----------------

func TestNeedsManualSyncWithoutPidRed(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := t.TempDir()
	initGitRepo(t, dir, "beads-sync")
	b, _ := json.Marshal(map[string]any{"needs_manual_sync": true})
	must(t, os.WriteFile(filepath.Join(dir, ".beads", "sync-state.json"), b, 0o644))
	r := Run([]string{dir}, 4)
	if !hasCheck(r.Projects[0], "sync-state", RED) {
		t.Fatalf("expected sync-state RED; got %+v", r.Projects[0])
	}
	if r.Projects[0].Severity != RED {
		t.Fatalf("project severity %v want RED", r.Projects[0].Severity)
	}
}

func TestFiveFailuresRed(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := t.TempDir()
	initGitRepo(t, dir, "beads-sync")
	b, _ := json.Marshal(map[string]any{"consecutive_failures": 5})
	must(t, os.WriteFile(filepath.Join(dir, ".beads", "sync-state.json"), b, 0o644))
	r := Run([]string{dir}, 4)
	if !hasCheck(r.Projects[0], "sync-state", RED) {
		t.Fatalf("expected sync-state RED; got %+v", r.Projects[0])
	}
}

func TestOneFailureYellow(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := t.TempDir()
	initGitRepo(t, dir, "beads-sync")
	b, _ := json.Marshal(map[string]any{"consecutive_failures": 1})
	must(t, os.WriteFile(filepath.Join(dir, ".beads", "sync-state.json"), b, 0o644))
	r := Run([]string{dir}, 4)
	if !hasCheck(r.Projects[0], "sync-state", YELLOW) {
		t.Fatalf("expected sync-state YELLOW; got %+v", r.Projects[0])
	}
}

// --- sync-branch empty -------------------------------------------------

func TestEmptySyncBranchYellow(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := t.TempDir()
	initGitRepo(t, dir, "")
	r := Run([]string{dir}, 4)
	if !hasCheck(r.Projects[0], "sync-branch", YELLOW) {
		t.Fatalf("expected sync-branch YELLOW; got %+v", r.Projects[0])
	}
}

// --- render -----------------------------------------------------------

func TestRenderTextEmptyAndProjects(t *testing.T) {
	t.Parallel()
	empty := Report{ScannedPaths: []string{"/nowhere"}, Worst: GREEN}
	text := RenderText(empty, false)
	if !strings.Contains(text, "No projects") {
		t.Fatalf("expected 'No projects' in %q", text)
	}
	one := Report{
		ScannedPaths: []string{"/a"},
		Worst:        RED,
		Projects: []ProjectHealth{
			{
				ProjectRoot: "/a", Severity: RED,
				Checks: []Check{{Name: "x", Severity: RED, Message: "bad"}},
			},
		},
	}
	text = RenderText(one, false)
	if !strings.Contains(text, "Overall: RED") {
		t.Fatalf("expected Overall: RED in %q", text)
	}
	// ANSI escapes only when useColor=true.
	if strings.Contains(text, "\x1b[") {
		t.Fatalf("color leak in no-color render: %q", text)
	}
}

func TestRenderJSONShape(t *testing.T) {
	t.Parallel()
	r := Report{
		Worst:    GREEN,
		Projects: []ProjectHealth{{ProjectRoot: "/a", Severity: GREEN, Checks: nil}},
	}
	out := RenderJSON(r)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("RenderJSON not valid JSON: %v\n%s", err, out)
	}
	if parsed["worst"] != "green" {
		t.Fatalf("worst=%v", parsed["worst"])
	}
}

// --- helpers ----------------------------------------------------------

func hasCheck(ph ProjectHealth, name string, sev Severity) bool {
	for _, c := range ph.Checks {
		if c.Name == name && c.Severity == sev {
			return true
		}
	}
	return false
}
