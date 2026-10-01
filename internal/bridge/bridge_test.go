package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sarv-projects/litepsm/internal/ipc"
)

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
	shim := NewShim("test-host", nil, nil, nil)

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
	if !strings.Contains(listToolRes.Content[0].Text, "LitePSM Capabilities") {
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
			if toolRes.IsError {
				t.Fatalf("tool %s returned unexpected error: %s", tc.name, toolRes.Content[0].Text)
			}
			if len(toolRes.Content) == 0 || toolRes.Content[0].Text == "" {
				t.Fatalf("tool %s returned empty text", tc.name)
			}
		})
	}
}

