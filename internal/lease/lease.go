// Package lease wraps `bd update --status in_progress --assignee
// <canonical>` for claim/release plus a stale-aware list across
// projects.
//
// Port of Python beadkeeper.lease. Safety semantics replicated
// verbatim:
//
//   - claim/release REFUSE while the bd daemon is alive (mutation rule).
//   - claim is IDEMPOTENT for the holder (no bd call); conflict raises
//     LeaseConflict (a typed subset of LeaseError).
//   - release by non-holder is a LeaseConflict; release of an unclaimed
//     issue is a no-op success.
//   - bd is invoked through an injectable BdRunner so unit tests don't
//     need a real bd binary or workspace.
package lease

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/theaichimera/beekeeper-go/internal/config"
	execwrap "github.com/theaichimera/beekeeper-go/internal/exec"
	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
)

// BdRunner mirrors Python's `BdRunner` typing alias: invoke `bd <args>`
// in `cwd` and return (rc, stdout, stderr, err).
type BdRunner = execwrap.Runner

// DefaultStaleAfterSeconds = 7 days, mirroring Python.
const DefaultStaleAfterSeconds = 7 * 24 * 3600

// --- errors -------------------------------------------------------------

// LeaseError is the base error for lease ops. Mirrors Python.
type LeaseError struct{ Msg string }

func (e *LeaseError) Error() string { return e.Msg }

// LeaseConflict is a typed subset signalling "different canonical
// holds the lease". `errors.As` callers can distinguish it from a
// generic LeaseError.
type LeaseConflict struct{ Msg string }

func (e *LeaseConflict) Error() string { return e.Msg }

// IsLeaseError reports whether `err` is a *LeaseError OR *LeaseConflict
// (a *LeaseConflict is treated as a LeaseError per Python's class
// hierarchy: LeaseConflict(LeaseError)(RuntimeError)).
func IsLeaseError(err error) bool {
	var le *LeaseError
	var lc *LeaseConflict
	return errors.As(err, &le) || errors.As(err, &lc)
}

// --- result types -------------------------------------------------------

// ClaimResult is returned on a successful claim.
type ClaimResult struct {
	IssueID        string
	Canonical      string
	Success        bool
	AlreadyHeld    bool
	AssigneeBefore string // empty when no previous assignee
}

// ReleaseResult is returned on a successful release.
type ReleaseResult struct {
	IssueID         string
	Canonical       string
	Success         bool
	AlreadyReleased bool
}

// Lease is one row in a list scan.
type Lease struct {
	ProjectRoot string
	IssueID     string
	Assignee    string
	UpdatedAt   string  // ISO-8601 raw
	AgeSeconds  float64 // 0 when unparseable
	HasAge      bool
	IsStale     bool
}

// ListReport is the cross-project list result.
type ListReport struct {
	Leases []Lease
}

// Stale returns only the leases flagged as stale.
func (r ListReport) Stale() []Lease {
	var out []Lease
	for _, l := range r.Leases {
		if l.IsStale {
			out = append(out, l)
		}
	}
	return out
}

// --- caller resolution --------------------------------------------------

// RawHandle resolves the caller's raw (pre-canonical) handle from the
// usual env vars + `git config user.email`. Matches Python
// `_default_raw_handle`.
func RawHandle() string {
	for _, key := range []string{"BD_ACTOR", "GIT_AUTHOR_NAME"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	out, err := exec.Command("git", "config", "user.email").Output()
	if err == nil {
		if v := strings.TrimSpace(string(out)); v != "" {
			return v
		}
	}
	for _, key := range []string{"USER", "USERNAME"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

// ResolveCaller maps `rawHandle` (or the auto-detected default) to the
// canonical handle declared in `.beadkeeper/identity.toml`.
//
// Identity is OPT-IN per `cmd/bk/identity.go`: a workspace without
// `.beadkeeper/identity.toml` SHOULD still be able to use lease
// commands. Behavior:
//
//   - cfg == nil (no identity.toml): degrade to the raw handle from
//     BD_ACTOR / GIT_AUTHOR_NAME / `git config user.email` (RawHandle).
//     The raw handle becomes the canonical for this call. The downstream
//     `bd update --assignee <handle>` happily accepts it.
//   - cfg != nil and handle is canonical / aliased: return the canonical.
//   - cfg != nil and handle is UNKNOWN: error "unknown alias" (preserves
//     the strict-mode behavior the bkg-bqa.* identity tests rely on).
//   - No raw handle resolvable at all: error regardless of cfg.
//
// Fixes bkg-8nw: previously hard-failed on missing identity.toml,
// contradicting the opt-in framing.
func ResolveCaller(repo string, rawHandle string) (string, error) {
	cfg, _ := config.LoadIdentityConfig(repo)
	raw := rawHandle
	if raw == "" {
		raw = RawHandle()
	}
	if raw == "" {
		return "", &LeaseError{Msg: "could not determine the caller's handle " +
			"(BD_ACTOR, GIT_AUTHOR_NAME, or `git config user.email` are all unset)."}
	}
	if cfg == nil {
		// Opt-in: no identity config -> raw handle is canonical.
		return raw, nil
	}
	if mapped := cfg.Map(raw); mapped != "" {
		return mapped, nil
	}
	return "", &LeaseError{Msg: fmt.Sprintf(
		"handle %q is not a canonical identity nor a known alias. "+
			"Add it to `.beadkeeper/identity.toml` under `[identity].canonical` "+
			"or `[identity.aliases]`.", raw,
	)}
}

// --- core mutations -----------------------------------------------------

func refuseOnLiveDaemon(repo string) error {
	st := bkproject.ReadDaemonState(bkproject.Project{Root: repo})
	if st.PIDAlive {
		return &LeaseError{Msg: fmt.Sprintf(
			"refusing: bd daemon (pid %d) is alive for %s. Stop the daemon and re-run.",
			st.PID, repo,
		)}
	}
	return nil
}

func defaultRunner(args []string, cwd string, timeout time.Duration) (int, string, string, error) {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return execwrap.Default(args, cwd, timeout)
}

// Claim claims the lease on `issueID` for `caller`. Idempotent when
// the caller already holds it. Raises *LeaseConflict on contention.
func Claim(repo, issueID, caller string, runner BdRunner) (ClaimResult, error) {
	repoAbs := absResolve(repo)
	issue, err := findIssue(repoAbs, issueID)
	if err != nil {
		return ClaimResult{}, err
	}
	currentStatus, _ := issue["status"].(string)
	currentAssignee := nonEmptyString(issue["assignee"])

	if currentStatus == "in_progress" && currentAssignee != "" {
		if currentAssignee == caller {
			return ClaimResult{
				IssueID:        issueID,
				Canonical:      caller,
				Success:        true,
				AlreadyHeld:    true,
				AssigneeBefore: currentAssignee,
			}, nil
		}
		return ClaimResult{}, &LeaseConflict{Msg: fmt.Sprintf(
			"refusing to claim %s: held by '%s', not '%s'.", issueID, currentAssignee, caller,
		)}
	}

	if err := refuseOnLiveDaemon(repoAbs); err != nil {
		return ClaimResult{}, err
	}
	if runner == nil {
		runner = defaultRunner
	}
	rc, _, errStr, _ := runner(
		[]string{"bd", "update", issueID, "--status", "in_progress", "--assignee", caller},
		repoAbs, 30*time.Second,
	)
	if rc != 0 {
		return ClaimResult{}, &LeaseError{Msg: fmt.Sprintf(
			"bd update failed for %s: rc=%d err=%q", issueID, rc, strings.TrimSpace(errStr),
		)}
	}
	return ClaimResult{
		IssueID:        issueID,
		Canonical:      caller,
		Success:        true,
		AssigneeBefore: currentAssignee,
	}, nil
}

// Release frees the lease on `issueID` if held by `caller`. No-op
// success when the issue isn't claimed. *LeaseConflict on contention.
func Release(repo, issueID, caller string, runner BdRunner) (ReleaseResult, error) {
	repoAbs := absResolve(repo)
	issue, err := findIssue(repoAbs, issueID)
	if err != nil {
		return ReleaseResult{}, err
	}
	currentStatus, _ := issue["status"].(string)
	currentAssignee := nonEmptyString(issue["assignee"])

	if currentStatus != "in_progress" || currentAssignee == "" {
		return ReleaseResult{
			IssueID:         issueID,
			Canonical:       caller,
			Success:         true,
			AlreadyReleased: true,
		}, nil
	}
	if currentAssignee != caller {
		return ReleaseResult{}, &LeaseConflict{Msg: fmt.Sprintf(
			"refusing to release %s: held by '%s', not '%s'.", issueID, currentAssignee, caller,
		)}
	}

	if err := refuseOnLiveDaemon(repoAbs); err != nil {
		return ReleaseResult{}, err
	}
	if runner == nil {
		runner = defaultRunner
	}
	rc, _, errStr, _ := runner(
		[]string{"bd", "update", issueID, "--status", "open", "--assignee", ""},
		repoAbs, 30*time.Second,
	)
	if rc != 0 {
		return ReleaseResult{}, &LeaseError{Msg: fmt.Sprintf(
			"bd update failed for %s: rc=%d err=%q", issueID, rc, strings.TrimSpace(errStr),
		)}
	}
	return ReleaseResult{
		IssueID:   issueID,
		Canonical: caller,
		Success:   true,
	}, nil
}

// --- list ---------------------------------------------------------------

// ListLeases enumerates active leases across projects under `paths`.
func ListLeases(paths []string, maxDepth, staleAfterSeconds int) ListReport {
	if maxDepth <= 0 {
		maxDepth = bkproject.DefaultMaxDepth
	}
	if staleAfterSeconds <= 0 {
		staleAfterSeconds = DefaultStaleAfterSeconds
	}
	projects := bkproject.FindProjects(paths, maxDepth)
	out := ListReport{}
	now := float64(time.Now().Unix())
	for _, p := range projects {
		for rec := range iterJSONL(p.IssuesJSONL()) {
			if rec["status"] != "in_progress" {
				continue
			}
			assignee := nonEmptyString(rec["assignee"])
			if assignee == "" {
				continue
			}
			updatedAt, _ := rec["updated_at"].(string)
			age, hasAge := parseTimestampAge(updatedAt, now)
			l := Lease{
				ProjectRoot: p.Root,
				IssueID:     stringOr(rec["id"], ""),
				Assignee:    assignee,
				UpdatedAt:   updatedAt,
				AgeSeconds:  age,
				HasAge:      hasAge,
				IsStale:     hasAge && age > float64(staleAfterSeconds),
			}
			out.Leases = append(out.Leases, l)
		}
	}
	sort.SliceStable(out.Leases, func(i, j int) bool {
		if out.Leases[i].ProjectRoot != out.Leases[j].ProjectRoot {
			return out.Leases[i].ProjectRoot < out.Leases[j].ProjectRoot
		}
		return out.Leases[i].IssueID < out.Leases[j].IssueID
	})
	return out
}

// --- internals ----------------------------------------------------------

func iterJSONL(path string) chan map[string]any {
	ch := make(chan map[string]any)
	go func() {
		defer close(ch)
		f, err := os.Open(path)
		if err != nil {
			return
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		buf := make([]byte, 0, 1024*1024)
		sc.Buffer(buf, 16*1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var rec map[string]any
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				continue
			}
			ch <- rec
		}
	}()
	return ch
}

func findIssue(repo, issueID string) (map[string]any, error) {
	p := bkproject.Project{Root: repo}
	for rec := range iterJSONL(p.IssuesJSONL()) {
		if id, _ := rec["id"].(string); id == issueID {
			return rec, nil
		}
	}
	return nil, &LeaseError{Msg: fmt.Sprintf(
		"issue not found in %s: %s", filepath.Join(repo, ".beads", "issues.jsonl"), issueID,
	)}
}

func absResolve(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

func nonEmptyString(v any) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return ""
}

func stringOr(v any, dflt string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return dflt
}

func parseTimestampAge(updatedAt string, nowEpoch float64) (float64, bool) {
	if updatedAt == "" {
		return 0, false
	}
	// Tolerate both "Z" and "+00:00" suffixes.
	s := strings.Replace(updatedAt, "Z", "+00:00", 1)
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0, false
	}
	age := nowEpoch - float64(t.Unix())
	if age < 0 {
		age = 0
	}
	return age, true
}
