// Package proc enumerates `bd daemon` processes on the host and looks
// up each process's current working directory.
//
// The Python tool's daemons.py is the contract:
//
//   - List processes via `ps -axo pid=,command=`.
//   - Match lines that look like `(^|/)bd(\s|$)` AND contain
//     `\bdaemon\b`.
//   - Workspace resolution priority:
//     1. `--workspace=<path>` / `--workspace <path>` in the cmdline.
//     2. The process's cwd.
//     3. The rightmost looks-like-a-path token in the cmdline
//     (excluding tokens ending in `/bd`).
//
// PID liveness uses kill(pid, 0): ProcessLookupError -> dead;
// PermissionError -> alive (different user).
//
// The platform-sensitive bits live in proc_unix.go (kill(2) liveness),
// proc_darwin.go (lsof -p cwd), proc_linux.go (/proc/<pid>/cwd), and
// proc_windows.go (OpenProcess liveness; cwd lookup is unsupported and
// falls back to the cmdline path token).
package proc

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	execwrap "github.com/theaichimera/beekeeper-go/internal/exec"
)

// PsArgs is the exact argv used to list processes. Mirrors Python.
var PsArgs = []string{"ps", "-axo", "pid=,command="}

// DaemonProcess is a parsed `bd daemon` process.
type DaemonProcess struct {
	PID       int
	Workspace string // absolute, may be "" when unresolvable
}

// CwdLookup resolves a PID's current working directory. Each platform
// supplies a default implementation via init(); tests inject fakes.
var CwdLookup func(pid int) string = defaultCwdLookup

// PsRunner is the subprocess runner used to fetch `ps` output. Tests
// inject fakes that emit fixture strings.
var PsRunner execwrap.Runner = execwrap.Default

// PidAlive returns true iff signal-0 to `pid` does not raise ESRCH.
// EPERM means the process exists but belongs to another user -> alive.
// Mirrors `_pid_alive` in project.py.
func PidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return pidAlive(pid)
}

// EnumerateBdDaemons runs `ps` and returns every `bd daemon` process.
func EnumerateBdDaemons() []DaemonProcess {
	rc, stdout, _, _ := PsRunner(PsArgs, "", 10*time.Second)
	if rc != 0 {
		return nil
	}
	return ParsePs(stdout)
}

// ListWorkspaceDaemons returns processes whose workspace resolves to
// `workspace` (after EvalSymlinks).
func ListWorkspaceDaemons(workspace string) []DaemonProcess {
	target, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		target = filepath.Clean(workspace)
	}
	if abs, err := filepath.Abs(target); err == nil {
		target = abs
	}
	var out []DaemonProcess
	for _, p := range EnumerateBdDaemons() {
		if p.Workspace == target {
			out = append(out, p)
		}
	}
	return out
}

// --- parser -------------------------------------------------------------

var (
	bdRe     = regexp.MustCompile(`(^|/)bd(\s|$)`)
	daemonRe = regexp.MustCompile(`\bdaemon\b`)
	wsFlagRe = regexp.MustCompile(`--workspace[=\s]+(\S+)`)
)

// ParsePs converts raw `ps -axo pid=,command=` output to a list of
// DaemonProcess entries. Exported so tests can verify the parser
// against recorded fixtures without invoking `ps`.
func ParsePs(output string) []DaemonProcess {
	var out []DaemonProcess
	for _, line := range strings.Split(output, "\n") {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		// `ps -axo pid=,command=` prints `<pid> <cmd>` with a space
		// between (after suppressed headers). Split on the first run
		// of whitespace.
		i := indexFirstSpace(s)
		if i < 0 {
			continue
		}
		pidStr, cmd := s[:i], strings.TrimLeft(s[i:], " \t")
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}
		if !bdRe.MatchString(cmd) {
			continue
		}
		if !daemonRe.MatchString(cmd) {
			continue
		}
		ws := extractWorkspace(cmd, pid)
		out = append(out, DaemonProcess{PID: pid, Workspace: ws})
	}
	return out
}

func indexFirstSpace(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			return i
		}
	}
	return -1
}

func extractWorkspace(cmd string, pid int) string {
	if m := wsFlagRe.FindStringSubmatch(cmd); m != nil {
		return resolveAbs(m[1])
	}
	if CwdLookup != nil {
		if c := CwdLookup(pid); c != "" {
			return c
		}
	}
	// Rightmost looks-like-a-path token; skip "/.../bd".
	toks := strings.Fields(cmd)
	for i := len(toks) - 1; i >= 0; i-- {
		t := toks[i]
		if strings.Contains(t, "/") && !strings.HasSuffix(t, "/bd") {
			return resolveAbs(t)
		}
	}
	return ""
}

func resolveAbs(p string) string {
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return filepath.Clean(p)
}
