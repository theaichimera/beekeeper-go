package syncbranch

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	execwrap "github.com/theaichimera/beekeeper-go/internal/exec"
	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
)

// forceConfigJSONFallback mirrors Python's `_force_config_json_fallback`
// test fixture so `bd config get` never runs against synthetic repos.
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

// initRepo builds a workspace at tmp/name with a configured
// sync.branch and an initial empty JSONL commit on `main`.
func initRepo(t *testing.T, name, syncBranch string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := filepath.Join(t.TempDir(), name)
	must(t, os.MkdirAll(filepath.Join(dir, ".beads"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, ".beads", "issues.jsonl"), nil, 0o644))
	if syncBranch != "" {
		must(t, os.WriteFile(
			filepath.Join(dir, ".beads", "config.json"),
			mustJSON(map[string]any{"sync": map[string]any{"branch": syncBranch}}),
			0o644,
		))
	}
	mustGit(t, dir, "init", "-q", "-b", "main")
	mustGit(t, dir, "config", "user.email", "t@t")
	mustGit(t, dir, "config", "user.name", "t")
	mustGit(t, dir, "add", ".beads/issues.jsonl")
	mustGit(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func commitOn(t *testing.T, repo, branch, line string) {
	t.Helper()
	// `-B <branch>` resets an existing branch; we use plain checkout
	// when the branch already exists, else create.
	if branchExists(repo, branch) {
		mustGit(t, repo, "checkout", "-q", branch)
	} else {
		mustGit(t, repo, "checkout", "-q", "-b", branch)
	}
	jsonl := filepath.Join(repo, ".beads", "issues.jsonl")
	existing, _ := os.ReadFile(jsonl)
	new := append(existing, []byte(line+"\n")...)
	must(t, os.WriteFile(jsonl, new, 0o644))
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
func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func TestEmptySyncBranchIsYellow(t *testing.T) {
	forceConfigJSONFallback(t)
	repo := initRepo(t, "empty-sb", "")
	r := Scan([]string{repo}, 4)
	if r.Worst() != YELLOW {
		t.Fatalf("worst=%v want YELLOW", r.Worst())
	}
	if len(r.Findings) != 1 || r.Findings[0].Kind != "empty-sync-branch" {
		t.Fatalf("findings=%v", r.Findings)
	}
}

func TestMissingSyncBranchIsYellow(t *testing.T) {
	forceConfigJSONFallback(t)
	repo := initRepo(t, "missing-sb", "beads-sync") // configured but never created
	r := Scan([]string{repo}, 4)
	if r.Worst() != YELLOW {
		t.Fatalf("worst=%v want YELLOW", r.Worst())
	}
	if len(r.Findings) != 1 || r.Findings[0].Kind != "missing-sync-branch" {
		t.Fatalf("findings=%v", r.Findings)
	}
}

func TestCleanNoStrandedCommits(t *testing.T) {
	forceConfigJSONFallback(t)
	repo := initRepo(t, "clean", "beads-sync")
	mustGit(t, repo, "branch", "beads-sync")
	commitOn(t, repo, "beads-sync", `{"id":"x.1","status":"open"}`)
	r := Scan([]string{repo}, 4)
	if r.Worst() != GREEN || len(r.Findings) != 0 {
		t.Fatalf("expected clean; got %+v", r)
	}
}

func TestStrandedCommitOnFeatureBranchIsRed(t *testing.T) {
	forceConfigJSONFallback(t)
	repo := initRepo(t, "stranded", "beads-sync")
	mustGit(t, repo, "branch", "beads-sync")
	commitOn(t, repo, "feature/whatever", `{"id":"x.2","status":"open"}`)
	r := Scan([]string{repo}, 4)
	if r.Worst() != RED {
		t.Fatalf("worst=%v want RED; findings=%v", r.Worst(), r.Findings)
	}
	var branches []string
	for _, f := range r.Findings {
		if f.Kind == "stranded-bead-commit" {
			branches = append(branches, f.Branch)
		}
	}
	want := "feature/whatever"
	for _, b := range branches {
		if b == want {
			return
		}
	}
	t.Fatalf("expected branch %q in stranded findings; got %v", want, branches)
}

func TestContentSubsetSuppressesStranding(t *testing.T) {
	// A trunk-sync replay commit on trunk: JSONL identical to the
	// sync branch. Commit-graph diff would flag it; content-aware
	// detection must NOT.
	forceConfigJSONFallback(t)
	repo := initRepo(t, "subset", "beads-sync")
	mustGit(t, repo, "branch", "beads-sync")
	commitOn(t, repo, "beads-sync", `{"id":"b1","status":"open"}`)
	commitOn(t, repo, "beads-sync", `{"id":"b2","status":"open"}`)
	// Trunk gets a DISTINCT commit (different message -> SHA) whose
	// JSONL is a content subset of beads-sync. Same id b1 -> subset.
	mustGit(t, repo, "checkout", "-q", "main")
	jsonl := filepath.Join(repo, ".beads", "issues.jsonl")
	must(t, os.WriteFile(jsonl, []byte(`{"id":"b1","status":"open"}`+"\n"), 0o644))
	mustGit(t, repo, "add", ".beads/issues.jsonl")
	mustGit(t, repo, "commit", "-q", "-m", "trunk-sync replay")

	r := Scan([]string{repo}, 4)
	for _, f := range r.Findings {
		if f.Kind == "stranded-bead-commit" && f.Branch == "main" {
			t.Fatalf("main flagged as stranded despite content subset: %+v", f)
		}
	}
}

func TestStrandedMessageFormat(t *testing.T) {
	forceConfigJSONFallback(t)
	repo := initRepo(t, "msgfmt", "beads-sync")
	mustGit(t, repo, "branch", "beads-sync")
	commitOn(t, repo, "feature/x", `{"id":"a"}`)
	r := Scan([]string{repo}, 4)
	if len(r.Findings) == 0 {
		t.Fatal("expected at least one finding")
	}
	f := r.Findings[0]
	if !strings.Contains(f.Message, "feature/x") || !strings.Contains(f.Message, "beads-sync") {
		t.Fatalf("message=%q", f.Message)
	}
	if f.CommitCount != 1 {
		t.Fatalf("CommitCount=%d want 1", f.CommitCount)
	}
}
