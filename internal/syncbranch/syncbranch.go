// Package syncbranch detects bead-data commits stranded on non-sync
// branches and other sync-branch misconfigurations.
//
// Port of Python beadkeeper.syncbranch (DETECTION ONLY for M2 — the
// --set mutation path lands in M3 alongside the daemon-live refusal
// contract).
//
// Detection is content-aware (id-based): a branch whose JSONL records
// are a strict subset of the sync branch's JSONL records is NOT
// stranded, even if it has unique commits in the commit graph. This
// is the recently hardened semantic from the Python repo (see
// `_branch_records_subset_of_sync` / `_jsonl_records_at`).
package syncbranch

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/theaichimera/beekeeper-go/internal/git"
	"github.com/theaichimera/beekeeper-go/internal/project"
)

// JSONLRelPath is the bead JSONL location inside a project.
const JSONLRelPath = ".beads/issues.jsonl"

// Severity mirrors Python's syncbranch.Severity. Local to this
// package so callers don't tangle it with doctor.Severity.
type Severity string

const (
	GREEN  Severity = "green"
	YELLOW Severity = "yellow"
	RED    Severity = "red"
)

var sevOrder = map[Severity]int{GREEN: 0, YELLOW: 1, RED: 2}

// Finding is one row in a per-project scan.
type Finding struct {
	ProjectRoot string
	Kind        string // "empty-sync-branch" | "stranded-bead-commit" | "missing-sync-branch"
	Severity    Severity
	Branch      string // populated for "stranded-bead-commit"
	CommitCount int
	Message     string
	Remediation string
}

// Report is the cross-project scan result.
type Report struct {
	Findings []Finding
}

// Worst returns the worst severity across findings (GREEN when empty).
func (r Report) Worst() Severity {
	w := GREEN
	for _, f := range r.Findings {
		if sevOrder[f.Severity] > sevOrder[w] {
			w = f.Severity
		}
	}
	return w
}

// DiagnoseProject reports findings for a single project. Mirrors
// Python `_diagnose_project`.
func DiagnoseProject(p project.Project) []Finding {
	state := project.ReadGitState(p)
	sb := strings.TrimSpace(state.SyncBranch)

	if sb == "" {
		return []Finding{{
			ProjectRoot: p.Root,
			Kind:        "empty-sync-branch",
			Severity:    YELLOW,
			Message: "bd config `sync.branch` is empty or unset. Bead writes will ride " +
				"whichever branch you're on and can strand on feature branches.",
			Remediation: "Configure a dedicated sync branch: " +
				"`bk guard sync-branch --set beads-sync` " +
				"or `bd config set sync.branch beads-sync`.",
		}}
	}

	branches := localBranches(p.Root)
	if !contains(branches, sb) {
		return []Finding{{
			ProjectRoot: p.Root,
			Kind:        "missing-sync-branch",
			Severity:    YELLOW,
			Message:     "sync.branch = " + sb + ", but no local branch by that name exists.",
			Remediation: "Create the branch (`git branch " + sb + "`) or run `bd sync` to bootstrap it. " +
				"Without it we cannot tell which JSONL commits are stranded.",
		}}
	}

	var out []Finding
	for _, branch := range branches {
		if branch == sb {
			continue
		}
		if branchRecordsSubsetOfSync(p.Root, branch, sb) {
			continue
		}
		n := strandedCommitCount(p.Root, branch, sb)
		if n > 0 {
			out = append(out, Finding{
				ProjectRoot: p.Root,
				Kind:        "stranded-bead-commit",
				Severity:    RED,
				Branch:      branch,
				CommitCount: n,
				Message: countString(n) + " on `" + branch + "` modify `.beads/issues.jsonl` " +
					"and are not reachable from sync.branch `" + sb + "`. Bead state is stranded.",
				Remediation: "Move bead writes to `" + sb + "` (the configured sync branch). " +
					"For history already stranded, cherry-pick or replay the JSONL onto `" + sb + "`. " +
					"Going forward, run `bd sync` after every bead mutation so the daemon " +
					"commits on the sync branch instead of the feature branch.",
			})
		}
	}
	return out
}

// Scan walks roots and aggregates per-project findings.
func Scan(paths []string, maxDepth int) Report {
	if maxDepth <= 0 {
		maxDepth = project.DefaultMaxDepth
	}
	projects := project.FindProjects(paths, maxDepth)
	var all []Finding
	for _, p := range projects {
		all = append(all, DiagnoseProject(p)...)
	}
	return Report{Findings: all}
}

// --- helpers ------------------------------------------------------------

func localBranches(cwd string) []string {
	rc, out, _, _ := git.Run(
		[]string{"for-each-ref", "--format=%(refname:short)", "refs/heads/"},
		cwd, 10*time.Second,
	)
	if rc != 0 {
		return nil
	}
	var bs []string
	for _, line := range strings.Split(out, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			bs = append(bs, s)
		}
	}
	sort.Strings(bs)
	return bs
}

func strandedCommitCount(cwd, branch, syncBranch string) int {
	rc, out, _, _ := git.Run(
		[]string{
			"log", "--no-merges",
			"refs/heads/" + branch,
			"^refs/heads/" + syncBranch,
			"--pretty=%H",
			"--", JSONLRelPath,
		},
		cwd, 15*time.Second,
	)
	if rc != 0 {
		return 0
	}
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// branchRecordsSubsetOfSync: true iff every bead id present on
// `branch`'s JSONL is also present on `syncBranch`'s JSONL.
// Content-aware stranding suppression — mirrors Python's
// `_branch_records_subset_of_sync`. Reads via `git show <ref>:<path>`,
// no checkout.
func branchRecordsSubsetOfSync(cwd, branch, syncBranch string) bool {
	branchRecs, ok := jsonlIDsAt(cwd, branch)
	if !ok {
		return false
	}
	syncRecs, ok := jsonlIDsAt(cwd, syncBranch)
	if !ok {
		return false
	}
	for id := range branchRecs {
		if _, ok := syncRecs[id]; !ok {
			return false
		}
	}
	return true
}

// jsonlIDsAt returns the set of bead ids present in
// `<ref>:.beads/issues.jsonl`, or (nil, false) if the file can't be
// read. An empty file yields an empty set with ok=true.
func jsonlIDsAt(cwd, ref string) (map[string]struct{}, bool) {
	out, ok := git.Show(cwd, ref, JSONLRelPath)
	if !ok {
		return nil, false
	}
	ids := map[string]struct{}{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if id, ok := rec["id"].(string); ok && id != "" {
			ids[id] = struct{}{}
		}
	}
	return ids, true
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func countString(n int) string {
	if n == 1 {
		return "1 commit"
	}
	// pluralization mirrors Python's "n commit(s)" exactly:
	// Python prints `{n} commit(s)`. Match that string for parity.
	return formatInt(n) + " commit(s)"
}

func formatInt(n int) string {
	// strconv.Itoa-free path to keep import surface small in this
	// hot package; n is always non-negative here.
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "-" + formatInt(-n)
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}
