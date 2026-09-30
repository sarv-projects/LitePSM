# Architecture Decision Records (ADRs)

## 1. Foundational Architecture Decisions (D-001 – D-010)

### D-001: Provider-Neutral Shared Product & Local Execution
LitePSM is designed to serve multiple agent hosts (Codex, Claude Code, Grok Build, OpenCode, Cline). The hosted service provides catalog discovery only. Package installation, process supervision, and tool execution occur strictly on the user's workstation.

### D-002: Product and Marketplace Naming
The product and CLI binary are named **LitePSM** (`litepsm`). The public web catalog is **LitePSM Market**. `PSM` expands to Plugins, Skills, and MCP. Directory slug is `litePSM`.

### D-003: Client-Side Downstream Credentials
Downstream credentials (API keys, OAuth tokens) reside in the operating system's native credential store and are never transmitted to LitePSM cloud services. Downstream providers receive tokens only at the time of user-authorized execution.

### D-004: Static Catalog on Cloudflare Pages
The public catalog is deployed as static, immutable JSON files via Cloudflare Pages from a private GitHub repository. No application server, database, or worker is required for public discovery.

### D-005: Source Federation over Monolithic Rewriting
LitePSM aggregates documented upstream feeds and Git marketplace manifests. It preserves original source attribution and upstream identifiers rather than attempting to hand-curate or rewrite thousands of packages.

### D-006: Preservation of Package Format Semantics
Upstream package formats (Agent Skills, MCP servers, portable plugins) are preserved in their native structures. Incompatible or host-specific components are flagged explicitly rather than silently rewritten or dropped.

### D-007: Mandatory Local Plan & User Approval
Discovery is read-only. Installing, updating, or removing capabilities requires generating an immutable `InstallPlan` and obtaining explicit user confirmation before modifying local files or starting processes.

### D-008: No Silent Auto-Updates by Default
Installed versions bind to immutable content digests. Updates are user-initiated, present delta diffs across code and permissions, and require explicit approval.

### D-009: Integration-Based Host Support
LitePSM exposes standard MCP stdio, HTTPS APIs, and documented host adapters. It does not claim automatic integration with hosts that lack a documented MCP or configuration extension point.

### D-010: One-Time Host Bridge Registration
For supported agents, LitePSM configures a single Bridge entry per host. Subsequent skills and MCP providers are managed within LitePSM's central local store, avoiding repeated edits to host configuration files.

---

## 2. Core Implementation Decisions (D-011 – D-020)

### D-011: Single-Writer Local Control Plane (Daemon)
*   **Context:** Multiple agent hosts (Codex, Claude, OpenCode) can run simultaneously. If each Bridge shim directly modified files or started child processes, concurrency races and state corruption would occur.
*   **Decision:** All mutable SQLite transactions, provider process supervision, OS secret access, and filesystem commits are owned exclusively by a single local LitePSM Daemon per user account.
*   **Status:** Accepted.

### D-012: SQLite (WAL) + Content-Addressed Storage (CAS)
*   **Context:** Flat lockfiles (`installs.lock`) cannot handle concurrent reads, transactional journals, or rollbacks.
*   **Decision:** Structured metadata is stored in SQLite 3 with Write-Ahead Logging (`WAL`). Extracted package trees and raw downloads are stored in a Content-Addressed Store (`trees/` and `artifacts/`) indexed by SHA-256 digests.
*   **Status:** Accepted.

### D-013: Split Bridge Architecture (Stateless Shims)
*   **Context:** Host agents expect an MCP stdio server.
*   **Decision:** The host-facing Bridge executable is a lightweight, stateless shim. It handles stdio JSON-RPC framing and forwards all state, discovery, and execution requests over authenticated local IPC to the Daemon.
*   **Status:** Accepted.

### D-014: Statically Linked Native Adapters in v1
*   **Context:** Allowing dynamic third-party adapter scripts introduces supply-chain code execution risks during ingestion and resolution.
*   **Decision:** All source adapters, artifact fetchers, runtime adapters, and host adapters are compiled directly into the LitePSM Go binary. No dynamic adapter code is downloaded or executed.
*   **Status:** Accepted.

### D-015: Cryptographic Plan Binding (InstallPlan v2)
*   **Context:** Time-of-Check to Time-of-Use (TOCTOU) attacks could alter package contents or permissions between plan creation and user confirmation.
*   **Decision:** `InstallPlan` v2 computes an RFC 8785 canonical SHA-256 `planHash` across all execution fields. Approval binds strictly to `planHash`. Plans include mandatory expiration and precondition checks.
*   **Status:** Accepted.

### D-016: Capability Schema-Drift Invalidates Grants
*   **Context:** Downstream MCP providers could alter tool parameter schemas after receiving approval.
*   **Decision:** Tool inputs are fingerprinted via SHA-256 digests of their JSON Schemas (`schemaFingerprint`). Any drift upon provider reconnection immediately invalidates pre-existing capability grants and halts execution until re-approved.
*   **Status:** Accepted.

### D-017: Official MCP SDK & Named Protocol Profiles
*   **Context:** Custom wire protocol implementations risk subtle incompatibilities.
*   **Decision:** LitePSM utilizes the official MCP Go SDK and explicitly tests two named protocol profiles:
    1.  **Modern Profile:** 2026-07-28 stateless architecture with Streamable HTTP and header mirroring (`Mcp-Method`).
    2.  **Legacy Profile:** 2025-11-25 stateful initialization for backwards compatibility.
*   **Status:** Accepted.

### D-018: TUF Metadata Framework for Future Signed Releases
*   **Context:** Ad-hoc cryptographic signing schemes are vulnerable to rollback and freeze attacks.
*   **Decision:** If cryptographic catalog signing is introduced, LitePSM will adopt The Update Framework (TUF) standard. V1 implements HTTPS origin verification and monotonic sequence verification.
*   **Status:** Accepted.

### D-019: Prohibition of Command Marketplace Sources
*   **Context:** Claude Code and Grok Build manifests support `command` sources that execute shell scripts during catalog ingestion.
*   **Decision:** Marketplace sources of type `command` are strictly prohibited in v1. They are flagged as unsupported to prevent arbitrary remote code execution during ingestion.
*   **Status:** Accepted.

### D-020: Five-Stage Support Taxonomy
*   **Context:** Generic "verified" badges are ambiguous and misleading.
*   **Decision:** All catalog listings explicitly and independently display five operational status values: `listed`, `resolvable`, `installable`, `runnable`, and `tested`.
*   **Status:** Accepted.
