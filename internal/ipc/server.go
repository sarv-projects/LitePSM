package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// HandlerFunc handles an individual RPC method invocation.
type HandlerFunc func(ctx context.Context, params json.RawMessage) (any, *RPCError)

// Server implements a JSON-RPC 2.0 daemon server.
type Server struct {
	handlers        map[string]HandlerFunc
	listener        net.Listener
	activeConns     map[net.Conn]struct{}
	mu              sync.RWMutex
	quit            chan struct{}
	stopOnce        sync.Once
	stopped         bool
	ctx             context.Context
	cancelCtx       context.CancelFunc
	wg              sync.WaitGroup
	daemonVersion   string
	protocolVersion string

	// handshakeTimeout bounds how long a new connection may stay silent (or
	// slow) before sending daemon.handshake; idleTimeout bounds the gap between
	// requests afterwards. handlerSem caps handlers running at once across all
	// connections so one client cannot exhaust the daemon.
	handshakeTimeout time.Duration
	idleTimeout      time.Duration
	handlerSem       chan struct{}
}

// Defaults for the per-connection read deadlines and the handler bound.
const (
	DefaultHandshakeTimeout      = 10 * time.Second
	DefaultIdleTimeout           = 30 * time.Minute
	DefaultMaxConcurrentHandlers = 64
)

// NewServer creates a new IPC JSON-RPC server with baseline handshake registered.
func NewServer(daemonVersion, protocolVersion string) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		handlers:        make(map[string]HandlerFunc),
		activeConns:     make(map[net.Conn]struct{}),
		quit:            make(chan struct{}),
		ctx:             ctx,
		cancelCtx:       cancel,
		daemonVersion:   daemonVersion,
		protocolVersion: protocolVersion,

		handshakeTimeout: DefaultHandshakeTimeout,
		idleTimeout:      DefaultIdleTimeout,
		handlerSem:       make(chan struct{}, DefaultMaxConcurrentHandlers),
	}

	// Register mandatory initial handshake
	s.RegisterHandler("daemon.handshake", func(ctx context.Context, params json.RawMessage) (any, *RPCError) {
		var hs HandshakeParams
		if len(params) == 0 {
			return nil, &RPCError{Code: CodeInvalidParams, Message: "invalid handshake params: clientKind is required"}
		}
		if err := json.Unmarshal(params, &hs); err != nil {
			return nil, &RPCError{
				Code:    CodeInvalidParams,
				Message: fmt.Sprintf("invalid handshake params: %v", err),
			}
		}
		if strings.TrimSpace(hs.ClientKind) == "" {
			return nil, &RPCError{Code: CodeInvalidParams, Message: "invalid handshake params: clientKind is required"}
		}

		return &HandshakeResult{
			DaemonVersion:   s.daemonVersion,
			ProtocolVersion: s.protocolVersion,
			PID:             os.Getpid(),
		}, nil
	})

	return s
}

// SetTimeouts overrides the pre-handshake and idle read deadlines. A
// non-positive value keeps the current setting. Call before Serve.
func (s *Server) SetTimeouts(handshake, idle time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if handshake > 0 {
		s.handshakeTimeout = handshake
	}
	if idle > 0 {
		s.idleTimeout = idle
	}
}

// SetMaxConcurrentHandlers bounds concurrently running handlers. Requests
// beyond the bound are answered with CodeRateLimited rather than queued. A
// non-positive n keeps the current bound. Call before Serve.
func (s *Server) SetMaxConcurrentHandlers(n int) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlerSem = make(chan struct{}, n)
}

// RegisterHandler registers a method handler.
func (s *Server) RegisterHandler(method string, handler HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = handler
}

// Serve starts accepting connections on the provided listener. It returns when
// the listener is closed by Stop or fails. The accept loop itself is tracked in
// the server WaitGroup so Stop's Wait can never race a late wg.Add from an
// accepted connection, and a Serve that starts after Stop returns immediately
// instead of registering work Stop has already finished waiting for.
func (s *Server) Serve(l net.Listener) error {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		_ = l.Close()
		return net.ErrClosed
	}
	s.listener = l
	// Register the accept loop before releasing the mutex. Stop sets stopped
	// and then Waits while holding the same mutex ordering, so a Serve that
	// wins the lock has its Add observed by Wait, and a Stop that wins the
	// lock prevents this Add entirely.
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()

	for {
		conn, err := l.Accept()
		if err != nil {
			select {
			case <-s.quit:
				return nil
			default:
				return err
			}
		}

		s.mu.Lock()
		s.activeConns[conn] = struct{}{}
		s.mu.Unlock()

		// The accept loop's own count is still held here, so the WaitGroup
		// counter is never zero while this Add runs; it cannot race a
		// concurrent Stop's Wait.
		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			s.handleConnection(c)
		}(conn)
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer func() {
		conn.Close()
		s.mu.Lock()
		delete(s.activeConns, conn)
		s.mu.Unlock()
	}()

	codec := NewLineDelimitedCodec(conn)
	cancelFuncs := make(map[string]context.CancelFunc)
	var cancelMu sync.Mutex

	s.mu.RLock()
	handshakeTimeout, idleTimeout, sem := s.handshakeTimeout, s.idleTimeout, s.handlerSem
	s.mu.RUnlock()

	handshaken := false
	writeRPCError := func(id *json.RawMessage, code int, msg string) {
		if id == nil {
			return
		}
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_ = codec.WriteResponse(&Response{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: code, Message: msg}})
		_ = conn.SetWriteDeadline(time.Time{})
	}

	for {
		// Per-connection read deadline: a connection must handshake promptly,
		// and an idle one is eventually reclaimed instead of pinning a
		// goroutine and descriptor forever.
		if handshaken {
			_ = conn.SetReadDeadline(time.Now().Add(idleTimeout))
		} else {
			_ = conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
		}
		req, err := codec.ReadRequest()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrDeadlineExceeded) {
				return
			}
			// Write parse error with deadline
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_ = codec.WriteResponse(&Response{
				JSONRPC: "2.0",
				Error: &RPCError{
					Code:    CodeParseError,
					Message: err.Error(),
				},
			})
			_ = conn.SetWriteDeadline(time.Time{})
			return
		}

		// Handle cancellation notification
		if req.Method == "$/cancelRequest" {
			var cancelParams CancelParams
			if err := json.Unmarshal(req.Params, &cancelParams); err == nil && len(cancelParams.ID) > 0 {
				idStr := string(cancelParams.ID)
				cancelMu.Lock()
				if cancel, exists := cancelFuncs[idStr]; exists {
					cancel()
					delete(cancelFuncs, idStr)
				}
				cancelMu.Unlock()
			}
			continue
		}

		// The handshake is the connection's first request: every other method
		// is refused until it has succeeded on this connection.
		if req.Method == "daemon.handshake" {
			s.mu.RLock()
			hsHandler := s.handlers[req.Method]
			s.mu.RUnlock()
			result, rpcErr := hsHandler(s.ctx, req.Params)
			if rpcErr == nil {
				handshaken = true
			}
			if req.ID != nil {
				resp := &Response{JSONRPC: "2.0", ID: req.ID, Error: rpcErr}
				if rpcErr == nil {
					if b, merr := json.Marshal(result); merr != nil {
						resp.Error = &RPCError{Code: CodeInternalError, Message: fmt.Sprintf("failed to marshal response result: %v", merr)}
					} else {
						resp.Result = json.RawMessage(b)
					}
				}
				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				_ = codec.WriteResponse(resp)
				_ = conn.SetWriteDeadline(time.Time{})
			}
			continue
		}
		if !handshaken {
			writeRPCError(req.ID, CodeUnauthorized, "daemon.handshake is required before any other method")
			continue
		}

		// Look up handler
		s.mu.RLock()
		handler, exists := s.handlers[req.Method]
		s.mu.RUnlock()

		if !exists {
			if req.ID != nil {
				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				_ = codec.WriteResponse(&Response{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error: &RPCError{
						Code:    CodeMethodNotFound,
						Message: fmt.Sprintf("method %q not found", req.Method),
					},
				})
				_ = conn.SetWriteDeadline(time.Time{})
			}
			continue
		}

		// Bound concurrent handlers: shed load with an explicit rate-limit
		// error instead of spawning without limit.
		select {
		case sem <- struct{}{}:
		default:
			writeRPCError(req.ID, CodeRateLimited, "too many concurrent requests; retry later")
			continue
		}

		// Create cancellable context for this request derived from the server lifecycle context
		reqCtx, cancel := context.WithCancel(s.ctx)
		var idKey string
		if req.ID != nil {
			idKey = string(*req.ID)
			cancelMu.Lock()
			if _, exists := cancelFuncs[idKey]; exists {
				cancelMu.Unlock()
				cancel()
				<-sem
				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				_ = codec.WriteResponse(&Response{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error: &RPCError{
						Code:    CodeInvalidRequest,
						Message: fmt.Sprintf("duplicate request id %s already in flight", idKey),
					},
				})
				_ = conn.SetWriteDeadline(time.Time{})
				continue
			}
			cancelFuncs[idKey] = cancel
			cancelMu.Unlock()
		}

		s.wg.Add(1)
		go func(r *Request, ctx context.Context, idStr string, cancel context.CancelFunc) {
			defer s.wg.Done()
			defer func() { <-sem }()
			// Always release the per-request context, including notifications
			// (which are never stored in cancelFuncs) so no context leaks.
			defer cancel()
			defer func() {
				if idStr != "" {
					cancelMu.Lock()
					delete(cancelFuncs, idStr)
					cancelMu.Unlock()
				}
			}()

			result, rpcErr := handler(ctx, r.Params)

			// Notifications (r.ID == nil) do not send responses
			if r.ID == nil {
				return
			}

			resp := &Response{
				JSONRPC: "2.0",
				ID:      r.ID,
			}

			if rpcErr != nil {
				resp.Error = rpcErr
			} else {
				resBytes, err := json.Marshal(result)
				if err != nil {
					resp.Error = &RPCError{
						Code:    CodeInternalError,
						Message: fmt.Sprintf("failed to marshal response result: %v", err),
					}
				} else {
					raw := json.RawMessage(resBytes)
					resp.Result = raw
				}
			}

			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_ = codec.WriteResponse(resp)
			_ = conn.SetWriteDeadline(time.Time{})
		}(req, reqCtx, idKey, cancel)
	}
}

// Stop gracefully shuts down the server. It is idempotent: only the first call
// closes the listener and connections, and every caller blocks until the accept
// loop and all in-flight handlers have returned.
func (s *Server) Stop() error {
	var err error
	s.stopOnce.Do(func() {
		s.mu.Lock()
		// Mark stopped under the same lock Serve uses to register work. A
		// Serve that runs after this point sees stopped and registers nothing,
		// so the Wait below cannot miss a late Add.
		s.stopped = true
		s.cancelCtx()
		close(s.quit)

		if s.listener != nil {
			err = s.listener.Close()
		}

		for conn := range s.activeConns {
			_ = conn.Close()
		}
		s.mu.Unlock()

		// Wait after releasing the mutex: handleConnection takes the lock to
		// unregister a connection, so holding it here would deadlock, and the
		// closed listener/connections above are what unblock the accept loop.
		s.wg.Wait()
	})

	return err
}
