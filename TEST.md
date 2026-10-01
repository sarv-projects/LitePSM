# Testing Guide: Cline, Pi Agent & Grok Build Adapters

This document details the test scenarios, mock configurations, and verification procedures for the initial trio of agent adapters: **Cline**, **Pi Agent**, and **Grok Build**.

---

## 1. Test Environment Setup & Mock Fixtures

Golden fixtures and test files are maintained in `fixtures/hosts/`:

```text
fixtures/hosts/
  ├── claude/
  │   └── claude.json
  ├── cline/
  │   └── existing_servers_cline_mcp_settings.json
  ├── codex/
  │   └── config.toml
  ├── grok/
  │   └── valid_grok_config.toml
  ├── opencode/
  │   ├── opencode_v1.json
  │   └── opencode_v2.json
  └── pi/
      └── valid_mcp.json
```

### 1.1 Mock Cline Fixture with Pre-Existing Servers
`fixtures/hosts/cline/existing_servers_cline_mcp_settings.json`:
```json
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/Users/test/workspace"],
      "disabled": false
    },
    "github": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": {
        "GITHUB_PERSONAL_ACCESS_TOKEN": "ghp_mocktoken12345"
      },
      "disabled": true
    }
  }
}
```

### 1.2 Mock Pi Agent Fixture
`fixtures/hosts/pi/valid_mcp.json`:
```json
{
  "model": "anthropic:claude-3-5-sonnet",
  "mcp": {
    "servers": {
      "local_bash": {
        "command": "/bin/bash",
        "args": ["-l"]
      }
    }
  }
}
```
Pi's current configuration key is `mcpServers`; this fixture exercises the nested `mcp.servers` shape. Project scope is `.pi/mcp.json` (trust-gated).

### 1.3 Mock Grok Build Fixture
`fixtures/hosts/grok/valid_grok_config.toml` (located at `~/.grok/config.toml`; project scope `.grok/config.toml`):
```toml
# Grok Build configuration file
[general]
theme = "dark"
auto_reload = true

[mcp_servers.sqlite]
command = "uvx"
args = ["mcp-server-sqlite", "--db-path", "test.db"]
```

### 1.4 Additional Host Fixtures
*   `fixtures/hosts/claude/claude.json` — user-scope `mcpServers` layout for Claude Code.
*   `fixtures/hosts/codex/config.toml` — `[mcp_servers.*]` layout for OpenAI Codex.
*   `fixtures/hosts/opencode/opencode_v1.json` — flat `mcp.<name>` layout.
*   `fixtures/hosts/opencode/opencode_v2.json` — nested `mcp.servers` layout.

---

## 2. Test Cases & Verification Procedures

### Test Case 1: Automated Configuration Discovery
*   **Objective:** Verify that `DetectConfig` locates target files across operating systems (including candidate paths for Pi: `~/.pi/agent/mcp.json`, Grok: `~/.grok/config.toml`, Codex: `~/.codex/config.toml`, Claude Code: `~/.claude.json`, OpenCode: `~/.config/opencode/opencode.json`).
*   **Procedure:**
    1. Set mock environment variables (`APPDATA` on Windows, `HOME` on Unix).
    2. Invoke `adapter.DetectConfig(ctx, domain.ScopeUser)`.
    3. Assert returned path matches the platform specification.

### Test Case 2: Missing Configuration Fallback Flow
*   **Objective:** Verify the interactive CLI gracefully prompts when configuration is missing.
*   **Procedure:**
    1. Run `litepsm setup cline` in an environment without VS Code.
    2. Assert stdout displays: `[!] Unable to locate default configuration file`.
    3. Pass custom path via stdin (`fixtures/hosts/cline/existing_servers_cline_mcp_settings.json`).
    4. Assert configuration completes successfully.

### Test Case 3: Pre-Edit Backup & Non-Destructive Merge
*   **Objective:** Ensure unrelated keys and pre-existing servers are never overwritten.
*   **Procedure:**
    1. Apply setup to `existing_servers_cline_mcp_settings.json`.
    2. Verify `DATA_ROOT/backups/cline/<timestamp>/config.bak` was created with identical pre-edit content.
    3. Assert `filesystem` and `github` entries remain intact in the modified file.
    4. Assert `mcpServers.litepsm` is injected with the version-pinned executable path.

### Test Case 4: Pre-Existing Tool Discovery & Read-Only Detection (Installed Tab)
*   **Objective:** Verify that LitePSM scans and correctly detects pre-existing native tools in read-only mode.
*   **Procedure:**
    1. Boot Bridge Shim with `--host cline`.
    2. Call MCP tool `list_installed()`.
    3. Assert returned list includes:
       - `filesystem` $\rightarrow$ `status: "ready"`, `greenLight: true`, `isExternal: true`, `readOnly: true`
       - `github` $\rightarrow$ `status: "disabled"`, `greenLight: false`, `isExternal: true`, `readOnly: true`
       - `litepsm` $\rightarrow$ `status: "ready"`, `greenLight: true`, `isExternal: false`
    4. Assert attempting to mutate/toggle external tools without `adopt_tool()` returns `LPSM-HOST-READONLY-EXTERNAL`.

### Test Case 5: Slash Command (`/marketplace`) Registration
*   **Objective:** Confirm slash command trigger is registered for the agent.
*   **Procedure:**
    1. For **Pi Agent**: verify `~/.pi/agent/extensions/litepsm.ts` exists and registers `/marketplace`.
    2. For **Cline**: verify custom instructions or prompt templates contain `/marketplace` trigger keyword.
    3. For **Grok Build**: verify `.grok/config.toml` command hook exists.

### Test Case 6: Dynamic Runtime Adapter Advisory Fetching
*   **Objective:** Verify the client queries the remote manifest at runtime for compatibility advisories without executing remote code.
*   **Procedure:**
    1. Start local mock HTTP server serving `/v1/current.json` and `/v1/adapters.json`.
    2. Execute `litepsm` with `--catalog-url http://127.0.0.1:<mock-port>`.
    3. Verify that adapter advisory metadata and version warnings from `adapters.json` appear in the interactive dropdown.
    4. Disconnect network and verify clean fallback to compiled-in adapters.

---

## 3. Automated Test Execution

Run the adapter test suite:

```bash
# Run unit tests for all host adapters
go test -v ./internal/host/...

# Adapters are separate files in the single `host` package (not subpackages),
# so adapter-specific tests are selected with -run filters, e.g.:
go test -v ./internal/host/... -run 'Test(PiAgent|GrokBuild|Codex|ClaudeCode|OpenCode)Adapter'

# Run end-to-end integration harness
go test -v ./tests/e2e/hosts_test.go
```
