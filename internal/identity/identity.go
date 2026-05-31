// Package identity is the canonical-actor scanner.
//
// Port of Python beadkeeper.identity. M2 is scan/dry-run only — the
// actual JSONL rewrite (mutation) lands in M3 alongside daemon-live
// refusal.
//
// Identity is OPT-IN: a workspace without `.beadkeeper/identity.toml`
// produces (nil, nil) from LoadConfig — callers treat that as "the
// feature is off here" and emit nothing.
package identity

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/theaichimera/beekeeper-go/internal/config"
	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
)

// NormalizeError signals a refused mutation (e.g. daemon alive).
type NormalizeError struct{ Msg string }

func (e *NormalizeError) Error() string { return e.Msg }

// ActorFields mirrors Python's `_ACTOR_FIELDS` — the set of JSONL
// fields that record a human/agent identity.
var ActorFields = []string{
	"assignee",
	"owner",
	"created_by",
	"closed_by",
	"actor",
}

// Report is the per-project scan result.
type Report struct {
	ProjectRoot         string
	Canonical           map[string]struct{}
	AliasedHandles      map[string]string // alias -> canonical
	UnmappedHandles     map[string]struct{}
	AliasedOccurrences  int
	UnmappedOccurrences int
}

// HasDrift returns true if any non-canonical handle is in use.
func (r Report) HasDrift() bool {
	return len(r.AliasedHandles) > 0 || len(r.UnmappedHandles) > 0
}

// SortedAliased returns the alias keys in stable order.
func (r Report) SortedAliased() []string {
	keys := make([]string, 0, len(r.AliasedHandles))
	for k := range r.AliasedHandles {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// SortedUnmapped returns the unmapped keys in stable order.
func (r Report) SortedUnmapped() []string {
	keys := make([]string, 0, len(r.UnmappedHandles))
	for k := range r.UnmappedHandles {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// LoadConfig delegates to internal/config. Re-exported here so callers
// don't have to import both packages.
func LoadConfig(repo string) (*config.IdentityConfig, error) {
	return config.LoadIdentityConfig(repo)
}

// ScanProject reads `.beads/issues.jsonl` and classifies every actor
// handle against the workspace's identity config. Mirrors Python
// scan_project.
func ScanProject(repo string) Report {
	cfg, _ := LoadConfig(repo) // err treated as no-config per Python
	if cfg == nil {
		cfg = &config.IdentityConfig{
			Canonical: map[string]struct{}{},
			Aliases:   map[string]string{},
		}
	}
	repoAbs := repo
	if abs, err := filepath.Abs(repo); err == nil {
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			repoAbs = resolved
		} else {
			repoAbs = abs
		}
	}

	report := Report{
		ProjectRoot:     repoAbs,
		Canonical:       copyStringSet(cfg.Canonical),
		AliasedHandles:  map[string]string{},
		UnmappedHandles: map[string]struct{}{},
	}

	jsonl := filepath.Join(repoAbs, ".beads", "issues.jsonl")
	for rec := range iterJSONL(jsonl) {
		for _, f := range ActorFields {
			raw, ok := rec[f].(string)
			if !ok || raw == "" {
				continue
			}
			if _, canonical := cfg.Canonical[raw]; canonical {
				continue
			}
			if target, isAlias := cfg.Aliases[raw]; isAlias {
				if _, ok := cfg.Canonical[target]; ok {
					report.AliasedHandles[raw] = target
					report.AliasedOccurrences++
					continue
				}
			}
			report.UnmappedHandles[raw] = struct{}{}
			report.UnmappedOccurrences++
		}
	}
	return report
}

// Scan walks roots and aggregates per-project Reports.
type ScanReport struct {
	Projects []Report
}

func Scan(paths []string, maxDepth int) ScanReport {
	if maxDepth <= 0 {
		maxDepth = bkproject.DefaultMaxDepth
	}
	projects := bkproject.FindProjects(paths, maxDepth)
	out := make([]Report, 0, len(projects))
	for _, p := range projects {
		out = append(out, ScanProject(p.Root))
	}
	return ScanReport{Projects: out}
}

// --- dry-run normalize --------------------------------------------------

// NormalizeResult is the result of a Normalize call.
type NormalizeResult struct {
	ProjectRoot       string
	JSONLPath         string
	DryRun            bool
	WouldRewriteCount int
	RewroteCount      int
	MappedHandles     map[string]string
	SkippedUnmapped   map[string]struct{}
}

// PlanNormalize walks the JSONL and reports what a normalize WOULD do.
// Never writes; same shape as Normalize(..., dryRun=true).
func PlanNormalize(repo string) NormalizeResult {
	cfg, _ := LoadConfig(repo)
	if cfg == nil {
		cfg = &config.IdentityConfig{
			Canonical: map[string]struct{}{},
			Aliases:   map[string]string{},
		}
	}
	result := NormalizeResult{
		ProjectRoot:     repo,
		JSONLPath:       filepath.Join(repo, ".beads", "issues.jsonl"),
		DryRun:          true,
		MappedHandles:   map[string]string{},
		SkippedUnmapped: map[string]struct{}{},
	}
	for rec := range iterJSONL(result.JSONLPath) {
		for _, f := range ActorFields {
			raw, ok := rec[f].(string)
			if !ok || raw == "" {
				continue
			}
			if _, canonical := cfg.Canonical[raw]; canonical {
				continue
			}
			if target, isAlias := cfg.Aliases[raw]; isAlias {
				if _, ok := cfg.Canonical[target]; ok {
					result.MappedHandles[raw] = target
					result.WouldRewriteCount++
					continue
				}
			}
			result.SkippedUnmapped[raw] = struct{}{}
		}
	}
	return result
}

// --- helpers ------------------------------------------------------------

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
		// Large JSONL records are routine; bump the buffer so a long
		// line doesn't truncate.
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

func copyStringSet(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for k := range in {
		out[k] = struct{}{}
	}
	return out
}

// Normalize rewrites aliased handles in `.beads/issues.jsonl` to their
// canonical form. Refuses while the bd daemon is alive. Returns a
// result describing what was (or would be) changed.
//
// Mirrors Python `normalize(... dry_run=...)` verbatim:
//   - Only ALIASED handles are rewritten; unmapped handles are left
//     untouched and surfaced via `SkippedUnmapped`.
//   - When `dryRun` is true, no IO is performed beyond reads.
//   - When `dryRun` is false and the JSONL has changes, the file is
//     rewritten atomically (write + rename).
func Normalize(repo string, dryRun bool) (NormalizeResult, error) {
	repoAbs := repo
	if abs, err := filepath.Abs(repo); err == nil {
		repoAbs = abs
	}
	p := bkproject.Project{Root: repoAbs}
	result := NormalizeResult{
		ProjectRoot:     repoAbs,
		JSONLPath:       p.IssuesJSONL(),
		DryRun:          dryRun,
		MappedHandles:   map[string]string{},
		SkippedUnmapped: map[string]struct{}{},
	}
	if _, err := os.Stat(result.JSONLPath); err != nil {
		// Missing JSONL is silent — nothing to rewrite.
		return result, nil
	}
	cfg, _ := LoadConfig(repoAbs)
	if cfg == nil {
		cfg = &config.IdentityConfig{
			Canonical: map[string]struct{}{},
			Aliases:   map[string]string{},
		}
	}

	if !dryRun {
		st := bkproject.ReadDaemonState(p)
		if st.PIDAlive {
			return result, &NormalizeError{Msg: fmt.Sprintf(
				"refusing to rewrite %s: bd daemon (pid %d) is alive. Stop the daemon and re-run.",
				result.JSONLPath, st.PID,
			)}
		}
	}

	// Read raw lines so we preserve original line ordering exactly.
	data, err := os.ReadFile(result.JSONLPath)
	if err != nil {
		return result, err
	}
	lines := splitLinesPreservingEmpty(string(data))
	anyChanges := false
	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		changed := false
		for _, f := range ActorFields {
			v, ok := rec[f].(string)
			if !ok || v == "" {
				continue
			}
			if _, canonical := cfg.Canonical[v]; canonical {
				continue
			}
			target, isAlias := cfg.Aliases[v]
			if isAlias {
				if _, ok := cfg.Canonical[target]; ok {
					rec[f] = target
					result.MappedHandles[v] = target
					changed = true
					if dryRun {
						result.WouldRewriteCount++
					} else {
						result.RewroteCount++
					}
					continue
				}
			}
			result.SkippedUnmapped[v] = struct{}{}
		}
		if changed {
			b, _ := json.Marshal(rec)
			lines[i] = string(b)
			anyChanges = true
		}
	}

	if !dryRun && anyChanges {
		out := strings.Join(lines, "\n")
		// Preserve trailing newline if the original had one.
		if len(data) > 0 && data[len(data)-1] == '\n' && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		tmp := result.JSONLPath + ".tmp"
		if err := os.WriteFile(tmp, []byte(out), 0o644); err != nil {
			return result, err
		}
		if err := os.Rename(tmp, result.JSONLPath); err != nil {
			return result, err
		}
	}
	return result, nil
}

func splitLinesPreservingEmpty(s string) []string {
	if s == "" {
		return nil
	}
	out := strings.Split(s, "\n")
	// trailing empty entry from trailing newline — drop so write-back
	// doesn't double the trailing newline.
	if len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}
