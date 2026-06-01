package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIBoardSummaryHumanFormat(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "summ")
	writeJSONLBoard(t, dir, []map[string]any{
		{"id": "x.1", "status": "open", "priority": 1},
		{"id": "x.2", "status": "in_progress", "priority": 0, "assignee": "alice"},
		{"id": "x.3", "status": "in_progress", "priority": 2}, // lease gap
		{"id": "x.4", "status": "closed", "priority": 1},
		{"id": "x.5", "status": "closed", "priority": 4},
	})
	out, _, rc := runCmd(t, "board", dir, "--summary")
	if rc != 0 {
		t.Fatalf("rc=%d out=%q", rc, out)
	}
	for _, want := range []string{
		"TOTAL 5",
		"closed=2",
		"in_progress=2",
		"open=1",
		"40.0% complete",
		"P0:1",
		"P1:1",
		"P2:1",
		"in_progress=2",
		"lease_gaps=1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in summary:\n%s", want, out)
		}
	}
	// Closed P4 must NOT appear in active-by-priority.
	if strings.Contains(out, "P4:") {
		t.Fatalf("closed P4 leaked into ACTIVE BY PRIORITY:\n%s", out)
	}
	// --summary must NOT print per-bead rows.
	if strings.Contains(out, "x.1") || strings.Contains(out, "x.2") {
		t.Fatalf("summary leaked per-bead rows:\n%s", out)
	}
}

func TestCLIBoardSummaryEmptyMessage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "empty")
	writeJSONLBoard(t, dir, nil)
	out, _, rc := runCmd(t, "board", dir, "--summary")
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if !strings.Contains(out, "no projects") && !strings.Contains(out, "no parseable") {
		t.Fatalf("unexpected empty-summary output: %q", out)
	}
}

func TestCLIBoardJSONIncludesSummary(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "json-sum")
	writeJSONLBoard(t, dir, []map[string]any{
		{"id": "x.1", "status": "open", "priority": 1},
		{"id": "x.2", "status": "closed", "priority": 2},
	})
	out, _, rc := runCmd(t, "board", dir, "--json")
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, out)
	}
	// Existing keys must still be present (back-compat).
	for _, k := range []string{"projects", "totals", "summary"} {
		if _, ok := doc[k]; !ok {
			t.Fatalf("missing %s key:\n%s", k, out)
		}
	}
	s := doc["summary"].(map[string]any)
	for _, k := range []string{
		"total", "by_status", "active_by_priority",
		"percent_complete", "in_progress_count", "lease_gaps_count", "stale_wip_count",
	} {
		if _, ok := s[k]; !ok {
			t.Fatalf("summary missing %s:\n%s", k, out)
		}
	}
	if int(s["total"].(float64)) != 2 {
		t.Fatalf("summary.total=%v want 2", s["total"])
	}
	if pc := s["percent_complete"].(float64); pc < 49.99 || pc > 50.01 {
		t.Fatalf("percent_complete=%v want ~50", pc)
	}
	pri := s["active_by_priority"].(map[string]any)
	if int(pri["P1"].(float64)) != 1 {
		t.Fatalf("P1 count: %v", pri)
	}
	// Closed P2 must be absent from active-by-priority.
	if _, hasP2 := pri["P2"]; hasP2 {
		t.Fatalf("closed P2 leaked into active_by_priority: %v", pri)
	}
}

func TestCLIBoardSummaryJSONNoInterleavedText(t *testing.T) {
	// --json must produce ONE valid JSON document with no human prose
	// before/after — a hard contract for downstream agent consumers.
	dir := filepath.Join(t.TempDir(), "no-prose")
	writeJSONLBoard(t, dir, []map[string]any{
		{"id": "x.1", "status": "open"},
	})
	out, _, _ := runCmd(t, "board", dir, "--json", "--summary") // both flags
	first := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
	if !strings.HasPrefix(first, "{") {
		t.Fatalf("--json output does not start with '{': %q", out)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not single JSON document: %v", err)
	}
}
