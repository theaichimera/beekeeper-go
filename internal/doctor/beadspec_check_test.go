package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
)

// writeProject creates a temp project with the given JSONL lines and
// returns it. Mirrors the minimal shape FindProjects would produce.
func writeProject(t *testing.T, lines []string) bkproject.Project {
	t.Helper()
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(beadsDir, "issues.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return bkproject.Project{Root: dir}
}

func TestCheckBeadspec_FlagsViolations(t *testing.T) {
	lines := []string{
		// bad epic: no ## Decisions section.
		`{"id":"x-1","issue_type":"epic","status":"open","description":"# t\n\n## Acceptance\n- ok\n"}`,
		// good epic: Decisions with Why per entry.
		`{"id":"x-2","issue_type":"epic","status":"open","description":"## Decisions\n- Decision: a — Why: b.\n"}`,
		// bad progression: missing the `progression` label.
		`{"id":"x-3","issue_type":"progression","status":"open","description":"## Current understanding\nhere\n\n## Log\n- 2026-06-05 baseline: y.\n"}`,
		// task: no schema, must be ignored.
		`{"id":"x-4","issue_type":"task","status":"open","description":""}`,
		// closed bad epic: must be skipped.
		`{"id":"x-5","issue_type":"epic","status":"closed","description":"nothing"}`,
	}
	checks := checkBeadspec(writeProject(t, lines))
	if len(checks) != 1 {
		t.Fatalf("want exactly 1 bead-schema check, got %d: %+v", len(checks), checks)
	}
	c := checks[0]
	if c.Name != "bead-schema" {
		t.Errorf("check name = %q, want bead-schema", c.Name)
	}
	if c.Severity != YELLOW {
		t.Errorf("severity = %q, want yellow", c.Severity)
	}
	if !strings.Contains(c.Message, "x-1") || !strings.Contains(c.Message, "x-3") {
		t.Errorf("message should cite x-1 and x-3: %q", c.Message)
	}
	if strings.Contains(c.Message, "x-2") {
		t.Errorf("conforming epic x-2 should not appear: %q", c.Message)
	}
}

func TestCheckBeadspec_CleanProjectSilent(t *testing.T) {
	lines := []string{
		`{"id":"y-1","issue_type":"epic","status":"open","description":"## Decisions\n- Decision: a — Why: b.\n"}`,
		`{"id":"y-2","issue_type":"task","status":"open","description":""}`,
	}
	if checks := checkBeadspec(writeProject(t, lines)); checks != nil {
		t.Fatalf("clean project should produce no checks, got %+v", checks)
	}
}
