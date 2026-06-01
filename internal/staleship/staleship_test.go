package staleship

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE=2026-06-01T12:00:00",
		"GIT_COMMITTER_DATE=2026-06-01T12:00:00",
	)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// initRepoWithJSONL builds a synthetic git repo with the given JSONL
// records committed on `main`, then layers the supplied list of
// commit subjects (one commit per subject, all on `main`) so test
// fixtures can simulate "PR #N landed for bead-X" patterns.
func initRepoWithJSONL(t *testing.T, recs []map[string]any, subjects []string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
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
	mustGit(t, dir, "init", "-q", "-b", "main")
	mustGit(t, dir, "config", "user.email", "t@t")
	mustGit(t, dir, "config", "user.name", "t")
	mustGit(t, dir, "add", ".beads/issues.jsonl")
	mustGit(t, dir, "commit", "-q", "-m", "init")
	for i, subj := range subjects {
		// Each commit needs a tree change to land. Touch a unique file.
		f := filepath.Join(dir, fmtName("c", i))
		if err := os.WriteFile(f, []byte(subj), 0o644); err != nil {
			t.Fatal(err)
		}
		mustGit(t, dir, "add", f)
		mustGit(t, dir, "commit", "-q", "-m", subj)
	}
	return dir
}

func fmtName(prefix string, i int) string {
	return prefix + string(rune('a'+(i%26))) + ".txt"
}

// --- prefix derivation -------------------------------------------------

func TestDerivePrefixFromFirstRecord(t *testing.T) {
	t.Parallel()
	dir := initRepoWithJSONL(t, []map[string]any{
		{"id": "demo-xymh", "status": "in_progress"},
	}, nil)
	got, ok := DerivePrefix(dir)
	if !ok || got != "demo" {
		t.Fatalf("DerivePrefix=%q ok=%v want (\"demo\", true)", got, ok)
	}
}

// --- token-bounded matching --------------------------------------------

func TestIDMatcherTokenBoundaries(t *testing.T) {
	t.Parallel()
	re := idMatcher("demo-xymh")
	good := []string{
		"demo-xymh: ship the thing",
		"feat(demo-xymh): ship",
		"[demo-xymh] hotfix",
		"close demo-xymh (#736)",
		"demo-xymh",
		" demo-xymh ",
	}
	for _, s := range good {
		if !re.MatchString(s) {
			t.Errorf("expected match: %q", s)
		}
	}
	bad := []string{
		"demo-xymhfoo: nope",       // suffix runs into id alphabet
		"demo-xymh-1: also a bead", // hyphen extends the id
		"demo-xymh.bar: nope",      // dot extends the id
		"see demo-xymh1234 elsewhere",
	}
	for _, s := range bad {
		if re.MatchString(s) {
			t.Errorf("expected NO match: %q", s)
		}
	}
}

// --- core acceptance: real-backlog-shaped fixture ----------------------

// Mirrors the bkg-bqa.3 acceptance criteria using neutral ids:
//
//   - Open beads `xymh`, `o1b4`, `0dh4`, `fz56`, `e42f`, `1k8x.1`,
//     `1k8x.2`, `4yzw`, `p561`, `lgd2`, `4dll`, `h2ml.1` etc. exist
//     in the JSONL.
//   - Their ids appear as delimited tokens in merged commit subjects
//     on `main` -> they MUST be flagged shipped-not-closed.
//   - Tangential ids `afby` and `6ml` exist in the JSONL but are only
//     referenced inside other subjects (e.g. "see demo-afby for
//     context") with no delimited-token landing -> they must NOT be
//     flagged. The detector's job is precision; over-claiming is
//     worse than missing one.
func TestDiagnoseFlagsShippedAndIgnoresTangential(t *testing.T) {
	t.Parallel()
	open := []string{
		"xymh", "o1b4", "0dh4", "fz56", "e42f",
		"1k8x.1", "1k8x.2", "1k8x.5", "1k8x.6", "1k8x.7", "1k8x.9",
		"4yzw", "p561", "lgd2", "4dll", "h2ml.1",
		// Tangential — must NOT be flagged.
		"afby", "6ml",
	}
	var recs []map[string]any
	for _, id := range open {
		recs = append(recs, map[string]any{
			"id":     "demo-" + id,
			"status": "in_progress",
		})
	}

	subjects := []string{
		"feat(demo-xymh): ship parser (#736)",
		"fix(demo-o1b4): retry handler (#737)",
		"feat: demo-0dh4 + demo-fz56 land together (#738)",
		"feat(demo-e42f): partition recovery (#777)",
		"feat(demo-1k8x.1): step 1 (#831)",
		"feat(demo-1k8x.2): step 2 (#834)",
		"feat(demo-1k8x.5): step 5 (#836)",
		"feat(demo-1k8x.6): step 6 (#837)",
		"feat(demo-1k8x.7): step 7 (#833)",
		"feat(demo-1k8x.9): step 9 (#839)",
		"feat(demo-4yzw): cohort sweep (#841)",
		"feat(demo-p561): pagination (#842)",
		"feat(demo-lgd2): grade rebuild (#843)",
		"feat(demo-4dll): drop-down list (#844)",
		"feat(demo-h2ml.1): chart fix (#876)",
		// Tangential references — afby + 6ml must NOT match.
		"chore: bump deps (no bead) — see demo-afby for context",
		"refactor: split builder; later we need demo-6ml work",
	}

	dir := initRepoWithJSONL(t, recs, subjects)
	r, err := Diagnose(dir, "main", "demo", 0)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}

	flagged := map[string]Finding{}
	for _, f := range r.Findings {
		flagged[f.BeadID] = f
	}

	must := []string{
		"demo-xymh", "demo-o1b4", "demo-0dh4", "demo-fz56", "demo-e42f",
		"demo-1k8x.1", "demo-1k8x.2", "demo-1k8x.5", "demo-1k8x.6", "demo-1k8x.7", "demo-1k8x.9",
		"demo-4yzw", "demo-p561", "demo-lgd2", "demo-4dll", "demo-h2ml.1",
	}
	for _, id := range must {
		if _, ok := flagged[id]; !ok {
			t.Errorf("missing finding for %s; flagged=%v", id, sortedKeys(flagged))
		}
	}

	mustNot := []string{"demo-afby", "demo-6ml"}
	for _, id := range mustNot {
		if _, ok := flagged[id]; ok {
			t.Errorf("FALSE POSITIVE: tangential reference flagged for %s: %+v", id, flagged[id])
		}
	}

	// Spot-check landing PR parsing.
	if got, ok := flagged["demo-xymh"]; !ok || got.LandingPR != 736 {
		t.Errorf("demo-xymh landing PR=%d want 736", got.LandingPR)
	}
	if got, ok := flagged["demo-h2ml.1"]; !ok || got.LandingPR != 876 {
		t.Errorf("demo-h2ml.1 landing PR=%d want 876", got.LandingPR)
	}
}

// --- subject-only contract ----------------------------------------------

// The detector must look at SUBJECT lines only. A bead id mentioned
// only in a commit BODY (line 2+) is tangential and must not match.
//
// This is the ground-truth contract that prevents false positives like
// "see demo-afby for context" — even when that string is in a body,
// not a subject. With our --pretty=%s the body is invisible, so the
// match is impossible by construction. This test pins the contract.
func TestDiagnoseIgnoresBodyOnlyReferences(t *testing.T) {
	t.Parallel()
	recs := []map[string]any{
		{"id": "demo-bod1", "status": "open"},
	}
	dir := initRepoWithJSONL(t, recs, nil)
	// Synthesize a commit whose subject is unrelated but whose body
	// mentions the bead. exec.Command via mustGit doesn't easily set
	// multi-line messages, so we use git -F.
	bodyMsg := "chore: refactor builders\n\nWhile here, see demo-bod1 for context.\n"
	mfile := filepath.Join(dir, "msg.txt")
	if err := os.WriteFile(mfile, []byte(bodyMsg), 0o644); err != nil {
		t.Fatal(err)
	}
	// Tree change so commit lands.
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "x.txt")
	mustGit(t, dir, "commit", "-q", "-F", mfile)

	r, err := Diagnose(dir, "main", "demo", 0)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if len(r.Findings) != 0 {
		t.Fatalf("body-only ref produced finding(s): %+v", r.Findings)
	}
}

// --- closed beads must not be flagged -----------------------------------

func TestDiagnoseClosedBeadsNotFlagged(t *testing.T) {
	t.Parallel()
	recs := []map[string]any{
		{"id": "demo-aaa", "status": "closed"},
	}
	subjects := []string{
		"feat(demo-aaa): legitimately closed and shipped",
	}
	dir := initRepoWithJSONL(t, recs, subjects)
	r, _ := Diagnose(dir, "main", "demo", 0)
	if len(r.Findings) != 0 {
		t.Fatalf("closed bead flagged: %+v", r.Findings)
	}
}

// --- offline operation: no network, no GitHub API -----------------------

func TestDiagnoseWorksOfflineNoNetwork(t *testing.T) {
	// t.Setenv forbids t.Parallel; this test runs serially.
	// This test relies on the fact that `git log` against a local
	// repo never touches the network. If a future maintainer adds
	// GitHub API integration, gate it behind a flag — the OFFLINE
	// path must keep this test green without a token.
	recs := []map[string]any{{"id": "demo-x", "status": "open"}}
	subjects := []string{"feat(demo-x): land it (#42)"}
	dir := initRepoWithJSONL(t, recs, subjects)

	t.Setenv("GH_TOKEN", "") // prove tokenless.
	t.Setenv("GITHUB_TOKEN", "")
	r, err := Diagnose(dir, "main", "demo", 0)
	if err != nil {
		t.Fatalf("offline Diagnose: %v", err)
	}
	if len(r.Findings) != 1 {
		t.Fatalf("findings=%d", len(r.Findings))
	}
	if r.Findings[0].LandingPR != 42 {
		t.Fatalf("PR=%d want 42", r.Findings[0].LandingPR)
	}
}

// --- ordering -----------------------------------------------------------

func TestDiagnoseOrderingDeterministic(t *testing.T) {
	t.Parallel()
	recs := []map[string]any{
		{"id": "demo-a", "status": "open"},
		{"id": "demo-b", "status": "open"},
		{"id": "demo-c", "status": "open"},
	}
	subjects := []string{
		"feat(demo-c): ship c (#100)",
		"feat(demo-a): ship a (#10)",
		"feat(demo-b): ship b",
	}
	dir := initRepoWithJSONL(t, recs, subjects)
	r, _ := Diagnose(dir, "main", "demo", 0)
	got := []string{}
	for _, f := range r.Findings {
		got = append(got, f.BeadID)
	}
	// Want: PR'd findings first by PR number asc; PR-less last.
	want := []string{"demo-a", "demo-c", "demo-b"}
	if !equalSlice(got, want) {
		t.Fatalf("order=%v want %v", got, want)
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]Finding) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- empty / missing JSONL ---------------------------------------------

func TestDiagnoseMissingJSONL(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q", "-b", "main")
	mustGit(t, dir, "config", "user.email", "t@t")
	mustGit(t, dir, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "x.txt")
	mustGit(t, dir, "commit", "-q", "-m", "init")

	if _, err := Diagnose(dir, "main", "demo", 0); err == nil {
		t.Fatal("expected error on missing JSONL")
	}
}

func TestDiagnoseRefMissing(t *testing.T) {
	t.Parallel()
	recs := []map[string]any{{"id": "demo-x", "status": "open"}}
	dir := initRepoWithJSONL(t, recs, nil)
	if _, err := Diagnose(dir, "no-such-branch", "demo", 0); err == nil {
		t.Fatal("expected error on missing ref")
	}
}

// --- subject can contain shell metacharacters --------------------------

func TestDiagnoseSubjectsWithBacktickAndQuotes(t *testing.T) {
	t.Parallel()
	recs := []map[string]any{{"id": "demo-x", "status": "open"}}
	subjects := []string{
		"feat(demo-x): use `setattr()` instead of \"setter\" (#1)",
	}
	dir := initRepoWithJSONL(t, recs, subjects)
	r, err := Diagnose(dir, "main", "demo", 0)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if len(r.Findings) != 1 || !strings.Contains(r.Findings[0].LandingSubject, "setattr") {
		t.Fatalf("subject preservation failed: %+v", r.Findings)
	}
}
