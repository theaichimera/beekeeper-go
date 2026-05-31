package syncbranch

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

func TestSetBranchWritesConfigJSON(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := SetBranch(repo, "beads-sync")
	if err != nil || !res.Written {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	data, _ := os.ReadFile(filepath.Join(repo, ".beads", "config.json"))
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid JSON written: %v\n%s", err, data)
	}
	sync, _ := doc["sync"].(map[string]any)
	if sync["branch"] != "beads-sync" {
		t.Fatalf("doc=%v", doc)
	}
}

func TestSetBranchRefusesNonWorkspace(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	res, err := SetBranch(repo, "beads-sync")
	var se *SetBranchError
	if !errors.As(err, &se) {
		t.Fatalf("expected SetBranchError; got %v", err)
	}
	if res.Written {
		t.Fatalf("res=%+v should be unwritten", res)
	}
}

func TestSetBranchRefusesLiveDaemon(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(repo, ".beads", "daemon.pid"),
		[]byte(strconv.Itoa(syscall.Getpid())),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	res, err := SetBranch(repo, "beads-sync")
	if err == nil || res.Written {
		t.Fatalf("expected refusal; res=%+v err=%v", res, err)
	}
}
