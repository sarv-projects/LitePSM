package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

type mockListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
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
	// Idempotent and safe for concurrent callers: Serve may close the
	// listener it was given while Stop/test cleanup also closes it.
	m.once.Do(func() { close(m.closed) })
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
	err = client.Call(ctx, "test.echo", map[string]string{"message": "hello litespm"}, &echoResult)
	if err != nil {
		t.Fatalf("echo call failed: %v", err)
	}
	if echoResult["reply"] != "hello litespm" {
		t.Fatalf("expected reply 'hello litespm', got %q", echoResult["reply"])
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

// TestServerStopDoesNotRaceServeStartup pins the server lifecycle invariant
// that Stop may run concurrently with, or before, Serve without racing the
// server WaitGroup. A positive WaitGroup delta must never start from zero
// concurrently with Wait; Serve registers its accept loop under the same mutex
// Stop uses to mark the server stopped, so either the Add is observed by Wait or
// it never happens. Before this invariant held, Stop's Wait and a late Serve
// Add raced (the internal/bridge -race flake).
func TestServerStopDoesNotRaceServeStartup(t *testing.T) {
	for i := 0; i < 100; i++ {
		server := NewServer("0.1.0", "2026-07-28")
		listener := newMockListener()

		serveDone := make(chan error, 1)
		go func() { serveDone <- server.Serve(listener) }()

		// Stop immediately, racing Serve startup: Stop must not deadlock when
		// Serve has not yet registered, and Serve must still terminate.
		if err := server.Stop(); err != nil {
			t.Fatalf("iteration %d: Stop returned %v", i, err)
		}
		_ = listener.Close()

		select {
		case <-serveDone:
		case <-time.After(2 * time.Second):
			t.Fatalf("iteration %d: Serve did not return after Stop", i)
		}
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
	rawHandshake(t, codec)

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

// rawHandshake performs daemon.handshake over a raw codec and fails the test on
// any error.
func rawHandshake(t *testing.T, codec *LineDelimitedCodec) {
	t.Helper()
	id := json.RawMessage(`"hs"`)
	params, _ := json.Marshal(HandshakeParams{ClientKind: "cli", PID: 1})
	if err := codec.WriteRequest(&Request{JSONRPC: "2.0", ID: &id, Method: "daemon.handshake", Params: params}); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	resp, err := codec.ReadResponse()
	if err != nil || resp.Error != nil {
		t.Fatalf("handshake failed: %v %+v", err, resp)
	}
}

func startRawServer(t *testing.T, configure func(*Server)) (*Server, *LineDelimitedCodec, net.Conn) {
	t.Helper()
	server := NewServer("test", "2026-07-28")
	if configure != nil {
		configure(server)
	}
	listener := newMockListener()
	go func() { _ = server.Serve(listener) }()
	serverConn, clientConn := net.Pipe()
	listener.conns <- serverConn
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = listener.Close()
		_ = server.Stop()
	})
	return server, NewLineDelimitedCodec(clientConn), clientConn
}

func TestServerRequiresHandshakeBeforeOtherMethods(t *testing.T) {
	called := make(chan struct{}, 1)
	_, codec, _ := startRawServer(t, func(s *Server) {
		s.RegisterHandler("test.secret", func(ctx context.Context, params json.RawMessage) (any, *RPCError) {
			called <- struct{}{}
			return "secret", nil
		})
	})

	id := json.RawMessage(`1`)
	if err := codec.WriteRequest(&Request{JSONRPC: "2.0", ID: &id, Method: "test.secret"}); err != nil {
		t.Fatal(err)
	}
	resp, err := codec.ReadResponse()
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != CodeUnauthorized {
		t.Fatalf("expected CodeUnauthorized before handshake, got %+v", resp)
	}
	select {
	case <-called:
		t.Fatal("handler ran before the handshake")
	default:
	}

	// A handshake without clientKind is rejected and does not unlock the conn.
	id2 := json.RawMessage(`2`)
	_ = codec.WriteRequest(&Request{JSONRPC: "2.0", ID: &id2, Method: "daemon.handshake", Params: json.RawMessage(`{}`)})
	resp, _ = codec.ReadResponse()
	if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
		t.Fatalf("expected invalid params for empty handshake, got %+v", resp)
	}
	id3 := json.RawMessage(`3`)
	_ = codec.WriteRequest(&Request{JSONRPC: "2.0", ID: &id3, Method: "test.secret"})
	resp, _ = codec.ReadResponse()
	if resp.Error == nil || resp.Error.Code != CodeUnauthorized {
		t.Fatalf("failed handshake must not unlock the connection, got %+v", resp)
	}

	rawHandshake(t, codec)
	id4 := json.RawMessage(`4`)
	_ = codec.WriteRequest(&Request{JSONRPC: "2.0", ID: &id4, Method: "test.secret"})
	resp, _ = codec.ReadResponse()
	if resp.Error != nil {
		t.Fatalf("method must work after handshake: %+v", resp.Error)
	}
}

func TestClientHandshakesAutomatically(t *testing.T) {
	server := NewServer("test", "2026-07-28")
	server.RegisterHandler("test.ping", func(ctx context.Context, params json.RawMessage) (any, *RPCError) {
		return "pong", nil
	})
	listener := newMockListener()
	go func() { _ = server.Serve(listener) }()
	serverConn, clientConn := net.Pipe()
	listener.conns <- serverConn
	client := NewClientFromConn(clientConn)
	t.Cleanup(func() { _ = client.Close(); _ = listener.Close(); _ = server.Stop() })

	var out string
	if err := client.Call(context.Background(), "test.ping", nil, &out); err != nil || out != "pong" {
		t.Fatalf("auto-handshake call failed: %q %v", out, err)
	}
}

func TestServerHandshakeDeadline(t *testing.T) {
	_, codec, conn := startRawServer(t, func(s *Server) {
		s.SetTimeouts(100*time.Millisecond, time.Minute)
	})
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	// A silent connection is closed by the server's read deadline.
	if _, err := codec.ReadResponse(); err == nil {
		t.Fatal("expected the server to close a connection that never handshakes")
	}
}

func TestServerBoundsConcurrentHandlers(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 4)
	_, codec, _ := startRawServer(t, func(s *Server) {
		s.SetMaxConcurrentHandlers(2)
		s.RegisterHandler("test.block", func(ctx context.Context, params json.RawMessage) (any, *RPCError) {
			entered <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
			}
			return "done", nil
		})
	})
	rawHandshake(t, codec)

	send := func(n int) {
		id := json.RawMessage(fmt.Sprintf("%d", n))
		if err := codec.WriteRequest(&Request{JSONRPC: "2.0", ID: &id, Method: "test.block"}); err != nil {
			t.Fatal(err)
		}
	}
	send(1)
	send(2)
	<-entered
	<-entered
	send(3)
	resp, err := codec.ReadResponse()
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != CodeRateLimited {
		t.Fatalf("third concurrent request must be rate limited, got %+v", resp)
	}
	close(release)
	for i := 0; i < 2; i++ {
		if resp, err := codec.ReadResponse(); err != nil || resp.Error != nil {
			t.Fatalf("in-flight request failed: %v %+v", err, resp)
		}
	}
	// Capacity is released afterwards.
	send(4)
	<-entered
	resp, err = codec.ReadResponse()
	if err != nil || resp.Error != nil {
		t.Fatalf("request after release failed: %v %+v", err, resp)
	}
}
