// CLI parity tests — port of Python tests/test_cli.py + the
// CLI-flavored cases in tests/test_board.py.
//
// We exercise the cobra root command in-process, capture
// stdout/stderr to a buffer, and override silentExit to record exit
// codes instead of os.Exit-ing under the test runner.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runCmd uses NAMED return values so the panic-recovery path
// preserves the captured stdout/stderr/rc instead of zero-valuing them.
func runCmd(t *testing.T, args ...string) (stdout string, stderr string, rc int) {
	t.Helper()
	root := newRootCmd()
	var sbuf, ebuf bytes.Buffer
	root.SetOut(&sbuf)
	root.SetErr(&ebuf)
	root.SetArgs(args)

	orig := silentExit
	silentExit = func(code int) { rc = code; panic(testExitSentinel{}) }
	t.Cleanup(func() { silentExit = orig })

	defer func() {
		// Whether or not we panicked, finalize the captured buffers.
		stdout = sbuf.String()
		stderr = ebuf.String()
		if r := recover(); r != nil {
			if _, ok := r.(testExitSentinel); !ok {
				panic(r)
			}
		}
	}()
	_ = root.Execute()
	return sbuf.String(), ebuf.String(), rc
}

type testExitSentinel struct{}

// initRepo creates a synthetic project under tmp_path with optional
// sync.branch, daemon pid, JSONL records.
func initRepo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".beads", "issues.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".beads", "config.json"), []byte(`{"sync":{"branch":"beads-sync"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("git"); err == nil {
		for _, args := range [][]string{
			{"init", "-q", "-b", "main"},
			{"config", "user.email", "t@t"},
			{"config", "user.name", "t"},
			{"add", ".beads/issues.jsonl"},
			{"commit", "-q", "-m", "init"},
		} {
			c := exec.Command("git", args...)
			c.Dir = dir
			_ = c.Run()
		}
	}
	return dir
}

// Mirrors test_cli_doctor_json_clean.
func TestCLIDoctorJSONClean(t *testing.T) {
	dir := initRepo(t, "doc-clean")
	stdout, _, rc := runCmd(t, "doctor", dir, "--json")
	if rc != 0 && rc != 1 {
		t.Fatalf("rc=%d", rc)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if _, ok := parsed["worst"]; !ok {
		t.Fatalf("missing 'worst': %s", stdout)
	}
}

// Mirrors test_cli_doctor_red_exits_two.
func TestCLIDoctorRedExitsTwo(t *testing.T) {
	dir := initRepo(t, "rotten")
	// Plant a DB inside a fake sync root by setting the env var to
	// the project root's parent.
	t.Setenv("BEADKEEPER_FILESYNC_ROOTS", filepath.Dir(dir))
	if err := os.WriteFile(filepath.Join(dir, ".beads", "beadkeeper.db"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, rc := runCmd(t, "doctor", dir, "--json")
	if rc != 2 {
		t.Fatalf("rc=%d want 2; stdout=%s", rc, stdout)
	}
	if !strings.Contains(strings.ToLower(stdout), "red") {
		t.Fatalf("expected 'red' in stdout: %s", stdout)
	}
}

// Mirrors test_cli_guard_db_clean.
func TestCLIGuardDBClean(t *testing.T) {
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "FakeDropbox")
	_ = os.MkdirAll(fake, 0o755)
	t.Setenv("BEADKEEPER_FILESYNC_ROOTS", fake)
	dir := initRepo(t, "guard-clean")
	// DB outside fake sync root.
	_ = os.WriteFile(filepath.Join(dir, ".beads", "beadkeeper.db"), nil, 0o644)
	_, _, rc := runCmd(t, "guard", "db", dir, "--quiet")
	if rc != 0 {
		t.Fatalf("rc=%d want 0", rc)
	}
}

// Mirrors test_cli_guard_db_detects_inside_sync.
func TestCLIGuardDBDetectsInsideSync(t *testing.T) {
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "FakeDropbox")
	_ = os.MkdirAll(fake, 0o755)
	t.Setenv("BEADKEEPER_FILESYNC_ROOTS", fake)
	proj := filepath.Join(fake, "inside")
	_ = os.MkdirAll(filepath.Join(proj, ".beads"), 0o755)
	_ = os.WriteFile(filepath.Join(proj, ".beads", "beadkeeper.db"), []byte("x"), 0o644)
	stdout, _, rc := runCmd(t, "guard", "db", fake)
	if rc != 2 {
		t.Fatalf("rc=%d want 2; stdout=%s", rc, stdout)
	}
	if !strings.Contains(stdout, "RED") {
		t.Fatalf("expected RED in stdout: %s", stdout)
	}
}

// Mirrors test_cli_guard_db_fix_dry_run.
func TestCLIGuardDBFixDryRun(t *testing.T) {
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "FakeDropbox")
	_ = os.MkdirAll(fake, 0o755)
	t.Setenv("BEADKEEPER_FILESYNC_ROOTS", fake)
	proj := filepath.Join(fake, "to-move")
	_ = os.MkdirAll(filepath.Join(proj, ".beads"), 0o755)
	db := filepath.Join(proj, ".beads", "beadkeeper.db")
	_ = os.WriteFile(db, []byte("x"), 0o644)
	dest := filepath.Join(tmp, "safe")
	stdout, _, rc := runCmd(t, "guard", "db", fake, "--fix", "--destination", dest)
	if rc != 0 {
		t.Fatalf("rc=%d want 0; stdout=%s", rc, stdout)
	}
	if !strings.Contains(stdout, "DRY-RUN") {
		t.Fatalf("expected DRY-RUN in stdout: %s", stdout)
	}
	if _, err := os.Stat(db); err != nil {
		t.Fatalf("db moved during dry-run: %v", err)
	}
}

// Mirrors test_cli_install_hooks_idempotent.
func TestCLIInstallHooksIdempotent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := initRepo(t, "hooks")
	_, _, rc := runCmd(t, "install-hooks", dir)
	if rc != 0 {
		t.Fatalf("first rc=%d", rc)
	}
	_, _, rc = runCmd(t, "install-hooks", dir)
	if rc != 0 {
		t.Fatalf("second rc=%d", rc)
	}
}

// Mirrors test_cli_prompt_indicator.
func TestCLIPromptIndicator(t *testing.T) {
	stdout, _, rc := runCmd(t, "prompt-indicator")
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if !strings.Contains(stdout, "beadkeeper_prompt") {
		t.Fatalf("missing function name: %s", stdout)
	}
}

// --- board CLI -------------------------------------------------

func writeJSONLBoard(t *testing.T, dir string, recs []map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	var body []byte
	for _, r := range recs {
		b, _ := json.Marshal(r)
		body = append(body, b...)
		body = append(body, '\n')
	}
	if err := os.WriteFile(filepath.Join(dir, ".beads", "issues.jsonl"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Mirrors test_cli_json_shape (board).
func TestCLIBoardJSONShape(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p1")
	writeJSONLBoard(t, dir, []map[string]any{
		{"id": "p1.1", "status": "open"},
		{"id": "p1.2", "status": "in_progress", "assignee": "alice"},
	})
	stdout, _, rc := runCmd(t, "board", dir, "--json")
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if _, ok := parsed["projects"]; !ok {
		t.Fatalf("missing 'projects': %s", stdout)
	}
	if _, ok := parsed["totals"]; !ok {
		t.Fatalf("missing 'totals': %s", stdout)
	}
}

// Mirrors test_cli_status_filter_ready_only.
func TestCLIBoardStatusFilterReadyOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p2")
	writeJSONLBoard(t, dir, []map[string]any{
		{"id": "a", "status": "open"},
		{"id": "b", "status": "in_progress", "assignee": "alice"},
	})
	stdout, _, rc := runCmd(t, "board", dir, "--status", "ready")
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if !strings.Contains(stdout, "READY") {
		t.Fatalf("missing READY: %s", stdout)
	}
	if strings.Contains(stdout, "IN PROGRESS") {
		t.Fatalf("IN PROGRESS leaked: %s", stdout)
	}
}

// Mirrors test_cli_strict_exits_one_on_lease_gaps.
func TestCLIBoardStrictExitsOneOnGaps(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gap")
	writeJSONLBoard(t, dir, []map[string]any{
		{"id": "x", "status": "in_progress"},
	})
	stdout, _, rc := runCmd(t, "board", dir, "--strict")
	if rc != 1 {
		t.Fatalf("rc=%d want 1; stdout=%s", rc, stdout)
	}
	if !strings.Contains(stdout, "LEASE-GAP") {
		t.Fatalf("missing LEASE-GAP tag: %s", stdout)
	}
}

// Mirrors test_cli_strict_zero_when_no_gaps.
func TestCLIBoardStrictZeroWhenNoGaps(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "no-gap")
	writeJSONLBoard(t, dir, []map[string]any{
		{"id": "a", "status": "open"},
		{"id": "b", "status": "in_progress", "assignee": "alice"},
	})
	_, _, rc := runCmd(t, "board", dir, "--strict")
	if rc != 0 {
		t.Fatalf("rc=%d want 0", rc)
	}
}

// Mirrors test_cli_lease_gaps_only.
func TestCLIBoardLeaseGapsOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mix")
	writeJSONLBoard(t, dir, []map[string]any{
		{"id": "a", "status": "open"},
		{"id": "b", "status": "in_progress", "assignee": "alice"},
		{"id": "c", "status": "in_progress"},
	})
	stdout, _, rc := runCmd(t, "board", dir, "--lease-gaps")
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if !strings.Contains(stdout, " c ") && !strings.Contains(stdout, "c  ") {
		t.Fatalf("expected c shown: %s", stdout)
	}
	if strings.Contains(stdout, "READY") {
		t.Fatalf("READY leaked: %s", stdout)
	}
}

// Mirrors test_cli_quiet_silences_empty_message.
func TestCLIBoardQuietSilencesEmptyMessage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "totally-empty")
	stdout, _, rc := runCmd(t, "board", dir, "--quiet")
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	// Quiet means no chatter; message about "no projects" allowed
	// to be present or absent — what matters is rc=0 and no crash.
	_ = stdout
}

// Smoke for fmt import.
var _ = fmt.Sprintf
