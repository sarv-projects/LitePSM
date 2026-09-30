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

## 2. Dynamic Runtime Adapter Discovery

When a user runs `litepsm`, the client dynamically queries the remote catalog release pointer (`/v1/current.json`) and fetches the latest `adapters.json` manifest:
*   **Adapter Readiness Registry:** Lists all officially verified and community-supported host adapters, their minimum agent versions, and required setup parameters.
*   **Zero Local Mutation:** Fetching the adapter list is a passive read. It allows the CLI to inform the user if an updated adapter exists for a newly released agent version without altering existing local configurations.
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
*   **Pre-Existing Component Detection:** Scans all other sibling keys in `mcpServers` (e.g., `filesystem`, `postgres`, `github`). Reports their active/disabled state and configuration arguments in the Installed tab.

### 3.2 Pi Agent (`pi-coding-agent`) (`internal/host/piagent`)
*   **Host ID:** `pi-agent`
*   **Target Configuration:**
    *   **Unix / macOS:** `~/.pi/config.json` or `~/.pi/extensions/`
    *   **Windows:** `%USERPROFILE%\.pi\config.json`
*   **Format:** JSON.
*   **Managed Injection:** Registers LitePSM via Pi's MCP extension config (`mcp.servers.litepsm`) and writes a companion command extension (`~/.pi/extensions/litepsm.ts` or skill).
*   **Pre-Existing Component Detection:** Reads Pi's configured extensions, tools, and native MCP entries.

### 3.3 Grok Build (`internal/host/grokbuild`)
*   **Host ID:** `grok-build`
*   **Target Configuration:**
    *   **Unix / macOS:** `~/.config/grok/config.toml` or `.grok-plugin/marketplace.json`
    *   **Windows:** `%APPDATA%\Grok\config.toml`
*   **Format:** TOML.
*   **Managed Injection:** Injects under `[mcp_servers.litepsm]`.
*   **Pre-Existing Component Detection:** Parses all declared `[mcp_servers.*]` and `.grok-plugin/` entries.

### 3.4 Claude Code (`internal/host/claudecode`)
*   **Host ID:** `claude-code`
*   **Target Configuration:** `~/.claude.json` (Unix) or `%USERPROFILE%\.claude.json` (Windows).
*   **Format:** JSON.
*   **Managed Injection:** Injects under `mcpServers.litepsm`.
*   **Pre-Existing Component Detection:** Scans existing `mcpServers` and `.claude/skills/`.

### 3.5 OpenAI Codex (`internal/host/codex`)
*   **Host ID:** `codex`
*   **Target Configuration:** `~/.codex/config.json` (Unix) or `%APPDATA%\Codex\config.json` (Windows).
*   **Format:** JSON.
*   **Managed Injection:** Injects under `mcp_servers.litepsm`.

### 3.6 OpenCode (`internal/host/opencode`)
*   **Host ID:** `opencode`
*   **Target Configuration:** `~/.config/opencode/opencode.json` (Unix) or `%APPDATA%\OpenCode\opencode.json` (Windows).
*   **Format:** JSON.
*   **Managed Injection:** Injects under `mcp.servers.litepsm`.

---

## 4. In-Agent `/litepsm` Panel & Tabs Specification

When an agent user invokes `/litepsm` (or triggers the LitePSM skill/tool), the Bridge opens an interactive panel (rendered as an interactive UI or structured text/markdown card deck with selectable actions):

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

### Tab 4: Installed (Comprehensive Local Status)
The final tab shows **ALL** installed capabilities currently registered in the environment:
*   **Green Light Indicator (●):** Visual indicator showing whether the server/skill is currently active, connected, and responding (`● Active / Ready`), degraded (`🟡 Needs Auth`), or off (`○ Disabled / Stopped`).
*   **Universal Discovery of Pre-Existing Components:**
    *   Crucially, this tab displays **both** items installed via LitePSM **and** items installed previously/natively by the user directly in the agent's config (e.g. pre-existing `github`, `fetch`, or `postgres` servers in `cline_mcp_settings.json` or `~/.claude.json`).
    *   Pre-existing items are tagged as `[Native / External]` with toggle controls to inspect, start, or stop them.

```text
INSTALLED CAPABILITIES:

[MCP SERVERS]
  ● postgres-prod        [LitePSM]   v1.4.0   Status: Ready (3 tools)
  ● github-native        [External]  v0.2.1   Status: Ready (5 tools)
  ○ memory-store         [LitePSM]   v1.0.0   Status: Stopped

[AGENT SKILLS]
  ● pr-reviewer          [LitePSM]   v1.2.0   Active (SKILL.md)
  ● release-drafter      [External]  local    Active (.claude/skills/)

[PLUGINS]
  ● web-navigator        [LitePSM]   v2.0.1   Active
```

---

## 5. Automated Slash Command (`/litepsm`) Registration

To guarantee that `/litepsm` is immediately accessible the next time the agent opens:
1.  **Cline:** Registers a custom prompt/workflow or workspace command triggering the LitePSM MCP bridge.
2.  **Pi Agent:** Writes a TypeScript command extension to `~/.pi/extensions/litepsm.ts` registering `/litepsm`.
3.  **Claude Code & Codex:** Installs a companion bootstrap skill `litepsm.skill.md` with trigger keyword `/litepsm`.
4.  **Grok Build:** Registers a custom command hook in `.grok-plugin/`.
