# LitePSM Delivery Ledger & Implementation Roadmap

All work is **proposed** until verified code and observable automated test evidence exist. Each stage must satisfy its acceptance gates before subsequent phases begin.

---

## Phase A: Architecture Freeze & Contract Verification (Prerequisite)

Before implementing application code, all normative specifications, data contracts, and verification harnesses must be frozen.

| ID | Task | Owner | Acceptance Evidence | Status |
|---|---|---|---|---|
| **LPSM-A001** | Freeze local process topology (single-writer daemon, Bridge shims, IPC transports on Windows named pipes & Unix domain sockets). | [ARCH/02](ARCH/02-HLD.md), [ARCH/11](ARCH/11-LOCAL-RUNTIME-IPC.md) | Concurrent host connections (Codex, Claude, OpenCode) verified to route through single daemon without state race. | In Progress |
| **LPSM-A002** | Freeze canonical domain identifiers (`SourceId`, `ListingId`, `ComponentId`, `InstallId`, `CapabilityId`) and JSON Schemas (Draft 2020-12). | [ARCH/10](ARCH/10-DOMAIN-MODEL.md), [ARCH/23](ARCH/23-SCHEMAS-EXAMPLES.md) | Validation suite passes for `listing`, `version`, `source`, `install-plan`, `catalog-release`, `errors`. | Planned |
| **LPSM-A003** | Freeze SQLite schema (20 core tables in WAL mode), CAS filesystem layout, and migration/downgrade rules. | [ARCH/12](ARCH/12-STORAGE-TRANSACTIONS-RECOVERY.md), [ARCH/22](ARCH/22-PLATFORM-RELEASE-MIGRATIONS.md) | DDL migrations execute forward transactionally; downgrade from higher schema version fails with explicit error. | Planned |
| **LPSM-A004** | Freeze operation journal state machine (14 discrete states) and deterministic crash-recovery algorithms. | [ARCH/12](ARCH/12-STORAGE-TRANSACTIONS-RECOVERY.md), [ARCH/21](ARCH/21-TESTING-CONFORMANCE.md) | Crash injection at every stage (staging, commit_intent, atomic rename) cleanly rolls back or completes on daemon restart. | Planned |
| **LPSM-A005** | Upgrade `InstallPlan` to cryptographic hash-bound Plan v2 with explicit expiry, preconditions, and `Approval` binding. | [ARCH/06](ARCH/06-API-CONTRACTS.md), [ARCH/13](ARCH/13-RESOLVER-INSTALL-ENGINE.md), [ARCH/15](ARCH/15-POLICY-APPROVALS.md) | Plan tampering or upstream digest drift between plan creation and commit triggers `LPSM-PLAN-STALE`. | Planned |
| **LPSM-A006** | Freeze canonical effect taxonomy (17 actions), `PolicyInput`/`PolicyDecision` engine, and schema-drift grant invalidation. | [ARCH/15](ARCH/15-POLICY-APPROVALS.md) | Invariant deny rules cannot be bypassed; modified provider tool schema revokes existing user capability grant. | Planned |
| **LPSM-A007** | Decouple and freeze interfaces for `SourceAdapter`, `ArtifactFetcher`, `RuntimeAdapter`, and extensible `HostAdapter`. | [ARCH/16](ARCH/16-HOST-ADAPTERS.md), [ARCH/17](ARCH/17-SOURCE-ARTIFACT-RUNTIME-ADAPTERS.md) | Clear isolation: source adapters do not fetch bytes or execute processes; host adapters only touch agent configs. | Planned |
| **LPSM-A008** | Freeze MCP protocol compatibility matrix (stateless 2026-07-28 Streamable HTTP + legacy 2025-11-25) and client capability clamping. | [ARCH/09](ARCH/09-RESEARCH.md), [ARCH/14](ARCH/14-BRIDGE-PROVIDER-MCP.md) | Downstream MCP servers have `roots` and `sampling` disabled by default; header mirroring (`Mcp-Method`) validated. | Planned |
| **LPSM-A009** | Freeze provider supervisor lifecycle, Windows Job Objects / Unix process groups, and capability routing model. | [ARCH/14](ARCH/14-BRIDGE-PROVIDER-MCP.md) | Child provider termination guaranteed on daemon crash or cancellation; routed vs projected capability dispatch tested. | Planned |
| **LPSM-A010** | Freeze `SecretStore` abstraction (Windows Credential Manager / DPAPI, macOS Keychain, Linux Secret Service) and OAuth PKCE loopback. | [ARCH/05](ARCH/05-SECURITY.md), [ARCH/19](ARCH/19-SECRETS-OAUTH.md) | Zero secrets stored in SQLite or config files; OAuth loopback binds `127.0.0.1` with ephemeral port and PKCE. | Planned |
| **LPSM-A011** | Freeze immutable catalog release layout (`/v1/releases/<release-id>/...`), `manifest.json`, and `SourceSnapshot` schema. | [ARCH/03](ARCH/03-CATALOG-SOURCES.md), [ARCH/18](ARCH/18-CATALOG-BUILDER-RELEASE-SEARCH.md) | Client can verify all release files against SHA-256 manifest; failed sync marks snapshot `stale` without cache tearing. | Planned |
| **LPSM-A012** | Create hostile archive fixtures (path traversal, zip bombs, symlink escapes) and crash-injection test harness. | [ARCH/21](ARCH/21-TESTING-CONFORMANCE.md) | Artifact extraction refuses all malicious paths and oversized payloads before disk allocation. | Planned |
| **LPSM-A013** | Create golden host configuration fixtures and documented version ranges for Codex, Claude Code, Grok Build, OpenCode, Cline. | [ARCH/16](ARCH/16-HOST-ADAPTERS.md), [ARCH/21](ARCH/21-TESTING-CONFORMANCE.md) | Golden test fixtures verify safe config merge, comment preservation, collision refusal, and atomic backup/restore. | Planned |
| **LPSM-A014** | Freeze machine-readable error taxonomy (`LPSM-*`) and stable CLI exit codes (0–70). | [ARCH/20](ARCH/20-ERRORS-AUDIT-DOCTOR.md) | All errors from daemon, CLI, or Bridge return structured JSON envelope with redacted details and correlation IDs. | Planned |
| **LPSM-A015** | Produce `ARCH/24-FUNCTION-INVENTORY.md` mapping all 20 packages, function signatures, and unit/integration tests. | [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Every public Go function has unambiguous signature, input/output contract, and designated test file. | Planned |

---

## Phase B: Core Foundations & Storage Layer

| ID | Task | Owner | Acceptance Evidence | Status |
|---|---|---|---|---|
| **LPSM-B001** | Implement `internal/domain` (ID parsing, canonical JSON RFC 8785, digest hashing, schema validation). | [ARCH/10](ARCH/10-DOMAIN-MODEL.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | 100% unit test coverage on pure domain types; zero external network or disk I/O. | Planned |
| **LPSM-B002** | Implement `internal/config` (platform paths for Windows `%LOCALAPPDATA%`, macOS `~/Library/Application Support`, Linux `$XDG_DATA_HOME`). | [ARCH/11](ARCH/11-LOCAL-RUNTIME-IPC.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Platform path resolution and configuration precedence test suite passing across OS targets. | Planned |
| **LPSM-B003** | Implement `internal/state` (SQLite WAL initialization, migration runner, 20 tables DDL). | [ARCH/12](ARCH/12-STORAGE-TRANSACTIONS-RECOVERY.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Database migration suite verifies clean up/down and foreign key constraint enforcement. | Planned |
| **LPSM-B004** | Implement `internal/ipc` (Named pipe server/client on Windows, Unix domain sockets on Linux/macOS, JSON-RPC 2.0 framing). | [ARCH/11](ARCH/11-LOCAL-RUNTIME-IPC.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Benchmark shows low-latency local RPC; permissions validated to reject non-owner processes. | Planned |
| **LPSM-B005** | Implement `internal/state` operation journal and recovery worker. | [ARCH/12](ARCH/12-STORAGE-TRANSACTIONS-RECOVERY.md), [ARCH/21](ARCH/21-TESTING-CONFORMANCE.md) | Fault-injection harness confirms incomplete operations cleanly recover on startup. | Planned |

---

## Phase C: Static Catalog & Discovery Plane

| ID | Task | Owner | Acceptance Evidence | Status |
|---|---|---|---|---|
| **LPSM-C001** | Implement `internal/source` adapters for Official MCP Registry and Agent Skills. | [ARCH/03](ARCH/03-CATALOG-SOURCES.md), [ARCH/17](ARCH/17-SOURCE-ARTIFACT-RUNTIME-ADAPTERS.md) | Pinned metadata fixtures map to valid `Listing` records with source digests and timestamps. | Planned |
| **LPSM-C002** | Implement `internal/catalogbuild` (deterministic release compiler, manifest generator, shard partitioner). | [ARCH/18](ARCH/18-CATALOG-BUILDER-RELEASE-SEARCH.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Two successive builds from identical source fixtures yield byte-for-byte identical output. | Planned |
| **LPSM-C003** | Implement `internal/catalog` client (release fetcher, manifest integrity checker, ETag cache, lexical search index). | [ARCH/06](ARCH/06-API-CONTRACTS.md), [ARCH/18](ARCH/18-CATALOG-BUILDER-RELEASE-SEARCH.md) | Offline search functional from local cache; updates detected via `/v1/current.json` sequence bump. | Planned |
| **LPSM-C004** | Configure Cloudflare Pages deployment pipeline with strict dist allowlist (zero private source leaks). | [ARCH/02](ARCH/02-HLD.md), [ARCH/18](ARCH/18-CATALOG-BUILDER-RELEASE-SEARCH.md) | CI audit verifies only generated release JSON and static HTML/CSS/JS are published. | Planned |

---

## Phase D: Safe Artifact Extraction & Skill Management

| ID | Task | Owner | Acceptance Evidence | Status |
|---|---|---|---|---|
| **LPSM-D001** | Implement `internal/artifact` fetcher and safe archive extractor with hard limit enforcement. | [ARCH/05](ARCH/05-SECURITY.md), [ARCH/13](ARCH/13-RESOLVER-INSTALL-ENGINE.md) | Rejection of directory traversal (`../`), zip bombs, absolute paths, and unauthorized symlinks. | Planned |
| **LPSM-D002** | Implement `internal/resolver` (pure DFS dependency resolution with cycle detection). | [ARCH/13](ARCH/13-RESOLVER-INSTALL-ENGINE.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Topological order generated without side effects; dependency cycles return clean error. | Planned |
| **LPSM-D003** | Implement `internal/install` atomic staging, CAS immutable tree placement, and SQLite commit. | [ARCH/12](ARCH/12-STORAGE-TRANSACTIONS-RECOVERY.md), [ARCH/13](ARCH/13-RESOLVER-INSTALL-ENGINE.md) | Staged install moves atomically; aborted install leaves zero orphaned files in live directories. | Planned |
| **LPSM-D004** | Implement `internal/skills` loader (progressive disclosure: metadata first, body/resources on demand). | [ARCH/04](ARCH/04-CLIENT-INSTALL.md), [ARCH/14](ARCH/14-BRIDGE-PROVIDER-MCP.md) | `load_skill` returns untrusted text instructions; zero script execution during loading or install. | Planned |

---

## Phase E: Daemon Supervision, Bridge Shim & Host Adapters

| ID | Task | Owner | Acceptance Evidence | Status |
|---|---|---|---|---|
| **LPSM-E001** | Implement `internal/provider` process supervisor (Windows Job Objects / Unix process groups, stdio piping). | [ARCH/14](ARCH/14-BRIDGE-PROVIDER-MCP.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Child process cleanup verified on SIGKILL/crash; stderr captured to bounded rotating ring buffer. | Planned |
| **LPSM-E002** | Implement `internal/policy` engine and `internal/approval` service. | [ARCH/15](ARCH/15-POLICY-APPROVALS.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Effectful actions require valid approval; model cannot self-grant tool permissions. | Planned |
| **LPSM-E003** | Implement `internal/bridge` stdio MCP server shim exposing the 12 core Bridge tools. | [ARCH/06](ARCH/06-API-CONTRACTS.md), [ARCH/14](ARCH/14-BRIDGE-PROVIDER-MCP.md) | Tested with official MCP inspector; routed capability calls correctly validated against provider schemas. | Planned |
| **LPSM-E004** | Implement extensible `internal/host` adapter architecture with interactive wizard and auto-detection. | [ARCH/16](ARCH/16-HOST-ADAPTERS.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Auto-locates config on Windows/Mac/Linux; prompts clean fallback/path prompt if not found; safe merge. | Planned |
| **LPSM-E005** | Build and test initial priority host adapters for **Cline**, **Pi Agent**, and **Grok Build** (plus Claude Code, Codex, OpenCode). | [ARCH/16](ARCH/16-HOST-ADAPTERS.md), [AGENTS.md](AGENTS.md), [TEST.md](TEST.md) | Idempotent registration of pinned binary; pre-edit backups created; pre-existing external tools detected; automated test suite in TEST.md passes. | Planned |
| **LPSM-E006** | Implement dynamic runtime adapter fetching (`/v1/adapters.json`). | [ARCH/16](ARCH/16-HOST-ADAPTERS.md), [AGENTS.md](AGENTS.md) | Running `litepsm` fetches remote ready adapter list at runtime; falls back gracefully to compiled-in list if offline. | Planned |

---

## Phase F: MCP Protocol Profiles, Secrets & OAuth

| ID | Task | Owner | Acceptance Evidence | Status |
|---|---|---|---|---|
| **LPSM-F001** | Implement `internal/mcpclient` supporting stateless MCP 2026-07-28 (Streamable HTTP) and legacy 2025-11-25. | [ARCH/09](ARCH/09-RESEARCH.md), [ARCH/14](ARCH/14-BRIDGE-PROVIDER-MCP.md) | Dual-protocol conformance suite passes against mock and reference servers. | Planned |
| **LPSM-F002** | Implement capability probing, schema fingerprinting, and drift detection. | [ARCH/14](ARCH/14-BRIDGE-PROVIDER-MCP.md), [ARCH/15](ARCH/15-POLICY-APPROVALS.md) | Altered downstream tool schema automatically invalidates prior grants and alerts user. | Planned |
| **LPSM-F003** | Implement `internal/secrets` OS credential store wrapper (WinCred/DPAPI, Keychain, Secret Service). | [ARCH/05](ARCH/05-SECURITY.md), [ARCH/19](ARCH/19-SECRETS-OAUTH.md) | Can store, retrieve, and delete tokens; synthetic canaries confirm zero secrets leak into logs/DB. | Planned |
| **LPSM-F004** | Implement `internal/auth` OAuth 2.0 PKCE loopback broker. | [ARCH/19](ARCH/19-SECRETS-OAUTH.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Loopback browser flow completes token exchange; stores credentials directly to OS vault. | Planned |

---

## Phase G: Marketplaces Federation, In-Agent `/litepsm` & Web Frontend

| ID | Task | Owner | Acceptance Evidence | Status |
|---|---|---|---|---|
| **LPSM-G001** | Implement source adapters for Claude, Codex, Grok plugin marketplace formats (strictly rejecting command sources). | [ARCH/03](ARCH/03-CATALOG-SOURCES.md), [ARCH/17](ARCH/17-SOURCE-ARTIFACT-RUNTIME-ADAPTERS.md) | Valid public manifests parsed; unsupported/command components cleanly reported as non-executable. | Planned |
| **LPSM-G002** | Implement interactive CLI TUI wizard (`litepsm` interactive runner, dropdown agent selector, update check). | [ARCH/04](ARCH/04-CLIENT-INSTALL.md), [ARCH/16](ARCH/16-HOST-ADAPTERS.md) | User can run `litepsm`, select agent from dropdown, view diff of available updates, and approve. | Planned |
| **LPSM-G003** | Implement in-agent `/litepsm` command with 4-tab panel (MCP Servers, Agent Skills, Plugins, Installed with green lights & external tool scan). | [ARCH/04](ARCH/04-CLIENT-INSTALL.md), [ARCH/16](ARCH/16-HOST-ADAPTERS.md), [AGENTS.md](AGENTS.md) | Agent triggers `/litepsm`; displays tabs; Installed tab shows all native & LitePSM tools with status indicators. | Planned |
| **LPSM-G004** | Build static Web Marketplace frontend inspired by mcpmarket.com. | [ARCH/25](ARCH/25-WEB-FRONTEND-UI.md) | Next.js/React static export deployed to Cloudflare Pages; omni-search, category rail, cards, and detail drawer functional. | Planned |
| **LPSM-G005** | Implement `internal/doctor` diagnostic engine and `--repair` plan generator. | [ARCH/20](ARCH/20-ERRORS-AUDIT-DOCTOR.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Detects DB corruption, dangling files, host config drift, missing secrets, and outputs corrective plan. | Planned |

---

## Phase H: Release Engineering, Conformance & Packaging

| ID | Task | Owner | Acceptance Evidence | Status |
|---|---|---|---|---|
| **LPSM-H001** | Setup cross-platform Go build matrix (`windows/amd64`, `windows/arm64`, `linux/amd64`, `darwin/arm64`). | [ARCH/22](ARCH/22-PLATFORM-RELEASE-MIGRATIONS.md) | Binaries compile cleanly with CGO-free or cross-compiled SQLite support. | Planned |
| **LPSM-H002** | Create npm wrapper package (`litepsm` / `@litepsm/cli`) with platform-specific native binary downloaders. | [ARCH/04](ARCH/04-CLIENT-INSTALL.md), [ARCH/22](ARCH/22-PLATFORM-RELEASE-MIGRATIONS.md) | `npm install -g litepsm` or `npx litepsm` downloads and verifies binary checksum before launch. | Planned |
| **LPSM-H003** | Complete end-to-end conformance, secret leak canary, and crash-recovery test suite. | [ARCH/21](ARCH/21-TESTING-CONFORMANCE.md) | Full CI pipeline green across Windows, Ubuntu, and macOS runners. | Planned |

---

**Next Safe Action:** Execute Phase A architecture documents: update `ARCH/00`–`ARCH/09` and author `ARCH/10` through `ARCH/24`.
