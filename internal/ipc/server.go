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
	daemonVersion   string
	protocolVersion string
}

// NewServer creates a new IPC JSON-RPC server with baseline handshake registered.
func NewServer(daemonVersion, protocolVersion string) *Server {
	s := &Server{
		handlers:        make(map[string]HandlerFunc),
		activeConns:     make(map[net.Conn]struct{}),
		quit:            make(chan struct{}),
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

		go s.handleConnection(conn)
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
			// Write parse error
			_ = codec.WriteResponse(&Response{
				JSONRPC: "2.0",
				Error: &RPCError{
					Code:    CodeParseError,
					Message: err.Error(),
				},
			})
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
				_ = codec.WriteResponse(&Response{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error: &RPCError{
						Code:    CodeMethodNotFound,
						Message: fmt.Sprintf("method %q not found", req.Method),
					},
				})
			}
			continue
		}

		// Create cancellable context for this request
		reqCtx, cancel := context.WithCancel(context.Background())
		var idKey string
		if req.ID != nil {
			idKey = string(*req.ID)
			cancelMu.Lock()
			cancelFuncs[idKey] = cancel
			cancelMu.Unlock()
		}

		go func(r *Request, ctx context.Context, idStr string) {
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

			_ = codec.WriteResponse(resp)
		}(req, reqCtx, idKey)
	}
}

// Stop gracefully shuts down the server.
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	close(s.quit)

	var err error
	if s.listener != nil {
		err = s.listener.Close()
	}

	for conn := range s.activeConns {
		_ = conn.Close()
	}

	return err
}
