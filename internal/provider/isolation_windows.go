//go:build windows

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
	// On Windows, create in a new process group
	cmd.SysProcAttr.CreationFlags = syscall.CREATE_NEW_PROCESS_GROUP
}

func killProcessTree(h *ProviderHandle, gracePeriod time.Duration) error {
	if h.Cmd == nil || h.Cmd.Process == nil {
		return nil
	}

	done := make(chan error, 1)
	go func() {
		done <- h.Cmd.Wait()
	}()

	_ = h.Cmd.Process.Kill()

	select {
	case <-time.After(gracePeriod):
		_ = h.Cmd.Process.Kill()
	case <-done:
	}
	return nil
}
