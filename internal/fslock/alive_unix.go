//go:build !windows

package fslock

import (
	"errors"
	"os"
	"syscall"
)

// ProcessAlive reports whether a process with this pid exists. EPERM means the
// process exists but belongs to another user, which still counts as alive.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}
