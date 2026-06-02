package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/theaichimera/beekeeper-go/internal/hooks"
)

// initRepoForHookCheck builds a synthetic project under tmp with
// `.beads/` and an initialized git working tree. The hook layer (the
// thing we're testing) requires `.git/hooks/` to exist, so we let
// `git init` create it.
func initRepoForHookCheck(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".beads", "issues.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	must := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	must("init", "-q", "-b", "main")
	must("config", "user.email", "t@t")
	must("config", "user.name", "t")
	must("add", ".beads/issues.jsonl")
	must("commit", "-q", "-m", "init")
	return dir
}

// TestDoctorBkHooksMissingFlagsYellow — bkg-6h7 acceptance: a fresh
// repo with no hooks installed must surface a YELLOW row pointing at
// `bk install-hooks`.
func TestDoctorBkHooksMissingFlagsYellow(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := initRepoForHookCheck(t)
	r := Run([]string{dir}, 4)
	row := findCheck(r.Projects[0].Checks, "bk-hooks")
	if row == nil {
		t.Fatalf("missing bk-hooks check; got %+v", r.Projects[0].Checks)
	}
	if row.Severity != YELLOW {
		t.Fatalf("severity=%s want YELLOW", row.Severity)
	}
	if !strings.Contains(row.Remediation, "bk install-hooks") {
		t.Fatalf("remediation should mention `bk install-hooks`: %q", row.Remediation)
	}
	for _, want := range []string{"pre-push", "pre-commit"} {
		if !strings.Contains(row.Message, want) {
			t.Fatalf("message should list %q as missing: %q", want, row.Message)
		}
	}
}

// TestDoctorBkHooksOnlyBdFlushFlagsMissing — explicit acceptance from
// bkg-6h7: a repo whose only pre-commit is bd's flush-only hook must
// STILL be flagged as missing the bk drift guard.
func TestDoctorBkHooksOnlyBdFlushFlagsMissing(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := initRepoForHookCheck(t)
	hookPath := filepath.Join(dir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\nexec bd sync --flush-only\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := Run([]string{dir}, 4)
	row := findCheck(r.Projects[0].Checks, "bk-hooks")
	if row == nil || row.Severity != YELLOW {
		t.Fatalf("bd-flush-only repo should still flag missing bk hooks; got %+v", row)
	}
	if !strings.Contains(row.Message, "pre-commit") {
		t.Fatalf("message should still call out pre-commit when only bd flush is present: %q", row.Message)
	}
}

// TestDoctorBkHooksPresentSilent — when both bk hooks ARE installed,
// the check emits no row at all (parity with other doctor checks
// that stay quiet on the GREEN path).
func TestDoctorBkHooksPresentSilent(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := initRepoForHookCheck(t)
	if r := hooks.InstallPrePush(dir, false, false); !r.Written {
		t.Fatalf("pre-push install: %+v", r)
	}
	if r := hooks.InstallPreCommit(dir, false); !r.Written {
		t.Fatalf("pre-commit install: %+v", r)
	}
	r := Run([]string{dir}, 4)
	if row := findCheck(r.Projects[0].Checks, "bk-hooks"); row != nil {
		t.Fatalf("bk-hooks should be silent when both installed; got %+v", row)
	}
}

// TestDoctorBkHooksOnlyPrePushFlagsPreCommitMissing — partial-install
// case: pre-push is bk's, pre-commit is missing entirely → flag
// pre-commit only.
func TestDoctorBkHooksOnlyPrePushFlagsPreCommitMissing(t *testing.T) {
	forceConfigJSONFallback(t)
	dir := initRepoForHookCheck(t)
	if r := hooks.InstallPrePush(dir, false, false); !r.Written {
		t.Fatalf("pre-push install: %+v", r)
	}
	r := Run([]string{dir}, 4)
	row := findCheck(r.Projects[0].Checks, "bk-hooks")
	if row == nil {
		t.Fatal("expected bk-hooks finding")
	}
	if strings.Contains(row.Message, "pre-push") {
		t.Fatalf("pre-push is installed; should NOT be in missing list: %q", row.Message)
	}
	if !strings.Contains(row.Message, "pre-commit") {
		t.Fatalf("pre-commit is missing; should be in list: %q", row.Message)
	}
}

func findCheck(checks []Check, name string) *Check {
	for i := range checks {
		if checks[i].Name == name {
			return &checks[i]
		}
	}
	return nil
}
