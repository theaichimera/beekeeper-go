//go:build darwin || linux

package proc

import "syscall"

// pidAlive returns true iff signal-0 to `pid` does not raise ESRCH.
// EPERM means the process exists but belongs to another user -> alive.
// Mirrors `_pid_alive` in project.py.
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	if err == syscall.EPERM {
		return true
	}
	// ESRCH or anything else => not alive (or indistinguishable).
	return false
}
