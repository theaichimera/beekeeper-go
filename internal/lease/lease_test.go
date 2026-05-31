package lease

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
)

func writeIdentity(t *testing.T, repo string, canonical []string, aliases map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repo, ".beadkeeper"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[identity]\ncanonical = ["
	for i, c := range canonical {
		if i > 0 {
			body += ", "
		}
		body += `"` + c + `"`
	}
	body += "]\n\n[identity.aliases]\n"
	for k, v := range aliases {
		body += `"` + k + `" = "` + v + `"` + "\n"
	}
	if err := os.WriteFile(filepath.Join(repo, ".beadkeeper", "identity.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeJSONL(t *testing.T, repo string, records []map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repo, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	var body []byte
	for _, r := range records {
		b, _ := json.Marshal(r)
		body = append(body, b...)
		body = append(body, '\n')
	}
	if err := os.WriteFile(filepath.Join(repo, ".beads", "issues.jsonl"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeBd mirrors Python's FakeBd: records calls and mutates the JSONL
// so a follow-on read sees the new state.
func fakeBd(t *testing.T, repo string) (BdRunner, *[][]string) {
	t.Helper()
	calls := &[][]string{}
	runner := BdRunner(func(args []string, cwd string, _ time.Duration) (int, string, string, error) {
		*calls = append(*calls, append([]string{}, args...))
		if len(args) >= 3 && args[0] == "bd" && args[1] == "update" {
			issueID := args[2]
			patch := map[string]any{}
			for i := 3; i+1 < len(args); i++ {
				switch args[i] {
				case "--status":
					patch["status"] = args[i+1]
					i++
				case "--assignee":
					patch["assignee"] = args[i+1]
					i++
				}
			}
			jsonlPath := filepath.Join(repo, ".beads", "issues.jsonl")
			raw, _ := os.ReadFile(jsonlPath)
			lines := strings.Split(string(raw), "\n")
			out := make([]string, 0, len(lines))
			for _, line := range lines {
				if strings.TrimSpace(line) == "" {
					continue
				}
				var rec map[string]any
				if err := json.Unmarshal([]byte(line), &rec); err != nil {
					out = append(out, line)
					continue
				}
				if rec["id"] == issueID {
					for k, v := range patch {
						rec[k] = v
					}
					if a, _ := rec["assignee"].(string); a == "" {
						delete(rec, "assignee")
					}
				}
				b, _ := json.Marshal(rec)
				out = append(out, string(b))
			}
			_ = os.WriteFile(jsonlPath, []byte(strings.Join(out, "\n")+"\n"), 0o644)
		}
		return 0, "", "", nil
	})
	return runner, calls
}

// --- caller resolution -------------------------------------------------

func TestResolveCallerAlias(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeIdentity(t, repo, []string{"alice"}, map[string]string{"alice@example.com": "alice"})
	got, err := ResolveCaller(repo, "alice@example.com")
	if err != nil || got != "alice" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestResolveCallerCanonical(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeIdentity(t, repo, []string{"alice"}, map[string]string{})
	got, err := ResolveCaller(repo, "alice")
	if err != nil || got != "alice" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestResolveCallerUnmappedErrs(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeIdentity(t, repo, []string{"alice"}, map[string]string{})
	if _, err := ResolveCaller(repo, "carol"); err == nil {
		t.Fatal("expected error")
	}
}

func TestResolveCallerNoConfigErrs(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	if _, err := ResolveCaller(repo, "alice"); err == nil {
		t.Fatal("expected error")
	}
}

// --- claim -------------------------------------------------------------

func TestClaimUnclaimedCallsBd(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeJSONL(t, repo, []map[string]any{{"id": "x.1", "status": "open"}})
	runner, calls := fakeBd(t, repo)
	res, err := Claim(repo, "x.1", "alice", runner)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if !res.Success || res.AlreadyHeld {
		t.Fatalf("res=%+v", res)
	}
	if len(*calls) != 1 {
		t.Fatalf("expected one bd call; got %v", *calls)
	}
	args := (*calls)[0]
	if args[1] != "update" || args[2] != "x.1" {
		t.Fatalf("args=%v", args)
	}
}

func TestClaimSelfIdempotentNoCall(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeJSONL(t, repo, []map[string]any{
		{"id": "x.1", "status": "in_progress", "assignee": "alice"},
	})
	runner, calls := fakeBd(t, repo)
	res, err := Claim(repo, "x.1", "alice", runner)
	if err != nil || !res.AlreadyHeld {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(*calls) != 0 {
		t.Fatalf("expected no bd call; got %v", *calls)
	}
}

func TestClaimByOtherIsConflict(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeJSONL(t, repo, []map[string]any{
		{"id": "x.1", "status": "in_progress", "assignee": "bob"},
	})
	runner, calls := fakeBd(t, repo)
	_, err := Claim(repo, "x.1", "alice", runner)
	var c *LeaseConflict
	if !errors.As(err, &c) {
		t.Fatalf("expected LeaseConflict; got %v", err)
	}
	if !strings.Contains(err.Error(), "bob") {
		t.Fatalf("err=%v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("expected no bd call; got %v", *calls)
	}
}

func TestClaimUnknownIssueErrs(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeJSONL(t, repo, []map[string]any{{"id": "x.1", "status": "open"}})
	runner, _ := fakeBd(t, repo)
	if _, err := Claim(repo, "missing.1", "alice", runner); err == nil {
		t.Fatal("expected error")
	}
}

func TestClaimRefusesLiveDaemon(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeJSONL(t, repo, []map[string]any{{"id": "x.1", "status": "open"}})
	pidFile := filepath.Join(repo, ".beads", "daemon.pid")
	_ = os.WriteFile(pidFile, []byte(strconv.Itoa(syscall.Getpid())), 0o644)
	runner, _ := fakeBd(t, repo)
	_, err := Claim(repo, "x.1", "alice", runner)
	if err == nil || !strings.Contains(err.Error(), "daemon") {
		t.Fatalf("expected daemon refusal; got %v", err)
	}
}

// --- release -----------------------------------------------------------

func TestReleaseByHolder(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeJSONL(t, repo, []map[string]any{
		{"id": "x.1", "status": "in_progress", "assignee": "alice"},
	})
	runner, calls := fakeBd(t, repo)
	res, err := Release(repo, "x.1", "alice", runner)
	if err != nil || !res.Success {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls=%v", *calls)
	}
	args := (*calls)[0]
	idx := -1
	for i, a := range args {
		if a == "--assignee" {
			idx = i
		}
	}
	if idx < 0 || args[idx+1] != "" {
		t.Fatalf("assignee not blanked: %v", args)
	}
}

func TestReleaseByNonHolderConflict(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeJSONL(t, repo, []map[string]any{
		{"id": "x.1", "status": "in_progress", "assignee": "bob"},
	})
	runner, _ := fakeBd(t, repo)
	_, err := Release(repo, "x.1", "alice", runner)
	var c *LeaseConflict
	if !errors.As(err, &c) {
		t.Fatalf("expected LeaseConflict; got %v", err)
	}
}

func TestReleaseUnclaimedIsNoop(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeJSONL(t, repo, []map[string]any{{"id": "x.1", "status": "open"}})
	runner, calls := fakeBd(t, repo)
	res, err := Release(repo, "x.1", "alice", runner)
	if err != nil || !res.AlreadyReleased {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(*calls) != 0 {
		t.Fatalf("expected no bd call; got %v", *calls)
	}
}

func TestReleaseRefusesLiveDaemon(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeJSONL(t, repo, []map[string]any{
		{"id": "x.1", "status": "in_progress", "assignee": "alice"},
	})
	pidFile := filepath.Join(repo, ".beads", "daemon.pid")
	_ = os.WriteFile(pidFile, []byte(strconv.Itoa(syscall.Getpid())), 0o644)
	runner, _ := fakeBd(t, repo)
	_, err := Release(repo, "x.1", "alice", runner)
	if err == nil || !strings.Contains(err.Error(), "daemon") {
		t.Fatalf("expected daemon refusal; got %v", err)
	}
}

// --- list / stale ------------------------------------------------------

func TestListReturnsActive(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeJSONL(t, repo, []map[string]any{
		{"id": "x.1", "status": "in_progress", "assignee": "alice", "updated_at": "2026-05-29T10:00:00+00:00"},
		{"id": "x.2", "status": "open"},
		{"id": "x.3", "status": "in_progress", "assignee": "bob", "updated_at": "2026-05-20T10:00:00+00:00"},
	})
	r := ListLeases([]string{repo}, 4, DefaultStaleAfterSeconds)
	byID := map[string]Lease{}
	for _, l := range r.Leases {
		byID[l.IssueID] = l
	}
	if _, ok := byID["x.1"]; !ok {
		t.Fatalf("missing x.1: %+v", r.Leases)
	}
	if _, ok := byID["x.2"]; ok {
		t.Fatalf("x.2 should be skipped (not in_progress)")
	}
	if _, ok := byID["x.3"]; !ok {
		t.Fatalf("missing x.3")
	}
}

func TestListFlagsStale(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	veryOld := time.Now().Add(-10 * 24 * time.Hour).UTC().Format(time.RFC3339)
	fresh := time.Now().Add(-1 * time.Minute).UTC().Format(time.RFC3339)
	writeJSONL(t, repo, []map[string]any{
		{"id": "x.old", "status": "in_progress", "assignee": "alice", "updated_at": veryOld},
		{"id": "x.fresh", "status": "in_progress", "assignee": "alice", "updated_at": fresh},
	})
	r := ListLeases([]string{repo}, 4, 7*24*3600)
	stale := map[string]bool{}
	for _, l := range r.Leases {
		if l.IsStale {
			stale[l.IssueID] = true
		}
	}
	if !stale["x.old"] {
		t.Fatalf("x.old should be stale; report=%+v", r)
	}
	if stale["x.fresh"] {
		t.Fatalf("x.fresh should not be stale")
	}
}
