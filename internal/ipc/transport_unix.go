//go:build !windows

package ipc

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// ListenIPC creates a Unix Domain Socket listener with strict 0600 permissions.
func ListenIPC(endpoint string) (net.Listener, error) {
	dir := filepath.Dir(endpoint)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create socket directory %s: %w", dir, err)
	}

	// Remove stale socket if present
	_ = os.Remove(endpoint)

	listener, err := net.Listen("unix", endpoint)
	if err != nil {
		// sockaddr_un.sun_path is 104 bytes including the NUL on macOS/BSD
		// and 108 on Linux; an over-long path otherwise surfaces as a bare
		// "bind: invalid argument" with no hint about the real cause.
		if len(endpoint) > 103 {
			return nil, fmt.Errorf("failed to listen on unix socket %s: %w (path is %d bytes; the sockaddr_un limit is ~104 — shorten XDG_RUNTIME_DIR or TMPDIR)",
				endpoint, err, len(endpoint))
		}
		return nil, fmt.Errorf("failed to listen on unix socket %s: %w", endpoint, err)
	}

	// Restrict permissions to owner only
	if err := os.Chmod(endpoint, 0600); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("failed to chmod socket %s: %w", endpoint, err)
	}

	return listener, nil
}

// DialIPC connects to the Unix Domain Socket endpoint.
func DialIPC(endpoint string) (net.Conn, error) {
	conn, err := net.Dial("unix", endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to unix socket %s: %w", endpoint, err)
	}
	return conn, nil
}
