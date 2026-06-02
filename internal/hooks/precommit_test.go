package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const bdFlushOnlyHookBody = `#!/bin/sh
# bd-managed: pre-commit (flush-only)
exec bd sync --flush-only
`

// TestInstallPreCommitOnEmptyRepo: clean install when no pre-commit
// exists. Hook is executable, contains marker + chain-exec line.
func TestInstallPreCommitOnEmptyRepo(t *testing.T) {
	t.Parallel()
	repo := initRepo(t)
	r := InstallPreCommit(repo, false)
	if !r.Written {
		t.Fatalf("install failed: %+v", r)
	}
	info, err := os.Stat(r.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("not executable: %v", info.Mode())
	}
	body, _ := os.ReadFile(r.Path)
	if !strings.Contains(string(body), PreCommitMarker) {
		t.Fatalf("marker missing: %s", body)
	}
	if !strings.Contains(string(body), "pre-commit.bk-chained") {
		t.Fatalf("chain-exec line missing: %s", body)
	}
	// Template renders as `$BK doctor "$REPO_DIR" $GATE_FLAGS` with
	// `BK=bk` set earlier — assert the structural pieces, not a
	// literal `bk doctor` substring (the binary name is via shell var).
	if !strings.Contains(string(body), "$BK doctor") {
		t.Fatalf("drift-gate doctor invocation missing: %s", body)
	}
	if !strings.Contains(string(body), "--gate") {
		t.Fatalf("drift-gate --gate flag missing: %s", body)
	}
}

// TestInstallPreCommitChainsExistingFlushHook — bkg-59b acceptance:
// bd's flush-only pre-commit must NOT be replaced. Install moves it
// to `pre-commit.bk-chained` and the bk wrapper exec's it.
func TestInstallPreCommitChainsExistingFlushHook(t *testing.T) {
	t.Parallel()
	repo := initRepo(t)
	hookPath := filepath.Join(repo, ".git", "hooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hookPath, []byte(bdFlushOnlyHookBody), 0o755); err != nil {
		t.Fatal(err)
	}

	r := InstallPreCommit(repo, false)
	if !r.Written {
		t.Fatalf("install with chain failed: %+v", r)
	}

	chained := filepath.Join(repo, ".git", "hooks", PreCommitChainedSuffix)
	chainedBody, err := os.ReadFile(chained)
	if err != nil {
		t.Fatalf("chained file missing: %v", err)
	}
	if !strings.Contains(string(chainedBody), "bd sync --flush-only") {
		t.Fatalf("chained content lost the bd flush command: %s", chainedBody)
	}
	chainedInfo, _ := os.Stat(chained)
	if chainedInfo.Mode()&0o111 == 0 {
		t.Fatalf("chained file lost exec bit: %v", chainedInfo.Mode())
	}
	wrapperBody, _ := os.ReadFile(hookPath)
	if !strings.Contains(string(wrapperBody), PreCommitMarker) {
		t.Fatalf("wrapper missing marker: %s", wrapperBody)
	}
	// The wrapper must reference the chained-suffix path so the
	// shell exec line resolves at hook time.
	if !strings.Contains(string(wrapperBody), PreCommitChainedSuffix) {
		t.Fatalf("wrapper missing chained-suffix reference: %s", wrapperBody)
	}
}

// TestInstallPreCommitIdempotent: re-installing rewrites the wrapper
// in place WITHOUT re-chaining (the existing pre-commit IS our
// wrapper, not a foreign hook).
func TestInstallPreCommitIdempotent(t *testing.T) {
	t.Parallel()
	repo := initRepo(t)
	if r := InstallPreCommit(repo, false); !r.Written {
		t.Fatalf("first install: %+v", r)
	}
	// No chained file should have been created the first time.
	chained := filepath.Join(repo, ".git", "hooks", PreCommitChainedSuffix)
	if _, err := os.Stat(chained); err == nil {
		t.Fatalf("first install incorrectly created chained file")
	}
	// Re-install: must succeed AND must not create a chained file
	// (would re-chain our own wrapper, which is the bug we're guarding).
	if r := InstallPreCommit(repo, false); !r.Written {
		t.Fatalf("re-install: %+v", r)
	}
	if _, err := os.Stat(chained); err == nil {
		t.Fatalf("re-install created chained file from our own wrapper")
	}
}

// TestInstallPreCommitRefusesForeignHookWhenChainedExists: don't
// clobber a chained file that may already hold a real hook. With
// --force, allow overwrite.
func TestInstallPreCommitRefusesForeignHookWhenChainedExists(t *testing.T) {
	t.Parallel()
	repo := initRepo(t)
	hookPath := filepath.Join(repo, ".git", "hooks", "pre-commit")
	chained := filepath.Join(repo, ".git", "hooks", PreCommitChainedSuffix)
	_ = os.MkdirAll(filepath.Dir(hookPath), 0o755)
	_ = os.WriteFile(hookPath, []byte(bdFlushOnlyHookBody), 0o755)
	_ = os.WriteFile(chained, []byte("#!/bin/sh\n# old chained\n"), 0o755)

	r := InstallPreCommit(repo, false)
	if r.Written {
		t.Fatalf("expected refusal; got %+v", r)
	}
	if !strings.Contains(r.SkippedReason, "force") {
		t.Fatalf("skip reason should mention --force: %q", r.SkippedReason)
	}

	// --force overwrites the chained file with the current hook.
	r = InstallPreCommit(repo, true)
	if !r.Written {
		t.Fatalf("force install: %+v", r)
	}
}

// TestUninstallPreCommitRestoresChained: removing the bk wrapper must
// move the chained predecessor back to pre-commit so bd's flush hook
// keeps firing.
func TestUninstallPreCommitRestoresChained(t *testing.T) {
	t.Parallel()
	repo := initRepo(t)
	hookPath := filepath.Join(repo, ".git", "hooks", "pre-commit")
	_ = os.MkdirAll(filepath.Dir(hookPath), 0o755)
	_ = os.WriteFile(hookPath, []byte(bdFlushOnlyHookBody), 0o755)

	if r := InstallPreCommit(repo, false); !r.Written {
		t.Fatalf("install: %+v", r)
	}
	if r := UninstallPreCommit(repo); !r.Written {
		t.Fatalf("uninstall: %+v", r)
	}
	body, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("pre-commit missing after uninstall: %v", err)
	}
	if !strings.Contains(string(body), "bd sync --flush-only") {
		t.Fatalf("chained predecessor not restored: %s", body)
	}
	chained := filepath.Join(repo, ".git", "hooks", PreCommitChainedSuffix)
	if _, err := os.Stat(chained); err == nil {
		t.Fatalf("chained file should be removed after restore")
	}
}

// TestUninstallPreCommitNoChainedDoesNothingExtra: when no chained
// file exists, uninstall just removes the bk wrapper.
func TestUninstallPreCommitNoChainedDoesNothingExtra(t *testing.T) {
	t.Parallel()
	repo := initRepo(t)
	if r := InstallPreCommit(repo, false); !r.Written {
		t.Fatalf("install: %+v", r)
	}
	if r := UninstallPreCommit(repo); !r.Written {
		t.Fatalf("uninstall: %+v", r)
	}
	hookPath := filepath.Join(repo, ".git", "hooks", "pre-commit")
	if _, err := os.Stat(hookPath); err == nil {
		t.Fatalf("pre-commit still present after uninstall")
	}
}

// TestUninstallPreCommitSkipsForeignHook: don't remove a non-bk
// pre-commit even via uninstall — that's a "marker-bearing only"
// contract, same as pre-push.
func TestUninstallPreCommitSkipsForeignHook(t *testing.T) {
	t.Parallel()
	repo := initRepo(t)
	hookPath := filepath.Join(repo, ".git", "hooks", "pre-commit")
	_ = os.MkdirAll(filepath.Dir(hookPath), 0o755)
	_ = os.WriteFile(hookPath, []byte(bdFlushOnlyHookBody), 0o755)
	r := UninstallPreCommit(repo)
	if r.Written {
		t.Fatalf("must not remove foreign hook; got %+v", r)
	}
}

// TestRenderPreCommitHasMarker: the rendered body always carries the
// marker line — doctor (bkg-6h7) keys on it for hook-presence
// detection.
func TestRenderPreCommitHasMarker(t *testing.T) {
	t.Parallel()
	body := RenderPreCommit()
	if !strings.Contains(body, PreCommitMarker) {
		t.Fatalf("rendered body missing marker:\n%s", body)
	}
}
