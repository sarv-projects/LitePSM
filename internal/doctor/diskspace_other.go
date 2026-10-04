//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !ios && !linux && !windows

package doctor

import "fmt"

// queryFreeBytes has no implementation on this platform; the disk check
// reports the failure instead of guessing. This tag is the exact complement of
// the Statfs-capable GOOS list in diskspace_unix.go, with windows handled by
// diskspace_windows.go, so every GOOS gets exactly one implementation.
func queryFreeBytes(path string) (uint64, error) {
	return 0, fmt.Errorf("free disk space query is not supported on this platform (path %s)", path)
}
