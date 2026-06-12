package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withBeadDB drops a bead DB into the project's .beads/ and sets the
// JSONL/DB mtimes relative to a base instant. dbOffset is the DB
// mtime minus the JSONL mtime — positive means "DB newer".
func withBeadDB(t *testing.T, root string, dbOffset time.Duration) {
	t.Helper()
	beads := filepath.Join(root, ".beads")
	db := filepath.Join(beads, "beads.db")
	if err := os.WriteFile(db, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(beads, "issues.jsonl"), base, base); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(db, base.Add(dbOffset), base.Add(dbOffset)); err != nil {
		t.Fatal(err)
	}
}

func TestCheckJSONLFreshness_StaleDBFlagged(t *testing.T) {
	p := writeProject(t, []string{`{"id":"f-1","issue_type":"task","status":"open"}`})
	withBeadDB(t, p.Root, 30*time.Second)

	checks := checkJSONLFreshness(p)
	if len(checks) != 1 {
		t.Fatalf("want exactly 1 jsonl-freshness check, got %d: %+v", len(checks), checks)
	}
	c := checks[0]
	if c.Name != "jsonl-freshness" {
		t.Errorf("check name = %q, want jsonl-freshness", c.Name)
	}
	if c.Severity != YELLOW {
		t.Errorf("severity = %q, want yellow", c.Severity)
	}
	if !strings.Contains(c.Message, "not yet exported") {
		t.Errorf("message should explain the export lag: %q", c.Message)
	}
	if !strings.Contains(c.Remediation, "bd sync") {
		t.Errorf("remediation should point at `bd sync`: %q", c.Remediation)
	}
}

func TestCheckJSONLFreshness_FreshSilent(t *testing.T) {
	p := writeProject(t, []string{`{"id":"f-1","issue_type":"task","status":"open"}`})
	withBeadDB(t, p.Root, -30*time.Second) // JSONL newer than DB.

	if checks := checkJSONLFreshness(p); checks != nil {
		t.Fatalf("fresh JSONL should produce no checks, got %+v", checks)
	}
}

func TestCheckJSONLFreshness_NoDBSilent(t *testing.T) {
	p := writeProject(t, []string{`{"id":"f-1","issue_type":"task","status":"open"}`})

	if checks := checkJSONLFreshness(p); checks != nil {
		t.Fatalf("no bead DB should produce no checks, got %+v", checks)
	}
}
