// CLI tests for `bk guard beadspec` — the freshness hint (bkg-ckb):
// when the gate fails AND the bead DB is newer than the JSONL, the
// failure output points at `bd sync` because the fix may already be
// in the DB, just not exported/committed yet. The gate's pass/fail
// decision itself never depends on freshness — the committed/working-
// tree JSONL is what gets pushed.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const badEpicLine = `{"id":"g-1","issue_type":"epic","status":"open","description":"# t\n\n## Acceptance\n- ok\n"}`
const goodEpicLine = `{"id":"g-2","issue_type":"epic","status":"open","description":"## Decisions\n- Decision: a — Why: b.\n"}`

// initBeadspecRepo writes a .beads/issues.jsonl with the given lines
// plus a bead DB whose mtime is dbOffset relative to the JSONL's
// (positive == DB newer == stale JSONL).
func initBeadspecRepo(t *testing.T, lines []string, dbOffset time.Duration) string {
	t.Helper()
	dir := t.TempDir()
	beads := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beads, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonl := filepath.Join(beads, "issues.jsonl")
	if err := os.WriteFile(jsonl, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(beads, "beads.db")
	if err := os.WriteFile(db, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour)
	if err := os.Chtimes(jsonl, base, base); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(db, base.Add(dbOffset), base.Add(dbOffset)); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCLIGuardBeadspecFailingStaleDBHint(t *testing.T) {
	dir := initBeadspecRepo(t, []string{badEpicLine}, 30*time.Second)
	_, stderr, rc := runCmd(t, "guard", "beadspec", dir)
	if rc != 2 {
		t.Fatalf("rc=%d want 2; stderr=%s", rc, stderr)
	}
	if !strings.Contains(stderr, "bd sync") {
		t.Fatalf("expected `bd sync` freshness hint on stale failure: %s", stderr)
	}
}

func TestCLIGuardBeadspecFailingFreshNoHint(t *testing.T) {
	dir := initBeadspecRepo(t, []string{badEpicLine}, -30*time.Second)
	_, stderr, rc := runCmd(t, "guard", "beadspec", dir)
	if rc != 2 {
		t.Fatalf("rc=%d want 2; stderr=%s", rc, stderr)
	}
	if strings.Contains(stderr, "bd sync") {
		t.Fatalf("fresh JSONL must not emit the freshness hint: %s", stderr)
	}
}

func TestCLIGuardBeadspecPassingStaleSilent(t *testing.T) {
	dir := initBeadspecRepo(t, []string{goodEpicLine}, 30*time.Second)
	_, stderr, rc := runCmd(t, "guard", "beadspec", dir)
	if rc != 0 {
		t.Fatalf("rc=%d want 0; stderr=%s", rc, stderr)
	}
	if strings.Contains(stderr, "bd sync") {
		t.Fatalf("passing gate must stay silent about freshness: %s", stderr)
	}
}
