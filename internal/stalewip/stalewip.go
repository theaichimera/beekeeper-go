// Package stalewip detects in_progress beads parked in WIP for too
// long. The motivating evidence: a real review of a 627-bead backlog
// found 22 of 33 in_progress beads untouched for 7+ days
// (oldest 26 days). `bk` surfaced none of this until now.
//
// The detector is read-only — pure file reads on `.beads/issues.jsonl`,
// no `bd` shell-out, no daemon touch. Timestamp parsing handles real
// bead `updated_at` values: RFC 3339 with timezone offsets AND
// fractional seconds (e.g. `2026-05-06T16:50:40.133129+03:00`),
// which jq's `fromdateiso8601` chokes on. We use `time.RFC3339Nano`
// which accepts both.
//
// Severity is YELLOW (advisory) by design — stale WIP is something a
// human should look at, not something CI should reflexively block on.
// Callers can elevate via --strict at the bk-cmd layer if they want a
// hard gate.
package stalewip

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
)

// JSONLRelPath duplicates board's constant so this package is
// import-isolated from board (avoids cyclic dependency).
const JSONLRelPath = ".beads/issues.jsonl"

// DefaultStaleDays is the threshold used when a caller passes 0.
const DefaultStaleDays = 7

// Stale is one offending bead.
type Stale struct {
	ProjectRoot string
	ID          string
	Status      string
	Assignee    string
	UpdatedAt   string  // raw string from JSONL
	AgeDays     float64 // (now - updated_at) in days, rounded down
}

// Report is the cross-project rollup.
type Report struct {
	StaleDays int     // threshold used for this scan
	Stale     []Stale // sorted oldest-first
}

// Count returns len(Stale).
func (r Report) Count() int { return len(r.Stale) }

// Oldest returns the oldest stale bead or (zero, false).
func (r Report) Oldest() (Stale, bool) {
	if len(r.Stale) == 0 {
		return Stale{}, false
	}
	return r.Stale[0], true
}

// Scan walks `paths`, finds projects, and reports in_progress beads
// whose `updated_at` is older than `staleDays`. `now` is injectable
// for tests. Records without a parseable `updated_at` are silently
// skipped — a missing timestamp is not the same as "stale", and the
// detector should never fire on partial data (matches the
// abstain-on-uncertainty pattern from prbeads).
//
// Returns sorted oldest-first. When `staleDays <= 0`, the check is
// disabled and Report.Stale is nil.
func Scan(paths []string, maxDepth int, staleDays int, now time.Time) Report {
	if staleDays <= 0 {
		return Report{StaleDays: staleDays}
	}
	if maxDepth <= 0 {
		maxDepth = bkproject.DefaultMaxDepth
	}
	r := Report{StaleDays: staleDays}
	threshold := now.AddDate(0, 0, -staleDays)
	for _, p := range bkproject.FindProjects(paths, maxDepth) {
		r.Stale = append(r.Stale, scanProject(p, threshold, now)...)
	}
	sort.Slice(r.Stale, func(i, j int) bool {
		// Older first. Tie-break by id for determinism.
		if r.Stale[i].AgeDays != r.Stale[j].AgeDays {
			return r.Stale[i].AgeDays > r.Stale[j].AgeDays
		}
		return r.Stale[i].ID < r.Stale[j].ID
	})
	return r
}

func scanProject(p bkproject.Project, threshold, now time.Time) []Stale {
	f, err := os.Open(p.IssuesJSONL())
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 1024*1024)
	sc.Buffer(buf, 16*1024*1024)
	var out []Stale
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec["status"] != "in_progress" {
			continue
		}
		updatedRaw, _ := rec["updated_at"].(string)
		t, ok := ParseTimestamp(updatedRaw)
		if !ok {
			continue
		}
		if !t.Before(threshold) {
			continue
		}
		id, _ := rec["id"].(string)
		assignee, _ := rec["assignee"].(string)
		ageHours := now.Sub(t).Hours()
		out = append(out, Stale{
			ProjectRoot: p.Root,
			ID:          id,
			Status:      "in_progress",
			Assignee:    assignee,
			UpdatedAt:   updatedRaw,
			AgeDays:     math.Floor(ageHours/24*10) / 10, // 1-decimal
		})
	}
	return out
}

// ParseTimestamp accepts RFC 3339 timestamps with or without
// fractional seconds, and with any UTC offset (or `Z`). Mirrors the
// parser shape in internal/prbeads but is exposed so callers (doctor
// JSON renderer, board summary) can format ages consistently.
func ParseTimestamp(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
