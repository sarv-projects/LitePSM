//go:build linux

package provider

import "syscall"

func setParentDeathSignal(attr *syscall.SysProcAttr) {
	attr.Pdeathsig = syscall.SIGKILL
}

func startWatchdogIfRequired(h *ProviderHandle) {
	// Linux natively enforces process cleanup via PR_SET_PDEATHSIG in the kernel
}
