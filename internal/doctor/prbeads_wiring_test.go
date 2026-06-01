package doctor

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func mustGitDoctor(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeAndCommitDoctorJSONL(t *testing.T, repo, branch string, recs []map[string]any) {
	t.Helper()
	c := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	c.Dir = repo
	if c.Run() == nil {
		mustGitDoctor(t, repo, "checkout", "-q", branch)
	} else {
		mustGitDoctor(t, repo, "checkout", "-q", "-b", branch)
	}
	var sb strings.Builder
	for _, rec := range recs {
		b, _ := json.Marshal(rec)
		sb.Write(b)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(repo, ".beads", "issues.jsonl"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGitDoctor(t, repo, "add", ".beads/issues.jsonl")
	c2 := exec.Command("git", "diff", "--cached", "--quiet")
	c2.Dir = repo
	if c2.Run() == nil {
		return
	}
	mustGitDoctor(t, repo, "commit", "-q", "-m", "edit on "+branch)
}

// Doctor must be SILENT on a clean repo with a remote default branch
// resolvable but the working branch identical to base content.
func TestDoctorPRBeadsSilentWhenClean(t *testing.T) {
	forceConfigJSONFallback(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".beads", "issues.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		_ = c.Run()
	}
	writeAndCommitDoctorJSONL(t, dir, "main", []map[string]any{
		{"id": "a-1", "status": "open"},
	})

	// On `main` (no other branch). Doctor should not surface a pr-beads
	// row — comparing main to itself is suppressed.
	r := Run([]string{dir}, 4)
	for _, c := range r.Projects[0].Checks {
		if c.Name == "pr-beads" {
			t.Fatalf("unexpected pr-beads row on clean main: %+v", c)
		}
	}
}

// Doctor must surface RED when the current branch carries a stale
// snapshot vs main (the motivating PR-13 incident, expressed at the
// doctor layer).
func TestDoctorPRBeadsRedOnStaleBranch(t *testing.T) {
	forceConfigJSONFallback(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".beads", "issues.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
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

	// main holds open snapshot; branch carries old open; main advances
	// to in_progress. Doctor runs while we're checked out on the stale
	// branch — that's the developer-side warning.
	writeAndCommitDoctorJSONL(t, dir, "main", []map[string]any{
		{"id": "a-24", "status": "open"},
	})
	writeAndCommitDoctorJSONL(t, dir, "feature/stale", []map[string]any{
		{"id": "a-24", "status": "open"},
	})
	writeAndCommitDoctorJSONL(t, dir, "main", []map[string]any{
		{"id": "a-24", "status": "in_progress", "assignee": "queryparser-agent"},
	})
	mustGitDoctor(t, dir, "checkout", "-q", "feature/stale")

	r := Run([]string{dir}, 4)
	var prCheck *Check
	for i := range r.Projects[0].Checks {
		if r.Projects[0].Checks[i].Name == "pr-beads" {
			prCheck = &r.Projects[0].Checks[i]
			break
		}
	}
	if prCheck == nil {
		t.Fatalf("missing pr-beads check; checks=%+v", r.Projects[0].Checks)
	}
	if prCheck.Severity != RED {
		t.Fatalf("pr-beads severity=%s want RED", prCheck.Severity)
	}
}

// Doctor must NOT surface pr-beads when no remote default branch
// resolves (e.g. fresh init without `origin`). This protects new
// projects from a noisy red.
func TestDoctorPRBeadsSilentWithoutRemote(t *testing.T) {
	forceConfigJSONFallback(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".beads", "issues.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "topic"},
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
	// Single branch `topic`, no `main`, no remote -> resolveBaseRef
	// fails -> no pr-beads row.
	r := Run([]string{dir}, 4)
	for _, c := range r.Projects[0].Checks {
		if c.Name == "pr-beads" {
			t.Fatalf("pr-beads row on single-branch repo without remote: %+v", c)
		}
	}
}
