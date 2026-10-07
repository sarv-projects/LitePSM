//go:build linux

package provider

import "syscall"

// setParentDeathSignal asks the kernel to SIGKILL the direct child when its
// parent (the daemon) dies, including daemon SIGKILL — a guarantee an
// in-process goroutine cannot provide.
//
// One-generation limit: Pdeathsig is a per-process attribute of the direct
// child only. Grandchildren the provider spawns do NOT inherit it; if the
// intermediate child exits they reparent to init and survive the daemon's
// death. Providers that double-fork/daemonize escape this guarantee.
func setParentDeathSignal(attr *syscall.SysProcAttr) {
	attr.Pdeathsig = syscall.SIGKILL
}

func startWatchdogIfRequired(h *ProviderHandle) error {
	// Linux natively enforces process cleanup via PR_SET_PDEATHSIG in the kernel
	return nil
}
