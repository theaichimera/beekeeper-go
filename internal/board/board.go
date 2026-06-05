// Package board is the cross-project work board (ulm.7 in the Python
// tool). Read-only — pure file reads on `.beads/issues.jsonl`.
//
// Buckets: ready / in_progress / blocked. Classification rules mirror
// Python beadkeeper.board EXACTLY:
//
//   - closed: status == "closed". Excluded from buckets, counted in
//     closed_count.
//   - in_progress: status == "in_progress". Lease-gap iff assignee is
//     null/empty.
//   - blocked: status == "blocked" OR any `dependencies[].type ==
//     "blocks"` whose `depends_on_id` resolves IN THE SAME PROJECT to
//     a non-closed issue.
//   - ready: status == "open" and not blocked.
//   - parent-child deps are IGNORED (epic grouping, not gates).
//   - Unresolved blockers (no issue with that id in the project) are
//     non-blocking but recorded on `UnresolvedBlockers`.
package board

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
)

// Bucket names match Python's bucket labels.
const (
	BucketReady      = "ready"
	BucketInProgress = "in_progress"
	BucketBlocked    = "blocked"
)

// ExcludedLabels lists bead labels whose beads are omitted entirely
// from all board / ready / summary surfaces — they are not counted in
// Total or ByStatus. Progression beads are living documents, not
// actionable work (bkg-4zi.5). Configurable by callers; default is the
// single `progression` label.
var ExcludedLabels = []string{"progression"}

// hasExcludedLabel reports whether a record carries any label in
// ExcludedLabels.
func hasExcludedLabel(rec map[string]any) bool {
	raw, ok := rec["labels"].([]any)
	if !ok {
		return false
	}
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			continue
		}
		for _, ex := range ExcludedLabels {
			if s == ex {
				return true
			}
		}
	}
	return false
}

// Issue is one row in a bucket.
type Issue struct {
	ProjectRoot        string
	ID                 string
	Title              string
	Status             string
	Priority           *int // nil when missing / non-integer
	IssueType          string
	Assignee           string
	UpdatedAt          string
	Bucket             string // BucketReady | BucketInProgress | BucketBlocked
	IsLeaseGap         bool
	UnresolvedBlockers []string
}

// ProjectBoard is the per-project breakdown.
type ProjectBoard struct {
	ProjectRoot string
	Ready       []Issue
	InProgress  []Issue
	Blocked     []Issue
	ClosedCount int
	OtherCount  int

	// ByStatus is the raw `status` field count across ALL records in
	// this project, including closed. Distinct from the Ready/InProgress/
	// Blocked buckets — those apply blocking-deps evaluation, this is
	// the unmodified field. Keys: "open", "in_progress", "blocked",
	// "closed", "other".
	ByStatus map[string]int

	// ActiveByPriority counts NON-CLOSED beads by integer priority.
	// Records without a numeric priority go under key -1.
	ActiveByPriority map[int]int

	// Total is the count of all parseable records.
	Total int
}

// LeaseGaps returns the subset of InProgress flagged as gaps.
func (p ProjectBoard) LeaseGaps() []Issue {
	var out []Issue
	for _, i := range p.InProgress {
		if i.IsLeaseGap {
			out = append(out, i)
		}
	}
	return out
}

// Report aggregates per-project breakdowns.
type Report struct {
	Projects []ProjectBoard
}

// Totals returns the same ready/in_progress/blocked/lease_gaps/closed/
// other counts the Python `totals` property emits.
func (r Report) Totals() map[string]int {
	t := map[string]int{
		"ready":       0,
		"in_progress": 0,
		"blocked":     0,
		"closed":      0,
		"other":       0,
		"lease_gaps":  0,
	}
	for _, p := range r.Projects {
		t["ready"] += len(p.Ready)
		t["in_progress"] += len(p.InProgress)
		t["blocked"] += len(p.Blocked)
		t["closed"] += p.ClosedCount
		t["other"] += p.OtherCount
		t["lease_gaps"] += len(p.LeaseGaps())
	}
	return t
}

// Summary is the cross-project rollup used by `bk board --summary`
// and `--json`. Keys are stable for downstream JSON consumers.
type Summary struct {
	Total            int            `json:"total"`
	ByStatus         map[string]int `json:"by_status"`
	ActiveByPriority map[int]int    `json:"active_by_priority"`
	PercentComplete  float64        `json:"percent_complete"`
	InProgress       int            `json:"in_progress_count"`
	LeaseGaps        int            `json:"lease_gaps_count"`
	StaleWIP         int            `json:"stale_wip_count"` // populated by callers (see internal/stalewip).
}

// Aggregate folds per-project counts into a single Summary. Projects
// without a parsable .beads/issues.jsonl contribute zeros.
func (r Report) Aggregate() Summary {
	s := Summary{
		ByStatus:         map[string]int{},
		ActiveByPriority: map[int]int{},
	}
	for _, p := range r.Projects {
		s.Total += p.Total
		for k, v := range p.ByStatus {
			s.ByStatus[k] += v
		}
		for k, v := range p.ActiveByPriority {
			s.ActiveByPriority[k] += v
		}
		s.InProgress += len(p.InProgress)
		s.LeaseGaps += len(p.LeaseGaps())
	}
	if s.Total > 0 {
		s.PercentComplete = float64(s.ByStatus["closed"]) / float64(s.Total) * 100.0
	}
	return s
}

// LeaseGaps returns every lease-gap issue across all projects.
func (r Report) LeaseGaps() []Issue {
	var out []Issue
	for _, p := range r.Projects {
		out = append(out, p.LeaseGaps()...)
	}
	return out
}

// Scan walks `paths` and aggregates per-project boards.
func Scan(paths []string, maxDepth int) Report {
	if maxDepth <= 0 {
		maxDepth = bkproject.DefaultMaxDepth
	}
	projects := bkproject.FindProjects(paths, maxDepth)
	boards := make([]ProjectBoard, 0, len(projects))
	for _, p := range projects {
		boards = append(boards, classifyProject(p))
	}
	sort.Slice(boards, func(i, j int) bool {
		return boards[i].ProjectRoot < boards[j].ProjectRoot
	})
	return Report{Projects: boards}
}

// --- per-project classification -----------------------------------------

func classifyProject(p bkproject.Project) ProjectBoard {
	pb := ProjectBoard{
		ProjectRoot:      p.Root,
		ByStatus:         map[string]int{},
		ActiveByPriority: map[int]int{},
	}

	records := readRecords(p.IssuesJSONL())
	statusByID := buildStatusIndex(records)

	for _, rec := range records {
		id := stringField(rec, "id")
		st := stringField(rec, "status")
		if id == "" {
			continue
		}
		// Progression (and other excluded-label) beads are living
		// documents, not work — drop them from every surface, including
		// Total/ByStatus (bkg-4zi.5).
		if hasExcludedLabel(rec) {
			continue
		}
		pb.Total++
		switch st {
		case "open", "in_progress", "blocked", "closed":
			pb.ByStatus[st]++
		default:
			pb.ByStatus["other"]++
		}
		if st != "closed" {
			pri := -1
			if p := intField(rec, "priority"); p != nil {
				pri = *p
			}
			pb.ActiveByPriority[pri]++
		}
		if rec["status"] == nil {
			pb.OtherCount++
			continue
		}
		if st == "closed" {
			pb.ClosedCount++
			continue
		}

		blockingActive, unresolved := evalBlocks(rec, statusByID)

		issue := Issue{
			ProjectRoot:        p.Root,
			ID:                 id,
			Title:              stringField(rec, "title"),
			Status:             st,
			Priority:           intField(rec, "priority"),
			IssueType:          stringField(rec, "issue_type"),
			Assignee:           stringField(rec, "assignee"),
			UpdatedAt:          stringField(rec, "updated_at"),
			UnresolvedBlockers: unresolved,
		}

		switch {
		case st == "in_progress":
			issue.Bucket = BucketInProgress
			issue.IsLeaseGap = issue.Assignee == ""
			pb.InProgress = append(pb.InProgress, issue)
		case st == "blocked" || blockingActive:
			issue.Bucket = BucketBlocked
			pb.Blocked = append(pb.Blocked, issue)
		case st == "open":
			issue.Bucket = BucketReady
			pb.Ready = append(pb.Ready, issue)
		default:
			pb.OtherCount++
		}
	}

	sortIssues(pb.Ready)
	sortIssues(pb.InProgress)
	sortIssues(pb.Blocked)
	return pb
}

// sortIssues orders by (priority asc, None last, id asc) — matches
// Python's `_key` in board.py.
func sortIssues(xs []Issue) {
	sort.SliceStable(xs, func(i, j int) bool {
		a, b := xs[i], xs[j]
		ap, bp := 1, 1 // 1 == "None group", sorts after 0
		var av, bv int
		if a.Priority != nil {
			ap = 0
			av = *a.Priority
		}
		if b.Priority != nil {
			bp = 0
			bv = *b.Priority
		}
		if ap != bp {
			return ap < bp
		}
		if av != bv {
			return av < bv
		}
		return a.ID < b.ID
	})
}

// evalBlocks evaluates the `dependencies` field for a record. Returns
// (blockingActive, unresolved). Mirrors Python's loop verbatim.
func evalBlocks(rec map[string]any, statusByID map[string]string) (bool, []string) {
	deps, ok := rec["dependencies"].([]any)
	if !ok {
		return false, nil
	}
	var unresolved []string
	blocking := false
	for _, raw := range deps {
		dep, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if dep["type"] != "blocks" {
			continue
		}
		tgt, ok := dep["depends_on_id"].(string)
		if !ok || tgt == "" {
			continue
		}
		st, present := statusByID[tgt]
		if !present {
			unresolved = append(unresolved, tgt)
			continue
		}
		if st != "closed" {
			blocking = true
		}
	}
	return blocking, unresolved
}

// --- JSONL reading ------------------------------------------------------

func readRecords(path string) []map[string]any {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 1024*1024)
	sc.Buffer(buf, 16*1024*1024)
	var out []map[string]any
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		out = append(out, rec)
	}
	return out
}

func buildStatusIndex(recs []map[string]any) map[string]string {
	idx := map[string]string{}
	for _, r := range recs {
		id := stringField(r, "id")
		st := stringField(r, "status")
		if id != "" && st != "" {
			idx[id] = st
		}
	}
	return idx
}

// --- tiny helpers -------------------------------------------------------

func stringField(rec map[string]any, key string) string {
	if v, ok := rec[key].(string); ok {
		return v
	}
	return ""
}

func intField(rec map[string]any, key string) *int {
	switch v := rec[key].(type) {
	case float64:
		// JSON numbers decode as float64.
		i := int(v)
		return &i
	case int:
		return &v
	case string:
		// Some tools emit "P1" or "1" strings; only "<digits>" matches.
		n := 0
		valid := false
		for _, r := range v {
			if r < '0' || r > '9' {
				valid = false
				break
			}
			n = n*10 + int(r-'0')
			valid = true
		}
		if valid {
			return &n
		}
	}
	return nil
}

// helper exposed for callers that want to format a project root the
// same way the board renders it.
func RelativeJSONLPath() string {
	return filepath.Join(".beads", "issues.jsonl")
}
