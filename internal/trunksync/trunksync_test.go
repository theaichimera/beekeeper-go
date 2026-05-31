package trunksync

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

func initRepo(t *testing.T, name, syncBranch string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := filepath.Join(t.TempDir(), name)
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
	return dir
}

func commitOn(t *testing.T, repo, branch, line string) {
	t.Helper()
	if branchExists(repo, branch) {
		mustGit(t, repo, "checkout", "-q", branch)
	} else {
		mustGit(t, repo, "checkout", "-q", "-b", branch)
	}
	jsonl := filepath.Join(repo, ".beads", "issues.jsonl")
	existing, _ := os.ReadFile(jsonl)
	must(t, os.WriteFile(jsonl, append(existing, []byte(line+"\n")...), 0o644))
	mustGit(t, repo, "add", ".beads/issues.jsonl")
	mustGit(t, repo, "commit", "-q", "-m", "bead edit")
}

func branchExists(repo, branch string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = repo
	return cmd.Run() == nil
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// --- scan -----------------------------------------------------------------

func TestScanClean(t *testing.T) {
	forceConfigJSONFallback(t)
	repo := initRepo(t, "clean", "beads-sync")
	commitOn(t, repo, "main", `{"id":"a","status":"open"}`)
	mustGit(t, repo, "branch", "beads-sync")
	r := Scan([]string{repo}, 4)
	if r.Worst() != GREEN || len(r.Findings) != 0 {
		t.Fatalf("expected clean; got %+v", r)
	}
}

func TestScanSyncBranchAhead(t *testing.T) {
	forceConfigJSONFallback(t)
	repo := initRepo(t, "ahead", "beads-sync")
	mustGit(t, repo, "branch", "beads-sync")
	commitOn(t, repo, "beads-sync", `{"id":"b","status":"closed"}`)
	r := Scan([]string{repo}, 4)
	if r.Worst() != YELLOW {
		t.Fatalf("worst=%v want YELLOW; %+v", r.Worst(), r.Findings)
	}
	if r.Findings[0].Kind != "sync-branch-ahead" {
		t.Fatalf("kind=%s", r.Findings[0].Kind)
	}
}

func TestScanDivergentTrunkEdit(t *testing.T) {
	forceConfigJSONFallback(t)
	repo := initRepo(t, "diverge", "beads-sync")
	mustGit(t, repo, "branch", "beads-sync")
	commitOn(t, repo, "beads-sync", `{"id":"c","status":"open"}`)
	// Genuine divergence: trunk holds bead id absent from sync.
	commitOn(t, repo, "main", `{"id":"d","status":"open"}`)
	r := Scan([]string{repo}, 4)
	if r.Worst() != RED {
		t.Fatalf("worst=%v want RED; %+v", r.Worst(), r.Findings)
	}
}

func TestScanTrunkSubsetOfSyncIsNotDivergent(t *testing.T) {
	// trunk holds a bead id already present on sync -> NOT divergent.
	forceConfigJSONFallback(t)
	repo := initRepo(t, "subset", "beads-sync")
	mustGit(t, repo, "branch", "beads-sync")
	commitOn(t, repo, "beads-sync", `{"id":"b1","status":"open"}`)
	commitOn(t, repo, "beads-sync", `{"id":"b2","status":"open"}`)
	// Distinct trunk commit, JSONL content is a subset of beads-sync.
	mustGit(t, repo, "checkout", "-q", "main")
	jsonl := filepath.Join(repo, ".beads", "issues.jsonl")
	must(t, os.WriteFile(jsonl, []byte(`{"id":"b1","status":"open"}`+"\n"), 0o644))
	mustGit(t, repo, "add", ".beads/issues.jsonl")
	mustGit(t, repo, "commit", "-q", "-m", "replay")
	r := Scan([]string{repo}, 4)
	for _, f := range r.Findings {
		if f.Kind == "divergent-trunk-edit" {
			t.Fatalf("trunk subset must not be divergent: %+v", f)
		}
	}
}

func TestScanMissingSyncBranchYellow(t *testing.T) {
	forceConfigJSONFallback(t)
	repo := initRepo(t, "no-sb", "beads-sync") // configured but never created
	r := Scan([]string{repo}, 4)
	if r.Worst() != YELLOW {
		t.Fatalf("worst=%v want YELLOW; %+v", r.Worst(), r.Findings)
	}
	if r.Findings[0].Kind != "missing-sync-branch" {
		t.Fatalf("kind=%s", r.Findings[0].Kind)
	}
}

// --- apply ----------------------------------------------------------------

func TestApplyDryRunNoMutation(t *testing.T) {
	forceConfigJSONFallback(t)
	repo := initRepo(t, "dry", "beads-sync")
	mustGit(t, repo, "branch", "beads-sync")
	commitOn(t, repo, "beads-sync", `{"id":"x","status":"closed"}`)
	beforeSha := revParse(t, repo, "main")
	res, err := ApplyOne(bkproject.Project{Root: repo}, true)
	if err != nil || !res.WouldApply || res.Applied {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if revParse(t, repo, "main") != beforeSha {
		t.Fatal("trunk should be unchanged after dry-run")
	}
}

func TestApplyFastForwardsCleanDelta(t *testing.T) {
	forceConfigJSONFallback(t)
	repo := initRepo(t, "ff", "beads-sync")
	mustGit(t, repo, "branch", "beads-sync")
	commitOn(t, repo, "beads-sync", `{"id":"y","status":"closed"}`)
	res, err := ApplyOne(bkproject.Project{Root: repo}, false)
	if err != nil || !res.Applied {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	// trunk's JSONL must now match beads-sync's JSONL.
	mustGit(t, repo, "checkout", "-q", "main")
	trunkContent, _ := os.ReadFile(filepath.Join(repo, ".beads", "issues.jsonl"))
	mustGit(t, repo, "checkout", "-q", "beads-sync")
	syncContent, _ := os.ReadFile(filepath.Join(repo, ".beads", "issues.jsonl"))
	if string(trunkContent) != string(syncContent) {
		t.Fatalf("trunk=%q sync=%q", trunkContent, syncContent)
	}
}

func TestApplyIdempotent(t *testing.T) {
	forceConfigJSONFallback(t)
	repo := initRepo(t, "idem", "beads-sync")
	mustGit(t, repo, "branch", "beads-sync")
	commitOn(t, repo, "beads-sync", `{"id":"z","status":"open"}`)
	if _, err := ApplyOne(bkproject.Project{Root: repo}, false); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	res2, err := ApplyOne(bkproject.Project{Root: repo}, false)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if res2.Applied || res2.WouldApply {
		t.Fatalf("second apply should be no-op; got %+v", res2)
	}
}

func TestApplyRefusesOnDivergentTrunkEdit(t *testing.T) {
	forceConfigJSONFallback(t)
	repo := initRepo(t, "div-apply", "beads-sync")
	mustGit(t, repo, "branch", "beads-sync")
	commitOn(t, repo, "beads-sync", `{"id":"e","status":"open"}`)
	commitOn(t, repo, "main", `{"id":"f","status":"open"}`)
	_, err := ApplyOne(bkproject.Project{Root: repo}, false)
	if err == nil || !strings.Contains(err.Error(), "divergent") {
		t.Fatalf("expected divergent refusal; got %v", err)
	}
}

func TestApplyRefusesLiveDaemon(t *testing.T) {
	forceConfigJSONFallback(t)
	repo := initRepo(t, "live", "beads-sync")
	mustGit(t, repo, "branch", "beads-sync")
	commitOn(t, repo, "beads-sync", `{"id":"g"}`)
	must(t, os.WriteFile(filepath.Join(repo, ".beads", "daemon.pid"), []byte(strconv.Itoa(syscall.Getpid())), 0o644))
	_, err := ApplyOne(bkproject.Project{Root: repo}, false)
	if err == nil || !strings.Contains(err.Error(), "daemon") {
		t.Fatalf("expected daemon refusal; got %v", err)
	}
}

func TestApplyRefusesDirtyTree(t *testing.T) {
	forceConfigJSONFallback(t)
	repo := initRepo(t, "dirty", "beads-sync")
	mustGit(t, repo, "branch", "beads-sync")
	commitOn(t, repo, "beads-sync", `{"id":"h"}`)
	mustGit(t, repo, "checkout", "-q", "main")
	// Dirty the tree (tracked addition, not just untracked).
	must(t, os.WriteFile(filepath.Join(repo, "extra.txt"), []byte("hi"), 0o644))
	mustGit(t, repo, "add", "extra.txt")
	_, err := ApplyOne(bkproject.Project{Root: repo}, false)
	if err == nil {
		t.Fatal("expected dirty-tree refusal")
	}
	if !(strings.Contains(err.Error(), "dirty") ||
		strings.Contains(err.Error(), "working tree") ||
		strings.Contains(err.Error(), "index")) {
		t.Fatalf("err=%v", err)
	}
}

// --- helpers --------------------------------------------------------------

func revParse(t *testing.T, repo, ref string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", ref)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse %s: %v\n%s", ref, err, out)
	}
	return strings.TrimSpace(string(out))
}
