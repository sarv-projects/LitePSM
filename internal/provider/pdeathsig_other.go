//go:build unix && !linux

package provider

import (
	"os"
	"syscall"
)

func setParentDeathSignal(attr *syscall.SysProcAttr) {
	// macOS / BSD do not have PR_SET_PDEATHSIG; watchdog control pipe is used
}

func startWatchdogIfRequired(h *ProviderHandle) {
	r, w, err := os.Pipe()
	if err != nil {
		return
	}
	h.PipeWriter = w

	go func(pid int, readPipe *os.File) {
		defer readPipe.Close()
		buf := make([]byte, 1)
		// Blocks until write pipe closes (daemon process exits or receives SIGKILL)
		_, _ = readPipe.Read(buf)
		// EOF trapped -> kill entire child process group immediately
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}(h.PID, r)
}
