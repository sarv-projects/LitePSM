# Product Contract & User Experience

## 1. Product Identity

*   **LiteSPM Market:** The public, federated discovery surface for AI agent plugins, skills, and MCP servers. Available as a fast, static web application and a read-only HTTP catalog.
*   **LiteSPM Client:** The local manager and control plane running on the user's workstation. It resolves, verifies, installs, updates, and supervises capabilities across supported agents — the six wizard hosts (Cline, Pi Agent, Grok Build, Claude Code, OpenAI Codex, OpenCode) plus 44 additional generic bridge targets, 50 in total (ARCH/30).
*   **Expansion & Acronym:** **LiteSPM** — *The Lightweight Skill & Package Manager for AI Agents*. **SPM** expands to **Skill & Package Manager**; the catalog spans Plugins, Skills, and MCP servers. A `connector` listing kind remains in the domain enum but is **deprecated and deferred to v2**: `internal/connector` was removed under decision `D1`, and `ARCH/29` is a design record only ([STATUS.md](../STATUS.md) §4).

---

## 2. Problem Statement

AI agent capabilities are scattered across fragmented ecosystems: the official MCP registry, Agent Skills Git repositories, Claude plugin marketplaces, OpenAI portable plugins, and bespoke client directories. Each ecosystem requires different configuration formats, different authentication mechanics, and repetitive manual setup in every agent.

Users face two unacceptable trade-offs today:
1.  **Manual Configuration Burden:** Manually maintaining JSON configuration files across multiple agents leads to configuration drift, duplicate processes, and broken environments.
2.  **Centralized SaaS Tool Proxies:** Commercial tool marketplaces often require routing downstream credentials and real-time execution traffic through third-party cloud gateways, creating severe data-privacy and supply-chain risks.

LiteSPM solves this by providing **centralized federated discovery** combined with **strictly local execution and client-side credential custody**.

---

## 3. Product Goals & Non-Goals

### Goals
1.  **Zero-Account Public Discovery:** Search and inspect public listings via website, CLI, or Discovery MCP without requiring registration or an API key.
2.  **One-Time Agent Setup:** Connect an agent host once to LiteSPM. Subsequent additions, updates, or removals of skills and MCP servers are managed centrally without editing host configuration files again.
3.  **Client-Side Secret & Execution Custody:** Downstream API keys and OAuth tokens remain exclusively on the user's device in native operating system vaults (Windows Credential Manager / DPAPI, macOS Keychain, Linux Secret Service). Tool calls flow directly from the user's machine to the provider.
4.  **Preservation of Upstream Semantics:** Retain original package formats, upstream identifiers, and version digests. Normalization is a discovery projection, not an erasure of provenance.
5.  **Multi-Stage Capability Verification:** Clearly report the independent operational status of every item (`listed`, `resolvable`, `installable`, `runnable`, `tested`).
6.  **Fail-Closed User Approval:** Agents cannot self-authorize capabilities. Effectful actions (filesystem writes, command execution, network requests) require explicit user approval.
7.  **Passive Freshness & Non-Destructive Invocations:** Invocations of `litespm` or in-agent `/marketplace` check for available catalog updates without mutating local installations or configuration unless explicitly confirmed. *(Target: today the wizard prints only compiled-in advisory metadata and in-agent reads resolve against the local catalog index — see §5.1 step 5.)*

### Non-Goals
1.  **Cloud Tool Proxying:** LiteSPM hosted services will never proxy tool requests, execute plugin code in the cloud, or store downstream service credentials.
2.  **Universal OS Sandbox:** The local LiteSPM daemon enforces capability routing, schema validation, and policy checks, but it is **not an OS-level sandbox**. Executing a local stdio MCP provider runs under the user's operating system privileges. This is consistent with `ARCH/08` §3 (no OS-kernel-sandboxing claims) and `ARCH/05` §10. A formal **isolation-tier declaration** is `DESIGNED` only — no tier is declared or reported by `doctor` today ([STATUS.md](../STATUS.md) §5; `ARCH/31` proposal #22, "sandbox tiers 0–3").
3.  **Automatic Capability Grants:** LiteSPM will never automatically grant permissions or install packages simply because an LLM requested them.
4.  **Proprietary Adapter Scripting:** Third-party execution code is not downloaded dynamically during ingestion or resolution.
5.  **Universal Direct Tool Projection:** LiteSPM does not project thousands of catalog tools into an agent's context window simultaneously. Tool discovery is progressively routed on-demand.

### Forward-Looking Scope (all `DESIGNED`)

Several product expectations above imply capabilities that are specified but **not implemented**. Each is documented in a dedicated design record and must not be presented as working:

| Product expectation | Design record | State |
|---|---|---|
| Reproducible installs, project manifest/lockfile, interop export, canonical identity graph | `ARCH/32` (Manifest, Lockfile & Interop) | `DESIGNED` |
| Durable ownership of host-config edits and uninstall/update reconciliation | `ARCH/33` (Deployment Ledger & Reconciliation) | `DESIGNED` |
| Real capability invocation, receipts, schema-drift session handling at runtime | `ARCH/34` (Runtime, Invocation & Receipts) | `DESIGNED` |
| Runtime profiles and time/scope-bounded capability leases | `ARCH/35` (Profiles & Capability Leases) | `DESIGNED` |
| Policy hierarchy + `policy explain`, `audit --ci`, advisories/quarantine, signed catalog, SBOM | `ARCH/36` (Enterprise Policy, Audit, Provenance & Supply-Chain Trust) | `DESIGNED` |
| Terminal TUI, local dashboard, shell completion, `why` | `ARCH/37` (TUI, Local Dashboard & Shell Completion) | `DESIGNED` |

---

## 4. Multi-Stage Capability Lifecycle

To prevent misleading claims of compatibility, LiteSPM categorizes every item across five explicit, independent stages:

```text
[Listed] ──> [Resolvable] ──> [Installable] ──> [Runnable] ──> [Tested]
```

> Vocabulary note: these five stages are the product contract for user-facing status. They are not operation states — daemon operations track `created → resolving → awaiting_approval → … → committed / rolled_back` (`internal/state/operations.go`). No automated `runnable`/`tested` prober populates stages 4–5 yet; report them as `Unknown` until evidence exists (honesty rule, ARCH/26 §12.4).

1.  **Listed:** The item's metadata has been ingested from an upstream source and normalized into a valid LiteSPM Listing record.
2.  **Resolvable:** All package artifacts, external references, and dependencies can be resolved to immutable hashes (Git commit SHA, archive SHA-256 digest).
3.  **Installable:** The artifact has been verified to unpack safely into the local Content-Addressed Store (CAS) without exceeding security limits or triggering path-traversal errors.
4.  **Runnable:** The user's workstation satisfies the necessary runtime prerequisites (e.g., Node.js, Python, or native executable) and a compatible LiteSPM `RuntimeAdapter` exists (the `RuntimeAdapter` seam itself is `DESIGNED` — ARCH/17 §5).
5.  **Tested:** Automated integration tests have executed the provider or skill against a specific host agent and verified successful initialization, schema discovery, and safe cleanup.

---

## 5. User Journeys & Interaction Models

### 5.1 Interactive CLI Setup (`litespm`)
When a user installs LiteSPM (via `npm install -g litespm`, `curl`, or direct binary download) and runs `litespm`:

```text
$ litespm
Select your primary AI Agent Host:
  [1] Cline          (VS Code Extension - cline_mcp_settings.json)
  [2] Pi Agent       (Terminal Coding Agent (pi) - mcp.json / config.json + TS Extension)
  [3] Grok Build     (Terminal / IDE (grok) - config.toml (TOML))
  [4] Claude Code    (Terminal CLI (claude) - ~/.claude.json (JSON))
  [5] OpenAI Codex   (Terminal CLI (codex) - config.toml (TOML))
  [6] OpenCode       (Open-source CLI (opencode) - opencode.json (v1 / v2))
  [q] Quit
```

> Implementation note: the wizard lists only the 6 bespoke adapters (`cmd/litespm/wizard.go:supportedAgents`). The remaining 44 generic `BridgeTarget` rows are managed via `litespm host setup <id>` / `litespm host list` (50 total — see ARCH/30). There is no separate `Generic MCP Configuration (JSON export)` wizard entry.

1.  **Agent Selection:** User selects their agent from the interactive terminal dropdown.
2.  **Automated Path Discovery:** LiteSPM scans documented default paths across Windows, macOS, and Linux (e.g., `~/.claude.json`, `~/.codex/config.toml`; native Windows user scope is `%USERPROFILE%\.codex\config.toml` — `%APPDATA%\Codex\config.toml` is only a legacy fallback, `internal/host/codex.go:35-42`).
3.  **Graceful Fallback:** If the configuration file is not found, LiteSPM provides clear feedback (`cmd/litespm/wizard.go:187-197`):
    ```text
    ⚠ Configuration file not found in default locations for Claude Code.

    Choose a configuration option:
      [1] Enter configuration path manually
      [2] Print copy-paste snippet
      [3] Retry auto-detection
      [q] Cancel setup
    ```
4.  **Atomic Registration:** Upon locating or receiving the path, LiteSPM creates a timestamped pre-edit backup (`DATA_ROOT/backups/<host-id>_<timestamp>_<hash>.bak`), safely parses the file, injects a single `litespm` bridge entry (`<binary> bridge stdio --host <host-id>` — no version field and no per-capability snippets are written), and atomically replaces the file.
5.  **Passive Advisory Notice (target — currently compiled-in only):** The intended flow queries the static catalog pointer (`/v1/current.json`) for capability updates without touching local state. Current `verifyRuntimeAdvisories` (`cmd/litespm/wizard.go:138-143`) prints only the compiled-in protocol version and adapter count with no network fetch; `litespm update` / `self-update` updates the LiteSPM binary itself (`internal/update`), not installed capabilities. Capability refresh is `litespm catalog sync` followed by reinstall until an update-notice lands:
    ```text
    [*] 2 installed capabilities have updates available. Run 'litespm catalog sync' to refresh, then reinstall to inspect changes.
    ```
    (`litespm update` is reserved for binary self-update — see `runSelfUpdate` in `cmd/litespm/main.go`.)

### 5.2 In-Agent Interaction (`/marketplace`)
Inside any configured agent (e.g., Claude Code, Codex, OpenCode), the agent or user can invoke LiteSPM:

```text
User / Agent: /marketplace search postgres
```

1.  **Bounded MCP Surface:** The agent queries the local LiteSPM Bridge shim — 12 registered tools (`internal/bridge/shim.go:initTools`). Today `search_catalog`, `get_extension`, `prepare_install`, `list_installed`, `load_skill`, and `read_skill_resource` resolve against the daemon; `search_capabilities`, `describe_capability`, `invoke_capability`, `get_invocation`, and `cancel_invocation` return explicit JSON-RPC `-32601` with a concrete reason ([STATUS.md](../STATUS.md) §4); `request_install` completes for skill listings and fails closed for MCP/plugin (see item 3).
2.  **Progressive Disclosure:** Search results return compact summaries (name, kind, publisher, verified status). Detailed tool schemas and skill contents are retrieved only when specifically requested.
3.  **Install Plan Display:** When an agent proposes installing a capability, it calls `prepare_install`, which previews an immutable `InstallPlan` (resolver → plan, `planHash` persisted) detailing affected paths, runtime commands, and declared permissions. Execution goes through `request_install` (`planId` + human approval token), not CLI flags — **and it completes for skills**, which are installed as files through the skills ledger. MCP/plugin installs still fail closed (`LPSM-ARTIFACT-UNAVAILABLE`) because the catalog carries no artifact locator for them (`internal/install/engine.go:204-207`; [STATUS.md](../STATUS.md) §3).
4.  **User Confirmation Boundary:** Installation cannot proceed without out-of-band user approval. If the agent's host UI does not support reliable interactive form elicitation, the human path is the CLI. The Bridge surfaces the daemon's refusal as an MCP tool error; it does **not** generate a command string. The documented CLI invocation is:
    ```text
    To approve this installation, run in your terminal:
    litespm install <listing-id> --version <ver> --scope user|project
    ```
    (`litespm install` usage: `litespm install <listing-id> [--version <ver>] [--scope user|project] [--workspace <id>]` — `cmd/litespm/main.go:757`. There is no `--plan-id` flag: the CLI does not consume a persisted plan. It routes by kind — a skill listing installs its real files; MCP/plugin listings fail closed with `LPSM-ARTIFACT-UNAVAILABLE`.)

---

## 6. Product Vocabulary

The primary user-facing interfaces (CLI, Web Market, in-agent outputs) must strictly adhere to human-centered language:

| Standard User Term | Technical Implementation Term | Prohibited Jargon in Main Flows |
|---|---|---|
| **Connect** | Register Bridge Shim in host configuration | "Inject stdio IPC proxy" |
| **Install** | Stage, verify CAS tree, and commit DB row | "Materialize runtime artifacts" |
| **Update** | Plan v2 delta evaluation and atomic pointer swap | "Mutate lockfile snapshot" |
| **Remove** | Delete DB references and prune unreferenced CAS | "Garbage collect tree digests" |
| **Capabilities** | Bundled MCP tools, skills, or plugin components | "Heterogeneous RPC endpoints" |
| **Verified** | Automated host test evidence passed on date X | "Certified 100% secure" |
| **Needs Approval** | Policy engine returned `ask` requirement | "Elicitation boundary triggered" |

> State note: the table is the **vocabulary contract**, not a status claim. `Install` for archive kinds runs the real staging → CAS verify → journal commit path, but nothing supplies an artifact source for those kinds yet; **skills install as files** through the skills ledger. `Update` delta evaluation and lockfile pointer swap do not exist yet (`ARCH/32`, `DESIGNED`), and `Remove` currently deletes DB rows and host-config entries but performs **no CAS pruning** (`install.remove` deletes rows; no prune routine exists — [STATUS.md](../STATUS.md) §3). `Verified` may only be printed when real test evidence exists (ARCH/26 §12.4).
