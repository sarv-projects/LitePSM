//go:build !linux && !windows

package provider

import "syscall"

func setParentDeathSignal(attr *syscall.SysProcAttr) {
	// macOS does not have PDEATHSIG; Unix process group + watchdog control pipe is used
}
