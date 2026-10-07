//go:build unix && !linux

package provider

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

// watchdogGrace is how long the watchdog lets a process group honour SIGTERM
// before escalating to SIGKILL.
const watchdogGrace = 2 * time.Second

func setParentDeathSignal(attr *syscall.SysProcAttr) {
	// macOS / BSD have no PR_SET_PDEATHSIG; the watchdog control pipe below
	// is the parent-death mechanism on these platforms.
}

// startWatchdogIfRequired installs the macOS/BSD parent-death watchdog.
//
// HONEST LIMITATION: this is an in-process goroutine, not a separate watchdog
// process. The read end of the pipe lives inside the daemon, so if the daemon
// itself is SIGKILLed or crashes, the goroutine dies with it and cannot kill
// the child — no kernel-enforced parent-death cleanup exists on these
// platforms (unlike Linux PR_SET_PDEATHSIG or Windows KILL_ON_JOB_CLOSE). A
// provider may therefore be orphaned when the daemon dies abnormally;
// graceful shutdown (Terminate/StopAll) is the supported path and is
// unaffected. A provider that double-forks/daemonizes also escapes its
// process group and must be supervised out of band.
//
// The pipe is created AFTER the child starts, so the child never inherits
// either end (no fd leak into the provider). The writer is closed only by
// monitorProcess after the child is reaped or by an explicit Terminate, so a
// normal exit never triggers a kill and the goroutine and both fds are always
// released (no leak).
func startWatchdogIfRequired(h *ProviderHandle) error {
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("watchdog pipe: %w", err)
	}
	h.PipeReader = r
	h.PipeWriter = w

	go watchdogLoop(h, h.PID, r)
	return nil
}

func watchdogLoop(h *ProviderHandle, pid int, readPipe *os.File) {
	defer readPipe.Close()
	buf := make([]byte, 1)
	// Blocks until the daemon's write end closes (daemon exit or Terminate).
	// Any read error/EOF unblocks; the byte content is irrelevant.
	_, _ = readPipe.Read(buf)

	// A graceful Terminate closes the write end on purpose: do not kill what
	// was asked to stop. An already-reaped child (waitDone closed) is also
	// left alone so a normal exit never triggers a stray SIGKILL.
	h.mu.RLock()
	terminating := h.terminating
	h.mu.RUnlock()
	if terminating {
		return
	}
	select {
	case <-h.waitDone:
		return
	default:
	}

	// Grace works: SIGTERM first so the provider can flush, then SIGKILL.
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	time.Sleep(watchdogGrace)
	select {
	case <-h.waitDone:
		return
	default:
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
