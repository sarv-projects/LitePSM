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
	// macOS / BSD have no PR_SET_PDEATHSIG equivalent.
}

// startWatchdogIfRequired arms the best-effort watchdog on macOS/BSD.
//
// HONEST LIMITATION: the read end of the pipe lives inside this same process.
// It therefore protects against a handle being dropped or its writer being
// closed while the daemon is alive, but it CANNOT fire if the daemon itself is
// SIGKILLed or crashes — the goroutine dies with it. Unlike Linux
// (PR_SET_PDEATHSIG) or Windows (job object KILL_ON_JOB_CLOSE), no
// kernel-enforced parent-death cleanup exists here; a provider may be orphaned
// when the daemon dies abnormally. Graceful shutdown (Terminate/StopAll) is the
// supported path and is unaffected.
//
// The writer is closed only by monitorProcess after the child is reaped, so a
// normal exit never triggers a kill and the goroutine and both fds are always
// released (no leak). A requested Terminate never closes it earlier, so the
// SIGTERM grace period applies.
func startWatchdogIfRequired(h *ProviderHandle) error {
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("watchdog pipe: %w", err)
	}
	h.PipeWriter = w

	go func(pid int, readPipe *os.File, done <-chan struct{}) {
		defer readPipe.Close()
		buf := make([]byte, 1)
		// Unblocks on EOF: the writer closed (process reaped, or the handle's
		// writer was dropped while the daemon is still alive).
		_, _ = readPipe.Read(buf)

		select {
		case <-done:
			return // child already reaped: nothing to kill
		default:
		}

		// Writer closed while the child still runs: graceful first, then force.
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(watchdogGrace):
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
	}(h.PID, r, h.waitDone)
	return nil
}
