package ipc

import (
	"context"
	"fmt"
	"net"
)

// This file holds the platform-independent half of peer authentication
// (register item A6 / I1): the identity type, the enforcement rule, and the
// context plumbing. The platform-specific credential lookups live in the
// build-tagged transport files:
//
//   - transport_linux.go   — SO_PEERCRED (uid + pid from the kernel)
//   - transport_darwin.go  — LOCAL_PEERCRED / LOCAL_PEERPID + sysctl (macOS
//     has no SO_PEERCRED on unix sockets)
//   - transport_windows.go — documented fallback (the OS-enforced owner-only
//     pipe DACL does the gatekeeping; no in-process check exists)
//   - transport_other.go   — documented fallback for every other GOOS
//
// A nonce/token file was deliberately NOT added as defense in depth: the
// credential check above is kernel-authoritative and unforgeable, while a
// token file readable only by the owner (0600 in the runtime root) is
// readable by the very same-uid attacker the socket permissions already
// admit, and requiring it in the handshake would refuse every older client
// binary for no gain over the credential check.

// PeerIdentity is the outcome of peer authentication for one connection. It
// has exactly three states:
//
//   - Verified: the platform reported the peer's credentials through an
//     authoritative kernel mechanism and they matched the daemon's uid.
//   - Refused: credentials were reported and did not match. The connection is
//     closed before any method is served; handlers never see this identity —
//     it exists only so the refusal can be explained.
//   - Unverified: the platform or the connection offers no credential
//     mechanism (see peerCredentials in the transport files). The connection
//     is allowed — in-memory pipe listeners and platforms without a wired
//     mechanism keep working — but Reason states exactly what was NOT
//     checked, so nothing downstream can mistake it for a check that
//     happened.
type PeerIdentity struct {
	// Verified is true only when credentials were obtained and matched the
	// daemon's uid.
	Verified bool

	// Method names the kernel mechanism that produced UID/PID (e.g.
	// "SO_PEERCRED", "LOCAL_PEERCRED"); empty when no credentials were
	// obtained.
	Method string

	// UID is the peer's user id as reported by the OS, or -1 when it could
	// not be established.
	UID int

	// PID is the peer process id when the mechanism reports one, else 0.
	PID int

	// Reason states why the connection was not verified (or why it was
	// refused). Empty when Verified is true.
	Reason string
}

// PeerAuthStatus is the wire form of PeerIdentity, embedded in
// HandshakeResult so a client can see how — or whether — the daemon
// authenticated it. It is additive: clients that predate it simply ignore the
// JSON field.
type PeerAuthStatus struct {
	// Verified is true only when the daemon obtained peer credentials and
	// they matched its own uid.
	Verified bool `json:"verified"`

	// Method names the mechanism that produced the credentials, when any.
	Method string `json:"method,omitempty"`

	// UID is the peer's uid, or -1 when it was not established.
	UID int `json:"uid"`

	// PID is the peer process id when the mechanism reports one, else 0.
	PID int `json:"pid,omitempty"`

	// Reason states what was not checked when Verified is false.
	Reason string `json:"reason,omitempty"`
}

// Status returns the wire form of the identity.
func (p PeerIdentity) Status() *PeerAuthStatus {
	return &PeerAuthStatus{
		Verified: p.Verified,
		Method:   p.Method,
		UID:      p.UID,
		PID:      p.PID,
		Reason:   p.Reason,
	}
}

// peerIdentityKey is the unexported context key under which the server stores
// a connection's PeerIdentity. Handlers receive it on their request context
// and read it back with PeerFromContext.
type peerIdentityKey struct{}

// PeerFromContext returns the PeerIdentity the server established for the
// connection a handler is running on. The second result is false when the
// handler is invoked outside a server connection (direct invocation in a
// test, for example), in which case no peer was ever observed.
func PeerFromContext(ctx context.Context) (PeerIdentity, bool) {
	p, ok := ctx.Value(peerIdentityKey{}).(PeerIdentity)
	return p, ok
}

// peerCreds is what a platform's credential mechanism observed about the peer
// of a socket. It is produced only by the build-tagged peerCredentials
// functions, whose contract is:
//
//   - (creds, _, nil): the platform identified the peer.
//   - (nil, reason, nil): the platform/connection offers no credential
//     mechanism. The connection stays allowed but is marked unverified with
//     reason. net.Pipe and other in-memory listeners always take this path —
//     the package's public API has always accepted them, and refusing them
//     would break every test and in-process consumer.
//   - (_, _, err): a real mechanism exists but failed. The connection must be
//     refused (fail closed): "could not prove same uid" is not "same uid".
//
// Consequence: a unix socket listener enforces the uid check on Linux and
// macOS; on Windows the owner-only pipe DACL is the (OS-enforced) boundary;
// on other unix GOOS no mechanism is wired and connections are honestly
// recorded as unverified.
type peerCreds struct {
	// UID is the peer's user id as reported by the kernel.
	UID int

	// PID is the peer process id when the mechanism reports one, else 0.
	PID int

	// Method names the mechanism ("SO_PEERCRED", "LOCAL_PEERCRED", ...).
	Method string
}

// reasonNotUnixSocket explains why no credentials were consulted: a
// connection that is not a unix socket carries no kernel peer credentials at
// all. This is the path in-memory pipe listeners (net.Pipe) and any other
// non-unix listener take.
const reasonNotUnixSocket = "connection is not a unix socket (in-memory pipe or other listener): no kernel peer credentials exist to consult"

// checkPeerUID is the enforcement rule: a peer is accepted only when its
// uid equals the daemon's uid. The uids are parameters so the rule can be
// exercised with injected values — a genuine cross-uid connect attempt needs
// a second OS user, which a unit test cannot create.
//
// The daemon compares against os.Getuid() (its real uid); it is not a setuid
// binary, so real and effective uid are the same and the comparison is exact.
func checkPeerUID(peerUID, daemonUID int) error {
	if peerUID != daemonUID {
		return fmt.Errorf("peer uid %d does not match daemon uid %d", peerUID, daemonUID)
	}
	return nil
}

// classifyPeer applies the enforcement rule to a platform observation and
// produces the connection's identity. A non-nil error means the connection
// must be refused.
func classifyPeer(creds *peerCreds, unavailableReason string, cause error, daemonUID int) (PeerIdentity, error) {
	// A mechanism exists but failed: fail closed. Refusing is safe (the
	// legitimate same-uid client simply retries or reports), while allowing
	// would turn "lookup broken" into "any uid accepted".
	if cause != nil {
		return PeerIdentity{UID: -1, Reason: cause.Error()}, fmt.Errorf("peer credential lookup failed: %w", cause)
	}

	// No mechanism available: allowed, but explicitly unverified.
	if creds == nil {
		return PeerIdentity{UID: -1, Reason: unavailableReason}, nil
	}

	if err := checkPeerUID(creds.UID, daemonUID); err != nil {
		return PeerIdentity{
			Method: creds.Method,
			UID:    creds.UID,
			PID:    creds.PID,
			Reason: err.Error(),
		}, err
	}

	return PeerIdentity{
		Verified: true,
		Method:   creds.Method,
		UID:      creds.UID,
		PID:      creds.PID,
	}, nil
}

// identityForConn is the platform-independent entry point the server uses: it
// asks the platform for the peer's credentials and applies the uid rule.
func identityForConn(conn net.Conn, daemonUID int) (PeerIdentity, error) {
	creds, reason, err := peerCredentials(conn)
	return classifyPeer(creds, reason, err, daemonUID)
}
