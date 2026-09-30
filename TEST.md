# Testing Guide: Cline, Pi Agent & Grok Build Adapters

This document details the test scenarios, mock configurations, and verification procedures for the initial trio of agent adapters: **Cline**, **Pi Agent**, and **Grok Build**.

---

## 1. Test Environment Setup & Mock Fixtures

Golden fixtures and test files are maintained in `fixtures/hosts/`:

```text
fixtures/hosts/
  ├── cline/
  │   ├── valid_cline_mcp_settings.json
  │   ├── with_comments_cline_mcp_settings.json
  │   └── existing_servers_cline_mcp_settings.json
  ├── pi/
  │   ├── valid_pi_config.json
  │   └── with_extensions_pi_config.json
  └── grok/
      ├── valid_grok_config.toml
      └── complex_grok_config.toml
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
        "GITHUB_PERSONAL_ACCESS_TOKEN": "ghp_mocktoken"
      },
      "disabled": true
    }
  }
}
```

### 1.2 Mock Pi Agent Fixture
`fixtures/hosts/pi/valid_mcp.json` (or `valid_pi_config.json`):
```json
{
  "model": "anthropic:claude-3-5-sonnet",
  "mcp": {
    "servers": {
      "local_bash": {
        "command": "/bin/bash"
      }
    }
  }
}
```

### 1.3 Mock Grok Build Fixture
`fixtures/hosts/grok/valid_grok_config.toml` (located at `~/.grok/config.toml`):
```toml
[general]
theme = "dark"
auto_reload = true

[mcp_servers.sqlite]
command = "uvx"
args = ["mcp-server-sqlite", "--db-path", "test.db"]
```

---

## 2. Test Cases & Verification Procedures

### Test Case 1: Automated Configuration Discovery
*   **Objective:** Verify that `DetectConfig` locates target files across operating systems (including candidate paths for Pi: `~/.pi/agent/mcp.json`, Grok: `~/.grok/config.toml`, Codex: `~/.codex/config.toml`).
*   **Procedure:**
    1. Set mock environment variables (`APPDATA` on Windows, `HOME` on Unix).
    2. Invoke `adapter.DetectConfig(ctx, ScopeUser)`.
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

### Test Case 5: Slash Command (`/litepsm`) Registration
*   **Objective:** Confirm slash command trigger is registered for the agent.
*   **Procedure:**
    1. For **Pi Agent**: verify `~/.pi/agent/extensions/litepsm.ts` exists and registers `/litepsm`.
    2. For **Cline**: verify custom instructions or prompt templates contain `/litepsm` trigger keyword.
    3. For **Grok Build**: verify `.grok/` command hook exists.

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

# Run specific tests for Cline, Pi Agent, and Grok Build
go test -v ./internal/host/cline/...
go test -v ./internal/host/piagent/...
go test -v ./internal/host/grokbuild/...

# Run end-to-end integration harness
go test -v ./tests/e2e/hosts_test.go
```
