// Package exec is a thin wrapper over os/exec used by every other
// internal package that shells out to git, bd, ps, or lsof.
//
// The contract mirrors the Python `_run` helper in project.py:
//
//		rc, stdout, stderr, err := exec.Run(ctx, cwd, args, timeout)
//
//	  - rc == 0 on success.
//	  - rc == 127 when the binary cannot be found (Python's FileNotFoundError).
//	  - rc == 124 on timeout (Python's TimeoutExpired).
//	  - err is non-nil only when stdout/stderr could not be captured;
//	    callers should branch on rc, not err.
package execwrap

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os/exec"
	"time"
)

// Runner is the function shape every package depends on; tests inject
// fakes by swapping the package-level var.
type Runner func(args []string, cwd string, timeout time.Duration) (rc int, stdout string, stderr string, err error)

// Default is the production implementation. Override via Run.WithRunner
// (or, in package-level tests, by overriding `Default` directly).
var Default Runner = Run

// Run executes args[0] with args[1:] in cwd. It never panics. A zero
// timeout means no explicit cap (still subject to context cancellation).
func Run(args []string, cwd string, timeout time.Duration) (int, string, string, error) {
	if len(args) == 0 {
		return 1, "", "", errors.New("execwrap.Run: empty args")
	}
	ctx := context.Background()
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	if ctx.Err() == context.DeadlineExceeded {
		return 124, stdout.String(), stderr.String(), nil
	}
	if runErr != nil {
		// Translate exec.Error / fs.ErrNotExist into the not-found code
		// (Python's FileNotFoundError -> 127).
		var execErr *exec.Error
		if errors.As(runErr, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
			return 127, "", execErr.Error(), nil
		}
		if errors.Is(runErr, fs.ErrNotExist) {
			return 127, "", runErr.Error(), nil
		}
		// A non-zero exit shows up as *exec.ExitError; its ExitCode is
		// the process's exit code.
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return exitErr.ExitCode(), stdout.String(), stderr.String(), nil
		}
		return 1, stdout.String(), stderr.String(), runErr
	}
	return 0, stdout.String(), stderr.String(), nil
}
