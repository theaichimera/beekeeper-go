package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init", "-q", "-b", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return dir
}

func TestInstallWritesExecutableHook(t *testing.T) {
	t.Parallel()
	repo := initRepo(t)
	r := InstallPrePush(repo, false, false)
	if !r.Written {
		t.Fatalf("not written: %+v", r)
	}
	info, err := os.Stat(r.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("not executable: mode=%v", info.Mode())
	}
	body, _ := os.ReadFile(r.Path)
	if !strings.Contains(string(body), Marker) {
		t.Fatalf("marker missing: %s", body)
	}
	if !strings.Contains(string(body), "bk doctor") {
		t.Fatalf("invocation missing: %s", body)
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	t.Parallel()
	repo := initRepo(t)
	if r := InstallPrePush(repo, false, false); !r.Written {
		t.Fatalf("first: %+v", r)
	}
	r := InstallPrePush(repo, false, false)
	if !r.Written {
		t.Fatalf("re-install must overwrite: %+v", r)
	}
}

func TestInstallRefusesForeignHook(t *testing.T) {
	t.Parallel()
	repo := initRepo(t)
	hook := filepath.Join(repo, ".git", "hooks", "pre-push")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/usr/bin/env bash\n# not ours\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := InstallPrePush(repo, false, false)
	if r.Written || !strings.Contains(r.SkippedReason, "non-beadkeeper") {
		t.Fatalf("expected refusal; got %+v", r)
	}
	// --force overrides.
	r = InstallPrePush(repo, false, true)
	if !r.Written {
		t.Fatalf("force should overwrite; got %+v", r)
	}
}

func TestUninstallOnlyRemovesOurHook(t *testing.T) {
	t.Parallel()
	repo := initRepo(t)
	hook := filepath.Join(repo, ".git", "hooks", "pre-push")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	// Foreign hook: uninstall is a no-op.
	if err := os.WriteFile(hook, []byte("#!/usr/bin/env bash\necho keep\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := UninstallPrePush(repo)
	if r.Written {
		t.Fatalf("must not remove foreign hook; got %+v", r)
	}
	if _, err := os.Stat(hook); err != nil {
		t.Fatalf("hook removed: %v", err)
	}
	// Install ours, then uninstall.
	_ = InstallPrePush(repo, false, true)
	r = UninstallPrePush(repo)
	if !r.Written {
		t.Fatalf("expected removal; got %+v", r)
	}
	if _, err := os.Stat(hook); err == nil {
		t.Fatalf("hook still present")
	}
}

func TestRenderPromptIndicator(t *testing.T) {
	t.Parallel()
	body := RenderPromptIndicator()
	if !strings.Contains(body, "beadkeeper_prompt()") {
		t.Fatalf("missing function: %s", body)
	}
	if !strings.Contains(body, Marker) {
		t.Fatalf("missing marker")
	}
}

func TestRenderPrePushIncludesPRBeadsPolicyPlumbing(t *testing.T) {
	t.Parallel()
	body := RenderPrePush(false)
	for _, want := range []string{
		"BEADKEEPER_PRBEADS_POLICY",
		"guard pr-beads",
		`PRBEADS_POLICY="${BEADKEEPER_PRBEADS_POLICY:-off}"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in pre-push template:\n%s", want, body)
		}
	}
}

func TestRenderPrePushDefaultsPRBeadsOff(t *testing.T) {
	t.Parallel()
	body := RenderPrePush(false)
	// The OFF default is critical: an unconfigured installation must
	// NOT start running pr-beads behind the user's back. The plumbing
	// is only inert until the user opts in via env.
	if !strings.Contains(body, `if [ "$PRBEADS_POLICY" != "off" ]; then`) {
		t.Fatalf("default-off branch missing:\n%s", body)
	}
}

func TestInstallOnNonGitDir(t *testing.T) {
	t.Parallel()
	bare := filepath.Join(t.TempDir(), "not-a-repo")
	_ = os.MkdirAll(bare, 0o755)
	r := InstallPrePush(bare, false, false)
	if r.Written || !strings.Contains(strings.ToLower(r.SkippedReason), "git") {
		t.Fatalf("expected non-git refusal; got %+v", r)
	}
}
