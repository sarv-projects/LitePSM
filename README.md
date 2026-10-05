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
| **Phase C** | **Static Catalog & Discovery Plane** | `TESTED` for build+sync; `catalog sync` `WIRED` **but broken at live origin** | `internal/catalogbuild` + `litespm catalog build` are `TESTED` (real-dataset + end-to-end build→serve→sync→search tests); `internal/catalog` is `WIRED` through `litespm catalog sync`, whose automated E2E is green against the deployable tree. **The live origin has not been re-published** — it still serves only the legacy pointer (no `schemaVersion`, no tree), so sync fails closed there until `scripts/deploy-pages.sh` output is uploaded — [ARCH/31 §4.2](ARCH/31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#42-the-published-catalog-and-the-client-read-different-files--the-sync-path-is-dead-at-the-origin) |
| **Phase D** | **Safe Extraction & Skill Store** | `WIRED` (resolver, skills); `IMPLEMENTED` (install engine); `TESTED` (artifact) | `internal/resolver` and `internal/skills` are reached from production paths and `internal/artifact` is test-covered ([STATUS.md](STATUS.md) §1/§3); `internal/install` is `IMPLEMENTED`. The daemon's `install.execute` supplies no artifact source, so an agent-driven install cannot complete — [ARCH/31 §4.1](ARCH/31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#41-the-agent-facing-install-path-cannot-complete--wired-is-false) |
| **Phase E** | **Process Supervision, Bridge & Host Adapters** | `WIRED` for hosts; `IMPLEMENTED` for provider runtime | 6 bespoke adapters + 44 generic BridgeTargets (50 total, `ARCH/30`) + 77 skill targets are registered. Provider autostart is inert: the `providers` table is never populated by non-test code (finding `m4`). See [STATUS.md](STATUS.md) §1/§4 |
| **Phase F** | **MCP Protocol Dual-Profile, Secrets & OAuth** | `WIRED` (secrets) / `IMPLEMENTED` (mcpclient, auth) | Dual-profile client, native OS keystores, OAuth PKCE loopback exist; the secrets vault is opened before serving (`WIRED`), but secrets are not injected at provider launch and `mcpclient` / `auth` have zero production importers — the `AuthBroker` currently has no consumer (finding 99), [STATUS.md](STATUS.md) §1/§4 |
| **Phase G** | **In-Agent `/marketplace` Panel & Web UI** | `WIRED` for the static web marketplace; panel `WIRED` except install | The Next.js static export and Bridge tool surface exist; the panel's install action is blocked by Phase D. See [STATUS.md](STATUS.md) §1 |
| **Phase H** | **Release Engineering & Packaging** | `IMPLEMENTED` | Cross-platform builds, npm wrapper, CI checks. `self-update` completes only once a release publishes `litespm-*` assets (packaging P7) |
| **Phase I** | **Golden Fixtures, Self-Update & Migrations** | `TESTED` for fixtures and migrations; self-update `WIRED` (unsigned) | Fixtures corpus and migration engine (forward apply + downgrade guard) are tested; `self-update` is fail-closed on SHA-256 with rollback. Release signing is not done — see [SECURITY.md](SECURITY.md). Per-row detail: [TODO.md](TODO.md) Phase I |

**Not yet true of LiteSPM, and not claimed anywhere:** a catalog release tree published **at the
live origin** (the tree builds and tests in-repo; the origin upload is pending), an
end-to-end agent-driven install, provider autostart, a project manifest + lockfile, signed releases
or packages, runtime policy enforcement, and any isolation level beyond process supervision.

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
    │      versions — the tree is not published yet; shards are DESIGNED)     │
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
    *   **Process Supervisor:** Manages child provider processes. On Windows, child processes are attached to Windows Job Objects configured with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`. On Linux/macOS, processes run in dedicated process groups controlled by an internal supervisor watchdog holding a control pipe (`PR_SET_PDEATHSIG` on Linux) to guarantee zero orphans.
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
        `catalog sync` path is the only network fetch, and it fails closed at the live origin
        (legacy pointer, no release tree — [STATUS.md](STATUS.md) §2).
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
*   **Install (`request_install`):** reaches `install.execute`, which supplies **no artifact
    source** and therefore cannot complete — [STATUS.md](STATUS.md) §3.
*   **Skills (`load_skill`, `read_skill_resource`):** resolve against real skill data.
*   **Capabilities & invocation (`search_capabilities`, `describe_capability`,
    `invoke_capability`, `get_invocation`, `cancel_invocation`):** return an explicit `-32601`
    "not implemented" because no capability index, invocation registry, or provider rows exist.

**Installed view (`list_installed`).** The daemon returns ledger-backed installs plus
**read-only** components detected in the host's own config file. LiteSPM does **not** health-check
installed capabilities, so no ready/needs-auth/stopped light is asserted: unobserved status renders
as `— Unknown`. External components are detected; there is **no `Adopt` action** — the label is
rendered by the panel, but no adopt handler or tool exists (it is `DESIGNED`).

### 3.5 Capability Schema-Drift & Identity Protection (`IMPLEMENTED`, not `WIRED`)
*   Upon tool discovery, LiteSPM generates a SHA-256 fingerprint of the tool's input JSON Schema (`schemaFingerprint`).
*   User approvals are designed to bind cryptographically to strong identity tuples:
    *   **Local Stdio:** `(capability_id, schemaFingerprint, casTreeDigest)`
    *   **Remote HTTP:** `(capability_id, schemaFingerprint, endpointOrigin, serverVersionDigest)`
*   If an upstream provider modifies its schema upon reconnection, or if local code or remote endpoints change, LiteSPM invalidates the grant (`status: "changed"`), blocking unapproved execution until re-reviewed.

**Honest limit:** the `capability_grants` and `capabilities` writers have no non-test caller, and the
invocation path returns `-32601`, so no grant is persisted or enforced at runtime yet. This is
`IMPLEMENTED` (types and fingerprinting exist), not `WIRED`. See [STATUS.md](STATUS.md) §4.

---

## 4. Supported Agent Ecosystem

| Agent Host | Interface / Environment | Configuration Format | Default Location |
|---|---|---|---|
| **Cline** | VS Code Extension | JSON (`cline_mcp_settings.json`) | Windows: `%APPDATA%\Code\User\globalStorage\saoudrizwan.claude-dev\settings\`<br>macOS: `~/Library/Application Support/Code/User/globalStorage/saoudrizwan.claude-dev/settings/`<br>Linux: `~/.config/Code/User/globalStorage/saoudrizwan.claude-dev/settings/`<br>Cline CLI (not managed): `~/.cline/data/settings/cline_mcp_settings.json` |
| **Pi Agent** | Terminal Coding Agent (`pi`) | JSON (`mcp.json` / `config.json`) + TS Extension | Unix: `~/.pi/agent/mcp.json`<br>Windows: `%USERPROFILE%\.pi\agent\mcp.json`<br>Project: `.pi/mcp.json` (trust-gated)<br>Legacy fallback: `~/.pi/config.json` / `~/.pi/mcp.json` |
| **Grok Build** | Terminal / IDE (`grok`) [id: `grok-build`] | TOML (`config.toml`) | Unix: `~/.grok/config.toml`<br>Windows: `%USERPROFILE%\.grok\config.toml`<br>Project: `.grok/config.toml`<br>Legacy fallback: `%APPDATA%\Grok\config.toml` |
| **Claude Code** | Terminal CLI (`claude`) | JSON (`~/.claude.json`) | Unix: `~/.claude.json`<br>Windows: `%USERPROFILE%\.claude.json`<br>Project: `.mcp.json` in project root (local scope entry in `~/.claude.json`)<br>Host-side: `CLAUDE_CONFIG_DIR` overrides the config directory **for Claude Code itself**; LiteSPM's adapter does not read it (`ARCH/16` §3.4) |
| **OpenAI Codex** | Terminal CLI (`codex`) | TOML (`config.toml`) | Unix: `~/.codex/config.toml`<br>Windows: `%USERPROFILE%\.codex\config.toml`<br>Project: `.codex/config.toml`<br>Legacy fallback: `%APPDATA%\Codex\config.toml`; host-side: `$CODEX_HOME` overrides the directory **for Codex itself**, but the LiteSPM adapter does not honour it (`ARCH/16` §3.5) |
| **OpenCode** | Open-source CLI (`opencode`) | JSON (`opencode.json` - v1 `mcp` / v2 `mcp.servers`) | Unix: `~/.config/opencode/opencode.json`<br>Windows: `%USERPROFILE%\.config\opencode\opencode.json`<br>Project: `opencode.json` or `.opencode/`<br>Legacy fallback: `%APPDATA%\OpenCode\opencode.json` |

LiteSPM registers exactly one `litespm` bridge entry per host (`litespm bridge stdio --host <agent-id>`); individual capabilities are resolved by the daemon at runtime, and OpenCode local entries require `"type": "local"` with a combined string-array `"command"`.

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
      ├── 24-FUNCTION-INVENTORY.md                      # Aspirational function map (21 packages; §21 verified)
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
                                         NOTE: uses a synthetic in-memory package (see §3.1 / STATUS.md)
litespm uninstall [--dry-run]            remove the bridge entry from every host config
litespm catalog sync                     fetch the release pointer (broken at origin — STATUS.md §2)
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
    catalog. Synthetic canary tests assert this in CI. An MCP server process receives a scoped
    credential only when a launch actually supplies one (launch-time injection is `IMPLEMENTED`,
    not `WIRED` — [STATUS.md](STATUS.md) §1); a future brokered connector never hands its token to
    the agent ([ARCH/29](ARCH/29-CONNECTOR-SYSTEM-DESIGN.md)).
*   **Release integrity:** release binaries are published with `SHA256SUMS.txt` and the self-updater
    fails closed on a digest mismatch. **Releases are not yet signed** — see
    [`SECURITY.md`](SECURITY.md) for the open gap and the planned mitigations.
