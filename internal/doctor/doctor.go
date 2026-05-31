// Package doctor composes per-project health checks and rolls them
// up to a single severity per project + overall.
//
// Port of Python beadkeeper.doctor (M2 subset: jsonl, db-in-filesync,
// git, sync-branch, daemon, sync-state, daemon-hygiene, identity).
// The trunk-sync and lease checks land in M3 alongside their
// underlying modules.
//
// Read-only. Never starts/stops a daemon, never writes JSONL, never
// shells out beyond what internal/git already does.
package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/theaichimera/beekeeper-go/internal/daemons"
	"github.com/theaichimera/beekeeper-go/internal/filesync"
	"github.com/theaichimera/beekeeper-go/internal/identity"
	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
	"github.com/theaichimera/beekeeper-go/internal/syncbranch"
)

// Severity is GREEN/YELLOW/RED. Used in both per-check and
// per-project / overall rollups.
type Severity string

const (
	GREEN  Severity = "green"
	YELLOW Severity = "yellow"
	RED    Severity = "red"
)

var sevOrder = map[Severity]int{GREEN: 0, YELLOW: 1, RED: 2}

// Check is one row in a ProjectHealth.
type Check struct {
	Name        string   `json:"name"`
	Severity    Severity `json:"severity"`
	Message     string   `json:"message"`
	Remediation string   `json:"remediation,omitempty"`
}

// ProjectHealth is the per-project rollup.
type ProjectHealth struct {
	ProjectRoot string   `json:"project_root"`
	Severity    Severity `json:"severity"`
	Checks      []Check  `json:"checks"`
}

// Report is the cross-project rollup.
type Report struct {
	GeneratedAt  float64         `json:"generated_at"`
	ScannedPaths []string        `json:"scanned_paths"`
	Worst        Severity        `json:"worst"`
	Projects     []ProjectHealth `json:"projects"`
}

// --- public entrypoint --------------------------------------------------

// Run scans `paths` and produces a Report. Mirrors Python doctor.run.
func Run(paths []string, maxDepth int) Report {
	if maxDepth <= 0 {
		maxDepth = bkproject.DefaultMaxDepth
	}
	projects := bkproject.FindProjects(paths, maxDepth)
	scanned := make([]string, 0, len(paths))
	for _, p := range paths {
		if abs, err := filepath.Abs(p); err == nil {
			scanned = append(scanned, abs)
		} else {
			scanned = append(scanned, p)
		}
	}

	r := Report{
		GeneratedAt:  float64(time.Now().Unix()),
		ScannedPaths: scanned,
		Worst:        GREEN,
	}
	for _, p := range projects {
		ph := diagnose(p)
		r.Projects = append(r.Projects, ph)
		if sevOrder[ph.Severity] > sevOrder[r.Worst] {
			r.Worst = ph.Severity
		}
	}
	return r
}

// --- diagnose -----------------------------------------------------------

func diagnose(p bkproject.Project) ProjectHealth {
	// IMPORTANT: read daemon state BEFORE any bd shell-out. Python's
	// `bd config get` self-heals a stale daemon.pid otherwise.
	daemon := bkproject.ReadDaemonState(p)
	gitState := bkproject.ReadGitState(p)

	checks := []Check{}
	checks = append(checks, checkJSONL(p)...)
	checks = append(checks, checkDBInFilesync(p)...)
	checks = append(checks, checkGit(gitState)...)
	checks = append(checks, checkSyncBranch(p, gitState)...)
	checks = append(checks, checkDaemonPID(daemon)...)
	checks = append(checks, checkSyncState(daemon)...)
	checks = append(checks, checkDaemonHygiene(p)...)
	checks = append(checks, checkIdentity(p)...)

	ph := ProjectHealth{
		ProjectRoot: p.Root,
		Checks:      checks,
		Severity:    GREEN,
	}
	for _, c := range checks {
		if sevOrder[c.Severity] > sevOrder[ph.Severity] {
			ph.Severity = c.Severity
		}
	}
	return ph
}

// --- check_jsonl --------------------------------------------------------

func checkJSONL(p bkproject.Project) []Check {
	if !fileExists(p.IssuesJSONL()) {
		return []Check{{
			Name:        "issues-jsonl",
			Severity:    YELLOW,
			Message:     ".beads/issues.jsonl missing.",
			Remediation: "This may be a freshly-initialized project. Otherwise verify that the project was created with `bd init`.",
		}}
	}
	return nil
}

// --- check_db_in_filesync -----------------------------------------------

func checkDBInFilesync(p bkproject.Project) []Check {
	dbs := bkproject.DiscoverDBPaths(p)
	if len(dbs) == 0 {
		return nil
	}
	var out []Check
	for _, db := range dbs {
		m := filesync.MatchPath(db, nil, filesync.Options{})
		if m == nil {
			continue
		}
		sev := RED
		if !strings.HasSuffix(db, ".db") {
			// WAL/SHM sidecars are YELLOW; the live .db is RED.
			sev = YELLOW
		}
		name := filepath.Base(db)
		out = append(out, Check{
			Name:        "db-in-filesync",
			Severity:    sev,
			Message:     fmt.Sprintf("%s is inside %s.", name, m.Label),
			Remediation: "Move the bead DB out of the file-sync folder; the DB is a rebuildable cache. See `bk guard db --help`.",
		})
	}
	return out
}

// --- check_git ----------------------------------------------------------

func checkGit(g bkproject.GitState) []Check {
	var out []Check
	if g.Branch == "" {
		out = append(out, Check{
			Name:        "git",
			Severity:    YELLOW,
			Message:     "Not in a git working tree (or git not available).",
			Remediation: "beadkeeper expects beads projects to live in git repos.",
		})
		return out
	}
	if g.Upstream == "" {
		out = append(out, Check{
			Name:     "git-upstream",
			Severity: YELLOW,
			Message:  fmt.Sprintf("Branch %s has no upstream.", g.Branch),
			Remediation: "Bead sync cannot push without an upstream. " +
				"`git push -u origin <branch>` once, or set the sync branch's upstream.",
		})
	}
	return out
}

// --- check_sync_branch --------------------------------------------------

func checkSyncBranch(p bkproject.Project, g bkproject.GitState) []Check {
	sb := strings.TrimSpace(g.SyncBranch)
	if sb == "" {
		return []Check{{
			Name:     "sync-branch",
			Severity: YELLOW,
			Message:  "bd config `sync.branch` is empty.",
			Remediation: "Bead writes will ride whatever branch you're on. " +
				"Configure a dedicated sync branch: " +
				"`bk guard sync-branch --set beads-sync`.",
		}}
	}
	// Escalate to RED when syncbranch.DiagnoseProject finds stranded
	// JSONL commits on non-sync branches.
	findings := syncbranch.DiagnoseProject(p)
	var stranded []syncbranch.Finding
	for _, f := range findings {
		if f.Kind == "stranded-bead-commit" {
			stranded = append(stranded, f)
		}
	}
	if len(stranded) == 0 {
		return []Check{{
			Name:     "sync-branch",
			Severity: GREEN,
			Message:  fmt.Sprintf("sync.branch = %s.", sb),
		}}
	}
	branches := map[string]struct{}{}
	total := 0
	for _, f := range stranded {
		if f.Branch != "" {
			branches[f.Branch] = struct{}{}
		}
		total += f.CommitCount
	}
	sortedBranches := make([]string, 0, len(branches))
	for b := range branches {
		sortedBranches = append(sortedBranches, b)
	}
	sort.Strings(sortedBranches)
	return []Check{{
		Name:     "sync-branch",
		Severity: RED,
		Message: fmt.Sprintf(
			"sync.branch = %s, but %d bead-data commit(s) are stranded on non-sync branch(es): %s.",
			sb, total, strings.Join(sortedBranches, ", "),
		),
		Remediation: fmt.Sprintf(
			"Replay / cherry-pick the stranded JSONL commits onto `%s`. "+
				"Run `bd sync` after bead mutations so the daemon commits on the sync branch, not the feature branch. "+
				"Detail: `bk guard sync-branch`.",
			sb,
		),
	}}
}

// --- check_daemon_pid + check_sync_state --------------------------------

func checkDaemonPID(d bkproject.DaemonState) []Check {
	if !d.PIDPresent {
		return []Check{{
			Name:     "daemon",
			Severity: YELLOW,
			Message:  "No bd daemon PID file found.",
			Remediation: "If this workspace is meant to auto-sync, start the daemon " +
				"(`bd daemon start`). Otherwise this is informational.",
		}}
	}
	if !d.PIDAlive {
		return []Check{{
			Name:     "daemon",
			Severity: RED,
			Message:  fmt.Sprintf("daemon.pid = %d but no such process is alive.", d.PID),
			Remediation: "Stale PID file. Remove `.beads/daemon.pid` and restart the daemon. " +
				"Investigate `.beads/daemon.log` for the crash reason.",
		}}
	}
	return []Check{{
		Name:     "daemon",
		Severity: GREEN,
		Message:  fmt.Sprintf("daemon pid %d is alive.", d.PID),
	}}
}

func checkSyncState(d bkproject.DaemonState) []Check {
	state := d.LastSyncState
	if state == nil {
		return nil
	}
	if needsManual, _ := state["needs_manual_sync"].(bool); needsManual {
		return []Check{{
			Name:     "sync-state",
			Severity: RED,
			Message:  "bd sync state reports `needs_manual_sync`.",
			Remediation: "Run `bd sync` by hand; investigate the error in the sync state " +
				"and `.beads/daemon.log` before relying on auto-sync again.",
		}}
	}
	failureCount := readFailureCount(state)
	if failureCount > 0 {
		sev := YELLOW
		if failureCount >= 3 {
			sev = RED
		}
		return []Check{{
			Name:     "sync-state",
			Severity: sev,
			Message: fmt.Sprintf(
				"bd sync state reports %d consecutive sync failures.", failureCount,
			),
			Remediation: "Check `.beads/daemon.log` for the underlying git error " +
				"(remote helper missing, auth, push rejected, etc.).",
		}}
	}
	if age := lastSyncAgeSeconds(state); age != nil && *age > 24*3600 {
		return []Check{{
			Name:        "sync-state",
			Severity:    YELLOW,
			Message:     fmt.Sprintf("Last successful sync was %ds ago (>24h).", *age),
			Remediation: "Old but not failing. If this workspace is active, expect more recent syncs.",
		}}
	}
	return []Check{{
		Name:     "sync-state",
		Severity: GREEN,
		Message:  "sync state nominal.",
	}}
}

func readFailureCount(state map[string]any) int {
	for _, key := range []string{"consecutive_failures", "failure_count"} {
		switch v := state[key].(type) {
		case float64:
			if v > 0 {
				return int(v)
			}
		case int:
			if v > 0 {
				return v
			}
		}
	}
	return 0
}

func lastSyncAgeSeconds(state map[string]any) *int {
	for _, key := range []string{"last_sync_at", "last_sync", "last_push_at", "last_success_at"} {
		switch v := state[key].(type) {
		case float64:
			age := int(time.Now().Unix() - int64(v))
			if age < 0 {
				age = 0
			}
			return &age
		case int:
			age := int(time.Now().Unix() - int64(v))
			if age < 0 {
				age = 0
			}
			return &age
		case string:
			if t, err := time.Parse(time.RFC3339, strings.Replace(v, "Z", "+00:00", 1)); err == nil {
				age := int(time.Since(t).Seconds())
				if age < 0 {
					age = 0
				}
				return &age
			}
		}
	}
	return nil
}

// --- check_daemon_hygiene ----------------------------------------------

func checkDaemonHygiene(p bkproject.Project) []Check {
	var out []Check
	for _, f := range daemons.DiagnoseProject(p) {
		out = append(out, Check{
			Name:        "daemon-hygiene",
			Severity:    severityFromDaemons(f.Severity),
			Message:     f.Message,
			Remediation: f.Remediation,
		})
	}
	return out
}

func severityFromDaemons(s daemons.Severity) Severity {
	switch s {
	case daemons.RED:
		return RED
	case daemons.YELLOW:
		return YELLOW
	}
	return GREEN
}

// --- check_identity ----------------------------------------------------

func checkIdentity(p bkproject.Project) []Check {
	cfg, _ := identity.LoadConfig(p.Root)
	if cfg == nil {
		return nil
	}
	report := identity.ScanProject(p.Root)
	if !report.HasDrift() {
		return []Check{{
			Name:     "identity",
			Severity: GREEN,
			Message:  "All actor handles in bead JSONL are canonical.",
		}}
	}
	var parts []string
	if len(report.AliasedHandles) > 0 {
		parts = append(parts, fmt.Sprintf(
			"%d aliased handle(s) in use (%d occurrence(s))",
			len(report.AliasedHandles), report.AliasedOccurrences,
		))
	}
	if len(report.UnmappedHandles) > 0 {
		parts = append(parts, fmt.Sprintf(
			"%d unmapped handle(s) (%d occurrence(s))",
			len(report.UnmappedHandles), report.UnmappedOccurrences,
		))
	}
	return []Check{{
		Name:     "identity",
		Severity: YELLOW,
		Message:  "Identity drift: " + strings.Join(parts, "; ") + ".",
		Remediation: "Run `bk identity check` for the full list. " +
			"`bk identity normalize` rewrites aliases to canonical form (dry-run by default). " +
			"Add unmapped handles to `.beadkeeper/identity.toml`'s `[identity.aliases]` " +
			"or `[identity].canonical` list.",
	}}
}

// --- rendering ----------------------------------------------------------

const (
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiRed    = "\033[31m"
	ansiReset  = "\033[0m"
)

func ansi(s Severity, txt string) string {
	switch s {
	case GREEN:
		return ansiGreen + txt + ansiReset
	case YELLOW:
		return ansiYellow + txt + ansiReset
	case RED:
		return ansiRed + txt + ansiReset
	}
	return txt
}

// RenderText mirrors Python's render_text output.
func RenderText(r Report, useColor bool) string {
	var b strings.Builder
	if len(r.Projects) == 0 {
		b.WriteString("No projects with a .beads/ directory found.\n")
		b.WriteString("Scanned: " + strings.Join(r.ScannedPaths, ", "))
		return b.String()
	}
	for _, ph := range r.Projects {
		tag := strings.ToUpper(string(ph.Severity))
		if useColor {
			tag = ansi(ph.Severity, tag)
		}
		fmt.Fprintf(&b, "[%s] %s\n", tag, ph.ProjectRoot)
		for _, c := range ph.Checks {
			csev := strings.ToUpper(string(c.Severity))
			if useColor {
				csev = ansi(c.Severity, csev)
			}
			fmt.Fprintf(&b, "  - %6s  %s: %s\n", csev, c.Name, c.Message)
			if c.Remediation != "" {
				for _, line := range strings.Split(c.Remediation, "\n") {
					fmt.Fprintf(&b, "           %s\n", line)
				}
			}
		}
		b.WriteString("\n")
	}
	worst := strings.ToUpper(string(r.Worst))
	if useColor {
		worst = ansi(r.Worst, worst)
	}
	fmt.Fprintf(&b, "Overall: %s", worst)
	return b.String()
}

// RenderJSON mirrors render_json. Stable key order via sort_keys.
func RenderJSON(r Report) string {
	b, _ := json.MarshalIndent(r, "", "  ")
	return string(b)
}

// --- exit-code contract -------------------------------------------------

// ExitCode mirrors Python's CLI contract:
//
//	RED                -> 2
//	YELLOW + --strict  -> 1
//	otherwise          -> 0
func ExitCode(worst Severity, strict bool) int {
	if worst == RED {
		return 2
	}
	if strict && worst == YELLOW {
		return 1
	}
	return 0
}

// --- tiny helpers -------------------------------------------------------

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
