//go:build darwin

package proc

import (
	"path/filepath"
	"strconv"
	"strings"
	"time"

	execwrap "github.com/theaichimera/beekeeper-go/internal/exec"
)

// LsofRunner is the subprocess runner used for `lsof`. Exposed so
// tests can inject a fixture-driven fake.
var LsofRunner = execwrap.Default

// defaultCwdLookup invokes `lsof -a -p <pid> -d cwd -Fn` and parses
// the first line beginning with 'n' as the cwd. Mirrors Python's
// _process_cwd on macOS.
func defaultCwdLookup(pid int) string {
	if pid <= 0 {
		return ""
	}
	rc, stdout, _, _ := LsofRunner(
		[]string{"lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn"},
		"",
		5*time.Second,
	)
	if rc != 0 {
		return ""
	}
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "n") {
			path := line[1:]
			if resolved, err := filepath.EvalSymlinks(path); err == nil {
				return resolved
			}
			return filepath.Clean(path)
		}
	}
	return ""
}
