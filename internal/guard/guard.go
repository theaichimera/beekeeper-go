// Package guard is the DB-in-file-sync guardrail. Detection +
// remediation planning. NEVER moves files while the bd daemon is alive.
//
// Port of Python beadkeeper.guard. Mirrors:
//
//   - GuardSeverity, DBFinding, GuardReport, FixAction, FixPlan
//   - ScanDBs / ScanPaths (detect)
//   - PlanFix (plan, no IO)
//   - ApplyFix (DryRun by default; refuses on live daemon)
package guard

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/theaichimera/beekeeper-go/internal/filesync"
	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
)

// Severity mirrors Python's GuardSeverity.
type Severity string

const (
	GREEN  Severity = "green"
	YELLOW Severity = "yellow"
	RED    Severity = "red"
)

var sevOrder = map[Severity]int{GREEN: 0, YELLOW: 1, RED: 2}

// DBFinding is one DB-in-filesync hit.
type DBFinding struct {
	Project     bkproject.Project
	DBPath      string
	Match       *filesync.Match
	Severity    Severity
	Message     string
	Remediation string
}

// Report aggregates findings.
type Report struct {
	Findings []DBFinding
}

// IsClean reports whether there are zero findings.
func (r Report) IsClean() bool { return len(r.Findings) == 0 }

// Worst returns the worst severity (GREEN when empty).
func (r Report) Worst() Severity {
	w := GREEN
	for _, f := range r.Findings {
		if sevOrder[f.Severity] > sevOrder[w] {
			w = f.Severity
		}
	}
	return w
}

// FixAction is one planned move.
type FixAction struct {
	Src    string
	Dst    string
	Reason string
}

// FixPlan is the per-project plan.
type FixPlan struct {
	Project        bkproject.Project
	Actions        []FixAction
	DaemonBlocking bool
	Daemon         bkproject.DaemonState
	Notes          []string
}

// FixError signals an apply-time refusal.
type FixError struct{ Msg string }

func (e *FixError) Error() string { return e.Msg }

// ScanDBs walks each project's `.beads/` and flags live DBs inside a
// known file-sync root. Mirrors `scan_dbs` in Python verbatim.
func ScanDBs(projects []bkproject.Project, roots []string) Report {
	r := Report{}
	for _, p := range projects {
		for _, db := range bkproject.DiscoverDBPaths(p) {
			var m *filesync.Match
			if roots == nil {
				m = filesync.MatchPath(db, nil, filesync.Options{})
			} else {
				m = filesync.MatchPath(db, roots, filesync.Options{})
			}
			if m == nil {
				continue
			}
			sev := RED
			// WAL/SHM sidecars are YELLOW (Python: severity = RED if .db, else YELLOW).
			if !strings.HasSuffix(db, ".db") {
				sev = YELLOW
			}
			r.Findings = append(r.Findings, DBFinding{
				Project:  p,
				DBPath:   db,
				Match:    m,
				Severity: sev,
				Message: fmt.Sprintf(
					"Live bead DB inside %s: %s. "+
						"The SQLite DB and its WAL/SHM sidecars must not be in a file-sync folder "+
						"— concurrent writers between bd and the sync agent can corrupt it.",
					m.Label, db,
				),
				Remediation: "Move the .beads/ database to a non-synced location:\n" +
					"  1. Stop the bd daemon for this project (e.g. `bd daemon stop`).\n" +
					"  2. Verify no `bd` process holds the WAL: `lsof` / `fuser` the .db file.\n" +
					fmt.Sprintf("  3. Move the .db / .db-wal / .db-shm files OUT of %s.\n", m.Label) +
					"  4. Point bd at the new path (e.g. via `BEADS_DB` env or bd config),\n" +
					"     OR clone the project itself outside the file-sync folder.\n" +
					"  5. The DB is a rebuildable cache of issues.jsonl — if anything goes\n" +
					"     wrong, you can re-import from JSONL.",
			})
		}
	}
	return r
}

// ScanPaths discovers projects + scans them. Returns (projects,
// report) so callers can show counts.
func ScanPaths(paths []string, maxDepth int, roots []string) ([]bkproject.Project, Report) {
	if maxDepth <= 0 {
		maxDepth = bkproject.DefaultMaxDepth
	}
	projects := bkproject.FindProjects(paths, maxDepth)
	return projects, ScanDBs(projects, roots)
}

// PlanFix builds a relocation plan for `destination`. Does NOT touch
// the filesystem. Refuses to plan over a hot daemon (records in
// FixPlan.DaemonBlocking; ApplyFix is the one that errors out).
func PlanFix(p bkproject.Project, destination string) FixPlan {
	daemon := bkproject.ReadDaemonState(p)
	plan := FixPlan{
		Project:        p,
		DaemonBlocking: daemon.PIDAlive,
		Daemon:         daemon,
	}
	dst, _ := filepath.Abs(destination)
	if resolved, err := filepath.EvalSymlinks(dst); err == nil {
		dst = resolved
	}
	if info, err := os.Stat(dst); err == nil && !info.IsDir() {
		plan.Notes = append(plan.Notes, "destination is not a directory: "+dst)
		return plan
	}
	for _, db := range bkproject.DiscoverDBPaths(p) {
		plan.Actions = append(plan.Actions, FixAction{
			Src:    db,
			Dst:    filepath.Join(dst, filepath.Base(db)),
			Reason: fmt.Sprintf("Relocate bead DB out of file-sync folder (%s).", filepath.Base(db)),
		})
	}
	if plan.DaemonBlocking {
		plan.Notes = append(plan.Notes, fmt.Sprintf(
			"bd daemon (pid %d) is alive — refusing to move DB files. Stop the daemon first.",
			daemon.PID,
		))
	}
	return plan
}

// ApplyFix executes (or dry-runs) a plan. Returns one line per action.
// Refuses with *FixError when the daemon is alive.
func ApplyFix(plan FixPlan, dryRun bool) ([]string, error) {
	if plan.DaemonBlocking {
		return nil, &FixError{Msg: fmt.Sprintf(
			"refusing to apply: bd daemon (pid %d) is alive for %s; stop the daemon and re-run",
			plan.Daemon.PID, plan.Project.Root,
		)}
	}
	var lines []string
	for _, a := range plan.Actions {
		if dryRun {
			lines = append(lines, fmt.Sprintf("DRY-RUN move: %s -> %s", a.Src, a.Dst))
			continue
		}
		if err := os.MkdirAll(filepath.Dir(a.Dst), 0o755); err != nil {
			return nil, err
		}
		if _, err := os.Stat(a.Dst); err == nil {
			return nil, &FixError{Msg: "destination already exists: " + a.Dst}
		}
		if err := os.Rename(a.Src, a.Dst); err != nil {
			// Cross-device rename falls back to copy+remove.
			if err := copyAndRemove(a.Src, a.Dst); err != nil {
				return nil, err
			}
		}
		lines = append(lines, fmt.Sprintf("moved: %s -> %s", a.Src, a.Dst))
	}
	return lines, nil
}

func copyAndRemove(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Remove(src)
}
