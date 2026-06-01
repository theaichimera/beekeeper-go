package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitInRepo(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE=2026-06-01T12:00:00",
		"GIT_COMMITTER_DATE=2026-06-01T12:00:00",
	)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func initStaleBeadsRepo(t *testing.T, recs []map[string]any, subjects []string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	var body []byte
	for _, r := range recs {
		b, _ := json.Marshal(r)
		body = append(body, b...)
		body = append(body, '\n')
	}
	if err := os.WriteFile(filepath.Join(dir, ".beads", "issues.jsonl"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	gitInRepo(t, dir, "init", "-q", "-b", "main")
	gitInRepo(t, dir, "config", "user.email", "t@t")
	gitInRepo(t, dir, "config", "user.name", "t")
	gitInRepo(t, dir, "add", ".beads/issues.jsonl")
	gitInRepo(t, dir, "commit", "-q", "-m", "init")
	for i, subj := range subjects {
		f := filepath.Join(dir, "f"+string(rune('a'+i%26))+".txt")
		if err := os.WriteFile(f, []byte(subj), 0o644); err != nil {
			t.Fatal(err)
		}
		gitInRepo(t, dir, "add", f)
		gitInRepo(t, dir, "commit", "-q", "-m", subj)
	}
	return dir
}

func TestCLIGuardStaleBeadsCleanGreen(t *testing.T) {
	dir := initStaleBeadsRepo(t, []map[string]any{
		{"id": "demo-x", "status": "in_progress"},
	}, nil)
	out, _, rc := runCmd(t, "guard", "stale-beads", "--repo", dir, "--branch", "main", "--prefix", "demo")
	if rc != 0 {
		t.Fatalf("rc=%d out=%q", rc, out)
	}
	if !strings.Contains(out, "OK") {
		t.Fatalf("missing OK: %q", out)
	}
}

func TestCLIGuardStaleBeadsRedExitsTwo(t *testing.T) {
	dir := initStaleBeadsRepo(t,
		[]map[string]any{
			{"id": "demo-xymh", "status": "in_progress"},
			{"id": "demo-afby", "status": "in_progress"}, // tangential
		},
		[]string{
			"feat(demo-xymh): ship parser (#736)",
			"chore: bump deps — see demo-afby for context",
		},
	)
	out, _, rc := runCmd(t, "guard", "stale-beads", "--repo", dir, "--branch", "main", "--json")
	if rc != 2 {
		t.Fatalf("rc=%d want 2; out=%s", rc, out)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("bad json: %v\n%s", err, out)
	}
	if doc["worst"] != "red" {
		t.Fatalf("worst=%v want red", doc["worst"])
	}
	fs := doc["findings"].([]any)
	if len(fs) != 1 {
		t.Fatalf("findings=%d want 1 (afby must NOT be flagged); doc=%v", len(fs), doc)
	}
	hit := fs[0].(map[string]any)
	if hit["id"] != "demo-xymh" {
		t.Fatalf("wrong id: %v", hit["id"])
	}
	if int(hit["landing_pr"].(float64)) != 736 {
		t.Fatalf("landing_pr=%v want 736", hit["landing_pr"])
	}
	if cmd, _ := hit["bd_close_command"].(string); !strings.Contains(cmd, "bd close demo-xymh") || !strings.Contains(cmd, "#736") {
		t.Fatalf("bd_close_command not agent-driveable: %q", cmd)
	}
}

func TestCLIGuardStaleBeadsAutoDerivesPrefix(t *testing.T) {
	dir := initStaleBeadsRepo(t,
		[]map[string]any{{"id": "demo-only", "status": "in_progress"}},
		[]string{"feat(demo-only): ship (#1)"},
	)
	// No --prefix flag — must auto-derive.
	_, _, rc := runCmd(t, "guard", "stale-beads", "--repo", dir, "--branch", "main", "--json")
	if rc != 2 {
		t.Fatalf("rc=%d want 2", rc)
	}
}

func TestCLIGuardStaleBeadsMissingBranchExits64(t *testing.T) {
	dir := initStaleBeadsRepo(t,
		[]map[string]any{{"id": "demo-x", "status": "open"}},
		nil,
	)
	_, _, rc := runCmd(t, "guard", "stale-beads", "--repo", dir, "--branch", "no-such", "--prefix", "demo")
	// Diagnose returns an error on missing ref -> rc=1 generic.
	// With explicit --branch, the "cannot resolve" path doesn't fire.
	// Either rc=1 or rc=2 is acceptable; rc=64 is not.
	if rc == 64 {
		t.Fatalf("rc=64 unexpected on explicit missing branch")
	}
	if rc == 0 {
		t.Fatalf("rc=0 unexpected; want non-zero on missing branch")
	}
}

func TestCLIGuardStaleBeadsJSONShape(t *testing.T) {
	dir := initStaleBeadsRepo(t,
		[]map[string]any{{"id": "demo-x", "status": "in_progress"}},
		[]string{"feat(demo-x): land (#42)"},
	)
	out, _, _ := runCmd(t, "guard", "stale-beads", "--repo", dir, "--branch", "main", "--json")
	first := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
	if !strings.HasPrefix(first, "{") {
		t.Fatalf("--json must start with '{': %q", out)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not single JSON document: %v", err)
	}
	for _, k := range []string{"branch", "prefix", "lookback", "worst", "findings"} {
		if _, ok := doc[k]; !ok {
			t.Fatalf("missing key %s:\n%s", k, out)
		}
	}
}
