package main

import (
	"strings"
	"testing"

	"github.com/theaichimera/beekeeper-go/pkg/beadspec"
)

func TestNewProgressionBody_PassesSchema(t *testing.T) {
	body := newProgressionBody("Beadspec rollout", "2026-06-05")
	s, err := beadspec.Load("progression")
	if err != nil {
		t.Fatalf("Load(progression): %v", err)
	}
	bead := beadspec.Bead{
		ID:     "p-1",
		Type:   "progression",
		Labels: []string{progressionLabel},
		Body:   body,
	}
	if fs := beadspec.Validate(bead, s); len(fs) != 0 {
		t.Fatalf("scaffolded progression should pass schema, got findings: %+v", fs)
	}
}

func TestAppendLogEntry(t *testing.T) {
	body := newProgressionBody("Topic", "2026-06-05")
	got := appendLogEntry(body, "2026-06-06", "pivot", "changed direction")

	if !strings.Contains(got, "- 2026-06-06 pivot: changed direction") {
		t.Errorf("entry not appended:\n%s", got)
	}
	// The new entry must be inside the ## Log section (after the
	// baseline line, and no trailing heading was introduced).
	logIdx := strings.Index(got, "## Log")
	entryIdx := strings.Index(got, "pivot: changed direction")
	if logIdx == -1 || entryIdx < logIdx {
		t.Errorf("entry should be under ## Log; logIdx=%d entryIdx=%d", logIdx, entryIdx)
	}
	// Still valid against the schema after appending.
	s, _ := beadspec.Load("progression")
	bead := beadspec.Bead{Type: "progression", Labels: []string{progressionLabel}, Body: got}
	if fs := beadspec.Validate(bead, s); len(fs) != 0 {
		t.Fatalf("post-append body should still pass schema: %+v", fs)
	}
}

func TestAppendLogEntry_NoLogSection(t *testing.T) {
	got := appendLogEntry("## Current understanding\nx\n", "2026-06-06", "baseline", "first")
	if !strings.Contains(got, "## Log") || !strings.Contains(got, "- 2026-06-06 baseline: first") {
		t.Errorf("should synthesize a ## Log section:\n%s", got)
	}
}

func TestCurrentUnderstandingOneLiner(t *testing.T) {
	body := "## Current understanding\n\nWe consolidated onto beads.\n\n## Log\n- x\n"
	if got := currentUnderstandingOneLiner(body); got != "We consolidated onto beads." {
		t.Errorf("one-liner = %q", got)
	}
	// Italic placeholder is skipped.
	body2 := newProgressionBody("T", "2026-06-05")
	if got := currentUnderstandingOneLiner(body2); got != "" {
		t.Errorf("placeholder should yield empty one-liner, got %q", got)
	}
}

func TestParseBeadList(t *testing.T) {
	// bare array
	rows := parseBeadList(`[{"id":"a-1","title":"T","description":"d"}]`)
	if len(rows) != 1 || rows[0].id != "a-1" {
		t.Fatalf("bare array parse failed: %+v", rows)
	}
	// wrapped
	rows = parseBeadList(`{"issues":[{"id":"b-2","title":"U"}]}`)
	if len(rows) != 1 || rows[0].id != "b-2" {
		t.Fatalf("wrapped parse failed: %+v", rows)
	}
	if rows := parseBeadList(""); rows != nil {
		t.Errorf("empty input should yield nil")
	}
}
