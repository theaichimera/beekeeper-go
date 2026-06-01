package stalewip

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeWorkspace(t *testing.T, name string, recs []map[string]any) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	var body []byte
	for _, r := range recs {
		b, _ := json.Marshal(r)
		body = append(body, b...)
		body = append(body, '\n')
	}
	if err := os.WriteFile(filepath.Join(dir, ".beads", "issues.jsonl"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// `2026-05-06T16:50:40.133129+03:00` is the literal example from the
// epic spec — RFC3339 with TZ offset AND fractional seconds.
const realBeadTimestampLiteral = "2026-05-06T16:50:40.133129+03:00"

func TestParseTimestampHandlesFractionalSecondsAndOffset(t *testing.T) {
	t.Parallel()
	got, ok := ParseTimestamp(realBeadTimestampLiteral)
	if !ok {
		t.Fatalf("failed to parse %q (the exact literal jq's fromdateiso8601 chokes on)", realBeadTimestampLiteral)
	}
	if got.Hour() != 16 || got.Minute() != 50 || got.Second() != 40 {
		t.Fatalf("wrong time: %v", got)
	}
	// `+03:00` offset must shift to UTC: 16:50 +03 -> 13:50 UTC.
	if got.UTC().Hour() != 13 {
		t.Fatalf("offset not honored: utc=%v", got.UTC())
	}
}

func TestParseTimestampZulu(t *testing.T) {
	t.Parallel()
	if _, ok := ParseTimestamp("2026-06-01T00:00:00Z"); !ok {
		t.Fatal("zulu RFC3339 should parse")
	}
}

func TestParseTimestampEmptyAndJunk(t *testing.T) {
	t.Parallel()
	if _, ok := ParseTimestamp(""); ok {
		t.Fatal("empty must not parse")
	}
	if _, ok := ParseTimestamp("not-a-date"); ok {
		t.Fatal("junk must not parse")
	}
}

func TestScanFlagsStaleInProgress(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	repo := writeWorkspace(t, "stale", []map[string]any{
		{
			"id":         "x.fresh",
			"status":     "in_progress",
			"updated_at": now.Add(-3 * 24 * time.Hour).Format(time.RFC3339Nano),
		},
		{
			"id":         "x.stale",
			"status":     "in_progress",
			"updated_at": now.Add(-9 * 24 * time.Hour).Format(time.RFC3339Nano),
		},
		{
			"id":         "x.ancient",
			"status":     "in_progress",
			"updated_at": realBeadTimestampLiteral, // ~26 days before now
			"assignee":   "alice",
		},
		{
			"id":         "x.open-stale",
			"status":     "open", // status filter excludes
			"updated_at": now.Add(-30 * 24 * time.Hour).Format(time.RFC3339Nano),
		},
	})
	r := Scan([]string{repo}, 4, 7, now)
	if r.Count() != 2 {
		t.Fatalf("count=%d want 2 (offenders=%+v)", r.Count(), r.Stale)
	}
	// Oldest first.
	if r.Stale[0].ID != "x.ancient" || r.Stale[1].ID != "x.stale" {
		t.Fatalf("ordering: %+v", r.Stale)
	}
	if r.Stale[0].Assignee != "alice" {
		t.Fatalf("assignee not propagated: %+v", r.Stale[0])
	}
	if r.Stale[0].AgeDays < 25 || r.Stale[0].AgeDays > 27 {
		t.Fatalf("ancient AgeDays=%v want ~26", r.Stale[0].AgeDays)
	}
}

func TestScanThresholdZeroDisables(t *testing.T) {
	t.Parallel()
	repo := writeWorkspace(t, "off", []map[string]any{
		{
			"id":         "x.1",
			"status":     "in_progress",
			"updated_at": "2025-01-01T00:00:00Z", // very old
		},
	})
	r := Scan([]string{repo}, 4, 0, time.Now())
	if r.Count() != 0 {
		t.Fatalf("threshold 0 should disable; count=%d", r.Count())
	}
}

func TestScanSkipsRecordsWithoutTimestamp(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	repo := writeWorkspace(t, "no-ts", []map[string]any{
		{"id": "x.1", "status": "in_progress"}, // no updated_at
	})
	r := Scan([]string{repo}, 4, 7, now)
	if r.Count() != 0 {
		t.Fatalf("missing timestamp should abstain; got %+v", r.Stale)
	}
}

func TestScanCleanRepoReturnsEmpty(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	repo := writeWorkspace(t, "clean", []map[string]any{
		{
			"id":         "x.1",
			"status":     "in_progress",
			"updated_at": now.Add(-2 * 24 * time.Hour).Format(time.RFC3339Nano),
		},
	})
	r := Scan([]string{repo}, 4, 7, now)
	if r.Count() != 0 {
		t.Fatalf("count=%d want 0", r.Count())
	}
}
