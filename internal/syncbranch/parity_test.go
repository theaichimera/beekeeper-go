// Tests that close the parity gap with Python test_syncbranch.py.
package syncbranch

import (
	"os"
	"path/filepath"
	"testing"
)

// Mirrors test_guard_main_branch_with_bead_commits_is_red.
func TestStrandedCommitOnMainBranchIsRed(t *testing.T) {
	forceConfigJSONFallback(t)
	repo := initRepo(t, "main-stranded", "beads-sync")
	mustGit(t, repo, "branch", "beads-sync")
	commitOn(t, repo, "main", `{"id":"a","status":"open"}`)
	r := Scan([]string{repo}, 4)
	if r.Worst() != RED {
		t.Fatalf("worst=%v want RED; %+v", r.Worst(), r.Findings)
	}
	branches := map[string]bool{}
	for _, f := range r.Findings {
		if f.Kind == "stranded-bead-commit" {
			branches[f.Branch] = true
		}
	}
	if !branches["main"] {
		t.Fatalf("expected main flagged; got %v", branches)
	}
}

// Mirrors test_guard_trunk_identical_to_sync_is_not_stranded.
// Trunk and sync point at THE SAME blob — never stranded.
func TestTrunkIdenticalToSyncIsNotStranded(t *testing.T) {
	forceConfigJSONFallback(t)
	repo := initRepo(t, "ident", "beads-sync")
	mustGit(t, repo, "branch", "beads-sync")
	commitOn(t, repo, "beads-sync", `{"id":"x","status":"open"}`)
	// Make main include the same content via cherry-pick-equivalent
	// (write the same bytes + commit).
	mustGit(t, repo, "checkout", "-q", "main")
	jsonl := filepath.Join(repo, ".beads", "issues.jsonl")
	must(t, os.WriteFile(jsonl, []byte(`{"id":"x","status":"open"}`+"\n"), 0o644))
	mustGit(t, repo, "add", ".beads/issues.jsonl")
	mustGit(t, repo, "commit", "-q", "-m", "trunk identical")
	r := Scan([]string{repo}, 4)
	for _, f := range r.Findings {
		if f.Kind == "stranded-bead-commit" {
			t.Fatalf("identical-content trunk flagged stranded: %+v", f)
		}
	}
}

// Mirrors test_guard_trunk_stale_record_is_not_stranded.
func TestTrunkStaleRecordIsNotStranded(t *testing.T) {
	forceConfigJSONFallback(t)
	repo := initRepo(t, "stale", "beads-sync")
	mustGit(t, repo, "branch", "beads-sync")
	commitOn(t, repo, "beads-sync", `{"id":"a","status":"open"}`)
	// Update beads-sync's a -> closed.
	jsonl := filepath.Join(repo, ".beads", "issues.jsonl")
	must(t, os.WriteFile(jsonl, []byte(`{"id":"a","status":"closed"}`+"\n"), 0o644))
	mustGit(t, repo, "add", ".beads/issues.jsonl")
	mustGit(t, repo, "commit", "-q", "-m", "a -> closed")
	// Trunk has stale a.
	mustGit(t, repo, "checkout", "-q", "main")
	must(t, os.WriteFile(jsonl, []byte(`{"id":"a","status":"open"}`+"\n"), 0o644))
	mustGit(t, repo, "add", ".beads/issues.jsonl")
	mustGit(t, repo, "commit", "-q", "-m", "trunk stale a")
	r := Scan([]string{repo}, 4)
	for _, f := range r.Findings {
		if f.Kind == "stranded-bead-commit" {
			t.Fatalf("stale-record trunk should not be stranded: %+v", f)
		}
	}
}

// Mirrors test_doctor_red_when_stranded_bead_commits — verify the
// per-package signal that doctor surfaces. The doctor wiring test
// lives in internal/doctor; here we just confirm the syncbranch
// layer exposes the RED finding.
func TestDoctorSignalRedWhenStranded(t *testing.T) {
	forceConfigJSONFallback(t)
	repo := initRepo(t, "doc-red", "beads-sync")
	mustGit(t, repo, "branch", "beads-sync")
	commitOn(t, repo, "feature/bad", `{"id":"x"}`)
	r := Scan([]string{repo}, 4)
	if r.Worst() != RED {
		t.Fatalf("worst=%v want RED; %+v", r.Worst(), r.Findings)
	}
}

// Mirrors test_doctor_green_when_no_stranding (mirror).
func TestDoctorSignalGreenWhenClean(t *testing.T) {
	forceConfigJSONFallback(t)
	repo := initRepo(t, "doc-green", "beads-sync")
	mustGit(t, repo, "branch", "beads-sync")
	commitOn(t, repo, "beads-sync", `{"id":"a","status":"open"}`)
	r := Scan([]string{repo}, 4)
	if r.Worst() != GREEN {
		t.Fatalf("worst=%v want GREEN; %+v", r.Worst(), r.Findings)
	}
}
