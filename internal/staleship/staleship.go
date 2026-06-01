// Package staleship is the post-merge sibling of `bk guard pr-beads`.
// It catches the inverse failure mode: a bead's work has LANDED on the
// default branch (its id appears in a merged commit/PR subject) but the
// bead is still open or in_progress in `.beads/issues.jsonl`.
//
// Motivating evidence: a real review of a 627-bead backlog found ~18
// of 33 in_progress beads in this state — work that shipped, beads
// that were never moved to closed. Detecting this with raw tools means
// running `git log <branch> --grep=<prefix>-<id>` once per bead by
// hand. `bk guard stale-beads` is the deterministic version.
//
// Match precision (the make-or-break detail per the spec):
//
//   - The bead id MUST appear in the commit SUBJECT line, not just
//     anywhere in the body. Tangential references in commit bodies
//     ("see prefix-afby for context") are NOT shipped.
//   - The bead id MUST be flanked by characters that are NOT part of
//     the id alphabet (letters / digits / `_` / `.` / `-`). Common
//     forms: `(prefix-id)`, `prefix-id:`, `[prefix-id]`, leading or
//     trailing whitespace, end-of-line.
//   - The bead id is the FULL string (e.g. `prefix-1k8x.1`), so
//     `prefix-1k8x` does NOT match `prefix-1k8x.1` and vice versa.
//
// Severity: shipped-not-closed beads are RED (the bead is lying about
// reality and a CI gate should fail). YELLOW + advisory variants
// (closed-no-landing) are deferred.
//
// Read-only. No JSONL mutation, no `bd` shell-out, no daemon touch.
// Closing a bead remains a human/agent decision; this package returns
// a `--json` shape that lets a downstream agent drive
// `bd close <id> --reason "shipped in #<PR>"` deterministically.
package staleship

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/theaichimera/beekeeper-go/internal/git"
	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
)

// JSONLRelPath duplicates the bead JSONL constant — this package is
// import-isolated from `board` / `prbeads`.
const JSONLRelPath = ".beads/issues.jsonl"

// Severity stays binary in v1 — RED on shipped-not-closed, GREEN
// otherwise. YELLOW reserved for the deferred closed-no-landing
// advisory.
type Severity string

const (
	GREEN Severity = "green"
	RED   Severity = "red"
)

// Finding is one shipped-not-closed bead.
type Finding struct {
	BeadID         string
	Status         string // "open" | "in_progress"
	LandingSHA     string
	LandingSubject string
	LandingPR      int // 0 when no `(#N)` marker found
}

// Report is the full diagnose result.
type Report struct {
	Branch   string
	Prefix   string
	Lookback int // days; 0 == no filter
	Findings []Finding
}

// Opts controls Diagnose behavior. Zero value means defaults.
type Opts struct {
	// LookbackDays bounds `git log --since`; 0 means no filter.
	LookbackDays int

	// AllowedShipTypes is the set of conventional-commit types treated
	// as shipping the named bead. Nil/empty means the default
	// implementation allowlist: feat, fix, perf, refactor.
	//
	// `spec` and any subject whose scope is `bd` or `beads` are
	// ALWAYS treated as non-shipping regardless of this list — those
	// are bd-management commits (filing / updating beads), not
	// implementation work. Spec rules are codified in nonShippingScopes
	// and parseConvCommit.
	AllowedShipTypes []string

	// StatusSource controls where bead status is read from:
	//
	//   StatusSourceAuto   (default): try `bd list --json`, fall back
	//                                  to JSONL on bd error.
	//   StatusSourceBd:                require `bd list --json`; error
	//                                  when bd is unavailable.
	//   StatusSourceJSONL:              read `.beads/issues.jsonl` only
	//                                  (legacy bkg-bqa.3 behavior).
	//
	// The default uses bd as the authoritative source per bkg-td0.2 —
	// `.beads/issues.jsonl` lags the bd SQLite DB after a `bd close`
	// and produced false positives in the dogfood run.
	StatusSource StatusSource
}

// StatusSource selects the bead-status read strategy. See Opts.
type StatusSource int

const (
	// StatusSourceAuto tries bd first, then JSONL.
	StatusSourceAuto StatusSource = iota
	// StatusSourceBd reads only from bd (errors if bd unavailable).
	StatusSourceBd
	// StatusSourceJSONL reads only the JSONL on disk.
	StatusSourceJSONL
)

// DefaultShipTypes is the default conventional-commit type allowlist:
// implementation work that genuinely lands a bead. Bumped to a public
// constant so callers can extend it (e.g. add `docs` for repos that
// ship doc beads).
var DefaultShipTypes = []string{"feat", "fix", "perf", "refactor"}

// Worst returns RED when any findings exist.
func (r Report) Worst() Severity {
	if len(r.Findings) == 0 {
		return GREEN
	}
	return RED
}

// Diagnose is the shipped-not-closed scanner. Existing callers pass
// `lookbackDays` directly; new callers can use DiagnoseWithOpts to
// configure the conventional-commit allowlist (bkg-td0.1).
func Diagnose(repo, branch, prefix string, lookbackDays int) (Report, error) {
	return DiagnoseWithOpts(repo, branch, prefix, Opts{LookbackDays: lookbackDays})
}

// DiagnoseWithOpts is the option-bearing scanner. Subjects are
// classified per parseConvCommit + the supplied allowlist:
//
//   - `spec(<id>): ...` is NEVER shipping (bead filing / spec writing).
//   - Subjects with scope `bd` or `beads` are NEVER shipping
//     (`chore(bd): file <id>` etc. — bd-management).
//   - Conventional-commit types not in the allowlist are NEVER shipping.
//   - Bare-prefix subjects (`<id>:`, `[<id>]`) bypass the type filter
//     because they're explicit bead-led landings.
//   - PR-merge mode (`(#N)` marker) requires the type to be in the
//     allowlist — otherwise a `chore(deps): bump foo (#42)` subject
//     would falsely ship any token-bounded id elsewhere in it.
//
// Default allowlist: feat / fix / perf / refactor (DefaultShipTypes).
func DiagnoseWithOpts(repo, branch, prefix string, opts Opts) (Report, error) {
	r := Report{
		Branch:   branch,
		Prefix:   prefix,
		Lookback: opts.LookbackDays,
	}
	if !git.Available() {
		return r, fmt.Errorf("git binary not on PATH")
	}
	if prefix == "" {
		return r, fmt.Errorf("empty bead-id prefix")
	}
	if !git.RefExists(repo, branch) {
		return r, fmt.Errorf("ref not found: %s", branch)
	}

	allowed := allowedTypeSet(opts.AllowedShipTypes)

	openIDs, err := readOpenIssueIDsFrom(repo, opts.StatusSource)
	if err != nil {
		return r, err
	}
	if len(openIDs) == 0 {
		return r, nil
	}

	// One `git log` call gets every candidate commit. We post-filter
	// to enforce subject-line + token-boundary precision.
	args := []string{
		"log",
		"--no-merges",
		// Pretty: SHA \x00 SUBJECT (single-line) — \x00 because subjects
		// frequently contain '\t'/':'/etc.
		`--pretty=%H%x00%s`,
		"--grep=" + prefix,
		branch,
	}
	if opts.LookbackDays > 0 {
		since := time.Now().AddDate(0, 0, -opts.LookbackDays).Format("2006-01-02")
		args = append(args, "--since="+since)
	}
	rc, stdout, _, _ := git.Run(args, repo, 30*time.Second)
	if rc != 0 {
		// Empty result is rc 0 with empty stdout. Non-zero is a real error.
		return r, fmt.Errorf("git log failed (rc=%d) on %s", rc, branch)
	}

	// Per-id matchers used by subjectShipsBead.
	tokens := make(map[string]*regexp.Regexp, len(openIDs))
	scopes := make(map[string]*regexp.Regexp, len(openIDs))
	for id := range openIDs {
		tokens[id] = idMatcher(id)
		scopes[id] = scopeMatcher(id)
	}

	prRe := regexp.MustCompile(`\(#(\d+)\)`)
	seen := map[string]struct{}{}

	for _, line := range strings.Split(stdout, "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x00", 2)
		if len(parts) != 2 {
			continue
		}
		sha, subject := parts[0], parts[1]
		hasPR := prRe.MatchString(subject)
		shape := parseConvCommit(subject)
		if !shape.eligibleForShipping(allowed) {
			continue
		}
		for id := range openIDs {
			if _, already := seen[id]; already {
				continue
			}
			if !subjectShipsBead(subject, hasPR, tokens[id], scopes[id], shape, allowed) {
				continue
			}
			pr := 0
			if m := prRe.FindStringSubmatch(subject); m != nil {
				if n, err := parseIntStrict(m[1]); err == nil {
					pr = n
				}
			}
			r.Findings = append(r.Findings, Finding{
				BeadID:         id,
				Status:         openIDs[id],
				LandingSHA:     sha,
				LandingSubject: subject,
				LandingPR:      pr,
			})
			seen[id] = struct{}{}
		}
	}

	sort.Slice(r.Findings, func(i, j int) bool {
		// Stable: by PR number (asc, missing last), then bead id.
		a, b := r.Findings[i], r.Findings[j]
		if (a.LandingPR == 0) != (b.LandingPR == 0) {
			return a.LandingPR != 0
		}
		if a.LandingPR != b.LandingPR {
			return a.LandingPR < b.LandingPR
		}
		return a.BeadID < b.BeadID
	})
	return r, nil
}

// idMatcher returns a regexp that matches `id` as a delimited token in
// a commit subject. Token boundary: start-of-string, end-of-string, or
// any character NOT in the id alphabet (letters / digits / `_` / `.` /
// `-`). Compound ids like `prefix-1k8x.1` match literally because the
// id is regexp-escaped.
func idMatcher(id string) *regexp.Regexp {
	return regexp.MustCompile(
		`(^|[^A-Za-z0-9_.\-])` + regexp.QuoteMeta(id) + `([^A-Za-z0-9_.\-]|$)`,
	)
}

// scopeMatcher returns a regexp that matches `id` only when it appears
// in the conventional-commit SCOPE position at the start of the
// subject — `^<type>(<id>):`, `^<id>:`, or `^[<id>]`. These positions
// signal an authoritative landing for the bead, distinct from
// tangential mentions.
func scopeMatcher(id string) *regexp.Regexp {
	q := regexp.QuoteMeta(id)
	// (1) `feat(<id>): ...`            // conventional scope
	// (2) `<id>: ...`                  // bare prefix
	// (3) `[<id>] ...`                 // bracket prefix
	return regexp.MustCompile(
		`^(?:[A-Za-z]+\(` + q + `\):|` + q + `:|\[` + q + `\])`,
	)
}

// subjectShipsBead returns true iff `subject` should be treated as
// shipping the bead. Two acceptance modes:
//
//   - SCOPE mode:  the bead id is the conventional-commit scope OR
//     bare prefix OR bracket prefix at the very start of the subject.
//     This catches `feat(demo-x): ...`, `demo-x: ...`, `[demo-x] ...`.
//
//   - PR-MERGE mode: the subject ends with a `(#N)` PR-merge marker
//     (the GitHub squash-merge convention) AND the id appears as a
//     delimited token anywhere in the subject. This catches
//     multi-id subjects like `feat: demo-0dh4 + demo-fz56 land
//     together (#738)`.
//
// Subjects without either signature — e.g. `chore: bump deps (no
// bead) — see demo-afby for context` — are TANGENTIAL and skipped.
// The `see / later / context` pattern that drove false positives in
// the motivating run does NOT trigger either mode by construction.
//
// PR-MERGE mode additionally requires the conventional-commit type to
// be in the allowed allowlist (passed via `shape` + `allowed`). This
// is the bd-management filter from bkg-td0.1: a `chore(bd):` or
// `chore(deps):` subject with a `(#N)` marker would otherwise falsely
// ship any token-bounded bead id elsewhere in it. SCOPE mode is
// already type-gated upstream by eligibleForShipping (the caller
// short-circuits before this function fires).
func subjectShipsBead(subject string, hasPR bool, token, scope *regexp.Regexp, shape convCommit, allowed map[string]bool) bool {
	if scope.MatchString(subject) {
		return true
	}
	if hasPR && token.MatchString(subject) {
		// PR-merge mode is only safe for known-implementation subjects:
		// either the type is allowlisted, OR the subject has no
		// conventional prefix at all (in which case any `(#N)`
		// landing on a token-bounded id is the agent-of-record signal).
		if shape.ctype == "" || allowed[shape.ctype] {
			return true
		}
	}
	return false
}

// convCommit is a parsed conventional-commit subject head. Empty
// strings mean the subject didn't follow the `<type>(<scope>):` form
// at the start.
type convCommit struct {
	ctype string
	scope string
}

var convCommitRe = regexp.MustCompile(`^([A-Za-z]+)(?:\(([^)]*)\))?:`)

// parseConvCommit extracts the leading `<type>` and optional
// `<scope>` from a commit subject. Returns the zero value when the
// subject doesn't open with a conventional prefix.
func parseConvCommit(subject string) convCommit {
	m := convCommitRe.FindStringSubmatch(subject)
	if m == nil {
		return convCommit{}
	}
	return convCommit{ctype: strings.ToLower(m[1]), scope: strings.ToLower(strings.TrimSpace(m[2]))}
}

// nonShippingScopes lists conventional-commit scopes that always
// suppress shipping regardless of type. These cover bd-management
// commits whose subjects name beads they file or update — exactly
// the false-positive class that bkg-td0.1 fixes.
var nonShippingScopes = map[string]bool{
	"bd":    true,
	"beads": true,
}

// nonShippingTypes lists conventional-commit types that always
// suppress shipping. `spec` is the bead-spec / epic-filing pattern.
var nonShippingTypes = map[string]bool{
	"spec": true,
}

// eligibleForShipping returns false when the subject's type / scope
// rules out shipping a bead regardless of id-position. Subjects with
// no conventional prefix (ctype == "") are eligible — the bare-prefix
// `<id>:` and `[<id>]` shapes still land beads.
func (c convCommit) eligibleForShipping(allowed map[string]bool) bool {
	if nonShippingScopes[c.scope] {
		return false
	}
	if nonShippingTypes[c.ctype] {
		return false
	}
	if c.ctype == "" {
		return true
	}
	return allowed[c.ctype]
}

// allowedTypeSet builds the allowlist map for DiagnoseWithOpts. nil /
// empty -> DefaultShipTypes. Comparison is case-insensitive on the
// caller-supplied list (we lowercase both sides).
func allowedTypeSet(types []string) map[string]bool {
	if len(types) == 0 {
		types = DefaultShipTypes
	}
	out := make(map[string]bool, len(types))
	for _, t := range types {
		out[strings.ToLower(t)] = true
	}
	return out
}

// DerivePrefix infers the bead-id prefix from the JSONL: takes the
// first record's id, splits at the rightmost `-`, returns the portion
// before. e.g. `myapp-xymh.1` -> `myapp`. Returns ("", false)
// when the JSONL is missing / empty / has no parseable id.
func DerivePrefix(repo string) (string, bool) {
	f, err := os.Open(filepath.Join(repo, JSONLRelPath))
	if err != nil {
		return "", false
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
		id, _ := rec["id"].(string)
		if id == "" {
			continue
		}
		// Bead ids look like `<prefix>-<rest>` where prefix can also
		// contain `-`. We split at the FIRST `-` (matches the bd
		// convention used in beads-go and the Python tool).
		i := strings.IndexByte(id, '-')
		if i <= 0 {
			continue
		}
		return id[:i], true
	}
	return "", false
}

// readOpenIssueIDsFrom returns id -> status keyed on the selected
// source. The bkg-td0.2 default (StatusSourceAuto) MERGES `bd list
// --json` output with `.beads/issues.jsonl`: bd's status is
// authoritative for ids it knows about (this is the fix — `bd close
// <id>` flushes the bd DB before the JSONL gets re-written), while
// the JSONL covers ids that bd hasn't ingested yet (uninitialized
// SQLite DB on a fresh clone, etc.).
//
// Read paths:
//
//	StatusSourceAuto  - merge: bd authoritative for ids it has;
//	                    JSONL covers the rest.
//	StatusSourceBd    - bd only; error when bd is unavailable.
//	StatusSourceJSONL - legacy bkg-bqa.3 behavior (JSONL only).
func readOpenIssueIDsFrom(repo string, src StatusSource) (map[string]string, error) {
	switch src {
	case StatusSourceJSONL:
		return readOpenIssueIDs(repo)
	case StatusSourceBd:
		all, err := readAllIssueIDsViaBd(repo)
		if err != nil {
			return nil, err
		}
		return filterOpen(all), nil
	default: // StatusSourceAuto
		bdAll, bdErr := readAllIssueIDsViaBd(repo)
		jsonlAll, jsonlErr := readAllIssueIDs(repo)
		if bdErr != nil && jsonlErr != nil {
			return nil, fmt.Errorf("both bd and jsonl reads failed (bd=%v, jsonl=%v)", bdErr, jsonlErr)
		}
		// Merge: start from JSONL (covers ids bd hasn't ingested),
		// then OVERLAY bd (authoritative for ids it knows about,
		// including post-close transitions).
		merged := map[string]string{}
		if jsonlErr == nil {
			for id, st := range jsonlAll {
				merged[id] = st
			}
		}
		if bdErr == nil {
			for id, st := range bdAll {
				merged[id] = st
			}
		}
		return filterOpen(merged), nil
	}
}

// filterOpen keeps only open/in_progress entries.
func filterOpen(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for id, st := range in {
		if st == "open" || st == "in_progress" {
			out[id] = st
		}
	}
	return out
}

// PlanCloses builds the apply plan for `findings`: which beads to
// close, which to skip (excluded), which to skip (blocked by open
// issues), and which to force-close. Pure read; never mutates state.
//
// The plan inspects bd's blocker view via `bd list --json` for each
// finding so a dry-run accurately reflects what `--apply` would do.
// `excluded` is checked first (excluded beads never reach the bd
// query). `force` switches blocked beads from skip to force-close.
//
// Reason rendering: "shipped in #<PR>" when LandingPR > 0, else
// "shipped in <short-sha>". Same shape as the bd_close_command
// JSON field shipped in bkg-bqa.3.
func PlanCloses(repo string, findings []Finding, excluded []string, force bool) ([]CloseAction, error) {
	excludeSet := map[string]bool{}
	for _, id := range excluded {
		excludeSet[strings.TrimSpace(id)] = true
	}
	plan := make([]CloseAction, 0, len(findings))
	// Cache bd's record set so we ask once per repo.
	bdRecs, _ := readAllIssueIDsViaBd(repo)
	blockers, _ := readOpenBlockersViaBd(repo)
	for _, f := range findings {
		reason := closeReason(f)
		act := CloseAction{BeadID: f.BeadID, Reason: reason}
		if excludeSet[f.BeadID] {
			act.Decision = DecisionSkipExcluded
			plan = append(plan, act)
			continue
		}
		// If bd already says closed, this would have been filtered out
		// by the caller's auto-source merge. Defense-in-depth: skip.
		if st, ok := bdRecs[f.BeadID]; ok && st == "closed" {
			continue
		}
		if blk, blocked := blockers[f.BeadID]; blocked {
			if force {
				act.Decision = DecisionForceCloseBlocked
				act.Blocker = blk
			} else {
				act.Decision = DecisionSkipBlocked
				act.Blocker = blk
			}
			plan = append(plan, act)
			continue
		}
		act.Decision = DecisionClose
		plan = append(plan, act)
	}
	return plan, nil
}

// ApplyCloses executes a plan against bd. dryRun=true returns the
// plan unchanged (Applied=false). dryRun=false invokes CloseBead per
// action; results land in CloseSummary.
//
// Idempotent: if a re-run plans to close a bead bd has already closed,
// CloseBead reports rc 0 and we record success. The bkg-bqa.3 auto
// merge means a re-run usually has zero plan rows anyway because bd
// already says closed and the bead never enters openIDs.
func ApplyCloses(repo string, plan []CloseAction, dryRun bool) CloseSummary {
	s := CloseSummary{
		DryRun: dryRun,
		Apply:  !dryRun,
	}
	for _, act := range plan {
		switch act.Decision {
		case DecisionSkipExcluded:
			s.SkippedExcluded = append(s.SkippedExcluded, act.BeadID)
			s.Actions = append(s.Actions, act)
			continue
		case DecisionSkipBlocked:
			s.SkippedBlocked = append(s.SkippedBlocked, act)
			s.Actions = append(s.Actions, act)
			continue
		}
		if dryRun {
			s.Actions = append(s.Actions, act)
			continue
		}
		force := act.Decision == DecisionForceCloseBlocked
		ok, blocker, _, err := CloseBead(repo, act.BeadID, act.Reason, force)
		switch {
		case ok:
			act.Applied = true
			s.Closed = append(s.Closed, act.BeadID)
		case blocker != "" && !force:
			act.Decision = DecisionSkipBlocked
			act.Blocker = blocker
			s.SkippedBlocked = append(s.SkippedBlocked, act)
		default:
			if err != nil {
				act.Error = err.Error()
			} else {
				act.Error = "bd close failed"
			}
			s.Failed = append(s.Failed, act)
		}
		s.Actions = append(s.Actions, act)
	}
	return s
}

// closeReason renders the canonical close-incantation reason.
func closeReason(f Finding) string {
	if f.LandingPR > 0 {
		return fmt.Sprintf("shipped in #%d", f.LandingPR)
	}
	if len(f.LandingSHA) >= 7 {
		return fmt.Sprintf("shipped in %s", f.LandingSHA[:7])
	}
	return fmt.Sprintf("shipped in %s", f.LandingSHA)
}

// readOpenBlockersViaBd returns id -> first-blocker-id for any open
// bead with at least one OPEN `blocks` dependency. Used by PlanCloses
// to predict which beads bd will refuse to close. Pure best-effort:
// when bd is unavailable we return ({}, err) and the planner falls
// back to discovering blockers at apply time via CloseBead's stderr
// parsing.
func readOpenBlockersViaBd(repo string) (map[string]string, error) {
	rc, stdout, _, err := bkproject.BdRun([]string{"bd", "list", "--json"}, repo, 30*time.Second)
	if err != nil || rc != 0 {
		return map[string]string{}, err
	}
	var recs []map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &recs); err != nil {
		return map[string]string{}, err
	}
	statusByID := map[string]string{}
	for _, rec := range recs {
		id, _ := rec["id"].(string)
		st, _ := rec["status"].(string)
		statusByID[id] = st
	}
	blockers := map[string]string{}
	for _, rec := range recs {
		id, _ := rec["id"].(string)
		deps, ok := rec["dependencies"].([]any)
		if !ok {
			continue
		}
		for _, raw := range deps {
			dep, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if dep["type"] != "blocks" {
				continue
			}
			tgt, _ := dep["depends_on_id"].(string)
			if tgt == "" {
				continue
			}
			if st := statusByID[tgt]; st != "" && st != "closed" {
				blockers[id] = tgt
				break
			}
		}
	}
	return blockers, nil
}

// CloseDecision is the planned action for one bead in apply mode.
type CloseDecision string

const (
	DecisionClose             CloseDecision = "close"
	DecisionSkipBlocked       CloseDecision = "skip-blocked"
	DecisionSkipExcluded      CloseDecision = "skip-excluded"
	DecisionForceCloseBlocked CloseDecision = "force-close-blocked"
)

// CloseAction is one row in the apply plan / result.
type CloseAction struct {
	BeadID   string
	Reason   string
	Decision CloseDecision
	Blocker  string // set when Decision is skip-blocked / force-close-blocked
	Applied  bool   // true if --apply ran AND bd close succeeded
	Error    string // populated when Applied=false and an attempt was made
}

// CloseSummary aggregates apply results for the JSON / text output.
type CloseSummary struct {
	DryRun          bool
	Apply           bool
	Closed          []string
	SkippedBlocked  []CloseAction
	SkippedExcluded []string
	Failed          []CloseAction
	Actions         []CloseAction
}

// blockedRe matches bd's refusal message:
//
//	"blocked by open issues [<id1>, <id2>] (use --force)"
//
// We capture the bracketed list and use the FIRST id as the canonical
// blocker for human/agent display. The full list is available in the
// raw stderr if a caller wants more detail.
var blockedRe = regexp.MustCompile(`blocked by open issues \[([^\]]+)\]`)

// CloseBead invokes `bd close <id> --reason <reason>` (with --force
// when force=true). Returns:
//
//	ok=true                         — bd reported close (rc 0)
//	ok=false, blocker non-empty     — bd refused: bead has open blockers
//	ok=false, err non-nil           — any other failure
//
// race-safe: if the bead is already closed (post bd-close from another
// agent), bd reports rc 0 with a "(already closed)"-ish message; we
// treat that as success too.
func CloseBead(repo, id, reason string, force bool) (ok bool, blocker string, stderr string, err error) {
	args := []string{"bd", "close", id, "--reason", reason}
	if force {
		args = append(args, "--force")
	}
	rc, _, errOut, runErr := bkproject.BdRun(args, repo, 30*time.Second)
	stderr = errOut
	if runErr != nil {
		return false, "", stderr, fmt.Errorf("bd close %s: %w", id, runErr)
	}
	if rc == 0 {
		return true, "", stderr, nil
	}
	if m := blockedRe.FindStringSubmatch(errOut); m != nil {
		first := strings.TrimSpace(strings.SplitN(m[1], ",", 2)[0])
		return false, first, stderr, nil
	}
	return false, "", stderr, fmt.Errorf("bd close %s (rc=%d): %s", id, rc, strings.TrimSpace(errOut))
}

// readAllIssueIDsViaBd returns id -> status for EVERY bead bd knows
// about (open / in_progress / blocked / closed). Closed entries are
// returned so the merge in readOpenIssueIDsFrom can use them to
// override stale JSONL "open" rows. Returns (nil, err) when bd is
// unavailable or its output can't be parsed.
func readAllIssueIDsViaBd(repo string) (map[string]string, error) {
	rc, stdout, stderr, err := bkproject.BdRun([]string{"bd", "list", "--json"}, repo, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("bd list --json: %w", err)
	}
	if rc != 0 {
		if rc == 127 {
			return nil, fmt.Errorf("bd not on PATH")
		}
		return nil, fmt.Errorf("bd list --json (rc=%d): %s", rc, strings.TrimSpace(stderr))
	}
	body := strings.TrimSpace(stdout)
	if body == "" {
		return map[string]string{}, nil
	}
	var recs []map[string]any
	if err := json.Unmarshal([]byte(body), &recs); err != nil {
		return nil, fmt.Errorf("bd list --json output not parseable: %w", err)
	}
	out := make(map[string]string, len(recs))
	for _, rec := range recs {
		id, _ := rec["id"].(string)
		st, _ := rec["status"].(string)
		if id == "" {
			continue
		}
		out[id] = st
	}
	return out, nil
}

// readAllIssueIDs returns id -> status for EVERY record in the JSONL
// (including closed). Used by the auto-merge path so callers can
// reason about "what does the JSONL claim about this id?" before
// overlaying bd's authoritative view.
func readAllIssueIDs(repo string) (map[string]string, error) {
	f, err := os.Open(filepath.Join(repo, JSONLRelPath))
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", JSONLRelPath, err)
	}
	defer f.Close()
	out := map[string]string{}
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
		id, _ := rec["id"].(string)
		st, _ := rec["status"].(string)
		if id == "" {
			continue
		}
		out[id] = st
	}
	return out, nil
}

// readOpenIssueIDs returns id -> status for every record whose status
// is "open" or "in_progress". Closed beads are intentionally absent —
// the closed-no-landing variant is out of scope for v1.
func readOpenIssueIDs(repo string) (map[string]string, error) {
	f, err := os.Open(filepath.Join(repo, JSONLRelPath))
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", JSONLRelPath, err)
	}
	defer f.Close()
	out := map[string]string{}
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
		id, _ := rec["id"].(string)
		st, _ := rec["status"].(string)
		if id == "" {
			continue
		}
		if st == "open" || st == "in_progress" {
			out[id] = st
		}
	}
	return out, nil
}

func parseIntStrict(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("non-digit")
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}
