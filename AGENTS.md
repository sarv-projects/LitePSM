# Supported AI Agents & Integration Guide

**LiteSPM** — *The Lightweight Skill & Package Manager for AI Agents*. It connects once to your AI agent host, enabling you to discover, install, update, and monitor MCP servers, skills, and plugins through a single unified interface.

> **State.** Install, invocation, and the catalog release tree are **not** end-to-end today.
> [STATUS.md](STATUS.md) is the single source of truth for what each subsystem actually does; this
> guide states the wiring, not the aspiration.

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
| **Cline** | VS Code Extension (plus the Cline CLI / JetBrains clients) | JSON (`cline_mcp_settings.json`) | **Current default (written):** `~/.cline/data/settings/cline_mcp_settings.json`, relocated by `CLINE_MCP_SETTINGS_PATH` or `CLINE_DATA_DIR`. **Legacy (read-only fallback, probed last):** the VS Code `globalStorage` path — Windows `%APPDATA%\Code\User\globalStorage\saoudrizwan.claude-dev\settings\`, macOS `~/Library/Application Support/Code/User/globalStorage/…`, Linux `~/.config/Code/User/globalStorage/…` (plus `Code - Insiders`); current Cline reads it only in a one-shot migration (`internal/host/cline.go`) |
| **Pi Agent** | Terminal Coding Agent (`pi`) | JSON (`mcp.json`) + TS Extension | `~/.pi/agent/mcp.json` (Windows `%USERPROFILE%\.pi\agent\mcp.json`), relocated by `PI_CODING_AGENT_DIR`; written under `mcpServers`, the only container Pi reads. Project `.pi/mcp.json` is host-documented but **not** scanned by the adapter, and the former `~/.pi/config.json` / `~/.pi/mcp.json` fallbacks were **removed** — no Pi release reads them (`internal/host/piagent.go`) |
| **Grok Build** | Terminal / IDE (`grok`) [id: `grok-build`] | TOML (`config.toml`) | `$GROK_HOME/config.toml` (default `~/.grok/config.toml`; Windows `%USERPROFILE%\.grok\config.toml`). Project `.grok/config.toml` is host-documented but not scanned; `%APPDATA%\Grok\config.toml` was **removed** as a fallback (`internal/host/grokbuild.go`) |
| **Claude Code** | Terminal CLI (`claude`) | JSON (`~/.claude.json`) | Unix: `~/.claude.json`<br>Windows: `%USERPROFILE%\.claude.json`<br>Project: `.mcp.json` in project root (local scope entry in `~/.claude.json`)<br>Host-side: `CLAUDE_CONFIG_DIR` overrides the config directory **for Claude Code itself**; LiteSPM's adapter does not read it (`ARCH/16` §3.4) |
| **OpenAI Codex** | Terminal CLI (`codex`) | TOML (`config.toml`) | `$CODEX_HOME/config.toml` (default `~/.codex/config.toml`; Windows `%USERPROFILE%\.codex\config.toml`) — the adapter **does** honour `$CODEX_HOME`. Project `.codex/config.toml` is host-documented but not scanned; `%APPDATA%\Codex\config.toml` was **removed** as a fallback (`internal/host/codex.go`) |
| **OpenCode** | Open-source CLI (`opencode`) | JSON (`opencode.jsonc` preferred, else `opencode.json`) | `$OPENCODE_CONFIG_DIR/{opencode.jsonc,opencode.json}` when set, else `$XDG_CONFIG_HOME/opencode/` (default `~/.config/opencode/`), `.jsonc` first — on every OS, because OpenCode resolves the directory through xdg-basedir. Project scope and `%APPDATA%\OpenCode\opencode.json` are **not** read by any release and are no longer probed. One layout only: servers are direct members of `mcp` (`internal/host/opencode.go`) |

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
1.  **Compiled-In Advisories:** The wizard prints advisory metadata from strings compiled into the
    binary (`cmd/litespm/wizard.go:138-143`). It performs **no** `/v1/current.json` fetch at setup
    time; the only network fetch path is `catalog sync`, which succeeds against the live origin
    ([STATUS.md](STATUS.md) §2).
2.  **Select Your Agent:** Choose your agent from the interactive dropdown (for Claude Code, Codex, OpenCode, and many more).
3.  **Auto-Detection:** LiteSPM scans your filesystem for the agent's configuration file.
4.  **Fallback Options:** If the configuration file is not found (e.g., custom installation directory), LiteSPM prompts:
    *   *Enter path manually*
    *   *Print copy-paste snippet*
    *   *Retry detection*
5.  **Safe Atomic Merge:** LiteSPM creates a backup in `DATA_ROOT/backups/`, preserves all existing entries and comments, and injects a **single** version-pinned LiteSPM Bridge entry (`litespm bridge stdio --host <agent-id>`). This is an **MCP-entry merge only**: no instruction, skill, or command file is written into the host, and individual capabilities are resolved by the daemon at runtime, so no per-capability host snippets are generated.

---

## 3. The In-Agent `/marketplace` Experience

Once configured, simply launch your agent and type:
```text
/marketplace
```

### UX Architecture: Portable Contract vs. Rich Host UI
*   **Portable Text / MCP Contract:** For terminal CLI agents (`claude`, `codex`, `grok`, `opencode`), `/marketplace` exposes 12 Bridge tools. Those that resolve against the daemon today: `search_catalog`, `get_extension`, `prepare_install`, `list_installed`, `load_skill`, `read_skill_resource`, `search_capabilities`, `describe_capability`, `invoke_capability`. `request_install` reaches `install.execute`, which — behind the plan + human-approval gate in `cmd/litespm/install_authz.go` — **completes for skill listings** (real skill files through the skills ledger) **and for MCP-server listings** (the server is registered in each target host's config), and fails closed with `LPSM-ARTIFACT-UNAVAILABLE` for **plugin** listings only, whose artifact source is not wired ([STATUS.md](STATUS.md) §3). Only `get_invocation` and `cancel_invocation` return JSON-RPC `-32601` ("not implemented"), because the asynchronous invocation registry is `ARCH/34` (`DESIGNED`).
*   **Rich Host Renderer:** In hosts supporting rich extension panels or terminal TUIs (e.g. Cline in VS Code or Pi Agent TUI), `/marketplace` opens the **LiteSPM Capability Panel** with 4 dedicated tabs:

```text
┌────────────────────────────────────────────────────────────────────────┐
│                          LiteSPM Capabilities                          │
├──────────────┬──────────────┬──────────────┬───────────────────────────┤
│ [MCP SERVERS]│[AGENT SKILLS]│  [PLUGINS]   │     [INSTALLED (n)] ●     │
└──────────────┴──────────────┴──────────────┴───────────────────────────┘
```

`n` is a placeholder: the renderer prints the live item count,
`fmt.Sprintf("…[INSTALLED (%d)]…", len(items))` (`internal/bridge/shim.go:520`), so the header
reads `[INSTALLED (0)]` on an empty install and grows with the ledger plus detected externals.

### Tab 1: MCP Servers
*   Full search bar filtering by name, category, or transport (`stdio` / `Streamable HTTP`).
*   Displays verified badges and upstream GitHub links. No star, download or install counts are shown: no upstream source exposes them, so the catalog publishes none rather than an estimate.
*   One-click install button that prepares an `InstallPlan`. **Execution completes for skills** (installs files through the skills ledger) **and for MCP servers** (registers the server in each target host's config), both behind the plan + human-approval gate; **plugin** listings still need an artifact source ([STATUS.md](STATUS.md) §3).

### Tab 2: Agent Skills
*   Search portable `SKILL.md` workflows from `agentskills.io`.
*   Preview instructions, tools used, and triggers.

### Tab 3: Plugins
*   Curated multi-component toolkits and extensions for complex workflows.

### Tab 4: Installed & Detected External Capabilities
The **Installed** view lists ledger-backed installs plus read-only components detected in the host's
own config file. It does **not** assert runtime health:

1.  **No health light is fabricated.** LiteSPM does not health-check installed capabilities, so a
    capability whose status was not observed renders as `— Unknown`. The `● Ready` / `🟡 Needs Auth`
    / `○ Stopped` lights exist in the renderer but are only shown for a status the daemon can
    actually observe (`internal/bridge/shim.go:514-560`).
2.  **Detected External Capabilities (Read-Only):**
    *   LiteSPM scans the agent's primary configuration file for **pre-existing native tools**
        (e.g. servers previously added to `cline_mcp_settings.json` or `config.toml`).
    *   Detection is limited to documented on-disk config files known to the adapter.
    *   Pre-existing items are rendered with an `[External / Detected]` badge in **read-only mode**
        (no toggle controls).
    *   **`Adopt` is not implemented.** The panel renders an `(Adopt)` label
        (`internal/bridge/shim.go:552-553`), but no adopt handler or tool exists; bringing an
        external tool under management is `DESIGNED` ([STATUS.md](STATUS.md) §5).

---

## 4. Agent-Specific Integration Guides

### 4.1 Cline (VS Code Extension)
*   **How it works:** LiteSPM adds itself to `cline_mcp_settings.json` under `mcpServers.litespm`. The file it writes is the **shared** one every current Cline client reads — `~/.cline/data/settings/cline_mcp_settings.json` (`CLINE_MCP_SETTINGS_PATH` or `CLINE_DATA_DIR` relocate it). The VS Code extension's `globalStorage` copy (`%APPDATA%\Code\User\globalStorage\saoudrizwan.claude-dev\settings\`, `~/Library/Application Support/Code/User/globalStorage/…`, `~/.config/Code/User/globalStorage/…`, plus `Code - Insiders`) is **legacy**: current Cline reads it only in a one-shot migration, so LiteSPM probes it last and only for a client too old to have migrated (`internal/host/cline.go`). There is no separate, unmanaged CLI file.
*   **Accessing in Cline:**
    1.  Open VS Code and launch the Cline panel.
    2.  Click the MCP icon or type `/marketplace` in the prompt.
    3.  Browse available tools or view existing tools. Cline displays the green status dot in its MCP settings view.

### 4.2 Pi Agent (`pi-coding-agent`)
*   **How it works:** Minimalist terminal agent by Mario Zechner. LiteSPM registers under the `mcpServers` key — the only container Pi reads — in `<agent-dir>/mcp.json`, where the agent directory is `~/.pi/agent` (`%USERPROFILE%\.pi\agent` on Windows) unless `PI_CODING_AGENT_DIR` overrides it, and creates `<agent-dir>/extensions/litespm.ts`. There is no project-scope or legacy-fallback file: `~/.pi/config.json` and `~/.pi/mcp.json` appear in no Pi documentation or source, and probing them used to write the bridge into a file Pi never reads (`internal/host/piagent.go`).
*   **Accessing in Pi:**
    1.  Launch `pi` in terminal.
    2.  Type `/marketplace search <term>` or run `/marketplace` to trigger the interactive capability selector.
    3.  Pi uses its minimal token footprint to query LiteSPM only on demand.

### 4.3 Grok Build
*   **How it works:** Host id is `grok-build`. LiteSPM injects the stdio bridge under `[mcp_servers.litespm]` in `$GROK_HOME/config.toml` (default `~/.grok/config.toml`; on native Windows `%USERPROFILE%\.grok\config.toml`). `%APPDATA%\Grok\config.toml` appears in no xAI documentation and was **removed** as a fallback; the host-documented project file `.grok/config.toml` is not scanned by the adapter (`internal/host/grokbuild.go`, `ARCH/16` §3).
*   **Accessing in Grok Build:**
    1.  Launch `grok` in terminal.
    2.  Type `/marketplace` to open the capability browser.
    3.  Grok detects the companion command hook and loads selected tools directly into its execution loop.

### 4.4 OpenAI Codex
*   **How it works:** LiteSPM injects the stdio bridge into `$CODEX_HOME/config.toml` (default `~/.codex/config.toml`; native Windows `%USERPROFILE%\.codex\config.toml`) under `[mcp_servers.litespm]`. The adapter **does** honour `$CODEX_HOME`, and the `%APPDATA%\Codex\config.toml` fallback was **removed** — it appears in no OpenAI documentation (the documented Windows system path is the administrator-owned `%ProgramData%\OpenAI\Codex\config.toml`, which LiteSPM does not write). The host-documented project file `.codex/config.toml` is not scanned by the adapter (`internal/host/codex.go`, `ARCH/16` §3.5).
*   **Accessing in Codex:** Launch `codex` and type `/marketplace` or use the registered companion skill.

### 4.5 OpenCode
*   **How it works:** LiteSPM writes into `$OPENCODE_CONFIG_DIR/opencode.jsonc` (preferred) or `opencode.json`, falling back to `$XDG_CONFIG_HOME/opencode/` — default `~/.config/opencode/` on Unix and `%USERPROFILE%\.config\opencode\` on Windows — because OpenCode resolves its config directory through xdg-basedir on every OS. `%APPDATA%\OpenCode\opencode.json` is read by no OpenCode release and is not probed; neither is a project-scope `opencode.json` / `.opencode/` (`internal/host/opencode.go`, `ARCH/16` §3).
*   **Layout:** OpenCode has **one** documented layout — servers are direct members of the `mcp` object — so the nested `mcp.servers` shape earlier revisions wrote is pruned when it is empty. Both shapes are still *read* when detecting pre-existing tools, so nothing already configured is lost.
*   **Local entry shape:** OpenCode local MCP entries require `"type": "local"` and a combined string array `"command"`:
    ```json
    {"mcp":{"litespm":{"type":"local","command":["litespm","bridge","stdio","--host","opencode"]}}}
    ```
*   **Accessing in OpenCode:** Type `/marketplace` in the OpenCode CLI.

### 4.6 Claude Code
*   **How it works:** User-scope servers live under the top-level `mcpServers` object in `~/.claude.json` (Windows `%USERPROFILE%\.claude.json`). Project scope is `.mcp.json` in the project root; local scope is a per-project entry inside `~/.claude.json`. `CLAUDE_CONFIG_DIR` overrides the config directory **for the host tool only**; LiteSPM's adapter does not read it — `DetectConfig` returns `$HOME/.claude.json` unconditionally (`internal/host/claudecode.go:30-33`, `ARCH/16` §3.4).
*   **Accessing in Claude Code:** Type `/marketplace` in the terminal to invoke the bootstrap skill.
