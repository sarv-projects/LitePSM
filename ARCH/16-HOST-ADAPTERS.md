# Host Adapters & In-Agent UX

## 1. Extensible HostAdapter Architecture

LitePSM connects to AI agent hosts through a strongly typed, compiled-in `HostAdapter` interface (`internal/host`).

```go
type HostAdapter interface {
    Descriptor() HostDescriptor
    DetectConfig(ctx context.Context, scope Scope) (string, error)
    PlanSetup(ctx context.Context, reg BridgeRegistration) (*HostChangePlan, error)
    ApplySetup(ctx context.Context, plan *HostChangePlan) (*HostApplyResult, error)
    VerifySetup(ctx context.Context) (*HostVerification, error)
    PlanRemove(ctx context.Context) (*HostChangePlan, error)
    ApplyRemove(ctx context.Context, plan *HostChangePlan) (*HostApplyResult, error)
    RenderManualSetup(ctx context.Context) (string, error)
    DetectPreExistingComponents(ctx context.Context) ([]PreExistingComponent, error)
}

type HostDescriptor struct {
    HostID                 string   `json:"hostId"`                 // e.g. cline, pi-agent, grok-build, claude-code
    DisplayName            string   `json:"displayName"`            // e.g. "Cline (VS Code Extension)"
    SupportedVersions      []string `json:"supportedVersions"`      // Pinned SemVer ranges
    DefaultConfigFileName  string   `json:"defaultConfigFileName"`  // e.g. cline_mcp_settings.json
    ConfigFormat           string   `json:"configFormat"`           // json | toml | yaml
    SupportsFormElicit     bool     `json:"supportsFormElicit"`     // MCP 2026-07-28 input_required
    RequiresBootstrapSkill bool     `json:"requiresBootstrapSkill"` // Needs companion SKILL.md / command
    SlashCommandTrigger    string   `json:"slashCommandTrigger"`    // e.g. "/litepsm"
}
```

---

## 2. Dynamic Runtime Adapter Advisory & Metadata Discovery

When a user runs `litepsm`, the client queries the remote catalog release pointer (`/v1/current.json`) and fetches the latest `adapters.json` metadata:
*   **Advisory Compatibility Metadata:** `adapters.json` contains version matrices, compatibility warnings, config path hints, and recommended setup snippets. It does **not** deliver dynamic executable Go code; config file parsing and mutation are strictly performed by the compiled-in binary. Adding new config parsers requires a client binary release.
*   **Zero Local Mutation:** Fetching adapter metadata is a passive read. It allows the CLI to inform the user if an updated client binary is required for a newer agent release without altering existing host configurations.
*   **Offline Fallback:** If internet access is unavailable, LitePSM falls back immediately to the compiled-in adapter registry.

---

## 3. Supported Agent Adapters

### 3.1 Cline (VS Code Extension) (`internal/host/cline`)
*   **Host ID:** `cline`
*   **Target Configuration:**
    *   **Windows:** `%APPDATA%\Code\User\globalStorage\saoudrizwan.claude-dev\settings\cline_mcp_settings.json`
    *   **macOS:** `~/Library/Application Support/Code/User/globalStorage/saoudrizwan.claude-dev/settings/cline_mcp_settings.json`
    *   **Linux:** `~/.config/Code/User/globalStorage/saoudrizwan.claude-dev/settings/cline_mcp_settings.json`
*   **Format:** JSON.
*   **Managed Injection:** Injects under `mcpServers.litepsm`.
*   **Detected External Capabilities:** Scans sibling keys in `mcpServers` (e.g., `filesystem`, `postgres`, `github`) as read-only detected entries.

### 3.2 Pi Agent (`pi-coding-agent`) (`internal/host/piagent`)
*   **Host ID:** `pi-agent`
*   **Target Configuration Candidate Paths:**
    *   Candidate 1: `~/.pi/agent/mcp.json` (Unix/macOS) or `%USERPROFILE%\.pi\agent\mcp.json` (Windows)
    *   Candidate 2: `~/.pi/config.json`
    *   Candidate 3: `~/.pi/agent/extensions/`
*   **Format:** JSON.
*   **Managed Injection:** Registers LitePSM in Pi's MCP config (`mcp.servers.litepsm`) and writes a companion command extension (`~/.pi/agent/extensions/litepsm.ts`).
*   **Detected External Capabilities:** Detects external tools from the discovered config file in read-only mode.

### 3.3 Grok Build (`internal/host/grokbuild`)
*   **Host ID:** `grok-build`
*   **Target Configuration:**
    *   **Unix / macOS:** `~/.grok/config.toml` (global) or `.grok/config.toml` (project)
    *   **Windows:** `%USERPROFILE%\.grok\config.toml` or `%APPDATA%\Grok\config.toml`
*   **Format:** TOML.
*   **Managed Injection:** Injects under `[mcp_servers.litepsm]`.
*   **Detected External Capabilities:** Parses declared external `[mcp_servers.*]` sections in read-only mode.

### 3.4 Claude Code (`internal/host/claudecode`)
*   **Host ID:** `claude-code`
*   **Target Configuration:** `~/.claude.json` (Unix) or `%USERPROFILE%\.claude.json` (Windows).
*   **Format:** JSON.
*   **Managed Injection:** Injects under `mcpServers.litepsm`.
*   **Detected External Capabilities:** Scans existing `mcpServers` and `.claude/skills/` in read-only mode.

### 3.5 OpenAI Codex (`internal/host/codex`)
*   **Host ID:** `codex`
*   **Target Configuration:**
    *   **Unix / macOS:** `~/.codex/config.toml`
    *   **Windows:** `%USERPROFILE%\.codex\config.toml` or `%APPDATA%\Codex\config.toml`
*   **Format:** TOML.
*   **Managed Injection:** Injects under `[mcp_servers.litepsm]`.
*   **Detected External Capabilities:** Parses declared external `[mcp_servers.*]` sections in read-only mode.

### 3.6 OpenCode (`internal/host/opencode`)
*   **Host ID:** `opencode`
*   **Target Configuration:** `~/.config/opencode/opencode.json` (Unix) or `%APPDATA%\OpenCode\opencode.json` (Windows).
*   **Format:** JSON.
*   **Version Mapping Profile:**
    *   `v1.x`: Uses root `mcp` dictionary (`mcp.litepsm`).
    *   `v2.x`: Uses nested `mcp.servers` object (`mcp.servers.litepsm`).
    The adapter inspects the existing document structure or schema version to write the correct layout.
*   **Detected External Capabilities:** Scans existing configured servers in read-only mode.

---

## 4. In-Agent `/litepsm` Experience & Capability Browser

The in-agent experience is architected as an abstract UX Model mapped to host-specific renderers:

```text
┌────────────────────────────────────────────────────────────────────────┐
│                        LitePSM Abstract UX Model                       │
│    (Stateful Capability Navigation: Search, Inspect, Install, Status)  │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │
         ┌──────────────────────────┴──────────────────────────┐
         ▼                                                     ▼
┌─────────────────────────────────┐           ┌─────────────────────────────────┐
│ Host-Specific Rich UI Renderer  │           │ Portable Text / MCP Renderer    │
│ (Cline Webview / Pi Modal TUI)  │           │ (Claude Code, Codex, Grok CLI)  │
│ 4-Tab Interactive GUI Deck      │           │ Formatted Markdown & Tool Calls │
└─────────────────────────────────┘           └─────────────────────────────────┘
```

### 4.1 Portable Contract vs. Rich Host UI
*   **Baseline Portable Contract:** Universal MCP does not support arbitrary GUI windows or webviews. For terminal CLI agents (`claude`, `codex`, `grok`, `opencode`), `/litepsm` prints structured markdown tables, action shortcuts, and standard MCP discovery tools (`search_catalog`, `describe_capability`, `invoke_capability`, `list_installed`).
*   **Rich Host Renderer:** Where agent hosts support custom extensions or webviews (e.g. Cline's VS Code extension panel or Pi's interactive terminal TUI), the companion extension renders the interactive 4-tab visual capability browser:

```text
┌────────────────────────────────────────────────────────────────────────┐
│                          LitePSM Capabilities                          │
├──────────────┬──────────────┬──────────────┬───────────────────────────┤
│ [MCP SERVERS]│[AGENT SKILLS]│  [PLUGINS]   │     [INSTALLED (4)] ●     │
└──────────────┴──────────────┴──────────────┴───────────────────────────┘
```

### Tab 1: MCP Servers
*   Search bar for filtering MCP servers by keyword or category (`database`, `developer-tools`, `browser`).
*   Lists server cards with publisher, verified status, and transport (`stdio` / `Streamable HTTP`).
*   One-click "Install" action triggering the `prepare_install` flow.

### Tab 2: Agent Skills
*   Browse portable `SKILL.md` skills from `agentskills.io` and public Git sources.
*   Shows declared description, triggers, and progressive disclosure preview.

### Tab 3: Plugins
*   Curated plugins and bundles combining MCP servers, skills, and tools.
*   Reports per-host compatibility badges (`Claude`, `Codex`, `Cline`, `Grok`).

### Tab 4: Installed & Detected External Capabilities
The final tab provides complete situational visibility across the host environment:
*   **Green Light Indicator (●):** Visual indicator showing whether the server/skill is currently active, connected, and responding (`● Active / Ready`), degraded (`🟡 Needs Auth`), or off (`○ Disabled / Stopped`).
*   **Detected External Capabilities (Read-Only by Default):**
    *   LitePSM scans the host's primary documented configuration file for pre-existing native tools (e.g., servers previously added manually to `cline_mcp_settings.json`, `~/.claude.json`, or `.codex/config.toml`).
    *   **Scope Limitation:** Detection is strictly limited to documented on-disk config files known to the adapter; in-memory sessions, proprietary cloud-managed extensions, or undocumented registries are not scanned.
    *   **Read-Only Observation:** To prevent data loss or config corruption, external tools are strictly **read-only** in the status view. LitePSM never toggles or edits external tools without explicit permission.
    *   **Explicit Adopt Action:** To bring an external tool under LitePSM lifecycle supervision, the user must explicitly choose **"Import / Adopt into LitePSM"**, which generates an atomic backup and creates a managed `InstallRecord`.

```text
INSTALLED & DETECTED CAPABILITIES:

[MCP SERVERS]
  ● postgres-prod        [LitePSM]   v1.4.0   Status: Ready (3 tools)
  ● github-native        [Detected]  external Status: Ready (Read-Only) [Adopt]
  ○ memory-store         [LitePSM]   v1.0.0   Status: Stopped

[AGENT SKILLS]
  ● pr-reviewer          [LitePSM]   v1.2.0   Active (SKILL.md)
  ● release-drafter      [Detected]  external Active (.claude/skills/)

[PLUGINS]
  ● web-navigator        [LitePSM]   v2.0.1   Active
```

---

## 5. Automated Slash Command (`/litepsm`) Registration

To guarantee that `/litepsm` is immediately accessible the next time the agent opens:
1.  **Cline:** Registers a custom prompt/workflow or workspace command triggering the LitePSM MCP bridge.
2.  **Pi Agent:** Writes a TypeScript command extension to `~/.pi/agent/extensions/litepsm.ts` (or `~/.pi/extensions/litepsm.ts`) registering `/litepsm`.
3.  **Claude Code & Codex:** Installs a companion bootstrap skill `litepsm.skill.md` with trigger keyword `/litepsm`.
4.  **Grok Build:** Registers a custom command hook in `.grok/` or project configuration.
