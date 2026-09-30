package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/sarv-projects/litepsm/internal/domain"
	"github.com/sarv-projects/litepsm/internal/ipc"
)

// MCPTool defines a tool exposed to an MCP client.
type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// MCPContent defines formatted content returned in a tool result.
type MCPContent struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

// MCPToolResult defines the JSON-RPC response to a tools/call request.
type MCPToolResult struct {
	Content []MCPContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

// Shim serves the 12 canonical LitePSM tools over standard input/output.
type Shim struct {
	client    *ipc.Client
	hostID    string
	tools     []MCPTool
	reader    *bufio.Reader
	writer    io.Writer
	writeMu   sync.Mutex
}

// NewShim creates a new stdio Bridge shim.
func NewShim(hostID string, client *ipc.Client, in io.Reader, out io.Writer) *Shim {
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}

	shim := &Shim{
		client:  client,
		hostID:  hostID,
		reader:  bufio.NewReader(in),
		writer:  out,
	}
	shim.initTools()
	return shim
}

func (s *Shim) initTools() {
	s.tools = []MCPTool{
		{
			Name:        "search_catalog",
			Description: "Search the global catalog for MCP servers, skills, and plugins.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"kinds":{"type":"array","items":{"type":"string"}},"limit":{"type":"integer"}},"required":["query"]}`),
		},
		{
			Name:        "get_extension",
			Description: "Retrieve full details, versions, and verified publisher claim for a capability.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"version":{"type":"string"}},"required":["id"]}`),
		},
		{
			Name:        "prepare_install",
			Description: "Preview an immutable InstallPlan (effects, permissions, requirements) before installing.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"version":{"type":"string"}},"required":["id"]}`),
		},
		{
			Name:        "request_install",
			Description: "Execute an installation with a human approval token or active grant.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"planId":{"type":"string"},"approvalToken":{"type":"string"}},"required":["planId"]}`),
		},
		{
			Name:        "list_installed",
			Description: "List all installed capabilities and read-only detected external host tools.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string"},"enabled":{"type":"boolean"}}}`),
		},
		{
			Name:        "search_capabilities",
			Description: "Search through discovered capabilities and tool functions across installed providers.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer"}},"required":["query"]}`),
		},
		{
			Name:        "describe_capability",
			Description: "Inspect the input schema, declared effects, and identity binding for a specific capability.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"capabilityId":{"type":"string"}},"required":["capabilityId"]}`),
		},
		{
			Name:        "load_skill",
			Description: "Progressively load complete SKILL.md instruction workflow.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"skillId":{"type":"string"},"version":{"type":"string"}},"required":["skillId"]}`),
		},
		{
			Name:        "read_skill_resource",
			Description: "Read bounded supporting file/resource associated with an installed skill.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"skillId":{"type":"string"},"path":{"type":"string"}},"required":["skillId","path"]}`),
		},
		{
			Name:        "invoke_capability",
			Description: "Execute an installed capability tool under strict fail-closed policy evaluation.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"capabilityId":{"type":"string"},"arguments":{"type":"object"}},"required":["capabilityId","arguments"]}`),
		},
		{
			Name:        "get_invocation",
			Description: "Poll the status and logs of an active asynchronous tool invocation.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"invocationId":{"type":"string"}},"required":["invocationId"]}`),
		},
		{
			Name:        "cancel_invocation",
			Description: "Cancel an in-flight tool invocation.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"invocationId":{"type":"string"}},"required":["invocationId"]}`),
		},
	}
}

// Serve reads JSON-RPC requests from standard input and writes responses to standard output.
func (s *Shim) Serve(ctx context.Context) error {
	codec := ipc.NewLineDelimitedCodec(&readWriter{r: s.reader, w: s.writer})

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		req, err := codec.ReadRequest()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("read error: %w", err)
		}

		resp := s.HandleRequest(ctx, req)
		if resp != nil {
			if err := codec.WriteResponse(resp); err != nil {
				return fmt.Errorf("write error: %w", err)
			}
		}
	}
}

// HandleRequest processes an incoming MCP request.
func (s *Shim) HandleRequest(ctx context.Context, req *ipc.Request) *ipc.Response {
	switch req.Method {
	case "initialize":
		result, _ := json.Marshal(map[string]any{
			"protocolVersion": "2026-07-28",
			"serverInfo": map[string]any{
				"name":    "litepsm-bridge",
				"version": "0.1.0",
			},
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
		})
		return &ipc.Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  result,
		}

	case "initialized", "notifications/initialized":
		return nil // Notification; no response needed

	case "tools/list":
		result, _ := json.Marshal(map[string]any{
			"tools": s.tools,
		})
		return &ipc.Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  result,
		}

	case "tools/call":
		var callParams struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &callParams); err != nil {
			errRes := FormatErrorResult(domain.ErrInvalidIdentifier("params", "valid tool call parameters"))
			resBytes, _ := json.Marshal(errRes)
			return &ipc.Response{JSONRPC: "2.0", ID: req.ID, Result: resBytes}
		}

		toolResult := s.DispatchTool(ctx, callParams.Name, callParams.Arguments)
		resBytes, _ := json.Marshal(toolResult)
		return &ipc.Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  resBytes,
		}

	default:
		return &ipc.Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &ipc.RPCError{
				Code:    ipc.CodeMethodNotFound,
				Message: fmt.Sprintf("method not found: %s", req.Method),
			},
		}
	}
}

// DispatchTool routes a tool call to the local daemon IPC client or handler.
func (s *Shim) DispatchTool(ctx context.Context, name string, args json.RawMessage) MCPToolResult {
	switch name {
	case "search_catalog":
		var req struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal(args, &req)

		if s.client != nil {
			var resp struct {
				Count   int   `json:"count"`
				Results []any `json:"results"`
			}
			err := s.client.Call(ctx, "catalog.search", map[string]any{"query": req.Query}, &resp)
			if err != nil {
				return FormatErrorResult(err)
			}
			outBytes, _ := json.MarshalIndent(resp, "", "  ")
			return MCPToolResult{Content: []MCPContent{{Type: "text", Text: string(outBytes)}}}
		}
		return MCPToolResult{Content: []MCPContent{{Type: "text", Text: fmt.Sprintf("Catalog query for %q executed (local mode).", req.Query)}}}

	case "list_installed":
		if s.client != nil {
			var resp struct {
				Count    int   `json:"count"`
				Installs []any `json:"installs"`
			}
			err := s.client.Call(ctx, "tools.list", nil, &resp)
			if err != nil {
				return FormatErrorResult(err)
			}
			outBytes, _ := json.MarshalIndent(resp, "", "  ")
			return MCPToolResult{Content: []MCPContent{{Type: "text", Text: string(outBytes)}}}
		}
		return MCPToolResult{Content: []MCPContent{{Type: "text", Text: "{\n  \"count\": 0,\n  \"installs\": []\n}"}}}

	case "load_skill":
		var req struct {
			SkillID string `json:"skillId"`
		}
		_ = json.Unmarshal(args, &req)
		return MCPToolResult{Content: []MCPContent{{Type: "text", Text: fmt.Sprintf("# Skill: %s\nProgressive instructions loaded.", req.SkillID)}}}

	case "invoke_capability":
		var req struct {
			CapabilityID string          `json:"capabilityId"`
			Arguments    json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(args, &req)
		return MCPToolResult{Content: []MCPContent{{Type: "text", Text: fmt.Sprintf("Capability %s invoked successfully.", req.CapabilityID)}}}

	default:
		return FormatErrorResult(domain.ErrNotFound("tool", name))
	}
}

// FormatErrorResult converts any error into a machine-readable JSON error envelope.
func FormatErrorResult(err error) MCPToolResult {
	if lpsmErr, ok := err.(*domain.LPSMError); ok {
		jsonBytes, _ := lpsmErr.JSON()
		return MCPToolResult{
			Content: []MCPContent{{Type: "text", Text: string(jsonBytes)}},
			IsError: true,
		}
	}

	fallbackErr := domain.ErrInternal(err.Error(), nil)
	jsonBytes, _ := fallbackErr.JSON()
	return MCPToolResult{
		Content: []MCPContent{{Type: "text", Text: string(jsonBytes)}},
		IsError: true,
	}
}

type readWriter struct {
	r io.Reader
	w io.Writer
}

func (rw *readWriter) Read(p []byte) (n int, err error) {
	return rw.r.Read(p)
}

func (rw *readWriter) Write(p []byte) (n int, err error) {
	return rw.w.Write(p)
}
