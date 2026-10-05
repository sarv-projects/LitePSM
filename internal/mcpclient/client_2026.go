package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

// StreamableHTTPClient implements the MCP 2026-07-28 modern stateless profile over Streamable HTTP.
type StreamableHTTPClient struct {
	endpoint   string
	headers    map[string]string
	httpClient *http.Client
	seq        uint64
	clientInfo ClientInfo
}

// ConnectStreamableHTTP creates a modern MCP 2026-07-28 client connected to a Streamable HTTP endpoint.
func ConnectStreamableHTTP(ctx context.Context, endpoint string, headers map[string]string, httpClient *http.Client) (*StreamableHTTPClient, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}

	c := &StreamableHTTPClient{
		endpoint:   endpoint,
		headers:    headers,
		httpClient: httpClient,
		clientInfo: ClientInfo{
			Name:    "litespm-control-plane",
			Version: "0.1.0",
		},
	}

	return c, nil
}

func (c *StreamableHTTPClient) ProtocolVersion() ProtocolVersion {
	return ProtocolModern2026
}

func (c *StreamableHTTPClient) nextID() string {
	return fmt.Sprintf("req-%d", atomic.AddUint64(&c.seq, 1))
}

// sendRequest executes a stateless HTTP POST request with header mirroring and metadata.
func (c *StreamableHTTPClient) sendRequest(ctx context.Context, method string, params any, toolName string) (*JSONRPCResponse, error) {
	reqID := c.nextID()

	var paramsRaw json.RawMessage
	if params != nil {
		pBytes, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal params: %w", err)
		}
		paramsRaw = pBytes
	}

	meta := &RequestMeta{
		ProtocolVersion: string(ProtocolModern2026),
		ClientInfo:      c.clientInfo,
		Capabilities:    DefaultClientCaps(),
	}

	rpcReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      reqID,
		Method:  method,
		Params:  paramsRaw,
		Meta:    meta,
	}

	bodyBytes, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	// Standard content type
	httpReq.Header.Set("Content-Type", "application/json")

	// MCP 2026-07-28 Header Mirroring
	httpReq.Header.Set("Mcp-Method", method)
	if toolName != "" {
		httpReq.Header.Set("Mcp-Name", toolName)
	}
	httpReq.Header.Set("Mcp-Protocol-Version", string(ProtocolModern2026))

	// User-provided headers (e.g. Auth tokens)
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		return nil, fmt.Errorf("http error %d: %s", httpResp.StatusCode, string(respBody))
	}

	respData, err := io.ReadAll(io.LimitReader(httpResp.Body, 16*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var rpcResp JSONRPCResponse
	if err := json.Unmarshal(respData, &rpcResp); err != nil {
		return nil, fmt.Errorf("failed to decode JSON-RPC response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, rpcResp.Error
	}

	return &rpcResp, nil
}

// ListTools queries the provider for available tools in modern stateless mode.
func (c *StreamableHTTPClient) ListTools(ctx context.Context) ([]ToolDefinition, error) {
	resp, err := c.sendRequest(ctx, "tools/list", map[string]any{}, "")
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

// CallTool invokes a tool on the remote server.
func (c *StreamableHTTPClient) CallTool(ctx context.Context, name string, args json.RawMessage) (*ToolResult, error) {
	params := map[string]any{
		"name":      name,
		"arguments": args,
	}

	resp, err := c.sendRequest(ctx, "tools/call", params, name)
	if err != nil {
		return nil, err
	}

	var toolRes ToolResult
	if err := json.Unmarshal(resp.Result, &toolRes); err != nil {
		return nil, fmt.Errorf("failed to parse tools/call result: %w", err)
	}

	return &toolRes, nil
}

// SubscribeToListChanges registers for list change notifications using modern subscriptions/listen.
func (c *StreamableHTTPClient) SubscribeToListChanges(ctx context.Context, ch chan<- ListChangeEvent) error {
	_, err := c.sendRequest(ctx, "subscriptions/listen", map[string]any{
		"types": []string{"tools"},
	}, "")
	return err
}

// CloseSession terminates the client session.
func (c *StreamableHTTPClient) CloseSession() error {
	return nil
}
