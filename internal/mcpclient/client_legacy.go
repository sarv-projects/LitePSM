package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// LegacyClient implements the legacy MCP 2025-11-25 stateful handshake profile.
type LegacyClient struct {
	endpoint     string
	headers      map[string]string
	httpClient   *http.Client
	seq          uint64
	initialized  bool
	initMu       sync.Mutex
	serverCaps   map[string]any
}

// ConnectLegacy creates a legacy MCP 2025-11-25 client.
func ConnectLegacy(ctx context.Context, endpoint string, headers map[string]string, httpClient *http.Client) (*LegacyClient, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}

	c := &LegacyClient{
		endpoint:   endpoint,
		headers:    headers,
		httpClient: httpClient,
	}

	// Perform stateful handshake
	if err := c.performHandshake(ctx); err != nil {
		return nil, fmt.Errorf("legacy handshake failed: %w", err)
	}

	return c, nil
}

func (c *LegacyClient) ProtocolVersion() ProtocolVersion {
	return ProtocolLegacy2025
}

func (c *LegacyClient) nextID() uint64 {
	return atomic.AddUint64(&c.seq, 1)
}

func (c *LegacyClient) performHandshake(ctx context.Context) error {
	c.initMu.Lock()
	defer c.initMu.Unlock()

	if c.initialized {
		return nil
	}

	initReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      c.nextID(),
		Method:  "initialize",
		Params: json.RawMessage(`{
			"protocolVersion": "2025-11-25",
			"clientInfo": {
				"name": "litepsm-legacy-client",
				"version": "0.1.0"
			},
			"capabilities": {}
		}`),
	}

	resp, err := c.postRPC(ctx, initReq)
	if err != nil {
		return err
	}

	var initRes struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
	}
	_ = json.Unmarshal(resp.Result, &initRes)
	c.serverCaps = initRes.Capabilities

	// Send notifications/initialized notification
	notifyReq := map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
		"params":  map[string]any{},
	}
	_ = c.postNotification(ctx, notifyReq)

	c.initialized = true
	return nil
}

func (c *LegacyClient) postRPC(ctx context.Context, rpcReq JSONRPCRequest) (*JSONRPCResponse, error) {
	bodyBytes, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		return nil, fmt.Errorf("http error %d: %s", httpResp.StatusCode, string(respBody))
	}

	respData, err := io.ReadAll(io.LimitReader(httpResp.Body, 16*1024*1024))
	if err != nil {
		return nil, err
	}

	var rpcResp JSONRPCResponse
	if err := json.Unmarshal(respData, &rpcResp); err != nil {
		return nil, err
	}

	if rpcResp.Error != nil {
		return nil, rpcResp.Error
	}

	return &rpcResp, nil
}

func (c *LegacyClient) postNotification(ctx context.Context, notifyReq map[string]any) error {
	bodyBytes, _ := json.Marshal(notifyReq)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := c.httpClient.Do(httpReq)
	if err == nil {
		_ = resp.Body.Close()
	}
	return err
}

func (c *LegacyClient) ListTools(ctx context.Context) ([]ToolDefinition, error) {
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      c.nextID(),
		Method:  "tools/list",
		Params:  json.RawMessage(`{}`),
	}

	resp, err := c.postRPC(ctx, req)
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

func (c *LegacyClient) CallTool(ctx context.Context, name string, args json.RawMessage) (*ToolResult, error) {
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

	resp, err := c.postRPC(ctx, req)
	if err != nil {
		return nil, err
	}

	var toolRes ToolResult
	if err := json.Unmarshal(resp.Result, &toolRes); err != nil {
		return nil, fmt.Errorf("failed to parse tools/call result: %w", err)
	}

	return &toolRes, nil
}

func (c *LegacyClient) SubscribeToListChanges(ctx context.Context, ch chan<- ListChangeEvent) error {
	return nil
}

func (c *LegacyClient) CloseSession() error {
	return nil
}
