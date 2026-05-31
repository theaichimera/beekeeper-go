// Package project discovers beads workspaces under a set of roots and
// reads their on-disk state (git refs, daemon PID/log, bd config
// fallback).
//
// Port of Python `beadkeeper.project`. Skips heavy directories
// (`.git`, `node_modules`, `.venv`, etc.) the same way the Python
// walker does.
package project

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/theaichimera/beekeeper-go/internal/config"
	"github.com/theaichimera/beekeeper-go/internal/git"
	"github.com/theaichimera/beekeeper-go/internal/proc"
)

// BeadsDirName is the directory name we look for inside a project
// root. Mirrors Python's `BEADS_DIR_NAME`.
const BeadsDirName = ".beads"

// DefaultMaxDepth is the walk depth used by all top-level commands
// in the Python tool.
const DefaultMaxDepth = 4

// SkipDirs is the set of directory names skipped during discovery.
// Mirrors Python's `skip_names`.
var SkipDirs = map[string]struct{}{
	".git":          {},
	"node_modules":  {},
	".venv":         {},
	"venv":          {},
	"__pycache__":   {},
	".mypy_cache":   {},
	".pytest_cache": {},
	".ruff_cache":   {},
	"dist":          {},
	"build":         {},
}

// Project is a discovered workspace.
type Project struct {
	Root string // absolute, symlinks resolved
}

// BeadsDir returns Root + "/.beads".
func (p Project) BeadsDir() string { return filepath.Join(p.Root, BeadsDirName) }

// IssuesJSONL returns Root + "/.beads/issues.jsonl".
func (p Project) IssuesJSONL() string { return filepath.Join(p.BeadsDir(), "issues.jsonl") }

// FindProjects walks each root (up to maxDepth) looking for any
// directory containing a `.beads/` subdir. Order is deterministic
// (sorted by absolute path).
func FindProjects(roots []string, maxDepth int) []Project {
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	seen := make(map[string]struct{})
	var out []string
	for _, r := range roots {
		root, err := absResolve(r)
		if err != nil {
			continue
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			continue
		}
		// Root itself can be a project.
		if isProject(root) {
			if _, dup := seen[root]; !dup {
				seen[root] = struct{}{}
				out = append(out, root)
			}
		}
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if errors.Is(walkErr, fs.ErrPermission) {
					return fs.SkipDir
				}
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			// Compute depth relative to root.
			rel, _ := filepath.Rel(root, path)
			depth := 0
			if rel != "." {
				depth = len(strings.Split(rel, string(filepath.Separator)))
			}
			if _, skip := SkipDirs[d.Name()]; skip && path != root {
				return fs.SkipDir
			}
			if depth >= maxDepth {
				return fs.SkipDir
			}
			if isProject(path) {
				if _, dup := seen[path]; !dup {
					seen[path] = struct{}{}
					out = append(out, path)
				}
			}
			return nil
		})
	}
	sort.Strings(out)
	projs := make([]Project, len(out))
	for i, p := range out {
		projs[i] = Project{Root: p}
	}
	return projs
}

func isProject(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, BeadsDirName))
	return err == nil && info.IsDir()
}

func absResolve(p string) (string, error) {
	if p == "" {
		return "", errors.New("empty path")
	}
	if !filepath.IsAbs(p) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		p = abs
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved, nil
	}
	return filepath.Clean(p), nil
}

// DiscoverDBPaths returns every `.beads/<name>` that looks like a
// SQLite DB or its sidecars (`.db`, `.db-wal`, `.db-shm`).
//
// Mirrors Python `discover_db_paths`.
func DiscoverDBPaths(p Project) []string {
	entries, err := os.ReadDir(p.BeadsDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasSuffix(n, ".db") ||
			strings.HasSuffix(n, ".db-wal") ||
			strings.HasSuffix(n, ".db-shm") {
			out = append(out, filepath.Join(p.BeadsDir(), n))
		}
	}
	sort.Strings(out)
	return out
}

// --- daemon state -------------------------------------------------------

// DaemonState mirrors Python's read_daemon_state result.
type DaemonState struct {
	PID           int
	PIDPresent    bool
	PIDAlive      bool
	LogPath       string
	StatePath     string
	LastSyncState map[string]any
	Notes         []string
}

// ReadDaemonState reads `.beads/daemon.pid` + a `*sync*state*.json`
// file if present. Never invokes a subprocess; pure file reads + a
// kill(pid, 0) liveness check via internal/proc.
func ReadDaemonState(p Project) DaemonState {
	st := DaemonState{}
	if _, err := os.Stat(p.BeadsDir()); err != nil {
		st.Notes = append(st.Notes, ".beads/ missing")
		return st
	}

	if pidBytes, err := os.ReadFile(filepath.Join(p.BeadsDir(), "daemon.pid")); err == nil {
		s := strings.TrimSpace(string(pidBytes))
		if s != "" {
			if pid, err := strconv.Atoi(s); err == nil {
				st.PID = pid
				st.PIDPresent = true
				st.PIDAlive = proc.PidAlive(pid)
			} else {
				st.Notes = append(st.Notes, "daemon.pid unreadable")
			}
		}
	}

	logPath := filepath.Join(p.BeadsDir(), "daemon.log")
	if _, err := os.Stat(logPath); err == nil {
		st.LogPath = logPath
	}

	// Known state-file names, in priority order.
	candidates := []string{"sync-state.json", "sync_state.json", "daemon-state.json"}
	for _, name := range candidates {
		full := filepath.Join(p.BeadsDir(), name)
		if _, err := os.Stat(full); err == nil {
			st.StatePath = full
			break
		}
	}
	if st.StatePath == "" {
		// Fall back to any "*sync*state*.json" glob hit (lexicographic).
		matches, _ := filepath.Glob(filepath.Join(p.BeadsDir(), "*sync*state*.json"))
		if len(matches) > 0 {
			sort.Strings(matches)
			st.StatePath = matches[0]
		}
	}
	if st.StatePath != "" {
		if data, err := os.ReadFile(st.StatePath); err == nil {
			var m map[string]any
			if err := json.Unmarshal(data, &m); err == nil {
				st.LastSyncState = m
			} else {
				st.Notes = append(st.Notes, "state file unparseable: "+err.Error())
			}
		}
	}

	return st
}

// --- git state ----------------------------------------------------------

// GitState mirrors Python's read_git_state result.
type GitState struct {
	Branch     string
	Upstream   string
	IsClean    *bool // nil when status couldn't be read
	SyncBranch string
	Notes      []string
}

// ReadGitState reads the current git branch / upstream / cleanness,
// then resolves `sync.branch` from bd config (with config.json
// fallback). Identical contract to Python read_git_state.
func ReadGitState(p Project) GitState {
	st := GitState{}
	if br, ok := git.CurrentBranch(p.Root); ok {
		st.Branch = br
	} else {
		st.Notes = append(st.Notes, "git branch: failed")
	}
	if up, ok := git.Upstream(p.Root); ok {
		st.Upstream = up
	}
	if clean, ok := git.IsClean(p.Root); ok {
		st.IsClean = &clean
	}

	// bd config get sync.branch
	bdGave := false
	rc, out, _, _ := bdRun([]string{"bd", "config", "get", "sync.branch"}, p.Root)
	if rc == 0 {
		if val := config.ParseBdConfigValue(out); val != "" {
			st.SyncBranch = val
			bdGave = true
		}
	}
	if !bdGave {
		if val, err := config.ReadSyncBranchFromJSON(p.Root); err == nil && val != "" {
			st.SyncBranch = val
		}
	}

	return st
}
