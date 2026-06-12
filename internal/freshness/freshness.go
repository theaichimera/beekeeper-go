// Package freshness detects when a project's bead DB has mutations
// not yet exported to `.beads/issues.jsonl`.
//
// bd writes mutations to its sqlite DB immediately but exports
// DB -> JSONL on a debounce (daemon flush), so right after a
// `bd update` every bk surface that reads the JSONL (doctor, board,
// guard beadspec) reports stale state until `bd sync` runs. The probe
// is a pure mtime comparison — read-only, no bd shell-out, and bk
// NEVER auto-flushes the DB itself (bkg-ckb).
package freshness

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Result is the outcome of one DB-vs-JSONL mtime comparison.
type Result struct {
	// Stale is true when the bead DB (or its WAL sidecar) is strictly
	// newer than the JSONL. Any DB-newer-than-JSONL counts — mtime
	// granularity is fine; no grace window.
	Stale bool
	// DBPath is the `.db` file whose effective mtime won. Empty when
	// no DB exists under .beads/.
	DBPath string
	// DBTime is the newest of the winning DB's own mtime and its
	// `-wal` sidecar's (a newer WAL means unflushed sqlite writes).
	// Zero when no DB exists.
	DBTime time.Time
	// JSONLTime is the mtime of `.beads/issues.jsonl`. Zero when the
	// JSONL is missing.
	JSONLTime time.Time
}

// Probe compares the mtimes of every `.beads/*.db` (and `-wal`
// sidecar) against `.beads/issues.jsonl` in repoRoot. Missing JSONL
// or missing DB is NOT stale — other checks own those failure modes
// (e.g. doctor's issues-jsonl check).
func Probe(repoRoot string) Result {
	beadsDir := filepath.Join(repoRoot, ".beads")
	var r Result
	jsonlInfo, err := os.Stat(filepath.Join(beadsDir, "issues.jsonl"))
	if err != nil {
		return r
	}
	r.JSONLTime = jsonlInfo.ModTime()

	entries, err := os.ReadDir(beadsDir)
	if err != nil {
		return r
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		dbPath := filepath.Join(beadsDir, e.Name())
		info, err := os.Stat(dbPath)
		if err != nil {
			continue
		}
		mt := info.ModTime()
		if walInfo, err := os.Stat(dbPath + "-wal"); err == nil && walInfo.ModTime().After(mt) {
			mt = walInfo.ModTime()
		}
		if mt.After(r.DBTime) {
			r.DBTime = mt
			r.DBPath = dbPath
		}
	}
	if r.DBPath == "" {
		return r
	}
	r.Stale = r.DBTime.After(r.JSONLTime)
	return r
}
