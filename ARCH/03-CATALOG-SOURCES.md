# Catalog and Source Federation

## 1. Upstream Federation Model

LitePSM is an **aggregator and compatibility layer**, not a monolithic proprietary repository. It ingests public metadata from established registries, directories, and Git repositories, normalizes this metadata into unified search schemas, and links directly back to upstream sources.

```text
 Upstream Source Adapters ──► SourceSnapshot ──► Schema Normalization ──► Immutable Catalog Release
            │
            ├─ Official MCP Registry (server.json & API)
            ├─ Agent Skills Specification (SKILL.md & Git repos)
            ├─ Claude Code Marketplaces (.claude-plugin/marketplace.json)
            ├─ OpenAI Codex Marketplaces (.agents/plugins/marketplace.json)
            ├─ Cursor Marketplaces (.cursor-plugin/marketplace.json)
            ├─ OpenAI Portable Plugins (plugin.json)
            ├─ Grok Build Marketplaces (.grok-plugin/marketplace.json)
            └─ User-Configured Local / Private Git Repositories
```

---

## 2. Decoupling the Adapter Roles

Previous designs conflated metadata scraping, artifact downloading, and runtime execution into a single generic "adapter". LitePSM strictly decouples these into three distinct subsystem interfaces:

```text
┌─────────────────────────┐     ┌─────────────────────────┐     ┌─────────────────────────┐
│      SourceAdapter      │     │     ArtifactFetcher     │     │     RuntimeAdapter      │
│  (CI Build-Time Only)   │     │  (Client-Side Download) │     │  (Client-Side Runtime)  │
│                         │     │                         │     │                         │
│ Reads registry APIs     │     │ Downloads raw tar/zip   │     │ Prepares virtualenv/npm │
│ Normalizes JSON schemas │ ──► │ Verifies SHA-256 digest │ ──► │ Configures process args │
│ Generates catalog shards│     │ Enforces archive limits │     │ Builds LaunchSpec       │
│ Never downloads bytes   │     │ Never executes scripts  │     │ Launches supervision    │
└─────────────────────────┘     └─────────────────────────┘     └─────────────────────────┘
```

1.  **SourceAdapter (Build-Time):** Operates exclusively in CI or during explicit local catalog builds. Fetches upstream manifests, normalizes them into `Listing` records, and resolves immutable version references. It never downloads full package binaries or executes code.
2.  **ArtifactFetcher (Client Download):** Runs on the user's workstation. Given an `ArtifactRef`, downloads archive bytes, verifies cryptographic digests, and unpacks files into the Content-Addressed Store under strict safety limits.
3.  **RuntimeAdapter (Client Execution):** Manages the environment required to run an installed component (e.g., configuring Python paths, verifying Node.js runtimes, or launching native binaries).

---

## 3. Supported Upstream Source Families

| Source Family | Ingestion Interface | Supported Package Formats | Security & Compatibility Caveats |
|---|---|---|---|
| **Official MCP Registry** | Registry API (`registry.modelcontextprotocol.io`) | npm, PyPI, Cargo, OCI, NuGet, MCPB, Streamable HTTP | Registry metadata is discovery-only. Does not imply safe execution. Supports multiple runtimes. |
| **Agent Skills** | `agentskills.io` directory API & Git repositories | Git tree containing `SKILL.md` + `scripts/`, `references/`, `assets/` | Progressive disclosure: loads metadata first, body on demand. `allowed-tools` frontmatter is **informational only** and confers no execution rights. |
| **Claude Code Marketplaces** | Git repository / `.claude-plugin/marketplace.json` | `github`, `git-subdir`, `url`, `archive`, `npm`, local paths | Ingests static manifests. Sources of type `command` execute shell scripts during fetch and are **strictly rejected in v1**. Registered upstreams: `git:anthropics-skills`, `git:claude-plugins-official`, `git:knowledge-work-plugins` (see `internal/source/sources.go`). |
| **OpenAI Portable Plugins** | Root `plugin.json` format | `skills/`, `mcp.json`, `hooks/`, `assets/` | Portable components are normalized. Vendor-specific extensions under `extensions.com.openai` are preserved in raw metadata. |
| **OpenAI Codex Marketplaces** | Git repository / `.agents/plugins/marketplace.json` | `local`, `url`, `git-subdir` | Registered upstream: `git:openai-plugins` (both `marketplace.json` and `api_marketplace.json`). Entries carry a declared `policy.authentication` requirement recorded verbatim in `RequirementsSummary`. |
| **Cursor Marketplaces** | Git repository / `.cursor-plugin/marketplace.json` | same-repo subpath strings | Registered upstream: `git:cursor-plugins`. Per-plugin detail lives in each plugin's own `plugin.json` and is not fetched at discovery. |
| **Grok Build Marketplaces** | `.grok-plugin/marketplace.json` | Remote Git sources pinned to full commit SHAs | Supports distinct component kinds (commands, agents, hooks, MCP, LSP). Pinned SHA-1/SHA-256 commits are required for reproducibility. Registered upstream: `git:xai-plugin-marketplace`. |
| **Generic Git / Local** | Local directories or authenticated Git repos | Any supported LitePSM manifest format | Uses the user's native Git credential helper or SSH agent. Credentials are never sent to LitePSM services. |

---

## 4. SourceSnapshot & Ingestion State Machine

Every ingestion cycle of an upstream source produces a durable, immutable `SourceSnapshot`:

```json
{
  "$schema": "https://litepsm.dev/schemas/v1/source-snapshot.schema.json",
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

---

## 5. Ingestion Security & SSRF Defense

The build-time source ingestion runner operates under rigid security boundaries:
*   **Protocol Restriction:** Only `https://` URLs are permitted for remote sources. All other schemes (`http://`, `file://`, `data:`, `ftp://`) are rejected.
*   **SSRF Protection:** Outbound HTTP clients resolve hostnames and strictly reject IP addresses in private, link-local, loopback, or cloud-metadata ranges:
    *   `127.0.0.0/8`, `::1` (Loopback)
    *   `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16` (Private RFC 1918)
    *   `169.254.0.0/16`, `fe80::/10` (Link-local & Cloud Metadata)
    *   `fc00::/7` (IPv6 Unique Local)
*   **Resource Bounds:** Maximum response body size per metadata query is capped at 16 MiB. Timeouts are enforced at 30 seconds per request. Maximum redirect depth is capped at 3 hops across identical origins.
