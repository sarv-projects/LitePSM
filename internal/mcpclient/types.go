package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// ProtocolVersion represents supported MCP specification profiles.
type ProtocolVersion string

const (
	ProtocolModern2026 ProtocolVersion = "2026-07-28"
	ProtocolLegacy2025 ProtocolVersion = "2025-11-25"
)

// ToolDefinition describes an MCP tool exposed by a downstream provider.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ToolContent describes output content from a tool call.
type ToolContent struct {
	Type string `json:"type"` // "text", "image", "resource"
	Text string `json:"text,omitempty"`
	Data string `json:"data,omitempty"`
}

// ToolResult encapsulates the outcome of a tool execution.
type ToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ListChangeEvent notifies when tools/resources change on a provider.
type ListChangeEvent struct {
	Type string `json:"type"` // "tools", "resources", "prompts"
}

// ClientSession defines the common contract for interacting with an MCP server.
type ClientSession interface {
	ProtocolVersion() ProtocolVersion
	ListTools(ctx context.Context) ([]ToolDefinition, error)
	CallTool(ctx context.Context, name string, args json.RawMessage) (*ToolResult, error)
	SubscribeToListChanges(ctx context.Context, ch chan<- ListChangeEvent) error
	CloseSession() error
}

// RequestMeta encapsulates standard 2026-07-28 stateless metadata.
type RequestMeta struct {
	ProtocolVersion string          `json:"protocolVersion"`
	ClientInfo      ClientInfo      `json:"clientInfo"`
	Capabilities    ClientCaps      `json:"capabilities"`
	ProgressToken   string          `json:"progressToken,omitempty"`
}

// ClientInfo describes LitePSM client identity to downstream providers.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ClientCaps strictly clamps downstream permissions to protect user privacy.
type ClientCaps struct {
	Roots        *RootsCap `json:"roots,omitempty"`        // Clamped: nil or false by default
	Sampling     *struct{} `json:"sampling,omitempty"`     // Clamped: nil by default
	Experimental map[string]any `json:"experimental,omitempty"`
}

// RootsCap defines roots capability settings.
type RootsCap struct {
	ListChanged bool `json:"listChanged"`
}

// DefaultClientCaps returns clamped capabilities for zero-trust downstream MCP servers.
func DefaultClientCaps() ClientCaps {
	// Roots forwarding and sampling are intentionally disabled by default
	return ClientCaps{
		Roots:    nil,
		Sampling: nil,
	}
}

// JSONRPCRequest models outbound JSON-RPC 2.0 requests.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Meta    *RequestMeta    `json:"_meta,omitempty"`
}

// JSONRPCResponse models inbound JSON-RPC 2.0 responses.
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError models standard JSON-RPC errors.
type JSONRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *JSONRPCError) Error() string {
	return fmt.Sprintf("MCP error %d: %s", e.Code, e.Message)
}

// ReadWriteCloser combines io.Reader, io.Writer, and io.Closer.
type ReadWriteCloser interface {
	io.Reader
	io.Writer
	io.Closer
}
