package ipc

// Wiring evidence for register item A6 (I1 peer authentication): the IPC
// server obtains the peer's identity before serving any method, refuses a
// connection whose uid differs from the daemon's, accepts a same-uid peer,
// and never silently claims verification for a connection it could not
// check. The uid rule itself is exercised with injected uids — a real
// cross-uid connect requires a second OS user, which a unit test cannot
// create — while the same-uid path runs over a real unix socket.

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestPeerUIDEnforcementRule pins the core rule: same uid is accepted, any
// other uid is refused. Uids are injected so the rule can be proven without
// spawning a process as another user.
func TestPeerUIDEnforcementRule(t *testing.T) {
	const daemonUID = 1000

	if err := checkPeerUID(daemonUID, daemonUID); err != nil {
		t.Fatalf("same uid must be accepted: %v", err)
	}
	if err := checkPeerUID(os.Getuid(), os.Getuid()); err != nil {
		t.Fatalf("own uid must be accepted: %v", err)
	}

	for _, peerUID := range []int{0, 1, 999, 1001, 65534, -1} {
		if err := checkPeerUID(peerUID, daemonUID); err == nil {
			t.Fatalf("peer uid %d must be refused when the daemon uid is %d", peerUID, daemonUID)
		}
	}
}

// TestClassifyPeerStates covers the three outcomes classifyPeer can produce:
// verified (matching uid), refused (mismatched uid or a failed lookup on a
// platform where a real check exists), and unverified-but-allowed (no
// mechanism at all) — the last of which must always carry a reason.
func TestClassifyPeerStates(t *testing.T) {
	const daemonUID = 1000

	// Same uid: accepted and marked verified with the mechanism recorded.
	id, err := classifyPeer(&peerCreds{UID: daemonUID, PID: 42, Method: "test-method"}, "", nil, daemonUID)
	if err != nil {
		t.Fatalf("same-uid peer must be accepted: %v", err)
	}
	if !id.Verified {
		t.Fatal("same-uid peer must be verified")
	}
	if id.UID != daemonUID || id.PID != 42 || id.Method != "test-method" {
		t.Fatalf("unexpected identity: %+v", id)
	}
	if id.Reason != "" {
		t.Fatalf("verified identity must not carry a reason, got %q", id.Reason)
	}

	// Differing uid: refused, never verified.
	id, err = classifyPeer(&peerCreds{UID: daemonUID + 1, PID: 43, Method: "test-method"}, "", nil, daemonUID)
	if err == nil {
		t.Fatal("a differing uid must be refused")
	}
	if id.Verified {
		t.Fatal("a refused identity must never be verified")
	}
	if id.UID != daemonUID+1 {
		t.Fatalf("refused identity must record the observed uid, got %d", id.UID)
	}

	// No mechanism: allowed, but explicitly unverified with a reason.
	id, err = classifyPeer(nil, "no credential mechanism on this platform", nil, daemonUID)
	if err != nil {
		t.Fatalf("a connection without any credential mechanism must be allowed: %v", err)
	}
	if id.Verified {
		t.Fatal("an unverified connection must not be reported as verified")
	}
	if id.Reason == "" {
		t.Fatal("an unverified connection must state what was not checked")
	}

	// Mechanism present but failed: fail closed.
	_, err = classifyPeer(nil, "", errors.New("getsockopt: boom"), daemonUID)
	if err == nil {
		t.Fatal("a failed credential lookup must fail closed")
	}
}

// TestHandshakeReportsUnverifiedPeerOnPipeListener pins the honesty rule for
// the package's in-memory listeners: net.Pipe has no kernel credentials, so
// the handshake must report verified=false with a reason rather than staying
// silent or, worse, claiming verification.
func TestHandshakeReportsUnverifiedPeerOnPipeListener(t *testing.T) {
	server := NewServer("0.1.0", "2026-07-28")

	listener := newMockListener()
	go func() { _ = server.Serve(listener) }()
	serverConn, clientConn := net.Pipe()
	listener.conns <- serverConn

	client := NewClientFromConn(clientConn)
	t.Cleanup(func() { _ = client.Close(); _ = listener.Close(); _ = server.Stop() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	hs, err := client.Handshake(ctx, "cli", "")
	if err != nil {
		t.Fatalf("handshake failed: %v", err)
	}
	if hs.PeerAuth == nil {
		t.Fatal("handshake must report peer authentication status, even when unverified")
	}
	if hs.PeerAuth.Verified {
		t.Fatalf("an in-memory pipe has no credentials and must not be reported verified: %+v", hs.PeerAuth)
	}
	if hs.PeerAuth.Reason == "" {
		t.Fatal("an unverified peer must carry the reason it was not verified")
	}
}

// TestPeerAuthenticationAcceptsSameUIDOverUnixSocket runs the real accept
// path over a real unix socket in a temp dir: the connecting process has the
// same uid as the daemon, so the peer must be verified with the daemon's own
// uid reported back, and handlers must see the identity on their context.
func TestPeerAuthenticationAcceptsSameUIDOverUnixSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows named pipes have no unix peer credentials; the DACL path is transport_windows.go")
	}
	// t.TempDir() lands under $TMPDIR. macOS's sockaddr_un.sun_path limit is
	// 104 bytes and this test's long name overflows it under the runner's
	// deep /var/folders tree, so pin a short base (Linux caps at 108 — same
	// class of failure waiting to happen).
	t.Setenv("TMPDIR", "/tmp")

	handlerIdentity := make(chan PeerIdentity, 1)
	handlerOK := make(chan bool, 1)

	server := NewServer("0.1.0", "2026-07-28")
	server.RegisterHandler("test.peer", func(ctx context.Context, params json.RawMessage) (any, *RPCError) {
		peer, ok := PeerFromContext(ctx)
		handlerIdentity <- peer
		handlerOK <- ok
		return "ok", nil
	})

	endpoint := filepath.Join(t.TempDir(), "ipc.sock")
	// The production listener: 0700 directory, 0600 socket.
	listener, err := ListenIPC(endpoint)
	if err != nil {
		t.Fatalf("ListenIPC: %v", err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Stop()
		_ = listener.Close()
		<-serveDone
	})

	client, err := Dial(endpoint)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	hs, err := client.Handshake(ctx, "cli", "")
	if err != nil {
		t.Fatalf("handshake failed: %v", err)
	}
	if hs.PeerAuth == nil {
		t.Fatal("handshake must report peer authentication status")
	}
	if !hs.PeerAuth.Verified {
		t.Fatalf("same-uid unix socket must be verified, got %+v", hs.PeerAuth)
	}
	if hs.PeerAuth.UID != os.Getuid() {
		t.Fatalf("reported peer uid %d, want the daemon uid %d", hs.PeerAuth.UID, os.Getuid())
	}
	if hs.PeerAuth.Method == "" {
		t.Fatal("a verified peer must name the credential mechanism used")
	}

	var out string
	if err := client.Call(ctx, "test.peer", nil, &out); err != nil {
		t.Fatalf("call on verified connection failed: %v", err)
	}
	if out != "ok" {
		t.Fatalf("unexpected handler result %q", out)
	}

	select {
	case ok := <-handlerOK:
		if !ok {
			t.Fatal("handler must find the peer identity on its context")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not run")
	}
	peer := <-handlerIdentity
	if !peer.Verified || peer.UID != os.Getuid() {
		t.Fatalf("handler saw identity %+v, want verified with uid %d", peer, os.Getuid())
	}
}

// TestServerRefusesPeerWithMismatchedUID pins the refusal path end to end:
// a connection whose identity check fails is answered with a JSON-RPC
// CodeUnauthorized error and then closed without any handler running. The
// identity check is injected (peerIdentityFn) because a genuine cross-uid
// connect needs a second OS user; the uid rule itself is unit-tested in
// TestPeerUIDEnforcementRule and TestClassifyPeerStates.
func TestServerRefusesPeerWithMismatchedUID(t *testing.T) {
	server := NewServer("0.1.0", "2026-07-28")

	handled := make(chan struct{}, 1)
	server.RegisterHandler("test.secret", func(ctx context.Context, params json.RawMessage) (any, *RPCError) {
		handled <- struct{}{}
		return "secret", nil
	})

	// Installed before Serve so the accept loop observes it (the write
	// happens-before the goroutine that starts serving).
	server.peerIdentityFn = func(net.Conn) (PeerIdentity, error) {
		cause := errors.New("peer uid 1001 does not match daemon uid 1000")
		return PeerIdentity{UID: -1, Reason: cause.Error()}, cause
	}

	listener := newMockListener()
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = listener.Close(); _ = server.Stop() })

	serverConn, clientConn := net.Pipe()
	listener.conns <- serverConn
	codec := NewLineDelimitedCodec(clientConn)
	defer clientConn.Close()

	// The refused peer's first request is answered with a proper JSON-RPC
	// error instead of a bare disconnect.
	id := json.RawMessage(`"refuse-1"`)
	params, _ := json.Marshal(HandshakeParams{ClientKind: "cli", PID: os.Getpid()})
	if err := codec.WriteRequest(&Request{JSONRPC: "2.0", ID: &id, Method: "daemon.handshake", Params: params}); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	_ = clientConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	resp, err := codec.ReadResponse()
	if err != nil {
		t.Fatalf("expected a JSON-RPC refusal, got read error: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != CodeUnauthorized {
		t.Fatalf("expected CodeUnauthorized refusal, got %+v", resp)
	}
	if !strings.Contains(resp.Error.Message, "peer uid 1001") {
		t.Fatalf("refusal must explain the uid mismatch, got %q", resp.Error.Message)
	}

	// The connection is then closed: no further responses, no handler ever
	// invoked on it.
	if _, err := codec.ReadResponse(); err == nil {
		t.Fatal("connection must be closed after the refusal")
	}
	select {
	case <-handled:
		t.Fatal("a refused connection must never reach a handler")
	default:
	}
}
