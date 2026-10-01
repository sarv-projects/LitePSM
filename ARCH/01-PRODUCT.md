# Product Contract & User Experience

## 1. Product Identity

*   **LitePSM Market:** The public, federated discovery surface for AI agent plugins, skills, and MCP servers. Available as a fast, static web application and a read-only HTTP catalog.
*   **LitePSM Client:** The local manager and control plane running on the user's workstation. It resolves, verifies, installs, updates, and supervises capabilities across supported agents (Codex, Claude Code, Grok Build, OpenCode, Cline, etc.).
*   **Expansion & Acronym:** **PSM** stands for **Plugins, Skills, and MCP**. Connectors represent user-facing integration listings that resolve to MCP servers or plugin packages.

---

## 2. Problem Statement

AI agent capabilities are scattered across fragmented ecosystems: the official MCP registry, Agent Skills Git repositories, Claude plugin marketplaces, OpenAI portable plugins, and bespoke client directories. Each ecosystem requires different configuration formats, different authentication mechanics, and repetitive manual setup in every agent.

Users face two unacceptable trade-offs today:
1.  **Manual Configuration Burden:** Manually maintaining JSON configuration files across multiple agents leads to configuration drift, duplicate processes, and broken environments.
2.  **Centralized SaaS Tool Proxies:** Commercial tool marketplaces often require routing downstream credentials and real-time execution traffic through third-party cloud gateways, creating severe data-privacy and supply-chain risks.

LitePSM solves this by providing **centralized federated discovery** combined with **strictly local execution and client-side credential custody**.

---

## 3. Product Goals & Non-Goals

### Goals
1.  **Zero-Account Public Discovery:** Search and inspect public listings via website, CLI, or Discovery MCP without requiring registration or an API key.
2.  **One-Time Agent Setup:** Connect an agent host once to LitePSM. Subsequent additions, updates, or removals of skills and MCP servers are managed centrally without editing host configuration files again.
3.  **Client-Side Secret & Execution Custody:** Downstream API keys and OAuth tokens remain exclusively on the user's device in native operating system vaults (Windows Credential Manager / DPAPI, macOS Keychain, Linux Secret Service). Tool calls flow directly from the user's machine to the provider.
4.  **Preservation of Upstream Semantics:** Retain original package formats, upstream identifiers, and version digests. Normalization is a discovery projection, not an erasure of provenance.
5.  **Multi-Stage Capability Verification:** Clearly report the independent operational status of every item (`listed`, `resolvable`, `installable`, `runnable`, `tested`).
6.  **Fail-Closed User Approval:** Agents cannot self-authorize capabilities. Effectful actions (filesystem writes, command execution, network requests) require explicit user approval.
7.  **Passive Freshness & Non-Destructive Invocations:** Invocations of `litepsm` or in-agent `/marketplace` check for available catalog updates without mutating local installations or configuration unless explicitly confirmed.

### Non-Goals
1.  **Cloud Tool Proxying:** LitePSM hosted services will never proxy tool requests, execute plugin code in the cloud, or store downstream service credentials.
2.  **Universal OS Sandbox:** The local LitePSM daemon enforces capability routing, schema validation, and policy checks, but it is **not an OS-level sandbox**. Executing a local stdio MCP provider runs under the user's operating system privileges.
3.  **Automatic Capability Grants:** LitePSM will never automatically grant permissions or install packages simply because an LLM requested them.
4.  **Proprietary Adapter Scripting:** Third-party execution code is not downloaded dynamically during ingestion or resolution.
5.  **Universal Direct Tool Projection:** LitePSM does not project thousands of catalog tools into an agent's context window simultaneously. Tool discovery is progressively routed on-demand.

---

## 4. Multi-Stage Capability Lifecycle

To prevent misleading claims of compatibility, LitePSM categorizes every item across five explicit, independent stages:

```text
[Listed] ──> [Resolvable] ──> [Installable] ──> [Runnable] ──> [Tested]
```

1.  **Listed:** The item's metadata has been ingested from an upstream source and normalized into a valid LitePSM Listing record.
2.  **Resolvable:** All package artifacts, external references, and dependencies can be resolved to immutable hashes (Git commit SHA, archive SHA-256 digest).
3.  **Installable:** The artifact has been verified to unpack safely into the local Content-Addressed Store (CAS) without exceeding security limits or triggering path-traversal errors.
4.  **Runnable:** The user's workstation satisfies the necessary runtime prerequisites (e.g., Node.js, Python, or native executable) and a compatible LitePSM `RuntimeAdapter` exists.
5.  **Tested:** Automated integration tests have executed the provider or skill against a specific host agent and verified successful initialization, schema discovery, and safe cleanup.

---

## 5. User Journeys & Interaction Models

### 5.1 Interactive CLI Setup (`litepsm`)
When a user installs LitePSM (via `npm install -g litepsm`, `curl`, or direct binary download) and runs `litepsm`:

```text
$ litepsm
? Select your AI Agent to configure:
  > Claude Code
    OpenAI Codex
    Grok Build
    OpenCode
    Cline
    Generic MCP Configuration (JSON export)
```

1.  **Agent Selection:** User selects their agent from the interactive terminal dropdown.
2.  **Automated Path Discovery:** LitePSM scans documented default paths across Windows, macOS, and Linux (e.g., `~/.claude.json`, `%APPDATA%\Codex\config.json`).
3.  **Graceful Fallback:** If the configuration file is not found, LitePSM provides clear feedback:
    ```text
    [!] Unable to locate default configuration for Claude Code.
    ? How would you like to proceed?
      > Enter custom path to configuration file
      > Print manual setup snippet (copy & paste)
      > Retry auto-detection
      > Exit
    ```
4.  **Atomic Registration:** Upon locating or receiving the path, LitePSM creates a timestamped pre-edit backup, safely parses the file, injects the pinned LitePSM Bridge entry, and atomically replaces the file.
5.  **Passive Update Notice:** On every interactive launch, LitePSM queries the static catalog pointer (`/v1/current.json`). If updates exist for locally installed capabilities, it displays an informational banner without touching local state:
    ```text
    [*] 2 installed capabilities have updates available. Run 'litepsm update' to inspect changes.
    ```

### 5.2 In-Agent Interaction (`/marketplace`)
Inside any configured agent (e.g., Claude Code, Codex, OpenCode), the agent or user can invoke LitePSM:

```text
User / Agent: /marketplace search postgres
```

1.  **Bounded MCP Surface:** The agent queries the local LitePSM Bridge shim using `search_catalog`, `describe_capability`, or `list_installed`.
2.  **Progressive Disclosure:** Search results return compact summaries (name, kind, publisher, verified status). Detailed tool schemas and skill contents are retrieved only when specifically requested.
3.  **Install Plan Display:** When an agent proposes installing a capability, it calls `prepare_install`, which generates an immutable `InstallPlan` detailing affected paths, runtime commands, and declared permissions.
4.  **User Confirmation Boundary:** The Bridge enforces that installation cannot proceed without out-of-band user approval. If the agent's host UI does not support reliable interactive form elicitation, the Bridge returns the exact CLI command for the user to execute:
    ```text
    To approve this installation, run in your terminal:
    litepsm install mcp:builtin:mcp-registry/postgres --plan-id 01J9X...
    ```

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
