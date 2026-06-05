// Package beadlint is the shared plumbing that reads a repo's bead
// JSONL and validates each non-closed bead against its type schema via
// pkg/beadspec. Both `bk doctor` (visibility) and `bk guard beadspec`
// (the pre-push hard gate) call it, so the validation has exactly one
// implementation (bkg-4zi.3 / .6).
package beadlint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/theaichimera/beekeeper-go/pkg/beadspec"
)

// JSONLRelPath is the bead store location within a repo.
const JSONLRelPath = ".beads/issues.jsonl"

// ScanRepo validates the beads in repoRoot/.beads/issues.jsonl.
func ScanRepo(repoRoot string) []beadspec.Finding {
	return ScanJSONL(filepath.Join(repoRoot, JSONLRelPath))
}

// ScanJSONL validates every non-closed bead in the JSONL file at path
// against the schema for its issue_type. Beads whose type has no schema
// are skipped. Returns all findings (empty when clean or unreadable).
func ScanJSONL(path string) []beadspec.Finding {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var findings []beadspec.Finding
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		switch str(rec, "status") {
		case "closed", "tombstone":
			continue
		}
		typ := str(rec, "issue_type")
		if !beadspec.Has(typ) {
			continue
		}
		fs, err := beadspec.ValidateBead(beadspec.Bead{
			ID:     str(rec, "id"),
			Type:   typ,
			Labels: strs(rec, "labels"),
			Body:   str(rec, "description"),
		})
		if err != nil {
			continue
		}
		findings = append(findings, fs...)
	}
	return findings
}

// FilterByType returns findings on beads of the given issue type.
func FilterByType(fs []beadspec.Finding, beadType string) []beadspec.Finding {
	var out []beadspec.Finding
	for _, f := range fs {
		if f.Type == beadType {
			out = append(out, f)
		}
	}
	return out
}

func str(rec map[string]any, key string) string {
	if v, ok := rec[key].(string); ok {
		return v
	}
	return ""
}

func strs(rec map[string]any, key string) []string {
	raw, ok := rec[key].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
