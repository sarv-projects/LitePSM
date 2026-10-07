# LiteSPM Market & Client: Technical Architecture & System Specification

**The Lightweight Skill & Package Manager for AI Agents.**

LiteSPM is an open-source, provider-neutral package manager, federated catalog, and local control plane for AI agent capabilities: **Plugins, Skills, and MCP (Model Context Protocol) Servers**. 

It eliminates the need to manually configure, update, and manage capabilities across fragmented AI developer tools (for Claude Code, Codex, OpenCode, and many more). By registering a lightweight, version-pinned LiteSPM Bridge once per agent, users can discover, install, update, and supervise capabilities centrally from a single local control plane.

> **Foundational Security Invariant:** Credentials and execution remain strictly on the user's workstation or directly with the selected upstream provider. LiteSPM's hosted public catalog does not store credentials, execute plugin scripts, or proxy tool calls. The client-side control plane operates entirely within the local user's operating system privileges and enforces local, fail-closed authorization policies.

---

## 1. Project Status & In-Progress Roadmap

> **How to read this table.** Status is an *evidence state*, not a feeling. These six states are
> distinct and are defined in [ARCH/31 §2](ARCH/31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#2-evidence-vocabulary-binding-repo-wide):
> `DESIGNED` → `IMPLEMENTED` → `WIRED` → `TESTED` → `VERIFIED` → `SHIPPED`.
> "Files exist" is at most `IMPLEMENTED`; "tests exist" is at most `TESTED`. A component whose only
> caller is a test is `IMPLEMENTED`, not `WIRED`.
> The authoritative defect tracker is [REMEDIATION-PLAN.md](REMEDIATION-PLAN.md); the ordered
> forward backlog is [ARCH/31 §12](ARCH/31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#12-ordered-backlog).
>
> **[STATUS.md](STATUS.md) is the single source of truth for per-subsystem state.** Where this
> table and STATUS.md disagree, STATUS.md wins.

| Phase | Description | State | Evidence / what is missing |
|---|---|---|---|
| **Phase A** | **Architecture Freeze & LLD Specifications** | `DESIGNED` | `ARCH/00`–`ARCH/37` exist. `ARCH/24` is an aspirational inventory, not a compiled one; `ARCH/18` specifies an output tree (`index.json`, `shards/`, `items/`) that the compiler does not emit (those outputs are labelled `DESIGNED`) |
| **Phase B** | **Foundations, Storage & Local IPC** | `TESTED` | `internal/domain`, `internal/config`, `internal/state` (SQLite WAL, 22 tables), `internal/ipc` (named pipes / Unix sockets). Contract tests bind; see [STATUS.md](STATUS.md) §1 |
| **Phase C** | **Static Catalog & Discovery Plane** | `SHIPPED` | `internal/catalogbuild` + `litespm catalog build` are `TESTED` (real-dataset + end-to-end build→serve→sync→search tests); the release tree **is published** and `litespm catalog sync` was verified against the live origin from a clean data root (2026-10-05: release `rel-2026-10-05-01`, 5,814 indexed). `ARCH/18` §1–§2's `index.json`/`shards`/`items` remain un-emitted (shards are `DESIGNED`) |
| **Phase D** | **Safe Extraction & Skill Store** | `WIRED` (resolver, skills); `IMPLEMENTED` (install engine); `TESTED` (artifact) | `internal/resolver` and `internal/skills` are reached from production paths and `internal/artifact` is test-covered ([STATUS.md](STATUS.md) §1/§3); skills and MCP servers install end to end (verified against the live catalog). Only **plugins** still need an artifact source — [ARCH/31 §4.1](ARCH/31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#41-the-agent-facing-install-path-cannot-complete--wired-is-false) |
| **Phase E** | **Process Supervision, Bridge & Host Adapters** | `WIRED` for hosts; `IMPLEMENTED` for provider runtime | 6 bespoke adapters + 44 generic BridgeTargets (50 total, `ARCH/30`) + 77 skill targets are registered. Provider autostart is inert: the `providers` table is never populated by non-test code (finding `m4`). See [STATUS.md](STATUS.md) §1/§4 |
| **Phase F** | **MCP Protocol Dual-Profile, Secrets & OAuth** | `WIRED` (secrets, mcpclient) / `IMPLEMENTED` (auth) | Dual-profile client, native OS keystores, OAuth PKCE loopback exist; the secrets vault is opened before serving (`WIRED`), but secrets are not injected at provider launch, `mcpclient`'s first production importer is `internal/discover`, and `auth` still has zero production importers — the `AuthBroker` currently has no consumer (finding 99), [STATUS.md](STATUS.md) §1/§4 |
| **Phase G** | **In-Agent `/marketplace` Panel & Web UI** | `WIRED` for the static web marketplace; panel `WIRED` | The Next.js static export and Bridge tool surface exist; the panel's install action completes for skills and MCP servers. Plugins remain blocked by Phase D. See [STATUS.md](STATUS.md) §1 |
| **Phase H** | **Release Engineering & Packaging** | `IMPLEMENTED` | Cross-platform builds, npm wrapper, CI checks, and keyless cosign signing of every release artifact plus a CycloneDX SBOM (`release.yml`). `self-update` completes only once a release publishes `litespm-*` assets (packaging P7) |
| **Phase I** | **Golden Fixtures, Self-Update & Migrations** | `TESTED` for fixtures and migrations; self-update `WIRED` (signed) | Fixtures corpus and migration engine (forward apply + downgrade guard) are tested; `self-update` is fail-closed on SHA-256 with rollback and verifies the release's keyless signature bundle (required via `LITESPM_REQUIRE_SIGNED_UPDATE=1`). The first signed release is cut on the next `v*` tag — see [SECURITY.md](SECURITY.md). Per-row detail: [TODO.md](TODO.md) Phase I |

**Not yet true of LiteSPM, and not claimed anywhere:** provider autostart, a project manifest +
lockfile, npm package signatures, plugin installation, and any isolation level beyond process
supervision. (Release **signing** is implemented — every artifact gets a keyless cosign bundle the
updater verifies, and the catalog pointer is signed and checked on sync — but no signed release has
shipped yet; runtime policy **is** now enforced at invocation: `provider.invoke` runs the policy
engine and ungranted tools fail closed until an interactive `litespm grant`; the catalog release
tree **is** published — the live origin serves it and `catalog sync` succeeds against it; and
`request_install` **does** complete an agent-driven install for skill and MCP-server listings.)

---

## 2. System Architecture: "What Is What"

LiteSPM bifurcates system responsibilities between an untrusted public discovery layer and a privileged local control plane:

```text
                                 PUBLIC DISCOVERY PLANE
    ┌─────────────────────────────────────────────────────────────────────────┐
    │ Upstream Sources: MCP Registry · Agent Skills · Git Marketplaces        │
    └────────────────────────────────────┬────────────────────────────────────┘
                                         │ CI Source Ingestion & Validation
                                         ▼
    ┌─────────────────────────────────────────────────────────────────────────┐
    │ Cloudflare Pages (Static Distribution CDN)                              │
    │  ├── /v1/current.json (Lightweight atomic pointer to latest release)    │
    │  └── /v1/releases/<release-id>/ (Immutable manifests, listings,         │
    │      versions — immutable, byte-reproducible; shards are DESIGNED)      │
    └────────────────────────────────────┬────────────────────────────────────┘
                                         │ HTTPS (read-only)
═════════════════════════════════════════╪══════════════════════════════════════════
                                 LOCAL CONTROL PLANE
                      (Isolated to User Workstation & OS User)

    ┌─────────────┐   ┌─────────────┐   ┌─────────────┐   ┌─────────────────┐
    │    Cline    │   │  Pi Agent   │   │ Grok Build  │   │  CLI / TUI App  │
    └──────┬──────┘   └──────┬──────┘   └──────┬──────┘   └────────┬────────┘
           │ stdio           │ stdio           │ stdio             │
           ▼                 ▼                 ▼                   │
    ┌─────────────┐   ┌─────────────┐   ┌─────────────┐            │
    │ Bridge Shim │   │ Bridge Shim │   │ Bridge Shim │            │
    └──────┬──────┘   └──────┬──────┘   └──────┬──────┘            │
           │                 │                 │                   │
           └─────────────────┼─────────────────┴───────────────────┘
                             │ Local IPC (OS-level ACLs only:
                             │ Windows: Named Pipe DACL / Unix: Domain Socket 0600)
                             ▼
    ┌─────────────────────────────────────────────────────────────────────────┐
    │                           LiteSPM Daemon                                │
    │ ┌─────────────────────────────────────────────────────────────────────┐ │
    │ │ IPC Session Manager & Dispatcher                                    │ │
    │ └──────────────┬──────────────────┬───────────────────┬───────────────┘ │
    │                ▼                  ▼                   ▼                 │
    │ ┌─────────────────────────┐ ┌───────────────┐ ┌───────────────────────┐ │
    │ │ SQLite State (WAL mode) │ │ Policy Engine │ │ OS Secret Broker      │ │
    │ │ (22 Relational Tables)  │ │ & Approvals   │ │ (DPAPI/Keychain/DBus) │ │
    │ └─────────────────────────┘ └───────────────┘ └───────────────────────┘ │
    │                │                  │                   │                 │
    │                ▼                  ▼                   ▼                 │
    │ ┌─────────────────────────┐ ┌─────────────────────────────────────────┐ │
    │ │ Content-Addressed Store │ │ Provider Supervisor                     │ │
    │ │ (trees/; no artifacts/) │ │ (Windows Job Objects / Process Groups)  │ │
    │ └─────────────────────────┘ └────────────────────┬────────────────────┘ │
    └──────────────────────────────────────────────────┼──────────────────────┘
                                                       │
                                  ┌────────────────────┴────────────────────┐
                                  ▼                                         ▼
                      ┌───────────────────────┐                 ┌───────────────────────┐
                      │ Local stdio Provider  │                 │ Remote Streamable     │
                      │ (Node / Python / Bin) │                 │ HTTP Provider         │
                      └───────────────────────┘                 └───────────────────────┘
```

### 2.1 The Bridge Shim (Stateless Stdio Facade)
*   **What it is:** A thin binary invoked directly by host agents as an MCP server (`litespm bridge stdio --host <agent-id>`).
*   **Responsibilities:** Speaks standard MCP JSON-RPC over `stdin`/`stdout`, packages agent requests, and forwards them across local IPC to the Daemon. It terminates cleanly when the agent terminates stdio.
*   **What it does NOT do:** It never touches SQLite directly, never modifies configuration files, and never spawns downstream provider processes.

### 2.2 The LiteSPM Daemon (Single-Writer Control Plane)
*   **What it is:** A background service running per OS user account.
*   **Responsibilities:**
    *   **Sole SQLite Writer:** Exclusively holds SQLite write locks in WAL mode, serializing all mutations (installs, updates, approvals, config edits).
    *   **Process Supervisor:** Manages child provider processes. On Windows, child processes are attached to Windows Job Objects configured with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`. On Linux/macOS, processes run in dedicated process groups controlled by an internal supervisor watchdog holding a control pipe (`PR_SET_PDEATHSIG` on Linux). **Orphan reaping is not guaranteed on every platform:** on macOS/BSD the watchdog is an in-process goroutine, so a daemon that is SIGKILLed or crashes dies with it and leaves the child running — a provider can be orphaned there when the daemon dies abnormally (`internal/provider/pdeathsig_other.go`), and a provider that double-forks escapes its process group entirely. Graceful shutdown (`Terminate`/`StopAll`) is the supported path.
    *   **Policy Authority:** Evaluates tool invocation permissions and verifies cryptographic `planHash` values against human approvals.
    *   **Secret Broker:** Interfaces with native OS vaults (`secret-tool`, `/usr/bin/security`, DPAPI — [ARCH/19 §2](ARCH/19-SECRETS-OAUTH.md#2-platform-specific-secret-vault-backends)). Splicing a stored secret into a provider's environment at launch is `IMPLEMENTED`, **not `WIRED`**: `ResolveLaunchSecrets` has no production caller ([STATUS.md](STATUS.md) §1).

### 2.3 Storage Layer (`DATA_ROOT`)
*   **`state.db` (SQLite 3 in WAL mode):** Maintains 22 relational tables covering installations, active provider sessions, capability definitions, permission grants, host backups, persistent `auth_profiles`, `operation_trees`, and audit events.
*   **Content-Addressed Store (`DATA_ROOT/cas/trees/`):** Every extracted package tree is stored at `DATA_ROOT/cas/trees/sha256/<64-hex-digest>` (`internal/install/engine.go:471-481`); it is a flat digest directory, not a two-level fan-out. There is **no `artifacts/` directory** — downloads spool to a temp file and only trees are retained (`ARCH/04` §4). This enables tamper-evident rollbacks and deduplication.
*   **Operation Journal (14 States):** Every filesystem mutation is journaled (`created` $\rightarrow$ `staging` $\rightarrow$ `commit_intent` $\rightarrow$ `committed`). Interrupted operations cleanly recover on daemon startup, deleting only trees created by that operation.

---

## 3. End-to-End Workflows

### 3.1 Global Installation & Interactive Setup (`litespm`)
1.  **Installation:** Installed globally via npm (`npm install -g litespm`) or downloaded as a standalone native Go binary.
2.  **Interactive TUI Wizard:** Running `litespm` launches an interactive terminal interface:
    *   **Compiled-In Adapters:** The wizard prints advisory metadata from strings compiled into the
        binary (`cmd/litespm/wizard.go:138-143`). It makes **no HTTP request** at setup time; the
        `catalog sync` path is the only network fetch, and it succeeds against the live origin
        ([STATUS.md](STATUS.md) §2).
    *   **Agent Selector Dropdown:** Select your agent (Cline, Pi Agent, Grok Build, Claude Code, Codex, OpenCode).
    *   **Automated Config Discovery:** LiteSPM scans platform-standard paths across Windows, macOS, and Linux.
    *   **Graceful Fallback:** If the file is not found, prompts the user to:
        *   *Enter path manually*
        *   *Print copy-paste snippet*
        *   *Retry detection*
    *   **Safe Atomic Merge:** LiteSPM creates a timestamped backup in `DATA_ROOT/backups/`, preserves
        all existing comments/keys, and injects the single version-pinned Bridge entry. This is an
        **MCP-entry merge only** — no instruction, skill, or command file is projected into the host.

### 3.2 Skill Installer (`litespm skills add`)

Portable `SKILL.md` skills are installed per agent host, in the host's own skill
directory, at a chosen scope.

```bash
litespm skills add anthropics/skills                  # interactive
litespm skills add anthropics/skills --list           # what is in the repo
litespm skills add anthropics/skills --skill frontend-design --agent claude-code --agent codex --yes
litespm skills add ./my-skills --scope global -y --json
```

| Step | Behaviour |
|---|---|
| Source | `owner/repo`, an `https` Git URL, or a local directory. Remote sources are fetched with a shallow `git clone` into a temp dir. Non-`https` schemes and path traversal in skill names are refused. |
| Discovery | Walks the source tree up to depth 2 for directories containing `SKILL.md`/`skill.md` with both `name` and `description`. `.git` is never descended. A skill directory is never treated as a parent of another skill. |
| Scope | `This repo only` (default) writes project-relative dirs; `Global (user-wide)` writes user dirs and honours `CODEX_HOME`, `CLAUDE_CONFIG_DIR`, `GROK_HOME`, `XDG_CONFIG_HOME`. |
| Agents | Each selected skill is copied into every selected agent's own directory. Hosts without a dedicated dir (Cline, Pi Agent) share the universal `.agents/skills` tree. No universal copy is forced. |
| Copy | Symlinks are refused, hidden VCS dirs are skipped, existing destinations are never overwritten, and each skill is bounded at 32 MiB. |
| Risk panel | Always rendered. Without a real audit source it reports `unverified` per skill — a verdict is never invented. |

Host skill directories are transcribed from `vercel-labs/skills` `src/agents.ts`
(MIT) — the ecosystem's de-facto map of where each agent reads `SKILL.md` from.
LiteSPM tracks **77 agents**. Selected examples:

| Agent | This repo only | Global |
|---|---|---|
| `claude-code` | `<repo>/.claude/skills` | `$CLAUDE_CONFIG_DIR/skills` (default `~/.claude/skills`) |
| `codex` | `<repo>/.agents/skills` | `$CODEX_HOME/skills` (default `~/.codex/skills`) |
| `opencode` | `<repo>/.agents/skills` | `$XDG_CONFIG_HOME/opencode/skills` (default `~/.config/opencode/skills`) |
| `grok-build` | `<repo>/.grok/skills` | `$GROK_HOME/skills` (default `~/.grok/skills`) |
| `windsurf` | `<repo>/.windsurf/skills` | `~/.codeium/windsurf/skills` |
| `gemini-cli`, `github-copilot`, `cursor`, `amp`, `kilo`, `zed`, `droid`, … | `<repo>/.agents/skills` | per-agent |
| `cline`, `pi-agent`, `kimi-code-cli` | `<repo>/.agents/skills` | `~/.agents/skills` |

Agents sharing one directory (the universal `.agents/skills` group) collapse to a
single write, so installing for several of them never collides. Agents detected
on the machine are listed first; **popularity is not used to order them**,
because no measured usage data exists — presence then alphabetical.

Non-interactive runs require `--agent` and `--yes`; they never prompt, never
imply a consent that was not given, and emit `--json` output on stdout.

### 3.3 Non-Destructive Update Checking (`DESIGNED`)

There is **no passive update check today**. Running `litespm` or `/marketplace` does not fetch
`/v1/current.json`, compare versions, or render a delta diff. Update checking is an explicit,
user-initiated `litespm self-update`, which fetches the published release manifest, verifies the
binary's SHA-256 checksum, and applies it only on request. Version-diff rendering for installed
capabilities is designed and not implemented — see [STATUS.md](STATUS.md).

### 3.4 The In-Agent `/marketplace` Experience
Typing `/marketplace` in a configured agent invokes the Bridge's discovery tools. The portable
contract is the 12 Bridge tools (`search_catalog`, `get_extension`, `prepare_install`,
`request_install`, `list_installed`, `search_capabilities`, `describe_capability`, `load_skill`,
`read_skill_resource`, `invoke_capability`, `get_invocation`, `cancel_invocation`); rich hosts
render the installed view as a table. Tool-by-tool state is in [STATUS.md](STATUS.md) §4.

*   **Discovery (`search_catalog`, `get_extension`):** resolve against the daemon over real catalog
    data (`catalog.search` / `catalog.get_item`).
*   **Plan (`prepare_install`):** resolves a real plan (`resolver.prepare_plan`).
*   **Install (`request_install`):** reaches `install.execute`, which is gated by
    `install_authz.go`: the request must name a persisted, hash-verified plan **and** carry an
    approval a human recorded for that exact plan hash (`litespm approve <plan-id>`). With both
    present, **skill listings install their real files and MCP-server listings register the server
    in each target host's config**; plugin listings alone still fail closed with
    `LPSM-ARTIFACT-UNAVAILABLE` because the catalog carries no artifact locator for them —
    [STATUS.md](STATUS.md) §3.
*   **Skills (`load_skill`, `read_skill_resource`):** resolve against real skill data.
*   **Capabilities & invocation (`search_capabilities`, `describe_capability`,
    `invoke_capability`):** resolve over discovered capability rows through `internal/discover`
    (probe → `capabilities` row → invoke, refusing on schema drift). Only
    **`get_invocation` and `cancel_invocation`** still return an explicit `-32601`
    "not implemented", because the asynchronous invocation registry they need is ARCH/34
    (`DESIGNED`).

**Installed view (`list_installed`).** The daemon returns ledger-backed installs plus
**read-only** components detected in the host's own config file. LiteSPM does **not** health-check
installed capabilities, so no ready/needs-auth/stopped light is asserted: unobserved status renders
as `— Unknown`. External components are detected; there is **no `Adopt` action** — the label is
rendered by the panel, but no adopt handler or tool exists (it is `DESIGNED`).

### 3.5 Capability Schema-Drift & Identity Protection (`WIRED` for the drift gate, `IMPLEMENTED` for grants)
*   Upon tool discovery, LiteSPM generates a SHA-256 fingerprint of the tool's input JSON Schema (`schemaFingerprint`).
*   User approvals are designed to bind cryptographically to strong identity tuples:
    *   **Local Stdio:** `(capability_id, schemaFingerprint, casTreeDigest)`
    *   **Remote HTTP:** `(capability_id, schemaFingerprint, endpointOrigin, serverVersionDigest)`
*   If an upstream provider modifies its schema upon reconnection, or if local code or remote endpoints change, LiteSPM invalidates the grant (`status: "changed"`), blocking unapproved execution until re-reviewed.

**Honest limit:** the *check* half is real — `internal/discover` re-probes the server before every
call and refuses one whose fingerprint no longer matches what was discovered, and the policy engine
is evaluated before the server is spawned (`WIRED`). The *grant* half is not: `SaveCapabilityGrant`
has no non-test caller, so no `CapabilityGrant` row is ever written and therefore none can be
invalidated — `status: "changed"` is a state nothing yet produces. The `capabilities` and
`providers` writers **do** have a production caller now (`internal/discover`). See
[STATUS.md](STATUS.md) §4.

---

## 4. Supported Agent Ecosystem

Where LiteSPM actually reads and writes — verified against the adapters, not against marketing
pages. Every bespoke `DetectConfig` is **user-scope only**: it ignores the `scope` argument, so the
host-documented project paths below are *not* scanned (`ARCH/16` §3).

| Agent Host | Interface / Environment | Configuration Format | LiteSPM target (and what else exists) |
|---|---|---|---|
| **Cline** | VS Code Extension (and the Cline CLI / JetBrains clients) | JSON (`cline_mcp_settings.json`) | **Current default:** `~/.cline/data/settings/cline_mcp_settings.json` (created when nothing exists yet), relocated by `CLINE_MCP_SETTINGS_PATH` or `CLINE_DATA_DIR`. **Legacy, probed last:** the VS Code `globalStorage` path (`%APPDATA%\Code\User\globalStorage\saoudrizwan.claude-dev\settings\`, `~/Library/Application Support/Code/User/globalStorage/…`, `~/.config/Code/User/globalStorage/…`, plus `Code - Insiders`) — current Cline reads it only in a one-shot migration, so it is a read-only fallback for an un-migrated client (`internal/host/cline.go`) |
| **Pi Agent** | Terminal Coding Agent (`pi`) | JSON (`mcp.json`) + TS Extension | `~/.pi/agent/mcp.json`, relocated by `PI_CODING_AGENT_DIR`; the bridge is written under the `mcpServers` key — the only container Pi reads. `.pi/mcp.json` is host-documented but the adapter never scans it, and the former `~/.pi/config.json` / `~/.pi/mcp.json` fallbacks were **removed**: no Pi release reads them (`internal/host/piagent.go`) |
| **Grok Build** | Terminal / IDE (`grok`) [id: `grok-build`] | TOML (`config.toml`) | `$GROK_HOME/config.toml` (default `~/.grok/config.toml`; Windows `%USERPROFILE%\.grok\config.toml`). The `%APPDATA%\Grok\config.toml` fallback was **removed** — it appears in no xAI documentation (`internal/host/grokbuild.go`). Project `.grok/config.toml` is host-documented; the adapter does not scan it |
| **Claude Code** | Terminal CLI (`claude`) | JSON (`~/.claude.json`) | `~/.claude.json` unconditionally (`%USERPROFILE%\.claude.json` on Windows). `CLAUDE_CONFIG_DIR`, the project `.mcp.json`, and the per-project local entry inside `~/.claude.json` are host-documented locations LiteSPM does **not** read or write (`ARCH/16` §3.4) |
| **OpenAI Codex** | Terminal CLI (`codex`) | TOML (`config.toml`) | `$CODEX_HOME/config.toml` (default `~/.codex/config.toml`; Windows `%USERPROFILE%\.codex\config.toml`). The adapter **does** honour `$CODEX_HOME`. The `%APPDATA%\Codex\config.toml` fallback was **removed** — it appears in no OpenAI documentation (`internal/host/codex.go`). Project `.codex/config.toml` is host-documented; the adapter does not scan it |
| **OpenCode** | Open-source CLI (`opencode`) | JSON (`opencode.jsonc` preferred, else `opencode.json`) | `$OPENCODE_CONFIG_DIR/{opencode.jsonc,opencode.json}` when that variable is set, otherwise `$XDG_CONFIG_HOME/opencode/` (default `~/.config/opencode/`), `.jsonc` first — on every OS, because OpenCode resolves the directory through xdg-basedir. Project scope and `%APPDATA%\OpenCode\opencode.json` are **not** read by any release and are no longer probed |

LiteSPM registers exactly one `litespm` bridge entry per host (`litespm bridge stdio --host <agent-id>`); individual capabilities are resolved by the daemon at runtime. OpenCode has **one** documented layout — servers are direct members of the `mcp` object — so the entry is `{"mcp": {"litespm": {"type": "local", "command": ["litespm", "bridge", "stdio", "--host", "opencode"]}}}` (a combined string-array `command`); the nested `mcp.servers` shape earlier revisions wrote is pruned when empty because OpenCode rejects a member that is not a server definition.

---

## 5. Repository Structure & Architectural Guide

```text
/
  ├── README.md                                         # This master technical specification
  ├── STATUS.md                                         # SINGLE STATUS AUTHORITY (six evidence states)
  ├── CHANGELOG.md                                      # Change record
  ├── REMEDIATION-PLAN.md                               # Authoritative defect tracker + verification protocol
  ├── TODO.md                                           # Master delivery ledger (Phases A through I)
  ├── AGENTS.md                                         # Supported AI agents guide & /marketplace UX
  ├── TEST.md                                           # Test scenarios for Cline, Pi Agent, Grok Build
  ├── SECURITY.md                                       # Threat model, controls, open security gaps
  │
  ├── cmd/
  │   └── litespm/                                      # Root CLI entrypoint
  │       ├── main.go                                   # Command dispatch + daemon/IPC handlers
  │       ├── wizard.go                                 # Interactive agent setup wizard (compiled-in advisories)
  │       ├── skills_add.go                             # `skills add` installer (clone, select, copy)
  │       ├── skills_update.go                          # `skills update` (atomic swap, dry-run, rollback)
  │       ├── skills_remove.go                          # `skills remove/list` ledger-backed removal
  │       └── skills_policy.go                          # skills policy hook (deny rules → installed skills)
  │
  ├── internal/ (38 packages; the 21 listed below are the `ARCH/24` inventory)
  │   ├── domain/                                       # Pure domain models, canonical IDs, RFC 8785 JCS, errors
  │   ├── config/                                       # Platform paths (%LOCALAPPDATA%, XDG, runtimes) & config
  │   ├── state/                                        # SQLite WAL engine (22 tables), safe CAS rollback journal
  │   ├── ipc/                                          # Local IPC gated by OS-level ACLs (Named Pipe DACL / Unix socket 0600)
  │   ├── source/                                       # Upstream adapters (MCP Registry, Skills, Claude/Codex/Cursor/Grok, ACP)
  │   ├── catalogbuild/                                 # Deterministic release compiler & manifest generator
  │   ├── catalog/                                      # Catalog client, HTTP sync, digest verification & lexical search
  │   ├── artifact/                                     # Safe archive extraction (256 MiB/1 GiB/case-fold limits)
  │   ├── resolver/                                     # Pure DFS resolver + semver constraints
  │   ├── install/                                      # Atomic CAS staging + SQLite commit
  │   ├── skills/                                       # Skill loader, 77-agent installer, ledger
  │   ├── agent/                                        # ACP registry + launch-spec resolution + overrides
  │   ├── bridge/                                       # Stdio Bridge shim (12 tools)
  │   ├── provider/                                     # Supervisor (Job Objects/watchdog) + isolation
  │   ├── policy/                                       # 17-action effect taxonomy + 5-tier engine
  │   ├── host/                                         # 6 bespoke adapters + 44 generic BridgeTargets (50 total)
  │   ├── mcpclient/                                    # Dual-profile MCP client (2026-07-28 + legacy)
  │   ├── secrets/                                      # OS vault + memory/file stores
  │   ├── auth/                                         # OAuth PKCE loopback broker
  │   ├── doctor/                                       # 10-check diagnostics + repair plans
  │   └── update/                                       # Self-update with fail-closed checksum verification
  │
  ├── schemas/                                          # Draft 2020-12 Canonical JSON Schemas
  │   ├── install-plan.schema.json                      # Cryptographic plan schema with planHash
  │   ├── listing.schema.json                           # Normalized catalog listing schema
  │   ├── version.schema.json                           # Pinned version & artifact reference schema
  │   ├── source.schema.json                            # Upstream source & snapshot configuration schema
  │   ├── catalog-release.schema.json                   # Immutable release pointer & manifest schema
  │   └── errors.schema.json                            # Standardized LPSM-* machine error schema
  │
  ├── scripts/                                          # Release/deploy/catalog helpers — see scripts/README.md
  ├── web/                                              # Next.js static marketplace — see web/README.md
  ├── LICENSE                                           # Apache License 2.0
  ├── NOTICE                                            # Apache-2.0 attribution + third-party notices
  └── ARCH/                                             # 38 Architecture & LLD Documents (00–37)
      ├── 00-INDEX.md                                   # Normative status, precedence & registry
      ├── 01-PRODUCT.md                                 # Product contract & 5-stage lifecycle
      ├── 02-HLD.md                                     # Daemon/Bridge split & sequence diagrams
      ├── 03-CATALOG-SOURCES.md                         # Federation, SourceSnapshot & SSRF
      ├── 04-CLIENT-INSTALL.md                          # Interactive TUI, CAS layout & resolver
      ├── 05-SECURITY.md                                # Archive limits & schema-drift defense
      ├── 06-API-CONTRACTS.md                           # Immutable releases & Plan v2
      ├── 07-DECISIONS.md                               # Accepted ADRs D-001 through D-025
      ├── 08-DELIVERY.md                                # Delivery roadmap & acceptance gates
      ├── 09-RESEARCH.md                                # MCP 2026-07-28 & primary ecosystem research
      ├── 10-DOMAIN-MODEL.md                            # Domain models & RFC 8785 canonical hashing
      ├── 11-LOCAL-RUNTIME-IPC.md                       # Daemon lifecycle & Named Pipe/Socket IPC
      ├── 12-STORAGE-TRANSACTIONS-RECOVERY.md           # SQLite WAL DDL (22 tables) & journal
      ├── 13-RESOLVER-INSTALL-ENGINE.md                 # DFS resolver, archive safety & safe CAS rollback
      ├── 14-BRIDGE-PROVIDER-MCP.md                     # Stdio Bridge shim & supervisor watchdog
      ├── 15-POLICY-APPROVALS.md                        # 17-action effect taxonomy & policy engine
      ├── 16-HOST-ADAPTERS.md                           # Cline, Pi Agent, Grok Build, dynamic fetching, /marketplace
      ├── 17-SOURCE-ARTIFACT-RUNTIME-ADAPTERS.md        # Decoupled adapter interfaces
      ├── 18-CATALOG-BUILDER-RELEASE-SEARCH.md          # Deterministic builder & lexical ranking
      ├── 19-SECRETS-OAUTH.md                           # OS SecretStore, persistent auth_profiles & OAuth
      ├── 20-ERRORS-AUDIT-DOCTOR.md                     # Error taxonomy, exit codes & doctor framework
      ├── 21-TESTING-CONFORMANCE.md                     # Test pyramid, crash injection & canary scans
      ├── 22-PLATFORM-RELEASE-MIGRATIONS.md             # Cross-compilation & DB migrations
      ├── 23-SCHEMAS-EXAMPLES.md                        # Schema fixtures & examples
      ├── 24-FUNCTION-INVENTORY.md                      # Aspirational function map (21 of the 38 packages; §21 verified)
      ├── 25-WEB-FRONTEND-UI.md                         # Web marketplace frontend inspired by mcpmarket.com
      ├── 26-ECOSYSTEM-IA-PACKAGE-MODEL.md              # Neutral Package/Capability model, 8-type taxonomy, honesty rule
      ├── 27-CAPABILITY-SOURCE-SUPPORT-MATRIX.md        # Status snapshot per type/source with code evidence
      ├── 28-ACP-AGENT-DOCS-VERIFICATION.md             # ACP registry-vs-docs verification + overrides
      ├── 29-CONNECTOR-SYSTEM-DESIGN.md                 # Connector proxy & credential custody design (deleted under D-021; resurrection planned)
      ├── 30-DATA-DRIVEN-BRIDGE-TARGETS.md              # BridgeTarget table (44) + GenericAdapter, surgical merge
      ├── 31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md       # Verified competitor record, proposal adjudication, evidence vocabulary, backlog
      ├── 32-MANIFEST-LOCK-INTEROP.md                   # `litespm.yml`/`litespm.lock`, frozen install, SBOM, interop  [DESIGNED]
      ├── 33-DEPLOYMENT-LEDGER-RECONCILIATION.md        # Deployment ledger & three-way reconciliation  [DESIGNED]
      ├── 34-RUNTIME-INVOCATION-RECEIPTS.md             # Capability registry, invocation engine, receipts  [DESIGNED]
      ├── 35-PROFILES-AND-CAPABILITY-LEASES.md          # Multi-kind profiles & capability leases  [DESIGNED]
      ├── 36-ENTERPRISE-POLICY-AND-AUDIT.md             # Policy hierarchy, audit --ci, signing  [DESIGNED]
      └── 37-TUI-AND-COMPLETION.md                      # TUI, staged-plan tray, shell completion  [DESIGNED]
```

### 5.1 Command-line surface

```text
litespm                                  interactive setup wizard (no network fetch)
litespm setup | init                     same as `litespm`
litespm version                          version + protocol + build target
litespm search <query>                   search the local catalog index
litespm install <id> [--version <v>] [--scope user|project] [--workspace <id>]
                                         records a plan + human approval; skills and MCP servers
                                         install, plugins need artifact wiring (STATUS.md)
litespm uninstall [--dry-run]            remove the bridge entry from every host config
litespm catalog sync                     fetch the release pointer (live origin — STATUS.md §2)
litespm daemon serve                     start the supervisor + IPC engine
litespm bridge stdio [--host <id>]       stateless MCP stdio shim
litespm host list|detect|setup|remove    host adapters (remove takes <host-id> or --all)
litespm agent list|resolve <id>          ACP registry listing / launch-spec resolution [--registry|--file|--json]
litespm skills add|list|update|remove    SKILL.md lifecycle (add: --skill/--agent/--scope/--yes/--json)
litespm doctor [--repair] [--yes]        health checks and optional repair
litespm self-update [--force]            fetch the release manifest and update the binary
litespm help                             usage
```

Exit codes: `0` success · `1` internal · `2` usage · `10` catalog · `20` resolve · `30` approval ·
`40` install · `50` provider · `60` host · `70` state. The category codes `10`–`70` are emitted by
`litespm doctor` today; other commands return `0`/`1`/`2` until they gain categorized failures
([ARCH/20](ARCH/20-ERRORS-AUDIT-DOCTOR.md) §2).

---

## 6. Technical Stack & Development Invariants

*   **Local Core & Daemon:** Written in Go (1.26) for static cross-platform compilation, OS process group / Job Object control, SQLite WAL concurrency, and hand-rolled MCP JSON-RPC 2.0 (no external SDK).
*   **Database:** SQLite 3 with Write-Ahead Logging (`WAL`), utilizing pure-Go drivers (`modernc.org/sqlite`) for zero-CGO cross-compilation.
*   **Web Marketplace:** Static export using Next.js 15 / React 19 + Tailwind CSS + Radix UI, deployed to Cloudflare Pages — commands, layout and deploy path are documented in [`web/README.md`](web/README.md).
*   **Testing:** Multi-tier testing pyramid featuring property-based tests, hostile archive fuzzing, and synthetic secret canary scans. A systematic crash-injection harness is designed but **not built** (see [ARCH/21 §4](ARCH/21-TESTING-CONFORMANCE.md#4-crash-injection-fault-matrix-designed--no-harness-in-tree)).

---

## 7. Environment, Origins & Release Order

### Environment variables

| Variable | Default | Effect |
|---|---|---|
| `LITESPM_DATA_ROOT` | Linux `$XDG_DATA_HOME/litespm` (`~/.local/share/litespm`), macOS `~/Library/Application Support/LiteSPM`, Windows `%LOCALAPPDATA%\LiteSPM` | Cache, state database, installed providers, backups |
| `LITESPM_CONFIG_ROOT` | Linux `$XDG_CONFIG_HOME/litespm`, macOS Application Support, Windows `%APPDATA%\LiteSPM` | User `config.toml` |
| `LITESPM_RUNTIME_ROOT` | Linux `$XDG_RUNTIME_DIR/litespm` (else `/tmp/litespm-$UID`), macOS `~/Library/Caches/LiteSPM/run` | Socket, daemon lock, PID files |
| `LITESPM_REGISTRY_URL` | `config.DefaultRegistryURL` | Catalog and release-pointer origin (`/v1/current.json`) |
| `LITESPM_LOG_LEVEL` | `info` | Daemon log verbosity |
| `LITESPM_POLICY_DEFAULT_LEVEL` | `ask_once` | Default install policy gate |
| `LITESPM_POLICY_ENFORCE_SIGNATURES` | `true` | Fail closed on unverified artifacts |
| `LITESPM_NETWORK_TIMEOUT_SEC` | `30` | Timeout for registry and artifact fetches |
| `LITESPM_RELEASE_REPO` | `sarv-projects/LiteSPM` | npm `postinstall`: repository hosting the release assets |
| `NEXT_PUBLIC_SITE_URL` | live deployment origin (`web/lib/site.ts`) | Web build: canonical, Open Graph and JSON-LD origin |

The pre-rename `LITEPSM_*` variables (`LITEPSM_DATA_ROOT`, `LITEPSM_CONFIG_ROOT`,
`LITEPSM_RUNTIME_ROOT`, `LITEPSM_REGISTRY_URL`, `LITEPSM_LOG_LEVEL`,
`LITEPSM_POLICY_DEFAULT_LEVEL`, `LITEPSM_POLICY_ENFORCE_SIGNATURES`,
`LITEPSM_NETWORK_TIMEOUT_SEC`) remain honoured as fallbacks when the current
`LITESPM_*` name is unset (`internal/config/config.go:122-147`).

### Compiled origins must resolve

Two origins ship as compiled defaults, so each must name a host that answers:

*   **`config.DefaultRegistryURL`** is where the client fetches `/v1/current.json` and the release manifests it points at. It names the live deployment origin because `registry.litespm.dev` is not registered yet; a default that fails at DNS turns every catalog fetch into an opaque network error instead of a reportable one. Repoint it per install with `LITESPM_REGISTRY_URL` or `catalog.registryUrl` in `config.toml`.
*   **`SITE_URL`** (`web/lib/site.ts`) drives `metadataBase`, Open Graph cards, and the JSON-LD emitted on package pages. Canonical URLs must name the origin that actually serves the site, so any build can override it:

```bash
NEXT_PUBLIC_SITE_URL=https://litespm.market npm run build   # or ./scripts/deploy-pages.sh
```

### Release order: GitHub assets before npm

`npm/scripts/install-binary.js` downloads `litespm-<os>-<arch>` from the `v<version>` GitHub release during `postinstall`. Publishing the package before those assets exist ships a wrapper whose binary download 404s, so `release.yml` fixes the order: it builds the six-platform matrix plus `SHA256SUMS.txt`, creates the GitHub release, verifies that `npm/package.json` matches the tag, and only then publishes to npm with `--provenance` (skipped unless the `NPM_TOKEN` repository secret is set). [`scripts/README.md`](scripts/README.md) is the inventory of every repository script and which workflow — if any — runs it.

```bash
git tag v0.3.0 && git push --tags           # 1. tag -> workflow builds assets and creates the release
npm deprecate litepsm "Renamed to LiteSPM"   # 2. retire the previous package name afterwards
```

---

## 8. License & Legal

LiteSPM is licensed under the **Apache License, Version 2.0**. The full text is in [`LICENSE`](LICENSE).

Apache-2.0 was chosen over MIT for its explicit patent grant and its contribution terms, both of
which matter for a project that writes into other people's agent configuration files.

*   [`NOTICE`](NOTICE) — attribution required by the license, plus third-party acknowledgements.
*   **Secret handling:** credentials are held only in the operating-system-protected vault — the
    master key is protected by `secret-tool` (Linux Secret Service), `/usr/bin/security` (macOS
    Keychain), or Windows DPAPI; there is no WinCred binding ([ARCH/19 §2](ARCH/19-SECRETS-OAUTH.md#2-platform-specific-secret-vault-backends)).
    No secret is written to the state database, to any config file, to a log, or to the hosted
    catalog. `test/canary_test.go` (run by `go test ./...` on every CI OS) plants synthetic
    canaries through the real secret-store and launch-resolution paths, then scans the state
    database, its WAL/SHM sidecars, every file under the data and config roots, and the supervisor
    log buffer for them — it does not scan production logs or the hosted catalog. An MCP server
    process receives a scoped
    credential only when a launch actually supplies one (launch-time injection is `IMPLEMENTED`,
    not `WIRED` — [STATUS.md](STATUS.md) §1); a future brokered connector never hands its token to
    the agent ([ARCH/29](ARCH/29-CONNECTOR-SYSTEM-DESIGN.md)).
*   **Release integrity:** release binaries are published with `SHA256SUMS.txt` and the self-updater
    fails closed on a digest mismatch. **Releases are not yet signed** — see
    [`SECURITY.md`](SECURITY.md) for the open gap and the planned mitigations.
