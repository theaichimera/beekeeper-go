package identity

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestNormalizeApplyRewritesJSONL(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeIdentityTOML(t, repo, []string{"alice"}, map[string]string{"alice@example.com": "alice"})
	writeJSONL(t, repo, []map[string]any{
		{"id": "x.1", "assignee": "alice@example.com"},
		{"id": "x.2", "assignee": "alice"},
	})
	r, err := Normalize(repo, false)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if r.DryRun {
		t.Fatal("expected DryRun=false")
	}
	if r.RewroteCount != 1 {
		t.Fatalf("RewroteCount=%d want 1", r.RewroteCount)
	}
	jsonl := filepath.Join(repo, ".beads", "issues.jsonl")
	body, _ := os.ReadFile(jsonl)
	for _, line := range strings.Split(string(body), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		_ = json.Unmarshal([]byte(line), &rec)
		if rec["id"] == "x.1" && rec["assignee"] != "alice" {
			t.Fatalf("x.1 assignee not rewritten: %v", rec)
		}
		if rec["id"] == "x.2" && rec["assignee"] != "alice" {
			t.Fatalf("x.2 assignee changed unexpectedly: %v", rec)
		}
	}
}

func TestNormalizeApplyRefusesLiveDaemon(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeIdentityTOML(t, repo, []string{"alice"}, map[string]string{"a@x": "alice"})
	writeJSONL(t, repo, []map[string]any{
		{"id": "x.1", "assignee": "a@x"},
	})
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(repo, ".beads", "daemon.pid"), []byte(strconv.Itoa(syscall.Getpid())), 0o644))
	_, err := Normalize(repo, false)
	var ne *NormalizeError
	if !errors.As(err, &ne) {
		t.Fatalf("expected NormalizeError; got %v", err)
	}
}

func TestNormalizeDryRunDoesNotWrite(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeIdentityTOML(t, repo, []string{"alice"}, map[string]string{"alice@example.com": "alice"})
	jsonl := filepath.Join(repo, ".beads", "issues.jsonl")
	writeJSONL(t, repo, []map[string]any{
		{"id": "x.1", "assignee": "alice@example.com"},
	})
	before, _ := os.ReadFile(jsonl)
	r, err := Normalize(repo, true)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if !r.DryRun || r.WouldRewriteCount != 1 {
		t.Fatalf("r=%+v", r)
	}
	after, _ := os.ReadFile(jsonl)
	if string(before) != string(after) {
		t.Fatal("dry-run mutated the JSONL")
	}
}
