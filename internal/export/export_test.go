package export

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
)

// forceConfigJSONFallback stubs `bd` so ReadGitState's `bd config get
// sync.branch` short-circuits to the .beads/config.json fallback.
// Without this, real bd tries to init a SQLite DB in the synthetic
// repo and trips TestBuildIsReadOnly's mutation check.
func forceConfigJSONFallback(t *testing.T) {
	t.Helper()
	orig := bkproject.BdRunner
	bkproject.BdRunner = func(args []string, cwd string, _ time.Duration) (int, string, string, error) {
		// Refuse all bd calls — export's CommentsRunner is injected
		// in tests so this affects only ReadGitState.
		return 1, "", "(stubbed in test)", nil
	}
	t.Cleanup(func() { bkproject.BdRunner = orig })
}

// writeRepo lays down a synthetic .beads/issues.jsonl. When
// `withSyncBranchJSONL` is set, also writes that JSONL to the named
// branch so the authoritative-state reader picks it up over the
// working tree.
func writeRepo(t *testing.T, dir string, working []map[string]any, syncBranch string, syncBranchRecs []map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSONL := func(path string, recs []map[string]any) {
		var body []byte
		for _, r := range recs {
			b, _ := json.Marshal(r)
			body = append(body, b...)
			body = append(body, '\n')
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeJSONL(filepath.Join(dir, ".beads", "issues.jsonl"), working)

	if syncBranch == "" {
		return
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	mustGit := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mustGit("init", "-q", "-b", "main")
	mustGit("config", "user.email", "t@t")
	mustGit("config", "user.name", "t")
	mustGit("add", ".beads/issues.jsonl")
	mustGit("commit", "-q", "-m", "init working")
	mustGit("checkout", "-q", "-b", syncBranch)
	writeJSONL(filepath.Join(dir, ".beads", "issues.jsonl"), syncBranchRecs)
	mustGit("add", ".beads/issues.jsonl")
	mustGit("commit", "-q", "-m", "sync state")
	mustGit("checkout", "-q", "main")
	// Restore working JSONL so authoritative read MUST consult sync branch.
	writeJSONL(filepath.Join(dir, ".beads", "issues.jsonl"), working)
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".beads", "config.json"),
		[]byte(`{"sync":{"branch":"`+syncBranch+`"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func noopComments(repo, id string) ([]Comment, error) { return nil, nil }

func makeBead(id, title, body, status, issueType string, labels []string) map[string]any {
	rec := map[string]any{
		"id":          id,
		"title":       title,
		"description": body,
		"status":      status,
		"issue_type":  issueType,
		"created_at":  "2026-06-01T12:00:00Z",
		"updated_at":  "2026-06-02T12:00:00Z",
	}
	if len(labels) > 0 {
		raw := make([]any, 0, len(labels))
		for _, l := range labels {
			raw = append(raw, l)
		}
		rec["labels"] = raw
	}
	return rec
}

// --- schema shape ------------------------------------------------------

func TestBuildSchemaShapeAndOrdering(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := t.TempDir()
	writeRepo(t, dir, []map[string]any{
		makeBead("p-2", "second", "body 2", "open", "feature", nil),
		makeBead("p-1", "first", "body 1", "open", "feature", nil),
	}, "", nil)

	docs, err := Build(Opts{Paths: []string{dir}, Classify: true, IncludeComments: false, CommentsRunner: noopComments})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("len=%d want 2", len(docs))
	}
	// Deterministic id order.
	if docs[0].ID != "p-1" || docs[1].ID != "p-2" {
		t.Fatalf("ordering: %s, %s", docs[0].ID, docs[1].ID)
	}
	d := docs[0]
	if d.Title != "first" || d.Body != "body 1" {
		t.Fatalf("title/body: %+v", d)
	}
	if d.Status != "open" || d.IssueType != "feature" {
		t.Fatalf("status/type: %+v", d)
	}
	if d.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version=%d want %d", d.SchemaVersion, SchemaVersion)
	}
	// Text default template should include title + body, no comments
	// (none present here).
	if d.Text == "" {
		t.Fatalf("Text empty: %q", d.Text)
	}
}

// --- authoritative state -----------------------------------------------

// TestBuildPrefersSyncBranchJSONL — bkg-3xb acceptance: when a sync
// branch is configured + present, its JSONL beats the working tree's.
// We plant DIFFERENT records at each location; the export must
// reflect the sync branch.
func TestBuildPrefersSyncBranchJSONL(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := t.TempDir()
	working := []map[string]any{
		makeBead("p-1", "stale title", "stale", "open", "feature", nil),
	}
	syncRecs := []map[string]any{
		makeBead("p-1", "AUTHORITATIVE title", "auth body", "in_progress", "feature", nil),
	}
	writeRepo(t, dir, working, "beads-sync", syncRecs)

	// Force config-json fallback so ReadGitState doesn't shell out
	// to bd config get.
	t.Setenv("HOME", dir) // benign isolation
	docs, err := Build(Opts{Paths: []string{dir}, Classify: true, CommentsRunner: noopComments})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("len=%d", len(docs))
	}
	if docs[0].Title != "AUTHORITATIVE title" {
		t.Fatalf("did not read sync branch: title=%q", docs[0].Title)
	}
	if docs[0].Status != "in_progress" {
		t.Fatalf("status=%q want in_progress (sync branch)", docs[0].Status)
	}
}

// --- filters -----------------------------------------------------------

func TestBuildStatusFilter(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := t.TempDir()
	writeRepo(t, dir, []map[string]any{
		makeBead("p-1", "open one", "x", "open", "feature", nil),
		makeBead("p-2", "closed one", "x", "closed", "feature", nil),
	}, "", nil)

	openOnly, _ := Build(Opts{Paths: []string{dir}, Status: StatusOpen, CommentsRunner: noopComments})
	if len(openOnly) != 1 || openOnly[0].ID != "p-1" {
		t.Fatalf("open filter: %+v", openOnly)
	}
	closedOnly, _ := Build(Opts{Paths: []string{dir}, Status: StatusClosed, CommentsRunner: noopComments})
	if len(closedOnly) != 1 || closedOnly[0].ID != "p-2" {
		t.Fatalf("closed filter: %+v", closedOnly)
	}
	all, _ := Build(Opts{Paths: []string{dir}, Status: StatusAll, CommentsRunner: noopComments})
	if len(all) != 2 {
		t.Fatalf("all filter: %d", len(all))
	}
}

func TestBuildTypeFilter(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := t.TempDir()
	writeRepo(t, dir, []map[string]any{
		makeBead("p-1", "feat", "x", "open", "feature", nil),
		makeBead("p-2", "task", "x", "open", "task", nil),
		makeBead("p-3", "bug", "x", "open", "bug", nil),
	}, "", nil)

	docs, _ := Build(Opts{Paths: []string{dir}, Types: []string{"feature", "bug"}, CommentsRunner: noopComments})
	if len(docs) != 2 {
		t.Fatalf("type filter: %d", len(docs))
	}
}

func TestBuildLabelsFilter(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := t.TempDir()
	writeRepo(t, dir, []map[string]any{
		makeBead("p-1", "a", "x", "open", "feature", []string{"adr"}),
		makeBead("p-2", "b", "x", "open", "feature", []string{"chore"}),
		makeBead("p-3", "c", "x", "open", "feature", nil),
	}, "", nil)

	include, _ := Build(Opts{Paths: []string{dir}, IncludeLabels: []string{"adr"}, CommentsRunner: noopComments})
	if len(include) != 1 || include[0].ID != "p-1" {
		t.Fatalf("include labels: %+v", include)
	}
	exclude, _ := Build(Opts{Paths: []string{dir}, ExcludeLabels: []string{"chore"}, CommentsRunner: noopComments})
	if len(exclude) != 2 {
		t.Fatalf("exclude labels: %+v", exclude)
	}
}

func TestBuildSinceFilter(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := t.TempDir()
	old := makeBead("p-old", "old", "x", "open", "feature", nil)
	old["updated_at"] = "2026-01-01T00:00:00Z"
	new := makeBead("p-new", "new", "x", "open", "feature", nil)
	new["updated_at"] = "2026-06-01T00:00:00Z"
	writeRepo(t, dir, []map[string]any{old, new}, "", nil)

	cutoff, _ := time.Parse(time.RFC3339, "2026-03-01T00:00:00Z")
	docs, _ := Build(Opts{Paths: []string{dir}, Since: cutoff, CommentsRunner: noopComments})
	if len(docs) != 1 || docs[0].ID != "p-new" {
		t.Fatalf("since filter: %+v", docs)
	}
}

func TestBuildMinRichness(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := t.TempDir()
	writeRepo(t, dir, []map[string]any{
		makeBead("p-light", "light", "tiny", "open", "task", nil),
		makeBead("p-heavy", "heavy", repeat(200, 'a'), "open", "task", nil),
	}, "", nil)

	docs, _ := Build(Opts{Paths: []string{dir}, Classify: true, MinRichness: 100, CommentsRunner: noopComments})
	if len(docs) != 1 || docs[0].ID != "p-heavy" {
		t.Fatalf("min-richness drop low-signal: %+v", docs)
	}
}

func TestBuildDecisionsOnly(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := t.TempDir()
	writeRepo(t, dir, []map[string]any{
		makeBead("p-d", "decision", "we decided to use kafka", "open", "feature", []string{"adr"}),
		makeBead("p-r", "routine", "tiny", "open", "task", nil),
	}, "", nil)

	docs, _ := Build(Opts{Paths: []string{dir}, Classify: true, DecisionsOnly: true, CommentsRunner: noopComments})
	if len(docs) != 1 || docs[0].ID != "p-d" {
		t.Fatalf("decisions-only: %+v", docs)
	}
}

// --- classification heuristics -----------------------------------------

func TestClassifyDecisionByLabel(t *testing.T) {
	doc := Doc{
		Title:  "use Kafka",
		Body:   "short body",
		Labels: []string{"decision"},
	}
	r, d, _ := classify(doc)
	if !d {
		t.Fatalf("label decision should classify is_decision=true")
	}
	if r {
		t.Fatalf("label decision should NOT be routine: %+v", doc)
	}
}

func TestClassifyDecisionByPhrase(t *testing.T) {
	doc := Doc{
		Title: "queue choice",
		Body:  "we decided to use kafka instead of rabbitmq because we need partition order",
	}
	_, d, _ := classify(doc)
	if !d {
		t.Fatalf("decision phrase should fire is_decision=true")
	}
}

func TestClassifyRoutineShortChore(t *testing.T) {
	doc := Doc{
		Title:  "chore: bump deps",
		Body:   "bump",
		Labels: []string{"chore"},
	}
	r, d, _ := classify(doc)
	if d {
		t.Fatalf("routine should not be decision")
	}
	if !r {
		t.Fatalf("short chore should be routine")
	}
}

func TestClassifyLongIsNeitherRoutineNorDecisionByDefault(t *testing.T) {
	doc := Doc{
		Title:  "something",
		Body:   repeat(2000, 'x'),
		Labels: []string{"feature"},
	}
	r, d, _ := classify(doc)
	if r || d {
		t.Fatalf("long generic feature should be both false: routine=%v decision=%v", r, d)
	}
}

func TestClassifyRichnessIsBodyPlusComments(t *testing.T) {
	doc := Doc{
		Body: "abc",
		Comments: []Comment{
			{Body: "12345"},
			{Body: "ab"},
		},
	}
	_, _, rich := classify(doc)
	if rich != 3+5+2 {
		t.Fatalf("richness=%d want 10", rich)
	}
}

// --- supersession ------------------------------------------------------

func TestDeriveSupersededBy(t *testing.T) {
	out := deriveSupersededBy(map[string]any{}, "this RFC was superseded by bkg-9cs and obsoleted by bkg-z9z")
	if len(out) != 2 || out[0] != "bkg-9cs" || out[1] != "bkg-z9z" {
		t.Fatalf("superseded_by: %+v", out)
	}
}

// --- redaction ---------------------------------------------------------

func TestRedactScrubsEmailsAndTokens(t *testing.T) {
	in := "contact alice@example.com or use ghp_ABCDEFGHIJKLMNOPQRSTUVWX for auth"
	out := redactString(in)
	if out == in {
		t.Fatalf("nothing redacted: %q", out)
	}
	for _, leak := range []string{"alice@example.com", "ghp_ABC"} {
		if contains(out, leak) {
			t.Fatalf("leak %q remained in %q", leak, out)
		}
	}
}

// --- text template -----------------------------------------------------

func TestRenderTextDefaultTemplate(t *testing.T) {
	doc := Doc{
		Title:    "T",
		Body:     "B",
		Comments: []Comment{{Body: "c1"}, {Body: "c2"}},
	}
	got := renderText(DefaultTextTemplate, doc, false)
	for _, want := range []string{"T", "B", "c1", "c2"} {
		if !contains(got, want) {
			t.Fatalf("missing %q in: %q", want, got)
		}
	}
}

func TestRenderTextCustomTemplate(t *testing.T) {
	doc := Doc{Title: "T", Body: "B"}
	got := renderText("ONLY: {{title}}", doc, false)
	if got != "ONLY: T" {
		t.Fatalf("custom template: %q", got)
	}
}

// --- repo filter -------------------------------------------------------

func TestBuildRepoFilter(t *testing.T) {
	forceConfigJSONFallback(t)
	parent := t.TempDir()
	dirA := filepath.Join(parent, "alpha")
	dirB := filepath.Join(parent, "beta")
	writeRepo(t, dirA, []map[string]any{
		makeBead("a-1", "a", "x", "open", "feature", nil),
	}, "", nil)
	writeRepo(t, dirB, []map[string]any{
		makeBead("b-1", "b", "x", "open", "feature", nil),
	}, "", nil)

	docs, _ := Build(Opts{Paths: []string{parent}, RepoFilter: "alpha", CommentsRunner: noopComments})
	if len(docs) != 1 || docs[0].ID != "a-1" {
		t.Fatalf("repo-filter: %+v", docs)
	}
}

// --- identity normalization --------------------------------------------

func TestBuildNormalizesAssignee(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := t.TempDir()
	rec := makeBead("p-1", "x", "y", "open", "feature", nil)
	rec["assignee"] = "alice@example.com"
	writeRepo(t, dir, []map[string]any{rec}, "", nil)

	docs, _ := Build(Opts{
		Paths:          []string{dir},
		IdentityMap:    map[string]string{"alice@example.com": "alice"},
		CommentsRunner: noopComments,
	})
	if len(docs) != 1 {
		t.Fatalf("len=%d", len(docs))
	}
	if docs[0].Assignee != "alice" {
		t.Fatalf("assignee=%q want canonical 'alice'", docs[0].Assignee)
	}
}

// --- read-only contract ------------------------------------------------

func TestBuildIsReadOnly(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := t.TempDir()
	writeRepo(t, dir, []map[string]any{
		makeBead("p-1", "x", "y", "open", "feature", nil),
	}, "", nil)
	jsonlPath := filepath.Join(dir, ".beads", "issues.jsonl")
	pre, err := os.ReadFile(jsonlPath)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = Build(Opts{Paths: []string{dir}, Classify: true, CommentsRunner: noopComments})
	post, err := os.ReadFile(jsonlPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(pre) != string(post) {
		t.Fatalf("export mutated JSONL")
	}
}

// --- helpers -----------------------------------------------------------

func repeat(n int, c byte) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return string(b)
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
