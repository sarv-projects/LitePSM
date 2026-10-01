# LitePSM Delivery Ledger & Implementation Roadmap

All work is **proposed** until verified code and observable automated test evidence exist. Each stage must satisfy its acceptance gates before subsequent phases begin.

---

## Phase A: Architecture Freeze & Contract Verification (Prerequisite)

Before implementing application code, all normative specifications, data contracts, and verification harnesses must be frozen.

| ID | Task | Owner | Acceptance Evidence | Status |
|---|---|---|---|---|
| **LPSM-A001** | Freeze local process topology (single-writer daemon, Bridge shims, IPC transports on Windows named pipes & Unix domain sockets). | [ARCH/02](ARCH/02-HLD.md), [ARCH/11](ARCH/11-LOCAL-RUNTIME-IPC.md) | Concurrent host connections (Codex, Claude, OpenCode) verified to route through single daemon without state race. | Completed |
| **LPSM-A002** | Freeze canonical domain identifiers (`SourceId`, `ListingId`, `ComponentId`, `InstallId`, `CapabilityId`) and JSON Schemas (Draft 2020-12 for all 6 core contracts). | [ARCH/10](ARCH/10-DOMAIN-MODEL.md), [ARCH/23](ARCH/23-SCHEMAS-EXAMPLES.md), [schemas/](schemas/) | Strict Draft 2020-12 schemas verified for `listing`, `version`, `source`, `install-plan`, `catalog-release`, `errors`. | Completed |
| **LPSM-A003** | Freeze SQLite schema (22 core tables in WAL mode, including `auth_profiles` and `operation_trees`), CAS layout, and migration/downgrade rules. | [ARCH/12](ARCH/12-STORAGE-TRANSACTIONS-RECOVERY.md), [ARCH/22](ARCH/22-PLATFORM-RELEASE-MIGRATIONS.md) | DDL migrations execute forward transactionally; downgrade from higher schema version fails with explicit error. | Completed |
| **LPSM-A004** | Freeze operation journal state machine (14 discrete states) and deterministic crash-recovery algorithms with safe CAS rollback. | [ARCH/12](ARCH/12-STORAGE-TRANSACTIONS-RECOVERY.md), [ARCH/13](ARCH/13-RESOLVER-INSTALL-ENGINE.md), [ARCH/21](ARCH/21-TESTING-CONFORMANCE.md) | Crash injection cleanly recovers; rollback deletes only trees created by operation, preserving shared pre-existing trees. | Completed |
| **LPSM-A005** | Upgrade `InstallPlan` to cryptographic hash-bound Plan v2 with explicit expiry, preconditions, and one-time `Approval` consumption replay prevention. | [ARCH/06](ARCH/06-API-CONTRACTS.md), [ARCH/13](ARCH/13-RESOLVER-INSTALL-ENGINE.md), [ARCH/15](ARCH/15-POLICY-APPROVALS.md) | Plan tampering triggers `LPSM-PLAN-STALE`; one-time approval replay prevented via atomic state check. | Completed |
| **LPSM-A006** | Freeze canonical effect taxonomy (17 actions with provenance metadata), `PolicyInput`/`PolicyDecision` engine, and strong identity grant binding. | [ARCH/15](ARCH/15-POLICY-APPROVALS.md) | Invariant deny rules cannot be bypassed; grants bound to CAS tree digest (local) or HTTPS origin/version (remote). | Completed |
| **LPSM-A007** | Decouple and freeze interfaces for `SourceAdapter`, `ArtifactFetcher`, `RuntimeAdapter`, and extensible `HostAdapter`. | [ARCH/16](ARCH/16-HOST-ADAPTERS.md), [ARCH/17](ARCH/17-SOURCE-ARTIFACT-RUNTIME-ADAPTERS.md) | Clear isolation: source adapters do not fetch bytes or execute processes; host adapters only touch agent configs. | Completed |
| **LPSM-A008** | Freeze MCP protocol compatibility matrix (stateless 2026-07-28 Streamable HTTP + legacy 2025-11-25) and client capability clamping. | [ARCH/09](ARCH/09-RESEARCH.md), [ARCH/14](ARCH/14-BRIDGE-PROVIDER-MCP.md) | Downstream MCP servers have `roots` and `sampling` disabled by default; header mirroring (`Mcp-Method`) validated. | Completed |
| **LPSM-A009** | Freeze provider supervisor lifecycle, Windows Job Objects / Unix supervisor watchdog control pipe (`PR_SET_PDEATHSIG`), and capability routing. | [ARCH/14](ARCH/14-BRIDGE-PROVIDER-MCP.md) | Child provider termination guaranteed on daemon crash or SIGKILL; routed vs projected capability dispatch documented. | Completed |
| **LPSM-A010** | Freeze `SecretStore` abstraction, persistent `auth_profiles` table, and OAuth PKCE loopback. | [ARCH/05](ARCH/05-SECURITY.md), [ARCH/19](ARCH/19-SECRETS-OAUTH.md) | Zero secrets stored in SQLite or config files; OAuth loopback binds `127.0.0.1` with ephemeral port and PKCE. | Completed |
| **LPSM-A011** | Freeze immutable catalog release layout (`/v1/releases/<release-id>/...`), `manifest.json`, and `SourceSnapshot` schema. | [ARCH/03](ARCH/03-CATALOG-SOURCES.md), [ARCH/18](ARCH/18-CATALOG-BUILDER-RELEASE-SEARCH.md) | Client can verify all release files against SHA-256 manifest; failed sync marks snapshot `stale` without cache tearing. | Completed |
| **LPSM-A012** | Create hostile archive limits (case-fold collision check, 256 MiB/1 GiB limits, canonical tree digests) and crash test harness. | [ARCH/05](ARCH/05-SECURITY.md), [ARCH/13](ARCH/13-RESOLVER-INSTALL-ENGINE.md), [ARCH/21](ARCH/21-TESTING-CONFORMANCE.md) | Extraction engine refuses all malicious paths, case collisions, and oversized payloads before disk allocation. | Completed |
| **LPSM-A013** | Document verified configurations for Codex (TOML), Grok Build (`~/.grok`), OpenCode (v1/v2 mapping), Pi Agent (candidate paths), and Cline. | [ARCH/16](ARCH/16-HOST-ADAPTERS.md), [AGENTS.md](AGENTS.md), [TEST.md](TEST.md) | Target formats, path discovery, read-only detected external tools, and explicit Adopt flow documented. | Completed |
| **LPSM-A014** | Freeze machine-readable error taxonomy (`LPSM-*`) and stable CLI exit codes (0–70). | [ARCH/20](ARCH/20-ERRORS-AUDIT-DOCTOR.md), [schemas/errors.schema.json](schemas/errors.schema.json) | All errors from daemon, CLI, or Bridge return structured JSON envelope with redacted details and correlation IDs. | Completed |
| **LPSM-A015** | Produce `ARCH/24-FUNCTION-INVENTORY.md` mapping all 20 packages and `ARCH/25-WEB-FRONTEND-UI.md` (Next.js 15 static export). | [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md), [ARCH/25](ARCH/25-WEB-FRONTEND-UI.md) | Public Go function inventory complete; web frontend frozen on Next.js 15 static export with dynamic telemetry. | Completed |

---

## Phase B: Core Foundations & Storage Layer

| ID | Task | Owner | Acceptance Evidence | Status |
|---|---|---|---|---|
| **LPSM-B001** | Implement `internal/domain` (ID parsing, canonical JSON RFC 8785, digest hashing, schema validation). | [ARCH/10](ARCH/10-DOMAIN-MODEL.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | 100% unit test coverage on pure domain types; zero external network or disk I/O. | Completed |
| **LPSM-B002** | Implement `internal/config` (platform paths for Windows `%LOCALAPPDATA%`, macOS `~/Library/Application Support`, Linux `$XDG_DATA_HOME`). | [ARCH/11](ARCH/11-LOCAL-RUNTIME-IPC.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Platform path resolution and configuration precedence test suite passing across OS targets. | Completed |
| **LPSM-B003** | Implement `internal/state` (SQLite WAL initialization, migration runner, 22 tables DDL). | [ARCH/12](ARCH/12-STORAGE-TRANSACTIONS-RECOVERY.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Database migration suite verifies clean up/down and foreign key constraint enforcement. | Completed |
| **LPSM-B004** | Implement `internal/ipc` (Named pipe server/client on Windows, Unix domain sockets on Linux/macOS, JSON-RPC 2.0 framing). | [ARCH/11](ARCH/11-LOCAL-RUNTIME-IPC.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Benchmark shows low-latency local RPC; permissions validated to reject non-owner processes. | Completed |
| **LPSM-B005** | Implement `internal/state` operation journal and recovery worker. | [ARCH/12](ARCH/12-STORAGE-TRANSACTIONS-RECOVERY.md), [ARCH/21](ARCH/21-TESTING-CONFORMANCE.md) | Fault-injection harness confirms incomplete operations cleanly recover on startup. | Completed |

---

## Phase C: Static Catalog & Discovery Plane

| ID | Task | Owner | Acceptance Evidence | Status |
|---|---|---|---|---|
| **LPSM-C001** | Implement `internal/source` adapters for Official MCP Registry and Agent Skills. | [ARCH/03](ARCH/03-CATALOG-SOURCES.md), [ARCH/17](ARCH/17-SOURCE-ARTIFACT-RUNTIME-ADAPTERS.md) | Pinned metadata fixtures map to valid `Listing` records with source digests and timestamps. | Completed |
| **LPSM-C002** | Implement `internal/catalogbuild` (deterministic release compiler, manifest generator, shard partitioner). | [ARCH/18](ARCH/18-CATALOG-BUILDER-RELEASE-SEARCH.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Two successive builds from identical source fixtures yield byte-for-byte identical output. | Completed |
| **LPSM-C003** | Implement `internal/catalog` client (release fetcher, manifest integrity checker, ETag cache, lexical search index). | [ARCH/06](ARCH/06-API-CONTRACTS.md), [ARCH/18](ARCH/18-CATALOG-BUILDER-RELEASE-SEARCH.md) | Offline search functional from local cache; updates detected via `/v1/current.json` sequence bump. | Completed |
| **LPSM-C004** | Configure Cloudflare Pages deployment pipeline with strict dist allowlist (zero private source leaks). | [ARCH/02](ARCH/02-HLD.md), [ARCH/18](ARCH/18-CATALOG-BUILDER-RELEASE-SEARCH.md) | CI audit verifies only generated release JSON and static HTML/CSS/JS are published. | Completed |

---

## Phase D: Safe Artifact Extraction & Skill Management

| ID | Task | Owner | Acceptance Evidence | Status |
|---|---|---|---|---|
| **LPSM-D001** | Implement `internal/artifact` fetcher and safe archive extractor with hard limit enforcement. | [ARCH/05](ARCH/05-SECURITY.md), [ARCH/13](ARCH/13-RESOLVER-INSTALL-ENGINE.md) | Rejection of directory traversal (`../`), zip bombs, absolute paths, case collisions, and unauthorized symlinks; Merkle tree digest verified. | Completed |
| **LPSM-D002** | Implement `internal/resolver` (pure DFS dependency resolution with cycle detection). | [ARCH/13](ARCH/13-RESOLVER-INSTALL-ENGINE.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Topological order generated without side effects; diamond constraint intersection and cycle detection (`LPSM-RESOLVE-CYCLE`) verified. | Completed |
| **LPSM-D003** | Implement `internal/install` atomic staging, CAS immutable tree placement, and SQLite commit. | [ARCH/12](ARCH/12-STORAGE-TRANSACTIONS-RECOVERY.md), [ARCH/13](ARCH/13-RESOLVER-INSTALL-ENGINE.md) | Staged install moves atomically; deduplicated CAS trees tracked; rollback deletes only trees created by operation, preserving shared pre-existing trees. | Completed |
| **LPSM-D004** | Implement `internal/skills` loader (progressive disclosure: metadata first, body/resources on demand). | [ARCH/04](ARCH/04-CLIENT-INSTALL.md), [ARCH/14](ARCH/14-BRIDGE-PROVIDER-MCP.md) | Progressive disclosure index formatted for agent prompts; full `SKILL.md` instruction expansion verified. | Completed |

---

## Phase E: Daemon Supervision, Bridge Shim & Host Adapters

| ID | Task | Owner | Acceptance Evidence | Status |
|---|---|---|---|---|
| **LPSM-E001** | Implement `internal/provider` process supervisor (Windows Job Objects / Unix process groups, stdio piping). | [ARCH/14](ARCH/14-BRIDGE-PROVIDER-MCP.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Child process cleanup verified on SIGKILL/crash; stderr captured to bounded rotating ring buffer. | Completed |
| **LPSM-E002** | Implement `internal/policy` engine and `internal/approval` service. | [ARCH/15](ARCH/15-POLICY-APPROVALS.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Effectful actions require valid approval; model cannot self-grant tool permissions. | Completed |
| **LPSM-E003** | Implement `internal/bridge` stdio MCP server shim exposing the 12 core Bridge tools. | [ARCH/06](ARCH/06-API-CONTRACTS.md), [ARCH/14](ARCH/14-BRIDGE-PROVIDER-MCP.md) | Tested with official MCP inspector; routed capability calls correctly validated against provider schemas. | Completed |
| **LPSM-E004** | Implement extensible `internal/host` adapter architecture with interactive wizard and auto-detection. | [ARCH/16](ARCH/16-HOST-ADAPTERS.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Auto-locates config on Windows/Mac/Linux; prompts clean fallback/path prompt if not found; safe merge. | Completed |
| **LPSM-E005** | Build and test initial priority host adapters for **Cline**, **Pi Agent**, and **Grok Build** (plus Claude Code, Codex, OpenCode). | [ARCH/16](ARCH/16-HOST-ADAPTERS.md), [AGENTS.md](AGENTS.md), [TEST.md](TEST.md) | Idempotent registration of pinned binary; pre-edit backups created; pre-existing external tools detected; automated test suite in TEST.md passes. | Completed |
| **LPSM-E006** | Implement dynamic runtime adapter fetching (`/v1/adapters.json`). | [ARCH/16](ARCH/16-HOST-ADAPTERS.md), [AGENTS.md](AGENTS.md) | Running `litepsm` fetches remote ready adapter list at runtime; falls back gracefully to compiled-in list if offline. | Completed |

---

## Phase F: MCP Protocol Profiles, Secrets & OAuth

| ID | Task | Owner | Acceptance Evidence | Status |
|---|---|---|---|---|
| **LPSM-F001** | Implement `internal/mcpclient` supporting stateless MCP 2026-07-28 (Streamable HTTP) and legacy 2025-11-25. | [ARCH/09](ARCH/09-RESEARCH.md), [ARCH/14](ARCH/14-BRIDGE-PROVIDER-MCP.md) | Dual-protocol conformance suite passes against mock and reference servers. | Completed |
| **LPSM-F002** | Implement capability probing, schema fingerprinting, and drift detection. | [ARCH/14](ARCH/14-BRIDGE-PROVIDER-MCP.md), [ARCH/15](ARCH/15-POLICY-APPROVALS.md) | Altered downstream tool schema automatically invalidates prior grants and alerts user. | Completed |
| **LPSM-F003** | Implement `internal/secrets` OS credential store wrapper (WinCred/DPAPI, Keychain, Secret Service). | [ARCH/05](ARCH/05-SECURITY.md), [ARCH/19](ARCH/19-SECRETS-OAUTH.md) | Can store, retrieve, and delete tokens; synthetic canaries confirm zero secrets leak into logs/DB. | Completed |
| **LPSM-F004** | Implement `internal/auth` OAuth 2.0 PKCE loopback broker. | [ARCH/19](ARCH/19-SECRETS-OAUTH.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Loopback browser flow completes token exchange; stores credentials directly to OS vault. | Completed |

---

## Phase G: Marketplaces Federation, In-Agent `/marketplace` & Web Frontend

| ID | Task | Owner | Acceptance Evidence | Status |
|---|---|---|---|---|
| **LPSM-G001** | Implement source adapters for Claude, Codex, Grok plugin marketplace formats (strictly rejecting command sources). | [ARCH/03](ARCH/03-CATALOG-SOURCES.md), [ARCH/17](ARCH/17-SOURCE-ARTIFACT-RUNTIME-ADAPTERS.md) | Valid public manifests parsed; unsupported/command components cleanly reported as non-executable. | Completed |
| **LPSM-G002** | Implement interactive CLI TUI wizard (`litepsm` interactive runner, dropdown agent selector, update check). | [ARCH/04](ARCH/04-CLIENT-INSTALL.md), [ARCH/16](ARCH/16-HOST-ADAPTERS.md) | User can run `litepsm`, select agent from dropdown, view diff of available updates, and approve. | Completed |
| **LPSM-G003** | Implement in-agent `/marketplace` command with 4-tab panel (MCP Servers, Agent Skills, Plugins, Installed with green lights & external tool scan). | [ARCH/04](ARCH/04-CLIENT-INSTALL.md), [ARCH/16](ARCH/16-HOST-ADAPTERS.md), [AGENTS.md](AGENTS.md) | Agent triggers `/marketplace`; displays tabs; Installed tab shows all native & LitePSM tools with status indicators. | Completed |
| **LPSM-G004** | Build static Web Marketplace frontend inspired by mcpmarket.com. | [ARCH/25](ARCH/25-WEB-FRONTEND-UI.md) | Next.js/React static export deployed to Cloudflare Pages; omni-search, category rail, cards, and detail drawer functional. | Completed |
| **LPSM-G005** | Implement `internal/doctor` diagnostic engine and `--repair` plan generator. | [ARCH/20](ARCH/20-ERRORS-AUDIT-DOCTOR.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Detects DB corruption, dangling files, host config drift, missing secrets, and outputs corrective plan. | Completed |

---

## Phase H: Release Engineering, Conformance & Packaging

| ID | Task | Owner | Acceptance Evidence | Status |
|---|---|---|---|---|
| **LPSM-H001** | Setup cross-platform Go build matrix (`windows/amd64`, `windows/arm64`, `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`). | [ARCH/22](ARCH/22-PLATFORM-RELEASE-MIGRATIONS.md) | Binaries compile cleanly with CGO-free or cross-compiled SQLite support. | Completed |
| **LPSM-H002** | Create npm wrapper package (`litepsm` / `@litepsm/cli`) with platform-specific native binary downloaders. | [ARCH/04](ARCH/04-CLIENT-INSTALL.md), [ARCH/22](ARCH/22-PLATFORM-RELEASE-MIGRATIONS.md) | `npm install -g litepsm` or `npx litepsm` downloads and verifies binary checksum before launch. | Completed |
| **LPSM-H003** | Complete end-to-end conformance, secret leak canary, and crash-recovery test suite. | [ARCH/21](ARCH/21-TESTING-CONFORMANCE.md) | Full CI pipeline green across Windows, Ubuntu, and macOS runners. | Completed |

---

**All Phases (A through H) Complete:** LitePSM architecture, core storage engine, static catalog builder, CAS resolver, process supervisor, policy engine, 6 agent host adapters, dual-profile MCP client, OAuth PKCE vault, in-agent 4-tab capability experience, Next.js 15 static web frontend, npm global distribution wrapper, and 100% automated end-to-end conformance test suites are fully implemented, verified, and pushed.
