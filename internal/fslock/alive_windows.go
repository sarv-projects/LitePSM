//go:build windows

package fslock

import "syscall"

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
	errorAccessDenied              = syscall.Errno(5)
)

// ProcessAlive reports whether a process with this pid is running. On Windows
// os.FindProcess succeeds for any pid, so liveness is decided by opening the
// process and reading its exit code: STILL_ACTIVE (259) means running. An
// access-denied open means the process exists (it is just not ours to query).
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return err == errorAccessDenied
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
