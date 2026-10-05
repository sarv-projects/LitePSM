# LiteSPM Market & Client: Technical Architecture & System Specification

**The Lightweight Skill & Package Manager for AI Agents.**

LiteSPM is an open-source, provider-neutral package manager, federated catalog, and local control plane for AI agent capabilities: **Plugins, Skills, and MCP (Model Context Protocol) Servers**. 

It eliminates the need to manually configure, update, and manage capabilities across fragmented AI developer tools (for Claude Code, Codex, OpenCode, and many more). By registering a lightweight, version-pinned LiteSPM Bridge once per agent, users can discover, install, update, and supervise capabilities centrally from a single local control plane.

> **Foundational Security Invariant:** Credentials and execution remain strictly on the user's workstation or directly with the selected upstream provider. LiteSPM's hosted public catalog does not store credentials, execute plugin scripts, or proxy tool calls. The client-side control plane operates entirely within the local user's operating system privileges and enforces local, fail-closed authorization policies.

---

## 1. Project Status & In-Progress Roadmap

| Phase | Description | Focus Area | Status |
|---|---|---|---|
| **Phase A** | **Architecture Freeze & LLD Specifications** | `ARCH/00`–`ARCH/25`, 6 JSON Schemas (Draft 2020-12), `AGENTS.md`, `TEST.md` | **COMPLETED** |
| **Phase B** | **Foundations, Storage & Local IPC** | `internal/domain`, `internal/config`, `internal/state` (SQLite WAL 22 tables), `internal/ipc` (Named Pipes/Sockets), `cmd/litespm` | **COMPLETED** |
| **Phase C** | **Static Catalog & Discovery Plane** | `internal/source` (MCP Registry, Skills), `internal/catalogbuild` (Release builder), `internal/catalog` (Search) | **COMPLETED** |
| **Phase D** | **Safe Extraction & Skill Store** | `internal/artifact` (Archive safety limits), `internal/resolver` (Constraint solver), `internal/install` (Atomic CAS), `internal/skills` | **COMPLETED** |
| **Phase E** | **Process Supervision, Bridge & Host Adapters** | `internal/provider` (Job Objects/Watchdog), `internal/policy`, `internal/bridge`, 6 bespoke adapters (Codex, Claude, OpenCode, Cline, Pi Agent, Grok Build) + 44 generic BridgeTargets (50 total, `ARCH/30`), 77 skill targets | **COMPLETED** |
| **Phase F** | **MCP Protocol Dual-Profile, Secrets & OAuth** | Stateless MCP 2026-07-28 (Streamable HTTP), legacy 2025-11-25, WinCred/DPAPI/Keychain, OAuth PKCE Loopback | **COMPLETED** |
| **Phase G** | **In-Agent `/marketplace` Panel & Web UI** | 4-Tab Panel, pre-existing tool detection, Next.js static web frontend (`mcpmarket.com` style) | **COMPLETED** |
| **Phase H** | **Release Engineering & Packaging** | Cross-platform Go builds, npm wrapper (`litespm`), conformance test suites | **COMPLETED** |
| **Phase I** | **Golden Fixtures, Self-Update & Migrations** | Host fixtures corpus, `self-update` binary replacement, database migration engine | **COMPLETED** |

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
    │  ├── /v1/current.json (Lightweight atomic pointer to latest release)   │
    │  └── /v1/releases/<release-id>/ (Immutable metadata, shards, manifests) │
    └────────────────────────────────────┬────────────────────────────────────┘
                                         │ HTTPS (Read-only, ETag-cached)
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
                             │ Local Authenticated IPC
                             │ (Windows: Named Pipe with DACL / Unix: Domain Socket 0600)
                             ▼
    ┌─────────────────────────────────────────────────────────────────────────┐
    │                           LiteSPM Daemon                                │
    │ ┌─────────────────────────────────────────────────────────────────────┐ │
    │ │ IPC Session Manager & Dispatcher                                    │ │
    │ └──────────────┬──────────────────┬───────────────────┬───────────────┘ │
    │                ▼                  ▼                   ▼                 │
    │ ┌─────────────────────────┐ ┌───────────────┐ ┌───────────────────────┐ │
    │ │ SQLite State (WAL mode) │ │ Policy Engine │ │ OS Secret Broker      │ │
    │ │ (22 Relational Tables)  │ │ & Approvals   │ │ (WinCred/DPAPI/Keych) │ │
    │ └─────────────────────────┘ └───────────────┘ └───────────────────────┘ │
    │                │                  │                   │                 │
    │                ▼                  ▼                   ▼                 │
    │ ┌─────────────────────────┐ ┌─────────────────────────────────────────┐ │
    │ │ Content-Addressed Store │ │ Provider Supervisor                     │ │
    │ │ (trees/ & artifacts/)   │ │ (Windows Job Objects / Process Groups)  │ │
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
    *   **Process Supervisor:** Manages child provider processes. On Windows, child processes are attached to Windows Job Objects configured with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`. On Linux/macOS, processes run in dedicated process groups controlled by an internal supervisor watchdog holding a control pipe (`PR_SET_PDEATHSIG` on Linux) to guarantee zero orphans.
    *   **Policy Authority:** Evaluates tool invocation permissions and verifies cryptographic `planHash` values against human approvals.
    *   **Secret Broker:** Interfaces with native OS vaults; injects credentials into provider environments only in memory at launch time.

### 2.3 Storage Layer (`DATA_ROOT`)
*   **`state.db` (SQLite 3 in WAL mode):** Maintains 22 relational tables covering installations, active provider sessions, capability definitions, permission grants, host backups, persistent `auth_profiles`, `operation_trees`, and audit events.
*   **Content-Addressed Store (`trees/` and `artifacts/`):** All extracted packages and download artifacts are indexed strictly by SHA-256 digest (`trees/sha256/<2-hex>/<digest>/...`). This enables tamper-evident rollbacks and deduplication.
*   **Operation Journal (14 States):** Every filesystem mutation is journaled (`created` $\rightarrow$ `staging` $\rightarrow$ `commit_intent` $\rightarrow$ `committed`). Interrupted operations cleanly recover on daemon startup, deleting only trees created by that operation.

---

## 3. End-to-End Workflows

### 3.1 Global Installation & Interactive Setup (`litespm`)
1.  **Installation:** Installed globally via npm (`npm install -g litespm`) or downloaded as a standalone native Go binary.
2.  **Interactive TUI Wizard:** Running `litespm` launches an interactive terminal interface:
    *   **Dynamic Adapter Fetching:** The CLI contacts `/v1/current.json` to verify the latest verified adapter list.
    *   **Agent Selector Dropdown:** Select your agent (Cline, Pi Agent, Grok Build, Claude Code, Codex, OpenCode).
    *   **Automated Config Discovery:** LiteSPM scans platform-standard paths across Windows, macOS, and Linux.
    *   **Graceful Fallback:** If the file is not found, prompts the user to:
        *   *Enter path manually*
        *   *Print copy-paste snippet*
        *   *Retry detection*
    *   **Safe Atomic Merge:** LiteSPM creates a timestamped backup in `DATA_ROOT/backups/`, preserves all existing comments/keys, injects the version-pinned Bridge entry, and installs the `/marketplace` command hook.

### 3.4 Skill Installer (`litespm skills add`)

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

### 3.2 Non-Destructive Update Checking
*   Whenever a user runs `litespm` in terminal or invokes `/marketplace` in an agent, LiteSPM performs a passive read of `/v1/current.json`.
*   It compares installed versions against catalog release digests.
*   **Zero Silent Mutation:** If updates are available, it alerts the user with a clean delta diff, but **never mutates local files** without explicit user approval.

### 3.3 The In-Agent `/marketplace` Experience
Typing `/marketplace` in any configured agent opens the **Capability Panel** with 4 dedicated tabs:

```text
┌────────────────────────────────────────────────────────────────────────┐
│                          LiteSPM Capabilities                          │
├──────────────┬──────────────┬──────────────┬───────────────────────────┤
│ [MCP SERVERS]│[AGENT SKILLS]│  [PLUGINS]   │     [INSTALLED (5)] ●     │
└──────────────┴──────────────┴──────────────┴───────────────────────────┘
```

*   **Tab 1: MCP Servers:** Filter official and community servers by category or transport (`stdio` / `Streamable HTTP`). One-click install.
*   **Tab 2: Agent Skills:** Browse portable `SKILL.md` workflows from `agentskills.io`.
*   **Tab 3: Plugins:** Multi-component toolkits with per-host compatibility badges.
*   **Tab 4: Installed & Detected Capabilities:**
    *   **Green Light Indicator (●):** Visual status showing whether a capability is `● Active / Ready`, `🟡 Needs Auth`, or `○ Disabled`.
    *   **Detected External Capabilities (Read-Only by Default):** Scans the agent's primary on-disk configuration for pre-existing native tools. To prevent accidental config mutation, external tools are strictly read-only by default until the user explicitly triggers an **Adopt** action.

### 3.4 Capability Schema-Drift & Identity Protection
*   Upon tool discovery, LiteSPM generates a SHA-256 fingerprint of the tool's input JSON Schema (`schemaFingerprint`).
*   User approvals bind cryptographically to strong identity tuples:
    *   **Local Stdio:** `(capability_id, schemaFingerprint, casTreeDigest)`
    *   **Remote HTTP:** `(capability_id, schemaFingerprint, endpointOrigin, serverVersionDigest)`
*   If an upstream provider modifies its schema upon reconnection, or if local code or remote endpoints change, LiteSPM automatically invalidates the grant (`status: "changed"`), blocking unapproved execution until re-reviewed.

---

## 4. Supported Agent Ecosystem

| Agent Host | Interface / Environment | Configuration Format | Default Location |
|---|---|---|---|
| **Cline** | VS Code Extension | JSON (`cline_mcp_settings.json`) | Windows: `%APPDATA%\Code\User\globalStorage\saoudrizwan.claude-dev\settings\`<br>macOS: `~/Library/Application Support/Code/User/globalStorage/saoudrizwan.claude-dev/settings/`<br>Linux: `~/.config/Code/User/globalStorage/saoudrizwan.claude-dev/settings/`<br>Cline CLI (not managed): `~/.cline/data/settings/cline_mcp_settings.json` |
| **Pi Agent** | Terminal Coding Agent (`pi`) | JSON (`mcp.json` / `config.json`) + TS Extension | Unix: `~/.pi/agent/mcp.json`<br>Windows: `%USERPROFILE%\.pi\agent\mcp.json`<br>Project: `.pi/mcp.json` (trust-gated)<br>Legacy fallback: `~/.pi/config.json` / `~/.pi/mcp.json` |
| **Grok Build** | Terminal / IDE (`grok`) [id: `grok-build`] | TOML (`config.toml`) | Unix: `~/.grok/config.toml`<br>Windows: `%USERPROFILE%\.grok\config.toml`<br>Project: `.grok/config.toml`<br>Legacy fallback: `%APPDATA%\Grok\config.toml` |
| **Claude Code** | Terminal CLI (`claude`) | JSON (`~/.claude.json`) | Unix: `~/.claude.json`<br>Windows: `%USERPROFILE%\.claude.json`<br>Project: `.mcp.json` in project root (local scope entry in `~/.claude.json`)<br>`CLAUDE_CONFIG_DIR` overrides the config directory |
| **OpenAI Codex** | Terminal CLI (`codex`) | TOML (`config.toml`) | Unix: `~/.codex/config.toml`<br>Windows: `%USERPROFILE%\.codex\config.toml`<br>Project: `.codex/config.toml`<br>Legacy fallback: `%APPDATA%\Codex\config.toml`; `$CODEX_HOME` overrides the directory |
| **OpenCode** | Open-source CLI (`opencode`) | JSON (`opencode.json` - v1 `mcp` / v2 `mcp.servers`) | Unix: `~/.config/opencode/opencode.json`<br>Windows: `%USERPROFILE%\.config\opencode\opencode.json`<br>Project: `opencode.json` or `.opencode/`<br>Legacy fallback: `%APPDATA%\OpenCode\opencode.json` |

LiteSPM registers exactly one `litespm` bridge entry per host (`litespm bridge stdio --host <agent-id>`); individual capabilities are resolved by the daemon at runtime, and OpenCode local entries require `"type": "local"` with a combined string-array `"command"`.

---

## 5. Repository Structure & Architectural Guide

```text
/
  ├── README.md                                         # This master technical specification
  ├── TODO.md                                           # Master delivery ledger (Phases A through I)
  ├── AGENTS.md                                         # Supported AI agents guide & /marketplace UX
  ├── TEST.md                                           # Test scenarios for Cline, Pi Agent, Grok Build
  │
  ├── cmd/
  │   └── litespm/                                      # Root CLI entrypoint (daemon serve, bridge, host, skills, doctor, agent)
  │       ├── main.go                                   # Command dispatch + daemon/IPC handlers
  │       ├── wizard.go                                 # Interactive agent setup wizard
  │       ├── skills_add.go                             # `skills add` installer (clone, select, copy)
  │       └── skills_remove.go                          # `skills remove/list` ledger-backed removal
  │
  ├── internal/ (21 packages)
  │   ├── domain/                                       # Pure domain models, canonical IDs, RFC 8785 JCS, errors
  │   ├── config/                                       # Platform paths (%LOCALAPPDATA%, XDG, runtimes) & config
  │   ├── state/                                        # SQLite WAL engine (22 tables), safe CAS rollback journal
  │   ├── ipc/                                          # Local authenticated IPC (Named Pipes DACL / Unix 0600)
  │   ├── source/                                       # Upstream adapters (MCP Registry, Skills, Claude/Codex/Cursor/Grok, ACP)
  │   ├── catalogbuild/                                 # Deterministic release compiler & manifest generator
  │   ├── catalog/                                      # Catalog client, HTTP sync, ETag cache & lexical search
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
  └── ARCH/                                             # 31 Architecture & LLD Documents (00–30)
      ├── 00-INDEX.md                                   # Normative status, precedence & registry
      ├── 01-PRODUCT.md                                 # Product contract & 5-stage lifecycle
      ├── 02-HLD.md                                     # Daemon/Bridge split & sequence diagrams
      ├── 03-CATALOG-SOURCES.md                         # Federation, SourceSnapshot & SSRF
      ├── 04-CLIENT-INSTALL.md                          # Interactive TUI, CAS layout & resolver
      ├── 05-SECURITY.md                                # Archive limits & schema-drift defense
      ├── 06-API-CONTRACTS.md                           # Immutable releases & Plan v2
      ├── 07-DECISIONS.md                               # Accepted ADRs D-001 through D-020
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
      ├── 24-FUNCTION-INVENTORY.md                      # Public Go function inventory (21 packages)
      ├── 25-WEB-FRONTEND-UI.md                         # Web marketplace frontend inspired by mcpmarket.com
      ├── 26-ECOSYSTEM-IA-PACKAGE-MODEL.md              # Neutral Package/Capability model, 8-type taxonomy, honesty rule
      ├── 27-CAPABILITY-SOURCE-SUPPORT-MATRIX.md        # Status snapshot per type/source with code evidence
      ├── 28-ACP-AGENT-DOCS-VERIFICATION.md             # ACP registry-vs-docs verification + overrides
      ├── 29-CONNECTOR-SYSTEM-DESIGN.md                 # Local proxy execution design (deferred, not implemented)
      └── 30-DATA-DRIVEN-BRIDGE-TARGETS.md              # BridgeTarget table (44) + GenericAdapter, surgical merge
```

---

## 6. Technical Stack & Development Invariants

*   **Local Core & Daemon:** Written in Go (1.23+) for static cross-platform compilation, OS process group / Job Object control, SQLite WAL concurrency, and hand-rolled MCP JSON-RPC 2.0 (no external SDK).
*   **Database:** SQLite 3 with Write-Ahead Logging (`WAL`), utilizing pure-Go drivers (`modernc.org/sqlite`) for zero-CGO cross-compilation.
*   **Web Marketplace:** Static export using Next.js 15 / React 19 + Tailwind CSS + Radix UI, deployed to Cloudflare Pages.
*   **Testing:** Multi-tier testing pyramid featuring property-based tests, hostile archive fuzzing, crash injection, and synthetic secret canary scans.

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

### Compiled origins must resolve

Two origins ship as compiled defaults, so each must name a host that answers:

*   **`config.DefaultRegistryURL`** is where the client fetches `/v1/current.json` and the release manifests it points at. It names the live deployment origin because `registry.litespm.dev` is not registered yet; a default that fails at DNS turns every catalog fetch into an opaque network error instead of a reportable one. Repoint it per install with `LITESPM_REGISTRY_URL` or `catalog.registryUrl` in `config.toml`.
*   **`SITE_URL`** (`web/lib/site.ts`) drives `metadataBase`, Open Graph cards, and the JSON-LD emitted on package pages. Canonical URLs must name the origin that actually serves the site, so any build can override it:

```bash
NEXT_PUBLIC_SITE_URL=https://litespm.market npm run build   # or ./scripts/deploy-pages.sh
```

### Release order: GitHub assets before npm

`npm/scripts/install-binary.js` downloads `litespm-<os>-<arch>` from the `v<version>` GitHub release during `postinstall`. Publishing the package before those assets exist ships a wrapper whose binary download 404s, so `release.yml` fixes the order: it builds the six-platform matrix plus `SHA256SUMS.txt`, creates the GitHub release, verifies that `npm/package.json` matches the tag, and only then publishes to npm with `--provenance` (skipped unless the `NPM_TOKEN` repository secret is set).

```bash
git tag v0.3.0 && git push --tags           # 1. tag -> workflow builds assets and creates the release
npm deprecate litepsm "Renamed to LiteSPM"   # 2. retire the previous package name afterwards
```
