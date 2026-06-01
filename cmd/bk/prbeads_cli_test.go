package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initPRBeadsRepo builds a real git repo with an empty bead JSONL on
// `main`, then layers commits per the supplied (branch, recs) pairs.
// Returns the repo path.
func initPRBeadsRepo(t *testing.T, layers []struct {
	branch string
	recs   []map[string]any
}) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	mustGit := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, ".beads"), 0o755))
	must(os.WriteFile(filepath.Join(dir, ".beads", "issues.jsonl"), nil, 0o644))
	mustGit("init", "-q", "-b", "main")
	mustGit("config", "user.email", "t@t")
	mustGit("config", "user.name", "t")
	mustGit("add", ".beads/issues.jsonl")
	mustGit("commit", "-q", "-m", "init")

	branchExists := func(b string) bool {
		c := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+b)
		c.Dir = dir
		return c.Run() == nil
	}

	for _, layer := range layers {
		if branchExists(layer.branch) {
			mustGit("checkout", "-q", layer.branch)
		} else {
			mustGit("checkout", "-q", "-b", layer.branch)
		}
		var sb strings.Builder
		for _, rec := range layer.recs {
			b, _ := json.Marshal(rec)
			sb.Write(b)
			sb.WriteByte('\n')
		}
		must(os.WriteFile(filepath.Join(dir, ".beads", "issues.jsonl"), []byte(sb.String()), 0o644))
		mustGit("add", ".beads/issues.jsonl")
		c := exec.Command("git", "diff", "--cached", "--quiet")
		c.Dir = dir
		if c.Run() == nil {
			continue // identical tree
		}
		mustGit("commit", "-q", "-m", "edit on "+layer.branch)
	}
	return dir
}

func TestCLIPRBeadsCleanGreen(t *testing.T) {
	repo := initPRBeadsRepo(t, []struct {
		branch string
		recs   []map[string]any
	}{
		{"main", []map[string]any{{"id": "a-1", "status": "open"}}},
		{"feature/forward", []map[string]any{
			{"id": "a-1", "status": "open"},
			{"id": "a-2", "status": "open"},
		}},
	})
	out, _, rc := runCmd(t, "guard", "pr-beads",
		"--repo", repo, "--base", "main", "--head", "feature/forward")
	if rc != 0 {
		t.Fatalf("rc=%d (want 0); out=%q", rc, out)
	}
	if !strings.Contains(out, "OK") {
		t.Fatalf("missing OK in %q", out)
	}
}

func TestCLIPRBeadsRegressionExitsTwo(t *testing.T) {
	// PR-13 incident: branch carries open snapshot; main advanced to
	// in_progress with assignee.
	repo := initPRBeadsRepo(t, []struct {
		branch string
		recs   []map[string]any
	}{
		{"main", []map[string]any{{"id": "a-24", "status": "open"}}},
		{"feature/stale", []map[string]any{{"id": "a-24", "status": "open"}}},
		{"main", []map[string]any{
			{"id": "a-24", "status": "in_progress", "assignee": "queryparser-agent"},
		}},
	})
	out, _, rc := runCmd(t, "guard", "pr-beads",
		"--repo", repo, "--base", "main", "--head", "feature/stale", "--json")
	if rc != 2 {
		t.Fatalf("rc=%d (want 2); out=%s", rc, out)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if doc["worst"] != "red" {
		t.Fatalf("worst=%v want red", doc["worst"])
	}
	fs, ok := doc["findings"].([]any)
	if !ok || len(fs) == 0 {
		t.Fatalf("findings missing/empty: %v", doc)
	}
	wantKinds := map[string]bool{"status-rewind": false, "assignee-rewind": false}
	for _, f := range fs {
		row := f.(map[string]any)
		if row["id"] != "a-24" {
			t.Fatalf("unexpected id: %v", row)
		}
		if k, ok := wantKinds[row["kind"].(string)]; ok {
			_ = k
			wantKinds[row["kind"].(string)] = true
		}
	}
	for k, hit := range wantKinds {
		if !hit {
			t.Fatalf("missing kind %s in %v", k, fs)
		}
	}
}

func TestCLIPRBeadsNoBeadsPolicy(t *testing.T) {
	// Pure forward-only edit — passes regression policy, fails no-beads.
	repo := initPRBeadsRepo(t, []struct {
		branch string
		recs   []map[string]any
	}{
		{"main", []map[string]any{{"id": "a-1", "status": "open"}}},
		{"feature/touchy", []map[string]any{
			{"id": "a-1", "status": "open"},
			{"id": "a-2", "status": "open"},
		}},
	})

	_, _, rc := runCmd(t, "guard", "pr-beads",
		"--repo", repo, "--base", "main", "--head", "feature/touchy",
		"--policy", "regression")
	if rc != 0 {
		t.Fatalf("regression policy: rc=%d want 0", rc)
	}

	_, _, rc = runCmd(t, "guard", "pr-beads",
		"--repo", repo, "--base", "main", "--head", "feature/touchy",
		"--policy", "no-beads")
	if rc != 2 {
		t.Fatalf("no-beads policy: rc=%d want 2", rc)
	}
}

func TestCLIPRBeadsInvalidPolicyExits64(t *testing.T) {
	repo := initPRBeadsRepo(t, []struct {
		branch string
		recs   []map[string]any
	}{
		{"main", []map[string]any{{"id": "a-1", "status": "open"}}},
	})
	_, errOut, rc := runCmd(t, "guard", "pr-beads",
		"--repo", repo, "--base", "main", "--head", "main",
		"--policy", "wat")
	if rc != 64 {
		t.Fatalf("rc=%d want 64; stderr=%q", rc, errOut)
	}
}

func TestCLIPRBeadsMissingHeadExits64(t *testing.T) {
	repo := initPRBeadsRepo(t, []struct {
		branch string
		recs   []map[string]any
	}{
		{"main", []map[string]any{{"id": "a-1", "status": "open"}}},
	})
	_, _, rc := runCmd(t, "guard", "pr-beads",
		"--repo", repo, "--base", "main", "--head", "no-such-branch")
	if rc != 64 {
		t.Fatalf("rc=%d want 64", rc)
	}
}

func TestCLIPRBeadsJSONShape(t *testing.T) {
	repo := initPRBeadsRepo(t, []struct {
		branch string
		recs   []map[string]any
	}{
		{"main", []map[string]any{{"id": "a-1", "status": "open"}}},
		{"feature/clean", []map[string]any{{"id": "a-1", "status": "open"}}},
	})
	out, _, rc := runCmd(t, "guard", "pr-beads",
		"--repo", repo, "--base", "main", "--head", "feature/clean", "--json")
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	for _, k := range []string{"base", "head", "policy", "worst", "findings"} {
		if _, ok := doc[k]; !ok {
			t.Fatalf("missing key %s in %v", k, doc)
		}
	}
	if doc["worst"] != "green" {
		t.Fatalf("worst=%v want green", doc["worst"])
	}
}
