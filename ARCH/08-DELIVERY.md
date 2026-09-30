# Delivery Plan & Quality Gates

## 1. Staged Delivery Methodology

To ensure stability and prevent architectural regressions, LitePSM follows an 8-phase delivery sequence (Phase A through Phase H). Each phase begins in a **Proposed** state and advances to **Verified** and **Accepted** only when automated quality gates and test suites pass.

```text
[Phase A] Architecture Freeze & Schemas
    │
    ▼
[Phase B] Domain, SQLite Storage & Local IPC
    │
    ▼
[Phase C] Source Ingestion & Static Catalog Builder
    │
    ▼
[Phase D] Safe Extraction, DFS Resolver & Skill Store
    │
    ▼
[Phase E] Process Supervision, Bridge Shim & Host Adapters
    │
    ▼
[Phase F] MCP Protocols (Streamable HTTP), Secrets & OAuth
    │
    ▼
[Phase G] Marketplace Federation, In-Agent /litepsm & CLI TUI
    │
    ▼
[Phase H] Cross-Platform Build, Conformance & Packaging
```

---

## 2. Phase Breakdown and Acceptance Gates

### Phase A: Architecture Freeze & Schemas (Prerequisite)
*   **Deliverables:**
    *   Author normative LLD specifications `ARCH/10` through `ARCH/24`.
    *   Draft 2020-12 JSON Schemas for `listing`, `version`, `source`, `install-plan`, `catalog-release`, `errors`.
    *   Publish hostile archive test fixtures and golden host configuration files.
*   **Acceptance Gate:** All schema validation suites pass; no unresolved architectural questions remain.

### Phase B: Foundation & Storage Layer
*   **Deliverables:**
    *   `internal/domain`: Pure types, canonical JSON (RFC 8785), SHA-256 hashing.
    *   `internal/config`: Platform-standard paths across Windows, macOS, and Linux.
    *   `internal/state`: SQLite 3 initialization in WAL mode, migration runner, 22 relational tables DDL (including `auth_profiles` and `operation_trees`).
    *   `internal/ipc`: Windows Named Pipe and Unix domain socket JSON-RPC 2.0 transport.
*   **Acceptance Gate:** 100% unit test coverage on pure domain types; database migration tests pass forward and backward; IPC throughput benchmark demonstrates sub-millisecond local latency.

### Phase C: Static Catalog & Discovery Plane
*   **Deliverables:**
    *   `internal/source`: Source adapters for Official MCP Registry and Agent Skills.
    *   `internal/catalogbuild`: Deterministic CI compiler, manifest generator, and shard partitioner.
    *   `internal/catalog`: Client release fetcher, ETag cache, and deterministic search index.
    *   Cloudflare Pages deployment workflow with strict dist allowlist.
*   **Acceptance Gate:** Two successive CI builds from identical source fixtures generate byte-for-byte identical output; public deployment reveals zero private repository files.

### Phase D: Safe Artifact Extraction & Skill Management
*   **Deliverables:**
    *   `internal/artifact`: Archive fetcher and extractor enforcing hard limits (256 MiB download, 1 GiB extracted, case-fold checks, canonical tree digests, and path traversal rejection).
    *   `internal/resolver`: Pure dependency resolver with constraint intersection and cycle detection.
    *   `internal/install`: Two-phase atomic filesystem staging, safe CAS rollback, and SQLite commit engine.
    *   `internal/skills`: Progressive disclosure skill body and resource loader.
*   **Acceptance Gate:** Extraction harness rejects all hostile zip-slip, zip-bomb, and symlink escape fixtures; aborted installations leave zero orphaned files.

### Phase E: Process Supervision, Bridge Shim & Host Adapters
*   **Deliverables:**
    *   `internal/provider`: Process supervisor with Windows Job Objects and Unix supervisor watchdog control pipe (`PR_SET_PDEATHSIG` on Linux).
    *   `internal/policy`: 17-action effect taxonomy and approval engine with effect provenance.
    *   `internal/bridge`: Stdio MCP shim exposing the 12 core Bridge tools.
    *   `internal/host`: Extensible adapter framework with automated config path discovery for Codex, Claude Code, Grok Build, OpenCode, Cline, and Pi Agent.
*   **Acceptance Gate:** Terminating the daemon cleanly terminates all child provider processes; host adapters successfully merge Bridge entries into golden config fixtures without altering unrelated keys.

### Phase F: MCP Protocol Profiles, Secrets & OAuth
*   **Deliverables:**
    *   `internal/mcpclient`: Dual-protocol client supporting stateless 2026-07-28 (Streamable HTTP) and legacy 2025-11-25.
    *   Capability schema fingerprinting and automatic drift invalidation.
    *   `internal/secrets`: Native OS credential store backends (WinCred/DPAPI, Keychain, Secret Service).
    *   `internal/auth`: OAuth 2.0 PKCE loopback listener on `127.0.0.1`.
*   **Acceptance Gate:** Dual-protocol conformance suite passes against mock MCP servers; synthetic canary tokens confirm zero secret leaks in logs, database dumps, or error responses.

### Phase G: Marketplace Federation, In-Agent `/litepsm` & CLI TUI
*   **Deliverables:**
    *   Federated source adapters for Claude Code, Codex, and Grok Build marketplaces (rejecting command sources).
    *   Interactive CLI TUI wizard (`litepsm` runner with dropdown agent selector and passive update notices).
    *   In-agent `/litepsm` command and progressive tool discovery workflow.
    *   `internal/doctor`: Diagnostic checks and `--repair` plan generator.
*   **Acceptance Gate:** Running `litepsm` in terminal allows seamless agent selection and configuration; agents can invoke `/litepsm` to search and propose verified installations.

### Phase H: Cross-Platform Build, Conformance & Packaging
*   **Deliverables:**
    *   Go cross-compilation pipeline (`windows/amd64`, `windows/arm64`, `linux/amd64`, `darwin/arm64`).
    *   npm distribution package (`litepsm` / `@litepsm/cli`) with native binary bootstrapping.
    *   Comprehensive end-to-end integration and crash-injection test suite.
*   **Acceptance Gate:** Full CI test matrix green across Windows, Ubuntu, and macOS runners; npm package boots correctly via `npx litepsm`.

---

## 3. Explicitly Deferred Features (Non-Goals for v1)

The following capabilities are deliberately excluded from the initial release to maintain architectural focus and security:
*   Hosted cloud execution or proxying of third-party MCP servers.
*   Cloud-hosted credential brokering or SaaS token management.
*   Dynamic, third-party executable adapter scripting.
*   General SAT dependency solvers with backtracking.
*   Native connector runtimes for proprietary SaaS APIs.
*   User telemetry, tracking, or popularity ranking metrics.
*   Silent automatic extension updates by default.
*   Full OS kernel sandboxing claims without platform sandbox integration.
