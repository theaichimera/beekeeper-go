package board

import (
	"os"
	"path/filepath"
	"testing"
)

func writeBoardProject(t *testing.T, lines []string) string {
	t.Helper()
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "issues.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestExcludesProgressionLabel(t *testing.T) {
	dir := writeBoardProject(t, []string{
		`{"id":"w-1","status":"open","issue_type":"task"}`,
		`{"id":"prog-1","status":"open","issue_type":"task","labels":["progression"]}`,
	})
	r := Scan([]string{dir}, 2)
	if len(r.Projects) != 1 {
		t.Fatalf("want 1 project, got %d", len(r.Projects))
	}
	pb := r.Projects[0]
	if pb.Total != 1 {
		t.Errorf("Total = %d, want 1 (progression excluded)", pb.Total)
	}
	for _, iss := range pb.Ready {
		if iss.ID == "prog-1" {
			t.Errorf("progression bead prog-1 must not appear in Ready")
		}
	}
	if len(pb.Ready) != 1 || pb.Ready[0].ID != "w-1" {
		t.Errorf("Ready should contain only w-1, got %+v", pb.Ready)
	}
}
