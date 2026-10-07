//go:build unix

package provider

import "syscall"

// processAlive reports whether pid still exists. The killed child was reaped
// by abortStartedProcess, so signal 0 fails with ESRCH once it is gone.
func processAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }
