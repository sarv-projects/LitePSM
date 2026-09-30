# Supported AI Agents & Integration Guide

LitePSM connects once to your AI agent host, enabling you to discover, install, update, and monitor MCP servers, skills, and plugins through a single unified interface.

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
                      │   LitePSM Local Core    │
                      │  (Daemon & CAS Store)   │
                      └─────────────────────────┘
```

---

## 1. Supported Agent Directory

| Agent Host | Environment | Configuration Format | Default Config Path |
|---|---|---|---|
| **Cline** | VS Code Extension | JSON (`cline_mcp_settings.json`) | Windows: `%APPDATA%\Code\User\globalStorage\saoudrizwan.claude-dev\settings\`<br>macOS: `~/Library/Application Support/Code/User/globalStorage/saoudrizwan.claude-dev/settings/`<br>Linux: `~/.config/Code/User/globalStorage/saoudrizwan.claude-dev/settings/` |
| **Pi Agent** | Terminal Coding Agent (`pi`) | JSON (`mcp.json` / `config.json`) + TS Extension | Unix: `~/.pi/agent/mcp.json` or `~/.pi/config.json`<br>Windows: `%USERPROFILE%\.pi\agent\mcp.json` or `%USERPROFILE%\.pi\config.json` |
| **Grok Build** | Terminal / IDE (`grok`) | TOML (`config.toml`) | Unix: `~/.grok/config.toml` (or project `.grok/config.toml`)<br>Windows: `%USERPROFILE%\.grok\config.toml` or `%APPDATA%\Grok\config.toml` |
| **Claude Code** | Terminal CLI (`claude`) | JSON (`~/.claude.json`) | Unix: `~/.claude.json`<br>Windows: `%USERPROFILE%\.claude.json` |
| **OpenAI Codex** | Terminal CLI (`codex`) | TOML (`config.toml`) | Unix: `~/.codex/config.toml`<br>Windows: `%USERPROFILE%\.codex\config.toml` or `%APPDATA%\Codex\config.toml` |
| **OpenCode** | Open-source CLI (`opencode`) | JSON (`opencode.json` - v1 `mcp` / v2 `mcp.servers`) | Unix: `~/.config/opencode/opencode.json`<br>Windows: `%APPDATA%\OpenCode\opencode.json` |

---

## 2. Setup Workflow

### Step 1: Install LitePSM Globally
Install via npm or download the pre-compiled binary:
```bash
npm install -g litepsm
```

### Step 2: Run the Interactive Setup
```bash
litepsm
```
1.  **Dynamic Runtime Fetching:** LitePSM contacts `/v1/current.json` to verify the latest verified adapter advisory metadata (supported versions and warnings; actual parsers are compiled in).
2.  **Select Your Agent:** Choose your agent from the interactive dropdown (e.g. `Cline`, `Pi Agent`, `Grok Build`, `Codex`, `Claude Code`, `OpenCode`).
3.  **Auto-Detection:** LitePSM scans your filesystem for the agent's configuration file.
4.  **Fallback Options:** If the configuration file is not found (e.g., custom installation directory), LitePSM prompts:
    *   *Enter path manually*
    *   *Print copy-paste snippet*
    *   *Retry detection*
5.  **Safe Atomic Merge:** LitePSM creates a backup in `DATA_ROOT/backups/`, preserves all existing entries and comments, injects the LitePSM Bridge entry, and writes the `/litepsm` command hook.

---

## 3. The In-Agent `/litepsm` Experience

Once configured, simply launch your agent and type:
```text
/litepsm
```

### UX Architecture: Portable Contract vs. Rich Host UI
*   **Portable Text / MCP Contract:** For terminal CLI agents (`claude`, `codex`, `grok`, `opencode`), `/litepsm` prints structured markdown tables, action shortcuts, and standard MCP discovery tools (`search_catalog`, `describe_capability`, `invoke_capability`, `list_installed`).
*   **Rich Host Renderer:** In hosts supporting rich extension panels or terminal TUIs (e.g. Cline in VS Code or Pi Agent TUI), `/litepsm` opens the **LitePSM Capability Panel** with 4 dedicated tabs:

```text
┌────────────────────────────────────────────────────────────────────────┐
│                          LitePSM Capabilities                          │
├──────────────┬──────────────┬──────────────┬───────────────────────────┤
│ [MCP SERVERS]│[AGENT SKILLS]│  [PLUGINS]   │     [INSTALLED (5)] ●     │
└──────────────┴──────────────┴──────────────┴───────────────────────────┘
```

### Tab 1: MCP Servers
*   Full search bar filtering by name, category, or transport (`stdio` / `Streamable HTTP`).
*   Displays verified badges, upstream GitHub links, and star counts.
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
    *   LitePSM scans your agent's primary configuration file for **pre-existing native tools** (e.g., servers you previously added to `cline_mcp_settings.json` or `config.toml` manually).
    *   Detection is limited to documented on-disk config files known to the adapter.
    *   Pre-existing items are displayed with an `[External / Detected]` badge in **read-only mode** (no toggle controls) to prevent accidental config corruption.
    *   To bring an external tool under LitePSM lifecycle management, select the explicit **"Adopt"** action.

---

## 4. Agent-Specific Integration Guides

### 4.1 Cline (VS Code Extension)
*   **How it works:** LitePSM adds itself to `cline_mcp_settings.json`.
*   **Accessing in Cline:**
    1.  Open VS Code and launch the Cline panel.
    2.  Click the MCP icon or type `/litepsm` in the prompt.
    3.  Browse available tools or view existing tools. Cline displays the green status dot in its MCP settings view.

### 4.2 Pi Agent (`pi-coding-agent`)
*   **How it works:** Minimalist terminal agent by Mario Zechner. LitePSM registers in `~/.pi/agent/mcp.json` (or `~/.pi/config.json`) and creates `~/.pi/agent/extensions/litepsm.ts`.
*   **Accessing in Pi:**
    1.  Launch `pi` in terminal.
    2.  Type `/litepsm search <term>` or run `/litepsm` to trigger the interactive capability selector.
    3.  Pi uses its minimal token footprint to query LitePSM only on demand.

### 4.3 Grok Build
*   **How it works:** LitePSM injects the stdio bridge into `~/.grok/config.toml` (or project `.grok/config.toml`).
*   **Accessing in Grok Build:**
    1.  Launch `grok` in terminal.
    2.  Type `/litepsm` to open the capability browser.
    3.  Grok detects the companion command hook and loads selected tools directly into its execution loop.

### 4.4 OpenAI Codex
*   **How it works:** LitePSM injects the stdio bridge into `~/.codex/config.toml` under `[mcp_servers.litepsm]`.
*   **Accessing in Codex:** Launch `codex` and type `/litepsm` or use the registered companion skill.

### 4.5 OpenCode
*   **How it works:** LitePSM injects into `~/.config/opencode/opencode.json`, supporting both v1 root `mcp` and v2 nested `mcp.servers` layouts.
*   **Accessing in OpenCode:** Type `/litepsm` in the OpenCode CLI.

### 4.6 Claude Code
*   **How it works:** LitePSM injects under `mcpServers.litepsm` in `~/.claude.json`.
*   **Accessing in Claude Code:** Type `/litepsm` in the terminal to invoke the bootstrap skill.
