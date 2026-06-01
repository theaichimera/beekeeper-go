package doctor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeBeadJSONL is a helper for the stale-WIP wiring tests. We
// duplicate it here (rather than reuse wiring_test.go's) to keep the
// test files self-contained.
func writeBeadJSONLForStale(t *testing.T, repo string, recs []map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repo, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	var body []byte
	for _, r := range recs {
		b, _ := json.Marshal(r)
		body = append(body, b...)
		body = append(body, '\n')
	}
	if err := os.WriteFile(filepath.Join(repo, ".beads", "issues.jsonl"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDoctorStaleWIPYellowWithCount mirrors the demo case: an
// in_progress bead untouched far beyond the threshold should produce
// a YELLOW stale-wip check naming the count, threshold, and oldest.
func TestDoctorStaleWIPYellowWithCount(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := t.TempDir()
	old := time.Now().Add(-30 * 24 * time.Hour).Format(time.RFC3339Nano)
	mid := time.Now().Add(-10 * 24 * time.Hour).Format(time.RFC3339Nano)
	writeBeadJSONLForStale(t, dir, []map[string]any{
		{"id": "x.ancient", "status": "in_progress", "updated_at": old},
		{"id": "x.recent", "status": "in_progress", "updated_at": mid},
		{"id": "x.fresh", "status": "in_progress", "updated_at": time.Now().Format(time.RFC3339Nano)},
	})
	r := RunWithOpts([]string{dir}, Opts{StaleDays: 7})
	var hit *Check
	for i := range r.Projects[0].Checks {
		if r.Projects[0].Checks[i].Name == "stale-wip" {
			hit = &r.Projects[0].Checks[i]
			break
		}
	}
	if hit == nil {
		t.Fatalf("missing stale-wip check; checks=%+v", r.Projects[0].Checks)
	}
	if hit.Severity != YELLOW {
		t.Fatalf("severity=%s want YELLOW", hit.Severity)
	}
	for _, want := range []string{">7d", "x.ancient", "in_progress"} {
		if !strings.Contains(hit.Message, want) {
			t.Fatalf("missing %q in message: %q", want, hit.Message)
		}
	}
}

// TestDoctorStaleWIPSilentWhenClean: zero offenders, no row.
func TestDoctorStaleWIPSilentWhenClean(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := t.TempDir()
	writeBeadJSONLForStale(t, dir, []map[string]any{
		{"id": "x.fresh", "status": "in_progress", "updated_at": time.Now().Format(time.RFC3339Nano)},
		{"id": "x.closed", "status": "closed"},
	})
	r := RunWithOpts([]string{dir}, Opts{StaleDays: 7})
	for _, c := range r.Projects[0].Checks {
		if c.Name == "stale-wip" {
			t.Fatalf("stale-wip leaked on clean repo: %+v", c)
		}
	}
}

// TestDoctorStaleWIPDisabledByNegative: negative threshold turns the
// check off entirely.
func TestDoctorStaleWIPDisabledByNegative(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := t.TempDir()
	old := time.Now().Add(-30 * 24 * time.Hour).Format(time.RFC3339Nano)
	writeBeadJSONLForStale(t, dir, []map[string]any{
		{"id": "x.ancient", "status": "in_progress", "updated_at": old},
	})
	r := RunWithOpts([]string{dir}, Opts{StaleDays: -1})
	for _, c := range r.Projects[0].Checks {
		if c.Name == "stale-wip" {
			t.Fatalf("stale-wip ran despite -1 threshold: %+v", c)
		}
	}
}

// TestDoctorStaleWIPHandlesRealBeadTimestampLiteral uses the literal
// updated_at string from the bkg-bqa.2 acceptance criteria —
// `2026-05-06T16:50:40.133129+03:00` — RFC3339 with TZ offset AND
// fractional seconds, which jq's fromdateiso8601 rejects.
func TestDoctorStaleWIPHandlesRealBeadTimestampLiteral(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := t.TempDir()
	writeBeadJSONLForStale(t, dir, []map[string]any{
		{
			"id":         "demo-6ml",
			"status":     "in_progress",
			"updated_at": "2026-05-06T16:50:40.133129+03:00",
		},
	})
	// The literal is in May; "today" in this test is whatever the
	// runner thinks. As long as it's >7 days after May 6, the check
	// fires. RFC3339Nano parsing must succeed.
	r := RunWithOpts([]string{dir}, Opts{StaleDays: 7})
	hit := false
	for _, c := range r.Projects[0].Checks {
		if c.Name == "stale-wip" && strings.Contains(c.Message, "demo-6ml") {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("expected stale-wip on demo-6ml; checks=%+v", r.Projects[0].Checks)
	}
}
