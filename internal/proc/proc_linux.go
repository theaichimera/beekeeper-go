//go:build linux

package proc

import (
	"os"
	"path/filepath"
	"strconv"
)

// defaultCwdLookup reads /proc/<pid>/cwd, which on Linux is a
// readable symlink to the process's current directory.
func defaultCwdLookup(pid int) string {
	if pid <= 0 {
		return ""
	}
	link := "/proc/" + strconv.Itoa(pid) + "/cwd"
	target, err := os.Readlink(link)
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		return resolved
	}
	return filepath.Clean(target)
}
