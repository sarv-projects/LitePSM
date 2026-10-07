//go:build unix

package config

import (
	"os"
	"syscall"
)

// fileOwnerUID returns the owning user id of fi where the platform reports
// one (every unix does, via stat(2)). ok is false when the FileInfo carries no
// unix stat, in which case verifyRuntimeRoot skips the ownership check instead
// of guessing.
func fileOwnerUID(fi os.FileInfo) (uint32, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return 0, false
	}
	return uint32(st.Uid), true
}

// currentUID is the effective user id the runtime root must belong to.
func currentUID() uint32 { return uint32(os.Geteuid()) }
