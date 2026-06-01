// Package prbeads is a content-aware backlog-regression guard for
// pull requests. It diffs `.beads/issues.jsonl` between a base ref
// and a head ref and reports any change that would REWIND backlog
// state when the head is merged into the base ("stale-snapshot stomp").
//
// The motivating incident: a feature branch was cut from `main` while
// a bead was `open`. The daemon advanced the bead to `in_progress`
// (with an assignee) on `main` while the branch lived. The branch
// still carried the old JSONL. Merging would silently revert the
// status and drop the assignee.
//
// `bk guard sync-branch` does NOT catch this — its subset check is
// keyed on bead id, not content, and explicitly assumes "bead edits
// only ever originate on the sync branch." `prbeads` makes no such
// assumption: it diffs the actual JSONL records at two refs.
//
// Detection (per id present in BOTH base and head):
//
//   - Status rewind: rank open=0, in_progress=1, blocked=1, closed=2;
//     head.rank < base.rank -> RED.
//   - Assignee rewind: base assignee set, head clears or changes the
//     assignee -> RED.
//   - Stale timestamp: when both records carry an `updated_at` (or
//     `modified`) parsable as RFC 3339, head < base -> RED.
//   - Dropped record: id in base, absent in head -> RED.
//
// Forward-only (new ids on head, or status advances) is GREEN.
//
// Policies:
//
//   - PolicyRegression (default): only regressions fail. Suits repos
//     that allow bead edits on any branch.
//   - PolicyNoBeads (strict): ANY commit in `base..head` that touches
//     `.beads/issues.jsonl` fails. Suits repos that confine bead
//     writes to a dedicated sync branch.
//
// Severity is binary in v1: GREEN (no regressions) or RED (one or
// more regressions). YELLOW is reserved for future "advisory" cases
// (e.g. priority changes) — currently unused.
package prbeads

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/theaichimera/beekeeper-go/internal/git"
)

// JSONLRelPath is the bead JSONL location inside a project. Same
// constant as syncbranch / trunksync — duplicated here intentionally
// to keep prbeads import-isolated from those packages.
const JSONLRelPath = ".beads/issues.jsonl"

// Severity mirrors the other guard packages.
type Severity string

const (
	GREEN  Severity = "green"
	YELLOW Severity = "yellow"
	RED    Severity = "red"
)

var sevOrder = map[Severity]int{GREEN: 0, YELLOW: 1, RED: 2}

// Policy controls which JSONL deltas constitute a finding.
type Policy string

const (
	// PolicyRegression: only backlog-state rewinds are findings.
	PolicyRegression Policy = "regression"
	// PolicyNoBeads: any modification to .beads/issues.jsonl in
	// base..head is a finding. Strictest mode.
	PolicyNoBeads Policy = "no-beads"
)

// Kind enumerates the finding kinds. Stable strings — the JSON
// schema for `--json` output depends on these.
const (
	KindStatusRewind   = "status-rewind"
	KindAssigneeRewind = "assignee-rewind"
	KindStaleTimestamp = "stale-timestamp"
	KindDroppedRecord  = "dropped-record"
	KindNoBeadsPolicy  = "no-beads-policy"
)

// Finding is one regression hit. The four-tuple (id, field, base, head)
// is what the JSON consumer (e.g. a CI bot) keys on.
type Finding struct {
	ID        string
	Field     string
	Kind      string
	BaseValue string
	HeadValue string
	Severity  Severity
	Message   string
}

// Report is the diagnose result.
type Report struct {
	BaseRef  string
	HeadRef  string
	Policy   Policy
	Findings []Finding
}

// Worst is GREEN when no findings.
func (r Report) Worst() Severity {
	w := GREEN
	for _, f := range r.Findings {
		if sevOrder[f.Severity] > sevOrder[w] {
			w = f.Severity
		}
	}
	return w
}

// Diagnose compares the JSONL at `base` and `head` under `policy`.
// Returns a Report with findings.
//
// Either ref may be a branch name, a remote-tracking ref, a tag, or
// a sha. Caller is responsible for resolution (see ResolveBaseRef /
// ResolveHeadRef in this package's CLI shim).
//
// When PolicyNoBeads is in effect, the regression checks are still
// executed; a no-beads policy hit always fires alongside any specific
// regressions found. CI consumers can filter the findings list by
// kind.
func Diagnose(repo, base, head string, policy Policy) (Report, error) {
	r := Report{BaseRef: base, HeadRef: head, Policy: policy}

	baseRecs, baseOK := readJSONLAt(repo, base)
	headRecs, headOK := readJSONLAt(repo, head)
	if !baseOK {
		return r, fmt.Errorf("cannot read %s at %s (ref missing or not a git repo)", JSONLRelPath, base)
	}
	if !headOK {
		return r, fmt.Errorf("cannot read %s at %s (ref missing or not a git repo)", JSONLRelPath, head)
	}

	if policy == PolicyNoBeads {
		shas := git.JSONLCommitsBetween(repo, base+".."+head, JSONLRelPath)
		if len(shas) > 0 {
			r.Findings = append(r.Findings, Finding{
				Kind:      KindNoBeadsPolicy,
				Field:     JSONLRelPath,
				BaseValue: base,
				HeadValue: head,
				Severity:  RED,
				Message: fmt.Sprintf(
					"--policy no-beads: %d commit(s) in %s..%s modify %s. "+
						"This repo restricts bead writes to the sync branch; "+
						"the merging branch must not carry JSONL changes.",
					len(shas), base, head, JSONLRelPath,
				),
			})
		}
	}

	// Regression checks run under both policies. A no-beads finding
	// often co-occurs with a real regression; surfacing both helps the
	// reviewer understand the actual damage.
	r.Findings = append(r.Findings, regressions(baseRecs, headRecs)...)
	return r, nil
}

// --- core regression detector ------------------------------------------

func regressions(base, head map[string]map[string]any) []Finding {
	var out []Finding
	// Stable iteration order — sort ids for deterministic output.
	ids := sortedKeys(base)
	for _, id := range ids {
		brec := base[id]
		hrec, present := head[id]
		if !present {
			out = append(out, Finding{
				ID:        id,
				Field:     "presence",
				Kind:      KindDroppedRecord,
				BaseValue: "present",
				HeadValue: "absent",
				Severity:  RED,
				Message: fmt.Sprintf(
					"bead `%s` exists at base but is missing at head — merging would drop it.",
					id,
				),
			})
			continue
		}
		if f, ok := checkStatusRewind(id, brec, hrec); ok {
			out = append(out, f)
		}
		if f, ok := checkAssigneeRewind(id, brec, hrec); ok {
			out = append(out, f)
		}
		if f, ok := checkStaleTimestamp(id, brec, hrec); ok {
			out = append(out, f)
		}
	}
	return out
}

// statusRank: open=0, in_progress=1, blocked=1, closed=2. Unknown
// statuses get rank -1 (treated as "no opinion" — never triggers a
// rewind on its own).
func statusRank(s string) int {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "open":
		return 0
	case "in_progress", "blocked":
		return 1
	case "closed":
		return 2
	default:
		return -1
	}
}

func checkStatusRewind(id string, brec, hrec map[string]any) (Finding, bool) {
	bs, _ := brec["status"].(string)
	hs, _ := hrec["status"].(string)
	br, hr := statusRank(bs), statusRank(hs)
	if br < 0 || hr < 0 {
		return Finding{}, false
	}
	if hr >= br {
		return Finding{}, false
	}
	return Finding{
		ID:        id,
		Field:     "status",
		Kind:      KindStatusRewind,
		BaseValue: bs,
		HeadValue: hs,
		Severity:  RED,
		Message: fmt.Sprintf(
			"bead `%s` would regress from `%s` to `%s` — merging head would rewind status.",
			id, bs, hs,
		),
	}, true
}

func checkAssigneeRewind(id string, brec, hrec map[string]any) (Finding, bool) {
	ba, _ := brec["assignee"].(string)
	ha, _ := hrec["assignee"].(string)
	ba, ha = strings.TrimSpace(ba), strings.TrimSpace(ha)
	if ba == "" {
		// Base had no assignee; head adding or removing one is
		// non-regressive.
		return Finding{}, false
	}
	if ha == ba {
		return Finding{}, false
	}
	msg := fmt.Sprintf(
		"bead `%s` assignee would change from `%s` to `%s` — merging head would overwrite the assignment.",
		id, ba, ha,
	)
	if ha == "" {
		msg = fmt.Sprintf(
			"bead `%s` assignee would be cleared (was `%s`) — merging head would drop the assignment.",
			id, ba,
		)
	}
	return Finding{
		ID:        id,
		Field:     "assignee",
		Kind:      KindAssigneeRewind,
		BaseValue: ba,
		HeadValue: ha,
		Severity:  RED,
		Message:   msg,
	}, true
}

// checkStaleTimestamp prefers `updated_at`; falls back to `modified`.
// Either record missing the field, or unparsable timestamps, abstain
// (no finding) — stale-timestamp is an OPINIONATED signal that should
// not fire on partial data.
func checkStaleTimestamp(id string, brec, hrec map[string]any) (Finding, bool) {
	field, bv, hv := pickTimestampField(brec, hrec)
	if field == "" {
		return Finding{}, false
	}
	bt, ok1 := parseTimestamp(bv)
	ht, ok2 := parseTimestamp(hv)
	if !ok1 || !ok2 {
		return Finding{}, false
	}
	if !ht.Before(bt) {
		return Finding{}, false
	}
	return Finding{
		ID:        id,
		Field:     field,
		Kind:      KindStaleTimestamp,
		BaseValue: bv,
		HeadValue: hv,
		Severity:  RED,
		Message: fmt.Sprintf(
			"bead `%s` %s on head (%s) is older than on base (%s) — merging head carries a stale snapshot.",
			id, field, hv, bv,
		),
	}, true
}

func pickTimestampField(brec, hrec map[string]any) (field, bv, hv string) {
	for _, f := range []string{"updated_at", "modified"} {
		bs, _ := brec[f].(string)
		hs, _ := hrec[f].(string)
		if bs != "" && hs != "" {
			return f, bs, hs
		}
	}
	return "", "", ""
}

// parseTimestamp accepts RFC 3339 (with and without nanoseconds, with
// any timezone offset). Returns (zero, false) on parse failure.
func parseTimestamp(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// --- JSONL reading ------------------------------------------------------

// readJSONLAt returns the parsed records (id-keyed) at <ref>:<JSONL>.
// Returns (nil, false) when the ref or file can't be read; returns an
// empty map with ok=true when the file is present but empty.
func readJSONLAt(repo, ref string) (map[string]map[string]any, bool) {
	body, ok := git.Show(repo, ref, JSONLRelPath)
	if !ok {
		return nil, false
	}
	out := map[string]map[string]any{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
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
		out[id] = rec
	}
	return out, true
}

// ResolveBaseRef applies the documented fallback chain:
//
//  1. `explicit` if non-empty.
//  2. `$GITHUB_BASE_REF` (and its `origin/$GITHUB_BASE_REF` variant)
//     when `useEnv` is true.
//  3. `origin/<default>` and `<default>` from the remote's
//     symbolic-ref HEAD.
//  4. `origin/main` then `main`.
//
// Returns ("", false) when nothing in the chain resolves. Set
// `useEnv=true` for CLI calls (where GITHUB_BASE_REF is the natural
// CI override) and `useEnv=false` for callers that should ignore the
// CI environment (e.g. `bk doctor` running locally).
func ResolveBaseRef(repo, explicit string, useEnv bool) (string, bool) {
	candidates := []string{}
	if explicit != "" {
		candidates = append(candidates, explicit)
	}
	if useEnv {
		if env := os.Getenv("GITHUB_BASE_REF"); env != "" {
			candidates = append(candidates, "origin/"+env, env)
		}
	}
	if def, ok := git.DefaultRemoteBranch(repo, "origin"); ok {
		candidates = append(candidates, "origin/"+def, def)
	}
	candidates = append(candidates, "origin/main", "main")
	for _, ref := range candidates {
		if git.RefExists(repo, ref) {
			return ref, true
		}
	}
	return "", false
}

func sortedKeys(m map[string]map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Deterministic order — small N, simple insertion sort suffices.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}
