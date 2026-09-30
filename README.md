# LitePSM Market & Client: Technical Architecture & System Specification

LitePSM is an open-source, provider-neutral package manager, federated catalog, and local control plane for AI agent capabilities: **Plugins, Skills, and MCP (Model Context Protocol) Servers**. 

It eliminates the need to manually configure, update, and manage capabilities across fragmented AI developer tools (Cline, Pi Agent, Grok Build, Claude Code, OpenAI Codex, OpenCode). By registering a lightweight, version-pinned LitePSM Bridge once per agent, users can discover, install, update, and supervise capabilities centrally from a single local control plane.

> **Foundational Security Invariant:** Credentials and execution remain strictly on the user's workstation or directly with the selected upstream provider. LitePSM's hosted public catalog does not store credentials, execute plugin scripts, or proxy tool calls. The client-side control plane operates entirely within the local user's operating system privileges and enforces local, fail-closed authorization policies.

---

## 1. Project Status & In-Progress Roadmap

| Phase | Description | Focus Area | Status |
|---|---|---|---|
| **Phase A** | **Architecture Freeze & LLD Specifications** | `ARCH/00`–`ARCH/25`, 6 JSON Schemas (Draft 2020-12), `AGENTS.md`, `TEST.md` | **COMPLETED** |
| **Phase B** | **Foundations, Storage & Local IPC** | `internal/domain`, `internal/config`, `internal/state` (SQLite WAL 22 tables), `internal/ipc` (Named Pipes/Sockets), `cmd/litepsm` | **COMPLETED** |
| **Phase C** | **Static Catalog & Discovery Plane** | `internal/source` (MCP Registry, Skills), `internal/catalogbuild` (Release builder) | **IN PROGRESS** |
| **Phase D** | **Safe Extraction & Skill Store** | `internal/artifact` (Archive safety limits), `internal/resolver` (Constraint solver) | Planned |
| **Phase E** | **Process Supervision & First Adapters** | `internal/provider` (Job Objects/Watchdog), `internal/bridge`, **Cline, Pi Agent, Grok Build** adapters | Planned |
| **Phase F** | **MCP Protocol Dual-Profile & Secrets** | Stateless MCP 2026-07-28 (Streamable HTTP), legacy 2025-11-25, WinCred/DPAPI/Keychain | Planned |
| **Phase G** | **In-Agent `/litepsm` Panel & Web UI** | 4-Tab Panel, pre-existing tool detection, Next.js static web frontend (`mcpmarket.com` style) | Planned |
| **Phase H** | **Release Engineering & Packaging** | Cross-platform Go builds, npm wrapper (`litepsm`), conformance test suites | Planned |

---

## 2. System Architecture: "What Is What"

LitePSM bifurcates system responsibilities between an untrusted public discovery layer and a privileged local control plane:

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
    │                           LitePSM Daemon                                │
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
*   **What it is:** A thin binary invoked directly by host agents as an MCP server (`litepsm bridge stdio --host <agent-id>`).
*   **Responsibilities:** Speaks standard MCP JSON-RPC over `stdin`/`stdout`, packages agent requests, and forwards them across local IPC to the Daemon. It terminates cleanly when the agent terminates stdio.
*   **What it does NOT do:** It never touches SQLite directly, never modifies configuration files, and never spawns downstream provider processes.

### 2.2 The LitePSM Daemon (Single-Writer Control Plane)
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

### 3.1 Global Installation & Interactive Setup (`litepsm`)
1.  **Installation:** Installed globally via npm (`npm install -g litepsm`) or downloaded as a standalone native Go binary.
2.  **Interactive TUI Wizard:** Running `litepsm` launches an interactive terminal interface:
    *   **Dynamic Adapter Fetching:** The CLI contacts `/v1/current.json` to verify the latest verified adapter list.
    *   **Agent Selector Dropdown:** Select your agent (Cline, Pi Agent, Grok Build, Claude Code, Codex, OpenCode).
    *   **Automated Config Discovery:** LitePSM scans platform-standard paths across Windows, macOS, and Linux.
    *   **Graceful Fallback:** If the file is not found, prompts the user to:
        *   *Enter path manually*
        *   *Print copy-paste snippet*
        *   *Retry detection*
    *   **Safe Atomic Merge:** LitePSM creates a timestamped backup in `DATA_ROOT/backups/`, preserves all existing comments/keys, injects the version-pinned Bridge entry, and installs the `/litepsm` command hook.

### 3.2 Non-Destructive Update Checking
*   Whenever a user runs `litepsm` in terminal or invokes `/litepsm` in an agent, LitePSM performs a passive read of `/v1/current.json`.
*   It compares installed versions against catalog release digests.
*   **Zero Silent Mutation:** If updates are available, it alerts the user with a clean delta diff, but **never mutates local files** without explicit user approval.

### 3.3 The In-Agent `/litepsm` Experience
Typing `/litepsm` in any configured agent opens the **Capability Panel** with 4 dedicated tabs:

```text
┌────────────────────────────────────────────────────────────────────────┐
│                          LitePSM Capabilities                          │
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
*   Upon tool discovery, LitePSM generates a SHA-256 fingerprint of the tool's input JSON Schema (`schemaFingerprint`).
*   User approvals bind cryptographically to strong identity tuples:
    *   **Local Stdio:** `(capability_id, schemaFingerprint, casTreeDigest)`
    *   **Remote HTTP:** `(capability_id, schemaFingerprint, endpointOrigin, serverVersionDigest)`
*   If an upstream provider modifies its schema upon reconnection, or if local code or remote endpoints change, LitePSM automatically invalidates the grant (`status: "changed"`), blocking unapproved execution until re-reviewed.

---

## 4. Supported Agent Ecosystem

| Agent Host | Interface / Environment | Configuration Format | Default Location |
|---|---|---|---|
| **Cline** | VS Code Extension | JSON (`cline_mcp_settings.json`) | Windows: `%APPDATA%\Code\User\globalStorage\saoudrizwan.claude-dev\settings\`<br>macOS: `~/Library/Application Support/Code/User/globalStorage/saoudrizwan.claude-dev/settings/`<br>Linux: `~/.config/Code/User/globalStorage/saoudrizwan.claude-dev/settings/` |
| **Pi Agent** | Terminal Coding Agent (`pi`) | JSON (`mcp.json` / `config.json`) + TS Extension | Unix: `~/.pi/agent/mcp.json` or `~/.pi/config.json`<br>Windows: `%USERPROFILE%\.pi\agent\mcp.json` or `%USERPROFILE%\.pi\config.json` |
| **Grok Build** | Terminal / IDE (`grok`) | TOML (`config.toml`) | Unix: `~/.grok/config.toml` (or project `.grok/config.toml`)<br>Windows: `%USERPROFILE%\.grok\config.toml` or `%APPDATA%\Grok\config.toml` |
| **Claude Code** | Terminal CLI (`claude`) | JSON (`~/.claude.json`) | Unix: `~/.claude.json`<br>Windows: `%USERPROFILE%\.claude.json` |
| **OpenAI Codex** | Terminal CLI (`codex`) | TOML (`config.toml`) | Unix: `~/.codex/config.toml`<br>Windows: `%USERPROFILE%\.codex\config.toml` or `%APPDATA%\Codex\config.toml` |
| **OpenCode** | Open-source CLI (`opencode`) | JSON (`opencode.json` - v1 `mcp` / v2 `mcp.servers`) | Unix: `~/.config/opencode/opencode.json`<br>Windows: `%APPDATA%\OpenCode\opencode.json` |

---

## 5. Repository Structure & Architectural Guide

```text
/
  ├── README.md                                         # This master technical specification
  ├── TODO.md                                           # Master delivery ledger (Phases A through H)
  ├── AGENTS.md                                         # Supported AI agents guide & /litepsm UX
  ├── TEST.md                                           # Test scenarios for Cline, Pi Agent, Grok Build
  │
  ├── cmd/
  │   └── litepsm/                                      # Root CLI entrypoint (doctor, version, daemon serve)
  │       └── main.go
  │
  ├── internal/
  │   ├── domain/                                       # Pure domain models, canonical IDs, RFC 8785 JCS, errors
  │   ├── config/                                       # Platform paths (%LOCALAPPDATA%, XDG, runtimes) & config
  │   ├── state/                                        # SQLite WAL engine (22 tables), safe CAS rollback journal
  │   └── ipc/                                          # Local authenticated IPC (Named Pipes DACL / Unix 0600)
  │
  ├── schemas/                                          # Draft 2020-12 Canonical JSON Schemas
  │   ├── install-plan.schema.json                      # Cryptographic plan schema with planHash
  │   ├── listing.schema.json                           # Normalized catalog listing schema
  │   ├── version.schema.json                           # Pinned version & artifact reference schema
  │   ├── source.schema.json                            # Upstream source & snapshot configuration schema
  │   ├── catalog-release.schema.json                   # Immutable release pointer & manifest schema
  │   └── errors.schema.json                            # Standardized LPSM-* machine error schema
  │
  └── ARCH/                                             # 26 Architecture & LLD Documents
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
      ├── 16-HOST-ADAPTERS.md                           # Cline, Pi Agent, Grok, dynamic fetching, /litepsm
      ├── 17-SOURCE-ARTIFACT-RUNTIME-ADAPTERS.md        # Decoupled adapter interfaces
      ├── 18-CATALOG-BUILDER-RELEASE-SEARCH.md          # Deterministic builder & lexical ranking
      ├── 19-SECRETS-OAUTH.md                           # OS SecretStore, persistent auth_profiles & OAuth
      ├── 20-ERRORS-AUDIT-DOCTOR.md                     # Error taxonomy, exit codes & doctor framework
      ├── 21-TESTING-CONFORMANCE.md                     # Test pyramid, crash injection & canary scans
      ├── 22-PLATFORM-RELEASE-MIGRATIONS.md             # Cross-compilation & DB migrations
      ├── 23-SCHEMAS-EXAMPLES.md                        # Schema fixtures & examples
      ├── 24-FUNCTION-INVENTORY.md                      # Public Go function inventory (20 packages)
      └── 25-WEB-FRONTEND-UI.md                         # Web marketplace frontend inspired by mcpmarket.com
```

---

## 6. Technical Stack & Development Invariants

*   **Local Core & Daemon:** Written in Go (1.23+) for static cross-platform compilation, OS process group / Job Object control, SQLite WAL concurrency, and official MCP Go SDK integration.
*   **Database:** SQLite 3 with Write-Ahead Logging (`WAL`), utilizing pure-Go drivers (`modernc.org/sqlite`) for zero-CGO cross-compilation.
*   **Web Marketplace:** Static export using Next.js 15 / React 19 + Tailwind CSS + Radix UI, deployed to Cloudflare Pages.
*   **Testing:** Multi-tier testing pyramid featuring property-based tests, hostile archive fuzzing, crash injection, and synthetic secret canary scans.
