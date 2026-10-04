package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
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
	ctx             context.Context
	cancelCtx       context.CancelFunc
	wg              sync.WaitGroup
	daemonVersion   string
	protocolVersion string
}

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
	}

	// Register mandatory initial handshake
	s.RegisterHandler("daemon.handshake", func(ctx context.Context, params json.RawMessage) (any, *RPCError) {
		var hs HandshakeParams
		if len(params) > 0 {
			if err := json.Unmarshal(params, &hs); err != nil {
				return nil, &RPCError{
					Code:    CodeInvalidParams,
					Message: fmt.Sprintf("invalid handshake params: %v", err),
				}
			}
		}

		return &HandshakeResult{
			DaemonVersion:   s.daemonVersion,
			ProtocolVersion: s.protocolVersion,
			PID:             os.Getpid(),
		}, nil
	})

	return s
}

// RegisterHandler registers a method handler.
func (s *Server) RegisterHandler(method string, handler HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = handler
}

// Serve starts accepting connections on the provided listener.
func (s *Server) Serve(l net.Listener) error {
	s.mu.Lock()
	s.listener = l
	s.mu.Unlock()

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

	for {
		req, err := codec.ReadRequest()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
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

		// Create cancellable context for this request derived from the server lifecycle context
		reqCtx, cancel := context.WithCancel(s.ctx)
		var idKey string
		if req.ID != nil {
			idKey = string(*req.ID)
			cancelMu.Lock()
			if _, exists := cancelFuncs[idKey]; exists {
				cancelMu.Unlock()
				cancel()
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

// Stop gracefully shuts down the server.
func (s *Server) Stop() error {
	var err error
	s.stopOnce.Do(func() {
		s.cancelCtx()
		close(s.quit)

		s.mu.Lock()
		if s.listener != nil {
			err = s.listener.Close()
		}

		for conn := range s.activeConns {
			_ = conn.Close()
		}
		s.mu.Unlock()

		s.wg.Wait()
	})

	return err
}
