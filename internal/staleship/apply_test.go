package staleship

import (
	"strings"
	"testing"
	"time"

	execwrap "github.com/theaichimera/beekeeper-go/internal/exec"
	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
)

// stubBdProgrammable is a flexible BdRunner stub: callers register
// (matcher -> response) pairs. The matcher is a substring on the
// joined argv. First match wins; anything unmatched falls through to
// the real exec runner. Used by apply-mode tests that need to script
// `bd list --json` AND multiple `bd close <id> ...` calls in one run.
type bdResponse struct {
	rc     int
	stdout string
	stderr string
}

func stubBdProgrammable(t *testing.T, responses map[string]bdResponse) {
	t.Helper()
	orig := bkproject.BdRunner
	bkproject.BdRunner = func(args []string, cwd string, timeout time.Duration) (int, string, string, error) {
		joined := strings.Join(args, " ")
		for substr, resp := range responses {
			if strings.Contains(joined, substr) {
				return resp.rc, resp.stdout, resp.stderr, nil
			}
		}
		return execwrap.Default(args, cwd, timeout)
	}
	t.Cleanup(func() { bkproject.BdRunner = orig })
}

func sampleFinding(id string, pr int) Finding {
	return Finding{
		BeadID:         id,
		Status:         "in_progress",
		LandingSHA:     "0123456abcdef",
		LandingSubject: "feat(" + id + "): land it",
		LandingPR:      pr,
	}
}

// TestPlanClosesPlansEveryFinding: with no excludes / no blockers,
// every finding becomes a DecisionClose row in the plan.
func TestPlanClosesPlansEveryFinding(t *testing.T) {
	stubBdProgrammable(t, map[string]bdResponse{
		"bd list --json": {rc: 0, stdout: `[]`},
	})
	findings := []Finding{
		sampleFinding("demo-a", 1),
		sampleFinding("demo-b", 2),
	}
	plan, err := PlanCloses("/tmp/repo", findings, nil, false)
	if err != nil {
		t.Fatalf("PlanCloses: %v", err)
	}
	if len(plan) != 2 {
		t.Fatalf("plan len=%d want 2: %+v", len(plan), plan)
	}
	for _, a := range plan {
		if a.Decision != DecisionClose {
			t.Fatalf("decision=%s want %s", a.Decision, DecisionClose)
		}
	}
	if plan[0].Reason != "shipped in #1" {
		t.Fatalf("reason=%q want 'shipped in #1'", plan[0].Reason)
	}
}

// TestPlanClosesExcludesViaArg: --exclude bypasses bd entirely.
func TestPlanClosesExcludesViaArg(t *testing.T) {
	stubBdProgrammable(t, map[string]bdResponse{
		"bd list --json": {rc: 0, stdout: `[]`},
	})
	findings := []Finding{sampleFinding("demo-keep", 1)}
	plan, _ := PlanCloses("/tmp/repo", findings, []string{"demo-keep"}, false)
	if len(plan) != 1 || plan[0].Decision != DecisionSkipExcluded {
		t.Fatalf("excluded plan: %+v", plan)
	}
}

// TestPlanClosesSkipsBlocked: bd's blocker view marks beads with open
// `blocks` deps as DecisionSkipBlocked. Without --force, ApplyCloses
// records them under skipped_blocked and never invokes bd close.
func TestPlanClosesSkipsBlocked(t *testing.T) {
	// 9wup is blocked by an open child 7ftc — the dogfood case.
	stubBdProgrammable(t, map[string]bdResponse{
		"bd list --json": {rc: 0, stdout: `[
			{"id":"demo-9wup","status":"in_progress","dependencies":[
				{"type":"blocks","depends_on_id":"demo-7ftc"}
			]},
			{"id":"demo-7ftc","status":"open"}
		]`},
	})
	findings := []Finding{sampleFinding("demo-9wup", 100)}
	plan, _ := PlanCloses("/tmp/repo", findings, nil, false)
	if len(plan) != 1 {
		t.Fatalf("plan len=%d", len(plan))
	}
	if plan[0].Decision != DecisionSkipBlocked {
		t.Fatalf("decision=%s want %s", plan[0].Decision, DecisionSkipBlocked)
	}
	if plan[0].Blocker != "demo-7ftc" {
		t.Fatalf("blocker=%q want demo-7ftc", plan[0].Blocker)
	}
}

// TestPlanClosesForceClosesBlocked: --force flips DecisionSkipBlocked
// to DecisionForceCloseBlocked; ApplyCloses then invokes `bd close
// --force` for those beads.
func TestPlanClosesForceClosesBlocked(t *testing.T) {
	stubBdProgrammable(t, map[string]bdResponse{
		"bd list --json": {rc: 0, stdout: `[
			{"id":"demo-9wup","status":"in_progress","dependencies":[
				{"type":"blocks","depends_on_id":"demo-7ftc"}
			]},
			{"id":"demo-7ftc","status":"open"}
		]`},
	})
	findings := []Finding{sampleFinding("demo-9wup", 100)}
	plan, _ := PlanCloses("/tmp/repo", findings, nil, true)
	if len(plan) != 1 || plan[0].Decision != DecisionForceCloseBlocked {
		t.Fatalf("force plan: %+v", plan)
	}
}

// TestApplyClosesDryRunNoBdCalls: --apply false (dry-run) must not
// invoke `bd close`. We assert by stubbing only `bd list --json`; if
// ApplyCloses tried `bd close`, the fall-through to the real runner
// would either succeed (mutating real state) or fail loudly.
func TestApplyClosesDryRunNoBdCalls(t *testing.T) {
	calls := 0
	orig := bkproject.BdRunner
	bkproject.BdRunner = func(args []string, cwd string, timeout time.Duration) (int, string, string, error) {
		calls++
		if len(args) >= 2 && args[0] == "bd" && args[1] == "close" {
			t.Fatalf("dry-run invoked bd close: %v", args)
		}
		// Default: bd list returns empty.
		if len(args) >= 2 && args[0] == "bd" && args[1] == "list" {
			return 0, `[]`, "", nil
		}
		return 0, "", "", nil
	}
	t.Cleanup(func() { bkproject.BdRunner = orig })

	plan, _ := PlanCloses("/tmp/repo", []Finding{sampleFinding("demo-x", 1)}, nil, false)
	s := ApplyCloses("/tmp/repo", plan, true)
	if !s.DryRun {
		t.Fatal("expected DryRun=true")
	}
	if len(s.Closed) != 0 {
		t.Fatalf("dry-run closed beads: %v", s.Closed)
	}
}

// TestApplyClosesAppliesAndRecords: with --apply, plan rows fire
// `bd close` and land in CloseSummary.Closed. Stubbed bd reports rc 0.
func TestApplyClosesAppliesAndRecords(t *testing.T) {
	closeCalls := 0
	orig := bkproject.BdRunner
	bkproject.BdRunner = func(args []string, cwd string, timeout time.Duration) (int, string, string, error) {
		if len(args) >= 2 && args[0] == "bd" && args[1] == "list" {
			return 0, `[]`, "", nil
		}
		if len(args) >= 2 && args[0] == "bd" && args[1] == "close" {
			closeCalls++
			return 0, "Closed.\n", "", nil
		}
		return 0, "", "", nil
	}
	t.Cleanup(func() { bkproject.BdRunner = orig })

	plan, _ := PlanCloses("/tmp/repo", []Finding{
		sampleFinding("demo-a", 1),
		sampleFinding("demo-b", 2),
	}, nil, false)
	s := ApplyCloses("/tmp/repo", plan, false)
	if !s.Apply {
		t.Fatal("expected Apply=true")
	}
	if closeCalls != 2 {
		t.Fatalf("close calls=%d want 2", closeCalls)
	}
	if len(s.Closed) != 2 {
		t.Fatalf("closed=%v", s.Closed)
	}
	if len(s.Failed) != 0 || len(s.SkippedBlocked) != 0 {
		t.Fatalf("unexpected residuals: failed=%v blocked=%v", s.Failed, s.SkippedBlocked)
	}
}

// TestApplyClosesBlockedRefusalCapturedAtRuntime: even when the
// pre-plan blocker query missed (e.g. plan saw no blocker but bd
// refuses at apply time), the refusal stderr is parsed and the bead
// lands in skipped_blocked, not failed.
func TestApplyClosesBlockedRefusalCapturedAtRuntime(t *testing.T) {
	orig := bkproject.BdRunner
	bkproject.BdRunner = func(args []string, cwd string, timeout time.Duration) (int, string, string, error) {
		if len(args) >= 2 && args[0] == "bd" && args[1] == "list" {
			// Pre-plan: bd reports no blockers (race: blocker materialized
			// between plan and apply).
			return 0, `[]`, "", nil
		}
		if len(args) >= 2 && args[0] == "bd" && args[1] == "close" {
			return 1, "", "error: blocked by open issues [demo-7ftc] (use --force)\n", nil
		}
		return 0, "", "", nil
	}
	t.Cleanup(func() { bkproject.BdRunner = orig })

	plan, _ := PlanCloses("/tmp/repo", []Finding{sampleFinding("demo-9wup", 100)}, nil, false)
	s := ApplyCloses("/tmp/repo", plan, false)
	if len(s.Closed) != 0 {
		t.Fatalf("blocked bead landed in closed: %v", s.Closed)
	}
	if len(s.SkippedBlocked) != 1 {
		t.Fatalf("skipped_blocked=%v want 1", s.SkippedBlocked)
	}
	if s.SkippedBlocked[0].Blocker != "demo-7ftc" {
		t.Fatalf("blocker=%q want demo-7ftc", s.SkippedBlocked[0].Blocker)
	}
}

// TestCloseBeadParsesBlockerFromStderr unit-tests the regex-based
// blocker extraction. bd's exact wording was observed in the dogfood
// run: "blocked by open issues [<bead>] (use --force)".
func TestCloseBeadParsesBlockerFromStderr(t *testing.T) {
	orig := bkproject.BdRunner
	bkproject.BdRunner = func(args []string, cwd string, timeout time.Duration) (int, string, string, error) {
		return 1, "", "Refusing to close: blocked by open issues [demo-x, demo-y] (use --force)\n", nil
	}
	t.Cleanup(func() { bkproject.BdRunner = orig })
	ok, blocker, _, err := CloseBead("/tmp/repo", "demo-9wup", "shipped in #100", false)
	if ok || err != nil {
		t.Fatalf("expected (false, nil err); got (%v, %v)", ok, err)
	}
	if blocker != "demo-x" {
		t.Fatalf("blocker=%q want demo-x (first id in list)", blocker)
	}
}

// TestApplyClosesIdempotentWhenAlreadyClosed: re-running --apply on a
// bead bd already considers closed should be a no-op. The auto-source
// merge (bkg-td0.2) typically removes the bead from openIDs, but if
// somehow a closed-bead finding reaches the planner, PlanCloses skips
// it via the bdRecs check.
func TestApplyClosesIdempotentWhenAlreadyClosed(t *testing.T) {
	stubBdProgrammable(t, map[string]bdResponse{
		"bd list --json": {rc: 0, stdout: `[{"id":"demo-x","status":"closed"}]`},
	})
	plan, _ := PlanCloses("/tmp/repo", []Finding{sampleFinding("demo-x", 1)}, nil, false)
	if len(plan) != 0 {
		t.Fatalf("plan should be empty (bd already closed); got %+v", plan)
	}
}
