// Package drift is the cheap, commit-time / repo-entry drift gate.
//
// Why it exists: bk's existing drift guards (`pr-beads`, `trunk-sync`) are
// PUSH-gated. A branch with no upstream never triggers them. The bd
// daemon, meanwhile, auto-commits `bd sync` snapshots at COMMIT time.
// Drift therefore originates locally and compounds silently between the
// last manual `bk doctor` and the next push.
//
// Real-world incident (2026-06-01): a branch 513 commits behind
// `origin/main` accumulated 154 `pr-beads` regressions over 6 days
// because the only installed hook was bd's flush-only `pre-commit`,
// which performs no drift check. This package ships the missing
// commit-time signal.
//
// Cheapness contract: the scan runs on every commit (via the bk
// pre-commit hook) and on every repo-entry agent gate. It MUST stay
// cheap — no full pr-beads diff. Allowed primitives:
//
//   - `git rev-list --count <base>..HEAD` (already used elsewhere)
//   - `git symbolic-ref` / `git rev-parse` (one-shot)
//   - reads from `.beads/` (sync.branch, sync-state JSON)
//
// Heavy comparisons (`pr-beads`, `trunk-sync`) stay behind their own
// commands.
package drift

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/theaichimera/beekeeper-go/internal/git"
	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
)

// Severity mirrors the doctor / guard packages.
type Severity string

const (
	GREEN  Severity = "green"
	YELLOW Severity = "yellow"
	RED    Severity = "red"
)

var sevOrder = map[Severity]int{GREEN: 0, YELLOW: 1, RED: 2}

// Finding kinds. Stable strings — the JSON exit shape downstream
// consumers (CI bots, agent loops) keys on these.
const (
	KindBehindBase        = "behind-base"
	KindMissingSyncBranch = "missing-sync-branch"
	KindNeedsManualSync   = "needs-manual-sync"
	KindNotInGitRepo      = "not-in-git-repo"
	KindNoBaseRef         = "no-base-ref"
)

// Finding is one drift hit.
type Finding struct {
	Kind        string
	Severity    Severity
	Message     string
	Remediation string

	// Populated for KindBehindBase.
	Branch      string
	Base        string
	BehindCount int
	Threshold   int
}

// Report aggregates findings.
type Report struct {
	Repo        string
	Branch      string
	Base        string
	BehindCount int
	Threshold   int
	Findings    []Finding
}

// Worst returns the highest severity in the report.
func (r Report) Worst() Severity {
	w := GREEN
	for _, f := range r.Findings {
		if sevOrder[f.Severity] > sevOrder[w] {
			w = f.Severity
		}
	}
	return w
}

// Opts configures the scan.
type Opts struct {
	Repo string
	// Base ref to compare against. Empty -> auto-resolve to remote
	// default branch (origin/<default>) -> origin/main -> main.
	Base string
	// BehindThreshold: warn when current branch is more than N commits
	// behind base. Zero -> DefaultBehindThreshold.
	BehindThreshold int
}

// DefaultBehindThreshold — chosen empirically from the dogfood
// incident: 513 commits behind was unambiguously bad. 50 commits is
// a comfortable middle ground for a feature branch that's stalled but
// hasn't disaster-drifted; agents and humans can lower it via
// `--behind-threshold` or `BK_DRIFT_BEHIND_THRESHOLD`.
const DefaultBehindThreshold = 50

// Scan runs the cheap drift checks. Read-only; no mutations, no
// `bd update` calls, no pr-beads diff. Safe to invoke on every commit.
func Scan(opts Opts) (Report, error) {
	if opts.Repo == "" {
		opts.Repo = "."
	}
	if opts.BehindThreshold <= 0 {
		opts.BehindThreshold = DefaultBehindThreshold
	}
	r := Report{
		Repo:      opts.Repo,
		Threshold: opts.BehindThreshold,
	}

	if !git.Available() {
		r.Findings = append(r.Findings, Finding{
			Kind:        KindNotInGitRepo,
			Severity:    YELLOW,
			Message:     "git is not on PATH; drift gate cannot run.",
			Remediation: "install git and re-run; the gate is silent in non-git contexts otherwise.",
		})
		return r, nil
	}

	branch, _ := git.CurrentBranch(opts.Repo)
	r.Branch = branch

	// Base resolution: explicit -> origin/<default> -> origin/main -> main.
	base := strings.TrimSpace(opts.Base)
	if base == "" {
		if def, ok := git.DefaultRemoteBranch(opts.Repo, "origin"); ok {
			base = "origin/" + def
		}
	}
	if base != "" && !git.RefExists(opts.Repo, base) {
		base = ""
	}
	if base == "" {
		for _, candidate := range []string{"origin/main", "main", "origin/master", "master"} {
			if git.RefExists(opts.Repo, candidate) {
				base = candidate
				break
			}
		}
	}
	if base == "" {
		// Not having a base ref isn't an error — fresh clones, detached
		// branches, etc. Surface as a non-blocking note so an operator
		// who DID expect a base sees something. Caller can promote it
		// to a hard error via --strict.
		r.Findings = append(r.Findings, Finding{
			Kind:     KindNoBaseRef,
			Severity: YELLOW,
			Message: "no base ref resolvable (origin/<default>, origin/main, main, " +
				"origin/master, master); drift gate cannot measure behind-count.",
			Remediation: "set up a remote (`git remote add origin ...`) or pass --base explicitly.",
		})
		return r, nil
	}
	r.Base = base

	// behind-count: HEAD..base (commits in base not in HEAD)
	if branch != "" && branch != base {
		if n, ok := behindCount(opts.Repo, base); ok {
			r.BehindCount = n
			if n > opts.BehindThreshold {
				r.Findings = append(r.Findings, Finding{
					Kind:        KindBehindBase,
					Severity:    YELLOW,
					Branch:      branch,
					Base:        base,
					BehindCount: n,
					Threshold:   opts.BehindThreshold,
					Message: fmt.Sprintf(
						"branch `%s` is %d commit(s) behind `%s` (threshold %d). "+
							"bd may auto-commit a stale snapshot; rebase before further bead work.",
						branch, n, base, opts.BehindThreshold,
					),
					Remediation: fmt.Sprintf(
						"`git fetch origin && git rebase %s` (or merge), "+
							"then re-run `bk doctor` to confirm bead state.", base,
					),
				})
			}
		}
	}

	p := bkproject.Project{Root: opts.Repo}
	gst := bkproject.ReadGitState(p)

	// Empty sync.branch is a documented YELLOW (Python parity); the
	// drift gate surfaces it locally so the operator catches it BEFORE
	// the bd daemon decides which branch to commit on.
	if strings.TrimSpace(gst.SyncBranch) == "" {
		r.Findings = append(r.Findings, Finding{
			Kind:     KindMissingSyncBranch,
			Severity: YELLOW,
			Message: "bd `sync.branch` is empty / unset. Without it, bead writes ride the " +
				"current branch — exactly the drift pattern we're guarding against.",
			Remediation: "configure a dedicated sync branch: " +
				"`bk guard sync-branch --set beads-sync` (or `bd config set sync.branch beads-sync`).",
		})
	}

	// needs_manual_sync from bd sync-state.json — set when bd's sync
	// loop has given up. Drift gate elevates this to RED because it
	// means the daemon is no longer self-healing.
	dst := bkproject.ReadDaemonState(p)
	if dst.LastSyncState != nil {
		if needsManual, _ := dst.LastSyncState["needs_manual_sync"].(bool); needsManual {
			r.Findings = append(r.Findings, Finding{
				Kind:     KindNeedsManualSync,
				Severity: RED,
				Message: "bd sync state reports `needs_manual_sync` — the daemon stopped " +
					"healing automatically. Any bead commit from now until you fix this is drift.",
				Remediation: "run `bd sync` by hand; check `.beads/daemon.log` for the underlying error " +
					"(remote helper missing, auth, push rejected, etc.).",
			})
		}
	}

	return r, nil
}

// behindCount returns the number of commits reachable from `base` but
// not from HEAD, i.e. how far behind the current branch is. The
// `--count` form returns a single integer — cheap.
func behindCount(repo, base string) (int, bool) {
	rc, out, _, _ := git.Run(
		[]string{"rev-list", "--count", "HEAD.." + base},
		repo, 5*time.Second,
	)
	if rc != 0 {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, false
	}
	return n, true
}

// ExitCode maps a Worst() severity to the bk exit-code contract:
//
//	RED                -> 2
//	YELLOW + refuse    -> 2
//	YELLOW + !refuse   -> 1
//	GREEN              -> 0
//
// `refuse=true` is the "block-mode" knob (env: `BK_DRIFT_BLOCK=1`,
// flag: `--strict`). The default is warn-only: exit 1 on YELLOW so an
// agent loop can detect drift without the gate forcing a hard stop.
func ExitCode(worst Severity, refuse bool) int {
	switch worst {
	case RED:
		return 2
	case YELLOW:
		if refuse {
			return 2
		}
		return 1
	}
	return 0
}
