# Bridge, Provider Supervisor & MCP Client

Transcribed from the tree at `ff0a1db`. State labels use the vocabulary of
[`STATUS.md`](../STATUS.md).

## 1. Bridge Shim Architecture

The LiteSPM Bridge Shim (`litespm bridge stdio --host <host-id>`) is a
stateless, host-facing MCP stdio server. It is an adapter between the host
agent's MCP interface and the local LiteSPM Daemon, and it is `WIRED`
([STATUS.md](../STATUS.md) §1).

```text
┌─────────────────┐       MCP JSON-RPC (stdio)      ┌──────────────────┐
│ Host Agent      │ ◄─────────────────────────────► │ Bridge Shim      │
│ (any MCP host)  │                                 │ (Stateless Proxy)│
└─────────────────┘                                 └─────────┬────────┘
                                                              │ Local IPC (JSON-RPC)
                                                              ▼
                                                    ┌─────────┬────────┐
                                                    │ LiteSPM Daemon   │
                                                    │ (State & Superv) │
                                                    └──────────────────┘
```

### 1.1 Real dispatch API

The type is `bridge.Shim`; its entry points are a constructor and three methods — there
is no `BridgeShim.HandleToolCall`, no `mcp.ToolCallRequest`/`mcp.ToolResult`
type, and no `ipcClient.CatalogSearch` / `ProviderInvoke` / `SkillsLoad` helper
methods anywhere in the tree:

```go
// internal/bridge/shim.go
func NewShim(hostID string, client *ipc.Client, in io.Reader, out io.Writer) *Shim // :75
func (s *Shim) Serve(ctx context.Context) error                                    // :159  reads/writes ipc.NewLineDelimitedCodec frames
func (s *Shim) HandleRequest(ctx context.Context, req *ipc.Request) *ipc.Response   // :187  "initialize" | "ping" | "initialized" |
                                                                                   //       "notifications/initialized" | "tools/list" | "tools/call"
func (s *Shim) DispatchTool(ctx context.Context, name string, args json.RawMessage) MCPToolResult // :270
```

`tools/call` is translated inline to a single daemon method through the generic
IPC client (`ipc.Client.Call(ctx, method, params, &out)`), not through a typed
per-method wrapper:

```go
toolResult := s.DispatchTool(ctx, callParams.Name, callParams.Arguments)   // shim.go:243

func (s *Shim) DispatchTool(ctx context.Context, name string, args json.RawMessage) MCPToolResult {
        if !s.knownTool(name) {                      // one of the 12 advertised tools? else LPSM error
                return FormatErrorResult(domain.ErrNotFound("tool", name))
        }
        if s.client == nil {                         // standalone mode: fail closed, never fabricate
                return standaloneError(name)
        }
        switch name {
        case "search_catalog":                       // shim.go:282-304
                err := s.client.Call(ctx, "catalog.search", map[string]any{
                        "query": req.Query, "kinds": req.Kinds, "limit": req.Limit,
                }, &resp)
        case "request_install":                      // shim.go:341-358
                err := s.client.Call(ctx, "install.execute", map[string]any{
                        "planId": req.PlanID, "approvalToken": req.ApprovalToken,
                }, &resp)
        case "invoke_capability":                    // shim.go:452-474
                err := s.client.Call(ctx, "provider.invoke", map[string]any{
                        "capabilityId": req.CapabilityID, "arguments": req.Arguments,
                }, &resp)
        // ...
        }
}
```

Two consequences worth stating precisely:

*   **Naming split.** MCP tool names are `verb_noun` (`search_catalog`,
    `load_skill`); daemon methods are `domain.verb` (`catalog.search`,
    `skills.load_body`). `search_capabilities` (MCP tool) maps to the daemon
    method `capabilities.search` — it is *not* an alias of `search_catalog`,
    which queries the catalog index instead. The full mapping is §1.2.
*   **Error rendering.** A daemon `-32601` reaches the host as an MCP *tool*
    result, not a JSON-RPC error object: `FormatErrorResult`
    (`shim.go:590-605`) only passes through `*domain.LPSMError`, so a raw
    `*ipc.RPCError` is wrapped in an `LPSM-CORE-INTERNAL` envelope
    (`internal/domain/errors.go:296-306`) whose message text still carries the
    daemon's `rpc error: code=-32601 message=not implemented: …` reason, with
    `isError: true`.

### 1.2 The 12 tools and what each resolves to today

`tools/list` advertises exactly 12 tools (`internal/bridge/shim.go:93-156`);
`knownTool` rejects anything else (`shim.go:566-573`).

| # | MCP tool | Daemon method (`internal/bridge/shim.go`) | Status |
|---|---|---|---|
| 1 | `search_catalog` | `catalog.search` (`:282`) | `WIRED` — real index search |
| 2 | `get_extension` | `catalog.get_item` (`:305`) | `WIRED` |
| 3 | `prepare_install` | `resolver.prepare_plan` (`:323`) | `WIRED` — plan persisted with `planHash` |
| 4 | `request_install` | `install.execute` (`:341`) | `IMPLEMENTED` **(cannot complete)** — the handler supplies no artifact source ([STATUS.md](../STATUS.md) §3) |
| 5 | `list_installed` | `tools.list` (`:359`) | `WIRED` — installs + read-only detected external tools |
| 6 | `search_capabilities` | `capabilities.search` (`:370`) | **`-32601`** — handler returns `not implemented … use catalog.search` (`cmd/litespm/main.go:1628-1633`) |
| 7 | `describe_capability` | `capabilities.describe` (`:388`) | **`-32601`** (`main.go:1637-1642`) |
| 8 | `load_skill` | `skills.load_body` (`:404`) | `WIRED` |
| 9 | `read_skill_resource` | `skills.read_resource` (`:433`) | `WIRED` — path-traversal checked (`main.go:1601-1608`) |
| 10 | `invoke_capability` | `provider.invoke` (`:452`) | **`-32601`** — no capability rows, no session dispatch (`main.go:1676-1682`) |
| 11 | `get_invocation` | `invocation.get` (`:475`) | **`-32601`** — no invocation registry (`main.go:1685-1690`) |
| 12 | `cancel_invocation` | `invocation.cancel` (`:491`) | **`-32601`** (`main.go:1694-1699`) |

> Line numbers in the table are the `case` labels inside `DispatchTool`
> (`shim.go:270-511`).

The six resolving tools are exactly the ones [AGENTS.md](../AGENTS.md) §3 lists
as resolving today. `request_install` reaches the handler but stops at the
missing artifact source; the remaining five fail closed with an explicit
`-32601` reason rather than fabricated data. Standalone mode (no daemon
connection) fails **all** twelve with `LPSM-IPC-DAEMON-UNREACHABLE`
(`shim.go:274-280`, `:579-587`), and `ping` reports `{"connected": false}`
truthfully (`shim.go:206-214`).

**Protocol note.** `initialize` answers `protocolVersion: "2026-07-28"` with a
hard-coded `serverInfo.version` of `"0.1.0"` (`shim.go:189-204`), while the CLI
binary version is `Version = "0.3.0"` (`cmd/litespm/main.go:44`) — the shim does
not read the build version.

---

## 2. Provider Supervisor & Process Isolation

The daemon supervises local stdio provider processes so crashes, hangs, or
unexpected shutdowns do not leave orphaned children. The supervisor is
`WIRED` for `start` / `stop` / `probe`
([STATUS.md](../STATUS.md) §4):

*   `provider.NewSupervisor()` + `provider.StartConfigured(...)` run during
    daemon startup (`cmd/litespm/main.go:1184-1194`) and
    `supervisor.StopAll(...)` is deferred.
*   **Nothing autostarts in production.** `StartConfigured` consumes rows from
    the `providers` table, and that table has **no non-test writer**
    ([STATUS.md](../STATUS.md) §1, "Not yet true"), so in a real run it starts
    zero processes. `internal/provider/configured.go:41-67` reports only
    `started` / `skipped` / `failed` per row.
*   `provider.probe` is a **real** method: it returns the supervisor's snapshot
    (status, PID, start/stop times, exit error, last 2 KiB of stderr) for a tracked
    provider, or an explicit not-found for anything untracked
    (`cmd/litespm/main.go:1648-1671`, `Supervisor.SnapshotProvider`,
    `internal/provider/supervisor.go:298-328`).

### 2.1 Process Cleanup Guarantees

*   **Windows (Job Objects):** every spawned child is assigned to a Job Object
    created with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`, so the kernel terminates
    the tree when the daemon's handle closes
    (`internal/provider/isolation_windows.go:25-125`, assignment in
    `postStartProcessIsolation`). Children are also created in a new process
    group (`CREATE_NEW_PROCESS_GROUP`).
*   **Linux & macOS:** the child gets `SysProcAttr.Setpgid = true`
    (`internal/provider/isolation_unix.go:17`), then the platform-specific
    parent-death mechanism — and the two platforms are **not** the same:
    *   **Linux** relies on the kernel: `attr.Pdeathsig = syscall.SIGKILL`
        (`internal/provider/pdeathsig_linux.go:7-9`). `startWatchdogIfRequired`
        on Linux is an explicit **no-op** (`pdeathsig_linux.go:11-13`) — there is
        no control pipe on Linux.
    *   **macOS / other Unix** (no `PR_SET_PDEATHSIG`):
        `startWatchdogIfRequired` creates an anonymous `os.Pipe`, keeps the write
        end in the handle, and runs a goroutine blocked on the read end. If the
        daemon dies (including `SIGKILL`), the kernel closes the pipe, the read
        returns `EOF`, and the goroutine issues **one**
        `syscall.Kill(-pid, syscall.SIGKILL)` on the whole process group
        (`internal/provider/pdeathsig_other.go:14-29`). There is no SIGTERM
        stage and no 2-second grace in this watchdog path.
    *   **Graceful stop** is a different path: `StopProvider` calls
        `handle.Terminate(2 * time.Second)` (`supervisor.go:235-241`), which
        `killProcessTree` implements as `SIGTERM` to `-pgid`, then `SIGKILL`
        after the 2 s grace, then waits for the single reaper
        (`isolation_unix.go:28-52`).
    *   There is **no** separate watchdog CLI subcommand; the watchdog lives in
        `internal/provider/` as described.
*   **Stderr** is captured into a bounded 64 KiB rotating ring buffer per
    provider (`supervisor.go:14-64`) and surfaced through `provider.probe`.

### 2.2 Launch Specification Construction

```go
// internal/provider/supervisor.go:71-80
type LaunchSpec struct {
    Executable string            `json:"executable"` // Absolute path to runtime binary
    Args       []string          `json:"args"`       // Strictly argv array (no shell strings)
    WorkingDir string            `json:"workingDir"` // Explicit CAS or project directory
    Env        map[string]string `json:"env"`        // Minimal inherited environment
    SecretEnv  map[string]string `json:"secretEnv"`  // Injected from the secret vault
    TimeoutSec int               `json:"timeoutSec"` // Startup timeout (default: 30s)
}
```

`DefaultStartupTimeout = 30 * time.Second` is declared (`supervisor.go:20-21`)
but **nothing reads it**: `StartProvider` spawns with `exec.CommandContext` and
marks the handle `running` without enforcing `TimeoutSec`
(`supervisor.go:163-231`). Timeout enforcement is `DESIGNED`.
`supervisor.go` also defines `InvocationRequest` / `InvocationResult`
(`:132-150`); they are type declarations only — no registry or dispatch exists
(§1.2 rows 10-12).

---

## 3. Dual MCP Protocol Support (`internal/mcpclient`)

LiteSPM speaks both protocol profiles itself — `go.mod` carries **no** MCP SDK
dependency. This package is `IMPLEMENTED` with **zero production importers**
([STATUS.md](../STATUS.md) §4): nothing in `cmd/` or `internal/provider/` calls
it, so no provider session exists at runtime today.

### 3.1 Constructors and probes (exact signatures)

```go
func ConnectStdio(ctx context.Context, in io.Reader, out io.Writer) (*StdioClient, error)            // client_stdio.go:26
func ConnectStreamableHTTP(ctx context.Context, endpoint string, headers map[string]string,
        httpClient *http.Client) (*StreamableHTTPClient, error)                                     // client_2026.go:24
func ConnectLegacy(ctx context.Context, endpoint string, headers map[string]string,
        httpClient *http.Client) (*LegacyClient, error)                                             // client_legacy.go:27

func FingerprintSchema(schema json.RawMessage) (string, error)                                       // probe.go:20
func ProbeProvider(ctx context.Context, session ClientSession) ([]DiscoveredCapability, error)       // probe.go:25
func DetectDrift(grant *domain.CapabilityGrant, currentCap DiscoveredCapability,
        currentCASTreeDigest, currentEndpointOrigin, currentServerVersionDigest string) DriftReport // probe.go:67
```

There is **no `ConnectLegacySSE`** and no SSE reader anywhere in the package.
All three constructors satisfy one `ClientSession` interface
(`types.go:44-50`): `ProtocolVersion`, `ListTools`, `CallTool`,
`SubscribeToListChanges`, `CloseSession`.

### 3.2 Modern Profile (2026-07-28)

*   **Stateless per call:** one HTTP `POST` per JSON-RPC request, with
    protocol version, client identity, and clamped capabilities in a top-level
    `_meta` field (`types.go:53-59`, `client_2026.go:63-76`); the modern client
    performs no `initialize` handshake at all.
*   **Header mirroring:** `Mcp-Method`, `Mcp-Name` (when a tool is called), and
    `Mcp-Protocol-Version` are set on every request
    (`client_2026.go:91-95`).
*   **No streaming reader:** `sendRequest` reads the complete response body
    (bounded at 16 MiB) — chunked/SSE response consumption is not implemented
    (`client_2026.go:113`).
*   **Subscriptions:** `SubscribeToListChanges` issues a single
    `subscriptions/listen` request for `types: ["tools"]` and returns; it does
    not maintain a notification channel (`client_2026.go:168-173`).
*   **Clamped caps:** roots and sampling are deliberately `nil`
    (`types.go:79-85`).

### 3.3 Legacy Profile (2025-11-25)

*   **Stateful handshake:** `initialize` / `notifications/initialized` is
    performed inside `ConnectLegacy` (`client_legacy.go:27-44`, `:54-98`) before
    the first call.
*   **Transport is HTTP POST only.** `postRPC` and `postNotification` POST to
    the same endpoint (`client_legacy.go:100-159`); **no SSE transport
    exists**, and `SubscribeToListChanges` is a no-op returning `nil`
    (`client_legacy.go:214-216`).
*   `ModeLegacySSE` remains a value of the persisted `providers.mode`
    CHECK constraint (`internal/state/migrations/001_initial_schema.sql:159`,
    value declared at `internal/provider/configured.go:12`), i.e. a schema
    placeholder with no client behind it.

---

## 4. Routed vs. Projected Capability Execution

LiteSPM exposes installed capabilities to an agent host in one of two modes:

```text
┌──────────────────────────────────────────────┬──────────────────────────────────────────────┐
│ Routed Mode (Default in v1)                  │ Projected Mode (Optional Future UX)          │
├──────────────────────────────────────────────┼──────────────────────────────────────────────┤
│ Host tool list remains small (12 tools).     │ Pre-approved capabilities projected directly │
│ Host invokes:                                │ as native tools in host agent context:       │
│   invoke_capability(                         │   psm_postgres_query(sql="...")              │
│     capability_id="inst_user_mcp_builtin_mcp-│ Requires deterministic namespacing and       │
│     registry_a1b2c3d4/server/query",         │ dynamic tool list change notifications.      │
│     arguments={"sql": "SELECT 1"}            │                                              │
│   )                                          │                                              │
└──────────────────────────────────────────────┴──────────────────────────────────────────────┘
```

In Routed Mode, tool discovery is progressive: the model queries
`search_capabilities` and `describe_capability` on demand, keeping prompt
context overhead minimal. **That path is not functional yet** — both tools are
advertised but return `-32601` (§1.2), so progressive discovery currently has
no index behind it. Projected Mode is `DESIGNED`; host adapters today perform
**MCP-entry merge only**, with no projection of skills, commands, or agents
([STATUS.md](../STATUS.md) §1, [ARCH/16](16-HOST-ADAPTERS.md)).

### 4.1 Cryptographic Identity Binding Verification

Before executing any tool call, the approval/grant must cryptographically match
the current runtime state:

1.  **Local Stdio Providers:** tuple `(capability_id, schemaFingerprint,
    casTreeDigest)`. If the disk files change (tree digest drift) or the
    provider alters its input schema (schema drift), execution is rejected until
    re-approved.
2.  **Remote Streamable HTTP Providers:** tuple `(capability_id,
    schemaFingerprint, endpointOrigin, serverVersionDigest)`. If DNS redirects
    to another origin or the remote server changes version digest, execution is
    blocked.

These tuples are exactly the columns of `domain.CapabilityGrant`
([ARCH/10 §2.8](10-DOMAIN-MODEL.md)) and the inputs to
`mcpclient.DetectDrift` (`probe.go:67`), which classifies `schema` / `code` /
`endpoint` drift and returns a `LPSM` error (e.g.
`CodeProviderSchemaDrift`). **State today:** the drift checker and the grant
struct/writer are `IMPLEMENTED`, but `capability_grants` is never written in
production ([STATUS.md](../STATUS.md) §4) and no dispatch consumes
`DetectDrift`, so no execution is ever accepted or rejected on this basis.

## 5. Provider health, circuit breaking, fallback — `DESIGNED`

Health states, the `READY`/`UNHEALTHY`/`BACKOFF` circuit breaker, bounded output,
restart/retry rules, and cross-provider fallback are specified in
[ARCH/34](34-RUNTIME-INVOCATION-RECEIPTS.md) and are **`DESIGNED`** — no health
prober, breaker, or invocation engine exists in the tree
([STATUS.md](../STATUS.md) §4). The only real runtime signal today is the
supervisor snapshot behind `provider.probe` (§2). Do not describe a provider as
healthy, unhealthy, or circuit-broken: LiteSPM can only report
`starting`/`running`/`unresponsive`/`stopped`/`error` for a process it
supervises (`supervisor.go:81-89`), and `— Unknown` for anything it does not
(`internal/bridge/shim.go:513-562`, `FormatInstalledPanel`).
