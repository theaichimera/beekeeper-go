// Package daemons is the read-only daemon-hygiene scanner.
//
// Port of Python beadkeeper.daemons. Detects:
//
//   - duplicate-daemons (RED): more than one bd daemon process for
//     the same workspace.
//   - remote-helper-failure (RED): a live daemon whose
//     `.beads/daemon.log` shows >= 2 remote-helper / auth failures in
//     the tail.
//   - orphan-state (YELLOW): a `sync-state.json` is present but no
//     daemon and no daemon.pid file.
//
// READ-ONLY. NEVER starts or stops a daemon. The injectable
// `Enumerator` keeps `ps` out of synthetic test workspaces.
package daemons

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/theaichimera/beekeeper-go/internal/proc"
	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
)

// Severity mirrors the Python enum.
type Severity string

const (
	GREEN  Severity = "green"
	YELLOW Severity = "yellow"
	RED    Severity = "red"
)

var sevOrder = map[Severity]int{GREEN: 0, YELLOW: 1, RED: 2}

// Finding is one row in a scan.
type Finding struct {
	ProjectRoot string
	Kind        string // "duplicate-daemons" | "remote-helper-failure" | "orphan-state"
	Severity    Severity
	PIDs        []int
	Message     string
	Remediation string
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

// Enumerator is the injectable seam for listing bd-daemon processes.
// Tests assign a fake; the package-level default delegates to
// internal/proc.EnumerateBdDaemons.
var Enumerator func() []proc.DaemonProcess = proc.EnumerateBdDaemons

// RemoteHelperThreshold mirrors Python's `_REMOTE_HELPER_THRESHOLD`.
const RemoteHelperThreshold = 2

// MaxLogTailLines mirrors Python's `_tail_lines(max_lines=500)`.
const MaxLogTailLines = 500

// remoteHelperPatterns mirrors Python's `_REMOTE_HELPER_PATTERNS`.
var remoteHelperPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)Repository not found`),
	regexp.MustCompile(`(?i)could not find git remote helper`),
	regexp.MustCompile(`(?i)could not read Username`),
	regexp.MustCompile(`(?i)Authentication failed`),
	regexp.MustCompile(`(?i)Permission denied \(publickey\)`),
}

// listWorkspaceDaemons filters Enumerator() by workspace == p.Root.
func listWorkspaceDaemons(p bkproject.Project) []proc.DaemonProcess {
	target, err := filepath.EvalSymlinks(p.Root)
	if err != nil {
		target = filepath.Clean(p.Root)
	}
	if abs, err := filepath.Abs(target); err == nil {
		target = abs
	}
	var out []proc.DaemonProcess
	for _, d := range Enumerator() {
		if d.Workspace == target {
			out = append(out, d)
		}
	}
	return out
}

// CountRemoteHelperFailures scans the tail of `.beads/daemon.log`.
// Exported for the diff harness and per-package tests.
func CountRemoteHelperFailures(logPath string) int {
	data, err := os.ReadFile(logPath)
	if err != nil {
		return 0
	}
	// Tail to last MaxLogTailLines.
	all := splitLines(string(data))
	if len(all) > MaxLogTailLines {
		all = all[len(all)-MaxLogTailLines:]
	}
	n := 0
	for _, line := range all {
		for _, p := range remoteHelperPatterns {
			if p.MatchString(line) {
				n++
				break
			}
		}
	}
	return n
}

// DiagnoseProject is the per-project entrypoint.
func DiagnoseProject(p bkproject.Project) []Finding {
	var out []Finding
	here := listWorkspaceDaemons(p)

	if len(here) > 1 {
		pids := make([]int, len(here))
		for i, d := range here {
			pids[i] = d.PID
		}
		sortInts(pids)
		out = append(out, Finding{
			ProjectRoot: p.Root,
			Kind:        "duplicate-daemons",
			Severity:    RED,
			PIDs:        pids,
			Message: itoa(len(here)) + " bd daemon processes are running for this workspace " +
				"(pids: " + joinInts(pids, ", ") + "). Each one races the others on the JSONL and DB.",
			Remediation: "Pick the youngest healthy daemon, stop the others " +
				"(`bd daemons stop <workspace-path|pid>`), then verify with `bd daemon status --all`.",
		})
	}

	logPath := filepath.Join(p.BeadsDir(), "daemon.log")
	nFailures := CountRemoteHelperFailures(logPath)
	if len(here) > 0 && nFailures >= RemoteHelperThreshold {
		pids := make([]int, len(here))
		for i, d := range here {
			pids[i] = d.PID
		}
		out = append(out, Finding{
			ProjectRoot: p.Root,
			Kind:        "remote-helper-failure",
			Severity:    RED,
			PIDs:        pids,
			Message: "bd daemon log shows " + itoa(nFailures) +
				" recent remote-helper / auth failure(s). The daemon is alive but its " +
				"git push/pull is silently failing.",
			Remediation: "Verify a working git credential helper is on the daemon's PATH " +
				"(`git -C <repo> config --get-all credential.helper`); for private repos " +
				"this is the typical silent-sync-death root cause. Restart the daemon once " +
				"the helper is fixed.",
		})
	}

	// orphan-state YELLOW: state file present with no daemon AND no PID.
	hasState := fileExists(filepath.Join(p.BeadsDir(), "sync-state.json")) ||
		fileExists(filepath.Join(p.BeadsDir(), "sync_state.json")) ||
		fileExists(filepath.Join(p.BeadsDir(), "daemon-state.json"))
	pidFile := filepath.Join(p.BeadsDir(), "daemon.pid")
	if hasState && len(here) == 0 && !fileExists(pidFile) {
		out = append(out, Finding{
			ProjectRoot: p.Root,
			Kind:        "orphan-state",
			Severity:    YELLOW,
			Message: "sync-state.json is present but no bd daemon and no daemon.pid file " +
				"exist for this workspace.",
			Remediation: "The state was written by a daemon that's no longer here. Either " +
				"restart the daemon (`bd daemon start ...`) or, if you intentionally " +
				"stopped it, delete the stale `sync-state.json` to avoid confusing later runs.",
		})
	}

	return out
}

// Scan walks roots + aggregates findings.
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

// --- tiny helpers (kept local so the import surface stays narrow) -------

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func sortInts(a []int) {
	// Insertion sort — tiny n, deterministic, no dependency.
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
func joinInts(xs []int, sep string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += sep
		}
		out += strconv.Itoa(x)
	}
	return out
}
