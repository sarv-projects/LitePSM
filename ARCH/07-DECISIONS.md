# Architecture Decision Records (ADRs)

> **Numbering and precedence.** ADRs use the `D-0NN` form. The remediation tracker
> ([REMEDIATION-PLAN.md](../REMEDIATION-PLAN.md)) uses a separate, shorter `D1`–`D4` series; where an
> ADR restates one of those, it says so explicitly. Per [ARCH/00](00-INDEX.md#1-document-status--precedence)
> these records sit below the function inventory and above the product/delivery documents; for the
> *state* of anything described here, [STATUS.md](../STATUS.md) wins.

## 1. Foundational Architecture Decisions (D-001 – D-010)

### D-001: Provider-Neutral Shared Product & Local Execution
LiteSPM is designed to serve multiple agent hosts (Codex, Claude Code, Grok Build, OpenCode, Cline). The hosted service provides catalog discovery only. Package installation, process supervision, and tool execution occur strictly on the user's workstation.

### D-002: Product and Marketplace Naming
The product and CLI binary are named **LiteSPM** (`litespm`) — *The Lightweight Skill & Package Manager for AI Agents*. The public web catalog is **LiteSPM Market**. `SPM` expands to **Skill & Package Manager**. Directory slug is `liteSPM`.

### D-003: Client-Side Downstream Credentials
Downstream credentials (API keys, OAuth tokens) reside in the operating system's native credential store and are never transmitted to LiteSPM cloud services. Downstream providers receive tokens only at the time of user-authorized execution.

### D-004: Static Catalog on Cloudflare Pages
The public catalog is deployed as static, immutable JSON files via Cloudflare Pages from a private GitHub repository. No application server, database, or worker is required for public discovery.

> *Implementation note (2026-10-05):* the live origin is a Cloudflare **Worker** static-assets
> deployment (`wrangler.toml`, `[assets] directory = "./pages-dist"`) at
> `https://litespm.sarveshbh-2022.workers.dev`, packaged by `scripts/deploy-pages.sh`. The
> "static immutable JSON, no application server" half of the decision holds; the "Cloudflare Pages"
> product name does not match the deployment. A prior revision of this note recorded the origin as
> the pre-rebrand `litepsm` Worker and flagged the rename as a deliberate pending change; that
> migration was completed on 2026-10-05 (new Worker deployed and verified serving release
> `rel-2026-10-05-01`, all origin references updated together — `REMEDIATION-PLAN.md` M1).

### D-005: Source Federation over Monolithic Rewriting
LiteSPM aggregates documented upstream feeds and Git marketplace manifests. It preserves original source attribution and upstream identifiers rather than attempting to hand-curate or rewrite thousands of packages.

### D-006: Preservation of Package Format Semantics
Upstream package formats (Agent Skills, MCP servers, portable plugins) are preserved in their native structures. Incompatible or host-specific components are flagged explicitly rather than silently rewritten or dropped.

### D-007: Mandatory Local Plan & User Approval
Discovery is read-only. Installing, updating, or removing capabilities requires generating an immutable `InstallPlan` and obtaining explicit user confirmation before modifying local files or starting processes.

### D-008: No Silent Auto-Updates by Default
Installed versions bind to immutable content digests. Updates are user-initiated, present delta diffs across code and permissions, and require explicit approval.

### D-009: Integration-Based Host Support
LiteSPM exposes standard MCP stdio, HTTPS APIs, and documented host adapters. It does not claim automatic integration with hosts that lack a documented MCP or configuration extension point.

### D-010: One-Time Host Bridge Registration
For supported agents, LiteSPM configures a single Bridge entry per host. Subsequent skills and MCP providers are managed within LiteSPM's central local store, avoiding repeated edits to host configuration files.

> *Current behaviour:* host integration is **MCP-entry merge only** — no instruction, skill, command
> or agent projection is written into a host today ([STATUS.md](../STATUS.md) §1). D-010's
> "single entry" claim holds; the projection it anticipates is `DESIGNED`.

---

## 2. Core Implementation Decisions (D-011 – D-020)

### D-011: Single-Writer Local Control Plane (Daemon)
*   **Context:** Multiple agent hosts (Codex, Claude, OpenCode) can run simultaneously. If each Bridge shim directly modified files or started child processes, concurrency races and state corruption would occur.
*   **Decision:** All mutable SQLite transactions, provider process supervision, OS secret access, and filesystem commits are owned exclusively by a single local LiteSPM Daemon per user account.
*   **Status:** Accepted.

### D-012: SQLite (WAL) + Content-Addressed Storage (CAS)
*   **Context:** Flat lockfiles (`installs.lock`) cannot handle concurrent reads, transactional journals, or rollbacks.
*   **Decision:** Structured metadata is stored in SQLite 3 with Write-Ahead Logging (`WAL`). Extracted package trees are stored in a Content-Addressed Store (`cas/trees/sha256/<digest>`) indexed by SHA-256 digests. Downloads spool to a bounded temp file and are not retained: there is **no `artifacts/` directory** in the shipped layout (`ARCH/04` §4, `internal/install/engine.go:471-482`); the `artifacts` *table* holds references, not bytes.
*   **Status:** Accepted for **local machine state**.
*   **Superseded in part by [ARCH/32](32-MANIFEST-LOCK-INTEROP.md) (`DESIGNED`).** The context above
    rejects a flat lockfile as the *local ownership/state* record — that part stands, and SQLite +
    CAS is the shipped store. It does **not** reject a flat lockfile as a *project dependency pin*:
    `ARCH/32` specifies `litespm.yml` (intent) + `litespm.lock` (resolved pins, lock+ledger design)
    with frozen install, and [ARCH/33](33-DEPLOYMENT-LEDGER-RECONCILIATION.md) supplies the
    `deployment_mutations` ledger that replaces `host_backups` as the ownership model. Read D-012 as
    "the machine's own state is not a flat file", never as "projects may not carry a lockfile".

### D-013: Split Bridge Architecture (Stateless Shims)
*   **Context:** Host agents expect an MCP stdio server.
*   **Decision:** The host-facing Bridge executable is a lightweight, stateless shim. It handles stdio JSON-RPC framing and forwards all state, discovery, and execution requests over authenticated local IPC to the Daemon.
*   **Status:** Accepted. Standalone (daemon-less) mode fails closed instead of fabricating results (`internal/bridge/shim.go:274-280`).

### D-014: Statically Linked Native Adapters in v1
*   **Context:** Allowing dynamic third-party adapter scripts introduces supply-chain code execution risks during ingestion and resolution.
*   **Decision:** All source adapters, artifact fetchers, runtime adapters, and host adapters are compiled directly into the LiteSPM Go binary. No dynamic adapter code is downloaded or executed.
*   **Status:** Accepted. Note the artifact-fetcher and runtime-adapter packages this anticipates are still `DESIGNED` ([ARCH/00 §3](00-INDEX.md#3-glossary--core-architectural-concepts)); today `internal/artifact` does bounded spooling/extraction only.

### D-015: Cryptographic Plan Binding (InstallPlan v2)
*   **Context:** Time-of-Check to Time-of-Use (TOCTOU) attacks could alter package contents or permissions between plan creation and user confirmation.
*   **Decision:** `InstallPlan` v2 computes an RFC 8785 canonical SHA-256 `planHash` across all execution fields. Approval binds strictly to `planHash`. Plans include mandatory expiration and precondition checks.
*   **Status:** Accepted. Hashing and expiry are enforced (`internal/domain/canonical.go:19-57`, `internal/install/engine.go:105-120`); precondition and approval payloads are still empty in plans built today ([ARCH/06 §2](06-API-CONTRACTS.md#2-installplan-contract)).

### D-016: Capability Schema-Drift Invalidates Grants
*   **Context:** Downstream MCP providers could alter tool parameter schemas after receiving approval.
*   **Decision:** Tool inputs are fingerprinted via SHA-256 digests of their JSON Schemas (`schemaFingerprint`). Any drift upon provider reconnection immediately invalidates pre-existing capability grants and halts execution until re-approved.
*   **Status:** Accepted; code `IMPLEMENTED` but not reachable until capability-grant rows are written by non-test code ([ARCH/05 §5](05-SECURITY.md#5-capability-schema-drift-defense)).

### D-017: Hand-Rolled MCP JSON-RPC & Named Protocol Profiles
*   **Context:** Custom wire protocol implementations risk subtle incompatibilities.
*   **Decision:** LiteSPM hand-rolls MCP JSON-RPC 2.0 (`internal/ipc`, `internal/bridge`, `internal/mcpclient`; no external MCP SDK in `go.mod`) and explicitly tests two named protocol profiles:
    1.  **Modern Profile:** 2026-07-28 stateless architecture with Streamable HTTP and header mirroring (`Mcp-Method`).
    2.  **Legacy Profile:** 2025-11-25 stateful initialization for backwards compatibility.
*   **Status:** Accepted for `internal/ipc` and `internal/bridge` (`TESTED`). `internal/mcpclient` implements both profiles but has **zero production importers** (`IMPLEMENTED`) — see [STATUS.md](../STATUS.md) §4.

### D-018: TUF Metadata Framework for Future Signed Releases
*   **Context:** Ad-hoc cryptographic signing schemes are vulnerable to rollback and freeze attacks.
*   **Decision:** If cryptographic catalog signing is introduced, LiteSPM will adopt The Update Framework (TUF) standard. V1 implements HTTPS origin verification and monotonic sequence verification.
*   **Status:** Accepted, future-facing. The design record is now [ARCH/36 §4](36-ENTERPRISE-POLICY-AND-AUDIT.md) (`DESIGNED`), together with advisories/quarantine (`§3`) and SBOM/Sigstore/SLSA (`§5`). Today's reality: `self-update` verifies **SHA-256 only, with no signature check** ([STATUS.md](../STATUS.md) §1; [SECURITY.md](../SECURITY.md)).

### D-019: Prohibition of Command Marketplace Sources
*   **Context:** Claude Code and Grok Build manifests support `command` sources that execute shell scripts during catalog ingestion.
*   **Decision:** Marketplace sources of type `command` are strictly prohibited in v1. They are flagged as unsupported to prevent arbitrary remote code execution during ingestion.
*   **Status:** Accepted. Enforcement is by omission at ingestion plus a policy-level deny on `cmd:`/`command:` targets; no `UNSUPPORTED_COMMAND_SOURCE` annotation is emitted ([ARCH/05 §7](05-SECURITY.md#7-command-marketplace-source-prohibition)).

### D-020: Five-Stage Support Taxonomy
*   **Context:** Generic "verified" badges are ambiguous and misleading.
*   **Decision:** All catalog listings explicitly and independently display five operational status values: `listed`, `resolvable`, `installable`, `runnable`, and `tested`.
*   **Status:** Accepted as the taxonomy. None of the five is a claim about *LiteSPM's own* implementation state — that is governed separately by the evidence vocabulary in D-023.

---

## 3. Later Decisions (D-021 – D-025)

Each entry below records a decision that has **already been made and acted on** (or deliberately left
open); the rationale is preserved rather than back-ported into the earlier series.

### D-021: Delete the Connector Package; Re-Wiring Requires a New Decision
*   **Context:** `internal/connector` had zero production importers and no wiring path. Shipping unreachable code is worse than shipping nothing.
*   **Decision (D1 in [REMEDIATION-PLAN.md](../REMEDIATION-PLAN.md), locked):** delete the package rather than ship it — 9 files / 2,072 lines removed, with `ARCH/24`, `README`, `ARCH/00` module counts and `SECURITY.md` corrected in the same change. [ARCH/29](29-CONNECTOR-SYSTEM-DESIGN.md) remains the credential-custody **design record only**.
*   **Planned resurrection:** a connector executor is re-planned, not restored. `D1` must be re-decided before any implementation, the credential-custody design must be implemented as specified, and catalog promotion additionally needs the `connector` type admitted ([ARCH/26 §3.4.1](26-ECOSYSTEM-IA-PACKAGE-MODEL.md)). Current state: `DESIGNED` ([STATUS.md](../STATUS.md) §4).
*   **Status:** Accepted (delete executed); resurrection deferred.

### D-022: Canonical Catalog Builder Is the Go `catalogbuild` (D4 — contested, open)
*   **Context:** Two builders existed in the tree: `internal/catalogbuild.CompileRelease` (Go, deterministic, no non-test caller) and `scripts/build_full_catalog.py` (Python, the producer of everything actually deployed: `web/data/catalog.json`, `web/public/v1/current.json`).
*   **Decision (D4 in [REMEDIATION-PLAN.md](../REMEDIATION-PLAN.md)):** the Go `catalogbuild` is the single builder; canonical listing IDs are 4-segment.
*   **Honest status (updated 2026-10-05):** the code now runs a **split** — `litespm catalog build` owns the served pointer, release id/sequence, and `/v1/releases/` tree (byte-for-byte materialization at deploy, end-to-end tested), while the Python script is narrowed to dataset ingestion (`web/data/catalog.json` + stats) and no longer writes `web/public/v1/*`. The Go builder is no longer dead code, but **the contest is not closed**: the split itself has not been accepted or overruled as D4's resolution ([ARCH/31 §4.3](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#43-decision-d4-is-contradicted-by-the-repository--there-are-two-catalog-builders), [STATUS.md](../STATUS.md) §2/§6), and nothing publishes the tree to the live origin yet.
*   **Status:** Accepted as written; **contest open** (implemented as split, awaiting adjudication).

### D-023: Binding Evidence-State Vocabulary
*   **Context:** "Done" previously meant at least four different things across documents, which allowed claims a stricter reading of the code did not support.
*   **Decision:** exactly six states — `DESIGNED`, `IMPLEMENTED`, `WIRED`, `TESTED`, `VERIFIED`, `SHIPPED` — defined once in [ARCH/31 §2](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#2-evidence-vocabulary-binding-repo-wide), binding repo-wide, never collapsed. "Files exist" ≤ `IMPLEMENTED`; "tests exist" ≤ `TESTED`; a component whose only caller is a test is `IMPLEMENTED`, not `WIRED`. [STATUS.md](../STATUS.md) records the highest honest state per subsystem and wins any conflict.
*   **Consequence for this file:** ADRs state decisions; they never state a capability's state. Where an ADR's rationale implies a state, STATUS.md governs.
*   **Status:** Accepted, binding.

### D-024: Rename to LiteSPM, with Legacy Adoption
*   **Context:** `LitePSM` → `LiteSPM` changed directory roots, environment variables, project config paths, pipe/socket prefixes, and host bridge keys; an upgrade that simply renamed would lose data and leave a duplicate bridge entry.
*   **Decision:** the rename landed at `ff0a1db` **together with** non-destructive legacy adoption: `LITEPSM_*` environment fallback (`internal/config/config.go:132`), `.litepsm/config.toml` project-config fallback (`internal/config/config.go:103-105`), legacy data-root/pipe/socket brand adoption (`internal/config/paths.go:33-37`), and legacy host bridge-key (`litepsm`) adoption/removal (`internal/host/target.go:105`, `internal/host/legacy_adoption_test.go`). Legacy paths are read when the new path is absent; nothing is migrated destructively.
*   **Residual:** the module path casing still differs from the git remote (`m8`) — non-breaking, to be aligned at release.
*   **Status:** Accepted; rename shipped in-tree, residual noted.

### D-025: Restart/Retry Yes, Blind Write-Fallback No
*   **Context:** a runtime router will need a failure policy; the tempting default (silently send the call to whatever provider looks similar) changes what the user authorized.
*   **Decision:**
    *   **Restart/retry is allowed** against the *same* provider, same capability, and same schema fingerprint, bounded by the invocation deadline and circuit breaker; a retry consumes the same grant and the same policy decision.
    *   **Blind cross-provider semantic fallback is forbidden for writes.** Any effectful call that fails must not be re-routed to a look-alike provider; user-desired reroutes require a new plan, a new policy evaluation, and a new approval.
    *   Read-only substitution requires an explicit, approvable **equivalence declaration** — never name similarity.
*   **Where:** full rule at [ARCH/15 §5](15-POLICY-APPROVALS.md#5-failure-retry--fallback-policy); runtime constraints at [ARCH/34](34-RUNTIME-INVOCATION-RECEIPTS.md). Adopted from proposal #24 ([ARCH/31 §5](31-COMPETITIVE-LANDSCAPE-AND-ROADMAP.md#5-adjudication-of-the-60-proposals)).
*   **Status:** Accepted (as a normative constraint); the runtime that enforces it is `DESIGNED`.
