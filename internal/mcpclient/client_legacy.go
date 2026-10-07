package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// LegacyClient implements the MCP 2025-03-26 / 2025-11-25 stateful Streamable
// HTTP profile.
//
// Session lifecycle (2025-03-26 § Streamable HTTP):
//   - Client POSTs `initialize` with `Accept` and `MCP-Protocol-Version`.
//   - Server MAY assign a session by returning `Mcp-Session-Id` on the
//     InitializeResult response. When it does, the client MUST send that
//     value back in `Mcp-Session-Id` on every subsequent POST/GET/DELETE.
//   - A server that requires sessions SHOULD answer a missing session id
//     with 400; an unknown/expired session id yields 404, in which case the
//     client may re-initialize once (resume) and retry the failed call.
type LegacyClient struct {
	endpoint    string
	headers     map[string]string
	httpClient  *http.Client
	seq         uint64
	initialized bool
	initMu      sync.Mutex
	serverCaps  map[string]any

	sessionMu         sync.RWMutex
	sessionID         string
	negotiatedVersion string
}

// ConnectLegacy creates a legacy MCP 2025-11-25 client (session profile
// introduced in 2025-03-26 and retained in 2025-11-25).
func ConnectLegacy(ctx context.Context, endpoint string, headers map[string]string, httpClient *http.Client) (*LegacyClient, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}

	c := &LegacyClient{
		endpoint:          endpoint,
		headers:           headers,
		httpClient:        httpClient,
		negotiatedVersion: string(ProtocolLegacy2025),
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

func (c *LegacyClient) getSessionID() string {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()
	return c.sessionID
}

func (c *LegacyClient) setSessionID(id string) {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	c.sessionID = id
}

func (c *LegacyClient) getVersion() string {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()
	if c.negotiatedVersion != "" {
		return c.negotiatedVersion
	}
	return string(ProtocolLegacy2025)
}

func (c *LegacyClient) performHandshake(ctx context.Context) error {
	c.initMu.Lock()
	defer c.initMu.Unlock()

	if c.initialized {
		return nil
	}

	version := c.getVersion()
	initReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      c.nextID(),
		Method:  "initialize",
		Params: json.RawMessage(fmt.Sprintf(`{
			"protocolVersion": %q,
			"clientInfo": {
				"name": "litespm-legacy-client",
				"version": "0.1.0"
			},
			"capabilities": {}
		}`, version)),
	}

	resp, respHeaders, err := c.postRPCRaw(ctx, initReq, "")
	if err != nil {
		return err
	}

	// Capture an assigned session id for all later calls.
	if sid := respHeaders.Get("Mcp-Session-Id"); sid != "" {
		c.setSessionID(sid)
	}

	var initRes struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
	}
	_ = json.Unmarshal(resp.Result, &initRes)
	c.serverCaps = initRes.Capabilities
	// Adopt the server-negotiated version for every later request.
	if initRes.ProtocolVersion != "" {
		c.sessionMu.Lock()
		c.negotiatedVersion = initRes.ProtocolVersion
		c.sessionMu.Unlock()
	}

	// Send notifications/initialized notification (no id; 202 with empty
	// body is success per spec, as is 200).
	notifyReq := map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
		"params":  map[string]any{},
	}
	_ = c.postNotification(ctx, notifyReq)

	c.initialized = true
	return nil
}

// postRPCRaw sends one JSON-RPC POST with the legacy session headers and
// returns the decoded response plus the raw response headers (for session
// capture). sessionOverride forces a specific session id for the retry path.
func (c *LegacyClient) postRPCRaw(ctx context.Context, rpcReq JSONRPCRequest, sessionOverride string) (*JSONRPCResponse, http.Header, error) {
	bodyBytes, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	httpReq.Header.Set("MCP-Protocol-Version", c.getVersion())
	sid := sessionOverride
	if sid == "" {
		sid = c.getSessionID()
	}
	if sid != "" {
		httpReq.Header.Set("Mcp-Session-Id", sid)
	}
	for k, v := range c.headers {
		lk := strings.ToLower(strings.TrimSpace(k))
		if lk == "mcp-protocol-version" || lk == "accept" || lk == "mcp-session-id" {
			continue
		}
		httpReq.Header.Set(k, v)
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, nil, err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		return nil, httpResp.Header, fmt.Errorf("http error %d: %s", httpResp.StatusCode, string(respBody))
	}

	respData, err := io.ReadAll(io.LimitReader(httpResp.Body, 16*1024*1024))
	if err != nil {
		return nil, httpResp.Header, err
	}
	if len(respData) == 0 {
		return nil, httpResp.Header, fmt.Errorf("empty response body")
	}

	rpcResp, err := decodeStreamableResponse(respData, httpResp.Header.Get("Content-Type"))
	if err != nil {
		return nil, httpResp.Header, err
	}

	if rpcResp.Error != nil {
		return nil, httpResp.Header, rpcResp.Error
	}

	return rpcResp, httpResp.Header, nil
}

func (c *LegacyClient) postRPC(ctx context.Context, rpcReq JSONRPCRequest) (*JSONRPCResponse, error) {
	resp, _, err := c.postRPCRaw(ctx, rpcReq, "")
	if err == nil {
		return resp, nil
	}
	// Resume path: an expired/unknown session id surfaces as HTTP 404. When
	// we hold a session id, clear it, re-handshake once, and retry once.
	if strings.Contains(err.Error(), "http error 404") && c.getSessionID() != "" {
		c.setSessionID("")
		c.initMu.Lock()
		c.initialized = false
		c.initMu.Unlock()
		if herr := c.performHandshake(ctx); herr != nil {
			return nil, fmt.Errorf("%w (session resume re-initialize failed: %v)", err, herr)
		}
		resp2, _, err2 := c.postRPCRaw(ctx, rpcReq, "")
		if err2 != nil {
			return nil, err2
		}
		return resp2, nil
	}
	return nil, err
}

func (c *LegacyClient) postNotification(ctx context.Context, notifyReq map[string]any) error {
	bodyBytes, _ := json.Marshal(notifyReq)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	httpReq.Header.Set("MCP-Protocol-Version", c.getVersion())
	if sid := c.getSessionID(); sid != "" {
		httpReq.Header.Set("Mcp-Session-Id", sid)
	}
	for k, v := range c.headers {
		lk := strings.ToLower(strings.TrimSpace(k))
		if lk == "mcp-protocol-version" || lk == "accept" || lk == "mcp-session-id" {
			continue
		}
		httpReq.Header.Set(k, v)
	}
	resp, err := c.httpClient.Do(httpReq)
	if err == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		// Notifications succeed with 200 or 202; anything else is an error.
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
			return fmt.Errorf("notification failed with status %d", resp.StatusCode)
		}
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

// CloseSession terminates the server-side session with HTTP DELETE when one
// exists, then clears local state. Stateless servers (no session id) are a
// no-op.
func (c *LegacyClient) CloseSession() error {
	sid := c.getSessionID()
	if sid == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.endpoint, nil)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Mcp-Session-Id", sid)
	httpReq.Header.Set("MCP-Protocol-Version", c.getVersion())
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	c.setSessionID("")
	return nil
}
