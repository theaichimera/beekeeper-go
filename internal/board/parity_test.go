// Tests that close the parity gap with Python test_board.py.
package board

import "testing"

// Mirrors test_in_progress_with_assignee_no_lease_gap (Python had the
// no-gap variant separate from the gap variant).
func TestInProgressWithAssigneeNoLeaseGap(t *testing.T) {
	t.Parallel()
	repo := makeWorkspace(t, "ip-ok", []map[string]any{
		{"id": "x.1", "status": "in_progress", "assignee": "alice"},
	})
	p := Scan([]string{repo}, 4).Projects[0]
	if len(p.InProgress) != 1 {
		t.Fatalf("InProgress=%+v", p.InProgress)
	}
	if p.InProgress[0].IsLeaseGap {
		t.Fatal("expected no lease gap")
	}
	if len(p.LeaseGaps()) != 0 {
		t.Fatalf("LeaseGaps=%+v", p.LeaseGaps())
	}
}

// Mirrors test_closed_excluded_but_counted (already partly covered;
// asserts totals["closed"] explicitly).
func TestClosedExcludedButCounted(t *testing.T) {
	t.Parallel()
	repo := makeWorkspace(t, "closed-1", []map[string]any{
		{"id": "x.1", "status": "closed"},
		{"id": "x.2", "status": "closed"},
		{"id": "x.3", "status": "open"},
	})
	r := Scan([]string{repo}, 4)
	if got := r.Totals()["closed"]; got != 2 {
		t.Fatalf("totals.closed=%d want 2", got)
	}
	if got := r.Totals()["ready"]; got != 1 {
		t.Fatalf("totals.ready=%d want 1", got)
	}
}
