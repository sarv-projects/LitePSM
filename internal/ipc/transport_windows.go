//go:build windows

package ipc

import (
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
)

// ListenIPC creates a Windows Named Pipe listener restricted by SDDL DACL.
// SDDL: D:(A;;GA;;;OW) - Grants Generic All access only to the Owner SID.
func ListenIPC(endpoint string) (net.Listener, error) {
	pipeConfig := &winio.PipeConfig{
		SecurityDescriptor: "D:(A;;GA;;;OW)",
		MessageMode:        false, // byte stream mode for line delimited JSON-RPC
		InputBufferSize:    65536,
		OutputBufferSize:   65536,
	}

	listener, err := winio.ListenPipe(endpoint, pipeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create named pipe %s: %w", endpoint, err)
	}

	return listener, nil
}

// DialIPC connects to the Windows Named Pipe.
func DialIPC(endpoint string) (net.Conn, error) {
	conn, err := winio.DialPipe(endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to dial named pipe %s: %w", endpoint, err)
	}
	return conn, nil
}

// peerCredentials on Windows records the connection as unverified at the IPC
// layer — deliberately, and with the reason spelled out rather than a silent
// pass.
//
// The pipe itself is not an unguarded channel: ListenIPC creates it with the
// SDDL DACL "D:(A;;GA;;;OW)", so the OS rejects connect-time access
// (CreateFile) from any SID other than the pipe owner before Accept ever
// returns — a foreign user never reaches this code. But this package performs
// no in-process peer-identity query of its own (go-winio exposes no portable
// client-SID accessor, and per the fail-closed-only-where-a-real-check-exists
// rule we do not invent one), so the handshake reports verified=false with
// the DACL as the reason instead of claiming a verification it did not
// perform. daemonUID is os.Getuid(), which is -1 on Windows; it is never
// compared here because no credentials are ever returned.
func peerCredentials(conn net.Conn) (*peerCreds, string, error) {
	return nil, "windows named pipe: access is gated by the owner-only DACL the listener was created with (enforced by the OS at connect time); the IPC layer performs no additional in-process peer-identity check", nil
}
