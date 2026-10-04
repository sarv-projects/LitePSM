//go:build aix || android || darwin || dragonfly || freebsd || ios || linux

package doctor

import "syscall"

// queryFreeBytes returns the free space available to an unprivileged caller for
// the filesystem containing path, via statfs.
//
// The build tag is an explicit positive list rather than `unix` because this
// exact body (syscall.Statfs plus Statfs_t.Bavail/Bsize) does not compile on
// every unix GOOS: netbsd/openbsd have Statfs but no Bavail/Bsize fields, and
// illumos/solaris have neither syscall.Statfs nor syscall.Statfs_t. Those GOOS
// fall through to the honest "unsupported" stub in diskspace_other.go.
func queryFreeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
