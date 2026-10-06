# Catalog and Source Federation

## 1. Upstream Federation Model

LiteSPM is an **aggregator and compatibility layer**, not a monolithic proprietary repository. It is designed to ingest public metadata from established registries, directories, and Git repositories, normalize this metadata into unified search schemas, and link directly back to upstream sources. (Ingestion is `IMPLEMENTED` but **not wired** — see the state note under the diagram; the published dataset is assembled by `scripts/build_full_catalog.py`.)

```text
 Upstream Source Adapters (8 compiled) ──► SourceSnapshot ──► Schema Normalization ──► Immutable Catalog Release
            │
            ├─ MCPRegistryAdapter        (mcp_registry.go)       Official MCP Registry feed (server.json)
            ├─ AgentSkillsAdapter        (skills.go)             SKILL.md workflows (agentskills.io)
            ├─ ACPAgentAdapter           (acp_registry.go)       ACP agent registry index
            ├─ ClaudeMarketplaceAdapter  (claude_marketplace.go) .claude-plugin/marketplace.json
            ├─ CodexMarketplaceAdapter   (codex_marketplace.go)  .agents/plugins/marketplace.json
            ├─ CursorMarketplaceAdapter  (cursor_marketplace.go) .cursor-plugin/marketplace.json
            ├─ GrokMarketplaceAdapter    (grok_marketplace.go)   .grok-plugin/marketplace.json
            └─ OpenAIPluginAdapter       (openai_plugin.go)      plugin.json (portable plugins)
```

> **State of this pipeline.** The eight adapters are `IMPLEMENTED` with **test-only callers** — nothing outside `internal/source` imports the package, so no production ingestion exists. The `SourceSnapshot` step is `DESIGNED` (nothing writes snapshot rows in production). `catalogbuild.CompileRelease` / `WriteToDirectory` is `TESTED` with a non-test caller (`litespm catalog build`) but the release tree it emits is not published at the **live** origin (live origin answers 404 — [STATUS.md](../STATUS.md) §2). The deployed catalog dataset — **5,814 items** (`web/data/catalog.json`; the live `/v1/current.json` reports `itemCount: 5814`) — is produced by `scripts/build_full_catalog.py`, not by these adapters. The checked-in source registry (`KnownSources`, `internal/source/sources.go:31-113`) holds 11 upstream source *definitions*; the eight rows above are the adapter *implementations* (canonical roster: `ARCH/17` §2.1).

---

## 2. Decoupling the Adapter Roles

Previous designs conflated metadata scraping, artifact downloading, and runtime execution into a single generic "adapter". LiteSPM strictly decouples these into three distinct subsystem interfaces:

```text
┌─────────────────────────┐     ┌─────────────────────────┐     ┌─────────────────────────┐
│      SourceAdapter      │     │     ArtifactFetcher     │     │     RuntimeAdapter      │
│  (CI Build-Time Only)   │     │  (Client-Side Download) │     │  (Client-Side Runtime)  │
│                         │     │                         │     │                         │
│ Reads registry APIs     │     │ Downloads raw tar/zip   │     │ Prepares virtualenv/npm │
│ Normalizes JSON schemas │ ──► │ Verifies SHA-256 digest │ ──► │ Configures process args │
│ Emits release JSON files│     │ Enforces archive limits │     │ Builds LaunchSpec       │
│ Never downloads bytes   │     │ Never executes scripts  │     │ Launches supervision    │
└─────────────────────────┘     └─────────────────────────┘     └─────────────────────────┘
```

1.  **SourceAdapter (Build-Time):** Parses upstream manifest bytes supplied by its caller (CI or an explicit local catalog build), normalizes them into `Listing` records, and resolves immutable version references. It never downloads full package binaries or executes code. **State:** eight implementations, test-only callers; there is no `catalog build` CLI command and no ingestion job runs in CI ([STATUS.md](../STATUS.md) §2).
2.  **ArtifactFetcher (Client Download):** Runs on the user's workstation. Given an `ArtifactRef`, downloads archive bytes, verifies cryptographic digests, and unpacks files into the Content-Addressed Store under strict safety limits.
3.  **RuntimeAdapter (Client Execution):** Manages the environment required to run an installed component (e.g., configuring Python paths, verifying Node.js runtimes, or launching native binaries).

> **State of the three roles.** Role 1 is `IMPLEMENTED` (the eight adapters, test-only callers). Role 2 is split: the *fetcher* seam is `DESIGNED` (`ARCH/17` §4 — no `ArtifactFetcher` type exists), while bounded spooling, safe extraction, and tree-digest verification are compiled in `internal/artifact` and exercised by the install engine (`ARCH/17` §3). Role 3 is `DESIGNED` (`ARCH/17` §5 — no `RuntimeAdapter` type exists; launch specs are built ad hoc in `internal/install`, `internal/bridge`, and `internal/host`).

---

## 3. Supported Upstream Source Families

The catalog dataset shipped today holds **5,814 items** (built by `scripts/build_full_catalog.py`). The families below are what the adapters parse; ingestion itself has no production caller (see §1):

| Source Family | Ingestion Interface | Supported Package Formats | Security & Compatibility Caveats |
|---|---|---|---|
| **Official MCP Registry** | Registry API (`registry.modelcontextprotocol.io`) | npm, PyPI, Cargo, OCI, NuGet, MCPB, Streamable HTTP | Registry metadata is discovery-only. Does not imply safe execution. Supports multiple runtimes. |
| **Agent Skills** | `agentskills.io` directory API & Git repositories | Git tree containing `SKILL.md` + `scripts/`, `references/`, `assets/` | Progressive disclosure: loads metadata first, body on demand. `allowed-tools` frontmatter is **informational only** and confers no execution rights. |
| **Claude Code Marketplaces** | Git repository / `.claude-plugin/marketplace.json` | `github`, `git-subdir`, `url`, `archive`, `npm`, local paths | Ingests static manifests. Sources of type `command` execute shell scripts during fetch and are **strictly rejected in v1**. Registered upstreams: `git:anthropics-skills`, `git:claude-plugins-official`, `git:knowledge-work-plugins` (see `internal/source/sources.go`). |
| **OpenAI Portable Plugins** | Root `plugin.json` format | `skills/`, `mcp.json`, `hooks/`, `assets/` | Portable components are normalized. Vendor-specific extensions under `extensions.com.openai` are preserved in raw metadata. |
| **OpenAI Codex Marketplaces** | Git repository / `.agents/plugins/marketplace.json` | `local`, `url`, `git-subdir` | Registered upstream: `git:openai-plugins` (both `marketplace.json` and `api_marketplace.json`). Entries carry a declared `policy.authentication` requirement recorded verbatim in `RequirementsSummary`. |
| **Cursor Marketplaces** | Git repository / `.cursor-plugin/marketplace.json` | same-repo subpath strings | Registered upstream: `git:cursor-plugins`. Per-plugin detail lives in each plugin's own `plugin.json` and is not fetched at discovery. |
| **Grok Build Marketplaces** | `.grok-plugin/marketplace.json` | Remote Git sources pinned to full commit SHAs | Supports distinct component kinds (commands, agents, hooks, MCP, LSP). Pinned SHA-1/SHA-256 commits are required for reproducibility. Registered upstream: `git:xai-plugin-marketplace`. |
| **ACP Agent Registry** | Registry index bytes supplied to `ACPAgentAdapter` (`internal/source/acp_registry.go`) | Normalized agent listings (`kind: agent`) | Discovery metadata only; runtime requirements (`node` / `uv` / `binary`) are derived, not installed. `litespm agent list` reads `internal/agent`'s own registry, not this adapter. |
| **Generic Git / Local** | Local directories or authenticated Git repos | Any supported LiteSPM manifest format | **Target only:** no git/HTTP fetch exists in the ingestion path (adapters parse manifest bytes supplied by their caller); `git` is invoked today only by `litespm skills add` / `skills update`. Uses the user's native Git credential helper or SSH agent; credentials are never sent to LiteSPM services. |

---

## 4. SourceSnapshot & Ingestion State Machine

> **State: `DESIGNED`.** The `source_snapshots` table exists (`internal/state/migrations/001_initial_schema.sql:28-39`, with `status CHECK IN ('healthy','partial','failed','stale')`) and domain records carry `SourceSnapshotID` strings, but **no production ingestion runs** (§1), so no snapshot row is written outside tests. The example below is illustrative of the intended record shape.

Every ingestion cycle of an upstream source is intended to produce a durable, immutable `SourceSnapshot`:

```json
{
  "$schema": "https://litespm.dev/schemas/v1/source-snapshot.schema.json",
  "snapshotId": "snap_01J9X8K2M4N5P6Q7R8S9T0U1V2",
  "sourceId": "builtin:mcp-registry",
  "adapterVersion": "1.0.0",
  "upstreamRevision": "git:a1b2c3d4e5f67890",
  "startedAt": "2026-09-30T10:00:00Z",
  "completedAt": "2026-09-30T10:02:15Z",
  "status": "healthy",
  "itemCount": 642,
  "cursor": "cursor_page_32",
  "contentDigest": "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
  "errorSummary": null
}
```

### Freshness & Failure Semantics
1.  **Partial / Failed Ingestion:** If an upstream source fails (HTTP 5xx, network timeout, schema validation errors), the snapshot status is marked `failed` or `partial`.
2.  **No Silent Fallback to "Current":** The catalog builder **never** silently marks an old snapshot as `healthy` or current. If previous data is retained to allow continued searching, the published listing explicitly displays `status: "stale"`.
3.  **Upstream Deletions & Deprecations:** If an item disappears from an upstream registry, it is marked `status: "withdrawn"` or `status: "unavailable"` in future releases. It is **never** silently expunged from historical release archives, and already-installed local copies on user devices are not automatically deleted.

> These semantics are design intent. What exists in code today: `ListingStatus` carries `active | stale | deprecated | withdrawn | unavailable` (`internal/domain/models.go:23-31`) and the snapshot `status` CHECK constraint above. Nothing produces "future releases" from ingestion — published catalog data still comes from the Python builder (§1).

---

## 5. Ingestion Security & SSRF Defense

The build-time source ingestion runner is specified to operate under these rigid security boundaries (normative restatement of `ARCH/05` §3):
*   **Protocol Restriction:** Only `https://` URLs are permitted for remote sources. All other schemes (`http://`, `file://`, `data:`, `ftp://`) are rejected.
*   **SSRF Protection:** Outbound HTTP clients resolve hostnames and strictly reject IP addresses in private, link-local, loopback, or cloud-metadata ranges:
    *   `127.0.0.0/8`, `::1` (Loopback)
    *   `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16` (Private RFC 1918)
    *   `169.254.0.0/16`, `fe80::/10` (Link-local & Cloud Metadata)
    *   `fc00::/7` (IPv6 Unique Local)
*   **Resource Bounds:** Maximum response body size per metadata query is capped at 16 MiB. Timeouts are enforced at 30 seconds per request. Maximum redirect depth is capped at 3 hops across identical origins.

**Enforcement status (do not present the bullets above as live properties):** per `ARCH/05` §3.1–§3.2, HTTPS is enforced on skill git sources (`internal/skills/install.go:98-107`) and self-update downloads (`internal/update/updater.go:265-271`), and — since 2026-10-06 — on artifact fetches (`internal/artifact/fetcher.go`: HTTPS-only, SSRF refusal of loopback/link-local/private ranges at dial time, redirect cap, no downgrade, bounded spool, fail-closed digest verification). That fetcher has **no production caller yet**: the catalog carries no artifact locators, so nothing supplies it a target. The SSRF rule also exists as a *policy-level* check on declared effect targets (`internal/policy/engine.go:149-169`). No catalog-**ingestion** client performs DNS/IP-range filtering and none exists at all. The 30 s timeout does exist on the catalog client (`internal/catalog/client.go:40`); 16 MiB caps exist for IPC messages and MCP responses (`internal/ipc/protocol.go:26`, `internal/mcpclient`), not for catalog metadata bodies.

---

## 6. Related Design Records (`DESIGNED`)

Two product needs this document implies but does not satisfy are specified elsewhere, both `DESIGNED`:

*   **Project manifest (`litespm.yml`) + deterministic lockfile (`litespm.lock`), frozen resolve, SBOM/verify verbs, and interop import/export** — `ARCH/32` (Manifest, Lockfile & Interop). Neither file nor package exists ([STATUS.md](../STATUS.md) §5).
*   **Canonical identity/alias graph across sources** — also `ARCH/32`. Today only the `Listing` ID grammar is normative (`internal/domain/identifiers.go`; `ARCH/10`); there is no cross-source edge set.

Also relevant: the ingestion→release pipeline contract is `ARCH/17` (source/artifact/runtime adapters) and `ARCH/18` (builder, release layout, search ranking); the honesty rules for every claim on this page are `STATUS.md` and `ARCH/31` §2.
