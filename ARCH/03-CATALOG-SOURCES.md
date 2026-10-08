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

> **State of this pipeline.** The eight adapters are `IMPLEMENTED` with **test-only callers** — nothing outside `internal/source` imports the package, so no production ingestion exists. The `SourceSnapshot` step is `DESIGNED` (nothing writes snapshot rows in production). `catalogbuild.CompileRelease` / `WriteToDirectory` is `TESTED` with a non-test caller (`litespm catalog build`) and the release tree it emits **is published at the live origin** (probe 2026-10-05: `/v1/current.json` and `/v1/releases/rel-2026-10-05-01/{manifest,listings,versions}.json` all `200` and byte-identical to the committed release — [STATUS.md](../STATUS.md) §2). The deployed catalog dataset — **5,814 items** (`web/data/catalog.json`; the live `/v1/current.json` reports `itemCount: 5814`) — is produced by `scripts/build_full_catalog.py`, not by these adapters. The checked-in source registry (`KnownSources`, `internal/source/sources.go`) holds **13** upstream source *definitions* — the eleven manifest/registry entries plus the two directory sources the producer walks itself (`feed:skills-sh`, `feed:mcpservers-org`, §3); the eight rows above are the adapter *implementations* (canonical roster: `ARCH/17` §2.1).

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

1.  **SourceAdapter (Build-Time):** Parses upstream manifest bytes supplied by its caller (CI or an explicit local catalog build), normalizes them into `Listing` records, and resolves immutable version references. It never downloads full package binaries or executes code. **State:** eight implementations, test-only callers; no ingestion job runs in CI ([STATUS.md](../STATUS.md) §2). The CLI verb that *does* exist is `litespm catalog build` (`cmd/litespm/main.go:110`, `runCatalogBuild` at `main.go:1442`) — but it compiles the committed dataset (`web/data/catalog.json`, produced by `scripts/build_full_catalog.py`) into the `/v1` release tree; it invokes no source adapter.
2.  **ArtifactFetcher (Client Download):** Runs on the user's workstation. Given an `ArtifactRef`, downloads archive bytes, verifies cryptographic digests, and unpacks files into the Content-Addressed Store under strict safety limits.
3.  **RuntimeAdapter (Client Execution):** Manages the environment required to run an installed component (e.g., configuring Python paths, verifying Node.js runtimes, or launching native binaries).

> **State of the three roles.** All three types exist (`IMPLEMENTED`); **none of the three is `WIRED`** — Role 1's eight adapters have only test callers (nothing outside `internal/source` imports the package), and Roles 2 and 3 have none at all. Role 2: the `ArtifactFetcher` interface and `HTTPArchiveFetcher` are compiled in `internal/artifact/fetcher.go:69` (`ARCH/17` §4) — with **no production caller**, because the published catalog carries no artifact locators — alongside the bounded spooling, safe extraction, and tree-digest verification the install engine already uses (`ARCH/17` §3). Role 3: `internal/runtime` compiles a `Registry` of five launch-planning adapters (`ARCH/17` §5) — also with **no production caller**; launch specs are still built ad hoc in `internal/install`, `internal/bridge`, and `internal/host`.

---

## 3. Supported Upstream Source Families

The catalog dataset shipped today holds **5,814 items** (built by `scripts/build_full_catalog.py`). The families below are what the adapters parse — plus the two directory families that `build_full_catalog.py` walks itself through the snapshot layer; adapter-side ingestion still has no production caller (see §1):

| Source Family | Ingestion Interface | Supported Package Formats | Security & Compatibility Caveats |
|---|---|---|---|
| **Official MCP Registry** | Registry API (`registry.modelcontextprotocol.io`) | npm, PyPI, Cargo, OCI, NuGet, MCPB, Streamable HTTP | Registry metadata is discovery-only. Does not imply safe execution. Supports multiple runtimes. |
| **Agent Skills** | `agentskills.io` directory API & Git repositories | Git tree containing `SKILL.md` + `scripts/`, `references/`, `assets/` | Progressive disclosure: loads metadata first, body on demand. `allowed-tools` frontmatter is **informational only** and confers no execution rights. |
| **Claude Code Marketplaces** | Git repository / `.claude-plugin/marketplace.json` | `github`, `git-subdir`, `url`, `archive`, `npm`, local paths | Ingests static manifests. Sources of type `command` execute shell scripts during fetch and are **strictly rejected in v1**. Registered upstreams: `git:anthropics-skills`, `git:claude-plugins-official`, `git:knowledge-work-plugins` (see `internal/source/sources.go`). |
| **OpenAI Portable Plugins** | Root `plugin.json` format | `skills/`, `mcp.json`, `hooks/`, `assets/` | Portable components are normalized. Vendor-specific extensions under `extensions.com.openai` are preserved in raw metadata. |
| **OpenAI Codex Marketplaces** | Git repository / `.agents/plugins/marketplace.json` | `local`, `url`, `git-subdir` | Registered upstream: `git:openai-plugins` (both `marketplace.json` and `api_marketplace.json`). Entries carry a declared `policy.authentication` requirement recorded verbatim in `RequirementsSummary`. |
| **Cursor Marketplaces** | Git repository / `.cursor-plugin/marketplace.json` | same-repo subpath strings | Registered upstream: `git:cursor-plugins`. Per-plugin detail lives in each plugin's own `plugin.json` and is not fetched at discovery. |
| **Grok Build Marketplaces** | `.grok-plugin/marketplace.json` | Remote Git sources pinned to full commit SHAs | Supports distinct component kinds (commands, agents, hooks, MCP, LSP). Pinned SHA-1/SHA-256 commits are required for reproducibility. Registered upstream: `git:xai-plugin-marketplace`. |
| **skills.sh directory** | Sitemap walk + `GET /api/download/{owner}/{repo}/{slug}` (producer; every fetch snapshot-recorded) | `SKILL.md` frontmatter (`name`, `description`) | Source id `feed:skills-sh`. Rows are `discovery_only`: a directory listing is not a vendor manifest, so no version and no launch line. Only records whose frontmatter carries a description are emitted — the rest are skipped and counted, never given a summary by the build. Roughly 20k sitemap entries; ids the catalog already carries are not re-fetched. |
| **MCPServers.org directory** | Sitemap walk (en paths only) + **Wayback Machine replays** of each directory page, indexed through CDX pagination (producer; every fetch snapshot-recorded) | `<title>` + meta `description` of the archived page | Source id `feed:mcpservers-org`. Pages are **never fetched from mcpservers.org itself** (its API paths are robots-disallowed); content comes from public captures. Rows are `discovery_only`. Pages with no capture, and captures lacking title or description, are skipped and counted — never filled in. |
| **ACP Agent Registry** | Registry index bytes supplied to `ACPAgentAdapter` (`internal/source/acp_registry.go`) | Normalized agent listings (`kind: agent`) | Discovery metadata only; runtime requirements (`node` / `uv` / `binary`) are derived, not installed. `litespm agent list` reads `internal/agent`'s own registry, not this adapter. |
| **Generic Git / Local** | Local directories or authenticated Git repos | Any supported LiteSPM manifest format | **Target only:** no git/HTTP fetch exists in the ingestion path (adapters parse manifest bytes supplied by their caller); `git` is invoked today only by `litespm skills add` / `skills update`. Uses the user's native Git credential helper or SSH agent; credentials are never sent to LiteSPM services. |

---

## 4. SourceSnapshot & Ingestion State Machine

> **State: fetch layer `IMPLEMENTED` (file store, build-side); daemon table `DESIGNED`.** Every ingestion fetch now produces a durable, immutable snapshot of the raw upstream bytes — `scripts/snapshot_store.py` records body + ETag + `sha256:` digest + sizes + RFC3339 timestamps under a gitignored `source-snapshots/` tree (override: `LITESPM_SOURCE_SNAPSHOT_DIR`), `scripts/build_full_catalog.py` replays those bytes by default (zero network), refreshes conditionally (`If-None-Match`), refuses any digest mismatch, and never fabricates a snapshot for a fetch that produced no bytes (failures go to `failures.jsonl`; a failed refresh keeps the recording and prints an explicit `STALE` line rather than silently claiming currency). `internal/source` mirrors the record format with the same refusal rules — `WriteSnapshot`/`LoadSnapshot`/`MarkIngested`, tested against a Python-written fixture — but its callers are tests only. The daemon-side **`source_snapshots` table still has no production writer** (`internal/state/migrations/001_initial_schema.sql:28-39`, `status CHECK IN ('healthy','partial','failed','stale')`), and domain records carry `SourceSnapshotID` strings without an ingestion path behind them. The example below remains illustrative of the record shape (the live `snapshot.json` uses the same fields minus `cursor`, which is unused — no pagination).

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
*   **Resource Bounds:** Maximum response body size per metadata query is capped at 16 MiB. Timeouts are enforced at 30 seconds per request. Maximum redirect depth is capped at 3 hops (identical-origin is not required; each hop is re-validated instead).

**Enforcement status (do not present the bullets above as live properties of every client):** per `ARCH/05` §3.1, `https` is enforced on skill git sources (`internal/skills/install.go:107-113`), on self-update asset and checksum URLs (`internal/update/updater.go:279-291`, applied at `:189`, `:199`, `:215`, `:240`), on catalog fetches (the shared guard's `egress.CheckURL` behind `internal/catalog/client.go:208-210`), on remote (URL) MCP endpoints at plan time (`cmd/litespm/install_mcp.go:150-165`), on artifact fetches (`internal/artifact/fetcher.go:139-141`) and on the ACP agent registry (`internal/agent/registry.go:47-49`). DNS/IP-range filtering lives in the shared guard `internal/egress` (dial-time checked-IP pinning, per-request re-check, unconditional public re-validation of every redirect hop: `egress.go:401-430`, `:469-529`) and is reached by both the **catalog client** (`internal/catalog/client.go:171-203`, delegation points) and the **remote-MCP transports** (`internal/mcpclient/client_2026.go:66`); the **ingestion producer** `scripts/build_full_catalog.py` keeps its own boundary — every source URL validated against an exact per-source host allowlist with `https`, port 443 and no credentials, then resolved and refused unless every address is globally routable (`_validate_request_url`, `:168-206`; `_is_public_ip`, `:158-165`). The artifact fetcher applies the same refusal at dial time (`internal/artifact/fetcher.go`) but has **no production caller yet**: the catalog carries no artifact locators, so nothing supplies it a target. The SSRF rule also exists as an unconditional *policy-level* invariant on declared effect targets (`internal/policy/engine.go:376-396`). The 30 s timeout does exist on the catalog client (`internal/catalog/client.go:70`), and 16 MiB caps exist for catalog metadata bodies (`:60`; a 64 MiB bound covers the manifest-sized data files, `:65`), for IPC messages (`internal/ipc/protocol.go:25-26`) and for MCP responses. What is still missing — scheme, destination and redirect policy on the self-update redirect hops, and cross-origin re-authorization on any client — is recorded in `ARCH/05` §3.2.

---

## 6. Related Design Records (`DESIGNED`)

Two product needs this document implies but does not satisfy are specified elsewhere, both `DESIGNED`:

*   **Project manifest (`litespm.yml`) + deterministic lockfile (`litespm.lock`), frozen resolve, SBOM/verify verbs, and interop import/export** — `ARCH/32` (Manifest, Lockfile & Interop). Neither file nor package exists ([STATUS.md](../STATUS.md) §5).
*   **Canonical identity/alias graph across sources** — also `ARCH/32`. Today only the `Listing` ID grammar is normative (`internal/domain/identifiers.go`; `ARCH/10`); there is no cross-source edge set.

Also relevant: the ingestion→release pipeline contract is `ARCH/17` (source/artifact/runtime adapters) and `ARCH/18` (builder, release layout, search ranking); the honesty rules for every claim on this page are `STATUS.md` and `ARCH/31` §2.
