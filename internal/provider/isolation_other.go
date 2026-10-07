//go:build !unix && !windows

package provider

import (
	"os/exec"
	"time"
)

// setupProcessIsolation is a no-op on platforms outside the supported
// windows/darwin/linux set: no process-group or job-object isolation exists.
func setupProcessIsolation(cmd *exec.Cmd) {}

// postStartProcessIsolation is a no-op: there is no watchdog or job-object
// attachment available on this platform.
func postStartProcessIsolation(cmd *exec.Cmd, h *ProviderHandle) error {
	return nil
}

// killProcessTree terminates the child process directly. Without process
// groups, only the immediate child can be signalled.
func killProcessTree(h *ProviderHandle, gracePeriod time.Duration) error {
	if h.Cmd == nil || h.Cmd.Process == nil {
		return nil
	}

	_ = h.Cmd.Process.Kill()

	if h.waitDone == nil {
		time.Sleep(gracePeriod)
		return nil
	}

	select {
	case <-time.After(gracePeriod):
		_ = h.Cmd.Process.Kill()
		// Wait for the single reaper (monitorProcess) to observe the exit.
		<-h.waitDone
	case <-h.waitDone:
	}
	return nil
}

// killGroupNow is a no-op here: there is no process group to signal; the
// caller kills the direct child.
func killGroupNow(pid int) {}
