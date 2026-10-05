# Client and Installation Design

## 1. Entry Points and Interactive Workflows

LiteSPM provides both an interactive terminal interface for humans and a structured programmatic interface for agent hosts.

```text
┌────────────────────────────────────────────────────────────────────────┐
│                        User / Terminal Client                          │
│                                                                        │
│   $ litespm (Interactive TUI Wizard)                                   │
│     ├── Agent Selector Dropdown (Claude Code, Codex, Grok Build, OpenCode, Cline) │
│     ├── Automated Host Configuration Path Detection                    │
│     ├── Manual Path Prompt & Fallback Guidance                         │
│     └── Passive Catalog Update Notice                                  │
│                                                                        │
│   $ litespm setup <agent> (Scriptable Non-Interactive Setup)           │
│   $ litespm search / info / install / update / remove / doctor         │
│   $ litespm skills add <source> (Skill installer: clone, select, copy) │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│                        Agent Host Client                               │
│                                                                        │
│   In-Agent /marketplace Slash Command & Progressive Tool Discovery         │
│     ├── search_catalog(query, kinds, limit)                            │
│     ├── describe_capability(capability_id)                             │
│     ├── prepare_install(listing_id, version) -> InstallPlan v2         │
│     └── invoke_capability(capability_id, arguments)                    │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Interactive Setup Wizard & Agent Auto-Detection

Running `litespm` without subcommands opens an interactive Terminal User Interface (TUI):

### Step 1: Agent Selection Dropdown
```text
? Select your target AI Agent to configure:
  [●] Claude Code
  [ ] OpenAI Codex
  [ ] Grok Build
  [ ] OpenCode
  [ ] Cline (VS Code Extension)
  [ ] Generic MCP Configuration (Export JSON)
```

### Step 2: Automated Path Discovery & Detection Matrix
LiteSPM probes known default configuration locations by platform:

| Target Agent | Linux / macOS Default Path | Windows Default Path |
|---|---|---|
| **Claude Code** | `~/.claude.json` (project `.mcp.json`; local scope entry inside `~/.claude.json`) | `%USERPROFILE%\.claude.json` (`CLAUDE_CONFIG_DIR` overrides) |
| **OpenAI Codex** | `~/.codex/config.toml` (project `.codex/config.toml`) | `%USERPROFILE%\.codex\config.toml` (`%APPDATA%\Codex\config.toml` is a legacy fallback; `$CODEX_HOME` overrides) |
| **Grok Build** | `~/.grok/config.toml` (project `.grok/config.toml`) | `%USERPROFILE%\.grok\config.toml` (`%APPDATA%\Grok\config.toml` is a legacy fallback) |
| **Pi Agent** | `~/.pi/agent/mcp.json` (project `.pi/mcp.json`, trust-gated; legacy `~/.pi/config.json` / `~/.pi/mcp.json`) | `%USERPROFILE%\.pi\agent\mcp.json` (legacy `%USERPROFILE%\.pi\config.json` / `%USERPROFILE%\.pi\mcp.json`) |
| **OpenCode** | `~/.config/opencode/opencode.json` (project `opencode.json` or `.opencode/`) | `%USERPROFILE%\.config\opencode\opencode.json` (`%APPDATA%\OpenCode\opencode.json` is a legacy fallback) |
| **Cline** | `~/.config/Code/User/globalStorage/saoudrizwan.claude-dev/...` (Cline CLI: `~/.cline/data/settings/cline_mcp_settings.json`, not managed) | `%APPDATA%\Code\User\globalStorage\saoudrizwan.claude-dev\...` (Cline CLI: `~/.cline/data/settings/cline_mcp_settings.json`, not managed) |

### Step 3: Graceful Fallback Options
If the configuration file is absent, LiteSPM prompts the user:
```text
[!] Unable to locate default configuration file for Claude Code.
? Choose an action:
  > Enter path manually: [/path/to/claude.json]
  > Print manual setup snippet (copy & paste into agent config)
  > Retry detection
  > Cancel
```

### Step 4: Safe Atomic Configuration Injection
1.  **Backup:** Copies target configuration to `DATA_ROOT/backups/<host-id>/<timestamp>-<digest>/config.bak`.
2.  **Parse & Merge:** Parses JSON/TOML, preserving comments and formatting where possible. Injects a single version-pinned `litespm` bridge entry for the selected host (below shown for `claude-code`):
    ```json
    {
      "mcpServers": {
        "litespm": {
          "command": "/usr/local/bin/litespm",
          "args": ["bridge", "stdio", "--host", "claude-code"]
        }
      }
    }
    ```
    Only this one `litespm` entry is registered per host; the `--host <agent-id>` argument tells the daemon which host is calling, and individual capabilities are resolved by the daemon at runtime (no per-capability host snippets are generated).
3.  **Atomic Replacement:** Writes to a temporary file on the same filesystem, validates syntax, and renames atomically.

---

## 3. Local Storage Layout (`DATA_ROOT`)

LiteSPM maintains all user state, database files, and package trees within platform-standard data directories:
*   **Windows:** `%LOCALAPPDATA%\LiteSPM`
*   **macOS:** `~/Library/Application Support/LiteSPM`
*   **Linux:** `$XDG_DATA_HOME/litespm` (default: `~/.local/share/litespm`)

```text
DATA_ROOT/
  ├── state.db                     # SQLite database in WAL mode (22 core tables)
  ├── state.db-wal                 # SQLite Write-Ahead Log
  ├── state.db-shm                 # SQLite Shared Memory index
  ├── artifacts/                   # Content-Addressed Store (CAS) of raw downloads
  │   └── sha256/<2-hex>/<digest>/raw
  ├── trees/                       # Immutable unpacked package directories
  │   └── sha256/<2-hex>/<tree-digest>/...
  ├── runtimes/                    # Materialized runtime virtualenvs/caches
  │   └── <runtime-id>/...
  ├── staging/                     # In-flight installation scratch space
  │   └── <operation-id>/...
  ├── backups/                     # Pre-mutation host configuration backups
  │   └── <host-id>/<timestamp>-<digest>/...
  └── cache/                       # Cached catalog releases and search shards
      └── catalog/<release-id>/...

CONFIG_ROOT/                       # User configuration
  └── config.toml

RUNTIME_ROOT/                      # Ephemeral IPC sockets and daemon locks
  ├── daemon.lock                  # Exclusive instance process lock
  ├── daemon.pid                   # Current daemon process ID
  └── daemon.sock                  # Unix domain socket (Linux/macOS)
                                   # (Windows uses Named Pipe: \\.\pipe\litespm-daemon-<hash>)
```

---

## 4. Pure Dependency Resolver Algorithm

LiteSPM's dependency resolver is pure and deterministic. It performs no disk I/O or network fetches during resolution:
1.  **Input:** Target `ListingId` and requested version constraint.
2.  **Breadth-First / Depth-First Traversal:** Recursively evaluates dependency declarations against the catalog release snapshot.
3.  **Cycle Detection:** Maintains a visited traversal stack; if a dependency node re-occurs, halts with `LPSM-RESOLVE-CYCLE`.
4.  **Constraint Intersection:** Determines if requested version ranges intersect.
    *   If compatible versions exist, selects the highest stable immutable release.
    *   If conflicting constraints exist without an intersection, halts immediately with `LPSM-RESOLVE-CONFLICT`.
5.  **Output:** An immutable `InstallPlan` containing exact versions, content digests, and resolved dependency order.

---

## 5. Update and Upstream Repack Handling

### 5.1 Passive Update Checks
Whenever a user runs `litespm` or an agent invokes `/marketplace`, the client performs a passive read of `/v1/current.json`.
*   If the remote catalog release sequence exceeds the cached sequence, it downloads the release index.
*   It compares installed versions against catalog versions.
*   **Zero Local Mutation:** It displays update availability to the user or agent, but **never mutates local files** without an explicit update command.

### 5.2 Update Delta Evaluation
When `litespm update <listing-id>` is invoked, the engine generates an update plan detailing:
*   Version jump and artifact SHA-256 digest delta.
*   Changes in declared effects (e.g., added filesystem or network access).
*   Provider launch argument changes.
*   New or removed tools and skills.

### 5.3 Upstream Repack Detection
If an upstream source re-publishes the same version string with a modified artifact digest:
$$\text{Installed Digest } \neq \text{New Upstream Digest for identical version}$$
1.  LiteSPM flags the artifact as `status: "repacked/mutated-upstream"`.
2.  Automatic installation is blocked.
3.  The user is warned that upstream maintainers modified release artifacts in-place, requiring explicit confirmation to proceed.

---

## 6. Removal and Garbage Collection Algorithm

1.  **Identify Install Record:** Queries SQLite for the target `InstallId` and resolves all associated components.
2.  **Active Session Check:** Queries `provider_sessions`; if a managed provider process is currently running, stops the process gracefully (`SIGTERM` / `WM_CLOSE`).
3.  **Database Commit:** In a single SQLite transaction, removes `installs`, `install_components`, `capabilities`, and `capability_grants` records.
4.  **Unreferenced CAS Pruning:** Scans `trees/` and `artifacts/`. Deletes directories only if no remaining active installation references their content digests.
5.  **Host Config Cleanup:** When uninstalling the Bridge entry from an agent host, checks if the configuration entry still matches LiteSPM's recorded fingerprint. If modified by the user, leaves the file intact and prints manual cleanup instructions.
