package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stale_wip_count must surface in --json and --summary so agents can
// read it programmatically.
func TestCLIBoardSummaryStaleWIPCount(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stale-board")
	old := time.Now().Add(-30 * 24 * time.Hour).Format(time.RFC3339Nano)
	writeJSONLBoard(t, dir, []map[string]any{
		{"id": "x.1", "status": "in_progress", "updated_at": old},
		{"id": "x.2", "status": "in_progress", "updated_at": old},
		{"id": "x.3", "status": "in_progress", "updated_at": time.Now().Format(time.RFC3339Nano)},
	})

	out, _, rc := runCmd(t, "board", dir, "--summary")
	if rc != 0 {
		t.Fatalf("rc=%d out=%q", rc, out)
	}
	if !strings.Contains(out, "stale>7d=2") {
		t.Fatalf("missing 'stale>7d=2' in summary:\n%s", out)
	}

	out, _, rc = runCmd(t, "board", dir, "--json")
	if rc != 0 {
		t.Fatalf("--json rc=%d", rc)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	s := doc["summary"].(map[string]any)
	if int(s["stale_wip_count"].(float64)) != 2 {
		t.Fatalf("stale_wip_count=%v want 2", s["stale_wip_count"])
	}
}

// --stale-days N overrides the default. -1 disables (count = 0).
func TestCLIBoardStaleDaysFlag(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "flag-board")
	mid := time.Now().Add(-10 * 24 * time.Hour).Format(time.RFC3339Nano)
	writeJSONLBoard(t, dir, []map[string]any{
		{"id": "x.1", "status": "in_progress", "updated_at": mid},
	})

	// Default 7d -> stale.
	out, _, _ := runCmd(t, "board", dir, "--summary")
	if !strings.Contains(out, "stale>7d=1") {
		t.Fatalf("default 7d missed stale: %s", out)
	}

	// 14d -> not stale.
	out, _, _ = runCmd(t, "board", dir, "--summary", "--stale-days", "14")
	if !strings.Contains(out, "stale>14d=0") {
		t.Fatalf("14d threshold should yield 0: %s", out)
	}

	// -1 -> disabled. Count is 0; threshold-line still renders.
	out, _, _ = runCmd(t, "board", dir, "--summary", "--stale-days", "-1")
	// disabled path normalizes display threshold to default 7 (cosmetic).
	if !strings.Contains(out, "=0") {
		t.Fatalf("-1 should disable (count 0): %s", out)
	}
}
