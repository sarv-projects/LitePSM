# Architecture Index & Governance

## 1. Document Status & Precedence

This architecture defines the complete specification for **LitePSM**, comprising high-level system boundaries, protocol contracts, low-level component designs, database schemas, and verification suites.

### Document Hierarchy & Normative Authority
*   **Normative Specifications:** The security requirements ([05-SECURITY](05-SECURITY.md)), domain models ([10-DOMAIN-MODEL](10-DOMAIN-MODEL.md)), IPC/daemon architecture ([11-LOCAL-RUNTIME-IPC](11-LOCAL-RUNTIME-IPC.md)), storage/recovery models ([12-STORAGE-TRANSACTIONS-RECOVERY](12-STORAGE-TRANSACTIONS-RECOVERY.md)), API/schema contracts ([06-API-CONTRACTS](06-API-CONTRACTS.md), [23-SCHEMAS-EXAMPLES](23-SCHEMAS-EXAMPLES.md)), and function inventories ([24-FUNCTION-INVENTORY](24-FUNCTION-INVENTORY.md)) are **normative**. Code implementation must strictly conform to these specifications.
*   **Informative Documents:** Product background ([01-PRODUCT](01-PRODUCT.md)), delivery plans ([08-DELIVERY](08-DELIVERY.md)), and research snapshots ([09-RESEARCH](09-RESEARCH.md)) are **informative**. They describe context, user paths, and ecosystem observations.
*   **Conflict Resolution Rule:** In any case of conflict or ambiguity, document precedence is strictly ordered as follows:
    $$\text{Security } (05) \succ \text{Data/State Contracts } (06, 10, 11, 12, 14, 15) \succ \text{HLD } (02) \succ \text{ADRs } (07) \succ \text{Product/Delivery } (01, 08)$$

---

## 2. Document Registry

### Foundational Architecture (HLD)

| Document | Scope & Ownership | Status |
|---|---|---|
| [01 — Product Contract](01-PRODUCT.md) | Problem statement, user personas, UX terminology, 5-stage capability lifecycle | Informative |
| [02 — High-Level Design (HLD)](02-HLD.md) | Discovery plane vs. control plane, daemon/bridge split, process topology, sequence flows | Normative |
| [03 — Catalog and Source Federation](03-CATALOG-SOURCES.md) | Upstream source federation, SourceSnapshot, freshness, source vs. runtime split | Normative |
| [04 — Client and Installation](04-CLIENT-INSTALL.md) | CLI commands, interactive wizard, agent auto-detection, local CAS store | Normative |
| [05 — Security, Trust, and Credentials](05-SECURITY.md) | Privilege boundaries, archive limits, SSRF protection, nested MCP clamping, drift defense | Normative |
| [06 — API and Package Contracts](06-API-CONTRACTS.md) | Immutable static releases, Plan v2, local IPC protocol, Bridge MCP tool surface | Normative |
| [07 — Architecture Decisions (ADRs)](07-DECISIONS.md) | Accepted architectural decision records (D-001 through D-020) | Normative |
| [08 — Delivery Plan](08-DELIVERY.md) | Staged delivery phases (Phase A through Phase H) and automated acceptance gates | Informative |
| [09 — Research Ledger](09-RESEARCH.md) | Pinned primary sources, MCP 2026-07-28 protocol, ecosystem caveats | Informative |

### Low-Level Design Specifications (LLD)

| Document | Scope & Ownership | Status |
|---|---|---|
| [10 — Domain Model](10-DOMAIN-MODEL.md) | Canonical identifier grammars, domain entities, RFC 8785 canonical hashing | Normative |
| [11 — Local Runtime & IPC](11-LOCAL-RUNTIME-IPC.md) | Daemon lifecycle, Windows named pipes, Unix domain sockets, JSON-RPC 2.0 framing | Normative |
| [12 — Storage, Transactions & Recovery](12-STORAGE-TRANSACTIONS-RECOVERY.md) | SQLite WAL DDL (22 tables), CAS filesystem layout, 14-state operation journal, safe CAS rollback | Normative |
| [13 — Resolver & Install Engine](13-RESOLVER-INSTALL-ENGINE.md) | Pure DFS resolver, cycle detection, artifact verification, two-phase atomic commit | Normative |
| [14 — Bridge, Provider Supervisor & MCP](14-BRIDGE-PROVIDER-MCP.md) | Stdio Bridge shim, provider supervisor, Job Objects, dual-protocol MCP client | Normative |
| [15 — Policy & Approvals Engine](15-POLICY-APPROVALS.md) | 17-action effect taxonomy, PolicyInput/Decision, approval channels, schema-drift invalidation | Normative |
| [16 — Host Adapters & In-Agent UX](16-HOST-ADAPTERS.md) | HostAdapter interface, auto-detection, fallback prompt, Codex/Claude/Grok/OpenCode/Cline adapters, `/litepsm` | Normative |
| [17 — Source, Artifact & Runtime Adapters](17-SOURCE-ARTIFACT-RUNTIME-ADAPTERS.md) | Decoupled SourceAdapter, ArtifactFetcher, and RuntimeAdapter interface contracts | Normative |
| [18 — Catalog Builder, Releases & Search](18-CATALOG-BUILDER-RELEASE-SEARCH.md) | Deterministic CI builder, `/v1/releases/` immutable layout, manifest digests, search ranking | Normative |
| [19 — Secrets & OAuth Broker](19-SECRETS-OAUTH.md) | OS SecretStore backends (WinCred/DPAPI, Keychain, Secret Service), loopback PKCE OAuth | Normative |
| [20 — Errors, Audit & Doctor](20-ERRORS-AUDIT-DOCTOR.md) | `LPSM-*` error taxonomy, CLI exit codes (0–70), audit event logging, `litepsm doctor` | Normative |
| [21 — Testing & Conformance](21-TESTING-CONFORMANCE.md) | Test pyramid, property testing, archive fuzzing, crash injection, fake MCP conformance | Normative |
| [22 — Platform, Release & Migrations](22-PLATFORM-RELEASE-MIGRATIONS.md) | Multi-platform build matrix, transactional DB migrations, downgrade prevention, self-update | Normative |
| [23 — Schemas & Examples](23-SCHEMAS-EXAMPLES.md) | Complete JSON Schema (Draft 2020-12) specifications and golden test fixtures | Normative |
| [24 — Function Inventory](24-FUNCTION-INVENTORY.md) | Complete Go package and public function inventory across all 20 internal modules | Normative |
| [25 — Web Frontend UI](25-WEB-FRONTEND-UI.md) | Web marketplace UI inspired by mcpmarket.com, component hierarchy, detail drawer | Normative |

### Companion Operational Guides

| Document | Scope & Ownership | Status |
|---|---|---|
| [AGENTS.md](../AGENTS.md) | Supported AI Agents guide (Cline, Pi Agent, Grok Build, Claude, Codex, OpenCode) | Normative |
| [TEST.md](../TEST.md) | Testing guide for Cline, Pi Agent, and Grok Build host adapters | Normative |

---

## 3. Glossary & Core Architectural Concepts

*   **LitePSM Daemon:** The single-writer, persistent local background process running on the user's workstation. It holds exclusive write locks on SQLite (`state.db`), supervises provider child processes, evaluates policy, brokers OS secrets, and executes atomic journaled operations.
*   **LitePSM Bridge (Shim):** A lightweight, stateless MCP stdio server registered with a host agent (e.g., Codex, Claude Code). It translates host MCP JSON-RPC requests into authenticated local IPC calls to the LitePSM Daemon and exits cleanly when the host closes stdio.
*   **Listing:** A normalized discovery record published in the static catalog representing an upstream plugin, skill, MCP server, or connector.
*   **Artifact:** The physical software bundle (tarball, zip, git tree, or container image) retrieved from an upstream publisher.
*   **Content-Addressed Storage (CAS):** Local immutable storage indexed strictly by SHA-256 content digest, preventing in-place corruption and enabling safe rollbacks.
*   **SourceSnapshot:** A point-in-time capture of an upstream registry or marketplace, recording sync status (`healthy`, `partial`, `failed`, `stale`), upstream commit/revision, item count, and content digest.
*   **CatalogRelease:** An immutable, sequence-numbered public catalog deployment (`/v1/releases/<release-id>/...`) verified by a top-level `manifest.json`.
*   **InstallPlan (v2):** A cryptographically bound, immutable description of an install, update, or removal operation. It contains exact versions, artifact digests, local filesystem effects, declared permissions, and preconditions, hashed into a canonical SHA-256 `planHash`.
*   **Approval:** An authorization record binding a specific user confirmation (or explicit policy grant) to an immutable `subjectHash` (e.g., `planHash` or `schemaFingerprint`), with replay prevention for one-time approvals.
*   **CapabilityGrant:** A durable permission record authorizing an agent to invoke a specific provider tool, bound cryptographically to `(capability_id, schemaFingerprint, casTreeDigest)` for local providers or `(capability_id, schemaFingerprint, endpointOrigin, serverVersionDigest)` for remote providers.
*   **Schema Fingerprint:** The canonical SHA-256 digest of an MCP tool's JSON Schema. Any modification to tool parameters alters the fingerprint, triggering schema-drift invalidation.
*   **SourceAdapter:** Build-time adapter in CI responsible for fetching upstream discovery feeds and normalizing them into LitePSM Listing schemas without downloading package bytes.
*   **ArtifactFetcher:** Client-side component responsible for downloading raw bytes and verifying integrity digests without executing scripts.
*   **RuntimeAdapter:** Client-side component responsible for materializing the execution environment (e.g., virtual environment, node_modules) and constructing launch specifications.
*   **HostAdapter:** Integration module that discovers, backs up, and safely merges the LitePSM Bridge entry into an agent host's native configuration.
