// Package filesync detects whether a path lives inside a known
// file-sync root (Dropbox, iCloud Drive, OneDrive, Google Drive, etc.).
//
// Port of Python `beadkeeper.filesync`. The default-roots list and the
// label rules mirror the Python source verbatim — when in doubt, that
// implementation is the contract.
//
// Extend the root set via the BEADKEEPER_FILESYNC_ROOTS environment
// variable (a `:` -separated list of additional roots — same as the
// Python tool, which uses os.pathsep).
package filesync

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// EnvVar is the name of the environment variable used to extend the
// default sync-root list. Matches the Python tool.
const EnvVar = "BEADKEEPER_FILESYNC_ROOTS"

// DefaultRoots is the ordered list of candidate sync roots, expressed
// as paths relative to $HOME. Mirrors `_DEFAULT_ROOTS` in filesync.py.
var DefaultRoots = []string{
	"Dropbox",
	"Dropbox (Personal)",
	"Dropbox (Maestral)",
	"Library/Mobile Documents",
	"Library/CloudStorage",
	"OneDrive",
	"Google Drive",
	"GoogleDrive",
	"google-drive",
	"Insync",
}

// Match is returned when a path is found inside a known sync root.
type Match struct {
	Path  string // absolute, symlinks resolved
	Root  string // absolute root that matched
	Label string // short human label (e.g. "Dropbox", "iCloud Drive")
}

// LabelForRoot maps an absolute root path to a short human label.
// Logic mirrors `_label_for_root` in filesync.py.
func LabelForRoot(root string) string {
	name := filepath.Base(root)
	switch {
	case name == "Mobile Documents":
		return "iCloud Drive"
	case name == "CloudStorage":
		return "macOS CloudStorage (OneDrive / Google Drive / Box / iCloud)"
	case strings.HasPrefix(name, "Dropbox"):
		return "Dropbox"
	case name == "OneDrive":
		return "OneDrive"
	case name == "Google Drive", name == "GoogleDrive", name == "google-drive":
		return "Google Drive"
	case name == "Insync":
		return "Insync"
	}
	return name
}

// Options tweaks discovery. All fields are optional; the zero value
// matches the Python tool's defaults.
type Options struct {
	Home  string // override $HOME (tests)
	Env   func(string) string
	Extra []string // additional roots, absolute or ~-prefixed
}

func (o Options) home() string {
	if o.Home != "" {
		return o.Home
	}
	h, _ := os.UserHomeDir()
	return h
}

func (o Options) getenv(key string) string {
	if o.Env != nil {
		return o.Env(key)
	}
	return os.Getenv(key)
}

// KnownRoots returns the absolute paths of file-sync roots that
// EXIST on disk. Existence is checked so we don't warn about a
// Dropbox folder on a machine that has no Dropbox.
func KnownRoots(opts Options) []string {
	home := opts.home()
	cands := make([]string, 0, len(DefaultRoots)+8)
	for _, r := range DefaultRoots {
		cands = append(cands, filepath.Join(home, r))
	}
	cands = append(cands, opts.Extra...)
	envRoots := opts.getenv(EnvVar)
	if envRoots != "" {
		for _, p := range strings.Split(envRoots, string(os.PathListSeparator)) {
			if p != "" {
				cands = append(cands, p)
			}
		}
	}

	seen := make(map[string]struct{}, len(cands))
	out := make([]string, 0, len(cands))
	for _, raw := range cands {
		abs := expandUser(raw, home)
		if abs == "" {
			continue
		}
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			// Fall back to clean path; existence check below filters.
			resolved = filepath.Clean(abs)
		}
		if _, dup := seen[resolved]; dup {
			continue
		}
		seen[resolved] = struct{}{}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			continue
		}
		out = append(out, resolved)
	}
	return out
}

// MatchPath returns a *Match if `path` resolves inside any root in
// `roots`, else nil. When `roots == nil`, KnownRoots is consulted.
// `path` does not need to exist; comparison is on the resolved
// (symlinks-followed) absolute path. Roots passed in by callers are
// NOT existence-filtered (tests supply synthetic roots).
func MatchPath(path string, roots []string, opts Options) *Match {
	target := expandUser(path, opts.home())
	target, _ = absResolve(target)

	if roots == nil {
		roots = KnownRoots(opts)
	}
	// Normalise + sort by depth desc so nested mounts pick the innermost
	// label (Python: candidate_roots.sort by len(parts) reverse).
	normalised := make([]string, 0, len(roots))
	for _, r := range roots {
		abs, _ := absResolve(expandUser(r, opts.home()))
		normalised = append(normalised, abs)
	}
	sort.SliceStable(normalised, func(i, j int) bool {
		return depth(normalised[i]) > depth(normalised[j])
	})

	for _, r := range normalised {
		if isWithin(target, r) {
			return &Match{Path: target, Root: r, Label: LabelForRoot(r)}
		}
	}
	return nil
}

// --- internal -----------------------------------------------------------

func expandUser(p, home string) string {
	if p == "" {
		return ""
	}
	if home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

func absResolve(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	if !filepath.IsAbs(p) {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return filepath.Clean(p), err
	}
	return resolved, nil
}

func depth(p string) int {
	if p == "" {
		return 0
	}
	return strings.Count(filepath.Clean(p), string(filepath.Separator))
}

func isWithin(child, parent string) bool {
	if child == "" || parent == "" {
		return false
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if strings.HasPrefix(rel, "..") {
		return false
	}
	return true
}
