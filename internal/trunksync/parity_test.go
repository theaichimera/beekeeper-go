// Tests that close the parity gap with Python test_trunksync.py.
package trunksync

import (
	"os"
	"path/filepath"
	"testing"

	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
)

// Mirrors test_scan_trunk_stale_record_is_not_divergent.
// trunk holds an OLDER copy of the same id (sync newer); not
// divergent because divergence is by ID, not content.
func TestScanTrunkStaleRecordIsNotDivergent(t *testing.T) {
	forceConfigJSONFallback(t)
	repo := initRepo(t, "stale-rec", "beads-sync")
	mustGit(t, repo, "branch", "beads-sync")
	commitOn(t, repo, "beads-sync", `{"id":"b1","status":"open"}`)
	// beads-sync now updates b1 -> closed.
	jsonl := filepath.Join(repo, ".beads", "issues.jsonl")
	must(t, os.WriteFile(jsonl, []byte(`{"id":"b1","status":"closed"}`+"\n"), 0o644))
	mustGit(t, repo, "add", ".beads/issues.jsonl")
	mustGit(t, repo, "commit", "-q", "-m", "b1 closed")
	// Trunk has the OLD b1.
	mustGit(t, repo, "checkout", "-q", "main")
	must(t, os.WriteFile(jsonl, []byte(`{"id":"b1","status":"open"}`+"\n"), 0o644))
	mustGit(t, repo, "add", ".beads/issues.jsonl")
	mustGit(t, repo, "commit", "-q", "-m", "trunk stale b1")

	r := Scan([]string{repo}, 4)
	for _, f := range r.Findings {
		if f.Kind == "divergent-trunk-edit" {
			t.Fatalf("stale-record trunk must not be divergent: %+v", f)
		}
	}
}

// Mirrors test_apply_proceeds_when_trunk_subset_of_sync.
// Apply replays the sync-branch JSONL onto trunk even when the
// trunk has a graph-unique commit whose records are a subset of sync.
func TestApplyProceedsWhenTrunkSubsetOfSync(t *testing.T) {
	forceConfigJSONFallback(t)
	repo := initRepo(t, "subset-apply", "beads-sync")
	mustGit(t, repo, "branch", "beads-sync")
	commitOn(t, repo, "beads-sync", `{"id":"b1","status":"open"}`)
	commitOn(t, repo, "beads-sync", `{"id":"b2","status":"open"}`)
	mustGit(t, repo, "checkout", "-q", "main")
	jsonl := filepath.Join(repo, ".beads", "issues.jsonl")
	must(t, os.WriteFile(jsonl, []byte(`{"id":"b1","status":"open"}`+"\n"), 0o644))
	mustGit(t, repo, "add", ".beads/issues.jsonl")
	mustGit(t, repo, "commit", "-q", "-m", "trunk subset")

	res, err := ApplyOne(bkproject.Project{Root: repo}, false)
	if err != nil {
		t.Fatalf("ApplyOne err=%v", err)
	}
	if !res.Applied {
		t.Fatalf("expected Applied=true; got %+v", res)
	}
	mustGit(t, repo, "checkout", "-q", "main")
	got, _ := os.ReadFile(jsonl)
	mustGit(t, repo, "checkout", "-q", "beads-sync")
	want, _ := os.ReadFile(jsonl)
	if string(got) != string(want) {
		t.Fatalf("trunk JSONL != sync JSONL after apply\ntrunk:%s\nsync:%s", got, want)
	}
}

// Mirrors test_doctor_yellow_on_sync_branch_drift (lives in trunksync's
// test corpus in Python). Asserts doctor.diagnose composes the
// trunk-sync row at YELLOW.
func TestDoctorYellowOnSyncBranchDrift(t *testing.T) {
	forceConfigJSONFallback(t)
	// We test via the doctor module from a separate test in
	// internal/doctor/wiring_test.go. Here we simply re-confirm the
	// trunksync.DiagnoseProject layer hands back YELLOW for ahead.
	repo := initRepo(t, "doc-drift", "beads-sync")
	mustGit(t, repo, "branch", "beads-sync")
	commitOn(t, repo, "beads-sync", `{"id":"k","status":"closed"}`)
	fs := DiagnoseProject(bkproject.Project{Root: repo})
	yellow := false
	for _, f := range fs {
		if f.Severity == YELLOW && f.Kind == "sync-branch-ahead" {
			yellow = true
		}
	}
	if !yellow {
		t.Fatalf("expected sync-branch-ahead YELLOW; got %+v", fs)
	}
}

// Mirrors test_doctor_red_on_divergent_trunk_edit (mirror).
func TestDoctorRedOnDivergentTrunkEdit(t *testing.T) {
	forceConfigJSONFallback(t)
	repo := initRepo(t, "doc-red", "beads-sync")
	mustGit(t, repo, "branch", "beads-sync")
	commitOn(t, repo, "beads-sync", `{"id":"m"}`)
	commitOn(t, repo, "main", `{"id":"n"}`)
	fs := DiagnoseProject(bkproject.Project{Root: repo})
	red := false
	for _, f := range fs {
		if f.Severity == RED && f.Kind == "divergent-trunk-edit" {
			red = true
		}
	}
	if !red {
		t.Fatalf("expected divergent-trunk-edit RED; got %+v", fs)
	}
}
