package mergeslot

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	execwrap "github.com/theaichimera/beekeeper-go/internal/exec"
)

const slotID = "bk-merge-slot"

func writeSlot(t *testing.T, repo, status, holder string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repo, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	rec := map[string]any{"id": slotID, "status": status, "type": "merge-slot"}
	if holder != "" {
		rec["holder"] = holder
	}
	b, _ := json.Marshal(rec)
	if err := os.WriteFile(filepath.Join(repo, ".beads", "issues.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fakeBd(t *testing.T, repo string) (BdRunner, *[][]string) {
	t.Helper()
	calls := &[][]string{}
	runner := BdRunner(func(args []string, cwd string, _ time.Duration) (int, string, string, error) {
		*calls = append(*calls, append([]string{}, args...))
		if len(args) >= 3 && args[0] == "bd" && args[1] == "merge-slot" {
			rec := readMergeSlot(filepath.Join(repo, ".beads", "issues.jsonl"))
			switch args[2] {
			case "create":
				if rec == nil {
					rec = map[string]any{"id": slotID, "status": "open", "type": "merge-slot"}
					writeMergeSlot(t, repo, rec)
				}
				return 0, "", "", nil
			case "check":
				return 0, "", "", nil
			case "acquire":
				holder := ""
				for i, a := range args {
					if a == "--holder" && i+1 < len(args) {
						holder = args[i+1]
					}
				}
				if rec == nil {
					return 1, "", "no slot", nil
				}
				if rec["status"] == "in_progress" {
					return 1, "", "held", nil
				}
				rec["status"] = "in_progress"
				if holder == "" {
					holder = "unknown"
				}
				rec["holder"] = holder
				writeMergeSlot(t, repo, rec)
				return 0, "", "", nil
			case "release":
				if rec == nil || rec["status"] != "in_progress" {
					return 1, "", "not held", nil
				}
				rec["status"] = "open"
				delete(rec, "holder")
				writeMergeSlot(t, repo, rec)
				return 0, "", "", nil
			}
		}
		return execwrap.Default(args, cwd, 10*time.Second)
	})
	return runner, calls
}

func readMergeSlot(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec["type"] == "merge-slot" {
			return rec
		}
	}
	return nil
}

func writeMergeSlot(t *testing.T, repo string, rec map[string]any) {
	t.Helper()
	b, _ := json.Marshal(rec)
	if err := os.WriteFile(filepath.Join(repo, ".beads", "issues.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- status ----------------------------------------------------------------

func TestStatusMissing(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	_ = os.MkdirAll(filepath.Join(repo, ".beads"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, ".beads", "issues.jsonl"), nil, 0o644)
	s := Status(repo)
	if s.Status != "missing" || s.Holder != "" {
		t.Fatalf("status=%+v", s)
	}
}

func TestStatusOpen(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeSlot(t, repo, "open", "")
	s := Status(repo)
	if s.Status != "open" || s.Holder != "" || s.SlotID != slotID {
		t.Fatalf("status=%+v", s)
	}
}

func TestStatusHeld(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeSlot(t, repo, "in_progress", "alice")
	s := Status(repo)
	if s.Status != "in_progress" || s.Holder != "alice" {
		t.Fatalf("status=%+v", s)
	}
}

// --- acquire ---------------------------------------------------------------

func TestAcquireOpenSlotSucceeds(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeSlot(t, repo, "open", "")
	runner, calls := fakeBd(t, repo)
	res, err := Acquire(repo, "alice", false, runner)
	if err != nil || !res.Acquired || res.AlreadyHeld {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(*calls) == 0 {
		t.Fatalf("expected bd call")
	}
	last := (*calls)[len(*calls)-1]
	if last[1] != "merge-slot" || last[2] != "acquire" {
		t.Fatalf("last call=%v", last)
	}
	s := Status(repo)
	if s.Status != "in_progress" || s.Holder != "alice" {
		t.Fatalf("post-acquire status=%+v", s)
	}
}

func TestAcquireSelfIdempotentNoCall(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeSlot(t, repo, "in_progress", "alice")
	runner, calls := fakeBd(t, repo)
	res, err := Acquire(repo, "alice", false, runner)
	if err != nil || !res.AlreadyHeld {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(*calls) != 0 {
		t.Fatalf("expected no bd call; got %v", *calls)
	}
}

func TestAcquireConflictRefused(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeSlot(t, repo, "in_progress", "bob")
	runner, _ := fakeBd(t, repo)
	_, err := Acquire(repo, "alice", false, runner)
	var c *MergeSlotConflict
	if !errors.As(err, &c) {
		t.Fatalf("expected MergeSlotConflict; got %v", err)
	}
}

func TestAcquireRefusesLiveDaemon(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeSlot(t, repo, "open", "")
	_ = os.WriteFile(filepath.Join(repo, ".beads", "daemon.pid"), []byte(strconv.Itoa(syscall.Getpid())), 0o644)
	runner, _ := fakeBd(t, repo)
	_, err := Acquire(repo, "alice", false, runner)
	if err == nil || !strings.Contains(err.Error(), "daemon") {
		t.Fatalf("expected daemon refusal; got %v", err)
	}
}

// --- release ---------------------------------------------------------------

func TestReleaseByHolder(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeSlot(t, repo, "in_progress", "alice")
	runner, _ := fakeBd(t, repo)
	res, err := Release(repo, "alice", runner)
	if err != nil || !res.Released {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	s := Status(repo)
	if s.Status != "open" || s.Holder != "" {
		t.Fatalf("post-release status=%+v", s)
	}
}

func TestReleaseNonHolderConflict(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeSlot(t, repo, "in_progress", "bob")
	runner, _ := fakeBd(t, repo)
	_, err := Release(repo, "alice", runner)
	var c *MergeSlotConflict
	if !errors.As(err, &c) {
		t.Fatalf("expected MergeSlotConflict; got %v", err)
	}
}

func TestReleaseWhenOpenIsNoop(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeSlot(t, repo, "open", "")
	runner, calls := fakeBd(t, repo)
	res, err := Release(repo, "alice", runner)
	if err != nil || !res.AlreadyReleased {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(*calls) != 0 {
		t.Fatalf("expected no bd call; got %v", *calls)
	}
}

// --- critical section -----------------------------------------------------

func TestCriticalSectionAcquiresThenReleases(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeSlot(t, repo, "open", "")
	runner, _ := fakeBd(t, repo)
	err := CriticalSection(repo, "alice", false, runner, func() error {
		s := Status(repo)
		if s.Status != "in_progress" || s.Holder != "alice" {
			t.Fatalf("inside CS: %+v", s)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	s := Status(repo)
	if s.Status != "open" || s.Holder != "" {
		t.Fatalf("post-CS status=%+v", s)
	}
}

func TestCriticalSectionReleasesOnError(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeSlot(t, repo, "open", "")
	runner, _ := fakeBd(t, repo)
	boom := errors.New("cleanup test")
	err := CriticalSection(repo, "alice", false, runner, func() error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("expected boom to propagate; got %v", err)
	}
	if s := Status(repo); s.Status != "open" {
		t.Fatalf("slot not released on error: %+v", s)
	}
}

func TestCriticalSectionRefusedWhenHeldByOther(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeSlot(t, repo, "in_progress", "bob")
	runner, _ := fakeBd(t, repo)
	err := CriticalSection(repo, "alice", false, runner, func() error {
		t.Fatal("body should not run")
		return nil
	})
	var c *MergeSlotConflict
	if !errors.As(err, &c) {
		t.Fatalf("expected MergeSlotConflict; got %v", err)
	}
}
