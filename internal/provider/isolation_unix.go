//go:build !windows

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

func killProcessTree(h *ProviderHandle, gracePeriod time.Duration) error {
	if h.Cmd == nil || h.Cmd.Process == nil {
		return nil
	}

	done := make(chan error, 1)
	go func() {
		done <- h.Cmd.Wait()
	}()

	// Signal the entire process group
	_ = syscall.Kill(-h.PID, syscall.SIGTERM)

	select {
	case <-time.After(gracePeriod):
		_ = syscall.Kill(-h.PID, syscall.SIGKILL)
		_ = h.Cmd.Process.Kill()
	case <-done:
	}
	return nil
}
