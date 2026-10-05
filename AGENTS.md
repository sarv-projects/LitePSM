# Supported AI Agents & Integration Guide

**LiteSPM** — *The Lightweight Skill & Package Manager for AI Agents*. It connects once to your AI agent host, enabling you to discover, install, update, and monitor MCP servers, skills, and plugins through a single unified interface.

```text
┌─────────────────┐       ┌─────────────────┐       ┌─────────────────┐
│      Cline      │       │    Pi Agent     │       │   Grok Build    │
│(VS Code Plugin) │       │(Terminal Agent) │       │ (xAI Dev Tool)  │
└────────┬────────┘       └────────┬────────┘       └────────┬────────┘
         │                         │                         │
         └─────────────────────────┼─────────────────────────┘
                                   │ Stdio MCP Bridge
                                   ▼
                      ┌─────────────────────────┐
                      │   LiteSPM Local Core    │
                      │  (Daemon & CAS Store)   │
                      └─────────────────────────┘
```

---

## 1. Supported Agent Directory

| Agent Host | Environment | Configuration Format | Default Config Path |
|---|---|---|---|
| **Cline** | VS Code Extension | JSON (`cline_mcp_settings.json`) | Windows: `%APPDATA%\Code\User\globalStorage\saoudrizwan.claude-dev\settings\`<br>macOS: `~/Library/Application Support/Code/User/globalStorage/saoudrizwan.claude-dev/settings/`<br>Linux: `~/.config/Code/User/globalStorage/saoudrizwan.claude-dev/settings/`<br>Cline CLI (not managed): `~/.cline/data/settings/cline_mcp_settings.json` |
| **Pi Agent** | Terminal Coding Agent (`pi`) | JSON (`mcp.json` / `config.json`) + TS Extension | Unix: `~/.pi/agent/mcp.json`<br>Windows: `%USERPROFILE%\.pi\agent\mcp.json`<br>Project: `.pi/mcp.json` (trust-gated)<br>Legacy fallback: `~/.pi/config.json` / `~/.pi/mcp.json` |
| **Grok Build** | Terminal / IDE (`grok`) [id: `grok-build`] | TOML (`config.toml`) | Unix: `~/.grok/config.toml`<br>Windows: `%USERPROFILE%\.grok\config.toml`<br>Project: `.grok/config.toml`<br>Legacy fallback: `%APPDATA%\Grok\config.toml` |
| **Claude Code** | Terminal CLI (`claude`) | JSON (`~/.claude.json`) | Unix: `~/.claude.json`<br>Windows: `%USERPROFILE%\.claude.json`<br>Project: `.mcp.json` in project root (local scope entry in `~/.claude.json`)<br>`CLAUDE_CONFIG_DIR` overrides the config directory |
| **OpenAI Codex** | Terminal CLI (`codex`) | TOML (`config.toml`) | Unix: `~/.codex/config.toml`<br>Windows: `%USERPROFILE%\.codex\config.toml`<br>Project: `.codex/config.toml`<br>Legacy fallback: `%APPDATA%\Codex\config.toml`; `$CODEX_HOME` overrides the directory |
| **OpenCode** | Open-source CLI (`opencode`) | JSON (`opencode.json` - v1 `mcp` / v2 `mcp.servers`) | Unix: `~/.config/opencode/opencode.json`<br>Windows: `%USERPROFILE%\.config\opencode\opencode.json`<br>Project: `opencode.json` or `.opencode/`<br>Legacy fallback: `%APPDATA%\OpenCode\opencode.json` |

> Six bespoke adapters above. The full registry is 50 bridge targets (6 bespoke + 44 generic `BridgeTarget` rows) plus 77 skill targets — see `ARCH/30-DATA-DRIVEN-BRIDGE-TARGETS.md` and `litespm host list`.

---

## 2. Setup Workflow

### Step 1: Install LiteSPM Globally
Install via npm or download the pre-compiled binary:
```bash
npm install -g litespm
```

### Step 2: Run the Interactive Setup
```bash
litespm
```
1.  **Dynamic Runtime Fetching:** LiteSPM contacts `/v1/current.json` to verify the latest verified adapter advisory metadata (supported versions and warnings; actual parsers are compiled in).
2.  **Select Your Agent:** Choose your agent from the interactive dropdown (for Claude Code, Codex, OpenCode, and many more).
3.  **Auto-Detection:** LiteSPM scans your filesystem for the agent's configuration file.
4.  **Fallback Options:** If the configuration file is not found (e.g., custom installation directory), LiteSPM prompts:
    *   *Enter path manually*
    *   *Print copy-paste snippet*
    *   *Retry detection*
5.  **Safe Atomic Merge:** LiteSPM creates a backup in `DATA_ROOT/backups/`, preserves all existing entries and comments, injects a **single** version-pinned LiteSPM Bridge entry (`litespm bridge stdio --host <agent-id>`), and writes the `/marketplace` command hook. Individual capabilities are resolved by the daemon at runtime, so no per-capability host snippets are generated.

---

## 3. The In-Agent `/marketplace` Experience

Once configured, simply launch your agent and type:
```text
/marketplace
```

### UX Architecture: Portable Contract vs. Rich Host UI
*   **Portable Text / MCP Contract:** For terminal CLI agents (`claude`, `codex`, `grok`, `opencode`), `/marketplace` prints structured markdown tables, action shortcuts, and standard MCP discovery tools (`search_catalog`, `describe_capability`, `invoke_capability`, `list_installed`).
*   **Rich Host Renderer:** In hosts supporting rich extension panels or terminal TUIs (e.g. Cline in VS Code or Pi Agent TUI), `/marketplace` opens the **LiteSPM Capability Panel** with 4 dedicated tabs:

```text
┌────────────────────────────────────────────────────────────────────────┐
│                          LiteSPM Capabilities                          │
├──────────────┬──────────────┬──────────────┬───────────────────────────┤
│ [MCP SERVERS]│[AGENT SKILLS]│  [PLUGINS]   │     [INSTALLED (5)] ●     │
└──────────────┴──────────────┴──────────────┴───────────────────────────┘
```

### Tab 1: MCP Servers
*   Full search bar filtering by name, category, or transport (`stdio` / `Streamable HTTP`).
*   Displays verified badges and upstream GitHub links. No star, download or install counts are shown: no upstream source exposes them, so the catalog publishes none rather than an estimate.
*   One-click install button that creates a verified `InstallPlan`.

### Tab 2: Agent Skills
*   Search portable `SKILL.md` workflows from `agentskills.io`.
*   Preview instructions, tools used, and triggers.

### Tab 3: Plugins
*   Curated multi-component toolkits and extensions for complex workflows.

### Tab 4: Installed & Detected External Capabilities
The **Installed** tab provides complete situational awareness of your tool ecosystem:
1.  **Green Light Status Indicator (●):**
    *   `● Green (Ready / Connected)`: The server is running and responding to tool health checks.
    *   `🟡 Yellow (Needs Auth)`: OAuth or API key token required.
    *   `○ Grey (Stopped / Disabled)`: The server is configured but inactive.
2.  **Detected External Capabilities (Read-Only by Default):**
    *   LiteSPM scans your agent's primary configuration file for **pre-existing native tools** (e.g., servers you previously added to `cline_mcp_settings.json` or `config.toml` manually).
    *   Detection is limited to documented on-disk config files known to the adapter.
    *   Pre-existing items are displayed with an `[External / Detected]` badge in **read-only mode** (no toggle controls) to prevent accidental config corruption.
    *   To bring an external tool under LiteSPM lifecycle management, select the explicit **"Adopt"** action.

---

## 4. Agent-Specific Integration Guides

### 4.1 Cline (VS Code Extension)
*   **How it works:** LiteSPM adds itself to `cline_mcp_settings.json` under `mcpServers.litespm`. This path is correct for the VS Code extension. The standalone Cline CLI uses a separate file (`~/.cline/data/settings/cline_mcp_settings.json`, and `~/.cline/mcp.json`) that LiteSPM does not currently manage.
*   **Accessing in Cline:**
    1.  Open VS Code and launch the Cline panel.
    2.  Click the MCP icon or type `/marketplace` in the prompt.
    3.  Browse available tools or view existing tools. Cline displays the green status dot in its MCP settings view.

### 4.2 Pi Agent (`pi-coding-agent`)
*   **How it works:** Minimalist terminal agent by Mario Zechner. LiteSPM registers under the `mcpServers` key in `~/.pi/agent/mcp.json` (project scope `.pi/mcp.json` is trust-gated; the legacy `~/.pi/config.json` and `~/.pi/mcp.json` are fallbacks) and creates `~/.pi/agent/extensions/litespm.ts`.
*   **Accessing in Pi:**
    1.  Launch `pi` in terminal.
    2.  Type `/marketplace search <term>` or run `/marketplace` to trigger the interactive capability selector.
    3.  Pi uses its minimal token footprint to query LiteSPM only on demand.

### 4.3 Grok Build
*   **How it works:** Host id is `grok-build`. LiteSPM injects the stdio bridge under `[mcp_servers.litespm]` in `~/.grok/config.toml` (project scope `.grok/config.toml`). On native Windows the config is `%USERPROFILE%\.grok\config.toml`; `%APPDATA%\Grok\config.toml` is only a legacy fallback.
*   **Accessing in Grok Build:**
    1.  Launch `grok` in terminal.
    2.  Type `/marketplace` to open the capability browser.
    3.  Grok detects the companion command hook and loads selected tools directly into its execution loop.

### 4.4 OpenAI Codex
*   **How it works:** LiteSPM injects the stdio bridge into `~/.codex/config.toml` under `[mcp_servers.litespm]`. Project scope is `.codex/config.toml`; native Windows user scope is `%USERPROFILE%\.codex\config.toml` (`%APPDATA%\Codex\config.toml` is a legacy fallback). The `$CODEX_HOME` environment variable overrides the config directory.
*   **Accessing in Codex:** Launch `codex` and type `/marketplace` or use the registered companion skill.

### 4.5 OpenCode
*   **How it works:** LiteSPM injects into `~/.config/opencode/opencode.json` (native Windows `%USERPROFILE%\.config\opencode\opencode.json`; `%APPDATA%\OpenCode\opencode.json` is a legacy fallback), supporting both v1 flat `mcp` and v2 nested `mcp.servers` layouts. Project scope also supports `opencode.json` in the project root or `.opencode/`.
*   **Local entry shape:** OpenCode local MCP entries require `"type": "local"` and a combined string array `"command"`:
    ```json
    {"mcp":{"servers":{"litespm":{"type":"local","command":["litespm","bridge","stdio","--host","opencode"]}}}}
    ```
*   **Accessing in OpenCode:** Type `/marketplace` in the OpenCode CLI.

### 4.6 Claude Code
*   **How it works:** User-scope servers live under the top-level `mcpServers` object in `~/.claude.json` (Windows `%USERPROFILE%\.claude.json`). Project scope is `.mcp.json` in the project root; local scope is a per-project entry inside `~/.claude.json`. `CLAUDE_CONFIG_DIR` overrides the config directory.
*   **Accessing in Claude Code:** Type `/marketplace` in the terminal to invoke the bootstrap skill.
