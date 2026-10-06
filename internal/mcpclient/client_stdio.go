package mcpclient

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// StdioClient implements an MCP client communicating over local stdio streams.
type StdioClient struct {
	reader  *bufio.Reader
	writer  io.Writer
	writeMu sync.Mutex
	seq     uint64
	pending map[string]chan *JSONRPCResponse
	pendMu  sync.Mutex
	done    chan struct{}
	closeMu sync.Once
}

// ConnectStdio initializes a stdio client over an input reader and output writer.
func ConnectStdio(ctx context.Context, in io.Reader, out io.Writer) (*StdioClient, error) {
	c := &StdioClient{
		reader:  bufio.NewReader(in),
		writer:  out,
		pending: make(map[string]chan *JSONRPCResponse),
		done:    make(chan struct{}),
	}

	go c.readLoop()

	// Perform initialize handshake
	initReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      c.nextID(),
		Method:  "initialize",
		Params: json.RawMessage(`{
			"protocolVersion": "2026-07-28",
			"clientInfo": {
				"name": "litespm-stdio-client",
				"version": "0.1.0"
			},
			"capabilities": {}
		}`),
	}

	if _, err := c.sendRequest(ctx, initReq); err != nil {
		_ = c.CloseSession()
		return nil, fmt.Errorf("stdio initialize failed: %w", err)
	}

	// A server must receive `notifications/initialized` before it will answer
	// anything beyond the handshake. Omitting it leaves a session that connects,
	// initializes, and then hangs on the first tools/list — which is why the
	// legacy and HTTP clients both sent it and this one did not.
	if err := c.sendNotification(ctx, "notifications/initialized", map[string]any{}); err != nil {
		_ = c.CloseSession()
		return nil, fmt.Errorf("stdio initialized notification failed: %w", err)
	}

	return c, nil
}

// sendNotification writes a JSON-RPC notification, which has no id and no reply.
func (c *StdioClient) sendNotification(ctx context.Context, method string, params map[string]any) error {
	if method == "" {
		return fmt.Errorf("notification method is empty")
	}
	if params == nil {
		params = map[string]any{}
	}
	encoded, err := json.Marshal(JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  json.RawMessage(mustMarshal(params)),
	})
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.writer.Write(append(encoded, '\n')); err != nil {
		return err
	}
	if flusher, ok := c.writer.(interface{ Flush() error }); ok {
		return flusher.Flush()
	}
	return nil
}

func mustMarshal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func (c *StdioClient) ProtocolVersion() ProtocolVersion {
	return ProtocolModern2026
}

func (c *StdioClient) nextID() string {
	return fmt.Sprintf("stdio-%d", atomic.AddUint64(&c.seq, 1))
}

func readBoundedLine(r *bufio.Reader, max int) ([]byte, error) {
	var buf []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return nil, err
		}
		if len(buf)+len(chunk) > max {
			return nil, fmt.Errorf("message size exceeds limit of %d bytes", max)
		}
		buf = append(buf, chunk...)
		if !isPrefix {
			break
		}
	}
	return buf, nil
}

func (c *StdioClient) readLoop() {
	for {
		line, err := readBoundedLine(c.reader, 16*1024*1024)
		if err != nil {
			c.closePending(err)
			return
		}

		var resp JSONRPCResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}

		idStr := fmt.Sprintf("%v", resp.ID)
		c.pendMu.Lock()
		ch, exists := c.pending[idStr]
		if exists {
			delete(c.pending, idStr)
		}
		c.pendMu.Unlock()

		if exists {
			ch <- &resp
		}
	}
}

func (c *StdioClient) closePending(err error) {
	c.pendMu.Lock()
	defer c.pendMu.Unlock()
	for id, ch := range c.pending {
		delete(c.pending, id)
		close(ch)
	}
}

func (c *StdioClient) sendRequest(ctx context.Context, req JSONRPCRequest) (*JSONRPCResponse, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')

	idStr := fmt.Sprintf("%v", req.ID)
	respChan := make(chan *JSONRPCResponse, 1)

	c.pendMu.Lock()
	c.pending[idStr] = respChan
	c.pendMu.Unlock()

	c.writeMu.Lock()
	_, err = c.writer.Write(data)
	c.writeMu.Unlock()
	if err != nil {
		c.pendMu.Lock()
		delete(c.pending, idStr)
		c.pendMu.Unlock()
		return nil, err
	}

	select {
	case <-ctx.Done():
		c.pendMu.Lock()
		delete(c.pending, idStr)
		c.pendMu.Unlock()
		return nil, ctx.Err()
	case <-c.done:
		return nil, fmt.Errorf("stdio client closed")
	case resp, ok := <-respChan:
		if !ok {
			return nil, fmt.Errorf("stdio connection terminated")
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp, nil
	}
}

func (c *StdioClient) ListTools(ctx context.Context) ([]ToolDefinition, error) {
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      c.nextID(),
		Method:  "tools/list",
		Params:  json.RawMessage(`{}`),
	}

	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	var result struct {
		Tools []ToolDefinition `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to parse tools/list result: %w", err)
	}

	return result.Tools, nil
}

func (c *StdioClient) CallTool(ctx context.Context, name string, args json.RawMessage) (*ToolResult, error) {
	params := map[string]any{
		"name":      name,
		"arguments": args,
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      c.nextID(),
		Method:  "tools/call",
		Params:  paramsJSON,
	}

	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	var toolRes ToolResult
	if err := json.Unmarshal(resp.Result, &toolRes); err != nil {
		return nil, fmt.Errorf("failed to parse tools/call result: %w", err)
	}

	return &toolRes, nil
}

func (c *StdioClient) SubscribeToListChanges(ctx context.Context, ch chan<- ListChangeEvent) error {
	return nil
}

func (c *StdioClient) CloseSession() error {
	c.closeMu.Do(func() {
		close(c.done)
	})
	return nil
}
