package freshness

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeBeads creates <root>/.beads with the named files and returns
// the repo root plus the .beads path. Contents are irrelevant — the
// probe is mtime-only.
func writeBeads(t *testing.T, names ...string) (string, string) {
	t.Helper()
	root := t.TempDir()
	beads := filepath.Join(root, ".beads")
	if err := os.MkdirAll(beads, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(beads, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, beads
}

// touch sets a file's mtime (and atime) to the given instant.
func touch(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestProbe_DBNewerIsStale(t *testing.T) {
	root, beads := writeBeads(t, "issues.jsonl", "beads.db")
	base := time.Now().Add(-time.Hour)
	touch(t, filepath.Join(beads, "issues.jsonl"), base)
	touch(t, filepath.Join(beads, "beads.db"), base.Add(30*time.Second))

	r := Probe(root)
	if !r.Stale {
		t.Fatalf("DB newer than JSONL must be stale: %+v", r)
	}
	if filepath.Base(r.DBPath) != "beads.db" {
		t.Errorf("DBPath = %q, want beads.db", r.DBPath)
	}
	if !r.DBTime.After(r.JSONLTime) {
		t.Errorf("DBTime %v should be after JSONLTime %v", r.DBTime, r.JSONLTime)
	}
}

func TestProbe_JSONLNewerIsFresh(t *testing.T) {
	root, beads := writeBeads(t, "issues.jsonl", "beads.db")
	base := time.Now().Add(-time.Hour)
	touch(t, filepath.Join(beads, "beads.db"), base)
	touch(t, filepath.Join(beads, "issues.jsonl"), base.Add(30*time.Second))

	if r := Probe(root); r.Stale {
		t.Fatalf("JSONL newer than DB must not be stale: %+v", r)
	}
}

func TestProbe_MissingJSONLNotStale(t *testing.T) {
	root, _ := writeBeads(t, "beads.db")
	if r := Probe(root); r.Stale {
		t.Fatalf("missing JSONL is owned by other checks, not freshness: %+v", r)
	}
}

func TestProbe_MissingDBNotStale(t *testing.T) {
	root, _ := writeBeads(t, "issues.jsonl")
	r := Probe(root)
	if r.Stale {
		t.Fatalf("no DB means nothing to compare: %+v", r)
	}
	if r.DBPath != "" {
		t.Errorf("DBPath should be empty without a DB, got %q", r.DBPath)
	}
}

func TestProbe_MissingBeadsDirNotStale(t *testing.T) {
	if r := Probe(t.TempDir()); r.Stale {
		t.Fatalf("no .beads/ at all must not be stale: %+v", r)
	}
}

func TestProbe_WALNewerThanDBAndJSONLIsStale(t *testing.T) {
	root, beads := writeBeads(t, "issues.jsonl", "beads.db", "beads.db-wal")
	base := time.Now().Add(-time.Hour)
	// DB itself is older than the JSONL, but the WAL (unflushed sqlite
	// writes) is newer than both.
	touch(t, filepath.Join(beads, "beads.db"), base)
	touch(t, filepath.Join(beads, "issues.jsonl"), base.Add(10*time.Second))
	touch(t, filepath.Join(beads, "beads.db-wal"), base.Add(30*time.Second))

	r := Probe(root)
	if !r.Stale {
		t.Fatalf("WAL newer than JSONL must be stale: %+v", r)
	}
	if !r.DBTime.After(r.JSONLTime) {
		t.Errorf("DBTime should reflect the WAL mtime (after JSONL): db=%v jsonl=%v",
			r.DBTime, r.JSONLTime)
	}
}

func TestProbe_MultipleDBsPicksNewest(t *testing.T) {
	root, beads := writeBeads(t, "issues.jsonl", "old.db", "new.db")
	base := time.Now().Add(-time.Hour)
	touch(t, filepath.Join(beads, "old.db"), base)
	touch(t, filepath.Join(beads, "issues.jsonl"), base.Add(10*time.Second))
	touch(t, filepath.Join(beads, "new.db"), base.Add(30*time.Second))

	r := Probe(root)
	if !r.Stale {
		t.Fatalf("newest DB beats the JSONL: %+v", r)
	}
	if filepath.Base(r.DBPath) != "new.db" {
		t.Errorf("DBPath = %q, want new.db", r.DBPath)
	}
}
