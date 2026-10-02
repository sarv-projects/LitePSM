# Host Adapters & In-Agent UX

## 1. Extensible HostAdapter Architecture

LitePSM connects to AI agent hosts through a strongly typed, compiled-in `HostAdapter` interface (`internal/host`).

```go
type HostAdapter interface {
    Descriptor() HostDescriptor
    DetectConfig(ctx context.Context, scope domain.InstallScope) (string, error)
    PlanSetup(ctx context.Context, binaryPath string, backupDir string) (*HostChangePlan, error)
    ApplySetup(ctx context.Context, plan *HostChangePlan) (*HostApplyResult, error)
    VerifySetup(ctx context.Context) (*HostVerification, error)
    DetectPreExistingComponents(ctx context.Context) ([]PreExistingComponent, error)
    RenderManualSetup(binaryPath string) string
}

type HostDescriptor struct {
    HostID                 string   `json:"hostId"`                 // e.g. cline, pi-agent, grok-build, claude-code
    DisplayName            string   `json:"displayName"`            // e.g. "Cline (VS Code Extension)"
    SupportedVersions      []string `json:"supportedVersions"`      // Pinned SemVer ranges
    DefaultConfigFileName  string   `json:"defaultConfigFileName"`  // e.g. cline_mcp_settings.json
    ConfigFormat           string   `json:"configFormat"`           // json | toml | yaml
    SupportsFormElicit     bool     `json:"supportsFormElicit"`     // MCP 2026-07-28 input_required
    RequiresBootstrapSkill bool     `json:"requiresBootstrapSkill"` // Needs companion SKILL.md / command
    SlashCommandTrigger    string   `json:"slashCommandTrigger"`    // e.g. "/marketplace"
}
```

---

## 2. Dynamic Runtime Adapter Advisory & Metadata Discovery

When a user runs `litepsm`, the client reads the remote catalog release pointer (`/v1/current.json`), which carries an `advisories` array (hostId, status, minVersion) — there is no separate `adapters.json` endpoint. The interactive wizard (`cmd/litepsm/wizard.go:verifyRuntimeAdvisories`) currently reports compiled-in protocol/adapters without a network fetch:
*   **Advisory Compatibility Metadata:** `current.json.advisories` contains per-host status and minimum versions. It does **not** deliver dynamic executable Go code; config file parsing and mutation are strictly performed by the compiled-in binary. Adding new config parsers requires a client binary release.
*   **Zero Local Mutation:** Reading advisory metadata is a passive read. It allows the CLI to inform the user if an updated client binary is required for a newer agent release without altering existing host configurations.
*   **Offline Fallback:** If internet access is unavailable, LitePSM uses the compiled-in adapter registry (`internal/host/registry.go`).

---

## 3. Supported Agent Adapters

### 3.1 Cline (VS Code Extension) (`internal/host/cline.go`)
*   **Host ID:** `cline`
*   **Target Configuration:**
    *   **Windows:** `%APPDATA%\Code\User\globalStorage\saoudrizwan.claude-dev\settings\cline_mcp_settings.json`
    *   **macOS:** `~/Library/Application Support/Code/User/globalStorage/saoudrizwan.claude-dev/settings/cline_mcp_settings.json`
    *   **Linux:** `~/.config/Code/User/globalStorage/saoudrizwan.claude-dev/settings/cline_mcp_settings.json`
*   **Format:** JSON.
*   **Managed Injection:** Injects under `mcpServers.litepsm`.
*   **Cline CLI:** The standalone Cline CLI uses a separate file (`~/.cline/data/settings/cline_mcp_settings.json`, and `~/.cline/mcp.json`) that LitePSM does not currently manage.
*   **Detected External Capabilities:** Scans sibling keys in `mcpServers` (e.g., `filesystem`, `postgres`, `github`) as read-only detected entries.

### 3.2 Pi Agent (`pi-coding-agent`) (`internal/host/piagent.go`)
*   **Host ID:** `pi-agent`
*   **Target Configuration Candidate Paths:**
    *   User scope: `~/.pi/agent/mcp.json` (Unix/macOS) or `%USERPROFILE%\.pi\agent\mcp.json` (Windows)
    *   Project scope: `.pi/mcp.json` (trust-gated)
    *   Legacy fallback: `~/.pi/config.json` / `~/.pi/mcp.json`
*   **Format:** JSON.
*   **Managed Injection:** Registers LitePSM under the `mcpServers` key in Pi's MCP config and writes a companion command extension (`~/.pi/agent/extensions/litepsm.ts`).
*   **Detected External Capabilities:** Detects external tools from the discovered config file in read-only mode.

### 3.3 Grok Build (`internal/host/grokbuild.go`)
*   **Host ID:** `grok-build`
*   **Target Configuration:**
    *   **Unix / macOS:** `~/.grok/config.toml` (global) or `.grok/config.toml` (project)
    *   **Windows:** `%USERPROFILE%\.grok\config.toml` (`%APPDATA%\Grok\config.toml` is a legacy fallback)
*   **Format:** TOML.
*   **Managed Injection:** Injects under `[mcp_servers.litepsm]`.
*   **Detected External Capabilities:** Parses declared external `[mcp_servers.*]` sections in read-only mode.

### 3.4 Claude Code (`internal/host/claudecode.go`)
*   **Host ID:** `claude-code`
*   **Target Configuration:** User scope `~/.claude.json` (Windows `%USERPROFILE%\.claude.json`), project scope `.mcp.json` in the project root, and a per-project local entry inside `~/.claude.json`. `CLAUDE_CONFIG_DIR` overrides the config directory.
*   **Format:** JSON.
*   **Managed Injection:** Injects under `mcpServers.litepsm`.
*   **Detected External Capabilities:** Scans existing `mcpServers` and `.claude/skills/` in read-only mode.

### 3.5 OpenAI Codex (`internal/host/codex.go`)
*   **Host ID:** `codex`
*   **Target Configuration:**
    *   **Unix / macOS:** `~/.codex/config.toml` (project `.codex/config.toml`)
    *   **Windows:** `%USERPROFILE%\.codex\config.toml` (`%APPDATA%\Codex\config.toml` is a legacy fallback; `$CODEX_HOME` overrides the directory)
*   **Format:** TOML.
*   **Managed Injection:** Injects under `[mcp_servers.litepsm]`.
*   **Detected External Capabilities:** Parses declared external `[mcp_servers.*]` sections in read-only mode.

### 3.6 OpenCode (`internal/host/opencode.go`)
*   **Host ID:** `opencode`
*   **Target Configuration:** `~/.config/opencode/opencode.json` (Unix/macOS) or `%USERPROFILE%\.config\opencode\opencode.json` (native Windows; `%APPDATA%\OpenCode\opencode.json` is a legacy fallback). Project scope supports `opencode.json` in the project root or `.opencode/`.
*   **Format:** JSON.
*   **Local Entry Shape:** Local MCP entries require `"type": "local"` and a combined string array `"command"`, e.g. `{"mcp":{"servers":{"litepsm":{"type":"local","command":["litepsm","bridge","stdio","--host","opencode"]}}}}`.
*   **Version Mapping Profile:**
    *   `v1.x`: Uses root `mcp` dictionary (`mcp.litepsm`).
    *   `v2.x`: Uses nested `mcp.servers` object (`mcp.servers.litepsm`).
    The adapter inspects the existing document structure or schema version to write the correct layout.
*   **Detected External Capabilities:** Scans existing configured servers in read-only mode.

### 3.7 Data-driven targets: 44 generic + 6 bespoke = 50 total (`internal/host/target.go`, `ARCH/30`)
*   The six adapters above are hand-written and retained (they carry behaviour the generic path does not model: OpenCode v1/v2 layouts, TOML handling, Pi extension generation, Cline comment preservation).
*   All other agents are data rows (`verifiedBridgeTargets` in `internal/host/targets_data.go`, 44 rows) served by the single `GenericAdapter` (`internal/host/generic.go`). Bespoke IDs always win name collisions (`TestBridgeTargetTableDoesNotShadowBespokeAdapters`).
*   Surgical merge (`internal/host/jsonc_merge.go` + TOML merger) preserves comments/key order; strict-JSON hosts refuse commented files rather than guessing. See ARCH/30 for the honesty contract, exclusion table (23 unverified agents), and integrity tests.

---

## 4. In-Agent `/marketplace` Experience & Capability Browser

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
*   **Baseline Portable Contract:** Universal MCP does not support arbitrary GUI windows or webviews. For terminal CLI agents (`claude`, `codex`, `grok`, `opencode`), `/marketplace` prints structured markdown tables, action shortcuts, and standard MCP discovery tools (`search_catalog`, `describe_capability`, `invoke_capability`, `list_installed`).
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
*   Reports per-host compatibility badges (`Claude`, `Codex`, `Cline`, `Grok Build`).

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

## 5. Automated Slash Command (`/marketplace`) Registration

To guarantee that `/marketplace` is immediately accessible the next time the agent opens:
1.  **Cline:** Registers a custom prompt/workflow or workspace command triggering the LitePSM MCP bridge.
2.  **Pi Agent:** Writes a TypeScript command extension to `~/.pi/agent/extensions/litepsm.ts` (or `~/.pi/extensions/litepsm.ts`) registering `/marketplace`.
3.  **Claude Code & Codex:** Installs a companion bootstrap skill `litepsm.skill.md` with trigger keyword `/marketplace`.
4.  **Grok Build:** Registers a custom command hook in `.grok/config.toml` (project scope).
