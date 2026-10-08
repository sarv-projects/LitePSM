# Architecture Index & Governance

## 1. Document Status & Precedence

This architecture defines the complete specification for **LiteSPM**, comprising high-level system boundaries, protocol contracts, low-level component designs, database schemas, and verification suites.

### Document Hierarchy & Normative Authority
*   **Normative Specifications:** The security requirements ([05-SECURITY](05-SECURITY.md)), domain models ([10-DOMAIN-MODEL](10-DOMAIN-MODEL.md)), IPC/daemon architecture ([11-LOCAL-RUNTIME-IPC](11-LOCAL-RUNTIME-IPC.md)), storage/recovery models ([12-STORAGE-TRANSACTIONS-RECOVERY](12-STORAGE-TRANSACTIONS-RECOVERY.md)), and API/schema contracts ([06-API-CONTRACTS](06-API-CONTRACTS.md), [23-SCHEMAS-EXAMPLES](23-SCHEMAS-EXAMPLES.md)) are **normative**. Code implementation must strictly conform to these specifications.
*   **Informative Documents:** Product background ([01-PRODUCT](01-PRODUCT.md)), delivery plans ([08-DELIVERY](08-DELIVERY.md)), research snapshots ([09-RESEARCH](09-RESEARCH.md)), and the function-inventory map ([24-FUNCTION-INVENTORY](24-FUNCTION-INVENTORY.md) — self-ranked *informative / `DESIGNED`*; the source tree is the API) are **informative**. They describe context, user paths, and ecosystem observations.
*   **Conflict Resolution Rule:** In any case of conflict or ambiguity, document precedence is strictly ordered as follows:
    $$\text{Security } (05) \succ \text{Data/State/Schema Contracts } (06, 10, 11, 12, 14, 15, 23) \succ \text{Catalog/Source/Install Contracts } (03, 04, 30) \succ \text{HLD } (02) \succ \text{Function Inventory } (24) \succ \text{ADRs } (07) \succ \text{Product/Delivery } (01, 08)$$
    (`24` keeps its tier for tie-breaking only: it is informative and never overrides the source
    tree or a normative document above it.)
*   **Delivery-phase vocabulary:** the delivery sequence is the nine phases **A through I** of [08-DELIVERY](08-DELIVERY.md), mirrored row-for-row in [TODO.md](../TODO.md); both say nine. [31 §12](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#12-ordered-backlog) numbers its *backlog* 0–9 — a separate ordering for the forward plan, not the delivery phases.

---

## 2. Document Registry

The registry below covers **39 architecture documents — `ARCH/00` through `ARCH/38`** — plus the
companion operational guides, [STATUS.md](../STATUS.md) and [CHANGELOG.md](../CHANGELOG.md), which
sit at the repository root. Status columns state the highest honest evidence state per
[STATUS.md](../STATUS.md); `STATUS.md` wins wherever a row here and that file disagree.

### Foundational Architecture (HLD)

| Document | Scope & Ownership | Status |
|---|---|---|
| [01 — Product Contract](01-PRODUCT.md) | Problem statement, user personas, UX terminology, 5-stage capability lifecycle | Informative |
| [02 — High-Level Design (HLD)](02-HLD.md) | Discovery plane vs. control plane, daemon/bridge split, process topology, sequence flows | Normative |
| [03 — Catalog and Source Federation](03-CATALOG-SOURCES.md) | Upstream source federation, SourceSnapshot, freshness, source vs. runtime split | Normative |
| [04 — Client and Installation](04-CLIENT-INSTALL.md) | CLI commands, interactive wizard, agent auto-detection, local CAS store | Normative |
| [05 — Security, Trust, and Credentials](05-SECURITY.md) | Privilege boundaries, archive limits, SSRF protection, nested MCP clamping, drift defense | Normative |
| [06 — API and Package Contracts](06-API-CONTRACTS.md) | Immutable static releases, Plan v2, local IPC protocol, Bridge MCP tool surface | Normative |
| [07 — Architecture Decisions (ADRs)](07-DECISIONS.md) | Accepted architectural decision records (D-001 through D-031) | Normative |
| [08 — Delivery Plan](08-DELIVERY.md) | Staged delivery phases (Phase A through Phase I) and automated acceptance gates | Informative |
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
| [16 — Host Adapters & In-Agent UX](16-HOST-ADAPTERS.md) | HostAdapter interface, auto-detection, fallback prompt, Codex/Claude/Grok/OpenCode/Cline adapters, `/marketplace` | Normative for the `HostAdapter` contract and adapter registry; status-qualified elsewhere — §2 records advisory discovery as offline today and §5 (`/marketplace` registration) is `DESIGNED` |
| [17 — Source, Artifact & Runtime Adapters](17-SOURCE-ARTIFACT-RUNTIME-ADAPTERS.md) | Real `source.Adapter` contract, the eight compiled source adapters and `internal/artifact` exports, plus the compiled fetcher/runtime interfaces (no production caller) | Normative for the `source.Adapter`/`artifact` contract; `IMPLEMENTED` (unwired) for the fetcher/runtime split |
| [18 — Catalog Builder, Releases & Search](18-CATALOG-BUILDER-RELEASE-SEARCH.md) | Deterministic CI builder, `/v1/releases/` immutable layout, manifest digests, search ranking; shard/`index.json`/`items`/`metadata.json` tree is `DESIGNED` | Normative for the client path + search contract; `DESIGNED` for the unbuilt shard tree |
| [19 — Secrets & OAuth Broker](19-SECRETS-OAUTH.md) | OS SecretStore backends (master key protected by `secret-tool`, `/usr/bin/security`, or DPAPI — no WinCred binding exists), loopback PKCE OAuth | Normative for the `SecretStore` contract and platform backends; §3 (OAuth broker) is `IMPLEMENTED`, not `WIRED` |
| [20 — Errors, Audit & Doctor](20-ERRORS-AUDIT-DOCTOR.md) | `LPSM-*` error taxonomy, CLI exit codes (0–70), audit event logging, `litespm doctor` | Normative |
| [21 — Testing & Conformance](21-TESTING-CONFORMANCE.md) | Test pyramid, property testing, archive fuzzing, crash injection, fake MCP conformance | Normative |
| [22 — Platform, Release & Migrations](22-PLATFORM-RELEASE-MIGRATIONS.md) | Multi-platform build matrix, transactional DB migrations, downgrade prevention, self-update | Normative |
| [23 — Schemas & Examples](23-SCHEMAS-EXAMPLES.md) | Complete JSON Schema (Draft 2020-12) specifications and golden test fixtures | Normative |
| [24 — Function Inventory](24-FUNCTION-INVENTORY.md) | Aspirational map of Go package entry points across all 21 internal modules — §1 symbol names read from `go doc` at `ff0a1db`, §21 (`internal/agent`) the only verified signature section | Informative / `DESIGNED` — the source tree is the API, not this document |
| [25 — Web Frontend UI](25-WEB-FRONTEND-UI.md) | Web marketplace UI inspired by mcpmarket.com, component hierarchy, detail drawer | Normative |
| [26 — Ecosystem IA & Package Model](26-ECOSYSTEM-IA-PACKAGE-MODEL.md) | Neutral `Package`/`Capability` model, 8-type v1 taxonomy, website IA, honesty rule, phased roadmap | Normative for IA/vocabulary/matrix; informative for roadmap |
| [27 — Capability & Source Support Matrix](27-CAPABILITY-SOURCE-SUPPORT-MATRIX.md) | Research-backed status snapshot per capability type and source, with code evidence | Informative status snapshot (not a new contract) |
| [28 — ACP Agent Docs Verification](28-ACP-AGENT-DOCS-VERIFICATION.md) | Per-agent registry-vs-docs verification and override corrections | Informative status snapshot with provenance |
| [29 — Connector System Design](29-CONNECTOR-SYSTEM-DESIGN.md) | Local proxy execution and credential custody design record for the deferred `connector` type | Design record; `internal/connector` deleted under `D-021`. Current direction: resurrect and wire, prerequisite = re-decide `D1` |
| [30 — Data-Driven Bridge Targets](30-DATA-DRIVEN-BRIDGE-TARGETS.md) | `BridgeTarget`/`GenericAdapter` table, surgical config merge, integrity tests | Normative |
| [31 — Competitive Landscape & Roadmap](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md) | Verified competitor record, proposal adjudication, evidence-state vocabulary, new-defect register, ordered backlog | Informative; **binding on §2 evidence vocabulary and §13 forbidden claims** |
| [32 — Manifest, Lockfile & Interop](32-MANIFEST-LOCK-INTEROP.md) | `litespm.yml`/`litespm.lock` schemas, frozen installs, SBOM export, OpenAPM/skills-lock/plugin/MCP interop, canonical identity/alias graph | **§2–§4 `WIRED` (manifest, lockfile, lock verbs, `install --frozen`); §5–§6 `DESIGNED` (interop, identity graph)** |
| [33 — Deployment Ledger & Reconciliation](33-DEPLOYMENT-LEDGER-RECONCILIATION.md) | `DeploymentMutation` ledger schema, locator grammar, three-way reconciliation (before/owned/current), uninstall & update semantics | **DESIGNED** |
| [34 — Runtime, Invocation & Receipts](34-RUNTIME-INVOCATION-RECEIPTS.md) | Capability registry, real `provider.invoke`/`invocation.get`/`invocation.cancel`, invocation engine, health/circuit-breaker, bounded output, tamper-evident receipts | **DESIGNED** |
| [35 — Profiles & Capability Leases](35-PROFILES-AND-CAPABILITY-LEASES.md) | Multi-kind profiles, OCI distribution, `profile` verbs, capability leases (scope/TTL/credential), auto-revocation, session/task binding | **DESIGNED** |
| [36 — Enterprise Policy & Audit](36-ENTERPRISE-POLICY-AND-AUDIT.md) | Tighten-only policy hierarchy, `policy explain` provenance, `audit --ci` (text/json/SARIF/CycloneDX/SPDX), advisories/revocation, TUF-style catalog, Sigstore/SLSA provenance, air-gapped bundles | **DESIGNED** |
| [37 — TUI & Completion](37-TUI-AND-COMPLETION.md) | First-class TUI with zero business logic, tab inventory, staged-plan tray, local dashboard boundary, dynamic shell completion, `why` | **DESIGNED** |
| [38 — CLI Product Surface: Port, Orient & Help](38-CLI-PRODUCT-SURFACE.md) | `litespm copy` cross-agent porting (canonical IR, plan/approve/verify), `list`/`inventory`, the `help` system, orient commands, profile wiring, plan/apply spine | **DESIGNED** |

### Companion Operational Guides

| Document | Scope & Ownership | Status |
|---|---|---|
| [STATUS.md](../STATUS.md) | **Single status authority** — highest honest evidence state for every subsystem | Authority; wins over every other document |
| [CHANGELOG.md](../CHANGELOG.md) | Released-versus-tree change record | Informative |
| [REMEDIATION-PLAN.md](../REMEDIATION-PLAN.md) | Authoritative defect tracker and verification protocol | Informative |
| [SECURITY.md](../SECURITY.md) | Threat model, controls, and the open security gaps | Normative for security claims |
| [AGENTS.md](../AGENTS.md) | Supported AI Agents guide (Cline, Pi Agent, Grok Build, Claude, Codex, OpenCode) | Normative |
| [TEST.md](../TEST.md) | Testing guide for Cline, Pi Agent, and Grok Build host adapters | Normative |
| [scripts/README.md](../scripts/README.md) | Inventory of `scripts/` — what each script does, and which workflow (if any) runs it | Informative |
| [web/README.md](../web/README.md) | Public marketplace site: commands, layout, deploy path, honest state | Informative |
| [LICENSE](../LICENSE) / [NOTICE](../NOTICE) | Apache-2.0 license text and attribution notice | Normative |

---

## 3. Glossary & Core Architectural Concepts

*   **LiteSPM Daemon:** The single-writer, persistent local background process running on the user's workstation. It holds exclusive write locks on SQLite (`state.db`), supervises provider child processes, evaluates policy, brokers OS secrets, and executes atomic journaled operations.
*   **LiteSPM Bridge (Shim):** A lightweight, stateless MCP stdio server registered with a host agent (e.g., Codex, Claude Code). It translates host MCP JSON-RPC requests into local IPC calls to the LiteSPM Daemon — reachability is limited by OS-level ACLs (Windows Named Pipe DACL / Unix socket `0600`) **and** by same-uid peer authentication, which the daemon performs before reading the first request (`SO_PEERCRED`/`LOCAL_PEERCRED`; a uid mismatch is refused with `-32001`, `internal/ipc/peer.go:152`, `internal/ipc/server.go:200-205`) — and exits cleanly when the host closes stdio.
*   **Listing:** A normalized discovery record published in the static catalog representing an upstream plugin, skill, MCP server, or connector.
*   **Artifact:** The physical software bundle (tarball, zip, git tree, or container image) retrieved from an upstream publisher.
*   **Content-Addressed Storage (CAS):** Local immutable storage indexed strictly by SHA-256 content digest, preventing in-place corruption and enabling safe rollbacks.
*   **SourceSnapshot:** A point-in-time capture of an upstream registry or marketplace, recording sync status (`healthy`, `partial`, `failed`, `stale`), upstream commit/revision, item count, and content digest.
*   **CatalogRelease:** An immutable, sequence-numbered public catalog deployment (`/v1/releases/<release-id>/...`) verified by a top-level `manifest.json`. **Published and served live** — the origin answers `200` for `/v1/current.json` and for `/v1/releases/<id>/{manifest,listings,versions}.json`, and `litespm catalog sync` was verified against it from a clean data root (probe 2026-10-05, release `rel-2026-10-05-01`); see [STATUS.md](../STATUS.md) §2. `index.json` / `shards/` / `items/` remain un-emitted by design ([31](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md) §4.2 records the defect this closes).
*   **Evidence State:** Where `DESIGNED`, `IMPLEMENTED`, `WIRED`, `TESTED`, `VERIFIED`, or `SHIPPED` appears as a status, it means exactly what [31](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#2-evidence-vocabulary-binding-repo-wide) §2 defines. Those states are never collapsed.
*   **InstallPlan (v2):** A cryptographically bound, immutable description of an install, update, or removal operation. It contains exact versions, artifact digests, local filesystem effects, declared permissions, and preconditions, hashed into a canonical SHA-256 `planHash`.
*   **Approval:** An authorization record binding a specific user confirmation (or explicit policy grant) to an immutable `subjectHash` (e.g., `planHash` or `schemaFingerprint`), with replay prevention for one-time approvals.
*   **CapabilityGrant:** A durable permission record authorizing an agent to invoke a specific provider tool, bound cryptographically to `(capability_id, schemaFingerprint, casTreeDigest)` for local providers or `(capability_id, schemaFingerprint, endpointOrigin, serverVersionDigest)` for remote providers.
*   **Schema Fingerprint:** The canonical SHA-256 digest of an MCP tool's JSON Schema. Any modification to tool parameters alters the fingerprint, triggering schema-drift invalidation.
*   **Source Adapter (`source.Adapter`):** The real, compiled metadata-ingestion interface in `internal/source` (`SourceID()` + `Ingest`). Adapters parse and normalize upstream discovery metadata into LiteSPM `Listing` schemas and never download package bytes or execute code. Eight adapters exist; five marketplace adapters take an extra raw-manifest byte slice and therefore do not satisfy the two-method interface verbatim (see [17](17-SOURCE-ARTIFACT-RUNTIME-ADAPTERS.md)).
*   **Artifact Fetcher:** **`IMPLEMENTED`, not `WIRED`** — client-side component responsible for downloading raw bytes and verifying integrity digests without executing scripts. The seam exists in `internal/artifact`: the `ArtifactFetcher` interface (`fetcher.go:66`) and `HTTPArchiveFetcher`, covered by `internal/artifact/fetcher_test.go` (https-only, credential refusal, SSRF ranges, redirect cap + downgrade, byte bound, digest) — but it has **no production caller**, because the catalog publishes no artifact locators. What `internal/artifact` additionally exports is bounded spooling, safe extraction and canonical tree digests.
*   **Runtime Adapter:** **`IMPLEMENTED`** — client-side component responsible for materializing the execution environment (e.g., virtual environment, node_modules) and constructing launch specifications. `internal/runtime` (`ARCH/17` §5) plans Node/npm, Python/uv, native, OCI and remote launches and probes availability by `PATH` lookup only; it never executes package code at plan time.
*   **HostAdapter:** Integration module that discovers, backs up, and safely merges the LiteSPM Bridge entry into an agent host's native configuration.
*   **Package / Capability:** The neutral top-level abstraction defined in [26](26-ECOSYSTEM-IA-PACKAGE-MODEL.md): `type` (what it is), `source` (where it came from), `compatibility` (which hosts/runtimes, with evidence level), plus an install adapter (how it is materialised). User-facing synonym: **Capability**.
*   **BridgeTarget:** Data-driven per-agent MCP configuration description (`internal/host/target.go`) consumed by the single `GenericAdapter`; each row records the documentation URL it was verified against ([30](30-DATA-DRIVEN-BRIDGE-TARGETS.md)).
*   **Connector (deferred):** Authenticated runtime state with a lifecycle (not a file format); local proxy execution design is recorded in [29](29-CONNECTOR-SYSTEM-DESIGN.md). `internal/connector` was deleted under `D-021` and no connector code ships; the recorded direction is to resurrect and wire that design, subject to re-deciding `D1`.
