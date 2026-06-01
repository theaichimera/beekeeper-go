//go:build windows

package proc

import "syscall"

// stillActive is the exit code Windows reports for a process that has
// not yet terminated (STILL_ACTIVE / STATUS_PENDING).
const stillActive = 259

// pidAlive reports whether `pid` is a live process. It opens a query
// handle and inspects the exit code. Access-denied means the process
// exists but is owned by another principal -> alive (mirrors the EPERM
// branch of the Unix implementation).
func pidAlive(pid int) bool {
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		if err == syscall.ERROR_ACCESS_DENIED {
			return true
		}
		return false
	}
	defer syscall.CloseHandle(h)

	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		// Handle opened but exit code unreadable: treat as alive.
		return true
	}
	return code == stillActive
}

// defaultCwdLookup is unsupported on Windows (no /proc, no lsof). The
// parser falls back to the --workspace flag and the cmdline path token,
// so `bk guard daemon` degrades gracefully rather than failing to build.
func defaultCwdLookup(pid int) string {
	return ""
}
