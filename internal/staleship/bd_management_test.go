package staleship

import (
	"testing"
)

// --- conv-commit parser unit tests ------------------------------------

func TestParseConvCommit(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in        string
		wantType  string
		wantScope string
	}{
		{"feat(demo-x): land", "feat", "demo-x"},
		{"feat: bump deps", "feat", ""},
		{"chore(bd): file demo-x", "chore", "bd"},
		{"chore(beads): update demo-x", "chore", "beads"},
		{"spec(demo-1k8x): file (#830)", "spec", "demo-1k8x"},
		{"FIX(demo-x): caps land", "fix", "demo-x"},
		{"no conv prefix here", "", ""},
		{"[demo-x] hotfix", "", ""},
	}
	for _, tc := range cases {
		got := parseConvCommit(tc.in)
		if got.ctype != tc.wantType || got.scope != tc.wantScope {
			t.Errorf("parseConvCommit(%q) = (%q, %q) want (%q, %q)",
				tc.in, got.ctype, got.scope, tc.wantType, tc.wantScope)
		}
	}
}

// --- bd-management subject filter --------------------------------------

// The bkg-td0.1 acceptance set: 7 false positives observed in the real
// run. Each subject names a bead but the commit is bd-management
// (filing / updating / spec) — must NOT ship. Synthetic neutral-id
// fixture mirroring the actual subjects.
func TestDiagnoseExcludesBdManagementCommits(t *testing.T) {
	t.Parallel()
	open := []string{
		// Real run: 7 false positives. Neutral ids reproduce the shape.
		"1k8x", "5ctm", "km84", "h2ud", "znpx", "cazl", "ix5u",
	}
	var recs []map[string]any
	for _, id := range open {
		recs = append(recs, map[string]any{
			"id":     "demo-" + id,
			"status": "in_progress",
		})
	}

	subjects := []string{
		// type=spec -> NEVER shipping
		"spec(demo-1k8x): file epic + children (#830)",
		// scope=bd -> NEVER shipping (filing)
		"chore(bd): file demo-5ctm and demo-km84 (#1052)",
		"chore(bd): file demo-app onboarding bead (demo-h2ud) (#1003)",
		"chore(bd): file demo-znpx (#1009)",
		"chore(bd): update demo-ix5u (#1013)",
		// scope=beads -> NEVER shipping (filing)
		"chore(beads): file demo-cazl (#1166)",
	}

	dir := initRepoWithJSONL(t, recs, subjects)
	r, err := Diagnose(dir, "main", "demo", 0)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if len(r.Findings) != 0 {
		t.Fatalf("bd-management commits flagged: %+v", r.Findings)
	}
}

// Existing genuine feat/fix beads must STILL be flagged after the
// bd-management filter is in place. Pin the regression: any change
// that regresses precision against the bkg-bqa.3 fixture fails here.
func TestDiagnoseStillFlagsImplementationCommits(t *testing.T) {
	t.Parallel()
	recs := []map[string]any{
		{"id": "demo-xymh", "status": "in_progress"},
		{"id": "demo-bug1", "status": "open"},
		{"id": "demo-pf1", "status": "in_progress"},
		{"id": "demo-rf1", "status": "open"},
	}
	subjects := []string{
		"feat(demo-xymh): ship parser (#736)",
		"fix(demo-bug1): retry handler (#777)",
		"perf(demo-pf1): partition recovery (#800)",
		"refactor(demo-rf1): split builder (#900)",
	}
	dir := initRepoWithJSONL(t, recs, subjects)
	r, err := Diagnose(dir, "main", "demo", 0)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if len(r.Findings) != 4 {
		t.Fatalf("genuine feat/fix/perf/refactor count=%d want 4: %+v",
			len(r.Findings), r.Findings)
	}
}

// Subjects with conventional types that are NOT in the allowlist (e.g.
// `docs`, `test`, `chore` without bd/beads scope) must NOT ship even
// when they carry a `(#N)` PR-merge marker — these are non-shipping
// types per the bkg-td0.1 contract. Sample includes the false-positive
// pattern that bypassed the prior matcher: a chore with a token-bounded
// id elsewhere in the subject.
func TestDiagnoseSkipsNonAllowlistTypes(t *testing.T) {
	t.Parallel()
	recs := []map[string]any{
		{"id": "demo-x", "status": "in_progress"},
	}
	subjects := []string{
		"docs(demo-x): readme update (#100)",
		"test(demo-x): add cases (#101)",
		"chore(deps): bump foo touching demo-x (#102)",
	}
	dir := initRepoWithJSONL(t, recs, subjects)
	r, err := Diagnose(dir, "main", "demo", 0)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if len(r.Findings) != 0 {
		t.Fatalf("non-implementation type flagged: %+v", r.Findings)
	}
}

// The `--ship-types` flag (passed via Opts.AllowedShipTypes) lets a
// repo opt into different conventions — e.g. include `docs` for repos
// that ship doc beads.
func TestDiagnoseAllowedShipTypesOverride(t *testing.T) {
	t.Parallel()
	recs := []map[string]any{
		{"id": "demo-x", "status": "in_progress"},
	}
	subjects := []string{
		"docs(demo-x): wiki rewrite (#100)",
	}
	dir := initRepoWithJSONL(t, recs, subjects)
	// Default allowlist excludes docs -> 0 findings.
	r, _ := DiagnoseWithOpts(dir, "main", "demo", Opts{})
	if len(r.Findings) != 0 {
		t.Fatalf("default config flagged docs: %+v", r.Findings)
	}
	// Extended allowlist -> docs counts -> 1 finding.
	r2, _ := DiagnoseWithOpts(dir, "main", "demo", Opts{
		AllowedShipTypes: []string{"feat", "fix", "perf", "refactor", "docs"},
	})
	if len(r2.Findings) != 1 {
		t.Fatalf("override allowlist did not include docs: %+v", r2.Findings)
	}
}

// Bare-prefix shapes (`<id>:`, `[<id>]`) are explicit bead-led
// landings and bypass the type filter — they HAVE no conventional
// type. This preserves the bkg-bqa.3 contract for terse subjects.
func TestDiagnoseBarePrefixStillShipsAfterFilter(t *testing.T) {
	t.Parallel()
	recs := []map[string]any{
		{"id": "demo-a", "status": "in_progress"},
		{"id": "demo-b", "status": "in_progress"},
	}
	subjects := []string{
		"demo-a: ship it",
		"[demo-b] hotfix",
	}
	dir := initRepoWithJSONL(t, recs, subjects)
	r, err := Diagnose(dir, "main", "demo", 0)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if len(r.Findings) != 2 {
		t.Fatalf("bare-prefix landings count=%d want 2: %+v", len(r.Findings), r.Findings)
	}
}

// eligibleForShipping unit table — pin the rule matrix for review.
func TestEligibleForShipping(t *testing.T) {
	t.Parallel()
	allowed := allowedTypeSet(nil) // default
	cases := []struct {
		ctype, scope string
		want         bool
	}{
		{"feat", "", true},
		{"feat", "demo-x", true},
		{"fix", "", true},
		{"perf", "", true},
		{"refactor", "", true},
		{"spec", "demo-x", false},
		{"chore", "bd", false},
		{"chore", "beads", false},
		{"chore", "deps", false},
		{"docs", "", false},
		{"test", "", false},
		// Empty type (no conv prefix) -> eligible (bare-prefix path).
		{"", "", true},
	}
	for _, tc := range cases {
		got := convCommit{ctype: tc.ctype, scope: tc.scope}.eligibleForShipping(allowed)
		if got != tc.want {
			t.Errorf("eligibleForShipping(type=%q, scope=%q)=%v want %v",
				tc.ctype, tc.scope, got, tc.want)
		}
	}
}
