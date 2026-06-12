// CLI tests for the `bk board` freshness banner (bkg-ckb): the
// per-project text view warns when the bead DB is newer than the
// JSONL the board was built from.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// plantBeadDB adds a bead DB to an existing project dir and offsets
// its mtime from the JSONL's (positive == DB newer == stale board).
func plantBeadDB(t *testing.T, dir string, dbOffset time.Duration) {
	t.Helper()
	jsonl := filepath.Join(dir, ".beads", "issues.jsonl")
	db := filepath.Join(dir, ".beads", "beads.db")
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
}

func TestCLIBoardStaleDBBanner(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stale")
	writeJSONLBoard(t, dir, []map[string]any{{"id": "a", "status": "open"}})
	plantBeadDB(t, dir, 30*time.Second)
	stdout, _, rc := runCmd(t, "board", dir)
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if !strings.Contains(stdout, "bd sync") {
		t.Fatalf("expected stale-board banner pointing at `bd sync`: %s", stdout)
	}
}

func TestCLIBoardFreshNoBanner(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fresh")
	writeJSONLBoard(t, dir, []map[string]any{{"id": "a", "status": "open"}})
	plantBeadDB(t, dir, -30*time.Second)
	stdout, _, rc := runCmd(t, "board", dir)
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if strings.Contains(stdout, "bd sync") {
		t.Fatalf("fresh JSONL must not emit the banner: %s", stdout)
	}
}
