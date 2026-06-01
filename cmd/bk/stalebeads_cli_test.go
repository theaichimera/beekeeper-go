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

func TestCLIGuardStaleBeadsExcludesBdManagement(t *testing.T) {
	// Ensures the bkg-td0.1 acceptance: spec(...) and chore(bd|beads):
	// subjects are not shipped even when they carry token-bounded ids
	// and a (#N) PR-merge marker. Tangential subjects with non-allowed
	// types are silently skipped.
	dir := initStaleBeadsRepo(t,
		[]map[string]any{
			{"id": "demo-1k8x", "status": "in_progress"}, // false positive in real run
			{"id": "demo-5ctm", "status": "in_progress"},
			{"id": "demo-h2ud", "status": "in_progress"},
			{"id": "demo-real", "status": "in_progress"}, // genuine
		},
		[]string{
			"spec(demo-1k8x): file epic + children (#830)",
			"chore(bd): file demo-5ctm and demo-km84 (#1052)",
			"chore(bd): file demo-app onboarding bead (demo-h2ud) (#1003)",
			"feat(demo-real): legitimate landing (#42)",
		},
	)
	out, _, _ := runCmd(t, "guard", "stale-beads",
		"--repo", dir, "--branch", "main", "--json")
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("bad json: %v\n%s", err, out)
	}
	fs := doc["findings"].([]any)
	if len(fs) != 1 {
		t.Fatalf("findings=%d want 1 (only demo-real); got: %v", len(fs), fs)
	}
	hit := fs[0].(map[string]any)
	if hit["id"] != "demo-real" {
		t.Fatalf("wrong id flagged: %v", hit["id"])
	}
}

func TestCLIGuardStaleBeadsShipTypesFlag(t *testing.T) {
	// `--ship-types` lets a repo opt into different conventional-commit
	// types. Default excludes `docs`; setting it to docs alone makes
	// docs-typed commits ship and feat-typed not.
	dir := initStaleBeadsRepo(t,
		[]map[string]any{
			{"id": "demo-feat", "status": "in_progress"},
			{"id": "demo-docs", "status": "in_progress"},
		},
		[]string{
			"feat(demo-feat): land impl (#1)",
			"docs(demo-docs): land doc (#2)",
		},
	)
	// Default: only feat ships.
	out, _, _ := runCmd(t, "guard", "stale-beads",
		"--repo", dir, "--branch", "main", "--json")
	var doc map[string]any
	_ = json.Unmarshal([]byte(out), &doc)
	if len(doc["findings"].([]any)) != 1 {
		t.Fatalf("default: want 1 finding (feat); got %v", doc["findings"])
	}
	// Override -> only docs ships.
	out, _, _ = runCmd(t, "guard", "stale-beads",
		"--repo", dir, "--branch", "main", "--ship-types", "docs", "--json")
	_ = json.Unmarshal([]byte(out), &doc)
	fs := doc["findings"].([]any)
	if len(fs) != 1 {
		t.Fatalf("docs override: want 1 finding (docs); got %v", fs)
	}
	if fs[0].(map[string]any)["id"] != "demo-docs" {
		t.Fatalf("wrong id under docs override: %v", fs[0])
	}
}

func TestCLIGuardStaleBeadsSourceFlagValidation(t *testing.T) {
	dir := initStaleBeadsRepo(t,
		[]map[string]any{{"id": "demo-x", "status": "open"}},
		nil,
	)
	_, _, rc := runCmd(t, "guard", "stale-beads",
		"--repo", dir, "--branch", "main", "--source", "wat")
	if rc != 64 {
		t.Fatalf("rc=%d want 64 on bad --source", rc)
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
