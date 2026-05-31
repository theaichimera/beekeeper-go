// Tests that close the parity gap with Python's doctor.py wiring
// suite — checks that the doctor composer pulls in trunk-sync, lease,
// identity, and syncbranch findings as Check rows correctly.
package doctor

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	execwrap "github.com/theaichimera/beekeeper-go/internal/exec"
	bkproject "github.com/theaichimera/beekeeper-go/internal/project"
)

func writeIdentityTOML(t *testing.T, repo string, canonical []string, aliases map[string]string) {
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

func writeJSONLRecords(t *testing.T, repo string, records []map[string]any) {
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

// Mirrors Python test_doctor_yellow_on_identity_drift.
func TestDoctorYellowOnIdentityDrift(t *testing.T) {
	forceConfigJSONFallback(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	writeIdentityTOML(t, dir, []string{"alice"}, map[string]string{"a@x": "alice"})
	writeJSONLRecords(t, dir, []map[string]any{
		{"id": "x.1", "assignee": "a@x"},
		{"id": "x.2", "assignee": "stranger"},
	})
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		_ = c.Run()
	}
	r := Run([]string{dir}, 4)
	ph := r.Projects[0]
	rows := []Check{}
	for _, c := range ph.Checks {
		if c.Name == "identity" {
			rows = append(rows, c)
		}
	}
	yellow := false
	for _, c := range rows {
		if c.Severity == YELLOW {
			yellow = true
		}
	}
	if !yellow {
		t.Fatalf("expected YELLOW identity row; got %+v", rows)
	}
}

// Mirrors test_doctor_no_identity_check_without_config.
func TestDoctorNoIdentityCheckWithoutConfig(t *testing.T) {
	forceConfigJSONFallback(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	writeJSONLRecords(t, dir, []map[string]any{{"id": "x.1", "assignee": "anyone"}})
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		_ = c.Run()
	}
	r := Run([]string{dir}, 4)
	ph := r.Projects[0]
	for _, c := range ph.Checks {
		if c.Name == "identity" {
			t.Fatalf("identity row leaked when no config: %+v", c)
		}
	}
}

// Mirrors test_doctor_yellow_on_stale_leases.
func TestDoctorYellowOnStaleLeases(t *testing.T) {
	forceConfigJSONFallback(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	veryOld := time.Now().Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	writeJSONLRecords(t, dir, []map[string]any{
		{"id": "x.old", "status": "in_progress", "assignee": "alice", "updated_at": veryOld},
	})
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		_ = c.Run()
	}
	r := Run([]string{dir}, 4)
	ph := r.Projects[0]
	yellow := false
	for _, c := range ph.Checks {
		if c.Name == "lease" && c.Severity == YELLOW {
			yellow = true
		}
	}
	if !yellow {
		t.Fatalf("expected YELLOW lease row; got %+v", ph.Checks)
	}
}

// Mirrors test_doctor_no_lease_row_when_no_active_leases.
func TestDoctorNoLeaseRowWhenNoActiveLeases(t *testing.T) {
	forceConfigJSONFallback(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	writeJSONLRecords(t, dir, []map[string]any{{"id": "x.1", "status": "open"}})
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		_ = c.Run()
	}
	r := Run([]string{dir}, 4)
	ph := r.Projects[0]
	for _, c := range ph.Checks {
		if c.Name == "lease" {
			t.Fatalf("lease row should be silent; got %+v", c)
		}
	}
}

// Mirrors test_dead_pid_red_even_when_bd_self_heals_pid_file.
// Simulate the race by mutating bdRunner to delete the pid file
// during `bd config get`; verify doctor still sees the stale pid
// because daemon state is read FIRST (M2 fix).
func TestDeadPIDRedEvenWhenBdSelfHealsPidFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, ".beads"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, ".beads", "issues.jsonl"), nil, 0o644))
	must(t, os.WriteFile(filepath.Join(dir, ".beads", "config.json"), []byte(`{"sync":{"branch":"beads-sync"}}`), 0o644))
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
		{"add", ".beads/issues.jsonl"},
		{"commit", "-q", "-m", "init"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	pid := filepath.Join(dir, ".beads", "daemon.pid")
	must(t, os.WriteFile(pid, []byte("999999\n"), 0o644))

	// Replace BdRunner: when called, delete the pid file (mid-flight
	// race simulation) before returning.
	orig := bkproject.BdRunner
	bkproject.BdRunner = func(args []string, cwd string, _ time.Duration) (int, string, string, error) {
		if len(args) >= 3 && args[0] == "bd" && args[1] == "config" && args[2] == "get" {
			_ = os.Remove(pid)
			return 1, "", "(forced fallback after delete)", nil
		}
		return execwrap.Default(args, cwd, 10*time.Second)
	}
	defer func() { bkproject.BdRunner = orig }()

	r := Run([]string{dir}, 4)
	ph := r.Projects[0]
	red := false
	for _, c := range ph.Checks {
		if c.Name == "daemon" && c.Severity == RED && strings.Contains(c.Message, "999999") {
			red = true
		}
	}
	if !red {
		t.Fatalf("expected RED stale-pid daemon row; got %+v", ph.Checks)
	}
}

// Mirrors test_needs_manual_sync_is_red (with pid alive — variant of
// the without-pid case already in M2).
func TestNeedsManualSyncWithAlivePIDAlsoRed(t *testing.T) {
	forceConfigJSONFallback(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	initGitRepo(t, dir, "beads-sync")
	must(t, os.WriteFile(filepath.Join(dir, ".beads", "daemon.pid"), []byte(itoa(os.Getpid())), 0o644))
	b, _ := json.Marshal(map[string]any{"needs_manual_sync": true})
	must(t, os.WriteFile(filepath.Join(dir, ".beads", "sync-state.json"), b, 0o644))
	r := Run([]string{dir}, 4)
	if !hasCheck(r.Projects[0], "sync-state", RED) {
		t.Fatalf("expected sync-state RED; got %+v", r.Projects[0])
	}
}

// Mirrors test_last_sync_age_over_24h_is_yellow.
func TestLastSyncAgeOver24hYellow(t *testing.T) {
	forceConfigJSONFallback(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	initGitRepo(t, dir, "beads-sync")
	old := time.Now().Add(-25 * time.Hour).Unix()
	b, _ := json.Marshal(map[string]any{"last_sync_at": old})
	must(t, os.WriteFile(filepath.Join(dir, ".beads", "sync-state.json"), b, 0o644))
	r := Run([]string{dir}, 4)
	if !hasCheck(r.Projects[0], "sync-state", YELLOW) {
		t.Fatalf("expected sync-state YELLOW; got %+v", r.Projects[0])
	}
}

// Mirrors test_bd_config_not_set_string_is_yellow.
func TestBdConfigNotSetStringIsYellow(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, ".beads"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, ".beads", "issues.jsonl"), nil, 0o644))
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		_ = c.Run()
	}
	// Simulate bd printing the "(not set)" sentinel.
	orig := bkproject.BdRunner
	bkproject.BdRunner = func(args []string, cwd string, _ time.Duration) (int, string, string, error) {
		if len(args) >= 3 && args[0] == "bd" && args[1] == "config" && args[2] == "get" {
			return 0, "sync.branch (not set in config.yaml)\n", "", nil
		}
		return execwrap.Default(args, cwd, 10*time.Second)
	}
	defer func() { bkproject.BdRunner = orig }()

	r := Run([]string{dir}, 4)
	if !hasCheck(r.Projects[0], "sync-branch", YELLOW) {
		t.Fatalf("expected sync-branch YELLOW; got %+v", r.Projects[0])
	}
}

// Mirrors test_sync_branch_set_is_green.
func TestSyncBranchSetIsGreen(t *testing.T) {
	forceConfigJSONFallback(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	initGitRepo(t, dir, "beads-sync")
	r := Run([]string{dir}, 4)
	if !hasCheck(r.Projects[0], "sync-branch", GREEN) {
		t.Fatalf("expected sync-branch GREEN; got %+v", r.Projects[0])
	}
}

// Tiny strconv-free itoa to avoid dragging in another import here.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
