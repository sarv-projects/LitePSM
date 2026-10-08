# Client and Installation Design

## 1. Entry Points and Interactive Workflows

> **Phase-J command surface (`DESIGNED`).** The planned `copy`, `list`,
> `inventory`, orient commands (`status`/`diff`/`why`/`outdated`) and the
> `help` redesign are specified in [ARCH/38](38-CLI-PRODUCT-SURFACE.md) and
> are not implemented; this document covers what ships today.

LiteSPM provides both an interactive terminal interface for humans and a structured programmatic interface for agent hosts.

```text
┌────────────────────────────────────────────────────────────────────────┐
│                        User / Terminal Client                          │
│                                                                        │
│   $ litespm (Interactive Setup Wizard)                                 │
│     ├── Agent Selector (6 hosts: Cline, Pi Agent, Grok Build,          │
│     │   Claude Code, OpenAI Codex, OpenCode)                           │
│     ├── Automated Host Configuration Path Detection                    │
│     ├── Manual Path Prompt & Fallback Guidance                         │
│     └── Compiled-In Advisory Notice (no network fetch)                 │
│                                                                        │
│   $ litespm host setup <host-id> (Scriptable Non-Interactive Setup)    │
│   $ litespm search / install / uninstall / catalog sync / doctor       │
│   $ litespm skills add <source> (Skill installer: clone, select, copy) │
│   $ litespm agent list|resolve · skills list|update|remove             │
│   $ litespm self-update · daemon serve · bridge stdio [--host <id>]    │
└────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│                        Agent Host Client                               │
│                                                                        │
│   In-Agent /marketplace Slash Command & Progressive Tool Discovery         │
│     ├── search_catalog(query, kinds, limit)          — resolves        │
│     ├── get_extension(id) / list_installed()         — resolves        │
│     ├── load_skill(id) / read_skill_resource(...)    — resolves        │
│     ├── prepare_install(id, version) -> InstallPlan  — resolves        │
│     ├── request_install(planId, approvalToken)       — completes for    │
│     │     skills (files), MCP (host entry); plugins need a locator       │
│     └── search_capabilities / describe_capability /                    │
│           invoke_capability / get_invocation /                         │
│           cancel_invocation                      — JSON-RPC -32601     │
└────────────────────────────────────────────────────────────────────────┘
```

> Command notes (`cmd/litespm/main.go:58-141`): there is **no** `info` or `update` subcommand —
> `update` is an alias of `self-update` (binary update only). Removal is `uninstall` (bridge
> entries in host configs), `host remove <host-id>` (one host), or `skills remove` (skills);
> `litespm setup` / `litespm init` simply open the interactive wizard (they take no agent
> argument — use `litespm host setup <host-id>` for scripted setup).
>
> **Remote archive install resolution is not wired.** `litespm install <listing-id> [--version <ver>]
> [--scope user|project]` resolves the listing from the local catalog index and
> routes by kind: **skills install for real** (fetch → ledger → record), and **MCP servers register
> their published launch descriptor in approved host configs**. Plugins still fail closed with
> `LPSM-ARTIFACT-UNAVAILABLE` because the catalog carries no plugin artifact locator. The earlier
> in-memory synthetic archive is gone; the bridge's `request_install` completes for skills and MCP
> servers, and fails closed for plugins ([STATUS.md](../STATUS.md) §3). `--workspace` is rejected:
> workspace-targeted installation is not implemented.

---

## 2. Interactive Setup Wizard & Agent Auto-Detection

Running `litespm` (or `litespm setup`) without further arguments opens the interactive wizard:

### Step 1: Agent Selection Dropdown
```text
Select your primary AI Agent Host:
  [1] Cline          (VS Code Extension - cline_mcp_settings.json)
  [2] Pi Agent       (Terminal Coding Agent (pi) - mcp.json / config.json + TS Extension)
  [3] Grok Build     (Terminal / IDE (grok) - config.toml (TOML))
  [4] Claude Code    (Terminal CLI (claude) - ~/.claude.json (JSON))
  [5] OpenAI Codex   (Terminal CLI (codex) - config.toml (TOML))
  [6] OpenCode       (Open-source CLI (opencode) - opencode.json)
  [q] Quit
```

> Exactly these 6 bespoke entries exist (`cmd/litespm/wizard.go:25-68`); there is **no**
> "Generic MCP Configuration (Export JSON)" entry. The other 44 generic `BridgeTarget` rows
> (50 targets total) are managed with `litespm host setup <host-id>` / `litespm host list`
> (`internal/host/targets_data.go`; ARCH/30).

### Step 2: Automated Path Discovery & Detection Matrix
LiteSPM probes known default configuration locations by platform:

| Target Agent | Linux / macOS Default Path | Windows Default Path |
|---|---|---|
| **Claude Code** | `~/.claude.json` (project `.mcp.json`; local scope entry inside `~/.claude.json`) | `%USERPROFILE%\.claude.json` (`CLAUDE_CONFIG_DIR` overrides — host-side only, the adapter does not read it) |
| **OpenAI Codex** | `~/.codex/config.toml` (project `.codex/config.toml`) | `%USERPROFILE%\.codex\config.toml` (`%APPDATA%\Codex\config.toml` is a legacy fallback; `$CODEX_HOME` overrides — host-side only, the adapter does not honour it) |
| **Grok Build** | `~/.grok/config.toml` (project `.grok/config.toml`) | `%USERPROFILE%\.grok\config.toml` (`%APPDATA%\Grok\config.toml` is a legacy fallback) |
| **Pi Agent** | `~/.pi/agent/mcp.json` (project `.pi/mcp.json`, trust-gated; legacy `~/.pi/config.json` / `~/.pi/mcp.json`) | `%USERPROFILE%\.pi\agent\mcp.json` (legacy `%USERPROFILE%\.pi\config.json` / `%USERPROFILE%\.pi\mcp.json`) |
| **OpenCode** | `~/.config/opencode/opencode.json` (project `opencode.json` or `.opencode/`) | `%USERPROFILE%\.config\opencode\opencode.json` (`%APPDATA%\OpenCode\opencode.json` is a legacy fallback) |
| **Cline (VS Code)** | Linux: `~/.config/Code/User/globalStorage/saoudrizwan.claude-dev/settings/cline_mcp_settings.json`<br>macOS: `~/Library/Application Support/Code/User/globalStorage/saoudrizwan.claude-dev/settings/…` (Insiders variants also probed) | `%APPDATA%\Code\User\globalStorage\saoudrizwan.claude-dev\settings\cline_mcp_settings.json` (Insiders variant also probed) |

> The standalone **Cline CLI** config (`~/.cline/data/settings/cline_mcp_settings.json`) is not
> probed or managed. Paths above are the candidate lists in `internal/host/*.go`
> (`cline.go:31-46`, `codex.go:35-55`, `grokbuild.go:35-54`, `piagent.go:36-54`,
> `opencode.go:37-58`, `claudecode.go:32`); the first existing candidate wins, and the first
> candidate is returned as the suggested path when none exist.
>
> **Env-var overrides are host-side, not LiteSPM-side.** `CLAUDE_CONFIG_DIR` and `$CODEX_HOME`
> move the config directory *for the host tool itself*. LiteSPM's adapters do not read either
> variable: `ClaudeCodeAdapter.DetectConfig` returns `$HOME/.claude.json` unconditionally
> (`internal/host/claudecode.go:30-33`) and `CodexAdapter.DetectConfig` resolves only
> `$HOME`/`%USERPROFILE%` plus the `%APPDATA%` fallback (`internal/host/codex.go:35-55`) —
> [ARCH/16 §3.4–§3.5](16-HOST-ADAPTERS.md). (`internal/skills` *does* honour `CODEX_HOME`,
> `CLAUDE_CONFIG_DIR`, `GROK_HOME` and `XDG_CONFIG_HOME`, but only when choosing where to write
> skill directories — `README.md` §skills.)

### Step 3: Graceful Fallback Options
If the configuration file is absent (or the user declines the detected path), the wizard prompts
(`cmd/litespm/wizard.go:187-199`):
```text
⚠ Configuration file not found in default locations for Claude Code.

Choose a configuration option:
  [1] Enter configuration path manually
  [2] Print copy-paste snippet
  [3] Retry auto-detection
  [q] Cancel setup
```

### Step 4: Safe Atomic Configuration Injection
1.  **Backup:** Copies the target configuration to `DATA_ROOT/backups/<host-id>_<YYYYMMDD_HHMMSS>_<8-hex>.bak` (flat directory, mode `0600` — `internal/host/backup.go:28-39`).
2.  **Parse & Merge:** Parses JSON/JSONC (comments preserved — `internal/host/jsonc_merge.go`) or merges TOML line-wise outside the managed table (`internal/host/generic.go:mergeTOMLEntry`), then injects a single `litespm` bridge entry for the selected host (shown below for `claude-code`):
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
    Only this one `litespm` entry is registered per host; the `--host <agent-id>` argument tells the daemon which host is calling, and individual capabilities are resolved by the daemon at runtime (no per-capability host snippets are written, and no version field is embedded in the entry). Hosts whose format expects a local-array shape receive `{"type": "local", "command": ["<bin>", "bridge", "stdio", "--host", "<id>"]}` instead (`internal/host/generic.go:300-316`).
3.  **Atomic Replacement:** Writes to a temporary file on the same filesystem, validates syntax, and renames atomically.
4.  **Detection of pre-existing tools:** After setup the wizard lists any pre-existing native tools found in the config as read-only discoveries; **`Adopt` is not implemented** (`DESIGNED` — STATUS §5).

---

## 3. Local Storage Layout (`DATA_ROOT`)

Platform roots (`internal/config/paths.go:42-96`):

| Root | Windows | macOS | Linux |
|---|---|---|---|
| `DATA_ROOT` | `%LOCALAPPDATA%\LiteSPM` | `~/Library/Application Support/LiteSPM` | `$XDG_DATA_HOME/litespm` (default `~/.local/share/litespm`) |
| `CONFIG_ROOT` | `%APPDATA%\LiteSPM` | same base as `DATA_ROOT` | `$XDG_CONFIG_HOME/litespm` (default `~/.config/litespm`) |
| `RUNTIME_ROOT` | **= `DATA_ROOT`** | `~/Library/Caches/LiteSPM/run` | `$XDG_RUNTIME_DIR/litespm`, else `/tmp/litespm-<uid>` |

> **Legacy adoption:** pre-rename `LitePSM`/`litepsm` roots, `LITEPSM_*` environment variables,
> and the legacy `.litepsm` project config are adopted automatically when the new-name root does
> not exist (`internal/config/paths.go:119-174`; `LITESPM_*` overrides win first). Runtime
> endpoints (socket/pipe) always use the current names.

```text
DATA_ROOT/
  ├── state.db                     # SQLite database in WAL mode (22 tables)
  ├── state.db-wal                 # SQLite Write-Ahead Log
  ├── state.db-shm                 # SQLite Shared Memory index
  ├── daemon.lock                  # Exclusive daemon lock; file contents = daemon PID
  │                                #   (there is no daemon.pid file anywhere)
  ├── cas/
  │   └── trees/
  │       └── sha256/<64-hex>/     # Immutable unpacked package trees — the entire CAS
  │                                #   (internal/install/engine.go:471-482; no artifacts/ dir)
  ├── staging/
  │   └── <operation-id>/          # In-flight extraction (source.archive, extracted/)
  ├── backups/
  │   └── <host-id>_<timestamp>_<8-hex>.bak   # Pre-edit host config snapshots (flat)
  └── v1/
      ├── current.json             # Catalog client cache: release pointer
      └── releases/<release-id>/
          ├── manifest.json        # Written by `litespm catalog sync` (DATA_ROOT
          └── listings.json        #   is the cacheDir — cmd/litespm/main.go:719,894)

CONFIG_ROOT/
  └── config.toml                  # User config; project override .litespm/config.toml
                                   #   (legacy .litepsm/config.toml) — internal/config/config.go:97-105

RUNTIME_ROOT/
  └── litespm.sock                 # Unix socket, dir 0700 / file 0600 (Linux, macOS)
                                   # Windows instead uses the named pipe
                                   #   \\.\pipe\litespm-daemon-<12-hex-of-username>
                                   #   (internal/config/paths.go:56-64) — no socket file
```

Directories are created with mode `0700` by `EnsureDirectories` (`internal/config/paths.go:228-244`):
`DataRoot`, `ConfigRoot`, `RuntimeRoot`, `cas/`, `backups/`, `staging/`. There is **no**
`runtimes/` or `cache/` directory. Skill directories installed by `litespm skills add` are copied
into each host agent's own skills tree (outside `DATA_ROOT`) and are removed only by
`litespm skills remove`.

---

## 4. Pure Dependency Resolver Algorithm

LiteSPM's dependency resolver is pure and deterministic: it performs no disk I/O or network fetches
during resolution (`internal/resolver/resolver.go`):
1.  **Input:** target `ListingId` and requested version constraint.
2.  **Recursive DFS traversal:** a visited-set depth-first walk over dependency declarations (`resolver.go:195-227`).
3.  **Cycle Detection:** if a node re-occurs on the traversal stack, halts with `LPSM-RESOLVE-CYCLE`.
4.  **Constraint Intersection (semver):** determines whether requested version ranges intersect.
    *   If compatible versions exist, selects the highest stable immutable release.
    *   If conflicting constraints exist without an intersection, halts immediately with `LPSM-RESOLVE-CONFLICT`.
5.  **Output:** an immutable `InstallPlan` with exact versions, content digests, and resolved dependency order; the install path persists it with a canonical `planHash` (`db.SavePlan`, `cmd/litespm/main.go:1391`) and the engine re-verifies hash and expiry on execution (`internal/install/engine.go:94`).

---

## 5. Update and Upstream Repack Handling

### 5.1 Passive Update Checks (target — partially implemented)
The intended flow: whenever a user runs `litespm` or an agent invokes `/marketplace`, the client
passively reads `/v1/current.json` and compares installed versions against catalog versions,
**never mutating local files** without an explicit update command.

What actually happens today:
*   The setup wizard prints **compiled-in** advisory metadata only — no HTTP request
    (`cmd/litespm/wizard.go:138-143`).
*   In-agent `/marketplace` reads resolve against the **local catalog index**, refreshed only by an
    explicit `litespm catalog sync` (which succeeds against the live origin — STATUS §2).
*   `litespm update` / `self-update` updates the **binary** (`internal/update` SHA-256 fail-closed, plus the release's keyless Sigstore bundle when one is published — `cmd/litespm/main.go:391-438`; no published tag carries a bundle yet), not installed capabilities.

### 5.2 Update Delta Evaluation — `DESIGNED`
There is no `litespm update <listing-id>` command today (any such invocation is parsed as the
`self-update` alias and updates the binary). The specified target: an update plan detailing the
version jump and artifact SHA-256 delta, changes in declared effects, provider launch-argument
changes, and new/removed tools and skills. See [STATUS.md](../STATUS.md) §3 and `ARCH/32`
(manifest/lockfile) for the lockfile-driven path.

### 5.3 Upstream Repack Detection — `DESIGNED`
If an upstream source re-publishes the same version string with a modified artifact digest:

$$\text{Installed Digest } \neq \text{New Upstream Digest for identical version}$$

1.  Flag the artifact as `status: "repacked/mutated-upstream"`.
2.  Block automatic installation.
3.  Warn the user that upstream maintainers modified release artifacts in-place, requiring explicit confirmation to proceed.

No repack detection exists in code; it depends on remote resolve/verify being wired (STATUS §3).

---

## 6. Removal and Garbage Collection

### What exists today
1.  **`litespm uninstall [--dry-run]`** removes the `litespm` bridge entry from every managed host
    config (`cmd/litespm/main.go:493-558`): it strips the entry only when present, backs the file
    up first, reports untouched/failed hosts, and prints what remains on disk (skill directories
    and backups are not deleted). There is no recorded-fingerprint comparison — removal is
    presence-based (`internal/host/remove.go:100-140`).
2.  **`install.remove` (IPC)** deletes the `installs` row; `install_components` cascades via
    `ON DELETE CASCADE` (`internal/state/repositories.go:309`; schema table 12). Removing a
    missing install is an explicit not-found error.
3.  **`litespm skills remove <name>|--all`** removes skills recorded in the skills ledger
    (`cmd/litespm/skills_*.go`), including their copied directories.

### Target algorithm — `DESIGNED` where noted
1.  **Identify Install Record:** query SQLite for the target `InstallId` and resolve all associated components. *(exists)*
2.  **Active Session Check:** query `provider_sessions`; if a managed provider process is running, stop it gracefully (`SIGTERM` / `WM_CLOSE`). **Not implemented** — uninstall paths do not consult the supervisor.
3.  **Database Commit:** in one transaction remove `installs`, `install_components`, `capabilities`, and `capability_grants` records. *(row deletion exists for `installs`/`install_components`; the capability/grant tables are never populated by production code today)*
4.  **Unreferenced CAS Pruning:** scan `cas/trees/sha256/…` and delete trees no active installation references. **Not implemented** — no prune/GC routine exists (STATUS §3).
5.  **Host Config Cleanup with fingerprint:** when removing a host entry, compare it against a recorded fingerprint and leave a user-modified file intact. **Not implemented** — today's removal is presence-based and `host_registrations` writers have test-only callers (STATUS §4); durable ownership belongs to the `ARCH/33` deployment ledger (`DESIGNED`).
