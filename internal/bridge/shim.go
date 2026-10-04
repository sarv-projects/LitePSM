package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
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

// InstalledStatus represents runtime health.
type InstalledStatus string

const (
	StatusReady     InstalledStatus = "ready"      // ● Green
	StatusNeedsAuth InstalledStatus = "needs_auth" // 🟡 Yellow
	StatusDisabled  InstalledStatus = "disabled"   // ○ Grey
)

// CapabilityItem represents a capability summary across the 4 tabs.
type CapabilityItem struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Kind       string          `json:"kind"` // mcp | skill | plugin
	Summary    string          `json:"summary"`
	Transport  string          `json:"transport,omitempty"`
	Status     InstalledStatus `json:"status,omitempty"`
	IsExternal bool            `json:"isExternal,omitempty"`
	Verified   bool            `json:"verified,omitempty"`
	StarCount  int             `json:"starCount,omitempty"`
	Triggers   []string        `json:"triggers,omitempty"`
}

// Shim serves the 12 canonical LitePSM tools over standard input/output.
type Shim struct {
	client  *ipc.Client
	hostID  string
	tools   []MCPTool
	reader  *bufio.Reader
	writer  io.Writer
	writeMu sync.Mutex
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
		client: client,
		hostID: hostID,
		reader: bufio.NewReader(in),
		writer: out,
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

	case "ping":
		// ping reports connection state truthfully in both modes: standalone
		// shims answer connected=false rather than pretending to be healthy.
		result, _ := json.Marshal(map[string]any{"connected": s.client != nil})
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
			if req.ID == nil {
				return nil
			}
			errRes := FormatErrorResult(domain.ErrInvalidIdentifier("params", "valid tool call parameters"))
			resBytes, _ := json.Marshal(errRes)
			return &ipc.Response{JSONRPC: "2.0", ID: req.ID, Result: resBytes}
		}

		toolResult := s.DispatchTool(ctx, callParams.Name, callParams.Arguments)
		if req.ID == nil {
			return nil
		}
		resBytes, _ := json.Marshal(toolResult)
		return &ipc.Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  resBytes,
		}

	default:
		if req.ID == nil {
			return nil // Notification; never return error response to ID-less notifications
		}
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
	if !s.knownTool(name) {
		return FormatErrorResult(domain.ErrNotFound("tool", name))
	}
	if s.client == nil {
		// Standalone mode (no daemon connection): the shim holds no capability
		// data, so every tool fails closed with an explicit error instead of
		// fabricating success, statuses, or inventory.
		return standaloneError(name)
	}

	switch name {
	case "search_catalog":
		var req struct {
			Query string   `json:"query"`
			Kinds []string `json:"kinds,omitempty"`
			Limit int      `json:"limit,omitempty"`
		}
		_ = json.Unmarshal(args, &req)

		var resp struct {
			Count   int   `json:"count"`
			Results []any `json:"results"`
		}
		err := s.client.Call(ctx, "catalog.search", map[string]any{
			"query": req.Query,
			"kinds": req.Kinds,
			"limit": req.Limit,
		}, &resp)
		if err != nil {
			return FormatErrorResult(err)
		}
		outBytes, _ := json.MarshalIndent(resp, "", "  ")
		return MCPToolResult{Content: []MCPContent{{Type: "text", Text: string(outBytes)}}}

	case "get_extension":
		var req struct {
			ID      string `json:"id"`
			Version string `json:"version,omitempty"`
		}
		_ = json.Unmarshal(args, &req)

		var resp any
		err := s.client.Call(ctx, "catalog.get_item", map[string]any{
			"id":      req.ID,
			"version": req.Version,
		}, &resp)
		if err != nil {
			return FormatErrorResult(err)
		}
		outBytes, _ := json.MarshalIndent(resp, "", "  ")
		return MCPToolResult{Content: []MCPContent{{Type: "text", Text: string(outBytes)}}}

	case "prepare_install":
		var req struct {
			ID      string `json:"id"`
			Version string `json:"version,omitempty"`
		}
		_ = json.Unmarshal(args, &req)

		var resp any
		err := s.client.Call(ctx, "resolver.prepare_plan", map[string]any{
			"id":      req.ID,
			"version": req.Version,
		}, &resp)
		if err != nil {
			return FormatErrorResult(err)
		}
		outBytes, _ := json.MarshalIndent(resp, "", "  ")
		return MCPToolResult{Content: []MCPContent{{Type: "text", Text: string(outBytes)}}}

	case "request_install":
		var req struct {
			PlanID        string `json:"planId"`
			ApprovalToken string `json:"approvalToken,omitempty"`
		}
		_ = json.Unmarshal(args, &req)

		var resp any
		err := s.client.Call(ctx, "install.execute", map[string]any{
			"planId":        req.PlanID,
			"approvalToken": req.ApprovalToken,
		}, &resp)
		if err != nil {
			return FormatErrorResult(err)
		}
		outBytes, _ := json.MarshalIndent(resp, "", "  ")
		return MCPToolResult{Content: []MCPContent{{Type: "text", Text: string(outBytes)}}}

	case "list_installed":
		var resp struct {
			Count    int              `json:"count"`
			Installs []CapabilityItem `json:"installs"`
		}
		err := s.client.Call(ctx, "tools.list", nil, &resp)
		if err != nil {
			return FormatErrorResult(err)
		}
		return MCPToolResult{Content: []MCPContent{{Type: "text", Text: FormatInstalledPanel(resp.Installs)}}}

	case "search_capabilities":
		var req struct {
			Query string `json:"query"`
			Limit int    `json:"limit,omitempty"`
		}
		_ = json.Unmarshal(args, &req)

		var resp any
		err := s.client.Call(ctx, "capabilities.search", map[string]any{
			"query": req.Query,
			"limit": req.Limit,
		}, &resp)
		if err != nil {
			return FormatErrorResult(err)
		}
		outBytes, _ := json.MarshalIndent(resp, "", "  ")
		return MCPToolResult{Content: []MCPContent{{Type: "text", Text: string(outBytes)}}}

	case "describe_capability":
		var req struct {
			CapabilityID string `json:"capabilityId"`
		}
		_ = json.Unmarshal(args, &req)

		var resp any
		err := s.client.Call(ctx, "capabilities.describe", map[string]any{
			"capabilityId": req.CapabilityID,
		}, &resp)
		if err != nil {
			return FormatErrorResult(err)
		}
		outBytes, _ := json.MarshalIndent(resp, "", "  ")
		return MCPToolResult{Content: []MCPContent{{Type: "text", Text: string(outBytes)}}}

	case "load_skill":
		var req struct {
			SkillID string `json:"skillId"`
			Version string `json:"version,omitempty"`
		}
		_ = json.Unmarshal(args, &req)

		var resp struct {
			SkillID      string `json:"skillId"`
			Instructions string `json:"instructions"`
		}
		err := s.client.Call(ctx, "skills.load_body", map[string]any{
			"skillId": req.SkillID,
			"version": req.Version,
		}, &resp)
		if err != nil {
			return FormatErrorResult(err)
		}
		if resp.Instructions == "" {
			// The daemon answered with no instruction body; report that
			// instead of substituting canned instructions.
			return FormatErrorResult(domain.NewError(
				"LPSM-CORE-INTERNAL",
				fmt.Sprintf("skill %s returned no instructions from the daemon", req.SkillID),
				map[string]any{"skillId": req.SkillID},
			))
		}
		return MCPToolResult{Content: []MCPContent{{Type: "text", Text: resp.Instructions}}}

	case "read_skill_resource":
		var req struct {
			SkillID string `json:"skillId"`
			Path    string `json:"path"`
		}
		_ = json.Unmarshal(args, &req)

		var resp struct {
			Content string `json:"content"`
		}
		err := s.client.Call(ctx, "skills.read_resource", map[string]any{
			"skillId": req.SkillID,
			"path":    req.Path,
		}, &resp)
		if err != nil {
			return FormatErrorResult(err)
		}
		return MCPToolResult{Content: []MCPContent{{Type: "text", Text: resp.Content}}}

	case "invoke_capability":
		var req struct {
			CapabilityID string          `json:"capabilityId"`
			Arguments    json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(args, &req)

		var resp struct {
			Output  string `json:"output"`
			IsError bool   `json:"isError"`
		}
		err := s.client.Call(ctx, "provider.invoke", map[string]any{
			"capabilityId": req.CapabilityID,
			"arguments":    req.Arguments,
		}, &resp)
		if err != nil {
			return FormatErrorResult(err)
		}
		return MCPToolResult{
			Content: []MCPContent{{Type: "text", Text: resp.Output}},
			IsError: resp.IsError,
		}

	case "get_invocation":
		var req struct {
			InvocationID string `json:"invocationId"`
		}
		_ = json.Unmarshal(args, &req)

		var resp any
		err := s.client.Call(ctx, "invocation.get", map[string]any{
			"invocationId": req.InvocationID,
		}, &resp)
		if err != nil {
			return FormatErrorResult(err)
		}
		outBytes, _ := json.MarshalIndent(resp, "", "  ")
		return MCPToolResult{Content: []MCPContent{{Type: "text", Text: string(outBytes)}}}

	case "cancel_invocation":
		var req struct {
			InvocationID string `json:"invocationId"`
		}
		_ = json.Unmarshal(args, &req)

		var resp struct {
			Cancelled bool `json:"cancelled"`
		}
		err := s.client.Call(ctx, "invocation.cancel", map[string]any{
			"invocationId": req.InvocationID,
		}, &resp)
		if err != nil {
			return FormatErrorResult(err)
		}
		return MCPToolResult{Content: []MCPContent{{Type: "text", Text: fmt.Sprintf("Invocation %s cancelled: %v", req.InvocationID, resp.Cancelled)}}}

	default:
		return FormatErrorResult(domain.ErrNotFound("tool", name))
	}
}

// FormatInstalledPanel renders the in-agent 4-tab capability layout for installed tools.
func FormatInstalledPanel(items []CapabilityItem) string {
	var sb strings.Builder
	sb.WriteString("┌────────────────────────────────────────────────────────────────────────┐\n")
	sb.WriteString("│                          LitePSM Capabilities                          │\n")
	sb.WriteString("├──────────────┬──────────────┬──────────────┬───────────────────────────┤\n")
	sb.WriteString("│ [MCP SERVERS]│[AGENT SKILLS]│  [PLUGINS]   │")
	installedHeader := fmt.Sprintf("     [INSTALLED (%d)] ●   ", len(items))
	sb.WriteString(installedHeader[:27])
	sb.WriteString("│\n")
	sb.WriteString("└──────────────┴──────────────┴──────────────┴───────────────────────────┘\n\n")

	if len(items) == 0 {
		sb.WriteString("No capabilities currently installed. Use `search_catalog` to discover tools.\n")
		return sb.String()
	}

	sb.WriteString("Status: ● Ready | 🟡 Needs Auth | ○ Stopped | [External / Detected] Read-Only\n\n")
	sb.WriteString("| Status | Kind | Name / ID | Notes / Action |\n")
	sb.WriteString("|---|---|---|---|\n")

	for _, it := range items {
		var statusDot string
		switch it.Status {
		case StatusReady:
			statusDot = "● Ready"
		case StatusNeedsAuth:
			statusDot = "🟡 Needs Auth"
		default:
			statusDot = "○ Stopped"
		}

		note := "Managed"
		if it.IsExternal {
			note = "[External / Detected] (Adopt)"
		} else if it.Verified {
			note = "Verified ✓"
		}

		sb.WriteString(fmt.Sprintf("| %s | %s | **%s** | %s |\n", statusDot, strings.ToUpper(it.Kind), it.Name, note))
	}

	return sb.String()
}

// knownTool reports whether name is one of the 12 canonical tools this shim
// advertises in tools/list.
func (s *Shim) knownTool(name string) bool {
	for _, tool := range s.tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

// standaloneError is the fail-closed tool result used when the shim has no
// daemon connection. The bridge holds no capability data of its own, so every
// tool reports the missing connection instead of inventing results, statuses,
// or inventory.
func standaloneError(tool string) MCPToolResult {
	err := domain.NewError(
		"LPSM-IPC-DAEMON-UNREACHABLE",
		fmt.Sprintf("tool %s: not connected to daemon (standalone mode); no LitePSM daemon connection, so no capability data is available", tool),
		map[string]any{"tool": tool, "mode": "standalone"},
	)
	err.Retryable = true
	return FormatErrorResult(err)
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
