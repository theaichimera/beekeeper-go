package board

import (
	"testing"
)

func TestProjectAggregateByStatusAndPriority(t *testing.T) {
	t.Parallel()
	repo := makeWorkspace(t, "agg", []map[string]any{
		{"id": "x.1", "status": "open", "priority": 1},
		{"id": "x.2", "status": "open", "priority": 2},
		{"id": "x.3", "status": "in_progress", "priority": 0},
		{"id": "x.4", "status": "closed", "priority": 4},
		{"id": "x.5", "status": "closed"}, // no priority
	})
	r := Scan([]string{repo}, 4)
	if len(r.Projects) != 1 {
		t.Fatalf("projects=%d", len(r.Projects))
	}
	p := r.Projects[0]
	if p.Total != 5 {
		t.Fatalf("Total=%d want 5", p.Total)
	}
	wantStatus := map[string]int{"open": 2, "in_progress": 1, "closed": 2}
	for k, v := range wantStatus {
		if p.ByStatus[k] != v {
			t.Fatalf("ByStatus[%s]=%d want %d (full=%v)", k, p.ByStatus[k], v, p.ByStatus)
		}
	}
	// Priority counts are NON-CLOSED only.
	wantPri := map[int]int{0: 1, 1: 1, 2: 1}
	for k, v := range wantPri {
		if p.ActiveByPriority[k] != v {
			t.Fatalf("ActiveByPriority[%d]=%d want %d (full=%v)",
				k, p.ActiveByPriority[k], v, p.ActiveByPriority)
		}
	}
	if p.ActiveByPriority[4] != 0 {
		t.Fatalf("closed P4 leaked into active priority")
	}
}

func TestReportAggregatePercentComplete(t *testing.T) {
	t.Parallel()
	repo := makeWorkspace(t, "pct", []map[string]any{
		{"id": "x.1", "status": "open"},
		{"id": "x.2", "status": "open"},
		{"id": "x.3", "status": "closed"},
		{"id": "x.4", "status": "closed"},
	})
	s := Scan([]string{repo}, 4).Aggregate()
	if s.Total != 4 {
		t.Fatalf("Total=%d", s.Total)
	}
	if s.ByStatus["closed"] != 2 || s.ByStatus["open"] != 2 {
		t.Fatalf("ByStatus=%v", s.ByStatus)
	}
	if s.PercentComplete < 49.99 || s.PercentComplete > 50.01 {
		t.Fatalf("PercentComplete=%f want ~50", s.PercentComplete)
	}
}

func TestReportAggregateZeroDivisionSafe(t *testing.T) {
	t.Parallel()
	repo := makeWorkspace(t, "empty", nil)
	s := Scan([]string{repo}, 4).Aggregate()
	if s.Total != 0 {
		t.Fatalf("Total=%d", s.Total)
	}
	if s.PercentComplete != 0 {
		t.Fatalf("PercentComplete=%f", s.PercentComplete)
	}
}

func TestReportAggregateMultiProjectFolds(t *testing.T) {
	t.Parallel()
	repoA := makeWorkspace(t, "a", []map[string]any{
		{"id": "a.1", "status": "open", "priority": 1},
		{"id": "a.2", "status": "closed"},
	})
	repoB := makeWorkspace(t, "b", []map[string]any{
		{"id": "b.1", "status": "in_progress", "priority": 0},
	})
	s := Scan([]string{repoA, repoB}, 4).Aggregate()
	if s.Total != 3 {
		t.Fatalf("Total=%d", s.Total)
	}
	if s.ByStatus["open"] != 1 || s.ByStatus["closed"] != 1 || s.ByStatus["in_progress"] != 1 {
		t.Fatalf("ByStatus=%v", s.ByStatus)
	}
	if s.ActiveByPriority[0] != 1 || s.ActiveByPriority[1] != 1 {
		t.Fatalf("ActiveByPriority=%v", s.ActiveByPriority)
	}
}
