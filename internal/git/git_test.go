package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo builds a minimal repo with one commit and a user identity
// pinned, so downstream commits don't require host git config.
func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q", "-b", "main")
	mustGit(t, dir, "config", "user.email", "t@t")
	mustGit(t, dir, "config", "user.name", "t")
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".beads", "issues.jsonl"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", ".beads/issues.jsonl")
	mustGit(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	_ = os.MkdirAll(filepath.Dir(full), 0o755)
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCurrentBranchAndUpstream(t *testing.T) {
	t.Parallel()
	dir := initRepo(t)
	br, ok := CurrentBranch(dir)
	if !ok || br != "main" {
		t.Fatalf("CurrentBranch=%q ok=%v want main", br, ok)
	}
	if _, ok := Upstream(dir); ok {
		t.Fatal("Upstream should be absent on fresh repo")
	}
}

func TestStatusPorcelainAndIsClean(t *testing.T) {
	t.Parallel()
	dir := initRepo(t)
	clean, ok := IsClean(dir)
	if !ok || !clean {
		t.Fatalf("expected clean tree; clean=%v ok=%v", clean, ok)
	}
	writeFile(t, dir, "x.txt", "y")
	mustGit(t, dir, "add", "x.txt")
	clean, ok = IsClean(dir)
	if !ok || clean {
		t.Fatalf("expected dirty tree after staging; clean=%v ok=%v", clean, ok)
	}
}

func TestBranchExistsAndRevParse(t *testing.T) {
	t.Parallel()
	dir := initRepo(t)
	if !BranchExists(dir, "main") {
		t.Fatal("BranchExists(main)=false")
	}
	if BranchExists(dir, "nope") {
		t.Fatal("BranchExists(nope)=true")
	}
	sha, ok := RevParse(dir, "main")
	if !ok || len(sha) < 7 {
		t.Fatalf("RevParse(main)=%q ok=%v", sha, ok)
	}
}

func TestJSONLCommitsBetweenAndShow(t *testing.T) {
	t.Parallel()
	dir := initRepo(t)
	// branch off, edit JSONL, commit there.
	mustGit(t, dir, "checkout", "-q", "-b", "beads-sync")
	writeFile(t, dir, ".beads/issues.jsonl", `{"id":"x"}`+"\n")
	mustGit(t, dir, "add", ".beads/issues.jsonl")
	mustGit(t, dir, "commit", "-q", "-m", "bead edit")

	commits := JSONLCommitsBetween(dir, "main..beads-sync", ".beads/issues.jsonl")
	if len(commits) != 1 {
		t.Fatalf("expected 1 commit, got %d (%v)", len(commits), commits)
	}

	content, ok := Show(dir, "beads-sync", ".beads/issues.jsonl")
	if !ok || !strings.Contains(content, `"id":"x"`) {
		t.Fatalf("Show=%q ok=%v", content, ok)
	}

	// Empty range yields no commits.
	if got := JSONLCommitsBetween(dir, "beads-sync..main", ".beads/issues.jsonl"); len(got) != 0 {
		t.Fatalf("expected empty range, got %v", got)
	}
}

func TestRevParseFailsCleanly(t *testing.T) {
	t.Parallel()
	dir := initRepo(t)
	if _, ok := RevParse(dir, "never-existed"); ok {
		t.Fatal("RevParse should return ok=false on missing ref")
	}
}
