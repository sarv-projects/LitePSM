# LiteSPM Delivery Ledger & Implementation Roadmap

All work is **proposed** until verified code and observable automated test evidence exist. Each stage must satisfy its acceptance gates before subsequent phases begin.

> **Status vocabulary & authority.** [STATUS.md](STATUS.md) is the single source of truth for what
> each subsystem actually does. Where this ledger and STATUS.md disagree, **STATUS.md wins**. The
> `Status` column below uses the six evidence states defined by
> [ARCH/31 §2](ARCH/31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#2-evidence-vocabulary-binding-repo-wide)
> and fixed by STATUS.md: `DESIGNED` → `IMPLEMENTED` → `WIRED` → `TESTED` → `VERIFIED` → `SHIPPED`.
> They are distinct and are not collapsed.
>
> The blanket `Completed` marker has been **retired**. No row in this ledger is `SHIPPED` (nothing
> here is published through a release channel by virtue of this row), and `VERIFIED` appears only
> where an independent falsifier recorded the result in
> [REMEDIATION-PLAN.md](REMEDIATION-PLAN.md). Each cell states the **highest honest state**, and
> where the Acceptance Evidence text describes something the repository does not actually do, the
> gap is written into that cell rather than dropped silently.
>
> **Self-flagged overclaims corrected in this pass.** The ledger previously marked these rows
> `Completed` with acceptance text the tree does not support:
>
> | Row | What the ledger claimed | What is true |
> |---|---|---|
> | `LPSM-C002`, `LPSM-C004` | Release compiler + Cloudflare pipeline configured with a CI audit | `internal/catalogbuild.CompileRelease` has **no non-test caller**; `scripts/build_full_catalog.py` (Python) is the producer of the deployed catalog, contradicting locked decision D4. `scripts/deploy-pages.sh` is manual-only and is not referenced by any workflow. `ARCH/18`'s `index.json`/`shards/`/`items/` outputs are specified but never emitted (its §4 `_headers` cache policy **is** produced, by the deploy script). `ARCH/31` §4.3, §4.4 |
> | `LPSM-C003` | Catalog client updates via `/v1/current.json` sequence bump | The client is wired, but the origin it syncs against is dead: `/v1/current.json` returns 200 while the release files it points at (`/v1/releases/<id>/…`) return 404, so `litespm catalog sync` cannot succeed. `ARCH/31` §4.2 |
> | `LPSM-D003` | Staged install moves atomically, end to end | The engine rejects an `Execute` with neither `TreeSource` nor `ArchiveSource` (`internal/install/engine.go:204`), the daemon handler supplies neither (`cmd/litespm/main.go:1420-1426`), and `litespm install <id>` fabricates an in-memory synthetic zip. No artifact source exists, so the in-agent install flow cannot complete (`M3`). `ARCH/31` §4.1 |
> | `LPSM-E005` | "Automated test suite in TEST.md passes" | [TEST.md](TEST.md) documents **manual** host scenarios and fixture layouts; the automated coverage is `internal/host` unit tests plus the `test/` package run by `go test ./...`. There is no TEST.md-driven automated suite, and the "explicit Adopt flow" does not exist — no `adopt_tool` handler or tool is present (`STATUS.md` §5). |
> | `LPSM-F004` | Loopback flow completes token exchange and stores credentials to the OS vault | `internal/auth` has **zero production importers** (finding 99), so no consumer exercises the loopback flow; the broker is `IMPLEMENTED` and unwired (`STATUS.md` §4). |
> | `LPSM-G002`, `LPSM-G003` | Update diff viewable in the wizard; `/marketplace` install completes | The wizard performs **no HTTP fetch** — advisories are compiled-in prints (`cmd/litespm/wizard.go:138`), there is no update check or diff, and `litespm setup` / `init` always launches the interactive wizard (`cmd/litespm/main.go:62`) — there is no non-interactive `setup <agent>` form. The panel's install action reaches `install.execute` but cannot complete (no artifact source). |
> | `LPSM-H003` | Full end-to-end suite, crash-injection harness, CI green on three OSes | There is **no crash-injection harness** anywhere in the tree (recovery is simulated from journal state only). What `.github/workflows/ci.yml` actually runs is listed in the H003 row below; this ledger does not assert the current run status of that workflow. |
>
> The authoritative defect tracker is [REMEDIATION-PLAN.md](REMEDIATION-PLAN.md) (remediation
> history; capability states live in STATUS.md). The forward backlog is
> [ARCH/31 §12](ARCH/31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#12-ordered-backlog).

---

## Phase A: Architecture Freeze & Contract Verification (Prerequisite)

Before implementing application code, all normative specifications, data contracts, and verification harnesses must be frozen.

| ID | Task | Owner | Acceptance Evidence | Status |
|---|---|---|---|---|
| **LPSM-A001** | Freeze local process topology (single-writer daemon, Bridge shims, IPC transports on Windows named pipes & Unix domain sockets). | [ARCH/02](ARCH/02-HLD.md), [ARCH/11](ARCH/11-LOCAL-RUNTIME-IPC.md) | Evidence: `internal/ipc` tests (the `Serve`/`Stop` race is reproduced under `-race -count=50`) plus `test/conformance_test.go`. **Gap:** no dedicated multi-host concurrent-connection harness exists. | `TESTED` |
| **LPSM-A002** | Freeze canonical domain identifiers (`SourceId`, `ListingId`, `ComponentId`, `InstallId`, `CapabilityId`) and JSON Schemas (Draft 2020-12 for all 6 core contracts). | [ARCH/10](ARCH/10-DOMAIN-MODEL.md), [ARCH/23](ARCH/23-SCHEMAS-EXAMPLES.md), [schemas/](schemas/) | Evidence: the six schema files exist and Go types document conformance by comment (`internal/domain/models.go:435`). **Gap:** no automated Draft 2020-12 validation — no test loads `schemas/*.json`. | `DESIGNED` |
| **LPSM-A003** | Freeze SQLite schema (22 core tables in WAL mode, including `auth_profiles` and `operation_trees`), CAS layout, and migration/downgrade rules. | [ARCH/12](ARCH/12-STORAGE-TRANSACTIONS-RECOVERY.md), [ARCH/22](ARCH/22-PLATFORM-RELEASE-MIGRATIONS.md) | `internal/state/migrations_test.go` (initial + idempotent apply, downgrade rejection) and `TestForeignKeysEnforced`. | `TESTED` |
| **LPSM-A004** | Freeze operation journal state machine (14 discrete states) and deterministic crash-recovery algorithms with safe CAS rollback. | [ARCH/12](ARCH/12-STORAGE-TRANSACTIONS-RECOVERY.md), [ARCH/13](ARCH/13-RESOLVER-INSTALL-ENGINE.md), [ARCH/21](ARCH/21-TESTING-CONFORMANCE.md) | `TestOperationJournalAndRollback`, `TestSafeCASRollbackPreservation`, `TestRecovery_ResolvesInterruptedRollback`. **Gap:** recovery is simulated from journal state; there is **no process-crash injection harness** (see H003). | `TESTED` |
| **LPSM-A005** | Upgrade `InstallPlan` to cryptographic hash-bound Plan v2 with explicit expiry, preconditions, and one-time `Approval` consumption replay prevention. | [ARCH/06](ARCH/06-API-CONTRACTS.md), [ARCH/13](ARCH/13-RESOLVER-INSTALL-ENGINE.md), [ARCH/15](ARCH/15-POLICY-APPROVALS.md) | `internal/install/plan_test.go` asserts `LPSM-PLAN-STALE` on tamper; `TestApprovalAtomicReplayPrevention` binds one-time approval consumption; `planHash` is persisted (`STATUS.md` §3). **Gap:** approval *subject* binding is an open Phase-1 audit finding. | `TESTED` |
| **LPSM-A006** | Freeze canonical effect taxonomy (17 actions with provenance metadata), `PolicyInput`/`PolicyDecision` engine, and strong identity grant binding. | [ARCH/15](ARCH/15-POLICY-APPROVALS.md) | Policy is evaluated on the install and skills paths (`STATUS.md` §1). **Gap:** grant binding to a CAS tree digest or HTTPS origin is not independently demonstrated, and policy DB-path fail-open items remain open (REMEDIATION-PLAN "P1"). | `WIRED` |
| **LPSM-A007** | Decouple and freeze interfaces for `SourceAdapter`, `ArtifactFetcher`, `RuntimeAdapter`, and extensible `HostAdapter`. | [ARCH/16](ARCH/16-HOST-ADAPTERS.md), [ARCH/17](ARCH/17-SOURCE-ARTIFACT-RUNTIME-ADAPTERS.md) | Isolation holds structurally: source adapters fetch neither bytes nor processes; host adapters touch only agent configs. **Gap:** `internal/source` still has test-only callers and there is no `catalog build` command. | `IMPLEMENTED` |
| **LPSM-A008** | Freeze MCP protocol compatibility matrix (stateless 2026-07-28 Streamable HTTP + legacy 2025-11-25) and client capability clamping. | [ARCH/09](ARCH/09-RESEARCH.md), [ARCH/14](ARCH/14-BRIDGE-PROVIDER-MCP.md) | Dual-profile client and clamping exist in `internal/mcpclient` with tests. **Gap:** zero production importers, so no live server has been exercised through it (`STATUS.md` §4). | `IMPLEMENTED` |
| **LPSM-A009** | Freeze provider supervisor lifecycle, Windows Job Objects / Unix supervisor watchdog control pipe (`PR_SET_PDEATHSIG`), and capability routing. | [ARCH/14](ARCH/14-BRIDGE-PROVIDER-MCP.md) | `internal/provider/isolation_unix.go` sets `PR_SET_PDEATHSIG` and kills the process group; Windows uses Job Objects (`isolation_windows.go`); start/stop/probe is `WIRED` (`STATUS.md` §4). **Gap (`m4`):** the `providers` table is never populated by non-test code, so autostart is inert. | `WIRED` |
| **LPSM-A010** | Freeze `SecretStore` abstraction, persistent `auth_profiles` table, and OAuth PKCE loopback. | [ARCH/05](ARCH/05-SECURITY.md), [ARCH/19](ARCH/19-SECRETS-OAUTH.md) | `internal/secrets` is `WIRED` for vault open (fail-closed on open) and zero secrets are stored in SQLite or config files. **Gaps:** launch-time injection is `IMPLEMENTED`, not `WIRED` — `ResolveLaunchSecrets` has no production caller (`STATUS.md` §1); the OAuth loopback broker (`internal/auth`) has zero production importers (finding 99). | `WIRED` (vault open) |
| **LPSM-A011** | Freeze immutable catalog release layout (`/v1/releases/<release-id>/...`), `manifest.json`, and `SourceSnapshot` schema. | [ARCH/03](ARCH/03-CATALOG-SOURCES.md), [ARCH/18](ARCH/18-CATALOG-BUILDER-RELEASE-SEARCH.md) | Client and compiler agree on `/v1/releases/<id>/{manifest,listings,versions}.json` and a contract test pins the layout (`TestReleasePathContractPinsDocumentedLayout`). **Gap (broken at origin):** the tree is never published, so live sync 404s (`STATUS.md` §2). | `WIRED` **(broken at origin)** |
| **LPSM-A012** | Create hostile archive limits (case-fold collision check, 256 MiB/1 GiB limits, canonical tree digests) and crash test harness. | [ARCH/05](ARCH/05-SECURITY.md), [ARCH/13](ARCH/13-RESOLVER-INSTALL-ENGINE.md), [ARCH/21](ARCH/21-TESTING-CONFORMANCE.md) | Limits are enforced and tested: `test/fuzz_hostile_archive_test.go` (zip-slip, Windows drive-letter escape, case collision, tar symlink) and `internal/artifact/artifact_test.go` (bounded download, canonical tree digest). **Gap:** the crash-test-harness half was never built. | `TESTED` |
| **LPSM-A013** | Document verified configurations for Codex (TOML), Grok Build (`~/.grok`), OpenCode (v1/v2 mapping), Pi Agent (candidate paths), and Cline. | [ARCH/16](ARCH/16-HOST-ADAPTERS.md), [AGENTS.md](AGENTS.md), [TEST.md](TEST.md) | Formats, path discovery and read-only external detection are documented and match `internal/host/targets_data.go`. **Gap:** there is no explicit Adopt flow — the `(Adopt)` label renders with no handler (`STATUS.md` §5). | `WIRED` |
| **LPSM-A014** | Freeze machine-readable error taxonomy (`LPSM-*`) and stable CLI exit codes (0–70). | [ARCH/20](ARCH/20-ERRORS-AUDIT-DOCTOR.md), [schemas/errors.schema.json](schemas/errors.schema.json) | `LPSM-*` envelopes are emitted across daemon/CLI/Bridge, and doctor category codes 10/20/30/40/50/60/70 are test-bound (`cmd/litespm/main.go:935-982`). **Gap:** no test enumerates every error code or asserts redaction corpus-wide. | `WIRED` |
| **LPSM-A015** | Produce `ARCH/24-FUNCTION-INVENTORY.md` mapping all 21 packages and `ARCH/25-WEB-FRONTEND-UI.md` (Next.js 15 static export). | [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md), [ARCH/25](ARCH/25-WEB-FRONTEND-UI.md) | Both documents exist and the web half matches `web/next.config.ts` (`output: "export"`). **Gap:** `ARCH/24` is hand-written and not compiler-generated — README records it as an aspirational inventory, and the package count is 21 modules after the `internal/connector` deletion. | `DESIGNED` |

---

## Phase B: Core Foundations & Storage Layer

| ID | Task | Owner | Acceptance Evidence | Status |
|---|---|---|---|---|
| **LPSM-B001** | Implement `internal/domain` (ID parsing, canonical JSON RFC 8785, digest hashing, schema validation). | [ARCH/10](ARCH/10-DOMAIN-MODEL.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | `internal/domain/domain_test.go` covers the pure types with no network or disk I/O. **Gap:** the former "100% coverage" claim was never measured — CI runs no coverage gate; note `CanonicalizeJSON` takes `[]byte`, not `any` (`STATUS.md` §1). | `TESTED` |
| **LPSM-B002** | Implement `internal/config` (platform paths for Windows `%LOCALAPPDATA%`, macOS `~/Library/Application Support`, Linux `$XDG_DATA_HOME`). | [ARCH/11](ARCH/11-LOCAL-RUNTIME-IPC.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Path resolution is production code (`internal/config/paths.go`); legacy `LITEPSM_*` fallback is covered by `internal/config/legacy_test.go`, and CI runs the suite on Ubuntu, Windows and macOS (`.github/workflows/ci.yml`, `test-go` matrix). | `WIRED` |
| **LPSM-B003** | Implement `internal/state` (SQLite WAL initialization, migration runner, 22 tables DDL). | [ARCH/12](ARCH/12-STORAGE-TRANSACTIONS-RECOVERY.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Migrations apply forward transactionally and reject downgrade from a higher schema version (`migrations_test.go`); foreign keys are enforced (`TestForeignKeysEnforced`). | `TESTED` |
| **LPSM-B004** | Implement `internal/ipc` (Named pipe server/client on Windows, Unix domain sockets on Linux/macOS, JSON-RPC 2.0 framing). | [ARCH/11](ARCH/11-LOCAL-RUNTIME-IPC.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Named Pipe DACL / Unix socket 0600, dispatcher, and the `Serve`/`Stop` race fix verified under `-race -count=50` (`STATUS.md` §1). **Gap:** no latency benchmark exists — the former acceptance text claimed one. | `TESTED` |
| **LPSM-B005** | Implement `internal/state` operation journal and recovery worker. | [ARCH/12](ARCH/12-STORAGE-TRANSACTIONS-RECOVERY.md), [ARCH/21](ARCH/21-TESTING-CONFORMANCE.md) | Recovery of interrupted operations is tested (`TestRecovery_ResolvesInterruptedRollback`, `TestCommitInstallOperation_Transactional`) and the daemon **halts** on recovery failure (exit **1**, `Fatal: startup recovery failed`, `cmd/litespm/main.go:1173-1175`). **Gap:** fault injection is simulated from journal state — no process-crash harness; partial-CAS orphan recovery (`m6`) is still open. | `TESTED` |

---

## Phase C: Static Catalog & Discovery Plane

| ID | Task | Owner | Acceptance Evidence | Status |
|---|---|---|---|---|
| **LPSM-C001** | Implement `internal/source` adapters for Official MCP Registry and Agent Skills. | [ARCH/03](ARCH/03-CATALOG-SOURCES.md), [ARCH/17](ARCH/17-SOURCE-ARTIFACT-RUNTIME-ADAPTERS.md) | The 8 adapters parse pinned fixtures into `Listing` records with source digests. **Gap:** test-only callers; there is no `catalog build` CLI command and no production ingest (finding 97, `STATUS.md` §2). | `IMPLEMENTED` |
| **LPSM-C002** | Implement `internal/catalogbuild` (deterministic release compiler, manifest generator, shard partitioner). | [ARCH/18](ARCH/18-CATALOG-BUILDER-RELEASE-SEARCH.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | `CompileRelease` is byte-deterministic and manifest-integrity tested (`TestDeterministicReleaseCompilation`, `TestManifestIntegrity`). **Gap:** no non-test caller, no CI publish, and the "shard partitioner" does not exist — `index.json`/`shards/`/`items/` are never emitted (`ARCH/31` §4.4). The deployed catalog is produced by `scripts/build_full_catalog.py` instead (D4 contested). | `IMPLEMENTED` |
| **LPSM-C003** | Implement `internal/catalog` client (release fetcher, manifest integrity checker, ETag cache, lexical search index). | [ARCH/06](ARCH/06-API-CONTRACTS.md), [ARCH/18](ARCH/18-CATALOG-BUILDER-RELEASE-SEARCH.md) | Sequence-bump detection and integrity checks are implemented and tested (`TestCatalogClientSyncAndIntegrity`). **Gap (broken at origin):** `/v1/current.json` answers 200 but `/v1/releases/<id>/manifest.json` 404s, so `litespm catalog sync` cannot succeed (`ARCH/31` §4.2). | `WIRED` **(broken at origin)** |
| **LPSM-C004** | Configure Cloudflare Pages deployment pipeline with strict dist allowlist (zero private source leaks). | [ARCH/02](ARCH/02-HLD.md), [ARCH/18](ARCH/18-CATALOG-BUILDER-RELEASE-SEARCH.md) | `scripts/deploy-pages.sh` builds the export, stages `pages-dist/`, writes the `_headers` cache policy, and exits non-zero on forbidden file types. **Gap:** the script is **manual-only** (referenced by no workflow), it does not deploy — `wrangler.toml` serves `pages-dist/`, which the script only prepares — and CI never regenerates `web/data/catalog.json`. | `IMPLEMENTED` |

---

## Phase D: Safe Artifact Extraction & Skill Management

| ID | Task | Owner | Acceptance Evidence | Status |
|---|---|---|---|---|
| **LPSM-D001** | Implement `internal/artifact` fetcher and safe archive extractor with hard limit enforcement. | [ARCH/05](ARCH/05-SECURITY.md), [ARCH/13](ARCH/13-RESOLVER-INSTALL-ENGINE.md) | Traversal, zip-bomb sizing, absolute paths, case collisions and tar symlinks are rejected before disk allocation: `internal/artifact/artifact_test.go` + `test/fuzz_hostile_archive_test.go`; canonical tree digest verified. | `TESTED` |
| **LPSM-D002** | Implement `internal/resolver` (pure DFS dependency resolution with cycle detection). | [ARCH/13](ARCH/13-RESOLVER-INSTALL-ENGINE.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Called in production by the `resolver.prepare_plan` handler (`cmd/litespm/main.go:1359`, `resolver.NewResolver` at `:1849`, `SavePlan` at `:1391`) — `internal/install` does not import it; `planHash` is persisted; cycle detection returns `LPSM-RESOLVE-CYCLE` (`domain.ErrResolveCycle`, `internal/resolver/resolver.go:73,201`; `ARCH/13`) | `WIRED` |
| **LPSM-D003** | Implement `internal/install` atomic staging, CAS immutable tree placement, and SQLite commit. | [ARCH/12](ARCH/12-STORAGE-TRANSACTIONS-RECOVERY.md), [ARCH/13](ARCH/13-RESOLVER-INSTALL-ENGINE.md) | Staging, commit and rollback are transactional and unit-tested, including shared-tree preservation. **Gap:** the engine rejects any `Execute` without an artifact source (`internal/install/engine.go:204`), the daemon supplies none, and the CLI fabricates a synthetic zip — so no end-to-end install completes (`M3`, `ARCH/31` §4.1). | `IMPLEMENTED` **(cannot complete)** |
| **LPSM-D004** | Implement `internal/skills` loader (progressive disclosure: metadata first, body/resources on demand). | [ARCH/04](ARCH/04-CLIENT-INSTALL.md), [ARCH/14](ARCH/14-BRIDGE-PROVIDER-MCP.md) | Skills lifecycle is `WIRED` (`add`/`list`/`update`/`remove` with atomic swap, rollback, dry-run, provenance ledger, policy hook, scope-checked removal); the Bridge `load_skill` / `read_skill_resource` tools resolve against it (`STATUS.md` §1). | `WIRED` |

---

## Phase E: Daemon Supervision, Bridge Shim & Host Adapters

| ID | Task | Owner | Acceptance Evidence | Status |
|---|---|---|---|---|
| **LPSM-E001** | Implement `internal/provider` process supervisor (Windows Job Objects / Unix process groups, stdio piping). | [ARCH/14](ARCH/14-BRIDGE-PROVIDER-MCP.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | start/stop/probe is `WIRED`; `PR_SET_PDEATHSIG` on Linux, Job Objects on Windows, process-group `SIGKILL`, bounded rotating stderr ring (`internal/provider/isolation_*.go`, `TestSupervisor_Lifecycle`, `TestRingBuffer_BoundedMemory`). **Gap (`m4`):** provider rows are never persisted, so autostart is inert. | `WIRED` |
| **LPSM-E002** | Implement `internal/policy` engine with `internal/state` approval records (no separate `internal/approval` package). | [ARCH/15](ARCH/15-POLICY-APPROVALS.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | Policy gates install and skills; one-time approval consumption is atomic (`TestApprovalAtomicReplayPrevention`). **Gap:** approval subject binding and policy fail-open DB paths are open Phase-1 audit findings; the policy engine is flat with no hierarchy or provenance. | `WIRED` |
| **LPSM-E003** | Implement `internal/bridge` stdio MCP server shim exposing the 12 core Bridge tools. | [ARCH/06](ARCH/06-API-CONTRACTS.md), [ARCH/14](ARCH/14-BRIDGE-PROVIDER-MCP.md) | The 12-tool stateless shim is launched by `litespm bridge stdio --host`; read tools resolve, `request_install` reaches `install.execute`, and five tools return explicit `-32601` with reasons. **Gap:** no MCP-inspector run is recorded, and routed calls are never validated against provider schemas — there is no invocation registry (`STATUS.md` §1, §4). | `WIRED` |
| **LPSM-E004** | Implement extensible `internal/host` adapter architecture with interactive wizard and auto-detection. | [ARCH/16](ARCH/16-HOST-ADAPTERS.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | The wizard auto-locates configs on Windows/macOS/Linux, prompts for a manual path, and merges atomically with a pre-edit backup; `host list\|detect\|setup\|remove` are wired. **Gap:** `litespm setup` / `init` always launches the interactive wizard — there is no non-interactive agent form (the non-interactive path is `litespm host setup <host-id>`, `cmd/litespm/main.go:392`). | `WIRED` |
| **LPSM-E005** | Build and test 6 bespoke host adapters (Cline, Pi Agent, Grok Build, Claude Code, Codex, OpenCode) plus 44 generic BridgeTargets (50 total, `ARCH/30`). | [ARCH/16](ARCH/16-HOST-ADAPTERS.md), [ARCH/30](ARCH/30-DATA-DRIVEN-BRIDGE-TARGETS.md), [AGENTS.md](AGENTS.md), [TEST.md](TEST.md) | 6 bespoke + 44 generic targets are registered (`internal/host/targets_data.go`) alongside 77 skill targets; idempotent registration, backups and external detection are covered by `internal/host` tests and `test/`. **Gap:** TEST.md is a manual scenario guide, not an automated suite; external tools are read-only and no Adopt handler exists. | `WIRED` |
| **LPSM-E006** | Implement advisory metadata via `/v1/current.json` (`advisories` array); no separate `/v1/adapters.json` endpoint — parsing stays compiled-in. | [ARCH/16](ARCH/16-HOST-ADAPTERS.md), [AGENTS.md](AGENTS.md) | The wizard reports compiled-in adapter advisories offline-safe (`cmd/litespm/wizard.go:138`), and `/v1/adapters.json` does not exist. **Gap:** no Go code parses the `advisories` array from `/v1/current.json` — the array is produced by the Python builder for the pointer file and has no consumer in the binary. | `WIRED` **(compiled-in only)** |

---

## Phase F: MCP Protocol Profiles, Secrets & OAuth

| ID | Task | Owner | Acceptance Evidence | Status |
|---|---|---|---|---|
| **LPSM-F001** | Implement `internal/mcpclient` supporting stateless MCP 2026-07-28 (Streamable HTTP) and legacy 2025-11-25. | [ARCH/09](ARCH/09-RESEARCH.md), [ARCH/14](ARCH/14-BRIDGE-PROVIDER-MCP.md) | Dual-profile client with tests exists. **Gap:** zero production importers — the claimed conformance suite has never run against a live mock or reference server through a production path (`STATUS.md` §4). | `IMPLEMENTED` |
| **LPSM-F002** | Implement capability probing, schema fingerprinting, and drift detection. | [ARCH/14](ARCH/14-BRIDGE-PROVIDER-MCP.md), [ARCH/15](ARCH/15-POLICY-APPROVALS.md) | Probing and fingerprinting exist in `internal/mcpclient/probe.go` with tests. **Gap:** drift never invalidates grants — there is no capability index or persisted capability rows to invalidate (`STATUS.md` §4). | `IMPLEMENTED` |
| **LPSM-F003** | Implement `internal/secrets` OS credential store wrapper (DPAPI-protected master key on Windows, Keychain on macOS, Secret Service on Linux — no WinCred binding). | [ARCH/05](ARCH/05-SECURITY.md), [ARCH/19](ARCH/19-SECRETS-OAUTH.md) | Native keystores plus memory/file stores are `WIRED` for vault open, open is fail-closed, and zero secrets land in SQLite or config files. **Gap:** tokens are **not** injected at provider launch — `ResolveLaunchSecrets` has no production caller (`STATUS.md` §1). The plaintext-secret canary (`test/canary_test.go`) plus the doctor canary guard leaks. | `WIRED` (vault open) |
| **LPSM-F004** | Implement `internal/auth` OAuth 2.0 PKCE loopback broker. | [ARCH/19](ARCH/19-SECRETS-OAUTH.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | The broker compiles and unit-tests. **Gap:** zero production importers (finding 99) — no consumer completes the browser flow or writes to the OS vault, so the acceptance text describes unit-level behaviour only. | `IMPLEMENTED` |

---

## Phase G: Marketplaces Federation, In-Agent `/marketplace` & Web Frontend

| ID | Task | Owner | Acceptance Evidence | Status |
|---|---|---|---|---|
| **LPSM-G001** | Implement source adapters for Claude, Codex, Grok plugin marketplace formats (strictly rejecting command sources). | [ARCH/03](ARCH/03-CATALOG-SOURCES.md), [ARCH/17](ARCH/17-SOURCE-ARTIFACT-RUNTIME-ADAPTERS.md) | Marketplace-format parsers live inside `internal/source` (8 adapters) and are fixture-tested; unsupported/command components are reported as non-executable. **Gap:** test-only callers — nothing ingests these formats in production (finding 97). | `IMPLEMENTED` |
| **LPSM-G002** | Implement interactive CLI TUI wizard (`litespm` interactive runner, dropdown agent selector, update check). | [ARCH/04](ARCH/04-CLIENT-INSTALL.md), [ARCH/16](ARCH/16-HOST-ADAPTERS.md) | Running `litespm` (or `setup`/`init`) opens the interactive selector and completes a safe merge with a backup. **Gap:** there is **no update check and no diff view** — the wizard performs no HTTP fetch and advisories are compiled-in — and there is no non-interactive `setup <agent>` form. | `WIRED` |
| **LPSM-G003** | Implement in-agent `/marketplace` command with 4-tab panel (MCP Servers, Agent Skills, Plugins, Installed with status lights & external tool scan). | [ARCH/04](ARCH/04-CLIENT-INSTALL.md), [ARCH/16](ARCH/16-HOST-ADAPTERS.md), [AGENTS.md](AGENTS.md) | The shim renders the four tabs, lists detected external capabilities read-only, and shows a light only for a status the daemon can observe (`— Unknown` otherwise). **Gap:** install cannot complete (`request_install` → `install.execute` with no artifact source), and `(Adopt)` renders with no handler. | `WIRED` **(except install)** |
| **LPSM-G004** | Build static Web Marketplace frontend inspired by mcpmarket.com. | [ARCH/25](ARCH/25-WEB-FRONTEND-UI.md) | Next.js 15 static export builds in CI (`build-web` job) and the live origin serves `GET /v1/current.json` 200 with omni-search, category rail, cards and detail drawer. **Gap:** catalog data is the Python-built 5,814-row blob, not the Go compiler's output (`STATUS.md` §2). | `WIRED` |
| **LPSM-G005** | Implement `internal/doctor` diagnostic engine and `--repair` plan generator. | [ARCH/20](ARCH/20-ERRORS-AUDIT-DOCTOR.md), [ARCH/24](ARCH/24-FUNCTION-INVENTORY.md) | 10 real checks with category exit codes 10/20/30/40/50/60/70 (worst-wins, warnings 0), `--repair` skips in-flight staging, and the canary deletes on all paths — Wave 2 `VERIFIED` (REMEDIATION-PLAN findings 46, m7). | `WIRED` |

---

## Phase H: Release Engineering, Conformance & Packaging

| ID | Task | Owner | Acceptance Evidence | Status |
|---|---|---|---|---|
| **LPSM-H001** | Setup cross-platform Go build matrix (`windows/amd64`, `windows/arm64`, `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`). | [ARCH/22](ARCH/22-PLATFORM-RELEASE-MIGRATIONS.md) | `scripts/build-release.sh` cross-compiles all six targets `CGO_ENABLED=0` and emits `SHA256SUMS.txt`; it is invoked by `.github/workflows/release.yml` on `v*` tags. **Gap:** the CI workflow does not build binaries — only the release workflow does. | `WIRED` |
| **LPSM-H002** | Create npm wrapper package (`litespm` / `@litespm/cli`) with platform-specific native binary downloaders. | [ARCH/04](ARCH/04-CLIENT-INSTALL.md), [ARCH/22](ARCH/22-PLATFORM-RELEASE-MIGRATIONS.md) | `npm/package.json` + `npm/scripts/install-binary.js` exist; the `files` allowlist lists `LICENSE`/`NOTICE` (staged into `npm/` by `.github/workflows/release.yml` before `npm publish --provenance`) and the npm version is checked against the release tag. **Gap:** on a **missing** checksum entry the installer warns and proceeds unverified (fail-open, `STATUS.md` §1). | `IMPLEMENTED` |
| **LPSM-H003** | Complete end-to-end conformance, secret leak canary, and crash-recovery test suite. | [ARCH/21](ARCH/21-TESTING-CONFORMANCE.md) | What `.github/workflows/ci.yml` **actually runs**: (1) `static-checks` — `gofmt -l .`, `go vet ./...`, `bash -n scripts/*.sh`, `python3 -m py_compile scripts/*.py`, `node --check` on the two npm entry points; (2) `test-go` — `go test ./...` on ubuntu/windows/macos, `-race` **only** off Windows; (3) `build-web` — `tsc --noEmit`, `next build`, `test -f web/out/index.html`. Coverage: `test/conformance_test.go`, `test/canary_test.go`, journal-recovery tests. **Gaps:** no crash-injection harness exists; no dedicated secret-scan or SBOM job; no step regenerates `web/data/catalog.json`, so CI neither runs `scripts/build_full_catalog.py` nor the Go compiler; this ledger does not assert the workflow's current run status. | `TESTED` |

---

## Phase I: Golden Fixtures, Self-Update & Migrations

Per-row states: fixtures and migrations are `TESTED`; self-update is `WIRED` (unsigned).

| ID | Task | Owner | Acceptance Evidence | Status |
|---|---|---|---|---|
| **LPSM-I001** | Maintain a golden fixture corpus for host configs and source registries. | [ARCH/21](ARCH/21-TESTING-CONFORMANCE.md), [TEST.md](TEST.md) | `fixtures/hosts/{claude,cline,codex,grok,opencode,pi}` is consumed by `internal/host` tests and `fixtures/source/acp/registry.json` by `internal/agent` + `internal/source` tests; the host layout is documented in TEST.md §1. **Gap:** `fixtures/source/{claude,codex,cursor,grok-official}` are not referenced by any test today. | `TESTED` |
| **LPSM-I002** | Ship the SQLite migration engine with forward transactional apply and a downgrade guard. | [ARCH/12](ARCH/12-STORAGE-TRANSACTIONS-RECOVERY.md), [ARCH/22](ARCH/22-PLATFORM-RELEASE-MIGRATIONS.md) | `TestMigrations_InitialAndIdempotent` and `TestMigrations_DowngradeRejection` bind both directions; startup recovery runs before serving and halts the daemon (exit **1**) on failure (`cmd/litespm/main.go:1173-1175`). | `TESTED` |
| **LPSM-I003** | Ship a fail-closed self-updater with integrity checks, downgrade guard and rollback. | [ARCH/22](ARCH/22-PLATFORM-RELEASE-MIGRATIONS.md), [ARCH/05](ARCH/05-SECURITY.md) | `litespm self-update` fetches a real HTTPS release manifest, verifies **SHA-256** fail-closed, rejects downgrades, replaces atomically via `O_EXCL` in a private dir (Windows backup/swap), verifies itself (`VerifySelfBoot`) and rolls back on failure — Wave 2 `VERIFIED` (REMEDIATION-PLAN finding 47). **Gaps:** no signature check (hash-only), and completion depends on a tag publishing `litespm-*` assets through `release.yml` (packaging P7). | `WIRED` |

---

## Overhaul backlog (next-phase work)

Everything below is `DESIGNED` or unwired today. Owner documents are `ARCH/32`–`ARCH/37` (new,
all carrying an explicit `DESIGNED` status header) plus the still-open remediation items; the
gating phase for each row is [ARCH/31 §12](ARCH/31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#12-ordered-backlog).

| Work item | State | Owner doc | Gate / next step |
|---|---|---|---|
| Published catalog release tree + one builder (D4 contested) | `DESIGNED` at origin; client `WIRED` (broken) | [STATUS.md](STATUS.md) §2, [ARCH/18](ARCH/18-CATALOG-BUILDER-RELEASE-SEARCH.md), [ARCH/31 §4.2–4.4](ARCH/31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#4-new-defects-found-in-this-repository-during-the-comparison) | `litespm catalog sync` succeeds against the live origin (Phase 0) |
| Source pipeline: 8 adapters → real ingest, `catalog build`, CI publish (finding 97) | `IMPLEMENTED` | [ARCH/03](ARCH/03-CATALOG-SOURCES.md), [ARCH/17](ARCH/17-SOURCE-ARTIFACT-RUNTIME-ADAPTERS.md), [STATUS.md](STATUS.md) §2 | A non-test caller publishes a release tree |
| Artifact-source install (`M3` / `LPSM-D003`) | `IMPLEMENTED` (cannot complete) | [ARCH/13](ARCH/13-RESOLVER-INSTALL-ENGINE.md), [ARCH/31 §4.1](ARCH/31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#41-the-agent-facing-install-path-cannot-complete--wired-is-false) | An agent's `/marketplace` install completes (Phase 0) |
| Provider-row persistence (`m4`) and partial-CAS recovery (`m6`) | `IMPLEMENTED` | [STATUS.md](STATUS.md) §3–§4, [ARCH/12](ARCH/12-STORAGE-TRANSACTIONS-RECOVERY.md) | `providers` populated by non-test code; cross-device orphan handled (Phases 2 / 0) |
| Phase-1 security batch from the 159-finding audit (auth broker https/state/verifier/mutex, IPC `SO_PEERCRED` + deadlines, policy fail-open DB paths, approval subject binding, npm fail-open checksums, bridge `-race` guard gap) | `IMPLEMENTED`/`WIRED` with open fail-closed gaps | [REMEDIATION-PLAN.md](REMEDIATION-PLAN.md) ("P1"), [ARCH/05](ARCH/05-SECURITY.md) | Fail-closed on every path, re-audited per the Wave protocol |
| Project manifest `litespm.yml` + lockfile + frozen install | `DESIGNED` | [ARCH/32 §2–§4](ARCH/32-MANIFEST-LOCK-INTEROP.md) | `litespm install --frozen` reproduces byte-identical CAS trees |
| Deployment ledger + three-way reconciliation | `DESIGNED` | [ARCH/33](ARCH/33-DEPLOYMENT-LEDGER-RECONCILIATION.md) | A hand-edited owned config node produces a conflict, not an overwrite |
| `adopt` migration flow | `DESIGNED` (no handler/tool) | [ARCH/33](ARCH/33-DEPLOYMENT-LEDGER-RECONCILIATION.md), [STATUS.md](STATUS.md) §5 | Ownership records exist, then a real adopt path |
| Capability registry + invocation engine + tamper-evident receipts | handlers `IMPLEMENTED` (`-32601`); engine `DESIGNED` | [ARCH/34](ARCH/34-RUNTIME-INVOCATION-RECEIPTS.md) | A Bridge `invoke` reaches a provider under a deadline and returns a receipt (Phase 2) |
| Sandbox / isolation tier declaration | `DESIGNED` | [ARCH/08](ARCH/08-DELIVERY.md), [STATUS.md](STATUS.md) §5 | `doctor` reports the true tier; no higher claim in docs |
| Runtime profiles + OCI distribution + capability leases | `DESIGNED` | [ARCH/35](ARCH/35-PROFILES-AND-CAPABILITY-LEASES.md) | A lease expires and the capability disappears (Phase 7) |
| Canonical identity + alias graph | `DESIGNED` | [ARCH/32 §6](ARCH/32-MANIFEST-LOCK-INTEROP.md), [STATUS.md](STATUS.md) §5 | `litespm search` returns one canonical entry with its sources |
| Interop: OpenAPM, `skills-lock.json`, Agent Plugins, Claude/Codex plugins, Docker profiles, `server.json` | `DESIGNED` | [ARCH/32 §5](ARCH/32-MANIFEST-LOCK-INTEROP.md) | Import/export produces a previewable diff before any write (Phase 4) |
| Policy inheritance (tighten-only) + `policy explain` | `DESIGNED` | [ARCH/36 §1](ARCH/36-ENTERPRISE-POLICY-AND-AUDIT.md) | Every deny names the inherited rule chain (Phases 3/6) |
| `audit --ci` + SARIF + scratch replay | `DESIGNED` | [ARCH/36 §2](ARCH/36-ENTERPRISE-POLICY-AND-AUDIT.md) | A CI run fails on a violation and on a replay-detected hand edit |
| Advisories + quarantine | `DESIGNED` (compiled-in wizard print only) | [ARCH/36 §3](ARCH/36-ENTERPRISE-POLICY-AND-AUDIT.md) | A revoked item is refused with a reason |
| TUF-style signed catalog + signed releases + SBOM / Sigstore / SLSA provenance | `DESIGNED` | [ARCH/36 §4–§5](ARCH/36-ENTERPRISE-POLICY-AND-AUDIT.md), [SECURITY.md](SECURITY.md) | `self-update` verifies a signature (today: SHA-256 only) |
| Air-gapped signed bundles | `DESIGNED` | [ARCH/36 §6](ARCH/36-ENTERPRISE-POLICY-AND-AUDIT.md) | An offline bundle installs with no network (needs signing first) |
| Catalog deltas / shards / FTS index | `DESIGNED` | [ARCH/18](ARCH/18-CATALOG-BUILDER-RELEASE-SEARCH.md), [STATUS.md](STATUS.md) §2 | A measured trigger, then shard + delta output from the chosen builder |
| Token / context budgets | `DESIGNED` | [ARCH/31 §12 phase 7](ARCH/31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#12-ordered-backlog) | An update shows schema, permission and token deltas before applying |
| Compatibility evidence records (host × OS × version × date × test) | `DESIGNED` | [ARCH/26 §4.2](ARCH/26-ECOSYSTEM-IA-PACKAGE-MODEL.md), [STATUS.md](STATUS.md) §5 | Records come from tests, not publisher declaration |
| TUI, local dashboard, staged tray, shell completion, `why` | `DESIGNED` | [ARCH/37](ARCH/37-TUI-AND-COMPLETION.md) | Every TUI action reachable via the CLI with identical daemon calls (Phase 8) |

---

**Ledger status.** Phases A–I each list their named code and its honest state above; the ledger no
longer claims completion for any row. Capability states are authoritative in [STATUS.md](STATUS.md);
defect history and wave verification live in [REMEDIATION-PLAN.md](REMEDIATION-PLAN.md). Five
conditions still block the most work, tracked in
[ARCH/31 §4](ARCH/31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#4-new-defects-found-in-this-repository-during-the-comparison)
and [ARCH/31 §12](ARCH/31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#12-ordered-backlog):

1. the agent-facing install path cannot complete (no artifact source in the daemon handler);
2. the catalog release tree is never published, so `catalog sync` 404s against the live origin;
3. two catalog builders exist and the one named canonical is dead code (D4 conflict);
4. `ARCH/18` specifies `index.json`/`shards/`/`items/` outputs the compiler does not emit;
5. releases are unsigned (SHA-256 only), so neither `self-update` nor the catalog has a trust root.

Closing 1–4 is **Phase 0** of the ordered backlog, ahead of every new feature; the DESIGNED set
(`ARCH/32`–`ARCH/37`) is mapped row-by-row in **Overhaul backlog** above.
