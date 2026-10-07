//go:build unix

package provider

import (
	"os/exec"
	"syscall"
	"time"
)

func setupProcessIsolation(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}

	// Create a new process group so child and all its descendants can be signalled together
	cmd.SysProcAttr.Setpgid = true

	// On Linux, set parent death signal for kernel-guaranteed cleanup on daemon crash/SIGKILL
	setParentDeathSignal(cmd.SysProcAttr)
}

func postStartProcessIsolation(cmd *exec.Cmd, h *ProviderHandle) error {
	return startWatchdogIfRequired(h)
}

// killGroupNow SIGKILLs the child's whole process group immediately.
func killGroupNow(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}

func killProcessTree(h *ProviderHandle, gracePeriod time.Duration) error {
	if h.Cmd == nil || h.Cmd.Process == nil {
		return nil
	}

	// Signal the entire process group.
	_ = syscall.Kill(-h.PID, syscall.SIGTERM)

	if h.waitDone == nil {
		time.Sleep(gracePeriod)
		_ = syscall.Kill(-h.PID, syscall.SIGKILL)
		_ = h.Cmd.Process.Kill()
		return nil
	}

	select {
	case <-time.After(gracePeriod):
		_ = syscall.Kill(-h.PID, syscall.SIGKILL)
		_ = h.Cmd.Process.Kill()
		// Wait for the single reaper (monitorProcess) to observe the exit.
		<-h.waitDone
	case <-h.waitDone:
	}
	return nil
}
