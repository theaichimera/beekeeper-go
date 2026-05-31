// Package trunksync detects + replays JSONL drift between trunk
// (typically `main`) and the dedicated bead sync branch (typically
// `beads-sync`).
//
// Port of Python beadkeeper.trunksync. Detection is id-aware and
// content-aware (matches the recent regression-test cases):
//
//   - A bead id present on TRUNK but absent from the sync branch is
//     a genuine divergent edit -> RED.
//   - A bead id shared between trunk and sync (even with different
//     content) is NOT a divergence — sync-branch is strictly newer
//     in this topology, so replaying is lossless.
//   - Blob-equal JSONL across the two refs is no drift regardless of
//     commit graph (idempotency after a previous replay commit).
//
// Apply mutates trunk with a SINGLE commit that takes the
// sync-branch JSONL verbatim. Refuses on:
//   - bd daemon alive
//   - dirty working tree / index
//   - divergent trunk edits (operator must `bd sync` first)
//
// No force push, no history rewrite. Caller's branch is restored on
// exit (including error paths).
package trunksync

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/theaichimera/beekeeper-go/internal/git"
	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
)

// JSONLRelPath is the bead JSONL location inside a project.
const JSONLRelPath = ".beads/issues.jsonl"

// DefaultTrunkCandidates is the priority-ordered list used to resolve
// a project's trunk branch.
var DefaultTrunkCandidates = []string{"main", "master", "trunk"}

// Severity mirrors the Python enum, local to this package.
type Severity string

const (
	GREEN  Severity = "green"
	YELLOW Severity = "yellow"
	RED    Severity = "red"
)

var sevOrder = map[Severity]int{GREEN: 0, YELLOW: 1, RED: 2}

// Finding is one row in a scan.
type Finding struct {
	ProjectRoot    string
	Kind           string // "sync-branch-ahead" | "divergent-trunk-edit" | "missing-sync-branch"
	Severity       Severity
	Trunk          string
	SyncBranch     string
	SyncOnlyCount  int
	TrunkOnlyCount int
	Message        string
	Remediation    string
}

// Report aggregates findings across projects.
type Report struct {
	Findings []Finding
}

// Worst is GREEN when empty.
func (r Report) Worst() Severity {
	w := GREEN
	for _, f := range r.Findings {
		if sevOrder[f.Severity] > sevOrder[w] {
			w = f.Severity
		}
	}
	return w
}

// ApplyResult is returned from ApplyOne.
type ApplyResult struct {
	ProjectRoot    string
	DryRun         bool
	WouldApply     bool
	Applied        bool
	Trunk          string
	SyncBranch     string
	SyncOnlyCount  int
	TrunkOnlyCount int
	CommitSHA      string
	Message        string
}

// --- helpers ------------------------------------------------------------

func resolveTrunk(repo string) string {
	for _, b := range DefaultTrunkCandidates {
		if git.BranchExists(repo, b) {
			return b
		}
	}
	return ""
}

func resolveSyncBranch(p bkproject.Project) string {
	return strings.TrimSpace(bkproject.ReadGitState(p).SyncBranch)
}

// jsonlIDsAt returns the set of bead ids present at `<ref>:.beads/issues.jsonl`,
// or (nil, false) if the ref can't be read.
func jsonlIDsAt(repo, ref string) (map[string]struct{}, bool) {
	out, ok := git.Show(repo, ref, JSONLRelPath)
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

func trunkRecordsSubsetOfSync(repo, trunk, sync string) bool {
	tids, ok1 := jsonlIDsAt(repo, trunk)
	sids, ok2 := jsonlIDsAt(repo, sync)
	if !ok1 || !ok2 {
		return false
	}
	for id := range tids {
		if _, ok := sids[id]; !ok {
			return false
		}
	}
	return true
}

func jsonlBlobEqual(repo, a, b string) bool {
	shaA, okA := git.RevParse(repo, a+":"+JSONLRelPath)
	shaB, okB := git.RevParse(repo, b+":"+JSONLRelPath)
	if !okA || !okB {
		return false
	}
	return strings.TrimSpace(shaA) == strings.TrimSpace(shaB)
}

// driftSummary captures the trunk/sync commit graph delta on the JSONL.
type driftSummary struct {
	Trunk      string
	SyncBranch string
	SyncOnly   []string
	TrunkOnly  []string
}

// summarizeDrift returns (nil, false) when there's no comparison to
// make (no trunk or no sync.branch).
func summarizeDrift(p bkproject.Project) (*driftSummary, bool) {
	trunk := resolveTrunk(p.Root)
	sync := resolveSyncBranch(p)
	if trunk == "" || sync == "" {
		return nil, false
	}
	s := &driftSummary{Trunk: trunk, SyncBranch: sync}
	if !git.BranchExists(p.Root, sync) {
		return s, true
	}
	// Blob equality short-circuits idempotency (after a prior replay
	// commit, trunk has a new graph-unique commit but the file is
	// identical to sync).
	if jsonlBlobEqual(p.Root, trunk, sync) {
		return s, true
	}
	s.SyncOnly = git.JSONLCommitsBetween(p.Root, trunk+".."+sync, JSONLRelPath)
	s.TrunkOnly = git.JSONLCommitsBetween(p.Root, sync+".."+trunk, JSONLRelPath)
	// Content-aware suppression: trunk-only commits whose bead ids
	// are a subset of sync are NOT real divergence.
	if len(s.TrunkOnly) > 0 && trunkRecordsSubsetOfSync(p.Root, trunk, sync) {
		s.TrunkOnly = nil
	}
	return s, true
}

// DiagnoseProject yields findings for a single project.
func DiagnoseProject(p bkproject.Project) []Finding {
	var out []Finding
	trunk := resolveTrunk(p.Root)
	sync := resolveSyncBranch(p)
	if trunk == "" || sync == "" {
		return nil
	}
	if !git.BranchExists(p.Root, sync) {
		out = append(out, Finding{
			ProjectRoot: p.Root,
			Kind:        "missing-sync-branch",
			Severity:    YELLOW,
			Trunk:       trunk,
			SyncBranch:  sync,
			Message:     fmt.Sprintf("sync.branch = `%s` but no local branch by that name exists yet.", sync),
			Remediation: fmt.Sprintf(
				"Run `bd sync` once to materialize `%s` from the daemon, or "+
					"`git fetch origin && git branch %s origin/%s` if it exists on the remote.",
				sync, sync, sync,
			),
		})
		return out
	}
	s, _ := summarizeDrift(p)
	if s == nil || (len(s.SyncOnly) == 0 && len(s.TrunkOnly) == 0) {
		return nil
	}
	if len(s.TrunkOnly) > 0 {
		out = append(out, Finding{
			ProjectRoot:    p.Root,
			Kind:           "divergent-trunk-edit",
			Severity:       RED,
			Trunk:          trunk,
			SyncBranch:     sync,
			SyncOnlyCount:  len(s.SyncOnly),
			TrunkOnlyCount: len(s.TrunkOnly),
			Message: fmt.Sprintf(
				"%d JSONL commit(s) on `%s` are missing from `%s`, and %d on `%s` are missing from `%s`. The bead JSONL has diverged.",
				len(s.SyncOnly), sync, trunk, len(s.TrunkOnly), trunk, sync,
			),
			Remediation: "Refuse to auto-reconcile. Run `bd sync` (which uses bd's " +
				"JSONL merge driver to merge cell-wise) and then re-run " +
				"`bk trunk-sync --apply`.",
		})
		return out
	}
	out = append(out, Finding{
		ProjectRoot:    p.Root,
		Kind:           "sync-branch-ahead",
		Severity:       YELLOW,
		Trunk:          trunk,
		SyncBranch:     sync,
		SyncOnlyCount:  len(s.SyncOnly),
		TrunkOnlyCount: 0,
		Message: fmt.Sprintf(
			"%d JSONL commit(s) on `%s` are missing from `%s`. Bead state is stranded against trunk.",
			len(s.SyncOnly), sync, trunk,
		),
		Remediation: "Run `bk trunk-sync --apply` to fast-forward the JSONL onto trunk. " +
			"Daemon must be stopped first.",
	})
	return out
}

// Scan aggregates DiagnoseProject across paths.
func Scan(paths []string, maxDepth int) Report {
	if maxDepth <= 0 {
		maxDepth = bkproject.DefaultMaxDepth
	}
	projects := bkproject.FindProjects(paths, maxDepth)
	var all []Finding
	for _, p := range projects {
		all = append(all, DiagnoseProject(p)...)
	}
	return Report{Findings: all}
}

// --- apply --------------------------------------------------------------

// ApplyOne plans (dryRun=true) or executes (dryRun=false) the replay.
// Refuses on divergent edits, live daemon, or dirty tree.
func ApplyOne(p bkproject.Project, dryRun bool) (ApplyResult, error) {
	res := ApplyResult{ProjectRoot: p.Root, DryRun: dryRun}
	s, ok := summarizeDrift(p)
	if !ok || s == nil {
		res.Message = "No trunk or sync.branch configured for this project."
		return res, nil
	}
	res.Trunk = s.Trunk
	res.SyncBranch = s.SyncBranch
	res.SyncOnlyCount = len(s.SyncOnly)
	res.TrunkOnlyCount = len(s.TrunkOnly)
	res.WouldApply = len(s.SyncOnly) > 0 && len(s.TrunkOnly) == 0

	if len(s.TrunkOnly) > 0 {
		return res, fmt.Errorf(
			"refusing to apply: %d divergent JSONL edit(s) on `%s` since branching from `%s`; "+
				"run `bd sync` to merge cell-wise and re-run",
			len(s.TrunkOnly), s.Trunk, s.SyncBranch,
		)
	}
	if len(s.SyncOnly) == 0 {
		res.Message = "Trunk already includes every bead JSONL commit on the sync branch."
		return res, nil
	}
	if dryRun {
		res.Message = fmt.Sprintf(
			"would fast-forward %d JSONL commit(s) from `%s` onto `%s`.",
			len(s.SyncOnly), s.SyncBranch, s.Trunk,
		)
		return res, nil
	}

	// --- mutation path ---
	daemon := bkproject.ReadDaemonState(p)
	if daemon.PIDAlive {
		return res, fmt.Errorf(
			"refusing to apply: bd daemon (pid %d) is alive for %s; stop the daemon and re-run",
			daemon.PID, p.Root,
		)
	}
	if isIndexOrWorktreeDirty(p.Root) {
		return res, fmt.Errorf(
			"refusing to apply: working tree or index is dirty at %s; "+
				"commit, stash, or discard the changes and re-run",
			p.Root,
		)
	}

	originalBranch, _ := git.CurrentBranch(p.Root)

	if rc, _, errStr, _ := git.Run([]string{"checkout", "-q", s.Trunk}, p.Root, 15*time.Second); rc != 0 {
		return res, fmt.Errorf(
			"refusing to apply: `git checkout %s` failed: %s",
			s.Trunk, strings.TrimSpace(errStr),
		)
	}
	defer func() {
		if originalBranch != "" && originalBranch != s.Trunk {
			_, _, _, _ = git.Run([]string{"checkout", "-q", originalBranch}, p.Root, 15*time.Second)
		}
	}()

	if rc, _, errStr, _ := git.Run(
		[]string{"checkout", s.SyncBranch, "--", JSONLRelPath}, p.Root, 15*time.Second,
	); rc != 0 {
		return res, fmt.Errorf(
			"refusing to apply: `git checkout %s -- %s` failed: %s",
			s.SyncBranch, JSONLRelPath, strings.TrimSpace(errStr),
		)
	}

	// `git diff --cached --quiet` exits 0 when nothing staged.
	rc, _, _, _ := git.Run(
		[]string{"diff", "--cached", "--quiet", "--", JSONLRelPath}, p.Root, 15*time.Second,
	)
	if rc == 0 {
		res.WouldApply = false
		res.Message = "No diff after checkout; trunk already matches sync-branch JSONL."
		return res, nil
	}

	commitMsg := fmt.Sprintf(
		"trunk-sync: replay %d bead JSONL commit(s) from %s onto %s",
		len(s.SyncOnly), s.SyncBranch, s.Trunk,
	)
	if rc, _, errStr, _ := git.Run(
		[]string{"commit", "--no-verify", "-m", commitMsg, "--", JSONLRelPath},
		p.Root, 30*time.Second,
	); rc != 0 {
		return res, fmt.Errorf(
			"refusing to apply: `git commit` failed: %s", strings.TrimSpace(errStr),
		)
	}
	if sha, ok := git.RevParse(p.Root, "HEAD"); ok {
		res.CommitSHA = sha
	}
	res.Applied = true
	res.Message = commitMsg
	return res, nil
}

func isIndexOrWorktreeDirty(repo string) bool {
	out, ok := git.StatusPorcelain(repo)
	if !ok {
		return true
	}
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "??") {
			continue
		}
		return true
	}
	return false
}

// Ensure os import isn't dropped if upstream needs it later.
var _ = os.Stat
