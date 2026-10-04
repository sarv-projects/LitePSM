package ipc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Standard JSON-RPC 2.0 Error Codes
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603

	// LitePSM Specific RPC Error Codes
	CodeUnauthorized = -32001
	CodePlanStale    = -32002
	CodeSchemaDrift  = -32003
	CodeRateLimited  = -32004

	// MaxMessageSize sets the upper bound for a single JSON-RPC message (16 MiB).
	MaxMessageSize = 16 * 1024 * 1024
)

// Request defines a standard JSON-RPC 2.0 request or notification.
type Request struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"` // nil for notifications
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

// Response defines a standard JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *RPCError        `json:"error,omitempty"`
}

// RPCError defines a structured JSON-RPC 2.0 error.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("rpc error: code=%d message=%s", e.Code, e.Message)
}

// HandshakeParams defines mandatory client metadata sent upon connection.
type HandshakeParams struct {
	ClientVersion string `json:"clientVersion"`
	ClientKind    string `json:"clientKind"`       // "cli" | "bridge"
	HostID        string `json:"hostId,omitempty"` // "cline" | "codex" | "grok" | "pi" etc.
	PID           int    `json:"pid"`
}

// HandshakeResult defines daemon metadata returned upon successful handshake.
type HandshakeResult struct {
	DaemonVersion   string `json:"daemonVersion"`
	ProtocolVersion string `json:"protocolVersion"`
	PID             int    `json:"pid"`
}

// CancelParams defines parameters for $/cancelRequest notification.
type CancelParams struct {
	ID json.RawMessage `json:"id"`
}

// LineDelimitedCodec manages line-delimited streaming of JSON-RPC 2.0 messages.
type LineDelimitedCodec struct {
	reader *bufio.Reader
	writer io.Writer
	mu     sync.Mutex
}

// NewLineDelimitedCodec creates a new codec over an io.ReadWriter.
func NewLineDelimitedCodec(rw io.ReadWriter) *LineDelimitedCodec {
	return &LineDelimitedCodec{
		reader: bufio.NewReader(rw),
		writer: rw,
	}
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

// ReadRequest decodes the next JSON-RPC request from the line stream.
func (c *LineDelimitedCodec) ReadRequest() (*Request, error) {
	line, err := readBoundedLine(c.reader, MaxMessageSize)
	if err != nil {
		return nil, err
	}

	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		return nil, fmt.Errorf("malformed JSON-RPC request: %w", err)
	}
	if req.JSONRPC != "2.0" {
		return nil, fmt.Errorf("unsupported JSON-RPC version %q, expected '2.0'", req.JSONRPC)
	}
	return &req, nil
}

// ReadResponse decodes the next JSON-RPC response from the line stream.
func (c *LineDelimitedCodec) ReadResponse() (*Response, error) {
	line, err := readBoundedLine(c.reader, MaxMessageSize)
	if err != nil {
		return nil, err
	}

	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("malformed JSON-RPC response: %w", err)
	}
	return &resp, nil
}

// WriteResponse sends a JSON-RPC response followed by a newline.
func (c *LineDelimitedCodec) WriteResponse(resp *Response) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	resp.JSONRPC = "2.0"
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = c.writer.Write(data)
	return err
}

// WriteRequest sends a JSON-RPC request followed by a newline.
func (c *LineDelimitedCodec) WriteRequest(req *Request) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	req.JSONRPC = "2.0"
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = c.writer.Write(data)
	return err
}
