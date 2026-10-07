//go:build linux

package ipc

import (
	"fmt"
	"net"
	"syscall"
)

// peerMethodSOPEERCRED names the Linux credential mechanism in reported
// identities.
const peerMethodSOPEERCRED = "SO_PEERCRED"

// peerCredentials on Linux: the accepted socket's peer credentials come
// straight from the kernel through SO_PEERCRED (uid, gid and pid of the
// process that connected, snapshotted at connect time and immutable
// afterwards, so there is no pid-reuse race to resolve).
//
// A unix socket always has this mechanism. If it cannot be read (raw fd
// control fails, or getsockopt errors) the error is returned — the caller
// must fail closed rather than let an unknown-uid connection through. A
// connection that is not a unix socket (net.Pipe from an in-memory listener)
// has no credentials at all and is reported as unverified instead.
func peerCredentials(conn net.Conn) (*peerCreds, string, error) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return nil, reasonNotUnixSocket, nil
	}

	raw, err := uc.SyscallConn()
	if err != nil {
		return nil, "", fmt.Errorf("unix socket: obtain raw fd: %w", err)
	}

	var (
		ucred   *syscall.Ucred
		opErr   error
		ctrlErr error
	)
	ctrlErr = raw.Control(func(fd uintptr) {
		ucred, opErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if ctrlErr != nil {
		return nil, "", fmt.Errorf("unix socket: raw fd control: %w", ctrlErr)
	}
	if opErr != nil {
		return nil, "", fmt.Errorf("SO_PEERCRED: %w", opErr)
	}

	return &peerCreds{
		UID:    int(ucred.Uid),
		PID:    int(ucred.Pid),
		Method: peerMethodSOPEERCRED,
	}, "", nil
}
