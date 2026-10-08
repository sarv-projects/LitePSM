package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sarv-projects/litespm/internal/egress"
)

// StreamableHTTPClient implements the MCP 2026-07-28 modern stateless profile
// over Streamable HTTP.
//
// 2026-07-28 notes (SEP-2567 sessions removed, SEP-2575 handshake removed):
//   - No `initialize` handshake and NO `Mcp-Session-Id` header on any request.
//     Sessions were removed at the protocol layer; every request is
//     self-contained. A server that mints a session id is speaking the older
//     (2025-03-26 / 2025-11-25) profile — use LegacyClient for that.
//   - Every POST carries the protocol version twice: in the
//     `MCP-Protocol-Version` header AND in the body `_meta.protocolVersion`.
//     The two MUST match; a mismatch is a hard error, never a fallback.
//   - Every POST carries `Accept: application/json, text/event-stream` so a
//     stateless server may answer with plain JSON or an SSE stream.
type StreamableHTTPClient struct {
	endpoint   string
	headers    map[string]string
	httpClient *http.Client
	seq        uint64
	clientInfo ClientInfo
}

// remoteEgressPolicy is the egress posture for remote (URL) MCP endpoints:
// https-only with a loopback-http exception (a local dev endpoint — and the
// hermetic httptest doubles — keep working), at most 3 redirect hops,
// same-origin redirects only (a remote client may carry Authorization/session
// headers, which a cross-host hop would forward — Decision 3), private
// addresses refused by default (Decision 4: AllowPrivate is the
// consent opt-in wired by a later phase), no configured exception host,
// checked-IP dial, and the historical 60s client timeout. Response bodies
// stay bounded at 16 MiB, matching the reads in sendRequest/postRPCRaw.
func remoteEgressPolicy() egress.Policy {
	return egress.Policy{
		AllowLoopbackHTTP:   true,
		MaxRedirects:        3,
		SameOriginRedirects: true,
		AllowPrivate:        false,
		AllowedPrivateHost:  "",
		Timeout:             60 * time.Second,
		MaxBodyBytes:        16 << 20,
	}
}

// ConnectStreamableHTTP creates a modern MCP 2026-07-28 client connected to a Streamable HTTP endpoint.
//
// A nil httpClient builds a guarded client (fail-closed default: scheme,
// destination and redirect policy enforced at every layer); a
// caller-supplied client is wrapped in the same guard rather than trusted —
// the same pattern catalog.NewClientWithTimeout has always applied.
func ConnectStreamableHTTP(ctx context.Context, endpoint string, headers map[string]string, httpClient *http.Client) (*StreamableHTTPClient, error) {
	httpClient = egress.WrapClient(httpClient, remoteEgressPolicy())

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

	version := string(ProtocolModern2026)
	meta := &RequestMeta{
		ProtocolVersion: version,
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

	// Standard content type plus the mandatory dual Accept: a 2026-07-28
	// server may answer with plain JSON or upgrade the POST response to an
	// SSE stream.
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")

	// MCP 2026-07-28 header mirroring. MCP-Protocol-Version MUST match the
	// body _meta.protocolVersion on every POST.
	httpReq.Header.Set("MCP-Protocol-Version", version)
	httpReq.Header.Set("Mcp-Method", method)
	if toolName != "" {
		httpReq.Header.Set("Mcp-Name", toolName)
	}

	// User-provided headers (e.g. Auth tokens) must not be able to downgrade
	// the protocol version or Accept contract.
	for k, v := range c.headers {
		lk := strings.ToLower(strings.TrimSpace(k))
		if lk == "mcp-protocol-version" || lk == "accept" {
			continue
		}
		httpReq.Header.Set(k, v)
	}

	// Defensive header/body match: the header we just set must equal _meta.
	if hv := httpReq.Header.Get("MCP-Protocol-Version"); hv != meta.ProtocolVersion {
		return nil, fmt.Errorf("MCP header/body version mismatch: header %q vs _meta %q", hv, meta.ProtocolVersion)
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

	// A strict server echoes the negotiated version. When it does, it MUST
	// match what we sent; a version disagreement is a protocol error, not
	// something to silently accept.
	if rv := httpResp.Header.Get("MCP-Protocol-Version"); rv != "" && rv != version {
		return nil, fmt.Errorf("MCP protocol version mismatch: sent %q but server answered %q", version, rv)
	}

	respData, err := io.ReadAll(io.LimitReader(httpResp.Body, 16*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	rpcResp, err := decodeStreamableResponse(respData, httpResp.Header.Get("Content-Type"))
	if err != nil {
		return nil, err
	}

	if rpcResp.Error != nil {
		return nil, rpcResp.Error
	}

	return rpcResp, nil
}

// decodeStreamableResponse decodes a POST response that may be plain JSON or
// an SSE stream (Content-Type: text/event-stream). For SSE, each `data:` line
// carries one JSON-RPC message; the last complete message wins.
func decodeStreamableResponse(data []byte, contentType string) (*JSONRPCResponse, error) {
	if !strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		// Fast path: plain JSON. Fall back to SSE scanning when the body
		// looks like an event stream even without the content type.
		var rpcResp JSONRPCResponse
		if err := json.Unmarshal(data, &rpcResp); err == nil {
			return &rpcResp, nil
		}
		if bytes.Contains(data, []byte("data:")) {
			return decodeSSEPayload(data)
		}
		var rpcResp2 JSONRPCResponse
		if err := json.Unmarshal(data, &rpcResp2); err != nil {
			return nil, fmt.Errorf("failed to decode JSON-RPC response: %w", err)
		}
		return &rpcResp2, nil
	}
	return decodeSSEPayload(data)
}

func decodeSSEPayload(data []byte) (*JSONRPCResponse, error) {
	var last *JSONRPCResponse
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") || strings.HasPrefix(line, "retry:") || strings.HasPrefix(line, "id:") {
			continue
		}
		payload := line
		if strings.HasPrefix(line, "data:") {
			payload = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var candidate JSONRPCResponse
		if err := json.Unmarshal([]byte(payload), &candidate); err != nil {
			continue
		}
		// Accept the first message with an id or an error; notifications
		// without ids are skipped.
		if candidate.ID != nil || candidate.Error != nil {
			c := candidate
			last = &c
		}
	}
	if last == nil {
		return nil, fmt.Errorf("SSE stream contained no JSON-RPC response")
	}
	return last, nil
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

// CloseSession terminates the client session. Stateless 2026-07-28 has no
// session to close; this is a no-op for interface symmetry.
func (c *StreamableHTTPClient) CloseSession() error {
	return nil
}
