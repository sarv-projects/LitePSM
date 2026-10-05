package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
)

// Client represents a connected JSON-RPC 2.0 client session.
type Client struct {
	conn    net.Conn
	codec   *LineDelimitedCodec
	seq     uint64
	pending map[string]chan *Response
	mu      sync.Mutex
	done    chan struct{}
}

// Dial connects to a LiteSPM daemon IPC endpoint.
func Dial(endpoint string) (*Client, error) {
	conn, err := DialIPC(endpoint)
	if err != nil {
		return nil, err
	}

	c := &Client{
		conn:    conn,
		codec:   NewLineDelimitedCodec(conn),
		pending: make(map[string]chan *Response),
		done:    make(chan struct{}),
	}

	go c.readLoop()
	return c, nil
}

// NewClientFromConn wraps an existing net.Conn into a Client (useful for in-memory or pipe testing).
func NewClientFromConn(conn net.Conn) *Client {
	c := &Client{
		conn:    conn,
		codec:   NewLineDelimitedCodec(conn),
		pending: make(map[string]chan *Response),
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c
}

func (c *Client) readLoop() {
	for {
		resp, err := c.codec.ReadResponse()
		if err != nil {
			c.closePending(err)
			return
		}

		if resp.ID != nil {
			idStr := string(*resp.ID)
			c.mu.Lock()
			ch, ok := c.pending[idStr]
			if ok {
				delete(c.pending, idStr)
			}
			c.mu.Unlock()

			if ok {
				ch <- resp
			}
		}
	}
}

func (c *Client) closePending(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, ch := range c.pending {
		ch <- &Response{
			JSONRPC: "2.0",
			Error: &RPCError{
				Code:    CodeInternalError,
				Message: fmt.Sprintf("connection closed: %v", err),
			},
		}
	}
	c.pending = make(map[string]chan *Response)
}

// Handshake performs the mandatory initial handshake with the daemon.
func (c *Client) Handshake(ctx context.Context, clientKind, hostID string) (*HandshakeResult, error) {
	params := HandshakeParams{
		ClientVersion: "0.1.0",
		ClientKind:    clientKind,
		HostID:        hostID,
		PID:           os.Getpid(),
	}

	var res HandshakeResult
	if err := c.Call(ctx, "daemon.handshake", params, &res); err != nil {
		return nil, fmt.Errorf("handshake failed: %w", err)
	}
	return &res, nil
}

// Call sends a JSON-RPC request and blocks until a response is received or ctx is cancelled.
func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	seq := atomic.AddUint64(&c.seq, 1)
	idRaw := json.RawMessage(fmt.Sprintf("%d", seq))
	idStr := string(idRaw)

	var paramsRaw json.RawMessage
	if params != nil {
		bytes, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("failed to marshal params: %w", err)
		}
		paramsRaw = bytes
	}

	req := &Request{
		JSONRPC: "2.0",
		ID:      &idRaw,
		Method:  method,
		Params:  paramsRaw,
	}

	ch := make(chan *Response, 1)
	c.mu.Lock()
	c.pending[idStr] = ch
	c.mu.Unlock()

	if err := c.codec.WriteRequest(req); err != nil {
		c.mu.Lock()
		delete(c.pending, idStr)
		c.mu.Unlock()
		return fmt.Errorf("failed to write request: %w", err)
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, idStr)
		c.mu.Unlock()

		// Send cancellation notification
		_ = c.Notify("$/cancelRequest", CancelParams{ID: idRaw})
		return ctx.Err()

	case resp := <-ch:
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, result); err != nil {
				return fmt.Errorf("failed to unmarshal result: %w", err)
			}
		}
		return nil
	}
}

// Notify sends a JSON-RPC notification (no ID, no response expected).
func (c *Client) Notify(method string, params any) error {
	var paramsRaw json.RawMessage
	if params != nil {
		bytes, err := json.Marshal(params)
		if err != nil {
			return err
		}
		paramsRaw = bytes
	}

	req := &Request{
		JSONRPC: "2.0",
		Method:  method,
		Params:  paramsRaw,
	}

	return c.codec.WriteRequest(req)
}

// Close closes the client connection.
func (c *Client) Close() error {
	return c.conn.Close()
}
