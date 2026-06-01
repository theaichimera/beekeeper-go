package prbeads

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo builds a workspace at tmp/<name> with an empty bead JSONL
// committed on `main`. Caller writes JSONL via writeJSONL+commit.
func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, ".beads"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, ".beads", "issues.jsonl"), nil, 0o644))
	mustGit(t, dir, "init", "-q", "-b", "main")
	mustGit(t, dir, "config", "user.email", "t@t")
	mustGit(t, dir, "config", "user.name", "t")
	mustGit(t, dir, "add", ".beads/issues.jsonl")
	mustGit(t, dir, "commit", "-q", "-m", "init")
	return dir
}

// writeAndCommitJSONL replaces the JSONL with `recs` (one record per
// line) and commits on `branch`. Branch is created if absent. When
// the resulting tree is identical to HEAD (no-op edit), the function
// silently skips the commit — handy for tests that branch off a
// snapshot without diverging it.
func writeAndCommitJSONL(t *testing.T, repo, branch string, recs []map[string]any) {
	t.Helper()
	if branchExists(repo, branch) {
		mustGit(t, repo, "checkout", "-q", branch)
	} else {
		mustGit(t, repo, "checkout", "-q", "-b", branch)
	}
	var sb strings.Builder
	for _, rec := range recs {
		b, _ := json.Marshal(rec)
		sb.Write(b)
		sb.WriteByte('\n')
	}
	must(t, os.WriteFile(filepath.Join(repo, ".beads", "issues.jsonl"), []byte(sb.String()), 0o644))
	mustGit(t, repo, "add", ".beads/issues.jsonl")
	c := exec.Command("git", "diff", "--cached", "--quiet")
	c.Dir = repo
	if c.Run() == nil {
		return // nothing staged
	}
	mustGit(t, repo, "commit", "-q", "-m", "edit on "+branch)
}

func branchExists(repo, branch string) bool {
	c := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	c.Dir = repo
	return c.Run() == nil
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// rec builds a minimal bead record for the in-memory regression-detector
// tests. The unit tests in this file never need to commit JSONL —
// they call regressions() directly with id-keyed maps.
func rec(fields ...string) map[string]any {
	out := map[string]any{}
	for i := 0; i < len(fields); i += 2 {
		out[fields[i]] = fields[i+1]
	}
	return out
}

// --- regressions() unit tests (no git) ---------------------------------

func TestRegressionsTable(t *testing.T) {
	cases := []struct {
		name      string
		base      map[string]map[string]any
		head      map[string]map[string]any
		wantKinds []string
	}{
		{
			name: "forward-only is clean",
			base: map[string]map[string]any{
				"a-1": rec("id", "a-1", "status", "open"),
			},
			head: map[string]map[string]any{
				"a-1": rec("id", "a-1", "status", "in_progress"),
				"a-2": rec("id", "a-2", "status", "open"),
			},
			wantKinds: nil,
		},
		{
			name: "status rewind is RED",
			base: map[string]map[string]any{
				"a-1": rec("id", "a-1", "status", "in_progress"),
			},
			head: map[string]map[string]any{
				"a-1": rec("id", "a-1", "status", "open"),
			},
			wantKinds: []string{KindStatusRewind},
		},
		{
			name: "closed -> open is RED",
			base: map[string]map[string]any{
				"a-1": rec("id", "a-1", "status", "closed"),
			},
			head: map[string]map[string]any{
				"a-1": rec("id", "a-1", "status", "open"),
			},
			wantKinds: []string{KindStatusRewind},
		},
		{
			name: "closed -> in_progress is RED",
			base: map[string]map[string]any{
				"a-1": rec("id", "a-1", "status", "closed"),
			},
			head: map[string]map[string]any{
				"a-1": rec("id", "a-1", "status", "in_progress"),
			},
			wantKinds: []string{KindStatusRewind},
		},
		{
			name: "in_progress<->blocked is not a rewind (same rank)",
			base: map[string]map[string]any{
				"a-1": rec("id", "a-1", "status", "in_progress"),
			},
			head: map[string]map[string]any{
				"a-1": rec("id", "a-1", "status", "blocked"),
			},
			wantKinds: nil,
		},
		{
			name: "assignee dropped is RED",
			base: map[string]map[string]any{
				"a-1": rec("id", "a-1", "status", "open", "assignee", "alice"),
			},
			head: map[string]map[string]any{
				"a-1": rec("id", "a-1", "status", "open", "assignee", ""),
			},
			wantKinds: []string{KindAssigneeRewind},
		},
		{
			name: "assignee changed is RED",
			base: map[string]map[string]any{
				"a-1": rec("id", "a-1", "status", "open", "assignee", "alice"),
			},
			head: map[string]map[string]any{
				"a-1": rec("id", "a-1", "status", "open", "assignee", "bob"),
			},
			wantKinds: []string{KindAssigneeRewind},
		},
		{
			name: "assignee added is fine (base empty)",
			base: map[string]map[string]any{
				"a-1": rec("id", "a-1", "status", "open"),
			},
			head: map[string]map[string]any{
				"a-1": rec("id", "a-1", "status", "open", "assignee", "alice"),
			},
			wantKinds: nil,
		},
		{
			name: "dropped record is RED",
			base: map[string]map[string]any{
				"a-1": rec("id", "a-1", "status", "open"),
			},
			head:      map[string]map[string]any{},
			wantKinds: []string{KindDroppedRecord},
		},
		{
			name: "stale updated_at is RED",
			base: map[string]map[string]any{
				"a-1": rec("id", "a-1", "status", "open", "updated_at", "2026-05-30T12:00:00Z"),
			},
			head: map[string]map[string]any{
				"a-1": rec("id", "a-1", "status", "open", "updated_at", "2026-05-29T12:00:00Z"),
			},
			wantKinds: []string{KindStaleTimestamp},
		},
		{
			name: "newer updated_at on head is fine",
			base: map[string]map[string]any{
				"a-1": rec("id", "a-1", "status", "open", "updated_at", "2026-05-29T12:00:00Z"),
			},
			head: map[string]map[string]any{
				"a-1": rec("id", "a-1", "status", "open", "updated_at", "2026-05-30T12:00:00Z"),
			},
			wantKinds: nil,
		},
		{
			name: "missing updated_at on one side abstains",
			base: map[string]map[string]any{
				"a-1": rec("id", "a-1", "status", "open", "updated_at", "2026-05-30T12:00:00Z"),
			},
			head: map[string]map[string]any{
				"a-1": rec("id", "a-1", "status", "open"),
			},
			wantKinds: nil,
		},
		{
			name: "PR13 incident: status rewind + assignee drop",
			base: map[string]map[string]any{
				"queryparser-24": rec(
					"id", "queryparser-24",
					"status", "in_progress",
					"assignee", "queryparser-agent",
				),
			},
			head: map[string]map[string]any{
				"queryparser-24": rec(
					"id", "queryparser-24",
					"status", "open",
				),
			},
			wantKinds: []string{KindStatusRewind, KindAssigneeRewind},
		},
		{
			name: "multiple ids surfaced in deterministic order",
			base: map[string]map[string]any{
				"b-2": rec("id", "b-2", "status", "in_progress"),
				"a-1": rec("id", "a-1", "status", "in_progress"),
			},
			head: map[string]map[string]any{
				"a-1": rec("id", "a-1", "status", "open"),
				"b-2": rec("id", "b-2", "status", "open"),
			},
			wantKinds: []string{KindStatusRewind, KindStatusRewind},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := regressions(tc.base, tc.head)
			if len(got) != len(tc.wantKinds) {
				t.Fatalf("kinds=%v want %v", kinds(got), tc.wantKinds)
			}
			for i, k := range tc.wantKinds {
				if got[i].Kind != k {
					t.Fatalf("findings[%d]=%s want %s (got=%v)", i, got[i].Kind, k, kinds(got))
				}
				if got[i].Severity != RED {
					t.Fatalf("findings[%d].Severity=%s want RED", i, got[i].Severity)
				}
			}
		})
	}
}

func kinds(fs []Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Kind
	}
	return out
}

// --- Diagnose() integration tests (real git refs) ----------------------

func TestDiagnoseForwardOnlyIsGreen(t *testing.T) {
	repo := initRepo(t)
	writeAndCommitJSONL(t, repo, "main", []map[string]any{
		{"id": "a-1", "status": "open"},
	})
	writeAndCommitJSONL(t, repo, "feature/x", []map[string]any{
		{"id": "a-1", "status": "open"},
		{"id": "a-2", "status": "open"},
	})
	r, err := Diagnose(repo, "main", "feature/x", PolicyRegression)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if r.Worst() != GREEN {
		t.Fatalf("worst=%s findings=%+v", r.Worst(), r.Findings)
	}
}

func TestDiagnoseStatusRewindIsRed(t *testing.T) {
	repo := initRepo(t)
	// Branch first so it carries the snapshot, then advance main.
	writeAndCommitJSONL(t, repo, "main", []map[string]any{
		{"id": "a-1", "status": "open"},
	})
	writeAndCommitJSONL(t, repo, "feature/stale", []map[string]any{
		{"id": "a-1", "status": "open"}, // branch keeps old state
	})
	writeAndCommitJSONL(t, repo, "main", []map[string]any{
		{"id": "a-1", "status": "in_progress", "assignee": "alice"},
	})
	r, err := Diagnose(repo, "main", "feature/stale", PolicyRegression)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if r.Worst() != RED {
		t.Fatalf("worst=%s findings=%+v", r.Worst(), r.Findings)
	}
	wantKinds := map[string]bool{KindStatusRewind: false, KindAssigneeRewind: false}
	for _, f := range r.Findings {
		if _, ok := wantKinds[f.Kind]; ok {
			wantKinds[f.Kind] = true
		}
	}
	for k, hit := range wantKinds {
		if !hit {
			t.Fatalf("missing finding kind %s in %+v", k, r.Findings)
		}
	}
}

func TestDiagnosePolicyNoBeadsFailsOnAnyJSONLDiff(t *testing.T) {
	repo := initRepo(t)
	writeAndCommitJSONL(t, repo, "main", []map[string]any{
		{"id": "a-1", "status": "open"},
	})
	// A pure forward-only edit on the branch — no regression — but
	// no-beads must still catch it.
	writeAndCommitJSONL(t, repo, "feature/touchy", []map[string]any{
		{"id": "a-1", "status": "open"},
		{"id": "a-2", "status": "open"},
	})
	r, err := Diagnose(repo, "main", "feature/touchy", PolicyNoBeads)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if r.Worst() != RED {
		t.Fatalf("worst=%s findings=%+v", r.Worst(), r.Findings)
	}
	found := false
	for _, f := range r.Findings {
		if f.Kind == KindNoBeadsPolicy {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no-beads finding missing in %+v", r.Findings)
	}
}

func TestDiagnoseMissingRefErrors(t *testing.T) {
	repo := initRepo(t)
	if _, err := Diagnose(repo, "main", "does-not-exist", PolicyRegression); err == nil {
		t.Fatal("expected error for missing ref")
	}
}
