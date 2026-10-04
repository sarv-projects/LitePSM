package ipc

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"
)

type mockListener struct {
	conns  chan net.Conn
	closed chan struct{}
}

func newMockListener() *mockListener {
	return &mockListener{
		conns:  make(chan net.Conn, 10),
		closed: make(chan struct{}),
	}
}

func (m *mockListener) Accept() (net.Conn, error) {
	select {
	case conn := <-m.conns:
		return conn, nil
	case <-m.closed:
		return nil, net.ErrClosed
	}
}

func (m *mockListener) Close() error {
	select {
	case <-m.closed:
	default:
		close(m.closed)
	}
	return nil
}

func (m *mockListener) Addr() net.Addr {
	return &net.UnixAddr{Name: "mock-pipe", Net: "unix"}
}

func TestIPCHandshakeAndEcho(t *testing.T) {
	server := NewServer("0.1.0", "2026-07-28")

	// Register an echo method
	server.RegisterHandler("test.echo", func(ctx context.Context, params json.RawMessage) (any, *RPCError) {
		var req map[string]string
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, &RPCError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return map[string]string{"reply": req["message"]}, nil
	})

	listener := newMockListener()
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- server.Serve(listener)
	}()
	defer func() {
		_ = server.Stop()
		_ = listener.Close()
	}()

	// Connect client via net.Pipe
	serverConn, clientConn := net.Pipe()
	listener.conns <- serverConn

	client := NewClientFromConn(clientConn)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 1. Handshake
	hs, err := client.Handshake(ctx, "cli", "cline")
	if err != nil {
		t.Fatalf("Handshake failed: %v", err)
	}
	if hs.DaemonVersion != "0.1.0" || hs.ProtocolVersion != "2026-07-28" {
		t.Fatalf("unexpected handshake result: %+v", hs)
	}

	// 2. Echo Call
	var echoResult map[string]string
	err = client.Call(ctx, "test.echo", map[string]string{"message": "hello litepsm"}, &echoResult)
	if err != nil {
		t.Fatalf("echo call failed: %v", err)
	}
	if echoResult["reply"] != "hello litepsm" {
		t.Fatalf("expected reply 'hello litepsm', got %q", echoResult["reply"])
	}

	// 3. Method Not Found
	var notFoundResult any
	err = client.Call(ctx, "unknown.method", nil, &notFoundResult)
	if err == nil {
		t.Fatal("expected error for non-existent method, but succeeded")
	}
	rpcErr, ok := err.(*RPCError)
	if !ok || rpcErr.Code != CodeMethodNotFound {
		t.Fatalf("expected RPCError with code %d, got %v", CodeMethodNotFound, err)
	}
}

func TestIPCCancellation(t *testing.T) {
	server := NewServer("0.1.0", "2026-07-28")

	handlerEntered := make(chan struct{})
	handlerCancelled := make(chan struct{})

	server.RegisterHandler("test.longRunning", func(ctx context.Context, params json.RawMessage) (any, *RPCError) {
		close(handlerEntered)
		select {
		case <-ctx.Done():
			close(handlerCancelled)
			return nil, &RPCError{Code: CodeInternalError, Message: "request cancelled"}
		case <-time.After(5 * time.Second):
			return "completed", nil
		}
	})

	listener := newMockListener()
	go func() {
		_ = server.Serve(listener)
	}()
	defer func() {
		_ = server.Stop()
		_ = listener.Close()
	}()

	serverConn, clientConn := net.Pipe()
	listener.conns <- serverConn

	client := NewClientFromConn(clientConn)
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())

	callDone := make(chan error, 1)
	go func() {
		var res string
		callDone <- client.Call(ctx, "test.longRunning", nil, &res)
	}()

	// Wait until handler is running
	<-handlerEntered

	// Trigger client-side cancellation
	cancel()

	// Wait for call to return context.Canceled
	select {
	case err := <-callDone:
		if err != context.Canceled {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for client Call to cancel")
	}

	// Wait for server to receive cancellation
	select {
	case <-handlerCancelled:
		// Success!
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server handler context to cancel")
	}
}

func TestServerStopIdempotentAndCancelsHandlers(t *testing.T) {
	server := NewServer("0.1.0", "2026-07-28")
	handlerEntered := make(chan struct{})
	handlerCancelled := make(chan struct{})

	server.RegisterHandler("test.blocking", func(ctx context.Context, params json.RawMessage) (any, *RPCError) {
		close(handlerEntered)
		select {
		case <-ctx.Done():
			close(handlerCancelled)
			return nil, &RPCError{Code: CodeInternalError, Message: "server stopped"}
		case <-time.After(5 * time.Second):
			return "done", nil
		}
	})

	listener := newMockListener()
	go func() {
		_ = server.Serve(listener)
	}()

	serverConn, clientConn := net.Pipe()
	listener.conns <- serverConn

	client := NewClientFromConn(clientConn)
	defer client.Close()

	go func() {
		var res string
		_ = client.Call(context.Background(), "test.blocking", nil, &res)
	}()

	<-handlerEntered

	// Call Stop from multiple goroutines concurrently to verify idempotence and safe WaitGroup shutdown
	done := make(chan struct{})
	go func() {
		for i := 0; i < 5; i++ {
			go func() {
				_ = server.Stop()
			}()
		}
		_ = server.Stop()
		close(done)
	}()

	select {
	case <-handlerCancelled:
		// Handler context was canceled by Stop()
	case <-time.After(2 * time.Second):
		t.Fatal("handler context was not canceled when server stopped")
	}

	select {
	case <-done:
		// Stop completed cleanly
	case <-time.After(2 * time.Second):
		t.Fatal("server.Stop() timed out or deadlocked")
	}
}

func TestDuplicateRequestID(t *testing.T) {
	server := NewServer("0.1.0", "2026-07-28")
	handlerEntered := make(chan struct{})
	unblock := make(chan struct{})

	server.RegisterHandler("test.pause", func(ctx context.Context, params json.RawMessage) (any, *RPCError) {
		select {
		case <-handlerEntered:
		default:
			close(handlerEntered)
		}
		select {
		case <-unblock:
			return "ok", nil
		case <-ctx.Done():
			return nil, &RPCError{Code: CodeInternalError, Message: "canceled"}
		}
	})

	listener := newMockListener()
	go func() {
		_ = server.Serve(listener)
	}()
	defer func() {
		_ = server.Stop()
		_ = listener.Close()
	}()

	serverConn, clientConn := net.Pipe()
	listener.conns <- serverConn

	codec := NewLineDelimitedCodec(clientConn)
	defer clientConn.Close()

	reqID := json.RawMessage(`"req-dup-1"`)
	req1 := &Request{
		JSONRPC: "2.0",
		ID:      &reqID,
		Method:  "test.pause",
	}

	if err := codec.WriteRequest(req1); err != nil {
		t.Fatalf("failed to write req1: %v", err)
	}

	// Wait for req1 to start processing
	<-handlerEntered

	// Send duplicate request with identical ID while req1 is in flight
	req2 := &Request{
		JSONRPC: "2.0",
		ID:      &reqID,
		Method:  "test.pause",
	}
	if err := codec.WriteRequest(req2); err != nil {
		t.Fatalf("failed to write req2: %v", err)
	}

	// Read response for duplicate request - should immediately reject
	resp2, err := codec.ReadResponse()
	if err != nil {
		t.Fatalf("failed to read response for duplicate request: %v", err)
	}
	if resp2.Error == nil || resp2.Error.Code != CodeInvalidRequest {
		t.Fatalf("expected CodeInvalidRequest for duplicate request, got: %+v", resp2)
	}

	// Unblock req1 and read its response
	close(unblock)
	resp1, err := codec.ReadResponse()
	if err != nil {
		t.Fatalf("failed to read response for first request: %v", err)
	}
	if resp1.Error != nil {
		t.Fatalf("first request failed: %+v", resp1.Error)
	}
}
