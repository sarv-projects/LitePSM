# Bridge, Provider Supervisor & MCP Client

## 1. Bridge Shim Architecture

The LitePSM Bridge Shim (`cmd/litepsm bridge stdio --host <host-id>`) is a stateless, host-facing MCP stdio server. It acts as an adapter between the host agent's MCP interface and the local LitePSM Daemon.

```text
┌─────────────────┐       MCP JSON-RPC (stdio)      ┌──────────────────┐
│ Host Agent      │ ◄─────────────────────────────► │ Bridge Shim      │
│ (Claude/Codex)  │                                 │ (Stateless Proxy)│
└─────────────────┘                                 └─────────┬────────┘
                                                              │ Local IPC (JSON-RPC)
                                                              ▼
                                                    ┌──────────────────┐
                                                    │ LitePSM Daemon   │
                                                    │ (State & Superv) │
                                                    └──────────────────┘
```

### 1.1 Tool Dispatch & Translation
When a host issues an MCP `tools/call`, the Bridge shim translates it directly to the corresponding Daemon IPC call:

```go
func (b *BridgeShim) HandleToolCall(ctx context.Context, req mcp.ToolCallRequest) (mcp.ToolResult, error) {
    switch req.Name {
    case "search_catalog":
        var params domain.CatalogSearchParams
        _ = json.Unmarshal(req.Arguments, &params)
        res, err := b.ipcClient.CatalogSearch(ctx, params)
        return formatToolResult(res, err)

    case "invoke_capability":
        var params domain.InvokeCapabilityParams
        _ = json.Unmarshal(req.Arguments, &params)
        res, err := b.ipcClient.ProviderInvoke(ctx, params)
        return formatToolResult(res, err)

    case "load_skill":
        var params domain.LoadSkillParams
        _ = json.Unmarshal(req.Arguments, &params)
        res, err := b.ipcClient.SkillsLoad(ctx, params)
        return formatToolResult(res, err)

    default:
        return mcp.ToolResult{}, fmt.Errorf("unknown LitePSM tool: %s", req.Name)
    }
}
```

---

## 2. Provider Supervisor & Process Isolation

The LitePSM Daemon acts as a supervisor for all local stdio MCP servers. It ensures that crashes, hangs, or unexpected shutdowns do not leave orphaned processes running on the user's workstation.

### 2.1 Process Cleanup Guarantees
*   **Windows (Job Objects):**
    Each spawned child process is assigned to a Windows Job Object configured with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`. When the daemon process terminates for any reason, the Windows kernel automatically terminates all associated child processes.
*   **Linux & macOS (Process Groups):**
    Each child process is spawned in its own process group (`Setpgid = true`). Upon cancellation or termination, a signal is sent to the negative PID (`syscall.Kill(-pgid, syscall.SIGTERM)`), terminating the parent and any spawned subprocesses.

### 2.2 Launch Specification Construction
```go
type LaunchSpec struct {
    Executable   string            `json:"executable"` // Absolute path to runtime binary
    Args         []string          `json:"args"`       // Strictly argv array (no shell strings)
    WorkingDir   string            `json:"workingDir"` // Explicit CAS or project directory
    Env          map[string]string `json:"env"`        // Minimal inherited environment
    SecretEnv    map[string]string `json:"secretEnv"`  // Environment variables injected from secret vault
    TimeoutSec   int               `json:"timeoutSec"` // Startup timeout (default: 30s)
}
```

---

## 3. Dual MCP Protocol Support

LitePSM supports both the modern and legacy specifications of the Model Context Protocol:

### 3.1 Modern Profile (2026-07-28)
*   **Stateless Request Architecture:** Requests include version, client identity, and capability negotiation within a top-level `_meta` field.
*   **Streamable HTTP:** Uses single-endpoint HTTP POST for client-to-server messaging and chunked streaming for responses.
*   **Header Mirroring:** Outbound HTTP requests include `Mcp-Method` and `Mcp-Name` headers for transport-level routing.
*   **Unified Subscriptions:** Uses `subscriptions/listen` instead of resource-specific endpoints.

### 3.2 Legacy Profile (2025-11-25)
*   **Stateful Handshake:** Employs the `initialize` and `initialized` sequence for backward compatibility with older servers.
*   **SSE Support:** Connects via legacy Server-Sent Events for older remote providers.

---

## 4. Routed vs. Projected Capability Execution

LitePSM supports two modes for exposing installed capabilities to an agent host:

```text
┌──────────────────────────────────────────────┬──────────────────────────────────────────────┐
│ Routed Mode (Default in v1)                  │ Projected Mode (Optional Future UX)          │
├──────────────────────────────────────────────┼──────────────────────────────────────────────┤
│ Host tool list remains small (12 tools).     │ Pre-approved capabilities projected directly │
│ Host invokes:                                │ as native tools in host agent context:       │
│   invoke_capability(                         │   psm_postgres_query(sql="...")              │
│     capability_id="inst-1/db/query",         │                                              │
│     arguments={"sql": "SELECT 1"}            │ Requires deterministic namespacing and       │
│   )                                          │ dynamic tool list change notifications.      │
└──────────────────────────────────────────────┴──────────────────────────────────────────────┘
```
In Routed Mode, tool discovery is progressive: the model queries `search_capabilities` and `describe_capability` on demand, keeping prompt context overhead minimal.
