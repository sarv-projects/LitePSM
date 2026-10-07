//go:build darwin

package ipc

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// Mechanism names reported by peerCredentials on macOS.
const (
	// peerMethodLocalPeerCred marks a uid read directly from the peer's
	// xucred (SOL_LOCAL/LOCAL_PEERCRED) — the same data getpeereid()
	// returns.
	peerMethodLocalPeerCred = "LOCAL_PEERCRED"
	// peerMethodLocalPeerPID marks a uid resolved from the connecting pid
	// (SOL_LOCAL/LOCAL_PEERPID) through sysctl(KERN_PROC_PID).
	peerMethodLocalPeerPID = "LOCAL_PEERPID+sysctl"
)

// peerCredentials on macOS: there is no SO_PEERCRED on Darwin unix sockets,
// so two mechanisms are consulted:
//
//  1. LOCAL_PEERCRED (SOL_LOCAL) returns the peer's xucred; its Uid is the
//     peer's uid — the classic getpeereid() path, filled by the kernel at
//     connect time.
//  2. LOCAL_PEERPID (SOL_LOCAL) returns the connecting pid, kept for
//     reporting and used as the fallback: the pid is resolved to a uid via
//     sysctl(KERN_PROC_PID). That resolution races process exit/reuse, which
//     is why it is the fallback and not the primary.
//
// A unix socket on Darwin always has at least one of these, so failing to
// obtain a uid returns an error and the caller refuses the connection (fail
// closed). Non-unix connections (net.Pipe) have no credentials and are
// reported unverified instead.
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
		uid     = -1
		pid     int
		credErr error
		pidErr  error
		ctrlErr error
	)
	ctrlErr = raw.Control(func(fd uintptr) {
		var xc *unix.Xucred
		xc, credErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if credErr == nil {
			uid = int(xc.Uid)
		}
		var p int
		p, pidErr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
		if pidErr == nil {
			pid = p
		}
	})
	if ctrlErr != nil {
		return nil, "", fmt.Errorf("unix socket: raw fd control: %w", ctrlErr)
	}

	method := peerMethodLocalPeerCred
	if uid < 0 && pid > 0 {
		// Fallback: resolve the pid to a uid. Eproc.Ucred.Uid is the
		// process's effective uid as reported by the kernel.
		if kp, kerr := unix.SysctlKinfoProc("kern.proc.pid", pid); kerr == nil {
			uid = int(kp.Eproc.Ucred.Uid)
			method = peerMethodLocalPeerPID
		} else {
			credErr = fmt.Errorf("sysctl kern.proc.pid %d: %w", pid, kerr)
		}
	}
	if uid < 0 {
		return nil, "", fmt.Errorf("macos: could not establish peer uid (LOCAL_PEERCRED: %v, LOCAL_PEERPID: %v)", credErr, pidErr)
	}

	return &peerCreds{UID: uid, PID: pid, Method: method}, "", nil
}
