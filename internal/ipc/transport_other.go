//go:build !linux && !darwin && !windows

package ipc

import (
	"fmt"
	"net"
	"runtime"
)

// peerCredentials on every other GOOS (freebsd, openbsd, netbsd, dragonfly,
// solaris, aix, js, ...): no credential mechanism is wired, so the connection
// is recorded as unverified rather than failing.
//
// This is an honest gap, not a claim of verification. The BSDs and Solaris
// offer getpeereid()/LOCAL_PEERCRED for unix sockets and could be wired much
// like transport_darwin.go, but this project has no CI runner for those
// platforms, so nothing here pretends to check them. On such a platform the
// runtime-root directory permissions (0700 directory, ownership-verified —
// internal/config/runtime_root.go) remain the only boundary, exactly as
// before peer authentication was added.
func peerCredentials(conn net.Conn) (*peerCreds, string, error) {
	return nil, fmt.Sprintf(
		"no peer-credential mechanism is implemented for GOOS=%s: a unix socket here relies on runtime-root directory permissions only",
		runtime.GOOS,
	), nil
}
