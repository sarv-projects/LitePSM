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

### Test Case 2: Missing Configuration Fallback Flow (`DESIGNED` — no non-interactive setup)
*   **Objective:** Verify the interactive CLI gracefully prompts when configuration is missing.
*   **Status:** There is **no non-interactive `litespm setup <agent>`**. `setup`/`init` simply
    launches the interactive wizard (`cmd/litespm/main.go:62-63`), which requires a TTY. The
    behaviour below is the intended design and is not currently scriptable.
*   **Procedure (intended):**
    1. Run `litespm` (wizard) in an environment without VS Code and choose Cline.
    2. Assert stdout displays: `[!] Unable to locate default configuration file`.
    3. Pass custom path via stdin (`fixtures/hosts/cline/existing_servers_cline_mcp_settings.json`).
    4. Assert configuration completes successfully.

### Test Case 3: Pre-Edit Backup & Non-Destructive Merge
*   **Objective:** Ensure unrelated keys and pre-existing servers are never overwritten.
*   **Procedure:**
    1. Apply setup to `existing_servers_cline_mcp_settings.json`.
    2. Verify `DATA_ROOT/backups/cline/<timestamp>/config.bak` was created with identical pre-edit content.
    3. Assert `filesystem` and `github` entries remain intact in the modified file.
    4. Assert `mcpServers.litespm` is injected with the version-pinned executable path.

### Test Case 4: Pre-Existing Tool Discovery & Read-Only Detection (Installed Tab) (`DESIGNED` — `adopt_tool` does not exist)
*   **Objective:** Verify that LiteSPM scans and detects pre-existing native tools in read-only mode.
*   **Status:** Detection is implemented (`tools.list` returns external components from
    `DetectPreExistingComponents`, `cmd/litespm/main.go:1262-1276`). There is **no `adopt_tool()`
    tool and no `LPSM-HOST-READONLY-EXTERNAL` error code**; adopting an external tool is not
    implemented ([STATUS.md](STATUS.md) §5).
*   **Procedure:**
    1. Boot Bridge Shim with `--host cline`.
    2. Call MCP tool `list_installed()`.
    3. Assert the returned list includes the pre-existing tools marked `isExternal: true` with no
       health status (the daemon does not health-check installs, so status renders `— Unknown`
       rather than `ready`/`disabled`).
    4. Assert no mutation of external entries is possible (there is no adopt/toggle tool).

### Test Case 5: Slash Command (`/marketplace`) Registration
*   **Objective:** Confirm the host descriptor advertises the `/marketplace` trigger.
*   **Status:** Host adapters perform an **MCP-entry merge only**; they record
    `SlashCommandTrigger` metadata (`internal/host/*.go`) but do **not** write an extension, custom
    instruction, or command hook into the host. Assertions 1 and 3 below therefore describe an
    intended projection, not shipped behavior.
*   **Procedure (implemented part):** Assert the adapter descriptor for each host reports
    `SlashCommandTrigger: "/marketplace"` (`internal/host/targets_data_test.go:376`).

### Test Case 6: Dynamic Runtime Adapter Advisory Fetching (`DESIGNED` — not implemented)
*   **Objective:** Verify the client queries the remote manifest at runtime for compatibility advisories without executing remote code.
*   **Status:** The setup wizard does **not** fetch `/v1/current.json` and there is **no
    `--catalog-url` flag**; advisories are compiled-in strings (`cmd/litespm/wizard.go:138-143`).
    Runtime advisory fetching is not implemented.
*   **Procedure (intended):**
    1. Start local mock HTTP server serving `/v1/current.json` (with its `advisories` array; there is no separate `/v1/adapters.json` endpoint).
    2. Point the client at it via `LITESPM_REGISTRY_URL` (the implemented override; no `--catalog-url` flag exists).
    3. Verify that advisory metadata and version warnings from `current.json` appear without executing remote code.
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

# Run end-to-end integration harness (package `test`: canary, conformance, hostile-archive fuzz)
go test -v ./test/...
```
