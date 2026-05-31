package board

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func makeWorkspace(t *testing.T, name string, recs []map[string]any) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if recs == nil {
		_ = os.WriteFile(filepath.Join(dir, ".beads", "issues.jsonl"), nil, 0o644)
	} else {
		var body []byte
		for _, r := range recs {
			b, _ := json.Marshal(r)
			body = append(body, b...)
			body = append(body, '\n')
		}
		_ = os.WriteFile(filepath.Join(dir, ".beads", "issues.jsonl"), body, 0o644)
	}
	return dir
}

func TestOpenNoDepsIsReady(t *testing.T) {
	t.Parallel()
	repo := makeWorkspace(t, "ready", []map[string]any{
		{"id": "x.1", "status": "open"},
	})
	r := Scan([]string{repo}, 4)
	if len(r.Projects) != 1 {
		t.Fatalf("projects=%d", len(r.Projects))
	}
	p := r.Projects[0]
	if len(p.Ready) != 1 || p.Ready[0].ID != "x.1" {
		t.Fatalf("Ready=%+v", p.Ready)
	}
}

func TestBlockedByOpenDep(t *testing.T) {
	t.Parallel()
	repo := makeWorkspace(t, "blocked", []map[string]any{
		{"id": "x.1", "status": "open"},
		{
			"id":     "x.2",
			"status": "open",
			"dependencies": []any{
				map[string]any{"issue_id": "x.2", "depends_on_id": "x.1", "type": "blocks"},
			},
		},
	})
	p := Scan([]string{repo}, 4).Projects[0]
	if len(p.Blocked) != 1 || p.Blocked[0].ID != "x.2" {
		t.Fatalf("Blocked=%+v", p.Blocked)
	}
	if len(p.Ready) != 1 || p.Ready[0].ID != "x.1" {
		t.Fatalf("Ready=%+v", p.Ready)
	}
}

func TestUnblockedWhenDepClosed(t *testing.T) {
	t.Parallel()
	repo := makeWorkspace(t, "unblocked", []map[string]any{
		{"id": "x.1", "status": "closed"},
		{
			"id":     "x.2",
			"status": "open",
			"dependencies": []any{
				map[string]any{"issue_id": "x.2", "depends_on_id": "x.1", "type": "blocks"},
			},
		},
	})
	p := Scan([]string{repo}, 4).Projects[0]
	if len(p.Ready) != 1 || p.Ready[0].ID != "x.2" {
		t.Fatalf("Ready=%+v", p.Ready)
	}
	if p.ClosedCount != 1 {
		t.Fatalf("ClosedCount=%d", p.ClosedCount)
	}
}

func TestParentChildIsNotBlocking(t *testing.T) {
	t.Parallel()
	repo := makeWorkspace(t, "epic", []map[string]any{
		{"id": "epic", "status": "open"},
		{
			"id":     "child",
			"status": "open",
			"dependencies": []any{
				map[string]any{"issue_id": "child", "depends_on_id": "epic", "type": "parent-child"},
			},
		},
	})
	p := Scan([]string{repo}, 4).Projects[0]
	ids := map[string]bool{}
	for _, i := range p.Ready {
		ids[i.ID] = true
	}
	if !ids["epic"] || !ids["child"] || len(p.Blocked) != 0 {
		t.Fatalf("Ready=%+v Blocked=%+v", p.Ready, p.Blocked)
	}
}

func TestInProgressLeaseGap(t *testing.T) {
	t.Parallel()
	repo := makeWorkspace(t, "ip-gap", []map[string]any{
		{"id": "a", "status": "in_progress", "assignee": "alice"},
		{"id": "b", "status": "in_progress"},
		{"id": "c", "status": "in_progress", "assignee": ""},
	})
	p := Scan([]string{repo}, 4).Projects[0]
	if len(p.InProgress) != 3 {
		t.Fatalf("InProgress=%+v", p.InProgress)
	}
	gaps := p.LeaseGaps()
	if len(gaps) != 2 {
		t.Fatalf("gaps=%+v", gaps)
	}
}

func TestUnresolvedBlockerIsNonBlocking(t *testing.T) {
	t.Parallel()
	repo := makeWorkspace(t, "unresolved", []map[string]any{
		{
			"id":     "x.1",
			"status": "open",
			"dependencies": []any{
				map[string]any{"issue_id": "x.1", "depends_on_id": "missing.999", "type": "blocks"},
			},
		},
	})
	p := Scan([]string{repo}, 4).Projects[0]
	if len(p.Ready) != 1 {
		t.Fatalf("Ready=%+v", p.Ready)
	}
	if len(p.Ready[0].UnresolvedBlockers) != 1 || p.Ready[0].UnresolvedBlockers[0] != "missing.999" {
		t.Fatalf("UnresolvedBlockers=%v", p.Ready[0].UnresolvedBlockers)
	}
}

func TestMultipleBlocksBlockedIfAnyOpen(t *testing.T) {
	t.Parallel()
	repo := makeWorkspace(t, "multi", []map[string]any{
		{"id": "a", "status": "closed"},
		{"id": "b", "status": "open"},
		{
			"id":     "x",
			"status": "open",
			"dependencies": []any{
				map[string]any{"issue_id": "x", "depends_on_id": "a", "type": "blocks"},
				map[string]any{"issue_id": "x", "depends_on_id": "b", "type": "blocks"},
			},
		},
	})
	p := Scan([]string{repo}, 4).Projects[0]
	if len(p.Blocked) != 1 || p.Blocked[0].ID != "x" {
		t.Fatalf("Blocked=%+v", p.Blocked)
	}
}

func TestStatusBlockedString(t *testing.T) {
	t.Parallel()
	repo := makeWorkspace(t, "blk", []map[string]any{
		{"id": "x.1", "status": "blocked"},
	})
	p := Scan([]string{repo}, 4).Projects[0]
	if len(p.Blocked) != 1 {
		t.Fatalf("Blocked=%+v", p.Blocked)
	}
}

func TestUnknownStatusGoesToOther(t *testing.T) {
	t.Parallel()
	repo := makeWorkspace(t, "weird", []map[string]any{
		{"id": "x.1", "status": "frobnicated"},
		{"id": "x.2", "status": "open"},
	})
	r := Scan([]string{repo}, 4)
	p := r.Projects[0]
	if len(p.Ready) != 1 || p.Ready[0].ID != "x.2" {
		t.Fatalf("Ready=%+v", p.Ready)
	}
	if r.Totals()["other"] != 1 {
		t.Fatalf("totals.other=%d want 1", r.Totals()["other"])
	}
}

func TestCrossProjectOrderingAndPriority(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	a := filepath.Join(root, "proj-a")
	b := filepath.Join(root, "proj-b")
	_ = os.MkdirAll(filepath.Join(a, ".beads"), 0o755)
	_ = os.MkdirAll(filepath.Join(b, ".beads"), 0o755)
	bodyA, _ := json.Marshal(map[string]any{"id": "a.2", "status": "open", "priority": 2})
	bodyA1, _ := json.Marshal(map[string]any{"id": "a.1", "status": "open", "priority": 1})
	_ = os.WriteFile(filepath.Join(a, ".beads", "issues.jsonl"), append(append(bodyA, '\n'), append(bodyA1, '\n')...), 0o644)
	bodyB, _ := json.Marshal(map[string]any{"id": "b.1", "status": "open", "priority": 0})
	_ = os.WriteFile(filepath.Join(b, ".beads", "issues.jsonl"), append(bodyB, '\n'), 0o644)

	r := Scan([]string{a, b}, 4)
	if len(r.Projects) != 2 {
		t.Fatalf("projects=%d", len(r.Projects))
	}
	// Projects sorted by root path.
	if r.Projects[0].ProjectRoot >= r.Projects[1].ProjectRoot {
		t.Fatalf("projects not sorted: %+v", r.Projects)
	}
	// Within proj-a, Ready is by priority asc then id.
	var aBoard ProjectBoard
	for _, p := range r.Projects {
		if filepath.Base(p.ProjectRoot) == "proj-a" {
			aBoard = p
		}
	}
	if aBoard.Ready[0].ID != "a.1" || aBoard.Ready[1].ID != "a.2" {
		t.Fatalf("Ready order: %+v", aBoard.Ready)
	}
}

func TestMalformedLinesSkipped(t *testing.T) {
	t.Parallel()
	repo := filepath.Join(t.TempDir(), "malformed")
	_ = os.MkdirAll(filepath.Join(repo, ".beads"), 0o755)
	body := []byte(
		`{"id":"x.1","status":"open"}` + "\n" +
			"this is not json\n" +
			`"a string, not a dict"` + "\n" +
			`{"id":"x.2","status":"open"}` + "\n")
	_ = os.WriteFile(filepath.Join(repo, ".beads", "issues.jsonl"), body, 0o644)
	p := Scan([]string{repo}, 4).Projects[0]
	if len(p.Ready) != 2 {
		t.Fatalf("Ready=%+v", p.Ready)
	}
}

func TestEmptyJSONLSafe(t *testing.T) {
	t.Parallel()
	repo := makeWorkspace(t, "empty", []map[string]any{})
	p := Scan([]string{repo}, 4).Projects[0]
	if len(p.Ready) != 0 || len(p.InProgress) != 0 || len(p.Blocked) != 0 || p.ClosedCount != 0 {
		t.Fatalf("expected empty board; got %+v", p)
	}
}

func TestMissingJSONLSafe(t *testing.T) {
	t.Parallel()
	repo := filepath.Join(t.TempDir(), "no-jsonl")
	_ = os.MkdirAll(filepath.Join(repo, ".beads"), 0o755)
	r := Scan([]string{repo}, 4)
	if len(r.Projects) != 1 {
		t.Fatalf("expected one project, got %d", len(r.Projects))
	}
}
