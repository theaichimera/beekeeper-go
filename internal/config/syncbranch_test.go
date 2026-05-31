package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadSyncBranchFromJSONPresent(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(repo, BeadsConfigJSONRelPath),
		[]byte(`{"sync":{"branch":"beads-sync"}}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	branch, err := ReadSyncBranchFromJSON(repo)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if branch != "beads-sync" {
		t.Fatalf("branch=%q want beads-sync", branch)
	}
}

func TestReadSyncBranchFromJSONMissingFile(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	branch, err := ReadSyncBranchFromJSON(repo)
	if err != nil || branch != "" {
		t.Fatalf("missing file should be (\"\", nil); got %q, %v", branch, err)
	}
}

func TestReadSyncBranchFromJSONMalformedIsNonFatal(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(repo, BeadsConfigJSONRelPath),
		[]byte(`not json at all`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	branch, err := ReadSyncBranchFromJSON(repo)
	if err != nil || branch != "" {
		t.Fatalf("malformed should be (\"\", nil); got %q, %v", branch, err)
	}
}
