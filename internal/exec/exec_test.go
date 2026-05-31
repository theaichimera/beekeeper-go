package execwrap

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRunSucceeds(t *testing.T) {
	t.Parallel()
	rc, stdout, stderr, err := Run([]string{"true"}, "", time.Second)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d stdout=%q stderr=%q", rc, stdout, stderr)
	}
}

func TestRunCapturesStdout(t *testing.T) {
	t.Parallel()
	rc, stdout, _, err := Run([]string{"echo", "hello"}, "", time.Second)
	if err != nil || rc != 0 {
		t.Fatalf("echo failed: rc=%d err=%v", rc, err)
	}
	if strings.TrimSpace(stdout) != "hello" {
		t.Fatalf("stdout=%q", stdout)
	}
}

func TestRunNonZeroExit(t *testing.T) {
	t.Parallel()
	rc, _, _, err := Run([]string{"false"}, "", time.Second)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if rc == 0 {
		t.Fatalf("expected nonzero rc")
	}
}

func TestRunNotFoundReturns127(t *testing.T) {
	t.Parallel()
	rc, _, _, err := Run([]string{"definitely-not-a-real-binary-xyzzy"}, "", time.Second)
	if err != nil {
		t.Fatalf("Run should not error on missing binary; got %v", err)
	}
	if rc != 127 {
		t.Fatalf("expected rc=127, got %d", rc)
	}
}

func TestRunRespectsTimeout(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("sleep not available")
	}
	start := time.Now()
	rc, _, _, err := Run([]string{"sleep", "5"}, "", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if rc != 124 {
		t.Fatalf("expected timeout rc=124, got %d", rc)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Run did not honor timeout: elapsed=%v", elapsed)
	}
}

func TestRunRespectsCwd(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	rc, stdout, _, err := Run([]string{"pwd"}, dir, time.Second)
	if err != nil || rc != 0 {
		t.Fatalf("pwd failed: rc=%d err=%v", rc, err)
	}
	gotRaw := strings.TrimSpace(stdout)
	got, err := filepath.EvalSymlinks(gotRaw)
	if err != nil {
		got = gotRaw
	}
	if got != want {
		t.Fatalf("pwd=%q want %q", got, want)
	}
}

func TestRunEmptyArgsErrors(t *testing.T) {
	t.Parallel()
	rc, _, _, err := Run(nil, "", time.Second)
	if err == nil {
		t.Fatal("expected error on empty args")
	}
	if rc == 0 {
		t.Fatal("expected nonzero rc on empty args")
	}
}

func TestDefaultIsExported(t *testing.T) {
	t.Parallel()
	// Quick sanity that the injection seam works.
	called := false
	orig := Default
	Default = func(args []string, cwd string, timeout time.Duration) (int, string, string, error) {
		called = true
		return 42, "fake", "", nil
	}
	t.Cleanup(func() { Default = orig })
	rc, stdout, _, _ := Default([]string{"x"}, "", 0)
	if !called || rc != 42 || stdout != "fake" {
		t.Fatalf("injection failed: called=%v rc=%d stdout=%q", called, rc, stdout)
	}
}

// Helper used by the proc test fixtures — keep here so it lives where
// the injection seam is defined.
var _ = os.Environ
