package beadlint

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanRepo(t *testing.T) {
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"id":"e-1","issue_type":"epic","status":"open","description":"# t\n\n## Acceptance\n- ok\n"}`,
		`{"id":"e-2","issue_type":"epic","status":"open","description":"## Decisions\n- Decision: a — Why: b.\n"}`,
		`{"id":"p-1","issue_type":"progression","status":"open","description":"## Log\n- 2026-06-05 baseline: y.\n"}`,
		`{"id":"t-1","issue_type":"task","status":"open","description":""}`,
		`{"id":"e-3","issue_type":"epic","status":"closed","description":"nope"}`,
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "issues.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	findings := ScanRepo(dir)
	if len(findings) == 0 {
		t.Fatal("expected findings")
	}
	// e-1 (epic missing Decisions) and p-1 (progression missing label +
	// Current understanding) must appear; e-2 (clean) and the closed
	// e-3 must not.
	byID := map[string]int{}
	for _, f := range findings {
		byID[f.BeadID]++
	}
	if byID["e-1"] == 0 {
		t.Errorf("expected findings for e-1")
	}
	if byID["p-1"] == 0 {
		t.Errorf("expected findings for p-1")
	}
	if byID["e-2"] != 0 {
		t.Errorf("clean epic e-2 should have no findings")
	}
	if byID["e-3"] != 0 {
		t.Errorf("closed epic e-3 must be skipped")
	}

	epics := FilterByType(findings, "epic")
	for _, f := range epics {
		if f.Type != "epic" {
			t.Errorf("FilterByType returned non-epic: %+v", f)
		}
		if f.BeadID == "p-1" {
			t.Errorf("progression finding leaked into epic filter")
		}
	}
	if len(epics) == 0 {
		t.Errorf("expected at least one epic finding (e-1)")
	}
}

func TestScanRepo_MissingFile(t *testing.T) {
	if fs := ScanRepo(t.TempDir()); fs != nil {
		t.Errorf("missing JSONL should yield nil, got %+v", fs)
	}
}
