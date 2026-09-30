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
