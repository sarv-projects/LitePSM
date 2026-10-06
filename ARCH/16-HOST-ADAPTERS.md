# Host Adapters & In-Agent UX

> **Reality check (read first).** `internal/host` is `WIRED` for exactly one
> operation: merging **a single `litespm` MCP entry** into one host config file,
> atomically and comment/sibling-preserving (`STATUS.md` §1). The `HostAdapter`
> interface has **no compile or projection method**, no instruction / skill /
> command file is written into any host (the one exception is Pi's companion
> extension, §3.2), and no `/marketplace` hook is registered anywhere.
> Anything below that describes richer projection or automated slash-command
> registration is `DESIGNED`.

---

## 1. Extensible HostAdapter Architecture

LiteSPM connects to AI agent hosts through a strongly typed, compiled-in `HostAdapter` interface (`internal/host/types.go:58-66`).

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
    SupportedVersions      []string `json:"supportedVersions"`      // Declared ranges, e.g. ">=2.0.0"
    DefaultConfigFileName  string   `json:"defaultConfigFileName"`  // e.g. cline_mcp_settings.json
    ConfigFormat           string   `json:"configFormat"`           // json | toml
    SupportsFormElicit     bool     `json:"supportsFormElicit"`     // Declared only; nothing consumes it
    RequiresBootstrapSkill bool     `json:"requiresBootstrapSkill"` // Declared only; no skill file is written
    SlashCommandTrigger    string   `json:"slashCommandTrigger"`    // e.g. "/marketplace"; declared only
}
```

**Contract facts:**

*   The seven methods above are the whole interface. There is no `Compile`, `Project`, `WriteInstruction`, or `RegisterCommand` method, so the "write instructions/skills/commands into the host" behaviour is **`DESIGNED`, not implemented** — see §5.
*   `SupportsFormElicit`, `RequiresBootstrapSkill` and `SlashCommandTrigger` are descriptor metadata: each adapter sets them and `Descriptor()` returns them, but no setup, verify, detect, remove or wizard path branches on them. `RequiresBootstrapSkill` is declared `true` for `claude-code` and `codex`, and LiteSPM writes no companion skill for either.
*   `PlanSetup` → `ApplySetup` writes exactly one server entry named `litespm` (the pre-rename `litepsm` entry is detected, deleted and replaced so only one bridge entry survives — `internal/host/target.go:99-105`).
*   Removal is the inverse of merge: `stripBridgeEntry` (`internal/host/remove.go:170-185`) deletes **both** the `litespm` and the legacy `litepsm` entry and touches no sibling; for JSON configs the produced file must re-parse with both entries gone before anything is written (`remove.go:138-149`). `litespm host remove <id>` / `host remove --all` (`cmd/litespm/main.go:424-463`) and `litespm uninstall` all go through this path.
*   `DetectPreExistingComponents` always returns `ReadOnly: true` components of kind `mcp`; no adapter emits `kind: "skill"` today.

---

## 2. Advisory & Metadata Discovery

**Today this is offline.** When a user runs `litespm`, the wizard prints advisory
metadata compiled into the binary — `cmd/litespm/wizard.go:138-143`
(`verifyRuntimeAdvisories`) performs **no HTTP request at all**; it prints the
protocol version and the string `✓ 6 Verified Host Adapters Compiled & Available`
(the six bespoke adapters — `litespm host list` reports **50** adapters, §3.7).

*   **Where advisories actually exist:** the published release pointer
    `GET /v1/current.json` does carry an `advisories` array of
    `{hostId, status, minVersion}` (three entries in `web/public/v1/current.json`).
    There is no separate `adapters.json` endpoint.
*   **Nobody parses it yet.** No Go code reads `advisories`; `catalog.SyncResult`
    (`internal/catalog/client.go:20-26`) decodes only `releaseId`, `sequence`,
    `itemCount` and `updated`. The only network fetch of `/v1/current.json` is
    `catalog sync` (`Client.Sync` → `FetchCurrent`, `internal/catalog/client.go:196-197`
    and `91-101`), which is `SHIPPED`: the pointer and the release tree it names both answer 200
    at the live origin (verified 2026-10-05, `STATUS.md` §2).
*   **Zero local mutation.** Reading advisory metadata (when it is wired) is a
    passive read; it may tell the user a newer client binary is required. Config
    parsing and mutation are performed exclusively by the compiled-in binary, so
    supporting a new host config format requires a client release.
*   **Offline fallback.** With no network, the wizard uses the compiled-in
    adapter registry (`internal/host/registry.go`); this is the path that runs today.

---

## 3. Supported Agent Adapters

### 3.1 Cline (VS Code Extension) (`internal/host/cline.go`)
*   **Host ID:** `cline`
*   **Target Configuration (user scope only — `DetectConfig` ignores `scope`):**
    *   **All platforms:** `~/.cline/data/settings/cline_mcp_settings.json`. This is the file every Cline client reads (extension, CLI, JetBrains), per the extension source: *"The MCP settings file lives at `~/.cline/data/settings/` … shared across VSCode, CLI, and JetBrains clients."*
    *   **Overrides:** `CLINE_MCP_SETTINGS_PATH` (a file) or `CLINE_DATA_DIR` (a directory), both documented. When set, that file is the one created and written.
    *   **Legacy, probed last:** the VS Code `globalStorage` settings file (`%APPDATA%\Code…`, `~/Library/Application Support/Code…`, `~/.config/Code…`, plus `Code - Insiders`). Current Cline reads it only in a one-shot migration, so a bridge written there is invisible to a migrated install; it is probed only so a client too old to have migrated is still found.
*   **Format:** JSON (the host parses it with a strict `JSON.parse`; we read comment-tolerantly, see `ARCH/30` §8.1).
*   **Managed Injection:** Injects under `mcpServers.litespm`.
*   **Corrected 2026-10-06:** this section previously claimed the globalStorage path was current and the CLI's `~/.cline/…` path was "not managed". That was backwards; the audit (`ARCH/30` §8.1) traced the current contract in the vendor's own source.
*   **Detected External Capabilities:** Scans sibling keys under `mcpServers` (skipping `litespm`/`litepsm`) as read-only detected entries.

### 3.2 Pi Agent (`pi-coding-agent`) (`internal/host/piagent.go`)
*   **Host ID:** `pi-agent`
*   **Target Configuration:** `<agent-dir>/mcp.json`, where the agent directory is `~/.pi/agent` unless `PI_CODING_AGENT_DIR` overrides it (documented; also `agentDir` in the SDK). `~/.pi/config.json` and `~/.pi/mcp.json` were previously probed and are in no Pi documentation or source — removed, because probing them made an absent real file resolve to a path Pi never reads. Corrected 2026-10-06 (`ARCH/30` §8.1).
*   **Project scope:** `.pi/mcp.json` is documented for the host, but `DetectConfig` never scans the working directory — setup and detection are user-scope. Note the host *replaces* a user entry with a same-named project entry, so a project file can disable the bridge for that repository.
*   **Format:** JSON (strict `JSON.parse` upstream; comments are tolerated on our side only — see `ARCH/30` §8.1).
*   **Managed Injection:** Writes under `mcpServers` — the only container Pi reads — and creates `<agent-dir>/extensions/litespm.ts` if absent. Earlier revisions switched to `mcp` or `mcp.servers` whenever the file contained an unrelated top-level `mcp` object, which put the bridge where Pi never looks.
*   **Companion extension:** the generated file registers a Pi command named **`litespm`** (not `/marketplace`) whose handler calls the bridge's `list_installed`. **It is never removed by `host remove` / `uninstall`** — removal only strips config entries (`internal/host/remove.go`).
*   **Detected External Capabilities:** Detects external tools from the discovered config file in read-only mode.

### 3.3 Grok Build (`internal/host/grokbuild.go`)
*   **Host ID:** `grok-build`
*   **Target Configuration (user scope):**
    *   **Unix / macOS:** `~/.grok/config.toml`
    *   **Windows:** `%USERPROFILE%\.grok\config.toml` (`%APPDATA%\Grok\config.toml` is a legacy fallback)
    *   Project scope `.grok/config.toml` is host-documented; the adapter does not scan it.
*   **Format:** TOML.
*   **Managed Injection:** Injects under `[mcp_servers.litespm]`.
*   **Detected External Capabilities:** Parses declared external `[mcp_servers.*]` sections in read-only mode.

### 3.4 Claude Code (`internal/host/claudecode.go`)
*   **Host ID:** `claude-code`
*   **Target Configuration:** user scope `~/.claude.json` only (`DetectConfig` returns that path unconditionally). Windows is `%USERPROFILE%\.claude.json`; `CLAUDE_CONFIG_DIR`, project `.mcp.json` and the per-project local entry inside `~/.claude.json` are host-documented locations LiteSPM does **not** read or write today.
*   **Format:** JSON.
*   **Managed Injection:** Injects under `mcpServers.litespm`.
*   **Detected External Capabilities:** Scans `mcpServers` in that file in read-only mode. **`.claude/skills/` is not scanned** — no adapter inspects any skills directory.

### 3.5 OpenAI Codex (`internal/host/codex.go`)
*   **Host ID:** `codex`
*   **Target Configuration (user scope):**
    *   **Unix / macOS:** `~/.codex/config.toml`
    *   **Windows:** `%USERPROFILE%\.codex\config.toml` (`%APPDATA%\Codex\config.toml` is a legacy fallback; `$CODEX_HOME` overrides the directory for the host, but the adapter does not honour it)
    *   Project scope `.codex/config.toml` is host-documented; the adapter does not scan it.
*   **Format:** TOML.
*   **Managed Injection:** Injects under `[mcp_servers.litespm]`.
*   **Detected External Capabilities:** Parses declared external `[mcp_servers.*]` sections in read-only mode.

### 3.6 OpenCode (`internal/host/opencode.go`)
*   **Host ID:** `opencode`
*   **Target Configuration candidates:** `$OPENCODE_CONFIG_DIR/{opencode.jsonc,opencode.json}` when that variable is set, otherwise `$XDG_CONFIG_HOME/opencode/` (default `~/.config/opencode/`) with the same two filenames, `.jsonc` first. OpenCode resolves this directory through xdg-basedir on every OS including Windows. Project scope (`opencode.json` / `opencode.jsonc` / `.opencode/`) is host-documented; the adapter does not scan it. `~/.opencode.json` and `%APPDATA%\OpenCode\opencode.json` are **not** read by any release and are no longer probed.
*   **Format:** JSON or JSONC — both are documented, so comments are tolerated.
*   **Local Entry Shape:** Local MCP entries require `"type": "local"` and a combined string array `"command"`, e.g. `{"mcp":{"litespm":{"type":"local","command":["litespm","bridge","stdio","--host","opencode"]}}}`.
*   **Layout (corrected 2026-10-06):** there is exactly **one** documented layout — servers are direct members of the `mcp` object. The published schema types `mcp` as `additionalProperties` of `McpLocalConfig | McpRemoteConfig | {enabled}` and contains no `servers` key, and the runtime skips any member without `type` (*"Ignoring MCP config entry without type"*). A `mcp.servers.<name>` layout was previously documented here and implemented; it produced a config OpenCode refuses to load while our own verify reported ready. Setup now also prunes an `mcp.servers` object left by an earlier version, but only once it is empty.
*   **Detected External Capabilities:** Scans existing configured servers in read-only mode.

### 3.7 Data-driven targets: 44 generic + 6 bespoke = 50 total (`internal/host/target.go`, `ARCH/30`)
*   The six adapters above are hand-written and retained (they carry behaviour the generic path does not model: Cline's legacy-path fallback and shared-settings overrides, and the Pi extension file).
*   All other agents are data rows (`verifiedBridgeTargets` in `internal/host/targets_data.go`, **44 rows**) served by the single `GenericAdapter` (`internal/host/generic.go`). Bespoke IDs always win name collisions (`TestBridgeTargetTableDoesNotShadowBespokeAdapters`).
*   **Counts:** `litespm host list` prints `Registered Agent Host Adapters (50)`. Skill installation targets are a separate set of **77** rows in `internal/skills/agents.go` (`ARCH/30` §10); the two sets overlap but are not equal, and `host list` reports bridge adapters only.
*   **Scope:** only `GenericAdapter.DetectConfig` honours `domain.ScopeProject`; every other `DetectConfig` ignores the `scope` argument and all production callers pass `ScopeUser`.
*   Surgical merge (`internal/host/jsonc_merge.go` + TOML merger) preserves comments/key order. See ARCH/30 for the honesty contract, exclusion table (23 unverified agents), and integrity tests.

#### 3.7.2 Server-entry install (catalog MCP servers)

`host.InstallServerEntry` registers a third-party MCP server, by name, in the
same container the bridge occupies, so an agent can spawn it. It is the write path
behind `litespm install <mcp-id>`.

*   **The launch line comes from the version record.** A listing carries no
    command; `versions.json` → `components[].runtime` does, and that is why
    `catalog sync` fetches and digest-verifies that file. A listing with no
    runnable component fails closed rather than producing a config the host
    cannot start.
*   **Per-host shape is data, not code.** Container key, format and entry shape
    come from the target table for data-driven hosts and from
    `bespokeEntrySpecs` for the hand-written six: a plain `{command,args}`,
    OpenCode's required `{"type":"local","command":[…]}`, Crush's schema-required
    `type: "stdio"`, a bare command string, or a TOML table.
*   **Same write discipline as the bridge.** Surgical splice, atomic backup, and a
    re-parse plus read-back that refuses to write a config it cannot verify.
*   **Never silently clobber.** An existing entry of the same name is refused
    (`LPSM-NAME-CONFLICT`); `--force` is the deliberate override. Names are
    restricted to `[A-Za-z0-9_-]` and normalized, because several hosts document
    exactly that and a name that worked on one host must not be the reason an
    install fails on another. `litespm` and `litepsm` are reserved.
*   **Targets are consented, not discovered.** With no `--host`, the install goes
    to every host whose LiteSPM bridge verifies as registered — a host the user
    never set LiteSPM up in is never edited on their behalf. With no such host,
    the install fails closed (`LPSM-INSTALL-TARGET-UNAVAILABLE`).
*   **What is recorded.** One `installs` row and one `host_registrations` row per
    modified config, so the entry is not an orphan nothing can find again.

#### 3.7.1 Config-write contract (all hosts, bespoke and generic)

A host config belongs to the user, so a setup or removal must touch exactly one
member. The rules are uniform across all 50 adapters:

*   **Splice, never re-serialize.** JSON and JSONC hosts are edited by byte
    offset (`mergeJSONEntrySurgical`), TOML hosts by a table-level text edit.
    Comments, key order, indentation, unknown keys and the trailing newline
    outside the touched object survive byte for byte. The four bespoke JSON
    adapters (`claudecode.go`, `cline.go`, `opencode.go`, `piagent.go`) share the
    same helper as the generic path (`renderBridgeEntryJSON`), so no host
    re-serializes a user's file.
*   **Comments are tolerated on read and preserved on write.** A host's own file
    may legitimately be JSONC — Cline's lives in VS Code's globalStorage settings
    — so reads go through `parseHostJSON`. Genuinely invalid JSON is still
    refused; a commented config is not "invalid".
*   **Never write what cannot be read back.** The merged text is re-parsed and the
    entry asserted before it is written, and removal refuses rather than writing a
    document it has just proved is broken.
*   **Removal finds the entry wherever it is.** A host with more than one
    documented layout (OpenCode: `mcp.<name>`, `mcp.servers`, `mcpServers`) tries
    each path and rewrites only the one that actually holds an entry, so
    `host remove` cannot silently leave a bridge registration behind.
*   **Round-trip fidelity.** TOML configs and multi-line JSON objects are
    restored byte for byte by `host remove`. A single-line JSON object may retain
    one line break, because inserting an entry into it necessarily added a line;
    content and validity are preserved. Guarantees are pinned by
    `internal/host/json_splice_test.go` and the `host remove` round-trip tests.

---

## 4. In-Agent `/marketplace` Experience & Capability Browser

The in-agent experience is architected as an abstract UX Model mapped to host-specific renderers:

```text
┌────────────────────────────────────────────────────────────────────────┐
│                        LiteSPM Abstract UX Model                       │
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

### 4.1 Portable Contract vs. Rich Host UI — and what actually resolves

*   **Baseline Portable Contract:** Universal MCP does not support arbitrary GUI windows or webviews. For terminal CLI agents (`claude`, `codex`, `grok`, `opencode`), `/marketplace` means the 12 tools the stdio shim advertises in `tools/list` (`internal/bridge/shim.go:93-156`). Their real status today:

| Bridge tool | Daemon method | Status |
|---|---|---|
| `search_catalog` | `catalog.search` | resolves |
| `get_extension` | `catalog.get_item` | resolves |
| `prepare_install` | `resolver.prepare_plan` | resolves (plan only) |
| `request_install` | `install.execute` | **Completes for skills** (installs files through the skills ledger); MCP/plugin fail closed with `LPSM-ARTIFACT-UNAVAILABLE` (no artifact source) (`STATUS.md` §3) |
| `list_installed` | `tools.list` | resolves |
| `load_skill` | `skills.load_body` | resolves |
| `read_skill_resource` | `skills.read_resource` | resolves |
| `search_capabilities` | `capabilities.search` | **JSON-RPC `-32601`**, explicit reason (`cmd/litespm/main.go:1628`) |
| `describe_capability` | `capabilities.describe` | **JSON-RPC `-32601`** (`main.go:1637`) |
| `invoke_capability` | `provider.invoke` | **JSON-RPC `-32601`** — no capability rows, no session dispatch (`main.go:1676`) |
| `get_invocation` | `invocation.get` | **JSON-RPC `-32601`** — no invocation registry (`main.go:1685`) |
| `cancel_invocation` | `invocation.cancel` | **JSON-RPC `-32601`** (`main.go:1694`) |

  A shim with no daemon connection answers every tool with
  `LPSM-IPC-DAEMON-UNREACHABLE` rather than inventing inventory
  (`internal/bridge/shim.go:575-587`).

*   **Rich Host Renderer:** where an agent host supports a webview or TUI extension, that companion renders the interactive 4-tab browser:

```text
┌────────────────────────────────────────────────────────────────────────┐
│                          LiteSPM Capabilities                          │
├──────────────┬──────────────┬──────────────┬───────────────────────────┤
│ [MCP SERVERS]│[AGENT SKILLS]│  [PLUGINS]   │     [INSTALLED (N)] ●     │
└──────────────┴──────────────┴──────────────┴───────────────────────────┘
```

  The tab counts are **not fixed copy**: `FormatInstalledPanel`
  (`internal/bridge/shim.go:514-523`) prints `[INSTALLED (%d)]` from
  `len(items)` of the current `tools.list` response, and prints
  `No capabilities currently installed…` when that list is empty. Any sample in
  this document showing a number is illustrative of the layout only.

### Tab 1: MCP Servers
*   Search bar for filtering MCP servers by keyword or category (`database`, `developer-tools`, `browser`).
*   Lists server cards with publisher, verified status, and transport (`stdio` / `Streamable HTTP`).
*   One-click "Install" action triggering the `prepare_install` flow (plan preview). Execution completes for skills and fails closed for MCP/plugin (see the table above).

### Tab 2: Agent Skills
*   Browse portable `SKILL.md` skills from `agentskills.io` and public Git sources.
*   Shows declared description, triggers, and progressive disclosure preview.

### Tab 3: Plugins
*   Curated plugins and bundles combining MCP servers, skills, and tools.
*   Reports per-host compatibility badges (declared per target, not test evidence — `ARCH/26` §4.2).

### Tab 4: Installed & Detected External Capabilities
The final tab provides situational visibility across the host environment:

*   **Status lights are truthful, not decorative.** The renderer offers `● Ready`, `🟡 Needs Auth`, `○ Stopped` and `— Unknown` (`internal/bridge/shim.go:530-549`). The daemon's `tools.list` deliberately omits `Status` because it does not health-check installs (`cmd/litespm/main.go:1238-1243`), so **anything LiteSPM has not observed renders as `— Unknown`**; `Verified` is likewise false unless the data carries it. There is no simulated "active/connected" state.
*   **Detected External Capabilities (Read-Only):**
    *   `tools.list` unions ledger installs with components each adapter reports from its own documented config file (`main.go:1263-1276`).
    *   **Scope limitation:** detection is limited to documented on-disk config files known to the adapter; in-memory sessions, cloud-managed extensions, skills directories and undocumented registries are not scanned.
    *   **Read-only observation:** external tools are never toggled or edited by the status view.
    *   **Adopt is `DESIGNED`, not implemented.** The panel renders the note `[External / Detected] (Adopt)` (`shim.go:551-556`) and the setup wizard prints `(use 'Adopt' in /marketplace to manage)` (`cmd/litespm/wizard.go:271`), but **no adopt handler, bridge tool or `InstallRecord` creation exists** — adoption needs the deployment ledger (`STATUS.md` §5). The label is a placeholder and must not be presented as a working action.

```text
FormatInstalledPanel output (format sample — the count is len(items), and
every Status below is what the daemon actually reported, not a decoration):

┌────────────────────────────────────────────────────────────────────────┐
│                          LiteSPM Capabilities                          │
├──────────────┬──────────────┬──────────────┬───────────────────────────┤
│ [MCP SERVERS]│[AGENT SKILLS]│  [PLUGINS]   │     [INSTALLED (2)] ●     │
└──────────────┴──────────────┴──────────────┴───────────────────────────┘

Status: ● Ready | 🟡 Needs Auth | ○ Stopped | — Unknown | [External / Detected] Read-Only

| Status  | Kind | Name / ID       | Notes / Action                   |
|---------|------|-----------------|----------------------------------|
| — Unknown | MCP | **postgres-prod** | Managed                        |
| — Unknown | MCP | **github-native** | [External / Detected] (Adopt)  |
```

Rows appear only for what `tools.list` returns: install-ledger entries (kind
parsed from the listing ID) plus adapter-detected external components, all of
which are `mcp` today (§1). Skills managed by `litespm skills add` are listed by
`skills list`, not by this panel.

---

## 5. Automated Slash Command (`/marketplace`) Registration — `DESIGNED`

**What setup actually writes:** one `litespm` MCP entry in the host config
(§1), plus — for Pi only — `~/.pi/agent/extensions/litespm.ts` (§3.2). The
wizard ends by **printing** next-step instructions
(`cmd/litespm/wizard.go:280-309`), e.g. "Type `/marketplace` in the prompt".
It writes no instruction file, no skill and no command hook, and it does not
register `/marketplace` in any host.

The following registrations are the intended design and do not exist yet:

1.  **Cline:** register a custom prompt/workflow or workspace command triggering the LiteSPM MCP bridge.
2.  **Pi Agent:** the extension exists (§3.2) but registers `/marketplace` rather than the current `litespm` command — `DESIGNED`.
3.  **Claude Code & Codex:** install a companion bootstrap skill `litespm.skill.md` with trigger keyword `/marketplace`. `RequiresBootstrapSkill` is already declared `true` for both, and nothing consumes it.
4.  **Grok Build:** register a companion command hook in `.grok/config.toml` (project scope).

Projection of instruction/skill/command components is listed as the "to reach
the next state" step for host adapters in `STATUS.md` §1; until then, treat any
doc, wizard string or UI hint promising `/marketplace` registration as a
description of the target design, not of shipped behaviour.
