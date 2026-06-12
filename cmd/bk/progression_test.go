package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
	"github.com/theaichimera/beekeeper-go/pkg/beadspec"
)

// stubBdForProgression swaps the injectable bd runner with fn,
// recording every argv it receives, and restores the original on
// cleanup. Regression harness for bkg-qv6: progression commands
// must exec "bd" (argv[0]) and branch on rc, not err.
func stubBdForProgression(t *testing.T, fn func(args []string, cwd string) (int, string, string, error)) *[][]string {
	t.Helper()
	var calls [][]string
	orig := bkproject.BdRunner
	bkproject.BdRunner = func(args []string, cwd string, _ time.Duration) (int, string, string, error) {
		calls = append(calls, append([]string(nil), args...))
		return fn(args, cwd)
	}
	t.Cleanup(func() { bkproject.BdRunner = orig })
	return &calls
}

// contains reports whether xs has the exact element want.
func argvHas(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func TestProgressionNew_RunsBdAndPrintsID(t *testing.T) {
	calls := stubBdForProgression(t, func(args []string, _ string) (int, string, string, error) {
		return 0, "bkg-test1\n", "", nil
	})
	stdout, stderr, rc := runCmd(t, "progression", "new", "smoke topic")
	if rc != 0 {
		t.Fatalf("rc=%d want 0; stderr=%s", rc, stderr)
	}
	if len(*calls) != 1 {
		t.Fatalf("bd calls=%d want 1: %v", len(*calls), *calls)
	}
	argv := (*calls)[0]
	if len(argv) < 2 || argv[0] != "bd" || argv[1] != "create" {
		t.Fatalf("argv = %v; want [bd create ...]", argv)
	}
	if !strings.Contains(stdout, `created progression bkg-test1: "smoke topic"`) {
		t.Fatalf("stdout missing trimmed bead id: %q", stdout)
	}
}

func TestProgressionNew_BdFailureExitsNonZero(t *testing.T) {
	stubBdForProgression(t, func(args []string, _ string) (int, string, string, error) {
		return 127, "", `exec: "bd": executable file not found in $PATH`, nil
	})
	_, stderr, rc := runCmd(t, "progression", "new", "topic")
	if rc == 0 {
		t.Fatalf("rc=0 want non-zero; stderr=%s", stderr)
	}
	if !strings.Contains(stderr, "bd create failed") {
		t.Fatalf("stderr missing failure message: %q", stderr)
	}
	if !strings.Contains(stderr, "executable file not found") {
		t.Fatalf("stderr missing bd's stderr: %q", stderr)
	}
}

func TestProgressionNew_EmptyIDIsError(t *testing.T) {
	// bd exiting 0 with no id on stdout is an unfalsifiable success;
	// new must refuse to report a created progression without an id.
	stubBdForProgression(t, func(args []string, _ string) (int, string, string, error) {
		return 0, "", "", nil
	})
	stdout, stderr, rc := runCmd(t, "progression", "new", "topic")
	if rc == 0 {
		t.Fatalf("rc=0 want non-zero; stdout=%q", stdout)
	}
	if !strings.Contains(stderr, "no bead id") {
		t.Fatalf("stderr missing empty-id message: %q", stderr)
	}
}

func TestProgressionAdd_RunsBdShowThenUpdate(t *testing.T) {
	desc := newProgressionBody("Topic", "2026-06-01")
	showJSON, _ := json.Marshal(map[string]any{"description": desc})
	calls := stubBdForProgression(t, func(args []string, _ string) (int, string, string, error) {
		if argvHas(args, "show") {
			return 0, string(showJSON), "", nil
		}
		return 0, "", "", nil
	})
	stdout, stderr, rc := runCmd(t, "progression", "add", "bkg-test1", "new", "insight")
	if rc != 0 {
		t.Fatalf("rc=%d want 0; stderr=%s", rc, stderr)
	}
	if len(*calls) != 2 {
		t.Fatalf("bd calls=%d want 2 (show, update): %v", len(*calls), *calls)
	}
	show, update := (*calls)[0], (*calls)[1]
	if show[0] != "bd" || show[1] != "show" {
		t.Fatalf("show argv = %v; want [bd show ...]", show)
	}
	if update[0] != "bd" || update[1] != "update" {
		t.Fatalf("update argv = %v; want [bd update ...]", update)
	}
	if !strings.Contains(stdout, "added deepening entry to bkg-test1.") {
		t.Fatalf("stdout missing success message: %q", stdout)
	}
}

func TestProgressionAdd_ShowArrayShape(t *testing.T) {
	// bd 0.47+ emits `bd show --json` as a one-element array; add
	// must still find the description (was masked by bkg-qv6).
	desc := newProgressionBody("Topic", "2026-06-01")
	showJSON, _ := json.Marshal([]map[string]any{{"id": "bkg-test1", "description": desc}})
	stubBdForProgression(t, func(args []string, _ string) (int, string, string, error) {
		if argvHas(args, "show") {
			return 0, string(showJSON), "", nil
		}
		return 0, "", "", nil
	})
	stdout, stderr, rc := runCmd(t, "progression", "add", "bkg-test1", "entry")
	if rc != 0 {
		t.Fatalf("rc=%d want 0; stderr=%s", rc, stderr)
	}
	if !strings.Contains(stdout, "added deepening entry to bkg-test1.") {
		t.Fatalf("stdout missing success message: %q", stdout)
	}
}

func TestProgressionAdd_BdFailureExitsNonZero(t *testing.T) {
	stubBdForProgression(t, func(args []string, _ string) (int, string, string, error) {
		return 1, "", "no such issue: bkg-nope", nil
	})
	_, stderr, rc := runCmd(t, "progression", "add", "bkg-nope", "entry")
	if rc == 0 {
		t.Fatalf("rc=0 want non-zero; stderr=%s", stderr)
	}
	if !strings.Contains(stderr, "bd show bkg-nope failed") {
		t.Fatalf("stderr missing failure message: %q", stderr)
	}
}

func TestProgressionList_RunsBd(t *testing.T) {
	calls := stubBdForProgression(t, func(args []string, _ string) (int, string, string, error) {
		return 0, `[{"id":"p-1","title":"Topic arc","description":""}]`, "", nil
	})
	stdout, stderr, rc := runCmd(t, "progression", "list")
	if rc != 0 {
		t.Fatalf("rc=%d want 0; stderr=%s", rc, stderr)
	}
	if len(*calls) != 1 {
		t.Fatalf("bd calls=%d want 1: %v", len(*calls), *calls)
	}
	argv := (*calls)[0]
	if argv[0] != "bd" || argv[1] != "list" {
		t.Fatalf("argv = %v; want [bd list ...]", argv)
	}
	if !strings.Contains(stdout, "p-1") || !strings.Contains(stdout, "Topic arc") {
		t.Fatalf("stdout missing listed progression: %q", stdout)
	}
}

func TestProgressionList_BdFailureExitsNonZero(t *testing.T) {
	stubBdForProgression(t, func(args []string, _ string) (int, string, string, error) {
		return 127, "", `exec: "bd": executable file not found in $PATH`, nil
	})
	_, stderr, rc := runCmd(t, "progression", "list")
	if rc == 0 {
		t.Fatalf("rc=0 want non-zero; stderr=%s", stderr)
	}
	if !strings.Contains(stderr, "bd list failed") {
		t.Fatalf("stderr missing failure message: %q", stderr)
	}
}

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
