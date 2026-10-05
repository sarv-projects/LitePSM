package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/sarv-projects/litespm/internal/ipc"
)

// pipeListener is an in-memory net.Listener that hands a single pre-connected
// pipe to the accepting server, so bridge tests can exercise a real ipc.Server
// without opening sockets.
type pipeListener struct {
	conns chan net.Conn
	once  sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn, 1)}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	conn, ok := <-l.conns
	if !ok {
		return nil, net.ErrClosed
	}
	return conn, nil
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.conns) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

// newDaemonBackedShim starts a real ipc.Server with the given handlers on an
// in-memory pipe and returns a shim wired to it exactly as a live daemon
// connection would be. Handlers are the test's stand-in for daemon handlers;
// the shim itself must never invent data.
func newDaemonBackedShim(t *testing.T, handlers map[string]ipc.HandlerFunc) *Shim {
	t.Helper()

	server := ipc.NewServer("test-daemon", "2026-07-28")
	for method, handler := range handlers {
		server.RegisterHandler(method, handler)
	}

	listener := newPipeListener()
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()

	serverConn, clientConn := net.Pipe()
	listener.conns <- serverConn

	client := ipc.NewClientFromConn(clientConn)
	t.Cleanup(func() {
		_ = client.Close()
		// Close the listener first: it is idempotent, and it also covers the
		// race where Serve has not been scheduled yet when Stop runs.
		_ = listener.Close()
		_ = server.Stop()
		<-serveDone
	})

	return NewShim("test-host", client, nil, nil)
}

func TestBridge_InitializeAndListTools(t *testing.T) {
	ctx := context.Background()
	shim := NewShim("test-host", nil, nil, nil)

	// 1. Initialize
	idRaw := json.RawMessage(`1`)
	initReq := &ipc.Request{
		JSONRPC: "2.0",
		ID:      &idRaw,
		Method:  "initialize",
	}

	initResp := shim.HandleRequest(ctx, initReq)
	if initResp == nil || initResp.Error != nil {
		t.Fatalf("unexpected initialize error: %+v", initResp)
	}

	var initResult map[string]any
	if err := json.Unmarshal(initResp.Result, &initResult); err != nil {
		t.Fatal(err)
	}
	if initResult["protocolVersion"] != "2026-07-28" {
		t.Errorf("unexpected protocol version: %v", initResult["protocolVersion"])
	}

	// 2. Tools List
	listReq := &ipc.Request{
		JSONRPC: "2.0",
		ID:      &idRaw,
		Method:  "tools/list",
	}
	listResp := shim.HandleRequest(ctx, listReq)
	if listResp == nil || listResp.Error != nil {
		t.Fatalf("unexpected tools/list error: %+v", listResp)
	}

	var listResult struct {
		Tools []MCPTool `json:"tools"`
	}
	if err := json.Unmarshal(listResp.Result, &listResult); err != nil {
		t.Fatal(err)
	}

	if len(listResult.Tools) != 12 {
		t.Errorf("expected exactly 12 canonical tools, got %d", len(listResult.Tools))
	}

	toolMap := make(map[string]bool)
	for _, tool := range listResult.Tools {
		toolMap[tool.Name] = true
	}

	expectedTools := []string{
		"search_catalog", "get_extension", "prepare_install", "request_install",
		"list_installed", "search_capabilities", "describe_capability",
		"load_skill", "read_skill_resource", "invoke_capability",
		"get_invocation", "cancel_invocation",
	}

	for _, name := range expectedTools {
		if !toolMap[name] {
			t.Errorf("missing canonical tool: %s", name)
		}
	}
}

func TestBridge_ToolDispatchAndErrorFormatting(t *testing.T) {
	ctx := context.Background()

	// Daemon-backed shim: the connected path must relay real handler output,
	// never canned text. The handlers below stand in for the daemon's
	// catalog.search / tools.list RPCs over the real IPC wire format.
	shim := newDaemonBackedShim(t, map[string]ipc.HandlerFunc{
		"catalog.search": func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
			var req struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, &ipc.RPCError{Code: ipc.CodeInvalidParams, Message: err.Error()}
			}
			return map[string]any{
				"count": 1,
				"results": []any{map[string]any{
					"id":    "mcp:github:modelcontextprotocol:servers:postgres",
					"name":  "postgres",
					"query": req.Query,
				}},
			}, nil
		},
		"tools.list": func(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
			return map[string]any{
				"count": 2,
				"installs": []CapabilityItem{
					{
						ID:        "mcp:github:modelcontextprotocol:servers:postgres",
						Name:      "postgres",
						Kind:      "mcp",
						Summary:   "PostgreSQL Read/Write Inspection Tool",
						Transport: "stdio",
						Status:    StatusReady,
						Verified:  true,
					},
					{
						ID:         "external:native:fetch",
						Name:       "fetch",
						Kind:       "mcp",
						Summary:    "Pre-existing host fetch utility",
						Status:     StatusReady,
						IsExternal: true,
					},
				},
			}, nil
		},
	})

	idRaw := json.RawMessage(`2`)

	// 1. Call search_catalog
	callParams, _ := json.Marshal(map[string]any{
		"name":      "search_catalog",
		"arguments": map[string]any{"query": "postgres"},
	})
	callReq := &ipc.Request{
		JSONRPC: "2.0",
		ID:      &idRaw,
		Method:  "tools/call",
		Params:  callParams,
	}

	callResp := shim.HandleRequest(ctx, callReq)
	if callResp == nil || callResp.Error != nil {
		t.Fatalf("unexpected call error: %+v", callResp)
	}

	var toolRes MCPToolResult
	if err := json.Unmarshal(callResp.Result, &toolRes); err != nil {
		t.Fatal(err)
	}
	if toolRes.IsError {
		t.Fatalf("daemon-backed search_catalog returned an error: %s", toolRes.Content[0].Text)
	}
	if len(toolRes.Content) == 0 || !strings.Contains(toolRes.Content[0].Text, "postgres") {
		t.Errorf("unexpected content: %+v", toolRes)
	}

	// 2. Call list_installed
	listParams, _ := json.Marshal(map[string]any{
		"name":      "list_installed",
		"arguments": map[string]any{},
	})
	listCallReq := &ipc.Request{
		JSONRPC: "2.0",
		ID:      &idRaw,
		Method:  "tools/call",
		Params:  listParams,
	}

	listCallResp := shim.HandleRequest(ctx, listCallReq)
	var listToolRes MCPToolResult
	if err := json.Unmarshal(listCallResp.Result, &listToolRes); err != nil {
		t.Fatal(err)
	}
	if listToolRes.IsError {
		t.Fatalf("daemon-backed list_installed returned an error: %s", listToolRes.Content[0].Text)
	}
	if !strings.Contains(listToolRes.Content[0].Text, "LiteSPM Capabilities") {
		t.Errorf("expected 4-tab header, got: %s", listToolRes.Content[0].Text)
	}
	if !strings.Contains(listToolRes.Content[0].Text, "[External / Detected]") {
		t.Errorf("expected external detected item, got: %s", listToolRes.Content[0].Text)
	}

	// 3. Call non-existent tool -> returns structured error
	badCallParams, _ := json.Marshal(map[string]any{
		"name":      "non_existent_tool",
		"arguments": map[string]any{},
	})
	badCallReq := &ipc.Request{
		JSONRPC: "2.0",
		ID:      &idRaw,
		Method:  "tools/call",
		Params:  badCallParams,
	}

	badResp := shim.HandleRequest(ctx, badCallReq)
	var errToolRes MCPToolResult
	if err := json.Unmarshal(badResp.Result, &errToolRes); err != nil {
		t.Fatal(err)
	}
	if !errToolRes.IsError {
		t.Errorf("expected isError=true for non-existent tool")
	}
	if !strings.Contains(errToolRes.Content[0].Text, "LPSM-STATE-NOT-FOUND") {
		t.Errorf("expected canonical LPSM error code, got: %s", errToolRes.Content[0].Text)
	}
}

func TestBridge_ServeCodec(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var inBuf bytes.Buffer
	var outBuf bytes.Buffer

	// Write initialize request line
	reqLine := `{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n"
	inBuf.WriteString(reqLine)

	shim := NewShim("test-host", nil, &inBuf, &outBuf)
	err := shim.Serve(ctx)
	if err != nil {
		t.Fatalf("unexpected serve error: %v", err)
	}

	outStr := outBuf.String()
	if !strings.Contains(outStr, `"protocolVersion":"2026-07-28"`) {
		t.Errorf("unexpected serve output: %s", outStr)
	}
}

// A standalone shim (nil daemon client) must fail closed for every one of the
// 12 canonical tools: an explicit tool error that names the missing daemon
// connection, never fabricated data, statuses, or inventory.
func TestBridge_All12ToolsDispatch(t *testing.T) {
	ctx := context.Background()
	shim := NewShim("test-host", nil, nil, nil)
	idRaw := json.RawMessage(`1`)

	allTools := []struct {
		name string
		args map[string]any
	}{
		{"search_catalog", map[string]any{"query": "git"}},
		{"get_extension", map[string]any{"id": "mcp:github:modelcontextprotocol:servers:postgres"}},
		{"prepare_install", map[string]any{"id": "mcp:github:modelcontextprotocol:servers:postgres"}},
		{"request_install", map[string]any{"planId": "plan_123"}},
		{"list_installed", map[string]any{}},
		{"search_capabilities", map[string]any{"query": "sql"}},
		{"describe_capability", map[string]any{"capabilityId": "inst-1/db/query"}},
		{"load_skill", map[string]any{"skillId": "skill:builtin:git-release"}},
		{"read_skill_resource", map[string]any{"skillId": "skill:builtin:git-release", "path": "ref.md"}},
		{"invoke_capability", map[string]any{"capabilityId": "inst-1/db/query", "arguments": map[string]any{}}},
		{"get_invocation", map[string]any{"invocationId": "inv_123"}},
		{"cancel_invocation", map[string]any{"invocationId": "inv_123"}},
	}

	fabricatedMarkers := []string{
		"invoked successfully",
		"status: completed",
		"executed (standalone mode)",
		"Progressive instruction workflow",
		"LiteSPM Capabilities",
		"Verified ✓",
	}

	for _, tc := range allTools {
		t.Run(tc.name, func(t *testing.T) {
			paramsBytes, _ := json.Marshal(map[string]any{
				"name":      tc.name,
				"arguments": tc.args,
			})
			resp := shim.HandleRequest(ctx, &ipc.Request{
				JSONRPC: "2.0",
				ID:      &idRaw,
				Method:  "tools/call",
				Params:  paramsBytes,
			})
			if resp == nil || resp.Error != nil {
				t.Fatalf("tool %s failed with RPC error: %+v", tc.name, resp)
			}
			var toolRes MCPToolResult
			if err := json.Unmarshal(resp.Result, &toolRes); err != nil {
				t.Fatalf("failed to unmarshal tool result for %s: %v", tc.name, err)
			}
			if !toolRes.IsError {
				t.Fatalf("tool %s must fail closed in standalone mode, got success: %s", tc.name, toolRes.Content[0].Text)
			}
			if len(toolRes.Content) == 0 || toolRes.Content[0].Text == "" {
				t.Fatalf("tool %s returned empty error text", tc.name)
			}
			text := toolRes.Content[0].Text
			if !strings.Contains(text, "not connected to daemon") {
				t.Errorf("tool %s error must name the missing daemon connection, got: %s", tc.name, text)
			}
			if !strings.Contains(text, "LPSM-IPC-DAEMON-UNREACHABLE") {
				t.Errorf("tool %s error must carry the canonical unreachable-daemon code, got: %s", tc.name, text)
			}
			for _, marker := range fabricatedMarkers {
				if strings.Contains(text, marker) {
					t.Errorf("tool %s fabricated marker %q in standalone mode: %s", tc.name, marker, text)
				}
			}
		})
	}
}

// ping reports connection state truthfully in both modes.
func TestBridge_PingReportsConnectionState(t *testing.T) {
	ctx := context.Background()
	idRaw := json.RawMessage(`7`)

	pingReq := &ipc.Request{JSONRPC: "2.0", ID: &idRaw, Method: "ping"}

	standalone := NewShim("test-host", nil, nil, nil)
	resp := standalone.HandleRequest(ctx, pingReq)
	if resp == nil || resp.Error != nil {
		t.Fatalf("standalone ping failed: %+v", resp)
	}
	var standaloneResult map[string]any
	if err := json.Unmarshal(resp.Result, &standaloneResult); err != nil {
		t.Fatal(err)
	}
	if connected, ok := standaloneResult["connected"].(bool); !ok || connected {
		t.Fatalf("standalone ping must report connected=false, got %v", standaloneResult)
	}

	connected := newDaemonBackedShim(t, nil)
	resp = connected.HandleRequest(ctx, pingReq)
	if resp == nil || resp.Error != nil {
		t.Fatalf("connected ping failed: %+v", resp)
	}
	var connectedResult map[string]any
	if err := json.Unmarshal(resp.Result, &connectedResult); err != nil {
		t.Fatal(err)
	}
	if c, ok := connectedResult["connected"].(bool); !ok || !c {
		t.Fatalf("daemon-backed ping must report connected=true, got %v", connectedResult)
	}
}

// TestFormatInstalledPanelStatusTruthfulness proves an omitted/unknown status is
// not rendered as "Stopped", while Ready and Disabled keep their semantics.
func TestFormatInstalledPanelStatusTruthfulness(t *testing.T) {
	if StatusUnknown == StatusDisabled {
		t.Fatal("StatusUnknown must be distinct from StatusDisabled")
	}

	out := FormatInstalledPanel([]CapabilityItem{
		{ID: "mcp:a:b:unknown", Name: "unknown-tool", Kind: "mcp"}, // zero status: omitted by tools.list
		{ID: "mcp:a:b:ready", Name: "ready-tool", Kind: "mcp", Status: StatusReady},
		{ID: "mcp:a:b:stopped", Name: "stopped-tool", Kind: "mcp", Status: StatusDisabled},
	})

	var sawUnknown, sawReady, sawStopped bool
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, "unknown-tool"):
			sawUnknown = true
			if !strings.Contains(line, "Unknown") {
				t.Errorf("omitted status must render as Unknown, got row: %q", line)
			}
			if strings.Contains(line, "Stopped") {
				t.Errorf("omitted status must not render as Stopped, got row: %q", line)
			}
		case strings.Contains(line, "ready-tool"):
			sawReady = true
			if !strings.Contains(line, "● Ready") {
				t.Errorf("Ready status must render green, got row: %q", line)
			}
		case strings.Contains(line, "stopped-tool"):
			sawStopped = true
			if !strings.Contains(line, "○ Stopped") {
				t.Errorf("Disabled status must render as Stopped, got row: %q", line)
			}
		}
	}
	if !sawUnknown || !sawReady || !sawStopped {
		t.Fatalf("panel did not render all three rows (unknown=%v ready=%v stopped=%v):\n%s",
			sawUnknown, sawReady, sawStopped, out)
	}
}
